package desktop

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

const settingsAutosaveDelay = 500 * time.Millisecond

type settingsAutosaveEntry struct {
	value   string
	save    func(string)
	latest  func() string
	input   *string
	due     time.Time
	pending bool
	failed  bool
}

type settingsAutosaveAttempt struct {
	entry  *settingsAutosaveEntry
	value  string
	kind   string
	config string
}

// The window thread owns drafts and dispatch. Existing application/config locks
// perform the writes; this queue only coalesces typing and serializes fields.
type settingsAutosaveState struct {
	home    string
	entries map[string]*settingsAutosaveEntry
	active  *settingsAutosaveAttempt
	timer   *time.Timer
	failure string
	closing bool
	closed  bool
}

func (s *app) observeSettingsAutosave(key, value string, save func(string), latest ...func() string) {
	if save == nil {
		return
	}
	state := s.settingsUnit.state.settingsAutosave
	if state == nil || state.home != s.snapshot.Home {
		s.settingsUnit.stopSettingsAutosave()
		state = &settingsAutosaveState{home: s.snapshot.Home, entries: make(map[string]*settingsAutosaveEntry)}
		s.settingsUnit.state.settingsAutosave = state
	}
	entry := state.entries[key]
	if entry == nil {
		entry = &settingsAutosaveEntry{value: value}
		state.entries[key] = entry
	} else if entry.value != value {
		entry.value, entry.pending, entry.failed, entry.due = value, true, false, time.Now().Add(settingsAutosaveDelay)
	}
	entry.save = save
	if len(latest) > 0 {
		entry.latest = latest[0]
	}
	s.armSettingsAutosave()
}

func (s *app) cancelSettingsAutosave(key string) {
	if state := s.settingsUnit.state.settingsAutosave; state != nil {
		delete(state.entries, key)
	}
}

