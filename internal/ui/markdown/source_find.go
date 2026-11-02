package markdownui

import md "github.com/egoist/mygo/ui/markdown"

// Editing modes search the original normalized Buffer, including destinations
// and delimiters. Live mode can expand the active block into source; both modes
// must reveal through that same native surface rather than reader callbacks.
func (s *EditorState) sourceFindHighlights(source string) []md.SourceHighlight {
	if s.Mode() == Preview {
		return nil
	}
	find := s.reader.find
	var matches []findMatch
	for _, hit := range foldedMatches(source, find.Query) {
		matches = append(matches, findMatch{block: -1, start: hit[0], end: hit[1], documentID: s.document.ID})
	}
	// The query can change while renderFind builds, after reader.prepare
	// ran. Refresh before consuming that frame's reveal/highlight request.
	find.update(s.document.ID, matches)
	highlights := make([]md.SourceHighlight, 0, len(matches))
	for i, hit := range find.matches {
		if hit.documentID == s.document.ID {
			highlights = append(highlights, md.SourceHighlight{Start: hit.start, End: hit.end, Current: i == find.Current})
		}
	}
	if !s.native.Composing() {
		if hit, ok := find.revealFor(s.document.ID); ok {
			// Source edits can shift a highlighted match. Only a changed query,
			// index or explicit navigation may replace the native user selection.
			if s.sourceFindOwner != find || s.sourceFindRequest != find.selectionRequest {
				if s.Mode() == Live {
					if model := s.reader.properties.model; model != nil && hit.start < model.End {
						s.report(s.SetMode(Source))
					}
				}
				s.native.SetSelection(hit.start, hit.end)
			}
			find.revealed(hit)
		}
		s.sourceFindOwner, s.sourceFindRequest = find, find.selectionRequest
	}
	return highlights
}
