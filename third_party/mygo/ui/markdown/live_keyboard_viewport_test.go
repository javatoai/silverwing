package markdown

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func keyboardViewportTester(source string, at, width int, zoom float32, sourceMode, readOnly bool) (*EditorState, *ui.Tester) {
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	s.SourceMode = sourceMode
	options := EditorOptions{ContinueBlocks: true, InPlaceCode: true, InPlaceTables: true, RenderedLinks: true, ReadOnly: readOnly,
		Options: &Options{Theme: &Theme{FontSize: 15, LineHeight: 1.6}}}
	view := ui.NewTester(func(c *ui.Context) { Editor(c, s, options).Fill() }, width, 420)
	view.SetZoom(zoom)
	// This is initial configuration, not a zoom operation on a focused caret.
	s.SetSelection(at, at)
	s.Focus()
	view.Frame()
	return s, view
}

func keyboardViewportRunEvidence(t *testing.T, s *EditorState, at int) {
	t.Helper()
	t.Logf("source caret=%d selection=%d:%d reveal=%v composing=%v sourceMode=%v active=%d:%d root=%+v scroll=%+v runs=%d", at, s.anchor, s.caret, s.reveal, s.composing, s.SourceMode, s.activeStart, s.activeEnd, s.rect, s.scroll, len(s.runs))
	owners := 0
	for i, run := range s.runs {
		if at < run.caretStart || at > run.caretEnd {
			continue
		}
		owners++
		t.Logf("owning run[%d] range=%d:%d layout=%v rect=%+v text=%q", i, run.caretStart, run.caretEnd, run.layout != nil, run.rect, run.text[:min(len(run.text), 120)])
	}
	if nearest := s.caretRun(at); nearest != nil {
		t.Logf("caretRun result range=%d:%d owns=%v layout=%v rect=%+v; sourceOwners=%d", nearest.caretStart, nearest.caretEnd, at >= nearest.caretStart && at <= nearest.caretEnd, nearest.layout != nil, nearest.rect, owners)
	} else {
		t.Logf("caretRun result=nil; sourceOwners=%d", owners)
	}
}

func keyboardViewportCaret(t *testing.T, s *EditorState, view *ui.Tester, at int) ui.Rect {
	t.Helper()
	run := s.caretRun(at)
	if run == nil || run.layout == nil || at < run.caretStart || at > run.caretEnd {
		keyboardViewportRunEvidence(t, s, at)
		t.Fatalf("native caret lost its actual source run at %d", at)
	}
	box, ok := s.caretBounds(at)
	if !ok || box.H <= 0 || box.Y < -1 || box.Y+box.H > s.rect.H+1 {
		keyboardViewportRunEvidence(t, s, at)
		t.Fatalf("mapped caret is outside the real editor viewport: caret=%+v viewport=%+v scroll=%+v", box, s.rect, s.scroll)
	}
	native, active := view.TextCaret()
	if !active || native.H <= 0 || native.Y < -1 || native.Y+native.H > 421 {
		keyboardViewportRunEvidence(t, s, at)
		t.Fatalf("native input-method caret is not visible: %+v active=%v", native, active)
	}
	return native
}

