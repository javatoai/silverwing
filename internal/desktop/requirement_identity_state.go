package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type requirementIdentityContext struct {
	home, executable, profile    string
	profileModified, profileSize int64
}

type requirementIdentityListRequest struct {
	sprintKey string
	refresh   bool
}

type requirementIdentityJob struct {
	generation uint64
	context    requirementIdentityContext
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

// Context, confirmed identity, pending list and page fields belong to the window
// thread. Workers share only the running/closed fields protected by mu.
type requirementIdentityJobState struct {
	generation            uint64
	context               requirementIdentityContext
	hasContext            bool
	confirmed             bool
	identity, state, page string
	pending               *requirementIdentityListRequest
	current               *requirementIdentityJob
	mu                    sync.Mutex
	running               map[*requirementIdentityJob]struct{}
	closed                bool
}

func (s *app) ensureRequirementIdentityJobs() *requirementIdentityJobState {
	if s.requirementIdentityJobs == nil {
		s.requirementIdentityJobs = &requirementIdentityJobState{running: make(map[*requirementIdentityJob]struct{})}
	}
	return s.requirementIdentityJobs
}

// Like Source's contextStamp, this reads only file metadata, never credentials.
// It is called on feature entry, explicit reads and focus, not every frame.
func (s *app) currentRequirementIdentityContext() requirementIdentityContext {
	result := requirementIdentityContext{home: s.snapshot.Home, executable: strings.TrimSpace(s.configString("meegleExecutablePath"))}
	if userHome, err := os.UserHomeDir(); err == nil {
		result.profile = filepath.Join(userHome, ".meegle", "config.json")
		if info, err := os.Stat(result.profile); err == nil && !info.IsDir() {
			result.profileModified, result.profileSize = info.ModTime().UnixNano(), info.Size()
		}
	}
	return result
}

func (s *app) resetRequirementIdentityContext(state *requirementIdentityJobState, next requirementIdentityContext) {
	if state.current != nil {
		state.current.cancel()
		state.current = nil
	}
	state.generation++
	state.context, state.hasContext = next, true
	state.confirmed, state.identity, state.state = false, "", ""
	state.pending = nil
	s.clearMeegleIdentityBoundViews()
}

func (s *app) invalidateRequirementIdentityContext() {
	state := s.requirementIdentityJobs
	if state == nil {
		return
	}
	if state.current != nil {
		state.current.cancel()
		state.current = nil
	}
	state.generation++
	state.confirmed, state.identity, state.state = false, "", ""
	state.hasContext = false
	state.pending = nil
}
