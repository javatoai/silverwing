package desktop

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestUATCatalogReleaseRemainsDirectInCompactRows(t *testing.T) {
	s, tester := catalogRuntimeListFixture(t)
	service := s.snapshot.Services[0]
	release := uatReleaseLabel + " · " + service.Name
	menu := "查看更多 " + service.Name
	for _, width := range []int{1250, 700} {
		tester.SetSize(width, 650)
		tester.Frame()
		root := ui.Rect{W: float32(width), H: 650}
		button := taskServicesBoundsWithin(t, tester, root, release)
		more := taskServicesBoundsWithin(t, tester, root, menu)
		if button.H != more.H || button.Y < more.Y-.5 || button.Y > more.Y+.5 || button.X+button.W > more.X {
			t.Fatalf("UAT actions did not align: %+v %+v", button, more)
		}
		for _, unavailable := range s.snapshot.Services[1:] {
			if _, found := tester.Find(uatReleaseLabel + " · " + unavailable.Name); found {
				t.Fatalf("service %s exposed UAT without an active mapping and probe", unavailable.Name)
			}
		}
		if tester.HasText(uatRestartLabel) {
			t.Fatal("UAT restart was shown outside the overflow menu")
		}
		if err := tester.Click(menu); err != nil {
			t.Fatal(err)
		}
		if !tester.HasText(uatRestartLabel) {
			t.Fatal("UAT overflow omitted restart")
		}
		tester.Key(0, ui.KeyEscape)
	}
	if err := tester.Click("PROD 环境"); err != nil {
		t.Fatal(err)
	}
	if _, found := tester.Find(release); found {
		t.Fatal("the PROD row exposed UAT release")
	}
	if err := tester.Click(menu); err != nil {
		t.Fatal(err)
	}
	if tester.HasText(uatRestartLabel) {
		t.Fatal("the PROD menu exposed UAT restart")
	}
}

