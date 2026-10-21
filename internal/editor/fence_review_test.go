package editor

import "testing"

func TestFenceCodePreservesTrailingBlankLines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		for _, blankLines := range []int{0, 1, 2} {
			code := "first  "
			for range blankLines {
				code += newline
			}
			source := "```go" + newline + code + newline + "```" + newline
			fence := FindFence(source)
			if fence == nil {
				t.Fatalf("fixture did not parse: %q", source)
			}
			if actual := fence.Code(source); actual != code {
				t.Errorf("newline %q, blank lines %d: got %q, want %q", newline, blankLines, actual, code)
			}
		}
	}
}
