package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func catalogRuntimeDialogFixture(t *testing.T) *app {
	t.Helper()
	s := taskServicesDesktopFixture(t)
	s.page = "services"
	s.taskUnit.state.tasks = nil
	updateCatalogRuntimeService(t, s, "s1", func(service *domain.ServiceConfig) { service.Enabled = true })
	updateCatalogRuntimeService(t, s, "s2", func(service *domain.ServiceConfig) { service.Enabled = true })
	return s
}

func updateCatalogRuntimeService(t *testing.T, s *app, serviceID string, update func(*domain.ServiceConfig)) {
	t.Helper()
	var cfg domain.AppConfig
	if err := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	for i := range cfg.Services {
		if cfg.Services[i].ID == serviceID {
			update(&cfg.Services[i])
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON = string(data)
}

func expectCatalogRuntimeAction(t *testing.T, s *app, action application.Action, kind, serviceID, environment, mapping string) {
	t.Helper()
	cfg, err := s.readSnapshotAppConfig()
	if err != nil {
		t.Fatal(err)
	}
	scope := application.ServiceRuntimeContext{Home: s.snapshot.Home, GenbuServiceName: mapping, QueryIdentity: application.TaskServiceQueryIdentity(cfg)}
	if action.Kind != "service.runtime."+kind || action.ServiceID != serviceID || action.TaskID != "" || action.ModuleID != "" ||
		action.Path != "" || action.Text != "" || action.Payload["environment"] != environment || action.Payload["serviceRuntimeContext"] != scope {
		t.Fatalf("unexpected catalog runtime action: %+v", action)
	}
}

func TestCatalogRuntimeDialogsReadOnlyChosenServiceWithoutTask(t *testing.T) {
	s := catalogRuntimeDialogFixture(t)
	calls := genbuPendingReads(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderTaskServiceDialog(c, p)
		s.renderGenbuReadDialogs(c, p)
	}, 1000, 650)
	tester.Frame()
	if len(s.taskServicesUnit.jobs) != 0 || s.catalogRuntimeEnvironment() != "uat" {
		t.Fatal("the catalog queued a background query or entered in the wrong environment")
	}
	s.openCatalogRuntimeDialog("s1", taskServicePodsDetail)
	pods := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, pods.action, "pods", "s1", "uat", "alias-one")
	tester.Frame()
	if !tester.HasText("Pod 明细") || !tester.HasText("UAT · A service") || !tester.HasText("正在读取 Pod…") || len(s.taskServicesUnit.jobs) != 1 {
		t.Fatal("opening Pod details did not read and present only the selected catalog service")
	}
	s.selectCatalogRuntimeEnvironment("prod")
	if pods.ctx.Err() != context.Canceled || s.taskServicesUnit.catalog != nil {
		t.Fatal("switching environment kept the UAT request or dialog")
	}
	s.openCatalogRuntimeDialog("s2", taskServiceConfigDetail)
	config := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, config.action, "config", "s2", "prod", "alias-two")
	tester.Frame()
	if !tester.HasText("查看配置") || !tester.HasText("PROD · Second service") {
		t.Fatal("the configuration dialog did not show the selected service and environment")
	}
	productionView := s.taskServicesUnit.catalog
	s.openCatalogRuntimeDialog("s1", taskServiceConfigEditDetail)
	s.openCatalogRuntimeApprovals("s2")
	if config.ctx.Err() != nil || s.taskServicesUnit.catalog != productionView || s.genbuApprovals != nil || len(calls) != 0 {
		t.Fatal("a blocked PROD operation displaced the approved read dialog or dispatched a request")
	}
	s.selectCatalogRuntimeEnvironment("uat")
	s.openCatalogRuntimeDialog("s1", taskServiceConfigEditDetail)
	files := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, files.action, "config-files", "s1", "uat", "alias-one")
	if config.ctx.Err() != context.Canceled {
		t.Fatal("opening another service did not cancel the prior configuration read")
	}
	s.openCatalogRuntimeApprovals("s2")
	approvals := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, approvals.action, "approvals", "s2", "uat", "alias-two")
	tester.Frame()
	if files.ctx.Err() != context.Canceled || !tester.HasText("配置审批") || !tester.HasText("UAT · Second service") {
		t.Fatal("the approval dialog did not replace the old service operation")
	}
	s.page = "settings"
	tester.Frame()
	if approvals.ctx.Err() != context.Canceled || s.genbuApprovals != nil || s.catalogRuntimeEnvironment() != "uat" {
		t.Fatal("leaving the catalog retained an approval read or a production selection")
	}
}

