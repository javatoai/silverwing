package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"silverwing-go/internal/editor"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

const (
	csvPreviewRowHeight = float32(36)
	csvPreviewCellWidth = float32(160)
)

type csvSelectionState struct {
	identity                string
	anchorRow, anchorColumn int
	extentRow, extentColumn int
	dragging                bool
}

func (s *app) prepareDocumentFind() {
	if s.materialsTextEditingVisible() {
		return
	}
	if s.materialsUnit.state.documentEditing && s.materialsUnit.state.documentPath != "" && documentKindForPath(s.materialsUnit.state.documentPath) == "markdown" {
		s.ensureMarkdownEditor()
	}
	f := s.ensureDocumentFindState()
	f.preparePreview(s.materialsUnit.state.documentPath, s.materialsUnit.state.documentKind, s.materialsUnit.state.documentContent)
	if s.materialsTextPreviewVisible() {
		return
	}
	f.nativePreview = nil
	t := s.taskUnit.currentTask()
	identity := strings.Join([]string{t.id, normalizedReadingPath(t.path), normalizedReadingRoot(t.materialsPath), normalizedReadingPath(s.materialsUnit.state.documentPath), s.materialsUnit.state.documentKind, s.readingMode(), fmt.Sprint(s.materialsUnit.state.documentEditing)}, "\x00")
	if s.preparePDFDocumentFind(f, identity) {
		return
	}
	blocks := s.documentFindBlocks(f, identity)
	f.set(identity, s.materialsUnit.state.documentSearch, blocks)
}

func (s *app) documentFindBlocks(cache *documentFindState, identity string) []documentFindBlock {
	if s.materialsUnit.state.documentPath == "" || s.materialsUnit.state.documentContent == "" {
		cache.preparePreview(s.materialsUnit.state.documentPath, s.materialsUnit.state.documentKind, s.materialsUnit.state.documentContent)
		return nil
	}
	cache.preparePreview(s.materialsUnit.state.documentPath, s.materialsUnit.state.documentKind, s.materialsUnit.state.documentContent)
	ext := strings.ToLower(filepath.Ext(s.materialsUnit.state.documentPath))
	key := "document-content"
	switch {
	case ext == ".json":
		return []documentFindBlock{{key: key, text: s.materialsUnit.state.documentContent, lineSize: 20}}
	case ext == ".csv":
		if cache.csvErr != "" {
			return []documentFindBlock{{key: key, text: s.materialsUnit.state.documentContent, lineSize: 20}}
		}
		blocks := make([]documentFindBlock, 0)
		for rowIndex, row := range cache.csvRows {
			for column, cell := range row {
				blocks = append(blocks, documentFindBlock{key: csvFindKey(rowIndex, column), text: cell, scrollY: float32(rowIndex) * csvPreviewRowHeight, lineSize: 20})
			}
		}
		return blocks
	case documentKindForPath(s.materialsUnit.state.documentPath) == "markdown" && s.materialsUnit.state.documentEditing && !s.materialsUnit.state.documentReadOnly && s.markdownEditorIsCurrent(s.materialsUnit.state.markdown):
		return markdownEditorFindBlocks(s.materialsUnit.state.markdown)
	case documentKindForPath(s.materialsUnit.state.documentPath) == "markdown" && (!s.materialsUnit.state.documentEditing || s.materialsUnit.state.documentReadOnly) && s.readingMode() == "RENDERED":
		state := s.ensureMarkdownEditor()
		return frameworkMarkdownFindBlocks(state)
	default:
		return []documentFindBlock{{key: key, text: s.materialsUnit.state.documentContent, lineSize: 20}}
	}
}

func (f *documentFindState) preparePreview(path, kind, source string) {
	if f.previewPath == path && f.previewKind == kind && f.previewSource == source {
		return
	}
	f.previewPath, f.previewKind, f.previewSource = path, kind, source
	f.jsonText, f.jsonValid = prettyJSONDocument(source)
	var err error
	f.csvRows, err = parseCSVDocument(source)
	f.csvErr = ""
	if err != nil {
		f.csvErr = err.Error()
	}
	f.syntaxKey, f.syntaxText, f.syntaxSpans = "", "", nil
}

