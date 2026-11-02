package desktop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/storage"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

func writableMarkdownReadFixture(s *app, content documents.Content) (application.Action, application.ActionResult) {
	task := s.taskUnit.currentTask()
	content.ReadOnly = false
	content.MarkdownEditContext = taskMaterialsMarkdownEditContext
	action := application.Action{Kind: "document.read", TaskID: task.id, Path: content.Path, Payload: map[string]any{
		"markdownEditContext": taskMaterialsMarkdownEditContext, "draftTaskPath": task.path,
		"draftMaterialsRoot": task.materialsPath, "desktopRenameTaskCreatedAt": task.createdAt,
	}}
	return action, application.ActionResult{Path: content.Path, Text: content.Text, Data: content}
}

func TestMaterialsMarkdownRequiresMatchingReadCapability(t *testing.T) {
	for _, kind := range []string{"allowed", "request-marker", "result-marker", "backend-readonly", "task-path", "root", "creation"} {
		t.Run(kind, func(t *testing.T) {
			s, _, files := materialTextEditorFixture(t, map[string]string{"doc.md": "# 原文\r\n"})
			s.taskUnit.state.tasks[0].createdAt = "2026-10-29T10:00:00Z"
			content := files["doc.md"]
			action, result := writableMarkdownReadFixture(s, content)
			switch kind {
			case "request-marker":
				delete(action.Payload, "markdownEditContext")
			case "result-marker":
				data := result.Data.(documents.Content)
				data.MarkdownEditContext = ""
				result.Data = data
			case "backend-readonly":
				data := result.Data.(documents.Content)
				data.ReadOnly = true
				result.Data = data
			case "task-path":
				action.Payload["draftTaskPath"] = s.taskUnit.currentTask().path + "-old"
			case "root":
				action.Payload["draftMaterialsRoot"] = s.taskUnit.currentTask().materialsPath + "-old"
			case "creation":
				action.Payload["desktopRenameTaskCreatedAt"] = "old-task"
			}
			s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
			s.applyMaterialsActionResult(action, result)
			state := s.ensureMarkdownEditor()
			allowed := kind == "allowed"
			if s.materialsUnit.state.documentReadOnly == allowed || s.materialsUnit.state.documentEditing != allowed || (state.live != nil) != allowed || state.readOnly == allowed {
				t.Fatalf("capability %q: readonly=%v editing=%v native=%v", kind, s.materialsUnit.state.documentReadOnly, s.materialsUnit.state.documentEditing, state.live != nil)
			}
			ref, ok := s.materialsUnit.documentSaveRef(s.documentAutosaveKey(content.Path))
			if !ok || markdownSaveRefWritable(ref) != allowed {
				t.Fatalf("save capability differs from read: %#v", ref)
			}
			if allowed {
				payload := markdownDraftPayload(ref)
				if payload["desktopRenameTaskCreatedAt"] != s.taskUnit.currentTask().createdAt || payload["markdownEditContext"] != taskMaterialsMarkdownEditContext ||
					payload["draftTaskPath"] != s.taskUnit.currentTask().path || payload["draftMaterialsRoot"] != s.taskUnit.currentTask().materialsPath {
					t.Fatalf("autosave lost the captured materials identity: %#v", payload)
				}
			}
		})
	}
}

