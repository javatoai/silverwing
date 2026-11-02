package markdown

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// A code card is a projection of the root Buffer. Its rows exclude only the
// parser's container/indentation prefixes and its opening/closing fence.
type editorCodeLine struct {
	text       string
	start, end int
	pad        int
	synthetic  bool
}

type editorCode struct {
	block, start, end             int
	bodyStart, bodyEnd            int
	openStart, openEnd, openerEnd int
	languageStart, languageEnd    int
	closeStart, closeEnd          int
	marker                        byte
	closed, fenced                bool
	prefix                        string
	rows                          []editorCodeLine
}

func codeLineEnd(source string, at int) int {
	if next := strings.IndexByte(source[at:], '\n'); next >= 0 {
		return at + next
	}
	return len(source)
}

func (s *EditorState) readCode(d *parser.Document, index int) editorCode {
	b := d.Block(index)
	start, end := s.blockRange(b)
	code := editorCode{block: index, start: start, end: end, fenced: b.Fenced, closeStart: -1}
	if syntax, ok := d.CodeSource(index); ok {
		code.openStart, code.openEnd = syntax.OpeningStart, syntax.OpeningEnd
		code.openerEnd = codeLineEnd(s.source, code.openStart)
		code.marker = s.source[code.openStart]
		code.languageStart, code.languageEnd = syntax.LanguageStart, syntax.LanguageEnd
		code.prefix = codeContinuationPrefix(s.source[start:code.openStart])
		if syntax.ClosingStart >= 0 {
			code.closed, code.closeStart, code.closeEnd = true, syntax.ClosingStart, syntax.ClosingEnd
			lineStart := strings.LastIndexByte(s.source[:code.closeStart], '\n') + 1
			code.prefix = s.source[lineStart:code.closeStart]
		}
	}
	for k := 0; k < d.LineCount(index); k++ {
		text, at := d.Line(index, k)
		pad := linePad(s.source, text, at)
		code.rows = append(code.rows, editorCodeLine{text: text, start: at, end: at + len(text) - pad, pad: pad})
	}
	if len(code.rows) > 0 {
		code.bodyStart, code.bodyEnd = code.rows[0].start, code.rows[len(code.rows)-1].end
		row := code.rows[0]
		lineStart := strings.LastIndexByte(s.source[:row.start], '\n') + 1
		code.prefix = codeContinuationPrefix(s.source[lineStart:row.start])
	} else if code.closed {
		code.bodyStart, code.bodyEnd = code.closeStart, code.closeStart
	} else {
		code.bodyStart = min(code.openerEnd+1, len(s.source))
		code.bodyEnd = code.bodyStart
	}
	if !code.closed && code.end == len(s.source) && strings.HasSuffix(s.source, "\n") && len(code.rows) > 0 {
		code.rows = append(code.rows, editorCodeLine{start: len(s.source), end: len(s.source), synthetic: true})
		code.bodyEnd = len(s.source)
	}
	return code
}

// List markers become the same width of indentation on subsequent lines;
// quote markers and all original whitespace retain their literal spelling.
func codeContinuationPrefix(prefix string) string {
	prefix = strings.TrimPrefix(prefix, "\ufeff")
	return strings.Map(func(r rune) rune {
		if r == '>' || r == ' ' || r == '\t' {
			return r
		}
		return ' '
	}, prefix)
}

func (s *EditorState) codeKeepsCard(index int) bool {
	if !s.opts.InPlaceCode || s.SourceMode || !s.projectionCurrent() || index <= 0 || index >= s.view.doc.Len() {
		return false
	}
	b := s.view.doc.Block(index)
	if b.Kind != parser.CodeBlock || s.view.o.Code[b.Language()] != nil {
		return false
	}
	if s.composing {
		if s.codeGapComposition() {
			return true
		}
		code := s.readCode(s.view.doc, index)
		return s.composeStart >= code.bodyStart && s.composeEnd <= code.bodyEnd
	}
	return true
}

func (s *EditorState) codeSourceProjection(code editorCode) (string, []int) {
	var text strings.Builder
	boundaries := []int{code.bodyStart}
	for k, row := range code.rows {
		if k > 0 {
			text.WriteByte('\n')
			boundaries = append(boundaries, row.start)
		}
		for pad := 0; pad < row.pad; pad++ {
			text.WriteByte(' ')
			boundaries = append(boundaries, row.start)
		}
		for at, r := range s.source[row.start:row.end] {
			text.WriteRune(r)
			boundaries = append(boundaries, row.start+at+utf8.RuneLen(r))
		}
	}
	return text.String(), boundaries
}

