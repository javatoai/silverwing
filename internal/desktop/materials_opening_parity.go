package desktop

// restoreMaterialsDraftForOpening keeps the draft visible while the caller
// reads the selected file again. The disk read must still reach
// seedMarkdownAutosave so external changes are checked against the draft's
// original baseline rather than accepted as a new baseline.
func (s *app) restoreMaterialsDraftForOpening(path string) {
	if markdownReadingOnly && documentKindForPath(path) == "markdown" {
		return
	}
	if draft, ok := s.materialsUnit.state.documentDrafts[path]; ok {
		s.materialsUnit.state.documentContent = draft
		s.materialsUnit.state.documentDirty = true
	}
	if s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	key := s.documentAutosaveKeyForTask(s.taskUnit.currentTask().id, path)
	if key == "" {
		return
	}
	if ref, ok := s.materialsUnit.documentSaveRef(key); ok {
		s.materialsUnit.state.documentHash = ref.hash
	}
	content, ok := s.materialsUnit.state.markdownAutosave.Content(key)
	if !ok {
		return
	}
	status := s.materialsUnit.state.markdownAutosave.Status(key)
	s.materialsUnit.state.documentContent = content
	s.materialsUnit.state.documentDirty = status.Dirty
	if status.Dirty {
		if s.materialsUnit.state.documentDrafts == nil {
			s.materialsUnit.state.documentDrafts = make(map[string]string)
		}
		if s.materialsUnit.state.documentDirtyMap == nil {
			s.materialsUnit.state.documentDirtyMap = make(map[string]bool)
		}
		s.materialsUnit.state.documentDrafts[path] = content
		s.materialsUnit.state.documentDirtyMap[path] = true
	} else {
		// A save can finish while another document is selected. Its autosave
		// state is authoritative even if the path-only UI cache is still dirty.
		delete(s.materialsUnit.state.documentDrafts, path)
		delete(s.materialsUnit.state.documentDirtyMap, path)
	}
}
