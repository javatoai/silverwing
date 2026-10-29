package desktop

import (
	"context"
	"slices"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type materialsGitBranchesState struct {
	scope                            workspace.MaterialsGitScope
	branches                         []string
	err                              string
	loading, loaded, visible, closed bool
	loadedAt                         time.Time
	cancel                           context.CancelFunc
	done                             chan struct{}
	retired                          []*materialsGitBranchesState
}

func (s *app) materialsGitBranchesCurrent() *materialsGitBranchesState {
	state := s.materialsUnit.state.materialsGitBranches
	if state == nil || state.closed || !s.materialsGitScopeCurrent(state.scope) {
		return nil
	}
	return state
}

func (s *app) materialsGitBranchReady(branch string) bool {
	state := s.materialsGitBranchesCurrent()
	return state != nil && state.loaded && !state.loading && state.err == "" && slices.Contains(state.branches, branch)
}

func (s *app) observeMaterialsGitBranches() {
	state := s.materialsUnit.state.materialsGitBranches
	if state != nil && (s.page != "settings" || s.settingsUnit.state.settingsPage != "paths") {
		state.visible = false
	}
	if state != nil && !s.materialsGitScopeCurrent(state.scope) && state.cancel != nil {
		state.cancel()
	}
}

func (s *app) loadMaterialsGitBranches(force bool) {
	config, ok := s.materialsGitConfig()
	if !ok || s.materialsUnit.state.materialsGitCapture || s.materialsGitSettingsPending(config) {
		return
	}
	backend, ok := s.backend.(application.MaterialsGitBranchesBackend)
	update := taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate)
	if !ok || update == nil {
		return
	}
	scope := application.MaterialsGitScope(s.snapshot.Home, config)
	previous := s.materialsUnit.state.materialsGitBranches
	if previous != nil && previous.closed {
		return
	}
	if previous != nil && previous.scope == scope {
		visible := previous.visible
		previous.visible = true
		if previous.loading || !force && (visible || time.Since(previous.loadedAt) < 30*time.Second) {
			return
		}
	}
	state := &materialsGitBranchesState{scope: scope, visible: true, loading: true, branches: []string{}}
	if previous != nil {
		if previous.cancel != nil {
			previous.cancel()
		}
		for _, old := range append(previous.retired, previous) {
			if old.done == nil {
				continue
			}
			select {
			case <-old.done:
			default:
				state.retired = append(state.retired, old)
			}
		}
	}
	s.materialsUnit.state.materialsGitBranches = state
	if scope.URL == "" {
		state.loading, state.loadedAt = false, time.Now()
		return
	}
	if err := domain.ValidateRemoteGitURL(scope.URL); err != nil {
		state.loading, state.loaded, state.err = false, true, err.Error()
		state.loadedAt = time.Now()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	state.cancel, state.done = cancel, make(chan struct{})
	window := s.win
	go func() {
		defer close(state.done)
		defer cancel()
		branches, err := backend.MaterialsGitBranches(ctx, scope)
		if err == nil {
			err = ctx.Err()
		}
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.materialsUnit.state.materialsGitBranches != state || state.closed || !s.materialsGitScopeCurrent(scope) {
				return
			}
			state.loading, state.loaded, state.loadedAt = false, true, time.Now()
			if err != nil {
				state.err = "读取远程分支失败：" + err.Error()
			} else {
				state.branches = slices.Clone(branches)
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *materialsController) stopMaterialsGitBranches() {
	if state := s.state.materialsGitBranches; state != nil {
		for _, old := range append(state.retired, state) {
			old.closed = true
			if old.cancel != nil {
				old.cancel()
			}
		}
	}
}

func (s *materialsController) closeMaterialsGitBranches(ctx context.Context) error {
	s.stopMaterialsGitBranches()
	if state := s.state.materialsGitBranches; state != nil {
		for _, old := range append(state.retired, state) {
			if old.done == nil {
				continue
			}
			select {
			case <-old.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}
