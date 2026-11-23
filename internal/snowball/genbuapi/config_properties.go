package genbuapi

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const propertyWhitespace = " \t\f"

type configPropertyRecord struct {
	key    string
	value  string
	prefix string
	ending string
	start  int
	end    int
}

func editConfigProperties(content string, edits []PropertyEdit) (string, []ConfigEditChange, error) {
	if !utf8.ValidString(content) {
		return "", nil, fmt.Errorf("配置文件不是有效的 UTF-8")
	}
	wanted := make(map[string]bool, len(edits))
	for _, edit := range edits {
		if edit.Key == "" || !utf8.ValidString(edit.Key) || !utf8.ValidString(edit.Value) {
			return "", nil, fmt.Errorf("配置键或值无效")
		}
		switch edit.Operation {
		case "set", "add", "unset":
		default:
			return "", nil, fmt.Errorf("不支持的配置键操作")
		}
		if wanted[edit.Key] {
			return "", nil, fmt.Errorf("配置键 %q 收到多项操作", edit.Key)
		}
		wanted[edit.Key] = true
	}

	body := strings.TrimPrefix(content, "\uFEFF")
	records, newline, dangling, err := readConfigPropertyRecords(body, wanted)
	if err != nil {
		return "", nil, err
	}
	byKey := make(map[string]configPropertyRecord, len(records))
	for _, record := range records {
		if _, exists := byKey[record.key]; exists {
			return "", nil, fmt.Errorf("配置键 %q 在文件中重复", record.key)
		}
		byKey[record.key] = record
	}

	replacements := make(map[string]string, len(edits))
	var additions []string
	var changes []ConfigEditChange
	for _, edit := range edits {
		record, exists := byKey[edit.Key]
		switch edit.Operation {
		case "add":
			if exists {
				return "", nil, fmt.Errorf("不能新增已存在的配置键 %q", edit.Key)
			}
			additions = append(additions, encodeConfigProperty(edit.Key, true)+"="+encodeConfigProperty(edit.Value, false))
		case "set", "unset":
			if !exists {
				return "", nil, fmt.Errorf("配置键 %q 不存在", edit.Key)
			}
			replacement := ""
			if edit.Operation == "set" {
				oldValue, err := decodeConfigProperty(record.value)
				if err != nil {
					return "", nil, fmt.Errorf("配置键 %q 的值包含无效转义", edit.Key)
				}
				if oldValue == edit.Value {
					continue
				}
				replacement = record.prefix + encodeConfigProperty(edit.Value, false) + record.ending
			}
			replacements[edit.Key] = replacement
			if record.end == len(body) {
				dangling = false
			}
		}
		changes = append(changes, ConfigEditChange{Operation: edit.Operation, Key: edit.Key})
	}
	if len(changes) == 0 {
		return content, nil, nil
	}

	bom := content[:len(content)-len(body)]
	var output strings.Builder
	output.Grow(len(content))
	output.WriteString(bom)
	start := 0
	for _, record := range records {
		if replacement, changed := replacements[record.key]; changed {
			output.WriteString(body[start:record.start])
			output.WriteString(replacement)
			start = record.end
		}
	}
	output.WriteString(body[start:])
	if len(additions) > 0 {
		written := output.String()[len(bom):]
		if written != "" && !endsInPropertyNewline(written) {
			output.WriteString(newline)
		}
		if dangling {
			// An empty natural line closes an unfinished continuation.
			// Keep a trailing CR from combining with the added LF.
			if newline == "\n" && strings.HasSuffix(output.String(), "\r") {
				output.WriteByte('\r')
			}
			output.WriteString(newline)
		}
		output.WriteString(strings.Join(additions, newline))
		if body == "" || endsInPropertyNewline(body) {
			output.WriteString(newline)
		}
	}
	return output.String(), changes, nil
}

func readConfigPropertyRecords(body string, wanted map[string]bool) ([]configPropertyRecord, string, bool, error) {
	var records []configPropertyRecord
	newline := ""
	dangling := false
	for offset := 0; offset < len(body); {
		start := offset
		first, ending, next := takeConfigPropertyLine(body, offset)
		offset = next
		if newline == "" {
			newline = ending
		}
		line := strings.TrimLeft(first, propertyWhitespace)
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		indent := first[:len(first)-len(line)]
		if continuesConfigProperty(line) {
			var logical strings.Builder
			for continuesConfigProperty(line) {
				logical.WriteString(line[:len(line)-1])
				if offset == len(body) {
					dangling = true
					line = ""
					break
				}
				line, ending, offset = takeConfigPropertyLine(body, offset)
				line = strings.TrimLeft(line, propertyWhitespace)
				if logical.Len() == 0 && (line == "" || line[0] == '#' || line[0] == '!') {
					line = ""
					break
				}
			}
			logical.WriteString(line)
			line = logical.String()
		}
		keyEnd, valueStart := configPropertyBounds(line)
		key, err := decodeConfigProperty(line[:keyEnd])
		if err != nil {
			return nil, "", false, fmt.Errorf("配置文件中的键包含无效转义")
		}
		if !wanted[key] {
			continue
		}
		prefix := indent + line[:valueStart]
		if keyEnd == valueStart {
			prefix += "="
		}
		records = append(records, configPropertyRecord{
			key: key, value: line[valueStart:], prefix: prefix, ending: ending, start: start, end: offset,
		})
	}
	if newline == "" {
		newline = "\n"
	}
	return records, newline, dangling, nil
}

