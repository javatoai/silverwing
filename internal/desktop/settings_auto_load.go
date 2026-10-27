package desktop

import (
	"context"
	"strings"
	"sync"
)

type pageLocalReadJob struct {
	key, home, config string
	cancel            context.CancelFunc
	done              chan struct{}
}

// One local snapshot read owns the navigation entry. Retired reads finish
// before a new one starts; neither ordinary busy nor its backend gate is used.
type pageLocalReadState struct {
	mu                      sync.Mutex
	job                     *pageLocalReadJob
	closed                  bool
	entry, attempted, ready string // window thread only
	failure                 string
}

func (s *app) localPageReadKey() string {
	key := s.snapshot.Home + "\x00" + s.page
	if s.page == "settings" {
		key += "\x00" + s.settingsTab + "/" + s.settingsPage
	}
	return key
}

func (s *app) observePageLocalRead() {
	if s.loading || s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return
	}
	if s.pageLocalRead == nil {
		s.pageLocalRead = &pageLocalReadState{}
	}
	state := s.pageLocalRead
	key := s.localPageReadKey()
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return
	}
	if state.entry != key {
		state.entry, state.attempted, state.ready = key, "", ""
		state.failure = ""
		s.settingsLoaded = ""
		if state.job != nil {
			state.job.cancel()
		}
	}
	if state.job != nil || state.attempted == key {
		state.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &pageLocalReadJob{key: key, home: s.snapshot.Home, config: s.configJSON, cancel: cancel, done: make(chan struct{})}
	state.job = job
	// Mark this entry attempted even on failure; explicit refresh and re-entry
	// remain available, without a failed read restarting on every frame.
	state.attempted = key
	state.mu.Unlock()
	backend, win := s.backend, s.win
	go func() {
		defer close(job.done)
		snapshot, err := backend.Snapshot(ctx)
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			state.mu.Lock()
			current := state.job == job && !state.closed
			if state.job == job {
				state.job = nil
			}
			state.mu.Unlock()
			if !current || ctx.Err() != nil || s.win != win || s.snapshot.Home != job.home || s.localPageReadKey() != job.key {
				return
			}
			if err != nil {
				state.failure = "读取本机配置失败：" + err.Error()
				if s.configJSON != "" {
					state.ready = job.key
				}
				return
			}
			if strings.TrimSpace(snapshot.ConfigJSON) == "" {
				if snapshot.Error != "" {
					state.failure = snapshot.Error
				}
				if s.configJSON != "" {
					state.ready = job.key
				}
				return
			}
			// A newer save already supplied current configuration. Do not let
			// an older navigation read replace it or its task projection.
			if s.configJSON == job.config && snapshot.Home == job.home {
				s.applySnapshot(snapshot)
			}
			state.ready = job.key
		})
	}()
}

func (s *app) retryPageLocalRead() {
	if state := s.pageLocalRead; state != nil {
		state.attempted, state.ready, state.failure = "", "", ""
		s.settingsLoaded = ""
	}
}

func (s *app) pageLocalReadReady() bool {
	state := s.pageLocalRead
	if state == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.job == nil && state.ready == s.localPageReadKey()
}

func (s *app) pageLocalReadPending() bool {
	state := s.pageLocalRead
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.job != nil || state.attempted != s.localPageReadKey()
}

func (s *app) stopPageLocalRead() {
	if state := s.pageLocalRead; state != nil {
		state.mu.Lock()
		state.closed = true
		if state.job != nil {
			state.job.cancel()
		}
		state.mu.Unlock()
	}
}

func (s *app) closePageLocalRead(ctx context.Context) error {
	s.stopPageLocalRead()
	state := s.pageLocalRead
	if state == nil {
		return nil
	}
	state.mu.Lock()
	job := state.job
	state.mu.Unlock()
	if job == nil {
		return nil
	}
	select {
	case <-job.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *app) ensureSettingsPageData() {
	if s.busy || !s.pageLocalReadReady() {
		return
	}
	key := s.snapshot.Home + "\x00" + s.settingsTab + "/" + s.settingsPage
	if s.settingsLoaded == key {
		return
	}
	s.settingsLoaded, s.settingsOutput = key, ""
	switch s.settingsPage {
	case "config-backup":
		s.act("config.backups", "", "", "", "", "", nil)
	case "agents":
		s.loadSettingsAgentInstructions()
	case "instruction-templates":
		s.requestSettingsInstructionTemplates()
	case "codex-plugins":
		s.requestSettingsCLIStatus("codex", false)
		s.act("codex.extensions/snapshot", "", "", "", "", "", nil)
	case "codex-mcp":
		s.requestSettingsCLIStatus("codex", false)
		s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
	case "cli-meegle":
		s.requestSettingsCLIStatus("meegle", false)
	case "task-creation":
		s.requestSettingsCLIStatus("codex", false)
	case "skill-sources":
		s.act("skills.sources/catalog", "", "", "", "", "", nil)
	case "logs":
		s.act("logs.list", "", "", "", "", "", map[string]any{"filter": "recent"})
	}
}

func (s *app) requestSettingsInstructionTemplates() {
	state := s.ensureTaskNotesUI()
	state.settingsTemplatesRequestGeneration++
	state.settingsTemplatesLoading = true
	state.settingsTemplatesError = ""
	s.act("instructions.templates.list", "", "", "", "", "", map[string]any{
		"settingsTemplatesGeneration": state.settingsTemplatesRequestGeneration,
		"settingsTemplatesHome":       s.snapshot.Home,
		"settingsTemplatesPage":       "instruction-templates",
	})
}
