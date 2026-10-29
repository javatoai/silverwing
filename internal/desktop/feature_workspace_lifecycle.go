package desktop

func (s *app) prepareWorkspaceTaskSelection(transition TaskTransition) {
	s.workspaceUnit.leaveTaskSelection(transition, s)
}

func (s *app) refreshWorkspaceTaskSelection(force bool) { s.requestWorkspaceGitStatus(force) }

func (s *app) prepareTaskDialogSelection(transition TaskTransition) {
	s.taskUnit.leaveTaskSelection(transition, s)
}
