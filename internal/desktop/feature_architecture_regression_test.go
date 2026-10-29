package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/workspace"
)

// These fakes observe application calls and UI outcomes without a native
// window, processes, or user files. Dispatch remains deliberately available so
// a disconnected typed capability cannot accidentally pass by falling back.
type featureArchitectureBackend struct {
	mu              sync.Mutex
	snapshot        application.Snapshot
	actions         []application.Action
	nativeCalls     []string
	snapshotReached chan struct{}
	createRequest   application.TaskCreateRequest
	archiveRequest  application.TaskArchiveRequest
	listRequest     application.RequirementListRequest
	listError       error
	draftError      error
}

func (b *featureArchitectureBackend) Snapshot(context.Context) (application.Snapshot, error) {
	b.mu.Lock()
	snapshot := b.snapshot
	snapshot.Tasks = append([]application.Task(nil), snapshot.Tasks...)
	b.mu.Unlock()
	if b.snapshotReached != nil {
		b.snapshotReached <- struct{}{}
	}
	return snapshot, nil
}

func (b *featureArchitectureBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.mu.Lock()
	b.actions = append(b.actions, action)
	draftError := b.draftError
	b.mu.Unlock()
	switch action.Kind {
	case "document.draft":
		return application.ActionResult{}, draftError
	case "document.draft-discard":
		return application.ActionResult{}, nil
	case "document.save":
		return application.ActionResult{Data: documents.Content{Path: action.Path, Text: action.Text, Hash: documents.Hash([]byte(action.Text))}}, nil
	default:
		return application.ActionResult{}, fmt.Errorf("unexpected legacy dispatch: %s", action.Kind)
	}
}

func (b *featureArchitectureBackend) calls() ([]string, []application.Action) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.nativeCalls...), append([]application.Action(nil), b.actions...)
}

func (b *featureArchitectureBackend) CreateTask(_ context.Context, request application.TaskCreateRequest, progress func(workspace.TaskCreationProgress)) (application.OperationResult[domain.TaskManifest], error) {
	b.mu.Lock()
	b.nativeCalls = append(b.nativeCalls, "task.create")
	b.createRequest = request
	b.mu.Unlock()
	if progress != nil {
		progress(workspace.TaskCreationProgress{Sequence: 1, Message: "created by typed capability"})
	}
	return application.OperationResult[domain.TaskManifest]{Message: "native create", Data: domain.TaskManifest{TaskDirectoryName: request.FolderName}}, nil
}

func (b *featureArchitectureBackend) DispatchTaskCreation(context.Context, application.Action, func(workspace.TaskCreationProgress)) (application.ActionResult, error) {
	b.mu.Lock()
	b.nativeCalls = append(b.nativeCalls, "legacy-task-progress")
	b.mu.Unlock()
	return application.ActionResult{}, errors.New("legacy progress must not run")
}

func (b *featureArchitectureBackend) ArchiveTask(_ context.Context, request application.TaskArchiveRequest) (application.OperationResult[domain.TaskManifest], error) {
	b.mu.Lock()
	b.nativeCalls = append(b.nativeCalls, "task.archive")
	b.archiveRequest = request
	b.mu.Unlock()
	return application.OperationResult[domain.TaskManifest]{Message: "native archive"}, nil
}

func (b *featureArchitectureBackend) RestoreTask(_ context.Context, target application.TaskTarget) (application.OperationResult[domain.TaskManifest], error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nativeCalls = append(b.nativeCalls, "task.restore")
	for index := range b.snapshot.Tasks {
		if b.snapshot.Tasks[index].ID == target.TaskID {
			b.snapshot.Tasks[index].Archived = false
		}
	}
	return application.OperationResult[domain.TaskManifest]{Message: "native restore", Data: domain.TaskManifest{TaskDirectoryName: target.TaskID}}, nil
}

func (b *featureArchitectureBackend) DeleteTask(context.Context, application.TaskDeleteRequest) (application.OperationResult[struct{}], error) {
	return application.OperationResult[struct{}]{}, errors.New("delete is outside this fixture")
}

