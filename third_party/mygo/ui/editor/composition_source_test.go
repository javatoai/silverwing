package editor

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCompositionPreservesOutsideCR(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r")
	st := &State{Buffer: b}
	tt := ui.NewTester(func(c *ui.Context) { View(c, st).Fill().AutoFocus() }, 600, 400)
	st.Select(2, 2)
	tt.Frame()
	for _, s := range []string{"\n甲", "\n乙"} {
		tt.Compose(s, 2)
		if got := b.OriginalText(); got != "a\r"+s {
			t.Fatalf("composition %q: source = %q, want %q", s, got, "a\r"+s)
		}
	}
	tt.Type("终")
	if got := b.OriginalText(); got != "a\r终" {
		t.Fatalf("commit source = %q, want %q", got, "a\r终")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if got := b.OriginalText(); got != "a\r" {
		t.Fatalf("single undo source = %q, want %q", got, "a\r")
	}
}

func TestCompositionPreservesSourceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		at           int
		first, next  string
	}{
		{"trailing LF", "a\nb", 1, "甲\r", "乙\r"},
		{"both edges", "a\r\nb", 1, "\n甲\r", "\n乙\r"},
		{"mixed source", "a\r\nb", 1, "甲\r\nx\ny", "乙\nX\r\nY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBufferPreservingLineEndings(tc.source)
			st := &State{Buffer: b}
			tt := ui.NewTester(func(c *ui.Context) { View(c, st).Fill().AutoFocus() }, 600, 400)
			st.Select(tc.at, tc.at)
			tt.Frame()
			for i, s := range []string{tc.first, tc.next} {
				var target *ui.TextInputRange
				if i > 0 {
					r, ok := st.ed.MarkedRange()
					if !ok {
						t.Fatal("marked range missing")
					}
					target = &r
				}
				st.ed.SetMarkedText(target, s, ui.TextInputRange{Start: 1, End: 1})
				tt.Frame()
				want := tc.source[:tc.at] + s + tc.source[tc.at:]
				if got := b.OriginalText(); got != want {
					t.Fatalf("update %d source = %q, want %q", i, got, want)
				}
			}
			tt.Type("终")
			want := tc.source[:tc.at] + "终" + tc.source[tc.at:]
			if got := b.OriginalText(); got != want {
				t.Fatalf("commit source = %q, want %q", got, want)
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			if got := b.OriginalText(); got != tc.source {
				t.Fatalf("single undo source = %q, want %q", got, tc.source)
			}
			tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if got := b.OriginalText(); got != want {
				t.Fatalf("redo source = %q, want %q", got, want)
			}
		})
	}
}

func TestCompositionSourceMultipleCaretsAndCancel(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\rX\nb\rY")
	st := &State{Buffer: b}
	tt := ui.NewTester(func(c *ui.Context) { View(c, st).Fill().AutoFocus() }, 600, 400)
	st.SetSelections(Selection{Anchor: 2, Head: 2}, Selection{Anchor: 6, Head: 6})
	tt.Frame()
	for _, s := range []string{"\n甲", "\n乙"} {
		tt.Compose(s, 2)
		if got, want := b.OriginalText(), "a\r"+s+"X\nb\r"+s+"Y"; got != want {
			t.Fatalf("composition source = %q, want %q", got, want)
		}
	}
	tt.Type("终")
	if got, want := b.OriginalText(), "a\r终X\nb\r终Y"; got != want {
		t.Fatalf("commit source = %q, want %q", got, want)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if got, want := b.OriginalText(), "a\rX\nb\rY"; got != want {
		t.Fatalf("undo source = %q, want %q", got, want)
	}
	tt.Compose("\n甲", 2)
	st.ed.SetMarkedText(nil, "", ui.TextInputRange{})
	if got, want := b.OriginalText(), "a\rX\nb\rY"; got != want {
		t.Fatalf("cancel source = %q, want %q", got, want)
	}
}
