package desktop

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

func taskLocalNoteFixture(t *testing.T) (*app, *taskLocalNoteDraft) {
	t.Helper()
	root := t.TempDir()
	s := &app{page: "tasks", tab: 3, tasks: []task{{id: "task-a", path: filepath.Join(root, "task-a"), createdAt: "created-a"}, {id: "task-b", path: filepath.Join(root, "task-b"), createdAt: "created-b"}}}
	s.snapshot.Home = filepath.Join(root, "home")
	d := s.ensureTaskLocalNote()
	s.taskNotesUI.localActiveKey = d.key
	d.refreshPending = false
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
	action := s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyInstructionReadResult(action, taskLocalNoteResult(d, text, exists))
}

func taskLocalNoteTester(s *app, width int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, s.dark)
		ui.Column(c).Fill().Children(func() { s.renderTaskNotes(c, p) })
	}, width, 520)
}

func TestTaskLocalNoteAlwaysShowsEditableSource(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		exists     bool
	}{{"missing", "", false}, {"empty", "", true}, {"original", "中文原文\r\n混合换行\n尾部空格  \r\n\r\n", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, d := taskLocalNoteFixture(t)
			loadTaskLocalNote(s, d, tc.text, tc.exists)
			tester := taskLocalNoteTester(s, 480)
			if _, ok := tester.Find("需求说明编辑框"); !ok || d.view.ReadOnly || d.text() != tc.text {
				t.Fatalf("page did not expose complete editable file source: %q", d.text())
			}
			if _, ok := tester.Find("保存需求说明"); !ok || tester.HasText("进入编辑") || tester.HasText("暂无任务说明") || tester.HasText("只读阅读") || tester.HasText("模板") {
				t.Fatal("local editor added a read mode or filled an empty document")
			}
			if err := tester.Click("需求说明编辑框"); err != nil {
				t.Fatal(err)
			}
			tester.Key(ui.Ctrl, ui.KeyA)
			tester.Type("手动填写中文 😀")
			if d.text() != "手动填写中文 😀" || !d.dirty() || !tester.HasText("未保存") {
				t.Fatalf("native editing did not retain an unsaved draft: %q", d.text())
			}
			if err := tester.Click("保存需求说明"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(s.lastError, "尚未保存") || d.saving {
				t.Fatal("save action failed to validate the unavailable window/backend")
			}
		})
	}
}

func TestTaskLocalNoteRefreshNeverOverwritesDraft(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, d, "磁盘原文\r\n", true)
	d.view.Buffer.Set("尚未保存的中文草稿\r\n\r\n")
	read := s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyInstructionReadResult(read, taskLocalNoteResult(d, "外部版本\n", true))
	if d.text() != "尚未保存的中文草稿\r\n\r\n" || d.hash != documents.Hash([]byte("磁盘原文\r\n")) || d.saveError == "" {
		t.Fatal("refresh overwrote a dirty source or adopted the conflicting baseline")
	}
	older := read
	newer := s.taskLocalNoteAction(d, "instructions.read", true)
	s.applyInstructionReadResult(older, taskLocalNoteResult(d, "迟到旧版本", true))
	if d.text() != "尚未保存的中文草稿\r\n\r\n" {
		t.Fatal("late read replaced the draft")
	}
	s.applyInstructionReadResult(newer, taskLocalNoteResult(d, "明确接受的文件版本\r\n", true))
	if d.text() != "明确接受的文件版本\r\n" || d.dirty() || d.saveError != "" {
		t.Fatal("explicit disk acceptance did not replace the draft")
	}
}

func TestTaskLocalNoteReadFailureCannotSaveBlank(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	read := s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyInstructionReadFailure(read, errors.New("fixture permission denied"))
	tester := taskLocalNoteTester(s, 480)
	if !d.view.ReadOnly || d.loaded || !tester.HasText("读取失败") || !tester.HasText("fixture permission denied") {
		t.Fatal("read error masqueraded as an editable empty file")
	}
	_ = tester.Click("保存需求说明")
	if d.saving || d.saveGeneration != 0 {
		t.Fatal("read failure allowed a blank overwrite")
	}
	loadTaskLocalNote(s, d, "原文", true)
	d.view.Buffer.Set("保留草稿")
	read = s.taskLocalNoteAction(d, "instructions.read", false)
	s.applyInstructionReadFailure(read, errors.New("fixture refresh denied"))
	if d.text() != "保留草稿" || !d.dirty() {
		t.Fatal("failed refresh lost the previous draft")
	}
}

