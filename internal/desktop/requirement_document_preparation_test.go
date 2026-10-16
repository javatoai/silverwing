package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
)

type preparationFixtureBackend struct {
	mu              sync.Mutex
	actions         []application.Action
	imageAttempts   int
	imageFailed     bool
	block           chan struct{}
	started         chan string
	reads           map[string]int
	active, maximum int
}

func (*preparationFixtureBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (backend *preparationFixtureBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	backend.mu.Lock()
	backend.actions = append(backend.actions, action)
	id, _ := action.Payload["id"].(string)
	first := false
	if action.Kind == "requirements.read" {
		if backend.reads == nil {
			backend.reads = map[string]int{}
		}
		first = backend.reads[id] == 0
		backend.reads[id]++
	}
	backend.active++
	if backend.active > backend.maximum {
		backend.maximum = backend.active
	}
	block, started := backend.block, backend.started
	backend.mu.Unlock()
	defer func() { backend.mu.Lock(); backend.active--; backend.mu.Unlock() }()
	if first && block != nil {
		started <- id
		select {
		case <-block:
		case <-ctx.Done():
			return application.ActionResult{}, ctx.Err()
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if action.Kind == "requirements.attachment" {
		backend.imageAttempts++
		if backend.imageFailed && backend.imageAttempts == 1 {
			return application.ActionResult{Data: application.RequirementResource{URL: "https://image.invalid/a.png", Error: "fixture image failure"}}, nil
		}
		return application.ActionResult{Data: application.RequirementResource{URL: "https://image.invalid/a.png", Path: "fixture/a.png"}}, nil
	}
	content := application.RequirementContent{Generation: "fixture-generation", MarkdownPath: "fixture/body.md", Complete: true}
	if backend.imageFailed {
		resource := application.RequirementResource{URL: "https://image.invalid/a.png"}
		if backend.imageAttempts > 1 {
			resource.Path = "fixture/a.png"
		} else if backend.imageAttempts == 1 {
			resource.Error = "fixture image failure"
		}
		content.Resources = []application.RequirementResource{resource}
		content.Complete = resource.Path != ""
	}
	return application.ActionResult{Data: content}, nil
}

func waitPreparationRun(t *testing.T, state *requirementDocumentPreparationState) {
	t.Helper()
	state.mu.Lock()
	done := state.current.done
	state.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not finish")
	}
}

func TestRequirementDocumentPreparationImageRetryPreservesBody(t *testing.T) {
	backend := &preparationFixtureBackend{imageFailed: true}
	state := &requirementDocumentPreparationState{}
	item := application.Requirement{ID: "1", URL: "fixture/requirement/1"}
	state.start(backend, []application.Requirement{item, item}, false)
	waitPreparationRun(t, state)
	if total, completed, failures, active := state.snapshot(); total != 1 || completed != 0 || failures != 1 || active {
		t.Fatalf("initial snapshot: %d/%d failures=%d active=%v", completed, total, failures, active)
	}
	state.retry(backend)
	waitPreparationRun(t, state)
	if total, completed, failures, active := state.snapshot(); total != 1 || completed != 1 || failures != 0 || active {
		t.Fatalf("retry snapshot: %d/%d failures=%d active=%v", completed, total, failures, active)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for _, action := range backend.actions {
		if action.Kind == "requirements.read" && action.Payload["force"] != false {
			t.Fatal("image retry refreshed the body")
		}
	}
	lastImage := backend.actions[len(backend.actions)-2]
	if lastImage.Kind != "requirements.attachment" || lastImage.Payload["retry"] != true || lastImage.Payload["generation"] != "fixture-generation" {
		t.Fatalf("image retry payload: %+v", lastImage)
	}
}

func TestRequirementDocumentPreparationConcurrencyPriorityAndClose(t *testing.T) {
	backend := &preparationFixtureBackend{block: make(chan struct{}), started: make(chan string, 4)}
	state := &requirementDocumentPreparationState{}
	items := []application.Requirement{{ID: "1", URL: "fixture/1"}, {ID: "2", URL: "fixture/2"}, {ID: "3", URL: "fixture/3"}, {ID: "4", URL: "fixture/4"}}
	state.start(backend, items, true)
	for range 2 {
		select {
		case <-backend.started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	state.mu.Lock()
	state.priority = requirementKey(items[3])
	state.mu.Unlock()
	backend.block <- struct{}{}
	select {
	case id := <-backend.started:
		if id != "4" {
			t.Fatalf("next item=%s, want selected item 4", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("next item did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := state.close(ctx); err != nil {
		t.Fatal(err)
	}
	if total, _, _, _ := state.snapshot(); total != 0 {
		t.Fatal("cancelled generation republished progress")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.maximum > 2 {
		t.Fatalf("maximum=%d", backend.maximum)
	}
	for _, action := range backend.actions {
		if action.Kind == "requirements.read" && backend.reads[action.Payload["id"].(string)] == 1 && action.Payload["force"] != true {
			t.Fatal("explicit refresh was not propagated")
		}
	}
}

func TestRequirementDocumentPreparationCancellationStopsBeforeRead(t *testing.T) {
	backend := &preparationFixtureBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := prepareRequirementDocument(ctx, backend, requirementDocumentPreparationWork{item: application.Requirement{ID: "1"}})
	if !errors.Is(err, context.Canceled) || len(backend.actions) != 0 {
		t.Fatalf("error=%v actions=%d", err, len(backend.actions))
	}
}
