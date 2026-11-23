package editor

import (
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/internal/rope"
)

// sourceLF projects exact source bytes to the editor's LF line offsets.
func sourceLF(s string) string { return lf(s) }

// firstSourceLineEnding supplies new delimiters from the input baseline.
func firstSourceLineEnding(text string) string {
	index := strings.IndexAny(text, "\r\n")
	if index >= 0 && text[index] == '\r' {
		if index+1 < len(text) && text[index+1] == '\n' {
			return "\r\n"
		}
		return "\r"
	}
	return "\n"
}

// originalOffset maps an LF byte boundary to the same boundary in the source.
// CRLF and LF have the same line count, including when mixed in one document.
func originalOffset(text, original rope.Text, offset int) int {
	if text.Len() == original.Len() {
		return min(offset, original.Len())
	}
	if text.LineCount() != original.LineCount() {
		// Bare CR is a logical newline in source-preserving views. The usual
		// LF/CRLF path below stays logarithmic; only mixed CR+CRLF sources scan.
		raw := min(offset, original.Len())
		base := 0
		original.Chunks(0, original.Len(), func(chunk string) bool {
			for from := 0; from < len(chunk); {
				index := strings.IndexByte(chunk[from:], '\r')
				if index < 0 {
					break
				}
				index += from
				position := base + index
				if position >= raw {
					return false
				}
				if index+1 < len(chunk) && chunk[index+1] == '\n' || index+1 == len(chunk) && position+1 < original.Len() && original.ByteAt(position+1) == '\n' {
					raw++
				}
				from = index + 1
			}
			base += len(chunk)
			return base < raw
		})
		return min(raw, original.Len())
	}
	p := posOf(text, offset)
	return original.LineStart(p.Line) + p.Col
}

func originalProjectedOffset(text, original rope.Text, offset int, side bias) int {
	if text.Len() == original.Len() {
		return min(offset, text.Len())
	}
	if text.LineCount() != original.LineCount() {
		projected := offset
		base := 0
		original.Chunks(0, min(offset, original.Len()), func(chunk string) bool {
			for from := 0; from < len(chunk); {
				index := strings.IndexByte(chunk[from:], '\r')
				if index < 0 {
					break
				}
				index += from
				position := base + index
				pair := index+1 < len(chunk) && chunk[index+1] == '\n' || index+1 == len(chunk) && position+1 < original.Len() && original.ByteAt(position+1) == '\n'
				if pair && (position+1 < offset || side == biasLeft) {
					projected--
				}
				from = index + 1
			}
			base += len(chunk)
			return true
		})
		return min(projected, text.Len())
	}
	p := posOf(original, offset)
	off := text.LineStart(p.Line) + p.Col
	if off > text.LineEnd(p.Line) {
		// A source boundary between CR and LF has no distinct display
		// boundary. Keep its requested side of the single projected newline.
		off = text.LineEnd(p.Line)
		if side == biasRight {
			off++
		}
	}
	return min(off, text.Len())
}

// originalByteChanges keeps each source edit distinct for caret mapping even
// when projection must combine adjacent edits to normalize a new CRLF pair.
// Only byte ranges are used; b.mu is held.
func (b *Buffer) originalByteChanges(edits []Edit) []change {
	if !slices.IsSortedFunc(edits, func(x, y Edit) int { return x.Start - y.Start }) {
		edits = slices.Clone(edits)
		slices.SortStableFunc(edits, func(x, y Edit) int { return x.Start - y.Start })
	}
	changes := make([]change, 0, len(edits))
	delta, last := 0, 0
	for _, e := range edits {
		start := max(last, min(e.Start, b.text.Len()))
		end := max(start, min(e.End, b.text.Len()))
		last = end
		a, z := originalOffset(b.text, b.original, start), originalOffset(b.text, b.original, end)
		if b.original.Slice(a, z) == e.Text {
			continue
		}
		changes = append(changes, change{oldStart: a, oldEnd: z, newStart: a + delta, newEnd: a + delta + len(e.Text)})
		delta += len(e.Text) - (z - a)
	}
	return changes
}

