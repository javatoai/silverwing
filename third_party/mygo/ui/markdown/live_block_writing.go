package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
)

func (s *EditorState) blockWritingKey(key ui.Key, mods ui.Modifiers) bool {
	if !s.opts.SmartEditing || s.SourceMode || s.opts.ReadOnly || s.composing || key != ui.KeyBackspace || mods != 0 || s.anchor != s.caret {
		return false
	}
	if table, cell, ok := s.editingTable(); ok && cell.row == 0 && cell.column == 0 && (s.caret == cell.start || s.caret == cell.sourceStart) {
		empty := true
		for _, row := range table.rows {
			for _, value := range row {
				if strings.TrimSpace(s.source[value.start:value.end]) != "" {
					empty = false
				}
			}
		}
		if empty {
			start := s.documentPrefixEnd(table.start)
			s.report(s.ApplyEdits([]editor.Edit{{Start: start, End: table.end, Text: s.buffer.LineEnding()}}, start, start, s.snapshot.Version()))
			return true
		}
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.Heading {
			continue
		}
		start, end := s.blockRange(b)
		start = s.documentPrefixEnd(start)
		at := start
		for at < end && s.source[at] == '#' {
			at++
		}
		if at == start || at >= end || s.source[at] != ' ' {
			continue
		}
		for at < end && (s.source[at] == ' ' || s.source[at] == '\t') {
			at++
		}
		if s.caret == at {
			s.report(s.ApplyEdits([]editor.Edit{{Start: start, End: at}}, start, start, s.snapshot.Version()))
			return true
		}
	}
	return false
}
