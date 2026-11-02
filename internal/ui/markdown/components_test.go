package markdownui_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	markdownui "silverwing-go/internal/ui/markdown"
)

func TestReaderIsIndependentAndDoesNotChangeSource(t *testing.T) {
	source := "# 标题\r\n\r\n| 名称 | 内容 |\r\n|---|---|\r\n| 甲 | **乙** |\r\n\r\n[链接](https://example.com)\r\n"
	reader := markdownui.NewReaderState(markdownui.Document{ID: "reader", Source: source})
	t.Cleanup(reader.Close)
	view := ui.NewTester(func(c *ui.Context) { markdownui.MarkdownReader(c, reader, markdownui.ReaderOptions{}).Fill() }, 650, 500)
	for _, text := range []string{"标题", "名称", "甲", "乙", "链接"} {
		if !view.HasText(text) {
			t.Fatalf("missing %q in %v", text, view.Texts())
		}
	}
	view.Type("不能写入")
	reader.SetMode(markdownui.Source)
	view.Frame()
	if reader.Snapshot().Source != source {
		t.Fatal("reader rewrote original bytes")
	}
}

func TestLiveTableEditsOneCellAndUndoRestoresOriginalBytes(t *testing.T) {
	source := "before\r\n\r\n| A | B |\r\n|---|:---:|\r\n| 甲 | 乙 |\r\n\r\nafter\r\n"
	state := markdownui.NewEditorState(markdownui.Document{ID: "table", Source: source})
	t.Cleanup(state.Close)
	native := state.NativeState()
	normalized := native.Buffer().Snapshot().String()
	at := strings.Index(normalized, "甲") + len("甲")
	native.SetSelection(at, at)
	native.Focus()
	var changes []markdownui.Snapshot
	view := ui.NewTester(func(c *ui.Context) {
		markdownui.MarkdownEditor(c, state, markdownui.EditorOptions{OnChange: func(s markdownui.Snapshot) { changes = append(changes, s) }}).Fill()
	}, 700, 500)
	if view.HasText("| A | B |") || !view.HasText("甲") {
		t.Fatalf("active table expanded into source: %v", view.Texts())
	}
	view.Type("新")
	want := strings.Replace(source, "甲", "甲新", 1)
	if got := state.Snapshot().Source; got != want {
		t.Fatalf("cell edit = %q want %q", got, want)
	}
	if len(changes) != 1 {
		t.Fatalf("change delivery: %#v", changes)
	}
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatalf("undo lost original bytes: %q", state.Snapshot().Source)
	}
	view.Command("redo")
	if state.Snapshot().Source != want {
		t.Fatal("redo lost cell edit")
	}
	if err := state.SetMode(markdownui.Source); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := state.SetMode(markdownui.Live); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatal("mode switch discarded history")
	}
}

func TestTableNavigationAndStructuralCharacters(t *testing.T) {
	source := "| A | B |\n|---|---|\n| 甲 | 乙 |"
	state := markdownui.NewEditorState(markdownui.Document{ID: "table-key", Source: source})
	defer state.Close()
	native := state.NativeState()
	at := strings.Index(source, "甲") + len("甲")
	native.SetSelection(at, at)
	native.Focus()
	view := ui.NewTester(func(c *ui.Context) { markdownui.MarkdownEditor(c, state, markdownui.EditorOptions{}).Fill() }, 700, 500)
	view.Type("|")
	if !strings.Contains(state.Snapshot().Source, "甲\\|") {
		t.Fatalf("pipe escaped incorrectly: %q", state.Snapshot().Source)
	}
	view.Key(0, ui.KeyTab)
	view.Type("新")
	if !strings.Contains(state.Snapshot().Source, "新乙") {
		t.Fatalf("tab did not move to next cell: %q", state.Snapshot().Source)
	}
	view.Key(ui.Shift, ui.KeyEnter)
	if !strings.Contains(state.Snapshot().Source, "<br>") {
		t.Fatal("cell newline broke grid")
	}
	view.Key(0, ui.KeyTab)
	if strings.Count(state.Snapshot().Source, "\n") != 3 {
		t.Fatalf("last tab did not append row: %q", state.Snapshot().Source)
	}
}

