package desktop

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func (s *app) saveMaterialsGitSettings(settings domain.MaterialsGitSyncSettings) {
	config, ok := s.materialsGitConfig()
	if !ok {
		return
	}
	old := config.MaterialsGitSettings()
	if old.RepositoryURL != settings.RepositoryURL || old.Branch != settings.Branch {
		settings.Enabled = false
	}
	if err := settings.Validate(); err != nil {
		s.lastError = err.Error()
		return
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		s.lastError = err.Error()
		return
	}
	s.saveSettingsConfigFields(application.ConfigFieldsUpdate{Fields: map[string]json.RawMessage{"materialsGitSync": encoded}})
}

func (s *app) settingsMaterialsGitSync(c *ui.Context, p palette) {
	config, ok := s.materialsGitConfig()
	if !ok {
		return
	}
	settings := config.MaterialsGitSettings()
	s.loadMaterialsGitBranches(false)
	value, pending := s.settingsConfigSwitchInput("materials-git-enabled", settings.Enabled)
	state := s.materialsGit
	status, statusColor, visibleStatus := s.settingsMaterialsGitStatus(settings.Enabled, p)
	statusDetails := status
	if state != nil && s.materialsGitScopeCurrent(state.scope) {
		if when, err := time.Parse(time.RFC3339, state.status.LastSuccess); err == nil {
			statusDetails = settingsHelpText(status, "最近成功 "+when.Local().Format("2006-01-02 15:04"))
		}
	}
	help := settingsHelpText("按周期提交、拉取并推送任务资料；仅在 SW 运行时执行。", statusDetails)
	settingsAppearanceRow(c, p, "Git 自动同步", help, func(stacked bool) {
		ui.Switch(c.Key("settings-materials-git-enabled"), value).Width(52).Height(32).Radius(16).Label("Git 自动同步").Disabled(pending || s.busy || s.materialsGitSettingsPending(config) || !settings.Enabled && !s.materialsGitBranchReady(settings.Branch)).OnChange(func() {
			settings.Enabled = *value
			s.saveMaterialsGitSettings(settings)
		})
	})
	for _, field := range []struct{ key, title, placeholder, initial string }{
		{"repositoryUrl", "Git 仓库地址", "https://git.example.com/team/documents.git", settings.RepositoryURL},
		{"intervalMinutes", "同步周期（分钟）", "5", strconv.Itoa(settings.IntervalMinutes)},
	} {
		settingsAppearanceRow(c, p, field.title, "", func(stacked bool) {
			input := s.input("setting:materialsGitSync:"+field.key, field.initial)
			save := func(text string) {
				current, ok := s.materialsGitConfig()
				if !ok {
					return
				}
				next := current.MaterialsGitSettings()
				switch field.key {
				case "repositoryUrl":
					next.RepositoryURL = strings.TrimSpace(text)
				case "intervalMinutes":
					minutes, err := strconv.Atoi(strings.TrimSpace(text))
					if err != nil {
						s.lastError = "同步周期必须为正整数分钟"
						return
					}
					next.IntervalMinutes = minutes
				}
				s.saveMaterialsGitSettings(next)
			}
			observe := func() { s.autosaveSettingsInput("materials-git:"+field.key, input, save) }
			observe()
			ui.TextInput(c.Key("settings-materials-git-"+field.key), input).FillWidth().MinWidth(0).Height(36).Placeholder(field.placeholder).OnChange(observe).OnSubmit(func() { s.flushSettingsAutosaves(true) })
		})
		if field.key == "repositoryUrl" {
			s.settingsMaterialsGitBranch(c, p, settings)
		}
	}
	if state != nil && s.materialsGitScopeCurrent(state.scope) {
		if visibleStatus {
			label(c, status, 11, statusColor).Wrap()
		}
		if state.status.Error != "" {
			label(c, state.status.Error, 11, p.danger).Wrap().Selectable()
		}
	}
	ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Children(func() {
		if action(c, "materials-git-open", "folder", "打开仓库", p, false) && config.RequirementMaterialsRoot != nil {
			s.act("system.open", "", "", "", *config.RequirementMaterialsRoot, "", map[string]any{"desktopSystemOpenDirectory": true})
		}
		title := "立即同步"
		if state != nil && (state.status.Paused || state.needsResume) {
			title = "重新检查并恢复"
		}
		disabled := state == nil || state.reading || state.running || s.busy || s.materialsGitSettingsPending(config) || !s.materialsGitBranchReady(settings.Branch)
		fg := p.text
		if disabled {
			fg = p.muted
		}
		button := ui.ButtonBase(c.Key("materials-git-sync-now")).Height(34).Padding(0, 16).Radius(20).Border(1, p.line).Background(p.surface).Label(title).Disabled(disabled)
		button.Children(func() { label(c, title, 13, fg).NoWrap() })
		if button.Clicked() {
			s.beginMaterialsGitSync(true)
		}
	})
}

