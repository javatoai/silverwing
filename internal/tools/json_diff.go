package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	JSONDiffAdded   = "ADDED"
	JSONDiffRemoved = "REMOVED"
	JSONDiffChanged = "CHANGED"

	maxJSONDiffBytes = 8 * 1024 * 1024
)

type JSONDiffEntry struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Left  string `json:"left"`
	Right string `json:"right"`
}

type JSONDiffResult struct {
	Entries []JSONDiffEntry `json:"entries"`
	Added   int             `json:"added"`
	Removed int             `json:"removed"`
	Changed int             `json:"changed"`
}

// DifferencesText uses one line per present value. An empty value marks a
// missing side, while the JSON value null is written as "null".
func (result JSONDiffResult) DifferencesText() string {
	var text strings.Builder
	for _, entry := range result.Entries {
		if entry.Left != "" {
			writeJSONDiffLine(&text, "- ", entry.Path, entry.Left)
		}
		if entry.Right != "" {
			writeJSONDiffLine(&text, "+ ", entry.Path, entry.Right)
		}
	}
	return text.String()
}

func writeJSONDiffLine(text *strings.Builder, marker, path, value string) {
	text.WriteString(marker)
	text.WriteString(path)
	text.WriteString(" = ")
	text.WriteString(value)
	text.WriteByte('\n')
}

// CompareJSON compares JSON values with alphabetical object keys and
// positional array indices. Ignore rules are field names or exact paths.
func CompareJSON(ctx context.Context, left, right, ignore string) (JSONDiffResult, error) {
	l, err := parseJSONDiffSide(ctx, left)
	if err != nil {
		return JSONDiffResult{}, fmt.Errorf("左侧 JSON：%w", err)
	}
	r, err := parseJSONDiffSide(ctx, right)
	if err != nil {
		return JSONDiffResult{}, fmt.Errorf("右侧 JSON：%w", err)
	}
	rules, err := parseJSONDiffIgnores(ctx, ignore)
	if err != nil {
		return JSONDiffResult{}, fmt.Errorf("忽略规则：%w", err)
	}
	diff := jsonDiffWalker{ctx: ctx, rules: rules, result: JSONDiffResult{Entries: []JSONDiffEntry{}}}
	if err := diff.compare([]byte{'$'}, rules.paths, l, r); err != nil {
		return JSONDiffResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return JSONDiffResult{}, err
	}
	return diff.result, nil
}

func parseJSONDiffSide(ctx context.Context, source string) (*orderedJSON, error) {
	if err := checkSize(source); err != nil {
		return nil, err
	}
	if !utf8.ValidString(source) {
		return nil, errors.New("JSON 包含无效的 UTF-8 字符")
	}
	reader := jsonReader{
		ctx:              ctx,
		decoder:          json.NewDecoder(strings.NewReader(source)),
		source:           source,
		rejectDuplicates: true,
	}
	return reader.readDocument()
}

type jsonDiffWalker struct {
	ctx         context.Context
	rules       jsonDiffIgnores
	result      JSONDiffResult
	resultBytes int
}

func (d *jsonDiffWalker) compare(path []byte, ignore *jsonDiffIgnorePath, left, right *orderedJSON) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if ignore.skipsValue() {
		return nil
	}
	switch {
	case left == nil || right == nil:
		return d.add(path, ignore, left, right)
	case left.object && right.object:
		return d.compareObjects(path, ignore, left, right)
	case left.array != nil && right.array != nil:
		for index := range max(len(left.array), len(right.array)) {
			if err := d.compare(appendJSONDiffIndex(path, index), ignore.indexAt(index), jsonDiffArrayAt(left.array, index), jsonDiffArrayAt(right.array, index)); err != nil {
				return err
			}
		}
	case left.object || right.object || left.array != nil || right.array != nil:
		return d.add(path, ignore, left, right)
	case !sameJSONDiffScalar(left.value, right.value):
		return d.add(path, ignore, left, right)
	}
	return nil
}

func jsonDiffArrayAt(values []*orderedJSON, index int) *orderedJSON {
	if index < len(values) {
		return values[index]
	}
	return nil
}

func sortJSONDiffObject(entries []jsonEntry) {
	slices.SortFunc(entries, func(left, right jsonEntry) int {
		return strings.Compare(left.key, right.key)
	})
}

