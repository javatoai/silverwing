package desktop

import (
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"slices"
	"testing"
)

func TestReadonlyReadingContextActionsPreserveSelection(t *testing.T) {
	state := newReadonlyMarkdownState("menu-fixture", "# 标题\n\n正文 **重点**\n\n```go\nvalue()\n```\n", nil)
	defer closeMarkdownPreviewState(state)
	s := &app{}
	refreshes := 0
	disabled := false
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Children(func() {
			s.renderReadonlyMarkdownReaderWithMenu(c, p, state, func(m *overflowMenuBuilder) {
				if m.Item("refresh", "刷新测试文档").Disabled(disabled).Chosen() {
					refreshes++
				}
			})
		})
	}, 420, 500)
	if tester.HasText("更多文档操作") {
		t.Fatal("obsolete more trigger")
	}
	if err := tester.Click("正文 重点"); err != nil {
		t.Fatal(err)
	}
	tester.Command("selectAll")
	tester.Command("copy")
	selected := tester.Clipboard()
	tester.Key(ui.Cmd|ui.Shift, ui.KeyC)
	selectedMarkdown := tester.Clipboard()
	if err := tester.RightClick("正文 重点"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Copy", "Select All", "Copy as Markdown", "查找正文 · Ctrl+F", "查看 Markdown 源码", "刷新测试文档"} {
		if !slices.Contains(tester.Menu(), label) {
			t.Fatalf("missing %q: %q", label, tester.Menu())
		}
	}
	if refreshes != 0 {
		t.Fatal("opening menu performed refresh")
	}
	if err := tester.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != selected {
		t.Fatalf("right click lost selection: %q != %q", tester.Clipboard(), selected)
	}
	tester.RightClick("正文 重点")
	if err := tester.ChooseMenuItem("Copy as Markdown"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != selectedMarkdown {
		t.Fatalf("Markdown copy: %q", tester.Clipboard())
	}
	tester.RightClick("正文 重点")
	if err := tester.ChooseMenuItem("查找正文 · Ctrl+F"); err != nil {
		t.Fatal(err)
	}
	if !state.readonlyReader.findOpen {
		t.Fatal("find did not open")
	}
	tester.RightClick("正文 重点")
	if err := tester.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	if !state.readonlyReader.sourceMode {
		t.Fatal("source did not open")
	}
	if err := tester.RightClick("正文 **重点**"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tester.Menu(), "查看 Markdown 预览") {
		t.Fatalf("source lost reader actions: %q", tester.Menu())
	}
	if err := tester.ChooseMenuItem("查看 Markdown 预览"); err != nil {
		t.Fatal(err)
	}
	disabled = true
	tester.Frame()
	tester.RightClick("正文 重点")
	if tester.ChooseMenuItem("刷新测试文档") == nil {
		t.Fatal("disabled refresh chosen")
	}
	disabled = false
	tester.Frame()
	tester.RightClick("正文 重点")
	if err := tester.ChooseMenuItem("刷新测试文档"); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh calls %d", refreshes)
	}
}

func TestRequirementReadingContextRoutesBodyAndComments(t *testing.T) {
	s := &app{}
	r := s.requirementReader("", "https://example.test/requirement")
	r.bodyMarkdown = newReadonlyMarkdownState("body", "需求正文", nil)
	r.commentsMarkdown = newReadonlyMarkdownState("comments", "评论内容", nil)
	defer closeMarkdownPreviewState(r.bodyMarkdown)
	defer closeMarkdownPreviewState(r.commentsMarkdown)
	calls := 0
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Children(func() {
			s.renderRequirementReaderActions(c, p, r, func(m *overflowMenuBuilder) {
				if m.Item("refresh", "刷新需求正文评论").Chosen() {
					calls++
				}
			})
			s.prepareRequirementMarkdownReader(r)
			s.renderMarkdownPreview(c, p, r.bodyMarkdown)
			s.renderMarkdownPreview(c, p, r.commentsMarkdown)
		})
	}, 480, 360)
	for _, text := range []string{"需求正文", "评论内容"} {
		if err := tester.RightClick(text); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(tester.Menu(), "查找正文和评论 · Ctrl+F") || !slices.Contains(tester.Menu(), "刷新需求正文评论") {
			t.Fatalf("section menu %q", tester.Menu())
		}
		if calls != 0 {
			t.Fatal("menu open refreshed")
		}
	}
	if err := tester.ChooseMenuItem("刷新需求正文评论"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("refresh not routed")
	}
	// A prior-account callback cannot execute against the newly selected account.
	old := r.contextMenu
	s.snapshot.Home = "other-account"
	r.contextMenu = old
	tester = ui.NewTester(func(c *ui.Context) { s.renderMarkdownPreview(c, setTheme(c, false), r.bodyMarkdown) }, 480, 360)
	tester.RightClick("需求正文")
	if slices.Contains(tester.Menu(), "刷新需求正文评论") {
		t.Fatal("stale account actions survived")
	}
}

