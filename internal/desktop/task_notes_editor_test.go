package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	markdownui "silverwing-go/internal/ui/markdown"
	"testing"
	"time"
)

func taskLocalNoteFixture(t *testing.T) (*app, *taskLocalNoteDraft) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	backend := &settingsInstructionBackend{App: &application.App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Documents: documents.NewService(paths.Cache)}}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := backend.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	s := &app{page: "tasks", tab: 3, backend: backend, backendGate: make(chan struct{}, 1)}
	s.snapshot.Home = paths.Home
	for _, id := range []string{"task-a", "task-b"} {
		manifest := domain.DefaultTaskManifest()
		manifest.FolderName, manifest.TaskDirectoryName, manifest.CreatedAt = id, id, "created-"+id
		path := filepath.Join(paths.Tasks, id)
		if err := backend.Manifests.Save(path, manifest); err != nil {
			t.Fatal(err)
		}
		s.taskUnit.state.tasks = append(s.taskUnit.state.tasks, task{id: id, path: path, createdAt: manifest.CreatedAt})
	}
	d := s.ensureTaskLocalNote()
	s.materialsUnit.state.markdownAutosave.SetDebounce(time.Hour)
	s.taskUnit.state.taskNotesUI.localActiveKey, d.refreshPending = d.key, false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.materialsUnit.state.markdownAutosave.Close(ctx)
		for _, state := range s.ensureTaskNotesUI().markdownEditors {
			closeMarkdownPreviewState(state)
		}
	})
	return s, d
}

func taskLocalNoteResult(d *taskLocalNoteDraft, text string, exists bool) application.ActionResult {
	hash := ""
	if exists {
		hash = documents.Hash([]byte(text))
	}
	return application.ActionResult{Path: d.path, Text: text, Data: documents.TaskRulesSnapshot{MarkdownFileSnapshot: documents.MarkdownFileSnapshot{Path: d.path, Text: text, Hash: hash}, Exists: exists}}
}

func loadTaskLocalNote(s *app, d *taskLocalNoteDraft, text string, exists bool) {
	if exists {
		if err := os.MkdirAll(filepath.Dir(d.path), 0700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(d.path, []byte(text), 0600); err != nil {
			panic(err)
		}
	}
	action := s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyInstructionReadResult(action, taskLocalNoteResult(d, text, exists))
	notes := s.ensureTaskNotesUI()
	notes.localActiveKey, notes.activeKey, notes.activeKind = d.key, d.key, "task-local"
	notes.activeMarkdown = s.ensureTaskLocalMarkdown(d)
	d.refreshPending = false
}

func taskLocalNoteTester(s *app, width int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		s.beginOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() { s.renderTaskNotes(c, setTheme(c, s.dark)) })
		s.endOverflowMenuFrame()
	}, width, 520)
}

func setTaskLocalNoteText(t *testing.T, s *app, d *taskLocalNoteDraft, text string) {
	t.Helper()
	d.markdown.live.Buffer().Set(text)
	if err := s.captureTaskLocalNote(d); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLocalNoteSharedEditorAndMissingFile(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			s, d := taskLocalNoteFixture(t)
			loadTaskLocalNote(s, d, "", exists)
			tester := taskLocalNoteTester(s, 480)
			if _, ok := tester.Find("任务说明 Markdown 编辑区"); !ok || d.markdown.componentEditor.Mode() != markdownui.Live || d.view.Buffer != d.markdown.live.Buffer() {
				t.Fatal("task note must share public live editor and native buffer")
			}
			if !exists {
				if _, err := os.Stat(d.path); !os.IsNotExist(err) {
					t.Fatal("opening a missing note created it")
				}
			}
			buffer := d.markdown.live.Buffer()
			for _, mode := range []markdownui.Mode{markdownui.Preview, markdownui.Source, markdownui.Live} {
				if err := d.markdown.componentEditor.SetMode(mode); err != nil {
					t.Fatal(err)
				}
				tester.Frame()
				if d.markdown.live.Buffer() != buffer {
					t.Fatal("mode transition replaced buffer")
				}
			}
			setTaskLocalNoteText(t, s, d, "手动填写中文 😀\r\n\r\n")
			if !d.dirty() {
				t.Fatal("edit not queued in shared autosave")
			}
			if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
				t.Fatal(failures)
			}
			s.syncTaskLocalNoteSaved(d)
			data, err := os.ReadFile(d.path)
			if err != nil || string(data) != d.text() || !d.exists || d.dirty() {
				t.Fatalf("exact save failed: %q %v", data, err)
			}
		})
	}
}

