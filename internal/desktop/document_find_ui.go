package desktop

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

const documentFindMatchLimit = 10_000

type documentFindBlock struct {
	key      string
	text     string
	scrollY  float32
	lineSize float32
}

type documentFindHit struct {
	block string
	start int
	end   int
}

type documentFindState struct {
	focus    ui.Handle
	viewport ui.Rect

	nativePreview *nativeCodePreviewState

	identity   string
	query      string
	blocks     []documentFindBlock
	hits       []documentFindHit
	active     int
	truncated  bool
	navigation uint64
	lastReveal uint64

	previewPath   string
	previewKind   string
	previewSource string
	jsonText      string
	jsonValid     bool
	csvRows       [][]string
	csvErr        string

	syntaxKey     string
	syntaxText    string
	syntaxSpans   []documentSyntaxSpan
	syntaxPalette palette
}

type documentSyntaxSpan struct {
	start int
	end   int
	color ui.Color
}

func (s *app) ensureDocumentFindState() *documentFindState {
	if s.materialsUnit.state.documentFind == nil {
		s.materialsUnit.state.documentFind = &documentFindState{}
	}
	return s.materialsUnit.state.documentFind
}

func (f *documentFindState) set(identity, query string, blocks []documentFindBlock) {
	if f.identity == identity && f.query == query && sameDocumentFindBlocks(f.blocks, blocks) {
		return
	}
	changedTarget := f.identity != identity || f.query != query
	f.identity, f.query = identity, query
	f.blocks = append(f.blocks[:0], blocks...)
	if changedTarget {
		f.active = 0
	}
	f.hits, f.truncated = documentFindHits(blocks, query, documentFindMatchLimit)
	if f.active >= len(f.hits) {
		f.active = maxInt(len(f.hits)-1, 0)
	}
	f.navigation++
}

func sameDocumentFindBlocks(left, right []documentFindBlock) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func documentFindHits(blocks []documentFindBlock, query string, limit int) ([]documentFindHit, bool) {
	if query == "" || limit <= 0 {
		return nil, false
	}
	queryRunes := []rune(query)
	for i := range queryRunes {
		queryRunes[i] = unicode.ToLower(queryRunes[i])
	}
	hits := make([]documentFindHit, 0)
	for _, block := range blocks {
		textRunes, byteOffsets := foldedRunes(block.text)
		for start := 0; start+len(queryRunes) <= len(textRunes); {
			matched := true
			for i, value := range queryRunes {
				if textRunes[start+i] != value {
					matched = false
					break
				}
			}
			if !matched {
				start++
				continue
			}
			if len(hits) == limit {
				return hits, true
			}
			hits = append(hits, documentFindHit{block: block.key, start: byteOffsets[start], end: byteOffsets[start+len(queryRunes)]})
			start += len(queryRunes)
		}
	}
	return hits, false
}

func foldedRunes(text string) ([]rune, []int) {
	runes := make([]rune, 0, utf8.RuneCountInString(text))
	offsets := make([]int, 0, cap(runes)+1)
	for offset, value := range text {
		runes = append(runes, unicode.ToLower(value))
		offsets = append(offsets, offset)
	}
	offsets = append(offsets, len(text))
	return runes, offsets
}

func (f *documentFindState) current() (documentFindHit, bool) {
	if f == nil || len(f.hits) == 0 {
		return documentFindHit{}, false
	}
	index := minInt(maxInt(f.active, 0), len(f.hits)-1)
	return f.hits[index], true
}

func (f *documentFindState) move(delta int) {
	if f == nil || len(f.hits) == 0 {
		return
	}
	f.active = int((int64(f.active) + int64(delta) + int64(len(f.hits))) % int64(len(f.hits)))
	f.navigation++
}

func (f *documentFindState) close() {
	if f == nil {
		return
	}
	f.query = ""
	f.hits = nil
	f.active = 0
	f.truncated = false
	f.navigation++
}

func (f *documentFindState) block(key string) (documentFindBlock, bool) {
	for _, block := range f.blocks {
		if block.key == key {
			return block, true
		}
	}
	return documentFindBlock{}, false
}

func (f *documentFindState) activeInBlock(key string) (documentFindHit, bool) {
	hit, ok := f.current()
	return hit, ok && hit.block == key
}

func (f *documentFindState) shouldReveal(key string) bool {
	if f == nil || f.lastReveal == f.navigation {
		return false
	}
	if _, ok := f.activeInBlock(key); !ok {
		return false
	}
	f.lastReveal = f.navigation
	return true
}

func (f *documentFindState) shouldRevealRange(key string, start, end int) bool {
	if f == nil || f.lastReveal == f.navigation {
		return false
	}
	hit, ok := f.activeInBlock(key)
	if !ok || hit.start < start || hit.start >= end {
		return false
	}
	f.lastReveal = f.navigation
	return true
}