func (s *app) renderDocumentFindBar(c *ui.Context, p palette) {
	if s.materialsTextPreviewVisible() || s.materialsTextEditingVisible() {
		return
	}
	f := s.ensureDocumentFindState()
	if !s.materialsUnit.state.documentFindOpen {
		return
	}
	pdf := s.currentPDFDocumentFind()
	count, status := len(f.hits), documentFindStatus(f)
	if pdf != nil {
		count, status = len(pdf.searchResults), pdfDocumentFindStatus(pdf)
	}
	ui.Row(c).Height(38).Shrink(0).Padding(0, 12).Gap(7).Background(p.soft).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key("document-find-input"), &s.materialsUnit.state.documentSearch).Grow(1).MinWidth(90).MaxWidth(250).Height(30).Placeholder("查找正文").Label("查找正文").Bind(&f.focus)
		input.OnChange(func() {
			if pdf != nil {
				s.changePDFDocumentFindQuery(pdf, s.materialsUnit.state.documentSearch)
				return
			}
			f.set(f.identity, s.materialsUnit.state.documentSearch, f.blocks)
		})
		input.OnSubmit(func() { s.moveDocumentFind(1) })
		input.OnShortcut(0, ui.KeyEscape, func() { s.closeDocumentFind() })
		if iconButtonDisabled(c, "document-find-previous", "chevron-left", "上一个结果 · Shift+F3", p, count == 0).Clicked() {
			s.moveDocumentFind(-1)
		}
		if iconButtonDisabled(c, "document-find-next", "chevron", "下一个结果 · F3", p, count == 0).Clicked() {
			s.moveDocumentFind(1)
		}
		label(c, status, 11, p.muted).NoWrap()
		if f.truncated {
			label(c, "仅统计前 10,000 处", 10, p.amber).NoWrap()
		}
		ui.Spacer(c)
		if iconButton(c, "document-find-close", "close", "关闭查找", p).Clicked() {
			s.closeDocumentFind()
		}
	})
}

func prettyJSONDocument(source string) (string, bool) {
	if !json.Valid([]byte(source)) {
		return "", false
	}
	var output bytes.Buffer
	if err := json.Indent(&output, []byte(source), "", "  "); err != nil {
		return "", false
	}
	return output.String(), true
}

func parseCSVDocument(source string) ([][]string, error) {
	if source == "" {
		return [][]string{}, nil
	}
	rows := make([][]string, 0)
	row := make([]string, 0)
	var cell strings.Builder
	quoted, closedQuote := false, false
	for index := 0; index < len(source); index++ {
		value := source[index]
		switch {
		case quoted && value == '"' && index+1 < len(source) && source[index+1] == '"':
			cell.WriteByte('"')
			index++
		case quoted && value == '"':
			quoted, closedQuote = false, true
		case quoted:
			cell.WriteByte(value)
		case value == '"' && cell.Len() == 0 && !closedQuote:
			quoted = true
		case value == ',':
			row = append(row, cell.String())
			cell.Reset()
			closedQuote = false
		case value == '\n' || value == '\r':
			row = append(row, cell.String())
			cell.Reset()
			rows = append(rows, row)
			row = make([]string, 0)
			closedQuote = false
			if value == '\r' && index+1 < len(source) && source[index+1] == '\n' {
				index++
			}
		case closedQuote || value == '"':
			return nil, fmt.Errorf("CSV 引号格式无效")
		default:
			cell.WriteByte(value)
		}
	}
	if quoted {
		return nil, fmt.Errorf("CSV 引号未闭合")
	}
	if len(row) > 0 || cell.Len() > 0 || source[len(source)-1] == ',' {
		row = append(row, cell.String())
		rows = append(rows, row)
	}
	return rows, nil
}

func csvFindKey(row, column int) string { return fmt.Sprintf("csv:%d:%d", row, column) }

