package desktop

import (
	"errors"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

// Explicit-save drafts live with the task UI, independently from the read-only
// Markdown readers and their former autosave engine.
type taskLocalNoteDraft struct {
	key, taskID, taskPath, createdAt, path          string
	view                                            *codeeditor.State
	baseline, hash                                  string
	exists, loaded, reading, saving, refreshPending bool
	readGeneration, saveGeneration                  uint64
	readError, saveError                            string
}

func (d *taskLocalNoteDraft) text() string { return d.view.Buffer.OriginalText() }
func (d *taskLocalNoteDraft) dirty() bool  { return d.loaded && d.text() != d.baseline }

func (s *app) ensureTaskLocalNote() *taskLocalNoteDraft {
	item := s.currentTask()
	if item.id == "" || item.path == "" {
		return nil
	}
	state := s.ensureTaskNotesUI()
	path := filepath.Join(item.path, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
	key := s.instructionAutosaveKey("task", item.id, "", path) + "\x00" + item.createdAt
	if state.localDrafts == nil {
		state.localDrafts = make(map[string]*taskLocalNoteDraft)
	}
	draft := state.localDrafts[key]
	if draft == nil {
		draft = &taskLocalNoteDraft{key: key, taskID: item.id, taskPath: item.path, createdAt: item.createdAt, path: path, refreshPending: true,
			view: &codeeditor.State{Buffer: codeeditor.NewBufferPreservingLineEndings(""), Label: "需求说明编辑框", SoftWrap: true, NoFolding: true, NoIndentGuides: true, NoAutoClose: true, NoScrollPastEnd: true, LineNumbers: codeeditor.LineNumbersOff}}
		state.localDrafts[key] = draft
	}
	return draft
}

func (s *app) taskLocalNoteCurrent(d *taskLocalNoteDraft) bool {
	if d == nil {
		return false
	}
	t := s.currentTask()
	return t.id == d.taskID && t.path == d.taskPath && t.createdAt == d.createdAt && s.taskNotesUI != nil && s.taskNotesUI.localDrafts[d.key] == d
}

func (s *app) taskLocalNoteAction(d *taskLocalNoteDraft, kind string, acceptDisk bool) application.Action {
	payload := map[string]any{"localTaskRules": true, "taskNoteKey": d.key, "taskPath": d.taskPath, "taskCreatedAt": d.createdAt,
		"noteTab": taskRulesNoteTab, "expectedHash": d.hash, "expectedExists": d.exists, "acceptDisk": acceptDisk}
	if kind == "instructions.read" {
		d.readGeneration++
		payload["instructionGeneration"] = d.readGeneration
	} else {
		d.saveGeneration++
		payload["instructionGeneration"] = d.saveGeneration
	}
	return application.Action{Kind: kind, TaskID: d.taskID, Path: workspace.AgentRulesFileName, Text: d.text(), Payload: payload}
}

func (s *app) requestTaskLocalNoteRead(d *taskLocalNoteDraft, acceptDisk bool) {
	if !s.taskLocalNoteCurrent(d) || s.busy || d.reading || d.saving {
		return
	}
	d.refreshPending = false
	d.reading = true
	d.readError = ""
	s.dispatch(s.taskLocalNoteAction(d, "instructions.read", acceptDisk))
}

func (s *app) saveTaskLocalNote(d *taskLocalNoteDraft) {
	if !s.taskLocalNoteCurrent(d) || !d.loaded || d.reading || d.saving || d.readError != "" || s.busy {
		return
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.lastError = "窗口或应用后端不可用，需求说明尚未保存"
		return
	}
	d.saving = true
	d.saveError = ""
	s.dispatch(s.taskLocalNoteAction(d, "instructions.save", false))
}

func (s *app) taskLocalNoteForAction(action application.Action) *taskLocalNoteDraft {
	if s.taskNotesUI == nil {
		return nil
	}
	d := s.taskNotesUI.localDrafts[payloadString(action.Payload, "taskNoteKey")]
	if d == nil || d.taskID != action.TaskID || d.taskPath != payloadString(action.Payload, "taskPath") || d.createdAt != payloadString(action.Payload, "taskCreatedAt") {
		return nil
	}
	return d
}

func (s *app) applyTaskLocalNoteRead(action application.Action, result application.ActionResult, readErr error) {
	d := s.taskLocalNoteForAction(action)
	if d == nil || instructionGeneration(action) != d.readGeneration {
		return
	}
	d.reading = false
	if readErr != nil {
		d.readError = readErr.Error()
		if s.taskLocalNoteCurrent(d) {
			s.lastError = d.readError
		}
		return
	}
	if !sameInstructionPath(result.Path, d.path) {
		d.readError = "任务说明路径已变化，请重新打开"
		return
	}
	d.readError = ""
	if d.dirty() && !dataBool(action.Payload, "acceptDisk") {
		if d.exists != dataBool(result.Data, "exists") || d.hash != dataString(result.Data, "hash") {
			d.saveError = documents.ErrConflict.Error()
		}
		return
	}
	d.view.Buffer = codeeditor.NewBufferPreservingLineEndings(result.Text)
	d.baseline, d.hash = result.Text, dataString(result.Data, "hash")
	d.exists, d.loaded = dataBool(result.Data, "exists"), true
	d.saveError = ""
}

func (s *app) applyTaskLocalNoteSave(action application.Action, result application.ActionResult, saveErr error) {
	d := s.taskLocalNoteForAction(action)
	if d == nil || instructionGeneration(action) != d.saveGeneration {
		return
	}
	d.saving = false
	if saveErr != nil {
		d.saveError = saveErr.Error()
		if s.taskLocalNoteCurrent(d) {
			s.lastError = d.saveError
		}
		return
	}
	if !sameInstructionPath(result.Path, d.path) || dataString(result.Data, "hash") == "" {
		d.saveError = "保存未返回有效文件版本，请重新读取确认"
		return
	}
	d.baseline, d.hash = action.Text, dataString(result.Data, "hash")
	d.exists, d.loaded = true, true
	d.saveError = ""
	if s.taskLocalNoteCurrent(d) {
		s.lastMessage = "需求说明已保存"
	} else {
		s.lastMessage = ""
	}
}

func (s *app) reloadTaskLocalNote(d *taskLocalNoteDraft) {
	if !d.dirty() {
		s.requestTaskLocalNoteRead(d, false)
		return
	}
	version := d.view.Buffer.Version()
	s.showConfirmationDialog("重新读取需求说明", "放弃此任务尚未保存的修改，读取本地文件？", "", []confirmationDialogButton{{label: "取消"}, {label: "重新读取", primary: true}}, 0, 0, func(choice int) {
		if choice == 1 && s.taskLocalNoteCurrent(d) && d.view.Buffer.Version() == version {
			s.requestTaskLocalNoteRead(d, true)
		}
	})
}

func (s *app) taskLocalNotesCloseError() error {
	if s.taskNotesUI != nil {
		for _, d := range s.taskNotesUI.localDrafts {
			if d.dirty() || d.saving {
				return errors.New("需求说明仍有未保存的修改，请先保存后再关闭；各任务的草稿已保留")
			}
		}
	}
	return nil
}

func (s *app) renderTaskLocalNote(c *ui.Context, p palette) {
	d := s.ensureTaskLocalNote()
	if d == nil {
		return
	}
	s.noteTab = taskRulesNoteTab
	state := s.ensureTaskNotesUI()
	if state.localActiveKey != d.key {
		state.localActiveKey = d.key
		d.refreshPending = true
	}
	if d.refreshPending && !s.busy {
		s.requestTaskLocalNoteRead(d, false)
	}
	save := func() { s.saveTaskLocalNote(d) }
	c.OnShortcut(ui.Cmd, ui.KeyS, save)
	ui.Column(c).Grow(1).MinHeight(0).Padding(18).Gap(8).Background(p.surface).Children(func() {
		ui.Row(c).FillWidth().Height(32).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
			status := "已保存"
			switch {
			case d.reading:
				status = "读取中…"
			case d.readError != "":
				status = "读取失败"
			case d.saving:
				status = "保存中…"
			case d.dirty() || !d.exists:
				status = "未保存"
			}
			label(c, status, 11, p.muted).NoWrap()
			ui.Spacer(c)
			button := ui.ButtonBase(c.Key("task-note-save")).Height(30).Padding(0, 12).Radius(8).Background(p.selected).Label("保存需求说明").Tooltip("保存 · Ctrl+S").Disabled(!d.loaded || d.reading || d.saving || d.readError != "" || s.busy)
			button.Children(func() { label(c, "保存", 12, p.blue).NoWrap() })
			if button.Clicked() {
				save()
			}
			s.overflowMenu(c, p, "local-notes-more-"+d.taskID, "更多文档操作", s.busy || d.reading || d.saving, func(menu *overflowMenuBuilder) {
				if menu.Item("reload", "重新读取说明").Icon("refresh").Chosen() {
					s.reloadTaskLocalNote(d)
				}
				if menu.Item("copy", "复制说明").Icon("copy").Disabled(!d.loaded).Chosen() {
					s.copyTextToClipboard(c.Services(), d.text(), "已复制任务说明")
				}
				if menu.Item("reveal", "显示文件位置").Icon("folder").Disabled(!d.exists || s.systemRevealBusy()).Chosen() {
					s.act("system.reveal", d.taskID, "", "", d.path, "", nil)
				}
			})
		})
		if message := firstNonEmpty(d.readError, d.saveError); message != "" {
			label(c, message, 12, p.amber).Wrap()
		}
		d.view.ReadOnly = !d.loaded || d.readError != ""
		d.view.ClipboardWrite = func(text string) bool { return s.copyTextToClipboard(c.Services(), text, "") }
		ui.Box(c).Grow(1).FillWidth().MinHeight(0).Padding(6).Border(1, p.line).Radius(8).Children(func() { codeeditor.View(c, d.view).Fill() })
	})
}
