package desktop

import (
	"errors"
	"path/filepath"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"

	codeeditor "github.com/egoist/mygo/ui/editor"
)

type materialTextEditorState struct {
	key, taskID, path         string
	view                      *codeeditor.State
	version                   uint64
	source, diskSource        string
	editing, restored, closed bool
	discarding                bool
	inputError                string
}

func (s *app) currentMaterialTextEditor() *materialTextEditorState {
	if !documents.EditableMaterialText(s.documentPath) {
		return nil
	}
	key := s.documentAutosaveKeyForTask(s.currentTask().id, s.documentPath)
	state := s.materialTextEditors[key]
	if state == nil || state.closed || state.taskID != s.currentTask().id || normalizeIdentityPath(state.path) != normalizeIdentityPath(s.documentPath) {
		return nil
	}
	return state
}

func (s *app) materialTextBindingCurrent(state *materialTextEditorState) bool {
	return state != nil && !state.closed && s.materialTextEditors[state.key] == state && s.currentMaterialTextEditor() == state
}

func (s *app) seedMaterialTextEditor(taskID, path string, content documents.Content) {
	if !documents.EditableMaterialText(path) || content.ReadOnly || s.markdownAutosave == nil {
		return
	}
	taskPath, root, ok := s.markdownTaskPaths(taskID)
	if !ok {
		return
	}
	key := s.documentAutosaveKeyForIdentity(taskID, taskPath, root, path)
	if key == "" {
		return
	}
	previous, hadPrevious := s.documentSaveRef(key)
	s.markdownAutosave.Seed(key, content.Text)
	status := s.markdownAutosave.Status(key)
	draft := content.TextDraft
	restored := !status.Dirty && draft != nil && markdownDraftMatches(draft.Identity, taskID, taskPath, root, path)
	if restored {
		s.markdownAutosave.RestoreDraft(key, draft.Content)
	}
	s.markdownAutosave.SetCacheError(key, content.DraftError)
	source, _ := s.markdownAutosave.Content(key)
	status = s.markdownAutosave.Status(key)
	ref := documentSaveRef{taskID: taskID, taskPath: taskPath, materialsRoot: root, relativePath: relativeDocumentPath(root, path), path: path, kind: "material-text", hash: content.Hash, hasUTF8BOM: content.HasUTF8BOM, newline: content.Newline}
	if status.Dirty {
		if restored {
			ref.hash, ref.hasUTF8BOM, ref.newline = draft.BaselineHash, draft.HasUTF8BOM, draft.Newline
		} else if hadPrevious {
			ref.hash, ref.overwrite, ref.hasUTF8BOM, ref.newline = previous.hash, previous.overwrite, previous.hasUTF8BOM, previous.newline
		}
		if ref.hash == "" || ref.hash != content.Hash {
			s.markdownAutosave.MarkConflict(key, source, "文件已被外部修改，请接受外部版本或明确覆盖")
		}
	}
	if s.materialTextEditors == nil {
		s.materialTextEditors = make(map[string]*materialTextEditorState)
	}
	state := s.materialTextEditors[key]
	if state == nil {
		if len(s.materialTextEditors) >= documents.MaxMarkdownDraftRecords {
			for oldKey, old := range s.materialTextEditors {
				if old != s.materialTextActive && !s.markdownAutosave.Status(oldKey).Dirty {
					old.closed = true
					delete(s.materialTextEditors, oldKey)
					break
				}
			}
		}
		if len(s.materialTextEditors) >= documents.MaxMarkdownDraftRecords {
			s.lastError = "打开的文本草稿已达到 100 条上限，请先保存或丢弃部分草稿"
			return
		}
		state = &materialTextEditorState{key: key, taskID: taskID, path: path, editing: documents.Kind(path) == "text"}
		s.materialTextEditors[key] = state
	}
	state.closed, state.diskSource, state.restored = false, content.Text, state.restored || restored
	if state.view == nil || state.source != source && !status.Dirty && !state.view.Composing() {
		buffer := codeeditor.NewBufferPreservingLineEndings(source)
		buffer.SetLanguage(textEditorLanguage(filepath.Base(path)))
		state.view = &codeeditor.State{Buffer: buffer, Label: filepath.Base(path), LineNumbers: codeeditor.LineNumbersOn}
		state.source, state.version = source, buffer.Version()
	}
	state.view.OnCompositionChanged = func(composing bool) {
		if s.materialTextBindingCurrent(state) {
			s.markdownAutosave.SetComposing(state.key, composing)
		}
	}
	limit := documents.MaxMaterialsText
	if ref.hasUTF8BOM {
		limit -= 3
	}
	state.view.Buffer.SetMaxSourceBytes(limit)
	state.view.OnLimitExceeded = func() {
		if s.materialTextBindingCurrent(state) {
			state.inputError = "资料文本已达到 3 MiB（含 BOM），本次输入未写入"
		}
	}
	ref.textOwner = state
	s.documentSaveMu.Lock()
	s.documentSaveRefs[key] = ref
	s.documentSaveMu.Unlock()
	if s.currentTask().id == taskID && normalizeIdentityPath(s.documentPath) == normalizeIdentityPath(path) {
		s.documentContent, s.documentHash, s.documentDirty, s.documentEditing = source, ref.hash, s.markdownAutosave.Status(key).Dirty, state.editing
	}
}

