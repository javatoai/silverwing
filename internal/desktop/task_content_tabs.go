package desktop

import "silverwing-go/internal/application"

func availableTaskContentTab(index int) bool {
	return index >= 0 && index < len(tabs) && tabs[index] != ""
}

func (s *app) taskContentTabAvailable(index int) bool {
	if index == taskProjectInstructionsTab {
		return s.projectInstructionsConfigured()
	}
	return availableTaskContentTab(index) && (index != taskServicesTab || application.OptionalCLIFeatureGenbu.Available(s.integrationReady))
}

func (s *app) taskTabNeedsWorkspaceGit() bool {
	return s.tab != taskFilesTab && s.tab != taskServicesTab && s.tab != taskTerminalTab && s.tab != taskProjectInstructionsTab
}

// Capture input under its original owner before changing the visible content.
// The files page is a separate read-only owner and never starts materials work.
func (s *app) selectTaskContentTab(next int) bool {
	if !s.taskContentTabAvailable(next) {
		return false
	}
	s.markReadingInteraction()
	if !s.prepareTaskContentTab(next) {
		return false
	}
	s.initialGenbuRoute = false
	s.taskTabExplicit = true
	s.activateTaskContentTab(next)
	return true
}

func (s *app) prepareTaskContentTab(next int) bool {
	if s.tab == taskProjectInstructionsTab && s.tab != next && !s.captureProjectInstructionsInput() {
		return false
	}
	if s.tab == 1 && s.tab != next {
		if err := s.captureMaterialTextInput(); err != nil {
			s.lastError = err.Error()
			return false
		}
		if err := s.captureMarkdownEditorInput(s.materialsUnit.state.markdown); err != nil {
			s.lastError = err.Error()
			return false
		}
	}
	return true
}

func (s *app) activateTaskContentTab(next int) {
	if s.tab != next {
		if s.tab == taskFilesTab {
			s.leaveTaskFiles()
		}
		if s.tab == taskServicesTab {
			s.leaveTaskServices()
		}
	}
	s.tab = next
	// Changing task presentation from settings must not start hidden-page work.
	if (s.page != "tasks" && s.page != "archive") || s.workspaceReviewPageVisible() {
		return
	}
	switch next {
	case 1:
		s.loadDocuments()
	case taskTagTab:
		s.requestTagHistoryRefresh()
	case taskFilesTab:
		s.enterTaskFiles()
	case taskServicesTab:
		s.ensureTaskServicesCurrent()
	case taskTerminalTab:
		s.focusTaskTerminal()
	case taskProjectInstructionsTab:
		s.observeProjectInstructions()
	}
}
