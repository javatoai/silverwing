package markdown

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// Adding an existing style merges intersecting/adjacent spans. Removing only
// their delimiters before wrapping avoids invalid runs such as **a****b**.
func (s *EditorState) inlineAddition(index, a, z int) (editor.Edit, int, int, error) {
	kind := []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough}[index]
	d := s.editingDocument()
	start, end := a, z
	for {
		left, right := start, end
		for b := 1; b < d.Len(); b++ {
			for _, in := range d.Inlines(b) {
				if in.Kind == kind && in.Start <= end && in.End >= start {
					start = min(start, in.Start)
					end = max(end, in.End)
				}
			}
		}
		if left == start && right == end {
			break
		}
	}
	width := []int{2, 1, 2}[index]
	var remove []editor.Edit
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind == kind && in.Start >= start && in.End <= end {
				remove = append(remove, editor.Edit{Start: in.Start, End: in.Start + width}, editor.Edit{Start: in.End - width, End: in.End})
			}
		}
	}
	slices.SortFunc(remove, func(a, b editor.Edit) int { return a.Start - b.Start })
	merged := remove[:0]
	for _, edit := range remove {
		if len(merged) > 0 && edit.Start <= merged[len(merged)-1].End {
			merged[len(merged)-1].End = max(merged[len(merged)-1].End, edit.End)
		} else {
			merged = append(merged, edit)
		}
	}
	remove = merged
	var body strings.Builder
	cursor := start
	for _, edit := range remove {
		body.WriteString(s.snapshot.OriginalSlice(cursor, edit.Start))
		cursor = edit.End
	}
	body.WriteString(s.snapshot.OriginalSlice(cursor, end))
	text := body.String()
	prefixClose, prefixOpen, suffixClose, suffixOpen := s.crossingInlineMarkers(d, kind, start, end)
	inside := prefixOpen + text + suffixClose
	markers := []string{inlineMarkers[index]}
	if index == 0 {
		markers = append(markers, "__")
	}
	if index == 1 {
		markers = append(markers, "_")
	}
	scopeStart, scopeEnd := s.inlineScope(d, start, end)
	var scopeDrafts []int
	for _, at := range s.literalFenceLines() {
		if at >= scopeStart && at < scopeEnd {
			scopeDrafts = append(scopeDrafts, at-scopeStart)
		}
	}
	original := s.inlineSignature(s.source[scopeStart:scopeEnd], scopeDrafts)
	for variant := 0; variant < len(markers)+1; variant++ {
		marker := markers[min(variant, len(markers)-1)]
		separator := ""
		if variant == len(markers) {
			marker = inlineMarkers[index]
			separator = "<!-- -->"
		}
		outsidePrefix, outsideSuffix := prefixClose, suffixOpen
		if separator != "" && prefixClose != "" {
			outsidePrefix += separator
		}
		if separator != "" && suffixOpen != "" {
			outsideSuffix = separator + outsideSuffix
		}
		encoded, _ := wrapInline(inside, marker)
		replacement := outsidePrefix + encoded + outsideSuffix
		normalized := normalizedText(inside)
		left := len(normalized) - len(strings.TrimLeftFunc(normalized, unicode.IsSpace))
		right := len(strings.TrimRightFunc(normalized, unicode.IsSpace))
		mapAt := func(at int) int {
			at = commandOffset(at, remove) - start + len(normalizedText(prefixOpen))
			if at > right {
				at += len(marker)
			}
			if at >= left {
				at += len(marker)
			}
			return start + len(normalizedText(outsidePrefix)) + at
		}
		edit := editor.Edit{Start: start, End: end, Text: replacement}
		candidate := s.source[scopeStart:start] + normalizedText(replacement) + s.source[end:scopeEnd]
		drafts := moveFenceDrafts(scopeDrafts, start-scopeStart, end-scopeStart, len(normalizedText(replacement))-(end-start))
		parsed := parser.New(s.view.ext)
		parsed.SetWithPolicy(candidate, s.sourcePrefixFor(candidate), drafts)
		targetStart := start - scopeStart + len(normalizedText(outsidePrefix)) + left
		targetEnd := targetStart + 2*len(marker) + right - left
		found := false
		for b := 1; b < parsed.Len(); b++ {
			for _, in := range parsed.Inlines(b) {
				if in.Kind == kind && in.Start == targetStart && in.End == targetEnd {
					found = true
				}
			}
		}
		if !found {
			continue
		}
		signature := signatureOfInlineDocument(parsed)
		valid := signature.plain == original.plain && signature.links == original.links
		for i := range signature.styles {
			if i != index && signature.styles[i] != original.styles[i] {
				valid = false
			}
		}
		if valid {
			return edit, mapAt(a), mapAt(z), nil
		}
	}
	return editor.Edit{}, 0, 0, errors.New("该选区的 Markdown 样式无法安全转换")
}

