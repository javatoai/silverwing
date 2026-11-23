package desktop

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type projectInstructionsView struct {
	scope           workspace.MaterialsGitScope
	ctx             context.Context
	cancel          context.CancelFunc
	timer           *time.Timer
	timerGeneration uint64
	syncSettings    domain.MaterialsGitSyncSettings
	readingStatus   bool
	syncing         bool
	status          workspace.MaterialsGitSyncStatus
	browser         *app
	port            *projectMaterialsBackend
}

func (s *app) projectInstructionsConfigured() bool {
	return strings.TrimSpace(s.configString("projectInstructionsGitUrl")) != ""
}

func (s *app) observeProjectInstructions() {
	if s.projectInstructionsClosed || s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return
	}
	cfg, err := s.readCurrentAppConfig()
	if err != nil {
		return
	}
	scope := application.ProjectInstructionsGitScope(s.snapshot.Home, cfg)
	settings := cfg.ProjectInstructionsGitSettings()
	if current := s.projectInstructions; current != nil {
		if sameProjectInstructionsCheckout(current.scope, scope) {
			current.port.setScope(scope)
			current.browser.settingsUnit.state.configJSON = s.settingsUnit.state.configJSON
			s.updateProjectInstructionsSchedule(current, settings)
			return
		}
		if current.browser.materialsFileMutationRunning() || !s.captureProjectInstructionsInput() {
			return
		}
		s.retireProjectInstructions(current)
		s.projectInstructions = nil
	}
	if scope.URL == "" || s.snapshot.Home == "" {
		if s.tab == taskProjectInstructionsTab {
			s.activateTaskContentTab(s.restoredTaskContentTab(s.tab))
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	view := &projectInstructionsView{scope: scope, ctx: ctx, cancel: cancel}
	// A retired browser may still be releasing the same checkout lock.
	view.port = newProjectMaterialsBackend(s.backend, scope, ctx, s.projectInstructionsGate)
	s.projectInstructionsGate = view.port.checkout
	view.browser = s.newProjectMaterialsBrowser(view)
	s.projectInstructions = view
	s.updateProjectInstructionsSchedule(view, settings)
	if !settings.Enabled {
		s.readProjectInstructionsSyncStatus(view)
	}
}

func sameProjectInstructionsCheckout(a, b workspace.MaterialsGitScope) bool {
	return a.Home == b.Home && a.Root == b.Root && a.URL == b.URL && a.Branch == b.Branch &&
		a.RemoteName == b.RemoteName && a.BindingID == b.BindingID
}

func (s *app) projectInstructionsCurrent(view *projectInstructionsView) bool {
	if s.projectInstructionsClosed || view == nil || s.projectInstructions != view || view.ctx.Err() != nil {
		return false
	}
	config, ok := s.materialsGitConfig()
	return ok && sameProjectInstructionsCheckout(view.scope, application.ProjectInstructionsGitScope(s.snapshot.Home, config))
}

func (s *app) updateProjectInstructionsSchedule(view *projectInstructionsView, settings domain.MaterialsGitSyncSettings) {
	previous := view.syncSettings
	if previous == settings {
		return
	}
	view.syncSettings = settings
	view.stopSyncTimer()
	if !settings.Enabled || view.syncing || view.status.Paused {
		return
	}
	if !previous.Enabled {
		s.syncProjectInstructions(view, false)
	} else {
		s.scheduleProjectInstructionsSync(view)
	}
}

func (v *projectInstructionsView) stopSyncTimer() {
	v.timerGeneration++
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
}

func (s *app) scheduleProjectInstructionsSync(view *projectInstructionsView) {
	if !s.projectInstructionsCurrent(view) || !view.syncSettings.Enabled || view.status.Paused ||
		s.win == nil || s.win.IsDestroyed() {
		return
	}
	view.stopSyncTimer()
	generation := view.timerGeneration
	window := s.win
	view.timer = time.AfterFunc(time.Duration(view.syncSettings.IntervalMinutes)*time.Minute, func() {
		if view.ctx.Err() == nil && !window.IsDestroyed() {
			window.Update(func() {
				if view.timerGeneration == generation {
					s.syncProjectInstructions(view, false)
				}
			})
		}
	})
}

func (s *app) projectInstructionsJob(view *projectInstructionsView, worker func() func()) {
	window := s.win
	if window == nil || window.IsDestroyed() || !s.projectInstructionsCurrent(view) {
		return
	}
	s.runProjectInstructionsWorker(func() {
		apply := worker()
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			if s.projectInstructionsCurrent(view) && apply != nil {
				apply()
			}
		})
	})
}

