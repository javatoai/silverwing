package markdown

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func stabilityRun(s *EditorState, text string) *editorRun {
	for _, r := range s.runs {
		if r.text == text {
			return r
		}
	}
	return nil
}

func TestStabilityListLiteralPrefixesAndEmptyItems(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll("1. first\n2. **second**\n3. \n4. \n3. undo\n4. \n6.\n7\n4. after\n\noutside", "\n", ending)
		s, tt := liveTester(source, len(strings.ReplaceAll(source, "\r\n", "\n")), EditorOptions{})
		version := s.Buffer().Version()
		for _, prefix := range []string{"1. ", "2. ", "3. ", "4. ", "6."} {
			r := stabilityRun(s, prefix)
			if r == nil || len(r.boundaries) < 2 || s.source[r.boundaries[0]:r.boundaries[len(r.boundaries)-1]] != prefix {
				t.Errorf("literal editable prefix %q absent; texts=%q", prefix, tt.Texts())
			}
		}
		if stabilityRun(s, "second") == nil {
			t.Fatal("inactive list body lost inline rendering")
		}
		first, _ := s.caretBounds(strings.Index(s.source, "first"))
		last, _ := s.caretBounds(strings.Index(s.source, "undo"))
		s.SetSelection(strings.Index(s.source, "first"), strings.Index(s.source, "first"))
		tt.Frame()
		firstActive, _ := s.caretBounds(strings.Index(s.source, "first"))
		lastActive, _ := s.caretBounds(strings.Index(s.source, "undo"))
		if math.Abs(float64(first.Y-firstActive.Y)) > .1 || math.Abs(float64(last.Y-lastActive.Y)) > .1 || math.Abs(float64(first.X-firstActive.X)) > .1 {
			t.Errorf("list geometry changed on activation: first=%+v/%+v last=%+v/%+v", first, firstActive, last, lastActive)
		}
		if stabilityRun(s, "second") == nil {
			t.Error("activating one list item expanded an unrelated formatted item")
		}
		if s.Buffer().OriginalText() != source || s.Buffer().Version() != version {
			t.Fatal("projection changed original source")
		}
	}
}

func TestStabilityEOFNewlineHasOneEmptyRow(t *testing.T) {
	for _, source := range []string{"body\n", "body\r\n", "body\n\n"} {
		s, _ := liveTester(source, 0, EditorOptions{})
		rows := 0
		for _, r := range s.runs {
			rows += len(r.layout.Lines())
		}
		if want := strings.Count(source, "\n") + 1; rows != want {
			t.Errorf("%q has %d visual rows, want %d", source, rows, want)
		}
	}
}

func TestStabilityQuoteHeadingAndSourceHeadingStyle(t *testing.T) {
	for _, prefix := range []string{"", "> "} {
		color := ui.Hex("#ab2345")
		theme := &Theme{}
		theme.Headings[0] = HeadingStyle{Size: 2, Weight: 700, Color: color, Rule: true}
		source := prefix + "# Title\n\noutside"
		s, tt := liveTester(source, len(source), EditorOptions{Options: &Options{Theme: theme}})
		inactive := stabilityRun(s, "Title")
		if inactive == nil {
			t.Fatal("inactive heading absent")
		}
		before := inactive.rect
		s.SetSelection(strings.Index(source, "Title"), strings.Index(source, "Title"))
		tt.Frame()
		var active *editorRun
		for _, r := range s.runs {
			if strings.Contains(r.text, "Title") {
				active = r
			}
		}
		if active == nil || active.spans[0].Color != color || active.spans[0].Size != s.view.th.FontSize*2 {
			t.Fatalf("%q active heading lost font/color: %+v", prefix, active)
		}
		if math.Abs(float64(before.Y-active.rect.Y)) > .1 || math.Abs(float64(before.H-active.rect.H)) > .1 {
			t.Errorf("%q heading glyph geometry changed: %+v -> %+v", prefix, before, active.rect)
		}
	}
}

func TestStabilityMixedImagesMapTextAfterUnicodeAlt(t *testing.T) {
	source := "# Active\n\nbefore ![甲😀](one) after ![乙😀](two) final"
	opts := &Options{Image: func(c *ui.Context, _, _, _ int, _, _ string) { ui.Box(c).Size(30, 20) }}
	s, tt := liveTester(source, 0, EditorOptions{Options: opts})
	for _, text := range []string{" after ", " final"} {
		r := stabilityRun(s, text)
		if r == nil {
			t.Fatalf("run %q absent; texts=%q", text, tt.Texts())
		}
		want := strings.Index(source, text)
		if r.boundaries[0] != want || r.boundaries[len(r.boundaries)-1] != want+len(text) {
			t.Errorf("%q source map=%v want [%d,%d]", text, r.boundaries, want, want+len(text))
		}
	}
}

