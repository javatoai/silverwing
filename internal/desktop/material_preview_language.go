package desktop

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"silverwing-go/internal/filelanguage"

	codeeditor "github.com/egoist/mygo/ui/editor"
)

// workspaceFilePreviewLanguage adds only the languages missing from MyGO's
// workspace preview mapping. The editor, buffer and source remain MyGO's.
func workspaceFilePreviewLanguage(filename string, language filelanguage.Language) *codeeditor.Language {
	switch language {
	case filelanguage.Gradle:
		return workspacePreviewGradleLanguage
	case filelanguage.Properties:
		return workspacePreviewPropertiesLanguage
	case filelanguage.PowerShell:
		return workspacePreviewPowerShellLanguage
	case filelanguage.Batch:
		return workspacePreviewBatchLanguage
	}
	if native := codeeditor.LanguageFor(filename); native != nil {
		return native
	}
	// The typed result can still identify a language when a preview label has
	// no filename extension. Known filenames always retain the SDK mapping.
	switch language {
	case filelanguage.Java:
		return codeeditor.LanguageNamed("Java")
	case filelanguage.Kotlin:
		return codeeditor.LanguageNamed("Kotlin")
	case filelanguage.HTML:
		return codeeditor.LanguageNamed("HTML")
	case filelanguage.CSS:
		return codeeditor.LanguageNamed("CSS")
	case filelanguage.JavaScript:
		return codeeditor.LanguageNamed("JavaScript")
	case filelanguage.TypeScript:
		return codeeditor.LanguageNamed("TypeScript")
	case filelanguage.JSON:
		return codeeditor.LanguageNamed("JSON")
	case filelanguage.YAML:
		return codeeditor.LanguageNamed("YAML")
	case filelanguage.SQL:
		return codeeditor.LanguageNamed("SQL")
	case filelanguage.Python:
		return codeeditor.LanguageNamed("Python")
	case filelanguage.Shell:
		return codeeditor.LanguageNamed("Shell")
	case filelanguage.Markdown:
		return codeeditor.LanguageNamed("Markdown")
	default:
		return nil
	}
}

// These small tokenizers mirror WorkspaceSyntaxHighlighting.kt's lexical
// rules. They supply the native editor's UTF-8 byte tokens, not a new parser.
var (
	workspacePreviewGradleLanguage = &codeeditor.Language{
		Name: "Gradle", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Tokenizer: workspacePreviewCodeTokenizer{
			keywords:    workspacePreviewWords("as break class continue do else false for fun if in interface is null object package return super this throw true try typealias val var when while by catch constructor delegate dynamic field file finally get import init param property receiver set setparam where plugins dependencies implementation testImplementation repositories mavenCentral version group tasks"),
			lineComment: "//", blockOpen: "/*", blockClose: "*/",
		},
	}
	workspacePreviewPropertiesLanguage = &codeeditor.Language{
		Name: "Properties", LineComment: "#", Tokenizer: codeeditor.TokenizerFunc(workspacePreviewPropertiesTokens),
	}
	workspacePreviewPowerShellLanguage = &codeeditor.Language{
		Name: "PowerShell", LineComment: "#", BlockComment: [2]string{"<#", "#>"},
		Tokenizer: workspacePreviewCodeTokenizer{
			keywords:    workspacePreviewWords("function param if else elseif foreach for while switch return try catch finally throw begin process end true false null"),
			lineComment: "#", blockOpen: "<#", blockClose: "#>", powerShell: true,
		},
	}
	workspacePreviewBatchLanguage = &codeeditor.Language{
		Name: "Batch", LineComment: "rem",
		Tokenizer: workspacePreviewCodeTokenizer{
			keywords: workspacePreviewWords("echo set setlocal endlocal if else for in do goto call exit pause shift not exist defined equ neq lss leq gtr geq"), batch: true,
		},
	}
	workspacePreviewBatchVariables = regexp.MustCompile(`%[^%\r\n]+%|%[0-9*]|![^!\r\n]+!`)
)

func workspacePreviewWords(words string) map[string]bool {
	result := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		result[word] = true
	}
	return result
}

type workspacePreviewTokenSpan struct {
	start, end int
	kind       codeeditor.TokenKind
}

