package editor

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gfmast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var parsedPartID atomic.Uint64

// JetBrains GFM 0.7.9 recognizes bare file:// links and only angle-bracket
// email autolinks. Goldmark's GFM Linkify omits file:// and also links bare
// email addresses, so the extra parser adds the former while conversion filters
// the latter from the generated AST.
var (
	neverLinkRE = regexp.MustCompile(`a^`)
	gfmURLRE    = regexp.MustCompile(gfmAutoLinkRE(`^(https?|ftp|file)://`))
	gfmWWWRE    = regexp.MustCompile(gfmAutoLinkRE(`^www\.`))
)

var markdownParser = goldmark.New(goldmark.WithExtensions(
	extension.Table,
	extension.Strikethrough,
	extension.TaskList,
	jetbrainsAutoLinkExtension{},
	extension.NewLinkify(
		extension.WithLinkifyAllowedProtocols([]string{"http:", "https:", "ftp:", "file:"}),
		extension.WithLinkifyURLRegexp(gfmURLRE),
		extension.WithLinkifyWWWRegexp(gfmWWWRE),
		extension.WithLinkifyEmailRegexp(neverLinkRE),
	),
	mathExtension{},
)).Parser()

var (
	frontMatterRE = regexp.MustCompile(`\A(?:\x{FEFF})?---[ \t]*\n[\s\S]*?\n(?:---|\.\.\.)[ \t]*(?:\n|$)`)
	setextRE      = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	listRE        = regexp.MustCompile(`^([ \t]*)([-+*]|[0-9]+[.)])[ \t]+(.*)$`)
	taskRE        = regexp.MustCompile(`(?m)^([ \t]*)([-+*])[ \t]+\[([ xX])\][ \t]*(.*)$`)
	tableSepRE    = regexp.MustCompile(`^:?-{3,}:?$`)
	sourceTaskRE  = regexp.MustCompile(`(?m)^\s*[-+*]\s+\[[ xX]\](?:\s|$)`)
	sourceImageRE = regexp.MustCompile(`!\[[^]]*\]\(`)
	imageUUIDRE   = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	unsupportedRE = regexp.MustCompile(`<!--[\s\S]*?-->|<[/A-Za-z][^>]*>|\[\^[^]]+]|(^|\s):[A-Za-z]+\[|\{[.#][^}]+}`)
)

type sourceLine struct {
	start, contentEnd, end int
	text                   string
}

type parsedBlock struct {
	node   gast.Node
	kind   PartKind
	start  int
	end    int
	hidden bool
}

// Parse returns a GFM AST mapped back to exact source ranges. The parser is
// used only to classify and describe blocks; edits continue to replace original
// source slices and never serialize the document back from the AST.
func Parse(source string) Snapshot {
	parts, _ := parseDocument(source)
	for i := range parts {
		parts[i].ID = parsedPartID.Add(1)
	}
	return Snapshot{Source: source, Parts: parts, Version: 0, CanUndo: false, CanRedo: false}
}

func parseDocument(source string) ([]Part, []Node) {
	if source == "" {
		return []Part{{Kind: PartRich, Range: SourceRange{}, Source: ""}}, nil
	}

	normalized, offsets := normalizeLines(source)
	metadataEnd := 0
	if match := frontMatterRE.FindStringIndex(normalized); match != nil {
		metadataEnd = match[1]
	}
	bodyStart := offsets[metadataEnd]
	normalizedBody := []byte(normalized[metadataEnd:])
	root := markdownParser.Parse(text.NewReader(normalizedBody))
	mapper := sourceMapper{original: source, offsets: offsets, normalizedBase: metadataEnd}

	blocks := make([]parsedBlock, 0, root.ChildCount())
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		start := child.Pos()
		if start < 0 || start > len(normalizedBody) {
			continue
		}
		blocks = append(blocks, parsedBlock{node: child, start: start})
	}
	for i := range blocks {
		blocks[i].end = len(normalizedBody)
		if i+1 < len(blocks) {
			blocks[i].end = blocks[i+1].start
		}
		if blocks[i].end < blocks[i].start {
			blocks[i].end = blocks[i].start
		}
		blocks[i].kind = classifyBlock(blocks[i].node, normalizedBody, blocks[i].start, blocks[i].end)
		if i > 0 && safeAdjacentBlockImageMetadata(blocks[i-1], blocks[i], normalizedBody) {
			blocks[i].hidden = true
		}
	}

	parts := make([]Part, 0, len(blocks)*2+1)
	if bodyStart > 0 {
		parts = append(parts, Part{
			Kind: PartMetadata, Range: SourceRange{0, bodyStart}, Source: source[:bodyStart],
			Nodes: []Node{{Kind: "metadata", Range: SourceRange{0, bodyStart}, Text: source[:bodyStart]}},
		})
	}
	if len(blocks) == 0 {
		if bodyStart < len(source) {
			start, end := mapper.rawRange(0, len(normalizedBody))
			parts = append(parts, Part{
				Kind: PartRich, Range: SourceRange{start, end}, Source: source[start:end],
				Nodes: []Node{{Kind: "paragraph", Range: SourceRange{start, end}, Children: []Node{{Kind: "text", Range: SourceRange{start, end}, Text: source[start:end]}}}},
			})
		} else if len(parts) > 0 {
			parts = append(parts, Part{Kind: PartRich, Range: SourceRange{len(source), len(source)}, Source: ""})
		}
		return parts, collectNodes(parts)
	}

	richStart := 0
	previousEnd := 0
	richNodes := make([]Node, 0, len(blocks))
	flushRich := func(end int) {
		if end < richStart || end == richStart && len(richNodes) == 0 {
			return
		}
		startRaw, endRaw := mapper.rawRange(richStart, end)
		parts = append(parts, Part{Kind: PartRich, Range: SourceRange{startRaw, endRaw}, Source: source[startRaw:endRaw], Nodes: append([]Node(nil), richNodes...)})
		richNodes = richNodes[:0]
	}

	for _, block := range blocks {
		startRaw, endRaw := mapper.rawRange(block.start, block.end)
		nodeRange := SourceRange{startRaw, endRaw}
		if block.hidden {
			if len(richNodes) > 0 {
				flushRich(block.start)
			}
			parts = append(parts, Part{Kind: PartRich, Range: nodeRange, Source: source[startRaw:endRaw]})
			richStart, previousEnd = block.end, block.end
			continue
		}
		node := convertBlock(block.node, normalizedBody, mapper, nodeRange, block.kind)
		if block.kind == PartRich {
			if len(richNodes) == 0 {
				richStart = previousEnd
			}
			richNodes = append(richNodes, node)
			previousEnd = block.end
			continue
		}
		if len(richNodes) == 0 {
			richStart = previousEnd
		}
		flushRich(block.start)
		parts = append(parts, Part{Kind: block.kind, Range: nodeRange, Source: source[startRaw:endRaw], Nodes: []Node{node}})
		richStart = block.end
		previousEnd = block.end
	}
	if len(richNodes) > 0 {
		flushRich(len(normalizedBody))
	}
	if len(parts) == 0 || parts[len(parts)-1].Kind != PartRich {
		parts = append(parts, Part{Kind: PartRich, Range: SourceRange{len(source), len(source)}, Source: ""})
	}
	return parts, collectNodes(parts)
}

