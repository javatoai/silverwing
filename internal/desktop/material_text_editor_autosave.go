package desktop

import (
	"context"
	"errors"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
)

func (s *app) writeMaterialTextDraft(ctx context.Context, key string, ref documentSaveRef, content string, discard bool) error {
	if s.backend == nil {
		return errors.New("应用后端不可用，文本草稿尚未缓存")
	}
	// Never recreate a deleted/renamed identity from a late native callback.
	current, exists := s.materialsUnit.documentSaveRef(key)
	if !exists || current.path != ref.path || current.taskID != ref.taskID {
		return editor.ErrDraftUnsupported
	}
	kind := "document.draft"
	if discard {
		kind = "document.draft-discard"
	}
	payload := markdownDraftPayload(ref)
	payload["hasUtf8Bom"], payload["newline"] = ref.hasUTF8BOM, ref.newline
	_, err := s.backend.Dispatch(ctx, application.Action{Kind: kind, TaskID: ref.taskID, Path: ref.path, Text: content, Payload: payload})
	if err != nil {
		s.invalidateInstructionEditor()
	}
	return err
}

func (s *app) flushMaterialTextAutosave() {
	state := s.currentMaterialTextEditor()
	if state == nil || s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	if err := s.captureMaterialTextInput(); err != nil {
		s.lastError = err.Error()
		return
	}
	engine, key := s.materialsUnit.state.markdownAutosave, state.key
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		failures := engine.Flush(ctx)
		win := s.win
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if !s.materialTextBindingCurrent(state) {
				return
			}
			for _, failure := range failures {
				if failure.Path == key {
					s.lastError = failure.Message
					break
				}
			}
			win.Invalidate()
		})
	}()
}

func (s *app) retryMaterialTextAutosave(state *materialTextEditorState, overwrite bool) {
	if !s.materialTextBindingCurrent(state) || s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	if err := s.captureMaterialTextInput(); err != nil {
		s.lastError = err.Error()
		return
	}
	if overwrite {
		s.materialsUnit.state.documentSaveMu.Lock()
		ref, ok := s.materialsUnit.state.documentSaveRefs[state.key]
		if ok {
			ref.overwrite = true
			s.materialsUnit.state.documentSaveRefs[state.key] = ref
		}
		s.materialsUnit.state.documentSaveMu.Unlock()
		if !ok {
			return
		}
		s.materialsUnit.state.markdownAutosave.ResumeAfterConflict(state.key)
	}
	state.restored = false
	if s.materialsUnit.state.markdownAutosave.Retry(state.key) {
		s.lastError = ""
	}
}

func (s *app) discardMaterialTextAndReload(state *materialTextEditorState) {
	if !s.materialTextBindingCurrent(state) || s.materialsUnit.state.markdownAutosave == nil || state.discarding {
		return
	}
	if err := s.captureMaterialTextInput(); err != nil {
		s.lastError = err.Error()
		return
	}
	ref, exists := s.materialsUnit.documentSaveRef(state.key)
	if !exists {
		return
	}
	engine, status, backend, win := s.materialsUnit.state.markdownAutosave, s.materialsUnit.state.markdownAutosave.Status(state.key), s.backend, s.win
	if backend == nil || win == nil || win.IsDestroyed() {
		return
	}
	state.discarding = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		lease, err := engine.Quiesce(ctx, []string{state.key})
		if err == nil && engine.Status(state.key).Revision != status.Revision {
			err = errors.New("草稿已经产生新的编辑，请再次确认丢弃")
		}
		var result application.ActionResult
		if err == nil {
			result, err = backend.Dispatch(ctx, application.Action{Kind: "document.read", TaskID: ref.taskID, Path: ref.path, Payload: markdownDraftPayload(ref)})
		}
		if err == nil {
			err = engine.DiscardDraft(ctx, state.key)
		}
		if win.IsDestroyed() {
			if lease != nil {
				lease.Release()
			}
			return
		}
		win.Update(func() {
			state.discarding = false
			if lease != nil {
				defer lease.Release()
			}
			if err == nil && engine.Status(state.key).Revision != status.Revision {
				err = errors.New("草稿已经产生新的编辑，最新原文已保留")
			}
			if err != nil {
				s.lastError = "未能丢弃草稿：" + err.Error()
				win.Invalidate()
				return
			}
			if !s.materialTextBindingCurrent(state) {
				return
			}
			engine.AcceptDisk(state.key, result.Text, true)
			state.closed = true
			delete(s.materialsUnit.state.materialTextEditors, state.key)
			delete(s.materialsUnit.state.documentDrafts, state.path)
			delete(s.materialsUnit.state.documentDirtyMap, state.path)
			s.materialsUnit.state.documentDirty = false
			if content, decodeErr := decodeSingleMaterialsData[documents.Content](result.Data); decodeErr == nil {
				content.TextDraft = nil
				result.Data = content
			}
			s.applyMaterialsActionResult(application.Action{Kind: "document.read", TaskID: ref.taskID, Path: ref.path}, result)
			win.Invalidate()
		})
	}()
}

func (s *app) removeMaterialTextEditors(paths []string) {
	for key, state := range s.materialsUnit.state.materialTextEditors {
		for _, removed := range paths {
			if _, inside := pathRelativeToRoot(removed, state.path); !inside {
				continue
			}
			state.closed = true
			if s.materialsUnit.state.markdownAutosave != nil {
				s.materialsUnit.state.markdownAutosave.SetComposing(key, false)
				s.materialsUnit.state.markdownAutosave.AcceptDisk(key, "", true)
			}
			s.materialsUnit.state.documentSaveMu.Lock()
			delete(s.materialsUnit.state.documentSaveRefs, key)
			s.materialsUnit.state.documentSaveMu.Unlock()
			delete(s.materialsUnit.state.materialTextEditors, key)
			delete(s.materialsUnit.state.documentDrafts, state.path)
			delete(s.materialsUnit.state.documentDirtyMap, state.path)
			if s.materialsUnit.state.materialTextActive == state {
				s.materialsUnit.state.materialTextActive = nil
			}
			break
		}
	}
}