func TestUATTaskServicesScrolledActionsRespectTaskAndServiceState(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	updateCatalogRuntimeService(t, s, "s1", func(service *domain.ServiceConfig) { service.Enabled = true })
	v := s.ensureTaskServicesCurrent()
	reads := genbuPendingReads(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTaskServices(c, p)
		s.renderUATPodActionDialog(c, p)
	}, 530, 480)
	tester.ScrollWith(ui.Shift, 265, 110, 0, float32(taskServiceTableMinWidth))
	if v.scroll.X <= 0 {
		t.Fatal("narrow service table did not scroll to its row actions")
	}
	root := ui.Rect{W: 530, H: 480}
	releaseLabel, restartLabel := uatReleaseLabel+" · A service", uatRestartLabel+" · A service"
	button := taskServicesBoundsWithin(t, tester, root, releaseLabel)
	restart := taskServicesBoundsWithin(t, tester, root, restartLabel)
	texts := tester.Texts()
	if button.W != taskServicePublishWidth || restart.X < button.X+button.W || !slices.Contains(texts, "发布") || !slices.Contains(texts, "重启") {
		t.Fatal("scrolled service table lost inline release or restart")
	}
	if _, found := tester.Find("更多 · A service"); found {
		t.Fatal("task service still showed an overflow menu")
	}
	for _, operation := range []string{uatReleaseLabel, uatRestartLabel} {
		if _, found := tester.Find(operation + " · Second service"); found {
			t.Fatalf("disabled task service exposed %s", operation)
		}
	}
	if err := tester.Click(restartLabel); err != nil {
		t.Fatal(err)
	}
	read := genbuAwaitRead(t, reads).action
	scope, ok := read.Payload["taskServicesContext"].(application.TaskServicesContext)
	d := s.taskServicesUnit.uatDialog
	if d == nil || d.operation != uatRestart || !tester.HasText("A service · UAT") ||
		read.Kind != "task.services.uat-preview" || read.ServiceID != "s1" || read.TaskID != v.identity.id ||
		read.Payload["environment"] != "uat" || read.Payload["operation"] != uatRestart || read.Payload["tag"] != "" ||
		!ok || scope.TaskPath != v.identity.taskPath || scope.GenbuServiceName != "alias-one" || v.dialog != nil {
		t.Fatalf("inline restart did not open a preview for the chosen task service: %+v", read)
	}
	cancelGenbuRead(&d.previewRequest)
	s.finishUATPodPreview(d, "", application.ActionResult{Data: *uatPreviewFixture(d)}, nil)
	tester.Frame()
	if !tester.HasText("确认重启 UAT Pod") || !tester.HasText("core-workers · ID 12") || s.taskServicesUnit.uatSubmission != nil {
		t.Fatal("restart preview did not wait for explicit confirmation")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if s.taskServicesUnit.uatDialog != nil || s.taskServicesUnit.uatSubmission != nil || len(reads) != 0 {
		t.Fatal("canceling restart did not dismiss the preview without submitting")
	}
	if err := tester.Click("PROD 环境"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{releaseLabel, restartLabel} {
		if _, found := tester.Find(label); found {
			t.Fatalf("task PROD view exposed %s", label)
		}
	}
	s.page = "archive"
	s.taskUnit.state.tasks[0].archived = true
	tester.SetSize(1200, 480)
	tester.Frame()
	if err := tester.Click("UAT 环境"); err != nil {
		t.Fatal(err)
	}
	if _, found := tester.Find("A service uat Pod 明细"); !found {
		t.Fatal("archived task lost its read-only view")
	}
	for _, label := range []string{releaseLabel, restartLabel} {
		if _, found := tester.Find(label); found {
			t.Fatalf("archived task service exposed %s", label)
		}
	}
}

func TestUATTaskServicesDirectClickReadsOnlyChosenServiceTags(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	updateCatalogRuntimeService(t, s, "s1", func(service *domain.ServiceConfig) { service.Enabled = true })
	v := s.ensureTaskServicesCurrent()
	reads := genbuPendingReads(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTaskServices(c, p)
		s.renderUATPodActionDialog(c, p)
	}, 580, 500)
	tester.ScrollWith(ui.Shift, 290, 110, 0, float32(taskServiceTableMinWidth))
	if err := tester.Click(uatReleaseLabel + " · A service"); err != nil {
		t.Fatal(err)
	}
	read := genbuAwaitRead(t, reads).action
	scope, ok := read.Payload["taskServicesContext"].(application.TaskServicesContext)
	if read.Kind != "task.services.uat-tags" || read.ServiceID != "s1" || read.TaskID != v.identity.id || read.Payload["environment"] != "uat" ||
		!ok || scope.TaskPath != v.identity.taskPath || scope.GenbuServiceName != "alias-one" || v.dialog != nil {
		t.Fatalf("direct action read a different task service or triggered row details: %+v", read)
	}
}

func uatPreviewFixture(d *uatPodActionDialog) *application.CLIUATOperationResult {
	return &application.CLIUATOperationResult{
		Action: d.operation, Status: "preview", Service: d.service, GenbuService: d.genbuService,
		Environment: "UAT", Group: "core-workers", GroupID: 12, Namespace: "test-suite", Tag: d.tag,
		PodCount: 2, Preflight: "exact-preview-token",
	}
}

func newUATDesktopTester(s *app) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTaskServiceDialog(c, p)
		s.renderUATPodActionDialog(c, p)
	}, 740, 520)
}

