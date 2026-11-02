package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNative002VisibleSelectionKeepsHiddenDelimiters(t *testing.T) {
	for _, tc := range []struct{ name, source, visible, word string }{
		{"bold", "A **bold** Z", "A bold Z", "bold"},
		{"link", "A [link](target) Z", "A link Z", "link"},
		{"code", "A `code` Z", "A code Z", "code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "# Active\n\n" + tc.source
			s, tt := liveTester(source, 0, EditorOptions{})
			var run *editorRun
			for _, r := range s.runs {
				if r.text == tc.visible {
					run = r
					break
				}
			}
			if run == nil || run.layout == nil {
				t.Fatal("rendered run absent")
			}
			from := run.layout.CaretAt(ui.TextCaretPosition{Index: 2})
			to := run.layout.CaretAt(ui.TextCaretPosition{Index: 2 + len(tc.word)})
			tt.Press(run.rect.X+from.X, run.rect.Y+from.Y+from.H/2)
			tt.Move(run.rect.X+to.X, run.rect.Y+to.Y+to.H/2)
			tt.Release(run.rect.X+to.X, run.rect.Y+to.Y+to.H/2)
			a, z := s.Selection()
			if got := s.Buffer().Slice(min(a, z), max(a, z)); got != tc.word {
				t.Fatalf("visible-only selection includes syntax: %q; offsets %d,%d", got, a, z)
			}
			tt.Type("X")
			if want := strings.Replace(source, tc.word, "X", 1); s.Buffer().String() != want {
				t.Fatalf("replacement damaged delimiters: %q want %q", s.Buffer().String(), want)
			}
		})
	}
}

func TestNative003CrossBlockCompositionShowsOneReplacement(t *testing.T) {
	source := "# First\n\nMiddle **text**\n\nLast"
	s, tt := liveTester(source, len(source), EditorOptions{})
	s.SetSelection(0, len(source))
	tt.Frame()
	tt.Compose("候选😀", 3)
	if !tt.HasText("候选😀") {
		t.Fatalf("cross-block preedit is hidden: %#v", tt.Texts())
	}
	if tt.HasText("First") || tt.HasText("Middle text") {
		t.Fatalf("selected blocks still rendered beneath preedit: %#v", tt.Texts())
	}
	if s.Buffer().String() != source {
		t.Fatal("preedit changed Buffer")
	}
	tt.Type("终")
	if s.Buffer().String() != "终" {
		t.Fatal("composition replacement failed")
	}
	tt.Command("undo")
	if s.Buffer().String() != source {
		t.Fatal("composition undo lost original source")
	}
}

func TestNative004PartialMarkedCommitKeepsCaretAtInsertion(t *testing.T) {
	s, tt := liveTester("az", 1, EditorOptions{})
	s.client.SetMarkedText(nil, "中文", ui.TextInputRange{Start: 2, End: 2})
	r := ui.TextInputRange{Start: 1, End: 2}
	s.client.ReplaceText(&r, "X")
	if s.Buffer().String() != "aX文z" {
		t.Fatalf("commit text %q", s.Buffer().String())
	}
	a, z := s.Selection()
	if a != 2 || z != 2 {
		t.Fatalf("caret jumped over retained preedit suffix: %d,%d want 2,2", a, z)
	}
	tt.Type("Y")
	if s.Buffer().String() != "aXY文z" {
		t.Fatalf("next input missed insertion point: %q", s.Buffer().String())
	}
}

