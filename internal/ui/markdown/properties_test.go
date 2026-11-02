package markdownui

import (
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/markdowntext"
	"strings"
	"testing"
)

const propertyFixture = "\ufeff---\ntags:\n  - 业务/订单支付 # keep\n  - 环境/prod\ncreated_at: '2026-08-28 11:30:39'\nupdated_at: '2026-08-28 14:37:25'\nai_thread_id: 01a03b2-long-identifier\nunknown: {nested: true}\n---\n\n# 原文标题\n\n正文不改。\n"

func TestPublicPropertiesSharedReadingEditingAndUndo(t *testing.T) {
	for _, width := range []int{260, 880} {
		for _, dark := range []bool{false, true} {
			source := strings.ReplaceAll(propertyFixture, "\n", "\r\n")
			state := NewEditorState(Document{ID: "properties", Source: source})
			v := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				c.SetTheme(theme)
				MarkdownEditor(c, state, EditorOptions{}).Fill()
			}, width, 650)
			if !v.HasText("文档属性") || !v.HasText("业务 / 订单支付") || !v.HasText("创建时间：2026-08-28 11:30:39") || v.HasText("ai_thread_id") || v.HasText("# keep") {
				t.Fatal("metadata not compactly projected")
			}
			if err := v.Click("更多属性"); err != nil {
				t.Fatal(err)
			}
			if !v.HasText("01a03b2-long-identifier") {
				t.Fatal("details not readable")
			}
			buffer := state.native.Buffer()
			caret := strings.Index(buffer.String(), "正文不改") + len("正文")
			state.native.SetSelection(caret, caret)
			for _, change := range []TagChange{{Action: RenameTag, Index: 0, Value: "业务/签约"}, {Action: AddTag, Value: "平台/android"}, {Action: RemoveTag, Index: 0}} {
				change.DocumentID, change.Version = state.document.ID, state.Snapshot().Version
				if err := state.ApplyTagChange(change); err != nil {
					t.Fatal(err)
				}
				v.Frame()
				if strings.Contains(strings.ReplaceAll(state.Snapshot().Source, "\r\n", ""), "\n") {
					t.Fatal("metadata action introduced bare LF into CRLF document")
				}
				if state.native.Buffer() != buffer || !strings.HasSuffix(state.Snapshot().Source, "# 原文标题\r\n\r\n正文不改。\r\n") {
					t.Fatal("body or Buffer replaced")
				}
				state.native.Focus()
				v.Frame()
				v.Command("undo")
				if state.Snapshot().Source != source {
					t.Fatal("one Undo did not restore complete BOM/CRLF source")
				}
				v.Command("redo")
				v.Command("undo")
			}
			if err := v.Click("收起文档属性"); err != nil {
				t.Fatal(err)
			}
			if v.HasText("业务 / 订单支付") {
				t.Fatal("collapse did not hide properties")
			}
			state.Close()
		}
	}
}

