package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func taskNativeTester(source string, readOnly, enabled bool) (*EditorState, *ui.Tester) {
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	view := ui.NewTester(func(c *ui.Context) {
		Editor(c, s, EditorOptions{InteractiveTasks: enabled, ReadOnly: readOnly, ContinueBlocks: true, InPlaceTables: true, InPlaceCode: true}).Fill()
	}, 700, 420)
	s.SetSelection(len(s.source), len(s.source))
	s.Focus()
	view.Frame()
	return s, view
}

func taskMarkerPoint(t *testing.T, s *EditorState, mark int) (float32, float32) {
	t.Helper()
	for _, run := range s.runs {
		if run.layout == nil || mark < run.caretStart || mark+3 > run.caretEnd {
			continue
		}
		boxes := run.layout.SelectionRects(ui.TextInputRange{Start: run.unitForSource(mark), End: run.unitForSource(mark + 3)})
		if len(boxes) > 0 {
			b := boxes[0]
			return run.rect.X - s.rect.X + b.X + b.W/2, run.rect.Y - s.rect.Y + b.Y + b.H/2
		}
	}
	t.Fatal("task marker has no actual source-mapped glyph bounds")
	return 0, 0
}

func TestLiveTaskClickChangesOnlyOneMarkerAndPreservesHistory(t *testing.T) {
	for _, logical := range []string{"\ufeff- [ ] 中文😀\n\nuntouched\n", "- [X] 已完成\n\nuntouched\n", "> 2. [x] 有序引用\n\nuntouched\n", "- 外层\n  - [ ] 嵌套\n\nuntouched\n"} {
		for _, ending := range []string{"\n", "\r\n"} {
			source := strings.ReplaceAll(logical, "\n", ending)
			s, view := taskNativeTester(source, false, true)
			mark := strings.Index(s.source, "[")
			x, y := taskMarkerPoint(t, s, mark)
			a, z := s.Selection()
			view.ClickAt(x, y)
			old := strings.Index(source, "[") + 1
			replacement := "x"
			if source[old] != ' ' {
				replacement = " "
			}
			want := source[:old] + replacement + source[old+1:]
			if s.Buffer().OriginalText() != want || s.Buffer().Version() != 1 {
				t.Fatalf("task click did not toggle only its marker: %q want%q", s.Buffer().OriginalText(), want)
			}
			if nextA, nextZ := s.Selection(); nextA != a || nextZ != z {
				t.Fatal("task toggle moved the existing source selection")
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("one undo did not restore original task case/BOM/newlines")
			}
			view.Command("redo")
			if s.Buffer().OriginalText() != want {
				t.Fatal("task redo did not restore the same edit")
			}
		}
	}
}

func TestLiveTaskPointerSelectionAndModesDoNotToggle(t *testing.T) {
	for _, variant := range []string{"drag", "shift", "readonly", "source", "default"} {
		t.Run(variant, func(t *testing.T) {
			const source = "- [ ] task text\n\nbody\n"
			s, view := taskNativeTester(source, variant == "readonly", variant != "default")
			if variant == "source" {
				s.SourceMode = true
				view.Frame()
			}
			x, y := taskMarkerPoint(t, s, strings.Index(source, "["))
			if variant == "drag" {
				view.Press(x, y)
				view.Move(x+90, y)
				view.Release(x+90, y)
			} else if variant == "shift" {
				view.ClickAtWith(ui.Shift, x, y)
			} else {
				view.ClickAt(x, y)
			}
			if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source {
				t.Fatal("drag/modified click or protected mode toggled task")
			}
			if variant == "drag" {
				if a, z := s.Selection(); a == z {
					t.Fatal("task press swallowed ordinary drag selection")
				}
			}
		})
	}
}

