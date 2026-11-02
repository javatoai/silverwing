package desktop

import (
	"fmt"
	"image"
	"math"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

// The materials fixture renders the real editor but not the application's
// top-level lightbox layer. Include that same layer here so Expand must show
// its canvas, rather than merely assigning a viewer state.
func markdownStabilityAcceptanceTester(s *app, dark bool, width int) *ui.Tester {
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		s.beginOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() { s.materials(c, p) })
		s.renderMarkdownBitmapViewer(c, p)
		s.endOverflowMenuFrame()
	}, width, 850)
	tester.SetLocale("zh-Hans")
	return tester
}

// Only the asynchronous render result is controlled. Parsing, resource lookup,
// layout, event dispatch, source activation and the viewer are production paths.
func markdownStabilityAcceptanceDiagram(t *testing.T, s *app, state *markdownEditorState, width, height int) *markdownDiagramState {
	t.Helper()
	for _, part := range state.doc.Parts() {
		if part.Kind != editor.PartMermaid {
			continue
		}
		if old := state.diagrams[part.ID]; old != nil && old.cancel != nil {
			old.cancel()
		}
		code := editor.FindFence(part.Source).Code(part.Source)
		key := code
		if s.dark {
			key += "\x00dark"
		}
		diagram := &markdownDiagramState{key: key, source: code, bitmap: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, width, height)))}
		state.diagrams[part.ID] = diagram
		return diagram
	}
	t.Fatal("fixture did not parse a Mermaid fence")
	return nil
}

func assertMarkdownStabilityUnchanged(t *testing.T, s *app, state *markdownEditorState, source string, version uint64, anchor, caret int) {
	t.Helper()
	if s.materialsUnit.state.markdown != state || state.live.Buffer().Version() != version {
		t.Fatalf("presentation changed editor identity or source version: got=%d want=%d", state.live.Buffer().Version(), version)
	}
	if a, z := state.live.Selection(); a != anchor || z != caret {
		t.Fatalf("presentation changed selection: got=%d:%d want=%d:%d", a, z, anchor, caret)
	}
	assertMarkdownAcceptanceSource(t, s, state, source)
}

func TestMarkdownLiveStabilityAcceptanceOnlyTwoModesAndRefresh(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("newline-%q", newline), func(t *testing.T) {
			source := strings.ReplaceAll("# 两模式\n\n正文 **保留** 😀。\n", "\n", newline)
			s, _, content, tester := markdownLiveAcceptanceFixture(t, source, false, 960)
			state := focusMarkdownLiveAcceptance(t, s, tester)
			live, buffer := state.live, state.live.Buffer()
			if live.SourceMode || !s.materialsUnit.state.documentEditing {
				t.Fatal("new writable task material did not default to Live Preview")
			}
			for _, label := range []string{"实时预览", "源码"} {
				if _, ok := tester.Find(label); ok {
					t.Fatalf("writable mode %q should only be available through the context menu", label)
				}
			}
			if _, exists := tester.Find("阅读"); exists {
				t.Fatal("writable task material still exposes a third reading mode")
			}
			live.SetSelection(2, len("# 两模式"))
			tester.Frame()
			anchor, caret := live.Selection()
			version := buffer.Version()
			chooseMarkdownAcceptanceMode(t, tester, "source")
			assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
			if !live.SourceMode || !s.materialsUnit.state.documentEditing {
				t.Fatal("Source did not retain the writable document")
			}
			// An accepted reread of this exact document must retain the user's
			// mode, shared Buffer and selection rather than applying open defaults.
			action, result := writableMarkdownReadFixture(s, content)
			s.captureMaterialsMarkdownReadVersion(&action)
			s.applyMaterialsActionResult(action, result)
			tester.Frame()
			if s.materialsUnit.state.markdown != state || state.live != live || live.Buffer() != buffer || !live.SourceMode || !s.materialsUnit.state.documentEditing {
				t.Fatal("same-document refresh reset Source mode or replaced its editor")
			}
			assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
			chooseMarkdownAcceptanceMode(t, tester, "live")
			if live.SourceMode || !s.materialsUnit.state.documentEditing {
				t.Fatal("Live Preview did not restore the writable live surface")
			}
			assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
		})
	}
}

