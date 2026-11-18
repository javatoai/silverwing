package desktop

import (
	"context"
	"errors"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type taskAppendRemoteCheckState struct {
	signature, configJSON, home string
	cancel                      context.CancelFunc
	done                        chan struct{}
	running, cancelRequested    bool
	checks                      []workspace.WorktreeRemoteCheck
	message                     string
}

func (state *taskAppendUIState) clearRemoteCheck() {
	if state != nil && state.remoteCheck != nil {
		state.remoteCheck.cancel()
		state.remoteCheck = nil
	}
}

func (state *taskAppendUIState) hasWorktreeSelection() bool {
	for serviceID, ids := range state.moduleOrder {
		if state.mode == "services" && !state.selectedServices[serviceID] {
			continue
		}
		for _, id := range ids {
			if draft := state.moduleData[serviceID][id]; draft != nil && draft.Strategy == domain.WorkspaceStandardWorktree {
				return true
			}
		}
	}
	return false
}

func (s *app) taskAppendRemoteCheckCurrent(state *taskAppendUIState, check *taskAppendRemoteCheckState) bool {
	if s.workspaceUnit.state.workspaceAppend != state || !state.open || state.remoteCheck != check ||
		s.snapshot.Home != check.home || s.snapshot.ConfigJSON != check.configJSON {
		return false
	}
	_, signature, err := s.taskAppendRequest()
	return err == nil && signature == check.signature
}

func (s *app) beginTaskAppendRemoteCheck(state *taskAppendUIState) {
	backend, ok := s.backend.(application.TaskAppendRemoteCheckBackend)
	window := s.win
	update := taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
	if !ok || update == nil || s.workspaceUnit.state.workspaceAppend != state || !state.open || state.pending || s.busy ||
		!state.hasWorktreeSelection() || state.remoteCheck != nil && state.remoteCheck.running {
		return
	}
	payload, signature, err := s.taskAppendRequest()
	if err != nil {
		state.inlineError = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	check := &taskAppendRemoteCheckState{signature: signature, configJSON: s.snapshot.ConfigJSON, home: s.snapshot.Home,
		cancel: cancel, done: make(chan struct{}), running: true}
	state.remoteCheck = check
	state.inlineError = ""
	action := application.Action{Kind: taskAppendPreviewAction(state.mode), TaskID: state.taskID, ServiceID: state.serviceID, Payload: payload}
	go func() {
		defer close(check.done)
		defer cancel()
		checks, err := backend.CheckTaskAppendRemotes(ctx, action)
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if !s.taskAppendRemoteCheckCurrent(state, check) {
				return
			}
			check.running = false
			switch {
			case errors.Is(err, context.Canceled), err == nil && check.cancelRequested:
				check.message = "远端连接检查已取消。"
			case err != nil:
				check.message = "检查远端连接失败：" + err.Error()
			case len(checks) == 0:
				check.message = "没有待添加的 Worktree 远端。"
			default:
				check.checks = checks
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *app) renderTaskAppendRemoteCheckButton(c *ui.Context, p palette, state *taskAppendUIState) {
	if _, ok := s.backend.(application.TaskAppendRemoteCheckBackend); !ok || !state.hasWorktreeSelection() {
		return
	}
	if check := state.remoteCheck; check != nil && check.running {
		if taskAppendButton(c, "task-append-cancel-remote", "取消检查", p, false, check.cancelRequested) {
			check.cancelRequested = true
			check.cancel()
		}
		return
	}
	if taskAppendButton(c, "task-append-check-remote", "检查远端连接", p, false, s.busy || state.pending) {
		s.beginTaskAppendRemoteCheck(state)
	}
}

func renderTaskAppendRemoteCheck(c *ui.Context, p palette, check *taskAppendRemoteCheckState) {
	if check == nil {
		return
	}
	if check.running {
		text := "正在检查远端连接…"
		if check.cancelRequested {
			text = "正在取消检查…"
		}
		label(c, text, 11, p.muted).Wrap()
		return
	}
	if check.message != "" {
		label(c, check.message, 11, p.muted).Wrap().Selectable()
	}
	for _, result := range check.checks {
		text, color := "连接正常，基础分支存在。", p.muted
		if result.Failure != nil {
			text, color = result.Failure.Error(), p.amber
		}
		label(c, result.Repository+" · "+result.BaseRef+" · "+text, 11, color).Wrap().Selectable()
	}
}

func (s *workspaceController) closeTaskAppendRemoteCheck(ctx context.Context) error {
	state := s.state.workspaceAppend
	if state == nil || state.remoteCheck == nil {
		return nil
	}
	check := state.remoteCheck
	state.clearRemoteCheck()
	select {
	case <-check.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
