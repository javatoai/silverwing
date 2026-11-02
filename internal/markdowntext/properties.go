package markdowntext

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type PropertyField struct{ Name, Value string }
type PropertySourceEdit struct {
	Start, End int
	Text       string
}
type TagAction uint8

const (
	AddTag TagAction = iota
	RenameTag
	RemoveTag
)

// Properties is a source projection. No YAML serializer owns the document.
// End includes the closing delimiter and its line ending; all offsets use Source.
type Properties struct {
	Source       string
	End          int
	Tags         []string
	Fields       []PropertyField
	TagsEditable bool
	body, close  int
	key, value   *yaml.Node
	items        []*yaml.Node
	lines        []int
}

// ParseProperties recognizes a complete, bounded, unambiguous YAML mapping at
// the document start. Invalid/incomplete metadata remains ordinary source.
func ParseProperties(source string) *Properties {
	start := 0
	if strings.HasPrefix(source, "\ufeff") {
		start = len("\ufeff")
	}
	if len(source) < start+4 || source[start:start+3] != "---" || source[start+3] != '\n' && source[start+3] != '\r' {
		return nil
	}
	lineEnd, next := propertiesLine(source, start)
	if source[start:lineEnd] != "---" || next == lineEnd {
		return nil
	}
	p := &Properties{Source: source, body: next, TagsEditable: true}
	for at := next; at < len(source) && at-next <= 128*1024; {
		z, n := propertiesLine(source, at)
		if source[at:z] == "---" || source[at:z] == "..." {
			p.close, p.End = at, n
			break
		}
		at = n
	}
	if p.End == 0 {
		return nil
	}
	// YAML accepts Unicode line separators; the editor uses physical CR/LF
	// coordinates. Keep such headers literal instead of guessing node offsets.
	if strings.ContainsAny(source[p.body:p.close], "\u0085\u2028\u2029") {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(source[p.body:p.close]), &doc); err != nil {
		return nil
	}
	p.lines = []int{p.body}
	for at := p.body; at < p.close; {
		_, next := propertiesLine(source, at)
		if next < p.close {
			p.lines = append(p.lines, next)
		}
		at = next
	}
	if len(doc.Content) == 0 {
		return p
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 || !propertiesUnique(root, 0) {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		text := value.Value
		if value.Kind != yaml.ScalarNode {
			at := p.nodeOffset(value)
			end := p.close
			if i+2 < len(root.Content) {
				end = propertiesLineStart(source, p.nodeOffset(root.Content[i+2]))
			}
			if at < p.body || end < at || end > p.close {
				return nil
			}
			text = strings.TrimSpace(source[at:end])
		}
		p.Fields = append(p.Fields, PropertyField{key.Value, text})
		if key.Value != "tags" {
			continue
		}
		p.key, p.value = key, value
		switch {
		case value.Kind == yaml.SequenceNode:
			p.items = value.Content
		case value.Kind == yaml.ScalarNode && value.Tag == "!!str":
			p.items = []*yaml.Node{value}
		case value.Kind == yaml.ScalarNode && value.Tag == "!!null":
		default:
			p.TagsEditable = false
		}
		if value.Anchor != "" || key.Anchor != "" || key.Style&yaml.TaggedStyle != 0 || value.Style&yaml.TaggedStyle != 0 || value.Kind == yaml.SequenceNode && value.Tag != "!!seq" {
			p.TagsEditable = false
		}
		for _, item := range p.items {
			p.Tags = append(p.Tags, item.Value)
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Anchor != "" || item.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.TaggedStyle) != 0 {
				p.TagsEditable = false
				continue
			}
			if _, _, ok := p.scalarRange(item, value.Style&yaml.FlowStyle != 0); !ok {
				p.TagsEditable = false
			}
		}
		if value.Style&yaml.FlowStyle != 0 {
			if _, _, ok := p.flowRange(); !ok {
				p.TagsEditable = false
			}
		}
	}
	return p
}

func propertiesUnique(n *yaml.Node, depth int) bool {
	if depth > 64 {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
				return false
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if !propertiesUnique(child, depth+1) {
			return false
		}
	}
	return true
}

