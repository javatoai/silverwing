package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/systemtrash"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type taskDeleteDialogState struct {
	TaskID, TaskPath, TaskCreatedAt, FolderName string
	SelectionKey, SelectionPath                 string
	TaskArchived                                bool
	Page                                        string
	RequestID                                   uint64
	Risks                                       []domain.DeleteRisk
	RiskScope                                   *taskDeleteRiskScope
	Force                                       workspace.TaskForceDeletePreview
	DeleteGuard                                 string
	Open, Loading, ExternalWindowsClosed        bool
	DiscardChanges, Submitting, ForceReview     bool
	ForceConfirmed                              bool
	DeleteMaterials                             bool
	Materials                                   workspace.TaskMaterialsDeletePreview
	Error                                       string
}

var taskDeleteRequestSequence atomic.Uint64

func (s *app) openTaskDeleteDialog(t task) {
	if _, valid := s.taskActionScopeFor(t); !valid || s.busy || strings.TrimSpace(t.id) == "" {
		return
	}
	s.lastError = ""
	s.lastResult = ""
	s.cancelTaskDeleteRiskRead()
	s.taskUnit.state.taskDeleteDialog = &taskDeleteDialogState{
		TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt,
		FolderName: firstNonEmpty(t.title, t.id), Page: s.page, Open: true,
		SelectionKey: taskNavigationKey(s.taskUnit.currentTask()), SelectionPath: s.taskUnit.currentTask().path, TaskArchived: t.archived,
	}
	s.requestTaskDeleteRisks()
}

func (s *app) requestTaskDeleteRisks() {
	state := s.taskUnit.state.taskDeleteDialog
	if state == nil || !state.Open || state.Submitting {
		return
	}
	s.cancelTaskDeleteRiskRead()
	state.Risks = nil
	state.RiskScope = nil
	state.Force = workspace.TaskForceDeletePreview{}
	state.DeleteGuard = ""
	state.Materials = workspace.TaskMaterialsDeletePreview{}
	state.DeleteMaterials = false
	state.DiscardChanges = false
	state.ForceReview = false
	state.ForceConfirmed = false
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
	state := s.taskUnit.state.taskDeleteDialog
	if state == nil {
		return
	}
	state.Open = false
	s.taskUnit.state.taskDeleteDialog = nil
	if s.win != nil {
		s.win.Invalidate()
	}
}

// closeStaleTaskDeleteSubmission belongs to the action's request ID, so an old
// context result cannot leave its dialog submitting or close a newer dialog.
func (s *app) closeStaleTaskDeleteSubmission(action application.Action) {
	if !s.taskDeleteStateMatches(action) {
		return
	}
	s.closeTaskDeleteDialog()
}

func (s *app) taskDeleteStateMatches(action application.Action) bool {
	state := s.taskUnit.state.taskDeleteDialog
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
		s.taskUnit.state.taskDeleteDialog = nil
		if strings.TrimSpace(result.Text) != "" {
			s.lastMessage = result.Text
		}
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
	state := s.taskUnit.state.taskDeleteDialog
	state.Loading = false
	state.Submitting = false
	if strings.TrimSpace(payloadString(action.Payload, "forceGuard")) != "" {
		state.Force = workspace.TaskForceDeletePreview{}
		state.ForceReview = false
		state.ForceConfirmed = false
		state.Risks = nil
		state.RiskScope = nil
		if s.taskUnit.taskByID(state.TaskID).id == "" {
			s.taskUnit.state.taskDeleteDialog = nil
			return true
		}
		if cause != nil {
			state.Error = cause.Error()
		} else {
			state.Error = "强制删除结果无法确认，请重新检查。"
		}
		return true
	}
	// A cleanup error can arrive after the task itself was removed. The root
	// dispatcher has already applied the refreshed snapshot in that case.
	if s.taskUnit.taskByID(state.TaskID).id == "" {
		s.taskUnit.state.taskDeleteDialog = nil
	}
	return false
}

func (s *app) applyTaskDeleteRiskResult(action application.Action, result application.ActionResult) {
	if !s.taskDeleteStateMatches(action) {
		return
	}
	state := s.taskUnit.state.taskDeleteDialog
	state.Loading = false
	state.Error = ""
	data, err := json.Marshal(result.Data)
	var preview workspace.TaskDeletePreview
	if err == nil {
		err = json.Unmarshal(data, &preview)
	}
	if err != nil {
		// Optional test/older read adapters may still return the risk array.
		risks, legacyErr := decodeData[domain.DeleteRisk](result.Data)
		if legacyErr != nil {
			state.Risks = nil
			state.RiskScope = nil
			state.Error = "删除检查结果格式错误"
			return
		}
		preview.Risks = risks
	}
	state.Materials = preview.Materials
	state.Force = preview.Force
	state.DeleteGuard = preview.Guard
	state.ForceReview = false
	state.ForceConfirmed = false
	if !state.Materials.CanDelete {
		state.DeleteMaterials = false
	}
	risks := preview.Risks
	state.Risks = risks
}