func (d *jsonDiffWalker) compareObjects(path []byte, ignore *jsonDiffIgnorePath, left, right *orderedJSON) error {
	l, r := left.entries, right.entries
	sortJSONDiffObject(l)
	sortJSONDiffObject(r)
	for len(l) > 0 || len(r) > 0 {
		if err := d.ctx.Err(); err != nil {
			return err
		}
		var key string
		var lValue, rValue *orderedJSON
		switch {
		case len(r) == 0 || len(l) > 0 && l[0].key < r[0].key:
			key, lValue = l[0].key, l[0].value
			l = l[1:]
		case len(l) == 0 || r[0].key < l[0].key:
			key, rValue = r[0].key, r[0].value
			r = r[1:]
		default:
			key, lValue, rValue = l[0].key, l[0].value, r[0].value
			l, r = l[1:], r[1:]
		}
		if d.rules.skipField(key) {
			continue
		}
		if err := d.compare(appendJSONDiffField(path, key), ignore.fieldAt(key), lValue, rValue); err != nil {
			return err
		}
	}
	return nil
}

func (d *jsonDiffWalker) add(path []byte, ignore *jsonDiffIgnorePath, left, right *orderedJSON) error {
	entry := JSONDiffEntry{}
	switch {
	case left == nil:
		entry.Kind = JSONDiffAdded
	case right == nil:
		entry.Kind = JSONDiffRemoved
	default:
		entry.Kind = JSONDiffChanged
	}
	// Account for the repeated path, marker, separator and newline per line.
	size := len(path) + len("-  = \n")
	if entry.Kind == JSONDiffChanged {
		size *= 2
	}
	if d.resultBytes+size > maxJSONDiffBytes {
		return errors.New("JSON 差异结果不能超过 8 MiB")
	}
	var err error
	entry.Left, err = d.render(ignore, left)
	if err != nil {
		return err
	}
	entry.Right, err = d.render(ignore, right)
	if err != nil {
		return err
	}
	size += len(entry.Left) + len(entry.Right)
	if d.resultBytes+size > maxJSONDiffBytes {
		return errors.New("JSON 差异结果不能超过 8 MiB")
	}
	d.resultBytes += size
	entry.Path = string(path)
	switch entry.Kind {
	case JSONDiffAdded:
		d.result.Added++
	case JSONDiffRemoved:
		d.result.Removed++
	default:
		d.result.Changed++
	}
	d.result.Entries = append(d.result.Entries, entry)
	return nil
}

func (d *jsonDiffWalker) render(ignore *jsonDiffIgnorePath, value *orderedJSON) (string, error) {
	if value == nil {
		return "", nil
	}
	var text strings.Builder
	if err := d.writeValue(&text, ignore, value); err != nil {
		return "", err
	}
	return text.String(), nil
}

func (d *jsonDiffWalker) writeValue(text *strings.Builder, ignore *jsonDiffIgnorePath, value *orderedJSON) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	switch {
	case value.object:
		sortJSONDiffObject(value.entries)
		start := text.Len()
		text.WriteByte('{')
		for _, entry := range value.entries {
			if err := d.ctx.Err(); err != nil {
				return err
			}
			childIgnore := ignore.fieldAt(entry.key)
			if d.rules.skipField(entry.key) || childIgnore.skipsValue() {
				continue
			}
			if text.Len() > start+1 {
				text.WriteByte(',')
			}
			text.WriteString(quoteJSON(entry.key))
			text.WriteByte(':')
			if err := d.writeValue(text, childIgnore, entry.value); err != nil {
				return err
			}
		}
		text.WriteByte('}')
	case value.array != nil:
		start := text.Len()
		text.WriteByte('[')
		for index, child := range value.array {
			if err := d.ctx.Err(); err != nil {
				return err
			}
			childIgnore := ignore.indexAt(index)
			if childIgnore.skipsValue() {
				continue
			}
			if text.Len() > start+1 {
				text.WriteByte(',')
			}
			if err := d.writeValue(text, childIgnore, child); err != nil {
				return err
			}
		}
		text.WriteByte(']')
	default:
		text.WriteString(value.render(false, 0))
	}
	return nil
}

