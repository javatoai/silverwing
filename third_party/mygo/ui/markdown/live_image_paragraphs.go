package markdown

import (
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
)

// Existing separators may lead to the next list/quote container. Keep their
// bytes, but put the caret in this image's virtual writing row rather than on
// that container's source marker.
func (s *EditorState) imageInsertionWritingSelection(source string, start, end, at int, drafts []int) (editorSelection, bool) {
	d := parser.New(s.view.ext)
	d.SetWithPolicy(source, s.sourcePrefixFor(source), drafts)
	marker := false
	for block := 1; block < d.Len(); block++ {
		b := d.Block(block)
		if isEditorContainer(b.Kind) && b.Start == at {
			marker = true
			break
		}
	}
	if !marker {
		return editorSelection{}, false
	}
	var last ImageInfo
	found := false
	for block := 1; block < d.Len(); block++ {
		inlines := d.Inlines(block)
		for index := 0; index < len(inlines); index++ {
			image := inlines[index]
			if image.Kind != parser.Image {
				continue
			}
			if image.Start >= start && image.End <= end && image.End <= at {
				last, found = ImageInfo{Start: image.Start, End: image.End, Block: block}, true
			}
			index = image.Next - 1
		}
	}
	if !found {
		return editorSelection{}, false
	}
	b := d.Block(last.Block)
	if b.Kind != parser.Paragraph || strings.TrimSpace(source[b.Start:last.Start]) != "" || !recognizedImageTail(source[last.End:b.End]) {
		return editorSelection{}, false
	}
	continuation := codeContinuationPrefix(source[sourceLineStart(source, b.Start):b.Start])
	markerText := strings.TrimSpace(continuation)
	for _, line := range strings.Split(source[last.End:at], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed != markerText {
			return editorSelection{}, false
		}
	}
	return editorSelection{last.End, last.End}, true
}

// Reuse whole logical separators, including list/quote continuation markers.
// skip is a normalized source length already present on the right.
func (s *EditorState) imageParagraphSeparators(start, end int, continuation string) (prefix, suffix string, skip int) {
	ending := s.buffer.LineEnding()
	blank := strings.TrimRight(continuation, " \t")
	separator := ending + blank + ending + continuation
	contentStart := false
	d := s.editingDocument()
	for block := 1; block < d.Len(); block++ {
		if d.Block(block).Start == start {
			contentStart = true
			break
		}
	}
	if start > max(s.sourcePrefixEnd(), s.documentPrefixEnd(0)) && !contentStart {
		count := 0
		for at := start - 1; at >= 0 && s.source[at] == '\n' && count < 2; at-- {
			count++
		}
		switch count {
		case 0:
			prefix = separator
		case 1:
			prefix = blank + ending + continuation
		default:
			prefix = continuation
		}
	}
	suffix = separator
	if end >= len(s.source) {
		return
	}
	right := s.source[end:]
	if normalized := normalizedText(separator); strings.HasPrefix(right, normalized) {
		return prefix, "", len(normalized)
	}
	if strings.HasPrefix(right, "\n") {
		if lineEnd := strings.IndexByte(right[1:], '\n'); lineEnd >= 0 && strings.TrimSpace(right[1:lineEnd+1]) == strings.TrimSpace(continuation) {
			skip = lineEnd + 2
			bodyLine := end + skip
			for block := 1; block < d.Len(); block++ {
				b := d.Block(block)
				if b.Start >= bodyLine && sourceLineStart(s.source, b.Start) == bodyLine {
					skip = b.Start - end
					break
				}
			}
			return prefix, "", skip
		}
	}
	count := 0
	for count < len(right) && count < 2 && right[count] == '\n' {
		count++
	}
	switch count {
	case 2:
		suffix = ""
		skip = 2
	case 1:
		suffix = ending + blank
		skip = 1
	}
	if skip > 0 && continuation != "" && strings.HasPrefix(right[skip:], continuation) {
		skip += len(continuation)
	}
	return
}

func (s *EditorState) imageParagraphInput(a, z int, text string, endpoint int, gap *editorCodeGap) (int, int, string, int, bool) {
	block := gap.before
	if block == 0 {
		block = gap.after
	}
	if _, cell, ok := s.editingTable(); ok && a >= cell.sourceStart && z <= cell.sourceEnd {
		payload, caret := text, endpoint
		if normalizedText(text) != "\n" {
			payload = "\n" + text + "\n"
			caret = 1 + endpoint
		}
		payload, caret = s.tableInputText(a, z, payload, caret)
		return a, z, payload, caret, true
	}
	left, right, start, end := s.imageInsertionFormatting(a, z)
	prefix, suffix, _ := s.imageParagraphSeparators(start, end, s.imageContinuation(block))
	if normalizedText(text) == "\n" {
		text = ""
		endpoint = 0
	}
	payload := left + prefix + text + suffix + right
	caret := len(normalizedText(left+prefix)) + len(normalizedText(text[:endpoint]))
	return start, end, payload, caret, true
}
