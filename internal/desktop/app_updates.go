package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

type appUpdatePreferences struct {
	Disabled      bool                      `json:"disabled,omitempty"`
	CheckTime     string                    `json:"checkTime"`
	LastAttemptAt string                    `json:"lastAttemptAt,omitempty"`
	Result        *domain.UpdateCheckResult `json:"result,omitempty"`
}

type appUpdateState struct {
	home             string
	timeDraft        string
	failure          string
	result           domain.UpdateCheckResult
	checking, manual bool
	closed           bool
	generation       uint64
	timerGeneration  uint64
	timer            *time.Timer
	cancel           context.CancelFunc
	done             <-chan struct{}
	retired          []<-chan struct{}
	lastAttempt      time.Time
	now              func() time.Time
	post             func(func())
	install          *appUpdateInstallState
}

func normalizeAppUpdateTime(value string) (string, bool) {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse("15:04", value)
	return value, err == nil && parsed.Format("15:04") == value
}

// A manual check before today's scheduled time does not consume that check.
// Checks after it, including failed ones, prevent repeated requests on restart.
func nextAppUpdateCheck(now time.Time, clock string, lastAttempt time.Time) time.Time {
	if _, valid := normalizeAppUpdateTime(clock); !valid {
		clock = "10:00"
	}
	parsed, _ := time.Parse("15:04", clock)
	due := time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
	if lastAttempt.Before(due) {
		return due
	}
	return due.AddDate(0, 0, 1)
}

func (s *app) appUpdatePreferences() appUpdatePreferences {
	value := appUpdatePreferences{CheckTime: "10:00"}
	if prefs := s.desktopPreferences; prefs != nil {
		value = prefs.value.Updates
	}
	if _, valid := normalizeAppUpdateTime(value.CheckTime); !valid {
		value.CheckTime = "10:00"
	}
	return value
}

func (s *app) ensureAppUpdateState() *appUpdateState {
	state := s.appUpdates
	if state != nil && state.home == s.snapshot.Home {
		return state
	}
	var retired []<-chan struct{}
	var post func(func())
	if state != nil {
		post = state.post
		stopAppUpdateState(state)
		retired = append(retired, state.retired...)
		if state.done != nil {
			retired = append(retired, state.done)
		}
	}
	prefs := s.appUpdatePreferences()
	state = &appUpdateState{home: s.snapshot.Home, timeDraft: prefs.CheckTime, now: time.Now, post: post, retired: retired}
	state.lastAttempt, _ = time.Parse(time.RFC3339, prefs.LastAttemptAt)
	if prefs.Result != nil && validAppUpdateResult(*prefs.Result) {
		state.result = *prefs.Result
	}
	s.appUpdates = state
	return state
}

func (s *app) hasAppUpdate() bool {
	var result domain.UpdateCheckResult
	if state := s.appUpdates; state != nil && state.home == s.snapshot.Home {
		result = state.result
	} else if cached := s.appUpdatePreferences().Result; cached != nil {
		result = *cached
	}
	return validAppUpdateResult(result) && result.Available(domain.CurrentProductVersion, productprofile.ReleaseRepository, productprofile.ReleaseTagPrefix)
}

func validAppUpdateResult(result domain.UpdateCheckResult) bool {
	_, err := time.Parse(time.RFC3339, result.CheckedAt)
	return err == nil && result.Platform == runtime.GOOS+"/"+runtime.GOARCH && result.Valid(productprofile.ReleaseRepository, productprofile.ReleaseTagPrefix)
}

func (s *app) appUpdatePoster() func(func()) {
	var post func(func())
	if state := s.appUpdates; state != nil {
		post = state.post
	}
	return taskContextUpdater(s.win, post)
}

func (s *app) saveAppUpdatePreferences(value appUpdatePreferences) error {
	prefs := s.desktopPreferences
	if prefs == nil || !prefs.enabled || prefs.store == nil || prefs.closed || prefs.home != s.snapshot.Home {
		return errors.New("应用数据目录尚未加载，无法保存更新偏好")
	}
	saved, err := prefs.store.Update(func(latest *DesktopPreferences) { latest.Updates = value })
	if err != nil {
		return fmt.Errorf("保存更新偏好失败：%w", err)
	}
	prefs.value.Updates = saved.Updates
	return nil
}

func (s *app) setAppUpdateSchedule(enabled bool, clock string) {
	state := s.ensureAppUpdateState()
	clock, valid := normalizeAppUpdateTime(clock)
	if !valid {
		state.failure = "检查时间请填写 HH:mm，例如 10:00"
		return
	}
	prefs := s.appUpdatePreferences()
	prefs.Disabled, prefs.CheckTime = !enabled, clock
	if err := s.saveAppUpdatePreferences(prefs); err != nil {
		state.failure = err.Error()
		return
	}
	state.failure, state.timeDraft = "", clock
	stopAppUpdateTimer(state)
	if !enabled && state.checking && !state.manual {
		state.generation++
		state.cancel()
		state.checking = false
	}
	s.observeAppUpdates()
}

