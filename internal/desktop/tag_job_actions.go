package desktop

import (
	"context"
	"fmt"

	"silverwing-go/internal/application"
)

func tagJobAction(kind string) bool {
	return kind == "tag.build" || kind == "tag.retry" || kind == "tag.workspace-check"
}

// Optional adapters keep legacy/fake backends working; the real job backend is
// selected before dispatch reaches the ordinary global busy/backend gate.
func (s *app) dispatchTagJobAction(action application.Action) bool {
	if !tagJobAction(action.Kind) {
		return false
	}
	if _, ok := s.backend.(application.TagJobBackend); !ok {
		return false
	}
	s.queueTagAction(action)
	return true
}

func (s *app) queueTagAction(action application.Action) bool {
	var win tagGenbuAutoProbeWindow
	if s.win != nil {
		win = s.win
	}
	if win == nil && s.releaseUnit.state.tagJobs != nil {
		win = s.releaseUnit.state.tagJobs.window
	}
	return s.queueTagActionWindow(action, win)
}

func (s *app) queueTagActionWindow(action application.Action, win tagGenbuAutoProbeWindow) bool {
	backend, ok := s.backend.(application.TagJobBackend)
	if !tagJobAction(action.Kind) || !ok || win == nil || win.IsDestroyed() {
		s.lastError = "Tag 执行后端或窗口不可用。"
		return false
	}
	scope := s.tagJobScope()
	if action.TaskID != scope.TaskID || scope.TaskID == "" || scope.TaskPath == "" || scope.ConfigIdentity == "" {
		s.lastError = "任务上下文已变化，请重新选择后再操作。"
		return false
	}
	if _, scoped := action.Payload["tagHistoryTaskPath"]; scoped && !s.tagHistoryResultRelevant(action) {
		s.lastError = "Tag 记录所属任务已变化，请刷新后再操作。"
		return false
	}
	state := s.ensureTagDesktopJobs()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return false
	}
	view := s.tagJobView(scope)
	payload := map[string]any{}
	for key, value := range action.Payload {
		payload[key] = value
	}
	for key, value := range tagHistoryScopePayload(s.ensureTagHistoryView()) {
		payload[key] = value
	}
	action.Payload = payload
	pending := s.preparePendingTagSubmission(scope, action, view)
	if pending != nil {
		for _, command := range view.commands {
			if command.pending != nil && samePendingTagItems(command.pending, pending) {
				return true
			}
		}
		state.nextSubmission++
		pending.Nonce = fmt.Sprintf("tag-submit-%d", state.nextSubmission)
	}
	view.commands = append(view.commands, &tagDesktopJobCommand{action: action, pending: pending})
	if pending != nil && s.showStartedTagBuild(s.taskUnit.currentTask()) {
		if s.releaseUnit.state.tagBuild != nil {
			s.releaseUnit.state.tagBuild.Open = false
			s.releaseUnit.state.tagBuild = nil
		}
	}
	s.startNextTagJobCommand(scope, view, backend, win)
	return true
}

func (s *app) startNextTagJobCommand(scope application.TagJobScope, view *tagDesktopJobView, backend application.TagJobBackend, win tagGenbuAutoProbeWindow) {
	if view.commandRunning || len(view.commands) == 0 {
		return
	}
	worker := s.newTagDesktopWorker()
	if worker == nil {
		return
	}
	view.commandRunning = true
	command := view.commands[0]
	action := command.action
	appendID := ""
	if view.snapshot.Busy {
		appendID = view.snapshot.ID
	}
	go func() {
		defer s.finishTagDesktopWorker(worker)
		var snapshot application.TagJobSnapshot
		var err error
		if appendID != "" {
			snapshot, err = backend.AppendTagJob(worker.ctx, scope, appendID, action)
		} else {
			snapshot, err = backend.StartTagJob(worker.ctx, scope, action)
		}
		if err == nil && (snapshot.Scope != scope || snapshot.ID == "") {
			err = fmt.Errorf("Tag 作业响应与任务身份不一致")
		}
		if worker.ctx.Err() != nil || win.IsDestroyed() {
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			if worker.ctx.Err() != nil || win.IsDestroyed() {
				return
			}
			view.commandRunning = false
			view.commands = view.commands[1:]
			if err != nil {
				if s.tagJobScope() == scope {
					s.ensureTagHistoryView().Error = fmt.Sprintf("Tag 操作未开始：%v", err)
					s.applyTagWorkspaceCheckFailure(action, err)
				}
			} else {
				newJob := appendID == "" || snapshot.ID != view.snapshot.ID
				if snapshot.ID != view.snapshot.ID && s.tagJobScope() == scope {
					s.releaseUnit.stopTagJobSubscription()
				}
				s.acceptTagJobSnapshot(view, scope, snapshot, newJob)
				if s.tagJobScope() == scope && action.Kind == "tag.build" && s.showStartedTagBuild(s.taskUnit.currentTask()) {
					if s.releaseUnit.state.tagBuild != nil {
						s.releaseUnit.state.tagBuild.Open = false
						s.releaseUnit.state.tagBuild = nil
					}
				}
			}
			s.startNextTagJobCommand(scope, view, backend, win)
			s.observeTagJobsWindow(win)
		})
		select {
		case <-updated:
		case <-worker.ctx.Done():
		}
	}()
}

func (s *app) newTagDesktopWorker() *tagDesktopJobWorker {
	state := s.ensureTagDesktopJobs()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &tagDesktopJobWorker{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	state.workers[worker] = struct{}{}
	return worker
}

func (s *app) finishTagDesktopWorker(worker *tagDesktopJobWorker) {
	worker.cancel()
	state := s.releaseUnit.state.tagJobs
	state.mu.Lock()
	delete(state.workers, worker)
	close(worker.done)
	state.mu.Unlock()
}
