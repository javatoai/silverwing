package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func taskServiceConfigEditFixture(t *testing.T, environment string) (*app, *taskServicesView, *taskServiceDialog) {
	t.Helper()
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", environment)
	s.openTaskServiceDialog(v, row, taskServiceConfigEditDetail)
	if v.dialog == nil {
		t.Fatal("configuration edit dialog did not open")
	}
	return s, v, v.dialog
}

func taskServiceConfigSourceFixture(row *taskServiceRow, content string) application.TaskServiceConfigSourceResult {
	return application.TaskServiceConfigSourceResult{GenbuConfigFile: production.GenbuConfigFile{
		GenbuConfigSnapshot: production.GenbuConfigSnapshot{
			Service: row.genbuName, Environment: row.environment, Namespace: "fp", Name: "latest-config",
			UpdateTime: "2026-11-19T12:30:00+08:00", SnapshotID: "synthetic-snapshot",
		},
		File: "application.properties", Content: content,
	}, SourceToken: "synthetic-source-token"}
}

func bindTaskServiceConfigFixture(dialog *taskServiceDialog, content string) *taskServiceConfigEditView {
	source := taskServiceConfigSourceFixture(dialog.row, content)
	edit := &dialog.edit
	edit.readFailure = nil
	edit.catalog = source.GenbuConfigSnapshot
	edit.files = []string{source.File, "settings.yml"}
	edit.bindSource(source, false)
	return edit
}

func taskServiceConfigPreviewFixture(edit *taskServiceConfigEditView) *application.TaskServiceConfigPreviewResult {
	return &application.TaskServiceConfigPreviewResult{GenbuConfigEditResult: production.GenbuConfigEditResult{
		Service: edit.source.Service, Environment: edit.source.Environment, File: edit.file,
		Namespace: edit.source.Namespace, Name: edit.source.Name, UpdateTime: edit.source.UpdateTime,
		SnapshotID: edit.source.SnapshotID, Status: "preview",
	}, PreviewToken: "test-token", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339)}
}

func newTaskServiceConfigEditTester(s *app, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTaskServiceDialog(c, p)
		s.renderConfirmationDialog(c, p)
	}, width, height)
}

func recordTaskServiceConfigReadActions(t *testing.T, s *app) <-chan application.Action {
	t.Helper()
	calls := make(chan application.Action, 2)
	s.win = &mygo.Window{}
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		calls <- action
		<-ctx.Done()
		return application.ActionResult{}, ctx.Err()
	}}
	t.Cleanup(s.stopTaskServices)
	return calls
}

func expectTaskServiceConfigRead(t *testing.T, calls <-chan application.Action, row *taskServiceRow, kind, file string) {
	t.Helper()
	select {
	case action := <-calls:
		if action.Kind != kind || action.ServiceID != row.serviceID || action.Payload["environment"] != row.environment {
			t.Fatalf("dispatched an unexpected configuration read: %s", action.Kind)
		}
		if file == "" && len(action.Payload) != 2 || file != "" && (action.Payload["file"] != file || len(action.Payload) != 3) {
			t.Fatalf("%s did not request the exact file %q", kind, file)
		}
	case <-time.After(time.Second):
		t.Fatalf("no %s request was dispatched", kind)
	}
}

