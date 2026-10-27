package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
)

// Navigation is personal UI state, independent of task manifests and the
// expiring reading cache. All access to this presentation state is on the UI
// thread; the store coordinates persisted updates across application instances.
type taskNavigationState struct {
	home  string
	store *taskNavigationStore
	value taskNavigationPreferences
	error string
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
	if s.taskNavigation != nil && s.taskNavigation.home == home {
		return s.taskNavigation
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
	s.taskNavigation = state
	return state
}

func (s *app) taskIsPinned(t task) bool {
	return slices.Contains(s.taskNavigationFor().value.Pinned, taskNavigationKey(t))
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
		return
	}
	state.value.Pinned = taskNavigationMoveFirst(state.value.Pinned, key, pinned)
}

// Opening a row selects the task without changing persisted navigation data.
func (s *app) openTaskIndex(index int) {
	if index < 0 || index >= len(s.tasks) {
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
	for _, group := range s.snapshot.Groups {
		if group.ID == t.groupID {
			return firstNonEmpty(group.Name, group.ID)
		}
	}
	return firstNonEmpty(t.project, t.groupID)
}

func (s *app) taskMatchesSearch(t task, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	fields := []string{t.id, t.title, t.requirementID, t.requirementTitle, t.requirementURL,
		t.project, t.groupID, s.taskGroupName(t)}
	metadata := s.taskRequirementMetadataFor(t).Metadata
	fields = append(fields, metadata.ID)
	if metadata.Title != nil {
		fields = append(fields, *metadata.Title)
	}
	haystack := strings.ToLower(strings.Join(fields, "\n"))
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
	return s.taskIndexRowsFor(s.tasks, archived)
}

func (s *app) taskIndexRowsFor(tasks []task, archived bool) []int {
	state := s.taskIndexBrowsingFor(archived)
	preferences := s.taskNavigationFor().value
	terms := strings.Fields(strings.ToLower(state.search))
	rows := make([]int, 0, len(tasks))
	byKey := make(map[string]int, len(tasks))
	for index, t := range tasks {
		if t.archived == archived && s.taskMatchesSearch(t, terms) {
			rows = append(rows, index)
			byKey[taskNavigationKey(t)] = index
		}
	}
	ordered := make([]int, 0, len(rows))
	for _, key := range preferences.Pinned {
		if index, ok := byKey[key]; ok {
			ordered = append(ordered, index)
			delete(byKey, key)
		}
	}
	for _, index := range rows {
		if _, ok := byKey[taskNavigationKey(tasks[index])]; ok {
			ordered = append(ordered, index)
		}
	}
	return ordered
}

// Explicit creation reveals its result even from a filtered task index.
func (s *app) revealTaskInIndex(t task) {
	state := s.taskIndexBrowsingFor(t.archived)
	state.search = ""
	state.scroll.X, state.scroll.Y = 0, 0
	state.revealTaskKey = taskNavigationKey(t)
}