func (s *app) renderCSVPreview(c *ui.Context, p palette, source string) {
	f := s.ensureDocumentFindState()
	rows, parseErr := f.csvRows, f.csvErr
	if f.previewSource != source {
		var err error
		rows, err = parseCSVDocument(source)
		parseErr = ""
		if err != nil {
			parseErr = err.Error()
		}
	}
	if parseErr != "" {
		s.renderDetectedMaterialsText(c, p)
		return
	}
	if len(rows) == 0 {
		return
	}
	ui.ScrollHorizontal(c).TrackScroll(s.readingScrollState("csv-horizontal")).FillWidth().Children(func() {
		cacheIdentity := fmt.Sprintf("%s\x00%s\x00%d\x00%d", s.materialsUnit.state.documentPath, source, len(rows), len(rows[0]))
		root := ui.Column(c).Key("csv-grid-" + filepath.Clean(s.materialsUnit.state.documentPath))
		selection := ui.Local(root, "selection", func() csvSelectionState {
			return csvSelectionState{anchorRow: -1, anchorColumn: -1, extentRow: -1, extentColumn: -1}
		})
		if selection.identity != cacheIdentity {
			*selection = csvSelectionState{identity: cacheIdentity, anchorRow: -1, anchorColumn: -1, extentRow: -1, extentColumn: -1}
		}
		scroll := s.readingScrollState("text")
		columns := 0
		for _, row := range rows {
			columns = maxInt(columns, len(row))
		}
		if columns < 1 {
			return
		}
		inside := root.HandleInput(func(event ui.InputEvent) bool {
			cellAt := func(x, y float32) (int, int) {
				row := int(maxFloat32(y+scroll.Y, 0) / csvPreviewRowHeight)
				column := int(maxFloat32(x, 0) / csvPreviewCellWidth)
				return minInt(row, len(rows)-1), minInt(column, columns-1)
			}
			switch event.Kind {
			case ui.InputPointerDown:
				if event.Button != 0 {
					return false
				}
				row, column := cellAt(event.X, event.Y)
				selection.anchorRow, selection.anchorColumn = row, column
				selection.extentRow, selection.extentColumn = row, column
				selection.dragging = true
				root.Focus()
				return true
			case ui.InputPointerMove:
				if selection.dragging {
					row, column := cellAt(event.X, event.Y)
					selection.extentRow, selection.extentColumn = row, column
					return true
				}
			case ui.InputPointerUp:
				if selection.dragging {
					row, column := cellAt(event.X, event.Y)
					selection.extentRow, selection.extentColumn = row, column
					selection.dragging = false
					return true
				}
			case ui.InputKeyDown:
				if (event.Mods&ui.Ctrl != 0 || event.Mods&ui.Super != 0) && event.Key == ui.KeyC {
					if csvSelectionValid(*selection) {
						s.copyTextToClipboard(c.Services(), csvSelectedFragment(rows, *selection), "")
					}
					return true
				}
				if event.Key == ui.KeyEscape && csvSelectionValid(*selection) {
					selection.anchorRow, selection.anchorColumn = -1, -1
					selection.extentRow, selection.extentColumn = -1, -1
					return true
				}
			}
			return false
		})
		inside.Focusable().ContextMenu(func(menu *ui.Menu) {
			if menu.Item("复制").Disabled(!csvSelectionValid(*selection)).Chosen() {
				s.copyTextToClipboard(c.Services(), csvSelectedFragment(rows, *selection), "")
			}
		})
		inside.FillWidth().Children(func() {
			for rowIndex, row := range rows {
				line := ui.Row(c).Height(csvPreviewRowHeight).Shrink(0).Gap(0)
				line.Children(func() {
					for column := 0; column < columns; column++ {
						value := ""
						if column < len(row) {
							value = row[column]
						}
						selected := csvCellSelected(*selection, rowIndex, column)
						cell := ui.Box(c).Width(csvPreviewCellWidth).Height(csvPreviewRowHeight).Padding(8, 10).Border(1, p.line)
						if selected {
							cell.Background(p.selected)
						}
						cell.Children(func() {
							color := p.text
							if rowIndex == 0 {
								color = p.text
							}
							spans := f.spans(csvFindKey(rowIndex, column), value, 0, color, nil)
							if len(spans) == 0 {
								spans = []ui.Span{{Text: " "}}
							}
							text := ui.RichText(c, spans...).FillWidth().FontSize(11).SingleLine()
							if rowIndex == 0 {
								text.FontWeight(600)
							}
							if f.shouldReveal(csvFindKey(rowIndex, column)) {
								cell.ScrollIntoView()
							}
						})
					}
				})
			}
		})
	})
}

