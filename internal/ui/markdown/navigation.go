package markdownui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"silverwing-go/internal/markdowntext"
)

// RevealLocation moves within the current Buffer without replacing its source,
// mode or undo history. Line and column are one-based; a zero line resolves a
// heading fragment using the native renderer's slug and first-match rules.
func (s *EditorState) RevealLocation(line, column int, fragment string) error {
	if s == nil || s.closed {
		return ErrClosed
	}
	if s.native.Composing() {
		return ErrComposing
	}
	source := s.native.Buffer().Snapshot().String()
	at, ok := locationOffset(source, line, column, fragment)
	if !ok {
		return fmt.Errorf("找不到文档位置：%s", fragment)
	}
	s.native.SetSelection(at, at)
	s.reader.properties.setDocument(s.document.ID, source)
	if s.Mode() != Source {
		if model := s.reader.properties.model; model != nil && at < model.End {
			if err := s.SetMode(Source); err != nil {
				return err
			}
		}
	}
	if s.Mode() == Preview {
		s.reader.RevealLocation(line, fragment)
	} else {
		s.native.Focus()
	}
	return nil
}

func headingNamed(doc *parser.Document, fragment string) int {
	if fragment == "" {
		return -1
	}
	for i := 1; i < doc.Len(); i++ {
		if doc.Block(i).Kind == parser.Heading && md.Slug(parser.PlainText(doc.Inlines(i))) == strings.ToLower(fragment) {
			return i
		}
	}
	return -1
}

func locationOffset(source string, line, column int, fragment string) (int, bool) {
	if line <= 0 {
		if fragment == "" {
			return 0, false
		}
		doc := parser.New(parser.Default)
		prefix := 0
		if model := markdowntext.ParseProperties(source); model != nil {
			prefix = model.End
		}
		doc.SetWithPolicy(source, prefix, nil)
		if heading := headingNamed(doc, fragment); heading >= 0 {
			return doc.Block(heading).Start, true
		}
		return 0, false
	}
	at := 0
	for current := 1; current < line; current++ {
		next := strings.IndexByte(source[at:], '\n')
		if next < 0 {
			return len(source), true
		}
		at += next + 1
	}
	for current := 1; current < column && at < len(source) && source[at] != '\n'; current++ {
		_, size := utf8.DecodeRuneInString(source[at:])
		at += size
	}
	return at, true
}
