package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui/editor"
)

type taskServiceConfigEditView struct {
	files                   []string
	catalog, source         production.GenbuConfigSnapshot
	file                    string
	pendingRead             taskServiceConfigSourceRead
	sourceToken             string
	diff                    *editor.DiffState
	sourceBOM, draftBOM     string
	listLoading, previewing bool
	stale, noChange         bool
	readFailure             *taskServiceConfigReadFailure
	failure                 string
	notice                  string
	pendingApprovalAt       time.Time
	draftVersion            uint64
	dirtyVersion            uint64
	dirty, dirtyKnown       bool
	preview                 *application.TaskServiceConfigPreviewResult
	previewVersion          uint64
}

type taskServiceConfigReadFailure struct {
	message string
	source  taskServiceConfigSourceRead
}

type taskServiceConfigSourceRead struct {
	file      string
	keepDraft bool
}

func (edit *taskServiceConfigEditView) busy() bool {
	return edit.listLoading || edit.pendingRead.file != "" || edit.previewing
}

func (edit *taskServiceConfigEditView) draftText() string {
	return edit.draftBOM + edit.diff.New.OriginalText()
}

func (edit *taskServiceConfigEditView) draftDirty() bool {
	if edit.diff == nil {
		return false
	}
	version := edit.diff.New.Version()
	if !edit.dirtyKnown || edit.dirtyVersion != version {
		edit.dirty = edit.draftBOM != edit.sourceBOM || edit.diff.New.OriginalText() != edit.diff.Old.OriginalText()
		edit.dirtyKnown, edit.dirtyVersion = true, version
	}
	return edit.dirty
}

func (edit *taskServiceConfigEditView) bindSource(data application.TaskServiceConfigSourceResult, keepDraft bool) {
	body, bom := splitTaskServiceConfigBOM(data.Content)
	edit.source, edit.file, edit.sourceToken = data.GenbuConfigSnapshot, data.File, data.SourceToken
	edit.sourceBOM = bom
	if keepDraft && edit.diff != nil {
		edit.diff.Old = taskServiceConfigEditorBuffer(data.File, body)
	} else {
		edit.diff = newTaskServiceConfigDiff(data.File, body, bom)
		edit.draftBOM = bom
	}
	edit.draftVersion = edit.diff.New.Version()
	edit.dirtyKnown = false
	edit.stale, edit.noChange = false, false
	edit.failure, edit.notice = "", ""
	edit.pendingApprovalAt = time.Time{}
}

func taskServiceConfigEditKey(_ *taskServicesView, row *taskServiceRow) string {
	return row.serviceID + "\x00" + row.genbuName + "\x00" + row.environment
}

func (s *app) taskServiceConfigEditCurrent(v *taskServicesView, dialog *taskServiceDialog) bool {
	return s.taskServiceDialogCurrent(v, dialog) && dialog.kind == taskServiceConfigEditDetail
}

func (s *app) taskServiceConfigEditBlocked(v *taskServicesView, dialog *taskServiceDialog) bool {
	key := taskServiceConfigEditKey(v, dialog.row)
	return s.taskServicesUnit.configSubmissions[key]
}

func taskServiceConfigEditJobKey(row *taskServiceRow) string {
	return "config-edit:" + row.key()
}

func (s *app) resetTaskServiceConfigEdit(v *taskServicesView, dialog *taskServiceDialog) {
	s.cancelTaskServiceJob(v, taskServiceConfigEditJobKey(dialog.row))
	s.discardTaskServiceConfigPreview(v, dialog)
	dialog.edit = taskServiceConfigEditView{}
}

func (s *app) requestTaskServiceConfigFiles(v *taskServicesView, dialog *taskServiceDialog) {
	if !s.taskServiceConfigEditCurrent(v, dialog) || s.taskServiceConfigEditBlocked(v, dialog) || dialog.edit.busy() {
		return
	}
	edit, row := &dialog.edit, dialog.row
	edit.listLoading, edit.readFailure = true, nil
	action := s.taskServiceAction(v, row, "task.services.config-files")
	s.runTaskServiceJob(v, taskServiceConfigEditJobKey(row), action, func(result application.ActionResult, err error) {
		s.finishTaskServiceConfigFiles(v, dialog, result, err)
	})
}

