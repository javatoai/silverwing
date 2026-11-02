package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestTextBlocksDefaultOptionsAfterFrameworkUpgrade(t *testing.T) {
	doc := parser.New(parser.Default)
	doc.Set("# Title\n\n**Body** with $x$.")
	blocks := TextBlocks(doc)
	if len(blocks) != 2 || blocks[0].Text != "Title" || !strings.Contains(blocks[1].Text, "Body") {
		t.Fatalf("text blocks: %#v", blocks)
	}
}

func liveTester(src string, at int, opts EditorOptions) (*EditorState, *ui.Tester) {
	s := NewEditorState(editor.NewBufferPreservingLineEndings(src))
	s.SetSelection(at, at)
	s.Focus()
	tt := ui.NewTester(func(c *ui.Context) { Editor(c, s, opts).Fill() }, 700, 500)
	return s, tt
}

func TestLiveEditorProjectionKeepsInactiveMarkdownAndActiveSource(t *testing.T) {
	src := "# Heading\n\nSome **bold** and [link](https://go.dev).\n\nLast *paragraph*."
	s, tt := liveTester(src, strings.Index(src, "bold"), EditorOptions{})
	if !tt.HasText("Heading") || !tt.HasText("Some **bold** and [link](https://go.dev).") || !tt.HasText("Last paragraph.") {
		t.Fatalf("wrong projection: %#v", tt.Texts())
	}
	if active, _ := tt.TextInputActive(); !active {
		t.Fatal("root has no native input client")
	}
	s.SetSelection(len(src), len(src))
	tt.Frame()
	if !tt.HasText("Some bold and link.") || !tt.HasText("Last *paragraph*.") {
		t.Fatalf("moving caret did not change active block: %#v", tt.Texts())
	}
	if got := s.Buffer().OriginalText(); got != src {
		t.Fatalf("projection rewrote source: %q", got)
	}
}

func TestLiveEditorCrossBlockSelectionAndUndo(t *testing.T) {
	src := "甲😀\r\n\r\n乙 **bold**\r\n"
	s, tt := liveTester(src, 0, EditorOptions{})
	for range 5 {
		tt.Key(ui.Shift, ui.KeyRight)
	}
	a, z := s.Selection()
	if a != 0 || s.Buffer().Slice(a, z) != "甲😀\n\n乙" {
		t.Fatalf("cross-block selection [%d,%d] %q", a, z, s.Buffer().Slice(a, z))
	}
	tt.Type("新")
	if got := s.Buffer().OriginalText(); got != "新 **bold**\r\n" {
		t.Fatalf("replace: %q", got)
	}
	tt.Command("undo")
	if got := s.Buffer().OriginalText(); got != src {
		t.Fatalf("undo did not restore exact source: %q", got)
	}
	a, z = s.Selection()
	if a != 0 || z != len("甲😀\n\n乙") {
		t.Fatalf("undo selection %d,%d", a, z)
	}
	tt.Command("redo")
	if s.Buffer().OriginalText() != "新 **bold**\r\n" {
		t.Fatal("redo failed")
	}
}

func TestLiveEditorSourceModeSharesHistoryAndSelection(t *testing.T) {
	s, tt := liveTester("# Heading\n\nText", len("# Heading\n\nText"), EditorOptions{})
	tt.Type(" one")
	s.SourceMode = true
	tt.Frame()
	tt.Type(" two")
	s.SourceMode = false
	tt.Frame()
	tt.Command("selectAll")
	tt.Command("copy")
	if got := tt.Clipboard(); got != "# Heading\n\nText one two" {
		t.Fatalf("whole source copy: %q", got)
	}
	tt.Command("undo")
	if got := s.Buffer().String(); got != "# Heading\n\nText" {
		t.Fatalf("same-owner undo across display modes: %q", got)
	}
}

