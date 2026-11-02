package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const codeGapTestLabel = "在代码块之间插入内容"

func codeGapNativeTester(source string, width int, zoom float32, sourceMode, readOnly bool, changed *int) (*EditorState, *ui.Tester) {
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	s.SourceMode = sourceMode
	opts := EditorOptions{InPlaceCode: true, InPlaceTables: true, RenderedLinks: true, ContinueBlocks: true, ReadOnly: readOnly,
		Options: &Options{Theme: &Theme{FontSize: 15, LineHeight: 1.6}}, OnChange: func() { *changed = *changed + 1 }}
	view := ui.NewTester(func(c *ui.Context) { Editor(c, s, opts).Fill() }, width, 420)
	view.SetZoom(zoom)
	at := strings.Index(s.source, "SELECT")
	s.SetSelection(at, at)
	s.Focus()
	view.Frame()
	return s, view
}

func codeGapSourceParts(ending, blank string) (string, string) {
	left := "\ufeff# 相邻代码\n\n```sql title=kept\nSELECT 1;\n```\n"
	right := "```python\nprint(2)\n```\n\n<!-- untouched 中文😀 -->\n"
	return strings.ReplaceAll(left, "\n", ending), blank + strings.ReplaceAll(right, "\n", ending)
}

func codeGapCardBounds(t *testing.T, view *ui.Tester) (ui.Rect, ui.Rect) {
	t.Helper()
	first, ok := view.Find("Code, sql")
	if !ok || first.H <= 0 {
		t.Fatal("SQL card has no actual visible card bounds")
	}
	second, ok := view.Find("Code, python")
	if !ok || second.H <= 0 {
		t.Fatal("Python card has no actual visible card bounds")
	}
	return first, second
}

func assertCodeGapBodies(t *testing.T, s *EditorState) {
	t.Helper()
	d := parser.Parse(s.source)
	var got []string
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.CodeBlock {
			if d.Block(i).Open {
				t.Fatal("gap editing opened a previously closed fence")
			}
			got = append(got, d.Block(i).Language()+"\n"+d.Lines(i))
		}
	}
	if len(got) != 2 || got[0] != "sql\nSELECT 1;\n" || got[1] != "python\nprint(2)\n" {
		t.Fatalf("gap editing changed fenced code bodies: %q", got)
	}
}

func TestLiveCodeGapHasEditableLineWithoutPassiveSourceChanges(t *testing.T) {
	for _, fixture := range []struct {
		name, ending, blank string
		width               int
		zoom                float32
	}{
		{"adjacent-lf", "\n", "", 420, 1},
		{"adjacent-crlf", "\r\n", "", 960, 1.5},
		{"real-blank-lf", "\n", "\n", 960, 1},
		{"real-blank-crlf", "\r\n", "\r\n", 420, 1.5},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			left, right := codeGapSourceParts(fixture.ending, fixture.blank)
			source, changes := left+right, 0
			s, view := codeGapNativeTester(source, fixture.width, fixture.zoom, false, false, &changes)
			buffer, version := s.Buffer(), s.Buffer().Version()
			first, second := codeGapCardBounds(t, view)
			minimum := s.view.th.FontSize * s.view.th.LineHeight * fixture.zoom
			if second.Y-first.Y-first.H < minimum-.1 {
				t.Fatalf("cards have no body-line space for editing between their borders: first=%+v second=%+v minimum=%g", first, second, minimum)
			}
			gap, ok := view.Find(codeGapTestLabel)
			if !ok || gap.H < minimum-.1 || gap.Y < first.Y+first.H-.1 || gap.Y+gap.H > second.Y+.1 {
				t.Fatalf("accessible insertion row is not between the cards: %+v found=%v", gap, ok)
			}
			if err := view.Click(codeGapTestLabel); err != nil {
				t.Fatal(err)
			}
			at := len(normalizedText(left))
			if s.anchor != at || s.caret != at || buffer.Version() != version || buffer.OriginalText() != source || s.Buffer() != buffer || changes != 0 {
				t.Fatal("layout/focus rewrote source or mapped the writing row into a hidden code fence")
			}
			caret, active := view.TextCaret()
			if !active || caret.H <= 0 || caret.Y < gap.Y-.1 || caret.Y+caret.H > gap.Y+gap.H+.1 {
				t.Fatalf("gap has no native caret in its actual writing row: %+v gap=%+v active=%v", caret, gap, active)
			}
			view.Frame()
			if buffer.Version() != version || changes != 0 || buffer.OriginalText() != source {
				t.Fatal("passive redraw materialized an otherwise virtual insertion row")
			}
			assertCodeGapBodies(t, s)
		})
	}
}

