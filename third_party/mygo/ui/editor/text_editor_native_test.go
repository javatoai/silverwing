package editor

import (
	"image/color"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeCurrentLineGuidesRoundedAndInactiveSelectionPixels(t *testing.T) {
	theme := &Theme{Background: ui.Hex("#fcfcfd"), Text: ui.Hex("#2a2a33"), ActiveLine: ui.Hex("#eeeeF2"), Selection: ui.Hex("#80a8f0"), InactiveSelection: ui.Hex("#c8d8ee"), IndentGuide: ui.Hex("#dddde4"), ActiveIndentGuide: ui.Hex("#aaaab8")}
	state := &State{Buffer: NewBuffer("\t    word\n\t    second\n\t    third"), Font: ui.Font{Family: "monospace", Size: 13}, LineHeight: 20.0 / 13, Theme: theme}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { View(c, state).Grow(1).FillWidth().AutoFocus(); ui.Button(c, "outside") })
	}, 500, 240)
	rgba := func(c ui.Color) color.RGBA { return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A} }
	if got := tester.Image().RGBAAt(440, 10); got != rgba(theme.ActiveLine) {
		t.Fatalf("current line pixel=%v, want %v", got, rgba(theme.ActiveLine))
	}
	if got := tester.Image().RGBAAt(440, 30); got != rgba(theme.Background) {
		t.Fatalf("neighbor line pixel=%v, want neutral background", got)
	}
	ed := &state.ed
	guide := int(math.Round(float64(ed.textX + ed.view.indentGuideX(ed.view.line(2), 4))))
	if got := tester.Image().RGBAAt(guide, 45); got != rgba(theme.ActiveIndentGuide) {
		t.Fatalf("shaped indent guide pixel=%v at x=%d", got, guide)
	}
	start, end := len("\t    "), len("\t    word")
	state.Select(start, end)
	tester.Frame()
	line := ed.view.line(0)
	x := int(ed.textX + ed.view.xOf(line, &line.rows[0], line.toDisp(start, false)))
	if got := tester.Image().RGBAAt(x+4, 1); got != rgba(theme.Selection) {
		t.Fatalf("focused selection pixel=%v", got)
	}
	if got := tester.Image().RGBAAt(x, 0); got == rgba(theme.Selection) {
		t.Fatal("selection corner is square")
	}
	if err := tester.Click("outside"); err != nil {
		t.Fatal(err)
	}
	if got := tester.Image().RGBAAt(x+4, 1); got != rgba(theme.InactiveSelection) {
		t.Fatalf("inactive selection did not fade: %v", got)
	}
}

func TestSourcePreservingGeneratedNewlineAndMixedSource(t *testing.T) {
	for _, source := range []string{"甲\n乙\r\n尾😀", "甲\r\n乙\n尾😀", "甲\r乙\r\n尾😀", "甲\r乙\n尾😀", "甲😀"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(source, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			buffer := NewBufferPreservingLineEndings(source)
			state := &State{Buffer: buffer, NoAutoClose: true}
			tester := ui.NewTester(func(c *ui.Context) { View(c, state).Fill().AutoFocus() }, 600, 300)
			state.Select(buffer.Len(), buffer.Len())
			tester.Frame()
			tester.Key(0, ui.KeyEnter)
			want := source + firstSourceLineEnding(source)
			if got := buffer.OriginalText(); got != want {
				t.Fatalf("Enter source=%q, want %q", got, want)
			}
			tester.Type("新😀")
			if got := buffer.OriginalText(); got != want+"新😀" {
				t.Fatalf("Unicode source=%q", got)
			}
			tester.Key(ui.Cmd, ui.KeyZ)
			tester.Key(ui.Cmd, ui.KeyZ)
			if got := buffer.OriginalText(); got != source {
				t.Fatalf("undo source=%q, want %q", got, source)
			}
			state.Select(buffer.Len(), buffer.Len())
			state.Insert("\r\n贴\r上\n文")
			if got := buffer.OriginalText(); got != source+"\r\n贴\r上\n文" {
				t.Fatalf("paste normalized source: %q", got)
			}
		})
	}
}

