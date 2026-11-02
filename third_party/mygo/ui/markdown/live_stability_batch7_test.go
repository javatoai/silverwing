package markdown

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStabilityMarkerGapCompositionExactProjectionAndUndo(t *testing.T) {
	for _, source := range []string{"6.\n7\n8. x", "6.\n   7\n8. x", "6.\n\n   body\n8. x"} {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(source+fmt.Sprintf("/%q", ending), func(t *testing.T) {
				original := strings.ReplaceAll(source, "\n", ending)
				start, end := 1, strings.Index(source, "\n8. x")
				s, tt := liveTester(original, start, EditorOptions{})
				s.SetSelection(start, end)
				tt.Frame()
				version := s.Buffer().Version()
				tt.Compose("候选😀", 4)
				shown := 0
				for _, r := range s.runs {
					shown += strings.Count(r.text, "候选😀")
				}
				wantRun := "6候选😀"
				if source != "6.\n   7\n8. x" {
					// These controls end the preceding list. 8. cannot interrupt
					// their ordinary paragraph, so the frozen run includes it.
					wantRun += "\n8. x"
				}
				if shown != 1 || stabilityRun(s, wantRun) == nil {
					t.Fatalf("gap preedit must keep unchanged prefix in one exact row: count=%d texts=%q", shown, tt.Texts())
				}
				if s.virtualText() != "6候选😀\n8. x" || s.Buffer().OriginalText() != original || s.Buffer().Version() != version {
					t.Fatal("virtual replacement changed committed source or lost prefix/suffix")
				}
				tt.Type("中😀")
				want := strings.ReplaceAll(source[:start]+"中😀"+source[end:], "\n", ending)
				if s.Buffer().OriginalText() != want {
					t.Fatalf("commit=%q want=%q", s.Buffer().OriginalText(), want)
				}
				tt.Command("undo")
				if a, z := s.Selection(); s.Buffer().OriginalText() != original || a != start || z != end {
					t.Fatalf("undo source/selection not restored: source=%q selection=%d,%d", s.Buffer().OriginalText(), a, z)
				}
			})
		}
	}
}

func TestStabilityComplexChildrenKeepEditableLiteralPrefixes(t *testing.T) {
	for _, tc := range []struct{ name, source, prefix, native string }{
		{"code", "2. ```go\n   x\n   ```\n\noutside", "2. ", "native code"},
		{"image", "2. ![a](asset)\n\noutside", "2. ", "native image"},
		{"quote code", "> 2. ```go\n>    x\n>    ```\n\noutside", "> 2. ", "native code"},
		{"nested image", "- outer\n\n  2. ![a](asset)\n\noutside", "  2. ", "native image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := &Options{
				Image: func(c *ui.Context, _, _, _ int, _, _ string) { ui.Text(c, "native image") },
				Code:  map[string]func(*ui.Context, *CodeBlockView){"go": func(c *ui.Context, _ *CodeBlockView) { ui.Text(c, "native code") }},
			}
			s, tt := liveTester(tc.source, len(tc.source), EditorOptions{Options: opts})
			r := stabilityRun(s, tc.prefix)
			if r == nil || !tt.HasText(tc.native) {
				t.Fatalf("complex child lost mapped prefix/native renderer: %q", tt.Texts())
			}
			if got := s.source[r.boundaries[0]:r.boundaries[len(r.boundaries)-1]]; got != tc.prefix {
				t.Fatalf("prefix map=%q want=%q", got, tc.prefix)
			}
			at := strings.Index(tc.prefix, "2") + 1
			caret := r.layout.CaretAt(ui.TextCaretPosition{Index: at})
			x, y := r.rect.X+caret.X, r.rect.Y+caret.Y+caret.H/2
			tt.Press(x, y)
			tt.Release(x, y)
			wantAt := strings.Index(tc.source, tc.prefix) + at
			if a, z := s.Selection(); a != wantAt || z != wantAt {
				t.Fatalf("complex prefix click=%d,%d want=%d", a, z, wantAt)
			}
			count := 0
			for _, run := range s.runs {
				count += strings.Count(run.text, tc.prefix)
			}
			if count != 1 || tt.HasText(tc.native) {
				t.Fatalf("source expansion duplicated prefix or native child: count=%d texts=%q", count, tt.Texts())
			}
			tt.Type("9")
			want := tc.source[:wantAt] + "9" + tc.source[wantAt:]
			if s.Buffer().OriginalText() != want {
				t.Fatalf("complex prefix edit=%q want=%q", s.Buffer().OriginalText(), want)
			}
			tt.Command("undo")
			if s.Buffer().OriginalText() != tc.source {
				t.Fatal("complex prefix undo lost source")
			}
		})
	}
}

var stabilityVirtualTextSink string