func TestReloadRejectsStaleVersionAndInstancesAreIndependent(t *testing.T) {
	a := markdownui.NewEditorState(markdownui.Document{ID: "a", Source: "甲"})
	defer a.Close()
	b := markdownui.NewEditorState(markdownui.Document{ID: "b", Source: "乙"})
	defer b.Close()
	version := a.Snapshot().Version
	if err := a.NativeState().ReplaceRange(0, len("甲"), "新", version); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload("旧读取", version); err != markdownui.ErrStale {
		t.Fatalf("stale reload: %v", err)
	}
	if a.Snapshot().Source != "新" || b.Snapshot().Source != "乙" {
		t.Fatal("document state leaked")
	}
}

func TestResolveLocalAndExternalLinks(t *testing.T) {
	for _, test := range []struct {
		raw, path, url string
		line, column   int
	}{
		{"D:/tasks/中文 目录/A.java:75:3", "D:/tasks/中文 目录/A.java", "", 75, 3},
		{"file:///D:/tasks/A.java#L40", "D:/tasks/A.java", "", 40, 0},
		{"other.md:75", "D:/docs/other.md", "", 75, 0},
		{"Other.java:75:3", "D:/docs/Other.java", "", 75, 3},
		{"中文 文件.md:75:3#section", "D:/docs/中文 文件.md", "", 75, 3},
		{"./README:2", "D:/docs/README", "", 2, 0},
		{"../code/Other.java:75:3", "D:/code/Other.java", "", 75, 3},
		{"file:///D:/tasks/A.java#L40:3", "D:/tasks/A.java", "", 40, 3},
		{"https://example.com:443/a?q=1", "", "https://example.com:443/a?q=1", 0, 0},
		{"https://example.com/A.java:75:3", "", "https://example.com/A.java:75:3", 0, 0},
		{"mailto:user@example.com", "", "mailto:user@example.com", 0, 0},
	} {
		t.Run(test.raw, func(t *testing.T) {
			got, err := markdownui.ResolveLink("D:/docs/readme.md", test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if strings.ReplaceAll(got.Path, "\\", "/") != test.path || got.URL != test.url || got.Line != test.line || got.Column != test.column {
				t.Fatalf("target: %#v", got)
			}
		})
	}
	for _, raw := range []string{"javascript:alert(1)", "javascript:75", "javascript:Other.java:75", "unknown:75", "data:text/plain,hello", "file://server/share/a.md", "http:///missing-host", "http:75"} {
		if _, err := markdownui.ResolveLink("D:/a.md", raw); err == nil {
			t.Fatalf("accepted invalid link %s", raw)
		}
	}
}

func TestLiveLinkClickEditsLabelAndDestinationWithoutSourceMode(t *testing.T) {
	const source = "Before [中文链接](https://example.com) after."
	state := markdownui.NewEditorState(markdownui.Document{ID: "links", Source: source, Path: "D:/docs/a.md"})
	defer state.Close()
	state.NativeState().SetSelection(0, 0)
	state.NativeState().Focus()
	view := ui.NewTester(func(c *ui.Context) { markdownui.MarkdownEditor(c, state, markdownui.EditorOptions{}).Fill() }, 700, 500)
	if view.HasText(source) {
		t.Fatal("active link paragraph expanded source")
	}
	if err := view.Click("中文链接"); err != nil {
		t.Fatal(err)
	}
	if !view.HasText("编辑链接") {
		t.Fatalf("link click did not open editor: %v", view.Texts())
	}
	if err := view.Click("显示文字"); err != nil {
		t.Fatal(err)
	}
	view.Command("selectAll")
	view.Type("新的文字")
	if err := view.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Snapshot().Source, "[新的文字](<https://example.com>)") {
		t.Fatalf("link save: %q", state.Snapshot().Source)
	}
	state.NativeState().Focus()
	view.Frame()
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatalf("link edit did not undo atomically: %q", state.Snapshot().Source)
	}
}

