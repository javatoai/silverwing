package editor

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"silverwing-go/internal/markdowntext"

	"github.com/yuin/goldmark/util"
)

// RichBlock is the editable presentation of one contiguous RICH source group.
// Opening, selecting and moving its caret never rewrite Markdown. After an
// edit only this group's Markdown is serialized; Document owns all other parts.
type RichBlock struct {
	original         string
	newline          string
	prefix           string
	suffix           string
	paragraphs       []richParagraph
	dirty            bool
	pending          *RichStyle
	replacementCaret int
}

type RichStyle struct {
	Bold, Italic, Strike, Code bool
	Link, Title                string
}

type RichSpan struct {
	Text       string
	Style      RichStyle
	Heading    int
	ListPrefix bool
}

type richGlyph struct {
	value  rune
	style  RichStyle
	prefix bool
}
type richParagraph struct {
	glyphs        []richGlyph
	heading       int
	list          string
	depth, number int
}

func NewRichBlock(part Part) (*RichBlock, error) {
	if part.Kind != PartRich {
		return nil, fmt.Errorf("%s is not a rich Markdown group", part.Kind)
	}
	b := &RichBlock{original: part.Source, newline: detectNewline(part.Source)}
	body := strings.TrimLeft(part.Source, "\r\n")
	b.prefix = part.Source[:len(part.Source)-len(body)]
	b.suffix = body[len(strings.TrimRight(body, "\r\n")):]
	semanticPart := Part{
		Kind:   PartRich,
		Range:  SourceRange{Start: 0, End: len(part.Source)},
		Source: part.Source,
		Nodes:  richSemanticNodes(part.Source),
	}
	var appendNodes func([]Node, string, int, int)
	appendNodes = func(nodes []Node, list string, depth, number int) {
		for _, node := range nodes {
			switch node.Kind {
			case "heading", "paragraph":
				p := richParagraph{heading: node.Level, list: list, depth: depth, number: number}
				p.glyphs = richInlineGlyphs(node.Children, RichStyle{})
				b.paragraphs = append(b.paragraphs, p)
			case "list":
				kind, start := "unordered", 1
				if node.Ordered {
					kind = "ordered"
					lo, hi := node.Range.Start-semanticPart.Range.Start, node.Range.End-semanticPart.Range.Start
					if lo >= 0 && hi >= lo && hi <= len(semanticPart.Source) {
						if match := regexp.MustCompile(`(?m)^\s*([0-9]+)[.)]\s`).FindStringSubmatch(semanticPart.Source[lo:hi]); match != nil {
							start, _ = strconv.Atoi(match[1])
						}
					}
				}
				for i, item := range node.Children {
					var inline, nested []Node
					for _, child := range item.Children {
						if child.Kind == "list" {
							nested = append(nested, child)
						} else {
							inline = append(inline, child)
						}
					}
					p := richParagraph{glyphs: richInlineGlyphs(inline, RichStyle{}), list: kind, depth: depth, number: start + i}
					b.paragraphs = append(b.paragraphs, p)
					appendNodes(nested, kind, depth+1, start+i)
				}
			default:
				if len(node.Children) > 0 {
					appendNodes(node.Children, list, depth, number)
				} else if node.Text != "" {
					b.paragraphs = append(b.paragraphs, richParagraph{glyphs: richTextGlyphs(unescapeRichText(node.Text), RichStyle{})})
				}
			}
		}
	}
	appendNodes(semanticPart.Nodes, "", 0, 1)
	if len(b.paragraphs) == 0 {
		b.paragraphs = []richParagraph{{glyphs: richTextGlyphs(strings.TrimRight(part.Source, "\r\n"), RichStyle{})}}
	}
	for i := range b.paragraphs {
		b.addListPrefix(&b.paragraphs[i])
	}
	return b, nil
}

// richSemanticNodes parses one already-classified RICH group independently of
// whole-document part classification. The document parser intentionally
// preserves some syntax as ORIGINAL (for example HTML-looking text inside an
// inline code span); once a source range is already a RICH editor group, the
// rich editor still renders and edits its complete Markdown source.
func richSemanticNodes(source string) []Node {
	nodes := markdowntext.SemanticNodes(source)
	for i := range nodes {
		nodes[i] = preserveRichLineBreaks(nodes[i], source)
	}
	return nodes
}