func TestLiveCodeGapTypingEnterIMEAndPasteAreOutsideFencesAndOneUndo(t *testing.T) {
	for _, fixture := range []struct{ name, ending, blank, operation, payload string }{
		{"type-virtual-lf", "\n", "", "type", "gap 中文😀"},
		{"type-real-crlf", "\r\n", "\r\n", "type", "gap 中文😀"},
		{"enter-virtual-crlf", "\r\n", "", "enter", ""},
		{"ime-virtual-crlf", "\r\n", "", "ime", "提交😀"},
		{"paste-real-lf", "\n", "\n", "paste", "粘贴😀\n\n第二段"},
		{"paste-virtual-crlf", "\r\n", "", "paste", "粘贴😀\r\n第二行"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			left, right := codeGapSourceParts(fixture.ending, fixture.blank)
			source, changes := left+right, 0
			s, view := codeGapNativeTester(source, 700, 1, false, false, &changes)
			buffer, version := s.Buffer(), s.Buffer().Version()
			// Also exercise the reported physical boundary. On the old layout
			// this is a real click at the touching borders, not a synthetic
			// source selection that bypasses the broken hit testing.
			first, second := codeGapCardBounds(t, view)
			if _, ok := view.Find(codeGapTestLabel); ok {
				if err := view.Click(codeGapTestLabel); err != nil {
					t.Fatal(err)
				}
			} else {
				view.ClickAt(first.X+30, (first.Y+first.H+second.Y)/2)
			}
			if buffer.Version() != version || buffer.OriginalText() != source || changes != 0 {
				t.Fatal("gap focus changed committed source")
			}
			switch fixture.operation {
			case "type":
				view.Type(fixture.payload)
			case "enter":
				view.Key(0, ui.KeyEnter)
			case "ime":
				view.Compose("候选😀", 4)
				if !s.Composing() || buffer.Version() != version || buffer.OriginalText() != source || changes != 0 || !view.HasText("候选😀") {
					t.Fatal("gap preedit committed source or disappeared")
				}
				codeGapCardBounds(t, view)
				shown := 0
				for _, run := range s.runs {
					shown += strings.Count(run.text, "候选😀")
				}
				if shown != 1 {
					t.Fatalf("gap preedit was rendered %d times instead of once", shown)
				}
				view.Type(fixture.payload)
			case "paste":
				view.SetClipboard(fixture.payload)
				view.Command("paste")
			}
			got := buffer.OriginalText()
			untouchedRight := strings.TrimPrefix(right, fixture.blank)
			if !strings.HasPrefix(got, left) || !strings.HasSuffix(got, untouchedRight) || s.Buffer() != buffer || buffer.Version() != version+1 || changes != 1 {
				t.Fatalf("gap edit was not one source-preserving Buffer transaction: %q version=%d changes=%d", got, buffer.Version(), changes)
			}
			middle := got[len(left) : len(got)-len(untouchedRight)]
			if strings.Trim(middle, "\r\n") != fixture.payload || !strings.HasSuffix(middle, fixture.ending) {
				t.Fatalf("gap did not contain the exact inserted payload and separator: %q", middle)
			}
			if strings.ContainsAny(strings.ReplaceAll(middle, fixture.ending, ""), "\r\n") {
				t.Fatal("materializing a gap introduced inconsistent line endings")
			}
			assertCodeGapBodies(t, s)
			if fixture.payload != "" {
				d := parser.Parse(s.source)
				outside := false
				for i := 1; i < d.Len(); i++ {
					b := d.Block(i)
					if b.Kind == parser.Paragraph && b.Start >= len(normalizedText(left)) && b.End <= strings.Index(s.source, "```python") {
						outside = true
					}
				}
				if !outside {
					t.Fatal("typed gap content did not become an independent outside-code paragraph")
				}
			}
			if s.anchor != s.caret {
				t.Fatal("gap edit lost its collapsed real source caret")
			}
			keyboardViewportCaret(t, s, view, s.caret)
			view.Command("undo")
			if buffer.OriginalText() != source || s.Buffer() != buffer {
				t.Fatal("one gap undo did not restore original BOM, fences, EOL and untouched content")
			}
			assertCodeGapBodies(t, s)
			view.Command("redo")
			if buffer.OriginalText() != got {
				t.Fatal("gap redo did not reuse the same source history")
			}
			assertCodeGapBodies(t, s)
		})
	}
}