func TestPropertiesReviewBodyFormatRawRangeEOFAndIME(t *testing.T) {
	const source = "---\ntags: [a]\n---\nhello"
	s, v := dailyPublicEditor(source)
	s.native.SetSelection(len(source), len(source))
	v.Frame()
	v.Type("!")
	if s.Snapshot().Source != source+"!" {
		t.Fatal("body EOF inserted newline", s.Snapshot().Source)
	}
	s, v = dailyPublicEditor(source)
	a := strings.Index(source, "hello")
	s.native.SetSelection(a, a+5)
	v.Frame()
	if err := s.Execute(EditorCommand{Kind: Bold}); err != nil || !strings.HasSuffix(s.Snapshot().Source, "**hello**") {
		t.Fatal("body Bold after metadata failed", err)
	}
	v.Command("undo")
	s.native.SetSelection(a, a+5)
	v.Frame()
	if err := s.Execute(EditorCommand{Kind: InsertLink, Target: "https://example.com"}); err != nil || !strings.Contains(s.Snapshot().Source, "https://example.com") {
		t.Fatal("body link after metadata failed", err)
	}
	s, _ = dailyPublicEditor(source)
	if err := s.native.ReplaceRange(0, 3, "abc", s.Snapshot().Version); err != nil || !strings.HasPrefix(s.Snapshot().Source, "abc\n") {
		t.Fatal("raw range was redirected", err)
	}
	for _, text := range []string{"---\ntags: []\n---", "---\ntags: []\n---\n"} {
		s, v = dailyPublicEditor(text)
		v.Compose("中文", 2)
		if !v.HasText("中文") {
			t.Fatal("prefix-only preedit not displayed")
		}
		if !s.native.EndComposition() {
			t.Fatal("composition rejected")
		}
		if !strings.Contains(s.Snapshot().Source, "---\n中文") {
			t.Fatal("composition merged closing fence", s.Snapshot().Source)
		}
	}
	s, v = dailyPublicEditor("---\ntitle: hello\n---\n# title: hello\n")
	if err := s.RevealLocation(0, 0, "title-hello"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.native.Selection()
	if s.Mode() != Live || a != strings.LastIndex(s.Snapshot().Source, "# title") {
		t.Fatal("fragment navigated into metadata")
	}
	_ = v
}

func TestPropertiesReviewCodeFenceAndCRLFAddMissing(t *testing.T) {
	source := "---\ntags: [a]\nnote: |\n  ```sql\n---\n```sql\nx\n```\n"
	s, v := dailyPublicEditor(source)
	at := strings.LastIndex(source, "\nx\n") + 1
	s.native.SetSelection(at, at)
	v.Frame()
	v.Type("```\n")
	if !strings.Contains(s.Snapshot().Source, "---\n````sql\n") {
		t.Fatal("code fence escaped metadata-aware guard", s.Snapshot().Source)
	}
	for _, source := range []string{"---\r\ncreated_at: 'kept'\r\n---\r\nbody", "---\r\ntags:\r\n - a # comment   \r\n---\r\nbody"} {
		s, _ := dailyPublicEditor(source)
		if err := s.ApplyTagChange(TagChange{DocumentID: s.document.ID, Version: s.Snapshot().Version, Action: AddTag, Value: "新标签"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ReplaceAll(s.Snapshot().Source, "\r\n", ""), "\n") {
			t.Fatal("CRLF missing/block insertion lost line endings")
		}
	}
}

func TestPropertiesReviewReaderSourceWidthAndInteraction(t *testing.T) {
	const source = "---\ntags: &tags [a]\ncreated_at: 'unchanged'\n---\n# body\n"
	reader := NewReaderState(Document{ID: "embedded", Source: source})
	v := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().Children(func() { MarkdownReader(c, reader, ReaderOptions{ContentOnly: true}) })
	}, 300, 450)
	if err := v.Click("查看属性源码"); err != nil {
		t.Fatal(err)
	}
	if reader.Mode() != Source || !v.HasText("tags: &tags [a]") {
		t.Fatal("ContentOnly source entry did not show raw metadata")
	}
	reader = NewReaderState(Document{ID: "readonly-nav", Source: propertyFixture})
	v = ui.NewTester(func(c *ui.Context) { MarkdownReader(c, reader, ReaderOptions{}).Fill() }, 300, 450)
	reader.RevealLocation(3, "")
	v.Frame()
	if reader.Mode() != Source || reader.revealLine != 0 {
		t.Fatal("reader metadata navigation not revealed")
	}
	reader = NewReaderState(Document{ID: "width", Source: strings.Replace(propertyFixture, "环境/prod", "环境/一个非常长的多级路径/abcdefghijklmnopqrstuvwxyz0123456789", 1)})
	v = ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			MarkdownReader(c, reader, ReaderOptions{}).Grow(1).MinWidth(0)
			ui.Column(c).Width(300).Children(func() { ui.Text(c, "right") })
		})
	}, 600, 600)
	v.Frame()
	v.Frame()
	if rect, ok := v.Find("标签：环境/一个非常长的多级路径/abcdefghijklmnopqrstuvwxyz0123456789"); !ok || rect.X+rect.W > 301 {
		t.Fatal("reader properties exceed viewport", rect)
	}
	s, editor := dailyPublicEditor(propertyFixture)
	before := s.native.InteractionVersion()
	if err := editor.Click("添加标签"); err != nil {
		t.Fatal(err)
	}
	if s.native.InteractionVersion() <= before {
		t.Fatal("property interaction did not protect pending view restore")
	}
}