func (s *EditorState) inlineScope(d *parser.Document, start, end int) (int, int) {
	for i := 1; i < d.Len(); i = d.Block(i).Next {
		left, right := s.blockRange(d.Block(i))
		if start >= left && end <= right {
			return left, right
		}
	}
	return 0, len(s.source)
}

func (s *EditorState) inlineVisibleOverlap(start, end int) bool {
	d := s.editingDocument()
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind != parser.Text && in.Kind != parser.Code {
				continue
			}
			left, right := in.Start, in.End
			if in.Kind == parser.Code {
				width := 0
				for left+width < right && s.source[left+width] == '`' {
					width++
				}
				left += width
				right -= width
				if left < right && s.source[left] == ' ' && s.source[right-1] == ' ' && strings.Trim(s.source[left:right], " ") != "" {
					left++
					right--
				}
			}
			if in.Text != "" && left < end && right > start {
				return true
			}
		}
	}
	return false
}

// Split only crossed formatting containers; keep their original source aliases
// on each side. A code span is indivisible markup, so an inner style splits it
// at both selection boundaries rather than writing Markdown inside its body.
func (s *EditorState) crossingInlineMarkers(d *parser.Document, target parser.InlineKind, start, end int) (prefixClose, prefixOpen, suffixClose, suffixOpen string) {
	type crossed struct {
		in          parser.Inline
		open, close string
	}
	var first, last []crossed
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind == target {
				continue
			}
			index := -1
			for i, k := range []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Code} {
				if in.Kind == k {
					index = i
				}
			}
			if index < 0 {
				continue
			}
			width := []int{2, 1, 2, 0}[index]
			if index == 3 {
				for in.Start+width < in.End && s.source[in.Start+width] == '`' {
					width++
				}
			}
			left, right := in.Start+width, in.End-width
			if index == 3 && left < right && s.source[left] == ' ' && s.source[right-1] == ' ' && strings.Trim(s.source[left:right], " ") != "" {
				left++
				right--
			}
			item := crossed{in, s.source[in.Start:left], s.source[right:in.End]}
			enclosing := in.Start < start && in.End > end
			if in.Start < start && in.End > start && (in.End <= end || index == 3 && enclosing) {
				first = append(first, item)
			}
			if in.Start < end && in.End > end && (in.Start >= start || index == 3 && enclosing) {
				last = append(last, item)
			}
		}
	}
	slices.SortFunc(first, func(a, b crossed) int { return a.in.Start - b.in.Start })
	for _, item := range first {
		prefixOpen += item.open
	}
	for i := len(first) - 1; i >= 0; i-- {
		prefixClose += first[i].close
	}
	slices.SortFunc(last, func(a, b crossed) int { return a.in.Start - b.in.Start })
	for _, item := range last {
		suffixOpen += item.open
	}
	for i := len(last) - 1; i >= 0; i-- {
		suffixClose += last[i].close
	}
	return
}

type inlineSemanticSignature struct {
	plain  string
	styles [4]string
	links  string
}

