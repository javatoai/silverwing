package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

type materialTextTestBackend struct {
	documents        *documents.Service
	root, taskPath   string
	mu               sync.Mutex
	fail             bool
	started, release chan struct{}
}

func (b *materialTextTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *materialTextTestBackend) Dispatch(ctx context.Context, a application.Action) (application.ActionResult, error) {
	id, err := documents.NewMarkdownDraftIdentity(a.TaskID, b.taskPath, b.root, a.Path)
	if err != nil {
		return application.ActionResult{}, err
	}
	hash, _ := a.Payload["expectedHash"].(string)
	bom, _ := a.Payload["hasUtf8Bom"].(bool)
	switch a.Kind {
	case "document.draft":
		if documents.Kind(a.Path) == "markdown" {
			return application.ActionResult{}, b.documents.WriteMarkdownDraft(ctx, documents.MarkdownDraft{Identity: id, Content: a.Text, BaselineHash: hash, HasUTF8BOM: bom})
		}
		return application.ActionResult{}, b.documents.WriteMaterialTextDraft(ctx, documents.MaterialTextDraft{Identity: id, Content: a.Text, BaselineHash: hash, HasUTF8BOM: bom})
	case "document.draft-discard":
		if documents.Kind(a.Path) == "markdown" {
			return application.ActionResult{}, b.documents.DiscardMarkdownDraft(ctx, id)
		}
		return application.ActionResult{}, b.documents.DiscardMaterialTextDraft(ctx, id)
	case "document.save":
		b.mu.Lock()
		fail, started, release := b.fail, b.started, b.release
		b.mu.Unlock()
		if fail {
			return application.ActionResult{}, errors.New("fixture save failure")
		}
		if overwrite, _ := a.Payload["overwrite"].(bool); overwrite {
			current, e := b.documents.MaterialTextSnapshot(b.root, a.Path)
			if e != nil {
				return application.ActionResult{}, e
			}
			hash = current.Hash
		}
		var snap documents.MaterialTextSnapshot
		if documents.Kind(a.Path) == "markdown" {
			snap, err = b.documents.SaveMarkdown(ctx, b.root, a.Path, a.Text, hash)
		} else {
			snap, err = b.documents.SaveMaterialText(ctx, b.root, a.Path, a.Text, hash, bom)
		}
		if err != nil {
			return application.ActionResult{}, err
		}
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
				return application.ActionResult{}, ctx.Err()
			}
		}
		if documents.Kind(a.Path) == "markdown" {
			err = b.documents.CompleteMarkdownDraftSave(ctx, id, a.Text, hash, snap.Hash)
		} else {
			err = b.documents.CompleteMaterialTextDraftSave(ctx, id, a.Text, hash, snap.Hash)
		}
		if err != nil {
			return application.ActionResult{}, err
		}
		return application.ActionResult{Text: snap.Text, Path: snap.Path, Data: documents.Content{Path: snap.Path, Text: snap.Text, Hash: snap.Hash, HasUTF8BOM: snap.HasUTF8BOM}}, nil
	}
	return application.ActionResult{}, errors.New("unexpected test action: " + a.Kind)
}

