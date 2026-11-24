package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const uatTestBatchID = "batch-for-uat"

func uatBatchDesktopFixture(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	s, record := genbuTaskHistoryFixture(t, "tasks")
	updateCatalogRuntimeService(t, s, "s1", func(service *domain.ServiceConfig) { service.Enabled = true })
	updateCatalogRuntimeService(t, s, "s2", func(service *domain.ServiceConfig) { service.Enabled = true })
	record.BatchID = uatTestBatchID
	record.GenbuStatus.Build = domain.GenbuSuccess
	second := record
	second.ID, second.ServiceID, second.ServiceName, second.ModuleID = "operation-18", "s2", "Second service", "main"
	second.Tag = "3.3.13.beta-2"
	s.snapshot.TagHistory = []application.TagRecord{record, second}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTagHistory(c, p)
		s.renderUATBatchDialog(c, p)
	}, 1000, 650)
	return s, tester
}

func uatBatchPreviewFixture(targets []uatBatchTarget) application.UATBatchReleaseResult {
	rows := make([]application.UATBatchReleaseRow, 0, len(targets))
	for _, target := range targets {
		rows = append(rows, application.UATBatchReleaseRow{Item: target.item, Result: application.CLIUATOperationResult{
			Action: uatRelease, Environment: "UAT", GenbuService: target.alias, Tag: target.item.Tag,
			Status: "preview", GroupID: 100, Namespace: "fixture-namespace", Preflight: "preflight-" + target.item.OperationID,
		}})
	}
	return application.UATBatchReleaseResult{Items: rows}
}

