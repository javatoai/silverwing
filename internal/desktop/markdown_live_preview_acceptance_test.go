package desktop

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"

	"github.com/egoist/mygo/ui"
)

// These tests enter through the materials read result, render the real materials
// page and drive its native input client. They deliberately do not edit Buffer
// directly: changing the model alone would miss focus, commands and composition.
func markdownLiveAcceptanceFixture(t *testing.T, source string, dark bool, width int) (*app, *materialTextTestBackend, documents.Content, *ui.Tester) {
	t.Helper()
	s, backend, files := materialTextEditorFixture(t, map[string]string{"验收.md": source})
	s.readingSession = newMemoryReadingSession(s.snapshot.Home)
	content := files["验收.md"]
	content.ReadOnly = false
	content.MarkdownEditContext = taskMaterialsMarkdownEditContext
	s.materialsUnit.state.documents = []application.Document{{Path: content.Path, Name: filepath.Base(content.Path), Kind: "markdown"}}
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	action := application.Action{Kind: "document.read", TaskID: "task", Path: content.Path, Payload: map[string]any{
		"markdownEditContext": taskMaterialsMarkdownEditContext,
		"draftTaskPath":       backend.taskPath, "draftMaterialsRoot": backend.root,
		"desktopRenameTaskCreatedAt": "",
	}}
	s.applyMaterialsActionResult(action, application.ActionResult{Path: content.Path, Text: content.Text, Data: content})
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		s.beginOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() { s.materials(c, p) })
		s.endOverflowMenuFrame()
	}, width, 850)
	tester.SetLocale("zh-Hans")
	t.Cleanup(s.closeMarkdownEditor)
	return s, backend, content, tester
}

func focusMarkdownLiveAcceptance(t *testing.T, s *app, tester *ui.Tester) *markdownEditorState {
	t.Helper()
	state := s.ensureMarkdownEditor()
	if state == nil || state.live == nil || state.readOnly || s.materialsUnit.state.documentReadOnly || !s.materialsUnit.state.documentEditing {
		t.Fatal("writable task material did not open the native Markdown editor")
	}
	state.live.Focus()
	tester.Frame()
	if active, noInputMethod := tester.TextInputActive(); !active || noInputMethod {
		t.Fatalf("Markdown input client inactive or disables IME: active=%v noInputMethod=%v", active, noInputMethod)
	}
	bounds, ok := tester.Find("Markdown 编辑区")
	if !ok || bounds.W <= 0 || bounds.H <= 0 {
		t.Fatal("native Markdown editing surface is missing")
	}
	if caret, ok := tester.TextCaret(); !ok || caret.H <= 0 {
		t.Fatalf("native text client has no composition caret: %+v active=%v", caret, ok)
	} else if caret.X < bounds.X-1 || caret.X > bounds.X+bounds.W+1 || caret.Y < bounds.Y-1 || caret.Y >= bounds.Y+bounds.H {
		t.Fatalf("native input caret is outside the editor viewport: caret=%+v viewport=%+v", caret, bounds)
	}
	return state
}

func clickMarkdownAcceptance(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	if err := tester.Click(text); err != nil {
		t.Fatal(err)
	}
}

func chooseMarkdownAcceptanceMode(t *testing.T, tester *ui.Tester, mode string) {
	t.Helper()
	item := map[string]string{"source": "查看 Markdown 源码", "live": "实时编辑"}[mode]
	if item == "" {
		t.Fatalf("unknown Markdown mode %q", mode)
	}
	// Open the same context menu without moving the caret under test. Pointer
	// opening is covered by TestMaterialsMarkdownContextMenuChangesActualMode.
	tester.Key(0, ui.KeyContextMenu)
	if err := tester.ChooseMenuItem(item); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
}

