package parser

import "strings"

// CodeSource identifies only the syntax owned by a fenced code block. Offsets
// address Document.Source bytes; decoded info text never replaces those bytes.
// ClosingStart is -1 for an unclosed fence, including one ended by a container.
type CodeSource struct {
	OpeningStart, OpeningEnd   int
	LanguageStart, LanguageEnd int
	ClosingStart, ClosingEnd   int
}

// CodeSource returns the original opening fence, language token and actual
// closing fence of block i. Indented code and other block kinds return false.
// Body segments are authoritative: a fence-like final content row is not syntax.
func (d *Document) CodeSource(i int) (CodeSource, bool) {
	if i <= 0 || i >= len(d.blocks) {
		return CodeSource{}, false
	}
	b := &d.blocks[i]
	if b.Kind != CodeBlock || !b.Fenced {
		return CodeSource{}, false
	}
	source := CodeSource{OpeningStart: b.Start, OpeningEnd: b.Start + int(b.parse.fenceLength), ClosingStart: -1}
	lineEnd := len(d.src)
	if next := strings.IndexAny(d.src[source.OpeningEnd:], "\r\n"); next >= 0 {
		lineEnd = source.OpeningEnd + next
	}
	info := d.src[source.OpeningEnd:lineEnd]
	raw := strings.Trim(info, " \t")
	source.LanguageStart = source.OpeningEnd + len(info) - len(strings.TrimLeft(info, " \t"))
	source.LanguageEnd = source.LanguageStart + literalToSource(raw, b.Info(), len(b.Language()))
	// finalize ends an unclosed block at its last body segment (or opener
	// for an empty block). Only the fence-closing branch extends End past it.
	contentEnd := lineEnd
	if b.segB > b.segA {
		contentEnd = int(d.segs[b.segB-1].end)
	}
	if b.End <= contentEnd {
		return source, true
	}
	end := min(b.End, len(d.src))
	for end > contentEnd && (d.src[end-1] == ' ' || d.src[end-1] == '\t') {
		end--
	}
	start := end
	for start > contentEnd && d.src[start-1] == b.parse.fenceChar {
		start--
	}
	if end-start >= int(b.parse.fenceLength) {
		source.ClosingStart, source.ClosingEnd = start, end
	}
	return source, true
}
