package markdown

import (
	"errors"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
)

// CommandKind identifies source-preserving operations shared by all UI entrypoints.
type CommandKind string

const (
	CommandBold        CommandKind = "bold"
	CommandItalic      CommandKind = "italic"
	CommandStrike      CommandKind = "strike"
	CommandInlineCode  CommandKind = "inline-code"
	CommandParagraph   CommandKind = "paragraph"
	CommandHeading     CommandKind = "heading"
	CommandQuote       CommandKind = "quote"
	CommandBulletList  CommandKind = "bullet-list"
	CommandOrderedList CommandKind = "ordered-list"
	CommandTaskList    CommandKind = "task-list"
	CommandInsertCode  CommandKind = "insert-code"
	CommandInsertTable CommandKind = "insert-table"
	CommandInsertLink  CommandKind = "insert-link"
	CommandInsertImage CommandKind = "insert-image"
	CommandDeleteBlock CommandKind = "delete-block"
)

type EditorCommand struct {
	Kind                 CommandKind
	Level, Rows, Columns int
	Text, Target         string
}
type CommandStatus struct{ Enabled, Active bool }

func (s *EditorState) QueryCommand(command EditorCommand) CommandStatus {
	s.sync()
	status := CommandStatus{Enabled: !s.opts.ReadOnly && !s.composing && !s.SourceMode}
	if !status.Enabled {
		return status
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	_, inCode := s.editingCode(a, z)
	_, _, inTable := s.editingTable()
	if index := inlineCommandIndex(command.Kind); index >= 0 {
		status.Enabled = !inCode && !s.writingFrontmatter(a) && !s.selectionHasLiteralBlock(a, z)
		styles := s.inlineStyles(a, z)
		if a == z && s.formatPending {
			styles = s.pendingFormats
		}
		status.Active = styles[index]
		if a < z {
			status.Active = s.selectedInlineStyleActive(index, a, z)
		}
		return status
	}
	switch command.Kind {
	case CommandDeleteBlock:
		_, _, status.Enabled = s.commandBlockRange()
	case CommandInsertCode, CommandInsertTable, CommandParagraph, CommandHeading, CommandQuote, CommandBulletList, CommandOrderedList, CommandTaskList:
		status.Enabled = !inCode && !inTable && !s.writingFrontmatter(a) && !s.selectionHasLiteralBlock(a, z)
		if status.Enabled {
			status.Active = s.blockCommandActive(command, a, z)
		}
	case CommandInsertLink, CommandInsertImage:
		status.Enabled = !inCode && !s.writingFrontmatter(a) && !s.selectionHasLiteralBlock(a, z)
	default:
		status.Enabled = false
	}
	return status
}

func (s *EditorState) selectedInlineStyleActive(index, a, z int) bool {
	d := s.editingDocument()
	any := false
	for b := 1; b < d.Len(); b++ {
		kind := d.Block(b).Kind
		if kind != parser.Paragraph && kind != parser.Heading && kind != parser.TableCell || d.LineCount(b) == 0 {
			continue
		}
		_, first := d.Line(b, 0)
		text, last := d.Line(b, d.LineCount(b)-1)
		left, right := max(a, first), min(z, last+len(text)-linePad(s.source, text, last))
		if left >= right || strings.TrimSpace(s.source[left:right]) == "" {
			continue
		}
		any = true
		if _, _, _, _, active := s.inlineContainer(index, left, right); !active {
			return false
		}
	}
	return any
}

func (s *EditorState) blockCommandActive(command EditorCommand, a, z int) bool {
	d := s.editingDocument()
	heading, quote, list, task := false, false, false, false
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		start, end := s.blockRange(b)
		if a < start || z > end {
			continue
		}
		switch b.Kind {
		case parser.Heading:
			heading = true
			if command.Kind == CommandHeading && command.Level == b.Level {
				return true
			}
		case parser.Quote:
			quote = true
		case parser.Item:
			list = true
			item := s.readWritingItem(d, i)
			task = task || item.task
			if command.Kind == CommandOrderedList && b.Ordered || command.Kind == CommandBulletList && !b.Ordered && !item.task {
				return true
			}
		}
	}
	switch command.Kind {
	case CommandParagraph:
		return !heading && !quote && !list
	case CommandQuote:
		return quote
	case CommandTaskList:
		return task
	}
	return false
}

