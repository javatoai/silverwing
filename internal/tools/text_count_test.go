package tools

import (
	"strings"
	"testing"
)

func TestCountTextCountsGraphemesHanLinesAndBytes(t *testing.T) {
	tests := []struct {
		input string
		want  TextCounts
	}{
		{"", TextCounts{}},
		{"👨‍👩‍👧‍👦 e\u0301\t汉𰻞\r\n", TextCounts{Characters: 7, NonWhitespace: 4, Han: 2, Lines: 2, Bytes: 39}},
		{"🇨🇳\u2003👍🏽", TextCounts{Characters: 3, NonWhitespace: 2, Lines: 1, Bytes: 19}},
		{"\r\n\rx\n", TextCounts{Characters: 4, NonWhitespace: 1, Lines: 4, Bytes: 5}},
		{" \u00a0\u2003\t", TextCounts{Characters: 4, Lines: 1, Bytes: 7}},
	}
	for _, test := range tests {
		if got, err := CountText(test.input); err != nil || got != test.want {
			t.Errorf("CountText(%q) = %+v, %v, want %+v", test.input, got, err, test.want)
		}
	}
}

func TestCountTextRejectsInvalidUTF8AndOversizedInput(t *testing.T) {
	for _, invalid := range []string{"\xff", strings.Repeat("a", MaxTextBytes+1)} {
		if _, err := CountText(invalid); err == nil {
			t.Fatalf("accepted invalid text of %d bytes", len(invalid))
		}
	}
}
