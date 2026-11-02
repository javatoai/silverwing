package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Drive the production materials page and the real KeyDown/TextInput/KeyUp
// sequence. A text-only Type call cannot detect Space falling through to
// page scrolling before its text event is delivered.
func TestMarkdownKeyboardViewportMaterialsSpaceDoesNotPage(t *testing.T) {
	for _, sample := range []struct{ name, body string }{
		{"ordered", "1.¦\n\n"},
		{"bullet", "-¦\n\n"},
		{"task", "- [ ]¦\n\n"},
		{"quote", ">¦\n\n"},
		{"heading", "##¦\n\n"},
		{"paragraph", "当前中文😀¦正文\n\n"},
		{"code", "```sql\nSELECT¦id FROM sample;\n```\n\n"},
		{"table", "| 名称 | 内容 |\n|---|---|\n| 中文😀¦ | 值 |\n\n"},
	} {
		for _, width := range []int{960, 1280} {
			for _, zoom := range []float32{1, 1.5} {
				for _, newline := range []string{"\n", "\r\n"} {
					t.Run(fmt.Sprintf("%s/%d/zoom-%g/%q", sample.name, width, zoom, newline), func(t *testing.T) {
						prefix := "# TNS 第二轮代码审查与修复\n\n" + strings.Repeat("上文说明用于定位编辑区域。\n\n", 4)
						logical := prefix + sample.body + "## Standards\n\n" + strings.Repeat("### 后续章节\n\n后续长文档内容。\n\n", 40)
						at := strings.Index(logical, "¦")
						logical = strings.Replace(logical, "¦", "", 1)
						source := strings.ReplaceAll(logical, "\n", newline)
						s, _, _, view := markdownLiveAcceptanceFixture(t, source, width == 1280, width)
						view.SetZoom(zoom)
						state := focusMarkdownLiveAcceptance(t, s, view)
						state.live.SetSelection(at, at)
						view.Frame()
						buffer := state.live.Buffer()
						version := buffer.Version()
						before, active := view.TextCaret()
						if !active || before.H <= 0 {
							t.Fatal("fixture has no real native input caret")
						}
						view.KeyDown(ui.KeySpace)
						keydown, active := view.TextCaret()
						if !active || math.Abs(float64(keydown.Y-before.Y)) > .5 {
							t.Fatalf("Space KeyDown paged materials before any text: caret=%+v -> %+v", before, keydown)
						}
						if buffer.Version() != version || buffer.OriginalText() != source {
							t.Fatal("Space KeyDown changed the source before text input")
						}
						view.Type(" ")
						view.KeyUp(ui.KeySpace)
						want := strings.ReplaceAll(logical[:at]+" "+logical[at:], "\n", newline)
						assertMarkdownAcceptanceSource(t, s, state, want)
						if a, z := state.live.Selection(); a != at+1 || z != at+1 || state.live.Buffer() != buffer {
							t.Fatalf("real space changed shared buffer or selection: %d:%d want %d", a, z, at+1)
						}
						after, active := view.TextCaret()
						if !active || math.Abs(float64(after.Y-before.Y)) > float64(max(before.H, after.H))+1 {
							t.Fatalf("space commit lost the editing neighborhood: %+v -> %+v", before, after)
						}
						view.Command("undo")
						assertMarkdownAcceptanceSource(t, s, state, source)
						view.Command("redo")
						assertMarkdownAcceptanceSource(t, s, state, want)
					})
				}
			}
		}
	}
}

func TestMarkdownKeyboardViewportMaterialsFarTargetsAreActuallyVisible(t *testing.T) {
	for _, sample := range []struct{ name, body, label string }{
		{"task", "- [ ] 远处待办😀", "远处待办😀"},
		{"table", "| 名称 | 内容 |\n|---|---|\n| 远处单元格😀 | 值 |", "远处单元格😀"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			prefix := "# 长文档定位自测\n\n" + strings.Repeat("前文固定段落。\n\n", 45)
			source := prefix + sample.body + "\n\n## 后续章节\n\n" + strings.Repeat("后续正文。\n\n", 30)
			s, _, _, view := markdownLiveAcceptanceFixture(t, source, false, 960)
			state := focusMarkdownLiveAcceptance(t, s, view)
			at := len(prefix) + strings.Index(sample.body, sample.label) + len(sample.label)
			state.live.SetSelection(at, at)
			view.Frame()
			bounds, found := view.Find("Markdown 编辑区")
			target, exists := view.Find(sample.label)
			if !found || !exists {
				t.Fatalf("fixture cannot locate editor/target: editor=%v target=%v", found, exists)
			}
			if target.Y < bounds.Y || target.Y+target.H > bounds.Y+bounds.H {
				t.Fatalf("far source selection did not reveal its actual rendered target: target=%+v editor=%+v", target, bounds)
			}
			caret, active := view.TextCaret()
			if !active || caret.Y < target.Y-1 || caret.Y > target.Y+target.H {
				t.Fatalf("native caret points outside the actual target: caret=%+v target=%+v", caret, target)
			}
			assertMarkdownAcceptanceSource(t, s, state, source)
		})
	}
}

func TestMarkdownKeyboardViewportDocumentZoomKeepsActualTargetVisible(t *testing.T) {
	prefix := "# 文档缩放自测\n\n" + strings.Repeat("前文固定段落。\n\n", 35)
	const targetText = "缩放焦点目标😀"
	source := prefix + targetText + "\n\n" + strings.Repeat("后续正文。\n\n", 35)
	s, _, _, view := markdownLiveAcceptanceFixture(t, source, false, 960)
	state := focusMarkdownLiveAcceptance(t, s, view)
	at := len(prefix) + len(targetText)
	state.live.SetSelection(at, at)
	view.Frame()
	before, exists := view.Find(targetText)
	bounds, found := view.Find("Markdown 编辑区")
	if !exists || !found || before.Y < bounds.Y || before.Y+before.H > bounds.Y+bounds.H {
		t.Fatalf("zoom control must start with the actual target visible: %+v %+v", before, bounds)
	}
	buffer, version := state.live.Buffer(), state.live.Buffer().Version()
	// The materials page derives state.zoom from its reading session each
	// frame. Drive that production owner rather than a field it overwrites.
	s.setReadingMarkdownZoom(1.5)
	view.Frame()
	after, exists := view.Find(targetText)
	bounds, _ = view.Find("Markdown 编辑区")
	if !exists || after.Y < bounds.Y || after.Y+after.H > bounds.Y+bounds.H {
		t.Fatalf("document font zoom hid the focused target: before=%+v after=%+v editor=%+v", before, after, bounds)
	}
	if after.H <= before.H+1 {
		t.Fatalf("fixture did not actually enlarge document text: before=%+v after=%+v", before, after)
	}
	if state.live.Buffer() != buffer || buffer.Version() != version {
		t.Fatal("document zoom changed source buffer/version")
	}
	if a, z := state.live.Selection(); a != at || z != at {
		t.Fatal("document zoom changed source selection")
	}
	assertMarkdownAcceptanceSource(t, s, state, source)
}