func (b *featureArchitectureBackend) ReadRequirementList(_ context.Context, request application.RequirementListRequest, progress func(application.RequirementListProgress)) (application.OperationResult[application.RequirementList], error) {
	b.mu.Lock()
	b.nativeCalls = append(b.nativeCalls, "requirements.list")
	b.listRequest = request
	b.mu.Unlock()
	if progress != nil {
		progress(application.RequirementListProgress{Sequence: 1, Completed: 1, Total: 2})
		progress(application.RequirementListProgress{Sequence: 2, Completed: 2, Total: 2})
	}
	return application.OperationResult[application.RequirementList]{
		Message: "native list", Data: application.RequirementList{Items: []application.Requirement{{ID: "typed-123", Title: "typed result"}}, CompletedQueries: 2},
	}, b.listError
}

func (b *featureArchitectureBackend) ReadRequirement(_ context.Context, request application.RequirementReadRequest) (application.OperationResult[application.RequirementContent], error) {
	b.mu.Lock()
	b.nativeCalls = append(b.nativeCalls, "requirements.read")
	b.mu.Unlock()
	return application.OperationResult[application.RequirementContent]{Data: application.RequirementContent{URL: request.URL, Body: "late typed body", Complete: true}}, nil
}

func (b *featureArchitectureBackend) ReadRequirementComments(context.Context, application.RequirementReadRequest) (application.OperationResult[application.RequirementComments], error) {
	return application.OperationResult[application.RequirementComments]{}, errors.New("comments are outside this fixture")
}

func (b *featureArchitectureBackend) DispatchRequirementList(context.Context, application.Action, func(application.RequirementListProgress)) (application.ActionResult, error) {
	b.mu.Lock()
	b.nativeCalls = append(b.nativeCalls, "legacy-requirement-progress")
	b.mu.Unlock()
	return application.ActionResult{}, errors.New("legacy progress must not run")
}

