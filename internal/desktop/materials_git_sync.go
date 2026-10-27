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
	if s.snapshot.Home == "" || s.configJSON == "" {
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

func (s *app) materialsGitRunning() bool { return s.materialsGit != nil && s.materialsGit.running }

func (s *app) materialsGitSettingsPending(config domain.AppConfig) bool {
	settings := config.MaterialsGitSettings()
	expected := map[string]string{
		"setting:materialsGitSync:repositoryUrl":   settings.RepositoryURL,
		"setting:materialsGitSync:intervalMinutes": fmt.Sprint(settings.IntervalMinutes),
		"setting:requirementMaterialsRoot":         materialsGitOptional(config.RequirementMaterialsRoot),
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
	if s.materialsGitCapture {
		return
	}
	config, ok := s.materialsGitConfig()
	if !ok || s.backend == nil || taskContextUpdater(s.win, s.taskContextUpdate) == nil {
		return
	}
	backend, ok := s.backend.(application.MaterialsGitBackend)
	if !ok {
		return
	}
	scope := application.MaterialsGitScope(s.snapshot.Home, config)
	state := s.materialsGit
	if state != nil && state.closed {
		return
	}
	if state != nil && state.scope == scope {
		minutes := config.MaterialsGitSettings().IntervalMinutes
		if state.intervalMinutes != minutes {
			state.intervalMinutes = minutes
			if !state.running && !state.reading && config.MaterialsGitSettings().Enabled && !state.status.Paused && !state.needsResume {
				s.scheduleMaterialsGit(state, time.Duration(minutes)*time.Minute)
			}
		}
		return
	}
	changedTarget := state != nil && (state.scope.Root != scope.Root || state.scope.URL != scope.URL || state.scope.Branch != scope.Branch || state.scope.Subdirectory != scope.Subdirectory || state.scope.Home != scope.Home)
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
	state = &materialsGitDesktopState{scope: scope, reading: true, needsResume: changedTarget, intervalMinutes: config.MaterialsGitSettings().IntervalMinutes, retired: retired}
	s.materialsGit = state
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	state.cancel, state.done = cancel, make(chan struct{})
	window := s.win
	update := taskContextUpdater(window, s.taskContextUpdate)
	go func() {
		defer close(state.done)
		defer cancel()
		status, err := backend.MaterialsGitStatus(ctx, scope)
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.materialsGit != state || state.closed || !s.materialsGitScopeCurrent(scope) {
				return
			}
			state.reading, state.status = false, status
			if err != nil {
				state.status.Paused, state.status.Error = true, err.Error()
			}
			if changedTarget {
				state.phase = "同步目标已变化，请重新检查并恢复"
			}
			if config.MaterialsGitSettings().Enabled && !state.status.Paused && !state.needsResume {
				s.scheduleMaterialsGit(state, 0)
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *app) scheduleMaterialsGit(state *materialsGitDesktopState, delay time.Duration) {
	if state.closed || taskContextUpdater(s.win, s.taskContextUpdate) == nil {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	window := s.win
	update := taskContextUpdater(window, s.taskContextUpdate)
	state.timerGeneration++
	timerGeneration := state.timerGeneration
	state.timer = time.AfterFunc(delay, func() {
		if window != nil && window.IsDestroyed() {
			return
		}
		update(func() {
			if s.materialsGit != state || state.closed || state.timerGeneration != timerGeneration || !s.materialsGitScopeCurrent(state.scope) {
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
	if s.documentDirty && inside(s.documentPath) {
		return true
	}
	for path, dirty := range s.documentDirtyMap {
		if dirty && inside(path) {
			return true
		}
	}
	for path := range s.documentDrafts {
		if inside(path) {
			return true
		}
	}
	if s.markdownAutosave != nil {
		s.documentSaveMu.Lock()
		defer s.documentSaveMu.Unlock()
		for key, ref := range s.documentSaveRefs {
			if !inside(ref.path) {
				continue
			}
			status := s.markdownAutosave.Status(key)
			if status.Dirty || status.Saving || status.Composing || status.Error != "" {
				return true
			}
		}
	}
	return false
}

func (s *app) beginMaterialsGitSync(manual bool) {
	state := s.materialsGit
	backend, ok := s.backend.(application.MaterialsGitBackend)
	if !ok || state == nil || state.closed || state.reading || state.running || !s.materialsGitScopeCurrent(state.scope) || taskContextUpdater(s.win, s.taskContextUpdate) == nil {
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
	if s.busy || s.loading || s.materialsFileMutationRunning() || s.materialsFilePathBlocked() || s.materialsGitHasDraft(state.scope.Root) {
		state.phase = "等待资料保存或文件操作完成"
		if manual {
			s.lastError = "请先保存资料并等待文件操作完成，再同步 Git"
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
	update := taskContextUpdater(window, s.taskContextUpdate)
	go func() {
		defer close(state.done)
		defer cancel()
		status, err := backend.SyncMaterialsGit(ctx, scope, manual, func(phase string) {
			if window != nil && window.IsDestroyed() {
				return
			}
			update(func() {
				if s.materialsGit == state && state.generation == generation && !state.closed && s.materialsGitScopeCurrent(scope) {
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
			if s.materialsGit != state || state.closed {
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
			} else if err == nil {
				if manual {
					s.lastMessage = "任务资料 Git 同步完成"
				}
				s.ensureMaterialsFileManagementState().windowRefreshPending = true
				s.observeMaterialsWindowRefresh()
			}
			if status.Changed && err != nil {
				s.ensureMaterialsFileManagementState().windowRefreshPending = true
				s.observeMaterialsWindowRefresh()
			}
			current, valid := s.materialsGitConfig()
			if valid && current.MaterialsGitSettings().Enabled && !state.status.Paused {
				s.scheduleMaterialsGit(state, time.Duration(current.MaterialsGitSettings().IntervalMinutes)*time.Minute)
			}
			if window != nil {
				window.Invalidate()
			}
		})
	}()
}

func (s *app) stopMaterialsGitSync() {
	s.stopMaterialsGitBranches()
	if state := s.materialsGit; state != nil {
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

func (s *app) closeMaterialsGitSync(ctx context.Context) error {
	s.stopMaterialsGitSync()
	if state := s.materialsGit; state != nil {
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
