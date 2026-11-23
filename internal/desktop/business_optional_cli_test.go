package desktop

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"silverwing-go/internal/application"
	"silverwing-go/internal/asyncjob"
	"silverwing-go/internal/production"
	"silverwing-go/internal/releasenotice"
)

func TestOptionalGenbuHidesCurrentServiceAndProductionPages(t *testing.T) {
	for _, page := range []string{"tasks", "archive"} {
		t.Run(page, func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			s.page = page
			s.taskUnit.state.tasks[0].archived = page == "archive"
			view := s.ensureTaskServicesCurrent()
			if view == nil || !s.taskServicesPageActive() {
				t.Fatal("enabled service page did not load")
			}
			s.integrationContext("genbu")
			setTestPlatformSwitch(s, "genbu", false)
			if s.page != page || s.tab == taskServicesTab || view.active || s.taskServicesPageActive() || s.selectTaskContentTab(taskServicesTab) {
				t.Fatal("disabled service page was reachable or still active")
			}
			visible := s.visibleTaskContentTabs()
			if slices.Contains(visible, taskServicesTab) || !slices.Contains(visible, taskFilesTab) || !slices.Contains(visible, taskTagTab) {
				t.Fatalf("disabled service page affected unrelated tabs: %v", visible)
			}
			s.page = "production"
			s.reconcileSidebarNavigation()
			if s.page != "tasks" || slices.ContainsFunc(s.sidebarNavigationItems(), func(item sidebarNavigationItem) bool { return item.page == "production" }) {
				t.Fatal("disabled Genbu left the production route available")
			}
		})
	}
}

func TestNativeGenbuWaitsForSettingsOnExplicitStartupRoute(t *testing.T) {
	for _, route := range []Options{{Page: "production"}, {Page: "tasks", Tab: taskServicesTab}, {Page: "archive", Tab: taskServicesTab}} {
		for _, available := range []bool{true, false} {
			name := route.Page + "/missing"
			if available {
				name = route.Page + "/available"
			}
			t.Run(name, func(t *testing.T) {
				s := newApp(optionalCLIDesktopProbeBackend{}, route)
				if route.Page == "production" {
					s.desktopPreferences.value.HiddenTaskTabs = []string{"DETAIL"}
				}
				s.reconcileTaskContentTabs()
				if s.page != route.Page || s.tab != route.Tab {
					t.Fatal("initial route was discarded before loading configuration")
				}
				s.loading = false
				s.snapshot.Home = t.TempDir()
				state := s.ensurePlatformConnections()
				read, err := s.settingsUnit.state.platformJobs.Begin(context.Background(), asyncjob.Options{})
				if err != nil {
					t.Fatal(err)
				}
				state.read = read
				t.Cleanup(read.Cancel)
				s.resolveInitialGenbuRoute()
				s.reconcileTaskContentTabs()
				s.reconcileSidebarNavigation()
				if s.page != route.Page || s.tab != route.Tab || s.captureReady() || s.taskServicesPageActive() || s.releaseUnit.state.production != nil {
					t.Fatal("pending platform settings read exposed, started, or discarded the selected route")
				}
				read.Complete(nil)
				state.read = nil
				s.publishPlatformConnections(state, application.PlatformConnections{Username: "example", Configured: true, GenbuEnabled: available})
				s.resolveInitialGenbuRoute()
				s.reconcileTaskContentTabs()
				if s.initialGenbuRouteWaiting() {
					t.Fatal("completed settings read left the initial route pending")
				}
				if available {
					if s.page != route.Page || route.Page != "production" && s.tab != route.Tab {
						t.Fatal("ready Genbu did not preserve the explicit route")
					}
					if route.Page == "production" && (s.releaseUnit.state.production == nil || s.releaseUnit.state.production.loadedHome != s.snapshot.Home) {
						t.Fatal("production route did not enter after the probe completed")
					}
				} else if s.page == "production" || s.tab == taskServicesTab || s.releaseUnit.state.production != nil {
					t.Fatal("unavailable Genbu left a business route selected")
				}
			})
		}
	}
}