func TestTaskLocalNoteSaveAndTaskSwitchIsolation(t *testing.T) {
	s, a := taskLocalNoteFixture(t)
	loadTaskLocalNote(s, a, "A原文\r\n", true)
	a.view.Buffer.Set("A保存的完整草稿\r\n\r\n")
	save := s.taskLocalNoteAction(a, "instructions.save", false)
	a.saving = true
	if save.Text != a.text() || save.TaskID != "task-a" || payloadString(save.Payload, "expectedHash") != documents.Hash([]byte("A原文\r\n")) {
		t.Fatal("save did not capture the original task/path/hash and exact source")
	}
	a.view.Buffer.Set("保存开始后继续编辑的 A 草稿")
	s.deactivateInstructionEditor()
	s.selected = 1
	b := s.ensureTaskLocalNote()
	loadTaskLocalNote(s, b, "B原文", true)
	s.applyInstructionSaveResult(save, taskLocalNoteResult(a, save.Text, true))
	if b.text() != "B原文" || a.text() != "保存开始后继续编辑的 A 草稿" || !a.dirty() || s.lastMessage != "" {
		t.Fatal("late save changed another task or overwrote edits made during saving")
	}
	if s.documentCloseReady() == nil {
		t.Fatal("unsaved inactive-task draft did not block close")
	}
	s.selected = 0
	if s.ensureTaskLocalNote() != a {
		t.Fatal("returning to a task discarded its retained native buffer")
	}
	a.view.Buffer.Set("")
	empty := s.taskLocalNoteAction(a, "instructions.save", false)
	if empty.Text != "" || !dataBool(empty.Payload, "expectedExists") {
		t.Fatal("explicit clearing substituted content or lost its baseline")
	}
	s.applyInstructionSaveResult(empty, taskLocalNoteResult(a, "", true))
	if a.dirty() || !a.exists || s.documentCloseReady() != nil {
		t.Fatal("successful explicit empty save stayed dirty")
	}
	late := s.taskLocalNoteAction(a, "instructions.save", false)
	_ = s.taskLocalNoteAction(a, "instructions.save", false)
	a.saving = true
	s.applyInstructionSaveResult(late, taskLocalNoteResult(a, "wrong", true))
	if !a.saving || a.baseline != "" {
		t.Fatal("an older save reply settled a newer save")
	}
}

func TestTaskLocalNoteNativeInputKeepsUntouchedCRLF(t *testing.T) {
	s, d := taskLocalNoteFixture(t)
	source := "第一行\r\n混合换行\n末尾空格  \r\n\r\n"
	loadTaskLocalNote(s, d, source, true)
	tester := taskLocalNoteTester(s, 480)
	if err := tester.Click("需求说明编辑框"); err != nil {
		t.Fatal(err)
	}
	d.view.Select(0, 0)
	tester.Frame()
	tester.Type("前缀😀")
	if d.text() != "前缀😀"+source {
		t.Fatalf("native input normalized untouched source bytes: %q", d.text())
	}
	s.lastError = ""
	tester.Key(ui.Ctrl, ui.KeyS)
	if !strings.Contains(s.lastError, "尚未保存") {
		t.Fatal("existing Ctrl+S did not reach the explicit save action")
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
				editor, ok := tester.Find("需求说明编辑框")
				save, saveOK := tester.Find("保存需求说明")
				if !ok || !saveOK || editor.H < 350 || editor.X < 0 || editor.Y <= save.Y || editor.X+editor.W > float32(width) || editor.Y+editor.H > 520 || save.X+save.W > float32(width) || save.H > 32 {
					t.Fatalf("editor or compact save was clipped: editor=%+v save=%+v", editor, save)
				}
			})
		}
	}
}
