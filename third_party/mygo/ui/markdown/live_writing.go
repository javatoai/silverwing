package markdown

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type writingItem struct {
	index, list, parent, previous int
	start, end, lineEnd, content  int
	marker                        parser.ListItemSource
	gap, taskGap                  string
	task                          bool
}

func (s *EditorState) writingDocument() *parser.Document {
	return s.editingDocument()
}

// Metadata is kept literal even though the Markdown parser deliberately does
// not implement YAML/TOML frontmatter. An unfinished opening region stays raw.
func (s *EditorState) writingFrontmatter(at int) bool {
	if prefix := s.sourcePrefixEnd(); prefix > 0 {
		return at < prefix
	}
	start := 0
	if strings.HasPrefix(s.source, "\ufeff") {
		start = len("\ufeff")
	}
	end := codeLineEnd(s.source, start)
	marker := s.source[start:end]
	if marker != "---" && marker != "+++" {
		return false
	}
	for line := min(end+1, len(s.source)); line < len(s.source); {
		z := codeLineEnd(s.source, line)
		text := s.source[line:z]
		if text == marker || marker == "---" && text == "..." {
			return at <= z
		}
		line = z + 1
	}
	return true
}

func (s *EditorState) writingOwns(b *parser.Block, at int) bool {
	a, z := s.blockRange(b)
	return at >= a && (at < z || at == z && at == len(s.source) && !strings.HasSuffix(s.source, "\n"))
}

func (s *EditorState) writingLiteral(d *parser.Document, at int) bool {
	if s.writingFrontmatter(at) {
		return true
	}
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		switch b.Kind {
		case parser.CodeBlock, parser.HTMLBlock, parser.MathBlock, parser.Table, parser.ThematicBreak:
			if s.writingOwns(b, at) {
				return true
			}
		case parser.Heading:
			if b.Level <= 2 && s.writingOwns(b, at) {
				line := strings.LastIndexByte(s.source[:at], '\n') + 1
				if line > strings.LastIndexByte(s.source[:b.Start], '\n')+1 {
					underline := strings.Trim(s.source[line:codeLineEnd(s.source, line)], " \t>")
					if len(underline) > 1 && strings.Trim(underline, "-") == "" || underline != "" && strings.Trim(underline, "=") == "" {
						return true
					}
				}
			}
		}
	}
	return false
}

// Selection endpoints are exclusive. Only a collapsed caret belongs to its
// endpoint; a nonempty selection ends at its last selected UTF-8 rune.
func (s *EditorState) writingSelectionEnd(a, z int) int {
	if a < z {
		return byteBoundary(s.source, z-1)
	}
	return z
}

// Raw Tab is reserved for a selection wholly owned by one literal region.
// A mixed range must not fall back to replacing list content with a tab just
// because one directional caret happens to land in code, HTML or a table.
func (s *EditorState) writingLiteralRange(d *parser.Document, a, z int) bool {
	last := s.writingSelectionEnd(a, z)
	if !s.writingLiteral(d, a) || !s.writingLiteral(d, last) {
		return false
	}
	if a == z || s.writingFrontmatter(a) && s.writingFrontmatter(last) {
		return true
	}
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		switch b.Kind {
		case parser.CodeBlock, parser.HTMLBlock, parser.MathBlock, parser.Table, parser.ThematicBreak, parser.Heading:
			if s.writingOwns(b, a) && s.writingOwns(b, last) {
				return true
			}
		}
	}
	return false
}

func (s *EditorState) writingStructure(d *parser.Document, at int) int {
	structure := -1
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if (b.Kind == parser.Item || b.Kind == parser.Quote) && s.writingOwns(b, at) {
			structure = i
		}
	}
	return structure
}