func csvSelectionValid(state csvSelectionState) bool {
	return state.anchorRow >= 0 && state.anchorColumn >= 0 && state.extentRow >= 0 && state.extentColumn >= 0
}

func csvCellSelected(state csvSelectionState, row, column int) bool {
	if !csvSelectionValid(state) {
		return false
	}
	return row >= minInt(state.anchorRow, state.extentRow) && row <= maxInt(state.anchorRow, state.extentRow) &&
		column >= minInt(state.anchorColumn, state.extentColumn) && column <= maxInt(state.anchorColumn, state.extentColumn)
}

func csvSelectedFragment(rows [][]string, state csvSelectionState) string {
	if !csvSelectionValid(state) {
		return ""
	}
	firstRow, lastRow := minInt(state.anchorRow, state.extentRow), maxInt(state.anchorRow, state.extentRow)
	firstColumn, lastColumn := minInt(state.anchorColumn, state.extentColumn), maxInt(state.anchorColumn, state.extentColumn)
	var output strings.Builder
	for rowIndex := firstRow; rowIndex <= lastRow; rowIndex++ {
		if rowIndex > firstRow {
			output.WriteString("\r\n")
		}
		for column := firstColumn; column <= lastColumn; column++ {
			if column > firstColumn {
				output.WriteByte(',')
			}
			value := ""
			if rowIndex < len(rows) && column < len(rows[rowIndex]) {
				value = rows[rowIndex][column]
			}
			output.WriteString(escapeCSVCell(value))
		}
	}
	return output.String()
}