func TestLiveKeyboardSpaceRetainsViewportAcrossMarkdownTransitions(t *testing.T) {
	for _, fixture := range []struct {
		name, target, token, ending string
		ahead, after, width         int
		zoom                        float32
		sourceMode                  bool
	}{
		{"ordered-top", "1.", "1.", "\n", 0, 40, 420, 1, false},
		{"ordered-middle-zoom", "1.", "1.", "\r\n", 12, 28, 960, 1.5, false},
		{"ordered-near-end", "1.", "1.", "\r\n", 30, 2, 420, 1.5, false},
		{"bullet", "-", "-", "\n", 12, 28, 420, 1, false},
		{"task", "- [ ]", "- [ ]", "\r\n", 12, 28, 960, 1, false},
		{"quote", ">", ">", "\n", 12, 28, 420, 1.5, false},
		{"heading", "#", "#", "\r\n", 0, 40, 960, 1.5, false},
		{"table", "| A | B |\n|---|---|\n| alpha | beta |", "alpha", "\n", 12, 28, 960, 1, false},
		{"code", "```sql\nSELECT 1;\n```", "SELECT 1;", "\r\n", 12, 28, 420, 1, false},
		{"body-near-end", "普通正文😀", "普通正文😀", "\n", 30, 2, 960, 1.5, false},
		{"source-middle", "1.", "1.", "\n", 12, 28, 420, 1.5, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			paragraph := "前文中文说明😀 [link](https://example.com)。\n\n"
			prefix := "\ufeff# TNS 第二轮代码审查与修复\n\n" + strings.Repeat(paragraph, fixture.ahead) + "本次检查保留原始记录。\n\n"
			normalized := prefix + fixture.target + "\n\n## Standards\n\n" + strings.Repeat("### 后续章节\n\n后文内容，不能被空格按键错误定位。\n\n", fixture.after)
			source := strings.ReplaceAll(normalized, "\n", fixture.ending)
			at := len(prefix) + strings.Index(fixture.target, fixture.token) + len(fixture.token)
			s, view := keyboardViewportTester(source, at, fixture.width, fixture.zoom, fixture.sourceMode, false)
			buffer, version := s.Buffer(), s.Buffer().Version()
			snapshot := buffer.Snapshot()
			want := snapshot.OriginalSlice(0, at) + " " + snapshot.OriginalSlice(at, snapshot.Len())
			before, scroll := keyboardViewportCaret(t, s, view, at), s.scroll.Y
			view.KeyDown(ui.KeySpace)
			if s.scroll.Y != scroll || s.Buffer().Version() != version || buffer.OriginalText() != source || s.anchor != at || s.caret != at {
				t.Fatalf("Space KeyDown changed the viewport/source before commit: scroll %g -> %g caret=%d:%d", scroll, s.scroll.Y, s.anchor, s.caret)
			}
			down := keyboardViewportCaret(t, s, view, at)
			if math.Abs(float64(down.Y-before.Y)) > .1 {
				t.Fatalf("Space KeyDown moved the native caret before text: %+v -> %+v", before, down)
			}
			view.Type(" ")
			view.KeyUp(ui.KeySpace)
			if buffer.OriginalText() != want || s.Buffer() != buffer || buffer.Version() != version+1 || s.anchor != at+1 || s.caret != at+1 {
				t.Fatalf("Space commit lost source bytes, one Buffer or actual selection: source=%q selection=%d:%d", buffer.OriginalText(), s.anchor, s.caret)
			}
			after := keyboardViewportCaret(t, s, view, at+1)
			if math.Abs(float64(after.Y-before.Y)) > float64(max(before.H, after.H)*2+2) || math.Abs(float64(s.scroll.Y-scroll)) > float64(max(before.H, after.H)*2+2) {
				t.Fatalf("typing revealed an unrelated editing neighborhood: %+v -> %+v scroll=%g -> %g", before, after, scroll, s.scroll.Y)
			}
			view.Command("undo")
			if buffer.OriginalText() != source || s.anchor != at || s.caret != at {
				t.Fatal("Space undo did not restore original BOM/EOL/source selection")
			}
			keyboardViewportCaret(t, s, view, at)
			view.Command("redo")
			if buffer.OriginalText() != want {
				t.Fatal("Space redo did not use the same source history")
			}
			keyboardViewportCaret(t, s, view, at+1)
		})
	}
}