func (s *EditorState) readWritingItem(d *parser.Document, index int) writingItem {
	b := d.Block(index)
	marker, _ := d.ListItemSource(index)
	start, end := s.blockRange(b)
	item := writingItem{index: index, list: b.Parent, parent: -1, previous: -1, start: start, end: end,
		lineEnd: codeLineEnd(s.source, marker.MarkerStart), marker: marker}
	if parent := d.Block(item.list).Parent; parent >= 0 && d.Block(parent).Kind == parser.Item {
		item.parent = parent
	}
	for i := item.list + 1; i < index; i = d.Block(i).Next {
		if d.Block(i).Kind == parser.Item {
			item.previous = i
		}
	}
	item.content = marker.MarkerEnd
	for item.content < item.lineEnd && (s.source[item.content] == ' ' || s.source[item.content] == '\t') {
		item.content++
	}
	item.gap = s.source[marker.MarkerEnd:item.content]
	if item.gap == "" {
		item.gap = " "
	}
	// Empty checkboxes are intentionally NoTask in the parser. Only inspect
	// this confirmed Item's prefix, never arbitrary paragraph/code text.
	if item.content+3 <= item.lineEnd && s.source[item.content] == '[' && s.source[item.content+2] == ']' &&
		(s.source[item.content+1] == ' ' || s.source[item.content+1] == 'x' || s.source[item.content+1] == 'X') &&
		(item.content+3 == item.lineEnd || s.source[item.content+3] == ' ' || s.source[item.content+3] == '\t') {
		item.task = true
		item.content += 3
		gap := item.content
		for item.content < item.lineEnd && (s.source[item.content] == ' ' || s.source[item.content] == '\t') {
			item.content++
		}
		item.taskGap = s.source[gap:item.content]
		if item.taskGap == "" {
			item.taskGap = " "
		}
	}
	return item
}

// writingKey runs after the native IME and table handlers. It changes source
// ranges through the same Buffer and never installs another input client.
func (s *EditorState) writingKey(key ui.Key, mods ui.Modifiers) bool {
	if !s.opts.ContinueBlocks || s.SourceMode || s.opts.ReadOnly || s.composing || mods&^ui.Shift != 0 ||
		key != ui.KeyEnter && key != ui.KeyTab && key != ui.KeyBackspace || key == ui.KeyBackspace && mods != 0 {
		return false
	}
	d := s.writingDocument()
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	last := z
	if key == ui.KeyTab {
		last = s.writingSelectionEnd(a, z)
	}
	if s.writingLiteral(d, a) || s.writingLiteral(d, last) {
		return false
	}
	structure := s.writingStructure(d, a)
	if key == ui.KeyTab {
		return s.writingMoveItems(d, a, z, mods&ui.Shift == 0)
	}
	if structure < 0 {
		if a == z && mods == 0 && s.writingAdjacentEmptyDraft(d, a, key, s.snapshot.Version()) {
			return true
		}
		return false
	}
	if s.writingStructure(d, z) != structure {
		return false // cross-structure selections retain their explicit raw range
	}
	version := s.snapshot.Version()
	if d.Block(structure).Kind == parser.Quote {
		return s.writingQuote(d, structure, a, z, key, mods, version)
	}
	item := s.readWritingItem(d, structure)
	if a == z && mods == 0 && s.writingEmptyItemDraft(d, item, a, key, version) {
		return true
	}
	empty := strings.Trim(s.source[item.content:item.end], " \t\n") == ""
	if a == z && (key == ui.KeyEnter && mods == 0 && empty || key == ui.KeyBackspace && (empty || a == item.content)) {
		if item.parent >= 0 {
			if parent := s.readWritingItem(d, item.parent); empty && parent.start == item.start {
				after := editorSelection{item.marker.MarkerStart, item.marker.MarkerStart}
				return s.applyWriting(version, []editor.Edit{{Start: item.marker.MarkerStart, End: item.content}}, &after)
			}
			return s.writingMoveItems(d, a, z, false)
		}
		// Remove this level's marker and optional checkbox, keeping its outer
		// quote/list prefix and the rest of the document byte-for-byte.
		start, end := item.marker.MarkerStart, item.content
		after := editorSelection{start, start}
		edits := []editor.Edit{{Start: start, End: end}}
		if s.opts.SmartEditing && d.Block(item.index).Ordered {
			edits = append(edits, s.followingNumberEdits(d, item, -1)...)
		}
		return s.applyWriting(version, edits, &after)
	}
	if key != ui.KeyEnter {
		return false
	}
	a, z = s.writingEnterRange(a, z)
	prefix := codeContinuationPrefix(s.source[item.start:item.marker.MarkerStart])
	if mods&ui.Shift != 0 {
		prefix += strings.Repeat(" ", item.marker.Padding)
	} else {
		b := d.Block(item.index)
		marker := string(b.Marker)
		if b.Ordered {
			if b.Number >= 999999999 {
				return false // the parser accepts at most nine marker digits
			}
			width := item.marker.MarkerEnd - item.marker.MarkerStart - 1
			marker = fmt.Sprintf("%0*d%c", width, b.Number+1, b.Marker)
		}
		prefix += marker + item.gap
		if item.task {
			prefix += "[ ]" + item.taskGap
		}
	}
	text := s.buffer.LineEnding() + prefix
	after := editorSelection{a + len(normalizedText(text)), a + len(normalizedText(text))}
	edits := []editor.Edit{{Start: a, End: z, Text: text}}
	if s.opts.SmartEditing && mods&ui.Shift == 0 && d.Block(item.index).Ordered {
		edits = append(edits, s.followingNumberEdits(d, item, 1)...)
	}
	return s.applyWriting(version, edits, &after)
}

