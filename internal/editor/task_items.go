package editor

import (
	"regexp"
)

var simpleTaskItemRE = regexp.MustCompile(`^([ \t]*)([-+*])[ \t]+\[([ xX])\][ \t]*(.*)$`)

// TaskItem describes one source-located checkbox line. Ranges are half-open
// UTF-8 byte offsets into the original Markdown source.
type TaskItem struct {
	Index      int         `json:"index"`
	Checked    bool        `json:"checked"`
	CheckRange SourceRange `json:"checkRange"`
	TextRange  SourceRange `json:"textRange"`
	LineRange  SourceRange `json:"lineRange"`
	Text       string      `json:"text"`
	Prefix     string      `json:"prefix"`
}

// TaskItems parses checkbox list entries without normalizing line endings or
// the task prefix, so callers can patch only the marker or visible item text.
func TaskItems(source string) []TaskItem {
	var items []TaskItem
	for _, line := range scanLines(source) {
		match := simpleTaskItemRE.FindStringSubmatchIndex(line.text)
		if match == nil {
			continue
		}
		checkStart, checkEnd := line.start+match[6], line.start+match[7]
		textStart, textEnd := line.start+match[8], line.start+match[9]
		items = append(items, TaskItem{
			Index:      len(items),
			Checked:    source[checkStart:checkEnd] != " ",
			CheckRange: SourceRange{Start: checkStart, End: checkEnd},
			TextRange:  SourceRange{Start: textStart, End: textEnd},
			LineRange:  SourceRange{Start: line.start, End: line.end},
			Text:       source[textStart:textEnd],
			Prefix:     source[line.start:textStart],
		})
	}
	return items
}