func TestUATCatalogTagSelectionConfirmationAndDetachedReceipt(t *testing.T) {
	s := catalogRuntimeDialogFixture(t)
	s.win = &mygo.Window{}
	calls := make(chan genbuReadTestCall, 3)
	finish, stop := asyncLifecycleGate(t)
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, req application.Action) (application.ActionResult, error) {
		calls <- genbuReadTestCall{ctx: ctx, action: req}
		if req.Kind == "service.runtime.uat-submit" {
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
	s.openCatalogUATPodAction("s1", uatRelease)
	read := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, read.action, "uat-tags", "s1", "uat", "alias-one")
	if read.action.Payload["keyword"] != "" || read.action.Payload["page"] != 1 {
		t.Fatal("initial release did not load the service Tag list")
	}
	d := s.taskServicesUnit.uatDialog
	cancelGenbuRead(&d.tagsRequest)
	tag := "3.3.44.beta-1"
	d.tags, d.tagsLoaded = production.GenbuTagPage{Page: 1, PageSize: 10, Total: 1, Items: []production.GenbuTagItem{{Tag: tag}}}, true
	tester := newUATDesktopTester(s)
	tester.Frame()
	if !tester.HasText("A service · UAT") || tester.HasText("确认发布到 UAT") || tester.HasText("检查发布目标") {
		t.Fatal("UAT release did not identify the service or skipped the preview")
	}
	if err := tester.Click("选择 Tag " + tag); err != nil {
		t.Fatal(err)
	}
	if d.tag != tag || len(calls) != 0 {
		t.Fatal("selecting an exact Tag dispatched an operation")
	}
	if err := tester.Click(uatReleaseLabel); err != nil {
		t.Fatal(err)
	}
	read = genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, read.action, "uat-preview", "s1", "uat", "alias-one")
	if read.action.Payload["tag"] != tag || read.action.Payload["operation"] != uatRelease {
		t.Fatal("preview lost the chosen exact Tag")
	}
	if !tester.HasText("正在准备发布…") || len(calls) != 0 {
		t.Fatal("publication should wait for the preview result")
	}
	cancelGenbuRead(&d.previewRequest)
	preview := uatPreviewFixture(d)
	s.finishUATPodPreview(d, tag, application.ActionResult{Data: *preview}, nil)
	submit := genbuAwaitRead(t, calls)
	tester.Frame()
	for _, text := range []string{"alias-one", "test-suite", "core-workers · ID 12", "目标副本数", "确认发布到 UAT"} {
		if tester.HasText(text) {
			t.Fatalf("release exposed preflight detail %q: %v", text, tester.Texts())
		}
	}
	expectCatalogRuntimeAction(t, s, submit.action, "uat-submit", "s1", "uat", "alias-one")
	if submit.action.Payload["preflight"] != preview.Preflight || submit.action.Payload["tag"] != tag || !s.documentCloseOperationBusy() {
		t.Fatal("release did not use the exact confirmed preflight or keep its write alive")
	}
	s.selectCatalogRuntimeEnvironment("prod")
	if s.taskServicesUnit.uatDialog != nil || s.taskServicesUnit.uatSubmission != d || submit.ctx.Err() != nil {
		t.Fatal("changing environments retained an old dialog or canceled the dispatched write")
	}
	receipt := *preview
	receipt.Status = "submitted"
	s.finishUATPodAction(d, application.ActionResult{Data: receipt}, errors.New("模拟回执保存失败"))
	if s.taskServicesUnit.uatSubmission != nil || d.receipt != "submitted" || !strings.Contains(s.lastMessage, tag) ||
		!strings.Contains(s.lastMessage, "已提交") || !strings.Contains(s.lastMessage, "回执记录异常") || s.catalogRuntimeEnvironment() != "prod" {
		t.Fatal("detached receipt lost its original UAT scope or changed the new environment")
	}
}

func TestUATTagHistoryPinsTagAndUnknownShowsReusablePodDetails(t *testing.T) {
	s, record := genbuTaskHistoryFixture(t, "tasks")
	updateCatalogRuntimeService(t, s, record.ServiceID, func(service *domain.ServiceConfig) { service.Enabled = true })
	record.GenbuStatus.Build = domain.GenbuSuccess
	s.snapshot.TagHistory = []application.TagRecord{record}
	reads := genbuPendingReads(t, s)
	state := s.ensureTagHistoryView()
	s.taskServicesUnit.current = &taskServicesView{environment: "prod"}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTagHistory(c, p)
		s.renderTaskServiceDialog(c, p)
		s.renderUATPodActionDialog(c, p)
	}, 1040, 600)
	if err := tester.Click(tagHistoryPublishLabel + " · " + record.Tag); err != nil {
		t.Fatal(err)
	}
	d := s.taskServicesUnit.uatDialog
	tester.Frame()
	if d.pinned != record.Tag || tester.HasText("搜索 Tag") || !tester.HasText("Tag · "+record.Tag) || len(reads) != 0 {
		t.Fatal("opening a pinned Tag offered a picker or sent a precheck")
	}
	if err := tester.Click(uatReleaseLabel); err != nil {
		t.Fatal(err)
	}
	preview := genbuAwaitRead(t, reads).action
	if preview.Kind != "tag.history.uat-preview" || preview.TaskID != record.TaskID || preview.ServiceID != record.ServiceID ||
		preview.ModuleID != record.ModuleID || preview.Payload["operationId"] != record.ID || preview.Payload["tag"] != record.Tag ||
		preview.Payload["tagHistoryTaskPath"] != state.TaskPath || preview.Payload["tagHistoryTaskCreatedAt"] != state.TaskCreatedAt {
		t.Fatalf("Tag history lost the originating worktree or Tag: %+v", preview)
	}
	cancelGenbuRead(&d.previewRequest)
	s.taskServicesUnit.uatSubmission = d
	receipt := *uatPreviewFixture(d)
	receipt.Status = "unknown"
	s.finishUATPodAction(d, application.ActionResult{Data: receipt}, errors.New("simulated lost receipt"))
	tester.Frame()
	if !tester.HasText("结果待确认") || tester.HasText("确认发布到 UAT") {
		t.Fatal("uncertain write did not discourage resubmitting")
	}
	if err := tester.Click("查看 Pod 明细"); err != nil {
		t.Fatal(err)
	}
	pods := genbuAwaitRead(t, reads).action
	if pods.Kind != "task.services.pods" || pods.TaskID != record.TaskID || pods.ServiceID != record.ServiceID || pods.Payload["environment"] != "uat" || s.tab != taskTagTab {
		t.Fatalf("Tag history did not open the same Pod details in place: %+v", pods)
	}
	if !tester.HasText("Pod 明细") || s.taskServicesUnit.uatDialog != nil {
		t.Fatal("existing Pod dialog did not replace the release receipt")
	}
}