// A setext underline can be omitted from the Item's final content range. Only
// its immediately adjacent raw row is eligible for the same narrow draft rule;
// a following AST sibling bounds it, so earlier items cannot claim later text.
func (s *EditorState) writingAdjacentEmptyDraft(d *parser.Document, at int, key ui.Key, version uint64) bool {
	if key != ui.KeyEnter && key != ui.KeyBackspace {
		return false
	}
	line := strings.LastIndexByte(s.source[:at], '\n') + 1
	for i := d.Len() - 1; i > 0; i-- {
		b := d.Block(i)
		if b.Kind != parser.Item {
			continue
		}
		item := s.readWritingItem(d, i)
		if item.end != line {
			continue
		}
		if b.Next < d.Len() {
			nextLine := strings.LastIndexByte(s.source[:d.Block(b.Next).Start], '\n') + 1
			if line >= nextLine {
				continue
			}
		}
		if s.writingEmptyItemDraft(d, item, at, key, version) {
			return true
		}
	}
	return false
}

// A single '-' typed on a deeper, otherwise empty row of a confirmed Item
// temporarily parses as a setext underline, not as an Item. This narrowly
// recognizes that writing draft; ordinary headings/rules and paragraph text
// do not enter it. Real empty nested Items use the normal AST path above.
func (s *EditorState) writingEmptyItemDraft(d *parser.Document, item writingItem, at int, key ui.Key, version uint64) bool {
	if key != ui.KeyEnter && key != ui.KeyBackspace {
		return false
	}
	line := strings.LastIndexByte(s.source[:at], '\n') + 1
	end := codeLineEnd(s.source, line)
	if line <= item.start || at != end {
		return false
	}
	depth := writingQuoteDepth(d, item.index)
	base, ok := writingPrefixEnd(s.source, line, end, depth)
	parentBase, parentOK := writingPrefixEnd(s.source, item.start, item.lineEnd, depth)
	if !ok || !parentOK {
		return false
	}
	marker := base
	for marker < end && (s.source[marker] == ' ' || s.source[marker] == '\t') {
		marker++
	}
	if marker >= end || s.source[marker] != '-' || strings.Trim(s.source[marker+1:end], " \t") != "" {
		return false
	}
	column := writingColumn(s.source[line:marker]) - writingColumn(s.source[line:base])
	parentColumn := writingColumn(s.source[item.start:item.marker.MarkerStart]) - writingColumn(s.source[item.start:parentBase])
	if column < parentColumn+item.marker.Padding {
		return false
	}
	next, replacement, consumed := writingRemoveIndent(s.source, base, marker, column-parentColumn, writingColumn(s.source[line:base]))
	if consumed != column-parentColumn {
		return false
	}
	return s.applyWriting(version, []editor.Edit{{Start: base, End: next, Text: replacement}}, nil)
}

func (s *EditorState) writingEnterRange(a, z int) (int, int) {
	a, z = s.protectLinkEdit(a, z)
	if a == z {
		for _, link := range s.editableLinks() {
			if a == link.labelEnd {
				a, z = link.end, link.end
			} else if a == link.labelStart {
				a, z = link.start, link.start
			}
		}
	}
	return a, z
}

