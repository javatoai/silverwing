package desktop

import "strings"

// A menu belongs to both its control and the currently visible entity.
// File editors reuse controls, so their document identity is included too.
func (s *app) popupMenuScope() string {
	parts := []string{s.snapshot.Home, s.confirmationOwnerKey(), s.workspaceUnit.state.repositoryTab, s.workspaceUnit.state.serviceGroupID, s.toolsUnit.state.toolTab}
	if state := s.settingsUnit.state.skillsBrowser; state != nil {
		parts = append(parts, state.selectedDirectory, state.selectedPath)
	}
	return strings.Join(parts, "\x00")
}
