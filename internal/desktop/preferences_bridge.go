package desktop

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
)

type desktopPreferencesState struct {
	enabled        bool
	home           string
	store          *DesktopPreferencesStore
	value          DesktopPreferences
	materialsWidth float32
	timer          *time.Timer
	generation     uint64
	closed         bool
}

func (s *app) restoreDesktopPreferences(home string) {
	state := s.desktopPreferences
	if state == nil || !state.enabled || strings.TrimSpace(home) == "" || state.home == home {
		return
	}
	state.home = home
	state.store = NewDesktopPreferencesStore(filepath.Join(home, desktopPreferencesFile))
	value, err := state.store.Load()
	state.value = value
	if !s.taskTabExplicit {
		s.tab = s.restoredTaskContentTab(s.tab)
	}
	if input := s.inputs["setting:materialsExpansionDepth"]; input != nil {
		*input = fmt.Sprint(value.MaterialsExpansionDepth)
	}
	if err != nil {
		s.lastError = "读取桌面偏好失败：" + err.Error()
	}
	s.navigationCollapsed = value.WorkspaceNavigationCollapsed
	if value.TaskListPaneWidth > 0 {
		s.listWidth = float32(value.TaskListPaneWidth)
	}
	state.materialsWidth = fileTreeWidth
	if value.MaterialsDirectoryPaneWidth > 0 {
		state.materialsWidth = float32(value.MaterialsDirectoryPaneWidth)
	}
	name := settingsPreferenceName(value.SettingsSection)
	for _, section := range settingsSections {
		if section.name == name {
			s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage = name, section.pages[0].key
			s.settingsUnit.state.settingsLoaded = ""
			break
		}
	}
}

func settingsPreferenceKey(name string) string {
	switch name {
	case "外观与交互", "基础", "快捷工具":
		return "basic"
	case "任务与工作区", "任务":
		return "tasks"
	case "资料与说明", "存储", "AGENTS.md":
		return "storage"
	case "Git 与发布", "Git":
		return "git"
	case "AI 与扩展", "Codex", "skill":
		return "codex"
	case "工具与连接", "开发工具", "CLI", "代理":
		return "commands"
	case "数据与维护", "系统", "日志":
		return "system"
	}
	return "basic"
}

func settingsPreferenceName(key string) string {
	switch key {
	case "task-creation", "codex-task-creation", "tasks", "branch-naming":
		return "任务与工作区"
	case "paths", "storage", "agents", "instruction-templates":
		return "资料与说明"
	case "git", "tag":
		return "Git 与发布"
	case "codex", "codex-plugins", "codex-mcp", "skill-sources":
		return "AI 与扩展"
	case "groups", "development", "feishu", "lark", "genbu", "cli", "advanced", "commands", "network-proxy", "proxy":
		return "工具与连接"
	case "config-backup", "logs", "about", "updates", "app-data", "system":
		return "数据与维护"
	default:
		return "外观与交互"
	}
}

func (s *app) persistDesktopNavigation() error {
	state := s.desktopPreferences
	if state == nil || state.store == nil {
		return fmt.Errorf("工作空间路径尚未加载，无法保存导航偏好")
	}
	_, err := state.store.Update(func(value *DesktopPreferences) {
		value.WorkspaceNavigationCollapsed = s.navigationCollapsed
	})
	if err != nil {
		return fmt.Errorf("保存导航偏好失败：%w", err)
	}
	state.value.WorkspaceNavigationCollapsed = s.navigationCollapsed
	return nil
}

func (s *app) materialsDirectoryWidth() float32 {
	state := s.desktopPreferences
	if state == nil || state.materialsWidth <= 0 {
		return fileTreeWidth
	}
	return state.materialsWidth
}

