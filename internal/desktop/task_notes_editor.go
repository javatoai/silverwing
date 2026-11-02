package desktop

import (
	"errors"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

// Each task retains its public editor and shared autosave identity. The legacy
// view is only a Buffer alias for existing source/lifecycle consumers.
type taskLocalNoteDraft struct {
	key, taskID, taskPath, createdAt, path          string
	view                                            *codeeditor.State
	markdown                                        *markdownEditorState
	autosave                                        *editor.Autosave
	baseline, hash                                  string
	exists, loaded, reading, saving, refreshPending bool
	readGeneration, saveGeneration                  uint64
	readError, saveError                            string
}

func (d *taskLocalNoteDraft) text() string {
	if d.markdown != nil && d.markdown.live != nil {
		return d.markdown.live.Buffer().Snapshot().OriginalText()
	}
	return d.view.Buffer.OriginalText()
}

func (d *taskLocalNoteDraft) dirty() bool {
	if !d.loaded {
		return false
	}
	if d.autosave != nil {
		content, ok := d.autosave.Content(d.key)
		return d.autosave.Status(d.key).Dirty || ok && d.text() != content
	}
	return d.text() != d.baseline
}

func (s *app) ensureTaskLocalNote() *taskLocalNoteDraft {
	item := s.taskUnit.currentTask()
	if item.id == "" || item.path == "" {
		return nil
	}
	if s.materialsUnit.state.documentSaveRefs == nil {
		s.materialsUnit.state.documentSaveRefs = map[string]documentSaveRef{}
	}
	if s.materialsUnit.state.markdownAutosave == nil {
		s.initMarkdownAutosave()
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
			view: &codeeditor.State{Buffer: codeeditor.NewBufferPreservingLineEndings(""), Label: "需求说明编辑框"}}
		state.localDrafts[key] = draft
	}
	draft.autosave = s.materialsUnit.state.markdownAutosave
	s.syncTaskLocalNoteSaved(draft)
	return draft
}

func (s *app) taskLocalNoteCurrent(d *taskLocalNoteDraft) bool {
	if d == nil {
		return false
	}
	t := s.taskUnit.currentTask()
	return t.id == d.taskID && t.path == d.taskPath && t.createdAt == d.createdAt && s.taskUnit.state.taskNotesUI != nil && s.taskUnit.state.taskNotesUI.localDrafts[d.key] == d
}

func (s *app) syncTaskLocalNoteSaved(d *taskLocalNoteDraft) {
	if d == nil || d.autosave == nil {
		return
	}
	if ref, ok := s.instructionSaveRef(d.key); ok && ref.localTaskRules {
		d.hash, d.exists = ref.hash, ref.expectedExists
		status := d.autosave.Status(d.key)
		d.saving, d.saveError = status.Saving, status.Error
		if !status.Dirty {
			d.baseline, _ = d.autosave.Content(d.key)
		}
	}
}

func (s *app) taskLocalNoteAction(d *taskLocalNoteDraft, kind string, acceptDisk bool) application.Action {
	s.syncTaskLocalNoteSaved(d)
	payload := map[string]any{"localTaskRules": true, "taskNoteKey": d.key, "taskPath": d.taskPath, "taskCreatedAt": d.createdAt,
		"noteTab": taskRulesNoteTab, "expectedHash": d.hash, "expectedExists": d.exists, "acceptDisk": acceptDisk}
	if kind == "instructions.read" {
		d.readGeneration++
		payload["instructionGeneration"] = d.readGeneration
		if d.markdown != nil && d.markdown.live != nil {
			payload["instructionBufferVersion"] = d.markdown.live.Buffer().Version()
		}
		if ref, ok := s.instructionSaveRef(d.key); ok {
			payload["instructionSaveVersion"], payload["instructionBaselineHash"] = ref.saveVersion, ref.hash
		}
	} else {
		d.saveGeneration++
		payload["instructionGeneration"] = d.saveGeneration
	}
	return application.Action{Kind: kind, TaskID: d.taskID, Path: workspace.AgentRulesFileName, Text: d.text(), Payload: payload}
}