func TestNative004ReplacementCaretIncludesUnchangedSuffix(t *testing.T) {
	s, tt := liveTester("abc", 0, EditorOptions{})
	s.SetSelection(0, 3)
	tt.Frame()
	tt.Type("xbc")
	if got := s.Buffer().OriginalText(); got != "xbc" {
		t.Fatalf("replacement text %q", got)
	}
	if a, z := s.Selection(); a != 3 || z != 3 {
		t.Fatalf("caret follows minimal diff instead of requested insertion: %d,%d want 3,3", a, z)
	}
	tt.Command("undo")
	if got := s.Buffer().OriginalText(); got != "abc" {
		t.Fatalf("undo text %q", got)
	}
	if a, z := s.Selection(); a != 0 || z != 3 {
		t.Fatalf("undo selection %d,%d want 0,3", a, z)
	}
	tt.Command("redo")
	if a, z := s.Selection(); a != 3 || z != 3 {
		t.Fatalf("redo caret %d,%d want 3,3", a, z)
	}
	tt.Type("Y")
	if got := s.Buffer().OriginalText(); got != "xbcY" {
		t.Fatalf("next input missed insertion point: %q", got)
	}
}

func TestNative004PartialMarkedCommitWithUnchangedSourceSuffix(t *testing.T) {
	s, tt := liveTester("a中文z", 1, EditorOptions{})
	s.SetSelection(1, len("a中文"))
	tt.Frame()
	s.client.SetMarkedText(nil, "中文", ui.TextInputRange{Start: 2, End: 2})
	r := ui.TextInputRange{Start: 1, End: 2}
	s.client.ReplaceText(&r, "X")
	if got := s.Buffer().OriginalText(); got != "aX文z" {
		t.Fatalf("commit text %q", got)
	}
	if a, z := s.Selection(); a != 2 || z != 2 {
		t.Fatalf("caret subtracts retained suffix from an already trimmed diff: %d,%d want 2,2", a, z)
	}
	tt.Command("undo")
	if got := s.Buffer().OriginalText(); got != "a中文z" {
		t.Fatalf("undo text %q", got)
	}
	if a, z := s.Selection(); a != 1 || z != len("a中文") {
		t.Fatalf("undo selection %d,%d want 1,7", a, z)
	}
	tt.Command("redo")
	if a, z := s.Selection(); a != 2 || z != 2 {
		t.Fatalf("redo caret %d,%d want 2,2", a, z)
	}
	tt.Type("Y")
	if got := s.Buffer().OriginalText(); got != "aXY文z" {
		t.Fatalf("next input missed insertion point: %q", got)
	}
}

func TestNative004RequestEndpointAcrossSourceProjection(t *testing.T) {
	for _, tc := range []struct {
		name, source, text, want string
		start, end, caret        int
	}{
		{"unchanged", "abc", "abc", "abc", 0, 3, 3},
		{"only prefix changes", "abc", "xbc", "xbc", 0, 3, 3},
		{"only suffix changes", "abc", "abx", "abx", 0, 3, 3},
		{"common prefix and suffix", "abcde", "abXde", "abXde", 0, 5, 5},
		{"UTF8 suffix", "中😀文", "新😀文", "新😀文", 0, len("中😀文"), len("新😀文")},
		{"LF joins preceding CR", "a\rb", "\n中", "a\r\n中b", 2, 2, len("a\n中")},
		{"CR joins following LF", "a\nb", "\r", "a\r\nb", 1, 1, 2},
		{"existing CRLF stays separate", "a\r\nb", "\nX\r", "a\r\n\nX\rb", 2, 2, 5},
		{"two bare newline edges join", "a\rX\nb", "\nY\r", "a\r\nY\r\nb", 2, 3, 4},
		{"delete joins newline edges", "a\rX\nb", "", "a\r\nb", 2, 3, 2},
		{"CRLF inside insertion", "ab", "X\r\nY", "aX\r\nYb", 1, 1, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tt := liveTester(tc.source, tc.start, EditorOptions{})
			s.SetSelection(tc.start, tc.end)
			tt.Frame()
			version := s.Buffer().Version()
			tt.Type(tc.text)
			if got := s.Buffer().OriginalText(); got != tc.want {
				t.Fatalf("source %q want %q", got, tc.want)
			}
			if a, z := s.Selection(); a != tc.caret || z != tc.caret {
				t.Fatalf("caret %d,%d want %d,%d", a, z, tc.caret, tc.caret)
			}
			if tc.source == tc.want {
				if s.Buffer().Version() != version || s.Buffer().CanUndo() {
					t.Fatal("same-source replacement created a history step")
				}
				return
			}
			tt.Command("undo")
			if got := s.Buffer().OriginalText(); got != tc.source {
				t.Fatalf("undo source %q want %q", got, tc.source)
			}
			if a, z := s.Selection(); a != tc.start || z != tc.end {
				t.Fatalf("undo selection %d,%d want %d,%d", a, z, tc.start, tc.end)
			}
			tt.Command("redo")
			if got := s.Buffer().OriginalText(); got != tc.want {
				t.Fatalf("redo source %q want %q", got, tc.want)
			}
			if a, z := s.Selection(); a != tc.caret || z != tc.caret {
				t.Fatalf("redo caret %d,%d want %d,%d", a, z, tc.caret, tc.caret)
			}
		})
	}
}

