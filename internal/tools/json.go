package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxTextBytes = 2 * 1024 * 1024
const MaxJSONDepth = 256

func checkSize(text string) error {
	if len([]byte(text)) > MaxTextBytes {
		return errors.New("单个文本输入不能超过 2 MiB")
	}
	return nil
}

type JSONDocument struct {
	Formatted string           `json:"formatted"`
	Compact   string           `json:"compact"`
	Nodes     []JSONSourceNode `json:"nodes"`
}
type JSONSourceNode struct {
	Pointer     string `json:"pointer"`
	StartOffset int    `json:"startOffset"`
	EndOffset   int    `json:"endOffset"`
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
	IsContainer bool   `json:"isContainer"`
}
type JSONError struct {
	Message      string
	Line, Column int
}

func (e *JSONError) Error() string {
	return fmt.Sprintf("%s（第%d行，第%d列）", e.Message, e.Line, e.Column)
}

type orderedJSON struct {
	value   any
	entries []jsonEntry
	array   []*orderedJSON
	object  bool
}
type jsonEntry struct {
	key   string
	value *orderedJSON
}

func ParseJSON(source string) (JSONDocument, error) {
	if err := checkSize(source); err != nil {
		return JSONDocument{}, err
	}
	normalized, trailingClosings := jsonToolInputWithTrailingCommas(source)
	decoder := json.NewDecoder(strings.NewReader(normalized))
	decoder.UseNumber()
	var nodes []JSONSourceNode
	root, err := readJSON(decoder, source, 0, "", &nodes)
	if err == nil {
		if _, e := decoder.Token(); e != io.EOF {
			if e == nil {
				e = errors.New("JSON 结束后存在多余内容")
			}
			err = e
		}
	}
	var formatted string
	if err == nil {
		formatted, err = formatJSONTool(normalized, trailingClosings)
	}
	if err != nil {
		offset := int(decoder.InputOffset())
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			offset = int(syntax.Offset) - 1
		}
		line, column := location(source, offset)
		return JSONDocument{}, &JSONError{err.Error(), line, column}
	}
	indexJSONNodes(source, nodes)
	return JSONDocument{Formatted: formatted, Compact: root.render(false, 0), Nodes: nodes}, nil
}
func readJSON(d *json.Decoder, source string, depth int, pointer string, nodes *[]JSONSourceNode) (*orderedJSON, error) {
	start := int(d.InputOffset())
	for start < len(source) && strings.ContainsRune(" \r\n\t,:", rune(source[start])) {
		start++
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	nodeIndex := len(*nodes)
	*nodes = append(*nodes, JSONSourceNode{Pointer: pointer, StartOffset: start})
	n := &orderedJSON{}
	if delimiter, ok := token.(json.Delim); ok {
		if depth >= MaxJSONDepth {
			return nil, fmt.Errorf("JSON 嵌套不能超过 %d 层", MaxJSONDepth)
		}
		(*nodes)[nodeIndex].IsContainer = true
		switch delimiter {
		case '{':
			n.object = true
			indexes := map[string]int{}
			for d.More() {
				keyToken, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("对象的键必须是字符串")
				}
				child, e := readJSON(d, source, depth+1, pointer+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), nodes)
				if e != nil {
					return nil, e
				}
				if i, exists := indexes[key]; exists {
					n.entries[i].value = child
				} else {
					indexes[key] = len(n.entries)
					n.entries = append(n.entries, jsonEntry{key, child})
				}
			}
		case '[':
			n.array = make([]*orderedJSON, 0)
			for d.More() {
				child, e := readJSON(d, source, depth+1, fmt.Sprintf("%s/%d", pointer, len(n.array)), nodes)
				if e != nil {
					return nil, e
				}
				n.array = append(n.array, child)
			}
		default:
			return nil, errors.New("JSON 值无效")
		}
		if _, err = d.Token(); err != nil {
			return nil, err
		}
	} else {
		n.value = token
	}
	end := int(d.InputOffset())
	(*nodes)[nodeIndex].EndOffset = end
	return n, nil
}