func TestTaskServiceConfigEditorListsAndReadsSingleExactFileAutomatically(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	calls := recordTaskServiceConfigReadActions(t, s)
	s.openTaskServiceDialog(v, row, taskServiceConfigEditDetail)
	expectTaskServiceConfigRead(t, calls, row, "task.services.config-files", "")
	source := taskServiceConfigSourceFixture(row, "")
	s.finishTaskServiceConfigFiles(v, v.dialog, application.ActionResult{Data: production.GenbuConfigFiles{
		GenbuConfigSnapshot: source.GenbuConfigSnapshot,
		Files:               []string{source.File},
	}}, nil)
	expectTaskServiceConfigRead(t, calls, row, "task.services.config-source", source.File)
	s.finishTaskServiceConfigSource(v, v.dialog, source.File, application.ActionResult{}, errors.New("配置原文暂时不可用"))
	tester := newTaskServiceConfigEditTester(s, 700, 550)
	if err := tester.Click("重试读取配置"); err != nil {
		t.Fatal(err)
	}
	expectTaskServiceConfigRead(t, calls, row, "task.services.config-source", source.File)
	s.finishTaskServiceConfigSource(v, v.dialog, source.File, application.ActionResult{Data: source}, nil)
	tester = newTaskServiceConfigEditTester(s, 700, 550)
	if !tester.HasText("原配置（只读）") || !tester.HasText("修改后配置") || tester.HasText(source.File) ||
		tester.HasText(source.Name) || tester.HasText(source.UpdateTime) || tester.HasText("重试读取配置") {
		t.Fatal("the single-file editor did not show just the comparison after loading")
	}
	if _, ok := tester.Access("选择配置文件"); ok {
		t.Fatal("the single-file editor rendered a file selector")
	}
}

func TestTaskServiceConfigEditorRetriesFailedAndEmptyFileLists(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	calls := recordTaskServiceConfigReadActions(t, s)
	tester := newTaskServiceConfigEditTester(s, 700, 550)
	if err := tester.Click("重试读取文件列表"); err != nil {
		t.Fatal(err)
	}
	expectTaskServiceConfigRead(t, calls, dialog.row, "task.services.config-files", "")
	if !tester.HasText("正在读取配置文件列表…") || tester.HasText("重试读取文件列表") {
		t.Fatal("retry did not clear the previous file list failure while loading")
	}
	source := taskServiceConfigSourceFixture(dialog.row, "")
	s.finishTaskServiceConfigFiles(v, dialog, application.ActionResult{Data: production.GenbuConfigFiles{
		GenbuConfigSnapshot: source.GenbuConfigSnapshot,
	}}, nil)
	tester = newTaskServiceConfigEditTester(s, 700, 550)
	if !tester.HasText("当前 ConfigMap 没有可编辑的配置文件。") || tester.HasText("刷新文件列表") {
		t.Fatal("the empty file list did not show its empty state")
	}
	if err := tester.Click("重新读取文件列表"); err != nil {
		t.Fatal(err)
	}
	expectTaskServiceConfigRead(t, calls, dialog.row, "task.services.config-files", "")
}

func TestTaskServiceConfigEditorProtectsLeftAndPreservesExactDraftBytes(t *testing.T) {
	s, _, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "\ufeffalpha=旧\r\nbeta=尾\n  \r")
	tester := newTaskServiceConfigEditTester(s, 800, 600)
	var left, right ui.AccessInfo
	for _, field := range tester.AccessAll() {
		if field.Role == ui.RoleTextField {
			if field.ReadOnly {
				left = field
			} else {
				right = field
			}
		}
	}
	if left.Bounds.W == 0 || right.Bounds.W == 0 || left.Bounds.X >= right.Bounds.X ||
		!tester.HasText("原配置（只读）") || !tester.HasText("修改后配置") {
		t.Fatal("native editor is missing its read-only left or editable right pane")
	}
	tester.ClickAt(left.Bounds.X+90, left.Bounds.Y+10)
	tester.Type("blocked")
	if edit.diff.Old.OriginalText() != "alpha=旧\r\nbeta=尾\n  \r" || edit.draftDirty() {
		t.Fatal("typing on the original pane changed content")
	}
	draft := edit.diff.New
	tester.ClickAt(right.Bounds.X+90, right.Bounds.Y+10)
	tester.Key(0, ui.KeyEnd)
	tester.Type("蓝")
	if edit.draftText() != "\ufeffalpha=旧蓝\r\nbeta=尾\n  \r" || edit.diff.New != draft || !edit.draftDirty() {
		t.Fatal("typing did not preserve the UTF-8 BOM, mixed newlines, and stable draft buffer")
	}
}