// Idle details belong to the help; active and actionable states stay visible.
// Derive them on every frame from the current scope, never from cached help text.
func (s *app) settingsMaterialsGitStatus(enabled bool, p palette) (string, ui.Color, bool) {
	text, color, visible := "自动同步未启用", p.muted, false
	if enabled {
		text = "等待下次同步"
	}
	state := s.materialsGit
	if state == nil || !s.materialsGitScopeCurrent(state.scope) {
		return text, color, visible
	}
	if state.status.Paused || state.needsResume {
		text, color, visible = "同步已暂停", p.amber, true
	}
	if state.phase != "" {
		text, visible = state.phase, true
	} else if state.running {
		text, visible = "正在同步…", true
	}
	if state.reading {
		text, visible = "正在读取同步状态…", true
	}
	return text, color, visible
}

func (s *app) settingsMaterialsGitBranch(c *ui.Context, p palette, settings domain.MaterialsGitSyncSettings) {
	state := s.materialsGitBranchesCurrent()
	loading := state != nil && state.loading
	ready := state != nil && state.loaded && !loading && state.err == ""
	config, _ := s.materialsGitConfig()
	pending := s.materialsGitSettingsPending(config)
	settingsAppearanceRow(c, p, "同步分支", "", func(stacked bool) {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			selected, options := settings.Branch, []string{}
			if state != nil {
				options = state.branches
			}
			if !ready || !slices.Contains(options, selected) {
				selected = ""
			}
			placeholder := "选择远程分支"
			if settings.RepositoryURL == "" {
				placeholder = "先填写 Git 仓库地址"
			}
			if loading {
				placeholder = "正在读取远程分支…"
			}
			if selected == "" {
				selected = placeholder
			}
			// Replacing the query scope also replaces the native popup identity.
			// An option from an old repository cannot survive in an open menu.
			key := fmt.Sprintf("settings-materials-git-branch-%p", state)
			if ui.Select(c.Key(key), &selected, options).Grow(1).MinWidth(0).Height(36).Label("选择同步分支").Disabled(!ready || len(options) == 0 || s.busy || pending).Changed() && slices.Contains(options, selected) {
				current, ok := s.materialsGitConfig()
				if ok && s.materialsGitBranchesCurrent() == state && selected != current.MaterialsGitSettings().Branch {
					next := current.MaterialsGitSettings()
					next.Branch = selected
					s.saveMaterialsGitSettings(next)
				}
			}
			if miniButton(c, p, "materials-git-branches-refresh", "refresh", "刷新远程分支", loading || s.busy || pending || settings.RepositoryURL == "").Clicked() {
				s.loadMaterialsGitBranches(true)
			}
		})
	})
	if state == nil || loading || settings.RepositoryURL == "" {
		return
	}
	if state.err != "" {
		label(c, state.err, 11, p.danger).Wrap().Selectable()
		if action(c, "materials-git-branches-retry", "refresh", "重试读取分支", p, false) {
			s.loadMaterialsGitBranches(true)
		}
	} else if ready && len(state.branches) == 0 {
		label(c, "请先在远程仓库创建分支", 11, p.amber).Wrap()
	} else if ready && !slices.Contains(state.branches, settings.Branch) {
		label(c, fmt.Sprintf("已保存的分支 %s 不在远程，请重新选择。", settings.Branch), 11, p.amber).Wrap().Selectable()
	}
}