// Check the final batch in the real document context once. A block-local parse
// cannot resolve references defined elsewhere, and may accept crossed links.
func (s *EditorState) applyInlineStyleEdits(index int, edits []editor.Edit, anchor, caret int) error {
	before, after, err := s.inlineEditSignatures(edits)
	if err != nil {
		return err
	}
	valid := before.plain == after.plain && before.links == after.links
	for i := range before.styles {
		if i != index && before.styles[i] != after.styles[i] {
			valid = false
		}
	}
	if !valid {
		return errors.New("该选区的 Markdown 样式无法安全转换")
	}
	return s.ApplyEdits(edits, anchor, caret, s.snapshot.Version())
}

func (s *EditorState) inlineEditSignatures(edits []editor.Edit) (inlineSemanticSignature, inlineSemanticSignature, error) {
	ordered := slices.Clone(edits)
	slices.SortFunc(ordered, func(a, b editor.Edit) int { return a.Start - b.Start })
	var candidate strings.Builder
	at := 0
	for _, edit := range ordered {
		if edit.Start < at || edit.End < edit.Start || edit.End > len(s.source) {
			return inlineSemanticSignature{}, inlineSemanticSignature{}, errors.New("Markdown style edits overlap")
		}
		candidate.WriteString(s.source[at:edit.Start])
		candidate.WriteString(normalizedText(edit.Text))
		at = edit.End
	}
	candidate.WriteString(s.source[at:])
	drafts := slices.Clone(s.literalFenceLines())
	for _, edit := range slices.Backward(ordered) {
		drafts = moveFenceDrafts(drafts, edit.Start, edit.End, len(normalizedText(edit.Text))-(edit.End-edit.Start))
	}
	parsed := parser.New(s.view.ext)
	parsed.SetWithPolicy(candidate.String(), s.sourcePrefixFor(candidate.String()), drafts)
	return signatureOfInlineDocument(s.editingDocument()), signatureOfInlineDocument(parsed), nil
}

func (s *EditorState) inlineSignature(source string, drafts []int) inlineSemanticSignature {
	d := parser.New(s.view.ext)
	d.SetWithPolicy(source, s.sourcePrefixFor(source), drafts)
	return signatureOfInlineDocument(d)
}
func signatureOfInlineDocument(d *parser.Document) inlineSemanticSignature {
	var result inlineSemanticSignature
	var plain strings.Builder
	var links strings.Builder
	var styles [4]strings.Builder
	for b := 1; b < d.Len(); b++ {
		ins := d.Inlines(b)
		// A prefix maximum over starts answers enclosure queries without
		// rescanning every style owner for each text leaf in the paragraph.
		var coverage [4][][2]int
		for _, in := range ins {
			for i, kind := range []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Code} {
				if in.Kind == kind {
					coverage[i] = append(coverage[i], [2]int{in.Start, in.End})
					break
				}
			}
		}
		for i := range coverage {
			ranges := coverage[i]
			slices.SortFunc(ranges, func(a, b [2]int) int {
				if a[0] < b[0] {
					return -1
				}
				if a[0] > b[0] {
					return 1
				}
				return 0
			})
			for j := 1; j < len(ranges); j++ {
				ranges[j][1] = max(ranges[j][1], ranges[j-1][1])
			}
		}
		for _, in := range ins {
			if in.Kind == parser.FootnoteRef {
				links.WriteString("footnote:")
				links.WriteString(in.Text)
				links.WriteByte(0)
			}
			if in.Kind == parser.Link || in.Kind == parser.Image {
				links.WriteString(in.Dest)
				links.WriteByte(0)
				links.WriteString(in.Title)
				links.WriteByte(0)
			}
			if in.Kind != parser.Text && in.Kind != parser.Code {
				continue
			}
			plain.WriteString(in.Text)
			for i, ranges := range coverage {
				at := sort.Search(len(ranges), func(j int) bool { return ranges[j][0] > in.Start }) - 1
				if at >= 0 && ranges[at][1] >= in.End {
					styles[i].WriteString(in.Text)
				}
			}
		}
	}
	result.plain = plain.String()
	result.links = links.String()
	for i := range styles {
		result.styles[i] = styles[i].String()
	}
	return result
}

