package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// Navigation is personal UI state, independent of task manifests and the
// expiring reading cache. All access to this presentation state is on the UI
// thread; the store coordinates persisted updates across application instances.
type taskNavigationState struct {
	home     string
	store    *taskNavigationStore
	value    taskNavigationPreferences
	error    string
	pinned   map[string]bool
	revision uint64
}

// A task retains its identity through archive/restore and task-root relocation.
// A newly created task reusing the same directory must not inherit old pins.
func taskNavigationKey(t task) string {
	if strings.TrimSpace(t.id) == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(t.id + "\x00" + t.createdAt))
	return hex.EncodeToString(digest[:])
}

func (s *app) taskNavigationFor() *taskNavigationState {
	home := strings.TrimSpace(s.snapshot.Home)
	if s.desktopPreferences != nil && !s.desktopPreferences.enabled {
		// Captures and isolated UI fixtures must not write personal preferences.
		home = ""
	}
	if home != "" {
		if absolute, err := filepath.Abs(home); err == nil {
			home = filepath.Clean(absolute)
		}
	}
	if s.taskUnit.state.taskNavigation != nil && s.taskUnit.state.taskNavigation.home == home {
		return s.taskUnit.state.taskNavigation
	}
	state := &taskNavigationState{home: home}
	if home != "" {
		state.store = newTaskNavigationStore(filepath.Join(home, "task-navigation.json"))
		value, err := state.store.Load()
		if err != nil {
			state.error = "读取任务导航偏好失败：" + err.Error()
		} else {
			state.value = value
		}
	}
	s.taskUnit.state.taskNavigation = state
	state.publishPins()
	return state
}

func (state *taskNavigationState) publishPins() {
	state.pinned = make(map[string]bool, len(state.value.Pinned))
	for _, key := range state.value.Pinned {
		state.pinned[key] = true
	}
	state.revision++
}

func (s *app) taskIsPinned(t task) bool {
	return s.taskNavigationFor().pinned[taskNavigationKey(t)]
}

func (s *app) setTaskPinned(t task, pinned bool) {
	key := taskNavigationKey(t)
	if key == "" {
		return
	}
	state := s.taskNavigationFor()
	if state.store != nil {
		value, err := state.store.SetPinned(key, pinned)
		if err != nil {
			state.error = "保存任务置顶失败：" + err.Error()
			return
		}
		state.value, state.error = value, ""
		state.publishPins()
		return
	}
	state.value.Pinned = taskNavigationMoveFirst(state.value.Pinned, key, pinned)
	state.publishPins()
}

// Opening a row selects the task without changing persisted navigation data.
func (s *app) openTaskIndex(index int) {
	if index < 0 || index >= len(s.taskUnit.state.tasks) {
		return
	}
	if !s.selectTaskIndex(index) {
		// Capturing an unsaved editor can reject the switch.
		return
	}
}

func (s *app) switchTaskIndexCategory(archived bool) {
	index := -1
	if rows := s.taskIndexRows(archived); len(rows) > 0 {
		index = rows[0]
	}
	// Keep the route and browsing position together with the editor's accepted
	// selection. A pending composition can refuse to leave the current task.
	if !s.selectTaskIndex(index) {
		return
	}
	s.page = "tasks"
	if archived {
		s.page = "archive"
	}
}

func (s *app) taskGroupName(t task) string {
	if group, found := s.serviceCatalog().groups[t.groupID]; found {
		return firstNonEmpty(group.Name, group.ID)
	}
	return firstNonEmpty(t.project, t.groupID)
}

func (s *app) taskMatchesSearch(t task, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	haystack := s.taskSearchText(t)
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

// Return filtered task indices without mutating snapshot order or selection.
// Pinned tasks precede the remaining rows in the snapshot's stable order.
func (s *app) taskIndexRows(archived bool) []int {
	return s.cachedTaskIndexRows(archived)
}

func (s *app) taskIndexRowsFor(tasks []task, archived bool) []int {
	state := s.taskIndexBrowsingFor(archived)
	terms := strings.Fields(strings.ToLower(state.search))
	catalog := newTaskIndexCatalog(tasks)
	category := 0
	if archived {
		category = 1
	}
	return projectTaskIndexOrder(catalog.categories[category], catalog.keys, s.taskNavigationFor().value.Pinned, func(i int) bool { return s.taskMatchesSearch(tasks[i], terms) })
}

// Explicit creation reveals its result even from a filtered task index.
func (s *app) revealTaskInIndex(t task) {
	state := s.taskIndexBrowsingFor(t.archived)
	state.search = ""
	state.scroll.X, state.scroll.Y = 0, 0
	state.revealTaskKey = taskNavigationKey(t)
}