// codeProjection maps every displayed rune boundary to the virtual document,
// including UTF-16 native preedit. Container prefixes remain source-owned.
func (s *EditorState) codeProjection(code editorCode) (string, []int) {
	shown, boundaries := s.codeSourceProjection(code)
	if s.composing && s.composeStart >= code.bodyStart && s.composeEnd <= code.bodyEnd {
		first := sort.SearchInts(boundaries, s.composeStart)
		last := sort.SearchInts(boundaries, s.composeEnd)
		first, last = min(first, len(boundaries)-1), min(last, len(boundaries)-1)
		bytes := []int{0}
		for at, r := range shown {
			bytes = append(bytes, at+utf8.RuneLen(r))
		}
		shown = shown[:bytes[first]] + s.compose + shown[bytes[last]:]
		mapped := make([]int, 0, len(boundaries)+utf8.RuneCountInString(s.compose))
		for _, at := range boundaries[:first] {
			mapped = append(mapped, s.virtualOffset(at, false))
		}
		mapped = append(mapped, s.composeStart)
		for at, r := range s.compose {
			mapped = append(mapped, s.composeStart+at+utf8.RuneLen(r))
		}
		for _, at := range boundaries[last+1:] {
			mapped = append(mapped, s.virtualOffset(at, true))
		}
		boundaries = mapped
	} else {
		for k := range boundaries {
			boundaries[k] = s.virtualOffset(boundaries[k], false)
		}
	}
	if shown == "" {
		// One visual cell makes an empty code card clickable and gives the
		// root client a real candidate/caret rectangle without storing a space.
		shown = " "
		boundaries = append(boundaries[:1], boundaries[0])
	}
	return shown, boundaries
}

func (s *EditorState) codeBody(c *ui.Context, index int) ui.Element {
	st, th := s.view, &s.view.th
	code := s.readCode(st.doc, index)
	text, boundaries := s.codeProjection(code)
	lines := strings.Split(text, "\n")
	cs := st.codeState(index)
	st.highlightLines(cs, len(lines), func(k int) string { return lines[k] })
	pad := th.em(th.CodeBlockPadding)
	var outer ui.Element
	if st.o.WrapCode {
		outer = ui.Column(c)
	} else {
		outer = ui.ScrollHorizontal(c)
	}
	font := ui.Font{Family: th.CodeFont, Size: th.em(th.CodeSize)}
	outer.Direction(ui.LeftToRight).AlignSelf(ui.Stretch).Font(font.Family).FontSize(font.Size).LineHeight(th.CodeLineHeight)
	return outer.Children(func() {
		col := ui.Column(c).Role(ui.RoleGroup).Label("Code content").Padding(th.em(0.25), pad, pad, pad)
		if !st.o.WrapCode {
			col.Shrink(0)
		}
		col.Children(func() {
			at := 0
			for k, ch := range cs.chunks {
				count := utf8.RuneCountInString(spanText(ch.spans))
				logicalCount := count
				var mapped []int
				if ch.n == 1 && cs.lines[k*chunkLines] == "" {
					// The highlighter's clickable space represents an empty
					// final chunk, not a source character.
					logicalCount = 0
					mapped = []int{boundaries[at], boundaries[at]}
				} else {
					mapped = boundaries[at : at+count+1]
				}
				el := ui.RichText(c, ch.spans...).Font(font.Family).FontSize(font.Size).LineHeight(th.CodeLineHeight).MinHeight(font.Size * th.CodeLineHeight)
				if !st.o.WrapCode {
					el.NoWrap()
				}
				s.observe(el, ch.spans, mapped, mapped, font, th.CodeLineHeight, !st.o.WrapCode)
				if s.reveal && s.caret >= code.bodyStart && s.caret <= code.bodyEnd {
					el.ScrollIntoView()
				}
				at += logicalCount + 1 // the newline between display chunks
			}
		})
	})
}

var codeLanguages = []struct{ label, token string }{
	{"SQL", "sql"}, {"Python", "python"}, {"JavaScript", "javascript"},
	{"TypeScript", "typescript"}, {"Java", "java"}, {"Go", "go"},
	{"JSON", "json"}, {"YAML", "yaml"}, {"Shell", "shell"},
	{"HTML", "html"}, {"CSS", "css"}, {"纯文本", "text"},
}

