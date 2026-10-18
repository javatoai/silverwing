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

// Only explicit row activation counts as a visit. Snapshot refreshes, startup
// restoration, list filters and lifecycle-driven selections use selectTaskIndex.
func (s *app) openTaskIndex(index int) {
	if index < 0 || index >= len(s.tasks) {
		return
	}
	target := s.tasks[index]
	if !s.selectTaskIndex(index) {
		// Capturing an unsaved editor can reject the switch.
		return
	}
	key := taskNavigationKey(target)
	if key == "" {
		return
	}
	state := s.taskNavigationFor()
	if state.store != nil {
		value, err := state.store.Visit(key)
		if err == nil {
			state.value, state.error = value, ""
			return
		}
		state.error = "最近访问未保存：" + err.Error()
	}
	// A preference failure must not prevent opening the task. Retain this
	// session's visit while clearly reporting that it was not saved to disk.
	state.value.Recent = taskNavigationMoveFirst(state.value.Recent, key, true)
	if len(state.value.Recent) > taskNavigationRecent {
		state.value.Recent = state.value.Recent[:taskNavigationRecent]
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
	if index >= 0 {
		target := s.tasks[index]
		state := s.taskIndexBrowsingFor(archived)
		state.collapsedGroups[target.groupID] = false
		state.revealTaskKey = taskNavigationKey(target)
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
		t.project, t.groupID, s.taskGroupName(t), t.branch}
	fields = append(fields, t.services...)
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
// Recent view has strict visit order; all/search views put pins first.
func (s *app) taskIndexRows(archived bool) []int {
	state := s.taskIndexBrowsingFor(archived)
	preferences := s.taskNavigationFor().value
	terms := strings.Fields(strings.ToLower(state.search))
	rows := make([]int, 0, len(s.tasks))
	byKey := make(map[string]int, len(s.tasks))
	for index, t := range s.tasks {
		if t.archived == archived && s.taskMatchesSearch(t, terms) {
			rows = append(rows, index)
			byKey[taskNavigationKey(t)] = index
		}
	}
	ordered := make([]int, 0, len(rows))
	keys := preferences.Pinned
	if state.recentOnly {
		keys = preferences.Recent
	}
	for _, key := range keys {
		if index, ok := byKey[key]; ok {
			ordered = append(ordered, index)
			delete(byKey, key)
		}
	}
	if !state.recentOnly {
		for _, index := range rows {
			if _, ok := byKey[taskNavigationKey(s.tasks[index])]; ok {
				ordered = append(ordered, index)
			}
		}
	}
	return ordered
}

// Explicit create/restore navigation should reveal the selected result even if
// the user started the action from a filtered or collapsed task index.
func (s *app) revealTaskInIndex(t task) {
	state := s.taskIndexBrowsingFor(t.archived)
	state.search, state.recentOnly = "", false
	state.collapsedGroups[t.groupID] = false
	state.scroll.X, state.scroll.Y = 0, 0
	state.revealTaskKey = taskNavigationKey(t)
}

type taskIndexSection struct {
	groupID string
	title   string
	rows    []int
}

func (s *app) taskIndexSections(archived bool, rows []int) []taskIndexSection {
	state := s.taskIndexBrowsingFor(archived)
	if state.recentOnly || strings.TrimSpace(state.search) != "" {
		return []taskIndexSection{{rows: rows}}
	}
	pinned, remaining := make([]int, 0), make([]int, 0)
	for _, index := range rows {
		if s.taskIsPinned(s.tasks[index]) {
			pinned = append(pinned, index)
		} else {
			remaining = append(remaining, index)
		}
	}
	sections := make([]taskIndexSection, 0, len(s.snapshot.Groups)+2)
	if len(pinned) > 0 {
		sections = append(sections, taskIndexSection{title: "置顶", rows: pinned})
	}
	if len(s.snapshot.Groups) <= 1 {
		title := ""
		if len(pinned) > 0 {
			title = "其他任务"
		}
		if len(remaining) > 0 {
			sections = append(sections, taskIndexSection{title: title, rows: remaining})
		}
		return sections
	}
	grouped := make(map[string][]int)
	for _, index := range remaining {
		id := s.tasks[index].groupID
		grouped[id] = append(grouped[id], index)
	}
	for _, group := range s.snapshot.Groups {
		if members := grouped[group.ID]; len(members) > 0 {
			sections = append(sections, taskIndexSection{groupID: group.ID, title: firstNonEmpty(group.Name, group.ID), rows: members})
		}
		delete(grouped, group.ID)
	}
	// Tasks can outlive a project-group configuration. Keep them discoverable.
	orphans := make([]int, 0)
	for _, index := range remaining {
		if _, ok := grouped[s.tasks[index].groupID]; ok {
			orphans = append(orphans, index)
		}
	}
	if len(orphans) > 0 {
		sections = append(sections, taskIndexSection{title: "其他任务", rows: orphans})
	}
	return sections
}