func materialTextEditorFixture(t *testing.T, files map[string]string) (*app, *materialTextTestBackend, map[string]documents.Content) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "materials")
	taskPath := filepath.Join(base, "tasks", "task")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	backend := &materialTextTestBackend{documents: documents.NewService(filepath.Join(base, "home", "cache")), root: root, taskPath: taskPath}
	s := &app{
		taskUnit: taskController{state: taskState{tasks: []task{{id: "task", path: taskPath, materialsPath: root, materialsStatus: "READY"}}}},
		materialsUnit: materialsController{state: materialsState{documentsLoaded: true,
			documentDrafts:   map[string]string{},
			documentDirtyMap: map[string]bool{},
			documentSaveRefs: map[string]documentSaveRef{}}},
		page:        "tasks",
		tab:         1,
		backend:     backend,
		backendGate: make(chan struct{}, 1),
	}
	s.snapshot.Home = filepath.Join(base, "home")
	s.initMarkdownAutosave()
	s.materialsUnit.state.markdownAutosave.SetDebounce(time.Hour)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.materialsUnit.state.markdownAutosave.Close(ctx)
	})
	contents := make(map[string]documents.Content)
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		var snap documents.MaterialTextSnapshot
		var err error
		if documents.Kind(path) == "markdown" {
			snap, err = backend.documents.MarkdownSnapshot(root, path)
		} else {
			snap, err = backend.documents.MaterialTextSnapshot(root, path)
		}
		if err != nil {
			t.Fatal(err)
		}
		contents[name] = documents.Content{Path: path, Kind: documents.Kind(path), Text: snap.Text, Hash: snap.Hash, HasUTF8BOM: snap.HasUTF8BOM, Newline: snap.Newline, Size: snap.Size}
	}
	return s, backend, contents
}

func bindMaterialTextTest(s *app, content documents.Content) *materialTextEditorState {
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentKind, s.materialsUnit.state.documentReadOnly = content.Path, content.Kind, false
	s.seedMaterialTextEditor("task", content.Path, content)
	state := s.currentMaterialTextEditor()
	s.materialsUnit.state.materialTextActive = state
	return state
}

func flushMaterialTextTest(t *testing.T, s *app) []editor.FlushFailure {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.materialsUnit.state.markdownAutosave.Flush(ctx)
}

func TestMaterialTextEditorABAIdentitySelectionAndSaveEcho(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"a.sql": "\xef\xbb\xbfselect 1;\r\n-- keep\n", "b.txt": "B\n"})
	a := bindMaterialTextTest(s, files["a.sql"])
	buffer := a.view.Buffer
	buffer.Edit(codeeditor.Edit{Start: 7, End: 8, Text: "2"})
	a.view.Select(3, 8)
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	selection := a.view.Selection()
	source := buffer.OriginalText()
	b := bindMaterialTextTest(s, files["b.txt"])
	if b == a || b.view.Buffer == buffer {
		t.Fatal("B reused A buffer")
	}
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	ref, _ := s.materialsUnit.documentSaveRef(a.key)
	s.materialTextSaved(a.key, ref, s.materialsUnit.state.markdownAutosave.Status(a.key))
	if s.materialsUnit.state.documentDirtyMap[a.path] || s.materialsUnit.state.documentDrafts[a.path] != "" {
		t.Fatal("inactive successful save retained dirty tree/tab state")
	}
	saved, _ := os.ReadFile(a.path)
	if string(saved) != "\xef\xbb\xbf"+source {
		t.Fatalf("source changed: %q", saved)
	}
	fresh := files["a.sql"]
	fresh.Text = source
	fresh.Hash = documents.Hash(saved)
	rebound := bindMaterialTextTest(s, fresh)
	if rebound != a || rebound.view.Buffer != buffer || rebound.view.Selection() != selection {
		t.Fatal("A-B-A/save echo reset buffer or selection")
	}
	if rebound.view.Buffer.OriginalText() != source {
		t.Fatal("A restored stale bytes")
	}
	oldState := rebound
	s.removeMaterialTextEditors([]string{oldState.path})
	oldState.view.OnCompositionChanged(true)
	oldState.view.Buffer.Edit(codeeditor.Edit{Text: "late"})
	if err := s.captureMaterialTextState(oldState); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.materialsUnit.documentSaveRef(oldState.key); exists || s.materialsUnit.state.materialTextEditors[oldState.key] != nil {
		t.Fatal("late callback recreated deleted identity")
	}
}