func (s *app) runProjectInstructionsWorker(worker func()) {
	active := s.projectInstructionsWorkers[:0]
	for _, job := range s.projectInstructionsWorkers {
		select {
		case <-job:
		default:
			active = append(active, job)
		}
	}
	done := make(chan struct{})
	s.projectInstructionsWorkers = append(active, done)
	go func() {
		defer close(done)
		worker()
	}()
}

func (s *app) syncProjectInstructions(view *projectInstructionsView, resume bool) {
	backend, ok := s.backend.(application.ProjectInstructionsGitBackend)
	if !ok || s.win == nil || s.win.IsDestroyed() || !s.projectInstructionsCurrent(view) || view.syncing {
		return
	}
	cfg, ok := s.materialsGitConfig()
	if !ok || !resume && !cfg.ProjectInstructionsGitSettings().Enabled {
		return
	}
	if s.busy || s.documentsGitSettingsPending(projectInstructionsGit, cfg) {
		s.scheduleProjectInstructionsSync(view)
		return
	}
	scope := application.ProjectInstructionsGitScope(s.snapshot.Home, cfg)
	browserHasEdits := view.browser != nil && (view.browser.materialsFileMutationRunning() || view.browser.materialsGitHasDraft(scope.Root))
	if browserHasEdits || s.sharedDocumentsGitPeerBusy(cfg, projectInstructionsGit) {
		if resume {
			s.lastError = "请先保存资料，并等待文件操作和同步完成后再同步 Git"
		}
		s.scheduleProjectInstructionsSync(view)
		return
	}
	view.stopSyncTimer()
	view.syncing = true
	s.projectInstructionsJob(view, func() func() {
		var status workspace.MaterialsGitSyncStatus
		err := view.port.withCheckout(view.ctx, func(ctx context.Context) error {
			var err error
			status, err = backend.SyncProjectInstructionsGit(ctx, scope, resume, nil)
			return err
		})
		return func() {
			view.syncing = false
			current, ok := s.materialsGitConfig()
			if !ok || application.ProjectInstructionsGitScope(s.snapshot.Home, current) != scope {
				s.scheduleProjectInstructionsSync(view)
				return
			}
			if status.LastSuccess == "" {
				status.LastSuccess = view.status.LastSuccess
			}
			view.status = status
			if err != nil {
				view.status.Error = err.Error()
				view.browser.lastError = err.Error()
			}
			if err == nil || status.Changed {
				view.browser.requestMaterialsWindowRefresh()
			}
			if status.Changed {
				s.refreshSharedDocumentsGitPeer(current, projectInstructionsGit)
			}
			s.scheduleProjectInstructionsSync(view)
		}
	})
}

func (s *app) readProjectInstructionsSyncStatus(view *projectInstructionsView) {
	backend, ok := s.backend.(application.ProjectInstructionsGitBackend)
	if !ok {
		return
	}
	scope := view.scope
	view.readingStatus = true
	s.projectInstructionsJob(view, func() func() {
		ctx, cancel := context.WithTimeout(view.ctx, 20*time.Second)
		defer cancel()
		status, err := backend.ProjectInstructionsGitStatus(ctx, scope)
		return func() {
			view.readingStatus = false
			config, ok := s.materialsGitConfig()
			if view.syncing || !ok || application.ProjectInstructionsGitScope(s.snapshot.Home, config) != scope {
				return
			}
			if err != nil {
				status.Error = err.Error()
			}
			view.status = status
		}
	})
}

func (s *app) captureProjectInstructionsInput() bool {
	if view := s.projectInstructions; view != nil && view.browser != nil {
		browser := view.browser
		if err := browser.captureMaterialTextInput(); err != nil {
			s.lastError = err.Error()
			return false
		}
		if err := browser.captureMarkdownEditorInput(browser.materialsUnit.state.markdown); err != nil {
			s.lastError = err.Error()
			return false
		}
	}
	return true
}

func (s *app) renderProjectInstructions(c *ui.Context, p palette) {
	view := s.projectInstructions
	if !s.projectInstructionsCurrent(view) {
		label(c, "正在准备项目资料…", 13, p.muted).Padding(16)
		return
	}
	ui.Column(c.Key("project-instructions-browser:" + view.scope.Root + "\x00" + view.scope.Branch)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		view.browser.materials(c, p)
	})
}

func (s *app) retireProjectInstructions(view *projectInstructionsView) {
	if err := view.browser.closeReadingSession(); err != nil {
		s.lastError = err.Error()
	}
	view.stop()
	s.runProjectInstructionsWorker(func() {
		if err := view.drain(); err != nil && s.win != nil && !s.win.IsDestroyed() {
			s.win.Update(func() { s.lastError = err.Error() })
		}
	})
}

func (s *app) closeProjectInstructions(ctx context.Context) error {
	s.projectInstructionsClosed = true
	if view := s.projectInstructions; view != nil {
		view.stop()
	}
	for _, done := range s.projectInstructionsWorkers {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
