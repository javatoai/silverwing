package editor

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// HTMLUTF16 returns the selected visible text and an HTML clipboard fragment.
// Ranges use the same UTF-16 offsets as the native text input API.
func (b *RichBlock) HTMLUTF16(start, end int) (plain, fragment string, err error) {
	if b == nil {
		return "", "", errors.New("富文本块不可用")
	}
	if start > end {
		start, end = end, start
	}
	visible := b.Text()
	startByte, err := richUTF16ByteOffset(visible, start)
	if err != nil {
		return "", "", err
	}
	endByte, err := richUTF16ByteOffset(visible, end)
	if err != nil {
		return "", "", err
	}
	plain = visible[startByte:endByte]
	if start == end {
		return plain, "", nil
	}
	if plain == "\n" {
		return plain, "<br>", nil
	}

	selected := make([]richParagraph, 0, len(b.paragraphs))
	position := 0
	for _, paragraph := range b.paragraphs {
		paragraphUnits := richGlyphUnits(paragraph.glyphs)
		localStart := max(0, start-position)
		localEnd := min(paragraphUnits, end-position)
		if localStart <= localEnd && localEnd >= 0 && localStart <= paragraphUnits {
			glyphs, sliceErr := sliceRichGlyphsUTF16(paragraph.glyphs, localStart, localEnd)
			if sliceErr != nil {
				return "", "", sliceErr
			}
			if len(glyphs) > 0 || (plain == "\n" && len(selected) == 0) {
				copyParagraph := paragraph
				copyParagraph.glyphs = glyphs
				selected = append(selected, copyParagraph)
			}
		}
		position += paragraphUnits + 1
	}
	if len(selected) == 0 && plain == "\n" {
		return plain, "<br>", nil
	}
	return plain, renderRichParagraphsHTML(selected), nil
}

// ReplaceHTMLUTF16 imports common native clipboard HTML into this block. The
// parser is deliberately small and dependency-free; unsupported tags are
// transparent while script/style contents are omitted.
func (b *RichBlock) ReplaceHTMLUTF16(start, end int, fragment string) error {
	return b.transact(func() error {
		return b.replaceHTMLUTF16(start, end, fragment)
	})
}