func TestLiveKeyboardCompositionScrollAwayPasteAndModesRetainRealReveal(t *testing.T) {
	prefix := "\ufeff# 审查\n\n" + strings.Repeat("已有中文😀说明。\n\n", 18) + "当前正文😀"
	normalized := prefix + "\n\n" + strings.Repeat("后续内容与链接 [link](https://example.com)。\n\n", 45)
	source := strings.ReplaceAll(normalized, "\n", "\r\n")
	at := len(prefix)
	s, view := keyboardViewportTester(source, at, 700, 1, false, false)
	buffer, version := s.Buffer(), s.Buffer().Version()
	snapshot := buffer.Snapshot()
	view.Compose("候选😀", 3)
	preeditAt := s.composeStart + len(s.compose)
	before, scroll := keyboardViewportCaret(t, s, view, preeditAt), s.scroll.Y
	view.KeyDown(ui.KeySpace)
	view.KeyUp(ui.KeySpace)
	if !s.Composing() || buffer.Version() != version || buffer.OriginalText() != source || s.scroll.Y != scroll {
		t.Fatal("IME Space paged the viewport or committed preedit")
	}
	marked := keyboardViewportCaret(t, s, view, preeditAt)
	if math.Abs(float64(marked.Y-before.Y)) > .1 {
		t.Fatal("IME candidate geometry jumped on Space")
	}
	view.Type("提交😀")
	want := snapshot.OriginalSlice(0, at) + "提交😀" + snapshot.OriginalSlice(at, snapshot.Len())
	if buffer.OriginalText() != want || s.Composing() {
		t.Fatal("native IME did not commit through the original Buffer")
	}
	view.Command("undo")
	if buffer.OriginalText() != source {
		t.Fatal("one IME undo did not restore original source")
	}
	keyboardViewportCaret(t, s, view, at)
	realCaret, _ := s.caretBounds(at) // the helper just verified the actual owner
	beforeAway := s.scroll.Y
	documentY := realCaret.Y + beforeAway
	view.Scroll(350, 200, 0, 650)
	away := s.scroll.Y
	// Source/layout did not change, so the verified caret's document Y is
	// stable. A nearest visible run is not evidence for this offscreen caret.
	offscreenY := documentY - away
	if away <= beforeAway || offscreenY+realCaret.H > 0 {
		keyboardViewportRunEvidence(t, s, at)
		t.Fatalf("fixture did not genuinely scroll away from its actual source caret: docY=%g caretY=%g height=%g scroll=%g -> %g", documentY, offscreenY, realCaret.H, beforeAway, away)
	}
	view.Frame()
	view.KeyDown(ui.KeySpace)
	if s.scroll.Y != away || buffer.OriginalText() != source {
		t.Fatal("Space key or idle frame overrode user scrolling before text commit")
	}
	view.Type(" ")
	view.KeyUp(ui.KeySpace)
	keyboardViewportCaret(t, s, view, at+1)
	if s.scroll.Y >= away {
		t.Fatal("real text commit no longer reveals a caret scrolled out of view")
	}
	view.SetClipboard("粘贴😀")
	view.Command("paste")
	want = snapshot.OriginalSlice(0, at) + " 粘贴😀" + snapshot.OriginalSlice(at, snapshot.Len())
	if buffer.OriginalText() != want {
		t.Fatal("paste changed original source outside its insertion")
	}
	keyboardViewportCaret(t, s, view, s.caret)
	view.Command("undo")
	want = snapshot.OriginalSlice(0, at) + " " + snapshot.OriginalSlice(at, snapshot.Len())
	if buffer.OriginalText() != want {
		t.Fatal("paste did not retain its own native undo step")
	}
	s.SourceMode = true
	s.SetSelection(s.caret, s.caret)
	view.Frame()
	before = keyboardViewportCaret(t, s, view, s.caret)
	view.TypeKey(ui.Shift, ui.KeySpace, " ")
	if after := keyboardViewportCaret(t, s, view, s.caret); math.Abs(float64(after.Y-before.Y)) > float64(before.H+1) {
		t.Fatal("Shift+Space paged the source editor")
	}
	s.SourceMode = false
	s.SetSelection(s.caret, s.caret)
	view.Frame()
	view.Command("undo")
	if buffer.OriginalText() != want || s.Buffer() != buffer {
		t.Fatal("mode switching split the Space edit's source history")
	}
	keyboardViewportCaret(t, s, view, s.caret)
}

func TestLiveKeyboardDynamicZoomKeepsFocusedCaretVisible(t *testing.T) {
	prefix := "# 动态缩放\n\n" + strings.Repeat("已有中文😀说明与 [link](https://example.com)。\n\n", 18) + "当前编辑段落。\n\n"
	source := prefix + "1.\n\n## Standards\n\n" + strings.Repeat("后续段落，不应成为当前编辑目标。\n\n", 35)
	at := len(prefix) + len("1.")
	s, view := keyboardViewportTester(source, at, 700, 1, false, false)
	before := keyboardViewportCaret(t, s, view, at)
	buffer, version, scroll := s.Buffer(), s.Buffer().Version(), s.scroll.Y
	view.SetZoom(1.5) // a real focused 100% -> 150% transition; no selection reset
	if s.Buffer() != buffer || buffer.Version() != version || buffer.OriginalText() != source || s.anchor != at || s.caret != at {
		t.Fatal("dynamic zoom changed the original source/client selection")
	}
	after := keyboardViewportCaret(t, s, view, at)
	t.Logf("focused dynamic zoom native caret %+v -> %+v, scroll %g -> %g", before, after, scroll, s.scroll.Y)
}

