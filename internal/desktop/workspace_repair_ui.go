package desktop

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type workspaceRepairState struct {
	TaskID, ServiceID, ModuleID, Path string
	TaskPath, TaskCreatedAt           string
	ServiceName, WorkspaceLabel       string
	RequestKey, RequestKind           string
	Open, Loading                     bool
	ReuseRemoteBranch                 bool
	ShareCheckedOutBranch             bool
	Preview                           *workspace.WorkspaceRepairPreview
	Result                            *workspace.WorkspaceRepairResult
	Error                             string
}

var workspaceRepairRequestSequence atomic.Uint64

func workspaceRepairAction(kind string) bool {
	return kind == "workspace.repair-preview" || kind == "workspace.repair"
}

func (s *app) beginWorkspaceRepair(t task, item application.Workspace) {
	if s.busy || t.id == "" || item.ServiceID == "" || item.Path == "" {
		return
	}
	state := &workspaceRepairState{
		TaskID: t.id, ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		TaskPath: t.path, TaskCreatedAt: t.createdAt, ServiceName: item.Record.ServiceName,
		WorkspaceLabel: firstNonEmpty(item.Record.ModuleName, item.Record.ServiceName),
		Open:           true,
	}
	s.workspaceRepair = state
	s.requestWorkspaceRepairPreview(state)
}

func (s *app) requestWorkspaceRepairPreview(state *workspaceRepairState) {
	if state == nil || s.workspaceRepair != state || state.Loading || s.busy || !s.workspaceRepairIdentityCurrent(state) {
		return
	}
	state.Preview = nil
	state.Result = nil
	state.Error = ""
	state.ReuseRemoteBranch = false
	state.ShareCheckedOutBranch = false
	s.startWorkspaceRepairRequest(state, "workspace.repair-preview", map[string]any{
		"selectionKeys": []string{state.Path},
	})
}