func (s *EditorState) writingQuote(d *parser.Document, index, a, z int, key ui.Key, mods ui.Modifiers, version uint64) bool {
	lineStart := strings.LastIndexByte(s.source[:a], '\n') + 1
	lineEnd := codeLineEnd(s.source, a)
	// The innermost real Quote owns the last prefix marker on this row.
	marker, content := -1, lineStart
	scan := lineStart
	if start := d.Block(index).Start; start >= lineStart && start < lineEnd {
		scan = start
	}
	for at := scan; at < lineEnd; at++ {
		ch := s.source[at]
		if ch == '>' {
			marker, content = at, at+1
		} else if ch != ' ' && ch != '\t' {
			break
		}
	}
	for content < lineEnd && (s.source[content] == ' ' || s.source[content] == '\t') {
		content++
	}
	empty := strings.Trim(s.source[content:lineEnd], " \t") == ""
	if marker >= 0 && a == z && (key == ui.KeyEnter && mods == 0 && empty || key == ui.KeyBackspace && (empty || a == content)) {
		after := editorSelection{marker, marker}
		return s.applyWriting(version, []editor.Edit{{Start: marker, End: content}}, &after)
	}
	if key != ui.KeyEnter {
		return false
	}
	prefix := ""
	if marker >= 0 {
		prefix = s.source[lineStart:content]
	} else {
		// A lazy continuation has no literal > on this physical row. Use
		// the confirmed quote's opening prefix rather than inventing nesting.
		b := d.Block(index)
		start := strings.LastIndexByte(s.source[:b.Start], '\n') + 1
		end := min(b.Start+1, len(s.source))
		for end < len(s.source) && (s.source[end] == ' ' || s.source[end] == '\t') {
			end++
		}
		prefix = s.source[start:end]
	}
	prefix = codeContinuationPrefix(prefix)
	a, z = s.writingEnterRange(a, z)
	text := s.buffer.LineEnding() + prefix
	after := editorSelection{a + len(normalizedText(text)), a + len(normalizedText(text))}
	return s.applyWriting(version, []editor.Edit{{Start: a, End: z, Text: text}}, &after)
}

func writingItemAncestor(d *parser.Document, index int) int {
	for index >= 0 {
		if d.Block(index).Kind == parser.Item {
			return index
		}
		index = d.Block(index).Parent
	}
	return -1
}

func (s *EditorState) writingItemsAt(d *parser.Document, a, z int) []writingItem {
	first := writingItemAncestor(d, s.writingStructure(d, a))
	if z > a {
		z = s.writingSelectionEnd(a, z)
		// Selected separator whitespace belongs to no following block. Find
		// the last selected Item without changing the actual range or bytes.
		for z > a && (s.source[z] == '\n' || s.source[z] == ' ' || s.source[z] == '\t') {
			z = byteBoundary(s.source, z-1)
		}
	}
	last := writingItemAncestor(d, s.writingStructure(d, z))
	if first < 0 || last < 0 {
		return nil
	}
	for left := first; left >= 0; left = writingItemAncestor(d, d.Block(d.Block(left).Parent).Parent) {
		list := d.Block(left).Parent
		for right := last; right >= 0; right = writingItemAncestor(d, d.Block(d.Block(right).Parent).Parent) {
			if d.Block(right).Parent != list {
				continue
			}
			var items []writingItem
			for i := left; i <= right; i = d.Block(i).Next {
				if d.Block(i).Kind != parser.Item || d.Block(i).Parent != list {
					return nil
				}
				items = append(items, s.readWritingItem(d, i))
			}
			return items
		}
	}
	return nil
}

func writingQuoteDepth(d *parser.Document, index int) int {
	depth := 0
	for index = d.Block(index).Parent; index >= 0; index = d.Block(index).Parent {
		if d.Block(index).Kind == parser.Quote {
			depth++
		}
	}
	return depth
}

func writingPrefixEnd(source string, start, end, quotes int) (int, bool) {
	at := start
	if at == 0 && strings.HasPrefix(source, "\ufeff") {
		at += len("\ufeff")
	}
	for quotes > 0 {
		for at < end && (source[at] == ' ' || source[at] == '\t') {
			at++
		}
		if at >= end || source[at] != '>' {
			return at, false
		}
		at++
		if at < end && (source[at] == ' ' || source[at] == '\t') {
			at++
		}
		quotes--
	}
	return at, true
}

func writingColumn(text string) int {
	column := 0
	for _, ch := range text {
		if ch == '\t' {
			column += 4 - column%4
		} else if ch != '\ufeff' {
			column++
		}
	}
	return column
}

func writingRemoveIndent(source string, start, end, columns, column int) (int, string, int) {
	remaining := columns
	for start < end && remaining > 0 {
		ch := source[start]
		if ch != ' ' && ch != '\t' {
			break
		}
		width := 1
		if ch == '\t' {
			width = 4 - column%4
		}
		start++
		if width > remaining {
			return start, strings.Repeat(" ", width-remaining), columns
		}
		remaining -= width
		column += width
	}
	return start, "", columns - remaining
}