func TestLiveCodeGapArrowNavigationUsesTheWritingRow(t *testing.T) {
	left, right := codeGapSourceParts("\n", "")
	source, changes := left+right, 0
	s, view := codeGapNativeTester(source, 700, 1, false, false, &changes)
	end := strings.Index(s.source, "SELECT 1;") + len("SELECT 1;")
	s.SetSelection(end, end)
	view.Frame()
	view.Key(0, ui.KeyDown)
	at := len(left)
	if s.anchor != at || s.caret != at {
		t.Fatal("Down at the code body's final visual row skipped/stuck before the insertion row")
	}
	gap, ok := view.Find(codeGapTestLabel)
	caret, active := view.TextCaret()
	if !ok || !active || caret.Y < gap.Y-.1 || caret.Y+caret.H > gap.Y+gap.H+.1 {
		t.Fatal("keyboard gap navigation has no actual native caret in the gap")
	}
	view.Key(0, ui.KeyDown)
	start := strings.Index(s.source, "print(2)")
	if s.anchor != s.caret || s.caret < start || s.caret > start+len("print(2)") {
		t.Fatal("Down from the insertion row did not enter the next code body's first line")
	}
	view.Key(0, ui.KeyUp)
	if s.anchor != at || s.caret != at {
		t.Fatal("Up from the next code card did not return to the writing row")
	}
	view.Key(0, ui.KeyUp)
	start = strings.Index(s.source, "SELECT 1;")
	if s.anchor != s.caret || s.caret < start || s.caret > end {
		t.Fatal("Up from the insertion row did not enter the previous code body")
	}
	if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 || changes != 0 {
		t.Fatal("arrow-key navigation materialized a virtual gap without explicit editing")
	}
}

func TestLiveCodeGapSourceModeAndReadOnlyKeepOriginalContracts(t *testing.T) {
	left, right := codeGapSourceParts("\r\n", "")
	source, changes := left+right, 0
	s, view := codeGapNativeTester(source, 700, 1, true, false, &changes)
	if _, found := view.Find(codeGapTestLabel); found {
		t.Fatal("source mode exposed a virtual Markdown insertion affordance")
	}
	at := len(normalizedText(left))
	s.SetSelection(at, at)
	view.Frame()
	view.Type("literal")
	if s.Buffer().OriginalText() != left+"literal"+right || changes != 1 {
		t.Fatal("source-mode insertion added automatic gap separators or moved into a code body")
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("source-mode literal insertion lost its original undo history")
	}
	changes = 0
	s, view = codeGapNativeTester(source, 700, 1, false, true, &changes)
	first, second := codeGapCardBounds(t, view)
	if second.Y-first.Y-first.H < s.view.th.FontSize*s.view.th.LineHeight-.1 {
		t.Fatal("read-only neighboring cards lost their visual separation")
	}
	view.ClickAt(first.X+30, (first.Y+first.H+second.Y)/2)
	view.Type("不能写")
	view.Key(0, ui.KeyEnter)
	if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source || changes != 0 {
		t.Fatal("read-only gap materialized source or accepted text")
	}
	assertCodeGapBodies(t, s)
	legacy, legacyView := liveTester(source, 0, EditorOptions{})
	if _, found := legacyView.Find(codeGapTestLabel); found || legacy.Buffer().Version() != 0 || legacy.Buffer().OriginalText() != source {
		t.Fatal("SDK default options acquired an in-place virtual writing row")
	}
}