func workspacePreviewAppendTokens(line string, spans []workspacePreviewTokenSpan, tokens []codeeditor.Token) []codeeditor.Token {
	initial := len(tokens)
	appendRun := func(end int, kind codeeditor.TokenKind) {
		if len(tokens) > initial && tokens[len(tokens)-1].Kind == kind {
			tokens[len(tokens)-1].End = end
		} else {
			tokens = append(tokens, codeeditor.Token{End: end, Kind: kind})
		}
	}
	end := 0
	for _, span := range spans {
		if span.start < end || span.end <= span.start {
			continue
		}
		if span.start > end {
			appendRun(span.start, codeeditor.TokenText)
		}
		appendRun(span.end, span.kind)
		end = span.end
	}
	if end < len(line) {
		appendRun(len(line), codeeditor.TokenText)
	}
	return tokens
}

func workspacePreviewPropertiesTokens(line string, _ uint64, tokens []codeeditor.Token) ([]codeeditor.Token, uint64) {
	start := workspacePreviewNonSpace(line, 0)
	if start == len(line) {
		return workspacePreviewAppendTokens(line, nil, tokens), 0
	}
	spans := []workspacePreviewTokenSpan{}
	if line[start] == '#' || line[start] == '!' {
		spans = append(spans, workspacePreviewTokenSpan{start, len(line), codeeditor.TokenComment})
	} else {
		delimiter, escaped := -1, false
		for i, r := range line {
			if !escaped && (r == '=' || r == ':') {
				delimiter = i
				break
			}
			escaped = !escaped && r == '\\'
			if r != '\\' {
				escaped = false
			}
		}
		if delimiter < 0 {
			spans = append(spans, workspacePreviewTokenSpan{start, len(line), codeeditor.TokenProperty})
		} else {
			if delimiter > start {
				spans = append(spans, workspacePreviewTokenSpan{start, delimiter, codeeditor.TokenProperty})
			}
			if value := workspacePreviewNonSpace(line, delimiter+1); value < len(line) {
				spans = append(spans, workspacePreviewTokenSpan{value, len(line), codeeditor.TokenString})
			}
		}
	}
	return workspacePreviewAppendTokens(line, spans, tokens), 0
}

type workspacePreviewCodeTokenizer struct {
	keywords                           map[string]bool
	lineComment, blockOpen, blockClose string
	powerShell, batch                  bool
}

const (
	workspacePreviewBlockState uint64 = iota + 1
	workspacePreviewSingleQuoteState
	workspacePreviewDoubleQuoteState
	workspacePreviewVariableState
)

