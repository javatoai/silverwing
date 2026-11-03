package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func (s *EditorState) imageCompositionLeaf(block int) bool {
	if !s.opts.InPlaceImages || s.SourceMode || !s.composing || s.codeGapComposition() || !s.blockHasImages(block) {
		return false
	}
	b := s.view.doc.Block(block)
	return s.composeStart <= b.End && s.composeEnd >= b.Start
}

// The committed AST still owns the image atoms during IME. Only surrounding
// text uses the existing virtual source/input mapping; no second Buffer or
// synthetic image Markdown is produced for preedit.
func (s *EditorState) renderImageComposition(c *ui.Context, block int, style spanStyle) {
	d, th := s.view.doc, &s.view.th
	b := d.Block(block)
	font := ui.Font{Family: th.Font, Size: th.FontSize, Weight: style.weight}
	color := style.color
	if color == (Color{}) {
		color = th.Text
	}
	ui.Row(c).Wrap().AlignItems(ui.Start).AlignSelf(ui.Stretch).Children(func() {
		at := b.Start
		inlines := d.Inlines(block)
		for index := 0; index < len(inlines); index++ {
			in := inlines[index]
			if in.Kind != parser.Image {
				continue
			}
			if at < in.Start || s.composeStart == at {
				s.literalRun(c, at, in.Start, font, color, th.LineHeight, true)
			}
			s.imageView(c, block, &in, parser.PlainText(inlines[index+1:in.Next]))
			at = in.End
			index = in.Next - 1
		}
		if at < b.End || s.composeStart == at {
			s.literalRun(c, at, b.End, font, color, th.LineHeight, true)
		}
	})
}

func (s *EditorState) imageDropTail(c *ui.Context) {
	if s.composing || len(s.Images()) == 0 {
		return
	}
	at := len(s.source)
	for _, block := range s.blocks {
		if block.start == at {
			return
		}
	}
	box := ui.Box(c.Key("image-drop-tail")).Height(8).AlignSelf(ui.Stretch).Role(ui.RoleGroup).Label("将图片移到文末")
	block := &editorBlock{start: at, end: at, element: box}
	s.blocks = append(s.blocks, block)
	box.Draw(func(_ *ui.Painter, r ui.Rect) { block.rect = r })
}