func (s *app) applyTaskDeleteRiskFailure(action application.Action, cause error) {
	if !s.taskDeleteStateMatches(action) {
		return
	}
	state := s.taskUnit.state.taskDeleteDialog
	state.Loading = false
	state.Risks = nil
	state.RiskScope = nil
	state.Error = "删除风险检查失败，请重新检查。"
	if cause != nil {
		state.Error = cause.Error()
	}
}

func (s *app) taskDeleteHasStatusCheckFailure() bool {
	state := s.taskUnit.state.taskDeleteDialog
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
	state := s.taskUnit.state.taskDeleteDialog
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

func (state *taskDeleteDialogState) taskDeleteForceIsConfirmed() bool {
	return state != nil && state.ForceReview && state.ForceConfirmed && state.Force.CanForce && strings.TrimSpace(state.Force.Guard) != ""
}

func (s *app) taskDeleteConfirmationAction() (application.Action, bool) {
	state := s.taskUnit.state.taskDeleteDialog
	forceConfirmed := state.taskDeleteForceIsConfirmed()
	forceInspectionBlocksNormalDelete := state != nil && (state.Force.CanForce || strings.TrimSpace(state.Force.Reason) != "")
	if state == nil || !state.Open || state.Loading || state.Submitting || state.Error != "" || s.busy ||
		!state.ExternalWindowsClosed || forceInspectionBlocksNormalDelete && !forceConfirmed ||
		(s.taskDeleteHasStatusCheckFailure() && !forceConfirmed) || (state.DeleteMaterials && !state.Materials.CanDelete) ||
		(s.taskDeleteNeedsDiscardAck() && !state.DiscardChanges && !forceConfirmed) {
		return application.Action{}, false
	}
	current := s.taskUnit.taskByID(state.TaskID)
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
	forceDiscard := !forceConfirmed && len(state.Risks) > 0 && state.DiscardChanges
	return application.Action{
		Kind: "task.delete", TaskID: state.TaskID, Path: state.TaskPath,
		Payload: map[string]any{
			"forceDiscard": forceDiscard, "taskDeleteRequestID": fmt.Sprint(state.RequestID),
			"deleteMaterials": state.DeleteMaterials, "materialsGuard": state.Materials.Guard,
			"forceGuard":          forceGuard(state, forceConfirmed),
			"deleteGuard":         state.DeleteGuard,
			"taskDeleteCreatedAt": state.TaskCreatedAt,
		},
	}, true
}

func forceGuard(state *taskDeleteDialogState, confirmed bool) string {
	if state == nil || !confirmed {
		return ""
	}
	return state.Force.Guard
}

func (s *app) confirmTaskDelete() {
	action, ok := s.taskDeleteConfirmationAction()
	if !ok {
		return
	}
	state := s.taskUnit.state.taskDeleteDialog
	scope, valid := s.taskMenuScopeFor(s.taskUnit.taskByID(state.TaskID))
	if !valid || scope.taskPath != state.TaskPath || scope.taskCreatedAt != state.TaskCreatedAt ||
		scope.page != state.Page || scope.archived != state.TaskArchived {
		state.Error = errTaskContextChanged.Error()
		state.Loading = false
		state.Submitting = false
		return
	}
	state.Loading = true
	state.Submitting = true
	state.Error = ""
	s.dispatchTaskContextActionWithCompletion(action, &scope, func(_ application.ActionResult, err, _ error) {
		if err != nil && s.taskUnit.state.taskDeleteDialog == state && state.Submitting {
			state.Loading = false
			state.Submitting = false
			state.Error = err.Error()
		}
	})
}

func (s *app) renderTaskDeleteDialog(c *ui.Context, p palette) {
	s.observeTaskDeleteRiskJobs()
	state := s.taskUnit.state.taskDeleteDialog
	if state == nil {
		return
	}
	if !state.Open {
		s.closeTaskDeleteDialog()
		return
	}
	ui.DialogBase(c.Key("task-delete-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		width, height := c.Size()
		panel.Width(maxFloat32(380, minFloat32(620, width-48))).MaxHeight(maxFloat32(300, minFloat32(640, height-32))).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "delete", 18, p.danger)
				label(c, "删除任务", 17, p.text).FontWeight(600).Grow(1)
			})
			label(c, fmt.Sprintf("“%s”的任务目录和工作区文件将移入系统%s。独立资料按下方选项处理，本地和远程分支保留。", state.FolderName, systemtrash.Label()), 12, p.text).Wrap()
			ui.Box(c).FillWidth().Padding(10, 12).Radius(9).Background(p.amberBg).Border(1, p.line).Children(func() {
				label(c, "删除前请确认已关闭：Codex 中关联的项目或任务、IDE 中打开的此任务或工作区窗口，以及文件管理器中打开的任务目录。它们可能占用文件，导致删除失败。silverwing 不会自动关闭这些外部窗口。", 11, p.danger).Wrap()
			})
			ui.Checkbox(c.Key("task-delete-external-windows"), &state.ExternalWindowsClosed, "我已关闭上述 Codex 项目、IDE 窗口和文件夹").FillWidth()
			ui.Checkbox(c.Key("task-delete-materials"), &state.DeleteMaterials, "删除任务资料目录").FillWidth().Disabled(state.Loading || !state.Materials.CanDelete)
			if state.Materials.Root != "" {
				label(c, state.Materials.Root, 11, p.muted).Wrap().Selectable()
			}
			if state.Materials.Reason != "" {
				label(c, state.Materials.Reason, 11, p.muted).Wrap()
			}
			if state.Force.Reason != "" {
				label(c, state.Force.Reason, 11, p.danger).Wrap().Selectable()
			}
			if state.Force.CanForce {
				if !state.ForceReview {
					if action(c, "task-delete-force-open", "delete", "强制删除…", p, false) && !state.Loading && !state.Submitting {
						state.ForceReview = true
						state.ForceConfirmed = false
					}
				} else {
					ui.Scroll(c.Key("task-delete-force-review")).FillWidth().MinHeight(0).MaxHeight(maxFloat32(120, minFloat32(340, height-280))).Padding(10, 12).Gap(7).Radius(9).Background(p.amberBg).Border(1, p.line).Children(func() {
						label(c, "确认后会将任务目录及以下工作区移入"+systemtrash.Label()+"。工作区登记会在回收后清理，本地与远程分支保留。", 11, p.danger).Wrap()
						label(c, "任务目录："+firstNonEmpty(state.Force.TaskPath, state.TaskPath), 11, p.text).Wrap().Selectable()
						label(c, "工作区路径", 11, p.text).FontWeight(600)
						for _, path := range state.Force.Workspaces {
							label(c, path, 11, p.text).Wrap().Selectable()
						}
						if len(state.Force.Risks) > 0 {
							ui.Column(c.Key("task-delete-force-risks")).FillWidth().Gap(4).Children(func() {
								label(c, "工作区风险", 11, p.danger).FontWeight(600)
								for index, risk := range state.Force.Risks {
									label(c, "• "+taskDeleteRiskSummary(risk, index), 11, p.danger).Wrap().Selectable()
								}
							})
						}
						ui.Checkbox(c.Key("task-delete-force-confirmed"), &state.ForceConfirmed, "我已核对路径和风险，确认将工作区及本地改动移入"+systemtrash.Label()).FillWidth().Disabled(state.Loading || state.Submitting)
					})
				}
			}

			if state.Loading {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(15, 15).TextColor(p.blue).Label("检查中")
					if state.Submitting {
						label(c, "正在移入"+systemtrash.Label()+"…", 12, p.muted)
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
			if len(state.Risks) > 0 && !state.ForceReview {
				ui.Scroll(c).FillWidth().MaxHeight(180).Padding(9).Gap(5).Background(p.soft).Border(1, p.line).Radius(8).Children(func() {
					for index, risk := range state.Risks {
						label(c, "• "+taskDeleteRiskSummary(risk, index), 11, p.danger).Wrap().Selectable()
					}
				})
			}
			if s.taskDeleteNeedsDiscardAck() && !state.ForceReview {
				ui.Checkbox(c.Key("task-delete-discard-risk"), &state.DiscardChanges, "确认将包含未提交改动的工作区移入"+systemtrash.Label()).FillWidth()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if state.ForceReview && action(c, "task-delete-force-cancel", "close", "返回普通删除", p, false) && !state.Loading && !state.Submitting {
					state.ForceReview = false
					state.ForceConfirmed = false
				}
				if action(c, "task-delete-cancel", "close", "取消", p, false) {
					s.closeTaskDeleteDialog()
				}
				_, canDelete := s.taskDeleteConfirmationAction()
				confirmLabel := "移入" + systemtrash.Label()
				if state.ForceReview {
					confirmLabel = "确认强制删除"
				}
				button := ui.ButtonBase(c.Key("task-delete-confirm")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label(confirmLabel).Tooltip(confirmLabel).Disabled(!canDelete)
				button.Background(p.danger).Children(func() {
					icon(c, "delete", 16, ui.Hex("#FFFFFF"))
					label(c, confirmLabel, 12, ui.Hex("#FFFFFF")).NoWrap()
				})
				if button.Clicked() && canDelete {
					s.confirmTaskDelete()
				}
			})
		})
	})
}

func taskDeleteRiskSummary(risk domain.DeleteRisk, index int) string {
	name := firstNonEmpty(strings.TrimSpace(risk.ServiceName), fmt.Sprintf("工作区 %d", index+1))
	if risk.StatusCheckError != nil {
		return name + "：" + firstNonEmpty(strings.TrimSpace(*risk.StatusCheckError), "Git 状态检查失败")
	}
	var details []string
	if risk.Staged || risk.Unstaged || risk.Untracked {
		details = append(details, "有未提交改动")
	}
	if risk.OperationInProgress != nil && strings.TrimSpace(*risk.OperationInProgress) != "" {
		details = append(details, "Git 操作进行中："+strings.TrimSpace(*risk.OperationInProgress))
	}
	if risk.UnpushedCommits > 0 {
		details = append(details, fmt.Sprintf("%d 个仅本地提交", risk.UnpushedCommits))
	}
	if len(details) == 0 {
		details = append(details, "需要核对工作区状态")
	}
	return name + "：" + strings.Join(details, "；")
}