func assertMarkdownAcceptanceSource(t *testing.T, s *app, state *markdownEditorState, want string) {
	t.Helper()
	if got := state.live.Buffer().OriginalText(); got != want {
		t.Fatalf("native document source = %q, want %q", got, want)
	}
	if s.materialsUnit.state.documentContent != want {
		t.Fatalf("materials draft = %q, want %q", s.materialsUnit.state.documentContent, want)
	}
}

// This fixture has no links or colored source tokens. Accent pixels inside its
// document viewport must therefore belong to the single native caret. This
// catches a display bug that text/selection assertions alone cannot see.
func assertMarkdownAcceptanceSingleVisibleCaret(t *testing.T, tester *ui.Tester, dark bool) {
	t.Helper()
	bounds, ok := tester.Find("Markdown 编辑区")
	if !ok {
		t.Fatal("Markdown editor viewport missing")
	}
	caret, active := tester.TextCaret()
	if !active {
		t.Fatal("native caret geometry unavailable")
	}
	accent := colors(dark).blue
	pixels := tester.Image()
	close := func(a, b uint8) bool { return int(a)-int(b) <= 2 && int(b)-int(a) <= 2 }
	found := false
	for y := int(math.Ceil(float64(bounds.Y))); y < min(pixels.Bounds().Max.Y, int(bounds.Y+bounds.H)); y++ {
		for x := int(math.Ceil(float64(bounds.X))); x < min(pixels.Bounds().Max.X, int(bounds.X+bounds.W)); x++ {
			pixel := pixels.RGBAAt(x, y)
			if pixel.A != 255 || !close(pixel.R, accent.R) || !close(pixel.G, accent.G) || !close(pixel.B, accent.B) {
				continue
			}
			if float32(x) >= caret.X-2 && float32(x) <= caret.X+max(caret.W, 1)+2 && float32(y) >= caret.Y-2 && float32(y) <= caret.Y+caret.H+2 {
				found = true
				continue
			}
			t.Fatalf("extra accent caret pixel outside the native caret at (%d,%d); native caret=%+v", x, y, caret)
		}
	}
	if !found {
		t.Fatalf("native caret geometry has no visible caret pixels: %+v", caret)
	}
}

func TestMarkdownLivePreviewAcceptanceDefaultModesAndHistory(t *testing.T) {
	const source = "# 中文任务资料\n\n第一段 **强调** 中文😀。\n\n第二段保留原文。\n"
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%v", dark), func(t *testing.T) {
			s, _, content, tester := markdownLiveAcceptanceFixture(t, source, dark, 1100)
			state := focusMarkdownLiveAcceptance(t, s, tester)
			live, buffer := state.live, state.live.Buffer()
			if live.SourceMode {
				t.Fatal("writable Markdown opened in source mode instead of Live Preview")
			}
			for _, text := range []string{"实时预览", "源码"} {
				if _, ok := tester.Find(text); ok {
					t.Fatalf("document mode control %q should only be available through the context menu", text)
				}
			}
			if _, ok := tester.Find("阅读"); ok {
				t.Fatal("writable Markdown still exposes the removed reading mode")
			}
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != source {
				t.Fatalf("whole-document copy stopped at a block: %q", tester.Clipboard())
			}
			at := len("# 中文任务资料\n\n第一段 ")
			live.SetSelection(at, at)
			tester.Frame()
			if !tester.HasText("中文任务资料") || tester.HasText("# 中文任务资料") || !tester.HasText("第一段 **强调** 中文😀。") {
				t.Fatalf("Live Preview did not render inactive heading and reveal active paragraph syntax: %q", tester.Texts())
			}
			tester.Type("新增😀 ")
			want := source[:at] + "新增😀 " + source[at:]
			assertMarkdownAcceptanceSource(t, s, state, want)
			anchor, caret := live.Selection()
			if !s.materialsUnit.state.documentDirty {
				t.Fatal("native input was not captured as an unsaved materials draft")
			}
			assertMarkdownAcceptanceSingleVisibleCaret(t, tester, dark)
			captureDesktopTestFrame(t, tester, fmt.Sprintf("markdown-live-default-dark-%v", dark))
			chooseMarkdownAcceptanceMode(t, tester, "source")
			if !live.SourceMode {
				t.Fatal("source context menu item did not change the editor mode")
			}
			if sourceAnchor, sourceCaret := live.Selection(); sourceAnchor != anchor || sourceCaret != caret {
				t.Fatal("source mode did not retain the document selection")
			}
			captureDesktopTestFrame(t, tester, fmt.Sprintf("markdown-source-dark-%v", dark))
			chooseMarkdownAcceptanceMode(t, tester, "live")
			state = focusMarkdownLiveAcceptance(t, s, tester)
			afterAnchor, afterCaret := state.live.Selection()
			if state.live != live || state.live.Buffer() != buffer || live.SourceMode || afterAnchor != anchor || afterCaret != caret {
				t.Fatalf("mode switching replaced document or selection: before=%d:%d after=%d:%d", anchor, caret, afterAnchor, afterCaret)
			}
			assertMarkdownAcceptanceSource(t, s, state, want)
			tester.Command("undo")
			assertMarkdownAcceptanceSource(t, s, state, source)
			tester.Command("redo")
			assertMarkdownAcceptanceSource(t, s, state, want)
			if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != source {
				t.Fatal("mode switching implicitly saved or changed the disk source", err)
			}
		})
	}
}