func TestMarkdownLiveStabilityAcceptanceMermaidActionsKeepSelection(t *testing.T) {
	const rawFence = "```mermaid\nflowchart LR\n    A[开始] --> B[完成]\n```"
	for _, width := range []int{620, 1100} {
		for _, dark := range []bool{false, true} {
			for _, action := range []string{"expand", "more", "expand-drag-cancel", "more-drag-cancel", "title-source", "body-source"} {
				t.Run(fmt.Sprintf("width-%d/dark-%v/%s", width, dark, action), func(t *testing.T) {
					newline := "\n"
					if dark {
						newline = "\r\n"
					}
					fence := strings.ReplaceAll(rawFence, "\n", newline)
					source := "# 按钮与源码" + newline + newline + fence + newline + newline + "尾段保留。" + newline
					s, _, _, _ := markdownLiveAcceptanceFixture(t, source, dark, width)
					tester := markdownStabilityAcceptanceTester(s, dark, width)
					state := focusMarkdownLiveAcceptance(t, s, tester)
					diagram := markdownStabilityAcceptanceDiagram(t, s, state, 240, 72)
					// A nonempty heading selection catches controls that collapse or
					// relocate selection even when they eventually open the viewer.
					state.live.SetSelection(2, len("# 按钮"))
					tester.Frame()
					live, buffer := state.live, state.live.Buffer()
					version := buffer.Version()
					anchor, caret := live.Selection()
					target := "放大查看"
					switch action {
					case "more", "more-drag-cancel":
						target = "更多图表操作"
					case "title-source":
						target = "Mermaid 图表"
					case "body-source":
						target = "Mermaid 图表预览"
					}
					bounds, ok := tester.Find(target)
					if !ok || bounds.W <= 0 || bounds.H <= 0 {
						t.Fatalf("production Mermaid target %q missing: %+v texts=%q", target, bounds, tester.Texts())
					}
					x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
					tester.Press(x, y)
					if strings.HasSuffix(action, "-source") {
						tester.Release(x, y)
						normalized := strings.ReplaceAll(source, "\r\n", "\n")
						start := strings.Index(normalized, rawFence)
						a, z := live.Selection()
						if a != z || a < start || a >= start+len(rawFence) || !tester.HasText(rawFence) {
							t.Fatalf("%s did not reveal complete original fence: selection=%d:%d texts=%q", action, a, z, tester.Texts())
						}
						if s.materialsUnit.state.markdownBitmapViewer != nil || tester.HasText("复制图表源码") || buffer.Version() != version {
							t.Fatal("title/body opened a control action or changed source")
						}
						assertMarkdownAcceptanceSource(t, s, state, source)
						return
					}
					assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
					if s.materialsUnit.state.markdownBitmapViewer != nil || tester.HasText("复制图表源码") {
						t.Fatal("Mermaid action fired on press before pointer release")
					}
					if strings.HasSuffix(action, "-drag-cancel") {
						// Stay inside the document but outside the control. The release
						// must cancel the button, without starting an editor drag.
						x, y = bounds.X-45, bounds.Y+bounds.H+45
						tester.Move(x, y)
					}
					tester.Release(x, y)
					assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
					if state.live != live || live.Buffer() != buffer {
						t.Fatal("control replaced the live editor or its Buffer")
					}
					switch action {
					case "expand":
						viewer := s.materialsUnit.state.markdownBitmapViewer
						if viewer == nil || viewer.owner != state || viewer.media.imageExpandedBitmap != diagram.bitmap || !viewer.media.imageExpanded {
							t.Fatal("Expand did not open the shared bitmap viewer with the existing render")
						}
						if _, ok := tester.Find("图片画布"); !ok {
							t.Fatal("Expand assigned viewer state but did not display the actual canvas")
						}
					case "more":
						if !tester.HasText("复制图表源码") || s.materialsUnit.state.markdownBitmapViewer != nil {
							t.Fatal("More did not display its source-copy menu")
						}
					default:
						if s.materialsUnit.state.markdownBitmapViewer != nil || tester.HasText("复制图表源码") {
							t.Fatal("dragging outside the Mermaid control did not cancel its action")
						}
					}
				})
			}
		}
	}
}

