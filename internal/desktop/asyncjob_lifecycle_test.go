package desktop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/process"
)

type asyncLifecycleBackend struct {
	dispatch func(context.Context, application.Action) (application.ActionResult, error)
}

func (b *asyncLifecycleBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	return b.dispatch(ctx, action)
}

func (*asyncLifecycleBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func asyncLifecycleGate(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return gate, release
}

func awaitAsyncLifecycle(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("asynchronous backend did not reach its cleanup point")
	}
}

func awaitAsyncLifecycleError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("asynchronous backend did not return")
		return nil
	}
}

func newAsyncLifecycleApp(t *testing.T, backend application.Backend) *app {
	t.Helper()
	return &app{
		backend:  backend,
		page:     "settings",
		snapshot: application.Snapshot{Home: t.TempDir()},
		settingsUnit: settingsController{state: settingsState{
			configJSON:          `{"toolVisibility":{"killPort":true}}`,
			settingsPage:        "cli-codex",
			settingsCLIStatus:   make(map[string]map[string]any),
			settingsCLISessions: make(map[string]map[string]any),
		}},
	}
}

func beginAsyncLifecycleCLI(t *testing.T, s *app, kind string) *cliJobRequest {
	t.Helper()
	request, err := s.beginCLIJob(application.Action{Kind: kind, Payload: map[string]any{"tool": "codex", "sessionId": "fixture-session"}})
	if err != nil || request == nil {
		t.Fatalf("begin %s: request=%v err=%v", kind, request, err)
	}
	t.Cleanup(request.cancel)
	return request
}

func runAsyncLifecycleCLI(backend application.Backend, request *cliJobRequest) <-chan error {
	result := make(chan error, 1)
	go func() {
		_, _, err := executeCLIJob(backend, request)
		request.complete(err)
		result <- err
	}()
	return result
}

func TestAsyncJobToolShutdownPreservesPortStopUntilBackendResult(t *testing.T) {
	gate, release := asyncLifecycleGate(t)
	started := make(chan struct{}, 2)
	backend := &asyncLifecycleBackend{dispatch: func(context.Context, application.Action) (application.ActionResult, error) {
		started <- struct{}{}
		<-gate // A process-stop result must be observed even after shutdown starts.
		return application.ActionResult{}, nil
	}}
	s := newAsyncLifecycleApp(t, backend)
	s.page = "tools"
	state := s.ensureToolState()
	state.activeTab = "端口"
	action := application.Action{Kind: "tool.port-stop", Payload: map[string]any{
		"pid": 41, "name": "fixture", "path": "fixture", "startedAt": "fixture-start", "confirmed": true, "stopRequest": uint64(9),
	}}
	if !s.dispatchDeveloperToolJob(action) {
		t.Fatal("port-stop did not use the independent worker")
	}
	awaitAsyncLifecycle(t, started)
	request := s.toolsUnit.state.toolJobs.current[action.Kind]
	if request == nil || !request.preserveHide || !request.preserveClose {
		t.Fatal("port-stop lost its hide or shutdown policy")
	}
	s.cancelDeveloperToolJob(action.Kind)
	duplicate := cloneDeveloperToolAction(action)
	duplicate.Payload["stopRequest"] = uint64(10)
	s.dispatchDeveloperToolJob(duplicate)
	if request.ctx.Err() != nil || s.toolsUnit.state.toolJobs.current[action.Kind] != request {
		t.Fatal("hiding or a newer request canceled the active port mutation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.toolsUnit.closeDeveloperToolJobs(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown claimed the backend had finished: %v", err)
	}
	if request.ctx.Err() != nil {
		t.Fatal("shutdown canceled a port-stop before its real backend result")
	}
	release()
	awaitAsyncLifecycle(t, request.lease.Done())
	if err := s.toolsUnit.closeDeveloperToolJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("a second port-stop reached the backend")
	default:
	}
}