func TestLiveLinkControlClickAndCancelDoNotMutate(t *testing.T) {
	const source = "A [**链接**][ref] B\n\n[ref]: https://example.com"
	state := markdownui.NewEditorState(markdownui.Document{ID: "reference", Source: source})
	defer state.Close()
	var navigated string
	view := ui.NewTester(func(c *ui.Context) {
		markdownui.MarkdownEditor(c, state, markdownui.EditorOptions{ReaderOptions: markdownui.ReaderOptions{OnNavigate: func(target markdownui.LinkTarget) { navigated = target.URL }}}).Fill()
	}, 700, 500)
	if err := view.ClickWith(ui.Ctrl, "链接"); err != nil {
		t.Fatal(err)
	}
	if navigated != "https://example.com" || view.HasText("编辑链接") {
		t.Fatalf("direct navigation: %q %v", navigated, view.Texts())
	}
	if err := view.Click("链接"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if state.Snapshot().Source != source {
		t.Fatal("cancel changed reference source")
	}
	if err := view.Click("链接"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if state.Snapshot().Source != source {
		t.Fatal("no-op save reformatted reference")
	}
	if err := view.Click("链接"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("移除链接"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Snapshot().Source, "A **链接** B") || !strings.Contains(state.Snapshot().Source, "[ref]: https://example.com") {
		t.Fatalf("remove changed shared reference: %q", state.Snapshot().Source)
	}
}

func TestTableCompositionAndReadOnlyEditor(t *testing.T) {
	const source = "| A | B |\n|---|---|\n| 甲 | 乙 |"
	state := markdownui.NewEditorState(markdownui.Document{ID: "ime", Source: source})
	defer state.Close()
	at := strings.Index(source, "甲") + len("甲")
	state.NativeState().SetSelection(at, at)
	state.NativeState().Focus()
	readOnly := false
	changes := 0
	var composing []bool
	view := ui.NewTester(func(c *ui.Context) {
		markdownui.MarkdownEditor(c, state, markdownui.EditorOptions{ReadOnly: readOnly, OnChange: func(markdownui.Snapshot) { changes++ }, OnCompositionChanged: func(value bool) { composing = append(composing, value) }}).Fill()
	}, 700, 500)
	view.Compose("中😀", 2)
	if state.Snapshot().Source != source || changes != 0 || !state.Snapshot().Composing {
		t.Fatal("preedit leaked into source")
	}
	if err := state.Reload("stale", state.Snapshot().Version); err != markdownui.ErrComposing {
		t.Fatalf("composition reload %v", err)
	}
	if !state.NativeState().EndComposition() {
		t.Fatal("composition commit failed")
	}
	view.Frame()
	if !strings.Contains(state.Snapshot().Source, "甲中😀") || changes != 1 || len(composing) != 2 || !composing[0] || composing[1] {
		t.Fatalf("commit %q %d %v", state.Snapshot().Source, changes, composing)
	}
	readOnly = true
	view.Frame()
	before := state.Snapshot().Source
	view.Type("拒绝写入")
	if state.Snapshot().Source != before {
		t.Fatal("read-only editor accepted typing")
	}
}

func TestSharedReaderFindAndSourceSearch(t *testing.T) {
	first := markdownui.NewReaderState(markdownui.Document{ID: "first", Source: "first needle"})
	defer first.Close()
	second := markdownui.NewReaderState(markdownui.Document{ID: "second", Source: "second needle"})
	defer second.Close()
	find := &markdownui.FindController{Query: "needle"}
	view := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			markdownui.MarkdownReader(c, first, markdownui.ReaderOptions{Find: find}).Grow(1)
			markdownui.MarkdownReader(c, second, markdownui.ReaderOptions{Find: find}).Grow(1)
		})
	}, 700, 500)
	view.Frame()
	if find.MatchCount() != 2 {
		t.Fatalf("combined matches %d", find.MatchCount())
	}
	second.Close()
	if find.MatchCount() != 1 {
		t.Fatal("closed reader remained in search")
	}
	first.SetDocument(markdownui.Document{ID: "first", Source: "[label](https://example.com/private-address)"})
	first.SetMode(markdownui.Source)
	find.Query = "private-address"
	view.Frame()
	if find.MatchCount() != 1 {
		t.Fatalf("source URL not searchable: %d", find.MatchCount())
	}
}