func markdownStabilityAcceptanceMarkers(tester *ui.Tester) []string {
	var markers []string
	for _, text := range tester.Texts() {
		text = strings.TrimSpace(text)
		if len(text) < 2 || text[len(text)-1] != '.' {
			continue
		}
		numeric := true
		for _, ch := range text[:len(text)-1] {
			if ch < '0' || ch > '9' {
				numeric = false
			}
		}
		if numeric {
			markers = append(markers, text)
		}
	}
	return markers
}

func markdownStabilityAcceptanceTextBounds(t *testing.T, tester *ui.Tester, labels ...string) map[string]ui.Rect {
	t.Helper()
	result := make(map[string]ui.Rect, len(labels))
	for _, label := range labels {
		bounds, ok := tester.Find(label)
		if !ok || bounds.W <= 0 || bounds.H <= 0 {
			t.Fatalf("text %q has no rendered bounds: %q", label, tester.Texts())
		}
		result[label] = bounds
	}
	return result
}

func TestMarkdownLiveStabilityAcceptanceListLiteralMarkersAndGeometry(t *testing.T) {
	const initial = "1. first\n2. second\n3. \n4. \n3. undo\n4. \n6.\n\n4. after"
	const want = "1. first\n2. second\n3. \n4. \n3. undo\n4. \n6.\n7\n4. after"
	for _, width := range []int{620, 1100} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width-%d/dark-%v", width, dark), func(t *testing.T) {
				s, _, _, tester := markdownLiveAcceptanceFixture(t, initial, dark, width)
				state := focusMarkdownLiveAcceptance(t, s, tester)
				at := strings.Index(initial, "\n\n4. after") + 1
				state.live.SetSelection(at, at)
				tester.Frame()
				before := markdownStabilityAcceptanceTextBounds(t, tester, "first", "second", "undo")
				tester.Type("7")
				assertMarkdownAcceptanceSource(t, s, state, want)
				// An ordered list can interrupt a paragraph only when it starts
				// at 1. Here 7 and the following 4. after share an ordinary
				// paragraph; only the preceding seven items have marker nodes.
				wantMarkers := []string{"1.", "2.", "3.", "4.", "3.", "4.", "6."}
				if got := markdownStabilityAcceptanceMarkers(tester); !reflect.DeepEqual(got, wantMarkers) {
					t.Fatalf("typing independent 7 renumbered/removed literal list markers: got=%q want=%q texts=%q", got, wantMarkers, tester.Texts())
				}
				if !tester.HasText("4. after") {
					t.Fatalf("typing independent 7 removed the ordinary paragraph tail: %q", tester.Texts())
				}
				after := markdownStabilityAcceptanceTextBounds(t, tester, "first", "second", "undo")
				for label, original := range before {
					if after[label] != original {
						t.Fatalf("typing below the list displaced %q: before=%+v after=%+v", label, original, after[label])
					}
				}
				version := state.live.Buffer().Version()
				for _, label := range []string{"first", "undo", "7"} {
					if label == "7" {
						// The last paragraph is one text run, not a standalone 7
						// node. Route its source position through the public API.
						state.live.SetSelection(at, at)
						tester.Frame()
					} else {
						clickMarkdownAcceptance(t, tester, label)
					}
					if state.live.Buffer().Version() != version || state.live.Buffer().OriginalText() != want {
						t.Fatalf("focus on %q rewrote literal source", label)
					}
					current := markdownStabilityAcceptanceTextBounds(t, tester, "first", "second", "undo")
					for text, original := range after {
						if got := current[text]; math.Abs(float64(got.Y-original.Y)) > .5 || math.Abs(float64(got.H-original.H)) > .5 || math.Abs(float64(got.X-original.X)) > .5 {
							t.Fatalf("focus on %q changed list geometry for %q: before=%+v after=%+v", label, text, original, got)
						}
					}
				}
				tester.Command("undo")
				assertMarkdownAcceptanceSource(t, s, state, initial)
			})
		}
	}
}