func codeLanguageLabel(language string) string {
	language = strings.ToLower(language)
	switch language {
	case "", "txt", "plaintext":
		language = "text"
	case "js", "jsx":
		language = "javascript"
	case "ts", "tsx":
		language = "typescript"
	case "py", "python3":
		language = "python"
	case "yml":
		language = "yaml"
	case "sh", "bash", "zsh":
		language = "shell"
	}
	for _, option := range codeLanguages {
		if option.token == language || editor.LanguageNamed(language) != nil && editor.LanguageNamed(language) == editor.LanguageNamed(option.token) {
			return option.label
		}
	}
	return ""
}

// LanguageSelect builds the native Markdown info-language selector. Custom
// CodeBlock wrappers may place it beside CopyButton and keep using Body.
// In a reader it is a label; it never changes a filename or executes code.
func (b *CodeBlockView) LanguageSelect(c *ui.Context) ui.Element {
	s, th := b.st.editor, &b.st.th
	if s == nil || !s.opts.InPlaceCode || s.SourceMode {
		return ui.Text(c, b.Language).FontSize(th.em(.8)).TextColor(th.Muted).Unselectable()
	}
	cs := b.st.codeState(b.Index)
	current := codeLanguageLabel(b.Language)
	if current == "" {
		current = b.Language
	}
	if cs.languageSelection == "" {
		cs.languageSelection = current
	}
	choices := make([]string, 0, len(codeLanguages)+1)
	if codeLanguageLabel(b.Language) == "" {
		choices = append(choices, b.Language)
	}
	for _, option := range codeLanguages {
		choices = append(choices, option.label)
	}
	version := s.snapshot.Version()
	selectBox := ui.Select(c.Key(fmt.Sprintf("code-language-%d", version)), &cs.languageSelection, choices).
		Label("代码语言").Tooltip("Markdown 代码块语言").Disabled(s.opts.ReadOnly || s.composing).
		HandleInput(codeControlInput).
		MinWidth(th.em(6.5)).FontSize(th.em(.8)).Padding(th.em(.15), th.em(.45)).Radius(5).
		BorderWidth(0).Background(Color{})
	if selectBox.Hovered() && !s.opts.ReadOnly && !s.composing {
		selectBox.Background(th.Text.Alpha(.06))
	}
	if selectBox.Changed() && cs.languageSelection != current {
		for _, option := range codeLanguages {
			if option.label == cs.languageSelection {
				if err := s.setCodeLanguage(b.Index, option.token, version); err != nil {
					cs.languageSelection = current
					s.report(err)
				}
				break
			}
		}
	}
	return selectBox
}

// The root's text gesture handler must leave native card controls to their
// own Clicked/Shortcut handling. This installs no text input client.
func codeControlInput(ui.InputEvent) bool { return false }

func (s *EditorState) editingCode(a, z int) (editorCode, bool) {
	if !s.opts.InPlaceCode || s.SourceMode {
		return editorCode{}, false
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.CodeBlock || s.view.o.Code[b.Language()] != nil {
			continue
		}
		code := s.readCode(d, i)
		if a >= code.bodyStart && z <= code.bodyEnd {
			return code, true
		}
	}
	return editorCode{}, false
}

func (s *EditorState) codeDeleteRange(dir int, word bool) (int, int, bool) {
	if at := s.codeVisiblePosition(s.caret); at != s.caret {
		return at, at, true
	}
	code, ok := s.editingCode(s.caret, s.caret)
	if !ok {
		return 0, 0, false
	}
	at := s.grapheme(s.caret, dir)
	if word {
		at = s.wordBoundary(s.caret, dir)
	}
	at = max(code.bodyStart, min(at, code.bodyEnd))
	for k, row := range code.rows {
		if dir < 0 && s.caret == row.start && k > 0 {
			at = code.rows[k-1].end
		} else if dir > 0 && s.caret == row.end && k+1 < len(code.rows) {
			at = code.rows[k+1].start
		}
	}
	return min(at, s.caret), max(at, s.caret), true
}

