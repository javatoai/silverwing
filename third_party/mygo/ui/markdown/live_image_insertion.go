package markdown

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// InsertImageParagraphsAt imports no files. It places already-encoded image
// references as paragraphs, atomically with their required separators.
func (s *EditorState) InsertImageParagraphsAt(point InsertionPoint, images []string) error {
	if len(images) == 0 {
		return nil
	}
	before := s.CaptureInsertionPoint()
	if err := s.RestoreInsertionPoint(point); err != nil {
		return err
	}
	if s.opts.ReadOnly || s.composing {
		return errors.New("当前不可插入图片")
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	a, z = s.protectImageRange(a, z)
	prefixEnd := max(s.sourcePrefixEnd(), s.documentPrefixEnd(0))
	a, z = max(a, prefixEnd), max(z, prefixEnd)
	if s.SourceMode || s.SelectionContext() != ContextProse {
		return s.InsertMarkdownAt(point, strings.Join(images, s.buffer.LineEnding()))
	}
	if !s.richReplacementSafe(a, z, images[0]) {
		s.restoreInsertionPoint(before)
		return errors.New("该选区的 Markdown 结构无法安全替换")
	}
	ending := s.buffer.LineEnding()
	continuation := ""
	d := s.editingDocument()
	for block := 1; block < d.Len(); block++ {
		b := d.Block(block)
		if (b.Kind == parser.Paragraph || b.Kind == parser.Heading) && a >= b.Start && a <= b.End {
			line := max(sourceLineStart(s.source, b.Start), s.documentPrefixEnd(0))
			continuation = codeContinuationPrefix(s.source[line:b.Start])
			break
		}
	}
	blank := strings.TrimRight(continuation, " \t")
	separator := ending + blank + ending + continuation
	fragment := strings.Join(images, separator)
	leftClose, rightOpen, start, end := s.imageInsertionFormatting(a, z)
	prefix, suffix, skip := s.imageParagraphSeparators(start, end, continuation)
	text := leftClose + prefix + fragment + suffix + rightOpen
	// The insertion caret belongs to the following writing paragraph. Existing
	// separators are reused without changing their bytes or the right-hand text.
	at := start + len(normalizedText(leftClose+prefix+fragment+suffix+rightOpen))
	at += skip
	// The final writing position is part of this transaction's history, so
	// Redo restores the virtual row rather than the following item marker.
	var intent []editorCodeGapHistory
	inserted := normalizedText(text)
	candidate := s.source[:start] + inserted + s.source[end:]
	changes := []editor.Change{{OldStart: start, OldEnd: end, NewStart: start, NewEnd: start + len(inserted)}}
	drafts := s.draftsAfterEdit(candidate, changes, s.literalFenceLines(), -1)
	if selection, ok := s.imageInsertionWritingSelection(candidate, start, start+len(inserted), at, drafts); ok {
		intent = []editorCodeGapHistory{{selection: selection}}
	}
	if err := s.applySourceCommand([]editor.Edit{{Start: start, End: end, Text: text}}, at, at, point.version, -1, intent...); err != nil {
		s.restoreInsertionPoint(before)
		return err
	}
	s.Focus()
	return nil
}

// Split only inline formatting crossing the insertion. Empty halves consume
// their original delimiters instead of leaving empty formatting after paste.
func (s *EditorState) imageInsertionFormatting(a, z int) (leftClose, rightOpen string, start, end int) {
	start, end = a, z
	type span struct {
		start, end, left, right int
		style                   bool
	}
	var containers []span
	d := s.editingDocument()
	for block := 1; block < d.Len(); block++ {
		for index, in := range d.Inlines(block) {
			width := 0
			switch in.Kind {
			case parser.Strong, parser.Strikethrough:
				width = 2
			case parser.Emphasis:
				width = 1
			}
			if width > 0 && in.Start+width <= a && z <= in.End-width {
				containers = append(containers, span{in.Start, in.End, in.Start + width, in.End - width, true})
			}
			if in.Kind == parser.Link {
				if left, right, ok := d.LinkLabelRange(block, index); ok && left <= a && z <= right {
					containers = append(containers, span{in.Start, in.End, left, right, false})
				}
			}
		}
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].start == containers[j].start {
			return containers[i].end > containers[j].end
		}
		return containers[i].start < containers[j].start
	})
	for index := len(containers) - 1; index >= 0; index-- {
		in := containers[index]
		if start == in.left {
			start = in.start
		}
		if end == in.right {
			end = in.end
		}
	}
	closeStyle, openStyle := false, false
	for _, in := range containers {
		if start > in.left {
			leftClose = s.snapshot.OriginalSlice(in.right, in.end) + leftClose
			closeStyle = closeStyle || in.style
		}
		if end < in.right {
			rightOpen += s.snapshot.OriginalSlice(in.start, in.left)
			openStyle = openStyle || in.style
		}
	}
	// Emphasis delimiters cannot close after or open before whitespace.
	// Keep those original bytes outside the regenerated wrappers.
	if closeStyle {
		from := start
		for from > 0 {
			r, size := utf8.DecodeLastRuneInString(s.source[:from])
			if !unicode.IsSpace(r) || r == '\n' || r == '\r' {
				break
			}
			from -= size
		}
		leftClose += s.snapshot.OriginalSlice(from, start)
		start = from
	}
	if openStyle {
		to := end
		for to < len(s.source) {
			r, size := utf8.DecodeRuneInString(s.source[to:])
			if !unicode.IsSpace(r) || r == '\n' || r == '\r' {
				break
			}
			to += size
		}
		rightOpen = s.snapshot.OriginalSlice(end, to) + rightOpen
		end = to
	}
	return
}
