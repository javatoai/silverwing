package desktop

import (
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

// taskNotesUIState owns instruction readers and task-local explicit-save
// drafts. The latter keep their native buffers across task navigation.
type taskNotesUIState struct {
	taskID                             string
	tab                                int
	taskGeneration                     uint64
	taskRequestGeneration              uint64
	taskRequestPending                 bool
	taskReadDeferred                   bool
	taskReadAcceptDisk                 bool
	taskReadError                      string
	settingsGeneration                 uint64
	settingsRequestGeneration          uint64
	settingsRequestPending             bool
	settingsReadDeferred               bool
	settingsReadAcceptDisk             bool
	settingsReadError                  string
	settingsTemplatesLoading           bool
	settingsTemplatesError             string
	settingsTemplatesRequestGeneration uint64
	activeKey                          string
	activeKind                         string
	activeTab                          int
	activeContent                      string
	activeComposing                    bool
	activeMarkdown                     *markdownEditorState
	markdownEditors                    map[string]*markdownEditorState
	localDrafts                        map[string]*taskLocalNoteDraft
	localActiveKey                     string
}

var taskNoteNames = []string{"AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md"}

const taskRulesNoteTab = 2

func (s *app) ensureTaskNotesUI() *taskNotesUIState {
	if s.taskUnit.state.taskNotesUI == nil {
		s.taskUnit.state.taskNotesUI = &taskNotesUIState{tab: -1, activeTab: -1}
	}
	return s.taskUnit.state.taskNotesUI
}

func (s *app) renderTaskNotes(c *ui.Context, p palette) {
	s.renderTaskLocalNote(c, p)
}

func (s *app) renderTaskNoteMenuItems(c *ui.Context, menu *overflowMenuBuilder, selected int) {
	if selected < 0 || selected >= len(s.taskUnit.state.notePaths) {
		return
	}
	state := s.ensureTaskNotesUI()
	path := s.taskUnit.state.notePaths[selected]
	reloadLabel := "重新读取说明"
	if state.taskRequestPending {
		reloadLabel = "正在读取…"
	} else if state.taskReadDeferred {
		reloadLabel = "等待当前操作结束…"
	}
	if menu.Item("reload", reloadLabel).Icon("refresh").Disabled(s.busy || state.taskRequestPending || state.taskReadDeferred).Chosen() {
		s.requestTaskInstructionRead(false)
	}
	if menu.Item("reveal", "显示文件位置").Icon("folder").Disabled(path == "" || s.systemRevealBusy()).Chosen() {
		s.act("system.reveal", s.taskUnit.currentTask().id, "", "", path, "", nil)
	}
	if menu.Item("copy", "复制说明").Icon("copy").Disabled(path == "").Chosen() {
		s.copyTextToClipboard(c.Services(), s.taskUnit.state.notes[selected], "已复制任务说明")
	}
	menu.Separator()
	menu.Item("read-only", "只读阅读").Disabled(true).Chosen()
	if state.taskReadError != "" {
		menu.Item("read-error", "读取失败："+state.taskReadError).Disabled(true).Chosen()
	}
}

func (s *app) renderInstructionTextArea(c *ui.Context, p palette, key, kind string, tab int, content *string, height float32) {
	state := s.ensureTaskNotesUI()
	if state.activeKey != key {
		s.deactivateInstructionEditor()
		state.activeKey, state.activeKind, state.activeTab = key, kind, tab
	}
	state.activeContent = *content
	markdown := s.ensureInstructionMarkdownEditor(key, kind, tab, *content)
	state.activeMarkdown = markdown
	if markdown.readOnly {
		reader := ui.Column(c).FillWidth().MinHeight(0)
		if height > 0 {
			reader = reader.Height(height)
		} else {
			reader = reader.Grow(1)
		}
		reader.Children(func() {
			if kind == "task" {
				s.renderReadonlyMarkdownReaderWithMenu(c, p, markdown, func(menu *overflowMenuBuilder) {
					s.renderTaskNoteMenuItems(c, menu, tab)
				})
			} else if kind == "settings" {
				s.renderReadonlyMarkdownReaderContent(c, p, markdown)
			} else {
				s.renderReadonlyMarkdownReader(c, p, markdown)
			}
		})
		return
	}
	s.renderInstructionMarkdown(c, p, markdown, height)
	s.commitMarkdownEditorSource(markdown, markdown.doc.Source())
}

func (s *app) renderInstructionSaveStatus(c *ui.Context, p palette, key string) {
	if markdownReadingOnly || s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	status := s.materialsUnit.state.markdownAutosave.Status(key)
	labelText := markdownSaveLabel(status)
	if labelText == "" {
		labelText = "已自动保存"
	}
	color := p.muted
	if status.Conflict || status.Error != "" {
		color = p.amber
	}
	label(c, labelText, 10, color).Tooltip(labelText)
	if status.Conflict {
		if action(c, "instruction-accept-disk", "refresh", "接受外部版本", p, false) {
			s.requestInstructionDiskVersion(key)
		}
		if action(c, "instruction-overwrite", "save", "确认覆盖", p, false) {
			s.overwriteMarkdownAfterConflict(key)
		}
	} else if status.Error != "" {
		if action(c, "instruction-retry", "refresh", "重试保存", p, false) {
			s.retryMarkdownAutosave(key)
		}
	}
}

func (s *app) requestTaskInstructionRead(acceptDisk bool) {
	state := s.ensureTaskNotesUI()
	task := s.taskUnit.currentTask()
	if task.id == "" || s.taskUnit.state.noteTab < 0 || s.taskUnit.state.noteTab >= len(taskNoteNames) {
		return
	}
	if s.busy {
		state.taskReadDeferred = true
		state.taskReadAcceptDisk = acceptDisk
		return
	}
	state.taskReadDeferred = false
	state.taskGeneration++
	state.taskRequestGeneration = state.taskGeneration
	state.taskRequestPending = true
	state.taskReadError = ""
	payload := map[string]any{"noteTab": s.taskUnit.state.noteTab, "instructionGeneration": state.taskRequestGeneration}
	if acceptDisk {
		payload["acceptDisk"] = true
	}
	s.act("instructions.read", task.id, "", "", taskNoteNames[s.taskUnit.state.noteTab], "", payload)
}

func (s *app) requestSettingsInstructionRead(acceptDisk bool) {
	state := s.ensureTaskNotesUI()
	scope, groupID := s.settingsInstructionScope()
	if s.busy {
		state.settingsReadDeferred = true
		state.settingsReadAcceptDisk = acceptDisk
		return
	}
	state.settingsReadDeferred = false
	state.settingsGeneration++
	state.settingsRequestGeneration = state.settingsGeneration
	state.settingsRequestPending = true
	state.settingsReadError = ""
	payload := map[string]any{"scope": scope, "instructionGeneration": state.settingsRequestGeneration}
	if scope == "group" {
		payload["groupId"] = groupID
	}
	if acceptDisk {
		payload["acceptDisk"] = true
	}
	s.act("instructions.read", "", "", "", "AGENTS.md", "", payload)
}

func (s *app) settingsInstructionScope() (scope, groupID string) {
	if s.settingsUnit.state.settingsAgentScope == "global" {
		return "global", ""
	}
	groupID = s.settingsUnit.state.settingsAgentGroupID
	if groupID == "" {
		groupID = s.settingsUnit.state.settingsAgentScope
	}
	return "group", groupID
}

func (s *app) requestInstructionDiskVersion(key string) {
	ref, ok := s.instructionSaveRef(key)
	if !ok {
		return
	}
	if ref.scope == "group" || ref.scope == "global" {
		s.requestSettingsInstructionRead(true)
		return
	}
	for index, path := range s.taskUnit.state.notePaths {
		if sameInstructionPath(path, ref.path) {
			s.taskUnit.state.noteTab = index
			s.ensureTaskNotesUI().tab = index
			s.requestTaskInstructionRead(true)
			return
		}
	}
}

func (s *app) applyInstructionReadResult(action application.Action, result application.ActionResult) {
	if dataBool(action.Payload, "localTaskRules") {
		s.applyTaskLocalNoteRead(action, result, nil)
		return
	}
	state := s.ensureTaskNotesUI()
	path := result.Path
	if path == "" {
		path = action.Path
	}
	scope, taskID, groupID := instructionIdentity(action, path)
	generation := instructionGeneration(action)
	if scope == "global" || scope == "group" {
		contextKey := scope + ":" + payloadString(action.Payload, "groupId")
		if contextKey != s.settingsUnit.state.settingsAgentContext || s.settingsUnit.state.settingsPage != "agents" ||
			(generation != 0 && generation != state.settingsRequestGeneration) {
			return
		}
	} else {
		index := s.noteIndexForAction(action)
		if index < 0 || index >= len(s.taskUnit.state.notes) || taskID == "" || taskID != s.taskUnit.currentTask().id || index != s.taskUnit.state.noteTab {
			return
		}
		if generation != 0 && generation != state.taskRequestGeneration {
			return
		}
	}
	key := s.instructionAutosaveKey(scope, taskID, groupID, path)
	s.preserveActiveInstructionDraft(key)
	key, content, readOnly := s.seedInstructionAutosave(action, result)
	if key == "" {
		return
	}
	if scope == "global" || scope == "group" {
		s.settingsUnit.state.settingsAgentContent = content
		s.settingsUnit.state.settingsAgentPath = result.Path
		s.settingsUnit.state.settingsAgentLoaded = true
		if state.activeKey == key {
			state.activeContent = content
		}
		state.settingsRequestPending = false
		state.settingsReadDeferred = false
		state.settingsReadError = ""
		return
	}
	index := s.noteIndexForAction(action)
	s.taskUnit.state.notes[index], s.taskUnit.state.notePaths[index] = content, result.Path
	s.taskUnit.state.noteHashes[index] = dataString(result.Data, "hash")
	if state.activeKey == key {
		state.activeContent = content
	}
	state.taskID, state.tab = taskID, index
	state.taskRequestPending = false
	state.taskReadDeferred = false
	state.taskReadError = ""
	if readOnly {
		s.taskUnit.state.noteEditing = false
	}
}

func (s *app) applyInstructionReadFailure(action application.Action, err error) {
	if dataBool(action.Payload, "localTaskRules") {
		s.applyTaskLocalNoteRead(action, application.ActionResult{}, err)
		return
	}
	state := s.ensureTaskNotesUI()
	scope, taskID, _ := instructionIdentity(action, action.Path)
	generation := instructionGeneration(action)
	message := "读取说明失败"
	if err != nil {
		message = err.Error()
	}
	if scope == "global" || scope == "group" {
		contextKey := scope + ":" + payloadString(action.Payload, "groupId")
		if contextKey == s.settingsUnit.state.settingsAgentContext && s.settingsUnit.state.settingsPage == "agents" && (generation == 0 || generation == state.settingsRequestGeneration) {
			state.settingsRequestPending = false
			state.settingsReadDeferred = false
			state.settingsReadError = message
			s.settingsUnit.state.settingsAgentLoaded = false
		}
		return
	}
	index := s.noteIndexForAction(action)
	if taskID == s.taskUnit.currentTask().id && index == s.taskUnit.state.noteTab && (generation == 0 || generation == state.taskRequestGeneration) {
		state.taskRequestPending = false
		state.taskReadDeferred = false
		state.taskReadError = message
		s.lastError = message
	}
}

func (s *app) applyInstructionSaveResult(action application.Action, result application.ActionResult) {
	if dataBool(action.Payload, "localTaskRules") {
		s.applyTaskLocalNoteSave(action, result, nil)
		return
	}
	scope, taskID, groupID := instructionIdentity(action, action.Path)
	path := result.Path
	if path == "" {
		path = action.Path
	}
	key := s.instructionAutosaveKey(scope, taskID, groupID, path)
	s.materialsUnit.state.documentSaveMu.Lock()
	ref := s.materialsUnit.state.documentSaveRefs[key]
	if ref.kind == "instruction" {
		if hash := dataString(result.Data, "hash"); hash != "" {
			ref.hash, ref.overwrite = hash, false
		}
		ref.path = path
		s.materialsUnit.state.documentSaveRefs[key] = ref
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	if scope == "task" && taskID == s.taskUnit.currentTask().id {
		for index, notePath := range s.taskUnit.state.notePaths {
			if sameInstructionPath(notePath, path) {
				s.taskUnit.state.noteHashes[index] = dataString(result.Data, "hash")
			}
		}
	}
}

func (s *app) deactivateInstructionEditor() {
	state := s.taskUnit.state.taskNotesUI
	if state == nil {
		return
	}
	state.localActiveKey = ""
	if state.activeKey == "" {
		return
	}
	if markdown := state.activeMarkdown; markdown != nil {
		s.cancelInstructionMarkdownDrafts(markdown)
		suspendInstructionMarkdownPreviews(markdown)
		s.commitMarkdownEditorSource(markdown, markdown.doc.Source())
		s.clearMarkdownComposition(markdown)
		markdown.richLink = nil
	}
	state.activeContent = s.activeInstructionContent(state)
	s.editInstructionAutosave(state.activeKey, state.activeContent)
	s.setInstructionComposing(state.activeKey, false)
	state.activeKey = ""
	state.activeKind = ""
	state.activeTab = -1
	state.activeComposing = false
	state.activeMarkdown = nil
}

func (s *app) preserveActiveInstructionDraft(key string) {
	state := s.taskUnit.state.taskNotesUI
	if state == nil || state.activeKey != key {
		return
	}
	state.activeContent = s.activeInstructionContent(state)
	s.editInstructionAutosave(key, state.activeContent)
	s.setInstructionComposing(key, state.activeComposing)
}

func (s *app) activeInstructionContent(state *taskNotesUIState) string {
	if state == nil {
		return ""
	}
	switch state.activeKind {
	case "task":
		if state.activeTab >= 0 && state.activeTab < len(s.taskUnit.state.notes) {
			return s.taskUnit.state.notes[state.activeTab]
		}
	case "settings":
		return s.settingsUnit.state.settingsAgentContent
	}
	return state.activeContent
}

func (s *app) noteIndexForAction(action application.Action) int {
	if value, ok := action.Payload["noteTab"]; ok {
		switch index := value.(type) {
		case int:
			if index >= 0 && index < len(taskNoteNames) {
				return index
			}
		case int64:
			if index >= 0 && index < int64(len(taskNoteNames)) {
				return int(index)
			}
		case float64:
			if index >= 0 && index < float64(len(taskNoteNames)) {
				return int(index)
			}
		}
	}
	name := strings.ToLower(filepath.Base(action.Path))
	for index, candidate := range taskNoteNames {
		if strings.EqualFold(name, candidate) {
			return index
		}
	}
	return -1
}

func instructionGeneration(action application.Action) uint64 {
	value := action.Payload["instructionGeneration"]
	switch number := value.(type) {
	case uint64:
		return number
	case uint:
		return uint64(number)
	case int:
		if number > 0 {
			return uint64(number)
		}
	case int64:
		if number > 0 {
			return uint64(number)
		}
	case float64:
		if number > 0 {
			return uint64(number)
		}
	}
	return 0
}
