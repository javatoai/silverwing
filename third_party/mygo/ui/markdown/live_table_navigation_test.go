package markdown

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

const navigationTableSource = "# 表格导航\n\n| 日志 | 结果 |\n|---|---|\n" +
	"| `tns-review2-refund-red.log` | 第一行结果 |\n" +
	"| `tns-review2-lease-key-red.log` | 第二行结果 |\n" +
	"| `tns-review2-refund-mapper-red.log` | 第三行第一段<br>第三行第二段 |\n" +
	"| `tns-review2-mysql-boundary-red.log` | 第四行结果 |\n" +
	"| `tns-review2-lease-refund-key-green.log` | 第五行结果 |\n\n表格后的普通正文。\n"

func navigationTableCell(t *testing.T, s *EditorState, row, column int) editorTableCell {
	t.Helper()
	for _, hit := range s.tableCells {
		if hit.cell.row == row && hit.cell.column == column {
			return hit.cell
		}
	}
	t.Fatalf("missing rendered table cell %d:%d", row, column)
	return editorTableCell{}
}

func assertNavigationTableCell(t *testing.T, s *EditorState, row, column int) {
	t.Helper()
	_, cell, ok := s.editingTable()
	if !ok || cell.row != row || cell.column != column {
		t.Fatalf("caret entered the wrong cell: selection=%d:%d cell=%d:%d found=%v want=%d:%d", s.anchor, s.caret, cell.row, cell.column, ok, row, column)
	}
}

func TestLiveTableNavigationUpAcrossTallNeighborAndPointer(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll(navigationTableSource, "\n", ending)
		at := strings.Index(navigationTableSource, "tns-review2-mysql") + 12
		s, view := keyboardViewportTester(source, at, 960, 1, false, false)
		buffer, version := s.Buffer(), s.Buffer().Version()
		previous := navigationTableCell(t, s, 3, 0)
		run := s.sourceCaretRun(previous.start + 1)
		if run == nil || run.layout == nil {
			t.Fatal("the actual preceding inline-code cell is not shaped")
		}
		var rowHeight float32
		var previousRect ui.Rect
		for _, hit := range s.tableCells {
			if hit.cell.row == 3 && hit.cell.column == 0 {
				rowHeight = hit.rect.H
				previousRect = hit.rect
			}
		}
		if rowHeight < run.rect.H+s.view.th.FontSize*s.view.th.LineHeight/2 {
			t.Fatalf("fixture did not retain the empty height below the previous cell: row=%g text=%g", rowHeight, run.rect.H)
		}
		keyboardViewportCaret(t, s, view, at)
		view.Key(0, ui.KeyUp)
		assertNavigationTableCell(t, s, 3, 0)
		keyboardViewportCaret(t, s, view, s.caret)
		view.Key(0, ui.KeyDown)
		assertNavigationTableCell(t, s, 4, 0)
		// Pointer navigation must target the real cell, independently of the
		// desired column retained by the preceding arrow-key movement.
		run = s.sourceCaretRun(previous.start + 1)
		view.ClickAt(run.rect.X+min(30, run.rect.W/2), run.rect.Y+run.rect.H/2)
		assertNavigationTableCell(t, s, 3, 0)
		view.ClickAt(previousRect.X+30, previousRect.Y+previousRect.H-2)
		assertNavigationTableCell(t, s, 3, 0)
		if s.anchor != s.caret || s.Buffer() != buffer || buffer.Version() != version || buffer.OriginalText() != source {
			t.Fatal("table keyboard/mouse navigation changed source bytes or the single Buffer")
		}
	}
}

func TestLiveTableNavigationUsesFirstAndLastWrappedVisualLines(t *testing.T) {
	name := "tns-" + strings.Repeat("refund-lease-boundary-", 7) + "中文😀-red.log"
	source := "| 日志 | 结果 |\n|---|---|\n| upper.log | 上一行 |\n| `" + name + "` | 高格第一段<br>第二段 |\n| lower.log | 下一行 |\n"
	at := strings.Index(source, name) + 5
	s, view := keyboardViewportTester(source, at, 420, 1, false, false)
	buffer, version := s.Buffer(), s.Buffer().Version()
	run := s.sourceCaretRun(at)
	if run == nil || run.layout == nil {
		t.Fatal("wrapped inline-code cell has no actual source layout")
	}
	first := run.layout.CaretAt(ui.TextCaretPosition{Index: run.unitForSource(at), Affinity: s.affinity})
	if run.rect.H < first.H*2 || first.Y > first.H/2 {
		t.Fatalf("fixture is not on the first of multiple visual lines: %+v height=%g", first, run.rect.H)
	}
	view.Key(0, ui.KeyDown)
	assertNavigationTableCell(t, s, 2, 0)
	nextRun := s.sourceCaretRun(s.caret)
	if nextRun == nil || nextRun.layout == nil {
		t.Fatal("Down lost the wrapped cell's actual source layout")
	}
	next := nextRun.layout.CaretAt(ui.TextCaretPosition{Index: nextRun.unitForSource(s.caret), Affinity: s.affinity})
	if s.caret <= at || next.Y <= first.Y+first.H/2 {
		t.Fatal("Down crossed a cell boundary before exhausting the current visual lines")
	}
	limit := int(math.Ceil(float64(run.rect.H/first.H))) + 2
	for i := 0; i < limit; i++ {
		_, cell, ok := s.editingTable()
		if ok && cell.row == 3 {
			break
		}
		assertNavigationTableCell(t, s, 2, 0)
		view.Key(0, ui.KeyDown)
	}
	assertNavigationTableCell(t, s, 3, 0)
	view.Key(0, ui.KeyUp)
	assertNavigationTableCell(t, s, 2, 0)
	run = s.sourceCaretRun(s.caret)
	if run == nil || run.layout == nil {
		t.Fatal("Up lost the preceding wrapped cell's actual source layout")
	}
	last := run.layout.CaretAt(ui.TextCaretPosition{Index: run.unitForSource(s.caret), Affinity: s.affinity})
	if last.Y+last.H < run.rect.H-1 {
		t.Fatalf("Up did not enter the preceding cell's last visual line: %+v height=%g", last, run.rect.H)
	}
	for i := 0; i < limit; i++ {
		_, cell, ok := s.editingTable()
		if ok && cell.row == 1 {
			break
		}
		assertNavigationTableCell(t, s, 2, 0)
		view.Key(0, ui.KeyUp)
	}
	assertNavigationTableCell(t, s, 1, 0)
	keyboardViewportCaret(t, s, view, s.caret)
	if buffer.Version() != version || buffer.OriginalText() != source || s.Buffer() != buffer {
		t.Fatal("visual-line navigation rewrote the table")
	}
}

