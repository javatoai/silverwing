package desktop

import (
	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/filelanguage"
)

const textEditorFontSize float32 = 13
const textEditorLineHeight float32 = 20.0 / textEditorFontSize

// TextEditor is the shared native source editor. Its owner keeps the State and
// Buffer: rendering and successful saves never replace their navigation,
// selections, composition or undo history. Options remain the owner's, with a
// common font, line height and palette applied on each frame.
//
// A supplied theme lets a projection add its own token marks to the shared
// palette. Buffer offsets, including selections, are UTF-8 byte offsets.
func TextEditor(c *ui.Context, p palette, state *codeeditor.State, theme ...*codeeditor.Theme) ui.Element {
	if state.Buffer != nil {
		language := state.Buffer.Language()
		if language != nil && language.Tokenizer != nil && language.Highlighter == nil {
			state.Buffer.SetLanguage(textEditorBackgroundLanguage(language))
		}
	}
	if state.Font.Family == "" {
		state.Font.Family = "monospace"
	}
	if state.Font.Size == 0 {
		state.Font.Size = textEditorFontSize
	}
	if state.LineHeight == 0 {
		state.LineHeight = textEditorLineHeight
	}
	state.Theme = textEditorTheme(p)
	if len(theme) > 0 && theme[0] != nil {
		state.Theme = theme[0]
	}
	return codeeditor.View(c, state)
}

func textEditorBackgroundLanguage(language *codeeditor.Language) *codeeditor.Language {
	if language == nil || language.Tokenizer == nil || language.Highlighter != nil {
		return language
	}
	copy := *language
	copy.Highlighter = func(snapshot codeeditor.Snapshot, invalidate func()) codeeditor.Highlighter {
		return codeeditor.NewBackgroundTokenHighlighter(copy.Tokenizer, snapshot, invalidate)
	}
	return &copy
}

// textEditorLanguage keeps ordinary files and Git previews on the same language
// mapping. Unknown extensions deliberately have no highlighter.
func textEditorLanguage(filename string) *codeeditor.Language {
	return workspaceFilePreviewLanguage(filename, filelanguage.SourceEditorOverride(filename))
}

// textEditorTheme borrows Mayoi's neutral reading surface and syntax colors.
// Selection, carets and focus keep Silverwing's application blue.
func textEditorTheme(p palette) *codeeditor.Theme {
	dark := p.bg.R < 128
	theme := &codeeditor.Theme{
		Background: ui.Hex("#fcfcfd"), Text: ui.Hex("#2a2a33"),
		LineNumber: ui.Hex("#858591"), ActiveLineNumber: ui.Hex("#4a4a55"),
		ActiveLine: ui.RGBA(0, 0, 0, .03),
		Selection:  p.blue.Alpha(.22), InactiveSelection: p.blue.Alpha(.11), Cursor: p.blue,
		IndentGuide: ui.RGBA(0, 0, 0, .09), ActiveIndentGuide: ui.RGBA(0, 0, 0, .24),
		WrapGuide: ui.RGBA(0, 0, 0, .07), Whitespace: ui.Hex("#93939f"),
		MatchingBracket: ui.RGBA(0, 0, 0, .28), Occurrence: ui.RGBA(0, 0, 0, .065),
		Match: ui.RGBA(230, 160, 40, .22), CurrentMatch: ui.RGBA(230, 160, 40, .42),
		Added: p.green, Deleted: p.danger, Modified: p.blue,
	}
	keyword, typ, function := "#7c3bd6", "#0a708b", "#2a5bd7"
	str, number, comment := "#367b24", "#b24d16", "#6c6c79"
	operator, punctuation, tag, attribute := "#555a6b", "#646879", "#c8264f", "#975e0a"
	if dark {
		theme.Background, theme.Text = ui.Hex("#18181d"), ui.Hex("#d6d6de")
		theme.LineNumber, theme.ActiveLineNumber = ui.Hex("#858592"), ui.Hex("#b9b9c6")
		theme.ActiveLine = ui.RGBA(255, 255, 255, .035)
		theme.Selection, theme.InactiveSelection = p.blue.Alpha(.28), p.blue.Alpha(.13)
		theme.IndentGuide, theme.ActiveIndentGuide = ui.RGBA(255, 255, 255, .08), ui.RGBA(255, 255, 255, .22)
		theme.WrapGuide, theme.Whitespace = ui.RGBA(255, 255, 255, .065), ui.Hex("#8c8c99")
		theme.MatchingBracket, theme.Occurrence = ui.RGBA(255, 255, 255, .3), ui.RGBA(255, 255, 255, .075)
		theme.Match, theme.CurrentMatch = ui.RGBA(232, 184, 109, .2), ui.RGBA(232, 184, 109, .38)
		keyword, typ, function = "#b99cf7", "#7dcfe0", "#8fb4ff"
		str, number, comment = "#a9d48f", "#f2a679", "#8b8b98"
		operator, punctuation, tag, attribute = "#a0a5b8", "#8a8e9e", "#f590a8", "#e8c482"
	}
	for kind, color := range map[codeeditor.TokenKind]string{
		codeeditor.TokenKeyword: keyword, codeeditor.TokenType: typ,
		codeeditor.TokenFunction: function, codeeditor.TokenString: str,
		codeeditor.TokenNumber: number, codeeditor.TokenConstant: number,
		codeeditor.TokenComment: comment, codeeditor.TokenOperator: operator,
		codeeditor.TokenPunctuation: punctuation, codeeditor.TokenTag: tag,
		codeeditor.TokenAttribute: attribute, codeeditor.TokenProperty: function,
		codeeditor.TokenVariable: "", codeeditor.TokenEscape: typ,
		codeeditor.TokenMeta: keyword, codeeditor.TokenRegexp: str,
		codeeditor.TokenHeading: function, codeeditor.TokenCode: str,
		codeeditor.TokenLink: function,
	} {
		if color != "" {
			theme.Tokens[kind].Color = ui.Hex(color)
		}
	}
	theme.Tokens[codeeditor.TokenText].Color = theme.Text
	theme.Tokens[codeeditor.TokenVariable].Color = theme.Text
	theme.Tokens[codeeditor.TokenInserted].Color = theme.Added
	theme.Tokens[codeeditor.TokenDeleted].Color = ui.Hex(tag)
	theme.Tokens[codeeditor.TokenInvalid].Color = ui.Hex(tag)
	theme.Tokens[codeeditor.TokenHeading].Bold = true
	theme.Tokens[codeeditor.TokenStrong].Bold = true
	theme.Tokens[codeeditor.TokenEmphasis].Italic = true
	return theme
}