func TestUATBatchReleaseChecksThenSubmitsOnceAndRetainsDetachedResult(t *testing.T) {
	s, tester := uatBatchDesktopFixture(t)
	s.win = &mygo.Window{}
	calls := make(chan genbuReadTestCall, 4)
	finish, stop := asyncLifecycleGate(t)
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		calls <- genbuReadTestCall{ctx: ctx, action: action}
		if action.Kind == "tag.history.uat-batch-submit" {
			<-finish
		} else {
			<-ctx.Done()
		}
		return application.ActionResult{}, nil
	}}
	t.Cleanup(func() {
		stop()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTaskServices(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := tester.Click(uatBatchReleaseLabel + " · " + uatTestBatchID); err != nil {
		t.Fatal(err)
	}
	d := s.taskServicesUnit.uatBatchDialog
	if d == nil || len(d.targets) != 2 || len(calls) != 0 || !tester.HasText("A service") || !tester.HasText("3.3.13.beta-2") {
		t.Fatal("batch dialog did not freeze and display complete service/Tag pairs without sending")
	}
	if err := tester.Click(uatBatchReleaseLabel); err != nil {
		t.Fatal(err)
	}
	previewCall := genbuAwaitRead(t, calls)
	preview := previewCall.action
	items, ok := preview.Payload["items"].([]application.UATBatchReleaseItem)
	if !ok || len(items) != 2 || items[0] != d.targets[0].item || items[1] != d.targets[1].item ||
		preview.Kind != "tag.history.uat-batch-preview" || preview.TaskID != d.history.TaskID || preview.Payload["groupId"] != uatTestBatchID ||
		preview.Payload["tagHistoryTaskPath"] != d.history.TaskPath || len(calls) != 0 {
		t.Fatalf("precheck did not match the exact Tag batch: %+v", preview)
	}
	s.previewUATBatchRelease(d)
	if len(calls) != 0 || previewCall.ctx.Err() != nil || !tester.HasText("正在准备发布…") {
		t.Fatal("second click replaced the in-flight precheck")
	}
	cancelGenbuRead(&d.previewRequest)
	result := uatBatchPreviewFixture(d.targets)
	s.finishUATBatchPreview(d, application.ActionResult{Data: result}, nil)
	submit := genbuAwaitRead(t, calls)
	tester.Frame()
	submittedItems := submit.action.Payload["items"].([]application.UATBatchReleaseItem)
	if submit.action.Kind != "tag.history.uat-batch-submit" || len(submittedItems) != 2 || !s.documentCloseOperationBusy() ||
		submittedItems[0].Preflight != result.Items[0].Result.Preflight || submittedItems[1].Preflight != result.Items[1].Result.Preflight ||
		tester.HasText("fixture-namespace") || tester.HasText("检查发布目标") || !tester.HasText("正在提交…") {
		t.Fatal("precheck details were shown or exact batch did not automatically submit")
	}
	s.page = "settings"
	tester.Frame()
	if s.taskServicesUnit.uatBatchDialog != nil || submit.ctx.Err() != nil || s.taskServicesUnit.uatBatchSubmission != d {
		t.Fatal("leaving Tag closed the write, or retained the old dialog")
	}
	result.Items[0].Result.Status, result.Items[1].Result.Status = "submitted", "unknown"
	result.Items[1].Error = "模拟回执待核实"
	s.finishUATBatchRelease(d, application.ActionResult{Data: result}, errors.New("批次操作未全部完成"))
	if s.taskServicesUnit.uatBatchSubmission != nil || !strings.Contains(s.lastError, "已提交 1 项") || !strings.Contains(s.lastError, "待确认 1 项") {
		t.Fatal("detached batch receipt lost its actual per-item outcomes")
	}
	s.page = "tasks"
	tester.Frame()
	if !tester.HasText(uatBatchReleaseLabel) || tester.HasText("查看发布结果") || tester.HasText("UAT 批量发布结果") || len(calls) != 0 {
		t.Fatal("Tag history kept the retired result action or reopened a stale dialog")
	}
}

func TestUATBatchReleaseRequiresAllItemsAndSupportsOneRowBatch(t *testing.T) {
	s, tester := uatBatchDesktopFixture(t)
	s.snapshot.TagHistory[1].GenbuStatus.Build = domain.GenbuBuilding
	tester.Frame()
	header := uatBatchReleaseLabel + " · " + uatTestBatchID
	button, exists := tester.Find(header)
	if !exists {
		t.Fatal("ineligible batch lost its disabled entry")
	}
	if !tester.HasText("Second service · Genbu 构建尚未成功") {
		t.Fatal("disabled batch did not identify the affected service")
	}
	tester.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	if s.taskServicesUnit.uatBatchDialog != nil {
		t.Fatal("batch with an ineligible item could proceed")
	}
	s.snapshot.TagHistory[1].BatchID = ""
	tester.Frame()
	if _, ok := tester.Find(uatBatchReleaseLabel + " · " + s.snapshot.TagHistory[1].ID); ok {
		t.Fatal("a standalone non-batch record showed a batch action")
	}
	if err := tester.Click(header); err != nil {
		t.Fatal(err)
	}
	if d := s.taskServicesUnit.uatBatchDialog; d == nil || len(d.targets) != 1 || d.targets[0].item.OperationID != s.snapshot.TagHistory[0].ID {
		t.Fatal("one-member actual Tag batch could not be released")
	}
}

func TestUATBatchReleaseUsesAllRecordsWhenTagHistoryIsFiltered(t *testing.T) {
	s, _ := uatBatchDesktopFixture(t)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		state := s.ensureTagHistoryView()
		groups := filterTagHistory(s.taskTagHistoryRecords(state.TaskID), "A service", false)
		s.renderTagHistoryGroup(c, p, state, groups[0], false)
		s.renderUATBatchDialog(c, p)
	}, 360, 520)
	tester.Frame()
	if tester.HasText("Second service") || tester.HasText("批次记录不完整") {
		t.Fatal("filter fixture did not show only the matching record")
	}
	if err := tester.Click(uatBatchReleaseLabel + " · " + uatTestBatchID); err != nil {
		t.Fatal(err)
	}
	if d := s.taskServicesUnit.uatBatchDialog; d == nil || len(d.targets) != 2 || !tester.HasText("Second service") || !tester.HasText("3.3.13.beta-2") {
		t.Fatal("narrow batch dialog omitted a filtered member or exact Tag")
	}
	taskServicesBoundsWithin(t, tester, ui.Rect{W: 360, H: 520}, "取消")
}

