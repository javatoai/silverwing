package desktop

import (
	"encoding/json"
	"errors"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type taskCreationWorkspaceToolOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason"`
}

func normalizeTaskCreationWorkspaceTools(items []taskCreationWorkspaceToolOption, configuredIDs []string) []taskCreationWorkspaceToolOption {
	result := make([]taskCreationWorkspaceToolOption, 0, len(items)+len(configuredIDs))
	seen := make(map[string]bool, len(items)+len(configuredIDs))
	for _, option := range items {
		option.ID = strings.TrimSpace(option.ID)
		if option.ID == "" || seen[option.ID] || domain.IsLegacyCursorToolID(option.ID) {
			continue
		}
		if option.DisplayName == "" {
			option.DisplayName = option.ID
		}
		seen[option.ID] = true
		result = append(result, option)
	}
	for _, id := range configuredIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || domain.IsLegacyCursorToolID(id) {
			continue
		}
		seen[id] = true
		result = append(result, taskCreationWorkspaceToolOption{ID: id, DisplayName: workspaceToolDisplayName(id), Reason: "当前版本未返回此工具的注册和可用状态"})
	}
	return result
}

func workspaceToolDisplayName(id string) string {
	switch id {
	case "codex":
		return "Codex"
	default:
		return id
	}
}

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
}

func (s *app) settingsWorkspaceTools(c *ui.Context, p palette) {
	state := s.ensureSettingsExtensionRequests()
	scope := s.settingsExtensionCurrentScope()
	if state.workspaceToolsScope != scope {
		state.workspaceToolsScope = scope
		s.requestSettingsWorkspaceTools()
	}
	settingsHelpCardWithActions(c, p, "任务打开工具", "启用的工具会在任务创建完成后自动打开，也可从任务右键菜单打开。", func() {
		if settingsCardAction(c, p, "settings-workspace-tools-refresh", "refresh", "重新检查工具") {
			s.requestSettingsWorkspaceTools()
		}
	}, func() {
		if state.workspaceToolsLoading {
			settingsMuted(c, p, "正在检查可用工具…")
		} else if state.workspaceToolsError != "" {
			label(c, state.workspaceToolsError, 11, p.amber).Wrap()
		} else if state.workspaceToolsLoaded && len(state.workspaceTools) == 0 {
			settingsMuted(c, p, "当前没有已注册的工作区工具。")
		}
		configured := domain.WithoutLegacyCursorTool(stringArray(s.configArray("defaultWorkspaceToolIds")))
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
	})
}
