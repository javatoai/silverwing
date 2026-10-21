package desktop

import (
	"context"
	"errors"

	"silverwing-go/internal/application"
)

// The UI owns this registry, including canceled workers whose native handles
// are still closing. A barrier takes its own slice for the background waiter.
type materialsLiveRefreshLifecycle struct {
	workers []*materialsLiveRefreshState
	barrier *materialsLiveRefreshBarrier
	closed  bool
}

type materialsLiveRefreshBarrier struct {
	owner   *materialsLiveRefreshLifecycle
	workers []*materialsLiveRefreshState
}

func (s *app) ensureMaterialsLiveRefreshLifecycle() *materialsLiveRefreshLifecycle {
	if s.materialsLiveRefreshLifecycle == nil {
		s.materialsLiveRefreshLifecycle = &materialsLiveRefreshLifecycle{}
	}
	return s.materialsLiveRefreshLifecycle
}

func materialsLiveRefreshFinished(live *materialsLiveRefreshState) bool {
	if live == nil || live.done == nil {
		return false
	}
	select {
	case <-live.done:
		return true
	default:
		return false
	}
}

func (owner *materialsLiveRefreshLifecycle) remember(live *materialsLiveRefreshState) {
	remaining := owner.workers[:0]
	found := false
	for _, worker := range owner.workers {
		if !materialsLiveRefreshFinished(worker) {
			remaining = append(remaining, worker)
			found = found || worker == live
		}
	}
	owner.workers = remaining
	if live != nil && !found && !materialsLiveRefreshFinished(live) {
		owner.workers = append(owner.workers, live)
	}
}

func (s *app) retireMaterialsLiveRefresh(live *materialsLiveRefreshState) {
	if live == nil {
		return
	}
	s.ensureMaterialsLiveRefreshLifecycle().remember(live)
	if live.cancel != nil {
		live.cancel()
	}
}

func materialsMutationNeedsWatchBarrier(action application.Action) bool {
	switch action.Kind {
	case "migration.apply", "migration.recover", "config.task-root.save", "task.delete":
		return true
	case "workspace.remove":
		return action.Payload["preview"] != nil
	}
	return false
}

// Called on the UI thread before a filesystem mutation is scheduled. Pausing
// creation is necessary: a busy repaint otherwise starts a replacement watch.
func (s *app) pauseMaterialsLiveRefresh(action application.Action) *materialsLiveRefreshBarrier {
	if !materialsMutationNeedsWatchBarrier(action) {
		return nil
	}
	owner := s.ensureMaterialsLiveRefreshLifecycle()
	s.closeMaterialsLiveRefresh()
	owner.remember(nil)
	barrier := &materialsLiveRefreshBarrier{owner: owner, workers: append([]*materialsLiveRefreshState(nil), owner.workers...)}
	owner.barrier = barrier
	for _, live := range barrier.workers {
		if live.cancel != nil {
			live.cancel()
		}
	}
	return barrier
}

// Never wait on the UI thread or while holding the application backend gate.
func (barrier *materialsLiveRefreshBarrier) wait(ctx context.Context) error {
	if barrier == nil {
		return nil
	}
	for _, live := range barrier.workers {
		if live == nil || live.done == nil {
			return errors.New("资料目录监听缺少结束信号，无法安全移动目录")
		}
		select {
		case <-live.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// The action's UI result callback releases ownership after applying its result.
// Canceled workers stay in the registry until their done signals have closed.
func (s *app) resumeMaterialsLiveRefresh(barrier *materialsLiveRefreshBarrier) {
	if barrier == nil || barrier.owner.barrier != barrier {
		return
	}
	barrier.owner.barrier = nil
	barrier.owner.remember(nil)
	s.observeMaterialsLiveRefresh()
}

// App.Run has ended, so no further UI callbacks can create new workers. Native
// watcher shutdown depends only on cancellation and done, not Window.Update.
func (s *app) joinMaterialsLiveRefresh(ctx context.Context) error {
	owner := s.ensureMaterialsLiveRefreshLifecycle()
	owner.closed = true
	s.closeMaterialsLiveRefresh()
	owner.remember(nil)
	barrier := &materialsLiveRefreshBarrier{workers: append([]*materialsLiveRefreshState(nil), owner.workers...)}
	for _, live := range barrier.workers {
		if live.cancel != nil {
			live.cancel()
		}
	}
	return barrier.wait(ctx)
}