// JSON scanner offsets use Kotlin's UTF-16 units. Build the source index once,
// rather than rescanning the entire prefix for every tree node.
func indexJSONNodes(source string, nodes []JSONSourceNode) {
	units := make([]int32, len(source)+1)
	lines := make([]int32, len(source)+1)
	var offset int32
	line := int32(1)
	previousCR := false
	for i := 0; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		for j := i; j < i+size; j++ {
			units[j], lines[j] = offset, line
		}
		if r > 0xffff {
			offset += 2
		} else {
			offset++
		}
		if r == '\r' || r == '\n' {
			if r != '\n' || !previousCR {
				line++
			}
		}
		previousCR = r == '\r'
		i += size
		units[i], lines[i] = offset, line
	}
	lines[len(source)] = line
	for i := range nodes {
		start, end := nodes[i].StartOffset, nodes[i].EndOffset
		nodes[i].StartLine, nodes[i].EndLine = int(lines[start]), int(lines[max(start, end-1)])
		nodes[i].StartOffset, nodes[i].EndOffset = int(units[start]), int(units[end])
	}
}

func UTF16Length(text string) int {
	n := 0
	for _, r := range text {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Offsets inside a surrogate pair resolve to the beginning of that character.
func UTF16OffsetToByte(text string, offset int) int {
	if offset <= 0 {
		return 0
	}
	units := 0
	for i, r := range text {
		if units >= offset {
			return i
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > offset {
			return i
		}
		units += width
	}
	return len(text)
}
func quoteJSON(value string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(value)
	return strings.TrimSuffix(b.String(), "\n")
}
func (n *orderedJSON) render(pretty bool, level int) string {
	if n.object || n.array != nil || n.value == nil && len(n.entries) == 0 {
		// Empty arrays need a marker since a zero-length slice can be nil.
		if n.object {
			if len(n.entries) == 0 {
				return "{}"
			}
		} else if n.array == nil {
			return "null"
		}
		open, close := "[", "]"
		var children []string
		if n.object {
			open, close = "{", "}"
			for _, entry := range n.entries {
				sep := ":"
				if pretty {
					sep = ": "
				}
				children = append(children, quoteJSON(entry.key)+sep+entry.value.render(pretty, level+1))
			}
		} else {
			for _, entry := range n.array {
				children = append(children, entry.render(pretty, level+1))
			}
		}
		if len(children) == 0 {
			return open + close
		}
		if !pretty {
			return open + strings.Join(children, ",") + close
		}
		indent := strings.Repeat("    ", level+1)
		return open + "\n" + indent + strings.Join(children, ",\n"+indent) + "\n" + strings.Repeat("    ", level) + close
	}
	switch v := n.value.(type) {
	case string:
		return quoteJSON(v)
	case json.Number:
		return string(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return "null"
}
func EscapeJSON(source string) (string, error) {
	if err := checkSize(source); err != nil {
		return "", err
	}
	quoted := quoteJSON(source)
	return quoted[1 : len(quoted)-1], nil
}
func UnescapeJSON(source string) (string, error) {
	if err := checkSize(source); err != nil {
		return "", err
	}
	var result string
	if err := json.Unmarshal([]byte(`"`+source+`"`), &result); err != nil {
		offset := len(source)
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			// SyntaxError is one based and includes the supplied opening quote.
			offset = int(syntax.Offset) - 2
		}
		line, column := location(source, offset)
		return "", &JSONError{Message: err.Error(), Line: line, Column: column}
	}
	return result, nil
}

// JSONNodeValue verifies the requested UTF-16 range against this full source's
// parsed nodes before rendering. Object order and number lexemes stay intact.
func JSONNodeValue(source string, start, end int) (string, error) {
	doc, err := ParseJSON(source)
	if err != nil {
		return "", err
	}
	matched := false
	for _, node := range doc.Nodes {
		if node.StartOffset == start && node.EndOffset == end {
			matched = true
			break
		}
	}
	if !matched {
		return "", errors.New("JSON 节点范围已失效，请重新选择")
	}
	fragment := source[UTF16OffsetToByte(source, start):UTF16OffsetToByte(source, end)]
	if len(fragment) > 0 && fragment[0] == '"' {
		var text string
		if err := json.Unmarshal([]byte(fragment), &text); err != nil {
			return "", err
		}
		return text, nil
	}
	node, err := ParseJSON(fragment)
	return node.Compact, err
}
func location(source string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line, column := 1, 1
	for i, r := range source[:offset] {
		if r == '\r' || r == '\n' {
			if r == '\r' || i == 0 || source[i-1] != '\r' {
				line++
			}
			column = 1
		} else {
			if r > 0xffff {
				column += 2
			} else {
				column++
			}
		}
	}
	return line, column
}