func TestLiveTaskClickRejectsStalePressAndNonTaskText(t *testing.T) {
	const source = "- [ ] task\n\nbody\n"
	s, view := taskNativeTester(source, false, true)
	x, y := taskMarkerPoint(t, s, strings.Index(source, "["))
	view.Press(x, y)
	s.Buffer().ApplyTransaction(editor.Transaction{Version: s.Buffer().Version(), Edits: []editor.Edit{{Start: 0, End: 0, Text: "external\n\n"}}, Kind: editor.EditOther})
	view.Frame()
	view.Release(x, y)
	if s.Buffer().OriginalText() != "external\n\n"+source || s.Buffer().Version() != 1 {
		t.Fatal("stale task press changed a newer document")
	}
	for _, notTask := range []string{"ordinary [ ] text\n", "```text\n- [ ] code\n```\n"} {
		s, view = taskNativeTester(notTask, false, true)
		x, y = taskMarkerPoint(t, s, strings.Index(notTask, "["))
		view.ClickAt(x, y)
		if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != notTask {
			t.Fatal("checkbox-like ordinary/code text toggled")
		}
	}
}

func TestLiveTaskCompositionClickDoesNotAlsoCheckTheTask(t *testing.T) {
	const source = "- [ ] task\n\nbody\n"
	s, view := taskNativeTester(source, false, true)
	x, y := taskMarkerPoint(t, s, strings.Index(source, "["))
	view.Compose("候选😀", 4)
	view.ClickAt(x, y)
	if !strings.HasPrefix(s.Buffer().OriginalText(), "- [ ] task\n") || s.Buffer().Version() != 1 || !strings.HasSuffix(s.Buffer().OriginalText(), "候选😀") {
		t.Fatal("composition click lost its text or also toggled the task")
	}
}

func TestLiveTaskTogglePreservesOtherVirtualSelectionIntent(t *testing.T) {
	for _, kind := range []string{"omitted", "tail"} {
		t.Run(kind, func(t *testing.T) {
			source := "- [ ] task\n\n```js\nx\n```"
			if kind == "omitted" {
				source = "- [ ] task\n\n| A | B | C |\n|---|---|---|\n| x |\n"
			}
			s, view := taskNativeTester(source, false, true)
			if kind == "omitted" {
				at := strings.Index(source, "C")
				s.SetSelection(at, at)
				view.Frame()
				view.Key(0, ui.KeyDown)
			}
			x, y := taskMarkerPoint(t, s, strings.Index(source, "["))
			view.ClickAt(x, y)
			if kind == "omitted" {
				assertNavigationTableCell(t, s, 1, 2)
			} else if s.focusedCodeGap() == nil {
				t.Fatal("checking another task lost the outside-code caret")
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("task undo changed unrelated virtual structure")
			}
			if kind == "omitted" {
				assertNavigationTableCell(t, s, 1, 2)
			} else if s.focusedCodeGap() == nil {
				t.Fatal("task undo lost outside-code selection intent")
			}
		})
	}
}

func TestLiveTaskUndoRedoKeepsTheVisibleReadingPoint(t *testing.T) {
	source := "- [ ] top task\n\n" + strings.Repeat("long document body\n\n", 60)
	s, view := taskNativeTester(source, false, true)
	if s.scroll.Y <= 0 {
		t.Fatal("fixture did not place the source caret at distant EOF")
	}
	view.Scroll(30, 200, 0, -100000)
	if s.scroll.Y != 0 {
		t.Fatal("fixture did not scroll back to the visible task")
	}
	x, y := taskMarkerPoint(t, s, strings.Index(source, "["))
	view.ClickAt(x, y)
	if s.scroll.Y != 0 {
		t.Fatal("checking a task revealed the old offscreen source caret")
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source || s.scroll.Y != 0 {
		t.Fatalf("task undo lost bytes or jumped away from the reading point: scroll=%g", s.scroll.Y)
	}
	x, y = taskMarkerPoint(t, s, strings.Index(source, "["))
	if y < 0 || y > s.rect.H {
		t.Fatal("undone task is no longer actually visible")
	}
	view.Command("redo")
	if s.scroll.Y != 0 || !strings.HasPrefix(s.Buffer().OriginalText(), "- [x]") {
		t.Fatal("task redo lost the visible reading point")
	}
	view.Type("normal edit")
	if s.scroll.Y <= 0 {
		t.Fatal("normal input lost its genuine offscreen caret reveal")
	}
	view.Scroll(30, 200, 0, -100000)
	view.Command("undo")
	if s.scroll.Y <= 0 {
		t.Fatal("task-specific history change disabled ordinary text undo reveal")
	}
}
