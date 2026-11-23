package platform

import "testing"

// Lines end after their line feeds, in UTF-16 units: a character outside
// the Basic Multilingual Plane takes two.
func TestAccessLines(t *testing.T) {
	const value = "ab\n😀c\n\nlast" // lines at units 0, 3, 7 and 8
	for _, c := range []struct{ u, line int }{
		{0, 0}, {2, 0}, {3, 1}, {4, 1}, {6, 1}, {7, 2}, {8, 3}, {11, 3}, {12, 3}, {99, 3},
	} {
		if got := LineOfUnit(value, c.u); got != c.line {
			t.Errorf("LineOfUnit(%d) = %d, want %d", c.u, got, c.line)
		}
	}
	for _, c := range []struct {
		line, start, n int
		ok             bool
	}{
		{0, 0, 3, true}, {1, 3, 4, true}, {2, 7, 1, true}, {3, 8, 4, true}, {4, 0, 0, false}, {-1, 0, 0, false},
	} {
		start, n, ok := LineUnits(value, c.line)
		if ok != c.ok || ok && (start != c.start || n != c.n) {
			t.Errorf("LineUnits(%d) = %d, %d, %v, want %d, %d, %v", c.line, start, n, ok, c.start, c.n, c.ok)
		}
	}
	// Every unit is on the line whose range holds it.
	for u := range 12 {
		start, n, _ := LineUnits(value, LineOfUnit(value, u))
		if u < start || u >= start+n {
			t.Errorf("unit %d is outside its line's %d+%d", u, start, n)
		}
	}
	// A text ending with a line feed has an empty last line, and an
	// empty text one line.
	if start, n, ok := LineUnits("a\n", 1); !ok || start != 2 || n != 0 {
		t.Errorf("the line after a final line feed is %d+%d, %v", start, n, ok)
	}
	if start, n, ok := LineUnits("", 0); !ok || start != 0 || n != 0 {
		t.Errorf("an empty text's line is %d+%d, %v", start, n, ok)
	}
	if LineOfUnit("a\n", 2) != 1 {
		t.Error("the end of a text ending with a line feed is not on its last line")
	}
}

// A range is where the boxes of its lines are, and the node's bounds where
// no line of it shows.
func TestAccessRangeBounds(t *testing.T) {
	n := AccessNode{
		Value:  "a\nb\nc\nd",
		Bounds: RectF{0, 0, 100, 400},
		LineBoxes: []AccessLineBox{
			{Line: 1, Bounds: RectF{10, 20, 80, 20}},
			{Line: 2, Bounds: RectF{10, 40, 80, 40}}, // wrapped in two rows
		},
	}
	for _, c := range []struct {
		u, n int
		want RectF
	}{
		{2, 1, RectF{10, 20, 80, 20}}, // b
		{2, 2, RectF{10, 20, 80, 20}}, // b and its line feed
		{2, 3, RectF{10, 20, 80, 60}}, // into c
		{4, 0, RectF{10, 40, 80, 40}}, // the caret before c
		{0, 3, RectF{10, 20, 80, 20}}, // a, out of view, and b
		{0, 1, n.Bounds},              // a alone
		{6, 1, n.Bounds},              // d
	} {
		if got := RangeBounds(&n, c.u, c.n); got != c.want {
			t.Errorf("RangeBounds(%d, %d) = %v, want %v", c.u, c.n, got, c.want)
		}
	}
	none := AccessNode{Value: "a\nb", Bounds: RectF{1, 2, 3, 4}}
	if got := RangeBounds(&none, 2, 1); got != none.Bounds {
		t.Errorf("a field without line boxes gives %v for a range, want its bounds", got)
	}
}

// A run of lines collapsed under one box is found by any of its lines.
func TestAccessRangeBoundsCollapsed(t *testing.T) {
	n := AccessNode{
		Value:  "a\nb\nc\nd\ne",
		Bounds: RectF{0, 0, 100, 400},
		LineBoxes: []AccessLineBox{
			{Line: 0, Bounds: RectF{10, 0, 80, 20}},
			{Line: 1, Count: 3, Bounds: RectF{0, 20, 100, 32}}, // b to d, under a separator
			{Line: 4, Bounds: RectF{10, 52, 80, 20}},
		},
	}
	for _, c := range []struct {
		u, n int
		want RectF
	}{
		{2, 1, RectF{0, 20, 100, 32}}, // b
		{6, 1, RectF{0, 20, 100, 32}}, // d
		{8, 1, RectF{10, 52, 80, 20}}, // e
		{0, 3, RectF{0, 0, 100, 52}},  // a and b
	} {
		if got := RangeBounds(&n, c.u, c.n); got != c.want {
			t.Errorf("RangeBounds(%d, %d) = %v, want %v", c.u, c.n, got, c.want)
		}
	}
}

// A selection moving as the keys move it says how, as VoiceOver reads it:
// a line for Up and Down, a character for Left and Right.
func TestSelectionChange(t *testing.T) {
	const value = "one two\nthree\nfour\nfive" // lines at 0, 8, 14 and 19
	for _, c := range []struct {
		name                  string
		oldStart, oldEnd      int
		start, end            int
		typ, dir, granularity int
	}{
		{"down", 2, 2, 10, 10, TextChangeMove, TextDirectionNext, TextGranularityLine},
		{"up", 10, 10, 2, 2, TextChangeMove, TextDirectionPrevious, TextGranularityLine},
		{"down to an empty end", 15, 15, 19, 19, TextChangeMove, TextDirectionNext, TextGranularityLine},
		{"right", 2, 2, 3, 3, TextChangeMove, TextDirectionNext, TextGranularityCharacter},
		{"left over a line feed", 8, 8, 7, 7, TextChangeMove, TextDirectionPrevious, TextGranularityLine},
		{"a word", 0, 0, 3, 3, TextChangeMove, TextDirectionNext, TextGranularityWord},
		{"home", 12, 12, 8, 8, TextChangeMove, TextDirectionBeginning, TextGranularityLine},
		{"end", 9, 9, 13, 13, TextChangeMove, TextDirectionEnd, TextGranularityLine},
		{"the beginning", 16, 16, 0, 0, TextChangeMove, TextDirectionBeginning, TextGranularityDocument},
		{"the end", 2, 2, 23, 23, TextChangeMove, TextDirectionEnd, TextGranularityDocument},
		{"a click", 2, 2, 16, 16, TextChangeMove, TextDirectionDiscontiguous, TextGranularityUnknown},
		{"shift down", 2, 2, 2, 10, TextChangeExtend, TextDirectionNext, TextGranularityLine},
		{"shift left", 5, 5, 4, 5, TextChangeExtend, TextDirectionPrevious, TextGranularityCharacter},
		{"shift right, back", 4, 5, 5, 5, TextChangeMove, TextDirectionNext, TextGranularityCharacter},
	} {
		got := SelectionChange(value, c.oldStart, c.oldEnd, c.start, c.end)
		if want := (TextSelectionChange{c.typ, c.dir, c.granularity}); got != want {
			t.Errorf("%s: %+v, want %+v", c.name, got, want)
		}
	}
}