func (s *app) observeAppUpdates() {
	prefs := s.desktopPreferences
	if s.loading || prefs == nil || !prefs.enabled || prefs.store == nil || prefs.closed || s.snapshot.Home == "" || s.backend == nil {
		return
	}
	update := s.appUpdatePoster()
	if update == nil {
		return
	}
	state := s.ensureAppUpdateState()
	if state.closed || state.checking {
		return
	}
	preference := s.appUpdatePreferences()
	if preference.Disabled {
		stopAppUpdateTimer(state)
		return
	}
	now := state.now()
	due := nextAppUpdateCheck(now, preference.CheckTime, state.lastAttempt)
	if !due.After(now) {
		s.beginAppUpdateCheck(false)
		return
	}
	if state.timer != nil {
		return
	}
	// Re-evaluate wall time after sleep, clock or timezone changes.
	delay := min(due.Sub(now), time.Minute)
	state.timerGeneration++
	generation := state.timerGeneration
	state.timer = time.AfterFunc(delay, func() {
		update(func() {
			if s.appUpdates != state || state.closed || state.timerGeneration != generation || state.home != s.snapshot.Home {
				return
			}
			state.timer = nil
			s.observeAppUpdates()
		})
	})
}

func (s *app) beginAppUpdateCheck(manual bool) {
	state := s.ensureAppUpdateState()
	update := s.appUpdatePoster()
	if state.closed || state.checking || s.backend == nil || update == nil {
		return
	}
	stopAppUpdateTimer(state)
	state.generation++
	generation, home := state.generation, state.home
	state.lastAttempt = state.now()
	prefs := s.appUpdatePreferences()
	prefs.LastAttemptAt = state.lastAttempt.Format(time.RFC3339)
	if err := s.saveAppUpdatePreferences(prefs); err != nil {
		state.failure = err.Error()
		s.observeAppUpdates()
		return
	}
	state.checking, state.manual, state.failure = true, manual, ""
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	done := make(chan struct{})
	if state.done != nil {
		state.retired = append(state.retired, state.done)
	}
	state.cancel, state.done = cancel, done
	live := state.retired[:0]
	for _, previous := range state.retired {
		select {
		case <-previous:
		default:
			live = append(live, previous)
		}
	}
	state.retired = live
	backend := s.backend
	go func() {
		defer close(done)
		defer cancel()
		response, err := backend.Dispatch(ctx, application.Action{Kind: "update.check"})
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		var result domain.UpdateCheckResult
		if err == nil {
			encoded, encodeErr := json.Marshal(response.Data)
			if encodeErr != nil {
				err = encodeErr
			} else {
				err = json.Unmarshal(encoded, &result)
			}
			if err == nil {
				if !validAppUpdateResult(result) || result.CurrentVersion != domain.CurrentProductVersion {
					err = errors.New("更新响应无法识别，请稍后重试")
				}
			}
		}
		update(func() {
			if s.appUpdates != state || state.closed || state.generation != generation || state.home != home || s.snapshot.Home != home {
				return
			}
			state.checking = false
			if err != nil {
				state.failure = "检查更新失败：" + err.Error()
			} else {
				state.result = result
				prefs := s.appUpdatePreferences()
				prefs.Result = &result
				if err := s.saveAppUpdatePreferences(prefs); err != nil {
					state.failure = err.Error()
				}
			}
			s.observeAppUpdates()
		})
	}()
}

func stopAppUpdateTimer(state *appUpdateState) {
	state.timerGeneration++
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
}

func stopAppUpdateState(state *appUpdateState) {
	state.closed = true
	state.generation++
	stopAppUpdateTimer(state)
	if state.cancel != nil {
		state.cancel()
	}
	if install := state.install; install != nil {
		install.closed = true
		if install.timer != nil {
			install.timer.Stop()
			install.timer = nil
		}
		if install.cancel != nil && !install.exiting {
			install.cancel()
		}
	}
}

func (s *app) stopAppUpdates() {
	if state := s.appUpdates; state != nil {
		stopAppUpdateState(state)
	}
}

func (s *app) closeAppUpdates(ctx context.Context) error {
	state := s.appUpdates
	if state == nil {
		return nil
	}
	s.stopAppUpdates()
	for _, done := range append(state.retired, state.done) {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if install := state.install; install != nil && install.done != nil {
		select {
		case <-install.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