func propertiesLine(source string, at int) (int, int) {
	z := at
	for z < len(source) && source[z] != '\n' && source[z] != '\r' {
		z++
	}
	n := z
	if n < len(source) && source[n] == '\r' {
		n++
	}
	if n < len(source) && source[n] == '\n' {
		n++
	}
	return z, n
}
func propertiesLineStart(source string, at int) int {
	if at < 0 || at > len(source) {
		return -1
	}
	return strings.LastIndexByte(source[:at], '\n') + 1
}
func (p *Properties) nodeOffset(n *yaml.Node) int {
	if n.Line < 1 || n.Line > len(p.lines) {
		return -1
	}
	at := p.lines[n.Line-1]
	for c := 1; c < n.Column && at < len(p.Source); c++ {
		_, size := utf8.DecodeRuneInString(p.Source[at:])
		at += size
	}
	return at
}
func (p *Properties) scalarRange(n *yaml.Node, flow bool) (int, int, bool) {
	a := p.nodeOffset(n)
	if a < 0 || a > len(p.Source) {
		return 0, 0, false
	}
	z, _ := propertiesLine(p.Source, a)
	if a >= z {
		return a, a, false
	}
	if p.Source[a] == '\'' || p.Source[a] == '"' {
		quote := p.Source[a]
		for at := a + 1; at < z; at++ {
			if quote == '"' && p.Source[at] == '\\' {
				at++
				continue
			}
			if p.Source[at] != quote {
				continue
			}
			if quote == '\'' && at+1 < z && p.Source[at+1] == '\'' {
				at++
				continue
			}
			return a, at + 1, true
		}
		return a, a, false
	}
	for at := a; at < z; at++ {
		if flow && (p.Source[at] == ',' || p.Source[at] == ']') || p.Source[at] == '#' && (at == a || p.Source[at-1] == ' ' || p.Source[at-1] == '\t') {
			z = at
			break
		}
	}
	z = a + len(strings.TrimRight(p.Source[a:z], " \t"))
	return a, z, z > a && p.Source[a:z] == n.Value
}
func (p *Properties) flowRange() (int, int, bool) {
	if p.value == nil {
		return 0, 0, false
	}
	a := p.nodeOffset(p.value)
	if a < 0 || a > len(p.Source) {
		return 0, 0, false
	}
	z, _ := propertiesLine(p.Source, a)
	if a >= z || p.Source[a] != '[' {
		return 0, 0, false
	}
	quote := byte(0)
	for at := a + 1; at < z; at++ {
		ch := p.Source[at]
		if quote != 0 {
			if quote == '"' && ch == '\\' {
				at++
				continue
			}
			if ch == quote {
				if quote == '\'' && at+1 < z && p.Source[at+1] == '\'' {
					at++
					continue
				}
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == ']' {
			return a, at + 1, true
		}
		if ch == '#' {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

func (p *Properties) tagEdits(action TagAction, index int, value string) ([]PropertySourceEdit, error) {
	if p == nil || !p.TagsEditable {
		return nil, errors.New("此标签格式请通过源码编辑")
	}
	if action > RemoveTag || action != AddTag && (index < 0 || index >= len(p.items)) {
		return nil, errors.New("标签已变化，请重新打开编辑")
	}
	value = strings.TrimSpace(value)
	if action != RemoveTag {
		if value == "" || strings.ContainsAny(value, "#\\") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
			return nil, errors.New("请输入有效的标签层级，例如：业务/订单支付")
		}
		for _, r := range value {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return nil, errors.New("标签中不能包含空白或控制字符")
			}
		}
		for i, tag := range p.Tags {
			if (action == AddTag || i != index) && strings.EqualFold(tag, value) {
				return nil, errors.New("此标签已存在")
			}
		}
		if action == RenameTag && p.Tags[index] == value {
			return nil, nil
		}
	}
	quoted := strconv.Quote(value)
	edit := func(a, z int, text string) []PropertySourceEdit { return []PropertySourceEdit{{a, z, text}} }
	if p.key == nil {
		return edit(p.close, p.close, "tags:"+p.newline()+"  - "+quoted+p.newline()), nil
	}
	if action == RenameTag {
		a, z, ok := p.scalarRange(p.items[index], p.value.Style&yaml.FlowStyle != 0)
		if !ok {
			return nil, errors.New("标签源范围不明确")
		}
		return edit(a, z, quoted), nil
	}
	if p.value.Style&yaml.FlowStyle != 0 {
		a, z, _ := p.flowRange()
		if action == AddTag {
			sep := ""
			if len(p.items) > 0 {
				sep = ", "
				if strings.HasSuffix(strings.TrimSpace(p.Source[a+1:z-1]), ",") {
					sep = " "
				}
			}
			return edit(z-1, z-1, sep+quoted), nil
		}
		x, y, _ := p.scalarRange(p.items[index], true)
		if len(p.items) == 1 {
			return edit(a+1, z-1, ""), nil
		}
		if len(p.items) > 1 {
			if index+1 < len(p.items) {
				y = p.nodeOffset(p.items[index+1])
			} else {
				prev, _, _ := p.scalarRange(p.items[index-1], true)
				x = prev + strings.LastIndexByte(p.Source[prev:x], ',')
			}
		}
		if x <= a || y >= z {
			return nil, errors.New("标签分隔符不明确")
		}
		return edit(x, y, ""), nil
	}
	if p.value.Kind == yaml.ScalarNode && p.value.Tag == "!!str" {
		a, z, ok := p.scalarRange(p.value, false)
		if !ok {
			return nil, errors.New("标签源范围不明确")
		}
		if action == AddTag {
			return edit(a, z, "["+strconv.Quote(p.Tags[0])+", "+quoted+"]"), nil
		}
		return edit(a, z, "[]"), nil
	}
	keyAt := p.nodeOffset(p.key)
	if keyAt < 0 {
		return nil, errors.New("标签键名源范围不明确")
	}
	keyEnd, _ := propertiesLine(p.Source, keyAt)
	colon := strings.IndexByte(p.Source[keyAt:keyEnd], ':') + keyAt
	if colon < keyAt {
		return nil, errors.New("标签键名源范围不明确")
	}
	if len(p.items) == 0 {
		if p.value.Tag == "!!null" && strings.TrimSpace(p.Source[colon+1:keyEnd]) != "" && !strings.HasPrefix(strings.TrimSpace(p.Source[colon+1:keyEnd]), "#") {
			a := p.nodeOffset(p.value)
			z := a
			for z < keyEnd && p.Source[z] != ' ' && p.Source[z] != '\t' && p.Source[z] != '#' {
				z++
			}
			return edit(a, z, "["+quoted+"]"), nil
		}
		return edit(colon+1, colon+1, " ["+quoted+"]"), nil
	}
	last := p.items[len(p.items)-1]
	_, lastEnd, _ := p.scalarRange(last, false)
	_, lastNext := propertiesLine(p.Source, lastEnd)
	first := p.items[0]
	firstStart := propertiesLineStart(p.Source, p.nodeOffset(first))
	prefix := p.Source[firstStart:p.nodeOffset(first)]
	if strings.TrimSpace(prefix) != "-" {
		return nil, errors.New("此标签列表请通过源码编辑")
	}
	if action == AddTag {
		return edit(lastNext, lastNext, prefix+quoted+p.newline()), nil
	}
	a, z, _ := p.scalarRange(p.items[index], false)
	lineStart := propertiesLineStart(p.Source, a)
	lineEnd, next := propertiesLine(p.Source, z)
	comment := p.Source[z:lineEnd]
	replacement := ""
	if strings.TrimSpace(comment) != "" {
		commentStart := strings.IndexByte(comment, '#')
		if commentStart < 0 || strings.TrimSpace(comment[:commentStart]) != "" {
			return nil, fmt.Errorf("标签尾部包含不可编辑内容")
		}
		replacement = strings.Repeat(" ", len(prefix)-len(strings.TrimLeft(prefix, " \t"))) + comment[commentStart:] + p.newline()
	}
	edits := edit(lineStart, next, replacement)
	if len(p.items) == 1 {
		edits = append(edits, PropertySourceEdit{colon + 1, colon + 1, " []"})
	}
	return edits, nil
}

func (p *Properties) newline() string {
	_, next := propertiesLine(p.Source, p.body)
	if next >= 2 && p.Source[next-2:next] == "\r\n" {
		return "\r\n"
	}
	if next > 0 && p.Source[next-1] == '\r' {
		return "\r"
	}
	return "\n"
}

// TagEdits validates the candidate before returning any mutation to the owner.
func (p *Properties) TagEdits(action TagAction, index int, value string) ([]PropertySourceEdit, error) {
	edits, err := p.tagEdits(action, index, value)
	if err != nil || len(edits) == 0 {
		return edits, err
	}
	candidate := p.Source
	// At most two non-overlapping edits; apply from the end without rewriting YAML.
	if len(edits) > 1 && edits[0].Start < edits[1].Start {
		edits[0], edits[1] = edits[1], edits[0]
	}
	for _, e := range edits {
		if e.Start < 0 || e.End < e.Start || e.End > p.close {
			return nil, errors.New("属性源范围已变化")
		}
		candidate = candidate[:e.Start] + e.Text + candidate[e.End:]
	}
	next := ParseProperties(candidate)
	if next == nil {
		return nil, errors.New("无法安全更新此标签，请通过源码编辑")
	}
	expected := append([]string(nil), p.Tags...)
	switch action {
	case AddTag:
		expected = append(expected, strings.TrimSpace(value))
	case RenameTag:
		expected[index] = strings.TrimSpace(value)
	case RemoveTag:
		expected = append(expected[:index], expected[index+1:]...)
	}
	if len(expected) != len(next.Tags) {
		return nil, errors.New("标签格式无法无损更新")
	}
	for i := range expected {
		if expected[i] != next.Tags[i] {
			return nil, errors.New("标签格式无法无损更新")
		}
	}
	return edits, nil
}