type jsonDiffIgnores struct {
	fields map[string]struct{}
	paths  *jsonDiffIgnorePath
}

func (r jsonDiffIgnores) skipField(field string) bool {
	_, found := r.fields[field]
	return found
}

type jsonDiffIgnorePath struct {
	fields  map[string]*jsonDiffIgnorePath
	indexes map[int]*jsonDiffIgnorePath
	skip    bool
}

func (r *jsonDiffIgnorePath) skipsValue() bool {
	return r != nil && r.skip
}

func (r *jsonDiffIgnorePath) fieldAt(field string) *jsonDiffIgnorePath {
	if r == nil {
		return nil
	}
	return r.fields[field]
}

func (r *jsonDiffIgnorePath) indexAt(index int) *jsonDiffIgnorePath {
	if r == nil {
		return nil
	}
	return r.indexes[index]
}

func (r *jsonDiffIgnorePath) addField(field string) *jsonDiffIgnorePath {
	if existing := r.fieldAt(field); existing != nil {
		return existing
	}
	if r.fields == nil {
		r.fields = map[string]*jsonDiffIgnorePath{}
	}
	child := &jsonDiffIgnorePath{}
	r.fields[field] = child
	return child
}

func (r *jsonDiffIgnorePath) addIndex(index int) *jsonDiffIgnorePath {
	if existing := r.indexAt(index); existing != nil {
		return existing
	}
	if r.indexes == nil {
		r.indexes = map[int]*jsonDiffIgnorePath{}
	}
	child := &jsonDiffIgnorePath{}
	r.indexes[index] = child
	return child
}

func parseJSONDiffIgnores(ctx context.Context, source string) (jsonDiffIgnores, error) {
	if err := checkSize(source); err != nil {
		return jsonDiffIgnores{}, err
	}
	rules := jsonDiffIgnores{fields: map[string]struct{}{}, paths: &jsonDiffIgnorePath{}}
	for {
		if err := ctx.Err(); err != nil {
			return jsonDiffIgnores{}, err
		}
		source = strings.TrimLeftFunc(source, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		if source == "" {
			return rules, nil
		}
		end := jsonDiffRuleEnd(source)
		rule := strings.TrimSpace(source[:end])
		source = source[end:]
		if rule[0] != '$' {
			rules.fields[rule] = struct{}{}
			continue
		}
		if err := addJSONDiffPath(rules.paths, rule); err != nil {
			return jsonDiffIgnores{}, err
		}
	}
}

func jsonDiffRuleEnd(source string) int {
	isPath := source[0] == '$'
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ',', '\n', '\r':
			return index
		case '"':
			if isPath {
				end := jsonDiffQuotedEnd(source[index:])
				if end == 0 {
					return len(source)
				}
				index += end - 1
			}
		}
	}
	return len(source)
}

func addJSONDiffPath(node *jsonDiffIgnorePath, rule string) error {
	for depth, rest := 0, rule[1:]; rest != ""; depth++ {
		if depth >= MaxJSONDepth {
			return fmt.Errorf("JSON 路径不能超过 %d 层", MaxJSONDepth)
		}
		switch rest[0] {
		case '.':
			end := 1 + jsonDiffIdentifierEnd(rest[1:])
			if end == 1 {
				return invalidJSONDiffPath(rule)
			}
			node = node.addField(rest[1:end])
			rest = rest[end:]
		case '[':
			var ok bool
			node, rest, ok = readJSONDiffBracket(node, rest[1:])
			if !ok {
				return invalidJSONDiffPath(rule)
			}
		default:
			return invalidJSONDiffPath(rule)
		}
	}
	node.skip = true
	return nil
}

func invalidJSONDiffPath(rule string) error {
	return fmt.Errorf("无效的 JSON 路径 %s", quoteJSON(rule))
}