func TestUATBatchReleaseOmitsServicesWithGenbuProbeDisabled(t *testing.T) {
	s, tester := uatBatchDesktopFixture(t)
	first, second := s.snapshot.TagHistory[0], s.snapshot.TagHistory[1]
	s.snapshot.TagHistory[0].GenbuStatus.UAT = domain.GenbuInitial
	s.snapshot.TagHistory[1].GenbuStatus.UAT = domain.GenbuSuccess
	*s.input(tagGenbuErrorKey(second.ID), "") = "fixture previous query failure"
	updateCatalogRuntimeService(t, s, second.ServiceID, func(service *domain.ServiceConfig) { service.GenbuProbeEnabled = false })
	tester.Frame()
	if !tester.HasText(first.Tag) || !tester.HasText(second.Tag) || !tester.HasText("UAT 未开始") || tester.HasText("UAT 成功") ||
		tester.HasText("服务未开启 Genbu 探测") || tester.HasText("fixture previous query failure") {
		t.Fatal("disabling one service hid its local Tag or left its cached Genbu state and reminders visible")
	}
	if _, found := tester.Find(tagHistoryPublishLabel + " · " + first.Tag); !found {
		t.Fatal("enabled service lost its release action")
	}
	if _, found := tester.Find(tagHistoryPublishLabel + " · " + second.Tag); found {
		t.Fatal("disabled service still shows a release action")
	}

	reads := genbuPendingReads(t, s)
	if err := tester.Click(uatBatchReleaseLabel + " · " + uatTestBatchID); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click(uatBatchReleaseLabel); err != nil {
		t.Fatal(err)
	}
	preview := genbuAwaitRead(t, reads)
	items, ok := preview.action.Payload["items"].([]application.UATBatchReleaseItem)
	if !ok || len(items) != 1 || items[0].OperationID != first.ID || items[0].ServiceID != first.ServiceID || items[0].Tag != first.Tag {
		t.Fatalf("batch preview targeted the wrong Tag members: %+v", items)
	}

	updateCatalogRuntimeService(t, s, first.ServiceID, func(service *domain.ServiceConfig) { service.GenbuProbeEnabled = false })
	tester.Frame()
	if preview.ctx.Err() != context.Canceled || s.taskServicesUnit.uatBatchDialog != nil {
		t.Fatal("all services disabled while previewing kept the outdated batch dialog or request")
	}
	if tester.HasText(uatBatchReleaseLabel) || tester.HasText("构建 成功") || tester.HasText("UAT 未开始") || !tester.HasText(first.Tag) || !tester.HasText(second.Tag) {
		t.Fatal("all services disabled should show Git history only")
	}

	updateCatalogRuntimeService(t, s, first.ServiceID, func(service *domain.ServiceConfig) { service.GenbuProbeEnabled = true })
	setTestPlatformSwitch(s, "genbu", false)
	tester.Frame()
	if tester.HasText(uatBatchReleaseLabel) || tester.HasText("构建 成功") || tester.HasText("UAT 未开始") || !tester.HasText(first.Tag) || !tester.HasText(second.Tag) {
		t.Fatal("global Genbu disable did not suppress service-level release actions and state")
	}
}

func TestUATBatchReleaseAbortsOnBadPreviewOrChangedMembership(t *testing.T) {
	s, tester := uatBatchDesktopFixture(t)
	reads := genbuPendingReads(t, s)
	if err := tester.Click(uatBatchReleaseLabel + " · " + uatTestBatchID); err != nil {
		t.Fatal(err)
	}
	d := s.taskServicesUnit.uatBatchDialog
	s.previewUATBatchRelease(d)
	genbuAwaitRead(t, reads)
	cancelGenbuRead(&d.previewRequest)
	preview := uatBatchPreviewFixture(d.targets)
	bad := uatBatchPreviewFixture(d.targets)
	bad.Items[1].Result.Tag = "other-tag"
	s.finishUATBatchPreview(d, application.ActionResult{Data: bad}, nil)
	if d.failure == "" || s.taskServicesUnit.uatBatchSubmission != nil || len(reads) != 0 {
		t.Fatal("invalid preview reached write dispatch")
	}
	s.previewUATBatchRelease(d)
	checking := genbuAwaitRead(t, reads)
	s.snapshot.TagHistory[1].Tag = "different-tag"
	tester.Frame()
	s.finishUATBatchPreview(d, application.ActionResult{Data: preview}, nil)
	if checking.ctx.Err() != context.Canceled || s.taskServicesUnit.uatBatchDialog != nil || s.taskServicesUnit.uatBatchSubmission != nil || len(reads) != 0 {
		t.Fatal("a changed Tag left the old batch eligible to submit")
	}
}

func TestUATBatchDefiniteAdmissionRejectionShowsUnsubmittedRows(t *testing.T) {
	s, tester := uatBatchDesktopFixture(t)
	if err := tester.Click(uatBatchReleaseLabel + " · " + uatTestBatchID); err != nil {
		t.Fatal(err)
	}
	d := s.taskServicesUnit.uatBatchDialog
	s.taskServicesUnit.uatBatchSubmission = d
	result := uatBatchPreviewFixture(d.targets)
	for i := range result.Items {
		result.Items[i].Result = application.CLIUATOperationResult{Status: "not-sent"}
	}
	s.finishUATBatchRelease(d, application.ActionResult{Data: result}, errors.New("目标已变更"))
	tester.Frame()
	if !tester.HasText("未提交") || !tester.HasText("目标已变更") || tester.HasText("结果待确认") {
		t.Fatal("definite batch rejection was shown as an uncertain write")
	}
	if !tester.HasText("UAT 批量发布结果") || tester.HasText("查看发布结果") {
		t.Fatal("results belong to the active dialog only")
	}
}