func TestStabilityVirtualTextReusesUnchangedCompositionAndInvalidates(t *testing.T) {
	s, tt := liveTester(strings.Repeat("1. item\n", 500), 3, EditorOptions{})
	tt.Compose("候选", 2)
	want := s.source[:3] + "候选" + s.source[3:]
	if s.virtualText() != want {
		t.Fatal("initial virtual text incorrect")
	}
	if allocations := testing.AllocsPerRun(10, func() {
		for range 16 {
			stabilityVirtualTextSink = s.virtualText()
		}
	}); allocations != 0 {
		t.Fatalf("unchanged composition re-copied the full source: %g allocations", allocations)
	}
	tt.Compose("新😀", 3)
	if got, want := s.virtualText(), s.source[:3]+"新😀"+s.source[3:]; got != want {
		t.Fatalf("composition update reused stale virtual text: %q", got[:min(len(got), 40)])
	}
	r := ui.TextInputRange{Start: 0, End: 2}
	s.client.SetMarkedText(&r, "X", ui.TextInputRange{Start: 1, End: 1})
	virtual := s.source[:s.composeStart] + s.compose + s.source[s.composeEnd:]
	if s.virtualText() != virtual {
		t.Fatal("composition range update reused stale virtual text")
	}
	s.EndComposition()
	if s.virtualText() != s.Buffer().String() {
		t.Fatal("composition commit retained stale preedit")
	}
	tt.Command("undo")
	if s.virtualText() != s.Buffer().String() {
		t.Fatal("source undo retained stale virtual text")
	}
}

func BenchmarkStabilityCompositionRows(b *testing.B) {
	for _, rows := range []int{500, 1000, 2000} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			source := strings.Repeat("1. item\n", rows)
			_, tt := liveTester(source, len(source)/2+3, EditorOptions{})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%2 == 0 {
					tt.Compose("候选", 2)
				} else {
					tt.Compose("候選", 2)
				}
			}
		})
	}
}

func TestStabilityStableCompositionResourceResize(t *testing.T) {
	height := float32(72)
	source := "![image](asset)\n\n" + strings.Repeat("physical line\n", 45)
	s, tt := liveTester(source, len("![image](asset)\n\n")+20*len("physical line\n"), EditorOptions{Options: &Options{
		Image: func(c *ui.Context, _, _, _ int, _, _ string) { ui.Box(c).Size(80, height) },
	}})
	tt.Compose("候选", 2)
	tt.Frame()
	before, _ := tt.TextCaret()
	version := s.Buffer().Version()
	for _, size := range []float32{360, 48} {
		height = size
		tt.Frame()
		tt.Frame()
		after, active := tt.TextCaret()
		if !active || math.Abs(float64(before.Y-after.Y)) > .1 || math.Abs(float64(before.X-after.X)) > .1 {
			t.Fatalf("stable preedit viewport moved for height %g: %+v -> %+v", size, before, after)
		}
		if !s.Composing() || s.Buffer().Version() != version || s.Buffer().OriginalText() != source {
			t.Fatal("resize committed or changed preedit/source")
		}
	}
	oldScroll := s.scroll.Y
	s.scroll.Y += 20
	tt.Frame()
	tt.Frame()
	if math.Abs(float64(s.scroll.Y-oldScroll-20)) > .1 {
		t.Fatal("composition anchor undid explicit scrolling")
	}
	tt.Compose("新候选\n第二行", 7)
	tt.Frame()
	if s.virtualText() != s.source[:s.composeStart]+"新候选\n第二行"+s.source[s.composeEnd:] {
		t.Fatal("composition update retained old virtual text")
	}
	if box, active := tt.TextCaret(); !active || box.H <= 0 || box.Y+box.H > s.rect.Y+s.rect.H {
		t.Fatalf("updated composition did not retain normal caret reveal: %+v", box)
	}
}

func TestStabilityEmptyDocumentCompositionVisibleAndUndoable(t *testing.T) {
	for _, sourceMode := range []bool{false, true} {
		for _, preedit := range []string{"候选😀", "候选😀\n"} {
			t.Run(fmt.Sprintf("source=%v/preedit=%q", sourceMode, preedit), func(t *testing.T) {
				s, tt := liveTester("", 0, EditorOptions{})
				s.SourceMode = sourceMode
				tt.Frame()
				version := s.Buffer().Version()
				tt.Compose(preedit, editorUnits(preedit))
				shown, rows := 0, 0
				for _, r := range s.runs {
					shown += strings.Count(r.text, "候选😀")
					rows += len(r.layout.Lines())
				}
				if shown != 1 || rows != strings.Count(preedit, "\n")+1 || stabilityRun(s, preedit) == nil {
					t.Fatalf("empty-document preedit hidden/duplicated: shown=%d rows=%d texts=%q", shown, rows, tt.Texts())
				}
				if !s.Composing() || s.Buffer().Version() != version || s.Buffer().OriginalText() != "" {
					t.Fatal("empty-document preedit changed committed source")
				}
				if box, active := tt.TextCaret(); !active || box.H <= 0 {
					t.Fatalf("empty-document candidate caret unavailable: %+v", box)
				}
				tt.Type("中😀")
				if s.Composing() || s.Buffer().OriginalText() != "中😀" {
					t.Fatal("empty-document composition did not commit exactly once")
				}
				tt.Command("undo")
				if a, z := s.Selection(); s.Buffer().OriginalText() != "" || a != 0 || z != 0 {
					t.Fatalf("empty-document undo source/selection=%q %d,%d", s.Buffer().OriginalText(), a, z)
				}
			})
		}
	}
}