func TestStabilityContainerPrefixPointerEditAndUndo(t *testing.T) {
	source := "# Active\r\n\r\n2. first\r\n4. \r\n6. last"
	s, tt := liveTester(source, 0, EditorOptions{})
	r := stabilityRun(s, "4. ")
	if r == nil {
		t.Fatal("empty item's source marker missing")
	}
	caret := r.layout.CaretAt(ui.TextCaretPosition{Index: 1})
	x, y := r.rect.X+caret.X, r.rect.Y+caret.Y+caret.H/2
	tt.Press(x, y)
	tt.Release(x, y)
	wantAt := strings.Index(s.source, "4. ") + 1
	if a, z := s.Selection(); a != wantAt || z != wantAt {
		t.Fatalf("prefix pointer selected %d,%d want %d", a, z, wantAt)
	}
	tt.Type("9")
	if got := s.Buffer().OriginalText(); got != strings.Replace(source, "4. ", "49. ", 1) {
		t.Fatalf("prefix edit reached another item: %q", got)
	}
	tt.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("prefix undo lost original CRLF source")
	}
}

func TestStabilityContainerCompositionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		start, end   int
	}{
		{"prefix", "2. first\n4. second", 1, 1},
		{"body start", "2. first\n4. second", 3, 3},
		{"body end", "2. first\n4. second", 8, 8},
		{"quote heading", "> # Title\n> body", 9, 9},
		{"two physical rows", "> first\n> second", 5, 13},
		{"two items", "2. first\n4. second", 5, 14},
		{"empty item", "2. first\n4. \n6. last", 12, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tt := liveTester(tc.source, tc.start, EditorOptions{})
			s.SetSelection(tc.start, tc.end)
			tt.Frame()
			version := s.Buffer().Version()
			tt.Compose("候选😀", 4)
			shown := 0
			for _, r := range s.runs {
				shown += strings.Count(r.text, "候选😀")
			}
			if shown != 1 {
				t.Fatalf("virtual preedit displayed %d times: %q", shown, tt.Texts())
			}
			if s.Buffer().OriginalText() != tc.source || s.Buffer().Version() != version {
				t.Fatal("preedit changed source")
			}
			if box, active := tt.TextCaret(); !active || box.H <= 0 {
				t.Fatalf("IME caret unavailable: %+v %v", box, active)
			}
			tt.Type("中😀")
			if got, want := s.Buffer().String(), tc.source[:tc.start]+"中😀"+tc.source[tc.end:]; got != want {
				t.Fatalf("commit %q want %q", got, want)
			}
			tt.Command("undo")
			if s.Buffer().OriginalText() != tc.source {
				t.Fatal("IME undo lost source")
			}
		})
	}
}

func TestStabilityNestedContainersRetainStylesAndSource(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{240, 700} {
			source := "> # Title\r\n> - **bold**\r\n>   continuation\r\n>   - [x] task\r\n>\r\n> end\r\n\r\noutside"
			s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
			s.SetSelection(len(s.source), len(s.source))
			s.Focus()
			tt := ui.NewTester(func(c *ui.Context) {
				if dark {
					c.SetTheme(ui.DarkTheme())
				} else {
					c.SetTheme(ui.LightTheme())
				}
				Editor(c, s).Fill()
			}, width, 600)
			buffer, version := s.Buffer(), s.Buffer().Version()
			bold := stabilityRun(s, "bold")
			if bold == nil || bold.spans[0].Weight == 0 || stabilityRun(s, ">   - [x] ") == nil {
				t.Fatalf("nested formatting/prefix lost: dark=%v width=%d texts=%q", dark, width, tt.Texts())
			}
			at := strings.Index(s.source, "continuation")
			s.SetSelection(at, at)
			tt.Frame()
			if stabilityRun(s, "Title") == nil || stabilityRun(s, "task") == nil {
				t.Fatal("activating nested paragraph expanded unrelated children")
			}
			s.SourceMode = true
			tt.Frame()
			s.SourceMode = false
			tt.Frame()
			if s.Buffer() != buffer || buffer.Version() != version || buffer.OriginalText() != source {
				t.Fatal("mode round trip changed nested source")
			}
		}
	}
}

