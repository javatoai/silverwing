package desktop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func genbuApprovalReviewFixture(t *testing.T, page string) (*app, *genbuApprovalsDialogState) {
	t.Helper()
	s := taskServicesDesktopFixture(t)
	view, row := activateTaskServicesFixture(t, s, page, "uat")
	s.openTaskServiceApprovals(view, row, 42)
	d := s.genbuApprovals
	if d == nil {
		t.Fatal("UAT approval detail did not open")
	}
	d.detailError = ""
	s.setGenbuApprovalDetail(d, production.GenbuApprovalDetail{
		GenbuApprovalItem: production.GenbuApprovalItem{
			ID: 42, Service: row.genbuName, Environment: "uat", Status: "待处理", Result: "未处理", Creator: "fixture",
		},
		Changes: []production.GenbuApprovalConfigChange{
			{File: "application.yml", Before: "enabled: false\n", After: "enabled: true\n", BeforeExists: true, AfterExists: true},
			{File: "new.properties", After: "workers=3\n", AfterExists: true},
		},
		CanApprove: true, Revision: "reviewed-revision",
	})
	return s, d
}

func newGenbuApprovalReviewTester(s *app, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderGenbuReadDialogs(c, p)
		s.renderConfirmationDialog(c, p)
	}, width, height)
}

func TestGenbuApprovalReviewShowsReadOnlySplitAndSwitchesFiles(t *testing.T) {
	s, d := genbuApprovalReviewFixture(t, "tasks")
	tester := newGenbuApprovalReviewTester(s, 780, 680)
	tester.Frame()
	var panes []ui.AccessInfo
	for _, field := range tester.AccessAll() {
		if field.Role == ui.RoleTextField {
			panes = append(panes, field)
		}
	}
	if len(panes) != 2 || !panes[0].ReadOnly || !panes[1].ReadOnly || panes[0].Bounds.X >= panes[1].Bounds.X ||
		panes[0].Bounds.Y != panes[1].Bounds.Y || panes[0].Bounds.H <= 100 || panes[1].Bounds.H <= 100 ||
		d.comparison == nil || d.comparison.Layout != editor.DiffSplit ||
		d.comparison.Old.OriginalText() != "enabled: false\n" || d.comparison.New.OriginalText() != "enabled: true\n" ||
		!tester.HasText("当前配置（读取时）") || !tester.HasText("审批变更") || !tester.HasText("待审批") || !tester.HasText("未处理") {
		t.Fatal("approval changes are missing or do not fit in two read-only panes")
	}
	for _, button := range []string{"选择审批配置文件", "new.properties"} {
		if err := tester.Click(button); err != nil {
			t.Fatal(err)
		}
	}
	if !tester.HasText("当前配置（文件不存在）") || d.comparisonFile != "new.properties" ||
		d.comparison.Old.OriginalText() != "" || d.comparison.New.OriginalText() != "workers=3\n" {
		t.Fatal("approval details did not retain metadata and show the selected added file")
	}
	s.closeGenbuReadDialogs()
	if d.comparison != nil || len(d.detail.Changes) != 0 {
		t.Fatal("closing approval review retained configuration data")
	}
}