func TestMaterialsMarkdownRestoredDraftKeepsExternalConflict(t *testing.T) {
	s, backend, files := materialTextEditorFixture(t, map[string]string{"doc.md": "# external\r\n"})
	content := files["doc.md"]
	identity, err := documents.NewMarkdownDraftIdentity("task", backend.taskPath, backend.root, content.Path)
	if err != nil {
		t.Fatal(err)
	}
	draft := documents.MarkdownDraft{Identity: identity, Content: "# recovered\r\n\r\nkept  \n", BaselineHash: documents.Hash([]byte("# old baseline\r\n")), Newline: "\r\n"}
	if err := backend.documents.WriteMarkdownDraft(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	content.Draft = &draft
	action, result := writableMarkdownReadFixture(s, content)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.applyMaterialsActionResult(action, result)
	state := s.ensureMarkdownEditor()
	key, status := s.markdownSaveState()
	if state.live == nil || state.live.Buffer().Snapshot().OriginalText() != draft.Content || !status.Dirty || !status.Conflict {
		t.Fatalf("recovery lost original draft or conflict: %#v", status)
	}
	if ref, _ := s.materialsUnit.documentSaveRef(key); !ref.restoredDraft || ref.hash != draft.BaselineHash {
		t.Fatal("recovery accepted the newer disk hash as its baseline")
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 1 {
		t.Fatalf("conflicted recovered draft must remain unsaved: %#v", failures)
	}
	if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != content.Text {
		t.Fatalf("recovering the draft changed disk: %q, %v", disk, err)
	}
	stored, err := backend.documents.ReadMarkdownDraft(context.Background(), identity)
	if err != nil || stored == nil || stored.Content != draft.Content {
		t.Fatalf("recovery discarded the persistent draft: %#v, %v", stored, err)
	}
}

func TestMaterialsMarkdownRejectsStaleTaskEditor(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"doc.md": "# source\n"})
	content := files["doc.md"]
	action, result := writableMarkdownReadFixture(s, content)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.applyMaterialsActionResult(action, result)
	state := s.ensureMarkdownEditor()
	s.taskUnit.state.tasks[0].createdAt = "replacement-task"
	if s.markdownEditorIsCurrent(state) || s.materialsMarkdownWritable(content.Path) {
		t.Fatal("old native editor retained the replacement task's write capability")
	}
	if err := s.captureMaterialsMarkdownInput(state); err == nil {
		t.Fatal("stale editor capture should fail before saving")
	}
}

func TestMaterialsMarkdownReplacementTaskRequiresDraftResolution(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"doc.md": "# source\n"})
	content := files["doc.md"]
	action, result := writableMarkdownReadFixture(s, content)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.applyMaterialsActionResult(action, result)
	state := s.ensureMarkdownEditor()
	state.live.Buffer().Edit(codeeditor.Edit{Start: 0, Text: "old task draft\n"})
	if err := s.captureMaterialsMarkdownInput(state); err != nil {
		t.Fatal(err)
	}
	s.taskUnit.state.tasks[0].createdAt = "replacement-task"
	s.closeMarkdownEditor()
	action, result = writableMarkdownReadFixture(s, content)
	s.materialsUnit.state.documentReadPending = true
	s.applyMaterialsActionResult(action, result)
	_, status := s.markdownSaveState()
	if !status.Conflict || !status.Dirty || s.materialsUnit.state.documentContent != "old task draft\n"+content.Text {
		t.Fatalf("replacement task silently adopted an older task's draft: %#v, %q", status, s.materialsUnit.state.documentContent)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 1 {
		t.Fatalf("replacement task must resolve old draft before writing: %#v", failures)
	}
}

