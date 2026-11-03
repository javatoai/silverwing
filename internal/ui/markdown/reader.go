package markdownui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// CopyNativeOptions copies only public configuration. The private resource
// cache stays with the receiving component and is never copied across owners.
func CopyNativeOptions(dst, src *md.Options) {
	if src == nil {
		src = &md.Options{}
	}
	dst.Theme, dst.Extensions, dst.OnLink, dst.LoadImage = src.Theme, src.Extensions, src.OnLink, src.LoadImage
	dst.CodeBlock, dst.Code, dst.WrapCode = src.CodeBlock, src.Code, src.WrapCode
	dst.Math, dst.InlineMath, dst.HTML, dst.Block = src.Math, src.InlineMath, src.HTML, src.Block
	dst.SourcePrefix = src.SourcePrefix
	dst.Reveal, dst.ShareSelection = src.Reveal, src.ShareSelection
	dst.TransformText, dst.OnText, dst.Image, dst.Link, dst.BlockLink = src.TransformText, src.OnText, src.Image, src.Link, src.BlockLink
	dst.LinkView = src.LinkView
	dst.ImageContent, dst.OnImageOpen, dst.ValidateImage = src.ImageContent, src.OnImageOpen, src.ValidateImage
	dst.CopySource, dst.CopyCode, dst.ContextMenu = src.CopySource, src.CopyCode, src.ContextMenu
}

func (s *ReaderState) prepare(c *ui.Context, options ReaderOptions) *md.Options {
	if options.Find != nil && s.find != options.Find {
		s.find.remove(s.document.ID)
		s.find = options.Find
		s.findModeChanged = false
	}
	// A mode change remaps match coordinates, not the user's native selection.
	// Explicit navigation and a query/index changed by the caller still reveal.
	remapOnly := s.findModeChanged && s.find.Query == s.findModeQuery && s.find.Current == s.findModeCurrent && !s.find.reveal
	s.findModeChanged = false
	CopyNativeOptions(&s.native, options.Native)
	s.configureProperties(&s.native, nil, nil)
	if options.Theme != nil {
		s.native.Theme = options.Theme
	}
	if s.zoom != 1 {
		theme := md.Theme{}
		if s.native.Theme != nil {
			theme = *s.native.Theme
		}
		if theme.FontSize == 0 {
			theme.FontSize = md.DefaultTheme(c.Theme()).FontSize
		}
		theme.FontSize *= s.zoom
		s.native.Theme = &theme
	}
	if options.OnNavigate != nil {
		s.native.OnLink = func(raw string) {
			if s.closed {
				return
			}
			target, err := ResolveLink(s.document.Path, raw)
			if err != nil {
				s.errorText = err.Error()
				if options.OnError != nil {
					options.OnError(err)
				}
				return
			}
			options.OnNavigate(target)
		}
	}
	baseTransform, baseText := s.native.TransformText, s.native.OnText
	if options.ContentOnly && s.mode != Source && s.find.Query == "" && s.revealLine == 0 && s.revealFragment == "" {
		if s.find.documents != nil {
			s.find.update(s.document.ID, nil)
		}
		return &s.native
	}
	if s.parsed == nil {
		s.parsed = parser.New(parser.Default)
		s.parsed.SetWithPolicy(s.document.Source, s.native.SourcePrefix(s.document.Source), nil)
	}
	doc := s.parsed
	texts := map[int]string{}
	var localMatches []findMatch
	for _, block := range md.TextBlocks(doc) {
		texts[block.Index] = block.Text
		for _, hit := range foldedMatches(block.Text, s.find.Query) {
			localMatches = append(localMatches, findMatch{block.Index, hit[0], hit[1], s.document.ID})
		}
	}
	if s.mode == Source {
		localMatches = nil
		normalized := strings.ReplaceAll(strings.ReplaceAll(s.document.Source, "\r\n", "\n"), "\r", "\n")
		for _, hit := range foldedMatches(normalized, s.find.Query) {
			localMatches = append(localMatches, findMatch{-1, hit[0], hit[1], s.document.ID})
		}
	}
	s.find.update(s.document.ID, localMatches)
	if remapOnly {
		s.find.reveal = false
	}
	heading := headingNamed(doc, s.revealFragment)
	s.native.TransformText = func(block, offset int, spans []ui.Span) []ui.Span {
		if baseTransform != nil {
			spans = baseTransform(block, offset, spans)
		}
		return highlight(spans, offset, foldedMatches(texts[block], s.find.Query), ui.RGBA(242, 178, 41, .35))
	}
	s.native.OnText = func(ctx *ui.Context, element ui.Element, block, offset int, spans []ui.Span) {
		if baseText != nil {
			baseText(ctx, element, block, offset, spans)
		}
		if s.revealLine > 0 {
			b := doc.Block(block)
			start := strings.Count(s.document.Source[:b.Start], "\n") + 1
			end := start + strings.Count(s.document.Source[b.Start:b.End], "\n")
			if s.revealLine >= start && s.revealLine <= end {
				element.ScrollIntoView()
				s.revealLine, s.revealFragment = 0, ""
			}
		}
		if s.revealLine == 0 && s.revealFragment != "" && block == heading {
			element.ScrollIntoView()
			s.revealFragment = ""
		}
		if hit, ok := s.find.revealFor(s.document.ID); ok {
			length := 0
			for _, span := range spans {
				length += len(span.Text)
			}
			if hit.documentID == s.document.ID && hit.block == block && hit.start >= offset && hit.start < offset+length {
				element.ScrollIntoView()
				s.find.revealed(hit)
			}
		}
	}
	baseMenu := s.native.ContextMenu
	s.native.ContextMenu = func(ctx *ui.Context, menu *ui.Menu) {
		if baseMenu != nil {
			baseMenu(ctx, menu)
		}
		if options.ContextMenu != nil {
			options.ContextMenu(ctx, menu)
		}
		if options.ContentOnly {
			// An editor owns this reader's modes, including its source view.
			if s.onModeChanged != nil {
				return
			}
			if s.mode == Source && menu.Item("查看 Markdown 预览").Chosen() {
				s.SetMode(Preview)
				if s.onModeChanged != nil {
					s.onModeChanged(Preview)
				}
			}
			return
		}
		menu.Separator()
		if menu.Item("查找正文 · Ctrl+F").Chosen() {
			s.find.Open = true
			s.focusFind = true
		}
		if s.onModeChanged != nil {
			return
		}
		label := "查看 Markdown 源码"
		if s.mode == Source {
			label = "查看 Markdown 预览"
		}
		if menu.Item(label).Chosen() {
			if s.mode == Source {
				s.SetMode(Preview)
			} else {
				s.SetMode(Source)
			}
			if s.onModeChanged != nil {
				s.onModeChanged(s.mode)
			} else {
				s.find.requestReveal()
			}
		}
	}
	return &s.native
}