func (s *app) finishTaskServiceConfigFiles(v *taskServicesView, dialog *taskServiceDialog, result application.ActionResult, err error) {
	if !s.taskServiceConfigEditCurrent(v, dialog) {
		return
	}
	edit := &dialog.edit
	edit.listLoading = false
	if err != nil {
		edit.readFailure = &taskServiceConfigReadFailure{message: err.Error()}
		return
	}
	data, ok := result.Data.(production.GenbuConfigFiles)
	if !ok || !taskServiceConfigSnapshotValid(data.GenbuConfigSnapshot, dialog.row) {
		edit.readFailure = &taskServiceConfigReadFailure{message: "无法确认配置文件列表所属的环境或目标"}
		return
	}
	edit.catalog, edit.files = data.GenbuConfigSnapshot, data.Files
	if len(edit.files) == 1 && edit.file == "" {
		s.requestTaskServiceConfigSource(v, dialog, edit.files[0], false)
	}
}

func taskServiceConfigSnapshotValid(data production.GenbuConfigSnapshot, row *taskServiceRow) bool {
	return data.Service != "" && data.Environment == row.environment &&
		data.Namespace != "" && data.Name != "" && data.UpdateTime != "" && data.SnapshotID != ""
}

func (s *app) requestTaskServiceConfigSource(v *taskServicesView, dialog *taskServiceDialog, file string, keepDraft bool) {
	if !s.taskServiceConfigEditCurrent(v, dialog) || s.taskServiceConfigEditBlocked(v, dialog) || dialog.edit.busy() || file == "" {
		return
	}
	edit, row := &dialog.edit, dialog.row
	if keepDraft && (edit.diff == nil || file != edit.file) {
		return
	}
	s.discardTaskServiceConfigPreview(v, dialog)
	edit.pendingRead, edit.readFailure = taskServiceConfigSourceRead{file: file, keepDraft: keepDraft}, nil
	action := s.taskServiceAction(v, row, "task.services.config-source")
	action.Payload["file"] = file
	s.runTaskServiceJob(v, taskServiceConfigEditJobKey(row), action, func(result application.ActionResult, err error) {
		s.finishTaskServiceConfigSource(v, dialog, file, result, err)
	})
}

func (s *app) finishTaskServiceConfigSource(v *taskServicesView, dialog *taskServiceDialog, file string, result application.ActionResult, err error) {
	if !s.taskServiceConfigEditCurrent(v, dialog) || dialog.edit.pendingRead.file != file {
		return
	}
	edit := &dialog.edit
	read := edit.pendingRead
	edit.pendingRead = taskServiceConfigSourceRead{}
	if err != nil {
		edit.readFailure = &taskServiceConfigReadFailure{message: err.Error(), source: read}
		return
	}
	// The in-process DTO deliberately omits source contents from JSON.
	data, ok := result.Data.(application.TaskServiceConfigSourceResult)
	if !ok || data.File != file || data.SourceToken == "" ||
		!taskServiceConfigSnapshotValid(data.GenbuConfigSnapshot, dialog.row) ||
		!utf8.ValidString(data.Content) || len(data.Content) > production.GenbuConfigMaxBytes {
		edit.readFailure = &taskServiceConfigReadFailure{
			message: "无法确认配置文件或读取完整的 UTF-8 内容", source: read,
		}
		return
	}
	edit.bindSource(data, read.keepDraft)
	if read.keepDraft {
		edit.notice = "左侧已读取最新配置，右侧草稿已保留。"
	}
}

func (s *app) discardTaskServiceConfigPreview(v *taskServicesView, dialog *taskServiceDialog) {
	preview := dialog.edit.preview
	dialog.edit.preview = nil
	if preview != nil {
		s.discardTaskServiceConfigPreviewToken(v, dialog.row, preview.PreviewToken)
	}
}

func (s *app) discardTaskServiceConfigPreviewToken(v *taskServicesView, row *taskServiceRow, token string) {
	if token == "" || s.backend == nil {
		return
	}
	action := s.taskServiceAction(v, row, "task.services.config-preview-discard")
	action.Payload["previewToken"] = token
	backend := s.backend
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = backend.Dispatch(ctx, action)
	}()
}

func (s *app) syncTaskServiceConfigDraft(v *taskServicesView, dialog *taskServiceDialog) {
	edit := &dialog.edit
	if edit.diff == nil || edit.diff.New.Version() == edit.draftVersion {
		return
	}
	edit.draftVersion = edit.diff.New.Version()
	if !edit.stale {
		edit.failure = ""
	}
	edit.noChange = false
	edit.notice = ""
	edit.pendingApprovalAt = time.Time{}
	s.discardTaskServiceConfigPreview(v, dialog)
}

