package documents

import (
	"encoding/binary"
	"unicode"
	"unicode/utf16"
)

// The caller already owns the bounded, no-follow read. Decode only encodings
// already supported by this package; never guess a legacy code page.
func detectedMaterialsText(data []byte) (string, bool) {
	text, err := decodeText(data)
	if err != nil {
		return "", false
	}
	for _, r := range text {
		// Tabs, line endings, form feeds and ANSI escapes occur in text logs.
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' && r != '\f' && r != 0x1b {
			return "", false
		}
	}
	if len(data) >= 3 && string(data[:3]) == "\xef\xbb\xbf" {
		data = data[3:]
	}
	if len(data) >= 2 && (string(data[:2]) == "\xff\xfe" || string(data[:2]) == "\xfe\xff") {
		// decodeText replaces unpaired UTF-16 surrogates. A lossless round trip
		// distinguishes valid U+FFFD text from an unreliable decode.
		var order binary.ByteOrder = binary.BigEndian
		if data[0] == 0xff {
			order = binary.LittleEndian
		}
		units := utf16.Encode([]rune(text))
		if len(units)*2 != len(data)-2 {
			return "", false
		}
		for i, unit := range units {
			if order.Uint16(data[2+i*2:]) != unit {
				return "", false
			}
		}
	}
	return text, true
}