func TestSourceLimitRejectsAtomicMulticaretAndComposition(t *testing.T) {
	const source = "甲\r\nx"
	buffer := NewBufferPreservingLineEndings(source)
	buffer.SetMaxSourceBytes(len(source) + 1)
	var composition []bool
	rejected := 0
	state := &State{Buffer: buffer, OnLimitExceeded: func() { rejected++ }, OnCompositionChanged: func(active bool) { composition = append(composition, active) }}
	tester := ui.NewTester(func(c *ui.Context) { View(c, state).Fill().AutoFocus() }, 600, 300)
	state.SetSelections(Selection{Anchor: 0, Head: 0}, Selection{Anchor: buffer.Len(), Head: buffer.Len()})
	tester.Frame()
	before := state.Selections()
	state.Insert("z")
	if buffer.OriginalText() != source || buffer.CanUndo() || !slices.Equal(state.Selections(), before) || rejected != 1 {
		t.Fatal("multicaret limit changed source, selection or history")
	}
	state.Select(buffer.Len(), buffer.Len())
	tester.Frame()
	tester.Compose("中文", 2)
	if state.Composing() || buffer.OriginalText() != source || buffer.CanUndo() || len(composition) != 0 {
		t.Fatal("rejected composition was partially applied")
	}
	buffer.SetMaxSourceBytes(len(source) + 3)
	tester.Compose("中", 1)
	if !state.Composing() {
		t.Fatal("composition start was not observable")
	}
	tester.Type("文")
	if state.Composing() || !slices.Equal(composition, []bool{true, false}) || buffer.OriginalText() != source+"文" {
		t.Fatalf("commit/callback source=%q transitions=%v", buffer.OriginalText(), composition)
	}
	tester.Key(ui.Cmd, ui.KeyZ)
	if buffer.OriginalText() != source {
		t.Fatal("IME commit was not one undo transaction")
	}
	zero := NewBufferPreservingLineEndings("")
	zero.SetMaxSourceBytes(0)
	zero.Edit(Edit{Text: "x"})
	if zero.Len() != 0 || zero.CanUndo() {
		t.Fatal("zero source capacity admitted an edit")
	}
}

func TestNativeIndentAndSelectionGeometryAtScales(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		t.Run(strings.ReplaceAll(fmtScale(scale), ".", "_"), func(t *testing.T) {
			state := &State{Buffer: NewBuffer("\t  \t中文😀" + strings.Repeat("长行x", 350) + "\n\n\t\tend"), Font: ui.Font{Family: "monospace", Size: 13}, LineHeight: 20.0 / 13, SoftWrap: true}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { View(c, state).Grow(1).FillWidth().AutoFocus(); ui.Button(c, "outside") })
			}, 560, 360)
			tester.SetScale(scale)
			ed := &state.ed
			line := ed.view.line(0)
			if len(line.rows) < 2 {
				t.Fatal("long Unicode line did not wrap")
			}
			for _, column := range []int{0, 4, 8} {
				got := ed.view.indentGuideX(line, column)
				want := ed.view.xOf(line, &line.rows[0], column)
				if math.Abs(float64(got-want)) > .01 {
					t.Fatalf("guide column %d x=%v, shaped x=%v", column, got, want)
				}
			}
			if math.Abs(float64(ed.view.lineHeight-20)) > .01 || !ed.o.indentGuides || !ed.o.activeLine {
				t.Fatal("native current line, indent guides or 20 DIP line height missing")
			}
			state.Select(len("\t  \t"), len("\t  \t中文😀"))
			tester.Frame()
			if !state.Run(Copy) || tester.Clipboard() != "中文😀" {
				t.Fatalf("Unicode copy=%q", tester.Clipboard())
			}
			selection := state.Selection()
			if err := tester.Click("outside"); err != nil {
				t.Fatal(err)
			}
			if state.Focused() || state.Selection() != selection {
				t.Fatal("blur discarded selection")
			}
			if err := tester.Click("Code editor"); err != nil {
				t.Fatal(err)
			}
			bounds, ok := tester.Find("Code editor")
			if !ok {
				t.Fatal("editor missing")
			}
			tester.Press(bounds.X+ed.textX+2, bounds.Y+5)
			tester.Move(bounds.X+ed.textX+100, bounds.Y+5)
			tester.Release(bounds.X+ed.textX+100, bounds.Y+5)
			if state.Selection().Anchor == state.Selection().Head {
				t.Fatal("native drag did not select text")
			}
		})
	}
}

func fmtScale(scale float32) string {
	switch scale {
	case 1.5:
		return "1.5"
	case 2:
		return "2"
	default:
		return "1"
	}
}
