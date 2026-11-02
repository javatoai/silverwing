package markdownui

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

func TestReaderFindArrowClicksWrapBeforeSourceAndRenderedLookups(t *testing.T) {
	for _, mode := range []Mode{Preview, Source} {
		for _, count := range []int{0, 1, 3} {
			t.Run(fmt.Sprintf("mode-%d/matches-%d", mode, count), func(t *testing.T) {
				source := "no matching text"
				if count > 0 {
					source = strings.TrimSpace(strings.Repeat("needle ", count))
				}
				state := NewReaderState(Document{ID: "find-arrows", Source: source})
				defer state.Close()
				state.SetMode(mode)
				state.Find().Query, state.Find().Open = "needle", true
				view := ui.NewTester(func(c *ui.Context) { MarkdownReader(c, state, ReaderOptions{}).Fill() }, 600, 320)
				want := 0
				for _, arrow := range []string{"上一个", "下一个", "下一个", "下一个", "下一个", "上一个"} {
					if count > 0 {
						delta := 1
						if arrow == "上一个" {
							delta = -1
						}
						want = (want + delta + count) % count
					}
					if err := view.Click(arrow); err != nil {
						t.Fatal(err)
					}
					if got := state.Find().Current; got != want {
						t.Fatalf("%s current=%d, want %d", arrow, got, want)
					}
					if mode == Source && count > 0 {
						selection := state.sourceView.Selection()
						if selection.Anchor != want*len("needle ") || state.sourceView.SelectedText() != "needle" {
							t.Fatalf("source arrow did not select its actual match: %+v, %q", selection, state.sourceView.SelectedText())
						}
					}
				}
				// Hosts can mutate Current directly between frames as well.
				for _, current := range []int{-100, 100} {
					state.Find().Current = current
					view.Frame()
					if count == 0 && state.Find().Current != 0 || count > 0 && (state.Find().Current < 0 || state.Find().Current >= count) {
						t.Fatalf("invalid direct current survived: %d", state.Find().Current)
					}
				}
			})
		}
	}
}

func TestSharedReaderFindArrowRevealsDestinationViewport(t *testing.T) {
	for _, mode := range []Mode{Preview, Source} {
		t.Run(fmt.Sprintf("destination-mode-%d", mode), func(t *testing.T) {
			first := NewReaderState(Document{ID: "first", Source: strings.Repeat("first filler\n\n", 60) + "first needle"})
			defer first.Close()
			second := NewReaderState(Document{ID: "second", Source: strings.Repeat("filler paragraph\n\n", 60) + "destination needle"})
			defer second.Close()
			second.SetMode(mode)
			find := &FindController{Query: "needle", Open: true}
			var firstBounds, secondBounds ui.Rect
			view := ui.NewTester(func(c *ui.Context) {
				ui.Row(c).Fill().Children(func() {
					MarkdownReader(c, first, ReaderOptions{Find: find}).Grow(1).Draw(func(_ *ui.Painter, r ui.Rect) { firstBounds = r })
					MarkdownReader(c, second, ReaderOptions{Find: find}).Grow(1).Draw(func(_ *ui.Painter, r ui.Rect) { secondBounds = r })
				})
			}, 900, 360)
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			if find.Current != 1 {
				t.Fatalf("shared next did not select destination: %d", find.Current)
			}
			if mode == Source {
				if second.sourceView.SelectedText() != "needle" || second.sourceView.ScrollY() <= 0 {
					t.Fatalf("destination source did not reveal: selected=%q, scroll=%v", second.sourceView.SelectedText(), second.sourceView.ScrollY())
				}
			} else if second.scroll.Y <= 0 {
				t.Fatal("destination reader did not scroll to its shared match")
			}
			if find.reveal {
				t.Fatal("destination did not consume the shared reveal request")
			}
			// The second reader's Previous button targets a reader already built
			// earlier in the frame. It must schedule that reader's reveal too.
			first.scroll.Y = 0
			view.Frame()
			previous, ok := view.Find("上一个")
			if !ok {
				t.Fatal("missing previous button")
			}
			view.ClickAt(previous.X-firstBounds.X+secondBounds.X+previous.W/2, previous.Y+previous.H/2)
			if find.Current != 0 || first.scroll.Y <= 0 || find.reveal {
				t.Fatalf("earlier destination was not revealed: current=%d scroll=%v pending=%t", find.Current, first.scroll.Y, find.reveal)
			}
		})
	}
}

