package markdown

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// TextBlock is the exact displayed text used by application-owned find.
type TextBlock struct {
	Index int
	Text  string
}

func TextBlocks(doc *parser.Document) []TextBlock {
	st := newState()
	st.doc = doc
	st.th = *DefaultTheme(&ui.Theme{FontSize: 15})
	st.sb.d, st.sb.th = doc, &st.th
	st.numberNotes()
	var result []TextBlock
	for i := 1; i < doc.Len(); i++ {
		var text strings.Builder
		switch doc.Block(i).Kind {
		case parser.Paragraph, parser.Heading, parser.TableCell:
			for _, pc := range st.para(i, spanStyle{}).pieces {
				if pc.image {
					continue
				}
				for _, sp := range pc.spans {
					text.WriteString(sp.Text)
				}
			}
		case parser.CodeBlock:
			text.WriteString(doc.Lines(i))
		case parser.HTMLBlock, parser.MathBlock:
			text.WriteString(strings.TrimRight(doc.Lines(i), "\n"))
		default:
			continue
		}
		result = append(result, TextBlock{Index: i, Text: text.String()})
	}
	return result
}
