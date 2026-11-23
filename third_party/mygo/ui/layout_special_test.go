package ui

import (
	"fmt"
	"strings"
	"testing"
)

// The layout looks for the list, the toolbar, the form and the column fit
// of every element it lays out, and the special field tells it that there
// are none to look for: it must say what the element has.
func TestSpecialTellsWhatLaysOut(t *testing.T) {
	var root *node
	coreNewTester(func(c *context) {
		root = coreColumn(c).Children(func() {
			coreList(c, &ListState{}, 3, func(i int) {
				coreText(c, "row")
			})
			coreForm(c, func() {
				coreField(c, "label", func() {
					coreText(c, "value")
				})
			})
			coreToolbar(c, func() {
				coreButton(c, "ok")
			})
			coreTable(c, &ListState{}, []TableColumn{{Title: "Name"}}, 2, func(row, col int) {
				coreText(c, "cell")
			})
			coreGridView(c, &GridState{}, 4, 100, 40, func(i int) {
				coreText(c, "item")
			})
			coreBox(c)
		})
	}, 400, 400)
	var walk func(e *node)
	walk = func(e *node) {
		for ; e != nil; e = e.next {
			want := e.list != nil || e.overflow != nil || e.form != nil ||
				e.colFit != nil || e.gridFit != nil
			if e.special != want {
				t.Errorf("special is %v, and the element has %v", e.special, want)
			}
			walk(e.first)
		}
	}
	walk(root)
}

// The special field only tells the layout where to look: an element that
// says it lays out by more than it is built with, but has none of those,
// lays out as it does without saying so.
func TestSpecialChangesNothing(t *testing.T) {
	var root *node
	coreNewTester(func(c *context) {
		root = coreColumn(c).Size(300, 400).Children(func() {
			coreRow(c).Width(280).Children(func() {
				coreBox(c).Size(40, 20)
				coreText(c, "hello")
			})
			coreBox(c).Grow(1).Children(func() {
				coreText(c, "world")
			})
		})
	}, 300, 400)
	boxes := func() [][4]float32 {
		forgetLayout(root)
		layoutBox(root, root.w, root.h)
		return specialBoxes(root)
	}
	without := boxes()
	var walk func(e *node)
	walk = func(e *node) {
		for ; e != nil; e = e.next {
			e.special = true
			walk(e.first)
		}
	}
	walk(root)
	with := boxes()
	if len(with) != len(without) {
		t.Fatalf("%d boxes with the field set, %d without", len(with), len(without))
	}
	for i := range with {
		if with[i] != without[i] {
			t.Errorf("box %d is %v, and %v without the field",
				i, specialBoxString(with[i]), specialBoxString(without[i]))
			break
		}
	}
}

// specialBoxes returns the box of every element under root, in the order
// the layout laid them out.
func specialBoxes(e *node) [][4]float32 {
	var out [][4]float32
	var walk func(*node)
	walk = func(e *node) {
		for c := e; c != nil; c = c.next {
			out = append(out, [4]float32{c.x, c.y, c.w, c.h})
			walk(c.first)
		}
	}
	walk(e)
	return out
}

func specialBoxString(b [4]float32) string {
	s := make([]string, len(b))
	for i, v := range b {
		s[i] = fmt.Sprintf("%v", v)
	}
	return "{" + strings.Join(s, " ") + "}"
}