// Pull only changed buffers. A 3 MiB document must not be copied each frame.
func (s *app) captureMaterialTextState(state *materialTextEditorState) error {
	if state == nil || state.closed || s.materialTextEditors[state.key] != state || state.view == nil || state.view.Buffer == nil || s.markdownAutosave == nil {
		return nil
	}
	if _, exists := s.documentSaveRef(state.key); !exists {
		return nil
	}
	s.markdownAutosave.SetComposing(state.key, state.view.Composing())
	if version := state.view.Buffer.Version(); version != state.version {
		source := state.view.Buffer.OriginalText()
		ref, _ := s.documentSaveRef(state.key)
		bytes := len(source)
		if ref.hasUTF8BOM {
			bytes += 3
		}
		previousInputError := state.inputError
		state.version, state.source = version, source
		state.inputError = ""
		if bytes > documents.MaxMaterialsText {
			state.inputError = "资料文本不能超过 3 MiB（含 BOM）"
			s.markdownAutosave.Pause(state.key, state.inputError)
			return errors.New(state.inputError)
		}
		state.restored = false
		s.markdownAutosave.Edit(state.key, source)
		if previousInputError != "" && s.markdownAutosave.Status(state.key).Error == previousInputError {
			s.markdownAutosave.Retry(state.key)
		}
		if s.materialTextBindingCurrent(state) {
			s.documentContent, s.documentDirty = source, s.markdownAutosave.Status(state.key).Dirty
			if s.documentDirty {
				s.documentDrafts[state.path], s.documentDirtyMap[state.path] = source, true
			}
		}
	}
	if state.view.Composing() {
		return errors.New("中文输入尚未结束，请提交或取消输入后再切换；当前草稿已保留")
	}
	return nil
}

func (s *app) captureMaterialTextInput() error {
	return s.captureMaterialTextState(s.currentMaterialTextEditor())
}

func (s *app) observeMaterialTextInput() {
	if state := s.materialTextActive; state != nil {
		_ = s.captureMaterialTextState(state)
	}
}

func (s *app) materialTextSaved(key string, ref documentSaveRef, status editor.SaveState) {
	state := s.materialTextEditors[key]
	if state == nil || state.closed {
		return
	}
	if !status.Dirty {
		state.diskSource, state.restored = state.source, false
	}
	if !status.Dirty && ref.taskID == s.currentTask().id {
		delete(s.documentDrafts, ref.path)
		delete(s.documentDirtyMap, ref.path)
	}
	if s.materialTextBindingCurrent(state) {
		s.documentHash, s.documentDirty = ref.hash, status.Dirty
	}
}

func (s *app) remapMaterialTextEditors(taskID, taskPath, root, previous, replacement string) {
	for key, state := range s.materialTextEditors {
		_, exists := s.documentSaveRef(key)
		if state.taskID != taskID {
			continue
		}
		next := remapMaterialsAbsolutePath(state.path, previous, replacement)
		if next == state.path {
			continue
		}
		newKey := s.documentAutosaveKeyForIdentity(taskID, taskPath, root, next)
		if newKey == "" {
			continue
		}
		// The rename lease has already transferred the autosave/ref identity.
		if !exists {
			if _, moved := s.documentSaveRef(newKey); !moved {
				state.closed = true
				delete(s.materialTextEditors, key)
				continue
			}
		}
		if old := s.materialTextEditors[newKey]; old != nil && old != state {
			old.closed = true
		}
		delete(s.materialTextEditors, key)
		state.key, state.path = newKey, next
		if state.view != nil {
			state.view.Label = filepath.Base(next)
			state.view.Buffer.SetLanguage(textEditorLanguage(next))
			state.view.ReadOnly = !documents.EditableMaterialText(next)
		}
		s.materialTextEditors[newKey] = state
	}
}