func (s *app) armSettingsAutosave() {
	state := s.settingsUnit.state.settingsAutosave
	if state == nil || state.closed || state.active != nil || s.busy || s.win == nil || s.win.IsDestroyed() {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	var next time.Time
	for _, entry := range state.entries {
		if entry.pending && (next.IsZero() || entry.due.Before(next)) {
			next = entry.due
		}
	}
	if next.IsZero() {
		return
	}
	window := s.win
	state.timer = time.AfterFunc(max(time.Until(next), 0), func() {
		if !window.IsDestroyed() {
			window.Update(func() {
				if s.settingsUnit.state.settingsAutosave == state && !state.closed {
					s.flushSettingsAutosaves(false)
				}
			})
		}
	})
}

func (s *app) flushSettingsAutosaves(force bool) {
	state := s.settingsUnit.state.settingsAutosave
	if state == nil || state.closed || state.home != s.snapshot.Home || state.active != nil || s.busy {
		return
	}
	for {
		var next *settingsAutosaveEntry
		for _, entry := range state.entries {
			if entry.pending && (next == nil || entry.due.Before(next.due)) {
				next = entry
			}
		}
		if next == nil || !force && !state.closing && time.Now().Before(next.due) {
			break
		}
		attempt := &settingsAutosaveAttempt{entry: next, value: next.value, config: s.settingsUnit.state.configJSON}
		state.active, next.pending, s.lastError = attempt, false, ""
		next.save(attempt.value)
		if s.busy {
			attempt.kind = s.operationKind
			return
		}
		state.active = nil
		next.failed = s.lastError != ""
		if next.failed {
			state.failure, s.lastError, state.closing = "设置未能自动保存："+s.lastError+"；当前输入已保留。", "", false
		}
	}
	s.armSettingsAutosave()
	s.closeAfterSettingsAutosave()
}

func (s *app) finishSettingsAutosaveAction(action application.Action, result application.ActionResult, failure error) {
	state := s.settingsUnit.state.settingsAutosave
	if state == nil || state.closed {
		return
	}
	if attempt := state.active; attempt != nil && attempt.kind == action.Kind {
		state.active = nil
		if failure != nil {
			if attempt.entry.value == attempt.value {
				attempt.entry.failed = true
			}
			state.failure, state.closing = "设置未能自动保存："+failure.Error()+"；当前输入已保留。", false
			s.lastError = ""
		} else {
			attempt.entry.failed = false
			// Only the successful save may normalize its unchanged draft. Other
			// background reads must leave the in-flight input intact.
			if !attempt.entry.pending && attempt.entry.latest != nil && attempt.entry.latest() == attempt.value {
				s.syncSettingsConfigInputs(attempt.config, s.settingsUnit.state.configJSON)
			}
			if s.lastMessage == result.Message {
				s.lastMessage = ""
			}
		}
	}
	failed := false
	for _, entry := range state.entries {
		failed = failed || entry.failed
	}
	if !failed {
		state.failure = ""
	}
	s.flushSettingsAutosaves(false)
}

func (s *app) renderSettingsAutosaveNotification(c *ui.Context) {
	state := s.settingsUnit.state.settingsAutosave
	if state == nil {
		return
	}
	empty := ""
	s.operationNotifications.present(c, "settings-autosave", &empty, &state.failure, func() {
		for _, entry := range state.entries {
			if entry.failed {
				entry.failed, entry.pending, entry.due = false, true, time.Now()
			}
		}
		state.failure = ""
		s.flushSettingsAutosaves(true)
	})
}

func (s *app) handleSettingsAutosaveClose(event *mygo.CloseEvent) bool {
	state := s.settingsUnit.state.settingsAutosave
	if event == nil || state == nil || state.closed {
		return false
	}
	for _, entry := range state.entries {
		if entry.latest != nil {
			if value := entry.latest(); value != entry.value {
				entry.value, entry.pending, entry.failed, entry.due = value, true, false, time.Now()
			}
		}
	}
	pending, failed := state.active != nil, false
	for _, entry := range state.entries {
		pending, failed = pending || entry.pending, failed || entry.failed
	}
	if !pending && !failed {
		return false
	}
	event.PreventDefault()
	if !pending && failed {
		discarded := make(map[*settingsAutosaveEntry]string)
		for _, entry := range state.entries {
			if entry.failed {
				discarded[entry] = entry.value
			}
		}
		s.confirmNative("部分设置尚未保存", "是否放弃未保存的输入并关闭？", state.failure, func() {
			for entry, value := range discarded {
				if entry.failed && entry.value == value && (entry.latest == nil || entry.latest() == value) {
					entry.failed, entry.pending = false, false
				}
			}
			s.win.Close()
		})
		return true
	}
	state.closing = true
	s.flushSettingsAutosaves(true)
	return true
}

func (s *app) closeAfterSettingsAutosave() {
	state := s.settingsUnit.state.settingsAutosave
	if state == nil || !state.closing || state.active != nil || s.busy {
		return
	}
	for _, entry := range state.entries {
		if entry.pending || entry.failed {
			return
		}
	}
	state.closing = false
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Close()
	}
}

func (s *settingsController) stopSettingsAutosave() {
	if state := s.state.settingsAutosave; state != nil {
		state.closed = true
		if state.timer != nil {
			state.timer.Stop()
		}
	}
}

// Common text/path controls share a stable source pointer for final close flush.
func (s *app) autosaveSettingsInput(key string, value *string, save func(string)) {
	if value == nil || save == nil {
		return
	}
	s.observeSettingsAutosave(key, *value, save, func() string { return *value })
	s.settingsUnit.state.settingsAutosave.entries[key].input = value
}

func settingsAutosaveFieldKey(key string) string { return "setting:" + strings.TrimSpace(key) }

func (s *app) settingsAutosaveDraftPending(key string) bool {
	state := s.settingsUnit.state.settingsAutosave
	if state == nil || state.entries[key] == nil {
		return false
	}
	entry := state.entries[key]
	return entry.pending || entry.failed || state.active != nil && state.active.entry == entry || entry.latest != nil && entry.latest() != entry.value
}

func (s *app) settingsAutosaveInputPending(input *string) bool {
	if input == nil {
		return false
	}
	if state := s.settingsUnit.state.settingsAutosave; state != nil {
		for key, entry := range state.entries {
			if entry.input == input && s.settingsAutosaveDraftPending(key) {
				return true
			}
		}
	}
	return false
}

func (s *app) rebaseSettingsAutosaveInputs() {
	if state := s.settingsUnit.state.settingsAutosave; state != nil {
		for _, entry := range state.entries {
			if !entry.pending && !entry.failed && entry.latest != nil && (state.active == nil || state.active.entry != entry) {
				entry.value = entry.latest()
			}
		}
	}
}