func featureArchitectureFixture(t *testing.T) (*app, *featureArchitectureBackend) {
	t.Helper()
	base := t.TempDir()
	config, err := json.Marshal(map[string]any{
		"taskRoot": filepath.Join(base, "tasks"), "requirementMaterialsSubdirectory": "legacy-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := application.Snapshot{Home: filepath.Join(base, "home"), ConfigJSON: string(config)}
	for index, id := range []string{"task-a", "task-b"} {
		root := filepath.Join(base, "materials", "2026", fmt.Sprintf("%d-%s", index+100, id))
		snapshot.Tasks = append(snapshot.Tasks, application.Task{
			ID: id, Title: id, Path: filepath.Join(base, "tasks", id), CreatedAt: id + "-created",
			MaterialsPath: root, MaterialsStatus: "READY", Materials: documents.DescribeMaterials(filepath.Join(root, "snapshot-owner")),
		})
	}
	backend := &featureArchitectureBackend{snapshot: snapshot}
	s := newApp(backend, Options{Page: "tasks", Tab: 1, CapturePath: "feature-architecture-regression"})
	s.loading = false
	s.applySnapshot(snapshot)
	s.taskUnit.state.taskContextUpdate = func(update func()) { update() }
	s.materialsUnit.state.markdownAutosave.SetDebounce(time.Hour)
	s.readingSession = newMemoryReadingSession(snapshot.Home)
	t.Cleanup(func() {
		if s.cancelOperation != nil {
			s.cancelOperation()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		failures := s.materialsUnit.state.markdownAutosave.Close(ctx)
		if err := ctx.Err(); err != nil {
			t.Errorf("fixture autosave close timed out: %v", err)
		}
		// A dirty document may close once its latest source is durable in the
		// draft cache. Closing does not implicitly save its document body.
		if failures := s.materialsUnit.documentClosePendingFailures(failures); len(failures) > 0 {
			t.Errorf("fixture autosave did not settle: %v", failures)
		}
	})
	return s, backend
}

func featureArchitectureBindText(t *testing.T, s *app) *materialTextEditorState {
	t.Helper()
	current := s.taskUnit.currentTask()
	path := filepath.Join(current.materials.OwnedRoot, "draft.txt")
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentKind = path, "text"
	s.materialsUnit.state.documentTabs = []string{path}
	s.materialsUnit.state.documentsLoaded = true
	s.seedMaterialTextEditor(current.id, path, documents.Content{Path: path, Kind: "text", Text: "old text", Hash: documents.Hash([]byte("old text"))})
	state := s.currentMaterialTextEditor()
	if state == nil || state.view == nil {
		t.Fatal("text editor was not bound to the selected task")
	}
	s.materialsUnit.state.materialTextActive = state
	return state
}

func TestFeatureArchitectureTaskWorkerPrefersNativeTypedCapability(t *testing.T) {
	for _, kind := range []string{"task.create", "task.archive", "task.restore"} {
		t.Run(kind, func(t *testing.T) {
			s, backend := featureArchitectureFixture(t)
			backend.snapshotReached = make(chan struct{}, 1)
			current := s.taskUnit.currentTask()
			action := application.Action{Kind: kind, TaskID: current.id, Path: current.path,
				Payload: map[string]any{"title": "created-native", "workspaceToolIds": []string{}, "cleanup": true}}
			// The actual dispatch worker must select the typed port even though
			// both legacy Dispatch and legacy progress are available.
			s.dispatch(action)
			select {
			case <-backend.snapshotReached:
			case <-time.After(3 * time.Second):
				t.Fatalf("native task worker never reached snapshot refresh: %s", s.lastError)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := s.acquireBackend(ctx); err != nil {
				t.Fatal(err)
			}
			s.releaseBackend()
			calls, actions := backend.calls()
			if !reflect.DeepEqual(calls, []string{kind}) || len(actions) != 0 {
				t.Fatalf("worker used an extra/legacy operation: native=%v legacy=%v", calls, actions)
			}
			if kind == "task.create" && (backend.createRequest.FolderName != "created-native" || backend.createRequest.WorkspaceToolIDs == nil || len(*backend.createRequest.WorkspaceToolIDs) != 0) {
				t.Fatalf("creation lost explicit empty tools or the legacy title alias: %+v", backend.createRequest)
			}
			if kind == "task.archive" && (!backend.archiveRequest.Cleanup || backend.archiveRequest.Target.TaskID != current.id || backend.archiveRequest.Target.Path != current.path) {
				t.Fatalf("archive request lost its target/cleanup: %+v", backend.archiveRequest)
			}
		})
	}
}

func TestFeatureArchitectureRequirementWorkerPreservesTypedProgressAndErrors(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed-%t", failed), func(t *testing.T) {
			backend := &featureArchitectureBackend{}
			failure := errors.New("partial native failure")
			if failed {
				backend.listError = failure
			}
			var progress []uint64
			request := &requirementListJob{ctx: context.Background(),
				action:     application.Action{Kind: "requirements.refresh", Payload: map[string]any{"sprintKey": "sprint-native", "expectedIdentity": "account-native", "force": true}},
				onProgress: func(event application.RequirementListProgress) { progress = append(progress, event.Sequence) },
			}
			result, err := executeRequirementListJob(backend, request)
			if (failed && !errors.Is(err, failure)) || (!failed && err != nil) {
				t.Fatalf("typed error was lost: %v", err)
			}
			data, decodeErr := application.ResultData[application.RequirementList](result)
			if decodeErr != nil || len(data.Items) != 1 || data.Items[0].ID != "typed-123" || data.CompletedQueries != 2 || result.Message != "native list" {
				t.Fatalf("typed result was lost: %+v %v", result, decodeErr)
			}
			calls, actions := backend.calls()
			if !reflect.DeepEqual(calls, []string{"requirements.list"}) || len(actions) != 0 || !reflect.DeepEqual(progress, []uint64{1, 2}) {
				t.Fatalf("typed progress/fallback changed: calls=%v legacy=%v progress=%v", calls, actions, progress)
			}
			if got := backend.listRequest; !got.Refresh || !got.Force || got.SprintKey != "sprint-native" || got.ExpectedIdentity != "account-native" {
				t.Fatalf("requirement identity or refresh intent was lost: %+v", got)
			}
		})
	}
}

func TestFeatureArchitectureTaskSwitchCapturesOldDraftAndReadingOwner(t *testing.T) {
	s, backend := featureArchitectureFixture(t)
	previous := s.taskUnit.currentTask()
	text := featureArchitectureBindText(t, s)
	text.view.Buffer.Edit(codeeditor.Edit{Start: 0, End: len("old text"), Text: "unsaved old task"})
	oldPath := text.path
	if !s.selectTaskIndex(1) {
		t.Fatalf("task switch failed: %s", s.lastError)
	}
	if s.taskUnit.currentTask().id != "task-b" || s.materialsUnit.state.documentPath != "" {
		t.Fatal("task switch did not reset the visible materials after selecting task B")
	}
	if source, ok := s.materialsUnit.state.markdownAutosave.Content(text.key); !ok || source != "unsaved old task" {
		t.Fatalf("switch lost the native input before reset: %q present=%t", source, ok)
	}
	record := readingMaterialsFor(s.readingSession, previous.path, previous.materialsPath, false)
	relative := filepath.ToSlash(filepath.Join("snapshot-owner", "draft.txt"))
	if record == nil || record.SelectedPath == nil || *record.SelectedPath != relative || len(record.DocumentTabs.Tabs) != 1 || record.DocumentTabs.Tabs[0].Path != relative {
		t.Fatalf("old reading state was captured after selection changed: %+v", record)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if failures := s.materialsUnit.state.markdownAutosave.Flush(ctx); len(failures) > 0 {
		t.Fatal(failures)
	}
	_, actions := backend.calls()
	var saves []application.Action
	for _, action := range actions {
		if action.Kind == "document.save" {
			saves = append(saves, action)
		}
	}
	if len(saves) != 1 || saves[0].TaskID != previous.id || saves[0].Path != oldPath || saves[0].Text != "unsaved old task" {
		t.Fatalf("old draft saved under the new selection: %+v", saves)
	}
}

func TestFeatureArchitectureTypedRestoreKeepsCurrentDraft(t *testing.T) {
	s, backend := featureArchitectureFixture(t)
	backend.snapshot.Tasks[1].Archived = true
	s.applySnapshot(backend.snapshot)
	text := featureArchitectureBindText(t, s)
	text.view.Buffer.Edit(codeeditor.Edit{Start: 0, End: len("old text"), Text: "keep current draft"})
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	current, path := s.taskUnit.currentTask(), s.materialsUnit.state.documentPath
	action := application.Action{Kind: "task.restore", TaskID: "task-b", Path: backend.snapshot.Tasks[1].Path}
	result, err := executeTaskOperation(context.Background(), backend, action, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.applySnapshot(snapshot)
	if !s.applyTaskLifecycleSnapshotSelection(action, result) || s.taskUnit.currentTask().id != current.id || s.page != "tasks" || s.currentMaterialTextEditor() != text ||
		s.materialsUnit.state.documentPath != path || s.materialsUnit.state.documentContent != "keep current draft" || !s.materialsUnit.state.documentDirty {
		t.Fatal("restoring another task opened it or discarded the current editor/draft")
	}
	if source, _ := s.materialsUnit.state.markdownAutosave.Content(text.key); source != "keep current draft" {
		t.Fatalf("restore changed the pending draft: %q", source)
	}
	calls, actions := backend.calls()
	if !reflect.DeepEqual(calls, []string{"task.restore"}) {
		t.Fatalf("restore did not use its native capability: %v", calls)
	}
	for _, call := range actions {
		if call.Kind != "document.draft" && call.Kind != "document.draft-discard" {
			t.Fatalf("restore unexpectedly dispatched a legacy operation: %+v", call)
		}
	}
}

func TestFeatureArchitectureCloseDoesNotIgnoreUndurableDrafts(t *testing.T) {
	t.Run("cache-error-can-retry", func(t *testing.T) {
		s, backend := featureArchitectureFixture(t)
		backend.mu.Lock()
		backend.draftError = errors.New("fixture draft cache unavailable")
		backend.mu.Unlock()
		text := featureArchitectureBindText(t, s)
		text.view.Buffer.Edit(codeeditor.Edit{Start: 0, End: len("old text"), Text: "latest unsaved source"})
		if err := s.captureMaterialTextInput(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.materialsUnit.state.markdownAutosave.FlushDrafts(ctx); err == nil {
			t.Fatal("failed draft cache was acknowledged as durable")
		}
		status := s.materialsUnit.state.markdownAutosave.Status(text.key)
		remaining := s.materialsUnit.documentClosePendingFailures(s.materialsUnit.state.markdownAutosave.PendingFailures())
		if status.CacheError == "" || status.DraftSavedRevision >= status.Revision || len(remaining) != 1 || !strings.Contains(remaining[0].Message, "fixture draft cache unavailable") {
			t.Fatalf("production close filter ignored a non-durable cache failure: status=%+v failures=%v", status, remaining)
		}
		backend.mu.Lock()
		backend.draftError = nil
		backend.mu.Unlock()
		if err := s.materialsUnit.state.markdownAutosave.FlushDrafts(ctx); err != nil {
			t.Fatal(err)
		}
		status = s.materialsUnit.state.markdownAutosave.Status(text.key)
		remaining = s.materialsUnit.documentClosePendingFailures(s.materialsUnit.state.markdownAutosave.PendingFailures())
		if !status.Dirty || status.CacheError != "" || status.DraftSavedRevision < status.Revision || len(remaining) != 0 {
			t.Fatalf("durable retry did not retain the dirty body while permitting close: status=%+v failures=%v", status, remaining)
		}
		_, actions := backend.calls()
		for _, action := range actions {
			if action.Kind == "document.save" {
				t.Fatal("draft durability check implicitly saved the document body")
			}
		}
	})
	t.Run("unsupported-draft-remains-pending", func(t *testing.T) {
		key := filepath.Join(t.TempDir(), "unsupported-draft.txt")
		autosave := editor.NewAutosave(func(context.Context, string, editor.SaveSnapshot) error { return nil }, nil)
		autosave.SetDebounce(time.Hour)
		autosave.SetDraftWriter(func(context.Context, string, string) error { return editor.ErrDraftUnsupported }, nil)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// Unsupported drafts retain the engine's existing body-save
			// fallback on Close; no durability equivalence is asserted here.
			autosave.Close(ctx)
		})
		unit := materialsController{state: materialsState{markdownAutosave: autosave,
			documentSaveRefs: map[string]documentSaveRef{key: {kind: "material-text", path: key}}}}
		autosave.Seed(key, "disk source")
		autosave.Edit(key, "unsupported draft source")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := autosave.FlushDrafts(ctx); err != nil {
			t.Fatalf("unsupported draft changed its existing error contract: %v", err)
		}
		status := autosave.Status(key)
		remaining := unit.documentClosePendingFailures(autosave.PendingFailures())
		if !status.Dirty || status.CacheError != "" || status.DraftSavedRevision >= status.Revision || len(remaining) != 1 {
			t.Fatalf("production close filter treated unsupported source as durable: status=%+v failures=%v", status, remaining)
		}
	})
}

func TestFeatureArchitectureSnapshotPermissionsDriveMaterialsForms(t *testing.T) {
	for _, mode := range []string{"annual", "read-only", "explicit-none"} {
		t.Run(mode, func(t *testing.T) {
			s, backend := featureArchitectureFixture(t)
			snapshot := backend.snapshot
			permissions := snapshot.Tasks[0].Materials
			if mode == "read-only" {
				permissions.CanWrite = false
			}
			if mode == "explicit-none" {
				permissions = application.MaterialsPermissions{Kind: documents.MaterialsNone, ReadRoot: permissions.ReadRoot}
			}
			snapshot.Tasks[0].Materials = permissions
			s.applySnapshot(snapshot)
			root := snapshot.Tasks[0].MaterialsPath
			owned, temporary, peer := filepath.Join(root, "snapshot-owner"), filepath.Join(root, "temp"), filepath.Join(root, "colleague")
			s.materialsUnit.state.documentsLoaded = true
			for _, directory := range []string{owned, temporary, peer} {
				s.materialsUnit.state.documents = append(s.materialsUnit.state.documents, application.Document{Name: filepath.Base(directory), Path: directory, Kind: "directory"})
			}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.documentTree(c, setTheme(c, false), 300) })
			}, 340, 440)
			if !tester.HasText("只读") {
				t.Fatal("snapshot read-only permissions were not visible in the tree")
			}
			s.openMaterialsFileNameForm("document.create", "new document", root)
			if mode == "annual" {
				if !s.dialogOpen || s.form.path != owned {
					t.Fatalf("new-file UI inferred the legacy config instead of snapshot ownership: %+v", s.form)
				}
			} else if s.dialogOpen || s.lastError == "" {
				t.Fatal("read-only snapshot opened a create form")
			}
			s.dialogOpen, s.lastError = false, ""
			s.openMaterialsRenameForm(filepath.Join(peer, "case.txt"))
			if s.dialogOpen || s.lastError == "" {
				t.Fatal("colleague materials opened a rename form")
			}
			s.dialogOpen, s.lastError = false, ""
			s.openMaterialsRenameForm(filepath.Join(temporary, "scratch.txt"))
			if mode == "annual" && !s.dialogOpen || mode != "annual" && s.dialogOpen {
				t.Fatalf("temp write capability disagrees with the snapshot: mode=%s open=%t error=%s", mode, s.dialogOpen, s.lastError)
			}
			// The local ignore rule is protected even though scratch files are writable.
			if s.materialsWritable(filepath.Join(temporary, ".gitignore"), false) {
				t.Fatal("temp synchronization exclusion became editable")
			}
			// This directory would be writable if explicit NONE were confused
			// with the absent descriptor used by older test backends.
			if mode == "explicit-none" && s.materialsWritable(filepath.Join(root, "legacy-owner", "unsafe.txt"), false) {
				t.Fatal("explicit NONE fell back to permissive legacy configuration")
			}
		})
	}
}

