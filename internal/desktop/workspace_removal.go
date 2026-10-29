package desktop

import (
	"encoding/json"
	"strconv"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

const workspaceRemovalRequestSequenceInput = "workspace-removal:request-sequence"

type workspaceRemovalState struct {
	TaskID, ServiceID, ModuleID, Path string
	Home, TaskPath, TaskCreatedAt     string
	RequestID                         string
	Preview                           *workspace.WorkspaceModuleRemovalPreview
	Open, Loading, Acknowledge        bool
	Removing                          bool
	Error                             string
	Scroll                            ui.ScrollState
}

func (s *app) beginWorkspaceRemoval(task task, item application.Workspace) {
	if s.busy {
		return
	}
	if s.backend == nil {
		s.lastError = "应用后端不可用。"
		return
	}
	if !s.workspaceRemovalTaskMatches(task, item) {
		return
	}
	state := &workspaceRemovalState{
		TaskID: task.id, ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		Home: s.snapshot.Home, TaskPath: task.path, TaskCreatedAt: task.createdAt,
		RequestID: s.nextWorkspaceRemovalRequestID(), Open: true, Loading: true,
	}
	s.workspaceUnit.state.workspaceRemoval = state
	s.act("workspace.remove-preview", task.id, item.ServiceID, item.ModuleID, item.Path, "", state.actionPayload(nil))
}

func (s *app) nextWorkspaceRemovalRequestID() string {
	if s.inputs == nil {
		s.inputs = make(map[string]*string)
	}
	sequenceInput := s.input(workspaceRemovalRequestSequenceInput, "0")
	sequence, _ := strconv.Atoi(*sequenceInput)
	sequence++
	if sequence < 1 {
		sequence = 1
	}
	*sequenceInput = strconv.Itoa(sequence)
	return *sequenceInput
}

func (state *workspaceRemovalState) actionPayload(fields map[string]any) map[string]any {
	payload := map[string]any{
		"workspaceRemovalRequestID":     state.RequestID,
		"workspaceRemovalHome":          state.Home,
		"workspaceRemovalTaskPath":      state.TaskPath,
		"workspaceRemovalTaskCreatedAt": state.TaskCreatedAt,
	}
	for key, value := range fields {
		payload[key] = value
	}
	return payload
}

// applyWorkspaceRemovalResult is called by actions.go after a successful
// workspace.remove-preview or workspace.remove dispatch. Preview results
// require the originating dialog to remain open; execution results do not.
func (s *app) applyWorkspaceRemovalResult(action application.Action, result application.ActionResult) {
	if s.win != nil && s.win.IsDestroyed() {
		return
	}
	state := s.workspaceUnit.state.workspaceRemoval
	if state == nil || !state.matches(action) {
		return
	}
	if !s.workspaceRemovalIdentityCurrent(state) {
		s.workspaceUnit.state.workspaceRemoval = nil
		return
	}
	state.Loading = false
	state.Error = ""
	switch action.Kind {
	case "workspace.remove-preview":
		var preview workspace.WorkspaceModuleRemovalPreview
		if err := decodeWorkspaceValue(result.Data, &preview); err != nil {
			state.Error = "移除预览格式错误：" + err.Error()
			return
		}
		if workspaceHealthPathKey(preview.WorkspacePath) != workspaceHealthPathKey(state.Path) ||
			preview.ServiceID != state.ServiceID || preview.ModuleID != state.ModuleID {
			state.Error = "移除预览与当前工作区不匹配，请重新检查。"
			return
		}
		state.Preview = &preview
		state.Acknowledge = false
	case "workspace.remove":
		var removed workspace.WorkspaceModuleRemovalResult
		if err := decodeWorkspaceValue(result.Data, &removed); err != nil {
			state.Removing = false
			state.Error = "移除结果格式错误：" + err.Error()
			s.lastError = state.Error
			s.workspaceUnit.state.workspaceRemoval = nil
			return
		}
		s.workspaceUnit.state.workspaceRemoval = nil
		s.lastMessage = strings.TrimSpace(result.Message)
		if s.lastMessage == "" {
			s.lastMessage = "模块已删除"
		}
		if cleanupError := strings.TrimSpace(removed.CleanupError); cleanupError != "" {
			s.lastError = cleanupError
			if backupPath := strings.TrimSpace(removed.RetainedBackupPath); backupPath != "" {
				s.lastError += "\n保留备份：" + backupPath
			}
		}
	}
}

// applyWorkspaceRemovalFailure reports execution errors even after Escape,
// backdrop dismissal, or the cancel button closes the preview dialog.
func (s *app) applyWorkspaceRemovalFailure(action application.Action, err error) {
	if s.win != nil && s.win.IsDestroyed() {
		return
	}
	state := s.workspaceUnit.state.workspaceRemoval
	if state == nil || !state.matches(action) {
		return
	}
	if !s.workspaceRemovalIdentityCurrent(state) {
		s.workspaceUnit.state.workspaceRemoval = nil
		return
	}
	state.Loading = false
	if err == nil {
		return
	}
	state.Error = err.Error()
	if action.Kind == "workspace.remove" {
		state.Removing = false
		s.lastError = err.Error()
		if !state.Open {
			s.workspaceUnit.state.workspaceRemoval = nil
		}
	}
}

func (state *workspaceRemovalState) matches(action application.Action) bool {
	if state == nil || action.TaskID != state.TaskID || action.ServiceID != state.ServiceID ||
		action.ModuleID != state.ModuleID || !sameWorkspaceRemovalPath(action.Path, state.Path) ||
		!state.payloadMatches(action.Payload) {
		return false
	}
	switch action.Kind {
	case "workspace.remove-preview":
		return state.Open && !state.Removing
	case "workspace.remove":
		return state.Removing
	default:
		return false
	}
}

func (state *workspaceRemovalState) payloadMatches(payload map[string]any) bool {
	return state != nil && state.RequestID != "" &&
		payloadString(payload, "workspaceRemovalRequestID") == state.RequestID &&
		sameWorkspaceRemovalPath(payloadString(payload, "workspaceRemovalHome"), state.Home) &&
		sameWorkspaceRemovalPath(payloadString(payload, "workspaceRemovalTaskPath"), state.TaskPath) &&
		payloadString(payload, "workspaceRemovalTaskCreatedAt") == state.TaskCreatedAt
}

func (s *app) workspaceRemovalTaskMatches(task task, item application.Workspace) bool {
	current := s.taskUnit.currentTask()
	if current.id == "" || current.id != task.id || !sameWorkspaceRemovalPath(current.path, task.path) ||
		current.createdAt != task.createdAt {
		return false
	}
	for _, candidate := range current.workspaces {
		if candidate.ServiceID == item.ServiceID && candidate.ModuleID == item.ModuleID &&
			sameWorkspaceRemovalPath(candidate.Path, item.Path) {
			return true
		}
	}
	return false
}

func (s *app) workspaceRemovalIdentityCurrent(state *workspaceRemovalState) bool {
	if !s.workspaceRemovalTaskIdentityCurrent(state) {
		return false
	}
	if state.Removing {
		return true
	}
	return s.workspaceRemovalWorkspaceCurrent(state)
}

func (s *app) workspaceRemovalTaskIdentityCurrent(state *workspaceRemovalState) bool {
	if state == nil {
		return false
	}
	current := s.taskUnit.currentTask()
	if current.id != state.TaskID || !sameWorkspaceRemovalPath(current.path, state.TaskPath) ||
		current.createdAt != state.TaskCreatedAt || !sameWorkspaceRemovalPath(s.snapshot.Home, state.Home) {
		return false
	}
	return true
}

func (s *app) workspaceRemovalWorkspaceCurrent(state *workspaceRemovalState) bool {
	if state == nil {
		return false
	}
	current := s.taskUnit.currentTask()
	for _, item := range current.workspaces {
		if item.ServiceID == state.ServiceID && item.ModuleID == state.ModuleID &&
			sameWorkspaceRemovalPath(item.Path, state.Path) {
			return true
		}
	}
	return false
}

func sameWorkspaceRemovalPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return left == right
	}
	return workspaceHealthPathKey(left) == workspaceHealthPathKey(right)
}