func (s *app) materialsDirectoryWidthRef() *float32 {
	state := s.desktopPreferences
	if state == nil {
		state = &desktopPreferencesState{materialsWidth: fileTreeWidth}
		s.desktopPreferences = state
	}
	if state.materialsWidth <= 0 {
		state.materialsWidth = fileTreeWidth
	}
	return &state.materialsWidth
}

func (s *app) materialsExpansionDepth() int {
	if s.desktopPreferences == nil {
		return 2
	}
	return s.desktopPreferences.value.MaterialsExpansionDepth
}

func (s *app) materialsExpansionRevision() uint64 {
	if s.desktopPreferences == nil || s.desktopPreferences.value.MaterialsExpansionRevision == 0 {
		return 1
	}
	return s.desktopPreferences.value.MaterialsExpansionRevision
}

func (s *app) recordDesktopWindowBounds() {
	state := s.desktopPreferences
	if state == nil || !state.enabled || state.store == nil || s.win == nil || s.win.IsDestroyed() {
		return
	}
	state.value.Maximized = s.win.IsMaximized()
	if !state.value.Maximized && !s.win.IsMinimized() {
		width, height := s.win.Size() // MyGO reports window dimensions in DIPs.
		if width > 0 && height > 0 {
			state.value.Width, state.value.Height = width, height
		}
	}
}

func (s *app) observeDesktopPreferences() {
	state := s.desktopPreferences
	if s.loading || state == nil || !state.enabled || state.store == nil || state.closed {
		return
	}
	section := settingsPreferenceKey(s.settingsUnit.state.settingsTab)
	listWidth := int(math.Round(float64(s.listWidth)))
	materialsWidth := int(math.Round(float64(s.materialsDirectoryWidth())))
	if state.value.SettingsSection == section && state.value.TaskListPaneWidth == listWidth && state.value.MaterialsDirectoryPaneWidth == materialsWidth {
		return
	}
	state.value.SettingsSection = section
	state.value.TaskListPaneWidth = listWidth
	state.value.MaterialsDirectoryPaneWidth = materialsWidth
	state.generation++
	generation := state.generation
	if state.timer != nil {
		state.timer.Stop()
	}
	win := s.win
	state.timer = time.AfterFunc(200*time.Millisecond, func() {
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if state.closed || state.generation != generation {
				return
			}
			state.timer = nil
			if err := s.persistDesktopLayout(); err != nil {
				s.lastError = "保存桌面偏好失败：" + err.Error()
			}
		})
	})
}

func (s *app) persistDesktopLayout() error {
	state := s.desktopPreferences
	if state == nil || !state.enabled || state.store == nil {
		return nil
	}
	state.value.WorkspaceNavigationCollapsed = s.navigationCollapsed
	state.value.TaskListPaneWidth = int(math.Round(float64(s.listWidth)))
	state.value.MaterialsDirectoryPaneWidth = int(math.Round(float64(s.materialsDirectoryWidth())))
	state.value.SettingsSection = settingsPreferenceKey(s.settingsUnit.state.settingsTab)
	_, err := state.store.Update(func(value *DesktopPreferences) {
		value.Width, value.Height, value.Maximized = state.value.Width, state.value.Height, state.value.Maximized
		value.WorkspaceNavigationCollapsed = state.value.WorkspaceNavigationCollapsed
		value.TaskListPaneWidth, value.MaterialsDirectoryPaneWidth = state.value.TaskListPaneWidth, state.value.MaterialsDirectoryPaneWidth
		value.TaskListPaneCollapsed = state.value.TaskListPaneCollapsed
		value.SettingsSection = state.value.SettingsSection
		value.MaterialsExpansionDepth = state.value.MaterialsExpansionDepth
		value.MaterialsExpansionRevision = state.value.MaterialsExpansionRevision
	})
	return err
}

func (s *app) closeDesktopPreferences() error {
	state := s.desktopPreferences
	if state == nil || !state.enabled || state.closed {
		return nil
	}
	state.closed = true
	state.generation++
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	return s.persistDesktopLayout()
}