func (s *EditorState) writingMoveItems(d *parser.Document, a, z int, indent bool) bool {
	items := s.writingItemsAt(d, a, z)
	if len(items) == 0 {
		return false
	}
	first := items[0]
	if indent && first.previous < 0 || !indent && first.parent < 0 {
		return true // no legal AST destination; never fall through to literal Tab
	}
	columns := 0
	if indent {
		previous := s.readWritingItem(d, first.previous)
		columns = previous.marker.Indent + previous.marker.Padding - first.marker.Indent
		if columns <= 0 {
			return true
		}
	}
	var edits []editor.Edit
	for _, item := range items {
		remove := columns
		if !indent {
			parent := s.readWritingItem(d, item.parent)
			if parent.start == item.start {
				return true // a same-line nested marker cannot be moved by indentation
			}
			remove = parent.marker.Padding + item.marker.Indent
		}
		depth := writingQuoteDepth(d, item.index)
		for line := item.start; line < item.end; {
			end := min(codeLineEnd(s.source, line), item.end)
			at, ok := writingPrefixEnd(s.source, line, end, depth)
			if !ok {
				return true // a partial/lazy quote prefix cannot be safely rewritten
			}
			if indent {
				if line == item.start {
					at = item.marker.MarkerStart
				}
				edits = append(edits, editor.Edit{Start: at, End: at, Text: strings.Repeat(" ", columns)})
			} else {
				limit := end
				if line == item.start {
					limit = item.marker.MarkerStart
				}
				next, replacement, consumed := writingRemoveIndent(s.source, at, limit, remove, writingColumn(s.source[line:at]))
				if line == item.start && consumed < remove {
					return true
				}
				if next > at {
					edits = append(edits, editor.Edit{Start: at, End: next, Text: replacement})
				}
			}
			line = end + 1
		}
	}
	if indent && !s.writingNestedAt(items, d.Block(first.previous).Start, edits) {
		// An ordered marker other than 1 cannot interrupt the predecessor's
		// open paragraph. Add only the separator needed to make this an actual
		// child list, retaining the item's original number and the whole subtree.
		prefix := codeContinuationPrefix(s.source[first.start:first.marker.MarkerStart])
		separator := editor.Edit{Start: first.start, End: first.start, Text: prefix + s.buffer.LineEnding()}
		edits = append([]editor.Edit{separator}, edits...)
		if !s.writingNestedAt(items, d.Block(first.previous).Start, edits) {
			return true // a legal AST move could not be formed without broader edits
		}
	}
	return s.applyWriting(s.snapshot.Version(), edits, nil)
}

