package desktop

import (
	"errors"
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"
	"strings"
	"testing"
)

func TestReviewMarkdownRenameRetainsNativeHistoryAndRebindsCache(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			s, content, v := reviewMarkdownPathFixture(t, strings.Repeat("正文段落。\n\n", 30)+"末尾。", directory)
			state := focusMarkdownLiveAcceptance(t, s, v)
			originalPath := state.path
			// Keep a source edit in the native undo history before the successful rename callback.
			state.live.SetSelection(0, 0)
			v.Frame()
			v.Type("新增😀 ")
			buffer := state.live.Buffer()
			want := buffer.OriginalText()
			state.live.SetSelection(2, 5)
			v.Frame()
			before := state.componentEditor.GetViewState()
			oldWrapper := state.componentEditor
			task := s.taskUnit.currentTask()
			relative, err := filepath.Rel(task.materialsPath, content.Path)
			if err != nil {
				t.Fatal(err)
			}
			previous, replacement := filepath.ToSlash(relative), filepath.ToSlash(filepath.Join(filepath.Dir(relative), "renamed.md"))
			if directory {
				previous = filepath.ToSlash(filepath.Dir(relative))
				if previous == "." {
					t.Fatal("nested fixture lost its parent directory")
				}
				replacement = "renamed-directory"
			}
			oldPath, newPath := filepath.Join(task.materialsPath, filepath.FromSlash(previous)), filepath.Join(task.materialsPath, filepath.FromSlash(replacement))
			action := application.Action{Kind: "document.rename", TaskID: task.id, Path: oldPath}
			result := application.ActionResult{Path: newPath, Data: documents.FileOperationResult{Status: documents.FileOperationCompleted, PreviousRelativePath: previous, RelativePath: replacement, IsDirectory: directory}}
			s.applyMaterialsRenameResult(action, result)
			current := s.ensureMarkdownEditor()
			if current != state || current.live.Buffer() != buffer || buffer.OriginalText() != want {
				t.Fatal("rename replaced native source session")
			}
			if state.path == originalPath || state.componentEditor == oldWrapper || state.componentEditor.Snapshot().DocumentID != markdownEditorHostID(state) {
				t.Fatal("rename did not rebind wrapper identity")
			}
			after := state.componentEditor.GetViewState()
			if before.Anchor != after.Anchor || before.Caret != after.Caret || before.ScrollY != after.ScrollY {
				t.Fatalf("rename lost view: %+v -> %+v", before, after)
			}
			s.captureMarkdownViewMemory(state)
			if state.viewBinding == nil || state.viewBinding.document == filepath.ToSlash(relative) {
				t.Fatal("old cache binding retained")
			}
			buffer.Undo()
			if buffer.OriginalText() != content.Text {
				t.Fatal("renaming discarded undo history")
			}
		})
	}
}
func TestReviewTaskRootMovePreservesLiveEditorAndReadingIdentity(t *testing.T) {
	s, _, _, v := markdownLiveAcceptanceFixture(t, strings.Repeat("段落。\n\n", 20), false, 1000)
	s.readingSession.store = NewReadingStateStore(filepath.Join(t.TempDir(), "reading-state.json"))
	t.Cleanup(func() { _ = s.closeReadingSession() })
	state := focusMarkdownLiveAcceptance(t, s, v)
	state.live.SetSelection(0, 0)
	v.Frame()
	v.Type("新增 ")
	buffer := state.live.Buffer()
	state.live.SetSelection(1, 3)
	v.Frame()
	s.captureMarkdownViewMemory(state)
	task := s.taskUnit.currentTask()
	replacement := task.path + "-moved"
	plan := &markdownTaskRootMigrationPlan{preview: workspace.TaskRootMigrationPreview{PathMappings: []workspace.TaskRootMigrationPathMapping{{SourceDirectory: task.path, TargetDirectory: replacement}}}}
	reading, err := s.buildMarkdownTaskRootReadingRelocation(plan)
	if err != nil {
		t.Fatal(err)
	}
	uiState, err := s.buildMarkdownTaskRootMigrationUI(plan)
	if err != nil {
		t.Fatal(err)
	}
	before := state.componentEditor.GetViewState()
	s.applyMarkdownTaskRootMigrationUI(plan, uiState)
	if err := s.applyMarkdownTaskRootReadingRelocation(reading); err != nil {
		t.Fatal(err)
	}
	if current := s.ensureMarkdownEditor(); current != state || current.live.Buffer() != buffer {
		t.Fatal("task-root move recreated the native Buffer")
	}
	after := state.componentEditor.GetViewState()
	if before.Anchor != after.Anchor || before.Caret != after.Caret || before.ScrollY != after.ScrollY || after.DocumentID != markdownEditorHostID(state) {
		t.Fatal("task-root move lost live view identity")
	}
	s.captureMarkdownViewMemory(state)
	position, _ := readingPositionForBinding(s.readingSession, *state.viewBinding, false)
	if position.Editor == nil || position.Editor.DocumentID != after.DocumentID {
		t.Fatal("relocated cache retained old DocumentID")
	}
}
func TestReviewNormalFeatureCloseDisposesMarkdownComponents(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "headless-close"})
	reader := markdownui.NewReaderState(markdownui.Document{ID: "reader", Source: "text"})
	editor := markdownui.NewEditorState(markdownui.Document{ID: "editor", Source: "text"})
	ctx := reader.Context()
	s.materialsUnit.state.markdown = &markdownEditorState{componentReader: reader, componentEditor: editor}
	if err := s.closeDesktopFeatures(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("normal shutdown retained reader/import context")
	}
	if err := editor.Execute(markdownui.EditorCommand{Kind: markdownui.Bold}); !errors.Is(err, markdownui.ErrClosed) {
		t.Fatalf("editor wrapper still alive after close: %v", err)
	}
}
func TestReviewReadingRenameRebasesNativeDocumentIDAndKeepsUnknownMetadata(t *testing.T) {
	s := readingObservationFixture(t)
	task := s.taskUnit.currentTask()
	oldPath := s.materialsUnit.state.documentPath
	newPath := filepath.Join(task.materialsPath, "renamed.md")
	oldRelative, _ := filepath.Rel(task.materialsPath, oldPath)
	oldRelative = filepath.ToSlash(oldRelative)
	binding := readingScrollBinding{taskPath: task.path, root: task.materialsPath, document: oldRelative, id: "markdown-editor"}
	position, save := readingPositionForBinding(s.readingSession, binding, true)
	position.Editor = &markdownui.EditorViewState{DocumentID: task.id + "-" + normalizeIdentityPath(oldPath), SourceHash: strings.Repeat("a", 64), Anchor: 4, Caret: 8, ScrollY: 500}
	save(position)
	s.remapMaterialsReading(task.path, task.materialsPath, oldRelative, "renamed.md")
	binding.document = "renamed.md"
	next, _ := readingPositionForBinding(s.readingSession, binding, false)
	if next.Editor == nil || next.Editor.DocumentID != task.id+"-"+normalizeIdentityPath(newPath) || next.Editor.Anchor != 4 || next.Editor.ScrollY != 500 {
		t.Fatal("rename cache did not retain/update native view")
	}
}