func TestStabilityResourceResizePreservesViewportAndUserScroll(t *testing.T) {
	height := float32(40)
	source := "![image](asset)\n\n" + strings.Repeat("physical line\n", 60) + "\nlast"
	s, tt := liveTester(source, strings.Index(source, "physical line")+20*len("physical line\n"), EditorOptions{Options: &Options{
		Image: func(c *ui.Context, _, _, _ int, _, _ string) { ui.Box(c).Size(80, height) },
	}})
	tt.Frame()
	before, _ := tt.TextCaret()
	version := s.Buffer().Version()
	a, z := s.Selection()
	height = 240
	tt.Frame()
	tt.Frame()
	after, _ := tt.TextCaret()
	if math.Abs(float64(after.Y-before.Y)) > .1 {
		t.Fatalf("resource resize moved editing viewport: %+v -> %+v", before, after)
	}
	if s.Buffer().Version() != version || s.Buffer().OriginalText() != source {
		t.Fatal("resource resize changed source")
	}
	if aa, zz := s.Selection(); aa != a || zz != z {
		t.Fatal("resource resize changed selection")
	}
	height = 16
	tt.Frame()
	tt.Frame()
	afterShrink, _ := tt.TextCaret()
	if math.Abs(float64(afterShrink.Y-before.Y)) > .1 {
		t.Fatalf("resource shrink moved editing viewport: %+v -> %+v", before, afterShrink)
	}
	beforeScroll := s.scroll.Y
	s.scroll.Y += 40
	tt.Frame()
	tt.Frame()
	if math.Abs(float64(s.scroll.Y-beforeScroll-40)) > .1 {
		t.Fatalf("viewport anchoring undid explicit user scroll: %g -> %g", beforeScroll, s.scroll.Y)
	}
}

func TestStabilityResourceShrinkDistinguishesLayoutClampFromUserScroll(t *testing.T) {
	height := float32(360)
	source := "![image](asset)\n\n" + strings.Repeat("line\n", 30)
	s, tt := liveTester(source, len("![image](asset)\n\n")+20*len("line\n"), EditorOptions{Options: &Options{
		Image: func(c *ui.Context, _, _, _ int, _, _ string) { ui.Box(c).Size(80, height) },
	}})
	tt.Frame()
	before, _ := tt.TextCaret()
	oldScroll := s.scroll.Y
	height = 48
	tt.Frame()
	tt.Frame()
	after, _ := tt.TextCaret()
	if oldScroll <= s.scroll.MaxY {
		t.Fatal("test must exercise a layout-induced clamp of old scroll Y")
	}
	if math.Abs(float64(after.Y-before.Y)) > .1 {
		t.Fatalf("layout clamp defeated source anchor: %+v -> %+v", before, after)
	}
}

func TestStabilityVirtualEOFRowIsNotDuplicated(t *testing.T) {
	for _, tc := range []struct {
		source, preedit string
		start, end      int
		rows            int
	}{
		{"body", "候选\n", 4, 4, 2},
		{"body\n", "候选", 4, 5, 1},
		{"body\n", "候选\n", 4, 5, 2},
	} {
		s, tt := liveTester(tc.source, tc.start, EditorOptions{})
		s.SetSelection(tc.start, tc.end)
		tt.Frame()
		tt.Compose(tc.preedit, editorUnits(tc.preedit))
		rows, shown := 0, 0
		for _, r := range s.runs {
			rows += len(r.layout.Lines())
			shown += strings.Count(r.text, "候选")
		}
		if rows != tc.rows || shown != 1 {
			t.Errorf("source=%q preedit=%q rows=%d want=%d shown=%d texts=%q", tc.source, tc.preedit, rows, tc.rows, shown, tt.Texts())
		}
		if s.Buffer().OriginalText() != tc.source {
			t.Fatal("virtual EOF changed source")
		}
	}
}

func TestStabilityOrdinaryReadersKeepGeneratedListNumbers(t *testing.T) {
	for _, stream := range []bool{false, true} {
		tt := ui.NewTester(func(c *ui.Context) {
			opts := &Options{Reveal: &Reveal{Unit: AtOnce, Fade: -1}}
			if stream {
				Stream(c, "2. first\n9. second\n9. third", opts)
			} else {
				View(c, "2. first\n9. second\n9. third", opts)
			}
		}, 700, 500)
		if !tt.HasText("2.") || !tt.HasText("3.") || !tt.HasText("4.") || tt.HasText("9.") {
			t.Fatalf("ordinary reader numbering changed: stream=%v texts=%q", stream, tt.Texts())
		}
	}
}
