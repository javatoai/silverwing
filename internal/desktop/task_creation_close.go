package desktop

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

// A form keeps its own read cancellation handle so closing it cannot cancel
// an unrelated operation that subsequently occupies the desktop action slot.
type taskCreationReadRequest struct {
	kind      string
	cancel    context.CancelFunc
	cancelled bool
}

func (s *app) startTaskCreationRead(kind string, payload map[string]any) {
	state := s.taskCreation
	if state == nil {
		return
	}
	action := application.Action{Kind: kind, Payload: payload}
	if kind == "task.create-preview" {
		s.startTaskCreationPreflight(action)
		return
	}
	if s.busy || s.backend == nil {
		message := "当前有操作正在执行，请等待完成或取消后重试。"
		if s.backend == nil {
			message = "应用后端不可用。"
		}
		s.taskCreationActionError(action, errors.New(message))
		return
	}
	request := &taskCreationReadRequest{kind: kind}
	state.readRequest = request
	s.dispatch(action)
	request.cancel = s.cancelOperation
}

func (s *app) cancelTaskCreationReads(state *taskCreationState, preflightOnly bool) {
	cancelTaskCreationDirectoryCheck(state)
	s.cancelTaskCreationPreflight(state)
	if state == nil || state.readRequest == nil {
		return
	}
	request := state.readRequest
	if request.cancelled || preflightOnly && request.kind != "task.create-preview" {
		return
	}
	request.cancelled = true
	if request.cancel != nil {
		request.cancel()
	}
}

func (s *app) finishTaskCreationRead(action application.Action) bool {
	state := s.taskCreation
	if state == nil || state.token != payloadString(action.Payload, "taskCreateToken") ||
		state.readRequest == nil || state.readRequest.kind != action.Kind {
		return false
	}
	request := state.readRequest
	state.readRequest = nil
	if request.cancel != nil {
		request.cancel()
	}
	return request.cancelled
}

func taskCreationHasDraftChanges(state *taskCreationState) bool {
	if state == nil {
		return false
	}
	if strings.TrimSpace(state.draft.TaskName) != "" || strings.TrimSpace(state.draft.RequirementLink) != "" ||
		state.draft.BranchEdited || strings.TrimSpace(state.notes) != "" {
		return true
	}
	for _, selected := range state.services {
		if selected {
			return true
		}
	}
	if len(state.config.Groups) > 0 && state.groupID != state.config.Groups[0].ID {
		return true
	}
	if state.toolsLoaded {
		defaults := make(map[string]bool, len(state.config.DefaultWorkspaceToolIDs))
		for _, id := range state.config.DefaultWorkspaceToolIDs {
			defaults[id] = true
		}
		for id, selected := range state.selectedToolIDs {
			if selected != defaults[id] {
				return true
			}
		}
		for id := range defaults {
			if !state.selectedToolIDs[id] {
				return true
			}
		}
	}
	return false
}

func (s *app) requestTaskCreationDismiss() {
	s.discardTaskCreation(s.taskCreation)
}

func (s *app) discardTaskCreation(state *taskCreationState) {
	if state == nil || s.taskCreation != state {
		return
	}
	s.cancelTaskCreationReads(state, false)
	s.closeTaskCreationRequirements()
	s.closeTaskCreationAINaming()
	cancelTaskRepositoryChoices(state)
	s.stopTaskCreationPreviewDebounce()
	s.taskCreation = nil
	s.form = actionForm{}
	s.dialogOpen = false
}

func (s *app) renderTaskCreationDiscardConfirmation(c *ui.Context, p palette) {
	state := s.taskCreation
	if state == nil || !state.discardConfirmation {
		return
	}
	ui.DialogBase(c.Key("task-create-discard-dialog"), &state.discardConfirmation, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		width, _ := c.Size()
		panel.Width(min(440, width-48)).Padding(22).Gap(16).Background(p.surface).Border(1, p.line).Radius(18)
		panel.Children(func() {
			label(c, "放弃创建任务？", 18, p.text).FontWeight(600)
			label(c, "已填写的任务信息、服务选择和人工说明将不会保留。", 12, p.muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "task-create-keep-draft", "", "继续编辑", p, false) {
					state.discardConfirmation = false
				}
				if action(c, "task-create-discard-draft", "", "放弃创建", p, false) {
					s.discardTaskCreation(state)
				}
			})
		})
	})
}