func TestGenbuApprovalReviewMetadataPopupPreservesComparisonAndScopesInput(t *testing.T) {
	s, d := genbuApprovalReviewFixture(t, "tasks")
	s.win = &mygo.Window{}
	d.detail.Changes[1].After = strings.Repeat("workers=3\n", 100)
	d.detail.Details = make(map[string]string)
	for i := 1; i <= 50; i++ {
		d.detail.Details[fmt.Sprintf("附加信息 %02d", i)] = fmt.Sprintf("信息内容 %02d", i)
	}
	tester := newGenbuApprovalReviewTester(s, 1100, 680)
	for _, button := range []string{"选择审批配置文件", "new.properties"} {
		if err := tester.Click(button); err != nil {
			t.Fatal(err)
		}
	}
	comparison := d.comparison
	comparison.SetScrollY(120)
	tester.Frame()
	viewportHeight, scrollY := comparison.ViewportHeight(), comparison.ScrollY()
	headerBounds, _ := tester.Find("审批变更")
	approve, ok := tester.Access("同意审批")
	if !ok || viewportHeight <= 100 || scrollY <= 0 || headerBounds.W <= 0 {
		t.Fatal("comparison fixture needs a scrolled diff and an actionable approval")
	}
	if err := tester.Click("审批信息"); err != nil {
		t.Fatal(err)
	}
	popup, ok := tester.AccessByRole(ui.RoleDialog, "审批信息 #42")
	headerAfter, _ := tester.Find("审批变更")
	if !ok || popup.Bounds.H <= 260 || popup.Description != d.source.view.dialogScope(d.source.row) ||
		!tester.HasText("创建人") || !tester.HasText("fixture") || !d.metadataOpen ||
		d.comparison != comparison || d.comparisonFile != "new.properties" ||
		comparison.ViewportHeight() != viewportHeight || comparison.ScrollY() != scrollY || headerBounds != headerAfter {
		t.Fatalf("metadata popup did not preserve the selected comparison: %+v", popup)
	}
	tester.Key(0, ui.KeyTab)
	if _, found := tester.Access("同意审批"); found {
		t.Fatal("approval action remained accessible under the metadata popup")
	}
	for _, node := range tester.AccessAll() {
		if node.Focused && node.Role == ui.RoleButton && node.Name != "关闭" {
			t.Fatalf("tab moved focus out of the top dialog to %q", node.Name)
		}
	}
	metadataTop, _ := tester.Find("创建人")
	tester.Wheel(popup.Bounds.X+popup.Bounds.W/2, popup.Bounds.Y+popup.Bounds.H/2, 0, 400)
	tester.Advance(300 * time.Millisecond)
	metadataScrolled, _ := tester.Find("创建人")
	if metadataScrolled.Y >= metadataTop.Y || comparison.ScrollY() != scrollY {
		t.Fatal("metadata did not scroll independently of the diff")
	}
	tester.Key(0, ui.KeyEscape)
	if !d.open || d.metadataOpen || d.comparison != comparison || s.genbuApprovals != d {
		t.Fatalf("Escape failed to close only metadata: detail=%t metadata=%t sameComparison=%t current=%t",
			d.open, d.metadataOpen, d.comparison == comparison, s.genbuApprovals == d)
	}
	if err := tester.Click("审批信息"); err != nil {
		t.Fatal(err)
	}
	tester.ClickAt(approve.Bounds.X+approve.Bounds.W/2, approve.Bounds.Y+approve.Bounds.H/2)
	if !d.open || d.metadataOpen || s.confirmationDialog != nil {
		t.Fatal("metadata backdrop click reached the underlying approval action")
	}
	if err := tester.Click("审批信息"); err != nil {
		t.Fatal(err)
	}
	closeButton, ok := tester.Access("关闭")
	if !ok {
		t.Fatal("metadata popup is missing its own close button")
	}
	tester.ClickAt(closeButton.Bounds.X+closeButton.Bounds.W/2, closeButton.Bounds.Y+closeButton.Bounds.H/2)
	if !d.open || d.metadataOpen || d.comparison != comparison || comparison.ScrollY() != scrollY {
		t.Fatal("closing metadata changed or closed the approval comparison")
	}
	tester.Key(0, ui.KeyEscape)
	if d.open || s.genbuApprovals != nil {
		t.Fatal("comparison did not close with Escape after the popup had closed")
	}
}

func TestGenbuApprovalReviewMetadataPopupClosesOnScopeChange(t *testing.T) {
	s, d := genbuApprovalReviewFixture(t, "tasks")
	tester := newGenbuApprovalReviewTester(s, 900, 680)
	if err := tester.Click("审批信息"); err != nil {
		t.Fatal(err)
	}
	if !s.selectTaskServicesEnvironment(d.source.view, "prod") {
		t.Fatal("test could not switch the task service to PROD")
	}
	tester.Frame()
	if d.open || d.metadataOpen || s.genbuApprovals != nil || tester.HasText("审批信息 #42") {
		t.Fatal("metadata popup retained the old UAT approval after changing environment")
	}
}

