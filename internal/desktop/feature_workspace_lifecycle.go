package desktop

func (s *app) prepareWorkspaceTaskSelection(transition TaskTransition) {
	s.workspaceUnit.leaveTaskSelection(transition, s)
}

func (s *app) refreshWorkspaceTaskSelection(force bool) {
	if s.tab != taskFilesTab && s.tab != taskServicesTab && s.tab != taskTerminalTab {
		s.requestWorkspaceGitStatus(force)
	}
}

func (s *app) prepareTaskDialogSelection(transition TaskTransition) {
	s.taskUnit.leaveTaskSelection(transition, s)
}
