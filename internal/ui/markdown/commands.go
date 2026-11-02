package markdownui

import (
	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	"strings"
)

type EditorCommand = md.EditorCommand
type CommandKind = md.CommandKind
type CommandStatus = md.CommandStatus

const (
	Bold        = md.CommandBold
	Italic      = md.CommandItalic
	Strike      = md.CommandStrike
	InlineCode  = md.CommandInlineCode
	Paragraph   = md.CommandParagraph
	Heading     = md.CommandHeading
	Quote       = md.CommandQuote
	BulletList  = md.CommandBulletList
	OrderedList = md.CommandOrderedList
	TaskList    = md.CommandTaskList
	InsertCode  = md.CommandInsertCode
	InsertTable = md.CommandInsertTable
	InsertLink  = md.CommandInsertLink
	InsertImage = md.CommandInsertImage
	DeleteBlock = md.CommandDeleteBlock
)

func (s *EditorState) CommandState(command EditorCommand) CommandStatus {
	if s == nil || s.closed || s.options.ReadOnly || s.Mode() != Live {
		return CommandStatus{}
	}
	return s.native.QueryCommand(command)
}
func (s *EditorState) Execute(command EditorCommand) error {
	if s.closed {
		return ErrClosed
	}
	if s.native.Composing() {
		return ErrComposing
	}
	if !s.CommandState(command).Enabled {
		return mdCommandUnavailable
	}
	s.native.MarkUserInteraction()
	if (command.Kind == InsertLink || command.Kind == InsertImage) && command.Target == "" {
		s.openInsert(command.Kind)
		return nil
	}
	if command.Kind == InsertLink || command.Kind == InsertImage {
		target, err := ResolveLink(s.document.Path, command.Target)
		if err != nil {
			return err
		}
		if command.Kind == InsertImage && target.Path != "" && s.options.OnImportResources != nil {
			s.importResources(ImportRequest{Files: []string{target.Path}, Text: command.Text})
			return nil
		}
	}
	if err := s.native.Execute(command); err != nil {
		return err
	}
	s.native.Focus()
	s.notify()
	return nil
}

func (s *EditorState) commandMenu(menu *ui.Menu) {
	if s.options.ReadOnly || s.Mode() != Live {
		return
	}
	menu.Separator()
	for _, item := range []struct {
		label   string
		command EditorCommand
	}{
		{"加粗 · Ctrl+B", EditorCommand{Kind: Bold}}, {"斜体 · Ctrl+I", EditorCommand{Kind: Italic}},
		{"删除线 · Ctrl+Shift+X", EditorCommand{Kind: Strike}}, {"行内代码 · Ctrl+`", EditorCommand{Kind: InlineCode}},
		{"正文", EditorCommand{Kind: Paragraph}}, {"引用", EditorCommand{Kind: Quote}},
		{"无序列表", EditorCommand{Kind: BulletList}}, {"有序列表", EditorCommand{Kind: OrderedList}}, {"待办列表", EditorCommand{Kind: TaskList}},
		{"插入代码块", EditorCommand{Kind: InsertCode}}, {"插入表格", EditorCommand{Kind: InsertTable}},
		{"插入链接 · Ctrl+K", EditorCommand{Kind: InsertLink}}, {"插入图片", EditorCommand{Kind: InsertImage}},
		{"删除当前块", EditorCommand{Kind: DeleteBlock}},
	} {
		state := s.CommandState(item.command)
		if menu.Item(item.label).Disabled(!state.Enabled).Chosen() {
			s.report(s.Execute(item.command))
		}
	}
	for level := 1; level <= 6; level++ {
		command := EditorCommand{Kind: Heading, Level: level}
		if menu.Item(strings.Repeat("#", level) + " 标题").Disabled(!s.CommandState(command).Enabled).Chosen() {
			s.report(s.Execute(command))
		}
	}
}

func (s *EditorState) floatingToolbar(c *ui.Context) {
	if s.closed || s.options.ReadOnly || s.Mode() != Live || s.insert != nil && s.insert.open {
		return
	}
	rect, ok := s.native.SelectionBounds()
	if !ok {
		return
	}
	width, height := c.Size()
	x, y := max(6, min(rect.X, width-290)), rect.Y-38
	if y < 6 {
		y = min(height-40, rect.Y+rect.H+6)
	}
	version := s.Snapshot().Version
	theme := c.Theme()
	ui.Overlay(c, func() {
		ui.Row(c.Key("markdown-selection-toolbar")).Absolute().Left(x).Top(y).KeepFocus().Padding(4).Gap(2).Radius(8).Background(theme.Surface).Border(1, theme.Border).Children(func() {
			for _, item := range []struct {
				label, tip string
				kind       CommandKind
			}{{"B", "加粗 · Ctrl+B", Bold}, {"I", "斜体 · Ctrl+I", Italic}, {"S", "删除线 · Ctrl+Shift+X", Strike}, {"代码", "行内代码 · Ctrl+`", InlineCode}, {"链接", "插入链接 · Ctrl+K", InsertLink}} {
				command := EditorCommand{Kind: item.kind}
				state := s.CommandState(command)
				button := ui.Button(c, item.label).Label(item.tip).Tooltip(item.tip).KeepFocus().Height(28).MinWidth(30).FontSize(13).Disabled(!state.Enabled)
				if state.Active {
					button.Background(theme.Accent).TextColor(theme.AccentText)
				}
				if button.Clicked() {
					if s.Snapshot().Version != version {
						s.report(ErrStale)
					} else {
						s.report(s.Execute(command))
					}
				}
			}
		})
	})
}
