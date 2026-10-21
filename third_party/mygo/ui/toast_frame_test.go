package ui

import (
	"strconv"
	"testing"
)

func TestToastAddedAfterCustomViewportAppearsWithoutAnotherEvent(t *testing.T) {
	for _, additions := range []int{1, 4} {
		t.Run(strconv.Itoa(additions), func(t *testing.T) {
			remaining := 0
			tester := NewTester(func(c *Context) {
				ToastViewportBase(c, func(viewport Element, toasts []Toast) {
					viewport.Children(func() {
						for _, toast := range toasts {
							Text(c, toast.Title)
						}
					})
				})
				if remaining > 0 {
					remaining--
					c.AddToast(Toast{ID: "status", Title: "Saved " + strconv.Itoa(remaining), Timeout: -1})
				}
			}, 320, 240)
			remaining = additions
			tester.Frame()
			if !tester.HasText("Saved 0") {
				t.Fatalf("a toast added after its viewport waits for another event: remaining=%d frame=%d pass=%d requested=%v texts=%q", remaining, tester.rt.frame, tester.rt.pass, tester.h.requested.Load(), tester.Texts())
			}
			if tester.h.requested.Load() {
				t.Fatal("a settled toast keeps rebuilding idle frames")
			}
		})
	}
}

func TestClosingMissingToastDoesNotRequestFrames(t *testing.T) {
	tester := NewTester(func(c *Context) { c.CloseToast("missing") }, 320, 240)
	if tester.h.requested.Load() || tester.rt.pass != 0 {
		t.Fatal("closing a missing toast keeps rebuilding idle frames")
	}
}
