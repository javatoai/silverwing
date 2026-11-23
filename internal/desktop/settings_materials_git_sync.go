package desktop

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func (s *app) settingsMaterialsGitSync(c *ui.Context, p palette) {
	s.settingsDocumentsGitSync(c, p, taskMaterialsGit)
}

func (s *app) settingsDocumentsGitSync(c *ui.Context, p palette, target documentsGitTarget) {
	config, ok := s.materialsGitConfig()
	if !ok {
		return
	}
	settings := target.settings(config)
	s.loadDocumentsGitBranches(target, false)
	value, pending := s.settingsConfigSwitchInput(string(target)+"-enabled", settings.Enabled)
	scope := target.scope(s.snapshot.Home, config)
	status := s.documentsGitSettingsStatus(target, settings.Enabled, p)
	settingsPending := s.documentsGitSettingsPending(target, config)
	branchReady := s.documentsGitBranchReady(target, settings)
	bound := strings.TrimSpace(settings.RepositoryURL) != "" && scope.Root != ""
	s.settingsDocumentsGitLocation(c, p, target, scope, bound, status.busy || settingsPending)
	s.settingsDocumentsGitBranch(c, p, target, settings)
	statusDetails := status.label
	if status.sync != nil {
		if when, err := time.Parse(time.RFC3339, status.sync.LastSuccess); err == nil {
			statusDetails = settingsHelpText(status.label, "最近成功 "+when.Local().Format("2006-01-02 15:04"))
		}
	}
	help := settingsHelpText("按周期拉取远端内容，再提交并推送本地修改。仅在 Silverwing 运行时执行。", statusDetails)
	settingsAppearanceRow(c, p, "Git 自动同步", help, func(stacked bool) {
		ui.Switch(c.Key("settings-"+string(target)+"-enabled"), value).Width(52).Height(32).Radius(16).Label("Git 自动同步").Disabled(!bound || pending || s.busy || settingsPending || !settings.Enabled && !branchReady).OnChange(func() {
			settings.Enabled = *value
			s.saveDocumentsGitSetting(target, scope, settings, "enabled")
		})
	})
	const intervalTitle = "同步周期（分钟）"
	settingsAppearanceRow(c, p, intervalTitle, "", func(bool) {
		const field = "intervalMinutes"
		input := s.input(target.inputKey(field), strconv.Itoa(settings.IntervalMinutes))
		save := func(text string) {
			current, ok := s.materialsGitConfig()
			if !ok {
				return
			}
			minutes, err := strconv.Atoi(strings.TrimSpace(text))
			if err != nil || minutes <= 0 || int64(minutes) > int64((1<<63-1)/time.Minute) {
				s.lastError = "同步周期必须为正整数分钟"
				return
			}
			next := target.settings(current)
			next.IntervalMinutes = minutes
			s.saveDocumentsGitSetting(target, scope, next, field)
		}
		observe := func() { s.autosaveSettingsInput(string(target)+":"+field, input, save) }
		observe()
		ui.TextInput(c.Key("settings-"+string(target)+"-"+field), input).Width(120).MinWidth(0).Height(36).Label(intervalTitle).
			Disabled(!bound).OnChange(observe).OnSubmit(func() { s.flushSettingsAutosaves(true) })
	})
	if status.sync != nil {
		if status.visible {
			label(c, status.label, 11, status.color).Wrap()
		}
		if status.sync.Error != "" {
			label(c, status.sync.Error, 11, p.danger).Wrap().Selectable()
		}
	}
	ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Children(func() {
		ui.Row(c).Disabled(scope.Root == "" || settingsPending).Children(func() {
			if action(c, string(target)+"-open", "folder", "打开仓库", p, false) {
				s.act("system.open", "", "", "", scope.Root, "", map[string]any{"desktopSystemOpenDirectory": true})
			}
		})
		title := "立即同步"
		if status.paused {
			title = "重新检查并恢复"
		}
		disabled := !bound || status.sync == nil || status.busy || s.busy || settingsPending || !branchReady
		fg := p.text
		if disabled {
			fg = p.muted
		}
		button := ui.ButtonBase(c.Key(string(target)+"-sync-now")).Height(34).Padding(0, 16).Radius(20).Border(1, p.line).Background(p.surface).Label(title).Disabled(disabled)
		button.Children(func() { label(c, title, 13, fg).NoWrap() })
		if button.Clicked() {
			if target == projectInstructionsGit {
				s.syncProjectInstructions(s.projectInstructions, true)
			} else {
				s.beginMaterialsGitSync(true)
			}
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

type documentsGitStatus struct {
	label                 string
	color                 ui.Color
	sync                  *workspace.MaterialsGitSyncStatus
	visible, busy, paused bool
}

func (s *app) documentsGitSettingsStatus(target documentsGitTarget, enabled bool, p palette) documentsGitStatus {
	var result documentsGitStatus
	if target == taskMaterialsGit {
		result.label, result.color, result.visible = s.settingsMaterialsGitStatus(enabled, p)
		if state := s.materialsUnit.state.materialsGit; state != nil && s.materialsGitScopeCurrent(state.scope) {
			result.sync, result.busy, result.paused = &state.status, state.reading || state.running, state.status.Paused || state.needsResume
		}
		return result
	}
	result.label, result.color = "自动同步未启用", p.muted
	if enabled {
		result.label = "等待下次同步"
	}
	if view := s.projectInstructions; s.projectInstructionsCurrent(view) {
		result.sync, result.busy, result.paused = &view.status, view.syncing || view.readingStatus, view.status.Paused
		switch {
		case view.syncing:
			result.label, result.visible = "正在同步…", true
		case view.readingStatus:
			result.label, result.visible = "正在读取同步状态…", true
		case result.paused:
			result.label, result.color, result.visible = "同步已暂停", p.amber, true
		}
	}
	return result
}

const documentsGitDefaultBranch = "远端默认分支 (自动跟随)"

func (s *app) documentsGitBranchReady(target documentsGitTarget, settings domain.MaterialsGitSyncSettings) bool {
	if target == taskMaterialsGit {
		return s.materialsGitBranchReady(settings.Branch)
	}
	state := s.documentsGitBranchesCurrent(target)
	return state != nil && state.loaded && !state.loading && state.err == "" && len(state.branches) > 0 &&
		(settings.Branch == "" || slices.Contains(state.branches, settings.Branch))
}

func (s *app) settingsDocumentsGitBranch(c *ui.Context, p palette, target documentsGitTarget, settings domain.MaterialsGitSyncSettings) {
	state := s.documentsGitBranchesCurrent(target)
	loading := state != nil && state.loading
	ready := state != nil && state.loaded && !loading && state.err == ""
	config, _ := s.materialsGitConfig()
	pending := s.documentsGitSettingsPending(target, config)
	keyPrefix := string(target)
	followRemoteDefault := target == projectInstructionsGit && settings.LocalRoot == "" && !storage.SharedDocumentsCheckout(s.snapshot.Home, config)
	settingsAppearanceRow(c, p, "同步分支", "", func(stacked bool) {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			selected, options := settings.Branch, []string{}
			if state != nil {
				options = state.branches
			}
			if followRemoteDefault && len(options) > 0 {
				options = append([]string{documentsGitDefaultBranch}, options...)
				if selected == "" {
					selected = documentsGitDefaultBranch
				}
			}
			if !ready || !slices.Contains(options, selected) {
				selected = ""
			}
			placeholder := "选择远程分支"
			if settings.RepositoryURL == "" {
				placeholder = "请先选择仓库"
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
			key := fmt.Sprintf("settings-%s-branch-%p", target, state)
			if ui.Select(c.Key(key), &selected, options).Grow(1).MinWidth(0).Height(36).Label("选择同步分支").Disabled(!ready || len(options) == 0 || s.busy || pending).Changed() && slices.Contains(options, selected) {
				if selected == documentsGitDefaultBranch {
					selected = ""
				}
				current, ok := s.materialsGitConfig()
				if ok && s.documentsGitBranchesCurrent(target) == state && selected != target.settings(current).Branch {
					next := target.settings(current)
					next.Branch = selected
					s.saveDocumentsGitSetting(target, target.scope(s.snapshot.Home, current), next, "branch")
				}
			}
			if miniButton(c, p, keyPrefix+"-branches-refresh", "refresh", "刷新远程分支", false).Disabled(loading || state != nil && state.terminalPreparing || s.busy || pending || settings.RepositoryURL == "").Clicked() {
				s.loadDocumentsGitBranches(target, true)
			}
		})
	})
	if state != nil && state.authenticating {
		label(c, "正在等待 Git 凭据助手完成验证…", 11, p.muted).Wrap()
		if action(c, keyPrefix+"-login-cancel", "close", "取消登录验证", p, false) {
			state.cancel()
		}
	}
	if state == nil || loading || settings.RepositoryURL == "" {
		return
	}
	if state.err != "" {
		label(c, state.err, 11, p.danger).Wrap().Selectable()
		if state.needsLogin && s.settingsDocumentsGitLoginActions(c, p, target, settings, state) {
			return
		}
		if !pending && action(c, keyPrefix+"-branches-retry", "refresh", "重试读取分支", p, false) {
			s.loadDocumentsGitBranches(target, true)
		}
	} else if ready && len(state.branches) == 0 {
		label(c, "请先在远程仓库创建分支", 11, p.amber).Wrap()
	} else if ready && !s.documentsGitBranchReady(target, settings) {
		label(c, fmt.Sprintf("已保存的分支 %s 不在远程，请重新选择。", settings.Branch), 11, p.amber).Wrap().Selectable()
	}
}

func (s *app) settingsDocumentsGitLoginActions(c *ui.Context, p palette, target documentsGitTarget, settings domain.MaterialsGitSyncSettings, state *materialsGitBranchesState) bool {
	key := string(target)
	running := s.materialsGitRunning()
	if target == projectInstructionsGit {
		running = s.projectInstructions != nil && s.projectInstructions.syncing
	}
	if candidate := workspace.MaterialsGitHTTPSUpgrade(settings.RepositoryURL); candidate != "" {
		label(c, "Git 登录需要 HTTPS。请解除关联，更新本地仓库的远程地址后重新选择目录。", 11, p.muted).Wrap()
		label(c, candidate, 11, p.text).Wrap().Selectable()
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
	disabled := s.busy || state.terminalPreparing || running
	ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
		if ui.Button(c.Key(key+"-login"), "登录并验证").Disabled(disabled).Clicked() {
			s.beginDocumentsGitLogin(target)
		}
		if ui.Button(c.Key(key+"-login-terminal"), "打开终端").Disabled(disabled).Clicked() {
			s.prepareDocumentsGitLoginTerminal(target, services, true)
		}
		if ui.Button(c.Key(key+"-login-copy"), "复制验证命令").Disabled(disabled).Clicked() {
			s.prepareDocumentsGitLoginTerminal(target, services, false)
		}
	})
	return true
}
