package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/integrations"
	"silverwing-go/internal/noticeprep"
	"silverwing-go/internal/releasenotice"
)

func (f *tagNoticeDispatchFixture) refresh(t *testing.T) {
	t.Helper()
	_, err := f.app.Dispatch(context.Background(), Action{Kind: "requirements.metadata", Payload: map[string]any{"url": f.taskURL(), "force": true, "fullRefresh": true}})
	if err != nil {
		t.Fatal(err)
	}
	f.waitWarm(t)
}

func (f *tagNoticeDispatchFixture) taskURL() string {
	return "https://project.feishu.cn/notice/userstory/detail/101"
}
func (f *tagNoticeDispatchFixture) waitWarm(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := f.app.Integrations.WaitTagNoticePreparations(ctx); err != nil {
		t.Fatal(err)
	}
}
func (f *tagNoticeDispatchFixture) disk(t *testing.T) noticeprep.Snapshot {
	t.Helper()
	snapshot, _, hit, err := (noticeprep.Store{Directory: filepath.Join(f.app.Paths.Cache, "tag-notice-preparation")}).Load(context.Background(), f.taskURL())
	if err != nil || !hit {
		t.Fatal("no valid disk snapshot", hit, err)
	}
	return snapshot
}
func (f *tagNoticeDispatchFixture) newIntegrations(t *testing.T) {
	t.Helper()
	if err := f.app.Integrations.CloseRequirementDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.app.Integrations = integrations.NewService(f.app.Paths, f.app.Config, f.app.Runner)
}

func TestTagNoticeRefreshRoutesWarmAsynchronouslyAndNewInstanceReadsDisk(t *testing.T) {
	for _, route := range []string{"metadata", "metadata-full-only", "list-dispatch", "list-typed", "list-progress"} {
		t.Run(route, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			f.arm(t, "contact-TesterA")
			finished := make(chan error, 1)
			go func() {
				var err error
				switch route {
				case "metadata", "metadata-full-only":
					_, err = f.app.Dispatch(context.Background(), Action{Kind: "requirements.metadata", Payload: map[string]any{"url": f.taskURL(), "force": route == "metadata", "fullRefresh": true}})
				case "list-dispatch":
					_, err = f.app.Dispatch(context.Background(), Action{Kind: "requirements.refresh", Payload: map[string]any{"sprintKey": "notice-project:900"}})
				default:
					var progress func(RequirementListProgress)
					if route == "list-progress" {
						progress = func(RequirementListProgress) {}
					}
					_, err = f.app.ReadRequirementList(context.Background(), RequirementListRequest{Refresh: true, SprintKey: "notice-project:900"}, progress)
				}
				finished <- err
			}()
			if err := waitNoticeFixtureFile(f.state.Gate + ".started"); err != nil {
				t.Fatal(err)
			}
			// The contact process remains blocked until below. The route must have
			// returned independently; this is a barrier assertion, not a speed limit.
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("requirement refresh waited for notification directory lookup")
			}
			_ = os.WriteFile(f.state.Gate+".release", nil, 0600)
			f.waitWarm(t)
			beforeMetadata, beforeContacts := f.count(t, "metadata"), f.count(t, "contact-TesterA")
			if snapshot := f.disk(t); snapshot.Recipients[0].OpenID != "ou_a" || !snapshot.RosterComplete {
				t.Fatal(snapshot)
			}
			f.newIntegrations(t)
			prepared := f.prepare(t)
			if prepared.Recipients[0].OpenID != "ou_a" || f.count(t, "metadata") != beforeMetadata || f.count(t, "contact-TesterA") != beforeContacts {
				t.Fatal("new instance ignored prefetch", prepared)
			}
			if _, err := f.app.Dispatch(context.Background(), f.sendAction(t, prepared)); err != nil {
				t.Fatal(err)
			}
			if f.count(t, "metadata") != beforeMetadata || f.count(t, "contact-TesterA") != beforeContacts || f.count(t, "verify") != 2 {
				t.Fatal("send did not share permanent preparation")
			}
		})
	}
}