// Validate a proposed indent with an independent parser. This is a structural
// key action, not a per-keystroke reparsing path, and never touches the view's
// parser or the Buffer until all selected Items have the intended parent.
func (s *EditorState) writingNestedAt(items []writingItem, previous int, edits []editor.Edit) bool {
	text := s.source
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		text = text[:edit.Start] + normalizedText(edit.Text) + text[edit.End:]
	}
	mapAt := func(at int) int {
		delta := 0
		for _, edit := range edits {
			if edit.End <= at {
				delta += len(normalizedText(edit.Text)) - (edit.End - edit.Start)
			}
		}
		return at + delta
	}
	d := parser.New(s.view.ext)
	var drafts []int
	for _, at := range s.literalFenceLines() {
		drafts = append(drafts, sourceLineStart(text, mapAt(at)))
	}
	d.SetWithPolicy(text, s.sourcePrefixFor(text), drafts)
	parent := mapAt(previous)
	for _, item := range items {
		marker, found := mapAt(item.marker.MarkerStart), false
		for i := 1; i < d.Len(); i++ {
			b := d.Block(i)
			if b.Kind != parser.Item || b.Start != marker {
				continue
			}
			owner := d.Block(b.Parent).Parent
			if owner >= 0 && d.Block(owner).Kind == parser.Item && d.Block(owner).Start == parent {
				found = true
			}
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *EditorState) applyWriting(version uint64, edits []editor.Edit, desired *editorSelection) bool {
	if s.opts.ReadOnly || s.SourceMode || !s.opts.ContinueBlocks || s.composing {
		return true
	}
	s.sync()
	if s.snapshot.Version() != version {
		s.report(errors.New("Markdown source changed before the writing action could be applied"))
		return true
	}
	before, after := editorSelection{s.anchor, s.caret}, editorSelection{}
	var nextDrafts []int
	var nextPairs []autoPair
	s.buffer.EndUndoGroup()
	result := s.buffer.ApplyTransaction(editor.Transaction{Version: version, Edits: edits, Kind: editor.EditOther, Owner: s,
		SelectionBefore: s.writingHistory(before, s.fenceDrafts), SelectionAfterEdit: func(snapshot editor.Snapshot, changes []editor.Change) any {
			if desired != nil {
				after = *desired
			} else {
				after = editorSelection{mapEditorOffset(before.Anchor, changes), mapEditorOffset(before.Caret, changes)}
			}
			after.Anchor, after.Caret = byteBoundary(snapshot.String(), after.Anchor), byteBoundary(snapshot.String(), after.Caret)
			nextDrafts = s.draftsAfterEdit(snapshot.String(), changes, s.fenceDrafts, -1)
			nextPairs = s.pairsAfterEdit(snapshot.String(), changes, s.autoPairs)
			return s.writingHistory(after, nextDrafts, nextPairs)
		}})
	s.buffer.EndUndoGroup()
	if !result.Applied {
		if result.Rejected {
			s.report(errors.New("Markdown edit exceeds the source size limit"))
		} else {
			s.report(errors.New("Markdown source changed before the writing action could be applied"))
		}
		return true
	}
	s.snapshot = s.buffer.Snapshot()
	s.source = s.snapshot.String()
	s.fenceDrafts, s.autoPairs = nextDrafts, nextPairs
	s.anchor, s.caret, s.affinity = after.Anchor, after.Caret, ui.TextDownstream
	s.reveal, s.hasDesired = true, false
	if s.snapshot.Version() != version {
		s.notifyChanged()
	}
	return true
}

func (s *EditorState) followingNumberEdits(d *parser.Document, item writingItem, delta int) []editor.Edit {
	var edits []editor.Edit
	expected := d.Block(item.index).Number + 1
	for next := d.Block(item.index).Next; next < d.Len(); next = d.Block(next).Next {
		b := d.Block(next)
		if b.Parent != item.list || b.Kind != parser.Item || !b.Ordered || b.Number != expected || b.Marker != d.Block(item.index).Marker {
			break
		}
		number := b.Number + delta
		if number < 0 || number > 999999999 {
			break
		}
		sibling := s.readWritingItem(d, next)
		digits := fmt.Sprintf("%0*d", sibling.marker.MarkerEnd-sibling.marker.MarkerStart-1, number)
		edits = append(edits, editor.Edit{Start: sibling.marker.MarkerStart, End: sibling.marker.MarkerEnd - 1, Text: digits})
		if growth := len(digits) - (sibling.marker.MarkerEnd - sibling.marker.MarkerStart - 1); growth != 0 {
			edits = append(edits, s.renumberContinuationEdits(d, sibling, growth)...)
		}
		expected++
	}
	return edits
}

func (s *EditorState) renumberContinuationEdits(d *parser.Document, item writingItem, growth int) []editor.Edit {
	depth := writingQuoteDepth(d, item.index)
	first, _ := writingPrefixEnd(s.source, item.start, item.lineEnd, depth)
	required := writingColumn(s.source[item.start:item.marker.MarkerStart]) - writingColumn(s.source[item.start:first]) + item.marker.Padding
	var edits []editor.Edit
	for line := item.lineEnd + 1; line < item.end; {
		end := min(codeLineEnd(s.source, line), item.end)
		at, ok := writingPrefixEnd(s.source, line, end, depth)
		if ok {
			indent := at
			for indent < end && (s.source[indent] == ' ' || s.source[indent] == '\t') {
				indent++
			}
			columns := writingColumn(s.source[line:indent]) - writingColumn(s.source[line:at])
			if indent < end && columns >= required {
				if growth > 0 {
					edits = append(edits, editor.Edit{Start: indent, End: indent, Text: strings.Repeat(" ", growth)})
				} else {
					// Normalize only the affected indentation when a tab stop would
					// otherwise absorb the removed column.
					edits = append(edits, editor.Edit{Start: at, End: indent, Text: strings.Repeat(" ", max(0, columns+growth))})
				}
			}
		}
		line = end + 1
	}
	return edits
}
