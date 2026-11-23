package editor

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/markdown/parser"
	"golang.org/x/net/html"
)

// ProjectHTMLTables adapts standalone, regular HTML tables for the native
// Markdown reader. The mapper translates projected byte offsets back to source.
func ProjectHTMLTables(source string) (string, func(int) int) {
	if !strings.Contains(strings.ToLower(source), "<table") {
		return source, nil
	}
	doc := parser.Parse(source)
	var output strings.Builder
	var tables []readingTableOffsets
	cursor, occupiedUntil := 0, 0
	for i := 1; i < doc.Len(); i++ {
		block := doc.Block(i)
		if block.Kind != parser.HTMLBlock || block.Parent != 0 || block.Start < occupiedUntil {
			continue
		}
		start := block.Start
		end := completeHTMLTableEnd(source[start:])
		if end < 0 {
			break // The unclosed table owns the rest of the source.
		}
		if end == 0 {
			continue
		}
		end += start
		occupiedUntil = end
		restOfLine, _, _ := strings.Cut(source[end:], "\n")
		if strings.TrimSpace(restOfLine) != "" {
			continue
		}
		rows := readingTableCells(source[start:end])
		if len(rows) == 0 {
			continue
		}
		body, points := projectReadingTableRows(source, start, end, rows)
		if body == "" {
			continue
		}
		output.WriteString(source[cursor:start])
		projectedStart := output.Len()
		if start > 0 {
			output.WriteString("\n\n")
		}
		bodyStart := output.Len()
		output.WriteString(body)
		if end < len(source) {
			output.WriteString("\n\n")
		}
		tables = append(tables, readingTableOffsets{projectedStart, bodyStart, bodyStart + len(body), output.Len(), start, end, points})
		cursor = end
	}
	if len(tables) == 0 {
		return source, nil
	}
	output.WriteString(source[cursor:])
	projected := output.String()
	return projected, func(offset int) int {
		offset = min(max(offset, 0), len(projected))
		shift := 0
		for _, table := range tables {
			if offset < table.start {
				break
			}
			if offset < table.end {
				return table.originalOffset(source, offset)
			}
			shift = table.sourceEnd - table.end
		}
		return offset + shift
	}
}

// A blank line can end Markdown's HTML block before the table closes. The
// tokenizer follows the whole table including unsupported or nested markup.
// A negative result is a table opener without a matching close.
func completeHTMLTableEnd(source string) int {
	z := html.NewTokenizer(strings.NewReader(source))
	if z.Next() != html.StartTagToken {
		return 0
	}
	name, _ := z.TagName()
	if string(name) != "table" {
		return 0
	}
	consumed, depth := len(z.Raw()), 1
	for kind := z.Next(); kind != html.ErrorToken; kind = z.Next() {
		consumed += len(z.Raw())
		switch kind {
		case html.StartTagToken, html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "table" {
				if kind == html.StartTagToken {
					depth++
				} else if depth--; depth == 0 {
					return consumed
				}
			}
		}
	}
	return -1
}

func readingTableCells(table string) [][]SourceRange {
	z := html.NewTokenizer(strings.NewReader(table))
	var stack []string
	var rows [][]SourceRange
	consumed, cellStart := 0, 0
	for kind := z.Next(); kind != html.ErrorToken; kind = z.Next() {
		start := consumed
		consumed += len(z.Raw())
		var parent string
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		cell := parent == "td" || parent == "th"
		switch kind {
		case html.CommentToken:
			continue
		case html.TextToken:
			if cell || strings.TrimSpace(string(z.Raw())) == "" {
				continue
			}
			return nil
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			token := z.Token()
			tag := token.Data
			if cell && !isReadingTableTag(tag) {
				continue
			}
			if kind == html.EndTagToken && tag == parent {
				stack = stack[:len(stack)-1]
				if cell {
					index := len(rows) - 1
					rows[index] = append(rows[index], SourceRange{Start: cellStart, End: start})
				}
				continue
			}
			if kind != html.StartTagToken || !readingTableChild(parent, token) {
				return nil
			}
			stack = append(stack, tag)
			if tag == "tr" {
				rows = append(rows, nil)
			} else if tag == "td" || tag == "th" {
				cellStart = consumed
			}
		default:
			return nil
		}
	}
	for _, row := range rows {
		if len(row) == 0 {
			return nil
		}
	}
	if len(stack) != 0 || consumed != len(table) {
		return nil
	}
	return rows
}

func isReadingTableTag(tag string) bool {
	switch tag {
	case "table", "thead", "tbody", "tfoot", "tr", "td", "th":
		return true
	}
	return false
}

func readingTableChild(parent string, token html.Token) bool {
	switch token.Data {
	case "table":
		return parent == ""
	case "thead", "tbody", "tfoot":
		return parent == "table"
	case "tr":
		return parent == "table" || parent == "thead" || parent == "tbody" || parent == "tfoot"
	case "td", "th":
		for _, attr := range token.Attr {
			if (attr.Key == "rowspan" || attr.Key == "colspan") && strings.TrimSpace(attr.Val) != "1" {
				return false
			}
		}
		return parent == "tr"
	}
	return false
}

