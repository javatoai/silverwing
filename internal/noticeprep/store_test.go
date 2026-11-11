package noticeprep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"silverwing-go/internal/releasenotice"
	"silverwing-go/internal/storage"
)

func sampleSnapshot() Snapshot {
	return Snapshot{Scope: Scope{Config: "cfg", Requirement: "requirement", MeegleIdentity: "account", MeegleContext: "encrypted-local-fence", Project: "p", Kind: "userstory", ID: "1", Profile: "profile", AppID: "app", Sender: "ou_self"},
		FetchedAt: 1, RosterComplete: true, Title: "Requirement", Testers: []string{"Tester"}, Recipients: []releasenotice.Recipient{{Name: "Tester", OpenID: "ou_tester"}}}
}

func saveSnapshot(t *testing.T, store Store, snapshot Snapshot) Ticket {
	t.Helper()
	ticket, err := store.Begin(context.Background(), snapshot.Scope.Requirement, true)
	if err == nil {
		err = store.Commit(context.Background(), ticket, snapshot)
	}
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func TestPermanentSnapshotSurvivesClockAdvanceAndNewStore(t *testing.T) {
	ctx := context.Background()
	store := Store{Directory: t.TempDir(), now: func() time.Time { return time.Unix(1, 0) }}
	want := sampleSnapshot()
	saveSnapshot(t, store, want)
	store = Store{Directory: store.Directory, now: func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) }}
	got, ticket, hit, err := store.Read(ctx, want.Scope.Requirement, want.Scope)
	if err != nil || !hit || got.Title != want.Title || got.FetchedAt != 1 {
		t.Fatal(got, hit, err)
	}
	if err := store.Check(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Scope){
		func(s *Scope) { s.Config = "other" }, func(s *Scope) { s.MeegleIdentity = "other" }, func(s *Scope) { s.MeegleContext = "other" },
		func(s *Scope) { s.Project = "other" }, func(s *Scope) { s.Requirement = "other" }, func(s *Scope) { s.Profile = "other" },
		func(s *Scope) { s.AppID = "other" }, func(s *Scope) { s.Sender = "ou_other" },
	} {
		scope := want.Scope
		mutate(&scope)
		if _, _, hit, err := store.Read(ctx, want.Scope.Requirement, scope); err != nil || hit {
			t.Fatal("cross-context cache hit", scope, err)
		}
	}
}

func TestRefreshFailureCancellationAndOlderWriterPreserveGoodSnapshot(t *testing.T) {
	ctx, store := context.Background(), Store{Directory: t.TempDir()}
	old := sampleSnapshot()
	saveSnapshot(t, store, old)
	_, prepared, _, _ := store.Load(ctx, old.Scope.Requirement)
	first, _ := store.Begin(ctx, old.Scope.Requirement, true)
	if err := store.Check(ctx, prepared); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	second, _ := store.Begin(ctx, old.Scope.Requirement, true)
	newer := old
	newer.Title = "Newer"
	if err := store.Commit(ctx, first, newer); !errors.Is(err, ErrSuperseded) {
		t.Fatal("older writer won", err)
	}
	for _, state := range []string{"failed", "cancelled"} {
		if err := store.Fail(ctx, second, state); err != nil {
			t.Fatal(err)
		}
		got, proof, hit, err := store.Load(ctx, old.Scope.Requirement)
		if err != nil || !hit || got.Title != old.Title || got.RefreshState != state {
			t.Fatal(got, hit, err)
		}
		if err := store.Check(ctx, proof); err != nil {
			t.Fatal(err)
		}
	}
	latest, _ := store.Begin(ctx, old.Scope.Requirement, true)
	if err := store.Commit(ctx, latest, newer); err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(ctx, second); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	got, _, hit, _ := store.Load(ctx, old.Scope.Requirement)
	if !hit || got.Title != "Newer" || got.RefreshState != "ready" {
		t.Fatal(got)
	}
}

func TestRefreshFenceAcrossStoreInstancesAndRetirement(t *testing.T) {
	ctx := context.Background()
	one := Store{Directory: t.TempDir()}
	two := Store{Directory: one.Directory}
	snapshot := sampleSnapshot()
	saveSnapshot(t, one, snapshot)
	loading, _ := one.Begin(ctx, snapshot.Scope.Requirement, true)
	_, proof, hit, _ := two.Load(ctx, snapshot.Scope.Requirement)
	if !hit || !errors.Is(two.Check(ctx, proof), ErrRefreshing) {
		t.Fatal("loading snapshot silently authorized")
	}
	if err := two.InvalidateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := one.Commit(ctx, loading, snapshot); !errors.Is(err, ErrSuperseded) {
		t.Fatal(err)
	}
	got, proof, hit, err := two.Load(ctx, snapshot.Scope.Requirement)
	if err != nil || !hit || got.RefreshState != "cancelled" {
		t.Fatal(got, hit, err)
	}
	if err := two.Check(ctx, proof); err != nil {
		t.Fatal(err)
	}
}

func TestMissingCorruptIncompleteAndConfirmedEmpty(t *testing.T) {
	ctx, store := context.Background(), Store{Directory: t.TempDir()}
	snapshot := sampleSnapshot()
	if _, _, hit, err := store.Load(ctx, snapshot.Scope.Requirement); err != nil || hit {
		t.Fatal(hit, err)
	}
	for _, modify := range []func(*Snapshot){func(s *Snapshot) { s.RosterComplete = false }, func(s *Snapshot) { s.Testers = nil }, func(s *Snapshot) { s.Recipients = append(s.Recipients, s.Recipients[0]) }} {
		invalid := snapshot
		modify(&invalid)
		ticket, _ := store.Begin(ctx, snapshot.Scope.Requirement, true)
		if err := store.Commit(ctx, ticket, invalid); err == nil {
			t.Fatal("accepted incomplete roster", invalid)
		}
	}
	empty := snapshot
	empty.Testers = []string{}
	empty.Recipients = nil
	empty.FallbackReason = "需求未配置测试负责人"
	saveSnapshot(t, store, empty)
	if got, _, hit, err := store.Load(ctx, snapshot.Scope.Requirement); err != nil || !hit || got.Testers == nil || got.FallbackReason == "" {
		t.Fatal(got, hit, err)
	}
	if err := os.WriteFile(store.path(snapshot.Scope.Requirement), []byte(`{"truncated":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, hit, err := store.Load(ctx, snapshot.Scope.Requirement); err == nil || hit {
		t.Fatal(hit, err)
	}
	saveSnapshot(t, store, snapshot)
	if _, _, hit, err := store.Load(ctx, snapshot.Scope.Requirement); err != nil || !hit {
		t.Fatal(hit, err)
	}
}

func TestStorageFailureAndLockCancellationAreBounded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Store{Directory: blocked}).Begin(ctx, "requirement", true); err == nil {
		t.Fatal("expected IO error")
	}
	store := Store{Directory: dir}
	unlock, err := storage.AcquireLock(filepath.Join(dir, "preparation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Begin(cancelled, "requirement", true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