func reviewMarkdownPathFixture(t *testing.T, source string, nested bool) (*app, documents.Content, *ui.Tester) {
	if !nested {
		s, _, content, v := markdownLiveAcceptanceFixture(t, source, false, 1000)
		return s, content, v
	}
	const name = "parent/验收.md"
	s, backend, files := materialTextEditorFixture(t, map[string]string{name: source})
	s.readingSession = newMemoryReadingSession(s.snapshot.Home)
	content := files[name]
	content.ReadOnly = false
	content.MarkdownEditContext = taskMaterialsMarkdownEditContext
	s.materialsUnit.state.documents = []application.Document{{Path: content.Path, Name: filepath.Base(content.Path), Kind: "markdown"}}
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	action := application.Action{Kind: "document.read", TaskID: "task", Path: content.Path, Payload: map[string]any{"markdownEditContext": taskMaterialsMarkdownEditContext, "draftTaskPath": backend.taskPath, "draftMaterialsRoot": backend.root, "desktopRenameTaskCreatedAt": ""}}
	s.applyMaterialsActionResult(action, application.ActionResult{Path: content.Path, Text: content.Text, Data: content})
	v := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.beginOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() { s.materials(c, p) })
		s.endOverflowMenuFrame()
	}, 1000, 850)
	v.SetLocale("zh-Hans")
	t.Cleanup(s.closeMarkdownEditor)
	return s, content, v
}
