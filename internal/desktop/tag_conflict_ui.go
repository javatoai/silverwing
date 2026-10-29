package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

type tagWorkspaceCheckState struct {
	OperationID string                         `json:"operationId"`
	TaskID      string                         `json:"taskId"`
	ServiceID   string                         `json:"serviceId"`
	ModuleID    string                         `json:"moduleId"`
	Path        string                         `json:"path"`
	RequestKey  string                         `json:"requestKey"`
	Loading     bool                           `json:"loading"`
	Check       *application.TagWorkspaceCheck `json:"check,omitempty"`
	Error       string                         `json:"error,omitempty"`
}

func tagWorkspaceCheckEligible(record application.TagRecord) bool {
	state := strings.ToUpper(strings.TrimSpace(record.State))
	if state == "CONFLICT" {
		return true
	}
	return state == "FAILED" && strings.Contains(record.Detail, "特性工作区存在未提交改动")
}

func tagWorkspaceCheckStateKey(operationID string) string {
	return "tag-workspace-check:" + operationID
}

func (s *app) tagWorkspaceCheckState(operationID string) tagWorkspaceCheckState {
	var state tagWorkspaceCheckState
	_ = json.Unmarshal([]byte(*s.input(tagWorkspaceCheckStateKey(operationID), "")), &state)
	return state
}

func (s *app) saveTagWorkspaceCheckState(state tagWorkspaceCheckState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	*s.input(tagWorkspaceCheckStateKey(state.OperationID), "") = string(data)
}

func (s *app) tagConflictWorkspace(record application.TagRecord) (task, application.Workspace, string) {
	t := s.taskUnit.taskByID(record.TaskID)
	if t.id == "" || record.ServiceID == "" || record.ModuleID == "" {
		return task{}, application.Workspace{}, "未找到此 Tag 记录对应的任务或服务模块，已禁用工作区操作。"
	}
	var match application.Workspace
	count := 0
	for _, candidate := range t.workspaces {
		if candidate.ServiceID == record.ServiceID && candidate.ModuleID == record.ModuleID {
			match = candidate
			count++
		}
	}
	if count != 1 {
		return t, application.Workspace{}, "未找到唯一匹配的任务工作区，已禁用工作区操作。"
	}
	if strings.TrimSpace(match.Path) == "" {
		return t, application.Workspace{}, "任务工作区没有可用路径，已禁用工作区操作。"
	}
	info, err := os.Stat(match.Path)
	if err != nil || !info.IsDir() {
		return t, match, "工作区目录不存在或不可用：" + match.Path
	}
	return t, match, ""
}

func (s *app) renderTagConflictCard(c *ui.Context, p palette, record application.TagRecord) {
	state := s.tagWorkspaceCheckState(record.ID)
	t, workspace, workspaceError := s.tagConflictWorkspace(record)
	if workspaceError == "" && s.backend == nil {
		workspaceError = "应用后端不可用，无法执行工作区操作。"
	}
	conflict := strings.EqualFold(strings.TrimSpace(record.State), "CONFLICT")
	disabled := s.tagRecordBusy(record) || state.Loading || workspaceError != ""
	retryLabel, _ := tagHistoryRetryPresentation(record)
	if conflict {
		retryLabel = "重试冲突操作"
	}
	root := ui.Column(c).FillWidth().MinWidth(0).Gap(6).Label("Tag 恢复操作 · " + record.ID)
	open := ui.Local(root, "details", func() bool { return false })
	root.Children(func() {
		summary := tagFailureSummary(record)
		if item, ok := s.tagRecordJobItem(record); ok && item.Kind != "tag.workspace-check" && (item.Busy || item.Queued) {
			summary = "上次操作：" + summary
		}
		label(c, summary, 12, tagStatusErrorColor(s.dark)).Wrap().LineHeight(1.4)
		var details ui.CollapsibleParts
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).Wrap().AlignItems(ui.Center).Label("Tag 恢复动作 · " + record.ID).Children(func() {
			if tagRecordAction(c, p, "tag-conflict-check-"+record.ID, "refresh", "检查工作区", disabled).Label("检查工作区 · " + record.ID).Clicked() {
				s.beginTagWorkspaceCheck(t, workspace, record)
			}
			if tagHistoryRetryable(record) && tagRecordAction(c, p, "tag-conflict-retry-"+record.ID, "refresh", retryLabel, disabled).Label("重试 Tag · "+record.ID).Clicked() {
				s.queueTagAction(application.Action{Kind: "tag.retry", TaskID: t.id, ServiceID: workspace.ServiceID, ModuleID: workspace.ModuleID, Path: workspace.Path, Payload: s.tagHistoryRecordPayload(record)})
			}
			details = ui.CollapsibleBase(c.Key("failure-details"), open)
			details.Trigger.Height(28).Shrink(0).Padding(0, 8).Gap(5).AlignItems(ui.Center).Radius(8).
				Label("Tag 失败详情 · " + record.ID).Tooltip("展开处理提示与工作区详情").Children(func() {
				icon(c, map[bool]string{true: "chevron", false: "chevron-right"}[*open], 13, p.muted)
				label(c, "详情", 11, p.text).NoWrap()
			})
			if conflict && tagRecordAction(c, p, "tag-conflict-ide-"+record.ID, "code", "打开工作区", disabled).Label("打开工作区 · "+record.ID).Clicked() {
				s.act("system.ide", t.id, workspace.ServiceID, workspace.ModuleID, workspace.Path, "", map[string]any{
					"tool": string(workspace.Record.DevelopmentTool), "operationId": record.ID,
				})
			}
			if conflict && tagRecordAction(c, p, "tag-conflict-copy-"+record.ID, "copy", "复制冲突信息", false).Label("复制冲突信息 · "+record.ID).Clicked() {
				s.copyTextToClipboard(c.Services(), tagConflictCopyText(record), "冲突信息已复制")
			}
		})
		if state.Loading {
			label(c, "正在检查工作区…", 11, p.muted)
		}
		if state.Check != nil {
			if state.Check.Clean {
				label(c, "工作区干净，可以重试。", 11, p.green).Wrap().LineHeight(1.4)
			} else {
				label(c, "工作区仍有未提交改动，请处理后重试。", 11, p.amber).Wrap().LineHeight(1.4)
			}
		}
		if state.Error != "" && state.Error != workspaceError {
			label(c, "工作区检查失败，请展开详情查看。", 11, tagStatusErrorColor(s.dark)).Wrap()
		} else if workspaceError != "" {
			label(c, "工作区不可用，请展开详情查看。", 11, tagStatusErrorColor(s.dark)).Wrap()
		}
		details.Panel(func() { s.renderTagFailureDetails(c, p, record, state, workspaceError) }).FillWidth().MinWidth(0).Gap(6).Padding(4, 0)
	})
}