// Native document/word navigation and source-based host locations can land in
// a hidden fence. Keep collapsed live input at its visible code edge, while
// explicit selections and ReplaceRange still retain full source semantics.
func (s *EditorState) codeVisiblePosition(at int) int {
	if !s.opts.InPlaceCode || s.SourceMode {
		return at
	}
	s.ensureCodeTail(at)
	// A pointer/arrow can target the writing row while the old selection is
	// still inside a code body. Test the intended destination, not that old
	// selection, before clamping hidden fence positions to visible code.
	gap := &s.codeGapFocus.gap
	if s.codeGapFocus.valid && !s.opts.ReadOnly && gap.version == s.snapshot.Version() && gap.contains(at) {
		return at
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.CodeBlock || !b.Fenced || s.view.o.Code[b.Language()] != nil {
			continue
		}
		code := s.readCode(d, i)
		if at >= code.openStart && (at < code.end || at == code.end && at == len(s.source) && !strings.HasSuffix(s.source, "\n")) {
			return max(code.bodyStart, min(at, code.bodyEnd))
		}
	}
	return at
}

func (s *EditorState) codeHorizontal(dir int) (int, ui.TextAffinity, bool) {
	code, ok := s.editingCode(s.caret, s.caret)
	if !ok || dir < 0 && s.caret != code.bodyStart || dir > 0 && s.caret != code.bodyEnd {
		return 0, ui.TextDownstream, false
	}
	at, affinity := s.caret, ui.TextDownstream
	if dir < 0 {
		affinity = ui.TextUpstream
		found := false
		for _, run := range s.runs {
			end := run.boundaries[len(run.boundaries)-1]
			if end <= code.start && (!found || end > at) {
				at, found = end, true
			}
		}
	} else {
		for _, run := range s.runs {
			start := run.boundaries[0]
			if start >= code.end && (at == s.caret || start < at) {
				at = start
			}
		}
	}
	return at, affinity, true
}

// codeInputText decorates one user edit with continuation prefixes and, only
// if needed, longer fences. All edits go into the same root transaction.
func (s *EditorState) codeInputText(a, z int, text string, endpoint int) (string, int, []editor.Edit) {
	code, ok := s.editingCode(a, z)
	if !ok || text == "" && a == z {
		return text, endpoint, nil
	}
	literalText := text
	var out strings.Builder
	mapped := 0
	for at := 0; at < len(text); {
		if at == endpoint {
			mapped = out.Len()
		}
		ch := text[at]
		if ch == '\r' || ch == '\n' {
			out.WriteByte(ch)
			at++
			if ch == '\r' && at < len(text) && text[at] == '\n' {
				out.WriteByte('\n')
				at++
			}
			out.WriteString(code.prefix)
		} else {
			out.WriteByte(ch)
			at++
		}
	}
	if endpoint == len(text) {
		mapped = out.Len()
	}
	text = out.String()
	if code.fenced && len(code.rows) == 0 && text != "" {
		if code.closed {
			text += s.buffer.LineEnding() + code.prefix
		} else {
			prefix := code.prefix
			if code.openerEnd == len(s.source) {
				prefix = s.buffer.LineEnding() + prefix
			}
			text, mapped = prefix+text, len(prefix)+mapped
		}
	}
	if !code.fenced {
		return text, mapped, nil
	}
	// Test the candidate logical body for a possible closing fence, so quote
	// prefixes with different original whitespace cannot hide a collision.
	body, boundaries := s.codeSourceProjection(code)
	first, last := sort.SearchInts(boundaries, a), sort.SearchInts(boundaries, z)
	runeBytes := []int{0}
	for at, r := range body {
		runeBytes = append(runeBytes, at+utf8.RuneLen(r))
	}
	first, last = min(first, len(runeBytes)-1), min(last, len(runeBytes)-1)
	candidate := body[:runeBytes[first]] + normalizedText(literalText) + body[runeBytes[last]:]
	possibleClose := false
	for _, line := range strings.Split(candidate, "\n") {
		// This is only a cheap filter. The parser below decides indentation
		// using real container columns and tab stops, including partial tabs.
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		at := indent
		for at < len(line) && line[at] == code.marker {
			at++
		}
		if at-indent >= code.openEnd-code.openStart && strings.Trim(line[at:], " \t") == "" {
			possibleClose = true
			break
		}
	}
	if !possibleClose {
		return text, mapped, nil
	}
	needed := s.safeCodeFenceLength(code, a, z, text)
	if needed == code.openEnd-code.openStart {
		return text, mapped, nil
	}
	fence := strings.Repeat(string(code.marker), needed)
	edits := []editor.Edit{{Start: code.openStart, End: code.openEnd, Text: fence}}
	if code.closed && code.closeEnd-code.closeStart < needed {
		edits = append(edits, editor.Edit{Start: code.closeStart, End: code.closeEnd, Text: fence})
	}
	return text, mapped, edits
}

