package desktop

import (
	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

// nativeCodePreviewState owns one bounded, read-only editor for one visible
// document. Keeping it on the screen state preserves editor navigation across
// frames without retaining editors after their owner is discarded.
type nativeCodePreviewState struct {
	identity string
	source   string
	view     *codeeditor.State
}

func (s *nativeCodePreviewState) bind(identity, filename, source string) *codeeditor.State {
	if s.view == nil || s.identity != identity || s.source != source {
		buffer := codeeditor.NewBufferPreservingLineEndings(source)
		buffer.SetLanguage(textEditorLanguage(filename))
		s.view = &codeeditor.State{
			Buffer:      buffer,
			ReadOnly:    true,
			LineNumbers: codeeditor.LineNumbersOn,
			Label:       filename,
		}
		s.identity, s.source = identity, source
	}
	s.view.ReadOnly = true
	s.view.LineNumbers = codeeditor.LineNumbersOn
	return s.view
}

func renderNativeCodePreview(c *ui.Context, p palette, owner *nativeCodePreviewState, identity, filename, source, key string) ui.Element {
	view := owner.bind(identity, filename, source)
	return ui.Box(c.Key(key)).FillWidth().MinHeight(0).Padding(5).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
		TextEditor(c, p, view).Grow(1).FillWidth()
	})
}