func TestTaskServiceConfigEditDirtyCloseRequiresConfirmation(t *testing.T) {
	for _, trigger := range []string{"关闭", "Escape", "backdrop"} {
		t.Run(trigger, func(t *testing.T) {
			s, v, dialog := taskServiceConfigEditFixture(t, "uat")
			s.win = &mygo.Window{}
			edit := bindTaskServiceConfigFixture(dialog, "alpha=old\n")
			edit.diff.New.Set("alpha=changed\n")
			tester := newTaskServiceConfigEditTester(s, 700, 600)
			dismiss := func() {
				switch trigger {
				case "Escape":
					tester.Key(0, ui.KeyEscape)
				case "backdrop":
					tester.ClickAt(2, 2)
				default:
					if err := tester.Click(trigger); err != nil {
						t.Fatal(err)
					}
				}
			}
			dismiss()
			if !dialog.open || !tester.HasText("放弃并关闭") {
				t.Fatal("dirty editor closed without confirmation")
			}
			if err := tester.Click("继续编辑"); err != nil {
				t.Fatal(err)
			}
			if v.dialog != dialog || edit.draftText() != "alpha=changed\n" {
				t.Fatal("continuing did not retain the draft")
			}
			dismiss()
			if err := tester.Click("放弃并关闭"); err != nil {
				t.Fatal(err)
			}
			if v.dialog != nil || dialog.open || edit.diff != nil || edit.file != "" {
				t.Fatal("confirmed close retained editor data")
			}
		})
	}
}

func TestTaskServiceConfigEditDirtyFileSwitchRequiresConfirmation(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	s.win = &mygo.Window{}
	edit := bindTaskServiceConfigFixture(dialog, "")
	edit.diff.New.Set("draft")
	tester := newTaskServiceConfigEditTester(s, 800, 600)
	if tester.HasText(edit.source.Name) || tester.HasText(edit.source.UpdateTime) {
		t.Fatal("the multi-file selector also displayed configuration metadata")
	}
	if err := tester.Click("选择配置文件"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("settings.yml"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("放弃并切换") || edit.pendingRead.file != "" {
		t.Fatal("selecting another file did not confirm draft discard")
	}
	if err := tester.Click("继续编辑"); err != nil {
		t.Fatal(err)
	}
	if edit.file != "application.properties" || edit.draftText() != "draft" {
		t.Fatal("canceling file switch changed the draft or selection")
	}
	s.chooseTaskServiceConfigFile(v, dialog, "unlisted.txt")
	if s.confirmationDialog != nil || edit.pendingRead.file != "" {
		t.Fatal("unlisted file selection was accepted")
	}
}

func TestTaskServiceConfigEditPreviewUsesExactSnapshotAndDraft(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "\ufeffalpha=旧\r\nbeta=尾\n  \r")
	edit.diff.New.Edit(editor.Edit{Start: len("alpha="), End: len("alpha=旧"), Text: "新"})
	calls := make(chan application.Action, 1)
	s.win = &mygo.Window{}
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		calls <- action
		<-ctx.Done()
		return application.ActionResult{}, ctx.Err()
	}}
	t.Cleanup(s.stopTaskServices)
	tester := newTaskServiceConfigEditTester(s, 700, 600)
	if err := tester.Click("提交配置审批"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-calls:
		if action.Kind != "task.services.config-preview" || action.TaskID != v.identity.id || action.ServiceID != dialog.row.serviceID ||
			action.Payload["environment"] != "uat" || action.Payload["file"] != edit.file ||
			action.Payload["snapshotId"] != edit.source.SnapshotID || action.Payload["sourceToken"] != edit.sourceToken ||
			action.Payload["content"] != "\ufeffalpha=新\r\nbeta=尾\n  \r" || len(action.Payload) != 6 {
			t.Fatal("submission did not preview the exact scoped file, snapshot and replacement bytes")
		}
	case <-time.After(time.Second):
		t.Fatal("submission did not dispatch a preview")
	}
}