// Only a candidate that may contain fence syntax reaches this path. Parsing a
// scratch document reuses the actual list/quote/tab rules without changing the
// view's incremental parser cache. Extend past each real premature closer;
// fence-like literals with four columns of indentation never set the length.
func (s *EditorState) safeCodeFenceLength(code editorCode, a, z int, text string) int {
	originalLength := code.openEnd - code.openStart
	needed := originalLength
	replacement := normalizedText(text)
	candidate := s.source[:a] + replacement + s.source[z:]
	drafts := moveFenceDrafts(s.literalFenceLines(), a, z, len(replacement)-(z-a))
	boundary := code.end
	if code.closed {
		boundary = code.closeStart
	}
	boundary += len(replacement) - (z - a)
	d := parser.New(s.view.ext)
	for {
		d.SetWithPolicy(candidate, s.sourcePrefixFor(candidate), drafts)
		var closing parser.CodeSource
		found := false
		for i := 1; i < d.Len(); i++ {
			b := d.Block(i)
			if b.Start > code.openStart {
				break
			}
			if b.Kind == parser.CodeBlock && b.Fenced && b.Start == code.openStart {
				closing, found = d.CodeSource(i)
				break
			}
		}
		if !found || closing.ClosingStart < 0 || closing.ClosingStart >= boundary+needed-originalLength {
			return needed
		}
		previous := needed
		needed = closing.ClosingEnd - closing.ClosingStart + 1
		candidate = candidate[:code.openStart] + strings.Repeat(string(code.marker), needed) + candidate[code.openStart+previous:]
		drafts = moveFenceDrafts(drafts, code.openStart, code.openStart+previous, needed-previous)
	}
}

func moveFenceDrafts(drafts []int, start, end, delta int) []int {
	result := make([]int, 0, len(drafts))
	for _, at := range drafts {
		if at > start && at < end {
			continue
		}
		if at >= end {
			at += delta
		}
		result = append(result, at)
	}
	return result
}

// Code-only structural edits need the same literal-fence protection as typed
// text. The scratch replacement is never saved; each original range remains a
// separate edit in the single transaction, including any longer outer fences.
func (s *EditorState) applyCodeEdits(code editorCode, edits []editor.Edit, anchor, caret int) error {
	if code.fenced && len(edits) > 0 {
		first, last := edits[0].Start, edits[len(edits)-1].End
		var candidate strings.Builder
		at := first
		for _, edit := range edits {
			candidate.WriteString(s.source[at:edit.Start])
			candidate.WriteString(normalizedText(edit.Text))
			at = edit.End
		}
		candidate.WriteString(s.source[at:last])
		needed := code.openEnd - code.openStart
		body := s.source[code.bodyStart:first] + candidate.String() + s.source[last:code.bodyEnd]
		if strings.Contains(body, strings.Repeat(string(code.marker), needed)) {
			needed = s.safeCodeFenceLength(code, first, last, candidate.String())
		}
		if growth := needed - (code.openEnd - code.openStart); growth > 0 {
			fence := strings.Repeat(string(code.marker), needed)
			edits = append(edits, editor.Edit{Start: code.openStart, End: code.openEnd, Text: fence})
			if code.closed && code.closeEnd-code.closeStart < needed {
				edits = append(edits, editor.Edit{Start: code.closeStart, End: code.closeEnd, Text: fence})
			}
			anchor, caret = anchor+growth, caret+growth
		}
	}
	return s.ApplyEdits(edits, anchor, caret, s.snapshot.Version())
}

