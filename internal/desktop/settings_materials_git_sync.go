package desktop

import (
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
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
	state := s.materialsUnit.state.materialsGit
	status, statusColor, visibleStatus := s.settingsMaterialsGitStatus(settings.Enabled, p)
	statusDetails := status
	if state != nil && s.materialsGitScopeCurrent(state.scope) {
		if when, err := time.Parse(time.RFC3339, state.status.LastSuccess); err == nil {
			statusDetails = settingsHelpText(status, "最近成功 "+when.Local().Format("2006-01-02 15:04"))
		}
	}
	help := settingsHelpText("按周期先拉取远端内容，再提交并推送整个任务资料仓库；遵循仓库忽略规则，仅在 SW 运行时执行。", statusDetails)
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
			if paths, err := config.MaterialsPaths(); err == nil {
				s.act("system.open", "", "", "", paths.Root, "", map[string]any{"desktopSystemOpenDirectory": true})
			}
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
	state := s.materialsUnit.state.materialsGit
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
			if state != nil && state.authenticating {
				placeholder = "正在登录并验证…"
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
			if miniButton(c, p, "materials-git-branches-refresh", "refresh", "刷新远程分支", false).Disabled(loading || state != nil && state.terminalPreparing || s.busy || pending || settings.RepositoryURL == "").Clicked() {
				s.loadMaterialsGitBranches(true)
			}
		})
	})
	if state != nil && state.authenticating {
		label(c, "正在等待 Git 凭据助手完成验证…", 11, p.muted).Wrap()
		if action(c, "materials-git-login-cancel", "close", "取消登录验证", p, false) {
			state.cancel()
		}
	}
	if state == nil || loading || settings.RepositoryURL == "" {
		return
	}
	if state.err != "" {
		label(c, state.err, 11, p.danger).Wrap().Selectable()
		if state.needsLogin && s.settingsMaterialsGitLoginActions(c, p, settings, state) {
			return
		}
		if !pending && action(c, "materials-git-branches-retry", "refresh", "重试读取分支", p, false) {
			s.loadMaterialsGitBranches(true)
		}
	} else if ready && len(state.branches) == 0 {
		label(c, "请先在远程仓库创建分支", 11, p.amber).Wrap()
	} else if ready && !slices.Contains(state.branches, settings.Branch) {
		label(c, fmt.Sprintf("已保存的分支 %s 不在远程，请重新选择。", settings.Branch), 11, p.amber).Wrap().Selectable()
	}
}

func (s *app) settingsMaterialsGitLoginActions(c *ui.Context, p palette, settings domain.MaterialsGitSyncSettings, state *materialsGitBranchesState) bool {
	if candidate := workspace.MaterialsGitHTTPSUpgrade(settings.RepositoryURL); candidate != "" {
		label(c, "Git 登录需要 HTTPS。确认改用以下地址后可以登录。", 11, p.muted).Wrap()
		label(c, candidate, 11, p.text).Wrap().Selectable()
		if ui.Button(c.Key("materials-git-switch-https"), "确认改用 HTTPS").Disabled(s.busy || s.materialsGitRunning()).Clicked() && s.materialsGitBranchesCurrent() == state {
			settings.RepositoryURL = candidate
			s.saveMaterialsGitSettings(settings)
		}
		return true
	}
	if _, ok := s.backend.(application.MaterialsGitLoginBackend); !ok || !workspace.MaterialsGitUsesHTTPS(settings.RepositoryURL) {
		return false
	}
	if runtime.GOOS == "darwin" {
		label(c, "Git 使用已配置的凭据助手，未配置时使用 macOS 钥匙串。", 11, p.muted).Wrap()
	}
	label(c, "终端验证时，粘贴复制的命令，完成后刷新远程分支。", 11, p.muted).Wrap()
	services := c.Services()
	disabled := s.busy || state.terminalPreparing || s.materialsGitRunning()
	ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
		if ui.Button(c.Key("materials-git-login"), "登录并验证").Disabled(disabled).Clicked() {
			s.beginMaterialsGitLogin()
		}
		if ui.Button(c.Key("materials-git-login-terminal"), "打开终端").Disabled(disabled).Clicked() {
			s.prepareMaterialsGitLoginTerminal(services, true)
		}
		if ui.Button(c.Key("materials-git-login-copy"), "复制验证命令").Disabled(disabled).Clicked() {
			s.prepareMaterialsGitLoginTerminal(services, false)
		}
	})
	return true
}
