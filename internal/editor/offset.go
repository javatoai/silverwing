package editor

import (
	"unicode/utf8"
)

// UTF16ToByteOffset converts the text-selection offsets commonly exposed by
// native desktop text controls to this package's UTF-8 byte offsets.
func UTF16ToByteOffset(source string, offset int) int {
	if offset <= 0 {
		return 0
	}
	units := 0
	for byteOffset := 0; byteOffset < len(source); {
		r, size := utf8.DecodeRuneInString(source[byteOffset:])
		if size == 0 {
			break
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > offset {
			return byteOffset
		}
		units += width
		byteOffset += size
		if units == offset {
			return byteOffset
		}
	}
	return len(source)
}

// ByteToUTF16Offset converts a byte position back to UTF-16 text-control
// offsets, clamping a position inside a UTF-8 rune to the rune's start.
func ByteToUTF16Offset(source string, byteOffset int) int {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(source) {
		byteOffset = len(source)
	}
	units := 0
	for at := 0; at < byteOffset; {
		r, size := utf8.DecodeRuneInString(source[at:])
		if size == 0 || at+size > byteOffset {
			break
		}
		if r > 0xffff {
			units += 2
		} else {
			units++
		}
		at += size
	}
	return units
}

func RuneToByteOffset(source string, runeOffset int) int {
	if runeOffset <= 0 {
		return 0
	}
	count := 0
	for at := range source {
		if count == runeOffset {
			return at
		}
		count++
	}
	return len(source)
}

func ByteToRuneOffset(source string, byteOffset int) int {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(source) {
		byteOffset = len(source)
	}
	count := 0
	for at := 0; at < byteOffset; {
		_, size := utf8.DecodeRuneInString(source[at:])
		if size == 0 || at+size > byteOffset {
			break
		}
		count++
		at += size
	}
	return count
}