func safeAdjacentBlockImageMetadata(previous, current parsedBlock, source []byte) bool {
	if previous.kind != PartImage {
		return false
	}
	if _, ok := current.node.(*gast.HTMLBlock); !ok || current.start < 0 || current.start > current.end || current.end > len(source) {
		return false
	}
	metadataStart := strings.Index(string(source[current.start:current.end]), "<!--")
	if metadataStart < 0 {
		return false
	}
	commentStart := current.start + metadataStart
	imageEnd, destination, ok := lastImageEnd(previous.node, source, previous.start, previous.end)
	return ok && safeImageAnnotation(source[current.start:current.end], destination) && adjacentSourceWhitespace(source, imageEnd, commentStart)
}

func lastImageEnd(node gast.Node, source []byte, lower, upper int) (int, string, bool) {
	last, found := 0, false
	destination := ""
	var visit func(gast.Node)
	visit = func(current gast.Node) {
		if current == nil {
			return
		}
		if image, ok := current.(*gast.Image); ok {
			start := inlineNodeSourceStart(image, source, lower, upper)
			end := inlineNodeEnd(image, source, start, upper)
			if start <= end && end <= upper {
				last, found = end, true
				destination = string(image.Destination)
			}
		}
		for child := current.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(node)
	return last, destination, found
}

func collectNodes(parts []Part) []Node {
	var nodes []Node
	for _, part := range parts {
		nodes = append(nodes, part.Nodes...)
	}
	return nodes
}

type sourceMapper struct {
	original       string
	offsets        []int
	normalizedBase int
}

func (m sourceMapper) rawOffset(normalizedOffset int) int {
	index := m.normalizedBase + normalizedOffset
	if index < 0 {
		index = 0
	}
	if index >= len(m.offsets) {
		return len(m.original)
	}
	return m.offsets[index]
}

func (m sourceMapper) rawRange(start, end int) (int, int) {
	if end < start {
		end = start
	}
	return m.rawOffset(start), m.rawOffset(end)
}

func (m sourceMapper) rawText(start, end int) string {
	startRaw, endRaw := m.rawRange(start, end)
	if startRaw < 0 || endRaw < startRaw || endRaw > len(m.original) {
		return ""
	}
	return m.original[startRaw:endRaw]
}

func normalizeLines(raw string) (string, []int) {
	out := make([]byte, 0, len(raw))
	mapOffsets := make([]int, 0, len(raw)+1)
	for i := 0; i < len(raw); {
		mapOffsets = append(mapOffsets, i)
		if raw[i] == '\r' {
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i += 2
			} else {
				i++
			}
			out = append(out, '\n')
		} else {
			out = append(out, raw[i])
			i++
		}
	}
	mapOffsets = append(mapOffsets, len(raw))
	return string(out), mapOffsets
}

func classifyBlock(node gast.Node, source []byte, start, end int) PartKind {
	raw := string(source[start:end])
	switch node.(type) {
	case *gast.FencedCodeBlock:
		if fence := FindFence(raw); fence != nil && strings.EqualFold(fence.Language, "mermaid") {
			return PartMermaid
		}
		return PartCode
	case *gfmast.Table:
		return PartTable
	case *gast.ThematicBreak:
		return PartDivider
	case *gast.HTMLBlock, *gast.LinkReferenceDefinition, *gast.CodeBlock, *gast.Blockquote:
		return PartOriginal
	case *gast.Heading:
		if isSetextHeading(raw) {
			return PartOriginal
		}
	}
	ignoredMetadata := safeAdjacentPresentationMetadata(node, source, start, end)
	if containsUnsupportedSyntaxExcept(node, source, ignoredMetadata) || unsupportedRE.MatchString(withoutSourceRanges(raw, start, ignoredMetadata)) || hasInlineMath(node, source) {
		return PartOriginal
	}
	if sourceTaskRE.MatchString(raw) {
		return PartTasks
	}
	if sourceImageRE.MatchString(raw) {
		return PartImage
	}
	return PartRich
}

func containsUnsupportedSyntax(node gast.Node, source []byte) bool {
	return containsUnsupportedSyntaxExcept(node, source, nil)
}

func containsUnsupportedSyntaxExcept(node gast.Node, source []byte, ignored map[*gast.RawHTML]SourceRange) bool {
	if node == nil {
		return false
	}
	switch n := node.(type) {
	case *gast.RawHTML:
		if _, ok := ignored[n]; !ok {
			return true
		}
	case *gast.LinkReferenceDefinition, *gast.Blockquote:
		return true
	case *gast.CodeBlock:
		return true
	case *gast.Heading:
		if isSetextNode(n, source) {
			return true
		}
	case *gast.Link:
		if n.Reference != nil {
			return true
		}
	case *gast.Image:
		if n.Reference != nil {
			return true
		}
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if containsUnsupportedSyntaxExcept(child, source, ignored) {
			return true
		}
	}
	return false
}