func (s *EditorState) Execute(command EditorCommand) error {
	if !s.QueryCommand(command).Enabled {
		return errors.New("当前选区不能执行此编辑操作")
	}
	s.MarkUserInteraction()
	if index := inlineCommandIndex(command.Kind); index >= 0 {
		return s.formatSelection(index)
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	s.formatPending = false
	switch command.Kind {
	case CommandInsertCode:
		body := s.source[a:z]
		fence := parser.BacktickFence(body, 3)
		if strings.ContainsAny(command.Target, "`\r\n") {
			return errors.New("代码语言不应包含围栏或换行")
		}
		return s.insertCommandBlock(fence+command.Target+"\n"+body+"\n"+fence, len(fence+command.Target)+1)
	case CommandInsertTable:
		rows, columns := command.Rows, command.Columns
		if rows <= 0 {
			rows = 3
		}
		if columns <= 0 {
			columns = 3
		}
		if rows > 1000 || columns > 100 {
			return errors.New("表格尺寸过大")
		}
		var lines []string
		for row := 0; row < rows+1; row++ {
			cells := make([]string, columns)
			for i := range cells {
				if row == 1 {
					cells[i] = "---"
				} else {
					cells[i] = " "
				}
			}
			lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		}
		return s.insertCommandBlock(strings.Join(lines, "\n"), 2)
	case CommandInsertLink, CommandInsertImage:
		label := command.Text
		fromSelection := label == "" && a != z
		if label == "" {
			label = s.source[a:z]
		}
		if label == "" {
			label = command.Target
		}
		target := strings.TrimSpace(command.Target)
		if target == "" {
			return errors.New("请输入链接地址")
		}
		escape := strings.NewReplacer("&", "&amp;", "\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ", "\r", "")
		encodedLabel := escape.Replace(label)
		if fromSelection {
			encodedLabel = s.selectedLinkLabel(a, z)
		}
		prefix := "["
		if command.Kind == CommandInsertImage {
			prefix = "!["
		}
		text := prefix + encodedLabel + "](<" + parser.EscapeLinkDestination(target) + ">)"
		if command.Kind == CommandInsertImage && s.opts.InPlaceImages {
			return s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{text})
		}
		if _, _, cell := s.editingTable(); cell {
			text = escapeEditorTableText(text)
		}
		if fromSelection && command.Kind == CommandInsertLink {
			return s.insertMarkdownAt(s.CaptureInsertionPoint(), text, func(edit editor.Edit) error {
				before, after, err := s.inlineEditSignatures([]editor.Edit{edit})
				if err != nil {
					return err
				}
				if before.plain != after.plain || before.styles != after.styles {
					return errors.New("该选区的 Markdown 链接无法安全转换")
				}
				return nil
			})
		}
		return s.InsertMarkdown(a, z, text, s.snapshot.Version())
	case CommandDeleteBlock:
		start, end, ok := s.commandBlockRange()
		if !ok {
			return errors.New("没有可删除的块")
		}
		return s.ApplyEdits([]editor.Edit{{Start: start, End: end}}, start, start, s.snapshot.Version())
	default:
		return s.convertSelectedLines(command)
	}
}

func (s *EditorState) insertCommandBlock(text string, bodyOffset int) error {
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	prefix, suffix := "", ""
	if a > 0 {
		if s.source[a-1] != '\n' {
			prefix = "\n\n"
		} else if a > 1 && s.source[a-2] != '\n' {
			prefix = "\n"
		}
	}
	if z < len(s.source) {
		if s.source[z] != '\n' {
			suffix = "\n\n"
		} else if z+1 < len(s.source) && s.source[z+1] != '\n' {
			suffix = "\n"
		}
	}
	at := a + len(prefix) + bodyOffset
	text = prefix + text + suffix
	return s.ApplyEdits([]editor.Edit{{Start: a, End: z, Text: strings.ReplaceAll(text, "\n", s.buffer.LineEnding())}}, at, at, s.snapshot.Version())
}

func (s *EditorState) commandBlockRange() (int, int, bool) {
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	d := s.editingDocument()
	start, end, found := 0, 0, false
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.CodeBlock && b.Kind != parser.Table && b.Kind != parser.Quote && b.Kind != parser.Heading {
			continue
		}
		left, right := s.blockRange(b)
		if a >= left && z <= right {
			start, end, found = s.documentPrefixEnd(left), right, true
		}
	}
	return start, end, found
}

func (s *EditorState) selectionHasLiteralBlock(a, z int) bool {
	if a == z && s.focusedCodeGap() != nil {
		return false
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind == parser.Table {
			start, end := s.blockRange(b)
			if a < end && z > start || a == z && a >= start && a < end {
				_, cell, ok := s.editingTable()
				if !ok || a < cell.sourceStart || z > cell.sourceEnd {
					return true
				}
			}
		}
		if b.Kind != parser.CodeBlock && b.Kind != parser.HTMLBlock && b.Kind != parser.MathBlock {
			continue
		}
		start, end := s.blockRange(b)
		if a < end && z > start || a == z && a >= start && a < end {
			return true
		}
	}
	return false
}

// SelectionBounds supplies actual visible geometry for contextual controls.
func (s *EditorState) currentSelectionBounds() (ui.Rect, bool) {
	if s.anchor == s.caret || s.composing || s.dragging {
		return ui.Rect{}, false
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	for _, run := range s.runs {
		if run.layout == nil || len(run.boundaries) == 0 || z <= run.boundaries[0] || a >= run.boundaries[len(run.boundaries)-1] {
			continue
		}
		for _, r := range run.layout.SelectionRects(ui.TextInputRange{Start: run.unitForSource(a), End: run.unitForSource(z)}) {
			r.X += run.rect.X
			r.Y += run.rect.Y
			if r.Y+r.H >= s.rect.Y && r.Y <= s.rect.Y+s.rect.H {
				return r, true
			}
		}
	}
	return ui.Rect{}, false
}

// SelectionBounds returns the last completed layout for this exact selection.
func (s *EditorState) SelectionBounds() (ui.Rect, bool) {
	return s.selectionGeometry, s.selectionGeometryValid && s.focused && !s.composing && !s.dragging && s.selectionGeometryVersion == s.buffer.Version() && s.selectionGeometryAnchor == s.anchor && s.selectionGeometryCaret == s.caret
}
func (s *EditorState) cacheSelectionBounds() {
	rect, valid := s.currentSelectionBounds()
	changed := rect != s.selectionGeometry || valid != s.selectionGeometryValid || s.selectionGeometryVersion != s.snapshot.Version() || s.selectionGeometryAnchor != s.anchor || s.selectionGeometryCaret != s.caret
	s.selectionGeometry, s.selectionGeometryValid = rect, valid
	s.selectionGeometryVersion, s.selectionGeometryAnchor, s.selectionGeometryCaret = s.snapshot.Version(), s.anchor, s.caret
	if changed {
		s.invalidate()
	}
}