func TestOptionalGenbuStopsProductionProbeAndDropsQueuedResult(t *testing.T) {
	s, view, batch := productionFixture(t)
	state := s.releaseUnit.state.production
	backend := &productionTestBackend{calls: make(chan application.Action, 2), replies: make(chan productionTestAnswer, 1)}
	window := &refreshTestWindow{updates: make(chan func(), 8)}
	s.backend, state.window = backend, window
	batch.Tags = []production.Tag{{Name: "2.3.44", PushedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	s.scheduleProductionProbe(view, batch, window)
	if len(state.timers) != 1 {
		t.Fatal("enabled Genbu did not schedule the background probe")
	}
	s.productionRequest(application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": batch.ID}}, "background-probe")
	select {
	case <-backend.calls:
	case <-time.After(time.Second):
		t.Fatal("background probe did not start")
	}
	batch.Message = "late Genbu result"
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &batch}}}
	var apply func()
	select {
	case apply = <-window.updates:
	case <-time.After(time.Second):
		t.Fatal("background probe did not queue its result")
	}
	setTestPlatformSwitch(s, "genbu", false)
	if s.page != "tasks" || len(state.timers) != 0 || view.Preview != nil || !view.PreparationInvalid {
		t.Fatal("disabled production retained a timer, preview, or active route")
	}
	setTestPlatformSwitch(s, "genbu", true)
	apply()
	if view.Busy || len(state.timers) != 0 || state.batches[0].Message == batch.Message {
		t.Fatal("re-enabling Genbu revived a result from the previous account scope")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.releaseUnit.closeProductionJobs(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalLarkHidesSendAndKeepsBranchCopies(t *testing.T) {
	s, _, group := releaseNoticeDesktopFixture(t)
	s.tab = 0
	s.openTaskBranchInfo(s.taskUnit.currentTask())
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderTaskBranchInfoDialog(c, setTheme(c, false))
	}, 1000, 630)
	button := s.noticeButtonText(releasenotice.Branches, "")
	if _, found := tester.Find(button); !found {
		t.Fatal("enabled Lark did not show the branch send action")
	}
	s.optionalCLIContext("lark")
	ctx, cancel := context.WithCancel(context.Background())
	s.releaseNotices.pending.cancel = cancel
	s.settingsUnit.state.configJSON = `{"optionalCLIs":{"lark":false,"genbu":true}}`
	s.observeOptionalCLIs()
	tester.Frame()
	if _, found := tester.Find(button); found || s.releaseNoticeReady(group) || s.releaseNotices.pending != nil || ctx.Err() == nil {
		t.Fatal("disabled Lark left a send action or pending notification active")
	}
	for _, label := range []string{"复制服务名称", "复制服务与分支"} {
		if _, found := tester.Find(label); !found {
			t.Fatalf("disabled Lark hid the local action %q", label)
		}
	}
	s.settingsUnit.state.configJSON = `{"optionalCLIs":{"lark":true}}`
	setTestPlatformSwitch(s, "genbu", false)
	tester.Frame()
	if _, found := tester.Find(button); !found || s.releaseNoticeReady(group) {
		t.Fatal("Genbu disabled the independent Lark branch send or enabled Tag send")
	}
}

func TestOptionalLarkKeepsAdmittedSendAndReceiptAfterDisable(t *testing.T) {
	s, d, group := releaseNoticeDesktopFixture(t)
	prepCtx, cancel := context.WithCancel(s.optionalCLIContext("lark"))
	defer cancel()
	d.cancel = cancel
	session := *d.session
	started := make(chan context.Context, 1)
	finished := make(chan bool, 1)
	reply := make(chan struct{})
	defer close(reply)
	go func() {
		prepare, send := application.Action{Kind: "prepare"}, application.Action{Kind: "send"}
		result, err := executeNoticeSend(prepCtx, prepare, send, func(ctx context.Context, action application.Action) (application.ActionResult, error) {
			if action.Kind == "send" {
				started <- ctx
				select {
				case <-ctx.Done():
					return application.ActionResult{}, ctx.Err()
				case <-reply:
				}
				session.Receipts = []releasenotice.Receipt{{OpenID: session.Identity.OpenID, State: "sent", MessageID: "receipt"}}
			}
			return application.ActionResult{Data: session}, nil
		}, nil)
		receipt, found := result.LatestReceipt()
		finished <- err == nil && found && receipt.MessageID == "receipt"
	}()
	var sendCtx context.Context
	select {
	case sendCtx = <-started:
	case <-time.After(time.Second):
		t.Fatal("prepared send did not reach the backend")
	}
	s.settingsUnit.state.configJSON = `{"optionalCLIs":{"lark":false,"genbu":true}}`
	s.observeOptionalCLIs()
	s.sendTagNotice(s.releaseUnit.state.tagHistory, group)
	if prepCtx.Err() == nil || sendCtx.Err() != nil || s.releaseNotices.pending != nil {
		t.Fatal("disabling Lark canceled an admitted send or allowed another send")
	}
	reply <- struct{}{}
	select {
	case received := <-finished:
		if !received {
			t.Fatal("admitted send lost its receipt after disabling Lark")
		}
	case <-time.After(time.Second):
		t.Fatal("the admitted send did not complete")
	}
}

func TestOptionalDragonKeepsRunningApprovalAndDropsOldUIResult(t *testing.T) {
	s := dragonQATestApp(t)
	window := &refreshTestWindow{updates: make(chan func(), 4)}
	started := make(chan context.Context, 1)
	completed := make(chan struct{})
	receipt := application.DragonQAState{TaskID: "a", Phase: application.DragonQAApproved, ApprovalID: "1234", ApprovalURL: dragonQATestApproval}
	s.backend = &dragonQATestBackend{
		status: func(_ context.Context, _ application.DragonQATask) (application.DragonQAState, error) {
			return receipt, nil
		},
		submit: func(ctx context.Context, _ application.DragonQASubmitRequest) (application.DragonQAState, error) {
			started <- ctx
			<-completed
			return receipt, nil
		},
	}
	task := s.taskUnit.currentTask()
	s.openDragonQA(task)
	view := s.taskUnit.state.dragonQA
	view.loaded, view.result.RequirementURL = true, dragonQATestRequirement
	view.setServices([]application.DragonQAService{{ID: "svc", Name: "Service"}})
	s.startDragonQAJob(window, view, dragonQASubmit, "")
	var runContext context.Context
	select {
	case runContext = <-started:
	case <-time.After(time.Second):
		close(completed)
		t.Fatal("approval did not start")
	}
	defer func() {
		select {
		case <-completed:
		default:
			close(completed)
		}
	}()
	setTestPlatformSwitch(s, "dragon", false)
	if s.dragonQAAvailable(task) || s.taskUnit.state.dragonQA != nil || view.open || runContext.Err() != nil || s.taskUnit.state.dragonQAJobs[view.scope] == nil {
		t.Fatal("disable hid the wrong state or canceled the running approval")
	}
	setTestPlatformSwitch(s, "dragon", true)
	s.openDragonQA(task)
	newView := s.taskUnit.state.dragonQA
	close(completed)
	apply := func() {
		t.Helper()
		select {
		case pending := <-window.updates:
			pending()
		case <-time.After(time.Second):
			t.Fatal("approval did not queue a UI update")
		}
	}
	apply()
	if newView == view || newView.loaded || s.taskUnit.state.dragonQAJobs[view.scope] != nil {
		t.Fatal("the previous account scope replaced the reopened form")
	}
	s.startDragonQAJob(window, newView, dragonQALoad, "")
	apply()
	if !newView.loaded || newView.result.ApprovalURL != receipt.ApprovalURL {
		t.Fatal("reopened form did not read the durable approval record")
	}
}
