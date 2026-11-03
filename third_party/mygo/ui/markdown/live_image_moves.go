package markdown

import (
	"errors"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
)

// Keep the separators and container markers at each position. Moving a list
// paragraph must not move its item's leading "-" into the middle of the item.
func (s *EditorState) imageMoveRange(info ImageInfo, at int) (siblings []int, from, to int, ok bool) {
	if _, err := s.validImage(info); err != nil {
		return
	}
	if _, _, standalone := s.imageBlock(info); !standalone {
		return
	}
	d := s.editingDocument()
	parent := d.Block(info.Block).Parent
	from, to = -1, -1
	for block := parent + 1; block < d.Block(parent).Next; block = d.Block(block).Next {
		if d.Block(block).Kind == parser.SourcePrefix {
			continue
		}
		if block == info.Block {
			from = len(siblings)
		}
		start, _ := s.blockRange(d.Block(block))
		start = max(start, s.sourcePrefixEnd(), s.documentPrefixEnd(0))
		if at == start {
			to = len(siblings)
		}
		siblings = append(siblings, block)
	}
	if len(siblings) == 0 || from < 0 {
		return
	}
	_, end := s.blockRange(d.Block(siblings[len(siblings)-1]))
	if at == end || parent == 0 && at == len(s.source) {
		to = len(siblings)
	}
	if to > from {
		to--
	}
	if to < 0 || to == from {
		return
	}
	if parent != 0 {
		for index := min(from, to); index <= max(from, to); index++ {
			kind := d.Block(siblings[index]).Kind
			if kind != parser.Paragraph && kind != parser.Heading {
				return
			}
		}
	}
	ok = true
	return
}

func (s *EditorState) reorderImage(info ImageInfo, at int) error {
	siblings, from, to, ok := s.imageMoveRange(info, at)
	if !ok {
		return errors.New("图片只能移动到同级段落之间")
	}
	d := s.editingDocument()
	first, last := min(from, to), max(from, to)
	type section struct {
		start, end, bodyEnd, logical int
		body, prefix, gap            string
	}
	sections := make([]section, last-first+1)
	nested := d.Block(info.Block).Parent != 0
	for index := range sections {
		block := d.Block(siblings[first+index])
		start, end := s.blockRange(block)
		start = max(start, s.sourcePrefixEnd(), s.documentPrefixEnd(0))
		bodyEnd := end
		for bodyEnd > start && s.source[bodyEnd-1] == '\n' {
			bodyEnd--
		}
		logical := start
		if nested {
			logical = block.Start
		}
		sections[index] = section{start: start, end: end, bodyEnd: bodyEnd, logical: logical, body: s.snapshot.OriginalSlice(logical, bodyEnd), prefix: s.snapshot.OriginalSlice(start, logical)}
	}
	for index := range sections {
		next := sections[index].end
		if index+1 < len(sections) {
			next = sections[index+1].start
		}
		sections[index].gap = s.snapshot.OriginalSlice(sections[index].bodyEnd, next)
	}
	order := make([]int, len(sections))
	for index := range order {
		order[index] = index
	}
	localFrom, localTo := from-first, to-first
	if localTo < localFrom {
		copy(order[localTo+1:localFrom+1], order[localTo:localFrom])
	} else {
		copy(order[localFrom:localTo], order[localFrom+1:localTo+1])
	}
	order[localTo] = localFrom
	var text strings.Builder
	selected := 0
	for position, index := range order {
		text.WriteString(sections[position].prefix)
		if index == localFrom {
			selected = sections[0].start + len(normalizedText(text.String())) + info.Start - sections[index].logical
		}
		text.WriteString(sections[index].body)
		text.WriteString(sections[position].gap)
	}
	err := s.ApplyEdits([]editor.Edit{{Start: sections[0].start, End: sections[len(sections)-1].end, Text: text.String()}}, selected, selected+info.End-info.Start, info.Version)
	s.reveal = false
	return err
}
