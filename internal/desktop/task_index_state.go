package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Active and archived lists retain separate, session-only browsing positions.
type taskIndexBrowsingState struct {
	scroll        ui.ScrollState
	detailScrolls map[string]*ui.ScrollState
	search        string
	recentOnly    bool
	searchInput   ui.Handle
	rowHandles    map[string]*ui.Handle
	revealTaskKey string
	visibleRows   []int
}

func (s *app) taskIndexBrowsingFor(archived bool) *taskIndexBrowsingState {
	if s.taskIndexes == nil {
		s.taskIndexes = make(map[bool]*taskIndexBrowsingState)
	}
	if state := s.taskIndexes[archived]; state != nil {
		return state
	}
	state := &taskIndexBrowsingState{}
	s.taskIndexes[archived] = state
	return state
}

func (s *app) taskWorkspaceDetailScroll(t task) *ui.ScrollState {
	state := s.taskIndexBrowsingFor(t.archived)
	if state.detailScrolls == nil {
		state.detailScrolls = make(map[string]*ui.ScrollState)
	}
	key := strings.Join([]string{t.id, t.path, t.createdAt}, "\x00")
	if state.detailScrolls[key] == nil {
		state.detailScrolls[key] = &ui.ScrollState{}
	}
	return state.detailScrolls[key]
}
