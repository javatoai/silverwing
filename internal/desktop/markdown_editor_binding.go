package desktop

import (
	"silverwing-go/internal/editor"
)

// markdownEditorBinding keeps the segmented Markdown editor independent from
// the document/materials screen that hosts it. Instruction editors bind only
// to their own autosave key and never borrow the app-wide materials document.
type markdownEditorBinding struct {
	id           string
	isCurrent    func() bool
	commit       func(string)
	setComposing func(bool)
	save         func()
}

func markdownEditorHostID(state *markdownEditorState) string {
	if state != nil && state.binding != nil && state.binding.id != "" {
		return state.binding.id
	}
	if state == nil {
		return "markdown-editor"
	}
	return state.taskID + "-" + normalizeIdentityPath(state.path)
}

func markdownEditorControlKey(state *markdownEditorState, key string) string {
	if state == nil || state.binding == nil {
		return key
	}
	return "markdown-editor-" + markdownEditorHostID(state) + "-" + key
}

func (s *app) markdownEditorIsCurrent(state *markdownEditorState) bool {
	if state == nil || state.doc == nil || state.closed {
		return false
	}
	if state.binding != nil {
		return state.binding.isCurrent != nil && state.binding.isCurrent()
	}
	return s.markdown == state && s.documentPath == state.path
}

func (s *app) commitMarkdownEditorSource(state *markdownEditorState, source string) {
	if state == nil || state.doc == nil || state.readOnly || state.detached || !s.markdownEditorIsCurrent(state) {
		return
	}
	if state.binding != nil {
		if state.binding.isCurrent == nil || !state.binding.isCurrent() || state.binding.commit == nil {
			return
		}
		state.binding.commit(source)
		return
	}
	s.commitMarkdownSource(source)
}

func (s *app) ensureInstructionMarkdownEditor(key, kind string, tab int, source string) *markdownEditorState {
	uiState := s.ensureTaskNotesUI()
	if uiState.markdownEditors == nil {
		uiState.markdownEditors = make(map[string]*markdownEditorState)
	}
	state := uiState.markdownEditors[key]
	if state == nil {
		taskID, path := "", s.settingsAgentPath
		if kind == "task" {
			taskID = s.currentTask().id
			if tab >= 0 && tab < len(s.notePaths) {
				path = s.notePaths[tab]
			}
		}
		state = &markdownEditorState{
			taskID: taskID, path: path, doc: editor.NewDocument(source, key),
			readOnly: instructionMarkdownReadOnly(s, key), remoteImages: true,
			diagrams:     make(map[uint64]*markdownDiagramState),
			inlineImages: make(map[string]*markdownInlineImageState),
			richFields:   make(map[uint64]*markdownRichFieldState),
		}
		uiState.markdownEditors[key] = state
		state.binding = &markdownEditorBinding{id: key}
		state.binding.isCurrent = func() bool {
			current := s.taskNotesUI
			if current == nil || current.activeKey != key || current.activeMarkdown != state || current.markdownEditors[key] != state {
				return false
			}
			switch kind {
			case "task":
				if tab < 0 || tab >= len(s.notePaths) {
					return false
				}
				return current.activeKind == "task" && current.activeTab == tab && current.taskID == taskID &&
					s.currentTask().id == taskID && s.noteTab == tab && (markdownReadingOnly || s.noteEditing) &&
					s.instructionAutosaveKey("task", taskID, "", s.notePaths[tab]) == key
			case "settings":
				scope, groupID := s.settingsInstructionScope()
				return current.activeKind == "settings" && s.settingsPage == "agents" && s.settingsAgentLoaded &&
					s.instructionAutosaveKey(scope, "", groupID, s.settingsAgentPath) == key
			default:
				return false
			}
		}
		state.binding.commit = func(value string) {
			if !state.binding.isCurrent() {
				return
			}
			current := s.taskNotesUI
			if current.activeContent == value {
				return
			}
			if kind == "task" {
				if current.taskID != taskID || current.activeTab != tab || s.currentTask().id != taskID || tab < 0 || tab >= len(s.notes) {
					return
				}
				s.notes[tab] = value
			} else if kind == "settings" {
				s.settingsAgentContent = value
			} else {
				return
			}
			current.activeContent = value
			s.editInstructionAutosave(key, value)
		}
		state.binding.setComposing = func(composing bool) {
			if !state.binding.isCurrent() {
				return
			}
			s.taskNotesUI.activeComposing = composing
			s.setInstructionComposing(key, composing)
		}
		state.binding.save = func() {
			if state.binding.isCurrent() {
				s.flushMarkdownAutosave(key)
			}
		}
	}
	state.closed = false
	state.readOnly = instructionMarkdownReadOnly(s, key)
	if state.doc == nil {
		state.doc = editor.NewDocument(source, key)
	} else if state.doc.Source() != source {
		s.resetInstructionMarkdownEditor(state, source)
	}
	return state
}

