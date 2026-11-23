package ui

import (
	"slices"
	"testing"
	"time"
)

// endList is a list of rows 20 DIPs high, 20 in view in a window 400 high,
// whose EndReached it counts.
type endList struct {
	items   []int
	list    ListState
	reached []int
}

func newEndList(n int) *endList {
	l := &endList{}
	for i := range n {
		l.items = append(l.items, i)
	}
	l.list.Key = func(i int) any { return l.items[i] }
	l.list.NearEnd = 10
	l.list.EndReached = func(last int) { l.reached = append(l.reached, last) }
	return l
}

func (l *endList) view(c *Context) {
	List(c, &l.list, len(l.items), func(i int) {
		Box(c).Height(20)
	}).Grow(1)
}

// wheel turns the mouse wheel over the list by notches of dy, and lets
// each glide there.
func wheel(tt *Tester, notches int, dy float32) {
	tt.Move(100, 100)
	for range notches {
		tt.Wheel(100, 100, 0, dy)
		tt.Advance(time.Second)
	}
}

func TestListEndReachedWheel(t *testing.T) {
	l := newEndList(100)
	tt := NewTester(l.view, 300, 400)
	if len(l.reached) != 0 {
		t.Fatalf("showing the first rows: called with %v", l.reached)
	}
	// 1400 DIPs down shows rows 70 to 89: the 89th is 10 from the end.
	wheel(tt, 13, 100)
	if _, last := l.list.Visible(); len(l.reached) != 0 || last >= 89 {
		t.Fatalf("rows to %d in view: called with %v", last, l.reached)
	}
	// It is called with the last row in view in the frame the rows came
	// near the end; the glide may go a fraction of a row further after
	// (as on arm64, whose fused multiply-adds round the glide otherwise).
	wheel(tt, 1, 100)
	if _, last := l.list.Visible(); len(l.reached) != 1 || last < 89 || l.reached[0] < 89 || l.reached[0] > last {
		t.Fatalf("rows to %d in view, near the end: called with %v, want once with a row from 89 to it", last, l.reached)
	}
	// Further on to the end, and frames while the rows stay there, call it
	// no more.
	wheel(tt, 5, 100)
	for range 5 {
		tt.Frame()
	}
	if !l.list.AtEnd() || len(l.reached) != 1 {
		t.Fatalf("at the end %v: called with %v, want once", l.list.AtEnd(), l.reached)
	}
	// Reordering the same rows does not either.
	slices.Reverse(l.items[90:])
	tt.Frame()
	if len(l.reached) != 1 {
		t.Errorf("rows reordered: called with %v, want once", l.reached)
	}
	// Away from the end and back, it calls it again.
	wheel(tt, 5, -100)
	wheel(tt, 5, 100)
	if len(l.reached) != 2 {
		t.Errorf("away from the end and back: called with %v, want twice", l.reached)
	}
}

func TestListEndReachedAfterLoading(t *testing.T) {
	l := newEndList(100)
	// The rows load as the frames after it are built, as from another
	// goroutine.
	load := false
	l.list.EndReached = func(last int) {
		l.reached = append(l.reached, last)
		load = true
	}
	tt := NewTester(func(c *Context) {
		if load {
			load = false
			for range 100 {
				l.items = append(l.items, len(l.items))
			}
		}
		l.view(c)
	}, 300, 400)
	wheel(tt, 20, 100)
	if len(l.reached) != 1 || len(l.items) != 200 {
		t.Fatalf("near the end: called with %v, %d rows", l.reached, len(l.items))
	}
	// On to the end of the rows loaded: called again, near the new end.
	for k := 0; k < 40 && len(l.reached) < 2; k++ {
		wheel(tt, 1, 100)
	}
	if len(l.reached) != 2 || l.reached[1] < 189 || l.reached[1] > 199 || len(l.items) != 300 {
		t.Fatalf("near the end of the rows loaded: called with %v, %d rows", l.reached, len(l.items))
	}
	for range 5 {
		tt.Frame()
	}
	if len(l.reached) != 2 {
		t.Errorf("frames after: called with %v, want twice", l.reached)
	}
}

func TestListEndReachedNotByTheApp(t *testing.T) {
	l := newEndList(100)
	tt := NewTester(l.view, 300, 400)
	l.list.ScrollTo(95, Start)
	tt.Frame()
	if _, last := l.list.Visible(); last != 99 || len(l.reached) != 0 {
		t.Fatalf("ScrollTo near the end, rows to %d in view: called with %v", last, l.reached)
	}
	l.list.ScrollTo(0, Start)
	tt.Frame()
	l.list.ScrollIntoView(95)
	tt.Frame()
	l.list.ScrollTo(0, Start)
	tt.Frame()
	l.list.ScrollToEnd()
	tt.Frame()
	if !l.list.AtEnd() || len(l.reached) != 0 {
		t.Fatalf("ScrollIntoView and ScrollToEnd near the end: called with %v", l.reached)
	}
	// The user scrolling from there, still near the end, brings the rows
	// there: it calls it.
	wheel(tt, 1, -100)
	if len(l.reached) != 1 {
		t.Errorf("the wheel near the end after ScrollToEnd: called with %v, want once", l.reached)
	}
}

func TestListEndReachedNotByResizing(t *testing.T) {
	l := newEndList(100)
	banner := true
	tt := NewTester(func(c *Context) {
		Column(c).Grow(1).Children(func() {
			if banner {
				Box(c).Height(300)
			}
			l.view(c)
		})
	}, 300, 400)
	// Rows to 64 in view, 20 to 64 above the banner closing.
	wheel(tt, 6, 100)
	if _, last := l.list.Visible(); last >= 89 {
		t.Fatalf("rows to %d in view", last)
	}
	banner = false
	tt.Frame()
	tt.SetSize(300, 2400)
	tt.Frame()
	if _, last := l.list.Visible(); last != 99 || len(l.reached) != 0 {
		t.Fatalf("the list rebuilt taller, rows to %d in view: called with %v", last, l.reached)
	}
	// A list whose rows fit it from the start is not called either.
	short := newEndList(5)
	NewTester(short.view, 300, 400)
	if len(short.reached) != 0 {
		t.Errorf("five rows: called with %v", short.reached)
	}
}

func TestListEndReachedByTheKeys(t *testing.T) {
	l := newEndList(100)
	sel := 0
	l.list.Selected = &sel
	tt := NewTester(l.view, 300, 400)
	tt.ClickAt(100, 10) // the list takes the focus
	tt.Key(0, KeyEnd)
	if sel != 99 || len(l.reached) != 1 {
		t.Errorf("End chose row %d: called with %v, want once", sel, l.reached)
	}
}
