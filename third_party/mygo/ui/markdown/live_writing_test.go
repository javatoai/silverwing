package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func logWritingAST(t *testing.T, s *EditorState) {
	t.Helper()
	d := s.writingDocument()
	t.Logf("writing selection=%d:%d structure=%d source=%q", s.anchor, s.caret, s.writingStructure(d, s.caret), s.source)
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		start, end := s.blockRange(b)
		t.Logf("block %d kind=%v parent=%d next=%d range=%d:%d marker=%d:%d number=%d", i, b.Kind, b.Parent, b.Next, start, end, b.Start, b.End, b.Number)
	}
}

func TestWritingOrderedEnterContinuesActualItemWithoutRenumbering(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		for _, marker := range []string{".", ")"} {
			source := "\ufeff1" + marker + " 第一项" + ending + "2" + marker + " 中文😀" + ending + "9" + marker + " 保留编号"
			at := strings.Index(normalizedText(source), "中文😀") + len("中文😀")
			s, view := liveTester(source, at, EditorOptions{ContinueBlocks: true})
			buffer := s.Buffer()
			view.Key(0, ui.KeyEnter)
			want := strings.Replace(source, "中文😀", "中文😀"+ending+"3"+marker+" ", 1)
			if got := buffer.OriginalText(); got != want || s.Buffer() != buffer {
				t.Fatalf("ordered Enter did not continue the actual item: %q want %q", got, want)
			}
			caret := strings.Index(normalizedText(want), "3"+marker+" ") + len("3"+marker+" ")
			if a, z := s.Selection(); a != caret || z != caret {
				t.Fatalf("continued item caret = %d:%d want %d", a, z, caret)
			}
			view.Command("undo")
			if buffer.OriginalText() != source {
				t.Fatal("ordered Enter did not restore original BOM/EOL/numbering in one undo")
			}
			view.Command("redo")
			if buffer.OriginalText() != want {
				t.Fatal("ordered continuation redo lost exact source bytes")
			}
		}
	}
}

func TestWritingMarkerWidthTabGapAndRenderedLinkTail(t *testing.T) {
	for _, fixture := range []struct{ source, want string }{
		{"9) 甲😀", "9) 甲😀\n10) "},
		{"009. 甲😀", "009. 甲😀\n010. "},
		{"2. [链接😀](https://example.com)", "2. [链接😀](https://example.com)\n3. "},
	} {
		s, view := liveTester(fixture.source, len(fixture.source), EditorOptions{ContinueBlocks: true, RenderedLinks: true})
		view.Key(0, ui.KeyEnd) // use the real rendered-label caret at the line edge
		view.Key(0, ui.KeyEnter)
		if got := s.Buffer().OriginalText(); got != fixture.want {
			t.Fatalf("marker width or hidden link tail was damaged: %q want %q", got, fixture.want)
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != fixture.source {
			t.Fatal("marker/link-tail continuation lost its original source undo")
		}
	}
	const source = "-\t前项\r\n-\t当前😀"
	const want = "-\t前项\r\n    -\t当前😀"
	s, view := liveTester(source, len(normalizedText(source)), EditorOptions{ContinueBlocks: true})
	view.Key(0, ui.KeyTab)
	if s.Buffer().OriginalText() != want {
		t.Fatalf("Tab ignored the parser's actual tab-gap columns: %q", s.Buffer().OriginalText())
	}
	view.Key(ui.Shift, ui.KeyTab)
	if s.Buffer().OriginalText() != source {
		t.Fatal("tab-gap item did not outdent with original raw tabs preserved")
	}
}

func TestWritingMultiItemReverseSelectionPreservesDirectionAndUndo(t *testing.T) {
	const source = "> - 前项\r\n> - 甲😀\r\n>   wrap\r\n> - 乙😀\r\n> - 后项"
	const want = "> - 前项\r\n>   - 甲😀\r\n>     wrap\r\n>   - 乙😀\r\n> - 后项"
	normalized := normalizedText(source)
	a := strings.Index(normalized, "甲😀")
	z := strings.Index(normalized, "乙😀") + len("乙😀")
	s, view := liveTester(source, a, EditorOptions{ContinueBlocks: true})
	s.SetSelection(z, a)
	view.Frame()
	view.Key(0, ui.KeyTab)
	if s.Buffer().OriginalText() != want || s.anchor <= s.caret {
		t.Fatalf("multi-Item indent changed another block or reversed its selection: %q %d:%d", s.Buffer().OriginalText(), s.anchor, s.caret)
	}
	view.Key(ui.Shift, ui.KeyTab)
	if s.Buffer().OriginalText() != source || s.anchor <= s.caret {
		t.Fatal("multi-Item outdent did not keep original item bytes and selection direction")
	}
	view.Command("undo")
	view.Command("undo")
	if anchor, caret := s.Selection(); s.Buffer().OriginalText() != source || anchor != z || caret != a {
		t.Fatalf("one undo per tree move did not restore exact source/selection: %q %d:%d", s.Buffer().OriginalText(), anchor, caret)
	}
}

func TestWritingIMEAndTransactionRejectionRemainAtomic(t *testing.T) {
	const source = "2. 甲😀"
	s, view := liveTester(source, len(source), EditorOptions{ContinueBlocks: true})
	version := s.Buffer().Version()
	view.Compose("候选😀", 4)
	view.Key(0, ui.KeyEnter)
	if !s.Composing() || s.Buffer().Version() != version || s.Buffer().OriginalText() != source {
		t.Fatal("writing keys intercepted the active native IME")
	}
	view.Type("提交")
	view.Key(0, ui.KeyEnter)
	if s.Buffer().OriginalText() != source+"提交\n3. " {
		t.Fatalf("post-IME Enter did not continue the original Item: %q", s.Buffer().OriginalText())
	}
	view.Command("undo")
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("IME and subsequent writing action did not retain separate native undo steps")
	}
	s, view = liveTester(source, len(source), EditorOptions{ContinueBlocks: true})
	version = s.Buffer().Version()
	s.Buffer().SetMaxSourceBytes(len(source))
	view.Key(0, ui.KeyEnter)
	if s.Buffer().Version() != version || s.Buffer().OriginalText() != source {
		t.Fatal("size rejection partially inserted a writing prefix")
	}
	s.Buffer().SetMaxSourceBytes(1000)
	if err := s.ReplaceRange(len(source), len(source), "新", version); err != nil {
		t.Fatal(err)
	}
	before := s.Buffer().OriginalText()
	s.applyWriting(version, []editor.Edit{{Start: 0, End: 0, Text: "stale"}}, nil)
	if s.Buffer().OriginalText() != before {
		t.Fatal("stale writing transaction changed a newer document version")
	}
}

