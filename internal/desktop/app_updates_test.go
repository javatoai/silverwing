package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

type updateTestReply struct {
	result domain.UpdateCheckResult
	err    error
}

type updateTestCall struct {
	ctx   context.Context
	reply chan updateTestReply
}

type updateTestBackend struct {
	calls chan updateTestCall
}

func (b *updateTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}

func (b *updateTestBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	if action.Kind != "update.check" || action.TaskID != "" || action.Path != "" {
		return application.ActionResult{}, errors.New("unexpected update action")
	}
	call := updateTestCall{ctx: ctx, reply: make(chan updateTestReply, 1)}
	b.calls <- call
	// Deliberately allow a cancelled request to return a late response.
	select {
	case response := <-call.reply:
		return application.ActionResult{Data: response.result}, response.err
	case <-time.After(2 * time.Second):
		return application.ActionResult{}, errors.New("test response not supplied")
	}
}

func newUpdateTestApp(t *testing.T, now time.Time) (*app, *updateTestBackend, chan func()) {
	t.Helper()
	backend := &updateTestBackend{calls: make(chan updateTestCall, 8)}
	posted := make(chan func(), 16)
	s := &app{backend: backend, desktopPreferences: &desktopPreferencesState{enabled: true}}
	s.snapshot.Home = t.TempDir()
	s.restoreDesktopPreferences(s.snapshot.Home)
	state := s.ensureAppUpdateState()
	state.post = func(callback func()) { posted <- callback }
	state.now = func() time.Time { return now }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.closeAppUpdates(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, backend, posted
}

func takeUpdateCall(t *testing.T, backend *updateTestBackend) updateTestCall {
	t.Helper()
	select {
	case call := <-backend.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("update check was not dispatched")
		return updateTestCall{}
	}
}

func applyUpdatePost(t *testing.T, posted chan func()) {
	t.Helper()
	select {
	case callback := <-posted:
		callback()
	case <-time.After(time.Second):
		t.Fatal("update check was not posted to the UI thread")
	}
}

func newerUpdateResult() domain.UpdateCheckResult {
	version := "99.0.0"
	tag := productprofile.ReleaseTagPrefix + version
	base := productprofile.RepositoryURL + "/releases/"
	return domain.UpdateCheckResult{Repository: productprofile.ReleaseRepository, TagPrefix: productprofile.ReleaseTagPrefix,
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		CurrentVersion: domain.CurrentProductVersion, ReleasesURL: productprofile.ReleasesURL(), CheckedAt: "2026-11-03T02:00:00Z",
		Release: &domain.UpdateRelease{Version: version, Tag: tag, URL: base + "tag/" + tag,
			Package: &domain.UpdateAsset{Name: "example.exe", URL: base + "download/" + tag + "/example.exe", Size: 100}}}
}

func TestAppUpdateDailyTimeAndCatchup(t *testing.T) {
	zone := time.FixedZone("local", 8*60*60)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, zone)
	for _, test := range []struct {
		name  string
		now   time.Time
		clock string
		last  time.Time
		want  time.Time
	}{
		{"before", now.Add(-time.Minute), "10:00", time.Time{}, now},
		{"at", now, "10:00", time.Time{}, now},
		{"missed", now.Add(3 * time.Hour), "10:00", time.Time{}, now},
		{"manual-before", now, "10:00", now.Add(-time.Hour), now},
		{"once", now, "10:00", now, now.AddDate(0, 0, 1)},
		{"yesterday", now, "10:00", now.AddDate(0, 0, -1), now},
		{"custom", now, "12:30", time.Time{}, now.Add(150 * time.Minute)},
		{"invalid", now, "25:99", time.Time{}, now},
	} {
		if got := nextAppUpdateCheck(test.now, test.clock, test.last); !got.Equal(test.want) {
			t.Fatalf("%s: next=%v want=%v", test.name, got, test.want)
		}
	}
	// Calendar days, rather than 24 hours, retain wall time across DST changes.
	if zone, err := time.LoadLocation("America/New_York"); err == nil {
		now := time.Date(2026, 3, 7, 10, 0, 0, 0, zone)
		next := nextAppUpdateCheck(now, "10:00", now)
		if next.Hour() != 10 || next.Sub(now) != 23*time.Hour {
			t.Fatalf("DST shifted the daily check: %v", next)
		}
	}
}

