package desktop

import (
	"context"

	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type materialsGitDesktopState struct {
	generation, timerGeneration           uint64
	intervalMinutes                       int
	scope                                 workspace.MaterialsGitScope
	status                                workspace.MaterialsGitSyncStatus
	phase                                 string
	reading, running, closed, needsResume bool
	timer                                 *time.Timer
	cancel                                context.CancelFunc
	done                                  chan struct{}
	retired                               []*materialsGitDesktopState
}

func (s *app) materialsGitConfig() (domain.AppConfig, bool) {
	if s.snapshot.Home == "" || s.settingsUnit.state.configJSON == "" {
		return domain.AppConfig{}, false
	}
	config, err := s.readCurrentAppConfig()
	// Settings editors own their materials value; the rest of this borrowed
	// configuration is only used for read-only scope and environment checks.
	if config.MaterialsGitSync != nil {
		settings := *config.MaterialsGitSync
		config.MaterialsGitSync = &settings
	}
	return config, err == nil
}

func (s *app) materialsGitRunning() bool {
	return s.materialsUnit.state.materialsGit != nil && s.materialsUnit.state.materialsGit.running
}

func (s *app) materialsGitSettingsPending(config domain.AppConfig) bool {
	settings := config.MaterialsGitSettings()
	expected := map[string]string{
		"setting:materialsGitSync:intervalMinutes": fmt.Sprint(settings.IntervalMinutes),
		"setting:requirementMaterialsSubdirectory": materialsGitOptional(config.RequirementMaterialsSubdirectory),
	}
	for key, value := range expected {
		if input := s.inputs[key]; input != nil && strings.TrimSpace(*input) != value {
			return true
		}
	}
	return false
}

func (s *app) materialsGitScopeCurrent(scope workspace.MaterialsGitScope) bool {
	config, ok := s.materialsGitConfig()
	return ok && application.MaterialsGitScope(s.snapshot.Home, config) == scope
}

func (s *app) observeMaterialsGitSync() {
	if s.materialsUnit.state.materialsGitCapture {
		return
	}
	config, ok := s.materialsGitConfig()
	if !ok || s.backend == nil || taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate) == nil {
		return
	}
	backend, ok := s.backend.(application.MaterialsGitBackend)
	if !ok {
		return
	}
	scope := application.MaterialsGitScope(s.snapshot.Home, config)
	settings := config.MaterialsGitSettings()
	state := s.materialsUnit.state.materialsGit
	if state != nil && state.closed {
		return
	}
	if state != nil && state.scope == scope {
		minutes := settings.IntervalMinutes
		if state.intervalMinutes != minutes {
			state.intervalMinutes = minutes
			if !state.running && !state.reading && settings.Enabled && !state.status.Paused && !state.needsResume {
				s.scheduleMaterialsGit(state, time.Duration(minutes)*time.Minute)
			}
		}
		return
	}
	changedTarget := state != nil && (state.scope.Root != scope.Root ||
		!domain.SameMaterialsGitRepositoryURL(state.scope.URL, scope.URL) || state.scope.Branch != scope.Branch ||
		state.scope.Subdirectory != scope.Subdirectory || state.scope.Home != scope.Home ||
		state.scope.RemoteName != scope.RemoteName || state.scope.BindingID != scope.BindingID)
	spellingChanged := state != nil && !changedTarget && state.scope.URL != scope.URL
	needsResume := changedTarget || (spellingChanged && state.needsResume)
	if state != nil {
		if state.timer != nil {
			state.timer.Stop()
		}
		if state.cancel != nil {
			state.cancel()
		}
		if state.running {
			return
		} // cleanup owns the slot until the command ends
	}
	var retired []*materialsGitDesktopState
	if state != nil {
		for _, previous := range append(state.retired, state) {
			if previous.done == nil {
				continue
			}
			select {
			case <-previous.done:
			default:
				retired = append(retired, previous)
			}
		}
	}
	state = &materialsGitDesktopState{scope: scope, reading: true, needsResume: needsResume, intervalMinutes: settings.IntervalMinutes, retired: retired}
	s.materialsUnit.state.materialsGit = state
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	state.cancel, state.done = cancel, make(chan struct{})
	window := s.win
	update := taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
	go func() {
		defer close(state.done)
		defer cancel()
		status, err := backend.MaterialsGitStatus(ctx, scope)
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.materialsUnit.state.materialsGit != state || state.closed || !s.materialsGitScopeCurrent(scope) {
				return
			}
			state.reading, state.status = false, status
			if err != nil {
				state.status.Paused, state.status.Error = true, err.Error()
			}
			if state.needsResume {
				state.phase = "同步目标已变化，请重新检查并恢复"
			}
			if settings.Enabled && !state.status.Paused && !state.needsResume {
				delay := time.Duration(0)
				if spellingChanged {
					delay = time.Duration(state.intervalMinutes) * time.Minute
				}
				s.scheduleMaterialsGit(state, delay)
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *app) scheduleMaterialsGit(state *materialsGitDesktopState, delay time.Duration) {
	if state.closed || taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate) == nil {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	window := s.win
	update := taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
	state.timerGeneration++
	timerGeneration := state.timerGeneration
	state.timer = time.AfterFunc(delay, func() {
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.materialsUnit.state.materialsGit != state || state.closed || state.timerGeneration != timerGeneration || !s.materialsGitScopeCurrent(state.scope) {
				return
			}
			config, ok := s.materialsGitConfig()
			if !ok || !config.MaterialsGitSettings().Enabled || state.status.Paused || state.needsResume {
				return
			}
			s.beginMaterialsGitSync(false)
		})
	})
}

