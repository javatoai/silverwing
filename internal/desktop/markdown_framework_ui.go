package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// The application owns source, resource trust and reader state. MyGO owns the
// complete ordinary Markdown view, including lists, tables and highlighted code.
type frameworkMarkdownState struct {
	original        string
	richTextReading bool
	projection      editor.ReadingProjection
	document        *parser.Document
	blocks          []documentFindBlock
	options         markdown.Options
}

func ensureFrameworkMarkdown(state *markdownEditorState) *frameworkMarkdownState {
	source := state.doc.Source()
	if state.frameworkMarkdown != nil && state.frameworkMarkdown.original == source && state.frameworkMarkdown.richTextReading == state.richTextReading {
		return state.frameworkMarkdown
	}
	projection := editor.ProjectReading(source)
	if state.richTextReading {
		projection = editor.ProjectRichTextReading(source)
	}
	native := &frameworkMarkdownState{original: source, richTextReading: state.richTextReading, projection: projection}
	native.document = parser.Parse(native.projection.Source)
	native.blocks = frameworkMarkdownBlocks(native.document)
	state.frameworkMarkdown = native
	return native
}

func frameworkMarkdownKey(index int) string { return fmt.Sprintf("mygo-markdown:%d", index) }

func frameworkMarkdownBlocks(doc *parser.Document) []documentFindBlock {
	var blocks []documentFindBlock
	for _, visible := range markdown.TextBlocks(doc) {
		index := visible.Index
		block := doc.Block(index)
		blocks = append(blocks, documentFindBlock{key: frameworkMarkdownKey(index), text: visible.Text,
			scrollY: float32(strings.Count(doc.Source()[:block.Start], "\n")) * 24, lineSize: 24})
	}
	return blocks
}

func frameworkMarkdownFindBlocks(state *markdownEditorState) []documentFindBlock {
	if state == nil || state.doc == nil || state.closed {
		return nil
	}
	return ensureFrameworkMarkdown(state).blocks
}

func (native *frameworkMarkdownState) partAt(state *markdownEditorState, offset int) editor.Part {
	raw := native.projection.OriginalOffset(offset)
	for _, part := range state.doc.Parts() {
		if raw >= part.Range.Start && raw < part.Range.End {
			return part
		}
	}
	return editor.Part{ID: uint64(raw+1) | 1<<63}
}

func (s *app) renderFrameworkMarkdown(c *ui.Context, p palette, state *markdownEditorState) {
	native := ensureFrameworkMarkdown(state)
	find := s.markdownFindStateFor(state)
	o := &native.options
	o.Theme = &markdown.Theme{Font: markdownSansFontFamily, FontSize: 15 * state.zoom, LineHeight: 1.6,
		Text: p.text, Muted: p.muted, Link: p.blue, Strong: p.text, Code: p.text, CodeFont: "monospace",
		CodeSize: textEditorFontSize / 15, CodeLineHeight: textEditorLineHeight, CodeColors: textEditorTheme(p),
		CodeBackground: p.soft, CodeBlockBackground: p.soft, CodeBlockBorder: p.line,
		TableBorder: p.line, TableHeader: p.soft, QuoteBar: p.line, QuoteText: p.muted}
	if s.markdown == state {
		s.bindMaterialsReadingContextMenu(state)
	}
	o.ContextMenu = state.contextMenu
	o.OnLink = func(target string) { s.openMarkdownLink(state, target) }
	o.CopySource = func(start, end int) string {
		return native.original[native.projection.OriginalOffset(start):native.projection.OriginalOffset(end)]
	}
	o.TransformText = func(block, offset int, spans []ui.Span) []ui.Span {
		if find == nil || find.query == "" {
			return spans
		}
		var decorated []ui.Span
		for _, span := range spans {
			for _, hit := range find.spans(frameworkMarkdownKey(block), span.Text, offset, span.Color, nil) {
				copy := span
				copy.Text = hit.Text
				if hit.Background.A != 0 {
					copy.Background = hit.Background
				}
				decorated = append(decorated, copy)
			}
			offset += len(span.Text)
		}
		return decorated
	}
	o.OnText = func(c *ui.Context, element ui.Element, block, offset int, spans []ui.Span) {
		if find == nil {
			return
		}
		var text strings.Builder
		for _, span := range spans {
			text.WriteString(span.Text)
		}
		font := ui.Font{Family: markdownSansFontFamily, Size: 15 * state.zoom}
		switch native.document.Block(block).Kind {
		case parser.CodeBlock, parser.HTMLBlock, parser.MathBlock:
			font.Family, font.Size = "Consolas, monospace", 15*state.zoom*.875
		}
		if len(spans) > 0 {
			if spans[0].Font != "" {
				font.Family = spans[0].Font
			}
			if spans[0].Size > 0 {
				font.Size = spans[0].Size
			}
		}
		var scroll *ui.ScrollState
		if state.readonlyReader != nil {
			scroll = readonlyMarkdownReaderScroll(state.readonlyReader, false)
		} else {
			scroll = s.readingScrollState("markdown-native")
		}
		revealDocumentFindRichText(c, element, spans, font, frameworkMarkdownKey(block), text.String(), offset, find, scroll)
	}
	o.Image = func(c *ui.Context, block, start, end int, target, alt string) {
		part := native.partAt(state, start)
		node := editor.Node{Kind: "image", Destination: target, Text: alt, Range: editor.SourceRange{Start: native.projection.OriginalOffset(start), End: native.projection.OriginalOffset(end)}}
		s.renderMarkdownImage(c, p, state, part, node, 15*state.zoom)
	}
	o.Link = func(c *ui.Context, in *parser.Inline, block, offset int) bool {
		raw := native.projection.OriginalOffset(in.Start)
		for _, preview := range native.projection.LinkPreviews {
			if preview.Range.Start == raw && preview.Destination == in.Dest {
				part := native.partAt(state, in.Start)
				s.renderMarkdownLinkPreview(c, p, state, part, preview, 15*state.zoom, frameworkMarkdownKey(block), &offset, find)
				return true
			}
		}
		return false
	}
	o.BlockLink = func(in *parser.Inline) bool {
		raw := native.projection.OriginalOffset(in.Start)
		for _, preview := range native.projection.LinkPreviews {
			if preview.Range.Start == raw && preview.Destination == in.Dest {
				return true
			}
		}
		return false
	}
	o.Code = map[string]func(*ui.Context, *markdown.CodeBlockView){"mermaid": func(c *ui.Context, b *markdown.CodeBlockView) {
		part := native.partAt(state, native.document.Block(b.Index).Start)
		if part.Kind != editor.PartMermaid {
			part.Source = "```mermaid\n" + b.Code() + "```"
			part.Kind = editor.PartMermaid
		}
		s.renderMarkdownMermaid(c, p, state, part)
	}}
	// The native reader owns code layout, selection, highlighting and copy UI.
	// Only clipboard delivery maps back to the host's untouched source bytes.
	o.CodeBlock = nil
	o.CopyCode = func(c *ui.Context, b *markdown.CodeBlockView) bool {
		part := native.partAt(state, native.document.Block(b.Index).Start)
		code := b.Code()
		if part.Kind == editor.PartCode {
			if fence := editor.FindFence(part.Source); fence != nil {
				code = fence.Code(part.Source)
			}
		}
		return s.copyTextToClipboard(c.Services(), code, "已复制")
	}
	markdown.View(c.Key("framework-markdown-"+s.snapshot.Home+"-"+markdownEditorHostID(state)), native.projection.Source, o).FillWidth().MinWidth(0)
}