func TestReaderZoomResolvesDefaultAndPartialThemeWithoutMutatingHost(t *testing.T) {
	for _, kind := range []string{"default", "partial", "native-partial", "explicit"} {
		t.Run(kind, func(t *testing.T) {
			state := NewReaderState(Document{ID: "zoom", Source: "zoomed body"})
			defer state.Close()
			partial := &md.Theme{Link: ui.Hex("#a12345"), Text: ui.Hex("#163452")}
			options := ReaderOptions{}
			baseSize := float32(20)
			switch kind {
			case "partial":
				options.Theme = partial
			case "native-partial":
				options.Native = &md.Options{Theme: partial}
			case "explicit":
				partial.FontSize = 18
				baseSize, options.Theme = 18, partial
			}
			original := *partial
			view := ui.NewTester(func(c *ui.Context) {
				theme := *c.Theme()
				theme.FontSize = 20
				c.SetTheme(&theme)
				MarkdownReader(c, state, options).Fill()
			}, 600, 320)
			before, ok := view.Find("zoomed body")
			if !ok {
				t.Fatal("missing zoom fixture")
			}
			view.ScrollWith(ui.Ctrl, 100, 100, 0, -20)
			after, _ := view.Find("zoomed body")
			if state.zoom <= 1 || state.native.Theme == nil || math.Abs(float64(state.native.Theme.FontSize-baseSize*state.zoom)) > .01 || after.H <= before.H {
				t.Fatalf("zoom did not scale resolved font: zoom=%v theme=%+v before=%+v after=%+v", state.zoom, state.native.Theme, before, after)
			}
			if *partial != original {
				t.Fatal("zoom mutated the host theme")
			}
			if kind != "default" && (state.native.Theme.Link != partial.Link || state.native.Theme.Text != partial.Text) {
				t.Fatal("zoom discarded the host palette")
			}
		})
	}
}

func TestReaderSourceZoomPrecedesNativeScrollAndPersistsIntoPreview(t *testing.T) {
	source := strings.Repeat("read-only source line\r\n", 160)
	state := NewReaderState(Document{ID: "source-zoom", Source: source})
	defer state.Close()
	state.SetMode(Source)
	view := ui.NewTester(func(c *ui.Context) {
		theme := *c.Theme()
		theme.FontSize = 20
		c.SetTheme(&theme)
		MarkdownReader(c, state, ReaderOptions{}).Fill()
	}, 650, 350)
	state.sourceView.SetScrollY(100)
	view.Frame()
	if state.sourceView.ScrollY() <= 0 {
		t.Fatal("fixture did not reach a scrollable native source editor")
	}
	view.ScrollWith(ui.Ctrl, 100, 100, 0, 20)
	if math.Abs(float64(state.zoom-.9)) > .01 || math.Abs(float64(state.sourceView.Font.Size-14*.9)) > .01 {
		t.Fatalf("native scrolling consumed Ctrl-wheel before zoom: zoom=%v font=%v", state.zoom, state.sourceView.Font.Size)
	}
	beforeScroll := state.sourceView.ScrollY()
	view.ScrollWith(0, 100, 100, 0, 40)
	if state.sourceView.ScrollY() <= beforeScroll || math.Abs(float64(state.zoom-.9)) > .01 {
		t.Fatalf("ordinary source scrolling changed: before=%v after=%v zoom=%v", beforeScroll, state.sourceView.ScrollY(), state.zoom)
	}
	state.SetMode(Preview)
	view.Frame()
	if state.native.Theme == nil || math.Abs(float64(state.native.Theme.FontSize-18)) > .01 {
		t.Fatalf("source zoom did not persist into the resolved preview font: %+v", state.native.Theme)
	}
	if state.Snapshot().Source != source {
		t.Fatal("source zoom or scrolling rewrote document bytes")
	}
}

func TestReaderFillsRowCrossAxisOnlyForFullComponent(t *testing.T) {
	for _, contentOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("content-only-%t", contentOnly), func(t *testing.T) {
			state := NewReaderState(Document{ID: "row", Source: "short body"})
			defer state.Close()
			var bounds ui.Rect
			view := ui.NewTester(func(c *ui.Context) {
				ui.Row(c).Fill().Children(func() {
					MarkdownReader(c, state, ReaderOptions{ContentOnly: contentOnly}).Grow(1).Draw(func(_ *ui.Painter, r ui.Rect) { bounds = r })
				})
			}, 600, 400)
			if contentOnly {
				if bounds.H >= 100 {
					t.Fatalf("embedded reader expanded its host's content height: %+v", bounds)
				}
			} else {
				body, ok := view.Find("short body")
				if bounds.H < 399 || !ok || body.Y > 20 {
					t.Fatalf("full reader did not fill and top-align in a Row: reader=%+v body=%+v", bounds, body)
				}
			}
		})
	}
}
