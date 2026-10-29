package markdowntext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type jetbrainsAutoLinkExtension struct{}

func (jetbrainsAutoLinkExtension) Extend(markdown goldmark.Markdown) {
	markdown.Parser().AddOptions(
		parser.WithInlineParsers(util.Prioritized(jetbrainsAutoLinkParser{}, 1000)),
		parser.WithASTTransformers(util.Prioritized(jetbrainsAutoLinkTransformer{}, 1000)),
	)
}

type jetbrainsAutoLinkTransformer struct{}

func (jetbrainsAutoLinkTransformer) Transform(document *gast.Document, reader text.Reader, _ parser.Context) {
	transformAutoLinkText(document, reader.Source(), false)
}

func transformAutoLinkText(parent gast.Node, source []byte, blocked bool) {
	for child := parent.FirstChild(); child != nil; {
		next := child.NextSibling()
		if textNode, ok := child.(*gast.Text); ok && !blocked {
			last, end, mathRanges, after := contiguousTextAndMath(child)
			matches := findJetBrainsAutoLinks(source, textNode.Segment.Start, end)
			if len(matches) > 0 && mathNodesBelongToLinks(mathRanges, matches) {
				replaceTextRunWithAutoLinks(parent, child, last, textNode.Segment.Start, end, matches)
				child = after
				continue
			}

			textLast := child
			textEnd := textNode.Segment.Stop
			for nextText, ok := next.(*gast.Text); ok && nextText.Segment.Start == textEnd; nextText, ok = next.(*gast.Text) {
				textLast = next
				textEnd = nextText.Segment.Stop
				next = next.NextSibling()
			}
			textMatches := findJetBrainsAutoLinks(source, textNode.Segment.Start, textEnd)
			if len(textMatches) > 0 {
				replaceTextRunWithAutoLinks(parent, child, textLast, textNode.Segment.Start, textEnd, textMatches)
				child = next
				continue
			}
			child = next
			continue
		}
		childBlocked := blocked
		switch child.(type) {
		case *gast.CodeSpan, *gast.CodeBlock, *gast.FencedCodeBlock, *gast.Image, *gast.AutoLink, *mathNode:
			childBlocked = true
		}
		transformAutoLinkText(child, source, childBlocked)
		child = next
	}
}

type sourceAutoLink struct {
	start    int
	end      int
	protocol []byte
}

func findJetBrainsAutoLinks(source []byte, start, stop int) []sourceAutoLink {
	if start < 0 || stop > len(source) || start >= stop {
		return nil
	}
	var matches []sourceAutoLink
	for lineStart := start; lineStart < stop; {
		lineEnd := lineStart
		for lineEnd < stop && source[lineEnd] != '\n' && source[lineEnd] != '\r' {
			lineEnd++
		}
		for offset := lineStart; offset < lineEnd; {
			if source[offset] != 'h' && source[offset] != 'f' && source[offset] != 'w' {
				offset++
				continue
			}
			candidate := source[offset:lineEnd]
			match := gfmURLRE.FindIndex(candidate)
			var protocol []byte
			if match == nil {
				match = gfmWWWRE.FindIndex(candidate)
				if match != nil {
					protocol = []byte("http")
				}
			}
			if match == nil || match[0] != 0 || match[1] <= 0 || hasWordAutolinkPrefix(source, offset) {
				offset++
				continue
			}
			end := jetbrainsAutoLinkEnd(source, offset, offset+match[1])
			if end <= offset {
				offset++
				continue
			}
			matches = append(matches, sourceAutoLink{start: offset, end: end, protocol: protocol})
			offset = end
		}
		if lineEnd >= stop {
			break
		}
		lineStart = lineEnd + 1
		if source[lineEnd] == '\r' && lineStart < stop && source[lineStart] == '\n' {
			lineStart++
		}
	}
	return matches
}

func contiguousTextAndMath(first gast.Node) (last gast.Node, end int, mathRanges []SourceRange, after gast.Node) {
	last = first
	end = first.(*gast.Text).Segment.Stop
	for next := first.NextSibling(); next != nil; next = next.NextSibling() {
		switch node := next.(type) {
		case *gast.Text:
			if node.Segment.Start != end {
				return last, end, mathRanges, next
			}
			last = next
			end = node.Segment.Stop
		case *mathNode:
			if node.start != end {
				return last, end, mathRanges, next
			}
			last = next
			end = node.end
			mathRanges = append(mathRanges, SourceRange{Start: node.start, End: node.end})
		default:
			return last, end, mathRanges, next
		}
	}
	return last, end, mathRanges, nil
}