func (s *app) requestTaskLocalNoteRead(d *taskLocalNoteDraft, acceptDisk bool) {
	if !s.taskLocalNoteCurrent(d) || s.busy || d.reading {
		return
	}
	if err := s.captureTaskLocalNote(d); err != nil {
		d.readError = err.Error()
		return
	}
	d.refreshPending, d.reading, d.readError = false, true, ""
	s.dispatch(s.taskLocalNoteAction(d, "instructions.read", acceptDisk))
}

func (s *app) saveTaskLocalNote(d *taskLocalNoteDraft) {
	if !s.taskLocalNoteCurrent(d) || !d.loaded || d.autosave == nil {
		return
	}
	if err := s.captureTaskLocalNote(d); err != nil {
		d.saveError = err.Error()
		return
	}
	if ref, ok := s.instructionSaveRef(d.key); ok && !ref.expectedExists {
		d.autosave.RequestSave(d.key)
	}
	s.flushMarkdownAutosave(d.key)
}

func (s *app) taskLocalNoteForAction(action application.Action) *taskLocalNoteDraft {
	if s.taskUnit.state.taskNotesUI == nil {
		return nil
	}
	d := s.taskUnit.state.taskNotesUI.localDrafts[payloadString(action.Payload, "taskNoteKey")]
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
		return
	}
	if !sameInstructionPath(result.Path, d.path) {
		d.readError = "任务说明路径已变化，请重新打开"
		return
	}
	if d.markdown != nil && d.markdown.live != nil {
		live := d.markdown.live
		if live.Composing() || action.Payload["instructionBufferVersion"] != nil && live.Buffer().Version() != payloadUint64(action.Payload, "instructionBufferVersion") {
			d.refreshPending = !d.loaded
			return
		}
		if s.markdownEditorIsCurrent(d.markdown) {
			s.observeSettingsInstructionInput(d.markdown)
		}
	}
	d.readError = ""
	content := result.Text
	acceptDisk := dataBool(action.Payload, "acceptDisk")
	if d.autosave != nil {
		s.materialsUnit.state.documentSaveMu.Lock()
		old, exists := s.materialsUnit.state.documentSaveRefs[d.key]
		if _, captured := action.Payload["instructionSaveVersion"]; captured && (!exists || old.saveVersion != payloadUint64(action.Payload, "instructionSaveVersion") || old.hash != payloadString(action.Payload, "instructionBaselineHash")) || d.autosave.Status(d.key).Saving {
			s.materialsUnit.state.documentSaveMu.Unlock()
			d.refreshPending = !d.loaded
			return
		}
		if acceptDisk {
			d.autosave.AcceptDisk(d.key, result.Text, true)
		} else {
			d.autosave.Seed(d.key, result.Text)
		}
		content, _ = d.autosave.Content(d.key)
		ref := documentSaveRef{kind: "instruction", scope: "task", localTaskRules: true, taskID: d.taskID, taskPath: d.taskPath,
			createdAt: d.createdAt, path: d.path, hash: dataString(result.Data, "hash"), expectedExists: dataBool(result.Data, "exists"),
			hasUTF8BOM: dataBool(result.Data, "hasUtf8Bom"), newline: dataString(result.Data, "newline"), saveVersion: old.saveVersion}
		if exists && d.autosave.Status(d.key).Dirty && !acceptDisk {
			ref.hash, ref.expectedExists, ref.overwrite = old.hash, old.expectedExists, old.overwrite
			if old.hash != dataString(result.Data, "hash") || old.expectedExists != dataBool(result.Data, "exists") {
				d.autosave.MarkConflict(d.key, content, documents.ErrConflict.Error())
			}
		}
		s.materialsUnit.state.documentSaveRefs[d.key] = ref
		s.materialsUnit.state.documentSaveMu.Unlock()
	} else if d.dirty() && !acceptDisk {
		if d.exists != dataBool(result.Data, "exists") || d.hash != dataString(result.Data, "hash") {
			d.saveError = documents.ErrConflict.Error()
		}
		return
	}
	if d.markdown != nil {
		s.resetInstructionMarkdownEditor(d.markdown, content)
		d.view.Buffer = d.markdown.live.Buffer()
	} else {
		d.view.Buffer = codeeditor.NewBufferPreservingLineEndings(content)
	}
	d.baseline, d.hash, d.exists, d.loaded, d.saveError = result.Text, dataString(result.Data, "hash"), dataBool(result.Data, "exists"), true, ""
	s.syncTaskLocalNoteSaved(d)
}

