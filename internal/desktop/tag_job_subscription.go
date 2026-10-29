package desktop

import (
	"context"
	"fmt"

	"silverwing-go/internal/application"
)

func (s *app) observeTagJobs() {
	var win tagGenbuAutoProbeWindow
	if s.win != nil {
		win = s.win
	}
	if win == nil && s.releaseUnit.state.tagJobs != nil {
		win = s.releaseUnit.state.tagJobs.window
	}
	s.observeTagJobsWindow(win)
}

func (s *app) observeTagJobsWindow(win tagGenbuAutoProbeWindow) {
	backend, ok := s.backend.(application.TagJobBackend)
	visible := (s.page == "tasks" || s.page == "archive") && s.tab == taskTagTab && s.taskUnit.currentTask().id != ""
	state := s.ensureTagDesktopJobs()
	scope := s.tagJobScope()
	if !ok || !visible || s.loading || win == nil || win.IsDestroyed() {
		s.releaseUnit.stopTagJobSubscription()
		return
	}
	view := s.tagJobView(scope)
	s.restoreActiveTagJobRecords()
	if current := state.current; current != nil && current.scope == scope && (current.finished || current.ctx.Err() == nil) {
		return
	}
	s.releaseUnit.stopTagJobSubscription()
	if view.commandRunning && view.snapshot.ID == "" {
		return
	}
	worker := s.newTagDesktopWorker()
	if worker == nil {
		return
	}
	sub := &tagDesktopJobSubscription{tagDesktopJobWorker: worker, scope: scope, view: view}
	state.current = sub
	go func() {
		defer s.finishTagDesktopWorker(worker)
		defer s.updateTagJobSubscription(sub, win, func() { sub.finished, sub.loaded = true, true })
		snapshot, err := backend.TagJobSnapshot(worker.ctx, scope)
		if err != nil {
			s.updateTagJobSubscription(sub, win, func() { s.ensureTagHistoryView().Error = "读取当前 Tag 任务失败：" + err.Error() })
			return
		}
		if snapshot.ID == "" {
			return
		}
		s.updateTagJobSubscription(sub, win, func() { sub.loaded = true; s.acceptTagJobSnapshot(view, scope, snapshot, true) })
		err = backend.WatchTagJob(worker.ctx, scope, snapshot.ID, snapshot.Sequence, func(next application.TagJobSnapshot) {
			s.updateTagJobSubscription(sub, win, func() { s.acceptTagJobSnapshot(view, scope, next, false) })
		})
		if err != nil && worker.ctx.Err() == nil {
			s.updateTagJobSubscription(sub, win, func() { s.ensureTagHistoryView().Error = "更新 Tag 任务失败：" + err.Error() })
		}
	}()
}

func (s *app) updateTagJobSubscription(sub *tagDesktopJobSubscription, win tagGenbuAutoProbeWindow, apply func()) {
	if sub.ctx.Err() != nil || win.IsDestroyed() {
		return
	}
	updated := make(chan struct{})
	win.Update(func() {
		defer close(updated)
		if sub.ctx.Err() != nil || win.IsDestroyed() || s.releaseUnit.state.tagJobs.current != sub || s.tagJobScope() != sub.scope ||
			(s.page != "tasks" && s.page != "archive") || s.tab != taskTagTab {
			return
		}
		apply()
	})
	select {
	case <-updated:
	case <-sub.ctx.Done():
	}
}

func (s *releaseController) stopTagJobSubscription() {
	if s.state.tagJobs != nil && s.state.tagJobs.current != nil {
		s.state.tagJobs.current.cancel()
		s.state.tagJobs.current = nil
	}
}

func (s *app) closeTagDesktopJobs(ctx context.Context) error {
	if s.releaseUnit.state.tagJobs != nil {
		state := s.releaseUnit.state.tagJobs
		state.mu.Lock()
		state.closed = true
		workers := make([]*tagDesktopJobWorker, 0, len(state.workers))
		for worker := range state.workers {
			worker.cancel()
			workers = append(workers, worker)
		}
		state.mu.Unlock()
		for _, view := range state.views {
			view.commands = nil
			view.commandRunning = false
		}
		s.releaseUnit.stopTagJobSubscription()
		if backend, ok := s.backend.(application.TagJobCloser); ok {
			if err := backend.CloseTagJobs(ctx); err != nil {
				return err
			}
		}
		for _, worker := range workers {
			select {
			case <-worker.done:
			case <-ctx.Done():
				return fmt.Errorf("等待 Tag 任务订阅收尾：%w", ctx.Err())
			}
		}
	}
	return nil
}