func TestMarkdownLivePreviewAcceptanceCrossParagraphUnicodeAndComposition(t *testing.T) {
	const source = "甲😀\n\n乙中\n\n末尾"
	s, _, content, tester := markdownLiveAcceptanceFixture(t, source, false, 960)
	state := focusMarkdownLiveAcceptance(t, s, tester)
	live, buffer := state.live, state.live.Buffer()
	live.SetSelection(0, 0)
	tester.Frame()
	for range 5 {
		tester.Key(ui.Shift, ui.KeyRight)
	}
	tester.Command("copy")
	if copied := tester.Clipboard(); copied != "甲😀\n\n乙" {
		t.Fatalf("keyboard selection did not cross the paragraph/emoji boundary: %q", copied)
	}
	tester.Type("跨段😀")
	assertMarkdownAcceptanceSource(t, s, state, "跨段😀中\n\n末尾")
	tester.Command("undo")
	assertMarkdownAcceptanceSource(t, s, state, source)
	live.SetSelection(buffer.Len(), buffer.Len())
	tester.Frame()
	tester.Compose("zhongwen", 8)
	if !live.Composing() {
		t.Fatal("simulated composition did not reach the Markdown text client")
	}
	key := s.documentAutosaveKey(content.Path)
	if !s.materialsUnit.state.markdownAutosave.Status(key).Composing {
		t.Fatal("composition did not pause document autosave")
	}
	if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != source {
		t.Fatal("composition changed the file before commit", err)
	}
	tester.Type("中文😀")
	if live.Composing() || s.materialsUnit.state.markdownAutosave.Status(key).Composing {
		t.Fatal("composition remained active after committing Chinese and emoji")
	}
	assertMarkdownAcceptanceSource(t, s, state, source+"中文😀")
	tester.Command("undo")
	assertMarkdownAcceptanceSource(t, s, state, source)
	tester.Command("selectAll")
	tester.Compose("跨段组合", 4)
	if !live.Composing() || !tester.HasText("跨段组合") || buffer.OriginalText() != source {
		t.Fatal("whole-document composition did not display preedit while preserving the source")
	}
	tester.Type("替换😀")
	assertMarkdownAcceptanceSource(t, s, state, "替换😀")
	tester.Command("undo")
	assertMarkdownAcceptanceSource(t, s, state, source)
	if state.live != live || state.live.Buffer() != buffer {
		t.Fatal("selection or composition created another document buffer")
	}
	captureDesktopTestFrame(t, tester, "markdown-cross-paragraph-unicode")
}