func TestAppUpdateAutomaticOnceRestartAndFailureKeepsBadge(t *testing.T) {
	now := time.Date(2026, 11, 3, 11, 0, 0, 0, time.Local)
	s, backend, posted := newUpdateTestApp(t, now)
	s.observeAppUpdates()
	call := takeUpdateCall(t, backend)
	if s.hasAppUpdate() || !s.appUpdates.checking {
		t.Fatal("pending check already claims an update")
	}
	s.observeAppUpdates()
	if len(backend.calls) != 0 {
		t.Fatal("in-flight automatic check was duplicated")
	}
	call.reply <- updateTestReply{result: newerUpdateResult()}
	applyUpdatePost(t, posted)
	if !s.hasAppUpdate() || s.appUpdates.checking || s.appUpdates.failure != "" {
		t.Fatalf("notification not applied: %+v", s.appUpdates)
	}
	s.observeAppUpdates()
	if len(backend.calls) != 0 {
		t.Fatal("successful automatic check was repeated")
	}
	restarted := &app{backend: backend, desktopPreferences: &desktopPreferencesState{enabled: true}}
	restarted.snapshot.Home = s.snapshot.Home
	restarted.restoreDesktopPreferences(restarted.snapshot.Home)
	restarted.ensureAppUpdateState().post = func(callback func()) { posted <- callback }
	restarted.appUpdates.now = func() time.Time { return now }
	defer restarted.stopAppUpdates()
	if !restarted.hasAppUpdate() {
		t.Fatal("restart lost the pending update")
	}
	restarted.observeAppUpdates()
	if len(backend.calls) != 0 {
		t.Fatal("restart repeated today's scheduled check")
	}
	s.beginAppUpdateCheck(true)
	call = takeUpdateCall(t, backend)
	call.reply <- updateTestReply{err: errors.New("offline")}
	applyUpdatePost(t, posted)
	if !s.hasAppUpdate() || !strings.Contains(s.appUpdates.failure, "offline") {
		t.Fatal("failed manual check discarded an available update")
	}
}

func TestAppUpdateOffManualCancellationAndLateReceipt(t *testing.T) {
	now := time.Date(2026, 11, 3, 11, 0, 0, 0, time.Local)
	s, backend, posted := newUpdateTestApp(t, now)
	s.observeAppUpdates()
	call := takeUpdateCall(t, backend)
	s.setAppUpdateSchedule(false, "10:00")
	if !s.appUpdatePreferences().Disabled || call.ctx.Err() == nil {
		t.Fatal("disabling automatic checks did not persist or cancel")
	}
	call.reply <- updateTestReply{result: newerUpdateResult()}
	applyUpdatePost(t, posted)
	if s.hasAppUpdate() {
		t.Fatal("cancelled automatic check applied a late receipt")
	}
	s.observeAppUpdates()
	if len(backend.calls) != 0 {
		t.Fatal("disabled automatic check ran")
	}
	s.beginAppUpdateCheck(true)
	call = takeUpdateCall(t, backend)
	call.reply <- updateTestReply{result: newerUpdateResult()}
	applyUpdatePost(t, posted)
	if !s.hasAppUpdate() || !s.appUpdatePreferences().Disabled {
		t.Fatal("manual check must remain available without enabling auto checks")
	}
}

func TestAppUpdateHomeSwitchCloseAndMalformedReceipt(t *testing.T) {
	now := time.Date(2026, 11, 3, 11, 0, 0, 0, time.Local)
	s, backend, posted := newUpdateTestApp(t, now)
	s.observeAppUpdates()
	old := takeUpdateCall(t, backend)
	s.snapshot.Home = t.TempDir()
	s.restoreDesktopPreferences(s.snapshot.Home)
	s.ensureAppUpdateState().now = func() time.Time { return now }
	if old.ctx.Err() == nil {
		t.Fatal("home switch did not cancel the old request")
	}
	old.reply <- updateTestReply{result: newerUpdateResult()}
	applyUpdatePost(t, posted)
	if s.hasAppUpdate() {
		t.Fatal("old home receipt leaked into the new home")
	}
	s.beginAppUpdateCheck(true)
	call := takeUpdateCall(t, backend)
	result := newerUpdateResult()
	result.Repository = "other/repo"
	call.reply <- updateTestReply{result: result}
	applyUpdatePost(t, posted)
	if s.hasAppUpdate() || s.appUpdates.failure == "" {
		t.Fatal("foreign receipt accepted")
	}
	s.beginAppUpdateCheck(true)
	call = takeUpdateCall(t, backend)
	s.stopAppUpdates()
	call.reply <- updateTestReply{result: newerUpdateResult()}
	applyUpdatePost(t, posted)
	if s.hasAppUpdate() || call.ctx.Err() == nil {
		t.Fatal("shutdown accepted an update receipt")
	}
}

