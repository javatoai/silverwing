package desktop

import (
	"sort"
	"unicode/utf8"
)

// jsonTextIndex owns immutable coordinates for one bounded JSON source. The
// tool's 2 MiB limit keeps every byte, rune and UTF-16 boundary within int32.
// CRLF occupies two source runes but one native LF byte. A boundary inside
// CRLF maps after that native newline, as the editor's original-text adapter
// does; a byte inside a UTF-8 rune maps after that source rune.
type jsonTextIndex struct {
	source         string
	runeBytes      []int32
	runeLFBytes    []int32
	runeUTF16      []int32
	lineStarts     []int32
	hasBareCR      bool
	hasInvalidUTF8 bool
}

func newJSONTextIndex(source string) *jsonTextIndex {
	n := utf8.RuneCountInString(source)
	index := &jsonTextIndex{
		source:      source,
		runeBytes:   make([]int32, n+1),
		runeLFBytes: make([]int32, n+1),
		runeUTF16:   make([]int32, n+1),
		lineStarts:  []int32{0},
	}
	var units, lfBytes int32
	runes := 0
	for at := 0; at < len(source); {
		r, width := utf8.DecodeRuneInString(source[at:])
		if r == utf8.RuneError && width == 1 {
			index.hasInvalidUTF8 = true
		}
		at += width
		runes++
		units++
		if r > 0xffff {
			units++
		}
		if r != '\n' || at < 2 || source[at-2] != '\r' {
			lfBytes += int32(width)
		}
		index.runeBytes[runes] = int32(at)
		index.runeLFBytes[runes] = lfBytes
		index.runeUTF16[runes] = units
		switch r {
		case '\r':
			if at < len(source) && source[at] == '\n' {
				index.lineStarts = append(index.lineStarts, int32(runes+1))
			} else {
				index.hasBareCR = true
				index.lineStarts = append(index.lineStarts, int32(runes))
			}
		case '\n':
			if at < 2 || source[at-2] != '\r' {
				index.lineStarts = append(index.lineStarts, int32(runes))
			}
		}
	}
	return index
}

func (index *jsonTextIndex) runeCount() int { return len(index.runeBytes) - 1 }
func (index *jsonTextIndex) utf16Length() int {
	return int(index.runeUTF16[len(index.runeUTF16)-1])
}
func (index *jsonTextIndex) lineCount() int { return len(index.lineStarts) }

func (index *jsonTextIndex) byteAtRune(offset int) int {
	return int(index.runeBytes[bounded(offset, 0, index.runeCount())])
}

func (index *jsonTextIndex) lfByteAtRune(offset int) int {
	return int(index.runeLFBytes[bounded(offset, 0, index.runeCount())])
}

func (index *jsonTextIndex) runeAtLFByte(offset int) int {
	if offset <= 0 {
		return 0
	}
	at := sort.Search(len(index.runeLFBytes), func(i int) bool { return int(index.runeLFBytes[i]) >= offset })
	if at == len(index.runeLFBytes) {
		return index.runeCount()
	}
	// The two source boundaries of CRLF have the same LF byte coordinate.
	if at+1 < len(index.runeLFBytes) && index.runeLFBytes[at+1] == index.runeLFBytes[at] {
		at++
	}
	return at
}

func (index *jsonTextIndex) runeAtUTF16(offset int) int {
	if offset <= 0 {
		return 0
	}
	at := sort.Search(len(index.runeUTF16), func(i int) bool { return int(index.runeUTF16[i]) >= offset })
	if at == len(index.runeUTF16) {
		return index.runeCount()
	}
	if int(index.runeUTF16[at]) > offset {
		return max(0, at-1) // a UTF-16 boundary inside a surrogate pair
	}
	return at
}

// lineAtRune returns a zero-based logical line in the LF-projected editor.
func (index *jsonTextIndex) lineAtRune(offset int) int {
	return max(0, sort.Search(len(index.lineStarts), func(i int) bool { return int(index.lineStarts[i]) > offset })-1)
}

func (index *jsonTextIndex) errorRuneOffset(line, column int) int {
	if line < 1 {
		return 0
	}
	if line > len(index.lineStarts) {
		return index.runeCount()
	}
	start := int(index.lineStarts[line-1])
	return index.runeAtUTF16(int(index.runeUTF16[start]) + max(0, column-1))
}
