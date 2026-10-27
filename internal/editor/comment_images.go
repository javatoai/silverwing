package editor

import (
	"net/url"
	"sort"
	"strings"
)

// CommentImages returns rendered image nodes, never image examples in code or
// opaque HTML. Ranges refer to the unchanged UTF-8 source.
func CommentImages(source string) []Node {
	var images []Node
	var visit func(Node)
	visit = func(n Node) {
		if n.Kind == "image" && n.Range.Start >= 0 && n.Range.End > n.Range.Start && n.Range.End <= len(source) {
			images = append(images, n)
			return
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	for _, part := range Parse(source).Parts {
		for _, n := range part.Nodes {
			visit(n)
		}
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Range.Start < images[j].Range.Start })
	return images
}

// RewriteCommentImages replaces only selected real image nodes. A nil result
// removes that image; an empty replacement map is a byte-for-byte no-op.
func RewriteCommentImages(source string, replacements map[string]*string, annotations ...map[string]string) string {
	var out strings.Builder
	position := 0
	for _, n := range CommentImages(source) {
		destination, ok := replacements[n.Destination]
		if !ok || n.Range.Start < position {
			continue
		}
		out.WriteString(source[position:n.Range.Start])
		end := n.Range.End
		if destination != nil {
			if *destination == n.Destination {
				out.WriteString(source[n.Range.Start:end])
				position = end
				continue
			}
			markup := source[n.Range.Start:end]
			closeLabel := findMatchingBracket([]byte(markup), 1, len(markup), '[', ']')
			at := closeLabel + 2
			if closeLabel > 0 && at < len(markup) && markup[at-1] == '(' {
				for at < len(markup) && strings.ContainsRune(" \t\r\n", rune(markup[at])) {
					at++
				}
				if at < len(markup) && markup[at] == '<' {
					at++
				}
			}
			if n.Attributes["reference"] == "" && closeLabel > 0 && at < len(markup) && strings.HasPrefix(markup[at:], n.Destination) {
				out.WriteString(markup[:at] + *destination + markup[at+len(n.Destination):])
			} else {
				label := "![" + strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ").Replace(n.Text) + "]"
				if closeLabel > 0 {
					label = markup[:closeLabel+1]
				}
				out.WriteString(label + "(" + *destination)
				if n.Title != "" {
					out.WriteString(" \"" + strings.ReplaceAll(n.Title, "\"", "\\\"") + "\"")
				}
				out.WriteString(")")
			}
		}
		// Only remove an annotation belonging to this exact Meegle resource.
		if isFeishuStreamImageURL(n.Destination) {
			u, _ := url.Parse(n.Destination)
			token, _ := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/goapi/v5/platform/file/stream/download/"))
			start, newlines := end, 0
			for start < len(source) && strings.ContainsRune(" \t\r\n", rune(source[start])) {
				if source[start] == '\n' {
					newlines++
				}
				if newlines > 1 {
					break
				}
				start++
			}
			if newlines <= 1 && strings.HasPrefix(source[start:], "<!--") {
				if stop := strings.Index(source[start+4:], "-->"); stop >= 0 && strings.TrimSpace(source[start+4:start+4+stop]) == token {
					end = start + 4 + stop + 3
				}
			}
		}
		if destination != nil && len(annotations) > 0 {
			if token := annotations[0][n.Destination]; token != "" {
				out.WriteString("<!--" + token + "-->")
			}
		}
		position = end
	}
	out.WriteString(source[position:])
	return out.String()
}
