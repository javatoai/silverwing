package editor

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui/markdown/parser"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// ClipboardHTMLToMarkdown converts only incoming HTML, never an existing
// document. Unsupported presentation is transparent; scripts/styles are omitted.
func ClipboardHTMLToMarkdown(fragment string) (string, error) {
	if len(fragment) > 4<<20 {
		return "", errors.New("剪贴板HTML过大")
	}
	root, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return "", err
	}
	var render func(*html.Node, bool) string
	tableDepth := 0
	children := func(node *html.Node, code bool) string {
		var b strings.Builder
		tailNewlines := 0
		for n := node.FirstChild; n != nil; n = n.NextSibling {
			piece := render(n, code)
			if tailNewlines >= 2 {
				piece = strings.TrimLeft(piece, "\n")
			} else if tailNewlines == 1 && strings.HasPrefix(piece, "\n\n") {
				piece = piece[1:]
			}
			b.WriteString(piece)
			if piece != "" {
				tailNewlines = 0
				for i := len(piece) - 1; i >= 0 && piece[i] == '\n'; i-- {
					tailNewlines++
				}
			}
		}
		return b.String()
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	target := func(raw string) string {
		u, e := url.Parse(strings.TrimSpace(raw))
		if e != nil {
			return ""
		}
		switch strings.ToLower(u.Scheme) {
		case "", "http", "https", "mailto", "file":
		default:
			return ""
		}
		return parser.EscapeLinkDestination(strings.ReplaceAll(strings.TrimSpace(raw), "|", "%7C"))
	}
	var rawText func(*html.Node) string
	rawText = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(rawText(c))
		}
		return b.String()
	}
	render = func(n *html.Node, code bool) string {
		if n.Type == html.TextNode {
			if code {
				return n.Data
			}
			fields := strings.Fields(n.Data)
			if len(fields) == 0 {
				if n.Data != "" {
					return " "
				}
				return ""
			}
			text := strings.Join(fields, " ")
			first, _ := utf8.DecodeRuneInString(n.Data)
			last, _ := utf8.DecodeLastRuneInString(n.Data)
			if unicode.IsSpace(first) {
				text = " " + text
			}
			if unicode.IsSpace(last) {
				text += " "
			}
			return escapeIncomingText(text)
		}
		if n.Type != html.ElementNode && n.Type != html.DocumentNode {
			return ""
		}
		tag := strings.ToLower(n.Data)
		if tag == "script" || tag == "style" || tag == "noscript" {
			return ""
		}
		if tag == "pre" {
			body := rawText(n)
			fence := parser.BacktickFence(body, 3)
			language := ""
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				if child.Data == "code" {
					for _, class := range strings.Fields(attr(child, "class")) {
						if strings.HasPrefix(class, "language-") {
							language = strings.TrimPrefix(class, "language-")
						}
					}
				}
			}
			return "\n\n" + fence + language + "\n" + strings.TrimSuffix(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(body), "\n") + "\n" + fence + "\n\n"
		}
		if tag == "table" {
			var rows [][]string
			var walk func(*html.Node)
			walk = func(node *html.Node) {
				if node.Type == html.ElementNode && node.Data == "tr" {
					var cells []string
					for c := node.FirstChild; c != nil; c = c.NextSibling {
						if c.Data == "td" || c.Data == "th" {
							tableDepth++
							text := strings.TrimSpace(children(c, false))
							tableDepth--
							text = escapeRichTableCell(text)
							cells = append(cells, text)
						}
					}
					if len(cells) > 0 {
						rows = append(rows, cells)
					}
					return
				}
				for c := node.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(n)
			if len(rows) == 0 {
				return ""
			}
			columns := 0
			for _, r := range rows {
				columns = max(columns, len(r))
			}
			var lines []string
			for i, row := range rows {
				for len(row) < columns {
					row = append(row, "")
				}
				lines = append(lines, "| "+strings.Join(row, " | ")+" |")
				if i == 0 {
					sep := make([]string, columns)
					for k := range sep {
						sep[k] = "---"
					}
					lines = append(lines, "| "+strings.Join(sep, " | ")+" |")
				}
			}
			return "\n\n" + strings.Join(lines, "\n") + "\n\n"
		}
		if tag == "ul" || tag == "ol" {
			number := 1
			if raw := attr(n, "start"); raw != "" {
				if x, e := strconv.Atoi(raw); e == nil && x >= 0 {
					number = x
				}
			}
			var lines []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Data != "li" {
					continue
				}
				prefix := "- "
				if tag == "ol" {
					prefix = fmt.Sprintf("%d. ", number)
					number++
				}
				body := strings.TrimSpace(children(c, false))
				body = strings.ReplaceAll(body, "\n", "\n"+strings.Repeat(" ", len(prefix)))
				lines = append(lines, prefix+body)
			}
			return "\n\n" + strings.Join(lines, "\n") + "\n\n"
		}
		content := children(n, code || tag == "code")
		switch tag {
		case "br":
			return "  \n"
		case "b", "strong":
			return wrapIncomingFormat(content, "**")
		case "i", "em":
			return wrapIncomingFormat(content, "*")
		case "s", "del", "strike":
			return wrapIncomingFormat(content, "~~")
		case "code":
			// GFM unescapes \| before inline code parsing. Odd literal slash
			// runs before a pipe have no lossless single-code-span encoding in
			// that cell grammar; preserve their text rather than drop a slash.
			if tableDepth > 0 && oddSlashPipe(content) {
				return escapedCodeCellText(content)
			}
			return parser.EncodeCodeSpan(content)
		case "a":
			if destination := target(attr(n, "href")); destination != "" {
				return "[" + content + "](<" + destination + ">)"
			}
			return content
		case "img":
			if destination := target(attr(n, "src")); destination != "" {
				return "![" + escapeIncomingText(attr(n, "alt")) + "](<" + destination + ">)"
			}
			return escapeIncomingText(attr(n, "alt"))
		case "h1", "h2", "h3", "h4", "h5", "h6":
			return "\n\n" + strings.Repeat("#", int(tag[1]-'0')) + " " + strings.TrimSpace(content) + "\n\n"
		case "p", "div", "section", "article":
			return "\n\n" + strings.TrimSpace(content) + "\n\n"
		case "blockquote":
			return "\n\n> " + strings.ReplaceAll(strings.TrimSpace(content), "\n", "\n> ") + "\n\n"
		case "hr":
			return "\n\n---\n\n"
		}
		return content
	}
	result := strings.TrimSpace(render(root, false))
	return result, nil
}

