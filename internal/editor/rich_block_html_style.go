package editor

import "strings"

func parseRichHTMLInlineCSS(raw string) map[string]string {
	declarations := make(map[string]string)
	for _, declaration := range strings.Split(raw, ";") {
		parts := strings.Split(declaration, ":")
		if len(parts) != 2 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.ToLower(strings.TrimSpace(parts[1]))
		if name != "" {
			declarations[name] = value
		}
	}
	return declarations
}

// applyRichHTMLStyle mirrors the dependency's CSS-before-tag merge order:
// explicitly styled normal/italic values can reset inherited spans, while a
// semantic <b>/<strong>/<em> tag still supplies its own style afterward.
func applyRichHTMLStyle(current RichStyle, tag string, attrs map[string]string) RichStyle {
	style := current
	css := parseRichHTMLInlineCSS(attrs["style"])
	if value, ok := css["font-weight"]; ok {
		switch value {
		case "normal", "100", "200", "300", "400", "500", "medium", "lighter":
			style.Bold = false
		case "bold", "bolder", "600", "semibold", "700", "800", "extrabold", "900", "black":
			style.Bold = true
		}
	}
	if value, ok := css["font-style"]; ok {
		switch value {
		case "normal":
			style.Italic = false
		case "italic", "oblique":
			style.Italic = true
		}
	}
	if value, ok := css["text-decoration"]; ok {
		for _, decoration := range strings.Fields(value) {
			if decoration == "line-through" {
				style.Strike = true
				break
			}
		}
		// The source CSS decoder maps "none" to an unspecified decoration. Its
		// SpanStyle merge therefore preserves an inherited line-through.
	}

	switch tag {
	case "strong", "b":
		style.Bold = true
	case "em", "i":
		style.Italic = true
	case "del", "s", "strike":
		style.Strike = true
	case "code":
		style.Code = true
	case "a":
		style.Link = attrs["href"]
		style.Title = attrs["title"]
	}
	return style
}

func restoreRichHTMLStyle(tag string, current RichStyle, frames []richHTMLStyleFrame) (RichStyle, []richHTMLStyleFrame) {
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].tag == tag || equivalentRichHTMLTag(frames[i].tag, tag) {
			return frames[i].style, frames[:i]
		}
	}
	return current, frames
}

func richHTMLTagEnd(source string, start int) int {
	var quote byte
	for index := start; index < len(source); index++ {
		value := source[index]
		if quote != 0 {
			if value == quote {
				quote = 0
			}
			continue
		}
		switch value {
		case '\'', '"':
			quote = value
		case '>':
			return index
		}
	}
	return -1
}

func collapseRichHTMLWhitespace(value string, trimStart bool) string {
	var out strings.Builder
	out.Grow(len(value))
	spacePending := false
	for _, r := range value {
		if richHTMLCollapsibleWhitespace(r) {
			spacePending = true
			continue
		}
		if spacePending {
			out.WriteByte(' ')
			spacePending = false
		}
		out.WriteRune(r)
	}
	if spacePending {
		out.WriteByte(' ')
	}
	result := out.String()
	if trimStart {
		result = strings.TrimLeft(result, " \t\r\n\f")
	}
	return result
}

func richHTMLCollapsibleWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\f'
}

func richHTMLVoidElement(tag string) bool {
	switch tag {
	case "area", "base", "basefont", "br", "col", "command", "embed", "frame", "hr", "img", "input", "isindex", "keygen", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}