func TestNative004PartialMarkedCommitProjectsRawPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, source, preedit, inserted, want string
		start, end, caret                     int
	}{
		{"inserted CR before retained LF", "a\nxz", "\nx", "\r", "a\r\nxz", 1, 3, 2},
		{"inserted LF after source CR", "a\r文z", "文", "\nX", "a\r\nX文z", 2, 5, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tt := liveTester(tc.source, tc.start, EditorOptions{})
			s.SetSelection(tc.start, tc.end)
			tt.Frame()
			s.client.SetMarkedText(nil, tc.preedit, ui.TextInputRange{})
			marked, _ := s.client.MarkedRange()
			r := ui.TextInputRange{Start: marked.Start, End: marked.Start}
			s.client.ReplaceText(&r, tc.inserted)
			if got := s.Buffer().OriginalText(); got != tc.want {
				t.Fatalf("commit source %q want %q", got, tc.want)
			}
			if a, z := s.Selection(); a != tc.caret || z != tc.caret {
				t.Fatalf("caret %d,%d want %d,%d", a, z, tc.caret, tc.caret)
			}
			tt.Command("undo")
			if got := s.Buffer().OriginalText(); got != tc.source {
				t.Fatalf("undo source %q want %q", got, tc.source)
			}
			tt.Command("redo")
			if a, z := s.Selection(); a != tc.caret || z != tc.caret {
				t.Fatalf("redo caret %d,%d want %d,%d", a, z, tc.caret, tc.caret)
			}
		})
	}
}

func TestNative005EnterUsesBaselineLineEnding(t *testing.T) {
	for _, ending := range []string{"\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(ending, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			source := "first" + ending + "last"
			s, tt := liveTester(source, len("first"), EditorOptions{})
			tt.Key(0, ui.KeyEnter)
			want := "first" + ending + ending + "last"
			if got := s.Buffer().OriginalText(); got != want {
				t.Fatalf("Enter changed newline style: %q want %q", got, want)
			}
			tt.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("Enter undo failed")
			}
		})
	}
}

func TestNativeEndBeforeCommitIsOneUndoStep(t *testing.T) {
	s, tt := liveTester("az", 1, EditorOptions{})
	s.client.SetMarkedText(nil, "zhong", ui.TextInputRange{Start: 5, End: 5})
	// The platform adapter saves this range before UnmarkText, then supplies
	// it to the final ReplaceText callback for end-preedit/commit IMEs.
	r, _ := s.client.MarkedRange()
	s.client.UnmarkText()
	s.client.ReplaceText(&r, "中")
	if s.Buffer().String() != "a中z" {
		t.Fatalf("commit text %q", s.Buffer().String())
	}
	tt.Command("undo")
	if s.Buffer().String() != "az" {
		t.Fatalf("undo left intermediate preedit: %q", s.Buffer().String())
	}
}