type readingTablePoint struct{ rendered, raw int }

type readingTableOffsets struct {
	start, bodyStart, bodyEnd, end int
	sourceStart, sourceEnd         int
	points                         []readingTablePoint
}

func (table readingTableOffsets) originalOffset(source string, offset int) int {
	if offset <= table.bodyStart {
		return table.sourceStart
	}
	if offset >= table.bodyEnd {
		return table.sourceEnd
	}
	at := offset - table.bodyStart
	index := sort.Search(len(table.points), func(i int) bool { return table.points[i].rendered > at })
	left, right := table.points[index-1], table.points[index]
	raw := left.raw + (at-left.rendered)*(right.raw-left.raw)/(right.rendered-left.rendered)
	for raw > left.raw && !utf8.RuneStart(source[raw]) {
		raw--
	}
	return raw
}

func projectReadingTableRows(source string, start, end int, rows [][]SourceRange) (string, []readingTablePoint) {
	values := make([][]string, len(rows))
	maps := make([][][]readingTablePoint, len(rows))
	for rowIndex, row := range rows {
		for _, cell := range row {
			from, to := start+cell.Start, start+cell.End
			cellText := source[from:to]
			trimmedLeft := strings.TrimLeftFunc(cellText, unicode.IsSpace)
			value, points, ok := projectReadingTableCell(strings.TrimRightFunc(trimmedLeft, unicode.IsSpace), to-len(trimmedLeft))
			if !ok {
				return "", nil
			}
			values[rowIndex] = append(values[rowIndex], value)
			maps[rowIndex] = append(maps[rowIndex], points)
		}
	}
	points := []readingTablePoint{{0, start}}
	result := formatMarkdownTable(values, func(row, column, offset int) {
		for _, point := range maps[row][column] {
			points = append(points, readingTablePoint{point.rendered + offset, point.raw})
		}
	})
	return result, append(points, readingTablePoint{len(result), end})
}

func projectReadingTableCell(source string, base int) (string, []readingTablePoint, bool) {
	z := html.NewTokenizer(strings.NewReader(source))
	var output strings.Builder
	var points []readingTablePoint
	write := func(value string, from, to int) {
		points = append(points, readingTablePoint{output.Len(), base + from})
		output.WriteString(value)
		points = append(points, readingTablePoint{output.Len(), base + to})
	}
	var anchor string
	inAnchor := false
	consumed := 0
	for kind := z.Next(); kind != html.ErrorToken; kind = z.Next() {
		start := consumed
		consumed += len(z.Raw())
		if kind == html.TextToken {
			writeReadingTableText(source[start:consumed], start, write)
			continue
		}
		if kind == html.CommentToken {
			continue
		}
		if kind != html.StartTagToken && kind != html.EndTagToken && kind != html.SelfClosingTagToken {
			return "", nil, false
		}
		token := z.Token()
		raw := source[start:consumed]
		if (token.Data == "https:" || token.Data == "http:") && !strings.ContainsAny(raw, " \t\r\n") {
			writeReadingTableText(raw, start, write)
			continue
		}
		switch token.Data {
		case "a":
			switch {
			case kind == html.EndTagToken && inAnchor:
				if anchor != "" {
					write("](<"+anchor+">)", start, consumed)
				}
				anchor, inAnchor = "", false
			case kind == html.StartTagToken && !inAnchor:
				inAnchor = true
				for _, attr := range token.Attr {
					if attr.Key == "href" {
						anchor = markdownHTMLLinkTarget(attr.Val)
						break
					}
				}
				if anchor != "" {
					write("[", start, consumed)
				}
			default:
				return "", nil, false
			}
		case "br":
			if kind != html.EndTagToken {
				write("<br>", start, consumed)
			}
		case "span":
		case "p", "div":
			if kind == html.EndTagToken && consumed < len(source) {
				write("<br>", start, consumed)
			}
		case "b", "strong", "i", "em", "u", "ins", "s", "del", "strike", "code", "kbd", "samp", "mark", "sub", "sup", "small":
			if kind == html.EndTagToken {
				write("</"+token.Data+">", start, consumed)
			} else if kind == html.StartTagToken {
				write("<"+token.Data+">", start, consumed)
			}
		default:
			return "", nil, false
		}
	}
	return output.String(), points, consumed == len(source) && !inAnchor
}

func writeReadingTableText(source string, start int, write func(string, int, int)) {
	last, slashCount := 0, 0
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\r', '\n':
			write(source[last:i], start+last, start+i)
			end := i + 1
			for end < len(source) && strings.ContainsRune(" \t\r\n", rune(source[end])) {
				end++
			}
			write(" ", start+i, start+end)
			last, i = end, end-1
		case '|':
			if slashCount%2 == 0 {
				write(source[last:i], start+last, start+i)
				write(`\`, start+i, start+i)
				last = i
			}
		}
		if source[i] == '\\' {
			slashCount++
		} else {
			slashCount = 0
		}
	}
	write(source[last:], start+last, start+len(source))
}