func TestLiveEditorComplexBlocksRevealWholeSource(t *testing.T) {
	src := "# Title\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```mermaid\ngraph LR\nA-->B\n```\n\nEnd"
	options := &Options{Code: map[string]func(*ui.Context, *CodeBlockView){"mermaid": func(c *ui.Context, b *CodeBlockView) { ui.Text(c, "Native diagram") }}}
	s, tt := liveTester(src, len(src), EditorOptions{Options: options})
	if !tt.HasText("a") || !tt.HasText("Native diagram") {
		t.Fatalf("inactive complex renderer missing: %#v", tt.Texts())
	}
	s.SetSelection(strings.Index(src, "| 1"), strings.Index(src, "| 1"))
	tt.Frame()
	if !tt.HasText("| a | b |\n|---|---|\n| 1 | 2 |") {
		t.Fatalf("table not raw in place: %#v", tt.Texts())
	}
	s.SetSelection(strings.Index(src, "A-->"), strings.Index(src, "A-->"))
	tt.Frame()
	if !tt.HasText("```mermaid\ngraph LR\nA-->B\n```") {
		t.Fatalf("diagram not raw in place: %#v", tt.Texts())
	}
	if tt.HasText("Native diagram") {
		t.Fatal("diagram and source both rendered for active block")
	}
}

func TestLiveEditorCompositionIsVirtualAndOneUndoStep(t *testing.T) {
	src := "# Title\r\n\r\n甲😀乙\r\n"
	changes := 0
	var composing []bool
	s, tt := liveTester(src, len("# Title\n\n甲"), EditorOptions{OnChange: func() { changes++ }, OnCompositionChanged: func(b bool) { composing = append(composing, b) }})
	s.SetSelection(len("# Title\n\n甲"), len("# Title\n\n甲😀"))
	tt.Frame()
	tt.Compose("拼", 1)
	tt.Compose("拼音", 2)
	if !s.Composing() || s.Buffer().OriginalText() != src || changes != 0 {
		t.Fatalf("preedit changed buffer: %q, callbacks %d", s.Buffer().OriginalText(), changes)
	}
	if caret, active := tt.TextCaret(); !active || caret.H <= 0 || caret.Y < 0 || caret.Y >= 500 {
		t.Fatalf("IME candidate geometry unavailable: %+v %v", caret, active)
	}
	marked, ok := s.client.MarkedRange()
	if !ok || marked.End-marked.Start != 2 {
		t.Fatalf("marked range %+v %v", marked, ok)
	}
	tt.Type("拼音")
	if s.Composing() || changes != 1 || s.Buffer().OriginalText() != "# Title\r\n\r\n甲拼音乙\r\n" {
		t.Fatalf("commit: %q callbacks %d composing %v", s.Buffer().OriginalText(), changes, s.Composing())
	}
	tt.Command("undo")
	if s.Buffer().OriginalText() != src {
		t.Fatal("composition undo failed")
	}
	if len(composing) != 2 || !composing[0] || composing[1] {
		t.Fatalf("composition events %v", composing)
	}
}

func TestLiveEditorUTF16QueriesAndReplacement(t *testing.T) {
	s, tt := liveTester("甲😀乙\n\n**尾**", len("甲"), EditorOptions{})
	text, actual := s.client.TextForRange(ui.TextInputRange{Start: 1, End: 3})
	if text != "😀" || actual.Start != 1 || actual.End != 3 {
		t.Fatalf("UTF16 query %q %+v", text, actual)
	}
	r := ui.TextInputRange{Start: 1, End: 3}
	s.client.SetMarkedText(&r, "中😀", ui.TextInputRange{Start: 3, End: 3})
	tt.Frame()
	if got := s.virtualText(); got != "甲中😀乙\n\n**尾**" {
		t.Fatalf("virtual text: %q", got)
	}
	sel := s.client.Selection()
	if sel.Range.Start != 4 {
		t.Fatalf("UTF16 selection %+v", sel)
	}
	s.client.ReplaceText(nil, "文")
	if got := s.Buffer().String(); got != "甲文乙\n\n**尾**" {
		t.Fatalf("UTF16 replacement: %q", got)
	}
}

func TestLiveEditorLimitRejectionPreservesComposition(t *testing.T) {
	s, tt := liveTester("ab", 2, EditorOptions{})
	s.Buffer().SetMaxSourceBytes(3)
	tt.Compose("中文", 2)
	if s.EndComposition() {
		t.Fatal("over-limit composition accepted")
	}
	if !s.Composing() || s.Buffer().String() != "ab" {
		t.Fatal("rejected preedit was lost or buffer changed")
	}
	s.client.SetMarkedText(nil, "c", ui.TextInputRange{Start: 1, End: 1})
	if !s.EndComposition() || s.Buffer().String() != "abc" {
		t.Fatal("shortened preedit cannot commit")
	}
}