func preserveRichLineBreaks(node Node, source string) Node {
	for index := range node.Children {
		node.Children[index] = preserveRichLineBreaks(node.Children[index], source)
	}
	if !richInlineContainer(node.Kind) || len(node.Children) < 2 {
		return node
	}

	children := make([]Node, 0, len(node.Children)*2-1)
	children = append(children, node.Children[0])
	for index := 1; index < len(node.Children); index++ {
		previous, current := node.Children[index-1], node.Children[index]
		if !hasRichBreakAttribute(previous) && previous.Range.End >= 0 && current.Range.Start >= previous.Range.End && current.Range.Start <= len(source) {
			gap := source[previous.Range.End:current.Range.Start]
			if kind := richBreakKind(gap); kind != "" {
				children = append(children, Node{Kind: kind, Range: SourceRange{Start: previous.Range.End, End: current.Range.Start}})
			}
		}
		children = append(children, current)
	}
	node.Children = children
	return node
}

func richInlineContainer(kind string) bool {
	switch kind {
	case "paragraph", "heading", "strong", "emphasis", "strike", "link", "list-item", "table-cell":
		return true
	default:
		return false
	}
}

func hasRichBreakAttribute(node Node) bool {
	return node.Attributes["hardLineBreak"] == "true" || node.Attributes["softLineBreak"] == "true"
}

func richBreakKind(gap string) string {
	normalized, _ := normalizeLines(gap)
	if strings.Count(normalized, "\n") != 1 {
		return ""
	}
	lineEnd := strings.IndexByte(normalized, '\n')
	line := normalized[:lineEnd]
	withoutSpaces := strings.TrimRight(line, " \t")
	spaceCount := len(line) - len(withoutSpaces)
	if spaceCount >= 2 {
		return "hard-break"
	}
	backslashes := 0
	for index := len(withoutSpaces) - 1; index >= 0 && withoutSpaces[index] == '\\'; index-- {
		backslashes++
	}
	if backslashes%2 == 1 {
		return "hard-break"
	}
	return "soft-break"
}

func richInlineGlyphs(nodes []Node, inherited RichStyle) []richGlyph {
	var out []richGlyph
	for _, n := range nodes {
		if n.Kind == "hard-break" {
			out = append(out, richGlyph{value: '\n', style: inherited})
			continue
		}
		if n.Kind == "soft-break" {
			out = append(out, richGlyph{value: ' ', style: inherited})
			continue
		}
		style := inherited
		switch n.Kind {
		case "strong":
			style.Bold = true
		case "emphasis":
			style.Italic = true
		case "strike":
			style.Strike = true
		case "code":
			style.Code = true
		case "link":
			style.Link, style.Title = n.Destination, n.Title
		}
		if n.Kind == "code" && n.Text != "" {
			out = append(out, richTextGlyphs(n.Text, style)...)
		} else if len(n.Children) > 0 {
			out = append(out, richInlineGlyphs(n.Children, style)...)
		} else {
			out = append(out, richTextGlyphs(unescapeRichText(n.Text), style)...)
		}
		if n.Attributes["hardLineBreak"] == "true" {
			out = append(out, richGlyph{value: '\n', style: style})
		} else if n.Attributes["softLineBreak"] == "true" {
			out = append(out, richGlyph{value: ' ', style: style})
		}
	}
	return out
}

func unescapeRichText(raw string) string {
	var out strings.Builder
	runes := []rune(html.UnescapeString(raw))
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] < 128 && util.IsPunct(byte(runes[i+1])) {
			i++
		}
		out.WriteRune(runes[i])
	}
	return out.String()
}
func richTextGlyphs(text string, style RichStyle) []richGlyph {
	out := make([]richGlyph, 0, utf8.RuneCountInString(text))
	for _, r := range text {
		out = append(out, richGlyph{value: r, style: style})
	}
	return out
}
func richGlyphText(glyphs []richGlyph) string {
	var out strings.Builder
	for _, g := range glyphs {
		out.WriteRune(g.value)
	}
	return out.String()
}
func richGlyphUnits(glyphs []richGlyph) int {
	n := 0
	for _, g := range glyphs {
		n++
		if g.value > 0xffff {
			n++
		}
	}
	return n
}

func (b *RichBlock) Text() string {
	parts := make([]string, len(b.paragraphs))
	for i, p := range b.paragraphs {
		parts[i] = richGlyphText(p.glyphs)
	}
	return strings.Join(parts, "\n")
}

