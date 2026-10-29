package markdowntext

import "strings"

type Fence struct {
	Language  string `json:"language"`
	BodyStart int    `json:"bodyStart"`
	BodyEnd   int    `json:"bodyEnd"`
	Marker    string `json:"marker"`
}

// FindFence recognizes fenced code blocks and reports offsets relative to the
// supplied part. The final matching closing fence is used, matching the
// existing editor's handling of fence-like source lines inside code.
func FindFence(source string) *Fence {
	lines := scanLines(source)
	if len(lines) == 0 {
		return nil
	}
	line := lines[0]
	index := 0
	for index < len(line.text) && index < 3 && (line.text[index] == ' ' || line.text[index] == '\t') {
		index++
	}
	if index >= len(line.text) || (line.text[index] != '`' && line.text[index] != '~') {
		return nil
	}
	markerChar := line.text[index]
	markerStart := index
	for index < len(line.text) && line.text[index] == markerChar {
		index++
	}
	if index-markerStart < 3 || line.end == line.contentEnd {
		return nil
	}
	marker := line.text[markerStart:index]
	info := strings.TrimSpace(line.text[index:])
	language := info
	if space := strings.IndexByte(language, ' '); space >= 0 {
		language = language[:space]
	}
	bodyStart := line.end
	bodyEnd := len(source)
	for _, candidate := range lines[1:] {
		if closingFence(candidate.text, marker) {
			bodyEnd = candidate.start
		}
	}
	return &Fence{Language: language, BodyStart: bodyStart, BodyEnd: bodyEnd, Marker: marker}
}

func (f Fence) Code(source string) string {
	if f.BodyStart < 0 || f.BodyEnd < f.BodyStart || f.BodyEnd > len(source) {
		return ""
	}
	code := source[f.BodyStart:f.BodyEnd]
	// Remove only the fence separator. Chained trims also consume a real
	// trailing blank line when the document uses CRLF.
	if strings.HasSuffix(code, "\r\n") {
		return code[:len(code)-2]
	}
	if strings.HasSuffix(code, "\n") || strings.HasSuffix(code, "\r") {
		return code[:len(code)-1]
	}
	return code
}

func ReplaceFenceCode(source, code string) string {
	fence := FindFence(source)
	if fence == nil {
		return code
	}
	newline := DetectNewline(source)
	opening := source[:fence.BodyStart]
	closing := source[fence.BodyEnd:]
	bodyFence := longestFenceLine(code, fence.Marker[0])
	if bodyFence >= len(fence.Marker) {
		expanded := strings.Repeat(fence.Marker[:1], bodyFence+1)
		opening = strings.Replace(opening, fence.Marker, expanded, 1)
		closing = strings.Replace(closing, fence.Marker, expanded, 1)
	}
	code = strings.ReplaceAll(strings.ReplaceAll(code, "\r\n", "\n"), "\r", "\n")
	code = strings.ReplaceAll(code, "\n", newline)
	if code != "" && !strings.HasSuffix(code, "\n") && !strings.HasSuffix(code, "\r") {
		code += newline
	}
	return opening + code + closing
}

// DisplaySource normalizes line endings only for the rich renderer. The
// document model continues to own the untouched original source bytes.
func DisplaySource(source string) string {
	return strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n")
}

// RichReplacement applies a rich editor's serialization while retaining the
// edited block's original newline convention and trailing blank lines.
func RichReplacement(original, serialized, documentNewline string) string {
	newlineSource := original
	if !strings.ContainsAny(newlineSource, "\r\n") {
		newlineSource = documentNewline
	}
	newline := DetectNewline(newlineSource)
	trailing := ""
	for i := len(original); i > 0 && (original[i-1] == '\r' || original[i-1] == '\n'); i-- {
		trailing = original[i-1:i] + trailing
	}
	normalized := strings.TrimRight(serialized, "\r\n")
	normalized = strings.ReplaceAll(strings.ReplaceAll(normalized, "\r\n", "\n"), "\r", "\n")
	return strings.ReplaceAll(normalized, "\n", newline) + trailing
}

func closingFence(line, opening string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	leading := len(line) - len(trimmed)
	if leading > 3 || len(trimmed) == 0 || trimmed[0] != opening[0] {
		return false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == opening[0] {
		count++
	}
	return count >= len(opening) && horizontalOnly(trimmed[count:])
}

func longestFenceLine(source string, marker byte) int {
	longest := 0
	for _, line := range scanLines(source) {
		trimmed := strings.TrimLeft(line.text, " \t")
		if len(line.text)-len(trimmed) > 3 {
			continue
		}
		count := 0
		for count < len(trimmed) && trimmed[count] == marker {
			count++
		}
		if count > longest && horizontalOnly(trimmed[count:]) {
			longest = count
		}
	}
	return longest
}

func horizontalOnly(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != ' ' && value[i] != '\t' {
			return false
		}
	}
	return true
}

func DetectNewline(source string) string {
	if strings.Contains(source, "\r\n") {
		return "\r\n"
	}
	if strings.Contains(source, "\r") && !strings.Contains(source, "\n") {
		return "\r"
	}
	return "\n"
}