func TestLiveKeyboardWrappedListMiddleInputDoesNotBootstrapWholeRun(t *testing.T) {
	source := "- " + strings.Repeat("中文 mixed words😀 continue ", 130) + "尾部\n\n" + strings.Repeat("后续独立段落。\n\n", 20)
	s, view := keyboardViewportTester(source, 2, 420, 1, false, false)
	view.Scroll(210, 210, 0, 240)
	view.ClickAt(150, 210) // a genuine source-mapped pointer caret within a wrapped row
	a, z := s.Selection()
	if a != z {
		t.Fatal("wrapped-run click did not create a collapsed native caret")
	}
	before := keyboardViewportCaret(t, s, view, a)
	if before.Y < 120 || before.Y > 300 || s.scroll.Y <= 0 {
		t.Fatalf("fixture did not place the real caret in the viewport middle: %+v scroll=%g", before, s.scroll.Y)
	}
	owner := s.caretRun(a)
	if owner == nil || owner.rect.H <= s.rect.H {
		t.Fatal("fixture is not a long soft-wrapped Item run")
	}
	buffer, snapshot, scroll := s.Buffer(), s.Buffer().Snapshot(), s.scroll.Y
	view.TypeKey(0, ui.KeySpace, " ")
	view.TypeKey(0, ui.KeyX, "x")
	want := snapshot.OriginalSlice(0, a) + " x" + snapshot.OriginalSlice(a, snapshot.Len())
	if buffer.OriginalText() != want || s.Buffer() != buffer || s.anchor != a+2 || s.caret != a+2 {
		t.Fatal("middle-of-run typing did not retain exact original source and selection")
	}
	after := keyboardViewportCaret(t, s, view, a+2)
	if math.Abs(float64(after.Y-before.Y)) > float64(before.H+1) || math.Abs(float64(s.scroll.Y-scroll)) > float64(before.H+1) {
		t.Fatalf("visible wrapped owner was unnecessarily scrolled as a whole: %+v -> %+v scroll=%g -> %g", before, after, scroll, s.scroll.Y)
	}
}

func TestLiveKeyboardResizeAndFontZoomKeepVisibleSource(t *testing.T) {
	prefix := "# 调整窗口\n\n" + strings.Repeat("中文😀说明与 [link](https://example.com)。\n\n", 18) + "当前正文😀"
	source := prefix + "\n\n" + strings.Repeat("后续独立段落。\n\n", 50)
	at := len(prefix)
	s, view := keyboardViewportTester(source, at, 960, 1, false, false)
	keyboardViewportCaret(t, s, view, at)
	buffer, version := s.Buffer(), s.Buffer().Version()
	view.SetSize(420, 420) // keep the current source selection; no synthetic reveal
	keyboardViewportCaret(t, s, view, at)
	s.opts.Options.Theme.FontSize = 21
	view.Frame()
	keyboardViewportCaret(t, s, view, at)
	if buffer.OriginalText() != source || buffer.Version() != version || s.Buffer() != buffer || s.anchor != at || s.caret != at {
		t.Fatal("resize/font zoom changed source, selection or Buffer identity")
	}
}

func TestLiveKeyboardResizeAndZoomAfterUserScrollKeepContentAnchor(t *testing.T) {
	prefix := "# 主动滚动\n\n" + strings.Repeat("已有中文😀说明。\n\n", 18) + "保留的原始光标"
	source := prefix + "\n\n" + strings.Repeat("继续阅读中文😀内容与 [link](https://example.com)。\n\n", 70)
	at := len(prefix)
	s, view := keyboardViewportTester(source, at, 960, 1, false, false)
	keyboardViewportCaret(t, s, view, at)
	view.Scroll(400, 200, 0, 650)
	if !s.viewport.valid || s.viewport.caretVisible {
		keyboardViewportRunEvidence(t, s, at)
		t.Fatal("user scroll-away did not record a visible source-content anchor")
	}
	anchor := s.viewport.at
	before, ok := s.ownedCaretBounds(anchor)
	if !ok || before.Y+before.H <= 0 || before.Y >= s.rect.H {
		t.Fatal("the scroll-away content anchor is not actually visible")
	}
	buffer, version := s.Buffer(), s.Buffer().Version()
	for _, step := range []string{"resize", "zoom"} {
		if step == "resize" {
			view.SetSize(420, 420)
		} else {
			view.SetZoom(1.5)
		}
		after, owned := s.ownedCaretBounds(anchor)
		if !owned || after.Y+after.H <= 0 || after.Y >= s.rect.H || math.Abs(float64(after.Y-before.Y)) > float64(max(before.H, after.H)+1) {
			keyboardViewportRunEvidence(t, s, anchor)
			t.Fatalf("%s lost the source content the user was reading: %+v -> %+v", step, before, after)
		}
		if caret, owned := s.ownedCaretBounds(at); owned && caret.Y+caret.H > 0 && caret.Y < s.rect.H {
			t.Fatalf("%s snapped back to the offscreen document caret: %+v", step, caret)
		}
		if s.viewport.caretVisible || buffer.OriginalText() != source || buffer.Version() != version || s.Buffer() != buffer || s.anchor != at || s.caret != at {
			t.Fatalf("%s changed the user-selected scroll/content state", step)
		}
	}
}

