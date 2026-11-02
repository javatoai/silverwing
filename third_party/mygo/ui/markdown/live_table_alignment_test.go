package markdown

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func alignedTableTester(source string) (*EditorState, *ui.Tester) {
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	view := ui.NewTester(func(c *ui.Context) {
		theme := ui.LightTheme()
		theme.Accent = ui.Hex("#ff00ff")
		c.SetTheme(theme)
		Editor(c, s, EditorOptions{InPlaceTables: true, RenderedLinks: true, Options: &Options{Theme: &Theme{FontSize: 15, LineHeight: 1.6}}}).Fill()
	}, 800, 420)
	s.Focus()
	view.Frame()
	return s, view
}

func tableCellBounds(t *testing.T, s *EditorState, row, column int) ui.Rect {
	t.Helper()
	for _, hit := range s.tableCells {
		if hit.cell.row == row && hit.cell.column == column {
			return hit.rect
		}
	}
	t.Fatal("missing actual table cell bounds")
	return ui.Rect{}
}

func TestLiveTableAlignedCaretHitAndSelectionMatchVisibleGlyphs(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		logical := "\ufeff| Left | Middle | Right |\n|:---|:---:|---:|\n| L | MC | 12345 |\n\nuntouched\n"
		source := strings.ReplaceAll(logical, "\n", ending)
		s, view := alignedTableTester(source)
		for column, label := range []string{"L | MC", "MC | 12345", "12345"} {
			at := strings.Index(logical, label)
			length := 1
			if column == 1 {
				length = 2
			}
			if column == 2 {
				length = 5
			}
			s.SetSelection(at+length, at+length)
			view.Frame()
			caret, active := view.TextCaret()
			cell := tableCellBounds(t, s, 1, column)
			relative := (caret.X - cell.X) / cell.W
			if !active || column == 0 && relative > .35 || column == 1 && (relative < .4 || relative > .7) || column == 2 && relative < .75 {
				t.Fatalf("cell %d caret disagrees with its visible alignment: caret=%+v cell=%+v", column, caret, cell)
			}
		}
		cell := tableCellBounds(t, s, 1, 2)
		at := strings.Index(logical, "12345")
		// Find the actual painted glyphs, independently of the editor's own
		// selection layout. The row contains no other text in this cell.
		pixels := view.Image()
		minX, minY, maxY := pixels.Bounds().Max.X, pixels.Bounds().Max.Y, 0
		for y := int(cell.Y) + 2; y < int(cell.Y+cell.H)-2; y++ {
			for x := int(cell.X) + 2; x < int(cell.X+cell.W)-2; x++ {
				p := color.RGBAModel.Convert(pixels.At(x, y)).(color.RGBA)
				if p.R < 80 && p.G < 80 && p.B < 80 {
					minX = min(minX, x)
					minY = min(minY, y)
					maxY = max(maxY, y)
				}
			}
		}
		if minX == pixels.Bounds().Max.X || maxY < minY {
			t.Fatal("right-aligned actual glyph pixels missing")
		}
		view.ClickAt(float32(minX+1), float32(minY+maxY)/2)
		if a, z := s.Selection(); a != z || z < at || z > at+1 {
			t.Fatalf("clicking actual first right-aligned digit lands elsewhere: %d:%d want%d..%d", a, z, at, at+1)
		}
		s.SetSelection(at, at+5)
		view.Frame()
		pixels = view.Image()
		count, sumX := 0, 0
		for y := int(cell.Y) + 2; y < int(cell.Y+cell.H)-2; y++ {
			for x := int(cell.X) + 2; x < int(cell.X+cell.W)-2; x++ {
				p := color.RGBAModel.Convert(pixels.At(x, y)).(color.RGBA)
				if int(p.R)-int(p.G) > 35 && int(p.B)-int(p.G) > 35 {
					count++
					sumX += x
				}
			}
		}
		if count == 0 || float32(sumX)/float32(count) < float32(minX)-1 {
			t.Fatal("selection highlight is not under the actual right-aligned glyphs")
		}
		view.Type("replacement😀")
		want := strings.Replace(source, "12345", "replacement😀", 1)
		if s.Buffer().OriginalText() != want || s.Buffer().Version() != 1 {
			t.Fatal("aligned edit changed bytes beyond the selected cell")
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("aligned edit undo lost BOM/newlines/unrelated source")
		}
	}
}

func TestLiveAlignedEmptyAndOmittedCellsKeepIMEAndCaretGeometry(t *testing.T) {
	const source = "| First | Middle | Right |\n|---|:---:|---:|\n| left |  |  |\n| omitted |\n"
	s, view := alignedTableTester(source)
	for _, column := range []int{1, 2} {
		cell := navigationTableCell(t, s, 1, column)
		s.SetSelection(cell.start, cell.start)
		view.Frame()
		caret, active := view.TextCaret()
		box := tableCellBounds(t, s, 1, column)
		if !active || column == 1 && math.Abs(float64(caret.X-(box.X+box.W/2))) > 15 || column == 2 && caret.X < box.X+box.W*.75 {
			t.Fatalf("empty aligned cell caret is misplaced: %+v cell=%+v", caret, box)
		}
	}
	view.Compose("候选😀", 4)
	caret, active := view.TextCaret()
	box := tableCellBounds(t, s, 1, 2)
	if !active || caret.X < box.X+box.W*.6 || !view.HasText("候选😀") || s.Buffer().Version() != 0 {
		t.Fatal("right-aligned preedit has wrong geometry or commits source")
	}
	view.Type("提交😀")
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("aligned IME undo changed original source")
	}
	at := strings.Index(source, "Right")
	s.SetSelection(at, at)
	view.Frame()
	view.Key(0, ui.KeyDown)
	view.Key(0, ui.KeyDown)
	assertNavigationTableCell(t, s, 2, 2)
	caret, active = view.TextCaret()
	box = tableCellBounds(t, s, 2, 2)
	if !active || caret.X < box.X+box.W*.75 || s.Buffer().OriginalText() != source {
		t.Fatal("omitted aligned cell lost column geometry or materialized source")
	}
}