func mathNodesBelongToLinks(mathRanges []SourceRange, matches []sourceAutoLink) bool {
	for _, mathRange := range mathRanges {
		covered := false
		for _, match := range matches {
			if match.start <= mathRange.Start && mathRange.End <= match.end {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func replaceTextRunWithAutoLinks(parent, first, last gast.Node, start, stop int, matches []sourceAutoLink) {
	cursor := start
	var replacements []gast.Node
	appendText := func(textStart, textEnd int, final bool) {
		if textStart > textEnd {
			return
		}
		textNode := gast.NewTextSegment(text.NewSegment(textStart, textEnd))
		textNode.SetPos(textStart)
		if final {
			if original, ok := last.(*gast.Text); ok {
				textNode.SetSoftLineBreak(original.SoftLineBreak())
				textNode.SetHardLineBreak(original.HardLineBreak())
			}
		}
		replacements = append(replacements, textNode)
	}
	for _, match := range matches {
		if match.start < cursor || match.end > stop {
			continue
		}
		if match.start > cursor {
			appendText(cursor, match.start, false)
		}
		link := gast.NewAutoLink(gast.AutoLinkURL, gast.NewTextSegment(text.NewSegment(match.start, match.end)))
		link.SetPos(match.start)
		link.Protocol = match.protocol
		replacements = append(replacements, link)
		cursor = match.end
	}
	if cursor < stop {
		appendText(cursor, stop, true)
	} else if original, ok := last.(*gast.Text); ok && len(replacements) > 0 && (original.SoftLineBreak() || original.HardLineBreak()) {
		appendText(stop, stop, true)
	}
	if len(replacements) == 0 {
		return
	}
	for _, replacement := range replacements {
		parent.InsertBefore(parent, first, replacement)
	}
	for current := first; ; {
		next := current.NextSibling()
		parent.RemoveChild(parent, current)
		if current == last {
			break
		}
		current = next
	}
}

// jetbrainsAutoLinkParser complements Goldmark Linkify's start triggers.
// IntelliJ's GFM lexer recognizes a URL after any non-word token (for example
// after a comma), while Goldmark only invokes Linkify after whitespace and a
// small set of Markdown delimiters.
type jetbrainsAutoLinkParser struct{}

func (jetbrainsAutoLinkParser) Trigger() []byte {
	triggers := []byte{' '}
	for value := byte('!'); value <= '~'; value++ {
		if value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' {
			continue
		}
		triggers = append(triggers, value)
	}
	return triggers
}

func (jetbrainsAutoLinkParser) Parse(parent gast.Node, reader text.Reader, context parser.Context) gast.Node {
	if context.IsInLinkLabel() {
		return nil
	}
	line, segment := reader.PeekLine()
	if len(line) == 0 {
		return nil
	}
	start := segment.Start
	prefixLength := 0
	source := reader.Source()
	lineHead := start == 0 || source[start-1] == '\n' || source[start-1] == '\r'
	if lineHead {
		if line[0] != '<' && !isASCIIWord(line[0]) && line[0] != '_' {
			prefixLength = 1
		}
	} else {
		trigger := line[0]
		if trigger == '<' {
			return nil
		}
		if trigger == ' ' || trigger == '\t' {
			prefixLength = 1
		} else if isASCIIWord(trigger) {
			return nil
		} else {
			prefixLength = 1
			if trigger == '_' && start > 0 {
				previous, _ := utf8.DecodeLastRune(source[:start])
				if unicode.IsLetter(previous) || unicode.IsNumber(previous) || previous == '_' {
					return nil
				}
			}
		}
	}
	if prefixLength >= len(line) {
		return nil
	}
	candidate := line[prefixLength:]
	match := gfmURLRE.FindIndex(candidate)
	if match == nil {
		match = gfmWWWRE.FindIndex(candidate)
	}
	if match == nil || match[0] != 0 || match[1] <= 0 {
		return nil
	}
	linkStart := start + prefixLength
	end := jetbrainsAutoLinkEnd(source, linkStart, linkStart+match[1])
	if end <= linkStart {
		return nil
	}
	if prefixLength > 0 {
		gast.MergeOrAppendTextSegment(parent, segment.WithStop(start+prefixLength))
	}
	reader.Advance(end - start)
	link := gast.NewAutoLink(gast.AutoLinkURL, gast.NewTextSegment(text.NewSegment(linkStart, end)))
	link.SetPos(linkStart)
	if candidate[0] == 'w' {
		link.Protocol = []byte("http")
	}
	return link
}

func isASCIIWord(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func jetbrainsAutoLinkEnd(source []byte, start, end int) int {
	if start < 0 || end > len(source) || start >= end {
		return end
	}
	value := source[start:end]
	length := len(value)
	if value[length-1] == '/' {
		for length > 1 && value[length-2] == '/' {
			length--
		}
		return start + length
	}
	balance := -1
	const badEnding = ".,:;!?\"'*_~]`"
	for i := length - 1; i >= 0; i-- {
		c := value[i]
		if c == ')' {
			if balance == -1 {
				balance = 0
			balanceScan:
				for j := i; j >= 0; j-- {
					switch value[j] {
					case ')':
						balance++
					case '(':
						balance--
						if balance <= 0 {
							break balanceScan
						}
					}
				}
			}
			if balance > 0 {
				balance--
			} else {
				break
			}
		} else if strings.IndexByte(badEnding, c) < 0 {
			break
		}
		length--
	}
	return start + length
}

// gfmAutoLinkRE mirrors the host/path grammar in JetBrains markdown-jvm
// 0.7.9's gfm.flex. The Goldmark linkifier then applies its punctuation and
// parenthesis trimming to the matched source span.
func gfmAutoLinkRE(prefix string) string {
	hostPart := `[\p{L}\p{N}]([a-zA-Z0-9_-]*[\p{L}\p{N}])?`
	host := hostPart + `(\.` + hostPart + `)*`
	userInfo := `([a-zA-Z0-9.!#$%&'*+/=?^_{|}~:\x60-]+@)?`
	pathPart := `([^ \t\f\r\n\[\]()<]|\][^\[(<])`
	pathGroup := `\(` + pathPart + `*\)?` + pathPart + `*`
	path := `(` + pathPart + `+|` + pathGroup + `)` + `(` + pathGroup + `)*`
	return prefix + userInfo + host + `(:[0-9]+)?` + `(/` + path + `)?/?`
}
