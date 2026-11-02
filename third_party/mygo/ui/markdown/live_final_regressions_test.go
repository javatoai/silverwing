package markdown

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestTablePartialIMECommitMapsCaretBeforeEscapedRetainedSuffix(t *testing.T) {
	for _, test := range []struct{ marked, want string }{{"中|乙", `新\|乙`}, {"中\n乙", "新<br>乙"}, {"中|😀", `新\|😀`}, {`中\|乙`, `新\|乙`}} {
		t.Run(test.marked, func(t *testing.T) {
			source := "| A | B |\r\n|---|---|\r\n|  | keep |\r\n"
			normalized := strings.ReplaceAll(source, "\r\n", "\n")
			at := strings.Index(normalized, "|  |") + 2
			state, view := liveTester(source, at, EditorOptions{InPlaceTables: true})
			at = structuredTables(state)[0].rows[1][0].start
			state.SetSelection(at, at)
			view.Frame()
			state.client.SetMarkedText(nil, test.marked, ui.TextInputRange{Start: editorUnits(test.marked), End: editorUnits(test.marked)})
			view.Frame()
			marked, ok := state.client.MarkedRange()
			if !ok {
				t.Fatal("missing marked range")
			}
			state.client.ReplaceText(&ui.TextInputRange{Start: marked.Start, End: marked.Start + 1}, "新")
			view.Frame()
			rawAt := len(state.Buffer().Snapshot().OriginalSlice(0, at))
			want := source[:rawAt] + test.want + source[rawAt:]
			if state.Buffer().OriginalText() != want {
				t.Fatalf("partial source %q want %q", state.Buffer().OriginalText(), want)
			}
			a, z := state.Selection()
			if a != at+len("新") || z != a {
				t.Fatalf("escaped suffix moved caret %d:%d want %d", a, z, at+len("新"))
			}
			view.Type("后")
			if state.Buffer().OriginalText() != strings.Replace(want, "新", "新后", 1) {
				t.Fatalf("typing entered escape suffix: %q", state.Buffer().OriginalText())
			}
			view.Command("undo")
			if state.Buffer().OriginalText() != want {
				t.Fatal("ordinary typing undo not separate")
			}
			view.Command("undo")
			if state.Buffer().OriginalText() != source {
				t.Fatal("partial composition not one undo")
			}
			view.Command("redo")
			if state.Buffer().OriginalText() != want {
				t.Fatal("partial composition redo changed bytes")
			}
		})
	}
}

func TestTablePartialIMERejectedCommitKeepsOriginalBuffer(t *testing.T) {
	source := "| A | B |\n|---|---|\n|  | keep |"
	at := strings.Index(source, "|  |") + 2
	state, view := liveTester(source, at, EditorOptions{InPlaceTables: true})
	at = structuredTables(state)[0].rows[1][0].start
	state.SetSelection(at, at)
	view.Frame()
	state.Buffer().SetMaxSourceBytes(len(source))
	state.client.SetMarkedText(nil, "中|乙", ui.TextInputRange{Start: 3, End: 3})
	view.Frame()
	marked, _ := state.client.MarkedRange()
	version := state.Buffer().Version()
	state.client.ReplaceText(&ui.TextInputRange{Start: marked.Start, End: marked.Start + 1}, "新")
	view.Frame()
	if state.Buffer().OriginalText() != source || state.Buffer().Version() != version || !state.Composing() {
		t.Fatal("rejected partial IME lost source or composition")
	}
}

func TestHiddenLinkStartCaretIsActuallyPainted(t *testing.T) {
	for _, test := range []struct {
		name, source string
		at           int
		tab          bool
	}{
		{"document", "[Link](https://example.com)", 0, false},
		{"cell", "| A | B |\n|---|---|\n| [Link](https://example.com) | plain |", 0, false},
		{"tab", "| A | B |\n|---|---|\n| plain | [Link](https://example.com) |", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := NewEditorState(editor.NewBufferPreservingLineEndings(test.source))
			at := test.at
			if test.name == "cell" {
				at = strings.Index(test.source, "[Link]")
			}
			if test.tab {
				at = strings.Index(test.source, "plain")
			}
			state.SetSelection(at, at)
			state.Focus()
			view := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				theme.Accent = ui.Hex("#ff00ff")
				c.SetTheme(theme)
				Editor(c, state, EditorOptions{InPlaceTables: true, RenderedLinks: true, Options: &Options{Theme: &Theme{Link: ui.Hex("#008800")}}}).Fill()
			}, 700, 400)
			if test.tab {
				view.Key(0, ui.KeyTab)
			}
			view.Frame()
			caret, ok := view.TextCaret()
			if !ok {
				t.Fatal("no input-method caret geometry")
			}
			painted := false
			image := view.Image()
			bounds := image.Bounds()
			for y := int(math.Floor(float64(caret.Y))); y < int(math.Ceil(float64(caret.Y+caret.H))); y++ {
				for x := int(math.Floor(float64(caret.X))) - 1; x <= int(math.Ceil(float64(caret.X+caret.W)))+1; x++ {
					if x >= bounds.Min.X && y >= bounds.Min.Y && x < bounds.Max.X && y < bounds.Max.Y {
						if pixel := color.RGBAModel.Convert(image.At(x, y)).(color.RGBA); pixel.R == 255 && pixel.G == 0 && pixel.B == 255 {
							painted = true
						}
					}
				}
			}
			if !painted {
				t.Fatalf("caret geometry exists but no accent pixels at %+v source caret %d", caret, state.caret)
			}
			if state.Buffer().OriginalText() != test.source {
				t.Fatal("painting caret changed source")
			}
		})
	}
}