// MarkdownReader renders a self-contained, read-only document. ContentOnly
// allows a composed page to keep its own scroll container and combined find.
func MarkdownReader(c *ui.Context, s *ReaderState, options ReaderOptions) ui.Element {
	if s == nil || s.closed {
		return ui.Text(c, "Markdown 预览暂不可用。")
	}
	root := ui.Column(c.Key("markdown-reader-" + s.document.ID)).FillWidth().MinWidth(0).MinHeight(0)
	root.Draw(func(_ *ui.Painter, r ui.Rect) {
		if r.W > 0 && s.properties.viewportWidth != r.W {
			s.properties.viewportWidth = r.W
			c.Invalidate()
		}
	})
	native := s.prepare(c, options)
	if options.ContentOnly {
		return root.Children(func() {
			if s.mode == Source {
				s.renderEmbeddedSource(c)
				return
			}
			md.View(c.Key("markdown-content"), s.document.Source, native).FillWidth().MinWidth(0)
		})
	}
	root.AlignSelf(ui.Stretch)
	root.Focusable().ContextMenu(func(menu *ui.Menu) { menu.EditItems(); native.ContextMenu(c, menu) })
	root.OnShortcut(ui.Cmd, ui.KeyF, func() { s.find.Open = true; s.focusFind = true })
	root.OnShortcut(0, ui.KeyF3, func() { s.moveFind(c, 1) })
	root.OnShortcut(ui.Shift, ui.KeyF3, func() { s.moveFind(c, -1) })
	root.HandleInput(s.zoomInput)
	root.Children(func() {
		if s.find.Open {
			s.renderFind(c)
		}
		if s.errorText != "" {
			ui.Text(c, s.errorText).FontSize(12).TextColor(ui.Hex("#c43d3d"))
		}
		if s.mode == Source {
			s.ensureSourceView()
			s.sourceView.Font = ui.Font{Family: "monospace", Size: 14 * s.zoom}
			// A scrollable native code editor otherwise consumes the wheel
			// before the reader root receives it. Retain its other input paths.
			s.sourceView.OnInput = s.zoomInput
			if at, ok := locationOffset(s.sourceView.Buffer.Snapshot().String(), s.revealLine, 0, s.revealFragment); ok {
				s.sourceView.Select(at, at)
				s.revealLine, s.revealFragment = 0, ""
			}
			s.find.current()
			s.sourceView.FindHighlights = nil
			for i, hit := range s.find.matches {
				if hit.documentID == s.document.ID {
					s.sourceView.FindHighlights = append(s.sourceView.FindHighlights, code.FindHighlight{Start: hit.start, End: hit.end, Current: i == s.find.Current})
				}
			}
			if hit, ok := s.find.revealFor(s.document.ID); ok {
				s.sourceView.Select(hit.start, hit.end)
				s.find.revealed(hit)
			}
			s.sourceView.ContextMenu = func(menu *ui.Menu) { native.ContextMenu(c, menu) }
			code.View(c.Key("markdown-source"), s.sourceView).Grow(1).FillWidth()
			return
		}
		ui.ScrollBoth(c.Key("markdown-scroll")).TrackScroll(&s.scroll).Grow(1).MinHeight(0).Children(func() {
			body := md.View(c.Key("markdown-body"), s.document.Source, native).FillWidth().MinWidth(0)
			if s.properties.viewportWidth > 0 {
				// A two-axis scroll measures content with unbounded width. Keep
				// prose and card controls within the actual reader viewport.
				body.Width(s.properties.viewportWidth)
			}
		})
	})
	return root
}