func escapeCSVCell(value string) string {
	if !strings.ContainsAny(value, "\",\n\r") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (f *documentFindState) documentSyntax(text string, language workspace.WorkspaceFileLanguage, p palette) []documentSyntaxSpan {
	key := string(language)
	if f.syntaxKey == key && f.syntaxText == text && f.syntaxPalette == p {
		return f.syntaxSpans
	}
	f.syntaxKey, f.syntaxText, f.syntaxPalette = key, text, p
	f.syntaxSpans = syntaxSpansForDocument(text, language, p)
	return f.syntaxSpans
}

func syntaxSpansForDocument(text string, language workspace.WorkspaceFileLanguage, p palette) []documentSyntaxSpan {
	if language == workspace.FileLanguagePlainText || text == "" {
		return nil
	}
	var result []documentSyntaxSpan
	blockComment := false
	markupComment := false
	inMarkupTag := false
	offset := 0
	for offset < len(text) {
		end := strings.IndexByte(text[offset:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += offset
		}
		line := text[offset:end]
		for i := 0; i < len(line); {
			if markupComment {
				closeAt := strings.Index(line[i:], "-->")
				if closeAt < 0 {
					result = append(result, documentSyntaxSpan{offset + i, offset + len(line), p.green})
					i = len(line)
					continue
				}
				closeAt += i + 3
				result = append(result, documentSyntaxSpan{offset + i, offset + closeAt, p.green})
				i, markupComment = closeAt, false
				continue
			}
			if blockComment {
				closeAt := strings.Index(line[i:], "*/")
				if closeAt < 0 {
					result = append(result, documentSyntaxSpan{offset + i, offset + len(line), p.green})
					i = len(line)
					continue
				}
				closeAt += i + 2
				result = append(result, documentSyntaxSpan{offset + i, offset + closeAt, p.green})
				i, blockComment = closeAt, false
				continue
			}
			if language == workspace.FileLanguageHTML && strings.HasPrefix(line[i:], "<!--") {
				if closeAt := strings.Index(line[i+4:], "-->"); closeAt >= 0 {
					closeAt += i + 7
					result = append(result, documentSyntaxSpan{offset + i, offset + closeAt, p.green})
					i = closeAt
				} else {
					result = append(result, documentSyntaxSpan{offset + i, offset + len(line), p.green})
					i, markupComment = len(line), true
				}
				continue
			}
			if language == workspace.FileLanguageHTML && line[i] == '<' {
				j := i + 1
				if j < len(line) && line[j] == '/' {
					j++
				}
				for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
					j++
				}
				nameStart := j
				for j < len(line) && (isIdentifierRuneAt(line, j) || isASCIIOrUnicodeDigitAt(line, j) || line[j] == '-' || line[j] == ':') {
					_, size := utf8.DecodeRuneInString(line[j:])
					j += size
				}
				if j > nameStart {
					result = append(result, documentSyntaxSpan{offset + i, offset + j, p.blue})
					inMarkupTag = true
					i = j
					continue
				}
			}
			if language == workspace.FileLanguageHTML && line[i] == '>' {
				inMarkupTag = false
				i++
				continue
			}
			comment := documentLineCommentStart(line, i, language)
			if comment {
				result = append(result, documentSyntaxSpan{offset + i, offset + len(line), p.green})
				break
			}
			if language != workspace.FileLanguageJSON && strings.HasPrefix(line[i:], "/*") && supportsCComments(language) {
				if closeAt := strings.Index(line[i+2:], "*/"); closeAt >= 0 {
					closeAt += i + 4
					result = append(result, documentSyntaxSpan{offset + i, offset + closeAt, p.green})
					i = closeAt
				} else {
					result = append(result, documentSyntaxSpan{offset + i, offset + len(line), p.green})
					i, blockComment = len(line), true
				}
				continue
			}
			if line[i] == '"' || line[i] == '\'' || line[i] == '`' {
				quote := line[i]
				j := i + 1
				for j < len(line) {
					if line[j] == '\\' {
						j = minInt(j+2, len(line))
						continue
					}
					if line[j] == quote {
						j++
						break
					}
					j++
				}
				color := p.green
				if language == workspace.FileLanguageJSON && nextNonJSONWhitespaceByte(line, j) == ':' {
					color = p.blue
				}
				result = append(result, documentSyntaxSpan{offset + i, offset + j, color})
				i = j
				continue
			}
			if isASCIIOrUnicodeDigitAt(line, i) {
				j := i
				for j < len(line) {
					r, size := utf8.DecodeRuneInString(line[j:])
					if !unicode.IsDigit(r) && !(r < utf8.RuneSelf && strings.ContainsRune("._xXaAbBcCdDeEfF", r)) {
						break
					}
					j += size
				}
				result = append(result, documentSyntaxSpan{offset + i, offset + j, p.amber})
				i = j
				continue
			}
			if isIdentifierRuneAt(line, i) || (language == workspace.FileLanguageHTML && inMarkupTag && (line[i] == '-' || line[i] == ':')) ||
				(language == workspace.FileLanguageCSS && line[i] == '-') {
				j := i
				for j < len(line) {
					r, size := utf8.DecodeRuneInString(line[j:])
					extendedName := (language == workspace.FileLanguageHTML && inMarkupTag && (r == '-' || r == ':')) ||
						(language == workspace.FileLanguageCSS && r == '-')
					if !isIdentifierRuneAt(line, j) && !unicode.IsDigit(r) && !extendedName {
						break
					}
					j += size
				}
				word := line[i:j]
				if language == workspace.FileLanguageHTML && inMarkupTag && nextNonSpaceByte(line, j) == '=' {
					result = append(result, documentSyntaxSpan{offset + i, offset + j, p.blue})
				} else if language == workspace.FileLanguageCSS && nextNonSpaceByte(line, j) == ':' {
					result = append(result, documentSyntaxSpan{offset + i, offset + j, p.blue})
				} else if documentKeyword(language, word) {
					result = append(result, documentSyntaxSpan{offset + i, offset + j, p.blue})
				} else if language == workspace.FileLanguageJSON && (word == "true" || word == "false" || word == "null") {
					result = append(result, documentSyntaxSpan{offset + i, offset + j, p.amber})
				}
				i = j
				continue
			}
			i++
		}
		if end == len(text) {
			break
		}
		offset = end + 1
	}
	return result
}

func nextNonSpaceByte(text string, offset int) byte {
	for offset < len(text) && (text[offset] == ' ' || text[offset] == '\t') {
		offset++
	}
	if offset >= len(text) {
		return 0
	}
	return text[offset]
}

func nextNonJSONWhitespaceByte(text string, offset int) byte {
	for offset < len(text) {
		r, size := utf8.DecodeRuneInString(text[offset:])
		if !unicode.IsSpace(r) {
			return text[offset]
		}
		offset += size
	}
	return 0
}

func documentLineCommentStart(line string, at int, language workspace.WorkspaceFileLanguage) bool {
	if strings.HasPrefix(line[at:], "//") && language != workspace.FileLanguageShell && language != workspace.FileLanguagePowerShell && language != workspace.FileLanguageBatch && language != workspace.FileLanguagePython && language != workspace.FileLanguageYAML && language != workspace.FileLanguageProperties && language != workspace.FileLanguageSQL {
		return true
	}
	if language == workspace.FileLanguageSQL && strings.HasPrefix(line[at:], "--") {
		return true
	}
	if strings.HasPrefix(line[at:], "#") && (language == workspace.FileLanguageShell || language == workspace.FileLanguagePowerShell || language == workspace.FileLanguageBatch || language == workspace.FileLanguagePython || language == workspace.FileLanguageYAML || language == workspace.FileLanguageProperties) {
		return true
	}
	return false
}

func supportsCComments(language workspace.WorkspaceFileLanguage) bool {
	return language == workspace.FileLanguageJava || language == workspace.FileLanguageKotlin || language == workspace.FileLanguageGradle ||
		language == workspace.FileLanguageJavaScript || language == workspace.FileLanguageTypeScript || language == workspace.FileLanguageCSS
}

func isASCIIOrUnicodeDigitAt(text string, offset int) bool {
	if offset < 0 || offset >= len(text) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[offset:])
	return unicode.IsDigit(r)
}