func TestLiveKeyboardTablePaddingKeepsFormattedCellAndNativeSource(t *testing.T) {
	for _, fixture := range []struct{ name, body, label string }{
		{"plain", "alpha", "alpha"},
		{"strong", "**alpha**", "alpha"},
		{"link", "[link](https://example.com)", "link"},
	} {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s/eol%d", fixture.name, len(ending)), func(t *testing.T) {
				normalized := "\ufeff# 原位表格\n\n| A | B |\n|---|---|\n| " + fixture.body + " | beta |\n"
				source := strings.ReplaceAll(normalized, "\n", ending)
				at := strings.Index(normalized, fixture.body) + len(fixture.body)
				s, view := keyboardViewportTester(source, at, 960, 1, false, false)
				buffer, snapshot := s.Buffer(), s.Buffer().Snapshot()
				assertPreview := func() {
					t.Helper()
					text := strings.Join(view.Texts(), "\n")
					if len(s.tableCells) != 4 || !strings.Contains(text, fixture.label) || strings.Contains(text, "**") || strings.Contains(text, "](") || strings.Contains(text, "https://example.com") {
						t.Fatalf("padding expanded the formatted cell or discarded its grid: cells=%d text=%q", len(s.tableCells), text)
					}
					if fixture.name == "strong" {
						strong := false
						for _, run := range s.runs {
							for _, span := range run.spans {
								strong = strong || strings.Contains(span.Text, "alpha") && span.Weight >= s.view.th.StrongWeight
							}
						}
						if !strong {
							t.Fatal("strong cell lost its actual highlighted span style")
						}
					}
				}
				assertPreview()
				before, scroll := keyboardViewportCaret(t, s, view, at), s.scroll.Y
				view.TypeKey(0, ui.KeySpace, " ")
				withSpace := snapshot.OriginalSlice(0, at) + " " + snapshot.OriginalSlice(at, snapshot.Len())
				if buffer.OriginalText() != withSpace || s.Buffer() != buffer || s.anchor != at+1 || s.caret != at+1 {
					t.Fatal("padding Space lost exact source, selection or the shared Buffer")
				}
				assertPreview()
				after := keyboardViewportCaret(t, s, view, at+1)
				if math.Abs(float64(after.Y-before.Y)) > .1 || s.scroll.Y != scroll {
					t.Fatalf("padding moved the actual cell's caret: %+v -> %+v scroll=%g -> %g", before, after, scroll, s.scroll.Y)
				}
				buffer.EndUndoGroup()
				view.TypeKey(0, ui.KeyX, "x")
				if buffer.OriginalText() != snapshot.OriginalSlice(0, at)+" x"+snapshot.OriginalSlice(at, snapshot.Len()) || s.anchor != at+2 || s.caret != at+2 {
					t.Fatal("typing in a cell-padding source position left that cell")
				}
				assertPreview()
				keyboardViewportCaret(t, s, view, at+2)
				view.Command("undo")
				if buffer.OriginalText() != withSpace || s.anchor != at+1 || s.caret != at+1 {
					t.Fatal("ordinary character undo did not restore the source padding caret")
				}
				keyboardViewportCaret(t, s, view, at+1)
				view.Command("undo")
				if buffer.OriginalText() != source || s.anchor != at || s.caret != at {
					t.Fatal("padding Space undo did not restore original BOM/EOL and selection")
				}
				assertPreview()
				keyboardViewportCaret(t, s, view, at)
			})
		}
	}
}