func TestFeatureArchitectureLateTypedRequirementResultRejectsTaskIncarnation(t *testing.T) {
	for _, change := range []string{"replacement", "controller-close"} {
		t.Run(change, func(t *testing.T) {
			s, backend := featureArchitectureFixture(t)
			const link = "https://project.feishu.cn/fixture/userstory/detail/123"
			s.taskUnit.state.tasks[0].requirementURL = link
			reader := s.requirementReader("task-a", link)
			reader.body = &application.RequirementContent{URL: link, Body: "current body"}
			action := application.Action{Kind: "requirements.read", TaskID: "task-a", Payload: s.requirementReaderPayload(reader, "requirements.read", false)}
			request, err := s.beginRequirementContentJob(action)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(request.cancel)
			result, err := executeRequirementContentJob(backend, request)
			close(request.done) // the synchronous worker above has terminated
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := s.requirementUnit.closeRequirementContentJobs(ctx); err != nil {
					t.Errorf("fixture requirement worker did not settle: %v", err)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if change == "replacement" {
				s.taskUnit.state.tasks[0].createdAt = "recreated-after-request"
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := s.requirementUnit.closeRequirementContentJobs(ctx); err != nil {
					t.Fatal(err)
				}
			}
			s.finishRequirementContentJob(request, result, nil)
			if reader.body == nil || reader.body.Body != "current body" {
				t.Fatalf("late native result replaced another incarnation/closed owner: %+v", reader.body)
			}
			calls, actions := backend.calls()
			if !reflect.DeepEqual(calls, []string{"requirements.read"}) || len(actions) != 0 {
				t.Fatalf("requirement worker bypassed its typed port: native=%v legacy=%v", calls, actions)
			}
		})
	}
}
