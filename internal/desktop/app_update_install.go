package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/appupdate"
)

// This owner binds coordinator receipts to one window/home. It does not
// download packages, discover installers, replace files, or run commands.
type appUpdateInstallState struct {
	home                                string
	backend                             application.Backend
	status                              appupdate.Snapshot
	errorText                           string
	preparing, exiting, closed, polling bool
	generation                          uint64
	cancel                              context.CancelFunc
	timer                               *time.Timer
	done                                <-chan struct{}
	requestClose                        func()
}

func (s *app) beginAppUpdateInstall() {
	owner := s.ensureAppUpdateState()
	post := s.appUpdatePoster()
	if owner.closed || post == nil || s.backend == nil || !s.hasAppUpdate() || !owner.result.CanAutoUpdate {
		return
	}
	state := owner.install
	if state != nil && state.home == s.snapshot.Home && state.status.Phase == "ready" && !state.closed {
		s.requestAppUpdateExit(state)
		return
	}
	if state != nil && (state.preparing || state.exiting) {
		return
	}
	if err := s.appUpdateExitReady(); err != nil {
		owner.failure = err.Error()
		return
	}
	state = &appUpdateInstallState{home: s.snapshot.Home, backend: s.backend, preparing: true, status: appupdate.Snapshot{Phase: "preparing", Version: owner.result.Release.Version}}
	owner.install = state
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	state.cancel = cancel
	done := make(chan struct{})
	state.done = done
	state.generation++
	generation := state.generation
	version := state.status.Version
	s.pollAppUpdateInstall(owner, state)
	go func() {
		defer cancel()
		defer close(done)
		response, err := state.backend.Dispatch(ctx, application.Action{Kind: "update.prepare", Payload: map[string]any{"confirmed": true, "version": version}})
		var status appupdate.Snapshot
		if err == nil {
			content, e := json.Marshal(response.Data)
			if e != nil {
				err = e
			} else {
				err = json.Unmarshal(content, &status)
			}
		}
		post(func() {
			if s.appUpdates != owner || owner.closed || owner.install != state || state.closed || state.generation != generation || s.snapshot.Home != state.home {
				return
			}
			state.preparing = false
			if state.timer != nil {
				state.timer.Stop()
				state.timer = nil
			}
			if err != nil {
				state.status.Phase, state.errorText = "failed", err.Error()
				return
			}
			if status.Phase != "ready" || status.ID == "" {
				state.status.Phase, state.errorText = "failed", "更新准备未完成"
				return
			}
			state.status = status
			s.requestAppUpdateExit(state)
		})
	}()
}

func (s *app) pollAppUpdateInstall(owner *appUpdateState, state *appUpdateInstallState) {
	post := s.appUpdatePoster()
	if state.closed || !state.preparing || state.timer != nil || post == nil {
		return
	}
	state.timer = time.AfterFunc(350*time.Millisecond, func() {
		post(func() {
			state.timer = nil
			if s.appUpdates != owner || owner.closed || state.closed || !state.preparing || s.snapshot.Home != state.home || state.polling {
				return
			}
			state.polling = true
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				response, err := state.backend.Dispatch(ctx, application.Action{Kind: "update.status"})
				var status appupdate.Snapshot
				if err == nil {
					content, e := json.Marshal(response.Data)
					if e == nil {
						_ = json.Unmarshal(content, &status)
					}
				}
				post(func() {
					state.polling = false
					if s.appUpdates != owner || owner.closed || state.closed || !state.preparing || s.snapshot.Home != state.home {
						return
					}
					if status.Version == state.status.Version && status.ID != "" {
						state.status = status
					}
					s.pollAppUpdateInstall(owner, state)
				})
			}()
		})
	})
}

func (s *app) cancelAppUpdateInstall() {
	if s.appUpdates == nil || s.appUpdates.install == nil {
		return
	}
	state := s.appUpdates.install
	if state.exiting {
		return
	}
	state.generation++
	if state.cancel != nil {
		state.cancel()
	}
	state.preparing = false
	state.status.Phase = "cancelled"
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	// Cancelling this request's context cannot cancel a subsequently started job.
}

func (s *app) appUpdateExitReady() error {
	if s.documentCloseOperationBusy() {
		return errors.New("当前操作尚未完成，请稍后再更新")
	}
	if production := s.releaseUnit.state.production; production != nil {
		production.mu.Lock()
		active := len(production.workers) > 0
		production.mu.Unlock()
		if active {
			return errors.New("发布操作尚未结束，请完成后再更新")
		}
	}
	if jobs := s.releaseUnit.state.tagJobs; jobs != nil {
		for _, view := range jobs.views {
			if view.snapshot.Busy || view.commandRunning || len(view.commands) > 0 {
				return errors.New("Tag 操作尚未结束，请完成后再更新")
			}
		}
	}
	if state := s.settingsUnit.state.settingsAutosave; state != nil {
		for _, entry := range state.entries {
			if entry.failed {
				return errors.New("部分设置尚未保存，请处理后再更新")
			}
		}
	}
	return s.captureDocumentCloseInput()
}

func (s *app) requestAppUpdateExit(state *appUpdateInstallState) {
	if err := s.appUpdateExitReady(); err != nil {
		state.errorText, state.status.Phase = err.Error(), "ready"
		return
	}
	state.exiting, state.errorText = true, ""
	if state.requestClose != nil {
		state.requestClose()
		return
	}
	if s.win == nil || s.win.IsDestroyed() {
		state.exiting, state.errorText = false, "窗口不可用，更新包已保留"
		return
	}
	s.win.Close()
}

func (s *app) abortAppUpdateExit(err error) {
	if s.appUpdates == nil || s.appUpdates.install == nil || !s.appUpdates.install.exiting {
		return
	}
	state := s.appUpdates.install
	state.exiting, state.status.Phase = false, "ready"
	if err != nil {
		state.errorText = err.Error()
	}
	if settings := s.settingsUnit.state.settingsAutosave; settings != nil {
		settings.closing = false
	}
}

func (s *app) guardAppUpdateClose(event *mygo.CloseEvent) bool {
	if s.appUpdates == nil || s.appUpdates.install == nil || !s.appUpdates.install.exiting {
		return false
	}
	if err := s.appUpdateExitReady(); err != nil {
		event.PreventDefault()
		s.abortAppUpdateExit(err)
		return true
	}
	return false
}

func (s *app) observeAppUpdateInstall() {
	if s.appUpdates == nil || s.appUpdates.install == nil || !s.appUpdates.install.exiting {
		return
	}
	if state := s.settingsUnit.state.settingsAutosave; state != nil {
		for _, entry := range state.entries {
			if entry.failed {
				s.abortAppUpdateExit(errors.New("部分设置保存失败，更新未安装"))
				return
			}
		}
	}
}

// Called only after the native event loop has ended and all existing closers
// have durably saved state and released terminals. No file replacement happens
// on a cancelled close or a failed shutdown.
func (s *app) applyAppUpdateAfterExit(runErr error) error {
	if s.appUpdates == nil || s.appUpdates.install == nil {
		return runErr
	}
	state := s.appUpdates.install
	if !state.exiting || state.status.Phase != "ready" {
		return runErr
	}
	if runErr != nil {
		return fmt.Errorf("退出保存未完成，更新未安装：%w", runErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := state.backend.Dispatch(ctx, application.Action{Kind: "update.apply", Payload: map[string]any{"confirmed": true, "jobId": state.status.ID}})
	return err
}