func TestAppUpdatePreferencesDefaultsPersistAndPreserveOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), desktopPreferencesFile)
	if err := os.WriteFile(path, []byte(`{"width":1234,"height":800,"maximized":false,"settingsSection":"system","hiddenTaskTabs":["TERMINAL"],"unknown":{"keep":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewDesktopPreferencesStore(path)
	value, err := store.Load()
	if err != nil || value.Updates.Disabled || value.Updates.CheckTime != "10:00" {
		t.Fatalf("legacy preferences should enable daily 10:00 checks: %+v %v", value, err)
	}
	_, err = store.Update(func(value *DesktopPreferences) {
		value.Updates.Disabled, value.Updates.CheckTime = true, "12:30"
		result := newerUpdateResult()
		value.Updates.Result = &result
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(value *DesktopPreferences) { value.Width = 1400 })
	if err != nil {
		t.Fatal(err)
	}
	value, err = NewDesktopPreferencesStore(path).Load()
	content, _ := os.ReadFile(path)
	if err != nil || !value.Updates.Disabled || value.Updates.CheckTime != "12:30" || value.Updates.Result == nil || value.Width != 1400 || !strings.Contains(string(content), `"unknown"`) {
		t.Fatalf("partial preference update lost settings: %+v %s %v", value, content, err)
	}
}

func TestAppUpdateScheduleValidationAndSaveFailure(t *testing.T) {
	s, backend, _ := newUpdateTestApp(t, time.Now())
	s.setAppUpdateSchedule(false, "25:00")
	if s.appUpdatePreferences().Disabled || !strings.Contains(s.appUpdates.failure, "HH:mm") {
		t.Fatal("invalid schedule changed preferences")
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.desktopPreferences.store = NewDesktopPreferencesStore(filepath.Join(file, desktopPreferencesFile))
	s.setAppUpdateSchedule(false, "10:00")
	if s.appUpdatePreferences().Disabled || s.appUpdates.failure == "" || len(backend.calls) != 0 {
		t.Fatal("failed save changed the enabled setting or launched a check")
	}
}

func TestAppUpdateHeadlessRenderingDoesNotStartNetworkChecks(t *testing.T) {
	s, backend, posted := newUpdateTestApp(t, time.Date(2026, 11, 3, 11, 0, 0, 0, time.Local))
	s.appUpdates.post = nil
	// Another feature's test dispatcher is not an update scheduler opt-in.
	s.taskUnit.state.taskContextUpdate = func(callback func()) { posted <- callback }
	s.observeAppUpdates()
	if s.appUpdates.checking || s.appUpdates.timer != nil || len(backend.calls) != 0 {
		t.Fatal("headless rendering implicitly started update I/O")
	}
}

func TestAppUpdatePlatformCacheIsolationAndUnknownReceipt(t *testing.T) {
	s := &app{desktopPreferences: &desktopPreferencesState{value: DefaultDesktopPreferences()}}
	result := newerUpdateResult()
	result.Platform = "other/architecture"
	s.desktopPreferences.value.Updates.Result = &result
	if s.hasAppUpdate() || s.ensureAppUpdateState().result.Release != nil {
		t.Fatal("a platform copied from another computer offered the wrong installer")
	}
	result.Platform = runtime.GOOS + "/" + runtime.GOARCH
	result.CheckedAt = "not-a-time"
	if s.hasAppUpdate() {
		t.Fatal("invalid cached check was accepted")
	}
}

func TestAppUpdatePresentationDownloadAndNotification(t *testing.T) {
	for _, width := range []int{960, 360} {
		for _, dark := range []bool{false, true} {
			s := &app{desktopPreferences: &desktopPreferencesState{value: DefaultDesktopPreferences()}}
			result := newerUpdateResult()
			s.desktopPreferences.value.Updates.Result = &result
			tester := settingsSystemPageTester(s, width, dark, func(c *ui.Context, p palette) { s.settingsUpdates(c, p) })
			if !tester.HasText("发现新版 99.0.0") || !tester.HasText("有新版本可更新") || !tester.HasText("下载新版") {
				t.Fatalf("new release notification missing: %v", tester.Texts())
			}
			assertSettingsSystemCardBounds(t, tester, width)
			if err := tester.Click("下载新版"); err != nil {
				t.Fatal(err)
			}
			opened := tester.OpenedURLs()
			if len(opened) != 1 || opened[0] != result.Release.Package.URL || !s.hasAppUpdate() {
				t.Fatalf("download must open the matching package and retain the update mark: %v", opened)
			}
			tester.FailOpenURL(errors.New("no browser"))
			if err := tester.Click("下载新版"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(s.appUpdates.failure, "no browser") || !s.hasAppUpdate() {
				t.Fatal("failed download link must show a retryable error and retain the update")
			}
		}
	}
}
