package tools

import (
	"errors"
	"unicode"

	"github.com/go-text/typesetting/segmenter"
)

type TextCounts struct {
	Characters    int
	NonWhitespace int
	Han           int
	Lines         int
	Bytes         int
}

// CountText counts extended grapheme clusters, Han runes and UTF-8 bytes.
// CRLF, CR and LF terminate logical lines, including a final empty line.
func CountText(input string) (TextCounts, error) {
	if err := checkSize(input); err != nil {
		return TextCounts{}, err
	}
	if input == "" {
		return TextCounts{}, nil
	}
	var segmentation segmenter.Segmenter
	if err := segmentation.InitWithString(input); err != nil {
		return TextCounts{}, errors.New("请输入有效的 UTF-8 文本")
	}
	counts := TextCounts{Lines: 1, Bytes: len(input)}
	var previous rune
	graphemes := segmentation.GraphemeIterator()
	for graphemes.Next() {
		counts.Characters++
		whitespace := true
		for _, r := range graphemes.Grapheme().Text {
			whitespace = whitespace && unicode.IsSpace(r)
			if unicode.Is(unicode.Han, r) {
				counts.Han++
			}
			if r == '\r' || (r == '\n' && previous != '\r') {
				counts.Lines++
			}
			previous = r
		}
		if !whitespace {
			counts.NonWhitespace++
		}
	}
	return counts, nil
}