func TestMaterialTextEditorPreviewModeUsesSameDraftAndNativeInput(t *testing.T) {
	for _, name := range []string{"data.json", "data.csv", "plain.txt"} {
		t.Run(name, func(t *testing.T) {
			s, _, files := materialTextEditorFixture(t, map[string]string{name: "one\r\ntwo\n"})
			state := bindMaterialTextTest(s, files[name])
			view := state.view
			if documents.Kind(name) != "text" {
				if state.editing {
					t.Fatal("structured preview did not open first")
				}
				s.toggleMaterialsTextEditing()
			}
			tester := ui.NewTester(func(c *ui.Context) {
				s.observeMaterialTextInput()
				ui.Column(c).Fill().Children(func() {
					s.renderMaterialsTextEditorHeader(c, setTheme(c, false))
					s.renderMaterialsTextEditor(c, setTheme(c, false))
				})
				s.observeMaterialTextInput()
			}, 650, 350)
			if err := tester.Click(name); err != nil {
				t.Fatal(err)
			}
			tester.Key(ui.Ctrl, ui.KeyA)
			tester.Type("中文\nnew")
			if state.view != view || state.view.ReadOnly || !s.materialsUnit.state.documentDirty || !strings.Contains(state.source, "中文") {
				t.Fatalf("native edit not captured: %q", state.source)
			}
			if documents.Kind(name) != "text" {
				s.toggleMaterialsTextEditing()
				s.toggleMaterialsTextEditing()
				if state.view != view {
					t.Fatal("mode switch replaced shared draft")
				}
			}
			if tester.HasText("正在自动保存") || tester.HasText("已自动保存") || tester.HasText("修改待自动保存") {
				t.Fatal("ordinary save exposed transient status")
			}
		})
	}
}

func TestMaterialTextEditorRecoveryConflictRetryAndCloseCapture(t *testing.T) {
	s, backend, files := materialTextEditorFixture(t, map[string]string{"a.txt": "disk\n"})
	content := files["a.txt"]
	identity, _ := documents.NewMarkdownDraftIdentity("task", backend.taskPath, backend.root, content.Path)
	content.TextDraft = &documents.MaterialTextDraft{Identity: identity, Content: "restored\r\n", BaselineHash: content.Hash, Newline: "\n"}
	state := bindMaterialTextTest(s, content)
	if !state.restored || !s.materialsUnit.state.markdownAutosave.Status(state.key).Dirty {
		t.Fatal("draft not restored")
	}
	if data, _ := os.ReadFile(state.path); string(data) != "disk\n" {
		t.Fatal("restored draft implicitly saved")
	}
	state.view.Buffer.Edit(codeeditor.Edit{Text: "edit "})
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.fail = true
	backend.mu.Unlock()
	if failures := flushMaterialTextTest(t, s); len(failures) == 0 {
		t.Fatal("failure hidden")
	}
	if draft, err := backend.documents.ReadMaterialTextDraft(context.Background(), identity); err != nil || draft == nil || draft.Content != state.source {
		t.Fatalf("failure lost durable draft: %#v %v", draft, err)
	}
	backend.mu.Lock()
	backend.fail = false
	backend.mu.Unlock()
	s.retryMaterialTextAutosave(state, false)
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	if err := os.WriteFile(state.path, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	state.view.Buffer.Edit(codeeditor.Edit{Text: "new "})
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) == 0 || !s.materialsUnit.state.markdownAutosave.Status(state.key).Conflict {
		t.Fatal("external conflict not paused")
	}
	if data, _ := os.ReadFile(state.path); string(data) != "external" {
		t.Fatal("conflict overwrote external source")
	}
	s.retryMaterialTextAutosave(state, true)
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	state.view.OnCompositionChanged(true)
	if !s.materialsUnit.state.markdownAutosave.Status(state.key).Composing {
		t.Fatal("composition did not pause immediately")
	}
	state.view.OnCompositionChanged(false)
	state.view.Buffer.Edit(codeeditor.Edit{Text: "last "})
	if err := s.captureDocumentCloseInput(); err != nil {
		t.Fatal(err)
	}
	if cached, _ := s.materialsUnit.state.markdownAutosave.Content(state.key); cached != state.view.Buffer.OriginalText() {
		t.Fatal("close missed final native input")
	}
}