// Clone returns a deep copy suitable for keeping a rollback snapshot before a
// host commits the block's serialized Source.
func (b *RichBlock) Clone() *RichBlock {
	if b == nil {
		return nil
	}
	clone := *b
	clone.paragraphs = cloneRichParagraphs(b.paragraphs)
	if b.pending != nil {
		pending := *b.pending
		clone.pending = &pending
	}
	return &clone
}

func cloneRichParagraphs(paragraphs []richParagraph) []richParagraph {
	result := make([]richParagraph, len(paragraphs))
	for i := range paragraphs {
		result[i] = paragraphs[i]
		result[i].glyphs = append([]richGlyph(nil), paragraphs[i].glyphs...)
	}
	return result
}

func (b *RichBlock) restore(snapshot *RichBlock) {
	if b == nil || snapshot == nil {
		return
	}
	*b = *snapshot.Clone()
}

// ReplacementCaretUTF16 reports the visible caret position left by the most
// recent successful text/HTML replacement. It includes generated list markers,
// so callers can keep the insertion point after an automatically continued
// bullet or ordered-list prefix.
func (b *RichBlock) ReplacementCaretUTF16() int {
	if b == nil {
		return 0
	}
	return b.replacementCaret
}
func (b *RichBlock) Spans() []RichSpan {
	var spans []RichSpan
	for i, p := range b.paragraphs {
		if i > 0 {
			spans = append(spans, RichSpan{Text: "\n"})
		}
		for start := 0; start < len(p.glyphs); {
			key := p.glyphs[start]
			var text strings.Builder
			for start < len(p.glyphs) {
				glyph := p.glyphs[start]
				if glyph.style != key.style || glyph.prefix != key.prefix {
					break
				}
				text.WriteRune(glyph.value)
				start++
				if glyph.value == '\n' {
					break
				}
			}
			spans = append(spans, RichSpan{Text: text.String(), Style: key.style, Heading: p.heading, ListPrefix: key.prefix})
		}
	}
	return spans
}
func (b *RichBlock) Source() string {
	if !b.dirty {
		return b.original
	}
	return b.serializedSource()
}

func (b *RichBlock) serializedSource() string {
	var out strings.Builder
	for i, p := range b.paragraphs {
		if i > 0 {
			previous := b.paragraphs[i-1]
			if previous.list != "" && p.list != "" {
				out.WriteString(b.newline)
			} else {
				out.WriteString(b.newline + b.newline)
			}
		}
		glyphs := make([]richGlyph, 0, len(p.glyphs))
		for _, glyph := range p.glyphs {
			if !glyph.prefix {
				glyphs = append(glyphs, glyph)
			}
		}
		if p.list != "" {
			out.WriteString(strings.Repeat("  ", p.depth))
			if p.list == "ordered" {
				out.WriteString(strconv.Itoa(max(1, p.number)) + ". ")
			} else {
				out.WriteString("- ")
			}
		} else if p.heading > 0 {
			out.WriteString(strings.Repeat("#", p.heading) + " ")
		}
		out.WriteString(strings.ReplaceAll(serializeRichGlyphs(glyphs), "\n", b.newline))
	}
	return b.prefix + out.String() + b.suffix
}

func (b *RichBlock) transact(change func() error) error {
	if b == nil {
		return errors.New("富文本块不可用")
	}
	before := b.Clone()
	beforeSource := before.Source()
	if err := change(); err != nil {
		b.restore(before)
		return err
	}
	if equalRichParagraphs(before.paragraphs, b.paragraphs) {
		return nil
	}
	serialized := b.serializedSource()
	if serialized != beforeSource && len(serialized) > MaxDocumentBytes {
		b.restore(before)
		return errors.New("富文本内容超过 3 MiB（含 BOM）")
	}
	b.dirty = serialized != b.original
	return nil
}

func equalRichParagraphs(a, b []richParagraph) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].heading != b[i].heading || a[i].list != b[i].list || a[i].depth != b[i].depth || a[i].number != b[i].number || len(a[i].glyphs) != len(b[i].glyphs) {
			return false
		}
		for j := range a[i].glyphs {
			if a[i].glyphs[j] != b[i].glyphs[j] {
				return false
			}
		}
	}
	return true
}