func TestLiveCodeGapThreeCardsRecomputeAndUndoRestoresWritingIntent(t *testing.T) {
	left, right := codeGapSourceParts("\r\n", "")
	marker := "\r\n<!-- untouched 中文😀 -->\r\n"
	right = strings.TrimSuffix(right, marker)
	javascript := "```javascript\r\nconsole.log(3);\r\n```\r\n"
	source, changes := left+right+javascript+marker, 0
	s, view := codeGapNativeTester(source, 700, 1, false, false, &changes)
	buffer := s.Buffer()
	if len(s.codeGaps) != 2 {
		t.Fatal("three adjacent cards did not expose both independent source-owned writing rows")
	}
	if err := view.Click(codeGapTestLabel); err != nil {
		t.Fatal(err)
	}
	view.Type("first-gap")
	first := buffer.OriginalText()
	if changes != 1 || len(s.codeGaps) != 1 {
		t.Fatal("typing in the first gap did not recompute the remaining gap from current source")
	}
	if err := view.Click(codeGapTestLabel); err != nil {
		t.Fatal(err)
	}
	view.Type("second-gap")
	second := buffer.OriginalText()
	if changes != 2 || strings.Count(second, "first-gap") != 1 || strings.Count(second, "second-gap") != 1 || strings.Index(second, "second-gap") < strings.Index(second, "print(2)") || strings.Index(second, "second-gap") > strings.Index(second, "```javascript") {
		t.Fatalf("the second writing row used the stale first-gap source position: %q", second)
	}
	assertBodies := func() {
		t.Helper()
		d := parser.Parse(s.source)
		var bodies []string
		for i := 1; i < d.Len(); i++ {
			if d.Block(i).Kind == parser.CodeBlock {
				bodies = append(bodies, d.Block(i).Language()+"\n"+d.Lines(i))
			}
		}
		if len(bodies) != 3 || bodies[0] != "sql\nSELECT 1;\n" || bodies[1] != "python\nprint(2)\n" || bodies[2] != "javascript\nconsole.log(3);\n" || s.Buffer() != buffer || !strings.HasPrefix(buffer.OriginalText(), left) || !strings.HasSuffix(buffer.OriginalText(), javascript+marker) {
			t.Fatal("multi-gap writing rewrote a neighboring fence, code body, BOM, EOL or untouched comment")
		}
	}
	assertBodies()
	view.Command("undo")
	if buffer.OriginalText() != first {
		t.Fatal("one second-gap undo did not restore the exact first-gap result")
	}
	view.Command("undo")
	if buffer.OriginalText() != source {
		t.Fatal("one first-gap undo did not restore all original bytes")
	}
	// No re-click or programmatic selection: the restored cursor must still
	// mean the virtual writing row, rather than the following hidden opener.
	view.Type("undo-input")
	if !strings.Contains(buffer.OriginalText(), "undo-input") || strings.Index(buffer.OriginalText(), "undo-input") > strings.Index(buffer.OriginalText(), "```python") {
		t.Fatal("typing after undo lost the restored gap's source intent")
	}
	assertBodies()
}

func TestLiveCodeGapPureReaderRetainsSharedBlockSpacing(t *testing.T) {
	left, right := codeGapSourceParts("\n", "")
	source := left + right
	opts := &Options{Theme: &Theme{FontSize: 15, LineHeight: 1.6}}
	view := ui.NewTester(func(c *ui.Context) { View(c, source, opts).FillWidth() }, 700, 420)
	first, second := codeGapCardBounds(t, view)
	if second.Y-first.Y-first.H <= .1 {
		t.Fatal("shared native reader rendered touching code card borders")
	}
	if _, found := view.Find(codeGapTestLabel); found {
		t.Fatal("a pure reader acquired a writing affordance or editable Buffer")
	}
}

func TestLiveCodeGapSourceLimitRejectsTheWholeInsertion(t *testing.T) {
	left, right := codeGapSourceParts("\r\n", "")
	source, changes := left+right, 0
	s, view := codeGapNativeTester(source, 700, 1, false, false, &changes)
	if err := view.Click(codeGapTestLabel); err != nil {
		t.Fatal(err)
	}
	at := len(normalizedText(left))
	// The payload fits on its own, but a safe outside-fence insertion also
	// needs a separator. Reject the whole operation rather than truncating it.
	const payload = "界"
	s.Buffer().SetMaxSourceBytes(len(source) + len(payload))
	view.Type(payload)
	if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 || changes != 0 || s.anchor != at || s.caret != at {
		t.Fatal("rejected gap insertion changed source, selection or history")
	}
	assertCodeGapBodies(t, s)
	s.Buffer().SetMaxSourceBytes(len(source) + len(payload) + 2)
	view.Type(payload)
	if s.Buffer().OriginalText() != left+payload+"\r\n"+right || changes != 1 {
		t.Fatal("rejected insertion lost the virtual row's focus for a later valid edit")
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("a rejected insertion contaminated the next successful undo step")
	}
}
