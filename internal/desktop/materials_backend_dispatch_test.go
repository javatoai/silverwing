package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

type materialsLaneTestBackend struct {
	dispatch func(context.Context, application.Action) (application.ActionResult, error)
}

func (b materialsLaneTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b materialsLaneTestBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	return b.dispatch(ctx, action)
}

func waitMaterialsLaneUsers(t *testing.T, s *app, taskPath string, expected int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.materialsUnit.state.materialsBackendMu.Lock()
		users := 0
		if lane := s.materialsUnit.state.materialsBackendLanes[normalizeIdentityPath(taskPath)]; lane != nil {
			users = lane.users
		}
		s.materialsUnit.state.materialsBackendMu.Unlock()
		if users == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("materials lane did not reach %d users", expected)
}

func TestMaterialsBackendLaneSerializesTaskRequestsAndCancelsWaitingRead(t *testing.T) {
	s := &app{}
	taskPath := filepath.Join(t.TempDir(), "task")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan application.Action, 4)
	release := make(chan struct{})
	backend := materialsLaneTestBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		entered <- action
		if action.Kind == "document.save" {
			select {
			case <-release:
			case <-ctx.Done():
				return application.ActionResult{}, ctx.Err()
			}
		}
		return application.ActionResult{Text: action.Text}, nil
	}}
	saved := make(chan error, 1)
	go func() {
		_, err := s.dispatchMaterialsBackend(ctx, backend, taskPath, application.Action{Kind: "document.save", TaskID: "task"})
		saved <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	readCtx, cancelRead := context.WithCancel(ctx)
	canceled := make(chan error, 1)
	go func() {
		_, err := s.dispatchMaterialsBackend(readCtx, backend, filepath.Join(taskPath, "."), application.Action{Kind: "document.read", TaskID: "task", Text: "canceled"})
		canceled <- err
	}()
	waitMaterialsLaneUsers(t, s, taskPath, 2)
	cancelRead()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting read returned %v", err)
	}
	waitMaterialsLaneUsers(t, s, taskPath, 1)

	read := make(chan error, 1)
	go func() {
		_, err := s.dispatchMaterialsBackend(ctx, backend, taskPath, application.Action{Kind: "document.read", TaskID: "task", Text: "after save"})
		read <- err
	}()
	waitMaterialsLaneUsers(t, s, taskPath, 2)
	if _, err := s.dispatchMaterialsBackend(ctx, backend, taskPath+"-other", application.Action{Kind: "document.list", TaskID: "other"}); err != nil {
		t.Fatal(err)
	}
	if action := <-entered; action.Kind != "document.list" || action.TaskID != "other" {
		t.Fatalf("same-task read bypassed its save: %#v", action)
	}
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	if action := <-entered; action.Kind != "document.read" || action.Text != "after save" {
		t.Fatalf("canceled request reached the backend: %#v", action)
	}
	select {
	case action := <-entered:
		t.Fatalf("unexpected backend request: %#v", action)
	default:
	}
	waitMaterialsLaneUsers(t, s, taskPath, 0)
	s.materialsUnit.state.materialsBackendMu.Lock()
	defer s.materialsUnit.state.materialsBackendMu.Unlock()
	if len(s.materialsUnit.state.materialsBackendLanes) != 0 {
		t.Fatalf("finished requests retained %d task lanes", len(s.materialsUnit.state.materialsBackendLanes))
	}
}

func TestMaterialsDraftLanePreservesCacheWhileGlobalBackendBusy(t *testing.T) {
	for _, name := range []string{"doc.md", "doc.txt"} {
		t.Run(name, func(t *testing.T) {
			s, backend, files := materialTextEditorFixture(t, map[string]string{name: "original\r\n"})
			content := files[name]
			if content.Kind == "markdown" {
				action, result := writableMarkdownReadFixture(s, content)
				s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
				s.applyMaterialsActionResult(action, result)
			} else {
				bindMaterialTextTest(s, content)
			}
			key := s.documentAutosaveKey(content.Path)
			s.backendGate <- struct{}{}
			defer func() { <-s.backendGate }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			const draftText = "未落盘的草稿\r\n"
			if err := s.writeMarkdownDraft(ctx, key, draftText, false); err != nil {
				t.Fatalf("global work prevented the durable draft: %v", err)
			}
			identity, err := documents.NewMarkdownDraftIdentity("task", backend.taskPath, backend.root, content.Path)
			if err != nil {
				t.Fatal(err)
			}
			if content.Kind == "markdown" {
				draft, err := backend.documents.ReadMarkdownDraft(ctx, identity)
				if err != nil || draft == nil || draft.Content != draftText {
					t.Fatalf("Markdown draft missing: %#v, %v", draft, err)
				}
			} else {
				draft, err := backend.documents.ReadMaterialTextDraft(ctx, identity)
				if err != nil || draft == nil || draft.Content != draftText {
					t.Fatalf("text draft missing: %#v, %v", draft, err)
				}
			}
			if err := s.writeMarkdownDraft(ctx, key, "", true); err != nil {
				t.Fatalf("global work prevented explicit draft discard: %v", err)
			}
		})
	}
}