func (f *documentFindState) targetScroll() (float32, bool) {
	hit, ok := f.current()
	if !ok {
		return 0, false
	}
	block, ok := f.block(hit.block)
	if !ok {
		return 0, false
	}
	if hit.start > len(block.text) {
		return block.scrollY, true
	}
	line := strings.Count(block.text[:hit.start], "\n")
	return block.scrollY + float32(line)*block.lineSize, true
}

func (f *documentFindState) spans(key, text string, baseOffset int, baseColor ui.Color, syntax []documentSyntaxSpan) []ui.Span {
	if text == "" {
		return nil
	}
	type paintRange struct {
		start, end int
		color      ui.Color
		background ui.Color
	}
	var paints []paintRange
	for _, token := range syntax {
		start, end := maxInt(token.start, baseOffset), minInt(token.end, baseOffset+len(text))
		if end > start {
			paints = append(paints, paintRange{start: start - baseOffset, end: end - baseOffset, color: token.color})
		}
	}
	active, hasActive := f.activeInBlock(key)
	darkText := baseColor.R > 140 && baseColor.G > 140 && baseColor.B > 140
	for _, hit := range f.hits {
		if hit.block != key {
			continue
		}
		start, end := maxInt(hit.start, baseOffset), minInt(hit.end, baseOffset+len(text))
		if end <= start {
			continue
		}
		background := ui.Hex("#C9D7FF")
		if darkText {
			background = ui.Hex("#344A67")
		}
		if hasActive && hit == active {
			background = ui.Hex("#92AFFF")
			if darkText {
				background = ui.Hex("#526F9C")
			}
		}
		paints = append(paints, paintRange{start: start - baseOffset, end: end - baseOffset, background: background})
	}
	if len(paints) == 0 {
		return []ui.Span{{Text: text, Color: baseColor}}
	}
	boundaries := []int{0, len(text)}
	for _, paint := range paints {
		boundaries = append(boundaries, paint.start, paint.end)
	}
	// UTF-8 match offsets always come from rune boundaries. Syntax tokens are
	// generated by the same scanner and therefore share those boundaries.
	sort.Ints(boundaries)
	spans := make([]ui.Span, 0, len(boundaries))
	for index := 0; index+1 < len(boundaries); index++ {
		start, end := boundaries[index], boundaries[index+1]
		if start < 0 || end > len(text) || end <= start || !utf8.RuneStart(text[start]) || end < len(text) && !utf8.RuneStart(text[end]) {
			continue
		}
		span := ui.Span{Text: text[start:end], Color: baseColor}
		for _, paint := range paints {
			if paint.start <= start && end <= paint.end {
				if paint.color.A != 0 {
					span.Color = paint.color
				}
				if paint.background.A != 0 {
					span.Background = paint.background
				}
			}
		}
		if len(spans) > 0 && spans[len(spans)-1].Color == span.Color && spans[len(spans)-1].Background == span.Background {
			spans[len(spans)-1].Text += span.Text
		} else {
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		return []ui.Span{{Text: text, Color: baseColor}}
	}
	return spans
}

func (s *app) openDocumentFind(c *ui.Context) {
	if s.materialsUnit.state.documentPath == "" {
		return
	}
	if view := s.currentMaterialsTextPreview(); view != nil {
		view.Run(codeeditor.Find)
		return
	}
	f := s.ensureDocumentFindState()
	s.materialsUnit.state.documentFindOpen = true
	f.navigation++
	f.focus.Focus(c)
}

func (s *app) closeDocumentFind() {
	if pdf := s.currentPDFDocumentFind(); pdf != nil {
		s.changePDFDocumentFindQuery(pdf, "")
	}
	s.materialsUnit.state.documentFindOpen = false
	s.materialsUnit.state.documentSearch = ""
	if s.materialsUnit.state.documentFind != nil {
		s.materialsUnit.state.documentFind.close()
	}
}

func (s *app) moveDocumentFind(delta int) {
	if view := s.currentMaterialsTextPreview(); view != nil {
		command := codeeditor.FindNext
		if delta < 0 {
			command = codeeditor.FindPrevious
		}
		view.Run(command)
		return
	}
	if !s.materialsUnit.state.documentFindOpen {
		return
	}
	if pdf := s.currentPDFDocumentFind(); pdf != nil {
		s.movePDFDocumentFind(pdf, delta)
		return
	}
	f := s.ensureDocumentFindState()
	f.move(delta)
}

func (s *app) applyDocumentFindNavigation(scrollID string) {
	f := s.materialsUnit.state.documentFind
	if !s.materialsUnit.state.documentFindOpen || f == nil || f.lastReveal == f.navigation {
		return
	}
	if offset, ok := f.targetScroll(); ok {
		if scroll := s.readingScrollState(scrollID); scroll != nil {
			scroll.Y = maxFloat32(offset, 0)
		}
	}
}

func documentFindStatus(f *documentFindState) string {
	if f == nil {
		return "0 / 0"
	}
	if len(f.hits) == 0 {
		return "0 / 0"
	}
	count := fmt.Sprintf("%d / %d", f.active+1, len(f.hits))
	if f.truncated {
		count += "+"
	}
	return count
}
