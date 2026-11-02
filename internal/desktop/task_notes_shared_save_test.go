package desktop

import (
	"context"
	"os"
	"path/filepath"
	"silverwing-go/internal/workspace"
	"testing"
)

func TestTaskLocalNoteLateReadAfterSavedRevisionCannotRewind(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, d, "original\r\n", true)
	setTaskLocalNoteText(t, s, d, "new content\r\n\r\n")
	read := s.taskLocalNoteAction(d, "instructions.read", false)
	version := d.markdown.live.Buffer().Version()
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	ref, _ := s.instructionSaveRef(d.key)
	s.applyInstructionReadResult(read, taskLocalNoteResult(d, "original\r\n", true))
	current, _ := s.instructionSaveRef(d.key)
	if d.text() != "new content\r\n\r\n" || current.hash != ref.hash || current.saveVersion != ref.saveVersion || d.markdown.live.Buffer().Version() != version {
		t.Fatal("late clean read rewound a newly saved revision")
	}
}

func TestTaskLocalNoteRealReadKeepsBOMAndMixedNewlines(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, d, "", false)
	raw := "\ufefffirst\r\nsecond\ntrailing  \r\n\r\n"
	if err := os.MkdirAll(filepath.Dir(d.path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	read := s.taskLocalNoteAction(d, "instructions.read", false)
	result, err := s.backend.(*settingsInstructionBackend).Dispatch(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	s.applyInstructionReadResult(read, result)
	ref, _ := s.instructionSaveRef(d.key)
	if !ref.hasUTF8BOM {
		t.Fatal("real read lost BOM capability metadata")
	}
	setTaskLocalNoteText(t, s, d, "prefix "+result.Text)
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	data, err := os.ReadFile(d.path)
	if err != nil || string(data) != "\ufeffprefix "+result.Text {
		t.Fatalf("BOM/newline changed: %q %v", data, err)
	}
}

func TestTaskLocalNoteRootMoveKeepsNativeOwnerSelectionAndUndo(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, d, "original\n", true)
	view := taskLocalNoteTester(s, 800)
	native := d.markdown.live
	native.SetSelection(0, 0)
	native.Focus()
	view.Frame()
	view.Type("prefix ")
	if err := s.captureTaskLocalNote(d); err != nil {
		t.Fatal(err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	buffer := native.Buffer()
	native.SetSelection(1, 3)
	beforeView := d.markdown.componentEditor.GetViewState()
	oldKey, oldPath := d.key, d.taskPath
	oldRead := s.taskLocalNoteAction(d, "instructions.read", false)
	before, _ := s.instructionSaveRef(oldKey)
	after := before
	after.taskPath = oldPath + "-moved"
	after.path = filepath.Join(after.taskPath, ".workspace", "agent", "TASK-RULES.md")
	newKey := s.instructionAutosaveKey("task", d.taskID, "", after.path) + "\x00" + d.createdAt
	plan := &markdownTaskRootMigrationPlan{preview: workspace.TaskRootMigrationPreview{PathMappings: []workspace.TaskRootMigrationPathMapping{{SourceDirectory: oldPath, TargetDirectory: after.taskPath}}}, entries: []markdownTaskRootMigrationEntry{{oldKey: oldKey, newKey: newKey, before: before, after: after}}}
	uiState, err := s.buildMarkdownTaskRootMigrationUI(plan)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := d.autosave.Quiesce(context.Background(), []string{oldKey})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := lease.Move(map[string]string{oldKey: newKey}); err != nil {
		t.Fatal(err)
	}
	delete(s.materialsUnit.state.documentSaveRefs, oldKey)
	s.materialsUnit.state.documentSaveRefs[newKey] = after
	s.applyMarkdownTaskRootMigrationUI(plan, uiState)
	current := s.ensureTaskLocalNote()
	movedView := d.markdown.componentEditor.GetViewState()
	if current != d || d.key != newKey || d.markdown.live.Buffer() != buffer || movedView.Anchor != beforeView.Anchor || movedView.Caret != beforeView.Caret || movedView.Mode != beforeView.Mode || movedView.DocumentID != newKey {
		t.Fatal("root move lost native view/owner")
	}
	s.applyInstructionReadResult(oldRead, taskLocalNoteResult(d, "stale", true))
	if d.text() != "prefix original\n" {
		t.Fatal("old-path read reached moved owner")
	}
	buffer.Undo()
	if d.text() != "original\n" {
		t.Fatalf("root move lost undo history: %q", d.text())
	}
	d.autosave.AcceptDisk(newKey, d.text(), true)
}