func (s *app) requestTaskServiceConfigPreview(v *taskServicesView, dialog *taskServiceDialog) {
	if !s.taskServiceConfigEditCurrent(v, dialog) || s.taskServiceConfigEditBlocked(v, dialog) {
		return
	}
	s.syncTaskServiceConfigDraft(v, dialog)
	edit, row := &dialog.edit, dialog.row
	if edit.busy() || edit.stale || edit.noChange || !edit.pendingApprovalAt.IsZero() || edit.sourceToken == "" || !edit.draftDirty() {
		return
	}
	content := edit.draftText()
	if len(content) > production.GenbuConfigMaxBytes || !utf8.ValidString(content) {
		edit.failure = "配置内容必须是 4 MiB 以内的 UTF-8 文本"
		return
	}
	s.discardTaskServiceConfigPreview(v, dialog)
	edit.previewing, edit.failure, edit.notice = true, "", ""
	action := s.taskServiceAction(v, row, "task.services.config-preview")
	action.Payload["file"] = edit.file
	action.Payload["content"] = content
	action.Payload["snapshotId"] = edit.source.SnapshotID
	action.Payload["sourceToken"] = edit.sourceToken
	draft, version := edit.diff.New, edit.diff.New.Version()
	s.runTaskServiceJobWithDrop(v, taskServiceConfigEditJobKey(row), action, func(result application.ActionResult, err error) {
		s.finishTaskServiceConfigPreview(v, dialog, draft, version, result, err)
	}, func(result application.ActionResult) {
		if preview, ok := result.Data.(application.TaskServiceConfigPreviewResult); ok {
			s.discardTaskServiceConfigPreviewToken(v, row, preview.PreviewToken)
		}
	})
}

func (s *app) finishTaskServiceConfigPreview(v *taskServicesView, dialog *taskServiceDialog, draft *editor.Buffer, version uint64, result application.ActionResult, err error) {
	preview, decodeErr := decodeTaskFilesResult[application.TaskServiceConfigPreviewResult](result)
	if !s.taskServiceConfigEditCurrent(v, dialog) {
		s.discardTaskServiceConfigPreviewToken(v, dialog.row, preview.PreviewToken)
		return
	}
	edit := &dialog.edit
	edit.previewing = false
	if edit.diff == nil || edit.diff.New != draft {
		s.discardTaskServiceConfigPreviewToken(v, dialog.row, preview.PreviewToken)
		return
	}
	if draft.Version() != version {
		s.discardTaskServiceConfigPreviewToken(v, dialog.row, preview.PreviewToken)
		if taskServiceConfigNeedsReload(err) {
			edit.setSubmissionError(err)
		}
		return
	}
	if err != nil {
		edit.setSubmissionError(err)
		return
	}
	if decodeErr != nil {
		edit.failure = "无法读取配置预览，请重试"
		return
	}
	source := edit.source
	if preview.Environment != source.Environment || preview.File != edit.file || preview.Service != source.Service ||
		preview.Namespace != source.Namespace || preview.Name != source.Name || preview.UpdateTime != source.UpdateTime ||
		preview.SnapshotID != source.SnapshotID {
		s.discardTaskServiceConfigPreviewToken(v, dialog.row, preview.PreviewToken)
		edit.setSubmissionError(production.ErrGenbuConfigPreviewStale)
		return
	}
	if preview.Status == "no-change" && preview.PreviewToken == "" {
		edit.noChange = true
		edit.notice = "配置内容无变更。"
		return
	}
	edit.preview, edit.previewVersion = &preview, version
	if !edit.previewCurrent(&preview, time.Now()) {
		s.discardTaskServiceConfigPreview(v, dialog)
		edit.failure = "无法确认配置预览，请重试"
		return
	}
	s.submitTaskServiceConfig(v, dialog, &preview)
}

func (edit *taskServiceConfigEditView) setSubmissionError(err error) {
	if attemptedAt := taskServiceConfigPendingAttempt(err); !attemptedAt.IsZero() {
		edit.pendingApprovalAt = attemptedAt
		edit.failure = ""
		return
	}
	edit.failure = err.Error()
	if taskServiceConfigNeedsReload(err) {
		edit.stale = true
		edit.sourceToken = ""
	}
	if errors.Is(err, production.ErrGenbuConfigPreviewStale) {
		edit.failure = "原配置已变化或读取凭据已失效，请重新读取原配置。"
	}
}