func TestPropertiesReviewInitialPairsLineEndingsAndBareCR(t *testing.T) {
	const source = "---\r\ntags: []\r\n---"
	s, v := dailyPublicEditor(source)
	v.Type("(")
	if s.Snapshot().Source != source+"\r\n()" {
		t.Fatal("initial body input lost pair or CRLF", s.Snapshot().Source)
	}
	v.Type(")")
	if s.Snapshot().Source != source+"\r\n()" {
		t.Fatal("generated closing delimiter did not skip")
	}
	v.Command("undo")
	if s.Snapshot().Source != source {
		t.Fatal("structural first input was not one undo")
	}
	const cr = "---\rtitle: kept\r---\rbody"
	s, _ = dailyPublicEditor(cr)
	if err := s.ApplyTagChange(TagChange{DocumentID: s.document.ID, Version: s.Snapshot().Version, Action: AddTag, Value: "测试"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Snapshot().Source, "\n") {
		t.Fatal("introduced LF in CR document")
	}
}

func TestPropertiesReviewSourceReturnPreviewOwnerFragmentAndKeyboard(t *testing.T) {
	const source = "---\ntags: &tags [a]\n---\n# body\n"
	reader := NewReaderState(Document{ID: "source-return", Source: source})
	v := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().Children(func() { MarkdownReader(c, reader, ReaderOptions{ContentOnly: true}) })
	}, 400, 450)
	if err := v.Click("查看属性源码"); err != nil {
		t.Fatal(err)
	}
	if err := v.RightClick("tags: &tags [a]"); err != nil {
		t.Fatal(err)
	}
	if err := v.ChooseMenuItem("查看 Markdown 预览"); err != nil {
		t.Fatal(err)
	}
	if reader.Mode() != Preview || !v.HasText("文档属性") {
		t.Fatal("ContentOnly cannot return preview")
	}
	reader.SetMode(Source)
	reader.RevealLocation(0, "body")
	v.Frame()
	if reader.revealFragment != "" || reader.revealLine != 0 {
		t.Fatal("embedded source fragment not consumed")
	}
	s, editor := dailyPublicEditor(propertyFixture)
	if err := s.SetMode(Preview); err != nil {
		t.Fatal(err)
	}
	editor.Frame()
	if err := s.RevealLocation(3, 1, ""); err != nil {
		t.Fatal(err)
	}
	editor.Frame()
	if s.Mode() != Source {
		t.Fatal("owner preview reset metadata source navigation")
	}
	s, editor = dailyPublicEditor(propertyFixture)
	for i := 0; i < 24 && (!editor.Focused("添加标签") || editor.Focused("Markdown")); i++ {
		editor.Key(0, ui.KeyTab)
	}
	if !editor.Focused("添加标签") || editor.Focused("Markdown") {
		t.Fatal("add tag not keyboard reachable")
	}
	before := s.native.InteractionVersion()
	editor.Key(0, ui.KeyEnter)
	if !editor.HasText("标签名称") || s.native.InteractionVersion() <= before {
		t.Fatalf("keyboard open failed: dialog=%v epoch=%d->%d tagInput=%v addFocus=%v", s.reader.properties.dialog, s.native.InteractionVersion(), before, editor.HasText("标签名称"), editor.Focused("添加标签"))
	}
}

func TestPropertiesEmbeddedSourceExplicitLineWinsOverFragment(t *testing.T) {
	source := "---\ntags: &tags [a]\n---\n" + strings.Repeat("body\n", 50) + "# L2\n"
	reader := NewReaderState(Document{ID: "line-priority", Source: source})
	reader.SetMode(Source)
	var scroll ui.ScrollState
	view := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).TrackScroll(&scroll).Fill().Children(func() { MarkdownReader(c, reader, ReaderOptions{ContentOnly: true}) })
	}, 400, 120)
	reader.RevealLocation(2, "L2")
	view.Frame()
	view.Frame()
	if scroll.Y > 80 {
		t.Fatal("fragment scrolled past explicit metadata line", scroll.Y)
	}
	if rect, ok := view.Find("tags: &tags [a]"); !ok || rect.Y < 0 || rect.Y+rect.H > 120 {
		t.Fatal("fragment overrode explicit metadata line", rect)
	}
}

func TestPropertiesReaderStaysBelowHostHeader(t *testing.T) {
	reader := NewReaderState(Document{ID: "host-header", Source: propertyFixture + strings.Repeat("正文段落。\n\n", 50)})
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.Text(c, "Host header").Height(100)
			ui.Row(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).AlignSelf(ui.Stretch).Children(func() {
					MarkdownReader(c, reader, ReaderOptions{}).Grow(1).MinWidth(0)
				})
				ui.Text(c, "Other pane").Width(300)
			})
		})
	}, 900, 600)
	view.Frame()
	view.Frame()
	if rect, ok := view.Find("收起文档属性"); !ok || rect.Y < 100 {
		for _, label := range []string{"Host header", "文档属性", "原文标题", "Other pane"} {
			at, found := view.Find(label)
			t.Log(label, at, found)
		}
		t.Fatal("properties overlap host header", rect, reader.scroll)
	}
}