func (s *app) applyTaskLocalNoteSave(action application.Action, result application.ActionResult, saveErr error) {
	d := s.taskLocalNoteForAction(action)
	if d == nil || instructionGeneration(action) != d.saveGeneration {
		return
	}
	d.saving = false
	if saveErr != nil {
		d.saveError = saveErr.Error()
		return
	}
	if !sameInstructionPath(result.Path, d.path) || dataString(result.Data, "hash") == "" {
		d.saveError = "保存未返回有效文件版本，请重新读取确认"
		return
	}
	d.baseline, d.hash, d.exists, d.loaded, d.saveError = action.Text, dataString(result.Data, "hash"), true, true, ""
}

func (s *app) captureTaskLocalNote(d *taskLocalNoteDraft) error {
	if d == nil || !d.loaded || d.markdown == nil || d.markdown.live == nil {
		return nil
	}
	if s.markdownEditorIsCurrent(d.markdown) {
		return s.captureSettingsInstructionInput(d.markdown)
	}
	// Snapshot replacement can invalidate the visible task before presentation
	// leaves it. Capture only this still-active owner into its original key.
	if notes := s.taskUnit.state.taskNotesUI; notes != nil && notes.localActiveKey == d.key {
		snapshot, err := d.markdown.componentEditor.CaptureInput()
		if err != nil {
			return err
		}
		d.markdown.doc.SyncExternal(snapshot.Source)
		d.markdown.liveSource, d.markdown.liveVersion = snapshot.Source, snapshot.Version
		s.editInstructionAutosave(d.key, snapshot.Source)
		s.setInstructionComposing(d.key, false)
	}
	return nil
}

func (s *app) rebindTaskLocalNote(d *taskLocalNoteDraft, key, taskPath, path string) {
	notes := s.ensureTaskNotesUI()
	oldKey := d.key
	delete(notes.localDrafts, oldKey)
	delete(notes.markdownEditors, oldKey)
	d.key, d.taskPath, d.path, d.reading, d.refreshPending = key, taskPath, path, false, true
	d.readGeneration++
	d.saveGeneration++
	if state := d.markdown; state != nil {
		view := state.componentEditor.GetViewState()
		closeMarkdownPreviewAssets(state)
		state.path, state.linkRoot, state.binding.id = path, filepath.Dir(path), key
		state.liveFramework = nil
		state.componentEditor = markdownui.AdoptEditorState(markdownui.Document{ID: key, Path: path}, state.live)
		view.DocumentID = key
		_ = state.componentEditor.RestoreViewState(view)
		d.view.Buffer = state.live.Buffer()
		notes.markdownEditors[key] = state
	}
	notes.localDrafts[key] = d
	if notes.activeKey == oldKey {
		notes.activeKey = key
	}
	if notes.localActiveKey == oldKey {
		notes.localActiveKey = key
	}
	s.syncTaskLocalNoteSaved(d)
}

func (s *app) captureTaskLocalNoteInput() error {
	if notes := s.taskUnit.state.taskNotesUI; notes != nil {
		return s.captureTaskLocalNote(notes.localDrafts[notes.localActiveKey])
	}
	return nil
}

func (s *app) reloadTaskLocalNote(d *taskLocalNoteDraft) {
	if err := s.captureTaskLocalNote(d); err != nil {
		d.readError = err.Error()
		return
	}
	if !d.dirty() {
		s.requestTaskLocalNoteRead(d, false)
		return
	}
	version := d.view.Buffer.Version()
	s.showConfirmationDialog("重新读取任务说明", "放弃此任务尚未保存的修改，读取本地文件？", "", []confirmationDialogButton{{label: "取消"}, {label: "重新读取", primary: true}}, 0, 0, func(choice int) {
		if choice == 1 && s.taskLocalNoteCurrent(d) && d.view.Buffer.Version() == version {
			s.requestTaskLocalNoteRead(d, true)
		}
	})
}