func (s *app) startWorkspaceRepairRequest(state *workspaceRepairState, kind string, payload map[string]any) {
	if state == nil || s.workspaceRepair != state || !state.Open || s.busy || !s.workspaceRepairIdentityCurrent(state) {
		return
	}
	if s.backend == nil {
		state.Loading = false
		state.Error = "应用后端不可用。"
		return
	}
	sequence := workspaceRepairRequestSequence.Add(1)
	if sequence == 0 {
		sequence = workspaceRepairRequestSequence.Add(1)
	}
	state.RequestKey = strconv.FormatUint(sequence, 10)
	state.RequestKind = kind
	state.Loading = true
	state.Error = ""
	if payload == nil {
		payload = make(map[string]any)
	}
	payload["requestKey"] = state.RequestKey
	payload["selectionKeys"] = []string{state.Path}
	s.act(kind, state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", payload)
}

func (s *app) applyWorkspaceRepairResult(action application.Action, result application.ActionResult) {
	state := s.workspaceRepair
	if state == nil || !state.matches(action) {
		return
	}
	if !state.Open || !s.workspaceRepairIdentityCurrent(state) {
		s.workspaceRepair = nil
		return
	}
	state.Loading = false
	state.Error = ""
	switch action.Kind {
	case "workspace.repair-preview":
		var preview workspace.WorkspaceRepairPreview
		if err := decodeWorkspaceValue(result.Data, &preview); err != nil {
			state.Error = "修复预览格式错误：" + err.Error()
			return
		}
		if workspaceHealthPathKey(preview.WorkspacePath) != workspaceHealthPathKey(state.Path) ||
			state.WorkspaceLabel != "" && preview.ServiceName != state.WorkspaceLabel {
			state.Error = "修复预览与当前工作区身份不匹配，请重新检查。"
			return
		}
		state.Preview = &preview
		state.ReuseRemoteBranch = false
		state.ShareCheckedOutBranch = false
	case "workspace.repair":
		var repaired workspace.WorkspaceRepairResult
		if err := decodeWorkspaceValue(result.Data, &repaired); err != nil {
			state.Error = "修复结果格式错误：" + err.Error()
			return
		}
		if workspaceHealthPathKey(repaired.WorkspacePath) != workspaceHealthPathKey(state.Path) ||
			state.WorkspaceLabel != "" && repaired.ServiceName != state.WorkspaceLabel {
			state.Error = "修复结果与当前工作区身份不匹配，已丢弃结果。"
			return
		}
		if strings.TrimSpace(repaired.Message) == "" {
			repaired.Message = result.Message
		}
		state.Preview = nil
		state.Result = &repaired
	}
}

func (s *app) applyWorkspaceRepairFailure(action application.Action, err error) {
	state := s.workspaceRepair
	if state == nil || !state.matches(action) {
		return
	}
	if !state.Open || !s.workspaceRepairIdentityCurrent(state) {
		s.workspaceRepair = nil
		return
	}
	state.Loading = false
	if err != nil {
		state.Error = err.Error()
	}
}

func (state *workspaceRepairState) matches(action application.Action) bool {
	return state != nil && state.Open && action.Kind == state.RequestKind &&
		action.TaskID == state.TaskID && action.ServiceID == state.ServiceID && action.ModuleID == state.ModuleID &&
		workspaceHealthPathKey(action.Path) == workspaceHealthPathKey(state.Path) &&
		payloadString(action.Payload, "requestKey") == state.RequestKey
}

func (s *app) workspaceRepairIdentityCurrent(state *workspaceRepairState) bool {
	if state == nil {
		return false
	}
	current := s.currentTask()
	if current.id != state.TaskID || current.path != state.TaskPath || current.createdAt != state.TaskCreatedAt {
		return false
	}
	for _, item := range current.workspaces {
		if item.ServiceID == state.ServiceID && item.ModuleID == state.ModuleID &&
			workspaceHealthPathKey(item.Path) == workspaceHealthPathKey(state.Path) &&
			(state.ServiceName == "" || item.Record.ServiceName == state.ServiceName) &&
			(state.WorkspaceLabel == "" || firstNonEmpty(item.Record.ModuleName, item.Record.ServiceName) == state.WorkspaceLabel) {
			return true
		}
	}
	return false
}

func (s *app) confirmWorkspaceRepair() {
	state := s.workspaceRepair
	if state == nil || state.Preview == nil || !state.Preview.CanRepair || state.Loading || s.busy || state.Error != "" ||
		(state.Preview.RequiresRemoteReuseConfirmation && !state.ReuseRemoteBranch) ||
		(state.Preview.RequiresSharedBranchConfirmation && !state.ShareCheckedOutBranch) ||
		!s.workspaceRepairIdentityCurrent(state) {
		return
	}
	preview := *state.Preview
	confirmation := workspace.WorkspaceRepairConfirmation{
		ReuseRemoteBranch: state.ReuseRemoteBranch, ShareCheckedOutBranch: state.ShareCheckedOutBranch,
	}
	state.Result = nil
	s.startWorkspaceRepairRequest(state, "workspace.repair", map[string]any{
		"preview": preview, "confirmation": confirmation,
	})
}

func (s *app) dismissWorkspaceRepair() {
	s.workspaceRepair = nil
}

func (s *app) renderWorkspaceRepairDialog(c *ui.Context, p palette) {
	state := s.workspaceRepair
	if state == nil {
		return
	}
	if !state.Open || !s.workspaceRepairIdentityCurrent(state) {
		s.workspaceRepair = nil
		return
	}
	ui.DialogBase(c.Key("workspace-repair-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(760, w-48)).MaxHeight(min(740, h-48)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "build", 18, p.amber)
				title := "修复工作区"
				if state.Preview != nil && !state.Preview.CanRepair {
					title = "工作区无法自动修复"
				} else if state.Result != nil {
					title = "工作区修复完成"
				}
				label(c, title, 17, p.text).FontWeight(600).Grow(1)
			})
			if state.Loading {
				loading := "正在检查修复方案…"
				if state.RequestKind == "workspace.repair" {
					loading = "正在修复工作区并刷新任务快照…"
				}
				label(c, loading, 12, p.muted).Wrap()
			}
			if state.Error != "" {
				ui.Box(c).FillWidth().Padding(8, 10).Radius(8).Background(p.amberBg).Children(func() {
					label(c, state.Error, 12, p.amber).Wrap().Selectable()
				})
			}
			if repaired := state.Result; repaired != nil {
				ui.Column(c).FillWidth().Padding(10).Gap(6).Background(p.soft).Radius(8).Children(func() {
					label(c, repaired.ServiceName, 13, p.text).FontWeight(600).Wrap()
					label(c, repaired.Message, 12, p.text).Wrap().Selectable()
					if repaired.BackupPath != "" {
						label(c, "原目录备份："+repaired.BackupPath, 11, p.muted).Wrap().Selectable()
					}
					if len(repaired.Warnings) > 0 {
						label(c, "警告：\n"+strings.Join(repaired.Warnings, "\n"), 11, p.amber).Wrap().Selectable()
					}
				})
			}
			if preview := state.Preview; preview != nil && state.Result == nil {
				actual := "未检测到"
				if preview.ActualBranch != nil && strings.TrimSpace(*preview.ActualBranch) != "" {
					actual = *preview.ActualBranch
				} else if preview.Issue == "DETACHED_HEAD" {
					actual = "Detached HEAD"
				}
				expected := strings.TrimSpace(preview.ExpectedBranch)
				if expected == "" {
					expected = "未提供"
				}
				ui.Column(c).FillWidth().Padding(10).Gap(6).Background(p.soft).Radius(8).Children(func() {
					label(c, preview.ServiceName, 13, p.text).FontWeight(600).Wrap()
					label(c, workspaceHealthIssueLabel(preview.Issue), 11, p.amber).Wrap()
					label(c, "路径："+preview.WorkspacePath, 11, p.muted).Wrap().Selectable()
					label(c, "实际分支："+actual+" · 期望分支："+expected, 11, p.muted).Wrap().Selectable()
					if preview.Remote != "" {
						remoteState := "不存在"
						if preview.RemoteBranchExists {
							remoteState = "存在"
						}
						label(c, fmt.Sprintf("远程分支：%s/%s（%s）", preview.Remote, expected, remoteState), 11, p.muted).Wrap().Selectable()
					}
					label(c, preview.Message, 12, p.text).Wrap().Selectable()
					if preview.BackupPath != "" {
						label(c, "原目录会保留为备份："+preview.BackupPath, 11, p.muted).Wrap().Selectable()
					}
					if preview.RunsBootstrap {
						label(c, "修复后将执行 Bootstrap。", 11, p.muted).Wrap()
					}
					if len(preview.Steps) > 0 {
						ui.Divider(c)
						for index, step := range preview.Steps {
							label(c, fmt.Sprintf("%d. %s", index+1, step), 11, p.text).Wrap()
						}
					}
				})
				if preview.RequiresRemoteReuseConfirmation {
					ui.Row(c).FillWidth().Padding(7, 8).Gap(8).AlignItems(ui.Center).Background(p.amberBg).Radius(8).Children(func() {
						ui.Checkbox(c.Key("workspace-repair-remote-reuse"), &state.ReuseRemoteBranch, "").Shrink(0).Disabled(state.Loading || s.busy)
						label(c, "确认复用远程分支 "+preview.Remote+"/"+preview.ExpectedBranch, 11, p.amber).Wrap().Grow(1)
					})
				}
				if preview.RequiresSharedBranchConfirmation {
					ui.Column(c).FillWidth().Padding(8).Gap(5).Background(p.amberBg).Radius(8).Children(func() {
						label(c, "以下 Worktree 已检出同一分支：", 11, p.amber).Wrap()
						for _, path := range preview.OccupiedWorktreePaths {
							label(c, path, 11, p.muted).Wrap().Selectable()
						}
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Checkbox(c.Key("workspace-repair-shared-branch"), &state.ShareCheckedOutBranch, "").Shrink(0).Disabled(state.Loading || s.busy)
							label(c, "我了解多个 Worktree 将共享同一分支", 11, p.amber).Wrap().Grow(1)
						})
					})
				}
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				controlsDisabled := state.Loading || s.busy
				if state.Result == nil {
					recheck := ui.ButtonBase(c.Key("workspace-repair-recheck")).Row().Gap(7).Height(34).Padding(0, 14).Radius(20).Label("重新检查").Tooltip("重新检查").Disabled(controlsDisabled)
					recheck.Background(p.soft).Children(func() {
						icon(c, "refresh", 16, p.muted)
						label(c, "重新检查", 12, p.text).NoWrap()
					})
					if recheck.Clicked() && !controlsDisabled {
						s.requestWorkspaceRepairPreview(state)
					}
				}
				closeButton := ui.ButtonBase(c.Key("workspace-repair-cancel")).Row().Gap(7).Height(34).Padding(0, 14).Radius(20).Label("关闭").Tooltip("关闭").Disabled(controlsDisabled)
				closeButton.Background(p.soft).Children(func() {
					icon(c, "close", 16, p.muted)
					label(c, "关闭", 12, p.text).NoWrap()
				})
				if closeButton.Clicked() && !controlsDisabled {
					s.dismissWorkspaceRepair()
				}
				if state.Result == nil {
					canConfirm := state.Preview != nil && state.Preview.CanRepair && !state.Loading && !s.busy && state.Error == "" &&
						(!state.Preview.RequiresRemoteReuseConfirmation || state.ReuseRemoteBranch) &&
						(!state.Preview.RequiresSharedBranchConfirmation || state.ShareCheckedOutBranch)
					button := ui.ButtonBase(c.Key("workspace-repair-confirm")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label("确认修复").Tooltip("确认修复").Disabled(!canConfirm)
					button.Background(p.blue).Children(func() {
						icon(c, "build", 17, p.surface)
						label(c, "确认修复", 13, p.surface).NoWrap()
					})
					if button.Clicked() && canConfirm {
						s.confirmWorkspaceRepair()
					}
				}
			})
		})
	})
}