func TestLiveEditorPreservesGraphemeOnBackspace(t *testing.T) {
	text := "A👨‍👩‍👧‍👦B"
	s, tt := liveTester(text, len(text)-1, EditorOptions{})
	tt.Key(0, ui.KeyBackspace)
	if got := s.Buffer().String(); got != "AB" {
		t.Fatalf("split grapheme: %q", got)
	}
	tt.Command("undo")
	if s.Buffer().String() != text {
		t.Fatal("grapheme undo")
	}
}

func TestLiveEditorPointerSelectionCrossesRenderedBlocks(t *testing.T) {
	s, tt := liveTester("# Title\n\nFirst **bold**.\n\nLast *paragraph*.", 0, EditorOptions{})
	first, ok := tt.Find("First bold.")
	if !ok {
		t.Fatal("first paragraph absent")
	}
	last, ok := tt.Find("Last paragraph.")
	if !ok {
		t.Fatal("last paragraph absent")
	}
	tt.Press(first.X+2, first.Y+first.H/2)
	frozenStart, frozenEnd := s.frozenStart, s.frozenEnd
	tt.Move(last.X+last.W-1, last.Y+last.H/2)
	if s.activeStart != frozenStart || s.activeEnd != frozenEnd {
		t.Fatal("projection moved under drag")
	}
	tt.Release(last.X+last.W-1, last.Y+last.H/2)
	a, z := s.Selection()
	if a >= z || !strings.Contains(s.Buffer().Slice(a, z), "\n\n") {
		t.Fatalf("drag not continuous: %d,%d %q", a, z, s.Buffer().Slice(a, z))
	}
	tt.Command("copy")
	if tt.Clipboard() != s.snapshot.OriginalSlice(a, z) {
		t.Fatal("drag copy differs from source selection")
	}
}

func TestLiveEditorRevealsCaretAtEndOfLongSource(t *testing.T) {
	src := strings.Repeat("line 中文 😀\n", 180) + "tail"
	s, tt := liveTester(src, 0, EditorOptions{})
	s.SourceMode = true
	s.SetSelection(len(s.Buffer().String()), len(s.Buffer().String()))
	tt.Frame()
	tt.Frame()
	caret, active := tt.TextCaret()
	if !active || caret.H <= 0 || caret.Y < 0 || caret.Y+caret.H > 501 {
		t.Fatalf("caret not revealed: %+v active=%v scroll=%+v", caret, active, s.scroll)
	}
	tt.Type("!")
	if !strings.HasSuffix(s.Buffer().String(), "tail!") {
		t.Fatal("source tail input missed")
	}
}

func TestLiveEditorSourceMapsMatchParserEscapesAndCode(t *testing.T) {
	src := "中 **bold** &amp; \\*literal\\* [link](target) 😀\n\n```go\n中文😀\n  next()\n```"
	s, _ := liveTester(src, 0, EditorOptions{})
	d := s.view.doc
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind == parser.Paragraph {
			p := s.view.para(i, spanStyle{})
			maps := paragraphSourceBoundaries(p, src, 0, p.runes, b.Start)
			for r, got := range maps {
				if want := p.sourceAt(src, r, false); got != want {
					t.Fatalf("paragraph rune %d: %d want %d", r, got, want)
				}
			}
		}
		if b.Kind == parser.CodeBlock {
			maps := codeSourceBoundaries(d, i)
			for r, got := range maps {
				if want := s.view.srcAt(elemInfo{block: i, line: 0}, r, false); got != want {
					t.Fatalf("code rune %d: %d want %d", r, got, want)
				}
			}
		}
	}
}

func TestLiveEditorMarkedReplacementKeepsUnselectedPreedit(t *testing.T) {
	s, tt := liveTester("a😀z", 1, EditorOptions{})
	r := ui.TextInputRange{Start: 1, End: 3}
	s.client.SetMarkedText(&r, "中文", ui.TextInputRange{Start: 2, End: 2})
	inside := ui.TextInputRange{Start: 2, End: 3}
	s.client.SetMarkedText(&inside, "😀", ui.TextInputRange{Start: 2, End: 2})
	tt.Frame()
	if s.virtualText() != "a中😀z" {
		t.Fatalf("partial marked replacement: %q", s.virtualText())
	}
	s.EndComposition()
	tt.Command("undo")
	if s.Buffer().String() != "a😀z" {
		t.Fatal("partial replacement was more than one undo")
	}
}