func (s *ReaderState) zoomInput(event ui.InputEvent) bool {
	if event.Kind != ui.InputScroll || event.Mods&ui.Ctrl == 0 || event.DY == 0 {
		return false
	}
	if event.DY > 0 {
		s.zoom = max(.5, s.zoom-.1)
	} else {
		s.zoom = min(3, s.zoom+.1)
	}
	return true
}

func (s *ReaderState) renderFind(c *ui.Context) {
	ui.Row(c.Key("markdown-find")).FillWidth().Gap(6).Padding(4).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key("query"), &s.find.Query).Label("查找正文").Placeholder("查找正文").Grow(1).MinWidth(40).Height(30).OnChange(func() { s.find.Current = 0; s.find.requestReveal() })
		if s.focusFind {
			input.Focus()
			s.focusFind = false
		}
		s.find.current()
		ui.Text(c, fmt.Sprintf("%d / %d", min(s.find.Current+1, len(s.find.matches)), len(s.find.matches))).FontSize(12)
		if ui.Button(c, "上一个").Clicked() {
			s.moveFind(c, -1)
		}
		if ui.Button(c, "下一个").Clicked() {
			s.moveFind(c, 1)
		}
		if ui.Button(c, "关闭").Clicked() {
			s.find.Open = false
			s.find.Query = ""
		}
		input.OnShortcut(0, ui.KeyEnter, func() { s.moveFind(c, 1) }).OnShortcut(ui.Shift, ui.KeyEnter, func() { s.moveFind(c, -1) }).OnShortcut(0, ui.KeyEscape, func() { s.find.Open = false; s.find.Query = "" })
	})
}

func (s *ReaderState) moveFind(c *ui.Context, delta int) {
	s.find.move(delta)
	// The destination can precede this find bar in a composed page's frame.
	c.Invalidate()
}

// PlainLinkLabel extracts the visible label while retaining the original label
// source separately when only the destination is edited.
func PlainLinkLabel(source string, in parser.Inline) string {
	if in.Start < 0 || in.End > len(source) || in.Start >= in.End {
		return in.Dest
	}
	doc := parser.Parse(source)
	for block := 1; block < doc.Len(); block++ {
		inlines := doc.Inlines(block)
		for index, link := range inlines {
			if link.Kind != parser.Link || link.Start != in.Start || link.End != in.End {
				continue
			}
			var text strings.Builder
			for child := index + 1; child < link.Next; child++ {
				node := inlines[child]
				switch node.Kind {
				case parser.Text, parser.Code, parser.Math:
					text.WriteString(node.Text)
				case parser.SoftBreak, parser.LineBreak:
					text.WriteByte(' ')
				case parser.Image:
					child = node.Next - 1
				}
			}
			if text.Len() > 0 {
				return text.String()
			}
			return in.Dest
		}
	}
	return in.Dest
}