func (b *RichBlock) replaceHTMLUTF16(start, end int, fragment string) error {
	if start > end {
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
	parsed := parseRichHTMLFragment(fragment)
	if len(parsed.paragraphs) == 0 {
		parsed.paragraphs = []richParagraph{{}}
	}

	base := b.paragraphs[first]
	inlineInsertedUnits := richGlyphUnits(parsed.paragraphs[0].glyphs)
	if !parsed.hasBlock {
		parsed.paragraphs[0].list = base.list
		parsed.paragraphs[0].depth = base.depth
		parsed.paragraphs[0].number = base.number
		parsed.paragraphs[0].heading = base.heading
	}
	prefix := append([]richGlyph(nil), base.glyphs[:from]...)
	if parsed.paragraphs[0].list != base.list {
		prefix = withoutLeadingRichPrefix(prefix)
	}
	prefixBodyUnits := richGlyphUnits(withoutRichPrefixMarkers(prefix))
	parsed.paragraphs[0].glyphs = append(prefix, parsed.paragraphs[0].glyphs...)
	suffix := append([]richGlyph(nil), b.paragraphs[last].glyphs[to:]...)
	if last > first {
		suffix = withoutLeadingRichPrefix(suffix)
	}
	lastInserted := len(parsed.paragraphs) - 1
	lastInsertedUnits := richGlyphUnits(parsed.paragraphs[lastInserted].glyphs)
	parsed.paragraphs[lastInserted].glyphs = append(parsed.paragraphs[lastInserted].glyphs, suffix...)
	for i := range parsed.paragraphs {
		if parsed.paragraphs[i].list != "" && !hasRichPrefix(parsed.paragraphs[i].glyphs) {
			b.addListPrefix(&parsed.paragraphs[i])
		}
	}

	next := append([]richParagraph(nil), b.paragraphs[:first]...)
	next = append(next, parsed.paragraphs...)
	next = append(next, b.paragraphs[last+1:]...)
	b.paragraphs = next
	b.renumberOrderedLists()
	lastIndex := first + lastInserted
	caretLocal := leadingRichPrefixUnits(b.paragraphs[lastIndex].glyphs) + lastInsertedUnits
	if len(parsed.paragraphs) == 1 {
		caretLocal = leadingRichPrefixUnits(b.paragraphs[lastIndex].glyphs) + prefixBodyUnits + inlineInsertedUnits
	}
	b.replacementCaret = richParagraphStart(b.paragraphs, lastIndex) + caretLocal
	return nil
}

func richUTF16ByteOffset(text string, offset int) (int, error) {
	if offset < 0 {
		return 0, errors.New("富文本选区不能为负数")
	}
	units := 0
	for byteOffset, r := range text {
		if units == offset {
			return byteOffset, nil
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		if offset < units+width {
			return 0, errors.New("富文本选区位于字符内部")
		}
		units += width
	}
	if units == offset {
		return len(text), nil
	}
	return 0, errors.New("富文本选区超出内容范围")
}

func sliceRichGlyphsUTF16(glyphs []richGlyph, start, end int) ([]richGlyph, error) {
	if start < 0 || end < start || end > richGlyphUnits(glyphs) {
		return nil, errors.New("富文本选区无效")
	}
	startIndex, endIndex, units := -1, -1, 0
	for i, glyph := range glyphs {
		if units == start {
			startIndex = i
		}
		if units == end {
			endIndex = i
		}
		width := 1
		if glyph.value > 0xffff {
			width = 2
		}
		if (start > units && start < units+width) || (end > units && end < units+width) {
			return nil, errors.New("富文本选区位于字符内部")
		}
		units += width
	}
	if units == start {
		startIndex = len(glyphs)
	}
	if units == end {
		endIndex = len(glyphs)
	}
	if startIndex < 0 || endIndex < 0 {
		return nil, errors.New("富文本选区无效")
	}
	return append([]richGlyph(nil), glyphs[startIndex:endIndex]...), nil
}

func renderRichParagraphsHTML(paragraphs []richParagraph) string {
	var out strings.Builder
	for _, paragraph := range paragraphs {
		inline := renderRichGlyphsHTML(withoutRichPrefixMarkers(paragraph.glyphs))
		switch {
		case paragraph.list == "unordered":
			fmt.Fprintf(&out, "<ul><li>%s</li></ul>", inline)
		case paragraph.list == "ordered":
			if paragraph.number > 1 {
				fmt.Fprintf(&out, "<ol start=\"%d\"><li>%s</li></ol>", paragraph.number, inline)
			} else {
				fmt.Fprintf(&out, "<ol><li>%s</li></ol>", inline)
			}
		case paragraph.heading > 0:
			level := min(6, max(1, paragraph.heading))
			fmt.Fprintf(&out, "<h%d>%s</h%d>", level, inline, level)
		default:
			fmt.Fprintf(&out, "<p>%s</p>", inline)
		}
	}
	return out.String()
}

func renderRichGlyphsHTML(glyphs []richGlyph) string {
	var out strings.Builder
	for i := 0; i < len(glyphs); {
		j := i + 1
		for j < len(glyphs) && glyphs[j].style == glyphs[i].style {
			j++
		}
		text := strings.ReplaceAll(html.EscapeString(richGlyphText(glyphs[i:j])), "\n", "<br>")
		style := glyphs[i].style
		if style.Code {
			text = "<code>" + text + "</code>"
		}
		if style.Link != "" {
			inner := text
			text = "<a href=\"" + html.EscapeString(style.Link) + "\""
			if style.Title != "" {
				text += " title=\"" + html.EscapeString(style.Title) + "\""
			}
			text += ">" + inner + "</a>"
		}
		if style.Strike {
			text = "<del>" + text + "</del>"
		}
		if style.Italic {
			text = "<em>" + text + "</em>"
		}
		if style.Bold {
			text = "<strong>" + text + "</strong>"
		}
		out.WriteString(text)
		i = j
	}
	return out.String()
}

func withoutRichPrefixMarkers(glyphs []richGlyph) []richGlyph {
	result := make([]richGlyph, 0, len(glyphs))
	for _, glyph := range glyphs {
		if !glyph.prefix {
			result = append(result, glyph)
		}
	}
	return result
}

type richHTMLList struct {
	kind string
	next int
}

type richHTMLStyleFrame struct {
	tag   string
	style RichStyle
}

type richHTMLFragment struct {
	paragraphs []richParagraph
	hasBlock   bool
}

func parseRichHTMLFragment(source string) richHTMLFragment {
	result := richHTMLFragment{}
	var current richParagraph
	currentActive := false
	heading := 0
	style := RichStyle{}
	var styles []richHTMLStyleFrame
	var lists []richHTMLList
	skipTag := ""
	skipDepth := 0
	hasHTMLText := false
	var lastHTMLText rune
	recordHTMLSpace := func() {
		hasHTMLText = true
		lastHTMLText = ' '
	}
	flush := func(force bool) {
		if !currentActive && !force {
			return
		}
		if len(lists) > 0 {
			current.list = lists[len(lists)-1].kind
			current.depth = len(lists) - 1
			current.number = lists[len(lists)-1].next
		} else {
			current.list = ""
			current.depth = 0
		}
		current.heading = heading
		result.paragraphs = append(result.paragraphs, current)
		current = richParagraph{}
		currentActive = false
	}
	ensureCurrent := func() {
		if currentActive {
			return
		}
		currentActive = true
		if len(lists) > 0 {
			current.list = lists[len(lists)-1].kind
			current.depth = len(lists) - 1
			current.number = lists[len(lists)-1].next
		} else {
			current.heading = heading
		}
	}
	appendText := func(text string) {
		trimStart := !hasHTMLText || richHTMLCollapsibleWhitespace(lastHTMLText)
		normalized := collapseRichHTMLWhitespace(text, trimStart)
		if normalized == "" {
			return
		}
		decoded := html.UnescapeString(normalized)
		if decoded == "" {
			return
		}
		ensureCurrent()
		current.glyphs = append(current.glyphs, richTextGlyphs(decoded, style)...)
		for _, r := range decoded {
			hasHTMLText = true
			lastHTMLText = r
		}
	}
	for offset := 0; offset < len(source); {
		if source[offset] != '<' {
			next := strings.IndexByte(source[offset:], '<')
			if next < 0 {
				next = len(source) - offset
			}
			if skipTag == "" {
				appendText(source[offset : offset+next])
			}
			offset += next
			continue
		}
		if strings.HasPrefix(source[offset:], "<!--") {
			if end := strings.Index(source[offset+4:], "-->"); end >= 0 {
				offset += 4 + end + 3
				continue
			}
			break
		}
		closeAt := richHTMLTagEnd(source, offset+1)
		if closeAt < 0 {
			if skipTag == "" {
				appendText("<")
			}
			offset++
			continue
		}
		token := strings.TrimSpace(source[offset+1 : closeAt])
		offset = closeAt + 1
		closing, selfClosing, tag, attrs := parseRichHTMLTag(token)
		if tag == "" {
			continue
		}
		if skipTag != "" {
			if tag == skipTag {
				if closing {
					skipDepth--
					if skipDepth <= 0 {
						skipTag, skipDepth = "", 0
					}
				} else if !selfClosing {
					skipDepth++
				}
			}
			continue
		}
		if tag == "script" || tag == "style" {
			if !closing && !selfClosing {
				skipTag, skipDepth = tag, 1
			}
			continue
		}
		if !closing {
			before := style
			if tag != "ul" && tag != "ol" && tag != "br" {
				style = applyRichHTMLStyle(style, tag, attrs)
			}
			if richHTMLVoidElement(tag) {
				// HTML void elements close immediately in the source parser. Their
				// CSS can style the element itself but cannot affect following text.
				style = before
			} else {
				// Keep one frame for every non-void open tag, even when it does not
				// change style. The source HTML parser maintains the same tag stack;
				// omitting unchanged frames lets an inner </span> accidentally pop
				// an outer styled <span>.
				styles = append(styles, richHTMLStyleFrame{tag: tag, style: before})
			}
		}
		if closing {
			style, styles = restoreRichHTMLStyle(tag, style, styles)
			switch tag {
			case "p", "div":
				if len(current.glyphs) > 0 {
					recordHTMLSpace()
				}
				flush(currentActive)
			case "li":
				flush(currentActive)
				if len(lists) > 0 {
					lists[len(lists)-1].next++
				}
			case "ul", "ol":
				flush(currentActive)
				if len(lists) > 0 {
					lists = lists[:len(lists)-1]
				}
			case "h1", "h2", "h3", "h4", "h5", "h6":
				if len(current.glyphs) > 0 {
					recordHTMLSpace()
				}
				flush(currentActive)
				heading = 0
			}
			continue
		}
		switch tag {
		case "strong", "b", "em", "i", "del", "s", "strike", "code", "a":
		case "br":
			ensureCurrent()
			current.glyphs = append(current.glyphs, richGlyph{value: '\n', style: style})
			recordHTMLSpace()
		case "p", "div":
			if currentActive {
				if len(current.glyphs) > 0 {
					recordHTMLSpace()
				}
				flush(true)
			}
			result.hasBlock = true
			currentActive = true
		case "h1", "h2", "h3", "h4", "h5", "h6":
			if currentActive {
				if len(current.glyphs) > 0 {
					recordHTMLSpace()
				}
				flush(true)
			}
			heading, _ = strconv.Atoi(tag[1:])
			result.hasBlock = true
			currentActive = true
		case "ul", "ol":
			if currentActive {
				flush(true)
			}
			kind, next := "unordered", 1
			if tag == "ol" {
				kind = "ordered"
				if value, err := strconv.Atoi(attrs["start"]); err == nil && value > 0 {
					next = value
				}
			}
			lists = append(lists, richHTMLList{kind: kind, next: next})
			result.hasBlock = true
		case "li":
			if currentActive {
				if len(current.glyphs) > 0 {
					recordHTMLSpace()
				}
				flush(true)
				if len(lists) > 0 {
					lists[len(lists)-1].next++
				}
			}
			if len(lists) == 0 {
				lists = append(lists, richHTMLList{kind: "unordered", next: 1})
			}
			currentActive = true
			result.hasBlock = true
		}
		if selfClosing {
			if tag == "br" {
				continue
			}
			if tag == "p" || strings.HasPrefix(tag, "h") {
				flush(currentActive)
				heading = 0
			}
		}
	}
	flush(currentActive)
	return result
}

func parseRichHTMLTag(token string) (closing, selfClosing bool, tag string, attrs map[string]string) {
	attrs = make(map[string]string)
	token = strings.TrimSpace(token)
	if token == "" || token[0] == '!' || token[0] == '?' {
		return false, false, "", attrs
	}
	if token[0] == '/' {
		closing = true
		token = strings.TrimSpace(token[1:])
	}
	if strings.HasSuffix(token, "/") {
		selfClosing = true
		token = strings.TrimSpace(strings.TrimSuffix(token, "/"))
	}
	nameEnd := strings.IndexFunc(token, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' })
	if nameEnd < 0 {
		nameEnd = len(token)
	}
	tag = strings.ToLower(token[:nameEnd])
	if closing {
		return closing, selfClosing, tag, attrs
	}
	for index := nameEnd; index < len(token); {
		for index < len(token) && (token[index] == ' ' || token[index] == '\t' || token[index] == '\r' || token[index] == '\n') {
			index++
		}
		if index >= len(token) {
			break
		}
		start := index
		for index < len(token) && token[index] != '=' && token[index] != ' ' && token[index] != '\t' && token[index] != '\r' && token[index] != '\n' {
			index++
		}
		key := strings.ToLower(token[start:index])
		for index < len(token) && (token[index] == ' ' || token[index] == '\t' || token[index] == '\r' || token[index] == '\n') {
			index++
		}
		value := ""
		if index < len(token) && token[index] == '=' {
			index++
			for index < len(token) && (token[index] == ' ' || token[index] == '\t' || token[index] == '\r' || token[index] == '\n') {
				index++
			}
			if index < len(token) && (token[index] == '\'' || token[index] == '"') {
				quote := token[index]
				index++
				valueStart := index
				for index < len(token) && token[index] != quote {
					index++
				}
				value = token[valueStart:index]
				if index < len(token) {
					index++
				}
			} else {
				valueStart := index
				for index < len(token) && token[index] != ' ' && token[index] != '\t' && token[index] != '\r' && token[index] != '\n' {
					index++
				}
				value = token[valueStart:index]
			}
		}
		if key != "" {
			attrs[key] = html.UnescapeString(value)
		}
	}
	return closing, selfClosing, tag, attrs
}

func equivalentRichHTMLTag(a, b string) bool {
	if a == b {
		return true
	}
	return (a == "strong" || a == "b") && (b == "strong" || b == "b") ||
		(a == "em" || a == "i") && (b == "em" || b == "i") ||
		(a == "del" || a == "s" || a == "strike") && (b == "del" || b == "s" || b == "strike")
}