func isIdentifierRuneAt(text string, offset int) bool {
	if offset < 0 || offset >= len(text) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[offset:])
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func documentKeyword(language workspace.WorkspaceFileLanguage, word string) bool {
	var words string
	switch language {
	case workspace.FileLanguageJava, workspace.FileLanguageKotlin, workspace.FileLanguageGradle:
		words = " abstract as boolean break byte case catch class const continue default do double else enum extends false final finally float for if implements import in instanceof int interface is long new null object package private protected public return short static super switch this throw throws true try typealias var val void volatile while when fun functor suspend data sealed open override lateinit companion init constructor by where vararg crossinline noinline inline infix external operator tailrec reified actual expect"
	case workspace.FileLanguageJavaScript, workspace.FileLanguageTypeScript:
		words = "async await break case catch class const continue default delete do else export extends false finally for from function if import in instanceof let new null return static switch this throw true try typeof undefined var while yield abstract any boolean declare enum implements interface keyof never number private protected public readonly string type unknown"
	case workspace.FileLanguagePython:
		words = "False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case"
	case workspace.FileLanguageSQL:
		word = strings.ToLower(word)
		words = "select from where join left right inner outer on insert into update delete create alter drop table values set and or not null as group by order having limit union distinct"
	case workspace.FileLanguageShell:
		words = "if then else elif fi for in do done case esac function while export local readonly"
	case workspace.FileLanguagePowerShell:
		word = strings.ToLower(word)
		words = "function param if else elseif foreach for while switch return try catch finally throw begin process end true false null"
	case workspace.FileLanguageBatch:
		word = strings.ToLower(word)
		words = "echo set setlocal endlocal if else for in do goto call exit pause shift not exist defined equ neq lss leq gtr geq"
	case workspace.FileLanguageYAML:
		word = strings.ToLower(word)
		words = "true false null yes no on off"
	default:
		return false
	}
	for _, candidate := range strings.Fields(words) {
		if candidate == word {
			return true
		}
	}
	return false
}

func markdownDocumentFindBlocks(source string) []documentFindBlock {
	return markdownPartFindBlocks(source, editor.Parse(source).Parts)
}