func TestMarkdownLiveStabilityAcceptanceImagesRetainedUntilLastReference(t *testing.T) {
	const first = "![第一处😀](shared.png)"
	const second = "![第二处](shared.png)"
	const source = "前文可编辑。\n\n" + first + "\n\n中间段落。\n\n" + second + "\n\n末尾。"
	s, _, _, tester := markdownLiveAcceptanceFixture(t, source, false, 1100)
	state := focusMarkdownLiveAcceptance(t, s, tester)
	state.live.SetSelection(0, 0)
	tester.Frame()
	if len(state.inlineImages) != 1 {
		t.Fatalf("duplicate live references did not share one resource: got %d resources", len(state.inlineImages))
	}
	var loaded *markdownInlineImageState
	for _, resource := range state.inlineImages {
		loaded = resource
	}
	if loaded.cancel != nil {
		loaded.cancel()
	}
	// This supplies the completed decode result without native worker timing.
	// No cache key is fabricated: the production image callback created it.
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 160, 48)))
	releases := 0
	loaded.bitmap, loaded.loading, loaded.error = bitmap, false, ""
	loaded.cancel = func() { releases++ }
	retained := func(stage string) {
		t.Helper()
		if len(state.inlineImages) != 1 || releases != 0 {
			t.Fatalf("%s replaced/released a referenced image: resources=%d releases=%d", stage, len(state.inlineImages), releases)
		}
		for _, resource := range state.inlineImages {
			if resource != loaded || resource.bitmap != bitmap || resource.loading {
				t.Fatalf("%s discarded the existing loaded bitmap", stage)
			}
		}
	}
	tester.Frame()
	retained("loaded render")
	tester.Type("新增前文😀 ")
	want := "新增前文😀 " + source
	assertMarkdownAcceptanceSource(t, s, state, want)
	retained("unrelated source-offset edit")
	chooseMarkdownAcceptanceMode(t, tester, "source")
	retained("Source mode")
	chooseMarkdownAcceptanceMode(t, tester, "live")
	retained("Live mode round trip")
	// Activating either image temporarily removes its bitmap from the rendered
	// runs, but a document reference still owns the completed decode result.
	at := strings.Index(want, first)
	state.live.SetSelection(at+2, at+2)
	tester.Frame()
	retained("first image source expansion")
	state.live.SetSelection(at, at+len(first))
	tester.Frame()
	tester.Key(0, ui.KeyBackspace)
	want = want[:at] + want[at+len(first):]
	assertMarkdownAcceptanceSource(t, s, state, want)
	retained("one duplicate removed")
	at = strings.Index(want, second)
	state.live.SetSelection(at, at+len(second))
	tester.Frame()
	tester.Key(0, ui.KeyBackspace)
	want = want[:at] + want[at+len(second):]
	assertMarkdownAcceptanceSource(t, s, state, want)
	if len(state.inlineImages) != 0 || releases != 1 {
		t.Fatalf("last reference removal did not release exactly once: resources=%d releases=%d", len(state.inlineImages), releases)
	}
}