func TestTaskServiceConfigEditIgnoresStaleDraftVersionAndKeepsDraftOnReload(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "alpha=old\n")
	draft := edit.diff.New
	draft.Set("alpha=draft\n")
	before := draft.Version()
	draft.Set("alpha=updated-draft\n")
	preview := taskServiceConfigPreviewFixture(edit)
	s.finishTaskServiceConfigPreview(v, dialog, draft, before, application.ActionResult{Data: preview}, nil)
	if edit.preview != nil || len(s.taskServicesUnit.configSubmissions) != 0 {
		t.Fatal("older preview advanced after a local edit")
	}
	s.finishTaskServiceConfigPreview(v, dialog, draft, draft.Version(), application.ActionResult{}, production.ErrGenbuConfigPreviewStale)
	tester := newTaskServiceConfigEditTester(s, 700, 550)
	if !edit.stale || edit.sourceToken != "" || !tester.HasText("重新读取原配置") {
		t.Fatal("a stale target kept a submission path open or omitted reload")
	}
	calls := recordTaskServiceConfigReadActions(t, s)
	if err := tester.Click("重新读取原配置"); err != nil {
		t.Fatal(err)
	}
	expectTaskServiceConfigRead(t, calls, dialog.row, "task.services.config-source", edit.file)
	s.finishTaskServiceConfigSource(v, dialog, edit.file, application.ActionResult{}, errors.New("配置原文暂时不可用"))
	tester = newTaskServiceConfigEditTester(s, 700, 550)
	if button, ok := tester.Access("提交配置审批"); !ok || !button.Disabled || tester.HasText("重新读取原配置") {
		t.Fatal("a failed stale read offered a duplicate reload or made the stale draft submittable")
	}
	if err := tester.Click("重试读取配置"); err != nil {
		t.Fatal(err)
	}
	expectTaskServiceConfigRead(t, calls, dialog.row, "task.services.config-source", edit.file)
	latest := taskServiceConfigSourceFixture(dialog.row, "alpha=remote\n")
	latest.SnapshotID, latest.SourceToken = "latest-snapshot", "latest-token"
	if edit.readFailure != nil {
		t.Fatal("retry did not clear the failed source read")
	}
	s.finishTaskServiceConfigSource(v, dialog, edit.file, application.ActionResult{Data: latest}, nil)
	tester = newTaskServiceConfigEditTester(s, 700, 550)
	if edit.diff.Old.OriginalText() != "alpha=remote\n" || edit.diff.New != draft ||
		edit.draftText() != "alpha=updated-draft\n" || !edit.draftDirty() || edit.stale || edit.sourceToken != "latest-token" ||
		!tester.HasText("左侧已读取最新配置，右侧草稿已保留。") || tester.HasText("重试读取配置") {
		t.Fatal("reload did not replace only the original and retain the editable draft")
	}
}

func TestTaskServiceConfigEditNoChangeDoesNotOfferSubmission(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "")
	tester := newTaskServiceConfigEditTester(s, 700, 550)
	if button, ok := tester.Access("提交配置审批"); !ok || !button.Disabled {
		t.Fatal("an unchanged config can be submitted")
	}
	edit.diff.New.Set("new text")
	preview := taskServiceConfigPreviewFixture(edit)
	preview.Status, preview.PreviewToken = "no-change", ""
	s.syncTaskServiceConfigDraft(v, dialog)
	s.finishTaskServiceConfigPreview(v, dialog, edit.diff.New, edit.diff.New.Version(), application.ActionResult{Data: preview}, nil)
	tester = newTaskServiceConfigEditTester(s, 700, 550)
	if button, ok := tester.Access("提交配置审批"); !ok || !button.Disabled || !tester.HasText("配置内容无变更。") {
		t.Fatal("the no-change preview opened an approval path")
	}
}

