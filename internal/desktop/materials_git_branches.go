package desktop

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type materialsGitBranchesState struct {
	scope                            workspace.MaterialsGitScope
	branches                         []string
	err                              string
	loading, loaded, visible, closed bool
	invalidated                      bool
	needsLogin, authenticating       bool
	terminalPreparing                bool
	loadedAt                         time.Time
	cancel                           context.CancelFunc
	done                             chan struct{}
	retired                          []*materialsGitBranchesState
}

func (s *app) materialsGitBranchesCurrent() *materialsGitBranchesState {
	return s.documentsGitBranchesCurrent(taskMaterialsGit)
}

func (s *app) documentsGitBranchesSlot(target documentsGitTarget) **materialsGitBranchesState {
	if target == projectInstructionsGit {
		return &s.materialsUnit.state.projectGitBranches
	}
	return &s.materialsUnit.state.materialsGitBranches
}

func (s *app) documentsGitBranchesCurrent(target documentsGitTarget) *materialsGitBranchesState {
	state := *s.documentsGitBranchesSlot(target)
	config, ok := s.materialsGitConfig()
	if state == nil || state.closed || state.invalidated || !ok ||
		target.scope(s.snapshot.Home, config) != state.scope || s.documentsGitSettingsPending(target, config) {
		return nil
	}
	return state
}

func (s *app) materialsGitBranchReady(branch string) bool {
	state := s.materialsGitBranchesCurrent()
	return state != nil && state.loaded && !state.loading && state.err == "" && slices.Contains(state.branches, branch)
}

func (s *app) observeMaterialsGitBranches() {
	for _, target := range []documentsGitTarget{taskMaterialsGit, projectInstructionsGit} {
		state := *s.documentsGitBranchesSlot(target)
		if state == nil {
			continue
		}
		page := s.settingsUnit.state.settingsPage
		visible := page == "project-instructions"
		if target == taskMaterialsGit {
			visible = page == "materials-sync" || page == "paths"
		}
		if s.page != "settings" || !visible {
			state.visible = false
		}
		if s.documentsGitBranchesCurrent(target) == nil {
			state.invalidated = true
			if state.cancel != nil {
				state.cancel()
			}
		}
	}
}

func (s *app) loadMaterialsGitBranches(force bool) {
	s.loadDocumentsGitBranches(taskMaterialsGit, force)
}

func (s *app) loadDocumentsGitBranches(target documentsGitTarget, force bool) {
	config, ok := s.materialsGitConfig()
	if !ok || s.materialsUnit.state.materialsGitCapture || s.documentsGitSettingsPending(target, config) {
		return
	}
	backend, ok := s.backend.(application.MaterialsGitBranchesBackend)
	update := taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate)
	if !ok || update == nil {
		return
	}
	scope := target.scope(s.snapshot.Home, config)
	previous := *s.documentsGitBranchesSlot(target)
	if previous != nil && previous.closed {
		return
	}
	if previous != nil && !previous.invalidated && previous.scope == scope {
		visible := previous.visible
		previous.visible = true
		if previous.loading || previous.terminalPreparing || !force && (visible || time.Since(previous.loadedAt) < 30*time.Second) {
			return
		}
	}
	s.startMaterialsGitBranchRead(target, scope, backend, nil, update)
}

func (s *app) beginMaterialsGitLogin() {
	s.beginDocumentsGitLogin(taskMaterialsGit)
}

func (s *app) beginDocumentsGitLogin(target documentsGitTarget) {
	state := s.documentsGitBranchesCurrent(target)
	login, canLogin := s.backend.(application.MaterialsGitLoginBackend)
	branches, canRead := s.backend.(application.MaterialsGitBranchesBackend)
	update := taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate)
	if state == nil || !state.needsLogin || state.loading || state.terminalPreparing || s.busy || !canLogin || !canRead || update == nil ||
		!workspace.MaterialsGitUsesHTTPS(state.scope.URL) {
		return
	}
	s.startMaterialsGitBranchRead(target, state.scope, branches, login, update)
}

func (s *app) startMaterialsGitBranchRead(target documentsGitTarget, scope workspace.MaterialsGitScope, backend application.MaterialsGitBranchesBackend, login application.MaterialsGitLoginBackend, update func(func())) {
	slot := s.documentsGitBranchesSlot(target)
	previous := *slot
	state := &materialsGitBranchesState{scope: scope, visible: true, loading: true, authenticating: login != nil, branches: []string{}}
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
	*slot = state
	if scope.URL == "" {
		state.loading, state.loadedAt = false, time.Now()
		return
	}
	if err := domain.ValidateRemoteGitURL(scope.URL); err != nil {
		state.loading, state.loaded, state.err = false, true, err.Error()
		state.loadedAt = time.Now()
		return
	}
	timeout := 25 * time.Second
	if login != nil {
		timeout += 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	state.cancel, state.done = cancel, make(chan struct{})
	window := s.win
	go func() {
		defer close(state.done)
		defer cancel()
		var branches []string
		var err error
		if login != nil {
			err = login.VerifyMaterialsGitLogin(ctx, scope)
		}
		if err == nil && ctx.Err() == nil {
			branches, err = backend.MaterialsGitBranches(ctx, scope)
		}
		if err == nil {
			err = ctx.Err()
		}
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.documentsGitBranchesCurrent(target) != state {
				return
			}
			state.loading, state.loaded, state.authenticating, state.loadedAt = false, true, false, time.Now()
			if err != nil {
				state.needsLogin = login != nil || workspace.GitRemoteNeedsAuthentication(err)
				switch {
				case errors.Is(err, context.Canceled):
					state.err = "Git 登录验证已取消。"
				case login != nil:
					state.err = "登录验证或刷新分支失败：" + err.Error()
				default:
					state.err = "读取远程分支失败：" + err.Error()
				}
			} else {
				state.branches = slices.Clone(branches)
				if login != nil {
					s.lastMessage = "Git 登录验证通过，已刷新远程分支"
				}
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *app) prepareDocumentsGitLoginTerminal(target documentsGitTarget, services ui.Services, open bool) {
	state := s.documentsGitBranchesCurrent(target)
	backend, ok := s.backend.(application.MaterialsGitLoginBackend)
	window := s.win
	update := taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
	if state == nil || !state.needsLogin || state.loading || state.terminalPreparing || s.busy || !ok || update == nil ||
		!workspace.MaterialsGitUsesHTTPS(state.scope.URL) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	state.terminalPreparing, state.cancel, state.done = true, cancel, make(chan struct{})
	go func() {
		defer close(state.done)
		defer cancel()
		command, err := backend.MaterialsGitTerminalLoginCommand(ctx, state.scope)
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.documentsGitBranchesCurrent(target) != state {
				return
			}
			state.terminalPreparing = false
			if err != nil {
				s.lastError = "准备 Git 验证命令失败：" + err.Error()
			} else if s.copyTextToClipboard(services, command, "已复制 Git 验证命令") && open {
				s.act("system.terminal", "", "", "", state.scope.Home, "", nil)
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *materialsController) stopMaterialsGitBranches() {
	for _, state := range []*materialsGitBranchesState{s.state.materialsGitBranches, s.state.projectGitBranches} {
		if state == nil {
			continue
		}
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
	for _, state := range []*materialsGitBranchesState{s.state.materialsGitBranches, s.state.projectGitBranches} {
		if state == nil {
			continue
		}
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
