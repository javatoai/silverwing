package tools

import (
	"bytes"
	"encoding/json"
	"strings"
)

// This tolerance belongs only to the JSON tool parser. Replacing a trailing
// comma by one ASCII space keeps every byte offset, line and source node range
// aligned with the unchanged editor input. The normal JSON decoder still
// validates values, delimiters and all other syntax.
func normalizeJSONToolTrailingCommas(source string) string {
	normalized, _ := jsonToolInputWithTrailingCommas(source)
	return normalized
}

// Closing ordinals survive json.Indent unchanged, including duplicate keys.
// Record only the containers whose final comma was actually normalized.
func jsonToolInputWithTrailingCommas(source string) (string, []int) {
	var normalized []byte
	var trailingClosings []int
	closingIndex, trailingClose := 0, -1
	quoted, escaped := false, false
	for i := 0; i < len(source); i++ {
		ch := source[i]
		if quoted {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
			continue
		}
		if ch == '}' || ch == ']' {
			if i == trailingClose {
				trailingClosings = append(trailingClosings, closingIndex)
			}
			closingIndex++
			continue
		}
		if ch != ',' {
			continue
		}
		next := i + 1
		for next < len(source) && jsonToolWhitespace(source[next]) {
			next++
		}
		if next == len(source) || source[next] != '}' && source[next] != ']' {
			continue
		}
		previous := i - 1
		for previous >= 0 && jsonToolWhitespace(source[previous]) {
			previous--
		}
		// A leading comma, missing value or second comma must remain invalid.
		if previous < 0 {
			continue
		}
		switch source[previous] {
		case '{', '[', ',', ':':
			continue
		}
		if normalized == nil {
			normalized = []byte(source)
		}
		normalized[i] = ' '
		trailingClose = next
	}
	if normalized == nil {
		return source, nil
	}
	return string(normalized), trailingClosings
}

// Indent preserves raw strings, numeric lexemes, key order and duplicate keys.
// Only whitespace is reformatted; reinstate each recorded comma before the
// closing delimiter's preceding whitespace, outside strings.
func formatJSONTool(normalized string, trailingClosings []int) (string, error) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(normalized), "", "    "); err != nil {
		return "", err
	}
	formatted := strings.TrimRight(indented.String(), " \t\r\n")
	if len(trailingClosings) == 0 {
		return formatted, nil
	}
	var result strings.Builder
	result.Grow(len(formatted) + len(trailingClosings))
	quoted, escaped := false, false
	closingIndex, next, copied := 0, 0, 0
	for i := 0; i < len(formatted) && next < len(trailingClosings); i++ {
		ch := formatted[i]
		if quoted {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
		} else if ch == '}' || ch == ']' {
			if closingIndex == trailingClosings[next] {
				end := i
				for end > copied && jsonToolWhitespace(formatted[end-1]) {
					end--
				}
				result.WriteString(formatted[copied:end])
				result.WriteByte(',')
				copied = end
				next++
			}
			closingIndex++
		}
	}
	result.WriteString(formatted[copied:])
	return result.String(), nil
}

func jsonToolWhitespace(ch byte) bool { return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' }