func TestTaskServiceConfigEditUATAutomaticallySubmitsMatchingPreview(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "example=old\n")
	edit.diff.New.Set("example=draft\n")
	s.syncTaskServiceConfigDraft(v, dialog)
	dispatched := make(chan application.Action, 2)
	finish := make(chan struct{})
	t.Cleanup(func() {
		close(finish)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTaskServices(ctx); err != nil {
			t.Error(err)
		}
	})
	s.win = &mygo.Window{}
	s.backend = &asyncLifecycleBackend{dispatch: func(_ context.Context, action application.Action) (application.ActionResult, error) {
		dispatched <- action
		<-finish
		return application.ActionResult{}, nil
	}}
	preview := taskServiceConfigPreviewFixture(edit)
	s.finishTaskServiceConfigPreview(v, dialog, edit.diff.New, edit.diff.New.Version(), application.ActionResult{Data: *preview}, nil)
	select {
	case action := <-dispatched:
		if action.Kind != "task.services.config-submit" || action.TaskID != v.identity.id ||
			action.ServiceID != dialog.row.serviceID || action.Payload["environment"] != "uat" ||
			action.Payload["previewToken"] != "test-token" || action.Payload["confirmed"] != true || len(action.Payload) != 4 {
			t.Fatal("a validated UAT draft did not submit its scoped preview token")
		}
	case <-time.After(time.Second):
		t.Fatal("UAT preview did not lead to submission")
	}
	if s.confirmationDialog != nil || edit.preview != nil || !s.taskServicesUnit.configSubmissions[taskServiceConfigEditKey(v, dialog.row)] {
		t.Fatal("UAT required an additional confirmation or retained a replayable preview")
	}
	s.requestTaskServiceConfigPreview(v, dialog)
	if edit.previewing {
		t.Fatal("another preview could start while the UAT approval was pending")
	}
}

func TestTaskServiceConfigEditUATSubmitSurvivesPageExit(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "alpha=old\n")
	edit.diff.New.Set("alpha=draft\n")
	edit.draftVersion = edit.diff.New.Version()
	edit.preview, edit.previewVersion = taskServiceConfigPreviewFixture(edit), edit.draftVersion
	dispatched := make(chan application.Action, 1)
	contexts := make(chan context.Context, 1)
	finish := make(chan struct{})
	t.Cleanup(func() {
		close(finish)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTaskServices(ctx); err != nil {
			t.Error(err)
		}
	})
	s.win = &mygo.Window{}
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		contexts <- ctx
		dispatched <- action
		<-finish
		return application.ActionResult{}, nil
	}}
	tester := newTaskServiceConfigEditTester(s, 700, 600)
	if err := tester.Click("提交配置审批"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-dispatched:
		if action.Kind != "task.services.config-submit" || action.TaskID != v.identity.id ||
			action.ServiceID != dialog.row.serviceID || action.Payload["environment"] != "uat" ||
			action.Payload["previewToken"] != "test-token" || action.Payload["confirmed"] != true || len(action.Payload) != 4 {
			t.Fatal("submission did not carry only the active scope, token, and confirmation")
		}
	case <-time.After(time.Second):
		t.Fatal("explicit confirmation did not dispatch")
	}
	key := taskServiceConfigEditKey(v, dialog.row)
	s.leaveTaskServices()
	ctx := <-contexts
	if ctx.Err() != nil || !s.taskServicesUnit.configSubmissions[key] || dialog.edit.file != "" || dialog.edit.preview != nil {
		t.Fatal("page exit canceled a committed submit or retained input")
	}
	s.finishTaskServiceConfigSubmit(v, dialog, key, application.ActionResult{Data: production.GenbuConfigEditResult{
		Status: "submitted", ApprovalID: 71,
	}}, nil, true)
	if s.taskServicesUnit.configSubmissions[key] || !strings.Contains(s.lastMessage, "71") || s.genbuApprovals != nil {
		t.Fatal("detached result did not notify the user or displaced another page")
	}
}

