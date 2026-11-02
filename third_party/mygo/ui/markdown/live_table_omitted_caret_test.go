package markdown

import (
	"image/color"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestLiveOmittedCellPaintsOnlyTheIntendedCaret(t *testing.T) {
	const source = "| A | B | C |\n|---|---|---|\n| x |\n"
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	s.SetSelection(strings.Index(source, "C"), strings.Index(source, "C"))
	s.Focus()
	view := ui.NewTester(func(c *ui.Context) {
		theme := ui.LightTheme()
		theme.Accent = ui.Hex("#ff00ff")
		c.SetTheme(theme)
		Editor(c, s, EditorOptions{InPlaceTables: true}).Fill()
	}, 700, 420)
	view.Frame()
	view.Key(0, ui.KeyDown)
	assertOnlyCaret := func() {
		t.Helper()
		caret, active := view.TextCaret()
		if !active || caret.H <= 0 {
			t.Fatal("intended cell has no native caret")
		}
		pixels, found := view.Image(), false
		for y := pixels.Bounds().Min.Y; y < pixels.Bounds().Max.Y; y++ {
			for x := pixels.Bounds().Min.X; x < pixels.Bounds().Max.X; x++ {
				p := color.RGBAModel.Convert(pixels.At(x, y)).(color.RGBA)
				if p.R != 255 || p.G != 0 || p.B != 255 {
					continue
				}
				if float32(x) < caret.X-1 || float32(x) > caret.X+max(caret.W, 1)+1 || float32(y) < caret.Y-1 || float32(y) > caret.Y+caret.H+1 {
					t.Fatalf("extra caret pixel outside the intended cell at %d,%d; native=%+v", x, y, caret)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("native caret has no actual painted pixels")
		}
	}
	assertOnlyCaret()
	view.Type("third")
	view.Command("undo")
	assertNavigationTableCell(t, s, 1, 2)
	assertOnlyCaret()
	view.Key(0, ui.KeyLeft)
	assertNavigationTableCell(t, s, 1, 1)
	assertOnlyCaret()
	if s.Buffer().OriginalText() != source {
		t.Fatal("caret painting changed the original row")
	}
}

func TestLiveOmittedCellEmptyNativeInputAndCancelledPreeditDoNotMaterialize(t *testing.T) {
	for _, operation := range []string{"empty-input", "cancelled-preedit"} {
		t.Run(operation, func(t *testing.T) {
			const source = "| A | B | C |\r\n|---|---|---|\r\n| x |\r\n"
			s, view := keyboardViewportTester(source, strings.Index(source, "C"), 960, 1, false, false)
			view.Key(0, ui.KeyDown)
			if operation == "empty-input" {
				s.client.ReplaceText(nil, "")
			} else {
				s.client.SetMarkedText(nil, "候选😀", ui.TextInputRange{Start: 4, End: 4})
				view.Frame()
				s.client.SetMarkedText(nil, "", ui.TextInputRange{})
				s.client.UnmarkText()
			}
			view.Frame()
			if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 || s.Composing() {
				t.Fatalf("empty native input materialized delimiters or kept preedit: %q version=%d composing=%v", s.Buffer().OriginalText(), s.Buffer().Version(), s.Composing())
			}
			assertNavigationTableCell(t, s, 1, 2)
			view.Type("after")
			if s.Buffer().OriginalText() != strings.Replace(source, "| x |", "| x |  | after |", 1) {
				t.Fatal("empty input lost the intended column")
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("empty input contaminated subsequent undo history")
			}
		})
	}
}