func takeConfigPropertyLine(body string, start int) (line, ending string, next int) {
	length := strings.IndexAny(body[start:], "\r\n")
	if length < 0 {
		return body[start:], "", len(body)
	}
	end := start + length
	next = end + 1
	if body[end] == '\r' && next < len(body) && body[next] == '\n' {
		next++
	}
	return body[start:end], body[end:next], next
}

func continuesConfigProperty(line string) bool {
	last := len(line)
	for last > 0 && line[last-1] == '\\' {
		last--
	}
	return (len(line)-last)%2 == 1
}

func endsInPropertyNewline(text string) bool {
	return strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r")
}

func configPropertyBounds(line string) (keyEnd, valueStart int) {
	escaped := false
	for keyEnd < len(line) {
		character := line[keyEnd]
		if !escaped && strings.ContainsRune("=:"+propertyWhitespace, rune(character)) {
			break
		}
		escaped = character == '\\' && !escaped
		keyEnd++
	}
	separator := false
	for valueStart = keyEnd; valueStart < len(line); valueStart++ {
		character := line[valueStart]
		if strings.ContainsRune(propertyWhitespace, rune(character)) {
			continue
		}
		if !separator && (character == '=' || character == ':') {
			separator = true
			continue
		}
		break
	}
	return keyEnd, valueStart
}

func decodeConfigProperty(text string) (string, error) {
	if !strings.ContainsRune(text, '\\') {
		return text, nil
	}
	var decoded strings.Builder
	for len(text) > 0 {
		slash := strings.IndexByte(text, '\\')
		if slash == -1 {
			decoded.WriteString(text)
			break
		}
		decoded.WriteString(text[:slash])
		text = text[slash+1:]
		if text == "" {
			return "", fmt.Errorf("不完整的配置转义")
		}
		escape := text[0]
		text = text[1:]
		switch escape {
		case 't':
			decoded.WriteByte('\t')
		case 'r':
			decoded.WriteByte('\r')
		case 'n':
			decoded.WriteByte('\n')
		case 'f':
			decoded.WriteByte('\f')
		case 'u':
			character, length, err := decodeConfigPropertyUnicode(text)
			if err != nil {
				return "", err
			}
			decoded.WriteRune(character)
			text = text[length:]
		default:
			decoded.WriteByte(escape)
		}
	}
	return decoded.String(), nil
}

func decodeConfigPropertyUnicode(text string) (rune, int, error) {
	if len(text) < 4 {
		return 0, 0, fmt.Errorf("不完整的 Unicode 配置转义")
	}
	code, err := strconv.ParseUint(text[:4], 16, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("无效的 Unicode 配置转义")
	}
	character := rune(code)
	if !utf16.IsSurrogate(character) {
		return character, 4, nil
	}
	if len(text) >= 10 && text[4:6] == "\\u" {
		low, err := strconv.ParseUint(text[6:10], 16, 16)
		if err == nil {
			if decoded := utf16.DecodeRune(character, rune(low)); decoded != unicode.ReplacementChar {
				return decoded, 10, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("无效的 Unicode 配置转义")
}

func encodeConfigProperty(text string, key bool) string {
	var encoded strings.Builder
	for index, character := range text {
		switch character {
		case '\\':
			encoded.WriteString("\\\\")
		case '\t':
			encoded.WriteString("\\t")
		case '\r':
			encoded.WriteString("\\r")
		case '\n':
			encoded.WriteString("\\n")
		case '\f':
			encoded.WriteString("\\f")
		default:
			if character < ' ' || character == '\u007f' || (key && character == '\uFEFF') {
				fmt.Fprintf(&encoded, "\\u%04X", character)
				continue
			}
			// A leading = or : would be read as a separator after a whitespace-only prefix.
			if (index == 0 && strings.ContainsRune(" =:", character)) ||
				(key && strings.ContainsRune(" =:#!", character)) {
				encoded.WriteByte('\\')
			}
			encoded.WriteRune(character)
		}
	}
	return encoded.String()
}