func TestTaskServiceConfigEditSubmissionRoutesToScopedApprovals(t *testing.T) {
	attemptedAt := time.Date(2026, time.August, 17, 15, 59, 0, 0, time.UTC)
	followingDay := domain.SilverWingLocalDate(attemptedAt).AddDate(0, 0, 1)
	for _, scenario := range []struct {
		name      string
		result    production.GenbuConfigEditResult
		failure   error
		wantID    int64
		uncertain bool
		anchor    time.Time
	}{
		{name: "known approval", result: production.GenbuConfigEditResult{Status: "submitted", ApprovalID: 88}, wantID: 88},
		{name: "accepted without ID", result: production.GenbuConfigEditResult{Status: "submitted"}},
		{name: "unknown", failure: production.ErrGenbuConfigSubmitUnknown, uncertain: true},
		{name: "invalid response", uncertain: true},
		{name: "retained unknown", failure: &application.GenbuConfigPendingError{AttemptedAt: attemptedAt}, uncertain: true, anchor: followingDay},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, v, dialog := taskServiceConfigEditFixture(t, "uat")
			key := taskServiceConfigEditKey(v, dialog.row)
			s.taskServicesUnit.configSubmissions = map[string]bool{key: true}
			s.finishTaskServiceConfigSubmit(v, dialog, key, application.ActionResult{Data: scenario.result}, scenario.failure, true)
			approvals := s.genbuApprovals
			if v.dialog != nil || approvals == nil ||
				approvals.source.row != dialog.row || approvals.detailID != scenario.wantID {
				t.Fatal("submission did not open the corresponding, scoped configuration approval view")
			}
			if s.taskServicesUnit.configSubmissions[key] {
				t.Fatal("submission still occupies the service after completion")
			}
			if !scenario.anchor.IsZero() && !approvals.openedAt.Equal(scenario.anchor) {
				t.Fatal("approval lookup did not use the submission attempt time")
			}
			if scenario.uncertain {
				s.closeGenbuReadDialogs()
				s.openTaskServiceDialog(v, dialog.row, taskServiceConfigEditDetail)
				tester := newTaskServiceConfigEditTester(s, 700, 600)
				if tester.HasText("已确认未创建审批") || !tester.HasText("提交配置审批") ||
					!tester.HasText("重试读取文件列表") {
					t.Fatal("an earlier unknown result blocked editing or provided an acknowledgement bypass")
				}
			}
		})
	}
}

func TestTaskServiceConfigEditPreflightDeadlineKeepsTheDraft(t *testing.T) {
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "alpha=original\n")
	edit.diff.New.Set("alpha=draft\n")
	s.syncTaskServiceConfigDraft(v, dialog)
	key := taskServiceConfigEditKey(v, dialog.row)
	s.taskServicesUnit.configSubmissions = map[string]bool{key: true}
	s.finishTaskServiceConfigSubmit(v, dialog, key, application.ActionResult{}, context.DeadlineExceeded, true)
	if s.genbuApprovals != nil || v.dialog != dialog || s.taskServicesUnit.configSubmissions[key] ||
		edit.failure == "" || !edit.pendingApprovalAt.IsZero() || edit.draftText() != "alpha=draft\n" {
		t.Fatal("a preflight deadline was classified as a pending approval")
	}
}

func pendingTaskServiceConfigEdit(t *testing.T, attemptedAt time.Time) (*app, *taskServicesView, *taskServiceDialog) {
	t.Helper()
	s, v, dialog := taskServiceConfigEditFixture(t, "uat")
	edit := bindTaskServiceConfigFixture(dialog, "alpha=original\n")
	draft := edit.diff.New
	draft.Set("alpha=pending\n")
	s.syncTaskServiceConfigDraft(v, dialog)
	s.finishTaskServiceConfigPreview(v, dialog, draft, draft.Version(), application.ActionResult{},
		&application.GenbuConfigPendingError{AttemptedAt: attemptedAt})
	return s, v, dialog
}