func (s *app) confirmWorkspaceRemoval() {
	state := s.workspaceUnit.state.workspaceRemoval
	if state == nil || !state.Open || state.Preview == nil || state.Loading || state.Removing || s.busy ||
		!s.workspaceRemovalIdentityCurrent(state) ||
		(state.Preview.RequiresRiskConfirmation && !state.Acknowledge) {
		return
	}
	if s.backend == nil {
		state.Error = "应用后端不可用。"
		s.lastError = state.Error
		return
	}
	state.Loading = true
	state.Removing = true
	state.Error = ""
	preview := *state.Preview
	s.act("workspace.remove", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", state.actionPayload(map[string]any{
		"preview": preview,
		"confirmation": map[string]any{
			"fingerprint": preview.Fingerprint, "acknowledgeDataLoss": state.Acknowledge,
		},
	}))
}

func (s *app) renderWorkspaceRemovalDialog(c *ui.Context, p palette) {
	state := s.workspaceUnit.state.workspaceRemoval
	if state == nil {
		return
	}
	if !s.workspaceRemovalIdentityCurrent(state) {
		s.workspaceUnit.state.workspaceRemoval = nil
		return
	}
	if !state.Open {
		if !state.Removing {
			s.workspaceUnit.state.workspaceRemoval = nil
		}
		return
	}
	ui.DialogBase(c.Key("workspace-module-removal-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(workspaceRemovalDialogWidth(w)).MaxHeight(workspaceRemovalDialogMaxHeight(h)).
			Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "delete", 18, p.amber)
				title := "删除模块"
				if state.Preview != nil && state.Preview.ModuleName != "" {
					title += " · " + state.Preview.ModuleName
				}
				label(c, title, 17, p.text).FontWeight(600).Grow(1)
			})
			ui.Scroll(c).TrackScroll(&state.Scroll).Grow(1).MinHeight(0).FillWidth().Gap(12).Children(func() {
				if state.Loading {
					loading := "正在检查工作区、未提交内容和未推送提交…"
					if state.Removing {
						loading = "正在移除模块并清理临时备份…"
					}
					label(c, loading, 12, p.muted).Wrap()
				}
				if state.Error != "" {
					ui.Box(c).FillWidth().Padding(8, 10).Radius(8).Background(p.amberBg).Children(func() {
						label(c, state.Error, 12, p.amber).Wrap()
					})
				}
				if preview := state.Preview; preview != nil {
					message := "工作区将先移动到同级临时备份；任务清单更新成功后才清理备份。"
					if preview.PathMissing {
						message = "工作区目录已缺失，将只清理任务记录。"
					}
					label(c, message, 12, p.text).Wrap()
					if len(s.taskUnit.taskByID(state.TaskID).workspaces) == 1 {
						label(c, "这是最后一个工作区。移除后任务、需求资料和笔记仍会保留，可随时重新添加服务。", 12, p.muted).Wrap()
					}
					ui.Column(c).FillWidth().Padding(10).Gap(4).Background(p.soft).Radius(8).Children(func() {
						label(c, "路径："+preview.WorkspacePath, 11, p.muted).Wrap().Selectable()
						label(c, "分支："+preview.Branch, 11, p.muted).Wrap()
						base := "主分支：未配置"
						if preview.BaseRef != nil && strings.TrimSpace(*preview.BaseRef) != "" {
							base = "主分支：" + *preview.BaseRef
						}
						label(c, base, 11, p.muted).Wrap()
						label(c, "相对主分支：ahead "+fmtInt(preview.CommitsAheadOfBase)+" / behind "+fmtInt(preview.CommitsBehindBase), 11, p.muted).Wrap()
						label(c, "未推送提交："+fmtInt(preview.UnpushedCommits), 11, p.muted).Wrap()
						if len(preview.ChangedFiles) > 0 {
							label(c, "未提交文件：", 11, p.muted).Wrap()
							for _, file := range preview.ChangedFiles {
								label(c, "· "+file, 11, p.muted).Wrap().Selectable()
							}
						}
					})
					if preview.RequiresRiskConfirmation {
						ui.Row(c).FillWidth().Padding(7, 8).Gap(8).AlignItems(ui.Center).Background(p.amberBg).Radius(8).Children(func() {
							ui.Checkbox(c.Key("workspace-removal-risk"), &state.Acknowledge, "").Shrink(0)
							label(c, "我确认未提交文件会永久丢失，并接受提交差异及未推送提交不再保留在该工作区中的风险。", 11, p.amber).Wrap().Grow(1)
						})
					}
				}
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "workspace-removal-cancel", "close", "取消", p, false) {
					state.Open = false
				}
				canRemove := state.Preview != nil && !state.Loading && !state.Removing && !s.busy &&
					(!state.Preview.RequiresRiskConfirmation || state.Acknowledge)
				button := ui.ButtonBase(c.Key("workspace-removal-confirm")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label("删除模块").Tooltip("删除模块").Disabled(!canRemove)
				button.Background(p.amber).Children(func() {
					icon(c, "delete", 17, p.surface)
					label(c, "删除模块", 13, p.surface).NoWrap()
				})
				if button.Clicked() && canRemove {
					s.confirmWorkspaceRemoval()
				}
			})
		})
	})
}

func workspaceRemovalDialogWidth(windowWidth float32) float32 {
	return max(float32(0), min(float32(700), windowWidth-32))
}

func workspaceRemovalDialogMaxHeight(windowHeight float32) float32 {
	return max(float32(0), min(float32(660), windowHeight-32))
}

func decodeWorkspaceValue(value any, target any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func fmtInt(value int) string {
	return strconv.Itoa(value)
}
