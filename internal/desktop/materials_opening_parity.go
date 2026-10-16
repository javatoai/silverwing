package desktop

// restoreMaterialsDraftForOpening keeps the draft visible while the caller
// reads the selected file again. The disk read must still reach
// seedMarkdownAutosave so external changes are checked against the draft's
// original baseline rather than accepted as a new baseline.
func (s *app) restoreMaterialsDraftForOpening(path string) {
	if markdownReadingOnly && documentKindForPath(path) == "markdown" {
		return
	}
	if draft, ok := s.documentDrafts[path]; ok {
		s.documentContent = draft
		s.documentDirty = true
	}
	if s.markdownAutosave == nil {
		return
	}
	key := s.documentAutosaveKeyForTask(s.currentTask().id, path)
	if key == "" {
		return
	}
	if ref, ok := s.documentSaveRef(key); ok {
		s.documentHash = ref.hash
	}
	content, ok := s.markdownAutosave.Content(key)
	if !ok {
		return
	}
	status := s.markdownAutosave.Status(key)
	s.documentContent = content
	s.documentDirty = status.Dirty
	if status.Dirty {
		if s.documentDrafts == nil {
			s.documentDrafts = make(map[string]string)
		}
		if s.documentDirtyMap == nil {
			s.documentDirtyMap = make(map[string]bool)
		}
		s.documentDrafts[path] = content
		s.documentDirtyMap[path] = true
	} else {
		// A save can finish while another document is selected. Its autosave
		// state is authoritative even if the path-only UI cache is still dirty.
		delete(s.documentDrafts, path)
		delete(s.documentDirtyMap, path)
	}
}