func TestLiveKeyboardTableLeadingPaddingIMEAndDelimiterProtection(t *testing.T) {
	source := "\ufeff| A | B |\r\n|---|---|\r\n| alpha | beta |\r\n"
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	at := strings.Index(normalized, "alpha") - 1
	s, view := keyboardViewportTester(source, at, 960, 1, false, false)
	buffer, snapshot, version := s.Buffer(), s.Buffer().Snapshot(), s.Buffer().Version()
	keyboardViewportCaret(t, s, view, at)
	view.Compose("候选😀", 4)
	if buffer.OriginalText() != source || buffer.Version() != version || len(s.tableCells) != 4 || !strings.Contains(strings.Join(view.Texts(), "\n"), "候选😀") {
		t.Fatal("leading-padding preedit lost the grid or wrote to the source Buffer")
	}
	keyboardViewportCaret(t, s, view, s.displayCaretOffset())
	view.Type("汉")
	want := snapshot.OriginalSlice(0, at) + "汉" + snapshot.OriginalSlice(at, snapshot.Len())
	if s.Composing() || buffer.OriginalText() != want || s.Buffer() != buffer || s.anchor != at+len("汉") || s.caret != at+len("汉") {
		t.Fatal("leading-padding IME commit lost raw source or the actual native selection")
	}
	keyboardViewportCaret(t, s, view, at+len("汉"))
	view.Command("undo")
	if buffer.OriginalText() != source || s.anchor != at || s.caret != at {
		t.Fatal("leading-padding IME undo failed to restore the original source")
	}
	keyboardViewportCaret(t, s, view, at)
	table, cell, ok := s.editingTable()
	if !ok || len(table.rows) != 2 || cell.row != 1 || cell.column != 0 {
		t.Fatal("leading padding no longer belongs to the actual first data cell")
	}
	version = buffer.Version()
	view.Key(0, ui.KeyBackspace)
	s.SetSelection(cell.sourceEnd, cell.sourceEnd)
	view.Frame()
	view.Key(0, ui.KeyDelete)
	if buffer.OriginalText() != source || buffer.Version() != version {
		t.Fatal("a cell-padding edge deleted a pipe or a physical row boundary")
	}
	view.Type(`\`)
	if buffer.OriginalText() != snapshot.OriginalSlice(0, cell.sourceEnd)+`\\`+snapshot.OriginalSlice(cell.sourceEnd, snapshot.Len()) {
		t.Fatal("padding input escaped a structural cell delimiter")
	}
	keyboardViewportCaret(t, s, view, cell.sourceEnd+2)
	view.Command("undo")
	if buffer.OriginalText() != source {
		t.Fatal("padding delimiter-protection input did not share the source undo history")
	}
}

func TestLiveKeyboardReadOnlyTablePaddingKeepsFormattedPreview(t *testing.T) {
	source := "| A | B |\n|---|---|\n| **alpha** [link](https://example.com) | beta |\n"
	at := strings.Index(source, ") |") + 2
	s, view := keyboardViewportTester(source, at, 960, 1, false, true)
	buffer, version := s.Buffer(), s.Buffer().Version()
	view.TypeKey(0, ui.KeySpace, " ")
	view.TypeKey(0, ui.KeyX, "x")
	text := strings.Join(view.Texts(), "\n")
	if buffer.OriginalText() != source || buffer.Version() != version || s.Buffer() != buffer || len(s.tableCells) != 4 || !strings.Contains(text, "alpha") || !strings.Contains(text, "link") || strings.Contains(text, "**") || strings.Contains(text, "](") {
		t.Fatal("read-only cell padding became an editable/raw Markdown preview")
	}
}

func TestLiveKeyboardReadOnlySpaceRemainsReaderPaging(t *testing.T) {
	source := strings.Repeat("只读中文😀段落。\n\n", 80)
	s, view := keyboardViewportTester(source, len("只读中文😀段落。\n\n")*15, 700, 1, false, true)
	s.scroll.Y = 300
	view.Frame()
	before, version := s.scroll.Y, s.Buffer().Version()
	view.Key(0, ui.KeySpace)
	if s.scroll.Y <= before || s.Buffer().OriginalText() != source || s.Buffer().Version() != version {
		t.Fatal("read-only Space stopped paging or mutated original text")
	}
	if active, _ := view.TextInputActive(); active {
		t.Fatal("read-only fixture unexpectedly owns an editable native input client")
	}
	t.Logf("reader Space kept legacy paging: %g -> %g, version=%s", before, s.scroll.Y, fmt.Sprint(version))
}
