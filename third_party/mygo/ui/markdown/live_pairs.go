package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type autoPair struct {
	Open, Close      int
	Opening, Closing string
}

func (s *EditorState) pairsAfterEdit(source string, changes []editor.Change, pairs []autoPair) []autoPair {
	var result []autoPair
	for _, pair := range pairs {
		pair.Open = mapEditorOffset(pair.Open, changes)
		pair.Close = mapEditorOffset(pair.Close, changes)
		if pair.Open >= 0 && pair.Close >= pair.Open+len(pair.Opening) && pair.Close+len(pair.Closing) <= len(source) && strings.HasPrefix(source[pair.Open:], pair.Opening) && strings.HasPrefix(source[pair.Close:], pair.Closing) {
			result = append(result, pair)
		}
	}
	return result
}

func (s *EditorState) pairInput(a, z int, text string, kind editor.EditKind) (string, int, *autoPair, bool) {
	if !s.opts.SmartEditing || s.SourceMode || s.composing || kind != editor.EditTyping || utf8.RuneCountInString(text) != 1 {
		return text, len(text), nil, false
	}
	if a == z {
		for i, pair := range s.autoPairs {
			if pair.Close == a && pair.Closing == text && strings.HasPrefix(s.source[a:], text) {
				s.autoPairs = slices.Delete(s.autoPairs, i, i+1)
				s.anchor, s.caret = a+len(text), a+len(text)
				s.reveal = true
				s.invalidate()
				return text, 0, nil, true
			}
		}
	}
	// Backticks entered in code are literal content. Generating a second one
	// can change the delimiter width and even turn an inline span into a fence.
	if text == "`" {
		_, block := s.editingCode(a, z)
		if block || s.editingInlineCodeBody(a, z) {
			return text, len(text), nil, false
		}
	}
	closing, ok := map[string]string{"(": ")", "[": "]", "{": "}", "\"": "\"", "'": "'", "`": "`"}[text]
	if !ok {
		return text, len(text), nil, false
	}
	slashes := 0
	for at := a - 1; at >= 0 && s.source[at] == '\\'; at-- {
		slashes++
	}
	if slashes%2 != 0 {
		return text, len(text), nil, false
	}
	if text == "`" && a == z && s.fenceTypingPrefix(a) {
		return text, len(text), nil, false
	}
	before, after := rune(0), rune(0)
	if a > 0 {
		before, _ = utf8.DecodeLastRuneInString(s.source[:a])
	}
	if z < len(s.source) {
		after, _ = utf8.DecodeRuneInString(s.source[z:])
	}
	quote := text == "'" || text == "\"" || text == "`"
	if a == z && (unicode.IsLetter(after) || unicode.IsDigit(after) || quote && (unicode.IsLetter(before) || unicode.IsDigit(before))) {
		return text, len(text), nil, false
	}
	body := s.source[a:z]
	pair := &autoPair{Opening: text, Closing: closing}
	return text + body + closing, len(text) + len(body), pair, false
}

func (s *EditorState) fenceTypingPrefix(at int) bool {
	prefix := strings.TrimRight(s.source[sourceLineStart(s.source, at):at], "`")
	if strings.Trim(strings.TrimPrefix(prefix, "\ufeff"), " \t") == "" {
		return true
	}
	// Let the existing parser decide quote/list/tab indentation. Headings and
	// prose containing marker-like characters must still get inline pairing.
	d := parser.New(s.view.ext)
	d.Set(prefix + "```")
	for i := 1; i < d.Len(); i++ {
		if _, ok := d.CodeSource(i); ok {
			return true
		}
	}
	return false
}

func (s *EditorState) pairKey(key ui.Key, mods ui.Modifiers) bool {
	if !s.opts.SmartEditing || s.SourceMode || s.opts.ReadOnly || s.composing || key != ui.KeyBackspace || mods != 0 || s.anchor != s.caret {
		return false
	}
	for _, pair := range s.autoPairs {
		if pair.Close == s.caret && pair.Close == pair.Open+len(pair.Opening) {
			s.report(s.ApplyEdits([]editor.Edit{{Start: pair.Open, End: pair.Close + len(pair.Closing)}}, pair.Open, pair.Open, s.snapshot.Version()))
			return true
		}
	}
	return false
}
