package parser

import "strings"

// EncodeCodeSpan preserves literal edge backticks and spaces under the standard
// code-span delimiter and single-padding-space rules.
func EncodeCodeSpan(text string) string {
	open, close := CodeSpanDelimiters(text, 1)
	return open + text + close
}

// CodeSpanDelimiters preserves literal edge spaces/backticks while retaining
// an existing delimiter width when editing a code span in place.
func CodeSpanDelimiters(text string, minimum int) (string, string) {
	fence := BacktickFence(text, minimum)
	pad := strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") || strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") && strings.Trim(text, " ") != ""
	if pad {
		return fence + " ", " " + fence
	}
	return fence, fence
}

// BacktickFence chooses a delimiter longer than every literal run, in one
// pass even when pasted code contains a large run of backticks.
func BacktickFence(text string, minimum int) string {
	longest, run := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(1, minimum, longest+1))
}
