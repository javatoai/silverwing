package desktop

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/production"
)

func TestProductionOldReleaseCountsOnlyConfirmedCreation(t *testing.T) {
	now := time.Date(2026, time.November, 21, 17, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		createdAt string
		confirmed bool
		ended     bool
		old       bool
	}{
		{name: "exact boundary", createdAt: now.Add(-12 * time.Hour).Format(time.RFC3339Nano), confirmed: true},
		{name: "past boundary", createdAt: now.Add(-12*time.Hour - time.Nanosecond).Format(time.RFC3339Nano), confirmed: true, old: true},
		{name: "preparing", createdAt: now.Add(-13 * time.Hour).Format(time.RFC3339Nano)},
		{name: "missing creation time", confirmed: true},
		{name: "malformed time", createdAt: "invalid", confirmed: true},
		{name: "future time", createdAt: now.Add(time.Hour).Format(time.RFC3339Nano), confirmed: true},
		{name: "ended", createdAt: now.Add(-13 * time.Hour).Format(time.RFC3339Nano), confirmed: true, ended: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := production.Batch{CreatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), ReleaseCreatedAt: tc.createdAt, Ended: tc.ended, ReleaseBranch: "release/2.3.44"}
			if tc.confirmed {
				b.InitialSHA = "confirmed"
			}
			if got := productionReleaseIsOld(b, now); got != tc.old {
				t.Fatalf("productionReleaseIsOld=%t, want %t", got, tc.old)
			}
		})
	}
}

func oldProductionReleaseFixture(t *testing.T) (*app, *productionView, production.Batch, *productionTestBackend, *refreshTestWindow) {
	t.Helper()
	s, v, b, backend, window := productionRequestFixture(t)
	b.ReleaseCreatedAt = time.Now().UTC().Add(-13 * time.Hour).Format(time.RFC3339Nano)
	s.releaseUnit.state.production.batches[0] = b
	s.win = &mygo.Window{}
	return s, v, b, backend, window
}

func TestProductionOldReleasePromptUsesContinueAsDefault(t *testing.T) {
	for name, key := range map[string]ui.Key{"enter": ui.KeyEnter, "escape": ui.KeyEscape} {
		t.Run(name, func(t *testing.T) {
			s, v, b, backend, _ := oldProductionReleaseFixture(t)
			if !s.maybePromptOldProductionRelease(v, b) {
				t.Fatal("old release did not open a reminder")
			}
			tester := ui.NewTester(func(c *ui.Context) { s.renderConfirmationDialog(c, setTheme(c, false)) }, 960, 630)
			if !tester.Focused("继续发布") || !tester.HasText(b.Target.ServiceName) || !tester.HasText(b.ReleaseBranch) || !tester.HasText(productionHistoryCreated(b)) {
				t.Fatalf("reminder missing context or safe focus: %v", tester.Texts())
			}
			tester.Key(0, key)
			if s.confirmationDialog != nil || len(backend.calls) != 0 || v.BatchID != b.ID {
				t.Fatal("continuing changed the active release or sent a request")
			}
			if s.maybePromptOldProductionRelease(v, b) {
				t.Fatal("same release was prompted twice")
			}
			s.snapshot.Home = t.TempDir()
			if !s.maybePromptOldProductionRelease(v, b) {
				t.Fatal("a reminder in another data directory was incorrectly suppressed")
			}
			s.cancelConfirmationDialog()
		})
	}
}

func TestProductionOldReleasePromptEndsOnlyCurrentBatch(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			s, v, b, backend, window := oldProductionReleaseFixture(t)
			if !s.maybePromptOldProductionRelease(v, b) {
				t.Fatal("old release did not open a reminder")
			}
			if stale {
				s.releaseUnit.state.production.batches[0].ReleaseCreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
			s.win = nil
			s.finishConfirmationDialog(s.confirmationDialog, 0)
			if stale {
				if len(backend.calls) != 0 || v.Busy {
					t.Fatal("stale reminder changed the release")
				}
				return
			}
			if call := productionTestCall(t, backend); call.Kind != "production.end" || call.Payload["batchId"] != b.ID || call.Payload["confirmed"] != true {
				t.Fatalf("unexpected end request: %+v", call)
			}
			backend.replies <- productionTestAnswer{err: errors.New("fixture failed to end the release")}
			productionTestUpdate(t, window)
			if v.BatchID != b.ID || v.Busy || len(backend.calls) != 0 || v.Error == "" {
				t.Fatalf("failed End did not retain the batch: %+v", v)
			}
		})
	}
}