// Removing a style can cross another style too. Plan the two remaining styled
// sides against immutable source snapshots, then save only the affected outer
// container in the real Buffer's one transaction and undo history.
func (s *EditorState) inlineRemoval(index, a, z int) (editor.Edit, int, int, error) {
	start, end, left, right, ok := s.inlineContainer(index, a, z)
	if !ok {
		return editor.Edit{}, 0, 0, errors.New("Markdown style is no longer available")
	}
	a, z = max(a, left), min(z, right)
	kind := []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough}[index]
	width := []int{2, 1, 2}[index]
	d := s.editingDocument()
	var remove []editor.Edit
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind == kind && in.Start >= left && in.End <= right {
				remove = append(remove, editor.Edit{Start: in.Start, End: in.Start + width}, editor.Edit{Start: in.End - width, End: in.End})
			}
		}
	}
	slices.SortFunc(remove, func(a, b editor.Edit) int { return a.Start - b.Start })
	var body strings.Builder
	cursor := left
	for _, edit := range remove {
		if edit.Start < cursor {
			continue
		}
		body.WriteString(s.snapshot.OriginalSlice(cursor, edit.Start))
		cursor = edit.End
	}
	body.WriteString(s.snapshot.OriginalSlice(cursor, right))
	raw := body.String()
	from, to := commandOffset(a, remove)-left, commandOffset(z, remove)-left
	planner := func(text string) *EditorState {
		snapshot := editor.NewBufferPreservingLineEndings(text).Snapshot()
		p := &EditorState{source: snapshot.String(), snapshot: snapshot, view: newState(), opts: s.opts}
		p.view.ext = s.view.ext
		return p
	}
	p := planner(raw)
	apply := func(p *EditorState, edit editor.Edit) string {
		return p.snapshot.OriginalSlice(0, edit.Start) + edit.Text + p.snapshot.OriginalSlice(edit.End, len(p.source))
	}
	if to < len(p.source) && strings.TrimSpace(p.source[to:]) != "" && p.inlineVisibleOverlap(to, len(p.source)) {
		edit, _, _, err := p.inlineAddition(index, to, len(p.source))
		if err != nil {
			return editor.Edit{}, 0, 0, err
		}
		raw = apply(p, edit)
		p = planner(raw)
	}
	if from > 0 && strings.TrimSpace(p.source[:from]) != "" && p.inlineVisibleOverlap(0, from) {
		edit, _, _, err := p.inlineAddition(index, 0, from)
		if err != nil {
			return editor.Edit{}, 0, 0, err
		}
		raw = apply(p, edit)
		delta := len(normalizedText(edit.Text)) - (edit.End - edit.Start)
		from = edit.Start + len(normalizedText(edit.Text))
		to += delta
	}
	scopeStart, scopeEnd := s.inlineScope(d, start, end)
	var scopeDrafts []int
	for _, at := range s.literalFenceLines() {
		if at >= scopeStart && at < scopeEnd {
			scopeDrafts = append(scopeDrafts, at-scopeStart)
		}
	}
	candidate := s.source[scopeStart:start] + normalizedText(raw) + s.source[end:scopeEnd]
	drafts := moveFenceDrafts(scopeDrafts, start-scopeStart, end-scopeStart, len(normalizedText(raw))-(end-start))
	original, changed := s.inlineSignature(s.source[scopeStart:scopeEnd], scopeDrafts), s.inlineSignature(candidate, drafts)
	if original.plain != changed.plain || original.links != changed.links {
		return editor.Edit{}, 0, 0, errors.New("该选区的 Markdown 文字无法安全转换")
	}
	for i := range original.styles {
		if i != index && original.styles[i] != changed.styles[i] {
			return editor.Edit{}, 0, 0, errors.New("该选区的其他 Markdown 样式无法安全保留")
		}
	}
	return editor.Edit{Start: start, End: end, Text: raw}, start + from, start + to, nil
}
