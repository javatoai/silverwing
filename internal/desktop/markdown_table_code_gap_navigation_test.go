package desktop

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestMarkdownTableArrowsCrossTallRowsInMaterials(t *testing.T) {
	const previous = "tns-review2-refund-mapper-red.log"
	const current = "tns-review2-mysql-boundary-red.log"
	logical := "# 表格导航\n\n| 日志 | 结果 |\n|---|---|\n| `tns-review2-refund-red.log` | 第一行结果 |\n| `" + previous + "` | 上一行第一段<br>上一行第二段 |\n| `" + current + "` | 当前行结果 |\n| `last.log` | 最后一行结果 |\n"
	text := strings.ReplaceAll(logical, "\n", "\r\n")
	source := "\ufeff" + text
	s, _, content, view := markdownLiveAcceptanceFixture(t, source, false, 960)
	state := focusMarkdownLiveAcceptance(t, s, view)
	buffer, version := state.live.Buffer(), state.live.Buffer().Version()
	at := strings.Index(logical, current) + 12
	state.live.SetSelection(at, at)
	view.Frame()
	view.Key(0, ui.KeyUp)
	assertInCell := func(label string) {
		t.Helper()
		a, z := state.live.Selection()
		start := strings.Index(logical, label)
		if a != z || z < start || z > start+len(label)+1 {
			t.Fatalf("arrow did not reach intended cell %q: selection=%d:%d expected=%d..%d", label, a, z, start, start+len(label)+1)
		}
	}
	assertInCell(previous)
	view.Key(0, ui.KeyDown)
	assertInCell(current)
	right := strings.Index(logical, "当前行结果")
	state.live.SetSelection(right, right)
	view.Frame()
	view.Key(0, ui.KeyLeft)
	assertInCell(current)
	view.Key(0, ui.KeyRight)
	assertInCell("当前行结果")
	view.Key(ui.Shift, ui.KeyRight)
	if a, z := state.live.Selection(); a == z {
		t.Fatal("Shift+Right no longer extends native text selection")
	}
	if buffer.Version() != version || state.live.Buffer() != buffer {
		t.Fatal("cell navigation rewrote source or replaced the native Buffer")
	}
	// The documents service separates the file BOM from editable text. Keep
	// that production contract and check the original file bytes independently.
	assertMarkdownAcceptanceSource(t, s, state, text)
	if ref, ok := s.materialsUnit.documentSaveRef(s.documentAutosaveKey(content.Path)); !ok || !ref.hasUTF8BOM || ref.newline != "\r\n" {
		t.Fatal("navigation lost the file's BOM/newline metadata")
	}
	if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != source {
		t.Fatal("table navigation changed the original file bytes", err)
	}
}

func TestMarkdownCodeGapInMaterialsWritesOnlyOutsideFences(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(ending, "\n", "LF"), func(t *testing.T) {
			logical := "# 相邻代码块\n\n```sql\nselect 1;\n```\n```python\nprint(2)\n```\n\n<!-- untouched:keep -->\n"
			text := strings.ReplaceAll(logical, "\n", ending)
			source := "\ufeff" + text
			s, _, content, view := markdownLiveAcceptanceFixture(t, source, false, 960)
			state := focusMarkdownLiveAcceptance(t, s, view)
			buffer, version := state.live.Buffer(), state.live.Buffer().Version()
			gap, found := view.Find("在代码块之间插入内容")
			if !found || gap.H < 12 {
				t.Fatal("adjacent code cards do not expose a real, usable insertion row")
			}
			view.ClickAt(gap.X+min(float32(12), gap.W/2), gap.Y+gap.H/2)
			if buffer.Version() != version || buffer.OriginalText() != text {
				t.Fatal("focusing the virtual gap modified document bytes")
			}
			view.Type("中间段落😀")
			after := buffer.OriginalText()
			normalized := buffer.Snapshot().String()
			d := parser.Parse(normalized)
			codes, paragraph := 0, false
			for i := 1; i < d.Len(); i++ {
				b := d.Block(i)
				if b.Kind == parser.CodeBlock {
					want := "select 1;"
					if codes == 1 {
						want = "print(2)"
					}
					if strings.TrimSuffix(d.Lines(i), "\n") != want {
						t.Fatalf("gap typing changed code body: %q", d.Lines(i))
					}
					codes++
				}
				if b.Kind == parser.Paragraph && strings.Contains(d.Lines(i), "中间段落😀") {
					paragraph = true
				}
			}
			if codes != 2 || !paragraph || !strings.HasSuffix(after, "<!-- untouched:keep -->"+ending) {
				t.Fatal("gap did not create a source-preserving paragraph between exactly two fences")
			}
			if ref, ok := s.materialsUnit.documentSaveRef(s.documentAutosaveKey(content.Path)); !ok || !ref.hasUTF8BOM || ref.newline != ending {
				t.Fatal("gap editing lost the file's BOM/newline metadata")
			}
			if ending == "\r\n" && strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
				t.Fatal("gap materialization mixed newline formats")
			}
			assertMarkdownAcceptanceSource(t, s, state, after)
			view.Command("undo")
			assertMarkdownAcceptanceSource(t, s, state, text)
			view.Command("redo")
			assertMarkdownAcceptanceSource(t, s, state, after)
			if state.live.Buffer() != buffer {
				t.Fatal("gap editing replaced the shared native Buffer")
			}
			if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
				t.Fatal(failures)
			}
			if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != "\ufeff"+after {
				t.Fatal("saving gap input changed BOM, newline or untouched source bytes", err)
			}
		})
	}
}