func TestAsyncJobToolLateResultCannotChangeReplacement(t *testing.T) {
	gate, release := asyncLifecycleGate(t)
	started := make(chan struct{}, 2)
	backend := &asyncLifecycleBackend{dispatch: func(context.Context, application.Action) (application.ActionResult, error) {
		started <- struct{}{}
		<-gate
		return application.ActionResult{}, nil
	}}
	s := newAsyncLifecycleApp(t, backend)
	s.page = "tools"
	state := s.ensureToolState()
	state.activeTab, state.jsonSource, state.jsonPending = "JSON", `{"old":true}`, true
	oldAction := application.Action{Kind: "tool.json", Text: state.jsonSource, Payload: map[string]any{"revision": state.jsonRevision, "operation": "format"}}
	s.dispatchDeveloperToolJob(oldAction)
	awaitAsyncLifecycle(t, started)
	old := s.toolsUnit.state.toolJobs.current[oldAction.Kind]
	state.jsonRevision++
	state.jsonSource = `{"new":true}`
	newAction := application.Action{Kind: "tool.json", Text: state.jsonSource, Payload: map[string]any{"revision": state.jsonRevision, "operation": "format"}}
	s.dispatchDeveloperToolJob(newAction)
	awaitAsyncLifecycle(t, started)
	current := s.toolsUnit.state.toolJobs.current[newAction.Kind]
	s.finishDeveloperToolJob(old, application.ActionResult{Text: "stale-result"}, nil)
	if old.ctx.Err() == nil || current == old || s.toolsUnit.state.toolJobs.current[newAction.Kind] != current || state.jsonSource != newAction.Text || !state.jsonPending {
		t.Fatal("a late tool result changed the new request or its input state")
	}
	release()
	awaitAsyncLifecycle(t, old.lease.Done())
	awaitAsyncLifecycle(t, current.lease.Done())
	if err := s.toolsUnit.closeDeveloperToolJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncJobToolShutdownRetainsBackendCleanupFailure(t *testing.T) {
	gate, release := asyncLifecycleGate(t)
	started := make(chan struct{}, 1)
	cleanupCause := errors.New("fixture tool process still alive")
	backend := &asyncLifecycleBackend{dispatch: func(context.Context, application.Action) (application.ActionResult, error) {
		started <- struct{}{}
		<-gate
		return application.ActionResult{}, &process.CleanupError{Cause: cleanupCause}
	}}
	s := newAsyncLifecycleApp(t, backend)
	s.page = "tools"
	state := s.ensureToolState()
	state.activeTab = "JSON"
	action := application.Action{Kind: "tool.json", Text: state.jsonSource, Payload: map[string]any{"revision": state.jsonRevision, "operation": "format"}}
	s.dispatchDeveloperToolJob(action)
	awaitAsyncLifecycle(t, started)
	request := s.toolsUnit.state.toolJobs.current[action.Kind]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.toolsUnit.closeDeveloperToolJobs(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not await tool process cleanup: %v", err)
	}
	release()
	awaitAsyncLifecycle(t, request.lease.Done())
	if err := s.toolsUnit.closeDeveloperToolJobs(context.Background()); !process.IsCleanupFailure(err) || !errors.Is(err, cleanupCause) {
		t.Fatalf("shutdown lost the backend cleanup failure after its window closed: %v", err)
	}
}

func TestAsyncJobCLIAuthCleanupBlocksCloseAfterInvalidation(t *testing.T) {
	gate, release := asyncLifecycleGate(t)
	started := make(chan struct{}, 1)
	cleanupCause := errors.New("fixture child process still alive")
	cleanupErr := &process.CleanupError{Phase: "fixture", Cause: cleanupCause}
	backend := &asyncLifecycleBackend{dispatch: func(context.Context, application.Action) (application.ActionResult, error) {
		started <- struct{}{}
		<-gate
		return application.ActionResult{}, cleanupErr
	}}
	s := newAsyncLifecycleApp(t, backend)
	request := beginAsyncLifecycleCLI(t, s, "cli.auth.start")
	result := runAsyncLifecycleCLI(backend, request)
	awaitAsyncLifecycle(t, started)
	s.invalidateCLIJobs("codex")
	if !s.documentCloseOperationBusy() || request.ctx.Err() == nil {
		t.Fatal("invalidating the UI request released the login cleanup boundary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.closeCLIJobs(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not wait for the retired backend: %v", err)
	}
	if request, err := s.beginCLIJob(application.Action{Kind: "cli.status", Payload: map[string]any{"tool": "codex"}}); err == nil || request != nil {
		t.Fatal("closed CLI lifecycle accepted another request")
	}
	release()
	if err := awaitAsyncLifecycleError(t, result); !process.IsCleanupFailure(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("worker discarded cleanup or cancellation evidence: %v", err)
	}
	if s.documentCloseOperationBusy() {
		t.Fatal("completed login cleanup still blocks closing")
	}
	if err := s.closeCLIJobs(context.Background()); !process.IsCleanupFailure(err) || !errors.Is(err, cleanupCause) {
		t.Fatalf("close discarded a cleanup failure after the UI request was removed: %v", err)
	}
}

func TestAsyncJobCLICancelRetiresPollAfterBackendMutation(t *testing.T) {
	gate, release := asyncLifecycleGate(t)
	started := make(chan struct{}, 1)
	mutationSawLivePoll := make(chan bool, 1)
	cleanupCause := errors.New("fixture poll cleanup failed")
	var poll *cliJobRequest
	backend := &asyncLifecycleBackend{dispatch: func(_ context.Context, action application.Action) (application.ActionResult, error) {
		if action.Kind == "cli.auth.poll" {
			started <- struct{}{}
			<-gate
			return application.ActionResult{}, &process.CleanupError{Cause: cleanupCause}
		}
		mutationSawLivePoll <- poll.ctx.Err() == nil
		return application.ActionResult{}, nil
	}}
	s := newAsyncLifecycleApp(t, backend)
	poll = beginAsyncLifecycleCLI(t, s, "cli.auth.poll")
	pollResult := runAsyncLifecycleCLI(backend, poll)
	awaitAsyncLifecycle(t, started)
	request := beginAsyncLifecycleCLI(t, s, "cli.auth.cancel")
	if request.retiring != poll || poll.ctx.Err() != nil {
		t.Fatal("registering cancel terminated the poll before the backend mutation")
	}
	cancelResult := runAsyncLifecycleCLI(backend, request)
	select {
	case live := <-mutationSawLivePoll:
		if !live {
			t.Fatal("poll was canceled before the backend recorded the cancel mutation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel mutation did not reach the backend")
	}
	awaitAsyncLifecycle(t, poll.ctx.Done())
	select {
	case <-request.done:
		t.Fatal("cancel completed before the poll backend released cleanup")
	default:
	}
	release()
	if err := awaitAsyncLifecycleError(t, pollResult); !process.IsCleanupFailure(err) {
		t.Fatal(err)
	}
	if err := awaitAsyncLifecycleError(t, cancelResult); !errors.Is(err, cleanupCause) {
		t.Fatalf("cancel lost the retiring poll cleanup error: %v", err)
	}
	if err := s.closeCLIJobs(context.Background()); !errors.Is(err, cleanupCause) {
		t.Fatalf("close lost the poll cleanup error: %v", err)
	}
}

func TestAsyncJobCLILateResultCannotEnterNewOwner(t *testing.T) {
	s := newAsyncLifecycleApp(t, nil)
	old := beginAsyncLifecycleCLI(t, s, "cli.status")
	oldOwner := old.owner
	s.toolsUnit.state.cliJobs = nil
	current := beginAsyncLifecycleCLI(t, s, "cli.status")
	s.settingsUnit.state.settingsOutput = "current-output"
	cleanupCause := errors.New("old-owner cleanup")
	old.complete(&process.CleanupError{Cause: cleanupCause})
	s.finishCLIJob(old, application.ActionResult{Message: "late-output"}, application.Snapshot{}, old.lease.Err())
	if s.toolsUnit.state.cliJobs.requests[current.key] != current || s.settingsUnit.state.settingsOutput != "current-output" {
		t.Fatal("old owner changed the new request or its presentation")
	}
	current.complete(nil)
	if err := s.closeCLIJobs(context.Background()); err != nil {
		t.Fatalf("old cleanup error contaminated the new owner: %v", err)
	}
	oldOwner.lifecycle.Close()
	if err := oldOwner.lifecycle.Wait(context.Background()); !errors.Is(err, cleanupCause) {
		t.Fatalf("old cleanup error was not retained by its original owner: %v", err)
	}
}

func TestAsyncJobCLILateCatalogResultKeepsReplacementLoading(t *testing.T) {
	s := newAsyncLifecycleApp(t, nil)
	s.settingsUnit.state.settingsCLIStatus["meegle"] = map[string]any{"authenticated": true, "host": "fixture"}
	identity, _ := s.currentMeegleProjectCatalogIdentity()
	catalog := &meegleProjectCatalogState{loading: true, generation: 7, identity: identity, context: s.currentRequirementIdentityContext()}
	s.settingsUnit.state.meegleProjectCatalog = catalog
	action := application.Action{Kind: "meegle.projects", Payload: map[string]any{"catalogGeneration": "7"}}
	old, err := s.beginCLIJob(action)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.beginCLIJob(action)
	if err != nil {
		t.Fatal(err)
	}
	s.finishCLIJob(old, application.ActionResult{}, application.Snapshot{}, nil)
	if !catalog.loading || catalog.error != "" || s.toolsUnit.state.cliJobs.requests[current.key] != current {
		t.Fatal("superseded catalog result cleared the replacement's loading state")
	}
	old.cancel()
	old.complete(nil)
	current.cancel()
	current.complete(nil)
	if err := s.closeCLIJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncJobCLIRetiringTimeoutPreservesBackendError(t *testing.T) {
	s := newAsyncLifecycleApp(t, nil)
	poll := beginAsyncLifecycleCLI(t, s, "cli.auth.poll")
	request := beginAsyncLifecycleCLI(t, s, "cli.auth.cancel")
	backendErr := errors.New("backend cancel mutation failed")
	backend := &asyncLifecycleBackend{dispatch: func(context.Context, application.Action) (application.ActionResult, error) {
		request.cancel() // The cleanup waiter is canceled while the old worker remains.
		return application.ActionResult{}, backendErr
	}}
	_, _, err := executeCLIJob(backend, request)
	request.complete(err)
	if !errors.Is(err, backendErr) || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "进程清理尚未完成") {
		t.Fatalf("retiring wait discarded its context or the backend error: %v", err)
	}
	poll.complete(nil)
	if err := s.closeCLIJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
}