func TestMaterialTextEditorRenameAndDeleteQuiesce(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"a.txt": "disk"})
	state := bindMaterialTextTest(s, files["a.txt"])
	state.view.Buffer.Edit(codeeditor.Edit{Text: "latest "})
	if _, err := s.captureMarkdownAutosaveRename("task", s.taskUnit.currentTask().materialsPath, state.path, filepath.Join(filepath.Dir(state.path), "a.md")); err == nil {
		t.Fatal("dirty cross-adapter rename allowed")
	}
	if source, _ := s.materialsUnit.state.markdownAutosave.Content(state.key); source != "latest disk" {
		t.Fatal("rename missed last native input")
	}
	action := application.Action{Kind: "document.trash", TaskID: "task", Payload: map[string]any{"paths": []string{state.path}, "confirmed": true}}
	plan, err := s.captureMaterialTextDelete(action)
	if err != nil || plan == nil {
		t.Fatalf("delete plan: %#v %v", plan, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lease, err := plan.autosave.Quiesce(ctx, plan.keys)
	if err != nil {
		t.Fatal(err)
	}
	request := &materialsFileJob{textDelete: plan, textDeleteLease: lease}
	result := application.ActionResult{Data: []documents.TrashItemResult{{Path: state.path}}}
	s.retireMaterialTextDeleteResult(request, result)
	request.finishMaterialTextDeleteLease(result)
	if _, exists := s.materialsUnit.documentSaveRef(state.key); exists || s.materialsUnit.state.materialTextEditors[state.key] != nil || s.materialsUnit.state.markdownAutosave.Status(state.key).Dirty {
		t.Fatal("delete left a writable identity")
	}
	state.view.OnCompositionChanged(true)
	if s.materialsUnit.state.markdownAutosave.Status(state.key).Composing {
		t.Fatal("deleted IME callback revived entry")
	}
}

func TestMaterialTextEditorBOMLimitAndBlockedSaveKeepsNewInput(t *testing.T) {
	s, backend, files := materialTextEditorFixture(t, map[string]string{"large.txt": "\xef\xbb\xbf" + strings.Repeat("x", documents.MaxMaterialsText-3)})
	state := bindMaterialTextTest(s, files["large.txt"])
	before, version := state.view.Buffer.OriginalText(), state.view.Buffer.Version()
	state.view.Buffer.Edit(codeeditor.Edit{Text: "overflow"})
	if state.view.Buffer.Version() != version || state.view.Buffer.OriginalText() != before {
		t.Fatal("BOM-inclusive edit limit changed native source/history")
	}
	state.view.Buffer.Edit(codeeditor.Edit{Start: 0, End: 1, Text: ""})
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	state.view.Buffer.Edit(codeeditor.Edit{Text: "y"})
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	if len(state.source) != documents.MaxMaterialsText-3 || !s.materialsUnit.state.markdownAutosave.Status(state.key).Dirty {
		t.Fatal("legal edit after rejected input remained paused")
	}
	backend.mu.Lock()
	backend.started = make(chan struct{}, 1)
	backend.release = make(chan struct{})
	started, release := backend.started, backend.release
	backend.mu.Unlock()
	flushed := make(chan []editor.FlushFailure, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		flushed <- s.materialsUnit.state.markdownAutosave.Flush(ctx)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not start")
	}
	state.view.Buffer.Edit(codeeditor.Edit{Start: 1, End: 2, Text: "z"})
	state.view.Select(10, 20)
	selection := state.view.Selection()
	buffer := state.view.Buffer
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-flushed:
		status := s.materialsUnit.state.markdownAutosave.Status(state.key)
		if status.Error != "" || status.Conflict {
			t.Fatal(status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save did not finish")
	}
	if state.view.Buffer != buffer || state.view.Selection() != selection || state.view.Buffer.OriginalText() != state.source {
		t.Fatal("save echo reset newer native input or selection")
	}
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	data, _ := os.ReadFile(state.path)
	if string(data) != "\xef\xbb\xbf"+state.source {
		t.Fatal("newer edit lost after save")
	}
}

func TestMaterialTextEditorAutosaveDebounceCompositionAndDurableDraft(t *testing.T) {
	s, backend, files := materialTextEditorFixture(t, map[string]string{"a.txt": "disk"})
	s.materialsUnit.state.markdownAutosave.SetDebounce(650 * time.Millisecond)
	state := bindMaterialTextTest(s, files["a.txt"])
	state.view.Buffer.Edit(codeeditor.Edit{Text: "edited "})
	if err := s.captureMaterialTextInput(); err != nil {
		t.Fatal(err)
	}
	state.view.OnCompositionChanged(true)
	time.Sleep(700 * time.Millisecond)
	if data, _ := os.ReadFile(state.path); string(data) != "disk" {
		t.Fatal("IME composition wrote the file")
	}
	identity, _ := documents.NewMarkdownDraftIdentity("task", backend.taskPath, backend.root, state.path)
	draft, err := backend.documents.ReadMaterialTextDraft(context.Background(), identity)
	if err != nil || draft == nil || draft.Content != state.source {
		t.Fatalf("independent draft debounce did not persist source: %#v %v", draft, err)
	}
	state.view.OnCompositionChanged(false)
	time.Sleep(400 * time.Millisecond)
	if data, _ := os.ReadFile(state.path); string(data) != "disk" {
		t.Fatal("save ignored the 650ms debounce")
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.materialsUnit.state.markdownAutosave.Status(state.key).Dirty && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if status := s.materialsUnit.state.markdownAutosave.Status(state.key); status.Dirty || status.Error != "" {
		t.Fatal(status)
	}
	if data, _ := os.ReadFile(state.path); string(data) != state.source {
		t.Fatal("committed composition did not autosave")
	}
}

func TestMaterialTextMarkdownReadonlyDeleteDoesNotStartSave(t *testing.T) {
	s, backend, files := materialTextEditorFixture(t, map[string]string{"note.md": "# disk\r\nkeep\n"})
	content := files["note.md"]
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.applyMaterialsActionResult(application.Action{Kind: "document.read", TaskID: "task", Path: content.Path},
		application.ActionResult{Path: content.Path, Text: content.Text, Data: content})
	key := s.documentAutosaveKey(content.Path)
	backend.mu.Lock()
	backend.started = make(chan struct{}, 1)
	started := backend.started
	backend.mu.Unlock()
	s.editMarkdownAutosave(content.Path, "blocked late edit")
	if failures := flushMaterialTextTest(t, s); len(failures) > 0 {
		t.Fatal(failures)
	}
	select {
	case <-started:
		t.Fatal("readonly Markdown started a save")
	default:
	}
	plan, err := s.captureMaterialTextDelete(application.Action{Kind: "document.trash", TaskID: "task", Payload: map[string]any{"paths": []string{content.Path}, "confirmed": true}})
	if err != nil || plan == nil || len(plan.keys) != 1 || plan.keys[0] != key {
		t.Fatalf("Markdown missing from delete lease: %#v %v", plan, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lease, err := plan.autosave.Quiesce(ctx, plan.keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(content.Path); err != nil {
		lease.Release()
		t.Fatal(err)
	}
	request := &materialsFileJob{textDelete: plan, textDeleteLease: lease}
	result := application.ActionResult{Data: []documents.TrashItemResult{{Path: content.Path}}}
	s.retireMaterialTextDeleteResult(request, result)
	request.finishMaterialTextDeleteLease(result)
	s.editMarkdownAutosave(content.Path, "late callback must not recreate")
	s.setMarkdownComposing("task", content.Path, true)
	if _, exists := s.materialsUnit.documentSaveRef(key); exists {
		t.Fatal("late Markdown callback recreated deleted reference")
	}
	if status := s.materialsUnit.state.markdownAutosave.Status(key); status.Dirty || status.Composing {
		t.Fatal(status)
	}
	if _, err := os.Stat(content.Path); !os.IsNotExist(err) {
		t.Fatal("deleted Markdown file was recreated")
	}
}