func taskServiceConfigPendingAttempt(err error) time.Time {
	var pending *application.GenbuConfigPendingError
	if errors.As(err, &pending) {
		return pending.AttemptedAt
	}
	return time.Time{}
}

func taskServiceConfigNeedsReload(err error) bool {
	return errors.Is(err, production.ErrGenbuConfigPreviewStale) || errors.Is(err, production.ErrGenbuConfigTarget)
}

func (edit *taskServiceConfigEditView) previewCurrent(preview *application.TaskServiceConfigPreviewResult, now time.Time) bool {
	if preview == nil || edit.preview != preview || edit.diff == nil || edit.diff.New.Version() != edit.previewVersion ||
		preview.Status != "preview" || preview.PreviewToken == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, preview.ExpiresAt)
	return err == nil && now.Before(expiresAt)
}

func (s *app) submitTaskServiceConfig(v *taskServicesView, dialog *taskServiceDialog, preview *application.TaskServiceConfigPreviewResult) {
	if !s.taskServiceConfigEditCurrent(v, dialog) || !dialog.edit.previewCurrent(preview, time.Now()) {
		return
	}
	row, unit := dialog.row, &s.taskServicesUnit
	key := taskServiceConfigEditKey(v, row)
	if dialog.edit.busy() || s.taskServiceConfigEditBlocked(v, dialog) {
		return
	}
	if s.win == nil || s.win.IsDestroyed() || s.backend == nil {
		dialog.edit.failure = "Genbu 服务不可用，请重试"
		return
	}
	action := s.taskServiceAction(v, row, "task.services.config-submit")
	action.Payload["previewToken"] = preview.PreviewToken
	action.Payload["confirmed"] = true
	dialog.edit.preview, dialog.edit.failure = nil, ""
	if unit.configSubmissions == nil {
		unit.configSubmissions = make(map[string]bool)
	}
	unit.configSubmissions[key] = true
	win, backend := s.win, s.backend
	optionalCtx := s.integrationContext("genbu")
	unit.wg.Add(1)
	go func() {
		defer unit.wg.Done()
		// Approval creation can complete after we leave the task. Keep waiting for
		// the outcome independently from the cancellable service reads.
		result, err := backend.Dispatch(context.Background(), action)
		if !win.IsDestroyed() {
			win.Update(func() {
				if !win.IsDestroyed() {
					s.finishTaskServiceConfigSubmit(v, dialog, key, result, err, optionalCtx.Err() == nil)
				}
			})
		}
	}()
}

func (s *app) finishTaskServiceConfigSubmit(v *taskServicesView, dialog *taskServiceDialog, key string, result application.ActionResult, err error, canPresent bool) {
	unit, row := &s.taskServicesUnit, dialog.row
	delete(unit.configSubmissions, key)
	current := s.taskServiceConfigEditCurrent(v, dialog)
	scope := row.name + " " + strings.ToUpper(row.environment)
	approvalID := int64(0)
	unknown := errors.Is(err, production.ErrGenbuConfigSubmitUnknown)
	if err == nil {
		response, decodeErr := decodeTaskFilesResult[production.GenbuConfigEditResult](result)
		approvalID = response.ApprovalID
		unknown = decodeErr != nil || response.Status != "submitted" || approvalID < 0
		if unknown {
			approvalID = 0
		}
	}
	if !canPresent || !s.integrationReady("genbu") {
		return
	}
	switch {
	case unknown:
		s.lastMessage = ""
		s.lastError = scope + " 配置审批提交结果待确认，请先核对玄武配置审批记录。"
	case err != nil:
		if current {
			dialog.edit.setSubmissionError(err)
		} else {
			s.lastMessage, s.lastError = "", scope+" 配置审批未提交。"+err.Error()
		}
		return
	default:
		s.lastError = ""
		if approvalID > 0 {
			s.lastMessage = fmt.Sprintf("%s 配置审批已提交，审批编号 %d", scope, approvalID)
		} else {
			s.lastMessage = scope + " 配置审批已提交，未返回编号。"
		}
		if result.Message != "" {
			s.lastMessage += " " + result.Message
		}
	}
	if current {
		s.openTaskServiceApprovalsAt(v, row, approvalID, taskServiceConfigPendingAttempt(err))
	}
}