func TestMarkdownLivePreviewAcceptanceComplexBlocksExpandInPlace(t *testing.T) {
	const table = "| 名称 | 内容 |\n| --- | --- |\n| 中文 | <span>内联内容</span> |\n| 转义 | a\\|b |"
	const mermaid = "```mermaid\nflowchart LR\n    A[开始] --> B{条件}\n    B -->|是| C[完成]\n```"
	const source = "# 复杂块验收\n\n前文 **保留格式**。\n\n" + table + "\n\n间隔段落。\n\n" + mermaid + "\n\n末尾段落。\n"
	for _, width := range []int{620, 1100} {
		s, _, _, tester := markdownLiveAcceptanceFixture(t, source, width == 620, width)
		state := focusMarkdownLiveAcceptance(t, s, tester)
		live, buffer := state.live, state.live.Buffer()
		// A caret in the heading leaves table and fenced diagram inactive.
		live.SetSelection(2, 2)
		tester.Frame()
		captureDesktopTestFrame(t, tester, fmt.Sprintf("markdown-complex-inactive-%d", width))
		for _, active := range []struct{ name, block string }{{"table", table}, {"mermaid", mermaid}} {
			name, block := active.name, active.block
			at := strings.Index(source, block) + 2
			live.SetSelection(at, at)
			tester.Frame()
			if name == "table" {
				if tester.HasText(block) || !tester.HasText("名称") || !tester.HasText("a|b") {
					t.Fatalf("active table lost its rendered grid: %q", tester.Texts())
				}
			} else if !tester.HasText(block) {
				t.Fatalf("active %s did not reveal its complete original source: %q", name, tester.Texts())
			}
			if !tester.Focused("Markdown 编辑区") {
				t.Fatalf("active %s moved text focus away from the document client", name)
			}
			focusMarkdownLiveAcceptance(t, s, tester)
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != source || state.live != live || state.live.Buffer() != buffer {
				t.Fatalf("active %s broke whole-document selection or buffer identity", name)
			}
			live.SetSelection(at, at)
			tester.Frame()
			captureDesktopTestFrame(t, tester, fmt.Sprintf("markdown-complex-%s-active-%d", name, width))
		}
		assertMarkdownAcceptanceSource(t, s, state, source)
	}
}

func TestMarkdownLivePreviewAcceptancePreservesSourceAndConflict(t *testing.T) {
	const source = "# 字节保真\r\n\r\n保留  尾空格  \r\n\r\n<!-- future-widget:{\"v\":2} -->\r\n\r\n:::unknown keep=\"yes\"\r\n原始内容😀\r\n:::\r\n"
	s, _, content, tester := markdownLiveAcceptanceFixture(t, "\xef\xbb\xbf"+source, false, 1100)
	state := focusMarkdownLiveAcceptance(t, s, tester)
	lineEnd := len("# 字节保真")
	state.live.SetSelection(lineEnd, lineEnd)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	assertMarkdownAcceptanceSource(t, s, state, source[:lineEnd]+"\r\n"+source[lineEnd:])
	tester.Command("undo")
	assertMarkdownAcceptanceSource(t, s, state, source)
	state.live.SetSelection(0, 0)
	tester.Frame()
	tester.Type("<!-- edited -->\r\n")
	want := "<!-- edited -->\r\n" + source
	assertMarkdownAcceptanceSource(t, s, state, want)
	chooseMarkdownAcceptanceMode(t, tester, "source")
	chooseMarkdownAcceptanceMode(t, tester, "live")
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != "\xef\xbb\xbf"+want {
		t.Fatalf("native edit/save changed BOM, CRLF or unknown syntax: %q, %v", disk, err)
	}
	state = focusMarkdownLiveAcceptance(t, s, tester)
	state.live.SetSelection(0, 0)
	tester.Frame()
	tester.Type("local ")
	const external = "\xef\xbb\xbf# 外部新版本\r\n不要覆盖。\r\n"
	if err := os.WriteFile(content.Path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) == 0 || !s.materialsUnit.state.markdownAutosave.Status(s.documentAutosaveKey(content.Path)).Conflict {
		t.Fatal("external edit did not pause the native document save as a conflict")
	}
	if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != external {
		t.Fatal("conflicting native input overwrote the external file", err)
	}
	assertMarkdownAcceptanceSource(t, s, state, "local "+want)
	tester.Frame()
	captureDesktopTestFrame(t, tester, "markdown-external-conflict")
}