func serializeRichGlyphs(glyphs []richGlyph) string {
	codeDelimiters := richCodeDelimiters(glyphs)
	framesAt := func(index int) []richMarkdownFrame {
		frames := richMarkdownFrames(glyphs[index].style, codeDelimiters[index])
		if !unicode.IsSpace(glyphs[index].value) || glyphs[index].value == '\n' {
			return frames
		}
		beforeFrames, afterFrames := []richMarkdownFrame(nil), []richMarkdownFrame(nil)
		for previous := index - 1; previous >= 0; previous-- {
			if !unicode.IsSpace(glyphs[previous].value) {
				beforeFrames = richMarkdownFrames(glyphs[previous].style, codeDelimiters[previous])
				break
			}
		}
		for next := index + 1; next < len(glyphs); next++ {
			if !unicode.IsSpace(glyphs[next].value) {
				afterFrames = richMarkdownFrames(glyphs[next].style, codeDelimiters[next])
				break
			}
		}
		common := 0
		for common < len(beforeFrames) && common < len(afterFrames) && beforeFrames[common].key == afterFrames[common].key {
			common++
		}
		retained := 0
		for retained < common && retained < len(frames) && frames[retained].key == beforeFrames[retained].key {
			retained++
		}
		return frames[:retained]
	}

	var out strings.Builder
	var open []richMarkdownFrame
	for index, glyph := range glyphs {
		want := framesAt(index)
		common := 0
		for common < len(open) && common < len(want) && open[common].key == want[common].key {
			common++
		}
		for close := len(open) - 1; close >= common; close-- {
			out.WriteString(open[close].close)
		}
		for start := common; start < len(want); start++ {
			out.WriteString(want[start].open)
		}
		open = want

		if glyph.value == '\n' {
			out.WriteString("  \n")
			continue
		}
		if richMarkdownHasFrame(open, "code") {
			out.WriteRune(glyph.value)
		} else {
			out.WriteString(escapeRichMarkdown(string(glyph.value)))
		}
	}
	for close := len(open) - 1; close >= 0; close-- {
		out.WriteString(open[close].close)
	}
	// A plain period needs escaping only when it would introduce a list.
	return richOrderedListTextPrefix.ReplaceAllString(out.String(), `${1}\.${2}`)
}

var richOrderedListTextPrefix = regexp.MustCompile(`(?m)^([ ]{0,3}[0-9]{1,9})\.([ \t]|$)`)

type richMarkdownFrame struct {
	key, open, close string
}

type richMarkdownCodeDelimiter struct {
	open, close string
}

func richMarkdownFrames(style RichStyle, codeDelimiter richMarkdownCodeDelimiter) []richMarkdownFrame {
	frames := make([]richMarkdownFrame, 0, 5)
	if style.Link != "" {
		destination := strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)", " ", "%20", "\n", "").Replace(style.Link)
		title := ""
		if style.Title != "" {
			title = " \"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(style.Title) + "\""
		}
		frames = append(frames, richMarkdownFrame{key: "link\x00" + style.Link + "\x00" + style.Title, open: "[", close: "](" + destination + title + ")"})
	}
	if style.Bold {
		frames = append(frames, richMarkdownFrame{key: "bold", open: "**", close: "**"})
	}
	if style.Italic {
		frames = append(frames, richMarkdownFrame{key: "italic", open: "*", close: "*"})
	}
	if style.Strike {
		frames = append(frames, richMarkdownFrame{key: "strike", open: "~~", close: "~~"})
	}
	if style.Code {
		if codeDelimiter.open == "" {
			codeDelimiter = richMarkdownCodeDelimiter{open: "`", close: "`"}
		}
		frames = append(frames, richMarkdownFrame{key: "code", open: codeDelimiter.open, close: codeDelimiter.close})
	}
	return frames
}

func richMarkdownHasFrame(frames []richMarkdownFrame, key string) bool {
	for _, frame := range frames {
		if frame.key == key {
			return true
		}
	}
	return false
}