func TestLiveTableNavigationHorizontalEdgesAndSelectionContracts(t *testing.T) {
	source := "| 左 | 右 |\n|---|---|\n| 甲😀乙 | `long-file-name.log` |\n"
	at := strings.Index(source, "甲")
	s, view := keyboardViewportTester(source, at, 960, 1, false, false)
	buffer, version := s.Buffer(), s.Buffer().Version()
	left, right := navigationTableCell(t, s, 1, 0), navigationTableCell(t, s, 1, 1)
	view.Key(0, ui.KeyRight)
	if s.anchor != at+len("甲") || s.caret != s.anchor {
		t.Fatal("ordinary Right stopped following native text boundaries")
	}
	view.Key(ui.Shift, ui.KeyRight)
	if s.anchor != at+len("甲") || s.caret != at+len("甲😀") {
		t.Fatal("Shift+Right no longer selects the whole emoji inside its cell")
	}
	view.Key(ui.Shift, ui.KeyLeft)
	if s.anchor != at+len("甲") || s.caret != s.anchor {
		t.Fatal("Shift+Left did not restore the original selection anchor")
	}
	s.SetSelection(left.end, left.end)
	view.Frame()
	view.Key(ui.Shift, ui.KeyRight)
	if s.anchor != left.end || s.caret != left.end+1 {
		t.Fatal("Shift selection skipped source padding/delimiters as cell navigation")
	}
	s.SetSelection(left.end, left.end)
	view.Frame()
	view.Key(0, ui.KeyRight)
	assertNavigationTableCell(t, s, 1, 1)
	if s.caret != right.start || s.anchor != s.caret {
		t.Fatal("Right at the visible edge did not enter the next cell at its content start")
	}
	view.Key(0, ui.KeyLeft)
	assertNavigationTableCell(t, s, 1, 0)
	if s.caret != left.end || s.anchor != s.caret {
		t.Fatal("Left entered a hidden pipe/padding instead of the previous cell's content end")
	}
	s.SetSelection(left.start, left.start)
	view.Frame()
	view.Key(0, ui.KeyTab)
	assertNavigationTableCell(t, s, 1, 1)
	view.Key(ui.Shift, ui.KeyTab)
	assertNavigationTableCell(t, s, 1, 0)
	if buffer.Version() != version || buffer.OriginalText() != source || s.Buffer() != buffer {
		t.Fatal("horizontal/selection/Tab navigation changed source or Buffer")
	}
}

func TestLiveTableNavigationPreservesIMEReadOnlyAndSourceMode(t *testing.T) {
	source := navigationTableSource
	at := strings.Index(source, "tns-review2-mysql") + 12
	s, view := keyboardViewportTester(source, at, 960, 1, false, false)
	buffer, version := s.Buffer(), s.Buffer().Version()
	view.Compose("候选😀", 4)
	for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown, ui.KeyLeft, ui.KeyRight, ui.KeyTab} {
		view.Key(0, key)
		if !s.Composing() || s.anchor != at || s.caret != at || buffer.Version() != version || buffer.OriginalText() != source {
			t.Fatal("table navigation took an IME-owned key or changed committed source")
		}
	}
	view.Type("提交😀")
	if buffer.OriginalText() != source[:at]+"提交😀"+source[at:] {
		t.Fatal("IME commit after direction keys did not use the same root source client")
	}
	view.Command("undo")
	if buffer.OriginalText() != source {
		t.Fatal("IME commit no longer shares one undo transaction")
	}
	s, view = keyboardViewportTester(source, at, 960, 1, false, true)
	for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown, ui.KeyLeft, ui.KeyRight, ui.KeyTab, ui.KeyEnter} {
		view.Key(0, key)
	}
	view.Type("不能写")
	if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source {
		t.Fatal("read-only table direction keys enabled source edits")
	}
	s, view = keyboardViewportTester(source, at, 960, 1, true, false)
	view.Key(0, ui.KeyTab)
	if s.Buffer().OriginalText() != source[:at]+"\t"+source[at:] {
		t.Fatal("source-mode Tab was taken over by visual cell navigation")
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("literal source-mode Tab lost its original undo semantics")
	}
}