func TestMarkdownLivePreviewAcceptanceOtherReadersRemainReadonly(t *testing.T) {
	const source = "# 只读入口\n\n正文 **保留** 中文😀。\n"
	for _, route := range []string{"generic", "requirement-body", "requirement-comment", "skill-preview"} {
		t.Run(route, func(t *testing.T) {
			s := &app{page: "requirements"}
			var state *markdownEditorState
			var render func(*ui.Context, palette)
			switch route {
			case "requirement-body", "requirement-comment":
				reader := s.requirementReader("", "https://project.feishu.cn/acceptance/story/detail/123")
				t.Cleanup(s.requirementUnit.closeRequirementReaders)
				body := source
				if route == "requirement-comment" {
					body = "需求正文"
					s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "acceptance", AuthorName: "验收用户", Content: source}}})
					for _, comment := range reader.commentMarkdown {
						state = comment
					}
				}
				s.updateRequirementReaderBody(reader, application.RequirementContent{Body: body})
				if state == nil {
					state = reader.bodyMarkdown
				}
				render = func(c *ui.Context, p palette) { s.renderRequirementReadonlyMarkdownReader(c, p, reader) }
			case "skill-preview":
				state = newReadonlyMarkdownState("acceptance-skill", source, nil)
				browser := &skillsBrowserState{app: s, selectedDirectory: "acceptance-skill", selectedPath: "SKILL.md", preview: source, previewLoaded: true, previewMarkdown: true, markdown: state}
				render = func(c *ui.Context, p palette) { s.renderSkillsBrowserPreview(c, p, browser) }
			default:
				state = newReadonlyMarkdownState("acceptance-generic-reader", source, nil)
				render = func(c *ui.Context, p palette) { s.renderReadonlyMarkdownReader(c, p, state) }
			}
			t.Cleanup(func() { closeMarkdownPreviewState(state) })
			tester := ui.NewTester(func(c *ui.Context) {
				s.beginOverflowMenuFrame()
				ui.Column(c).Fill().Children(func() { render(c, setTheme(c, false)) })
				s.endOverflowMenuFrame()
			}, 800, 500)
			clickMarkdownAcceptance(t, tester, "正文 保留 中文😀。")
			tester.Command("selectAll")
			tester.Type("blocked")
			tester.Command("undo")
			if state.doc.Source() != source || !state.readOnly || state.live != nil {
				t.Fatal("read-only Markdown host acquired the materials editor")
			}
			for _, forbidden := range []string{"实时预览", "Markdown 编辑区"} {
				if tester.HasText(forbidden) {
					t.Fatalf("read-only host exposed materials editing control %q", forbidden)
				}
			}
			captureDesktopTestFrame(t, tester, "markdown-other-reader-readonly-"+route)
		})
	}
}

