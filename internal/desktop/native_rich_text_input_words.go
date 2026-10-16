package desktop

import (
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"golang.org/x/text/unicode/bidi"

	"silverwing-go/internal/platform"
)

// Cache the platform's complete word boundaries for a committed/draft text
// value. ICU resources never enter the view; platform owns native calls.
type nativeRichWordNavigationState struct {
	text       string
	units      []uint16
	boundaries []int
	err        error
}

func (f *nativeRichTextField) wordNavigationState() *nativeRichWordNavigationState {
	if f.wordNavigation == nil || f.wordNavigation.text != f.draftText {
		boundaries, err := platform.WordBoundaries(f.draftText)
		f.wordNavigation = &nativeRichWordNavigationState{
			text: f.draftText, units: utf16.Encode([]rune(f.draftText)), boundaries: boundaries, err: err,
		}
	}
	return f.wordNavigation
}

func (f *nativeRichTextField) moveWord(right, extend bool) {
	forward := right
	if nativeRichParagraphRTL(f.draftText, f.caretPosition().Index) {
		forward = !forward
	}
	target, ok := f.wordOffset(f.caretPosition().Index, forward)
	if !ok {
		return
	}
	position := ui.TextCaretPosition{Index: target, Affinity: ui.TextDownstream}
	anchor := position
	if extend {
		anchor = f.anchorPosition()
	}
	f.setPositions(anchor, position)
	f.hasPreferredX = false
}

func (f *nativeRichTextField) deleteWord(forward bool) {
	r := f.selection.Range
	if r.Start == r.End {
		target, ok := f.wordOffset(f.caretPosition().Index, forward)
		if !ok {
			return
		}
		if forward {
			r.End = target
		} else {
			r.Start = target
		}
	}
	if r.Start != r.End {
		f.ReplaceText(&r, "")
	}
}

// Compose moves from the active caret and retries adjacent UTF16 offsets when
// its whitespace-aware word range would leave the caret in place.
func (f *nativeRichTextField) wordOffset(offset int, forward bool) (int, bool) {
	state := f.wordNavigationState()
	if state.err != nil {
		f.setError(state.err)
		return offset, false
	}
	length := len(state.units)
	offset = min(length, max(0, offset))
	for {
		if forward && offset >= length {
			return length, true
		}
		if !forward && offset <= 0 {
			return 0, true
		}
		start, end := state.wordRange(min(offset, length-1))
		if forward {
			if end > offset {
				return end, true
			}
			offset++
		} else {
			if start < offset {
				return start, true
			}
			offset--
		}
	}
}

func (state *nativeRichWordNavigationState) wordRange(offset int) (int, int) {
	if offset >= len(state.units) || nativeRichComposeWhitespace(state.units[offset]) {
		if offset > 0 && !nativeRichComposeWhitespace(state.units[offset-1]) {
			offset--
		} else {
			return offset, offset
		}
	}
	end := sort.SearchInts(state.boundaries, offset+1)
	return state.boundaries[end-1], state.boundaries[end]
}

// Kotlin/JVM Char.isWhitespace combines Character.isWhitespace/isSpaceChar.
// In particular it includes NBSP and the information separators, but not NEL.
func nativeRichComposeWhitespace(unit uint16) bool {
	switch {
	case unit >= 0x09 && unit <= 0x0d, unit >= 0x1c && unit <= 0x20:
		return true
	case unit == 0x00a0, unit == 0x1680, unit >= 0x2000 && unit <= 0x200a:
		return true
	case unit == 0x2028, unit == 0x2029, unit == 0x202f, unit == 0x205f, unit == 0x3000:
		return true
	default:
		return false
	}
}

// UAX #9 P2/P3: the first strong character outside directional isolates sets
// the LF paragraph direction; paragraphs with no strong character use LTR.
func nativeRichParagraphRTL(text string, caret int) bool {
	index := ui.UTF16ByteOffset(text, caret)
	start := strings.LastIndexByte(text[:index], '\n') + 1
	end := nativeRichParagraphEnd(text, index)
	isolateDepth := 0
	for _, character := range text[start:end] {
		properties, _ := bidi.LookupRune(character)
		switch properties.Class() {
		case bidi.LRI, bidi.RLI, bidi.FSI:
			isolateDepth++
		case bidi.PDI:
			if isolateDepth > 0 {
				isolateDepth--
			}
		case bidi.B:
			return false
		case bidi.L:
			if isolateDepth == 0 {
				return false
			}
		case bidi.R, bidi.AL:
			if isolateDepth == 0 {
				return true
			}
		}
	}
	return false
}
