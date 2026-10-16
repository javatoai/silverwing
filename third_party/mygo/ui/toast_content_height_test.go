package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestToastContentMaxHeight(t *testing.T) {
	for _, size := range [][2]int{{360, 320}, {1000, 700}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			body := "Copied:\n\n" + strings.Repeat("synthetic-service-very-long-name/branch · 1.2.3.beta-1\n", 50) + "\nPlease send to: Synthetic QA"
			var ctx *context
			closed := false
			tt, now := clockTester(func(c *context) { ctx = c }, size[0], size[1])
			ctx.AddToast(Toast{ID: "bounded", Title: body, ContentMaxHeight: 360, Timeout: 4 * time.Second, Action: "Close", OnClose: func() { closed = true }})
			tt.Frame()
			findContent := func() (scroller, text *node) {
				var walk func(*node)
				walk = func(n *node) {
					if n.flags&flagScrollY != 0 {
						scroller = n
					}
					if n.text == body {
						text = n
					}
					for child := n.first; child != nil; child = child.next {
						walk(child)
					}
				}
				walk(tt.rt.c.root)
				return
			}
			scroll, text := findContent()
			if scroll == nil || text == nil || text.text != body || text.single || text.noWrap {
				t.Fatal("opt-in toast must retain and wrap all source text in a native Scroll")
			}
			r := scroll.Bounds()
			maxHeight := min(float32(360), float32(size[1])-ctx.theme.Space(18))
			close, found := tt.Find("Close")
			if r.H > maxHeight || r.Y < 0 || r.Y+r.H > float32(size[1])-24 || !found || close.X < r.X+r.W || close.Y < 0 || close.Y+close.H > float32(size[1]) {
				t.Fatalf("bounded scroll and independent action escaped: scroll=%+v close=%+v", r, close)
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 100000)
			scroll, text = findContent()
			_, maxY := scroll.maxScroll()
			last := text.Bounds()
			if maxY <= 0 || scroll.st.scrollY != maxY || last.Y+last.H > r.Y+r.H+1 || last.Y+last.H < r.Y+r.H-2 {
				t.Fatalf("native scroll did not expose the final recipient line: offset=%v max=%v text=%+v viewport=%+v", scroll.st.scrollY, maxY, last, r)
			}
			// Reading the whole body keeps the original lifetime paused; moving
			// outside resumes it and the original OnClose runs on expiry.
			*now = now.Add(10 * time.Second)
			tt.Frame()
			if closed {
				t.Fatal("hovering the scrolling toast did not pause its lifetime")
			}
			tt.Move(0, 0)
			*now = now.Add(4 * time.Second)
			tt.Frame()
			if !closed {
				t.Fatal("original toast expiry/OnClose did not resume")
			}
		})
	}
}