func TestPropertiesTagRemoveIsIndependentAndUndoable(t *testing.T) {
	s, view := dailyPublicEditor(propertyFixture)
	if err := view.Click("删除标签：业务/订单支付"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Snapshot().Source, "业务/订单支付") || !strings.Contains(s.Snapshot().Source, "# keep") || view.HasText("标签名称") {
		t.Fatal("remove opened editor or changed unrelated comment", s.Snapshot().Source)
	}
	view.Command("undo")
	if s.Snapshot().Source != propertyFixture {
		t.Fatal("direct remove was not one undo")
	}
	view.Command("redo")
	if strings.Contains(s.Snapshot().Source, "业务/订单支付") {
		t.Fatal("redo did not remove tag")
	}
	view.Command("undo")
	if err := view.Click("标签：业务/订单支付"); err != nil {
		t.Fatal(err)
	}
	if !view.HasText("标签名称") {
		t.Fatal("text lost its independent edit action")
	}
	view.Key(0, ui.KeyEscape)
	reader := NewReaderState(Document{ID: "read-only-pills", Source: propertyFixture})
	readView := ui.NewTester(func(c *ui.Context) { MarkdownReader(c, reader, ReaderOptions{}).Fill() }, 320, 400)
	if readView.HasText("删除标签：") || readView.HasText("添加标签") || !readView.HasText("业务 / 订单支付") {
		t.Fatal("readonly pills expose mutation or lack text")
	}
}
func TestPropertiesPopoverAndReadOnly(t *testing.T) {
	s, v := dailyPublicEditor(propertyFixture)
	if err := v.Click("标签：业务/订单支付"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("标签名称"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("业务/签约")
	v.Key(0, ui.KeyEnter)
	v.Frame()
	if !strings.Contains(s.Snapshot().Source, "\"业务/签约\" # keep") {
		t.Fatal("popover did not edit tag", s.Snapshot().Source)
	}
	v.Key(ui.Cmd, ui.KeyZ)
	v.Frame()
	if s.Snapshot().Source != propertyFixture {
		t.Fatal("property keyboard undo failed")
	}
	s.options.ReadOnly = true
	if err := s.ApplyTagChange(TagChange{DocumentID: s.document.ID, Version: s.Snapshot().Version, Action: AddTag, Value: "拒绝"}); err == nil {
		t.Fatal("readonly mutation accepted")
	}
	s.options.ReadOnly = false
	if err := s.ApplyTagChange(TagChange{DocumentID: "other", Version: s.Snapshot().Version, Action: AddTag, Value: "拒绝"}); err == nil {
		t.Fatal("stale owner accepted")
	}
	reader := NewReaderState(Document{ID: "reader", Source: propertyFixture})
	r := ui.NewTester(func(c *ui.Context) { MarkdownReader(c, reader, ReaderOptions{}).Fill() }, 300, 600)
	if !r.HasText("业务 / 订单支付") || r.HasText("添加标签") {
		t.Fatal("reader differs or exposes write actions")
	}
}
func TestPropertiesBodyBoundaryAndSourceNavigation(t *testing.T) {
	s, v := dailyPublicEditor(propertyFixture)
	prefix := markdowntext.ParseProperties(s.native.Buffer().String()).End
	s.native.SetSelection(prefix, prefix)
	s.native.Focus()
	v.Frame()
	before := s.Snapshot().Source
	v.Key(0, ui.KeyBackspace)
	if s.Snapshot().Source != before {
		t.Fatal("Backspace erased hidden metadata")
	}
	v.Type("写正文")
	if !strings.Contains(s.Snapshot().Source, "---\n写正文") {
		t.Fatal("typing went into hidden metadata")
	}
	if err := s.RevealLocation(3, 1, ""); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != Source {
		t.Fatal("metadata navigation stayed hidden")
	}
	for _, source := range []string{"---\ntags: []\n---", "---\ntags: []\n---\n"} {
		s, v := dailyPublicEditor(source)
		s.native.Focus()
		v.Frame()
		v.Type("正文")
		if !strings.Contains(s.Snapshot().Source, "---\n正文") {
			t.Fatal("prefix-only document cannot start body", s.Snapshot().Source)
		}
	}
}

func TestPropertiesRejectStaleAndCompositionWithoutChangingSource(t *testing.T) {
	s, v := dailyPublicEditor(propertyFixture)
	command := TagChange{DocumentID: s.document.ID, Version: s.Snapshot().Version, Action: AddTag, Value: "工具/日志"}
	s.native.SetSelection(len(s.native.Buffer().String()), len(s.native.Buffer().String()))
	s.native.Focus()
	v.Frame()
	v.Type("新增正文")
	before := s.Snapshot().Source
	if err := s.ApplyTagChange(command); err != ErrStale || s.Snapshot().Source != before {
		t.Fatal("stale popup mutated document", err)
	}
	command.Version = s.Snapshot().Version
	v.Compose("中文😀", 2)
	if err := s.ApplyTagChange(command); err != ErrComposing || s.Snapshot().Source != before {
		t.Fatal("property mutation crossed IME", err)
	}
	s.native.EndComposition()
	before = s.Snapshot().Source
	s.options.ReadOnly = true
	command.Version = s.Snapshot().Version
	if err := s.ApplyTagChange(command); err == nil || s.Snapshot().Source != before {
		t.Fatal("readonly property mutation")
	}
}
