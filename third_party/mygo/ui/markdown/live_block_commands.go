package markdown

import (
	"errors"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strconv"
	"strings"
)

func commandLineContent(line string) (prefix, body string) {
	if strings.HasPrefix(line, "\ufeff") {
		prefix = "\ufeff"
		line = strings.TrimPrefix(line, "\ufeff")
	}
	at := 0
	for at < len(line) && (line[at] == ' ' || line[at] == '\t') {
		at++
	}
	prefix += line[:at]
	body = line[at:]
	for strings.HasPrefix(body, ">") {
		body = strings.TrimLeft(strings.TrimPrefix(body, ">"), " \t")
	}
	if strings.HasPrefix(body, "#") {
		n := 0
		for n < len(body) && body[n] == '#' {
			n++
		}
		if n <= 6 && n < len(body) && body[n] == ' ' {
			body = strings.TrimLeft(body[n:], " \t")
		}
	}
	list := false
	if len(body) >= 2 && strings.ContainsRune("-*+", rune(body[0])) && (body[1] == ' ' || body[1] == '\t') {
		list = true
		body = strings.TrimLeft(body[1:], " \t")
	} else {
		n := 0
		for n < len(body) && body[n] >= '0' && body[n] <= '9' {
			n++
		}
		if n > 0 && n <= 9 && n+1 < len(body) && (body[n] == '.' || body[n] == ')') && (body[n+1] == ' ' || body[n+1] == '\t') {
			list = true
			body = strings.TrimLeft(body[n+1:], " \t")
		}
	}
	if list && len(body) >= 3 && body[0] == '[' && body[2] == ']' && (body[1] == ' ' || body[1] == 'x' || body[1] == 'X') && (len(body) == 3 || body[3] == ' ' || body[3] == '\t') {
		body = strings.TrimLeft(body[3:], " \t")
	}
	return
}

// Only parser-owned markers are removed. A paragraph may contain text such as
// "[x]" or "2." that resembles a task/list prefix without being one.
func (s *EditorState) commandLineRange(start, end int) (indent string, from, to int) {
	from = start
	if start == 0 && strings.HasPrefix(s.source, "\ufeff") {
		from += len("\ufeff")
	}
	for from < end && (s.source[from] == ' ' || s.source[from] == '\t') {
		from++
	}
	indent = s.source[start:from]
	to = end
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind == parser.Heading && d.LineCount(i) == 0 && sourceLineStart(s.source, b.Start) == start {
			at := b.Start
			for at < end && s.source[at] == '#' {
				at++
			}
			for at < end && (s.source[at] == ' ' || s.source[at] == '\t') {
				at++
			}
			from = at
			return
		}
		if b.Kind != parser.Paragraph && b.Kind != parser.Heading {
			continue
		}
		for n := 0; n < d.LineCount(i); n++ {
			text, at := d.Line(i, n)
			if sourceLineStart(s.source, at) == start {
				from = max(from, at)
				to = min(end, at+len(text)-linePad(s.source, text, at))
				return
			}
		}
	}
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind == parser.Item && sourceLineStart(s.source, b.Start) == start {
			from = s.readWritingItem(d, i).content
			return
		}
	}
	// An empty quote has no leaf. Its confirmed container still owns its >.
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind == parser.Quote && sourceLineStart(s.source, b.Start) == start {
			_, body := commandLineContent(s.source[start:end])
			from = end - len(body)
			return
		}
	}
	return
}

func (s *EditorState) convertSelectedLines(command EditorCommand) error {
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	start, end := sourceLineStart(s.source, a), codeLineEnd(s.source, z)
	if z > a && sourceLineStart(s.source, z) == z {
		end = z - 1
	}
	var edits []editor.Edit
	underlines := map[int]bool{}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.Heading {
			continue
		}
		last, lastEnd, setext := s.commandSetextUnderline(d, i)
		first := sourceLineStart(s.source, b.Start)
		if setext && a <= lastEnd && z >= b.Start {
			underlines[last] = true
			start = min(start, first)
			end = max(end, lastEnd)
		}
	}
	number := 0
	for line := start; line <= end; {
		last := min(end, codeLineEnd(s.source, line))
		if underlines[line] {
			edits = append(edits, editor.Edit{Start: line, End: last})
			line = last + 1
			continue
		}
		indent, from, to := s.commandLineRange(line, last)
		prefix := ""
		switch command.Kind {
		case CommandParagraph:
		case CommandHeading:
			if command.Level < 1 || command.Level > 6 {
				return errors.New("标题级别应为1至6")
			}
			prefix = strings.Repeat("#", command.Level) + " "
		case CommandQuote:
			prefix = "> "
		case CommandBulletList:
			prefix = "- "
		case CommandOrderedList:
			prefix = strconv.Itoa(number+1) + ". "
		case CommandTaskList:
			prefix = "- [ ] "
		default:
			return errors.New("未知编辑命令")
		}
		if s.source[line:from] != indent+prefix {
			edits = append(edits, editor.Edit{Start: line, End: from, Text: indent + prefix})
		}
		if to < last {
			edits = append(edits, editor.Edit{Start: to, End: last})
		}
		number++
		line = last + 1
	}
	if len(edits) == 0 {
		return nil
	}
	return s.ApplyEdits(edits, commandOffset(s.anchor, edits), commandOffset(s.caret, edits), s.snapshot.Version())
}

func (s *EditorState) commandSetextUnderline(d *parser.Document, index int) (start, end int, ok bool) {
	b := d.Block(index)
	if b.Kind != parser.Heading || d.LineCount(index) == 0 {
		return
	}
	_, first := d.Line(index, 0)
	if first > b.Start && strings.HasPrefix(s.source[b.Start:first], "#") {
		return
	}
	_, last := d.Line(index, d.LineCount(index)-1)
	start = codeLineEnd(s.source, last) + 1
	if start >= len(s.source) {
		return
	}
	end = codeLineEnd(s.source, start)
	marker := strings.Trim(s.source[start:end], " \t>")
	ok = marker != "" && (strings.Trim(marker, "=") == "" || strings.Trim(marker, "-") == "")
	return
}

func commandOffset(at int, edits []editor.Edit) int {
	delta := 0
	for _, edit := range edits {
		if at < edit.Start {
			break
		}
		if at <= edit.End {
			return edit.Start + delta + len(normalizedText(edit.Text))
		}
		delta += len(normalizedText(edit.Text)) - (edit.End - edit.Start)
	}
	return at + delta
}
