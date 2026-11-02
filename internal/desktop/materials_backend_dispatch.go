package desktop

import (
	"context"
	"strings"

	"silverwing-go/internal/application"
)

type materialsBackendLane struct {
	gate  chan struct{}
	users int
}

// The application's task lock intentionally rejects concurrent callers. Order
// this window's own materials requests before entering that lock, while keeping
// unrelated global work independent from durable draft writes.
func (s *app) dispatchMaterialsBackend(ctx context.Context, backend application.Backend, taskPath string, action application.Action) (application.ActionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	if strings.TrimSpace(taskPath) == "" {
		return backend.Dispatch(ctx, action)
	}
	key := normalizeIdentityPath(taskPath)
	s.materialsUnit.state.materialsBackendMu.Lock()
	if s.materialsUnit.state.materialsBackendLanes == nil {
		s.materialsUnit.state.materialsBackendLanes = make(map[string]*materialsBackendLane)
	}
	lane := s.materialsUnit.state.materialsBackendLanes[key]
	if lane == nil {
		lane = &materialsBackendLane{gate: make(chan struct{}, 1)}
		s.materialsUnit.state.materialsBackendLanes[key] = lane
	}
	lane.users++
	s.materialsUnit.state.materialsBackendMu.Unlock()
	defer func() {
		s.materialsUnit.state.materialsBackendMu.Lock()
		lane.users--
		if lane.users == 0 {
			delete(s.materialsUnit.state.materialsBackendLanes, key)
		}
		s.materialsUnit.state.materialsBackendMu.Unlock()
	}()
	select {
	case lane.gate <- struct{}{}:
		defer func() { <-lane.gate }()
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	return backend.Dispatch(ctx, action)
}

func (s *app) materialsMarkdownSaveInProgress() bool {
	return documentKindForPath(s.materialsUnit.state.documentPath) == "markdown" && s.materialsUnit.state.markdownAutosave != nil &&
		s.materialsUnit.state.markdownAutosave.Status(s.documentAutosaveKey(s.materialsUnit.state.documentPath)).Saving
}