func TestTaskLocalNoteExplicitEmptyCreateAndClear(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, d, "", false)
	if !d.autosave.RequestSave(d.key) {
		t.Fatal("explicit empty creation rejected")
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	s.syncTaskLocalNoteSaved(d)
	if data, err := os.ReadFile(d.path); err != nil || len(data) != 0 || !d.exists {
		t.Fatalf("empty creation failed: %q %v", data, err)
	}
	for _, text := range []string{"正文\r\n", ""} {
		setTaskLocalNoteText(t, s, d, text)
		if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
			t.Fatal(failures)
		}
		if data, err := os.ReadFile(d.path); err != nil || string(data) != text {
			t.Fatalf("exact clearing failed: %q %v", data, err)
		}
	}
}

func TestTaskLocalNoteRefreshNeverOverwritesDraft(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, d, "磁盘原文\r\n", true)
	setTaskLocalNoteText(t, s, d, "尚未保存的中文草稿\r\n\r\n")
	read := s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyInstructionReadResult(read, taskLocalNoteResult(d, "外部版本\n", true))
	if d.text() != "尚未保存的中文草稿\r\n\r\n" || d.hash != documents.Hash([]byte("磁盘原文\r\n")) || !d.autosave.Status(d.key).Conflict {
		t.Fatal("refresh replaced dirty source/baseline")
	}
	newer := s.taskLocalNoteAction(d, "instructions.read", true)
	s.applyInstructionReadResult(read, taskLocalNoteResult(d, "迟到旧版本", true))
	if d.text() != "尚未保存的中文草稿\r\n\r\n" {
		t.Fatal("late read replaced draft")
	}
	s.applyInstructionReadResult(newer, taskLocalNoteResult(d, "明确接受的文件版本\r\n", true))
	if d.text() != "明确接受的文件版本\r\n" || d.dirty() {
		t.Fatal("explicit disk acceptance failed")
	}
}

func TestTaskLocalNoteReadFailureCannotSaveBlank(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	read := s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyTaskLocalNoteRead(read, application.ActionResult{}, errors.New("fixture read denied"))
	if d.loaded || d.readError == "" {
		t.Fatal("first-read failure enabled save")
	}
	loadTaskLocalNote(s, d, "保留草稿", true)
	setTaskLocalNoteText(t, s, d, "继续编辑")
	read = s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyTaskLocalNoteRead(read, application.ActionResult{}, errors.New("fixture refresh denied"))
	if d.text() != "继续编辑" || !d.dirty() {
		t.Fatal("failed refresh lost draft")
	}
}

func TestTaskLocalNoteSaveAndTaskSwitchIsolation(t *testing.T) {
	s, a := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, a, "A原文\r\n", true)
	buffer := a.markdown.live.Buffer()
	setTaskLocalNoteText(t, s, a, "A草稿\r\n\r\n")
	if !s.leaveSettingsInstructionEditor() {
		t.Fatal(s.lastError)
	}
	s.taskUnit.state.selected = 1
	b := s.ensureTaskLocalNote()
	loadTaskLocalNote(s, b, "B原文", true)
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	if data, err := os.ReadFile(a.path); err != nil || string(data) != "A草稿\r\n\r\n" || b.text() != "B原文" {
		t.Fatalf("task switch mixed content: %q %v", data, err)
	}
	if !s.leaveSettingsInstructionEditor() {
		t.Fatal(s.lastError)
	}
	s.taskUnit.state.selected = 0
	if s.ensureTaskLocalNote() != a || a.markdown.live.Buffer() != buffer {
		t.Fatal("task switch discarded buffer/history")
	}
}

func TestTaskLocalNoteNativeInputKeepsUntouchedCRLF(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	source := "第一行\r\n混合换行\n末尾空格  \r\n\r\n"
	loadTaskLocalNote(s, d, source, true)
	tester := taskLocalNoteTester(s, 480)
	d.markdown.live.SetSelection(0, 0)
	d.markdown.live.Focus()
	tester.Frame()
	tester.Type("前缀😀")
	if err := s.captureTaskLocalNote(d); err != nil {
		t.Fatal(err)
	}
	if d.text() != "前缀😀"+source {
		t.Fatalf("input normalized untouched bytes: %q", d.text())
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	if data, _ := os.ReadFile(d.path); string(data) != "前缀😀"+source {
		t.Fatalf("save normalized line endings: %q", data)
	}
}

func TestTaskLocalNoteEditorLayout(t *testing.T) {
	for _, width := range []int{360, 1000} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, d := taskLocalNoteFixture(t)
				s.dark = dark
				loadTaskLocalNote(s, d, "", true)
				tester := taskLocalNoteTester(s, width)
				bounds, ok := tester.Find("任务说明 Markdown 编辑区")
				if !ok || bounds.H < 300 || bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y+bounds.H > 521 {
					t.Fatalf("public editor clipped: %+v", bounds)
				}
			})
		}
	}
}