// MI-002: use the same read-version capture as the asynchronous materials job.
// Deliver that disk snapshot both while saving and after a successful save.
func TestMaterialsMarkdownLateReadPreservesSuccessfulSaveAndUndo(t *testing.T) {
	for _, timing := range []string{"after-save", "during-save"} {
		t.Run(timing, func(t *testing.T) {
			s, backend, files := materialTextEditorFixture(t, map[string]string{"doc.md": "# old disk\n"})
			oldDisk := files["doc.md"]
			action, result := writableMarkdownReadFixture(s, oldDisk)
			s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = oldDisk.Path, true
			s.applyMaterialsActionResult(action, result)
			state := s.ensureMarkdownEditor()
			buffer := state.live.Buffer()
			delayedAction := cloneMaterialsFileJobAction(action)
			s.captureMaterialsMarkdownReadVersion(&delayedAction)
			const inserted = "new successfully saved input\n"
			buffer.Edit(codeeditor.Edit{Start: 0, Text: inserted})
			if err := s.captureMaterialsMarkdownInput(state); err != nil {
				t.Fatal(err)
			}
			want := inserted + oldDisk.Text
			version := buffer.Version()
			if timing == "during-save" {
				backend.started, backend.release = make(chan struct{}, 1), make(chan struct{})
				finished := make(chan []editor.FlushFailure, 1)
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					finished <- s.materialsUnit.state.markdownAutosave.Flush(ctx)
				}()
				select {
				case <-backend.started:
				case <-time.After(5 * time.Second):
					t.Fatal("save did not reach the controlled completion boundary")
				}
				s.applyMaterialsActionResult(delayedAction, result)
				close(backend.release)
				if failures := <-finished; len(failures) != 0 {
					t.Fatal(failures)
				}
			} else {
				if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
					t.Fatal(failures)
				}
				s.applyMaterialsActionResult(delayedAction, result)
			}
			key := s.documentAutosaveKey(oldDisk.Path)
			ref, _ := s.materialsUnit.documentSaveRef(key)
			if got := buffer.OriginalText(); got != want || s.materialsUnit.state.documentContent != want || buffer.Version() != version || !buffer.CanUndo() || ref.hash == oldDisk.Hash || ref.saveVersion != 1 {
				t.Fatalf("late read rolled back saved source/history: source=%q ui=%q version=%d/%d ref=%#v", got, s.materialsUnit.state.documentContent, buffer.Version(), version, ref)
			}
			if status := s.materialsUnit.state.markdownAutosave.Status(key); status.Dirty || status.Conflict {
				t.Fatalf("late read changed the accepted save state: %#v", status)
			}
			if disk, err := os.ReadFile(oldDisk.Path); err != nil || string(disk) != want {
				t.Fatalf("late read altered disk: %q, %v", disk, err)
			}
			if !buffer.Undo() || buffer.OriginalText() != oldDisk.Text {
				t.Fatal("successful save and late read discarded the source undo entry")
			}
		})
	}
}

func TestMaterialsMarkdownFreshReadStillAcceptsExternalContent(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"doc.md": "# source\n"})
	content := files["doc.md"]
	action, result := writableMarkdownReadFixture(s, content)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.applyMaterialsActionResult(action, result)
	state := s.ensureMarkdownEditor()
	state.live.Buffer().Edit(codeeditor.Edit{Start: 0, Text: "saved local\n"})
	if err := s.captureMaterialsMarkdownInput(state); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	action = cloneMaterialsFileJobAction(action)
	s.captureMaterialsMarkdownReadVersion(&action)
	const external = "# fresh external\r\n"
	if err := os.WriteFile(content.Path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	content.Text, content.Hash = external, documents.Hash([]byte(external))
	_, result = writableMarkdownReadFixture(s, content)
	s.applyMaterialsActionResult(action, result)
	if state.live.Buffer().OriginalText() != external || s.materialsUnit.state.documentContent != external || s.materialsUnit.state.documentHash != content.Hash {
		t.Fatal("fresh read failed to accept the current external file")
	}
}

