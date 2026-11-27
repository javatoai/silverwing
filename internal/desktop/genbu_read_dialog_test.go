package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func genbuTaskHistoryFixture(t *testing.T, page string) (*app, application.TagRecord) {
	t.Helper()
	s := taskServicesDesktopFixture(t)
	s.page, s.tab = page, taskTagTab
	s.inputs = map[string]*string{}
	s.taskUnit.state.tasks[0].archived = page == "archive"
	record := application.TagRecord{
		ID: "operation-17", TaskID: s.taskUnit.currentTask().id, ServiceID: "s1", ServiceName: "A service", ModuleID: "module-one",
		Tag: "release-2026.11", State: "SUCCESS", CreatedAt: "2026-11-20T00:01:00Z",
	}
	s.snapshot.TagHistory = []application.TagRecord{record}
	return s, record
}

type genbuReadTestCall struct {
	ctx    context.Context
	action application.Action
}

func genbuPendingReads(t *testing.T, s *app) <-chan genbuReadTestCall {
	t.Helper()
	s.win = &mygo.Window{}
	calls := make(chan genbuReadTestCall, 2)
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		select {
		case calls <- genbuReadTestCall{ctx: ctx, action: action}:
		case <-ctx.Done():
		}
		<-ctx.Done()
		return application.ActionResult{Data: application.GenbuBuildLogResult{Text: "late build log"}}, nil
	}}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTaskServices(ctx); err != nil {
			t.Error(err)
		}
	})
	return calls
}

func genbuAwaitRead(t *testing.T, calls <-chan genbuReadTestCall) genbuReadTestCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("Genbu read was not dispatched")
		return genbuReadTestCall{}
	}
}

func TestGenbuReadTaskHistoryOmitsBuildLogAction(t *testing.T) {
	for _, page := range []string{"tasks", "archive"} {
		t.Run(page, func(t *testing.T) {
			s, record := genbuTaskHistoryFixture(t, page)
			tester := ui.NewTester(func(c *ui.Context) {
				s.renderTagHistory(c, setTheme(c, false))
			}, 1100, 650)
			if !tester.HasText(record.Tag) || tester.HasText("构建日志") {
				t.Fatal("Tag history lost the record or retained the build log action")
			}
		})
	}
}

func TestGenbuReadProductionLogFindCopyAndTail(t *testing.T) {
	s, view, batch := productionFixture(t)
	batch.Target.GenbuService = "build-service"
	tag := production.Tag{Name: "2.3.44", Commit: batch.ReleaseSHA, Object: "original-tag-object"}
	batch.Tags = []production.Tag{tag}
	s.releaseUnit.state.production.batches[0] = batch
	s.openProductionBuildLog(batch, tag, func() *production.Batch { return s.productionBatch(view) })
	d := s.genbuBuildLog
	if d == nil || d.logRequest != nil || !strings.Contains(d.logError, "窗口或后端不可用") {
		t.Fatalf("opening a production log did not attempt the initial read: %+v", d)
	}
	d.tail = "1001"
	s.readGenbuBuildLog(d)
	if !strings.Contains(d.tailError, "1 到 1000") || d.logRequest != nil {
		t.Fatalf("out-of-range tail started a read: request=%v error=%q", d.logRequest, d.tailError)
	}
	d.tail = " 12 "
	s.readGenbuBuildLog(d)
	if d.tail != "12" || d.logRequest != nil || !strings.Contains(d.logError, "窗口或后端不可用") {
		t.Fatalf("valid tail was not normalized before the unavailable-backend result: %+v", d)
	}
	d.logError = ""
	d.loaded, d.text = true, "building application\nfinished\n"
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderGenbuReadDialogs(c, setTheme(c, false))
	}, 980, 650)
	tester.Frame()
	tester.Frame()
	if !tester.HasText(batch.Target.ServiceName+" · PROD · "+tag.Name) || tester.HasText("运行版本候选") || tester.HasText("搜索 Tag") ||
		!tester.HasText("刷新日志") || d.preview.view == nil || !d.preview.view.ReadOnly {
		t.Fatal("build log lost the selected Tag context, refresh, or read-only preview, or exposed a Tag selector")
	}
	if err := tester.Click("复制"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != d.text {
		t.Fatal("copy altered the build output")
	}
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Find") {
		t.Fatal("log editor find bar did not open")
	}
	tester.Key(0, ui.KeyEscape)
	if s.genbuBuildLog != d || tester.HasText("Find") {
		t.Fatal("first Escape did not dismiss only the log search bar")
	}
	tester.Key(0, ui.KeyEscape)
	if s.genbuBuildLog != nil || d.text != "" {
		t.Fatal("closing the log retained the previous output")
	}
}

