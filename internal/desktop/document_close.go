package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo"
)

// The close decision and editor buffers belong to the window thread. Only
// Autosave.Flush runs in the background; a failed flush keeps that worker and
// the editable drafts alive so the user can resolve a conflict or retry.
type documentCloseState struct {
	preparing bool
	allowNext bool
}

func (s *app) handleDocumentClose(event *mygo.CloseEvent) {
	if event == nil {
		return
	}
	if s.materialsUnit.state.documentClose == nil {
		s.materialsUnit.state.documentClose = &documentCloseState{}
	}
	state := s.materialsUnit.state.documentClose
	if state.allowNext {
		state.allowNext = false
		if err := s.documentCloseReady(); err == nil {
			return
		} else {
			event.PreventDefault()
			s.showDocumentCloseError(err)
			return
		}
	}
	event.PreventDefault()
	if state.preparing {
		return
	}
	if err := s.captureDocumentCloseInput(); err != nil {
		s.showDocumentCloseError(err)
		return
	}
	if s.win == nil || s.win.IsDestroyed() {
		s.showDocumentCloseError(errors.New("窗口不可用，当前草稿尚未完成保存"))
		return
	}
	state.preparing = true
	window, autosave := s.win, s.materialsUnit.state.markdownAutosave
	window.Invalidate()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var failures []editor.FlushFailure
		if autosave != nil {
			failures = autosave.PrepareClose(ctx)
		}
		flushErr := ctx.Err()
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			if s.materialsUnit.state.documentClose != state {
				return
			}
			state.preparing = false
			if remaining := s.materialsUnit.documentClosePendingFailures(failures); len(remaining) > 0 {
				s.showDocumentCloseError(s.documentCloseFailures(remaining))
				return
			}
			if flushErr != nil {
				s.showDocumentCloseError(fmt.Errorf("保存尚未完成，当前草稿已保留：%w", flushErr))
				return
			}
			// Input or an operation may have changed while Flush was waiting.
			// Recheck on the window thread before granting one close attempt.
			if err := s.documentCloseReady(); err != nil {
				s.showDocumentCloseError(err)
				return
			}
			state.allowNext = true
			window.Close()
		})
	}()
}

func (s *app) captureDocumentCloseInput() error {
	if err := s.commentComposerCloseError(); err != nil {
		return err
	}
	for _, state := range s.materialsUnit.state.materialTextEditors {
		if err := s.captureMaterialTextState(state); err != nil {
			return err
		}
	}
	if err := s.captureMarkdownEditorInput(s.materialsUnit.state.markdown); err != nil {
		return err
	}
	if s.documentCloseOperationBusy() {
		return errors.New("操作正在执行，完成前不能关闭应用")
	}
	if err := s.taskLocalNotesCloseError(); err != nil {
		return err
	}
	if state := s.taskUnit.state.taskNotesUI; state != nil && state.activeKey != "" {
		s.preserveActiveInstructionDraft(state.activeKey)
		if markdown := state.activeMarkdown; markdown != nil && markdown.doc != nil && !markdown.closed {
			for _, rich := range allMarkdownRichFields(markdown) {
				if rich != nil && rich.input != nil && !rich.input.composing && (rich.input.dirty || rich.input.errorText != "") {
					return errors.New("富文本内容尚未提交，请处理编辑错误后再关闭；当前草稿已保留")
				}
			}
			if markdown.doc.Composing() {
				return errors.New("中文输入尚未结束，请提交输入后再关闭；当前草稿已保留")
			}
			if !markdown.doc.Flush() {
				return errors.New("编辑内容尚未完成回传，请处理编辑错误后再关闭；当前草稿已保留")
			}
			s.commitMarkdownEditorSource(markdown, markdown.doc.Source())
		}
		if state.activeComposing {
			return errors.New("中文输入尚未结束，请提交输入后再关闭；当前草稿已保留")
		}
	}
	if state := s.materialsUnit.state.markdown; state != nil && state.doc != nil && !state.closed && !state.readOnly && !state.detached {
		for _, rich := range allMarkdownRichFields(state) {
			if !rich.input.composing && (rich.input.dirty || rich.input.errorText != "") {
				return errors.New("富文本内容尚未提交，请处理编辑错误后再关闭；当前草稿已保留")
			}
		}
		if !state.doc.Flush() {
			return errors.New("编辑内容尚未完成回传，请处理编辑错误后再关闭；当前草稿已保留")
		}
		s.syncMarkdownEditor()
		if state.doc.Composing() {
			return errors.New("中文输入尚未结束，请提交输入后再关闭；当前草稿已保留")
		}
	}
	return nil
}

// Login initialization and logout use independent CLI workers, but the source
// app still treats them as close-blocking operations until worker cleanup ends.
// The CLI owner reports its auth mutation boundary; status reads and device-code polling do not
// block closing and are cancelled by closeCLIJobs after the window exits.
func (s *app) documentCloseOperationBusy() bool {
	if s.busy || s.materialsFileMutationRunning() {
		return true
	}
	if state := s.toolsUnit.state.cliJobs; state != nil && state.authMutationRunning() {
		return true
	}
	return false
}

func (s *app) documentCloseReady() error {
	if err := s.captureDocumentCloseInput(); err != nil {
		return err
	}
	if s.materialsUnit.state.markdownAutosave != nil {
		if failures := s.materialsUnit.documentClosePendingFailures(s.materialsUnit.state.markdownAutosave.PendingFailures()); len(failures) > 0 {
			return s.documentCloseFailures(failures)
		}
	}
	return nil
}

func (s *materialsController) documentClosePendingFailures(failures []editor.FlushFailure) []editor.FlushFailure {
	remaining := make([]editor.FlushFailure, 0, len(failures))
	for _, failure := range failures {
		ref, ok := s.documentSaveRef(failure.Path)
		state := s.state.markdownAutosave.Status(failure.Path)
		if ok && ref.kind != "instruction" && !state.Composing && !state.Saving && state.CacheError == "" && state.DraftSavedRevision >= state.Revision {
			continue
		}
		if state.CacheError != "" {
			failure.Message = "草稿缓存写入失败，最新原文尚未耐久保存：" + state.CacheError
		}
		remaining = append(remaining, failure)
	}
	return remaining
}

func (s *app) documentCloseFailures(failures []editor.FlushFailure) error {
	messages := make([]string, 0, len(failures))
	for _, failure := range failures {
		name := "文档"
		if ref, ok := s.materialsUnit.documentSaveRef(failure.Path); ok && ref.path != "" {
			name = filepath.Base(ref.path)
		}
		messages = append(messages, name+"："+failure.Message)
	}
	sort.Strings(messages)
	return errors.New("仍有文档未保存，请先处理后再关闭：\n" + strings.Join(messages, "\n"))
}

func (s *app) showDocumentCloseError(err error) {
	if err != nil {
		s.lastError = err.Error()
	}
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Invalidate()
	}
}