func TestMaterialsMarkdownContextMenuChangesActualMode(t *testing.T) {
	const source = "# 菜单验收\n\n正文 **保留格式**。\n\n另一段。\n"
	s, _, _, tester := markdownLiveAcceptanceFixture(t, source, false, 1000)
	state := focusMarkdownLiveAcceptance(t, s, tester)
	buffer := state.live.Buffer()
	tester.Command("selectAll")
	if err := tester.RightClick("Markdown 编辑区"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	if !s.materialsUnit.state.documentEditing || !state.live.SourceMode || state.live.Buffer() != buffer {
		t.Fatal("context menu changed only the legacy reading preference")
	}
	tester.Command("copy")
	if tester.Clipboard() != source {
		t.Fatal("source menu transition lost the document selection")
	}
	if err := tester.RightClick("Markdown 编辑区"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("实时编辑"); err != nil {
		t.Fatal(err)
	}
	if !s.materialsUnit.state.documentEditing || state.live.SourceMode {
		t.Fatal("context menu failed to return to live preview")
	}
	if _, ok := tester.Find("阅读"); ok {
		t.Fatal("writable Markdown still exposes the removed reading mode")
	}
	if err := tester.Click("Markdown 编辑区"); err != nil {
		t.Fatal(err)
	}
	tester.Command("selectAll")
	tester.Key(ui.Ctrl, ui.KeyC)
	anchor, caret := state.live.Selection()
	if anchor != 0 || caret != buffer.Len() || tester.Clipboard() != source {
		t.Fatal("live preview did not select and copy through the same editor state")
	}
	if err := tester.RightClick("Markdown 编辑区"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	if !s.materialsUnit.state.documentEditing || !state.live.SourceMode || state.live.Buffer() != buffer {
		t.Fatal("live preview context menu did not switch the shared editor to source")
	}
}

// Pause after the real application's save has committed and released all task
// and document locks, before the desktop callback can publish its new hash.
type materialMarkdownAfterSaveBackend struct {
	base                 *application.App
	saved, release       chan struct{}
	firstSave, unblocked sync.Once
}

func (backend *materialMarkdownAfterSaveBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (backend *materialMarkdownAfterSaveBackend) unblock() {
	backend.unblocked.Do(func() { close(backend.release) })
}

func (backend *materialMarkdownAfterSaveBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	result, err := backend.base.Dispatch(ctx, action)
	if action.Kind == "document.save" && err == nil {
		pause := false
		backend.firstSave.Do(func() { pause = true })
		if pause {
			close(backend.saved)
			select {
			case <-backend.release:
			case <-ctx.Done():
				return result, ctx.Err()
			}
		}
	}
	return result, err
}

func realMaterialMarkdownFixture(t *testing.T) (*app, *materialMarkdownAfterSaveBackend, application.Action, application.ActionResult) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	paths.Tasks = filepath.Join(paths.Home, "tasks")
	taskPath := filepath.Join(paths.Tasks, "task")
	if err := os.MkdirAll(taskPath, 0700); err != nil {
		t.Fatal(err)
	}
	scope, err := documents.PrepareLocalTaskMaterials(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	backendApp := &application.App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Documents: documents.NewService(paths.Cache)}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := backendApp.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = "task", "task", "test/task"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-29", "2026-10-29"
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &scope.Owned}
	if err := backendApp.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scope.Owned, "doc.md")
	if err := os.WriteFile(path, []byte("# old disk\n"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &materialMarkdownAfterSaveBackend{base: backendApp, saved: make(chan struct{}), release: make(chan struct{})}
	s := &app{page: "tasks", tab: 1,
		backend: backend, backendGate: make(chan struct{}, 1), materialsUnit: materialsController{state: materialsState{documentDrafts: map[string]string{}, documentDirtyMap: map[string]bool{}, documentSaveRefs: map[string]documentSaveRef{}}}, taskUnit: taskController{state: taskState{tasks: []task{{id: "task", path: taskPath, materialsPath: scope.Root, createdAt: manifest.CreatedAt, materialsStatus: "READY"}}}}}
	s.snapshot.Home = paths.Home
	s.initMarkdownAutosave()
	s.materialsUnit.state.markdownAutosave.SetDebounce(time.Hour)
	t.Cleanup(func() {
		backend.unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.materialsUnit.state.markdownAutosave.Close(ctx)
	})
	action := application.Action{Kind: "document.read", TaskID: "task", Path: path, Payload: map[string]any{
		"markdownEditContext": taskMaterialsMarkdownEditContext, "draftTaskPath": taskPath,
		"draftMaterialsRoot": scope.Root, "desktopRenameTaskCreatedAt": manifest.CreatedAt,
	}}
	s.captureMaterialsMarkdownReadVersion(&action)
	result, err := backend.Dispatch(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = path, true
	s.applyMaterialsActionResult(action, result)
	return s, backend, action, result
}

func TestMaterialsMarkdownReadOfOwnSaveWaitsForPublishedBaseline(t *testing.T) {
	s, backend, action, _ := realMaterialMarkdownFixture(t)
	defer backend.unblock()
	state := s.ensureMarkdownEditor()
	buffer := state.live.Buffer()
	buffer.Edit(codeeditor.Edit{Start: 0, Text: "new successfully saved input\n"})
	if err := s.captureMaterialsMarkdownInput(state); err != nil {
		t.Fatal(err)
	}
	want, version := buffer.OriginalText(), buffer.Version()
	finished := make(chan []editor.FlushFailure, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		finished <- s.materialsUnit.state.markdownAutosave.Flush(ctx)
	}()
	select {
	case <-backend.saved:
	case <-time.After(5 * time.Second):
		t.Fatal("real backend save did not commit")
	}
	key := s.documentAutosaveKey(action.Path)
	if !s.materialsUnit.state.markdownAutosave.Status(key).Saving {
		t.Fatal("fixture did not pause before the desktop baseline publication")
	}
	action = cloneMaterialsFileJobAction(action)
	s.captureMaterialsMarkdownReadVersion(&action)
	result, err := backend.Dispatch(context.Background(), action)
	if err != nil || result.Text != want {
		t.Fatalf("read after the real task lock release did not observe our own save: %q, %v", result.Text, err)
	}
	s.applyMaterialsActionResult(action, result)
	if status := s.materialsUnit.state.markdownAutosave.Status(key); status.Conflict || status.Error != "" {
		t.Fatalf("own committed bytes became an external conflict: %#v", status)
	}
	backend.unblock()
	if failures := <-finished; len(failures) != 0 {
		t.Fatal(failures)
	}
	s.captureMaterialsMarkdownReadVersion(&action)
	result, err = backend.Dispatch(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	s.applyMaterialsActionResult(action, result)
	if status := s.materialsUnit.state.markdownAutosave.Status(key); status.Dirty || status.Conflict || status.Error != "" || buffer.Version() != version || !buffer.CanUndo() {
		t.Fatalf("fresh read after baseline publication lost saved state or undo: %#v", status)
	}
	buffer.Edit(codeeditor.Edit{Start: 0, Text: "next edit\n"})
	if err := s.captureMaterialsMarkdownInput(state); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatalf("the next autosave was paused by our earlier read: %#v", failures)
	}
	if disk, err := os.ReadFile(action.Path); err != nil || string(disk) != "next edit\n"+want {
		t.Fatalf("the next edit was not saved: %q, %v", disk, err)
	}
}

func TestMaterialsMarkdownRejectedOpeningReadRetainsDefaultLive(t *testing.T) {
	s, backend, originalAction, oldResult := realMaterialMarkdownFixture(t)
	backend.unblock()
	state := s.ensureMarkdownEditor()
	state.live.Buffer().Edit(codeeditor.Edit{Start: 0, Text: "new input\n"})
	if err := s.captureMaterialsMarkdownInput(state); err != nil {
		t.Fatal(err)
	}
	s.closeMarkdownEditor()
	// selectDocumentWithReveal installs these flags before its asynchronous read.
	s.materialsUnit.state.documentReadOnly, s.materialsUnit.state.documentEditing, s.materialsUnit.state.documentReadPending = true, false, true
	delayedAction := cloneMaterialsFileJobAction(originalAction)
	s.captureMaterialsMarkdownReadVersion(&delayedAction)
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	s.applyMaterialsActionResult(delayedAction, oldResult)
	if !s.materialsUnit.state.documentReadPending || s.materialsUnit.state.documentEditing {
		t.Fatal("rejected opening read consumed the pending default-live transition")
	}
	freshAction := cloneMaterialsFileJobAction(originalAction)
	s.captureMaterialsMarkdownReadVersion(&freshAction)
	freshResult, err := backend.Dispatch(context.Background(), freshAction)
	if err != nil {
		t.Fatal(err)
	}
	s.applyMaterialsActionResult(freshAction, freshResult)
	state = s.ensureMarkdownEditor()
	if !s.materialsUnit.state.documentEditing || state.live == nil || state.live.SourceMode || s.materialsUnit.state.documentReadOnly || s.materialsUnit.state.documentReadPending {
		t.Fatalf("fresh read did not finish opening in Live Preview: editing=%v live=%v readonly=%v pending=%v", s.materialsUnit.state.documentEditing, state.live != nil, s.materialsUnit.state.documentReadOnly, s.materialsUnit.state.documentReadPending)
	}
	if state.live.Buffer().OriginalText() != freshResult.Text || s.materialsUnit.state.documentContent != freshResult.Text {
		t.Fatal("reopened native editor did not retain the saved source")
	}
}
