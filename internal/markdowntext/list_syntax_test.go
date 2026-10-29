package markdowntext

import (
	"strings"
	"testing"
)

func TestListSyntaxRetainsMarkersAndCheckboxByteOffsets(t *testing.T) {
	for _, test := range []struct {
		line, indent, marker string
		matched              bool
	}{
		{line: "- 中文内容  ", marker: "-", matched: true},
		{line: "\t12) 中文内容  ", indent: "\t", marker: "12)", matched: true},
		{line: "  9. 内容", indent: "  ", marker: "9.", matched: true},
		{line: "+\t内容", marker: "+", matched: true},
		{line: "1.no separating whitespace"},
		{line: "-"},
		{line: "ordinary text"},
	} {
		got, matched := ParseListMarker(test.line)
		if matched != test.matched || got.Indent != test.indent || got.Marker != test.marker {
			t.Errorf("ParseListMarker(%q) = %#v, %t", test.line, got, matched)
		}
	}
	source := "中文前缀\r\n- [ ] 中文任务  \r\n\t* [X] 第二项\r\n- [q] ignored\r\n"
	ranges := TaskCheckboxRanges(source)
	want := []int{strings.Index(source, "[ ]") + 1, strings.Index(source, "[X]") + 1}
	if len(ranges) != len(want) {
		t.Fatalf("checkbox ranges = %#v", ranges)
	}
	for index, sourceRange := range ranges {
		if sourceRange.Start != want[index] || sourceRange.End != want[index]+1 {
			t.Fatalf("checkbox state lost its original UTF-8 byte offset: %#v", ranges)
		}
	}
}