func TestReadonlyReadingThemedMenuEscapeAndEdges(t *testing.T) {
	for _, dark := range []bool{false, true} {
		state := newReadonlyMarkdownState("edge-fixture", "右键边缘正文", nil)
		s := &app{}
		tester := ui.NewTester(func(c *ui.Context) {
			p := setTheme(c, dark)
			ui.MenuScope(c, "reader-edge")
			ui.Column(c).Fill().Children(func() {
				ui.Spacer(c)
				s.renderReadonlyMarkdownReaderActions(c, p, state, nil)
				s.renderMarkdownPreview(c, p, state)
			})
			ui.ThemedMenus(c, "reader-edge")
		}, 360, 360)
		tester.Click("右键边缘正文")
		tester.Command("selectAll")
		tester.Command("copy")
		before := tester.Clipboard()
		r, _ := tester.Find("右键边缘正文")
		tester.RightClickAt(r.X+10, r.Y+r.H/2)
		tester.Frame()
		menu, ok := tester.Find("查看 Markdown 源码")
		if !ok {
			t.Fatalf("missing themed action: %q bounds=%+v native=%q", tester.Texts(), r, tester.Menu())
		}
		if menu.X < 0 || menu.Y < 0 || menu.X+menu.W > 360.5 || menu.Y+menu.H > 360.5 {
			t.Fatalf("menu outside window: %+v", menu)
		}
		tester.Key(0, ui.KeyEscape)
		if tester.HasText("查看 Markdown 源码") {
			t.Fatal("Escape did not dismiss")
		}
		tester.Command("copy")
		if tester.Clipboard() != before {
			t.Fatal("Escape lost selection/focus")
		}
		closeMarkdownPreviewState(state)
	}
}

func TestMaterialsReadingContextAndEmptyReader(t *testing.T) {
	s, action, result := materialsTextPreviewFixture(t, "fixture.md", []byte("资料 **正文**\n"))
	s.applyMaterialsActionResult(action, result)
	s.readingSession = &desktopReadingSession{loaded: true, store: NewReadingStateStore(filepath.Join(t.TempDir(), "reading.json")), cache: EmptyReadingSnapshot(), scrollStates: map[string]readingScrollBinding{}}
	t.Cleanup(func() { s.closeReadingSession() })
	tt := ui.NewTester(func(c *ui.Context) { s.materials(c, setTheme(c, false)) }, 800, 500)
	if err := tt.RightClick("资料 正文"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Copy", "Copy as Markdown", "查找正文 · Ctrl+F", "查看 Markdown 源码", "重新读取文档"} {
		if !slices.Contains(tt.Menu(), label) {
			t.Fatalf("missing %q in %q", label, tt.Menu())
		}
	}
	for _, label := range []string{"重命名", "新建文档", "移入回收站…"} {
		if slices.Contains(tt.Menu(), label) {
			t.Fatal("management action leaked into text menu")
		}
	}
	if err := tt.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	if s.readingMode() != "SOURCE" {
		t.Fatal("materials mode route lost")
	}
	// The source editor paints its text; its filename label also appears in
	// the tree, so hide the tree before resolving the editor's public bounds.
	s.documentsCollapsed = true
	tt.Frame()
	source, ok := tt.Find("fixture.md")
	if !ok || source.W <= 0 || source.H <= 0 {
		t.Fatal("materials source editor bounds missing")
	}
	tt.RightClickAt(source.X+source.W/2, source.Y+source.H/2)
	if !slices.Contains(tt.Menu(), "查看 Markdown 预览") {
		t.Fatal("materials source menu missing")
	}
	if err := tt.ChooseMenuItem("查看 Markdown 预览"); err != nil {
		t.Fatal(err)
	}
	if s.readingMode() != "RENDERED" {
		t.Fatal("materials preview mode route lost")
	}
	closeMarkdownPreviewState(s.markdown)
	empty := newReadonlyMarkdownState("empty", "", nil)
	defer closeMarkdownPreviewState(empty)
	tt = ui.NewTester(func(c *ui.Context) { s.renderReadonlyMarkdownReader(c, setTheme(c, false), empty) }, 420, 300)
	tt.RightClickAt(200, 150)
	if !slices.Contains(tt.Menu(), "查看 Markdown 源码") {
		t.Fatalf("empty reader lost actions %q", tt.Menu())
	}
}
