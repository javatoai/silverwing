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
			s.settingsTab, s.settingsPage = name, section.pages[0].key
			s.settingsLoaded = ""
			break
		}
	}
}

func settingsPreferenceKey(name string) string {
	keys := map[string]string{
		"基础": "basic", "快捷工具": "developer-tools", "工具": "developer-tools", "存储": "storage", "研发配置": "development", "开发工具": "development",
		"任务": "tasks", "AGENTS.md": "agents", "Codex": "codex", "CLI": "commands",
		"skill": "skill-sources", "代理": "proxy", "Git": "git", "日志": "logs", "系统": "system",
	}
	if key, ok := keys[name]; ok {
		return key
	}
	return "basic"
}

func settingsPreferenceName(key string) string {
	// Source also migrates older leaf-page preferences to their category.
	switch key {
	case "developer-tools":
		return "快捷工具"
	case "paths", "config-backup", "storage":
		return "存储"
	case "groups", "development":
		return "开发工具"
	case "tools":
		return "快捷工具"
	case "task-creation", "codex-task-creation", "task-area", "tasks":
		return "任务"
	case "agents", "instruction-templates":
		return "AGENTS.md"
	case "codex-plugins", "codex-mcp", "codex":
		return "Codex"
	case "feishu", "lark", "genbu", "cli", "advanced", "commands":
		return "CLI"
	case "skill-sources":
		return "skill"
	case "network-proxy", "proxy":
		return "代理"
	case "git", "branch-naming", "tag":
		return "Git"
	case "logs":
		return "日志"
	case "about", "updates", "app-data", "system":
		return "系统"
	default:
		return "基础"
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
	section := settingsPreferenceKey(s.settingsTab)
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
	state.value.SettingsSection = settingsPreferenceKey(s.settingsTab)
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