func (s *app) taskLocalNotesCloseError() error {
	if err := s.captureTaskLocalNoteInput(); err != nil {
		return err
	}
	if notes := s.taskUnit.state.taskNotesUI; notes != nil {
		for _, d := range notes.localDrafts {
			if d.markdown != nil && d.markdown.live != nil && d.markdown.live.Composing() {
				return editor.ErrComposing
			}
			if d.autosave == nil && (d.dirty() || d.saving) {
				return errors.New("任务说明仍有未保存的修改，请先保存后再关闭")
			}
		}
	}
	return nil
}

func (s *app) ensureTaskLocalMarkdown(d *taskLocalNoteDraft) *markdownEditorState {
	if d.markdown != nil {
		return d.markdown
	}
	state := &markdownEditorState{taskID: d.taskID, path: d.path, linkRoot: filepath.Dir(d.path), doc: editor.NewDocument(d.text(), d.key), remoteImages: true,
		diagrams: map[uint64]*markdownDiagramState{}, inlineImages: map[string]*markdownInlineImageState{}, richFields: map[uint64]*markdownRichFieldState{}}
	state.binding = &markdownEditorBinding{id: d.key, label: "任务说明 Markdown 编辑区"}
	state.binding.isCurrent = func() bool {
		notes := s.taskUnit.state.taskNotesUI
		return s.taskLocalNoteCurrent(d) && notes.activeKey == d.key && notes.activeMarkdown == state && notes.localActiveKey == d.key && s.page == "tasks" && s.tab == 3
	}
	state.binding.commit = func(source string) {
		if state.binding.isCurrent() {
			s.ensureTaskNotesUI().activeContent = source
			s.editInstructionAutosave(d.key, source)
		}
	}
	state.binding.setComposing = func(composing bool) {
		if state.binding.isCurrent() {
			s.ensureTaskNotesUI().activeComposing = composing
			s.setInstructionComposing(d.key, composing)
		}
	}
	state.binding.save = func() { s.saveTaskLocalNote(d) }
	d.markdown = state
	notes := s.ensureTaskNotesUI()
	if notes.markdownEditors == nil {
		notes.markdownEditors = map[string]*markdownEditorState{}
	}
	notes.markdownEditors[d.key] = state
	s.ensureSettingsInstructionLiveState(state)
	if state.live != nil {
		d.view.Buffer = state.live.Buffer()
	}
	return state
}

func (s *app) renderTaskLocalNote(c *ui.Context, p palette) {
	d := s.ensureTaskLocalNote()
	if d == nil {
		return
	}
	s.taskUnit.state.noteTab = taskRulesNoteTab
	notes := s.ensureTaskNotesUI()
	if notes.localActiveKey != d.key {
		if !s.leaveSettingsInstructionEditor() {
			return
		}
		notes.localActiveKey, d.refreshPending = d.key, true
	}
	if d.refreshPending && !s.busy {
		s.requestTaskLocalNoteRead(d, false)
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(18).Gap(8).Background(p.surface).Children(func() {
		ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
			if d.loaded {
				s.renderInstructionSaveStatus(c, p, d.key)
			}
			ui.Spacer(c)
			s.overflowMenu(c, p, "local-notes-more-"+d.taskID, "更多文档操作", false, func(menu *overflowMenuBuilder) {
				if menu.Item("save", "立即保存 · Ctrl+S").Icon("save").Disabled(!d.loaded).Chosen() {
					s.saveTaskLocalNote(d)
				}
				if menu.Item("reload", "重新读取说明").Icon("refresh").Disabled(s.busy || d.reading).Chosen() {
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
		if d.readError != "" {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				label(c, d.readError, 12, p.amber).Grow(1).Wrap()
				if action(c, "task-notes-read-retry", "refresh", "重试读取", p, false) {
					s.requestTaskLocalNoteRead(d, false)
				}
			})
		}
		if !d.loaded {
			label(c, "正在读取任务说明…", 12, p.muted)
			return
		}
		notes.activeKey, notes.activeKind, notes.activeTab, notes.activeContent = d.key, "task-local", taskRulesNoteTab, d.text()
		state := s.ensureTaskLocalMarkdown(d)
		notes.activeMarkdown = state
		s.renderSettingsInstructionMarkdown(c, p, state, 0)
	})
}