func (b *Buffer) originalSlice(start, end int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.original.Slice(originalOffset(b.text, b.original, start), originalOffset(b.text, b.original, end))
}

// writeProjectedText writes t to w with the line ending of crlf.
func writeProjectedText(t rope.Text, crlf bool, w io.Writer) (int64, error) {
	if !crlf {
		return rope.WriteTo(t.Root(), w)
	}
	var n int64
	var err error
	t.Chunks(0, t.Len(), func(s string) bool {
		var k int
		k, err = io.WriteString(w, strings.ReplaceAll(s, "\n", "\r\n"))
		n += int64(k)
		return err == nil
	})
	return n, err
}

// editsWithLineEndingCodes supplies the explicit separators for structural
// edits such as Move Lines. These edits may also replace the following break.
func (b *Buffer) editsWithLineEndingCodes(edits []Edit, codes []string) []Edit {
	edits = slices.Clone(edits)
	for i, e := range edits {
		if i >= len(codes) {
			break
		}
		var source strings.Builder
		start := originalOffset(b.text, b.original, e.Start)
		previousCR := start > 0 && b.original.ByteAt(start-1) == '\r'
		if i > 0 && edits[i-1].End == e.Start {
			previousCR = strings.HasSuffix(edits[i-1].Text, "\r")
		}
		remaining := lf(e.Text)
		for _, code := range []byte(codes[i]) {
			before, after, found := strings.Cut(remaining, "\n")
			separator := ending(code)
			// Keep consecutive breaks distinct when the reorder would
			// put a raw CR directly before an empty LF line.
			if before == "" && code == endLF && previousCR {
				separator = "\r\n"
			}
			if !found {
				if e.End < b.text.Len() && b.text.ByteAt(e.End) == '\n' {
					remaining += separator
					e.End++
				}
				break
			}
			source.WriteString(before)
			source.WriteString(separator)
			previousCR = code == endCR
			remaining = after
		}
		source.WriteString(remaining)
		// A following empty line can also need a wider separator, as in
		// the ordinary buffer's fixCR. Leave a following edit's range to it.
		if strings.HasSuffix(source.String(), "\r") && e.End < b.text.Len() &&
			(i+1 == len(edits) || e.End < edits[i+1].Start) &&
			b.original.ByteAt(originalOffset(b.text, b.original, e.End)) == '\n' {
			source.WriteString("\r\n")
			e.End++
		}
		e.Text = source.String()
		edits[i] = e
	}
	return edits
}