func TestNativePrefixEndBeforeCommitKeepsActualRange(t *testing.T) {
	const header = "\ufeff---\nname: 名😀\n---"
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(ending, "\n", "LF"), func(t *testing.T) {
			source := strings.ReplaceAll(header, "\n", ending)
			opts := &Options{SourcePrefix: func(text string) int {
				if !strings.HasPrefix(text, header) {
					return 0
				}
				end := len(header)
				if len(text) > end && text[end] == '\n' {
					end++
				}
				return end
			}}
			s, view := liveTester(source, len(header), EditorOptions{Options: opts})
			s.client.SetMarkedText(nil, "中文😀", ui.TextInputRange{Start: 4, End: 4})
			marked, _ := s.client.MarkedRange()
			s.client.UnmarkText()
			s.client.ReplaceText(&marked, "上海😀")
			want := source + ending + "上海😀"
			if got := s.Buffer().OriginalText(); got != want {
				t.Fatalf("final commit %q, want %q", got, want)
			}
			view.Command("undo")
			if got := s.Buffer().OriginalText(); got != source {
				t.Fatalf("undo retained intermediate IME %q", got)
			}
			view.Command("redo")
			if got := s.Buffer().OriginalText(); got != want {
				t.Fatalf("redo %q", got)
			}
			s, _ = liveTester(source, len(header), EditorOptions{Options: opts})
			s.client.SetMarkedText(nil, "候选", ui.TextInputRange{Start: 2, End: 2})
			s.client.SetMarkedText(nil, "", ui.TextInputRange{})
			marked, _ = s.client.MarkedRange()
			s.client.UnmarkText()
			s.client.ReplaceText(&marked, "")
			if got := s.Buffer().OriginalText(); got != source || s.composing {
				t.Fatalf("cancelled IME wrote body or remained marked: %q", got)
			}
		})
	}
}

func TestNative002ReversedAndPrefixSelectionKeepSyntax(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to int
		want     string
	}{
		{"reverse bold", 6, 2, "bold"},
		{"prefix before bold", 0, 2, "A "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tt := liveTester("# Active\n\nA **bold** Z", 0, EditorOptions{})
			var r *editorRun
			for _, run := range s.runs {
				if run.text == "A bold Z" {
					r = run
					break
				}
			}
			if r == nil || r.layout == nil {
				t.Fatal("rendered run missing")
			}
			a := r.layout.CaretAt(ui.TextCaretPosition{Index: tc.from})
			z := r.layout.CaretAt(ui.TextCaretPosition{Index: tc.to})
			tt.Press(r.rect.X+a.X, r.rect.Y+a.Y+a.H/2)
			tt.Move(r.rect.X+z.X, r.rect.Y+z.Y+z.H/2)
			tt.Release(r.rect.X+z.X, r.rect.Y+z.Y+z.H/2)
			from, to := s.Selection()
			if got := s.Buffer().Slice(min(from, to), max(from, to)); got != tc.want {
				t.Fatalf("selected %q want %q", got, tc.want)
			}
		})
	}
}

func TestNative002ReadOnlyKeyboardSelectionKeepsSyntax(t *testing.T) {
	s, tt := liveTester("A **bold** Z", 4, EditorOptions{ReadOnly: true})
	for range 4 {
		tt.Key(ui.Shift, ui.KeyRight)
	}
	a, z := s.Selection()
	if got := s.Buffer().Slice(min(a, z), max(a, z)); got != "bold" {
		t.Fatalf("read-only key selection includes syntax: %q", got)
	}
	tt.Command("copy")
	if tt.Clipboard() != "bold" {
		t.Fatalf("copy %q", tt.Clipboard())
	}
	tt.Type("X")
	tt.Command("paste")
	tt.Command("cut")
	if s.Buffer().String() != "A **bold** Z" {
		t.Fatal("read-only input changed buffer")
	}
}