func TestUATReleasePreflightDoesNotSubmitMismatchedOrCanceledResult(t *testing.T) {
	s := catalogRuntimeDialogFixture(t)
	reads := genbuPendingReads(t, s)
	s.openCatalogUATPodAction("s1", uatRelease)
	genbuAwaitRead(t, reads)
	d := s.taskServicesUnit.uatDialog
	d.tag = "3.3.44.beta-1"
	s.previewUATPodAction(d)
	first := genbuAwaitRead(t, reads)
	if first.action.Kind != "service.runtime.uat-preview" {
		t.Fatal(first.action)
	}
	s.previewUATPodAction(d)
	if len(reads) != 0 || first.ctx.Err() != nil {
		t.Fatal("a repeated action replaced the pending precheck")
	}
	cancelGenbuRead(&d.previewRequest)
	wrong := *uatPreviewFixture(d)
	wrong.GenbuService = "other-service"
	s.finishUATPodPreview(d, d.tag, application.ActionResult{Data: wrong}, nil)
	if d.failure == "" || s.taskServicesUnit.uatSubmission != nil || len(reads) != 0 {
		t.Fatal("mismatched precheck reached submission")
	}
	s.previewUATPodAction(d)
	retry := genbuAwaitRead(t, reads)
	s.selectCatalogRuntimeEnvironment("prod")
	s.finishUATPodPreview(d, d.tag, application.ActionResult{Data: *uatPreviewFixture(d)}, nil)
	if retry.ctx.Err() != context.Canceled || s.taskServicesUnit.uatDialog != nil || s.taskServicesUnit.uatSubmission != nil || len(reads) != 0 {
		t.Fatal("canceled precheck reached submission after environment change")
	}
}

func TestUATRestartPreviewDropsOnMappingChange(t *testing.T) {
	s := catalogRuntimeDialogFixture(t)
	reads := genbuPendingReads(t, s)
	s.openCatalogUATPodAction("s1", uatRestart)
	read := genbuAwaitRead(t, reads)
	expectCatalogRuntimeAction(t, s, read.action, "uat-preview", "s1", "uat", "alias-one")
	if read.action.Payload["operation"] != uatRestart || read.action.Payload["tag"] != "" || read.action.Payload["preflight"] != nil {
		t.Fatal("restart checked a Tag or sent a mutation without confirmation")
	}
	tester := newUATDesktopTester(s)
	tester.Frame()
	if tester.HasText("发布 Tag") || !tester.HasText(uatRestartLabel) {
		t.Fatal("restart offered a Tag instead of the current UAT group")
	}
	updateCatalogRuntimeService(t, s, "s1", func(service *domain.ServiceConfig) { service.GenbuServiceName = "new-alias" })
	tester.Frame()
	if read.ctx.Err() != context.Canceled || s.taskServicesUnit.uatDialog != nil || tester.HasText(uatRestartLabel) {
		t.Fatal("a stale mapping retained an actionable preview")
	}
}