// This opt-in fixture writes only synthetic state into a new, isolated home.
// It deliberately avoids App.Snapshot, which also discovers user-local tools.
func TestPrepareMarkdownLivePreviewNativeSmokeHome(t *testing.T) {
	home := os.Getenv("SILVERWING_NATIVE_SMOKE_HOME")
	if home == "" {
		t.Skip("set SILVERWING_NATIVE_SMOKE_HOME to create the isolated GUI fixture")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(home) != "native-home" || filepath.Base(filepath.Dir(home)) != "acceptance" {
		t.Fatal("the opt-in fixture destination must be acceptance/native-home")
	}
	if entries, err := os.ReadDir(home); err == nil && len(entries) != 0 {
		t.Fatal("fixture destination already contains files; preserving it unchanged")
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	paths := storage.NewPaths(home)
	taskRoot := filepath.Join(home, "tasks")
	taskPath := filepath.Join(taskRoot, "markdown-native-smoke")
	if err := os.MkdirAll(taskPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "os-user"), 0700); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &taskRoot
	config.Theme = domain.ThemeLight
	config.DefaultWorkspaceToolIDs = []string{}
	config.AIRequirementNamingEnabled, config.AIRequirementNamingPrewarmEnabled = false, false
	if err := storage.NewConfigStore(paths).Save(config); err != nil {
		t.Fatal(err)
	}
	scope, err := documents.PrepareLocalTaskMaterials(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName = "Markdown 原生编辑验收", filepath.Base(taskPath)
	manifest.FeatureBranch = "smoke/markdown-live-preview"
	manifest.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manifest.UpdatedAt = manifest.CreatedAt
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &scope.Owned}
	if err := storage.NewManifestStore().Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	const source = "# Markdown 原生编辑验收\r\n\r\n在这一段输入中文与 emoji 😀，检查光标、选区和撤销。\r\n\r\n## 操作顺序\r\n\r\n1. 点击本段末尾，输入“原生输入成功”。\r\n2. 切换到源码，再回到实时预览。\r\n3. 使用 Ctrl+Z 撤销、Ctrl+Y 重做。\r\n4. 等待一秒后检查磁盘文件。\r\n\r\n| 名称 | 内容 |\r\n| --- | --- |\r\n| 中文 | <span>复杂表格原文</span> |\r\n| emoji | 😀 |\r\n\r\n```mermaid\r\nflowchart LR\r\n    A[输入] --> B[保存]\r\n```\r\n\r\n<!-- future-widget:{\"retain\":true} -->\r\n"
	documentPath := filepath.Join(scope.Owned, "资料.md")
	if err := os.WriteFile(documentPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(source)...), 0600); err != nil {
		t.Fatal(err)
	}
	reading := EmptyReadingSnapshot()
	reading.View, reading.LastTaskPath = "MATERIALS", taskPath
	material := DefaultMaterialsSnapshot()
	relative := "docs/资料.md"
	material.TaskPath, material.Root, material.SelectedPath = taskPath, &scope.Root, &relative
	material.ExpandedDirectories, material.ExpansionInitialized = []string{"docs"}, true
	material.DocumentTabs = MaterialsDocumentTabsSnapshot{Tabs: []MaterialsDocumentTab{{Path: relative, IsPinned: true}}, ActivePath: &relative, Initialized: true}
	material.SetFileReading(relative, DefaultReadingPositionSnapshot())
	reading.Materials = []MaterialsSnapshot{material}
	if err := NewReadingStateStore(filepath.Join(paths.Cache, "reading-state.json")).Save(reading); err != nil {
		t.Fatal(err)
	}
	if loaded, err := storage.NewManifestStore().Load(taskPath); err != nil || loaded.RequirementMaterials.WriteRoot == nil || *loaded.RequirementMaterials.WriteRoot != scope.Owned {
		t.Fatal("fixture manifest failed its current-schema round trip", err)
	}
	if loaded, err := storage.NewConfigStore(paths).Load(); err != nil || loaded.TaskRoot == nil || *loaded.TaskRoot != taskRoot {
		t.Fatal("fixture config failed its current-schema round trip", err)
	}
	if snap, err := documents.NewService(paths.Cache).MarkdownSnapshot(scope.Root, documentPath); err != nil || snap.Text != source || !snap.HasUTF8BOM {
		t.Fatal("fixture Markdown failed its source-preserving round trip", err)
	}
	t.Logf("isolated home: %s", home)
	t.Logf("task: %s", taskPath)
	t.Logf("editable Markdown: %s", documentPath)
}
