package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type platformLoginCall struct {
	action application.Action
	ctx    context.Context
	result chan error
}

func newPlatformLoginTestApp(t *testing.T) (*app, <-chan platformLoginCall) {
	t.Helper()
	calls := make(chan platformLoginCall, 16)
	stop := make(chan struct{})
	backend := &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		call := platformLoginCall{action: action, ctx: ctx, result: make(chan error, 1)}
		calls <- call
		// Allow an obsolete backend result to arrive after cancellation.
		select {
		case err := <-call.result:
			return application.ActionResult{}, err
		case <-stop:
			return application.ActionResult{}, nil
		}
	}}
	s := newApp(backend, Options{Page: "settings"})
	s.win, s.loading = &mygo.Window{}, false
	navigatePlatformLoginTestPage(t, s, "basic")
	t.Cleanup(func() {
		jobs := &s.settingsUnit.state.platformJobs
		jobs.Close()
		close(stop)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := jobs.Wait(ctx); err != nil {
			t.Errorf("platform login workers did not stop: %v", err)
		}
	})
	return s, calls
}

func navigatePlatformLoginTestPage(t *testing.T, s *app, page string) {
	t.Helper()
	if !s.navigateSettingsPage(page, "") {
		t.Fatalf("could not navigate to settings page %q", page)
	}
	// The headless tester cannot apply the native window's local-read callback.
	key := s.localPageReadKey()
	s.settingsUnit.state.pageLocalRead = &pageLocalReadState{entry: key, attempted: key, ready: key}
}

func awaitPlatformLoginCalls(t *testing.T, calls <-chan platformLoginCall, tools ...string) map[string]platformLoginCall {
	t.Helper()
	wanted := make(map[string]bool, len(tools))
	for _, tool := range tools {
		wanted[tool] = true
	}
	started := make(map[string]platformLoginCall, len(tools))
	timeout := time.After(time.Second)
	for range tools {
		select {
		case call := <-calls:
			action := call.action
			if action.Kind != "platform.login.test" || !wanted[action.ServiceID] || len(action.Payload) != 0 {
				t.Fatalf("unexpected platform request: %+v", action)
			}
			started[action.ServiceID] = call
			delete(wanted, action.ServiceID)
		case <-timeout:
			t.Fatalf("missing login requests for %v", wanted)
		}
	}
	return started
}

func TestPlatformSettingsAutomaticallyTestsLoginsOnEachVisit(t *testing.T) {
	s, calls := newPlatformLoginTestApp(t)
	seedPlatformConnectionsForDesktopTest(s, true, true)
	state := s.ensurePlatformConnections()
	tester := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1150, 850)
	tester.Frame()
	if state.genbu.test != nil || state.dragon.test != nil {
		t.Fatal("opening basic settings started a platform login test")
	}
	navigatePlatformLoginTestPage(t, s, platformConnectionsPage)
	tester.Frame()
	first := awaitPlatformLoginCalls(t, calls, "genbu", "dragon")
	firstGenbu, firstDragon := state.genbu.test, state.dragon.test
	tester.Frame()
	if state.genbu.test != firstGenbu || state.dragon.test != firstDragon {
		t.Fatal("redrawing platform connections replaced the current tests")
	}
	navigatePlatformLoginTestPage(t, s, "basic")
	if first["genbu"].ctx.Err() != context.Canceled || first["dragon"].ctx.Err() != context.Canceled ||
		state.genbu.test != nil || state.dragon.test != nil {
		t.Fatal("leaving platform connections kept the previous tests active")
	}
	navigatePlatformLoginTestPage(t, s, platformConnectionsPage)
	tester.Frame()
	second := awaitPlatformLoginCalls(t, calls, "genbu", "dragon")
	secondGenbu, secondDragon := state.genbu.test, state.dragon.test
	first["genbu"].result <- nil
	first["dragon"].result <- nil
	awaitAsyncLifecycle(t, firstGenbu.Done())
	awaitAsyncLifecycle(t, firstDragon.Done())
	s.finishPlatformLogin(state, "genbu", firstGenbu, nil)
	s.finishPlatformLogin(state, "dragon", firstDragon, nil)
	if state.genbu.test != secondGenbu || state.dragon.test != secondDragon || state.genbu.testMessage != "" || state.dragon.testMessage != "" {
		t.Fatal("a retired visit's successful result replaced a current test")
	}
	s.page, s.loading = "tasks", true
	s.observePageLocalRead()
	if second["genbu"].ctx.Err() != context.Canceled || second["dragon"].ctx.Err() != context.Canceled {
		t.Fatal("leaving settings did not cancel the current login tests")
	}
}