func richCodeDelimiters(glyphs []richGlyph) []richMarkdownCodeDelimiter {
	delimiters := make([]richMarkdownCodeDelimiter, len(glyphs))
	for start := 0; start < len(glyphs); {
		if !glyphs[start].style.Code {
			start++
			continue
		}
		end := start + 1
		for end < len(glyphs) && glyphs[end].style.Code {
			end++
		}
		text := richGlyphText(glyphs[start:end])
		maxTicks, ticks := 0, 0
		for _, r := range text {
			if r == '`' {
				ticks++
				maxTicks = max(maxTicks, ticks)
			} else {
				ticks = 0
			}
		}
		delimiter := strings.Repeat("`", maxTicks+1)
		open, close := delimiter, delimiter
		if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") ||
			(strings.TrimSpace(text) != "" && (unicode.IsSpace([]rune(text)[0]) || unicode.IsSpace([]rune(text)[len([]rune(text))-1]))) {
			open += " "
			close = " " + close
		}
		for index := start; index < end; index++ {
			delimiters[index] = richMarkdownCodeDelimiter{open: open, close: close}
		}
		start = end
	}
	return delimiters
}
func escapeRichMarkdown(text string) string {
	var out strings.Builder
	for _, r := range text {
		if strings.ContainsRune("\\`*_{}[]<>!#+-|()~", r) {
			out.WriteByte('\\')
		}
		if r == '\n' {
			out.WriteString("  \n")
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (b *RichBlock) locate(position int) (int, int, error) {
	if position < 0 {
		return 0, 0, errors.New("富文本选区不能为负数")
	}
	units := 0
	for i, p := range b.paragraphs {
		for j, g := range p.glyphs {
			if position == units {
				return i, j, nil
			}
			width := 1
			if g.value > 0xffff {
				width = 2
			}
			if position < units+width {
				return 0, 0, errors.New("富文本选区位于字符内部")
			}
			units += width
		}
		if position == units {
			return i, len(p.glyphs), nil
		}
		if i+1 < len(b.paragraphs) {
			units++
		}
	}
	return 0, 0, errors.New("富文本选区超出内容范围")
}
func (b *RichBlock) ReplaceUTF16(start, end int, text string) error {
	return b.transact(func() error {
		return b.replaceUTF16(start, end, text)
	})
}

func (b *RichBlock) replaceUTF16(start, end int, text string) error {
	if end < start {
		start, end = end, start
	}
	start, end = b.protectRichListPrefix(start, end)
	first, from, err := b.locate(start)
	if err != nil {
		return err
	}
	last, to, err := b.locate(end)
	if err != nil {
		return err
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	visible := b.Text()
	startByte, err := richUTF16ByteOffset(visible, start)
	if err != nil {
		return err
	}
	endByte, err := richUTF16ByteOffset(visible, end)
	if err != nil {
		return err
	}
	if visible[startByte:endByte] == text {
		b.replacementCaret = start + uiUTF16Len(text)
		return nil
	}
	if start == end && text == "" {
		b.replacementCaret = start
		return nil
	}
	if start == end && text == "\n" && b.paragraphs[first].list != "" && allRichPrefix(b.paragraphs[first].glyphs) {
		b.paragraphs[first].glyphs = nil
		b.paragraphs[first].list = ""
		b.replacementCaret = richParagraphStart(b.paragraphs, first)
		return nil
	}
	style := RichStyle{}
	p := b.paragraphs[first]
	if from > 0 {
		style = p.glyphs[from-1].style
	} else if len(p.glyphs) > 0 {
		style = p.glyphs[0].style
	}
	if start < end && from < len(p.glyphs) {
		selectedStyle := p.glyphs[from].style
		// Source inherits the selected font/code style. Links reject typing at
		// their edges, so keep the insertion-side link instead of adding one.
		if from == 0 {
			style.Link, style.Title = "", ""
		}
		selectedStyle.Link, selectedStyle.Title = style.Link, style.Title
		style = selectedStyle
	}
	if start == end && b.pending != nil {
		style = *b.pending
	}
	lines := strings.Split(text, "\n")
	replacement := make([]richParagraph, len(lines))
	for i, line := range lines {
		replacement[i] = richParagraph{glyphs: richTextGlyphs(line, style), list: p.list, depth: p.depth, number: p.number + i}
		if i == 0 {
			replacement[i].heading = p.heading
		}
	}
	replacement[0].glyphs = append(append([]richGlyph(nil), p.glyphs[:from]...), replacement[0].glyphs...)
	suffix := append([]richGlyph(nil), b.paragraphs[last].glyphs[to:]...)
	if last > first {
		suffix = withoutLeadingRichPrefix(suffix)
	}
	replacement[len(replacement)-1].glyphs = append(replacement[len(replacement)-1].glyphs, suffix...)
	for i := range replacement {
		if replacement[i].list != "" {
			if i > 0 {
				b.addListPrefix(&replacement[i])
			} else if !hasRichPrefix(replacement[i].glyphs) {
				b.addListPrefix(&replacement[i])
			}
		}
	}
	next := append([]richParagraph(nil), b.paragraphs[:first]...)
	next = append(next, replacement...)
	next = append(next, b.paragraphs[last+1:]...)
	b.paragraphs = next
	b.renumberOrderedLists()
	caretParagraph := first + len(replacement) - 1
	caretLocal := richGlyphUnits(p.glyphs[:from]) + uiUTF16Len(text)
	if len(lines) > 1 {
		caretLocal = leadingRichPrefixUnits(b.paragraphs[caretParagraph].glyphs) + uiUTF16Len(lines[len(lines)-1])
	}
	b.replacementCaret = richParagraphStart(b.paragraphs, caretParagraph) + caretLocal
	return nil
}

func (b *RichBlock) protectRichListPrefix(start, end int) (int, int) {
	if start > end {
		start, end = end, start
	}
	first, from, err := b.locate(start)
	if err != nil {
		return start, end
	}
	last, to, err := b.locate(end)
	if err != nil {
		return start, end
	}
	firstPrefix := leadingRichPrefixCount(b.paragraphs[first].glyphs)
	lastPrefix := leadingRichPrefixCount(b.paragraphs[last].glyphs)
	if start == end {
		if b.paragraphs[first].list != "" && from < firstPrefix {
			start += firstPrefix - from
			end = start
		}
		return start, end
	}
	if b.paragraphs[first].list != "" && from < firstPrefix {
		start += firstPrefix - from
	}
	if b.paragraphs[last].list != "" && to < lastPrefix {
		end -= to
	}
	if start > end {
		end = start
	}
	return start, end
}

func withoutLeadingRichPrefix(glyphs []richGlyph) []richGlyph {
	count := leadingRichPrefixCount(glyphs)
	return append([]richGlyph(nil), glyphs[count:]...)
}

func leadingRichPrefixCount(glyphs []richGlyph) int {
	count := 0
	for count < len(glyphs) && glyphs[count].prefix {
		count++
	}
	return count
}

func leadingRichPrefixUnits(glyphs []richGlyph) int {
	return richGlyphUnits(glyphs[:leadingRichPrefixCount(glyphs)])
}

func richParagraphStart(paragraphs []richParagraph, index int) int {
	position := 0
	for i := 0; i < index && i < len(paragraphs); i++ {
		position += richGlyphUnits(paragraphs[i].glyphs) + 1
	}
	return position
}

func uiUTF16Len(text string) int {
	count := 0
	for _, r := range text {
		count++
		if r > 0xffff {
			count++
		}
	}
	return count
}

func (b *RichBlock) renumberOrderedLists() {
	for i := range b.paragraphs {
		p := &b.paragraphs[i]
		if p.list == "" {
			continue
		}
		if p.list == "ordered" {
			if i > 0 && b.paragraphs[i-1].list == "ordered" && b.paragraphs[i-1].depth == p.depth {
				p.number = b.paragraphs[i-1].number + 1
			} else if p.number < 1 {
				p.number = 1
			}
		}
		p.glyphs = withoutLeadingRichPrefix(p.glyphs)
		b.addListPrefix(p)
	}
}
func allRichPrefix(glyphs []richGlyph) bool {
	for _, g := range glyphs {
		if !g.prefix {
			return false
		}
	}
	return true
}
func hasRichPrefix(glyphs []richGlyph) bool { return len(glyphs) > 0 && glyphs[0].prefix }
func (b *RichBlock) addListPrefix(p *richParagraph) {
	if p.list == "" {
		return
	}
	prefix := strings.Repeat("  ", p.depth) + "• "
	if p.list == "ordered" {
		prefix = strings.Repeat("  ", p.depth) + strconv.Itoa(max(1, p.number)) + ". "
	}
	glyphs := richTextGlyphs(prefix, RichStyle{})
	for i := range glyphs {
		glyphs[i].prefix = true
	}
	p.glyphs = append(glyphs, p.glyphs...)
}

func (b *RichBlock) FormatUTF16(start, end int, command FormatCommand, level int, value string) error {
	return b.transact(func() error {
		return b.formatUTF16(start, end, command, level, value)
	})
}

func (b *RichBlock) formatUTF16(start, end int, command FormatCommand, level int, value string) error {
	if end < start {
		start, end = end, start
	}
	first, from, err := b.locate(start)
	if err != nil {
		return err
	}
	last, to, err := b.locate(end)
	if err != nil {
		return err
	}
	if command == FormatHeading || command == FormatUnorderedList || command == FormatOrderedList {
		if level < 0 || level > 6 {
			return errors.New("标题级别必须在0到6之间")
		}
		list := ""
		if command == FormatUnorderedList {
			list = "unordered"
		}
		if command == FormatOrderedList {
			list = "ordered"
		}
		allTarget := list != ""
		for i := first; i <= last; i++ {
			allTarget = allTarget && b.paragraphs[i].list == list
		}
		for i := first; i <= last; i++ {
			p := &b.paragraphs[i]
			for len(p.glyphs) > 0 && p.glyphs[0].prefix {
				p.glyphs = p.glyphs[1:]
			}
			p.list = ""
			p.depth = 0
			p.heading = 0
			if command == FormatHeading {
				p.heading = level
			} else if !allTarget {
				p.list = list
				p.number = i - first + 1
				b.addListPrefix(p)
			}
		}
		b.renumberOrderedLists()
		return nil
	}
	selected := make([]*richGlyph, 0)
	for i := first; i <= last; i++ {
		lo, hi := 0, len(b.paragraphs[i].glyphs)
		if i == first {
			lo = from
		}
		if i == last {
			hi = to
		}
		for j := lo; j < hi; j++ {
			if !b.paragraphs[i].glyphs[j].prefix {
				selected = append(selected, &b.paragraphs[i].glyphs[j])
			}
		}
	}
	if start == end {
		style := RichStyle{}
		if from > 0 {
			style = b.paragraphs[first].glyphs[from-1].style
		}
		if b.pending != nil {
			style = *b.pending
		}
		if err := toggleRichStyle(&style, command, !richStyleEnabled(style, command), value); err != nil {
			return err
		}
		b.pending = &style
		return nil
	}
	all := len(selected) > 0
	for _, g := range selected {
		all = all && richStyleEnabled(g.style, command)
	}
	for _, g := range selected {
		if err := toggleRichStyle(&g.style, command, !all, value); err != nil {
			return err
		}
	}
	b.pending = nil
	return nil
}
func richStyleEnabled(s RichStyle, c FormatCommand) bool {
	switch c {
	case FormatBold:
		return s.Bold
	case FormatItalic:
		return s.Italic
	case FormatStrike:
		return s.Strike
	case FormatInlineCode:
		return s.Code
	case FormatLink:
		return s.Link != ""
	}
	return false
}
func toggleRichStyle(s *RichStyle, c FormatCommand, on bool, value string) error {
	switch c {
	case FormatBold:
		s.Bold = on
	case FormatItalic:
		s.Italic = on
	case FormatStrike:
		s.Strike = on
	case FormatInlineCode:
		s.Code = on
	case FormatLink:
		s.Link = value
		s.Title = ""
	default:
		return fmt.Errorf("富文本格式不支持 %s", c)
	}
	return nil
}
func (b *RichBlock) SelectedLink(start, end int) string {
	first, from, err := b.locate(min(start, end))
	if err != nil {
		return ""
	}
	p := b.paragraphs[first]
	if from == len(p.glyphs) && from > 0 {
		from--
	}
	if from < len(p.glyphs) {
		return p.glyphs[from].style.Link
	}
	return ""
}
func (b *RichBlock) IndentUTF16(start, end, delta int) error {
	return b.transact(func() error {
		return b.indentUTF16(start, end, delta)
	})
}

func (b *RichBlock) indentUTF16(start, end, delta int) error {
	first, _, err := b.locate(min(start, end))
	if err != nil {
		return err
	}
	last, _, err := b.locate(max(start, end))
	if err != nil {
		return err
	}
	for i := first; i <= last; i++ {
		p := &b.paragraphs[i]
		if p.list == "" {
			continue
		}
		for len(p.glyphs) > 0 && p.glyphs[0].prefix {
			p.glyphs = p.glyphs[1:]
		}
		p.depth = max(0, p.depth+delta)
		b.addListPrefix(p)
	}
	b.renumberOrderedLists()
	return nil
}
