package markdowntext

import "regexp"

var (
	listRE = regexp.MustCompile(`^([ \t]*)([-+*]|[0-9]+[.)])[ \t]+(.*)$`)
	taskRE = regexp.MustCompile(`(?m)^([ \t]*)([-+*])[ \t]+\[([ xX])\][ \t]*(.*)$`)
)

// ListMarker describes the unchanged indentation and marker at a line's start.
type ListMarker struct {
	Indent string
	Marker string
}

func ParseListMarker(line string) (ListMarker, bool) {
	match := listRE.FindStringSubmatch(line)
	if match == nil {
		return ListMarker{}, false
	}
	return ListMarker{Indent: match[1], Marker: match[2]}, true
}

// TaskCheckboxRanges locates only each checkbox's state byte. Callers can patch
// that byte without normalizing indentation, text, or the original newlines.
func TaskCheckboxRanges(source string) []SourceRange {
	matches := taskRE.FindAllStringSubmatchIndex(source, -1)
	ranges := make([]SourceRange, len(matches))
	for index, match := range matches {
		ranges[index] = SourceRange{Start: match[6], End: match[7]}
	}
	return ranges
}
