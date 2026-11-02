package desktop

import "fmt"

// Reading view IDs are stable across label changes and keep CLI indices intact.
func normalizeHiddenTaskTabs(hidden []string) []string {
	set := make(map[string]bool, len(hidden))
	for _, id := range hidden {
		if _, ok := readingTabForView(id); ok {
			set[id] = true
		}
	}
	var result []string
	for index := range tabs {
		if id := readingViewForTab(index); set[id] {
			result = append(result, id)
		}
	}
	if len(result) == len(tabs) {
		result = result[1:] // A malformed preference must still leave one page.
	}
	return result
}

func taskContentTabEnabled(hidden []string, index int) bool {
	if index < 0 || index >= len(tabs) {
		return false
	}
	id := readingViewForTab(index)
	for _, item := range hidden {
		if item == id {
			return false
		}
	}
	return true
}

func (s *app) taskContentTabEnabled(index int) bool {
	var hidden []string
	if s.desktopPreferences != nil {
		hidden = s.desktopPreferences.value.HiddenTaskTabs
	}
	return taskContentTabEnabled(hidden, index)
}

func (s *app) enabledTaskContentTabCount() int {
	count := 0
	for index := range tabs {
		if s.taskContentTabEnabled(index) {
			count++
		}
	}
	return count
}

func firstEnabledTaskContentTab(hidden []string) int {
	for index := range tabs {
		if taskContentTabEnabled(hidden, index) {
			return index
		}
	}
	return 0
}

func (s *app) restoredTaskContentTab(index int) int {
	if s.taskContentTabEnabled(index) {
		return index
	}
	for next := range tabs {
		if s.taskContentTabEnabled(next) {
			return next
		}
	}
	return 0
}

func (s *app) visibleTaskContentTabs() []int {
	var visible []int
	for index := range tabs {
		// Explicit navigation may temporarily reveal a hidden active page.
		if s.taskContentTabEnabled(index) || s.tab == index {
			visible = append(visible, index)
		}
	}
	return visible
}

func (s *app) setTaskContentTabEnabled(index int, enabled bool) bool {
	if index < 0 || index >= len(tabs) {
		return false
	}
	if s.taskContentTabEnabled(index) == enabled {
		return true
	}
	if !enabled && s.enabledTaskContentTabCount() <= 1 {
		s.lastError = "至少保留一个任务页签"
		return false
	}
	state := s.desktopPreferences
	if state == nil || state.store == nil || state.closed {
		s.lastError = "工作空间路径尚未加载，无法保存任务页签偏好"
		return false
	}
	hidden := make([]string, 0, len(tabs)-1)
	for next := range tabs {
		if next == index && !enabled || next != index && !s.taskContentTabEnabled(next) {
			hidden = append(hidden, readingViewForTab(next))
		}
	}
	next := s.tab
	if !enabled && s.tab == index {
		next = firstEnabledTaskContentTab(hidden)
		if !s.prepareTaskContentTab(next) {
			return false
		}
	}
	value, err := state.store.Update(func(value *DesktopPreferences) { value.HiddenTaskTabs = hidden })
	if err != nil {
		s.lastError = fmt.Sprintf("保存任务页签偏好失败：%v", err)
		return false
	}
	state.value.HiddenTaskTabs = value.HiddenTaskTabs
	s.markReadingInteraction()
	if next != s.tab {
		s.activateTaskContentTab(next)
	}
	return true
}

func (s *app) reconcileTaskContentTabs() {
	if s.page != "tasks" && s.page != "archive" && !s.taskContentTabEnabled(s.tab) {
		// Leaving a temporarily revealed page restores the user's hidden entry.
		s.selectTaskContentTab(s.restoredTaskContentTab(s.tab))
	}
}