func instructionMarkdownReadOnly(s *app, key string) bool {
	ref, ok := s.instructionSaveRef(key)
	return markdownReadingOnly || ok && ref.readOnly
}

func (s *app) resetInstructionMarkdownEditor(state *markdownEditorState, source string) {
	if state == nil || state.doc == nil || state.doc.Source() == source {
		return
	}
	s.cancelInstructionMarkdownDrafts(state)
	s.clearMarkdownComposition(state)
	state.doc.SyncExternal(source)
	state.doc.SetCursor(nil)
	state.selectionPart = 0
	state.selectionFrom, state.selectionTo = 0, 0
	state.richFields = make(map[uint64]*markdownRichFieldState)
	state.structuredRichFields = nil
	state.selectedStructuredRich = nil
	state.findScrolls = nil
	state.richLink = nil
}

func (s *app) cancelInstructionMarkdownDrafts(state *markdownEditorState) {
	if state == nil {
		return
	}
	cancelled := false
	for _, rich := range allMarkdownRichFields(state) {
		if rich == nil || rich.input == nil {
			continue
		}
		if rich.input.composing {
			rich.input.cancelComposition()
			cancelled = true
		}
		if rich.input.dirty || rich.input.errorText != "" {
			rich.input.discardDraft()
			cancelled = true
		}
	}
	if cancelled {
		s.lastError = "切换说明时已取消尚未提交的富文本输入。"
	}
}

// Retained instruction editors can return after an asynchronous preview was
// discarded while inactive. Cancel pending work and invalidate only its cache
// keys so the next activation can request it again; completed previews remain.
func suspendInstructionMarkdownPreviews(state *markdownEditorState) {
	if state == nil {
		return
	}
	for _, image := range state.inlineImages {
		if image != nil && image.loading {
			if image.cancel != nil {
				image.cancel()
				image.cancel = nil
			}
			image.loading, image.key = false, ""
		}
	}
	for _, diagram := range state.diagrams {
		if diagram != nil && diagram.loading {
			if diagram.cancel != nil {
				diagram.cancel()
				diagram.cancel = nil
			}
			diagram.loading, diagram.key = false, ""
		}
	}
}

func (s *app) discardInstructionMarkdownEditorsForTask(taskID string) {
	if taskID == "" || s.taskNotesUI == nil {
		return
	}
	for key, state := range s.taskNotesUI.markdownEditors {
		if state == nil || state.taskID != taskID {
			continue
		}
		s.cancelInstructionMarkdownDrafts(state)
		s.clearMarkdownComposition(state)
		closeMarkdownPreviewAssets(state)
		state.closed = true
		delete(s.taskNotesUI.markdownEditors, key)
	}
}

func (s *app) activeMarkdownEditorForDialog() *markdownEditorState {
	if notes := s.taskNotesUI; notes != nil && notes.activeMarkdown != nil &&
		s.markdownEditorIsCurrent(notes.activeMarkdown) {
		if notes.activeMarkdown.richLink != nil {
			return notes.activeMarkdown
		}
	}
	if s.markdown != nil && s.markdownEditorIsCurrent(s.markdown) {
		return s.markdown
	}
	if notes := s.taskNotesUI; notes != nil && notes.activeMarkdown != nil &&
		s.markdownEditorIsCurrent(notes.activeMarkdown) {
		return notes.activeMarkdown
	}
	return nil
}

func markdownEditorInputKey(state *markdownEditorState, field string) string {
	if state == nil {
		return "markdown-editor-" + field
	}
	if state.binding == nil {
		return "markdown-editor-" + state.taskID + "-" + normalizeIdentityPath(state.path) + "-" + field
	}
	return markdownEditorControlKey(state, "input-"+field)
}
