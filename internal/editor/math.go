package editor

import (
	"strconv"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type mathNode struct {
	gast.BaseInline
	start, end int
	width      int
}

var kindMath = gast.NewNodeKind("Math")

func (*mathNode) Inline() {}

func (*mathNode) Kind() gast.NodeKind { return kindMath }

func (n *mathNode) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, map[string]string{
		"Start": strconv.Itoa(n.start),
		"End":   strconv.Itoa(n.end),
		"Width": strconv.Itoa(n.width),
	}, nil)
}

func (n *mathNode) Text(source []byte) []byte {
	if n.start < 0 || n.end < n.start || n.end > len(source) {
		return nil
	}
	return source[n.start:n.end]
}

type mathExtension struct{}

func (mathExtension) Extend(markdown goldmark.Markdown) {
	markdown.Parser().AddOptions(parser.WithInlineParsers(
		// The Kotlin GFM lexer emits an entire GFM_AUTOLINK token before
		// MathParser pairs dollar tokens. Run after Goldmark Linkify so dollar
		// signs inside a URL are consumed as URL text first.
		util.Prioritized(mathInlineParser{}, 1001),
	))
}

type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$'} }

func (mathInlineParser) Parse(parent gast.Node, reader text.Reader, _ parser.Context) gast.Node {
	line, position := reader.PeekLine()
	if len(line) == 0 || line[0] != '$' {
		return nil
	}
	width := 1
	for width < len(line) && line[width] == '$' {
		width++
	}
	source := reader.Source()
	start := position.Start
	if start > 0 && source[start-1] == '$' {
		// JetBrains groups a maximal run of dollars into one DOLLAR token;
		// do not reinterpret its second or later byte as a narrower token.
		return nil
	}
	end := len(source)
	if parent.Type() == gast.TypeBlock {
		if block, ok := parent.(interface{ Lines() *text.Segments }); ok {
			lines := block.Lines()
			if lines.Len() > 0 {
				end = lines.At(lines.Len() - 1).Stop
			}
		}
	}
	closeEnd := matchingMathDelimiter(source, start, end, width)
	if closeEnd <= start {
		return nil
	}
	reader.Advance(closeEnd - start)
	node := &mathNode{start: start, end: closeEnd, width: width}
	node.SetPos(start)
	return node
}

func matchingMathDelimiter(source []byte, start, end, width int) int {
	if start < 0 || start+width > len(source) {
		return -1
	}
	if end > len(source) {
		end = len(source)
	}
	for at := start + width; at < end; {
		switch source[at] {
		case '`':
			count := mathRunLength(source, at, end, '`')
			if closeEnd, ok := matchingBacktickRun(source, at+count, end, count); ok {
				at = closeEnd
				continue
			}
			at += count
		case '$':
			count := mathRunLength(source, at, end, '$')
			if isEscaped(source, at) {
				at += count
				continue
			}
			if count == width {
				return at + count
			}
			at += count
		default:
			at++
		}
	}
	return -1
}

func mathRunLength(source []byte, start, end int, token byte) int {
	stop := start
	for stop < end && source[stop] == token {
		stop++
	}
	return stop - start
}

func matchingBacktickRun(source []byte, start, end, width int) (int, bool) {
	for at := start; at < end; {
		if source[at] != '`' || isEscaped(source, at) {
			at++
			continue
		}
		count := mathRunLength(source, at, end, '`')
		if count == width {
			return at + count, true
		}
		at += count
	}
	return 0, false
}

func mathNodeKindName(width int) string {
	if width == 1 {
		return "math-inline"
	}
	return "math-block"
}