// replacePreserving applies exact source edits and projects only the affected
// spans. Include adjacent CR/LF so a pair formed across an edit boundary is
// normalized together. All edits use the LF coordinates of the previous text.
func (b *Buffer) replacePreserving(edits []Edit) preparedEdit {
	if !slices.IsSortedFunc(edits, func(x, y Edit) int { return x.Start - y.Start }) {
		edits = slices.Clone(edits)
		slices.SortStableFunc(edits, func(x, y Edit) int { return x.Start - y.Start })
	}
	type sourceSpan struct{ start, end, beforeDelta, delta int }
	spans := make([]sourceSpan, 0, len(edits))
	next := preparedEdit{text: b.text, original: b.original, ends: b.ends}
	delta, last := 0, 0
	for _, e := range edits {
		start := max(last, min(e.Start, b.text.Len()))
		end := max(start, min(e.End, b.text.Len()))
		last = end
		a, z := originalOffset(b.text, b.original, start), originalOffset(b.text, b.original, end)
		if b.original.Slice(a, z) == e.Text {
			continue
		}
		next.original = next.original.Replace(a+delta, z+delta, e.Text)
		d := len(e.Text) - (z - a)
		if a > 0 && b.original.ByteAt(a-1) == '\r' {
			start--
		}
		if z < b.original.Len() && b.original.ByteAt(z) == '\n' {
			end++
		}
		if n := len(spans); n > 0 && start <= spans[n-1].end {
			spans[n-1].end = max(spans[n-1].end, end)
			spans[n-1].delta += d
		} else {
			spans = append(spans, sourceSpan{start, end, delta, d})
		}
		delta += d
	}

	delta, lineDelta := 0, 0
	for _, span := range spans {
		a := originalOffset(b.text, b.original, span.start)
		z := originalOffset(b.text, b.original, span.end)
		oldRaw := b.original.Slice(a, z)
		newRaw := next.original.Slice(a+span.beforeDelta, z+span.beforeDelta+span.delta)
		if oldRaw == newRaw {
			continue
		}

		// Trim the source range at logical boundaries for exact undo and
		// remote conflict detection, even when only line endings changed.
		prefix, suffix := unchangedEdges(oldRaw, newRaw)
		if splitsCRLF(oldRaw, prefix) || splitsCRLF(newRaw, prefix) {
			prefix--
		}
		if splitsCRLF(oldRaw, len(oldRaw)-suffix) || splitsCRLF(newRaw, len(newRaw)-suffix) {
			suffix--
		}
		oldLF := b.text.Slice(span.start, span.end)
		newLF := lf(newRaw)
		start := span.start + len(lf(oldRaw[:prefix]))
		end := span.end - len(lf(oldRaw[len(oldRaw)-suffix:]))
		newPart, codes := readEnds(newRaw[prefix : len(newRaw)-suffix])
		source := projectedChange(b.text, start, end, newPart, delta, lineDelta)
		m, k := source.oldEndLine-source.oldLine, source.newEndLine-source.newLine
		if codes == "" {
			codes = strings.Repeat(string(endLF), k)
		}
		if m > 0 || k > 0 {
			next.ends = next.ends.replace(source.newLine, source.newLine+m, codes)
		}
		next.history = append(next.history, source)
		next.rawOnly = append(next.rawOnly, oldLF == newLF)

		// Keep source-only differences out of caret remapping and the
		// incremental parser. Display changes use logical text edges.
		if oldLF != newLF {
			prefix, suffix := unchangedEdges(oldLF, newLF)
			replacement := newLF[prefix : len(newLF)-suffix]
			c := projectedChange(b.text, span.start+prefix, span.end-suffix, replacement, delta, lineDelta)
			next.text = next.text.Replace(c.newStart, c.oldEnd+delta, replacement)
			next.changes = append(next.changes, c)
			delta += c.newEnd - c.newStart - (c.oldEnd - c.oldStart)
			lineDelta += c.newEndLine - c.newLine - (c.oldEndLine - c.oldLine)
		}
	}
	return next
}

// projectedChange describes one normalized edit in the current version.
func projectedChange(before rope.Text, start, end int, text string, delta, lineDelta int) change {
	line := before.LineOf(start)
	newStart, newLine := start+delta, line+lineDelta
	return change{oldStart: start, oldEnd: end, newStart: newStart, newEnd: newStart + len(text),
		oldLine: line, oldEndLine: before.LineOf(end), newLine: newLine, newEndLine: newLine + strings.Count(text, "\n")}
}

func splitsCRLF(text string, offset int) bool {
	return offset > 0 && offset < len(text) && text[offset-1] == '\r' && text[offset] == '\n'
}

// unchangedEdges returns the common prefix and suffix lengths, with both
// boundaries between UTF-8 runes.
func unchangedEdges(old, next string) (prefix, suffix int) {
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	for prefix > 0 && (prefix < len(old) && !utf8.RuneStart(old[prefix]) || prefix < len(next) && !utf8.RuneStart(next[prefix])) {
		prefix--
	}
	for suffix < len(old)-prefix && suffix < len(next)-prefix && old[len(old)-suffix-1] == next[len(next)-suffix-1] {
		suffix++
	}
	for suffix > 0 && (!utf8.RuneStart(old[len(old)-suffix]) || !utf8.RuneStart(next[len(next)-suffix])) {
		suffix--
	}
	return prefix, suffix
}