// Both recoverable failures share an actionable prompt; conflict files and
// workspace check results remain available on demand.
func (s *app) renderTagFailureDetails(c *ui.Context, p palette, record application.TagRecord, state tagWorkspaceCheckState, workspaceError string) {
	item, _ := s.tagRecordJobItem(record)
	if prompt := tagFailureAssistanceText(record, item.FailureKind); strings.TrimSpace(prompt) != "" {
		ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			label(c, prompt, 11, p.text).Shrink(1).MinWidth(0).Wrap().LineHeight(1.5).Selectable()
			if tagRecordAction(c, p, "tag-failure-prompt-copy-"+record.ID, "copy", "复制", false).
				Label("复制处理提示 · " + record.ID).Tooltip("复制给 AI 的完整处理提示").Clicked() {
				s.copyTextToClipboard(c.Services(), prompt, "已复制")
			}
		})
	}
	if strings.EqualFold(strings.TrimSpace(record.State), "CONFLICT") {
		label(c, "冲突文件", 11, p.text).FontWeight(600)
		if len(record.ConflictFiles) == 0 {
			label(c, "未返回具体冲突文件。", 11, p.muted).Wrap()
		}
		for _, file := range record.ConflictFiles {
			label(c, "• "+file, 11, p.text).Wrap().Selectable()
		}
	}
	if _, explanation := tagHistoryRetryPresentation(record); explanation != "" && tagHistoryRetryable(record) {
		label(c, explanation, 11, p.muted).Wrap().LineHeight(1.5)
	}
	for _, message := range []string{workspaceError, state.Error} {
		if message != "" {
			label(c, message, 11, p.danger).Wrap().LineHeight(1.5)
		}
	}
	if state.Check != nil && len(state.Check.Changes) > 0 {
		label(c, "工作区检查结果", 11, p.text).FontWeight(600)
		for _, change := range state.Check.Changes {
			label(c, change, 11, p.text).Wrap().Selectable()
		}
	}
}

func tagRecordAction(c *ui.Context, p palette, key, image, text string, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Row().Shrink(0).Height(28).Padding(0, 8).Gap(5).AlignItems(ui.Center).
		Radius(8).Border(1, p.line).Label(text).Tooltip(text).Disabled(disabled)
	color := p.text
	if disabled {
		color = p.muted
	} else if b.Hovered() {
		b.Background(p.soft)
	}
	b.Children(func() {
		icon(c, image, 13, color)
		label(c, text, 11, color).NoWrap()
	})
	return b
}