// safeAdjacentImageMetadata recognizes only Feishu's image annotation when
// Goldmark parsed it as raw HTML immediately after a real image AST node. The
// original source and all byte offsets remain untouched; callers use the
// returned ranges only to classify and omit this annotation from presentation.
func safeAdjacentImageMetadata(node gast.Node, source []byte, lower, upper int) map[*gast.RawHTML]SourceRange {
	ignored := make(map[*gast.RawHTML]SourceRange)
	var visit func(gast.Node)
	visit = func(parent gast.Node) {
		if parent == nil {
			return
		}
		children := make([]gast.Node, 0, parent.ChildCount())
		for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
			children = append(children, child)
		}
		for index, child := range children {
			comment, isComment := child.(*gast.RawHTML)
			if isComment {
				commentStart := inlineNodeSourceStart(comment, source, lower, upper)
				commentEnd := inlineNodeEnd(comment, source, commentStart, upper)
				if commentStart <= commentEnd && commentEnd <= upper {
					for previous := index - 1; previous >= 0; previous-- {
						if _, whitespace := children[previous].(*gast.Text); whitespace {
							continue
						}
						image, isImage := children[previous].(*gast.Image)
						if !isImage {
							break
						}
						imageStart := inlineNodeSourceStart(image, source, lower, upper)
						imageEnd := inlineNodeEnd(image, source, imageStart, upper)
						if imageEnd <= commentStart && safeImageAnnotation(source[commentStart:commentEnd], string(image.Destination)) && adjacentSourceWhitespace(source, imageEnd, commentStart) {
							ignored[comment] = SourceRange{Start: commentStart, End: commentEnd}
						}
						break
					}
				}
			}
			visit(child)
		}
	}
	visit(node)
	return ignored
}

// safeAdjacentPresentationMetadata recognizes only the presentation-only
// Feishu annotations that are attached to a real AST node. Their bytes remain
// in Source and are omitted only from the rendered AST.
func safeAdjacentPresentationMetadata(node gast.Node, source []byte, lower, upper int) map[*gast.RawHTML]SourceRange {
	ignored := safeAdjacentImageMetadata(node, source, lower, upper)
	if node == nil {
		return ignored
	}
	// Classification sees the whole block, including list items and emphasis.
	// Collect the same recognized annotations that inline conversion omits.
	_ = gast.Walk(node, func(parent gast.Node, entering bool) (gast.WalkStatus, error) {
		if entering {
			for comment, sourceRange := range safeAdjacentLinkPreviewMetadata(parent, source, lower, upper) {
				ignored[comment] = sourceRange
			}
			for comment, sourceRange := range safeAdjacentMentionMetadata(parent, source, lower, upper) {
				ignored[comment] = sourceRange
			}
		}
		return gast.WalkContinue, nil
	})
	return ignored
}

func safeAdjacentLinkPreviewMetadata(node gast.Node, source []byte, lower, upper int) map[*gast.RawHTML]SourceRange {
	ignored := make(map[*gast.RawHTML]SourceRange)
	children := astChildren(node)
	for index, child := range children {
		comment, ok := child.(*gast.RawHTML)
		if !ok {
			continue
		}
		commentStart := inlineNodeSourceStart(comment, source, lower, upper)
		commentEnd := inlineNodeEnd(comment, source, commentStart, upper)
		if commentStart < lower || commentEnd < commentStart || commentEnd > upper || string(source[commentStart:commentEnd]) != "<!-- linkPreview -->" {
			continue
		}
		for previous := index - 1; previous >= 0; previous-- {
			link, ok := children[previous].(*gast.Link)
			if !ok {
				if text, isText := children[previous].(*gast.Text); isText {
					textStart := inlineNodeSourceStart(text, source, lower, commentStart)
					textEnd := inlineNodeEnd(text, source, textStart, commentStart)
					if textStart == textEnd {
						continue
					}
				}
				break
			}
			if link.Reference != nil || !isHTTPURL(string(link.Destination)) {
				break
			}
			linkStart := inlineNodeSourceStart(link, source, lower, commentStart)
			linkEnd := inlineNodeEnd(link, source, linkStart, commentStart)
			if linkEnd == commentStart {
				ignored[comment] = SourceRange{Start: commentStart, End: commentEnd}
			}
			break
		}
	}
	return ignored
}

func safeAdjacentMentionMetadata(node gast.Node, source []byte, lower, upper int) map[*gast.RawHTML]SourceRange {
	ignored := make(map[*gast.RawHTML]SourceRange)
	children := astChildren(node)
	for index, child := range children {
		comment, ok := child.(*gast.RawHTML)
		if !ok {
			continue
		}
		commentStart := inlineNodeSourceStart(comment, source, lower, upper)
		commentEnd := inlineNodeEnd(comment, source, commentStart, upper)
		if commentStart < lower || commentEnd < commentStart || commentEnd > upper {
			continue
		}
		name, valid := safeFeishuMention(source[commentStart:commentEnd])
		if !valid {
			continue
		}
		if index == 0 {
			continue
		}
		previous, ok := children[index-1].(*gast.Text)
		if !ok {
			continue
		}
		textStart := inlineNodeSourceStart(previous, source, lower, commentStart)
		textEnd := inlineNodeEnd(previous, source, textStart, commentStart)
		if textStart < lower || textEnd < textStart || textEnd > commentStart || textEnd != commentStart {
			continue
		}
		text := string(source[textStart:textEnd])
		if !strings.HasSuffix(text, "@"+name) {
			continue
		}
		ignored[comment] = SourceRange{Start: commentStart, End: commentEnd}
	}
	return ignored
}

func safeFeishuMention(source []byte) (string, bool) {
	comment := string(source)
	if len(comment) > 4096 || !strings.HasPrefix(comment, "<!-- mention:") || !strings.HasSuffix(comment, " -->") {
		return "", false
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(comment, "<!-- mention:"), " -->")
	var metadata struct {
		ID      string `json:"id"`
		BlockID string `json:"blockId"`
		CNName  string `json:"cn_name"`
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	if err := decoder.Decode(&metadata); err != nil {
		return "", false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", false
	}
	name := strings.TrimSpace(metadata.CNName)
	if name == "" || len(name) > 256 || strings.TrimSpace(metadata.ID) == "" || strings.TrimSpace(metadata.BlockID) == "" {
		return "", false
	}
	return name, true
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func astChildren(parent gast.Node) []gast.Node {
	if parent == nil {
		return nil
	}
	children := make([]gast.Node, 0, parent.ChildCount())
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		children = append(children, child)
	}
	return children
}

func adjacentSourceWhitespace(source []byte, start, end int) bool {
	if start < 0 || end < start || end > len(source) {
		return false
	}
	gap := string(source[start:end])
	if strings.Trim(gap, " \t\r\n") != "" {
		return false
	}
	// Accept an inline annotation or one immediately following line, but do not
	// associate metadata across unrelated blank lines.
	return strings.Count(gap, "\n") <= 1
}

func safeFeishuImageMetadata(source []byte) bool {
	comment := strings.TrimSpace(string(source))
	if !strings.HasPrefix(comment, "<!--") || !strings.HasSuffix(comment, "-->") {
		return false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment, "<!--"), "-->"))
	if !strings.HasPrefix(body, "image:") {
		return false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(body, "image:"))
	var metadata struct {
		Width int    `json:"width"`
		UUID  string `json:"uuid"`
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return false
	}
	return metadata.Width > 0 && metadata.Width <= 10000 && strings.TrimSpace(metadata.UUID) != "" && len(metadata.UUID) <= 256
}

