package desktop

// onMaterialsWindowFocused rechecks the active task's local materials after
// returning from another application. Pending refreshes are scoped to the
// same task/root state and survive a currently running action.
func (s *app) onMaterialsWindowFocused() {
	if !s.materialsBrowserVisible() || !s.taskUnit.currentTask().materialsReady() {
		return
	}
	state := s.ensureMaterialsFileManagementState()
	if live := state.liveRefresh; live != nil {
		live.mu.Lock()
		failed := live.failed
		live.mu.Unlock()
		if failed {
			s.materialsUnit.closeMaterialsLiveRefresh()
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
	s.materialsUnit.state.documentsLoaded, s.materialsUnit.state.documentsRequested = false, false
	if s.materialsUnit.state.documentPath == "" {
		s.loadDocuments()
	} else {
		// The ordinary read path retains local drafts and checks their original
		// baseline. Activation must never discard an unsaved document.
		s.rereadSelectedMaterial(false)
	}
}

func (s *app) rereadSelectedMaterial(discardDraft bool) {
	if s.materialsUnit.state.documentPath == "" || s.materialsFileReadBlocked() || s.backend == nil {
		return
	}
	if discardDraft {
		if documentKindForPath(s.materialsUnit.state.documentPath) == "markdown" {
			s.discardMarkdownAndReload()
			return
		}
		s.materialsUnit.state.documentDirty = false
		delete(s.materialsUnit.state.documentDrafts, s.materialsUnit.state.documentPath)
		delete(s.materialsUnit.state.documentDirtyMap, s.materialsUnit.state.documentPath)
	}
	s.act("document.read", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, "", nil)
}
