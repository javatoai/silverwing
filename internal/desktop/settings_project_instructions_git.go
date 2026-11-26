package desktop

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type documentsGitTarget string

const (
	taskMaterialsGit       documentsGitTarget = "materials-git"
	projectInstructionsGit documentsGitTarget = "project-instructions-git"
)

func (target documentsGitTarget) settings(config domain.AppConfig) domain.MaterialsGitSyncSettings {
	if target == projectInstructionsGit {
		return config.ProjectInstructionsGitSettings()
	}
	return config.MaterialsGitSettings()
}

func (target documentsGitTarget) scope(home string, config domain.AppConfig) workspace.MaterialsGitScope {
	if target == projectInstructionsGit {
		return application.ProjectInstructionsGitScope(home, config)
	}
	return application.MaterialsGitScope(home, config)
}

func (target documentsGitTarget) inputKey(field string) string {
	if target == projectInstructionsGit {
		return "setting:projectInstructionsGit:" + field
	}
	return "setting:materialsGitSync:" + field
}

func (target documentsGitTarget) role() string {
	if target == projectInstructionsGit {
		return domain.ProjectInstructionsRepositoryRole
	}
	return domain.TaskMaterialsRepositoryRole
}

func (s *app) documentsGitSettingsPending(target documentsGitTarget, config domain.AppConfig) bool {
	if target == taskMaterialsGit {
		return s.materialsGitSettingsPending(config)
	}
	settings := target.settings(config)
	interval := s.inputs[target.inputKey("intervalMinutes")]
	if interval == nil {
		return false
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(*interval))
	return err != nil || minutes != settings.IntervalMinutes
}

func (s *app) saveDocumentsGitSetting(target documentsGitTarget, scope workspace.MaterialsGitScope, settings domain.MaterialsGitSyncSettings, field string) {
	config, ok := s.materialsGitConfig()
	if !ok || target.scope(s.snapshot.Home, config) != scope {
		s.lastError = "仓库配置已变化，请重新编辑。"
		return
	}
	var value any
	var projectField string
	switch field {
	case "branch":
		value, projectField = settings.Branch, "projectInstructionsGitBranch"
	case "intervalMinutes":
		value, projectField = settings.IntervalMinutes, "projectInstructionsGitIntervalMinutes"
	case "enabled":
		value, projectField = settings.Enabled, "projectInstructionsGitEnabled"
	default:
		return
	}
	encoded, _ := json.Marshal(value)
	var update application.ConfigFieldsUpdate
	if target == taskMaterialsGit {
		patch := map[string]json.RawMessage{field: encoded}
		if field == "branch" {
			patch["enabled"] = json.RawMessage("false")
		}
		update.ObjectFields = map[string]map[string]json.RawMessage{"materialsGitSync": patch}
	} else {
		if field == "branch" && !s.captureProjectInstructionsInput() {
			return
		}
		update.Fields = map[string]json.RawMessage{projectField: encoded}
	}
	s.dispatch(application.Action{Kind: "config.fields.save", Payload: map[string]any{
		"update": update, "documentsGitScope": scope, "documentsGitRole": target.role(),
	}})
}

func (s *app) settingsProjectInstructions(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "项目资料 Git 自动同步", "项目资料保存在仓库的 "+domain.ProjectInstructionsDirectory+" 目录。", func() {
		s.settingsDocumentsGitSync(c, p, projectInstructionsGit)
	})
}

func (s *app) settingsDocumentsGitLocation(c *ui.Context, p palette, target documentsGitTarget, scope workspace.MaterialsGitScope, bound, pending bool) {
	key := string(target)
	disabled := pending || s.busy || s.backend == nil || s.settingsUnit.state.settingsPickerBusy
	settingsAppearanceRow(c, p, "本地仓库目录", "选择已克隆的 Git 仓库根目录。", func(bool) {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			path, choose := firstNonEmpty(scope.Root, "尚未关联"), "选择仓库"
			if bound {
				choose = "更换目录"
			}
			label(c, path, 12, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				if settingsSystemAction(c, key+"-bind", "folder", choose, p, disabled) {
					s.pickNativePath("选择已克隆的 Git 仓库", firstNonEmpty(scope.Root, s.snapshot.Home), true, nil, func(path string) {
						s.changeDocumentsGitBinding(target, scope, path)
					})
				}
				if bound && settingsSystemAction(c, key+"-unbind", "", "解除关联", p, disabled) {
					s.changeDocumentsGitBinding(target, scope, "")
				}
			})
		})
	})
	settingsAppearanceRow(c, p, "Git 仓库地址", "从本地仓库的远程配置读取。", func(bool) {
		label(c, firstNonEmpty(scope.URL, "尚未读取"), 12, p.text).FillWidth().MinWidth(0).Wrap().Selectable()
	})
}

func (s *app) changeDocumentsGitBinding(target documentsGitTarget, scope workspace.MaterialsGitScope, path string) {
	config, ok := s.materialsGitConfig()
	if !ok || target.scope(s.snapshot.Home, config) != scope {
		s.lastError = "仓库配置已变化，请重新选择。"
		return
	}
	if s.busy || s.documentsGitSettingsPending(target, config) {
		s.lastError = "仓库设置正在保存，请稍后重试。"
		return
	}
	browser := s
	running := s.materialsGitRunning()
	if target == projectInstructionsGit {
		browser, running = nil, false
		if view := s.projectInstructions; s.projectInstructionsCurrent(view) {
			browser, running = view.browser, view.syncing
		}
	}
	if running || browser != nil && browser.materialsFileMutationRunning() {
		s.lastError = "请等待同步和文件操作完成后再更改仓库。"
		return
	}
	if browser != nil {
		if err := browser.captureMaterialTextInput(); err != nil {
			s.lastError = err.Error()
			return
		}
		if err := browser.captureMarkdownEditorInput(browser.materialsUnit.state.markdown); err != nil {
			s.lastError = err.Error()
			return
		}
	}
	kind := "config.documents-git.bind"
	if path == "" {
		kind = "config.documents-git.unbind"
	}
	s.dispatch(application.Action{Kind: kind, Path: path, Payload: map[string]any{"role": target.role(), "scope": scope}})
}