func TestTaskServiceConfigPendingPreviewRemainsEditableWithoutResubmit(t *testing.T) {
	s, _, dialog := pendingTaskServiceConfigEdit(t, time.Date(2026, time.August, 17, 14, 0, 0, 0, time.UTC))
	calls := recordTaskServiceConfigReadActions(t, s)
	tester := newTaskServiceConfigEditTester(s, 700, 600)
	if !tester.HasText("此变更的审批提交结果待确认。") || !tester.HasText("查看配置审批") ||
		tester.HasText("已确认未创建审批") || !dialog.edit.diff.Editable {
		t.Fatal("pending preview did not retain the editor and offer only approval lookup")
	}
	_ = tester.Click("提交配置审批")
	if dialog.edit.previewing || s.confirmationDialog != nil {
		t.Fatal("unchanged pending draft was allowed to retry submission")
	}

	dialog.edit.diff.New.Set("alpha=distinct\n")
	tester.Frame()
	if tester.HasText("此变更的审批提交结果待确认。") || tester.HasText("查看配置审批") {
		t.Fatal("a pending attempt from the old draft blocked another edit")
	}
	if err := tester.Click("提交配置审批"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-calls:
		if action.Kind != "task.services.config-preview" || action.Payload["content"] != "alpha=distinct\n" {
			t.Fatal("distinct draft did not reach a new preview")
		}
	case <-time.After(time.Second):
		t.Fatal("distinct draft preview was blocked")
	}
}

func TestTaskServiceConfigPendingApprovalLookupKeepsAttemptWindow(t *testing.T) {
	attemptedAt := time.Date(2026, time.August, 17, 15, 59, 0, 0, time.UTC)
	followingDay := domain.SilverWingLocalDate(attemptedAt).AddDate(0, 0, 1)
	s, v, dialog := pendingTaskServiceConfigEdit(t, attemptedAt)
	row := dialog.row
	calls := recordTaskServiceConfigReadActions(t, s)
	tester := newTaskServiceConfigEditTester(s, 700, 600)
	if err := tester.Click("查看配置审批"); err != nil {
		t.Fatal(err)
	}
	expectPage := func(page int, since time.Time) {
		t.Helper()
		select {
		case action := <-calls:
			if action.Kind != "task.services.approvals" || action.ServiceID != row.serviceID ||
				action.Payload["environment"] != row.environment || action.Payload["openedAt"] != since || action.Payload["page"] != page {
				t.Fatal("approval lookup lost the request scope or date")
			}
		case <-time.After(time.Second):
			t.Fatal("approval lookup did not dispatch")
		}
	}
	expectPage(1, followingDay)
	s.readGenbuApprovals(s.genbuApprovals, 2)
	expectPage(2, followingDay)
	s.openTaskServiceApprovals(v, row, 0)
	if s.genbuApprovals.openedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatal("normal approval browsing retained the pending attempt's historical date")
	}
	expectPage(1, s.genbuApprovals.openedAt)
}

func TestGenbuApprovalPendingLookupAccountsForShanghaiMidnight(t *testing.T) {
	attemptedAt := time.Date(2026, time.August, 17, 15, 59, 0, 0, time.UTC)
	sameDay := attemptedAt.Add(30 * time.Second)
	if got := genbuApprovalRangeEnd(attemptedAt, sameDay).Format(time.DateOnly); got != "2026-08-17" {
		t.Fatalf("same-day lookup extends past today's date: %s", got)
	}
	for _, now := range []time.Time{attemptedAt.Add(2 * time.Minute), attemptedAt.AddDate(0, 0, 30)} {
		if got := genbuApprovalRangeEnd(attemptedAt, now).Format(time.DateOnly); got != "2026-08-18" {
			t.Fatalf("later lookup omitted the neighboring day: %s", got)
		}
	}
}
