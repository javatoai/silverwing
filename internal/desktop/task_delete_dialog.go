package desktop

import (
	"fmt"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type taskDeleteDialogState struct {
	TaskID, TaskPath, TaskCreatedAt, FolderName string
	Page                                        string
	RequestID                                   uint64
	Risks                                       []domain.DeleteRisk
	RiskScope                                   *taskDeleteRiskScope
	Open, Loading, ExternalWindowsClosed        bool
	DiscardChanges, Submitting                  bool
	Error                                       string
}

var taskDeleteRequestSequence atomic.Uint64

func (s *app) openTaskDeleteDialog(t task) {
	if strings.TrimSpace(t.id) == "" {
		return
	}
	s.lastError = ""
	s.lastResult = ""
	s.cancelTaskDeleteRiskRead()
	s.taskDeleteDialog = &taskDeleteDialogState{
		TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt,
		FolderName: firstNonEmpty(t.title, t.id), Page: s.page, Open: true,
	}
	s.requestTaskDeleteRisks()
}

func (s *app) requestTaskDeleteRisks() {
	state := s.taskDeleteDialog
	if state == nil || !state.Open || state.Submitting {
		return
	}
	s.cancelTaskDeleteRiskRead()
	state.Risks = nil
	state.RiskScope = nil
	state.Error = ""
	state.RequestID = taskDeleteRequestSequence.Add(1)
	if s.backend == nil {
		state.Loading = false
		state.Error = "应用后端不可用。"
		return
	}
	state.Loading = true
	s.act("task.delete-risks", state.TaskID, "", "", state.TaskPath, "", map[string]any{
		"taskDeleteRequestID": fmt.Sprint(state.RequestID),
	})
}

func (s *app) closeTaskDeleteDialog() {
	s.cancelTaskDeleteRiskRead()
	state := s.taskDeleteDialog
	if state == nil {
		return
	}
	state.Open = false
	s.taskDeleteDialog = nil
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) taskDeleteStateMatches(action application.Action) bool {
	state := s.taskDeleteDialog
	if state == nil || action.TaskID != state.TaskID {
		return false
	}
	if action.Path != "" && state.TaskPath != "" && action.Path != state.TaskPath {
		return false
	}
	if action.Kind == "task.delete-risks" {
		return payloadString(action.Payload, "taskDeleteRequestID") == fmt.Sprint(state.RequestID)
	}
	return action.Kind == "task.delete" && state.Submitting &&
		payloadString(action.Payload, "taskDeleteRequestID") == fmt.Sprint(state.RequestID)
}

// applyTaskDeleteActionResult consumes the delete-risk result before the
// generic result panel can expose its raw JSON.
func (s *app) applyTaskDeleteActionResult(action application.Action, result application.ActionResult) bool {
	if action.Kind == "task.delete-risks" {
		s.applyTaskDeleteRiskResult(action, result)
		return true
	}
	if action.Kind == "task.delete" && s.taskDeleteStateMatches(action) {
		s.taskDeleteDialog = nil
		s.lastResult = ""
		return true
	}
	return false
}

// applyTaskDeleteActionFailure keeps inspection failures in the dialog and
// lets task.delete failures continue through the generic error path.
func (s *app) applyTaskDeleteActionFailure(action application.Action, cause error) bool {
	if action.Kind == "task.delete-risks" {
		s.applyTaskDeleteRiskFailure(action, cause)
		return true
	}
	if action.Kind != "task.delete" || !s.taskDeleteStateMatches(action) {
		return false
	}
	state := s.taskDeleteDialog
	state.Loading = false
	state.Submitting = false
	// A cleanup error can arrive after the task itself was removed. The root
	// dispatcher has already applied the refreshed snapshot in that case.
	if s.taskByID(state.TaskID).id == "" {
		s.taskDeleteDialog = nil
	}
	return false
}

func (s *app) applyTaskDeleteRiskResult(action application.Action, result application.ActionResult) {
	if !s.taskDeleteStateMatches(action) {
		return
	}
	state := s.taskDeleteDialog
	state.Loading = false
	state.Error = ""
	risks, err := decodeData[domain.DeleteRisk](result.Data)
	if err != nil {
		state.Risks = nil
		state.RiskScope = nil
		state.Error = "删除检查结果格式错误：" + err.Error()
		return
	}
	state.Risks = risks
}

func (s *app) applyTaskDeleteRiskFailure(action application.Action, cause error) {
	if !s.taskDeleteStateMatches(action) {
		return
	}
	state := s.taskDeleteDialog
	state.Loading = false
	state.Risks = nil
	state.RiskScope = nil
	state.Error = "删除风险检查失败，请重新检查。"
	if cause != nil {
		state.Error = cause.Error()
	}
}

func (s *app) taskDeleteHasStatusCheckFailure() bool {
	state := s.taskDeleteDialog
	if state == nil {
		return false
	}
	for _, risk := range state.Risks {
		if risk.StatusCheckError != nil {
			return true
		}
	}
	return false
}

func (s *app) taskDeleteNeedsDiscardAck() bool {
	state := s.taskDeleteDialog
	if state == nil {
		return false
	}
	for _, risk := range state.Risks {
		if risk.StatusCheckError == nil {
			return true
		}
	}
	return false
}

func (s *app) taskDeleteConfirmationAction() (application.Action, bool) {
	state := s.taskDeleteDialog
	if state == nil || !state.Open || state.Loading || state.Submitting || state.Error != "" || s.busy ||
		!state.ExternalWindowsClosed || s.taskDeleteHasStatusCheckFailure() ||
		(len(state.Risks) > 0 && !state.DiscardChanges) {
		return application.Action{}, false
	}
	current := s.taskByID(state.TaskID)
	if current.id == "" || (state.TaskPath != "" && current.path != state.TaskPath) ||
		(state.TaskCreatedAt != "" && current.createdAt != state.TaskCreatedAt) {
		return application.Action{}, false
	}
	if state.RiskScope != nil {
		scope, ok := s.taskDeleteRiskCurrentScope(state)
		if !ok || scope != *state.RiskScope {
			return application.Action{}, false
		}
	}
	forceDiscard := len(state.Risks) > 0 && state.DiscardChanges
	return application.Action{
		Kind: "task.delete", TaskID: state.TaskID, Path: state.TaskPath,
		Payload: map[string]any{
			"forceDiscard": forceDiscard, "taskDeleteRequestID": fmt.Sprint(state.RequestID),
			"taskDeleteCreatedAt": state.TaskCreatedAt,
		},
	}, true
}

func (s *app) confirmTaskDelete() {
	action, ok := s.taskDeleteConfirmationAction()
	if !ok {
		return
	}
	state := s.taskDeleteDialog
	state.Loading = true
	state.Submitting = true
	state.Error = ""
	s.dispatch(action)
}

func (s *app) renderTaskDeleteDialog(c *ui.Context, p palette) {
	s.observeTaskDeleteRiskJobs()
	state := s.taskDeleteDialog
	if state == nil {
		return
	}
	if !state.Open {
		s.closeTaskDeleteDialog()
		return
	}
	ui.DialogBase(c.Key("task-delete-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		width, _ := c.Size()
		panel.Width(maxFloat32(380, minFloat32(620, width-48))).MaxHeight(640).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "delete", 18, p.danger)
				label(c, "删除任务", 17, p.text).FontWeight(600).Grow(1)
			})
			label(c, fmt.Sprintf("即将永久删除“%s”的任务目录和所有工作区，远程分支不会被修改。", state.FolderName), 12, p.text).Wrap()
			ui.Box(c).FillWidth().Padding(10, 12).Radius(9).Background(p.amberBg).Border(1, p.line).Children(func() {
				label(c, "删除前请确认已关闭：Codex 中关联的项目或任务、IDE 中打开的此任务或工作区窗口，以及文件管理器中打开的任务目录。它们可能占用文件，导致删除失败。silverwing 不会自动关闭这些外部窗口。", 11, p.danger).Wrap()
			})
			ui.Checkbox(c.Key("task-delete-external-windows"), &state.ExternalWindowsClosed, "我已关闭上述 Codex 项目、IDE 窗口和文件夹").FillWidth()
			if state.Loading {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(15, 15).TextColor(p.blue).Label("检查中")
					if state.Submitting {
						label(c, "正在删除任务…", 12, p.muted)
					} else {
						label(c, "正在检查 Git 状态…", 12, p.muted)
					}
				})
			}
			if state.Error != "" {
				ui.Column(c).FillWidth().Padding(9, 10).Gap(6).Radius(8).Background(p.amberBg).Children(func() {
					label(c, "删除检查失败："+state.Error, 12, p.danger).Wrap().Selectable()
					if action(c, "task-delete-risk-retry", "refresh", "重新检查", p, false) && !state.Loading {
						s.requestTaskDeleteRisks()
					}
				})
			}
			if len(state.Risks) > 0 {
				ui.Scroll(c).FillWidth().MaxHeight(180).Padding(9).Gap(5).Background(p.soft).Border(1, p.line).Radius(8).Children(func() {
					for index, risk := range state.Risks {
						name := firstNonEmpty(strings.TrimSpace(risk.ServiceName), fmt.Sprintf("工作区 %d", index+1))
						detail := "存在未提交改动、Git 操作或未推送提交"
						if risk.StatusCheckError != nil {
							detail = firstNonEmpty(strings.TrimSpace(*risk.StatusCheckError), "Git 状态检查失败")
						} else if risk.UnpushedCommits > 0 {
							detail += fmt.Sprintf("，%d 个仅本地提交", risk.UnpushedCommits)
						}
						label(c, "• "+name+"："+detail, 11, p.danger).Wrap().Selectable()
					}
				})
			}
			if s.taskDeleteNeedsDiscardAck() {
				ui.Checkbox(c.Key("task-delete-discard-risk"), &state.DiscardChanges, "确认丢弃未提交改动").FillWidth()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "task-delete-cancel", "close", "取消", p, false) {
					s.closeTaskDeleteDialog()
				}
				_, canDelete := s.taskDeleteConfirmationAction()
				button := ui.ButtonBase(c.Key("task-delete-confirm")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label("永久删除任务").Tooltip("永久删除任务").Disabled(!canDelete)
				button.Background(p.danger).Children(func() {
					icon(c, "delete", 16, ui.Hex("#FFFFFF"))
					label(c, "永久删除任务", 12, ui.Hex("#FFFFFF")).NoWrap()
				})
				if button.Clicked() && canDelete {
					s.confirmTaskDelete()
				}
			})
		})
	})
}