func TestWritingBulletTaskQuoteAndShiftEnter(t *testing.T) {
	for _, fixture := range []struct {
		source, want string
		mods         ui.Modifiers
	}{
		{"- 甲😀", "- 甲😀\n- ", 0},
		{"* 甲😀", "* 甲😀\n* ", 0},
		{"+ 甲😀", "+ 甲😀\n+ ", 0},
		{"- [x] 甲😀", "- [x] 甲😀\n- [ ] ", 0},
		{"- [X] 甲😀", "- [X] 甲😀\n- [ ] ", 0},
		{"> 引用😀", "> 引用😀\n> ", 0},
		{"> > 引用😀", "> > 引用😀\n> > ", 0},
		{"> 2) 甲😀", "> 2) 甲😀\n> 3) ", 0},
		{"2. 甲😀", "2. 甲😀\n   ", ui.Shift},
		{"> - [x] 甲😀", "> - [x] 甲😀\n>   ", ui.Shift},
	} {
		t.Run(fixture.source, func(t *testing.T) {
			s, view := liveTester(fixture.source, len(fixture.source), EditorOptions{ContinueBlocks: true})
			view.Key(fixture.mods, ui.KeyEnter)
			if got := s.Buffer().OriginalText(); got != fixture.want {
				t.Fatalf("list/quote Enter = %q want %q", got, fixture.want)
			}
			view.Type("续写")
			if s.Buffer().OriginalText() != fixture.want+"续写" {
				t.Fatal("typing did not land after the new content prefix")
			}
			view.Command("undo")
			view.Command("undo")
			if s.Buffer().OriginalText() != fixture.source {
				t.Fatal("writing action did not keep one source undo step")
			}
		})
	}
}