func (t workspacePreviewCodeTokenizer) Tokenize(line string, state uint64, tokens []codeeditor.Token) ([]codeeditor.Token, uint64) {
	spans := []workspacePreviewTokenSpan{}
	if t.batch {
		state = 0
		start := workspacePreviewNonSpace(line, 0)
		trimmed := strings.ToLower(strings.TrimPrefix(line[start:], "@"))
		if strings.HasPrefix(trimmed, "::") || trimmed == "rem" || strings.HasPrefix(trimmed, "rem ") || strings.HasPrefix(trimmed, "rem\t") {
			return workspacePreviewAppendTokens(line, []workspacePreviewTokenSpan{{start, len(line), codeeditor.TokenComment}}, tokens), 0
		}
	}
	for i := 0; i < len(line); {
		start := i
		kind := codeeditor.TokenText
		r, size := utf8.DecodeRuneInString(line[i:])
		switch {
		case state == workspacePreviewBlockState:
			kind = codeeditor.TokenComment
			if close := strings.Index(line[i:], t.blockClose); close >= 0 {
				i += close + len(t.blockClose)
				state = 0
			} else {
				i = len(line)
			}
		case state == workspacePreviewSingleQuoteState || state == workspacePreviewDoubleQuoteState:
			kind = codeeditor.TokenString
			i, state = t.quotedEnd(line, i, state, false)
		case state == workspacePreviewVariableState:
			kind = codeeditor.TokenProperty
			if close := strings.IndexByte(line[i:], '}'); close >= 0 {
				i += close + 1
				state = 0
			} else {
				i = len(line)
			}
		case t.lineComment != "" && strings.HasPrefix(line[i:], t.lineComment):
			kind, i = codeeditor.TokenComment, len(line)
		case t.blockOpen != "" && strings.HasPrefix(line[i:], t.blockOpen):
			kind, i, state = codeeditor.TokenComment, i+len(t.blockOpen), workspacePreviewBlockState
			if close := strings.Index(line[i:], t.blockClose); close >= 0 {
				i += close + len(t.blockClose)
				state = 0
			} else {
				i = len(line)
			}
		case r == '\'' || r == '"':
			kind = codeeditor.TokenString
			state = workspacePreviewSingleQuoteState
			if r == '"' {
				state = workspacePreviewDoubleQuoteState
			}
			i, state = t.quotedEnd(line, i, state, true)
		case t.powerShell && r == '$':
			kind, i = codeeditor.TokenProperty, i+1
			if i < len(line) {
				next, nextSize := utf8.DecodeRuneInString(line[i:])
				switch {
				case next == '{':
					if close := strings.IndexByte(line[i+1:], '}'); close >= 0 {
						i += close + 2
					} else {
						i, state = len(line), workspacePreviewVariableState
					}
				case workspacePreviewIdentifierStart(next):
					i = workspacePreviewIdentifierEnd(line, i)
				case unicode.IsDigit(next) || strings.ContainsRune("?@*#-$!", next):
					i += nextSize
				}
			}
		case !t.batch && r == '@' && i+1 < len(line) && workspacePreviewIdentifierStartRune(line[i+1:]):
			kind, i = codeeditor.TokenMeta, workspacePreviewIdentifierEnd(line, i+1)
		case unicode.IsDigit(r):
			kind, i = codeeditor.TokenNumber, i+size
			for i < len(line) {
				next, n := utf8.DecodeRuneInString(line[i:])
				if !unicode.IsDigit(next) && !strings.ContainsRune(".xXaAbBcCdDeEfF_", next) {
					break
				}
				i += n
			}
		case workspacePreviewIdentifierStart(r):
			i = workspacePreviewIdentifierEnd(line, i)
			word := line[start:i]
			if t.powerShell || t.batch {
				word = strings.ToLower(word)
			}
			if t.keywords[word] {
				kind = codeeditor.TokenKeyword
			}
		default:
			i += size
		}
		if kind != codeeditor.TokenText {
			spans = append(spans, workspacePreviewTokenSpan{start, i, kind})
		}
	}
	if t.batch {
		for _, match := range workspacePreviewBatchVariables.FindAllStringIndex(line, -1) {
			spans = append(spans, workspacePreviewTokenSpan{match[0], match[1], codeeditor.TokenProperty})
		}
		sort.SliceStable(spans, func(i, j int) bool {
			if spans[i].start != spans[j].start {
				return spans[i].start < spans[j].start
			}
			return spans[i].end > spans[j].end
		})
		state = 0 // Source Batch scans each line independently.
	}
	return workspacePreviewAppendTokens(line, spans, tokens), state
}

func (t workspacePreviewCodeTokenizer) quotedEnd(line string, start int, state uint64, opening bool) (int, uint64) {
	quote, escape := '\'', '\\'
	if state == workspacePreviewDoubleQuoteState {
		quote = '"'
	}
	if t.powerShell {
		escape = '`'
		if quote == '\'' {
			escape = 0
		}
	} else if t.batch {
		escape = 0
	}
	i := start
	if opening {
		i++
	}
	for i < len(line) {
		r, size := utf8.DecodeRuneInString(line[i:])
		i += size
		if r == escape {
			if i < len(line) {
				_, size = utf8.DecodeRuneInString(line[i:])
				i += size
			}
		} else if r == quote {
			return i, 0
		}
	}
	return len(line), state
}

func workspacePreviewIdentifierStart(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) }

func workspacePreviewIdentifierStartRune(text string) bool {
	r, _ := utf8.DecodeRuneInString(text)
	return workspacePreviewIdentifierStart(r)
}

func workspacePreviewIdentifierEnd(line string, start int) int {
	_, size := utf8.DecodeRuneInString(line[start:])
	i := start + size
	for i < len(line) {
		r, size := utf8.DecodeRuneInString(line[i:])
		if !workspacePreviewIdentifierStart(r) && !unicode.IsDigit(r) {
			break
		}
		i += size
	}
	return i
}

func workspacePreviewNonSpace(line string, start int) int {
	for i, r := range line[start:] {
		if !unicode.IsSpace(r) {
			return start + i
		}
	}
	return len(line)
}
