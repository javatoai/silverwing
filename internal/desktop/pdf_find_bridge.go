package desktop

import "fmt"

// PDF keeps its existing rune-based search model. The common find bar and
// shortcuts route to that model rather than searching metadata-only content.
func (s *app) currentPDFDocumentFind() *nativeMediaState {
	m := s.materialsUnit.state.nativeMedia
	if m == nil || m.closed || m.kind != "pdf" || s.materialsUnit.state.documentKind != "pdf" ||
		s.materialsUnit.state.documentPath == "" || normalizedReadingPath(m.path) != normalizedReadingPath(s.materialsUnit.state.documentPath) {
		return nil
	}
	return m
}

func (s *app) preparePDFDocumentFind(f *documentFindState, identity string) bool {
	m := s.currentPDFDocumentFind()
	if m == nil {
		return false
	}
	f.identity, f.query = identity, s.materialsUnit.state.documentSearch
	f.blocks, f.hits, f.truncated = nil, nil, false
	s.changePDFDocumentFindQuery(m, s.materialsUnit.state.documentSearch)
	return true
}

func (s *app) changePDFDocumentFindQuery(m *nativeMediaState, query string) {
	if s.currentPDFDocumentFind() != m || m == nil {
		return
	}
	s.materialsUnit.state.documentSearch = query
	if m.materialsHit != nil && m.materialsHit.Query != query {
		m.materialsHit = nil
	}
	changed := m.search != query || m.searchKey != query
	m.search = query
	if query == "" {
		m.searchKey, m.searchResults, m.searchResult = "", nil, 0
	} else if changed {
		s.updatePDFSearch(m)
	}
	if s.materialsUnit.state.documentFind != nil {
		s.materialsUnit.state.documentFind.query = query
	}
}

func (s *app) movePDFDocumentFind(m *nativeMediaState, delta int) {
	if s.currentPDFDocumentFind() != m || m == nil || len(m.searchResults) == 0 {
		return
	}
	m.searchResult = int((int64(m.searchResult) + int64(delta) + int64(len(m.searchResults))) % int64(len(m.searchResults)))
	s.restorePDFSearchMatch(m, m.searchResults[m.searchResult].page)
}

func pdfDocumentFindStatus(m *nativeMediaState) string {
	if m == nil || len(m.searchResults) == 0 {
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", m.searchResult+1, len(m.searchResults))
}
