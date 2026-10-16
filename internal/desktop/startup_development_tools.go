package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type startupDevelopmentToolsState struct {
	cancel context.CancelFunc
	done   chan struct{}
	closed atomic.Bool
}

// Startup discovery owns one worker and does not occupy an ordinary operation
// or its backend gate. The existing detector persists only missing tool types.
func (s *app) startStartupDevelopmentTools() {
	if s.startupDevelopmentTools != nil || s.backend == nil || s.win == nil || s.win.IsDestroyed() || strings.TrimSpace(s.snapshot.ConfigJSON) == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &startupDevelopmentToolsState{cancel: cancel, done: make(chan struct{})}
	s.startupDevelopmentTools = state
	backend, win, home := s.backend, s.win, s.snapshot.Home
	go func() {
		// Completion never depends on a window callback: shutdown can cancel
		// and join the worker after the native event loop has ended.
		defer close(state.done)
		result, err := backend.Dispatch(ctx, application.Action{Kind: "development-tools/detect"})
		if err != nil || ctx.Err() != nil || state.closed.Load() || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if ctx.Err() != nil || state.closed.Load() || s.win != win || win.IsDestroyed() || s.snapshot.Home != home {
				return
			}
			s.applyStartupDevelopmentToolsResult(result)
		})
	}()
}

func (s *app) applyStartupDevelopmentToolsResult(result application.ActionResult) {
	var detected struct {
		Tools []struct {
			Type domain.DevelopmentToolType `json:"type"`
			Path string                     `json:"path"`
		} `json:"tools"`
		AddedTypes []domain.DevelopmentToolType `json:"addedTypes"`
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil || json.Unmarshal(encoded, &detected) != nil || len(detected.AddedTypes) == 0 {
		return
	}
	var config domain.AppConfig
	if domain.DecodeStrict([]byte(s.configJSON), &config) != nil {
		return
	}
	added := make(map[domain.DevelopmentToolType]bool, len(detected.AddedTypes))
	for _, kind := range detected.AddedTypes {
		added[kind] = true
	}
	present := make(map[domain.DevelopmentToolType]bool, len(config.DevelopmentTools))
	for _, tool := range config.DevelopmentTools {
		present[tool.Type] = true
	}
	changed := false
	for _, tool := range detected.Tools {
		if !added[tool.Type] || present[tool.Type] || strings.TrimSpace(tool.Path) == "" {
			continue
		}
		config.DevelopmentTools = append(config.DevelopmentTools, domain.DevelopmentToolConfig{Type: tool.Type, Path: tool.Path})
		present[tool.Type], changed = true, true
	}
	if !changed {
		return
	}
	encoded, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return
	}
	next := string(encoded)
	s.syncSettingsConfigInputs(s.configJSON, next)
	s.configJSON, s.snapshot.ConfigJSON = next, next
}

// stopStartupDevelopmentTools is safe on the native close callback: it cancels
// without waiting for a backend that may still be completing local cleanup.
func (s *app) stopStartupDevelopmentTools() {
	if state := s.startupDevelopmentTools; state != nil {
		state.closed.Store(true)
		state.cancel()
	}
}

func (s *app) closeStartupDevelopmentTools(ctx context.Context) error {
	state := s.startupDevelopmentTools
	if state == nil {
		return nil
	}
	s.stopStartupDevelopmentTools()
	select {
	case <-state.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
