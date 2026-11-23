package ui

import (
	"slices"
	"testing"
	"time"
)

// crossfade is a keyed text that fades out, over 200 ms, when its text
// changes.
func crossfade(c *context, s string) {
	coreText(c, s).Key(s).Transition(ElementTransition{Exit: &Motion{Opacity: 0}, Duration: 200 * time.Millisecond})
}

// What is inert, the copy of a text fading out, gives nothing to a name
// from content, as the accessibility tree and what live regions read
// leave it out too.
func TestAccessNameSkipsInertStatus(t *testing.T) {
	text := "Thinking"
	tt := coreNewTester(func(c *context) {
		coreBox(c).Role(RoleStatus).Children(func() { crossfade(c, text) })
	}, 300, 100)
	tt.UseScreenReader()
	text = "Done"
	tt.Frame()
	tt.Advance(50 * time.Millisecond)
	if n, ok := tt.AccessByRole(RoleStatus, ""); !ok || n.Name != "Done" {
		t.Errorf("mid-exit, the status is named %q (%v), want Done", n.Name, ok)
	}
	if !slices.Equal(tt.Announcements(), []string{"Done"}) {
		t.Errorf("announced %q, want Done once", tt.Announcements())
	}
	tt.Advance(300 * time.Millisecond)
	if n, ok := tt.AccessByRole(RoleStatus, ""); !ok || n.Name != "Done" {
		t.Errorf("after the exit, the status is named %q (%v), want Done", n.Name, ok)
	}
	if got := tt.Announcements(); len(got) != 0 {
		t.Errorf("announced %q after the exit", got)
	}
}

func TestAccessNameSkipsInertButton(t *testing.T) {
	text := "Save"
	tt := coreNewTester(func(c *context) {
		coreButtonBase(c).Children(func() { crossfade(c, text) })
	}, 300, 100)
	text = "Saving"
	tt.Frame()
	tt.Advance(50 * time.Millisecond)
	if n, ok := tt.AccessByRole(RoleButton, ""); !ok || n.Name != "Saving" {
		t.Errorf("mid-exit, the button is named %q (%v), want Saving", n.Name, ok)
	}
}

// A page a Router is leaving is inert too: a region around the router is
// not named by it.
func TestAccessNameSkipsLeavingPage(t *testing.T) {
	r := NewRouter("/notes")
	tt := coreNewTester(func(c *context) {
		coreBox(c).Role(RoleStatus).Children(func() {
			r.coreView(c, func(rt *Route) { coreText(c, "Page "+rt.Path()) })
		})
	}, 400, 300)
	tt.Frame()
	r.Push("/notes/1")
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	if !hasText(tt.rt.c.root, "Page /notes") {
		t.Fatal("the page leaving is gone already")
	}
	if n, ok := tt.AccessByRole(RoleStatus, ""); !ok || n.Name != "Page /notes/1" {
		t.Errorf("with a page leaving, the region is named %q (%v), want Page /notes/1", n.Name, ok)
	}
}

// hasText reports whether a text s is in the tree under e.
func hasText(e *node, s string) bool {
	if e.kind == kindText && e.text == s {
		return true
	}
	for ch := e.first; ch != nil; ch = ch.next {
		if hasText(ch, s) {
			return true
		}
	}
	return false
}
