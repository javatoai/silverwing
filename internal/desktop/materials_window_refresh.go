package desktop

// onMaterialsWindowFocused rechecks the active task's local materials after
// returning from another application. Pending refreshes are scoped to the
// same task/root state and survive a currently running action.
func (s *app) onMaterialsWindowFocused() {
	if !s.materialsBrowserVisible() || !s.currentTask().materialsReady() {
		return
	}
	state := s.ensureMaterialsFileManagementState()
	if live := state.liveRefresh; live != nil {
		live.mu.Lock()
		failed := live.failed
		live.mu.Unlock()
		if failed {
			s.closeMaterialsLiveRefresh()
		}
	}
	state.windowRefreshPending = true
	s.observeMaterialsWindowRefresh()
}

func (s *app) materialsBrowserVisible() bool {
	return (s.page == "tasks" || s.page == "archive" || s.page == "materials") && s.tab == 1
}

func (s *app) observeMaterialsWindowRefresh() {
	if !s.materialsBrowserVisible() || s.backend == nil || s.materialsFileReadBlocked() || s.loading {
		return
	}
	state := s.ensureMaterialsFileManagementState()
	if !state.windowRefreshPending {
		return
	}
	state.windowRefreshPending = false
	s.documentsLoaded, s.documentsRequested = false, false
	if s.documentPath == "" {
		s.loadDocuments()
	} else {
		// The ordinary read path retains local drafts and checks their original
		// baseline. Activation must never discard an unsaved document.
		s.rereadSelectedMaterial(false)
	}
}

func (s *app) rereadSelectedMaterial(discardDraft bool) {
	if s.documentPath == "" || s.materialsFileReadBlocked() || s.backend == nil {
		return
	}
	if discardDraft {
		if documentKindForPath(s.documentPath) == "markdown" {
			s.discardMarkdownAndReload()
			return
		}
		s.documentDirty = false
		delete(s.documentDrafts, s.documentPath)
		delete(s.documentDirtyMap, s.documentPath)
	}
	s.act("document.read", s.currentTask().id, "", "", s.documentPath, "", nil)
}
