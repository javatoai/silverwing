package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

const taskCreationDirectoryOccupied = "本地目录已存在，请修改文件夹名。"

var taskCreationDirectorySlots = make(chan struct{}, 2)

type taskCreationDirectoryCheck struct {
	form      *taskCreationState
	signature string
	ctx       context.Context
	cancel    context.CancelFunc
	resume    bool
}

type taskCreationDirectoryState struct {
	signature string
	request   *taskCreationDirectoryCheck
	message   string
	permit    string
}

func (s *app) taskCreationDirectorySignature() string {
	if s == nil || s.taskUnit.state.taskCreation == nil {
		return ""
	}
	form := s.taskUnit.state.taskCreation
	if form.config.TaskRoot == nil || strings.TrimSpace(*form.config.TaskRoot) == "" || domain.DirectoryNameValidationError(form.draft.TaskName) != nil {
		return ""
	}
	return form.token + "\x00" + s.snapshot.Home + "\x00" + *form.config.TaskRoot + "\x00" + form.draft.TaskName
}

func checkTaskCreationDirectory(root, name string) string {
	if err := domain.DirectoryNameValidationError(name); err != nil {
		return err.Error()
	}
	_, err := os.Lstat(filepath.Join(root, name))
	if err == nil {
		return taskCreationDirectoryOccupied
	}
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return "无法检查本地目录：" + err.Error()
}

func cancelTaskCreationDirectoryCheck(form *taskCreationState) {
	if form == nil {
		return
	}
	state := &form.directoryCheck
	if state.request != nil {
		state.request.cancel()
		state.request = nil
	}
	state.permit = ""
}

// Observing a name change schedules filesystem work; drawing never reads disk.
// A user pressing Check/Create requests a fresh check, including after an
// existing directory has been removed outside the application.
func (s *app) startTaskCreationDirectoryCheck(resume bool) bool {
	form := s.taskUnit.state.taskCreation
	if form == nil || form.phase != "editing" {
		return false
	}
	signature := s.taskCreationDirectorySignature()
	state := &form.directoryCheck
	if signature == "" {
		cancelTaskCreationDirectoryCheck(form)
		*state = taskCreationDirectoryState{}
		return false
	}
	if resume && state.permit == signature {
		state.permit = ""
		return false
	}
	if state.signature == signature {
		if state.request != nil {
			state.request.resume = state.request.resume || resume
			return resume
		}
		if !resume {
			return false
		}
	}
	// Headless tests drive the completion boundary explicitly.
	if s.win == nil || s.win.IsDestroyed() {
		return false
	}
	cancelTaskCreationDirectoryCheck(form)
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskCreationDirectoryCheck{form: form, signature: signature, ctx: ctx, cancel: cancel, resume: resume}
	state.signature, state.request, state.message = signature, request, ""
	root, name, win := *form.config.TaskRoot, form.draft.TaskName, s.win
	go func() {
		if !resume {
			timer := time.NewTimer(150 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		select {
		case taskCreationDirectorySlots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		message := checkTaskCreationDirectory(root, name)
		<-taskCreationDirectorySlots
		if ctx.Err() != nil || win.IsDestroyed() {
			cancel()
			return
		}
		win.Update(func() { s.finishTaskCreationDirectoryCheck(request, message) })
	}()
	return resume
}

func (s *app) finishTaskCreationDirectoryCheck(request *taskCreationDirectoryCheck, message string) {
	if request == nil {
		return
	}
	defer request.cancel()
	if request.ctx.Err() != nil || s.taskUnit.state.taskCreation != request.form || !s.dialogOpen || s.form.mode != "task-create" || s.taskCreationDirectorySignature() != request.signature {
		return
	}
	state := &request.form.directoryCheck
	if state.request != request {
		return
	}
	state.request, state.message = nil, message
	if request.resume {
		state.permit = request.signature
		s.beginTaskCreationPreflight()
	}
}
