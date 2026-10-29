package desktop

import "silverwing-go/internal/documents"

// Keep only the latest requested hit. The ordinary text-index completion
// applies it, so an extraction failure waits for the existing explicit retry.
func (s *app) requestMaterialsPDFHit(m *nativeMediaState, hit documents.SearchHit) {
	if m == nil || m.closed || s.materialsUnit.state.nativeMedia != m || m.search != hit.Query {
		return
	}
	copyHit := hit
	if hit.PageIndex != nil {
		page := *hit.PageIndex
		copyHit.PageIndex = &page
	}
	m.materialsHit = &copyHit
	s.updatePDFSearch(m)
}

func (s *app) applyMaterialsPDFHit(m *nativeMediaState) {
	hit := m.materialsHit
	m.materialsHit = nil
	if hit == nil || hit.Query != m.search || hit.PageIndex == nil || m.textIndex == nil {
		return
	}
	page := *hit.PageIndex
	if page < 0 || page >= len(m.textIndex.Pages) {
		return
	}
	offset := utf16ToRuneOffset(m.textIndex.Pages[page].Text, hit.Offset)
	bestDistance := int(^uint(0) >> 1)
	for index, match := range m.searchResults {
		if match.page == page {
			if distance := absInt(match.start - offset); distance < bestDistance {
				bestDistance, m.searchResult = distance, index
			}
		}
	}
}
