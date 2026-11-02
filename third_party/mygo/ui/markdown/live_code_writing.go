package markdown

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// codeWritingKey is deliberately ahead of literal input decoration: a draft
// opener belongs to ordinary text until an explicit Enter commits it.
func (s *EditorState) codeWritingKey(key ui.Key, mods ui.Modifiers) bool {
	if s.SourceMode || s.opts.ReadOnly || s.composing {
		return false
	}
	if key == ui.KeyEnter && mods == 0 && s.opts.ConfirmCodeFenceOnEnter && s.anchor == s.caret {
		line := sourceLineStart(s.source, s.caret)
		if slices.Contains(s.fenceDrafts, line) && s.caret == codeLineEnd(s.source, line) {
			first, last, ok := draftMarker(s.source, line)
			if ok {
				prefix := codeContinuationPrefix(s.source[line:first])
				ending := s.buffer.LineEnding()
				text := ending + prefix + ending + prefix + s.source[first:last]
				at := s.caret + len(normalizedText(ending+prefix))
				s.report(s.applySourceCommand([]editor.Edit{{Start: s.caret, End: s.caret, Text: text}}, at, at, s.snapshot.Version(), line))
				return true
			}
		}
	}
	if !s.opts.SmartEditing {
		return false
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	code, ok := s.editingCode(a, z)
	if !ok {
		return false
	}
	if key == ui.KeyBackspace && mods == 0 && a == z && a == code.bodyStart && strings.TrimSpace(s.source[code.bodyStart:code.bodyEnd]) == "" {
		start := s.documentPrefixEnd(code.start)
		s.report(s.ApplyEdits([]editor.Edit{{Start: start, End: code.end, Text: s.buffer.LineEnding()}}, start, start, s.snapshot.Version()))
		return true
	}
	if key == ui.KeyTab && (mods == 0 || mods == ui.Shift) {
		if a == z && mods == 0 {
			s.buffer.EndUndoGroup()
			s.replace(a, z, "    ", editor.EditOther)
			s.buffer.EndUndoGroup()
			return true
		}
		first, last := sourceLineStart(s.source, a), sourceLineStart(s.source, z)
		if z > a && z == last {
			last = sourceLineStart(s.source, z-1)
		}
		var edits []editor.Edit
		for _, row := range code.rows {
			line := sourceLineStart(s.source, row.start)
			if line < first || line > last {
				continue
			}
			if mods == 0 {
				edits = append(edits, editor.Edit{Start: row.start, End: row.start, Text: "    "})
			} else {
				end := row.start
				if end < row.end && s.source[end] == '\t' {
					end++
				} else {
					for end < min(row.start+4, row.end) && s.source[end] == ' ' {
						end++
					}
				}
				if end > row.start {
					edits = append(edits, editor.Edit{Start: row.start, End: end})
				}
			}
		}
		if len(edits) > 0 {
			mapAt := func(at int) int {
				delta := 0
				for _, edit := range edits {
					if at < edit.Start {
						break
					}
					if at < edit.End {
						return edit.Start + delta + len(edit.Text)
					}
					delta += len(edit.Text) - (edit.End - edit.Start)
				}
				return at + delta
			}
			s.report(s.applyCodeEdits(code, edits, mapAt(s.anchor), mapAt(s.caret)))
		}
		return true
	}
	if key == ui.KeyEnter && (mods == 0 || mods == ui.Shift) {
		line := sourceLineStart(s.source, a)
		start := max(line, code.bodyStart)
		for _, row := range code.rows {
			if a >= row.start && a <= row.end {
				start = row.start
				break
			}
		}
		end := start
		for end < a && (s.source[end] == ' ' || s.source[end] == '\t') {
			end++
		}
		text := s.buffer.LineEnding() + code.prefix + s.source[start:end]
		at := a + len(normalizedText(text))
		s.report(s.applyCodeEdits(code, []editor.Edit{{Start: a, End: z, Text: text}}, at, at))
		return true
	}
	return false
}
