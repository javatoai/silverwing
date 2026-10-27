package desktop

import (
	"encoding/json"
	"errors"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

func (s *app) requestSettingsWorkspaceTools() {
	s.act("workspace.tools-options", "", "", "", "", "", map[string]any{"taskCreateToken": "settings-workspace-tools"})
}

func (s *app) applySettingsWorkspaceTools(action application.Action, result application.ActionResult) {
	if !s.settingsExtensionActionRelevant(action) {
		return
	}
	state := s.ensureSettingsExtensionRequests()
	data, err := json.Marshal(result.Data)
	var options []taskCreationWorkspaceToolOption
	if err == nil {
		err = json.Unmarshal(data, &options)
	}
	if err == nil && options == nil {
		err = errors.New("工作区工具选项未返回有效列表，请重新读取。")
	}
	state.workspaceToolsLoading = false
	if err != nil {
		state.workspaceToolsError = err.Error()
		return
	}
	state.workspaceTools, state.workspaceToolsLoaded, state.workspaceToolsError = options, true, ""
	s.stopTaskOpenTools()
	s.taskOpenTools = &taskOpenToolsState{home: s.snapshot.Home, config: s.configJSON, options: options, loaded: true}
}

func (s *app) settingsWorkspaceTools(c *ui.Context, p palette) {
	state := s.ensureSettingsExtensionRequests()
	scope := s.settingsExtensionCurrentScope()
	if state.workspaceToolsScope != scope {
		state.workspaceToolsScope = scope
		s.requestSettingsWorkspaceTools()
	}
	settingsHelpCard(c, p, "任务打开工具", "启用后在任务创建窗口和右键菜单中显示，并默认勾选。", func() {
		if state.workspaceToolsLoading {
			settingsMuted(c, p, "正在检查可用工具…")
		} else if state.workspaceToolsError != "" {
			label(c, state.workspaceToolsError, 11, p.amber).Wrap()
		} else if state.workspaceToolsLoaded && len(state.workspaceTools) == 0 {
			settingsMuted(c, p, "当前没有已注册的工作区工具。")
		}
		configured := stringArray(s.configArray("defaultWorkspaceToolIds"))
		for _, option := range normalizeTaskCreationWorkspaceTools(state.workspaceTools, configured) {
			tool := option
			selected := containsString(configured, tool.ID)
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("settings-workspace-tool-"+tool.ID), &selected, "").Shrink(0).
					Label("启用 " + tool.DisplayName).Disabled(s.busy).OnChange(func() {
					ids := removeString(configured, tool.ID)
					if selected {
						ids = append(ids, tool.ID)
					}
					s.saveConfigField("defaultWorkspaceToolIds", ids)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					settingsHelpLabel(c, p, tool.DisplayName, tool.Description, 12, 500)
					if !tool.Available {
						label(c, "当前不可用："+firstNonEmpty(tool.Reason, "未检测到可执行入口"), 10, p.amber).Wrap()
					}
				})
			})
		}
		if action(c, "settings-workspace-tools-refresh", "refresh", "重新检查工具", p, false) {
			s.requestSettingsWorkspaceTools()
		}
	})
}
