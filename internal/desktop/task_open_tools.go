package desktop

import (
	"context"
	"time"

	"silverwing-go/internal/application"
)

// Availability is presentation-only. Launchers check it again at execution.
// Each read owns its immutable home/config scope and never launches a tool.
type taskOpenToolsState struct {
	home, config string
	options      []taskCreationWorkspaceToolOption
	loaded       bool
	running      bool
	retryAfter   time.Time
	cancel       context.CancelFunc
}

func (s *app) observeTaskOpenTools() {
	s.observeTaskOpenToolsAt(time.Now())
}

func (s *app) observeTaskOpenToolsAt(now time.Time) {
	state := s.taskUnit.state.taskOpenTools
	if state == nil || state.home != s.snapshot.Home || state.config != s.settingsUnit.state.configJSON {
		s.taskUnit.stopTaskOpenTools()
		state = &taskOpenToolsState{home: s.snapshot.Home, config: s.settingsUnit.state.configJSON}
		s.taskUnit.state.taskOpenTools = state
	}
	// The protocol-based Codex launcher needs no executable discovery. Avoid
	// probing unrelated tools when Cursor's menu entry is not enabled. Successful
	// unavailable results are cached; failed reads retry only after a cooldown.
	if !s.taskOpenToolEnabled("cursor") || state.loaded || state.running || now.Before(state.retryAfter) {
		return
	}
	window := s.win
	update := taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
	if s.backend == nil || update == nil || window != nil && window.IsDestroyed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	state.cancel, state.running = cancel, true
	backend := s.backend
	go func() {
		defer cancel()
		result, err := backend.Dispatch(ctx, application.Action{Kind: "workspace.tools-options", Payload: map[string]any{"taskCreateToken": "task-menu-tools"}})
		if window != nil && window.IsDestroyed() {
			return
		}
		options, decodeErr := decodeData[taskCreationWorkspaceToolOption](result.Data)
		success := ctx.Err() == nil && err == nil && decodeErr == nil && options != nil
		update(func() {
			if s.taskUnit.state.taskOpenTools != state || state.home != s.snapshot.Home || state.config != s.settingsUnit.state.configJSON {
				return
			}
			state.cancel, state.running = nil, false
			if success {
				state.options, state.loaded = options, true
				state.retryAfter = time.Time{}
			} else {
				state.retryAfter = time.Now().Add(5 * time.Second)
			}
		})
	}()
}

func (s *taskController) stopTaskOpenTools() {
	if state := s.state.taskOpenTools; state != nil && state.cancel != nil {
		state.cancel()
	}
	s.state.taskOpenTools = nil
}

func (s *app) taskOpenToolEnabled(id string) bool {
	return containsString(stringArray(s.configArray("defaultWorkspaceToolIds")), id)
}

func (s *app) taskOpenToolAvailable(id string) bool {
	state := s.taskUnit.state.taskOpenTools
	if state != nil && state.home == s.snapshot.Home && state.config == s.settingsUnit.state.configJSON && state.loaded {
		for _, option := range state.options {
			if option.ID == id {
				return option.Available
			}
		}
		return false
	}
	// Codex's registered launcher uses the native protocol and has no executable
	// probe. Cursor remains disabled until its executable has been discovered.
	return id == "codex"
}