func TestCatalogRuntimeRejectsChangedSavedMappingAndRetriesPodRead(t *testing.T) {
	s := catalogRuntimeDialogFixture(t)
	s.openCatalogRuntimeDialog("s1", taskServicePodsDetail)
	v := s.taskServicesUnit.catalog
	if v == nil || v.dialog == nil || v.dialog.row.error == "" {
		t.Fatal("failed initial Pod read did not open a retryable dialog")
	}
	tester := newTaskServiceConfigEditTester(s, 900, 650)
	calls := genbuPendingReads(t, s)
	if err := tester.Click("重试"); err != nil {
		t.Fatal(err)
	}
	read := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, read.action, "pods", "s1", "uat", "alias-one")
	updateCatalogRuntimeService(t, s, "s1", func(service *domain.ServiceConfig) { service.GenbuServiceName = "changed-mapping" })
	if s.taskServicesCurrent(v) {
		t.Fatal("a stale result could still address the former Genbu service")
	}
	tester.Frame()
	if read.ctx.Err() != context.Canceled || v.dialog != nil {
		t.Fatal("a saved mapping change kept the old Pod dialog and request")
	}
	s.openCatalogRuntimeDialog("s1", taskServicePodsDetail)
	expectCatalogRuntimeAction(t, s, genbuAwaitRead(t, calls).action, "pods", "s1", "uat", "changed-mapping")
}

func TestCatalogRuntimeUATApprovalAndPreviewLifecycle(t *testing.T) {
	s := catalogRuntimeDialogFixture(t)
	s.openCatalogRuntimeDialog("s1", taskServiceConfigEditDetail)
	v := s.taskServicesUnit.catalog
	dialog := v.dialog
	edit := bindTaskServiceConfigFixture(dialog, "config=old\n")
	edit.diff.New.Set("config=draft\n")
	edit.draftVersion = edit.diff.New.Version()
	edit.preview, edit.previewVersion = taskServiceConfigPreviewFixture(edit), edit.draftVersion
	calls := make(chan genbuReadTestCall, 2)
	release := make(chan struct{})
	s.win = &mygo.Window{}
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		calls <- genbuReadTestCall{ctx: ctx, action: action}
		if action.Kind == "service.runtime.config-submit" {
			<-release
		}
		return application.ActionResult{}, nil
	}}
	t.Cleanup(func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTaskServices(ctx); err != nil {
			t.Error(err)
		}
	})
	tester := newTaskServiceConfigEditTester(s, 900, 650)
	if err := tester.Click("提交配置审批"); err != nil {
		t.Fatal(err)
	}
	submit := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, submit.action, "config-submit", "s1", "uat", "alias-one")
	if submit.action.Payload["previewToken"] != "test-token" || submit.action.Payload["confirmed"] != true || edit.preview != nil {
		t.Fatal("submission did not consume the exact confirmed preview")
	}
	key := taskServiceConfigEditKey(v, dialog.row)
	s.selectCatalogRuntimeEnvironment("prod")
	if submit.ctx.Err() != nil || !s.taskServicesUnit.configSubmissions[key] || dialog.edit.diff != nil {
		t.Fatal("switching environments canceled an in-flight approval or retained the old draft")
	}
	s.finishTaskServiceConfigSubmit(v, dialog, key, application.ActionResult{}, &application.GenbuConfigPendingError{AttemptedAt: time.Now()}, true)
	if s.taskServicesUnit.configSubmissions[key] || !strings.Contains(s.lastError, "UAT 配置审批提交结果待确认") || s.genbuApprovals != nil {
		t.Fatal("a detached pending result did not notify the original environment or moved the new view")
	}
	// A still-unsubmitted preview is discarded on page exit with its original service scope.
	s.selectCatalogRuntimeEnvironment("uat")
	s.openCatalogRuntimeDialog("s2", taskServiceConfigEditDetail)
	expectCatalogRuntimeAction(t, s, genbuAwaitRead(t, calls).action, "config-files", "s2", "uat", "alias-two")
	unsent := s.taskServicesUnit.catalog.dialog
	edit = bindTaskServiceConfigFixture(unsent, "config=latest\n")
	edit.preview = taskServiceConfigPreviewFixture(edit)
	s.page = "settings"
	tester.Frame()
	discard := genbuAwaitRead(t, calls)
	expectCatalogRuntimeAction(t, s, discard.action, "config-preview-discard", "s2", "uat", "alias-two")
	if discard.action.Payload["previewToken"] != "test-token" || unsent.open || unsent.edit.diff != nil {
		t.Fatal("leaving the catalog retained an unsent preview or discarded the wrong token")
	}
}
