package ui

import "testing"

// A box with nothing in it is as high as its padding at every width:
// contentHeight returns 0 for it, and naturalHeight adds the padding.
// naturalHeight returns that before it measures the box and keeps what it
// measured, which cannot change with the width.
func TestLeafHeightIsItsPadding(t *testing.T) {
	var leaf *node
	coreNewTester(func(c *context) {
		leaf = coreBox(c).Padding(7)
	}, 100, 100)
	for _, w := range []float32{1, 10, 100, 1000} {
		if h := contentHeight(leaf, w); h != 0 {
			t.Errorf("contentHeight of a box with nothing in it is %v at width %v, want 0", h, w)
		}
		if h := naturalHeight(leaf, w, 0); h != leaf.padY() {
			t.Errorf("naturalHeight of a box with nothing in it is %v at width %v, and its padding is %v",
				h, w, leaf.padY())
		}
	}
}

// The leaves a wrapping row measures are not kept in measures: their
// height cannot change with the width, so nothing is there to keep. The
// containers around them are measured as they were.
func TestLeafIsNotMeasured(t *testing.T) {
	var root *node
	coreNewTester(func(c *context) {
		root = coreColumn(c).Size(300, 400).Children(func() {
			coreRow(c).Width(280).Wrap().Children(func() {
				for range 8 {
					coreBox(c).Width(60).Shrink(0)
				}
				coreBox(c).WidthPercent(50).Shrink(0)
			})
		})
	}, 300, 400)
	forgetLayout(root)
	layoutBox(root, root.w, root.h)
	var walk func(e *node)
	walk = func(e *node) {
		for ; e != nil; e = e.next {
			if e.kind == kindBox && e.first == nil && e.list == nil && e.nmeasure != 0 {
				t.Errorf("a box with nothing in it kept %d measurements, and its height cannot change with the width",
					e.nmeasure)
			}
			walk(e.first)
		}
	}
	walk(root)
}