func markdownPartFindBlocks(source string, parts []editor.Part) []documentFindBlock {
	blocks := make([]documentFindBlock, 0)
	lineSize := float32(24)
	for _, part := range parts {
		scroll := float32(strings.Count(source[:minInt(part.Range.Start, len(source))], "\n")) * lineSize
		switch part.Kind {
		case editor.PartCode:
			code := part.Source
			if fence := editor.FindFence(part.Source); fence != nil {
				code = fence.Code(part.Source)
			}
			blocks = append(blocks, documentFindBlock{key: markdownCodeFindKey(part.ID), text: code, scrollY: scroll, lineSize: lineSize})
		case editor.PartTable:
			if len(part.Nodes) > 0 {
				for row, cells := range part.Nodes[0].Children {
					for column, cell := range cells.Children {
						blocks = append(blocks, documentFindBlock{key: markdownTableFindKey(part.ID, row, column), text: markdownInlineSearchText(cell.Children),
							scrollY: scroll + float32(row)*csvPreviewRowHeight, lineSize: lineSize})
					}
				}
			}
		case editor.PartOriginal:
			blocks = append(blocks, documentFindBlock{key: markdownOriginalFindKey(part.ID), text: markdownReadonlyOriginalSource(source, part), scrollY: scroll, lineSize: lineSize})
		case editor.PartRich, editor.PartTasks, editor.PartImage:
			for _, node := range part.Nodes {
				appendMarkdownNodeFindBlocks(&blocks, source, part.ID, node, lineSize)
			}
		}
	}
	return blocks
}

func appendMarkdownNodeFindBlocks(blocks *[]documentFindBlock, source string, partID uint64, node editor.Node, lineSize float32) {
	if node.Kind == "paragraph" || node.Kind == "heading" || node.Kind == "task-item" || node.Kind == "list-item" {
		text := markdownInlineSearchText(node.Children)
		if text != "" {
			lineOffset := strings.Count(source[:minInt(node.Range.Start, len(source))], "\n")
			*blocks = append(*blocks, documentFindBlock{key: markdownInlineFindKey(partID, node.Range), text: text,
				scrollY: float32(lineOffset) * lineSize, lineSize: lineSize})
		}
		if node.Kind == "paragraph" || node.Kind == "heading" || node.Kind == "task-item" || node.Kind == "list-item" {
			return
		}
	}
	for _, child := range node.Children {
		appendMarkdownNodeFindBlocks(blocks, source, partID, child, lineSize)
	}
}

func markdownInlineSearchText(nodes []editor.Node) string {
	var result strings.Builder
	var walk func([]editor.Node)
	walk = func(items []editor.Node) {
		for _, node := range items {
			switch node.Kind {
			case "image":
				continue
			case "link":
				if len(node.Children) > 0 {
					walk(node.Children)
				} else {
					result.WriteString(node.Text)
				}
			case "code":
				if node.Text != "" {
					result.WriteString(node.Text)
				} else {
					walk(node.Children)
				}
			case "text":
				result.WriteString(node.Text)
				if node.Attributes["softLineBreak"] == "true" || node.Attributes["hardLineBreak"] == "true" {
					result.WriteByte('\n')
				}
			default:
				if node.Text != "" && len(node.Children) == 0 {
					result.WriteString(node.Text)
				}
				walk(node.Children)
			}
		}
	}
	walk(nodes)
	return result.String()
}

func markdownInlineFindKey(partID uint64, span editor.SourceRange) string {
	return fmt.Sprintf("markdown-inline:%d:%d:%d", partID, span.Start, span.End)
}

func markdownCodeFindKey(partID uint64) string { return fmt.Sprintf("markdown-code:%d", partID) }
func markdownTableFindKey(partID uint64, row, column int) string {
	return fmt.Sprintf("markdown-table:%d:%d:%d", partID, row, column)
}

func renderMarkdownCodeSearch(c *ui.Context, f *documentFindState, p palette, partID uint64, content string, zoom float32) {
	key := markdownCodeFindKey(partID)
	offset := 0
	for offset < len(content) {
		end := strings.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content)
		} else {
			end += offset
		}
		lineEnd := end
		if lineEnd > offset && content[lineEnd-1] == '\r' {
			lineEnd--
		}
		line := content[offset:lineEnd]
		spans := f.spans(key, line, offset, p.text, nil)
		if len(spans) == 0 {
			spans = []ui.Span{{Text: " "}}
		}
		e := ui.RichText(c, spans...).FillWidth().Font("Consolas, monospace").FontSize(scaleMarkdownSize(14, zoom)).LineHeight(1.5).NoWrap().Selectable()
		if f.shouldRevealRange(key, offset, maxInt(offset+1, lineEnd)) {
			e.ScrollIntoView()
		}
		if end == len(content) {
			break
		}
		offset = end + 1
	}
}