func TestTagNoticePermanentCacheChangesOnlyAfterManualRefresh(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	first := f.prepare(t)
	f.state.Testers, f.state.Title = []string{"TesterB"}, "Changed requirement"
	f.saveState(t)
	f.newIntegrations(t)
	still := f.prepare(t)
	if still.Title != first.Title || still.Recipients[0].OpenID != "ou_a" || f.count(t, "metadata") != 1 || f.count(t, "contact-TesterB") != 0 {
		t.Fatal("ordinary read refreshed permanent data", still)
	}
	f.refresh(t)
	updated := f.prepare(t)
	if updated.Title != "Changed requirement" || updated.Recipients[0].OpenID != "ou_b" || f.count(t, "metadata") != 2 || f.count(t, "contact-TesterB") != 1 {
		t.Fatal(updated)
	}
	if _, err := f.app.Dispatch(context.Background(), f.sendAction(t, first)); err == nil {
		t.Fatal("old prepared choice survived roster/title refresh")
	}
}

func TestTagNoticeCachedMetadataReentryDoesNotStartARefresh(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.prepare(t)
	path := filepath.Join(f.app.Paths.Cache, "tag-notice-preparation", releasenotice.Digest(f.taskURL())+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.app.Dispatch(context.Background(), Action{Kind: "requirements.metadata", Payload: map[string]any{"url": f.taskURL(), "force": false, "fullRefresh": true}})
	if err != nil {
		t.Fatal(err)
	}
	f.waitWarm(t)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || f.count(t, "metadata") != 1 || f.count(t, "contact-TesterA") != 1 {
		t.Fatal("ordinary cached read changed notification data or refresh state", err)
	}
}

func TestTagNoticeFailedIncompleteAndCancelledRefreshKeepLastGood(t *testing.T) {
	for _, mode := range []string{"failure", "incomplete", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			f.prepare(t)
			if mode == "cancel" {
				f.arm(t, "metadata")
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() {
					_, err := f.app.Dispatch(ctx, Action{Kind: "requirements.metadata", Payload: map[string]any{"url": f.taskURL(), "force": true}})
					done <- err
				}()
				if err := waitNoticeFixtureFile(f.state.Gate + ".started"); err != nil {
					t.Fatal(err)
				}
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				_ = os.WriteFile(f.state.Gate+".release", nil, 0600)
			} else {
				f.state.MetadataFailure = mode == "failure"
				f.state.IncompleteRoster = mode == "incomplete"
				f.saveState(t)
				f.refresh(t)
			}
			snapshot := f.disk(t)
			if snapshot.Title != "Notice fixture" || snapshot.Recipients[0].OpenID != "ou_a" || snapshot.RefreshState == "ready" {
				t.Fatal("failed refresh erased or falsely refreshed data", snapshot)
			}
			before := f.count(t, "metadata")
			prepared := f.prepare(t)
			if prepared.Recipients[0].OpenID != "ou_a" || prepared.PreparationStatus == "" || f.count(t, "metadata") != before {
				t.Fatal("did not expose retained data/failure", prepared)
			}
			if _, err := f.app.Dispatch(context.Background(), f.sendAction(t, prepared)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTagNoticeCacheMissCorruptionAndStorageFailureUseLivePath(t *testing.T) {
	for _, mode := range []string{"corrupt", "io-failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			dir := filepath.Join(f.app.Paths.Cache, "tag-notice-preparation")
			if mode == "corrupt" {
				f.prepare(t)
				if err := os.WriteFile(filepath.Join(dir, releasenotice.Digest(f.taskURL())+".json"), []byte("broken JSON"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(f.app.Paths.Cache, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dir, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			prepared := f.prepare(t)
			if prepared.Warning != "" || prepared.Recipients[0].OpenID != "ou_a" {
				t.Fatal("cache miss caused self fallback", prepared)
			}
			if _, err := f.app.Dispatch(context.Background(), f.sendAction(t, prepared)); err != nil {
				t.Fatal(err)
			}
			if f.count(t, "metadata") != 2 || f.count(t, "contact-TesterA") != 2 {
				t.Fatal("live recovery not exercised")
			}
		})
	}
}

func TestTagNoticeEmptyRosterAndUnresolvedRosterAreDurableFallbacks(t *testing.T) {
	for _, mode := range []string{"empty", "unresolved", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			switch mode {
			case "empty":
				f.state.Testers = []string{}
			case "unresolved":
				f.state.Contacts["TesterA"] = ""
			case "incomplete":
				f.state.IncompleteRoster = true
			}
			f.saveState(t)
			prepared := f.prepare(t)
			if prepared.Recipients[0].OpenID != "ou_self" || prepared.Warning == "" {
				t.Fatal(prepared)
			}
			f.newIntegrations(t)
			f.prepare(t)
			want := 1
			if mode == "incomplete" {
				want = 2
			}
			if f.count(t, "metadata") != want {
				t.Fatal("complete-empty and incomplete were conflated", mode, f.count(t, "metadata"))
			}
		})
	}
}

func TestTagNoticeListPrefetchKeepsTwoWorkersAndClosingPreservesDisk(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.prepare(t)
	f.state.ListSize, f.state.GateAll = 24, true
	f.arm(t, "contact-TesterA")
	result, err := f.app.ReadRequirementList(context.Background(), RequirementListRequest{Refresh: true, SprintKey: "notice-project:900"}, func(RequirementListProgress) {})
	if err != nil || len(result.Data.Items) != 24 {
		t.Fatal(result, err)
	}
	// One contact came from the initial prepare. Both workers must block and
	// a third full metadata read cannot start while they occupy the two slots.
	if err := waitNoticeFixtureFile(filepath.Join(f.state.Log, "contact-TesterA-3.started")); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "metadata") != 3 || f.count(t, "contact-TesterA") != 3 {
		t.Fatal("unbounded network workers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.app.Integrations.CloseRequirementDocuments(ctx); err != nil {
		t.Fatal(err)
	}
	if snapshot := f.disk(t); snapshot.Recipients[0].OpenID != "ou_a" || snapshot.RefreshState == "ready" {
		t.Fatal("close lost retained snapshot", snapshot)
	}
	before := f.count(t, "metadata")
	_ = os.WriteFile(f.state.Gate+".release", nil, 0600)
	f.app.Integrations = integrations.NewService(f.app.Paths, f.app.Config, f.app.Runner)
	if prepared := f.prepare(t); prepared.Recipients[0].OpenID != "ou_a" || f.count(t, "metadata") != before {
		t.Fatal(prepared)
	}
	files, _ := os.ReadDir(filepath.Join(f.app.Paths.Cache, "tag-notice-preparation"))
	if len(files) != f.state.ListSize+2 { // One record per input URL, epoch and lock.
		t.Fatal("list did not retain exactly its returned requirements", len(files))
	}
}

func TestTagNoticeListRefreshUpdatesCachedRequirementsBeyondSixteen(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.prepare(t)
	previous := f.disk(t)
	store := noticeprep.Store{Directory: filepath.Join(f.app.Paths.Cache, "tag-notice-preparation")}
	f.state.ListSize = 20
	for i := 0; i < f.state.ListSize; i++ {
		snapshot := previous
		snapshot.Scope.Requirement = fmt.Sprintf("https://project.feishu.cn/notice/userstory/detail/%d", 101+i)
		snapshot.Scope.ID = fmt.Sprint(101 + i)
		ticket, err := store.Begin(context.Background(), snapshot.Scope.Requirement, true)
		if err == nil {
			err = store.Commit(context.Background(), ticket, snapshot)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	f.state.Testers, f.state.Title = []string{"TesterB"}, "Changed list requirement"
	f.saveState(t)
	result, err := f.app.ReadRequirementList(context.Background(), RequirementListRequest{Refresh: true, SprintKey: "notice-project:900"}, nil)
	if err != nil || len(result.Data.Items) != f.state.ListSize {
		t.Fatal(result, err)
	}
	f.waitWarm(t)
	for _, item := range result.Data.Items {
		snapshot, _, hit, err := store.Load(context.Background(), item.URL)
		if err != nil || !hit || snapshot.Title != f.state.Title || len(snapshot.Recipients) != 1 || snapshot.Recipients[0].OpenID != "ou_b" {
			t.Fatal("manual list refresh left a returned requirement stale", item.URL, snapshot, hit, err)
		}
	}
	if got := f.count(t, "contact-TesterB"); got != f.state.ListSize {
		t.Fatal("workers did not drain every returned requirement", got, f.state.ListSize)
	}
}

func TestTagNoticeNewRefreshSupersedesOlderReadAndOutstandingClick(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	prepared := f.prepare(t)
	f.arm(t, "metadata")
	done := make(chan error, 1)
	go func() {
		_, err := f.app.Dispatch(context.Background(), Action{Kind: "requirements.metadata", Payload: map[string]any{"url": f.taskURL(), "force": true}})
		done <- err
	}()
	if err := waitNoticeFixtureFile(f.state.Gate + ".started"); err != nil {
		t.Fatal(err)
	}
	f.state.Testers = []string{"TesterB"}
	f.saveState(t)
	f.refresh(t)
	_ = os.WriteFile(f.state.Gate+".release", nil, 0600)
	if err := <-done; err == nil {
		t.Fatal("late old read accepted")
	}
	if snapshot := f.disk(t); snapshot.Recipients[0].OpenID != "ou_b" {
		t.Fatal(snapshot)
	}
	if _, err := f.app.Dispatch(context.Background(), f.sendAction(t, prepared)); err == nil {
		t.Fatal("stale prepared click reached message RPC")
	}
	if f.count(t, "send") != 0 {
		t.Fatal("refresh test sent a message")
	}
}

func TestTagNoticeChangedAccountContextNeverUsesPreviousPreparation(t *testing.T) {
	for _, mode := range []string{"meegle-account", "meegle-profile", "config-contacts"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			first := f.prepare(t)
			f.mutate(t, mode, func() {})
			if mode == "config-contacts" {
				cfg, _ := f.app.Config.Load()
				f.action.Payload["noticeConfigIdentity"] = ReleaseNoticeConfigIdentity(cfg)
			}
			second := f.prepare(t)
			if f.count(t, "metadata") != 2 {
				t.Fatal("context change inherited previous account data")
			}
			if strings.HasPrefix(mode, "meegle") && first.RequirementIdentity == second.RequirementIdentity {
				t.Fatal("Meegle identity missing from frozen snapshot")
			}
		})
	}
}

func TestTagNoticeUnknownRetryKeepsOriginalCardAfterFailedRefresh(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.state.Results["ou_a"] = "unknown"
	f.saveState(t)
	prepared := f.prepare(t)
	result, err := f.app.Dispatch(context.Background(), f.sendAction(t, prepared))
	if err == nil {
		t.Fatal("expected unknown")
	}
	old := result.Data.(releasenotice.Session).Receipts[0]
	f.state.MetadataFailure = true
	delete(f.state.Results, "ou_a")
	f.saveState(t)
	f.refresh(t)
	f.newIntegrations(t)
	prepared = f.prepare(t)
	if prepared.PreparationStatus == "" {
		t.Fatal("lost failed-refresh status")
	}
	result, err = f.app.Dispatch(context.Background(), f.sendAction(t, prepared))
	if err != nil {
		t.Fatal(err)
	}
	latest := result.Data.(releasenotice.Session).Receipts[0]
	if latest.Key != old.Key || latest.Card != old.Card || latest.State != "sent" || f.count(t, "metadata") != 2 {
		t.Fatal("failed refresh changed unknown retry", latest)
	}
}

func TestTagNoticePrefetchCannotCommitAfterAccountConfigOrLifecycleChanges(t *testing.T) {
	for _, mode := range []string{"account", "meegle-account", "profile", "config-contacts", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			f.prepare(t)
			f.state.Testers = []string{"TesterB"}
			f.arm(t, "contact-TesterB")
			_, err := f.app.Dispatch(context.Background(), Action{Kind: "requirements.metadata", Payload: map[string]any{"url": f.taskURL(), "force": true}})
			if err != nil {
				t.Fatal(err)
			}
			if err := waitNoticeFixtureFile(f.state.Gate + ".started"); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				if err := f.app.CancelRequirementDocuments(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				f.mutate(t, mode, func() {})
			}
			_ = os.WriteFile(f.state.Gate+".release", nil, 0600)
			f.waitWarm(t)
			if snapshot := f.disk(t); snapshot.Recipients[0].OpenID != "ou_a" || snapshot.RefreshState == "ready" {
				t.Fatal("stale prefetch replaced last good data", snapshot)
			}
		})
	}
}

func TestTagNoticeCredentialRotationRebindsSameAccountWithoutReloadingRoster(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.prepare(t)
	credential := filepath.Join(f.accountDir, ".meegle", "credentials-notice-fixture.enc")
	if err := os.WriteFile(credential, []byte("rotated fake encrypted credential for the same account"), 0600); err != nil {
		t.Fatal(err)
	}
	f.prepare(t)
	f.newIntegrations(t)
	f.prepare(t)
	if f.count(t, "metadata") != 1 || f.count(t, "contact-TesterA") != 1 || f.count(t, "meegleUser") != 2 {
		t.Fatal("token rotation reloaded roster or failed to rebind the local fence")
	}
}

func TestTagNoticeMissingSourceTitleDoesNotPersistTaskSpecificFallback(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.state.Title = ""
	f.saveState(t)
	first := f.prepare(t)
	f.newIntegrations(t)
	second := f.prepare(t)
	if first.Title == "" || first.Title != second.Title || f.count(t, "metadata") != 2 {
		t.Fatal("unconfirmed requirement title became a permanent task-specific title", first, second)
	}
}