// Only saved files are eligible. Never flush, discard or transplant a draft
// merely because a timer fired; a later cycle can retry after the user saves.
func (s *app) materialsGitHasDraft(root string) bool {
	inside := func(path string) bool {
		if path == "" || root == "" {
			return false
		}
		rel, err := filepath.Rel(root, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if s.materialsUnit.state.documentDirty && inside(s.materialsUnit.state.documentPath) {
		return true
	}
	for path, dirty := range s.materialsUnit.state.documentDirtyMap {
		if dirty && inside(path) {
			return true
		}
	}
	for path := range s.materialsUnit.state.documentDrafts {
		if inside(path) {
			return true
		}
	}
	if s.materialsUnit.state.markdownAutosave != nil {
		s.materialsUnit.state.documentSaveMu.Lock()
		defer s.materialsUnit.state.documentSaveMu.Unlock()
		for key, ref := range s.materialsUnit.state.documentSaveRefs {
			if !inside(ref.path) {
				continue
			}
			status := s.materialsUnit.state.markdownAutosave.Status(key)
			if status.Dirty || status.Saving || status.Composing || status.Error != "" {
				return true
			}
		}
	}
	return false
}

func (s *app) sharedDocumentsGitPeer(config domain.AppConfig, target documentsGitTarget) (browser *app, root string, syncing bool) {
	if !storage.SharedDocumentsCheckout(s.snapshot.Home, config) {
		return nil, "", false
	}
	if target == projectInstructionsGit {
		return s, application.MaterialsGitScope(s.snapshot.Home, config).Root, s.materialsGitRunning()
	}
	current := application.ProjectInstructionsGitScope(s.snapshot.Home, config)
	if view := s.projectInstructions; view != nil && view.scope.Home == current.Home && workspace.SameDocumentsCheckout(view.scope.Root, current.Root) {
		// A branch change can retire the view while its draft or an operation
		// still belongs to the same physical checkout.
		return view.browser, view.scope.Root, view.syncing
	}
	return nil, "", false
}

func (s *app) sharedDocumentsGitPeerBusy(config domain.AppConfig, target documentsGitTarget) bool {
	browser, root, syncing := s.sharedDocumentsGitPeer(config, target)
	return syncing || browser != nil && (browser.materialsFileMutationRunning() || browser.materialsFilePathBlocked() || browser.materialsGitHasDraft(root))
}

func (s *app) refreshSharedDocumentsGitPeer(config domain.AppConfig, target documentsGitTarget) {
	if target == taskMaterialsGit && !s.projectInstructionsCurrent(s.projectInstructions) {
		return
	}
	if browser, _, _ := s.sharedDocumentsGitPeer(config, target); browser != nil {
		browser.requestMaterialsWindowRefresh()
	}
}

func (s *app) beginMaterialsGitSync(manual bool) {
	state := s.materialsUnit.state.materialsGit
	backend, ok := s.backend.(application.MaterialsGitBackend)
	if !ok || state == nil || state.closed || state.reading || state.running || !s.materialsGitScopeCurrent(state.scope) || taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate) == nil {
		return
	}
	config, ok := s.materialsGitConfig()
	if !ok {
		return
	}
	settings := config.MaterialsGitSettings()
	if s.materialsGitSettingsPending(config) {
		state.phase = "等待同步设置保存"
		if settings.Enabled {
			s.scheduleMaterialsGit(state, time.Duration(settings.IntervalMinutes)*time.Minute)
		}
		return
	}
	if err := settings.Validate(); err != nil || settings.RepositoryURL == "" || !config.RequirementMaterialsConfigured() {
		state.status.Paused, state.status.Error = true, "请先填写有效的 Git 地址、分支、周期和任务资料目录"
		return
	}
	if s.busy || s.loading || s.materialsFileMutationRunning() || s.materialsFilePathBlocked() || s.materialsGitHasDraft(state.scope.Root) || s.sharedDocumentsGitPeerBusy(config, taskMaterialsGit) {
		state.phase = "等待资料保存或文件操作完成"
		if manual {
			s.lastError = "请先保存资料，并等待文件操作和同步完成后再同步 Git"
		}
		if settings.Enabled {
			s.scheduleMaterialsGit(state, time.Duration(settings.IntervalMinutes)*time.Minute)
		}
		return
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	state.running, state.needsResume, state.phase = true, false, "核对仓库与分支"
	state.generation++
	state.timerGeneration++
	generation := state.generation
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel, state.done = cancel, make(chan struct{})
	window, scope := s.win, state.scope
	update := taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
	go func() {
		defer close(state.done)
		defer cancel()
		status, err := backend.SyncMaterialsGit(ctx, scope, manual, func(phase string) {
			if window != nil && window.IsDestroyed() {
				return
			}
			update(func() {
				if s.materialsUnit.state.materialsGit == state && state.generation == generation && !state.closed && s.materialsGitScopeCurrent(scope) {
					state.phase = phase
					if window != nil {
						window.Invalidate()
					}
				}
			})
		})
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			state.running = false
			if s.materialsUnit.state.materialsGit != state || state.closed {
				return
			}
			if !s.materialsGitScopeCurrent(scope) {
				s.observeMaterialsGitSync()
				if window != nil {
					window.Invalidate()
				}
				return
			}
			previousSuccess := state.status.LastSuccess
			state.status, state.phase = status, ""
			if state.status.LastSuccess == "" {
				state.status.LastSuccess = previousSuccess
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				state.status.Error = err.Error()
				if manual || status.Paused {
					s.lastError = "任务资料 Git 同步失败：" + err.Error()
				}
			} else if err == nil && manual {
				s.lastMessage = "任务资料 Git 同步完成"
			}
			if err == nil || status.Changed {
				s.requestMaterialsWindowRefresh()
			}
			current, valid := s.materialsGitConfig()
			if valid {
				if status.Changed {
					s.refreshSharedDocumentsGitPeer(current, taskMaterialsGit)
				}
				if current.MaterialsGitSettings().Enabled && !state.status.Paused {
					s.scheduleMaterialsGit(state, time.Duration(current.MaterialsGitSettings().IntervalMinutes)*time.Minute)
				}
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *materialsController) stopMaterialsGitSync() {
	s.stopMaterialsGitBranches()
	if state := s.state.materialsGit; state != nil {
		for _, previous := range append(state.retired, state) {
			previous.closed = true
			if previous.timer != nil {
				previous.timer.Stop()
			}
			if previous.cancel != nil {
				previous.cancel()
			}
		}
	}
}

func (s *materialsController) closeMaterialsGitSync(ctx context.Context) error {
	s.stopMaterialsGitSync()
	if state := s.state.materialsGit; state != nil {
		for _, previous := range append(state.retired, state) {
			if previous.done == nil {
				continue
			}
			select {
			case <-previous.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.closeMaterialsGitBranches(ctx)
}

func materialsGitOptional(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
