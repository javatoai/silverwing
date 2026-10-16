package desktop

// localSkillsViewActive is the single visibility gate for the local Skill
// browser embedded in Settings. Keep its data and Markdown image lifetimes
// aligned with this route.
func (s *app) localSkillsViewActive() bool {
	return s != nil && s.page == "settings" && s.settingsPage == "skill-local"
}

// invalidateLocalSkillsCatalog preserves the current preview while marking it
// stale. The visible browser refreshes immediately; a hidden browser refreshes
// when its tab becomes active again.
func (s *app) invalidateLocalSkillsCatalog() {
	if s == nil {
		return
	}
	state := s.ensureSkillsBrowser()
	state.catalogStale = true
	if s.localSkillsViewActive() && state.pageActive {
		if state.loadingCatalog {
			state.catalogRefreshAfterPending = true
		} else {
			s.requestSkillsCatalog()
		}
	}
}