func TestPlatformSettingsWaitsForSavedConnectionBeforeAutomaticTest(t *testing.T) {
	for _, blocker := range []string{"pending local config", "unloaded metadata", "metadata refresh", "save in progress", "missing account"} {
		t.Run(blocker, func(t *testing.T) {
			s, calls := newPlatformLoginTestApp(t)
			seedPlatformConnectionsForDesktopTest(s, true, false)
			state := s.ensurePlatformConnections()
			navigatePlatformLoginTestPage(t, s, platformConnectionsPage)
			pageRead := s.settingsUnit.state.pageLocalRead
			switch blocker {
			case "pending local config":
				pageRead.ready = ""
			case "unloaded metadata":
				state.loaded = false
			case "metadata refresh":
				lease := s.beginPlatformRequest(false)
				state.read = lease
				t.Cleanup(func() { lease.Complete(nil) })
			case "save in progress":
				lease := s.beginPlatformRequest(true)
				state.save = lease
				t.Cleanup(func() { lease.Complete(nil) })
			case "missing account":
				state.value.Configured = false
			}
			tester := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1150, 850)
			tester.Frame()
			if state.genbu.test != nil || state.dragon.test != nil {
				t.Fatal("automatically started login before the saved connection was ready")
			}
			if state.read != nil {
				state.read.Complete(nil)
			}
			if state.save != nil {
				state.save.Complete(nil)
			}
			state.read, state.save = nil, nil
			state.loaded, state.value.Configured = true, true
			pageRead.ready = s.localPageReadKey()
			tester.Frame()
			awaitPlatformLoginCalls(t, calls, "genbu")
			if state.dragon.test != nil {
				t.Fatal("automatically tested a disabled connection")
			}
		})
	}
}

func TestPlatformSettingsAutomaticallyRetestsChangedConnections(t *testing.T) {
	s, calls := newPlatformLoginTestApp(t)
	seedPlatformConnectionsForDesktopTest(s, true, true)
	state := s.ensurePlatformConnections()
	navigatePlatformLoginTestPage(t, s, platformConnectionsPage)
	tester := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1150, 850)
	tester.Frame()
	first := awaitPlatformLoginCalls(t, calls, "genbu", "dragon")
	firstDragon := state.dragon.test
	setTestPlatformSwitch(s, "genbu", false)
	tester.Frame()
	if state.genbu.test != nil || first["genbu"].ctx.Err() != context.Canceled || first["dragon"].ctx.Err() != nil {
		t.Fatal("disabling Genbu did not retire only its login test")
	}
	setTestPlatformSwitch(s, "genbu", true)
	tester.Frame()
	newGenbu := awaitPlatformLoginCalls(t, calls, "genbu")["genbu"]
	if state.dragon.test != firstDragon {
		t.Fatal("re-enabling Genbu changed Dragon's login test")
	}
	next := state.value
	next.Revision++
	s.publishPlatformConnections(state, next)
	tester.Frame()
	awaitPlatformLoginCalls(t, calls, "genbu", "dragon")
	if newGenbu.ctx.Err() != context.Canceled || first["dragon"].ctx.Err() != context.Canceled {
		t.Fatal("saving changed credentials kept login tests for the previous credentials")
	}
}

func TestPlatformSettingsRetriesFailedLoginOnReentry(t *testing.T) {
	s, calls := newPlatformLoginTestApp(t)
	seedPlatformConnectionsForDesktopTest(s, true, false)
	state := s.ensurePlatformConnections()
	navigatePlatformLoginTestPage(t, s, platformConnectionsPage)
	tester := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1150, 850)
	tester.Frame()
	first := awaitPlatformLoginCalls(t, calls, "genbu")["genbu"]
	lease := state.genbu.test
	failure := errors.New("fixture login failure")
	first.result <- failure
	awaitAsyncLifecycle(t, lease.Done())
	// The headless UI tester does not drain MyGO's native window update queue.
	s.finishPlatformLogin(state, "genbu", lease, failure)
	tester.Frame()
	if state.genbu.test != nil {
		t.Fatal("the failure status was replaced by another automatic login test")
	}
	if _, ok := tester.Find("登录失败"); !ok || !tester.HasText("请核对账号或网络后重新进入此页。") {
		t.Fatal("the failed login did not show its status and reentry guidance")
	}
	navigatePlatformLoginTestPage(t, s, "basic")
	tester.Frame()
	navigatePlatformLoginTestPage(t, s, platformConnectionsPage)
	tester.Frame()
	retry := awaitPlatformLoginCalls(t, calls, "genbu")["genbu"]
	lease = state.genbu.test
	retry.result <- nil
	awaitAsyncLifecycle(t, lease.Done())
	s.finishPlatformLogin(state, "genbu", lease, nil)
	tester.Frame()
	if state.genbu.test != nil || !tester.HasText("登录成功") {
		t.Fatal("the automatic login on reentry did not show success for the rest of the visit")
	}
}