func TestGenbuApprovalReviewConfirmsExactScopeAndLocksUnknownResult(t *testing.T) {
	s, d := genbuApprovalReviewFixture(t, "tasks")
	s.win = &mygo.Window{}
	calls, finish := make(chan application.Action, 2), make(chan struct{})
	s.backend = &asyncLifecycleBackend{dispatch: func(_ context.Context, action application.Action) (application.ActionResult, error) {
		calls <- action
		<-finish
		return application.ActionResult{}, production.ErrGenbuApprovalUnknown
	}}
	t.Cleanup(func() {
		close(finish)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTaskServices(ctx); err != nil {
			t.Error(err)
		}
	})
	tester := newGenbuApprovalReviewTester(s, 1100, 680)
	for _, button := range []string{"同意审批", "取消", "同意审批"} {
		if err := tester.Click(button); err != nil {
			t.Fatal(err)
		}
	}
	confirmation := s.confirmationDialog
	if len(calls) != 0 || confirmation == nil || len(confirmation.Summary) != 3 ||
		confirmation.Summary[0].Value != "#42" || confirmation.Summary[1].Value != d.source.row.genbuName ||
		confirmation.Summary[2].Value != "UAT" || confirmation.DefaultIndex != confirmation.CancelIndex {
		t.Fatal("approval either skipped confirmation or omitted its scope")
	}
	if err := tester.Click("确认同意"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-calls:
		if action.Kind != "task.services.approval-approve" || action.TaskID != d.source.view.identity.id || action.ServiceID != d.source.row.serviceID ||
			action.Payload["environment"] != "uat" || action.Payload["approvalId"] != "42" || action.Payload["revision"] != d.detail.Revision {
			t.Fatalf("approval lost its reviewed scope: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation did not dispatch the approval")
	}
	pending := d.detail
	s.finishGenbuApproval(d, pending.ID, application.ActionResult{Data: pending}, production.ErrGenbuApprovalUnknown, true)
	tester.Frame()
	button, found := tester.Access("同意审批")
	if !found || !button.Disabled || !tester.HasText("审批结果待确认，请刷新详情。") {
		t.Fatal("ambiguous receipt left approval actionable or hid the result")
	}
	s.setGenbuApprovalDetail(d, pending)
	s.submitGenbuApproval(d, pending.ID, pending.Revision)
	if len(calls) != 0 || s.taskServicesUnit.approvalWrites[pending.ID] != genbuApprovalOutcomeUnknown {
		t.Fatal("rereading a pending approval allowed an ambiguous submission to be repeated")
	}
	pending.Status, pending.CanApprove = "已通过", false
	s.setGenbuApprovalDetail(d, pending)
	tester.Frame()
	if !tester.HasText("已通过") || tester.HasText("同意审批") || tester.HasText("审批结果待确认，请刷新详情。") {
		t.Fatal("verified outcome did not replace the uncertain state")
	}
}

func TestGenbuApprovalReviewArchivedTaskHasNoApproveAction(t *testing.T) {
	s, _ := genbuApprovalReviewFixture(t, "archive")
	tester := newGenbuApprovalReviewTester(s, 900, 700)
	if tester.HasText("同意审批") || !tester.HasText("当前配置（读取时）") || !tester.HasText("审批变更") {
		t.Fatal("archived task approval should let the user read the comparison only")
	}
}

func TestGenbuApprovalReviewDetachedReceiptPreservesOtherDetail(t *testing.T) {
	s, submitted := genbuApprovalReviewFixture(t, "tasks")
	receipt := submitted.detail
	receipt.Status, receipt.CanApprove = "已通过", false
	s.taskServicesUnit.approvalWrites = map[int64]genbuApprovalWriteState{receipt.ID: genbuApprovalSubmitting}
	s.closeGenbuReadDialogs()
	s.openTaskServiceApprovals(submitted.source.view, submitted.source.row, 43)
	active := s.genbuApprovals
	if active == nil || active.detailID != 43 {
		t.Fatal("next approval could not be opened")
	}
	s.finishGenbuApproval(submitted, receipt.ID, application.ActionResult{Data: receipt}, nil, true)
	if s.genbuApprovals != active || active.detailID != 43 || active.detailed ||
		s.lastMessage != submitted.source.row.name+" · UAT · 审批 #42 已通过" {
		t.Fatal("approval receipt overwrote the newly opened detail or lost its original scope")
	}
}