func TestGenbuReadProductionLogClosesWhenDisplayedTagChanges(t *testing.T) {
	s, view, batch := productionFixture(t)
	batch.Target.GenbuService = "build-service"
	tag := production.Tag{Name: "2.3.44", Commit: batch.ReleaseSHA, Object: "original-tag-object"}
	batch.Tags = []production.Tag{tag}
	s.releaseUnit.state.production.batches[0] = batch
	calls := genbuPendingReads(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderProductionTagRecords(c, p, view, *s.productionBatch(view))
		s.renderGenbuReadDialogs(c, p)
	}, 980, 650)
	if err := tester.Click("构建日志"); err != nil {
		t.Fatal(err)
	}
	call := genbuAwaitRead(t, calls)
	d := s.genbuBuildLog
	if d == nil || d.tag != tag.Name || call.action.Kind != "production.build-log" ||
		call.action.Payload["batchId"] != batch.ID || call.action.Payload["tag"] != tag.Name || call.action.Payload["tailLines"] != 1000 {
		t.Fatalf("production log did not bind the displayed batch and Tag: %+v", call.action)
	}
	d.text = "old output"
	replacement := tag
	replacement.Object = "replacement-tag-object"
	s.releaseUnit.state.production.batches[0].Tags = []production.Tag{replacement}
	tester.Frame()
	if call.ctx.Err() != context.Canceled || s.genbuBuildLog != nil || d.text != "" {
		t.Fatal("production log retained the prior Tag object or output")
	}
}

func TestGenbuReadConfigApprovalListOpensRetriesAndPaginatesWithSameDate(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	calls := genbuPendingReads(t, s)
	s.openTaskServiceApprovals(v, row, 0)
	d := s.genbuApprovals
	if d == nil || d.source.row != row || d.openedAt.IsZero() {
		t.Fatalf("approval list opened without its service context or date: %+v", d)
	}
	openedAt := d.openedAt
	assertPage := func(page int) {
		t.Helper()
		action := genbuAwaitRead(t, calls).action
		if action.Kind != "task.services.approvals" || action.TaskID != v.identity.id || action.ServiceID != row.serviceID ||
			action.Payload["environment"] != row.environment || action.Payload["openedAt"] != openedAt || action.Payload["page"] != page {
			t.Fatalf("approval page %d lost its date or service context: %+v", page, action)
		}
		cancelGenbuRead(&d.listRequest)
	}
	assertPage(1)
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderGenbuReadDialogs(c, setTheme(c, false))
	}, 1100, 650)
	d.listError = "审批读取失败"
	tester.Frame()
	if err := tester.Click("重试本页"); err != nil {
		t.Fatal(err)
	}
	assertPage(1)
	d.listed = true
	d.list = production.GenbuApprovalPage{Page: 1, PageSize: 20}
	tester.Frame()
	for _, text := range []string{"审批状态", "审批类型", "分类", "日期类型", "开始日期", "结束日期", "查询", "本页没有符合当前服务及筛选条件的审批。", "上一页", "下一页", "第 1 / 1 页"} {
		if tester.HasText(text) {
			t.Fatalf("empty approval list still displays %q", text)
		}
	}
	d.list.Total = 21
	tester.Frame()
	if err := tester.Click("下一页"); err != nil {
		t.Fatal(err)
	}
	assertPage(2)
	d.listError = "审批读取失败"
	tester.Frame()
	if err := tester.Click("重试本页"); err != nil {
		t.Fatal(err)
	}
	assertPage(2)
	d.listed = true
	d.list = production.GenbuApprovalPage{Page: 2, PageSize: 20}
	tester.Frame()
	if err := tester.Click("上一页"); err != nil {
		t.Fatal(err)
	}
	assertPage(1)
}

func TestGenbuReadApprovalDetailSeparatesStatusAndExecutionResult(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	s.openTaskServiceApprovals(v, row, 42)
	d := s.genbuApprovals
	if d == nil || d.detailID != 42 || d.detailError == "" {
		t.Fatalf("approval detail did not attempt to read the receipt ID: %+v", d)
	}
	d.detailError = ""
	detail := production.GenbuApprovalDetail{GenbuApprovalItem: production.GenbuApprovalItem{
		ID: 42, Service: row.genbuName, Environment: "uat", Status: "approved", Result: "执行失败: 超时",
	}}
	d.detailID, d.detail, d.detailed = detail.ID, detail, true
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderGenbuReadDialogs(c, setTheme(c, false))
	}, 1100, 650)
	if !tester.HasText("已通过") || !tester.HasText("执行失败: 超时") || !tester.HasText("审批状态") || !tester.HasText("执行结果") {
		t.Fatalf("approval status and execution result were not presented independently: %v", tester.Texts())
	}
	s.closeGenbuReadDialogs()
}