func readJSONDiffBracket(node *jsonDiffIgnorePath, rest string) (*jsonDiffIgnorePath, string, bool) {
	if rest == "" {
		return nil, "", false
	}
	if rest[0] == '"' {
		end := jsonDiffQuotedEnd(rest)
		if end == 0 || end == len(rest) || rest[end] != ']' {
			return nil, "", false
		}
		var key string
		if err := json.Unmarshal([]byte(rest[:end]), &key); err != nil {
			return nil, "", false
		}
		return node.addField(key), rest[end+1:], true
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 || end == len(rest) || rest[end] != ']' {
		return nil, "", false
	}
	index, err := strconv.Atoi(rest[:end])
	if err != nil {
		return nil, "", false
	}
	return node.addIndex(index), rest[end+1:], true
}

func jsonDiffQuotedEnd(value string) int {
	for index := 1; index < len(value); index++ {
		switch value[index] {
		case '"':
			return index + 1
		case '\\':
			index++
		}
	}
	return 0
}

func appendJSONDiffField(path []byte, field string) []byte {
	if field != "" && jsonDiffIdentifierEnd(field) == len(field) {
		return append(append(path, '.'), field...)
	}
	path = append(path, '[')
	path = append(path, quoteJSON(field)...)
	return append(path, ']')
}

func appendJSONDiffIndex(path []byte, index int) []byte {
	path = strconv.AppendInt(append(path, '['), int64(index), 10)
	return append(path, ']')
}

func jsonDiffIdentifierEnd(value string) int {
	for index, char := range value {
		if char == '_' || char == '$' || unicode.IsLetter(char) || index > 0 && unicode.IsDigit(char) {
			continue
		}
		return index
	}
	return len(value)
}

func sameJSONDiffScalar(left, right any) bool {
	if left == right {
		return true
	}
	l, lNumber := left.(json.Number)
	r, rNumber := right.(json.Number)
	if !lNumber || !rNumber {
		return false
	}
	lDigits, lExponent, lShift, lNegative := splitJSONDiffNumber(l)
	rDigits, rExponent, rShift, rNegative := splitJSONDiffNumber(r)
	if lDigits != rDigits || lNegative != rNegative {
		return false
	}
	if lDigits == "" {
		return true
	}
	return normalizeJSONDiffExponent(lExponent, lShift) == normalizeJSONDiffExponent(rExponent, rShift)
}

func splitJSONDiffNumber(number json.Number) (digits, exponent string, shift int, negative bool) {
	digits = string(number)
	digits, negative = strings.CutPrefix(digits, "-")
	if at := strings.IndexAny(digits, "eE"); at >= 0 {
		digits, exponent = digits[:at], digits[at+1:]
	}
	if at := strings.IndexByte(digits, '.'); at >= 0 {
		shift = at + 1 - len(digits)
		digits = digits[:at] + digits[at+1:]
	}
	digits = strings.TrimLeft(digits, "0")
	coefficient := strings.TrimRight(digits, "0")
	return coefficient, exponent, shift + len(digits) - len(coefficient), negative && coefficient != ""
}

type jsonDiffExponent struct {
	prefix   string
	tail     int
	negative bool
}

// A JSON exponent can be millions of digits. Split off a decimal tail larger
// than any coefficient adjustment so addition only carries or borrows once.
func normalizeJSONDiffExponent(exponent string, shift int) jsonDiffExponent {
	const tailDigits = 8
	const base = 100_000_000
	negative := strings.HasPrefix(exponent, "-")
	exponent = strings.TrimLeft(exponent, "+-0")
	split := max(0, len(exponent)-tailDigits)
	prefix := exponent[:split]
	tail, _ := strconv.Atoi(exponent[split:])
	if negative {
		shift = -shift
	}
	tail += shift
	switch {
	case tail < 0 && prefix == "":
		negative = !negative
		tail = -tail
	case tail < 0:
		prefix = shiftJSONDiffExponentPrefix(prefix, -1)
		tail += base
	case tail >= base:
		prefix = shiftJSONDiffExponentPrefix(prefix, 1)
		tail -= base
	}
	return jsonDiffExponent{prefix, tail, negative && (prefix != "" || tail != 0)}
}

func shiftJSONDiffExponentPrefix(prefix string, shift int) string {
	digits := []byte(prefix)
	for index := len(digits) - 1; index >= 0; index-- {
		digit := int(digits[index]-'0') + shift
		switch {
		case digit < 0:
			digits[index] = '9'
		case digit > 9:
			digits[index] = '0'
		default:
			digits[index] = byte(digit) + '0'
			return strings.TrimLeft(string(digits), "0")
		}
	}
	return "1" + string(digits)
}
