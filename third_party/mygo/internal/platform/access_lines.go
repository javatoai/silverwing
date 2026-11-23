package platform

import (
	"sort"
	"unicode/utf16"
)

// The lines of a text field's Value, for the bridges that count its text
// in UTF-16 units, as AppKit does: a line ends after its line feed, as
// the Windows and Linux bridges take lines (isBoundary, segment), so that
// every unit is on a line and the text after the last line feed, empty or
// not, is the last line.

// LineOfUnit returns the line of value that UTF-16 unit u is on, from 0:
// how many line feeds come before it. A unit past the end is on the last
// line.
func LineOfUnit(value string, u int) int {
	line, at := 0, 0
	for _, r := range value {
		if at >= u {
			break
		}
		at += utf16.RuneLen(r)
		if r == '\n' {
			line++
		}
	}
	return line
}

// LineUnits returns the UTF-16 units of line of value, from start, n of
// them, its line feed included, and false for a line value does not have:
// one below 0 or past the last.
func LineUnits(value string, line int) (start, n int, ok bool) {
	if line < 0 {
		return 0, 0, false
	}
	cur := 0
	for _, r := range value {
		units := utf16.RuneLen(r)
		if cur < line {
			start += units
		} else {
			n += units
		}
		if r == '\n' {
			if cur == line {
				return start, n, true
			}
			cur++
		}
	}
	return start, n, cur == line
}

// RangeBounds returns where UTF-16 units from u, count of them, of n's
// Value show: the union of the boxes of the lines they are on
// (LineBoxes), a run of lines collapsed sharing one, or the node's Bounds
// where it gives none of them, as for lines out of view.
func RangeBounds(n *AccessNode, u, count int) RectF {
	if len(n.LineBoxes) == 0 {
		return n.Bounds
	}
	first := LineOfUnit(n.Value, u)
	last := first
	if count > 0 {
		last = LineOfUnit(n.Value, u+count-1)
	}
	boxes := n.LineBoxes
	i := sort.Search(len(boxes), func(i int) bool { return boxes[i].Line+max(boxes[i].Count, 1)-1 >= first })
	var out RectF
	found := false
	for ; i < len(boxes) && boxes[i].Line <= last; i++ {
		b := boxes[i].Bounds
		if !found {
			out, found = b, true
			continue
		}
		x0, y0 := min(out.X, b.X), min(out.Y, b.Y)
		x1, y1 := max(out.X+out.W, b.X+b.W), max(out.Y+out.H, b.Y+b.H)
		out = RectF{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	}
	if !found {
		return n.Bounds
	}
	return out
}

// TextSelectionChange is how the selection of a text field moved, as
// WebKit tells VoiceOver in the userInfo of AXSelectedTextChanged
// (AXTextStateChangeType, AXTextSelectionDirection and
// AXTextSelectionGranularity), which it reads out by: the new line for a
// move by line, the character passed for a move by character.
type TextSelectionChange struct {
	Type, Direction, Granularity int
}

// The values of WebKit's AXTextStateChangeType, AXTextSelectionDirection
// and AXTextSelectionGranularity.
const (
	TextChangeMove   = 2 // AXTextStateChangeTypeSelectionMove
	TextChangeExtend = 3 // AXTextStateChangeTypeSelectionExtend

	TextDirectionUnknown       = 0
	TextDirectionBeginning     = 1
	TextDirectionEnd           = 2
	TextDirectionPrevious      = 3
	TextDirectionNext          = 4
	TextDirectionDiscontiguous = 5

	TextGranularityUnknown   = 0
	TextGranularityCharacter = 1
	TextGranularityWord      = 2
	TextGranularityLine      = 3
	TextGranularityDocument  = 7
)

// SelectionChange returns how the selection of value moved from the runes
// oldStart to oldEnd to those from start to end, as the keys move it: by
// a character, a word or a line, to the beginning or the end of a line or
// of the text, or elsewhere (discontiguous), judged from where its moving
// end went. A selection that holds runes extends, an empty one moves.
func SelectionChange(value string, oldStart, oldEnd, start, end int) TextSelectionChange {
	c := TextSelectionChange{Type: TextChangeMove}
	if end > start {
		c.Type = TextChangeExtend
	}
	// The end that moved: the caret of an empty selection, else the one
	// that is not the anchor.
	from, to := oldEnd, end
	if start != oldStart && (end == oldEnd || start == end) {
		from, to = oldStart, start
	}
	if from == to {
		c.Direction = TextDirectionDiscontiguous
		return c
	}
	// The lines of both places, and where the new one's starts and ends.
	runes := []rune(value)
	from, to = max(0, min(from, len(runes))), max(0, min(to, len(runes)))
	lines := func(rs []rune) int {
		n := 0
		for _, r := range rs {
			if r == '\n' {
				n++
			}
		}
		return n
	}
	lineFrom, lineTo := lines(runes[:from]), lines(runes[:to])
	startTo, endTo := to, to
	for startTo > 0 && runes[startTo-1] != '\n' {
		startTo--
	}
	for endTo < len(runes) && runes[endTo] != '\n' {
		endTo++
	}
	next := to > from
	dir := func(forward bool) int {
		if forward {
			return TextDirectionNext
		}
		return TextDirectionPrevious
	}
	switch d := lineTo - lineFrom; {
	case d == 1 || d == -1:
		c.Direction, c.Granularity = dir(next), TextGranularityLine
	case d != 0 && to == 0:
		c.Direction, c.Granularity = TextDirectionBeginning, TextGranularityDocument
	case d != 0 && to == len(runes):
		c.Direction, c.Granularity = TextDirectionEnd, TextGranularityDocument
	case d != 0:
		c.Direction = TextDirectionDiscontiguous
	case to-from == 1 || from-to == 1:
		c.Direction, c.Granularity = dir(next), TextGranularityCharacter
	case to == startTo:
		c.Direction, c.Granularity = TextDirectionBeginning, TextGranularityLine
	case to == endTo:
		c.Direction, c.Granularity = TextDirectionEnd, TextGranularityLine
	default:
		c.Direction, c.Granularity = dir(next), TextGranularityWord
	}
	return c
}