func (s *EditorState) setCodeLanguage(index int, language string, version uint64) error {
	s.sync()
	if s.opts.ReadOnly || s.SourceMode || !s.opts.InPlaceCode {
		return errors.New("Markdown code language cannot be changed in this mode")
	}
	if s.composing {
		return errors.New("Markdown input composition is active")
	}
	if s.snapshot.Version() != version || !s.projectionCurrent() || index <= 0 || index >= s.view.doc.Len() || s.view.doc.Block(index).Kind != parser.CodeBlock {
		return errors.New("Markdown source changed before the language could be applied")
	}
	b := s.view.doc.Block(index)
	if b.Language() == language || codeLanguageLabel(b.Language()) != "" && codeLanguageLabel(b.Language()) == codeLanguageLabel(language) {
		return nil // preserve aliases, capitalization, escapes and all whitespace
	}
	code := s.readCode(s.view.doc, index)
	edit := editor.Edit{Start: code.languageStart, End: code.languageEnd, Text: language}
	var converted []editorCodePosition
	if !code.fenced {
		var replacement string
		replacement, converted = s.fenceIndentedCode(code, language)
		edit = editor.Edit{Start: code.start, End: code.end, Text: replacement}
	}
	before, after := editorSelection{s.anchor, s.caret}, editorSelection{}
	s.buffer.EndUndoGroup()
	result := s.buffer.ApplyTransaction(editor.Transaction{Version: version, Edits: []editor.Edit{edit}, Kind: editor.EditOther, Owner: s,
		SelectionBefore: before, SelectionAfterEdit: func(_ editor.Snapshot, changes []editor.Change) any {
			mapAt := func(at int) int {
				for _, row := range converted {
					if at >= row.start && at <= row.end {
						return row.to + at - row.start
					}
				}
				return mapEditorOffset(at, changes)
			}
			after = editorSelection{mapAt(before.Anchor), mapAt(before.Caret)}
			return after
		}})
	s.buffer.EndUndoGroup()
	if !result.Applied {
		if result.Rejected {
			return errors.New("Markdown edit exceeds the source size limit")
		}
		return errors.New("Markdown source changed before the language could be applied")
	}
	s.sync()
	s.anchor, s.caret = byteBoundary(s.source, after.Anchor), byteBoundary(s.source, after.Caret)
	s.focusRequest, s.reveal = true, false
	s.metadataViewportVersion = s.snapshot.Version()
	if s.snapshot.Version() != version {
		s.notifyChanged()
	}
	return nil
}

type editorCodePosition struct{ start, end, to int }

func (s *EditorState) fenceIndentedCode(code editorCode, language string) (string, []editorCodePosition) {
	ending := s.buffer.LineEnding()
	fenceSize := 3
	for _, row := range code.rows {
		for _, part := range strings.FieldsFunc(row.text, func(r rune) bool { return r != '`' }) {
			fenceSize = max(fenceSize, len(part)+1)
		}
	}
	fence := strings.Repeat("`", fenceSize)
	prefixAt := func(row editorCodeLine) string {
		lineStart := strings.LastIndexByte(s.source[:row.start], '\n') + 1
		prefix := s.source[lineStart:row.start]
		// Parser rows have already consumed four columns of code indentation.
		// Strip those columns from the right, keeping quote/list indentation.
		columns := 4 - row.pad
		for len(prefix) > 0 && columns > 0 {
			ch := prefix[len(prefix)-1]
			if ch != ' ' && ch != '\t' {
				break
			}
			prefix = prefix[:len(prefix)-1]
			if ch == '\t' {
				columns -= 4
			} else {
				columns--
			}
		}
		return prefix
	}
	prefix := ""
	if len(code.rows) > 0 {
		prefix = prefixAt(code.rows[0])
	}
	var out strings.Builder
	normalizedSize := 0
	write := func(text string) {
		out.WriteString(text)
		normalizedSize += len(normalizedText(text))
	}
	write(prefix + fence + language + ending)
	var positions []editorCodePosition
	for _, row := range code.rows {
		if row.synthetic {
			continue
		}
		write(codeContinuationPrefix(prefixAt(row)))
		write(strings.Repeat(" ", row.pad))
		positions = append(positions, editorCodePosition{row.start, row.end, code.start + normalizedSize})
		write(s.snapshot.OriginalSlice(row.start, row.end))
		if row.end < len(s.source) && s.source[row.end] == '\n' {
			write(s.snapshot.OriginalSlice(row.end, row.end+1))
		} else {
			write(ending)
		}
	}
	write(codeContinuationPrefix(prefix) + fence)
	if code.end > code.start && s.source[code.end-1] == '\n' {
		write(s.snapshot.OriginalSlice(code.end-1, code.end))
	}
	return out.String(), positions
}