func (s *app) beginTagWorkspaceCheck(t task, workspace application.Workspace, record application.TagRecord) {
	if s.tagRecordBusy(record) || !tagWorkspaceCheckEligible(record) || t.id != record.TaskID ||
		workspace.ServiceID != record.ServiceID || workspace.ModuleID != record.ModuleID || workspace.Path == "" {
		return
	}
	if s.backend == nil {
		s.saveTagWorkspaceCheckState(tagWorkspaceCheckState{
			OperationID: record.ID, TaskID: t.id, ServiceID: workspace.ServiceID, ModuleID: workspace.ModuleID,
			Path: workspace.Path, Error: "应用后端不可用，无法执行工作区操作。",
		})
		return
	}
	counter := s.input("tag-workspace-check:request-sequence", "0")
	sequence, _ := strconv.ParseUint(*counter, 10, 64)
	sequence++
	*counter = strconv.FormatUint(sequence, 10)
	requestKey := fmt.Sprintf("tag-check-%d", sequence)
	state := tagWorkspaceCheckState{
		OperationID: record.ID, TaskID: t.id, ServiceID: workspace.ServiceID, ModuleID: workspace.ModuleID,
		Path: workspace.Path, RequestKey: requestKey, Loading: true,
	}
	s.saveTagWorkspaceCheckState(state)
	payload := s.tagHistoryRecordPayload(record)
	payload["requestKey"] = requestKey
	if !s.queueTagAction(application.Action{Kind: "tag.workspace-check", TaskID: t.id, ServiceID: workspace.ServiceID, ModuleID: workspace.ModuleID, Path: workspace.Path, Payload: payload}) {
		state.Loading = false
		state.Error = s.lastError
		s.saveTagWorkspaceCheckState(state)
	}
}

func (s *app) tagWorkspaceCheckActionCurrent(action application.Action) (tagWorkspaceCheckState, bool) {
	if action.Kind != "tag.workspace-check" {
		return tagWorkspaceCheckState{}, false
	}
	if _, scopedRequest := action.Payload["tagHistoryTaskPath"]; scopedRequest && !s.tagHistoryResultRelevant(action) {
		return tagWorkspaceCheckState{}, false
	}
	opID := payloadString(action.Payload, "operationId")
	requestKey := payloadString(action.Payload, "requestKey")
	state := s.tagWorkspaceCheckState(opID)
	if opID == "" || requestKey == "" || !state.Loading || state.RequestKey != requestKey ||
		state.OperationID != opID || state.TaskID != action.TaskID || state.ServiceID != action.ServiceID ||
		state.ModuleID != action.ModuleID || workspaceHealthPathKey(state.Path) != workspaceHealthPathKey(action.Path) {
		return state, false
	}
	var current application.TagRecord
	found := false
	for _, candidate := range s.snapshot.TagHistory {
		if candidate.ID == opID && candidate.TaskID == action.TaskID && candidate.ServiceID == action.ServiceID && candidate.ModuleID == action.ModuleID {
			current, found = candidate, true
			break
		}
	}
	if !found || !tagWorkspaceCheckEligible(current) {
		state.Loading = false
		state.RequestKey = ""
		state.Check = nil
		state.Error = ""
		s.saveTagWorkspaceCheckState(state)
		return state, false
	}
	t, workspace, workspaceError := s.tagConflictWorkspace(current)
	if t.id != action.TaskID || workspace.ServiceID != action.ServiceID || workspace.ModuleID != action.ModuleID ||
		workspaceHealthPathKey(workspace.Path) != workspaceHealthPathKey(action.Path) {
		state.Loading = false
		state.Check = nil
		state.Error = "当前任务工作区已变化，未应用过期的检查结果；请重新检查。"
		s.saveTagWorkspaceCheckState(state)
		return state, false
	}
	if workspaceError != "" {
		state.Loading = false
		state.Check = nil
		state.Error = workspaceError
		s.saveTagWorkspaceCheckState(state)
		return state, false
	}
	return state, true
}

func (s *app) applyTagWorkspaceCheckResult(action application.Action, result application.ActionResult) {
	state, current := s.tagWorkspaceCheckActionCurrent(action)
	if !current {
		return
	}
	var check application.TagWorkspaceCheck
	data, err := json.Marshal(result.Data)
	if err == nil && result.Data != nil {
		err = json.Unmarshal(data, &check)
	} else if err == nil {
		err = fmt.Errorf("没有返回工作区检查结果")
	}
	if err != nil {
		state.Loading = false
		state.Error = "工作区检查结果格式错误：" + err.Error()
		state.Check = nil
		s.saveTagWorkspaceCheckState(state)
		return
	}
	state.Loading = false
	state.Error = ""
	state.Check = &check
	s.saveTagWorkspaceCheckState(state)
}

func (s *app) applyTagWorkspaceCheckFailure(action application.Action, err error) bool {
	if action.Kind != "tag.workspace-check" {
		return false
	}
	state, current := s.tagWorkspaceCheckActionCurrent(action)
	if !current {
		return true
	}
	state.Loading = false
	state.Check = nil
	state.Error = "工作区检查失败"
	if err != nil {
		state.Error = err.Error()
	}
	s.saveTagWorkspaceCheckState(state)
	return true
}