func TestWritingEmptyPrefixesExitOneLevel(t *testing.T) {
	for _, fixture := range []struct {
		source, want string
		key          ui.Key
	}{
		{"- ", "", ui.KeyEnter},
		{"2) ", "", ui.KeyEnter},
		{"- [ ] ", "", ui.KeyEnter},
		{"- [x] ", "", ui.KeyEnter},
		{"- 上\n  - ", "- 上\n- ", ui.KeyEnter},
		{"- 上\n  - 子\n  - ", "- 上\n  - 子\n- ", ui.KeyEnter},
		{"> - ", "> ", ui.KeyEnter},
		{"> > ", "> ", ui.KeyEnter},
		{"> ", "", ui.KeyEnter},
		{"- ", "", ui.KeyBackspace},
		{"- [x] ", "", ui.KeyBackspace},
		{"- 上\n  - ", "- 上\n- ", ui.KeyBackspace},
		{"- 上\n  - 子\n  - ", "- 上\n  - 子\n- ", ui.KeyBackspace},
		{"> > ", "> ", ui.KeyBackspace},
	} {
		t.Run(fixture.source+string(rune(fixture.key)), func(t *testing.T) {
			s, view := liveTester(fixture.source, len(fixture.source), EditorOptions{ContinueBlocks: true})
			view.Key(0, fixture.key)
			if got := s.Buffer().OriginalText(); got != fixture.want {
				logWritingAST(t, s)
				t.Fatalf("empty prefix did not leave one AST level: %q want %q", got, fixture.want)
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != fixture.source {
				t.Fatal("empty-prefix action did not undo as one source edit")
			}
		})
	}
}

func TestWritingTabMovesWholeItemWithContinuationAndSubtree(t *testing.T) {
	const source = "1. 前项\r\n2. 当前😀\r\n   continuation\r\n   - 子项\r\n3. 后项"
	const want = "1. 前项\r\n\r\n   2. 当前😀\r\n      continuation\r\n      - 子项\r\n3. 后项"
	const outdented = "1. 前项\r\n\r\n2. 当前😀\r\n   continuation\r\n   - 子项\r\n3. 后项"
	at := strings.Index(normalizedText(source), "当前😀") + len("当前😀")
	s, view := liveTester(source, at, EditorOptions{ContinueBlocks: true})
	view.Key(0, ui.KeyTab)
	if got := s.Buffer().OriginalText(); got != want {
		t.Fatalf("Tab did not move the whole Item subtree: %q want %q", got, want)
	}
	d := parser.Parse(s.source)
	first, second, child := -1, -1, -1
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.Item {
			continue
		}
		if b.Ordered && b.Number == 1 {
			first = i
		} else if b.Ordered && b.Number == 2 {
			second = i
		} else if !b.Ordered && b.Start == strings.Index(s.source, "- 子项") {
			child = i
		}
	}
	if first < 0 || second < 0 || child < 0 || d.Block(d.Block(second).Parent).Parent != first || d.Block(d.Block(child).Parent).Parent != second {
		t.Fatal("indent did not create the intended real AST parent and preserve the child subtree")
	}
	continuation := false
	for i := second + 1; i < d.Block(second).Next; i++ {
		if d.Block(i).Kind == parser.Paragraph && strings.Contains(d.Lines(i), "continuation") {
			continuation = true
		}
	}
	if !continuation {
		t.Fatal("indent detached the Item's wrapped paragraph from its subtree")
	}
	view.Key(ui.Shift, ui.KeyTab)
	if s.Buffer().OriginalText() != outdented {
		logWritingAST(t, s)
		t.Fatalf("Shift+Tab did not outdent the same whole Item while preserving its separator: %q", s.Buffer().OriginalText())
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != want {
		t.Fatal("outdent was not a separate logical undo step")
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("indent undo did not restore exact original subtree bytes")
	}
	firstState, firstView := liveTester("- 首项😀", len("- 首项😀"), EditorOptions{ContinueBlocks: true})
	version := firstState.Buffer().Version()
	firstView.Key(0, ui.KeyTab)
	if firstState.Buffer().OriginalText() != "- 首项😀" || firstState.Buffer().Version() != version {
		t.Fatal("Tab on the first item invented a parent or turned it into literal code")
	}
}

func TestWritingFlagSourceReadOnlyAndLiteralBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		source string
		at     int
		opts   EditorOptions
		mode   bool
	}{
		{"2. item", len("2. item"), EditorOptions{}, false},
		{"2. item", len("2. item"), EditorOptions{ContinueBlocks: true}, true},
		{"```md\n- literal\n```", len("```md\n- literal"), EditorOptions{ContinueBlocks: true, InPlaceCode: true}, false},
		{"    - literal", len("    - literal"), EditorOptions{ContinueBlocks: true, InPlaceCode: true}, false},
		{"<div>\n- literal\n</div>", len("<div>\n- literal"), EditorOptions{ContinueBlocks: true}, false},
		{"---\ntitle:\n  - metadata\n---\n\n1. body", len("---\ntitle:\n  - metadata"), EditorOptions{ContinueBlocks: true}, false},
		{"- 上\n  ---", len("- 上\n  ---"), EditorOptions{ContinueBlocks: true}, false},
		{"- 上\n  ===", len("- 上\n  ==="), EditorOptions{ContinueBlocks: true}, false},
	} {
		s, view := liveTester(fixture.source, fixture.at, fixture.opts)
		s.SourceMode = fixture.mode
		view.Frame()
		view.Key(0, ui.KeyEnter)
		want := fixture.source[:fixture.at] + "\n" + fixture.source[fixture.at:]
		if fixture.source == "    - literal" {
			// Preserve the existing code-card continuation indentation; only
			// list/quote marker generation must stay outside literal code.
			want = "    - literal\n    "
		}
		if got := s.Buffer().OriginalText(); got != want {
			t.Fatalf("writing aid changed a raw/default/literal scope: %q want %q", got, want)
		}
	}
	s, view := liveTester("2. item", len("2. item"), EditorOptions{ContinueBlocks: true, ReadOnly: true})
	view.Key(0, ui.KeyEnter)
	view.Key(0, ui.KeyTab)
	view.Key(0, ui.KeyBackspace)
	if s.Buffer().OriginalText() != "2. item" {
		t.Fatal("read-only document accepted a writing action")
	}
}
