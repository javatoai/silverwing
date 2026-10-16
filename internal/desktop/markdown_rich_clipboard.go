package desktop

import (
	"errors"

	"silverwing-go/internal/application"
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func markdownRichClipboardCommand(event ui.InputEvent) string {
	if event.Kind == ui.InputCommand {
		return event.Text
	}
	if event.Kind == ui.InputKeyDown && event.Mods == ui.Cmd {
		switch event.Key {
		case ui.KeyC:
			return "copy"
		case ui.KeyX:
			return "cut"
		case ui.KeyV:
			return "paste"
		}
	}
	return ""
}

func (s *app) handleMarkdownRichClipboardCommand(c *ui.Context, state *markdownEditorState, rich *markdownRichFieldState, event ui.InputEvent) bool {
	command := markdownRichClipboardCommand(event)
	if command != "copy" && command != "cut" && command != "paste" {
		return false
	}
	if !s.markdownRichFieldCurrent(state, rich) || state.doc.Composing() {
		return true
	}
	r := rich.input.getSelection().Range
	if command == "copy" || command == "cut" {
		if r.Start == r.End {
			return true
		}
		plain, fragment, err := rich.model.HTMLUTF16(r.Start, r.End)
		if err == nil {
			err = s.writeMarkdownRichClipboard(c, application.RichClipboardContent{Text: plain, HTML: fragment})
		}
		if err != nil {
			s.lastError = err.Error()
			return true
		}
		if command == "cut" {
			rich.input.ReplaceText(&r, "")
		}
		return true
	}
	content, err := s.readMarkdownRichClipboard(c)
	if err != nil {
		s.lastError = err.Error()
		return true
	}
	if content.HTML == "" && content.Text == "" {
		return true
	}
	if content.HTML == "" {
		rich.input.ReplaceText(&r, content.Text)
		return true
	}
	err = s.commitMarkdownRichMutation(state, rich, false, func(model *editor.RichBlock) error { return model.ReplaceHTMLUTF16(r.Start, r.End, content.HTML) })
	if err != nil {
		s.lastError = err.Error()
		return true
	}
	caret := rich.model.ReplacementCaretUTF16()
	s.installMarkdownRichModel(rich, ui.TextInputSelection{Range: ui.TextInputRange{Start: caret, End: caret}})
	return true
}

func (s *app) readMarkdownRichClipboard(c *ui.Context) (application.RichClipboardContent, error) {
	if backend, ok := s.backend.(application.RichClipboardBackend); ok {
		content, err := backend.ReadRichClipboard()
		if !errors.Is(err, application.ErrRichClipboardUnsupported) {
			return content, err
		}
	}
	if c == nil {
		return application.RichClipboardContent{}, application.ErrRichClipboardUnsupported
	}
	return application.RichClipboardContent{Text: c.ReadClipboard()}, nil
}

func (s *app) writeMarkdownRichClipboard(c *ui.Context, content application.RichClipboardContent) error {
	if backend, ok := s.backend.(application.RichClipboardBackend); ok {
		err := backend.WriteRichClipboard(content)
		if !errors.Is(err, application.ErrRichClipboardUnsupported) {
			return err
		}
	}
	if c == nil {
		return application.ErrRichClipboardUnsupported
	}
	c.WriteClipboard(content.Text)
	return nil
}