func safeImageAnnotation(source []byte, destination string) bool {
	return safeFeishuImageMetadata(source) || isFeishuStreamImageURL(destination) && safeFeishuImageUUID(source)
}

func safeFeishuImageUUID(source []byte) bool {
	comment := strings.TrimSpace(string(source))
	return strings.HasPrefix(comment, "<!--") && strings.HasSuffix(comment, "-->") &&
		imageUUIDRE.MatchString(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment, "<!--"), "-->")))
}

func isFeishuStreamImageURL(value string) bool {
	parsed, err := url.Parse(value)
	const prefix = "/goapi/v5/platform/file/stream/download/"
	return err == nil && parsed.Scheme == "https" && parsed.Host == "project.feishu.cn" && parsed.User == nil &&
		strings.HasPrefix(parsed.EscapedPath(), prefix) && len(parsed.EscapedPath()) > len(prefix) &&
		!strings.Contains(parsed.EscapedPath()[len(prefix):], "/") && parsed.Fragment == ""
}

// FeishuCommentImageURLs returns only real rendered image nodes with the exact
// adjacent UUID annotation emitted by Feishu comments. Code and unknown HTML
// remain original blocks and cannot authorize an authenticated download.
func FeishuCommentImageURLs(source string) []string {
	parts, _ := parseDocument(source)
	seen := make(map[string]bool)
	var result []string
	var visit func(Node)
	visit = func(node Node) {
		if node.Kind == "image" && isFeishuStreamImageURL(node.Destination) && node.Range.End >= 0 && node.Range.End <= len(source) {
			tail := source[node.Range.End:]
			start := strings.Index(tail, "<!--")
			if start >= 0 && adjacentSourceWhitespace([]byte(tail), 0, start) {
				if end := strings.Index(tail[start:], "-->"); end >= 0 && safeFeishuImageUUID([]byte(tail[start:start+end+3])) && !seen[node.Destination] {
					seen[node.Destination] = true
					result = append(result, node.Destination)
				}
			}
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	for _, part := range parts {
		if part.Kind == PartImage {
			for _, node := range part.Nodes {
				visit(node)
			}
		}
	}
	return result
}

func withoutSourceRanges(source string, base int, ignored map[*gast.RawHTML]SourceRange) string {
	if len(ignored) == 0 {
		return source
	}
	ranges := make([]SourceRange, 0, len(ignored))
	for _, sourceRange := range ignored {
		start, end := sourceRange.Start-base, sourceRange.End-base
		if start >= 0 && start <= end && end <= len(source) {
			ranges = append(ranges, SourceRange{Start: start, End: end})
		}
	}
	if len(ranges) == 0 {
		return source
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	var result strings.Builder
	position := 0
	for _, sourceRange := range ranges {
		if sourceRange.Start < position {
			continue
		}
		result.WriteString(source[position:sourceRange.Start])
		position = sourceRange.End
	}
	result.WriteString(source[position:])
	return result.String()
}

func isSetextNode(node *gast.Heading, source []byte) bool {
	if node == nil || node.Lines().Len() < 2 {
		return false
	}
	last := node.Lines().At(node.Lines().Len() - 1)
	if last.Start < 0 || last.Stop > len(source) {
		return false
	}
	return setextRE.Match(source[last.Start:last.Stop])
}

func isSetextHeading(raw string) bool {
	lines := scanLines(raw)
	if len(lines) < 2 {
		return false
	}
	return setextRE.MatchString(lines[1].text)
}

func hasInlineMath(node gast.Node, _ []byte) bool {
	if node == nil {
		return false
	}
	if math, ok := node.(*mathNode); ok && math.width == 1 {
		return true
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if hasInlineMath(child, nil) {
			return true
		}
	}
	return false
}

func isEscaped(source []byte, at int) bool {
	backslashes := 0
	for i := at - 1; i >= 0 && source[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func convertBlock(node gast.Node, source []byte, mapper sourceMapper, nodeRange SourceRange, partKind PartKind) Node {
	base := Node{Range: nodeRange}
	startNorm, endNorm := mapper.normalizedRange(nodeRange)
	base.Text = mapper.rawText(startNorm, endNorm)
	switch n := node.(type) {
	case *gast.Paragraph:
		base.Kind = "paragraph"
		base.Text = ""
		base.Children = convertInlineChildren(n, source, mapper, startNorm, endNorm)
	case *gast.Heading:
		base.Kind = "heading"
		base.Level = n.Level
		base.Text = ""
		base.Children = convertInlineChildren(n, source, mapper, startNorm, endNorm)
	case *gast.FencedCodeBlock:
		base.Kind = "code-fence"
		if fence := FindFence(base.Text); fence != nil {
			base.Language = fence.Language
		} else {
			base.Language = strings.TrimSpace(string(n.Language(source)))
		}
	case *gast.CodeBlock:
		base.Kind = "original"
		base.Attributes = map[string]string{"reason": "indented-code"}
	case *gast.ThematicBreak:
		base.Kind = "divider"
	case *gast.Blockquote:
		base.Kind = "blockquote"
		base.Attributes = map[string]string{"reason": "block-quote"}
		base.Children = convertBlockChildren(n, source, mapper, startNorm, endNorm, partKind)
	case *gast.List:
		base.Kind = "list"
		if partKind == PartTasks || containsTaskCheckbox(n) {
			base.Kind = "task-list"
		}
		base.Ordered = n.IsOrdered()
		base.Attributes = map[string]string{"marker": string(n.Marker)}
		base.Children = convertBlockChildren(n, source, mapper, startNorm, endNorm, partKind)
	case *gast.ListItem:
		base.Kind = "list-item"
		if taskCheckbox(n) != nil {
			base.Kind = "task-item"
			checked := taskCheckbox(n).IsChecked
			base.Checked = &checked
		}
		base.Children = convertListItemChildren(n, source, mapper, startNorm, endNorm, partKind)
	case *gfmast.Table:
		base = convertTable(n, source, mapper, nodeRange)
	case *gfmast.TableHeader:
		base.Kind = "table-row"
		base.Attributes = map[string]string{"header": "true"}
		base.Children = convertTableCells(n, source, mapper, startNorm, endNorm, true)
	case *gfmast.TableRow:
		base.Kind = "table-row"
		base.Children = convertTableCells(n, source, mapper, startNorm, endNorm, false)
	case *gfmast.TableCell:
		base.Kind = "table-cell"
		base.Children = convertInlineChildren(n, source, mapper, startNorm, endNorm)
	case *gast.HTMLBlock:
		base.Kind = "original"
		base.Attributes = map[string]string{"reason": "html-block"}
	case *gast.LinkReferenceDefinition:
		base.Kind = "original"
		base.Attributes = map[string]string{"reason": "link-definition"}
	default:
		base.Kind = "original"
		base.Attributes = map[string]string{"reason": "unsupported-block", "ast": node.Kind().String()}
	}
	return base
}

// normalizedRange finds the normalized source bounds represented by an
// original-source range. The offset map is monotonic, including across CRLF.
func (m sourceMapper) normalizedRange(sourceRange SourceRange) (int, int) {
	start, end := 0, 0
	for i, raw := range m.offsets[m.normalizedBase:] {
		if raw <= sourceRange.Start {
			start = i
		}
		if raw <= sourceRange.End {
			end = i
		} else {
			break
		}
	}
	return start, maxInt(start, end)
}

func convertBlockChildren(parent gast.Node, source []byte, mapper sourceMapper, parentStart, parentEnd int, partKind PartKind) []Node {
	children := make([]gast.Node, 0, parent.ChildCount())
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		children = append(children, child)
	}
	var result []Node
	for i, child := range children {
		start := physicalLineStart(source, child.Pos())
		if start < parentStart || start > parentEnd {
			start = parentStart
		}
		end := parentEnd
		for j := i + 1; j < len(children); j++ {
			nextStart := physicalLineStart(source, children[j].Pos())
			if nextStart >= start && nextStart <= parentEnd {
				end = nextStart
				break
			}
		}
		startRaw, endRaw := mapper.rawRange(start, end)
		result = append(result, convertBlock(child, source, mapper, SourceRange{startRaw, endRaw}, partKind))
	}
	return result
}

func convertListItemChildren(parent gast.Node, source []byte, mapper sourceMapper, parentStart, parentEnd int, partKind PartKind) []Node {
	var result []Node
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if _, ok := child.(*gast.TextBlock); ok {
			result = append(result, convertInlineChildren(child, source, mapper, parentStart, parentEnd)...)
			continue
		}
		if _, ok := child.(*gast.Paragraph); ok {
			result = append(result, convertInlineChildren(child, source, mapper, parentStart, parentEnd)...)
			continue
		}
		start := physicalLineStart(source, child.Pos())
		if start < parentStart || start > parentEnd {
			start = parentStart
		}
		end := parentEnd
		if next := child.NextSibling(); next != nil {
			nextStart := physicalLineStart(source, next.Pos())
			if nextStart >= start && nextStart <= parentEnd {
				end = nextStart
			}
		}
		startRaw, endRaw := mapper.rawRange(start, end)
		result = append(result, convertBlock(child, source, mapper, SourceRange{startRaw, endRaw}, partKind))
	}
	return result
}

func convertTable(table *gfmast.Table, source []byte, mapper sourceMapper, nodeRange SourceRange) Node {
	node := Node{Kind: "table", Range: nodeRange, Attributes: map[string]string{}}
	alignments := make([]string, 0, len(table.Alignments))
	for _, alignment := range table.Alignments {
		alignments = append(alignments, alignment.String())
	}
	node.Attributes["alignment"] = strings.Join(alignments, ",")
	if nodeRange.Start < 0 || nodeRange.End < nodeRange.Start || nodeRange.End > len(mapper.original) {
		return node
	}
	// IntelliJ's native table editor finds cell boundaries from source, with
	// pipes inside code spans ignored. Goldmark's table AST follows GFM's pipe
	// splitting rules, so use its AST only to recognize the table and obtain
	// alignment; keep source-scanned cells as the editing/range authority.
	tableSource := mapper.original[nodeRange.Start:nodeRange.End]
	cellRows := TableCells(tableSource)
	rowLines := tableContentLines(tableSource)
	for rowIndex, cells := range cellRows {
		row := Node{Kind: "table-row"}
		if rowIndex < len(rowLines) {
			line := rowLines[rowIndex]
			row.Range = SourceRange{nodeRange.Start + line.start, nodeRange.Start + line.end}
		}
		for _, cell := range cells {
			cell.Range.Start += nodeRange.Start
			cell.Range.End += nodeRange.Start
			kind := "table-cell"
			if rowIndex == 0 {
				kind = "table-header-cell"
			}
			children := parseTableCellInline(cell.Text, cell.Range.Start)
			row.Children = append(row.Children, Node{Kind: kind, Range: cell.Range, Text: cell.Text, Children: children})
		}
		node.Children = append(node.Children, row)
	}
	return node
}

func tableContentLines(source string) []sourceLine {
	var lines []sourceLine
	for _, line := range scanLines(source) {
		if strings.TrimSpace(line.text) != "" && !separatorOnly(line.text) {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseTableCellInline(source string, absoluteStart int) []Node {
	if source == "" {
		return nil
	}
	bytes := []byte(source)
	offsets := make([]int, len(bytes)+1)
	for i := range offsets {
		offsets[i] = i
	}
	mapper := sourceMapper{original: source, offsets: offsets}
	root := markdownParser.Parse(text.NewReader(bytes))
	var result []Node
	for block := root.FirstChild(); block != nil; block = block.NextSibling() {
		if _, paragraph := block.(*gast.Paragraph); paragraph {
			for _, child := range convertInlineChildren(block, bytes, mapper, 0, len(bytes)) {
				shiftNodeRanges(&child, absoluteStart)
				result = append(result, child)
			}
		}
	}
	return result
}

func shiftNodeRanges(node *Node, delta int) {
	node.Range.Start += delta
	node.Range.End += delta
	for i := range node.Children {
		shiftNodeRanges(&node.Children[i], delta)
	}
}

func nodeRangeNormEnd(mapper sourceMapper, sourceRange SourceRange) int {
	_, end := mapper.normalizedRange(sourceRange)
	return end
}

func convertTableCells(row gast.Node, source []byte, mapper sourceMapper, parentStart, parentEnd int, header bool) []Node {
	var result []Node
	for child := row.FirstChild(); child != nil; child = child.NextSibling() {
		cell, ok := child.(*gfmast.TableCell)
		if !ok {
			continue
		}
		start, end := blockLinesRange(cell, parentStart, parentEnd)
		startRaw, endRaw := mapper.rawRange(start, end)
		kind := "table-cell"
		if header {
			kind = "table-header-cell"
		}
		result = append(result, Node{Kind: kind, Range: SourceRange{startRaw, endRaw}, Text: mapper.rawText(start, end), Children: convertInlineChildren(cell, source, mapper, start, end)})
	}
	return result
}

func blockLinesRange(node gast.Node, fallbackStart, fallbackEnd int) (int, int) {
	block, ok := node.(interface{ Lines() *text.Segments })
	if !ok || node.Type() != gast.TypeBlock || block.Lines().Len() == 0 {
		start := node.Pos()
		if start < fallbackStart || start > fallbackEnd {
			start = fallbackStart
		}
		return start, start
	}
	lines := block.Lines()
	start := lines.At(0).Start
	end := lines.At(lines.Len() - 1).Stop
	if start < fallbackStart || start > fallbackEnd {
		start = fallbackStart
	}
	if end < start || end > fallbackEnd {
		end = fallbackEnd
	}
	return start, end
}

func convertInlineChildren(parent gast.Node, source []byte, mapper sourceMapper, parentStart, parentEnd int) []Node {
	children := make([]gast.Node, 0, parent.ChildCount())
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		children = append(children, child)
	}
	var result []Node
	ignoredMetadata := safeAdjacentPresentationMetadata(parent, source, parentStart, parentEnd)
	linkPreviews := safeAdjacentLinkPreviewLinks(parent, source, parentStart, parentEnd, ignoredMetadata)
	for i, child := range children {
		if comment, ok := child.(*gast.RawHTML); ok {
			if _, ignored := ignoredMetadata[comment]; ignored {
				continue
			}
		}
		lower := parentStart
		upper := parentEnd
		if i > 0 {
			previous := children[i-1]
			previousStart := inlineNodeSourceStart(previous, source, parentStart, parentEnd)
			if previousEnd := inlineNodeEnd(previous, source, previousStart, parentEnd); previousEnd > lower {
				lower = previousEnd
			}
		}
		if i+1 < len(children) {
			if nextStart := inlineNodeSourceStart(children[i+1], source, lower, parentEnd); nextStart >= lower && nextStart < upper {
				upper = nextStart
			}
		}
		start := inlineNodeSourceStart(child, source, lower, upper)
		end := inlineNodeEnd(child, source, start, upper)
		if end < start {
			end = start
		}
		if textNode, ok := child.(*gast.Text); ok && textNode.Segment.Start == textNode.Segment.Stop {
			continue
		}
		startRaw, endRaw := mapper.rawRange(start, end)
		converted := convertInline(child, source, mapper, SourceRange{startRaw, endRaw}, start, end)
		if link, ok := child.(*gast.Link); ok && linkPreviews[link] {
			converted.Kind = "link-preview"
		}
		if _, ok := child.(*gast.AutoLink); ok && hasWordAutolinkPrefix(source, start) {
			converted.Kind = "text"
			converted.Text = mapper.rawText(start, end)
			converted.Destination = ""
			result = append(result, converted)
			continue
		}
		if autoLink, ok := child.(*gast.AutoLink); ok && start < end && source[start] != '<' {
			trimmedEnd := jetbrainsAutoLinkEnd(source, start, end)
			if trimmedEnd < end {
				trimmedStartRaw, trimmedEndRaw := mapper.rawRange(start, trimmedEnd)
				converted.Range = SourceRange{trimmedStartRaw, trimmedEndRaw}
				converted.Text = string(source[start:trimmedEnd])
				if autoLink.Protocol != nil {
					converted.Destination = string(autoLink.Protocol) + "://" + converted.Text
				} else {
					converted.Destination = converted.Text
				}
				result = append(result, converted)
				suffixStartRaw, suffixEndRaw := mapper.rawRange(trimmedEnd, end)
				result = append(result, Node{
					Kind:  "text",
					Text:  mapper.rawText(trimmedEnd, end),
					Range: SourceRange{suffixStartRaw, suffixEndRaw},
				})
				continue
			}
		}
		result = append(result, converted)
	}
	return result
}

func safeAdjacentLinkPreviewLinks(parent gast.Node, source []byte, lower, upper int, ignored map[*gast.RawHTML]SourceRange) map[*gast.Link]bool {
	links := make(map[*gast.Link]bool)
	children := astChildren(parent)
	for index, child := range children {
		link, ok := child.(*gast.Link)
		if !ok || link.Reference != nil || !isHTTPURL(string(link.Destination)) {
			continue
		}
		linkStart := inlineNodeSourceStart(link, source, lower, upper)
		linkEnd := inlineNodeEnd(link, source, linkStart, upper)
		for next := index + 1; next < len(children); next++ {
			comment, ok := children[next].(*gast.RawHTML)
			if !ok {
				if text, isText := children[next].(*gast.Text); isText {
					textStart := inlineNodeSourceStart(text, source, linkEnd, upper)
					textEnd := inlineNodeEnd(text, source, textStart, upper)
					if textStart == textEnd {
						continue
					}
				}
				break
			}
			rangeValue, recognized := ignored[comment]
			if recognized && rangeValue.Start >= lower && rangeValue.End <= upper && string(source[rangeValue.Start:rangeValue.End]) == "<!-- linkPreview -->" && linkEnd == rangeValue.Start {
				links[link] = true
			}
			break
		}
	}
	return links
}

func hasWordAutolinkPrefix(source []byte, start int) bool {
	if start < 1 || start > len(source) {
		return false
	}
	previous, size := utf8.DecodeLastRune(source[:start])
	if unicode.IsLetter(previous) || unicode.IsNumber(previous) {
		return true
	}
	if previous != '_' || start <= size {
		return false
	}
	joined, _ := utf8.DecodeLastRune(source[:start-size])
	return unicode.IsLetter(joined) || unicode.IsNumber(joined) || joined == '_'
}

func convertInline(node gast.Node, source []byte, mapper sourceMapper, sourceRange SourceRange, start, end int) Node {
	result := Node{Range: sourceRange}
	switch n := node.(type) {
	case *gast.Text:
		result.Kind = "text"
		result.Text = mapper.rawText(start, end)
		if n.SoftLineBreak() {
			result.Attributes = map[string]string{"softLineBreak": "true"}
		}
		if n.HardLineBreak() {
			if result.Attributes == nil {
				result.Attributes = map[string]string{}
			}
			result.Attributes["hardLineBreak"] = "true"
		}
	case *gast.String:
		result.Kind = "text"
		result.Text = string(n.Value)
	case *gast.Emphasis:
		if n.Level > 1 {
			result.Kind = "strong"
		} else {
			result.Kind = "emphasis"
		}
		result.Children = convertInlineChildren(n, source, mapper, start, end)
	case *gfmast.Strikethrough:
		result.Kind = "strike"
		result.Children = convertInlineChildren(n, source, mapper, start, end)
	case *gast.CodeSpan:
		result.Kind = "code"
		result.Text = string(n.Text(source))
		result.Children = convertInlineChildren(n, source, mapper, start, end)
	case *gast.Link:
		result.Kind = "link"
		result.Destination = string(n.Destination)
		result.Title = string(n.Title)
		result.Text = plainInlineText(n, source)
		if n.Reference != nil {
			result.Attributes = map[string]string{"reference": string(n.Reference.Value), "referenceType": n.Reference.Type.String()}
		}
		result.Children = convertInlineChildren(n, source, mapper, start, end)
	case *gast.Image:
		result.Kind = "image"
		result.Destination = string(n.Destination)
		result.Title = string(n.Title)
		result.Text = plainInlineText(n, source)
		if n.Reference != nil {
			result.Attributes = map[string]string{"reference": string(n.Reference.Value), "referenceType": n.Reference.Type.String()}
		}
		result.Children = convertInlineChildren(n, source, mapper, start, end)
	case *gast.AutoLink:
		result.Kind = "link"
		result.Text = string(n.Label(source))
		result.Destination = string(n.URL(source))
		if n.AutoLinkType == gast.AutoLinkEmail && (start < 0 || end > len(source) || start >= end || source[start] != '<' || source[end-1] != '>') {
			result.Kind = "text"
			result.Text = mapper.rawText(start, end)
			result.Destination = ""
		}
	case *mathNode:
		result.Kind = mathNodeKindName(n.width)
		formulaEnd := end - n.width
		formulaStart := start + n.width
		if formulaStart <= formulaEnd && formulaEnd <= len(source) {
			result.Text = string(source[formulaStart:formulaEnd])
		}
		result.Attributes = map[string]string{"delimiterWidth": strconv.Itoa(n.width)}
	case *gast.RawHTML:
		result.Kind = "html"
		result.Text = string(n.Text(source))
	case *gfmast.TaskCheckBox:
		result.Kind = "checkbox"
		checked := n.IsChecked
		result.Checked = &checked
	default:
		result.Kind = "text"
		result.Text = mapper.rawText(start, end)
		result.Attributes = map[string]string{"ast": node.Kind().String()}
		result.Children = convertInlineChildren(node, source, mapper, start, end)
	}
	return result
}

func inlineNodeStart(node gast.Node, fallback, upper int) int {
	if textNode, ok := node.(*gast.Text); ok {
		if textNode.Segment.Start >= fallback && textNode.Segment.Start <= upper {
			return textNode.Segment.Start
		}
	}
	if raw, ok := node.(*gast.RawHTML); ok && raw.Segments != nil && raw.Segments.Len() > 0 {
		start := raw.Segments.At(0).Start
		if start >= fallback && start <= upper {
			return start
		}
	}
	if pos := node.Pos(); pos >= fallback && pos <= upper {
		return pos
	}
	return fallback
}

// TaskCheckBox has no Goldmark source position. Its AST node is created after
// the inline parser consumes "[x]", so Pos remains zero; locate its source span
// from the current list-item range before deriving neighboring inline offsets.
func inlineNodeSourceStart(node gast.Node, source []byte, fallback, upper int) int {
	if _, ok := node.(*gfmast.TaskCheckBox); ok {
		fallback = maxInt(0, fallback)
		upper = minInt(len(source), upper)
		if fallback <= upper {
			if relative := bytes.IndexByte(source[fallback:upper], '['); relative >= 0 {
				return fallback + relative
			}
		}
		return fallback
	}
	return inlineNodeStart(node, fallback, upper)
}

func inlineNodeEnd(node gast.Node, source []byte, start, upper int) int {
	if textNode, ok := node.(*gast.Text); ok {
		if textNode.Segment.Stop >= start && textNode.Segment.Stop <= upper {
			return textNode.Segment.Stop
		}
	}
	if raw, ok := node.(*gast.RawHTML); ok && raw.Segments != nil && raw.Segments.Len() > 0 {
		end := raw.Segments.At(raw.Segments.Len() - 1).Stop
		if end >= start && end <= upper {
			return end
		}
	}
	switch n := node.(type) {
	case *gast.String:
		return minInt(start+len(n.Value), upper)
	case *gast.CodeSpan:
		if end, ok := codeSpanEnd(source, start, upper); ok {
			return end
		}
	case *gast.Emphasis:
		if end, ok := delimitedInlineEnd(node, source, start, upper, '*', n.Level); ok {
			return end
		}
		if end, ok := delimitedInlineEnd(node, source, start, upper, '_', n.Level); ok {
			return end
		}
	case *gfmast.Strikethrough:
		if end, ok := delimitedInlineEnd(node, source, start, upper, '~', 2); ok {
			return end
		}
	case *gast.Link:
		if end, ok := linkInlineEnd(node, source, start, upper); ok {
			return end
		}
	case *gast.Image:
		if end, ok := linkInlineEnd(node, source, start, upper); ok {
			return end
		}
	case *gast.AutoLink:
		if end, ok := autoLinkEnd(n, source, start, upper); ok {
			return end
		}
	case *mathNode:
		if n.start >= start && n.end <= upper {
			return n.end
		}
	case *gfmast.TaskCheckBox:
		if start < len(source) && source[start] == '[' {
			if close := bytes.IndexByte(source[start:upper], ']'); close >= 0 {
				return start + close + 1
			}
		}
	}
	if node.ChildCount() > 0 {
		end := start
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			if childEnd := inlineNodeEnd(child, source, inlineNodeStart(child, start, upper), upper); childEnd > end {
				end = childEnd
			}
		}
		return minInt(end, upper)
	}
	return upper
}

func firstDescendantSourcePos(node gast.Node, fallback, upper int) int {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if childStart := inlineNodeStart(child, fallback, upper); childStart >= fallback && childStart < upper {
			return childStart
		}
		if nested := firstDescendantSourcePos(child, fallback, upper); nested > fallback {
			return nested
		}
	}
	return fallback
}

func lastDescendantSourceEnd(node gast.Node, source []byte, fallback, upper int) int {
	end := fallback
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		childStart := inlineNodeStart(child, fallback, upper)
		childEnd := inlineNodeEnd(child, source, childStart, upper)
		if child.ChildCount() > 0 {
			if descendantsEnd := lastDescendantSourceEnd(child, source, childStart, childEnd); descendantsEnd > childEnd {
				childEnd = descendantsEnd
			}
		}
		if childEnd > end {
			end = childEnd
		}
	}
	return end
}

func delimitedInlineEnd(node gast.Node, source []byte, start, upper int, marker byte, minimumRun int) (int, bool) {
	if start < 0 || start >= upper || source[start] != marker {
		return 0, false
	}
	openRun := runLength(source, start, upper, marker)
	contentStart := firstDescendantSourcePos(node, start, upper)
	openLength := contentStart - start
	if openLength <= 0 {
		openLength = minimumRun
	}
	if openLength > openRun {
		openLength = openRun
	}
	if openLength < minimumRun {
		openLength = minimumRun
	}
	contentEnd := lastDescendantSourceEnd(node, source, contentStart, upper)
	for i := contentEnd; i < upper; {
		if source[i] == '\\' {
			i += 2
			continue
		}
		if source[i] != marker {
			i++
			continue
		}
		count := runLength(source, i, upper, marker)
		if count >= openLength {
			return i + openLength, true
		}
		i += count
	}
	return 0, false
}

func codeSpanEnd(source []byte, start, upper int) (int, bool) {
	if start < 0 || start >= upper || source[start] != '`' {
		return 0, false
	}
	count := runLength(source, start, upper, '`')
	for i := start + count; i < upper; {
		if source[i] != '`' {
			i++
			continue
		}
		closing := runLength(source, i, upper, '`')
		if closing == count {
			return i + closing, true
		}
		i += closing
	}
	return 0, false
}

func linkInlineEnd(node gast.Node, source []byte, start, upper int) (int, bool) {
	labelStart := start
	if _, image := node.(*gast.Image); image && labelStart < upper && source[labelStart] == '!' {
		labelStart++
	}
	if labelStart >= upper || source[labelStart] != '[' {
		return 0, false
	}
	closeLabel := findMatchingBracket(source, labelStart, upper, '[', ']')
	if closeLabel < 0 {
		return 0, false
	}
	end := closeLabel + 1
	if end < upper && source[end] == '(' {
		if closing := findBalancedSourceParen(source, end, upper); closing >= 0 {
			return closing + 1, true
		}
	}
	if end < upper && source[end] == '[' {
		if closing := findMatchingBracket(source, end, upper, '[', ']'); closing >= 0 {
			return closing + 1, true
		}
	}
	return end, true
}

func findMatchingBracket(source []byte, start, upper int, opening, closing byte) int {
	depth := 0
	for i := start; i < upper; i++ {
		if source[i] == '\\' {
			i++
			continue
		}
		if source[i] == '`' {
			if codeEnd, ok := codeSpanEnd(source, i, upper); ok {
				i = codeEnd - 1
			}
			continue
		}
		switch source[i] {
		case opening:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func findBalancedSourceParen(source []byte, start, upper int) int {
	depth := 0
	quote := byte(0)
	for i := start; i < upper; i++ {
		if source[i] == '\\' {
			i++
			continue
		}
		if quote != 0 {
			if source[i] == quote {
				quote = 0
			}
			continue
		}
		if (source[i] == '"' || source[i] == '\'') && depth == 1 && i > start && (source[i-1] == ' ' || source[i-1] == '\t' || source[i-1] == '\n') {
			quote = source[i]
			continue
		}
		switch source[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func autoLinkEnd(node *gast.AutoLink, source []byte, start, upper int) (int, bool) {
	label := node.Label(source)
	if len(label) == 0 {
		return 0, false
	}
	searchStart := start
	if searchStart < 0 {
		searchStart = 0
	}
	if searchStart > upper {
		return 0, false
	}
	relative := bytes.Index(source[searchStart:upper], label)
	if relative < 0 {
		return 0, false
	}
	labelStart := searchStart + relative
	if labelStart > start && source[labelStart-1] == '<' && labelStart+len(label) < upper && source[labelStart+len(label)] == '>' {
		return labelStart + len(label) + 1, true
	}
	return labelStart + len(label), true
}

func plainInlineText(node gast.Node, source []byte) string {
	var out strings.Builder
	var walk func(gast.Node)
	walk = func(current gast.Node) {
		switch n := current.(type) {
		case *gast.Text:
			out.Write(n.Segment.Value(source))
		case *gast.String:
			out.Write(n.Value)
		case *gast.AutoLink:
			out.Write(n.Label(source))
		case *gast.CodeSpan:
			out.Write(n.Text(source))
		default:
			for child := current.FirstChild(); child != nil; child = child.NextSibling() {
				walk(child)
			}
		}
	}
	walk(node)
	return out.String()
}

func containsTaskCheckbox(node gast.Node) bool { return taskCheckbox(node) != nil }

func taskCheckbox(node gast.Node) *gfmast.TaskCheckBox {
	var found *gfmast.TaskCheckBox
	var walk func(gast.Node)
	walk = func(current gast.Node) {
		if found != nil {
			return
		}
		if checkbox, ok := current.(*gfmast.TaskCheckBox); ok {
			found = checkbox
			return
		}
		for child := current.FirstChild(); child != nil; child = child.NextSibling() {
			if _, nestedList := child.(*gast.List); nestedList {
				continue
			}
			walk(child)
		}
	}
	walk(node)
	return found
}

func scanLines(source string) []sourceLine {
	if source == "" {
		return nil
	}
	lines := make([]sourceLine, 0, strings.Count(source, "\n")+1)
	for start := 0; start < len(source); {
		end := start
		for end < len(source) && source[end] != '\n' && source[end] != '\r' {
			end++
		}
		contentEnd := end
		if end < len(source) {
			if source[end] == '\r' && end+1 < len(source) && source[end+1] == '\n' {
				end += 2
			} else {
				end++
			}
		}
		lines = append(lines, sourceLine{start: start, contentEnd: contentEnd, end: end, text: source[start:contentEnd]})
		start = end
	}
	return lines
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func physicalLineStart(source []byte, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	for offset > 0 && source[offset-1] != '\n' {
		offset--
	}
	return offset
}

func runLength(source []byte, start, end int, value byte) int {
	i := start
	for i < end && source[i] == value {
		i++
	}
	return i - start
}