// ST-009: inject the same field update used by a successful async Mermaid
// completion, after the user has scrolled to an editing caret below the diagram.
// This checks public viewport/caret geometry; it does not inspect MyGO's private
// scroll state or claim to cover OS worker scheduling and image decoding.
func TestMarkdownLiveStabilityAcceptanceAsyncDiagramResize(t *testing.T) {
	const fence = "```mermaid\nflowchart LR\nA --> B\n```"
	const anchorText = "编辑锚点保持在屏幕原位。"
	source := "# 异步尺寸验收\n\n" + fence + "\n\n" + strings.Repeat("图表下面的中间段落。\n\n", 18) + anchorText + "\n\n" + strings.Repeat("锚点下面的段落。\n\n", 12)
	for _, test := range []struct {
		name          string
		composing     bool
		initialHeight int
		heights       []int
	}{
		{name: "non-composing", initialHeight: 72, heights: []int{360, 48}},
		{name: "composition-grow", composing: true, initialHeight: 72, heights: []int{360}},
		{name: "composition-shrink", composing: true, initialHeight: 360, heights: []int{48}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, _, tester := markdownLiveAcceptanceFixture(t, source, false, 1100)
			state := focusMarkdownLiveAcceptance(t, s, tester)
			live, buffer := state.live, state.live.Buffer()
			diagram := markdownStabilityAcceptanceDiagram(t, s, state, 240, test.initialHeight)
			tester.Frame()
			at := strings.Index(source, anchorText) + len("编辑锚点")
			live.SetSelection(at, at)
			tester.Frame()
			viewport, ok := tester.Find("Markdown 编辑区")
			if !ok {
				t.Fatal("native editor viewport missing")
			}
			before, active := tester.TextCaret()
			if !active || before.Y < viewport.Y || before.Y+before.H > viewport.Y+viewport.H+1 {
				t.Fatalf("test did not reveal the editing caret: caret=%+v viewport=%+v", before, viewport)
			}
			// Move the visible caret away from the viewport edge so the assertion
			// cannot pass merely because ordinary reveal clamps to the same edge.
			tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, before.Y-(viewport.Y+viewport.H/2))
			before, active = tester.TextCaret()
			if !active || before.Y <= viewport.Y+20 || before.Y+before.H >= viewport.Y+viewport.H-20 {
				t.Fatalf("test did not place the editing caret inside the viewport: caret=%+v viewport=%+v", before, viewport)
			}
			version := buffer.Version()
			anchor, caret := live.Selection()
			if test.composing {
				// ST-013: settle the preedit before changing the resource. This is
				// asynchronous layout movement, not a candidate-text update.
				tester.Compose("候选", 2)
				tester.Frame()
				before, active = tester.TextCaret()
				assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
				if !active || !live.Composing() || !tester.HasText("候选") || before.Y <= viewport.Y+20 || before.Y+before.H >= viewport.Y+viewport.H-20 {
					t.Fatalf("stable preedit caret is not inside the viewport: caret=%+v viewport=%+v composing=%v", before, viewport, live.Composing())
				}
			}
			for _, height := range test.heights {
				diagram.bitmap = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 240, height)))
				tester.Frame()
				after, active := tester.TextCaret()
				assertMarkdownStabilityUnchanged(t, s, state, source, version, anchor, caret)
				if state.live != live || live.Buffer() != buffer || live.Composing() != test.composing || test.composing && !tester.HasText("候选") {
					t.Fatal("async resize replaced the editor or changed its composition")
				}
				t.Logf("ST-009/ST-013 async bitmap height=%d composing=%v caret before=%+v after=%+v viewport=%+v", height, test.composing, before, after, viewport)
				if !active || math.Abs(float64(after.Y-before.Y)) > 1 || math.Abs(float64(after.X-before.X)) > 1 {
					t.Fatalf("async diagram resize displaced the editing viewport: height=%d composing=%v before=%+v after=%+v", height, test.composing, before, after)
				}
			}
		})
	}
}