func oddSlashPipe(text string) bool {
	slashes := 0
	for _, r := range text {
		if r == '|' && slashes%2 != 0 {
			return true
		}
		if r == '\\' {
			slashes++
		} else {
			slashes = 0
		}
	}
	return false
}

func escapedCodeCellText(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) {
			fmt.Fprintf(&out, "&#%d;", r)
		} else if r == '&' {
			out.WriteString("&amp;")
		} else {
			if strings.ContainsRune("\\`*_{}[]<>!#+-|()~", r) {
				out.WriteByte('\\')
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}

func escapeIncomingText(text string) string {
	return escapeRichMarkdown(strings.ReplaceAll(text, "&", "&amp;"))
}

func wrapIncomingFormat(text, marker string) string {
	first := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	last := len(strings.TrimRightFunc(text, unicode.IsSpace))
	if first >= last {
		return text
	}
	return text[:first] + marker + text[first:last] + marker + text[last:]
}

func escapeRichTableCell(text string) string {
	var out strings.Builder
	slashes := 0
	for _, r := range text {
		if r == '|' && slashes%2 == 0 {
			out.WriteByte('\\')
		}
		if r == '\n' {
			out.WriteString("<br>")
		} else {
			out.WriteRune(r)
		}
		if r == '\\' {
			slashes++
		} else {
			slashes = 0
		}
	}
	return out.String()
}
