package desktop

func (s *app) initialGenbuRouteWaiting() bool {
	state := s.settingsUnit.state.platformConnections
	return s.initialGenbuRoute && (s.loading || state != nil && state.read != nil && !state.loaded)
}

func (s *app) resolveInitialGenbuRoute() {
	if !s.initialGenbuRoute || s.initialGenbuRouteWaiting() {
		return
	}
	s.initialGenbuRoute = false
	if s.page == "production" && s.releaseUnit.state.production == nil {
		s.enterProductionPage()
	}
}

func (s *app) retireIntegrationViews(tool string) {
	switch tool {
	case "genbu":
		s.disableTagGenbuOptionalState()
		s.leaveTaskServices()
		s.leaveCatalogRuntime()
		s.leaveLinkedTaskServices()
		s.closeUATPodAction(s.taskServicesUnit.uatDialog)
		s.closeUATBatchDialog(s.taskServicesUnit.uatBatchDialog)
		s.closeGenbuReadDialogs()
		s.suspendProductionUI()
		s.reconcileTaskContentTabs()
		s.reconcileSidebarNavigation()
	case "dragon":
		if d := s.taskUnit.state.dragonQA; d != nil {
			d.open = false
			s.taskUnit.state.dragonQA = nil
		}
	}
}
