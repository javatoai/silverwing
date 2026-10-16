package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

func tagJobProbeFixture(t *testing.T) (*App, TagJobScope, map[string]domain.TagOperation) {
	t.Helper()
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope := scopes["synthetic"]
	manifest := tasks[scope.TaskID].manifest
	operations := map[string]domain.TagOperation{}
	for _, key := range []string{"fast", "slow"} {
		origin := "https://example.invalid/" + key + ".git"
		manifest.Services = append(manifest.Services, domain.ServiceWorkspace{RepositoryID: key, ServiceName: key, RepositoryPath: filepath.Join(scope.TaskPath, key), WorktreePath: filepath.Join(scope.TaskPath, key), Branch: "feature/synthetic", DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured, Warnings: []string{}, GroupServiceID: key, ModuleID: "module", OriginURL: &origin, TagMode: domain.TagBuildCurrentBranch, PushRemote: "origin", TagEnabled: true})
		tag := "1.0.1"
		operations[key] = domain.TagOperation{OperationID: delivery.NewTagJobID(), FolderName: scope.TaskID, ServiceName: key, RepositoryID: key, SourceBranch: "feature/synthetic", Remote: "origin", TagMode: domain.TagBuildCurrentBranch, State: domain.TagOperationSuccess, CreatedAt: "2026-10-15 18:00:00", UpdatedAt: "2026-10-15 18:00:00", Tag: &tag, GroupServiceID: key, ModuleID: "module", ConflictFiles: []string{}, GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
	}
	if err := a.Manifests.Save(scope.TaskPath, manifest); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		saveTagProbeFixture(t, scope.TaskPath, operation)
	}
	return a, scope, operations
}

func saveTagProbeFixture(t *testing.T, directory string, operation domain.TagOperation) {
	t.Helper()
	directory = filepath.Join(directory, "tag-operations")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, operation.OperationID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func tagProbeFixtureAction(scope TagJobScope, kind, id string) Action {
	return Action{Kind: kind, TaskID: scope.TaskID, Payload: map[string]any{"operationId": id, "force": false, "tagHistoryHome": scope.Home, "tagHistoryTaskRoot": scope.TaskRoot, "tagHistoryTaskPath": scope.TaskPath, "tagHistoryTaskCreatedAt": scope.TaskCreatedAt, "tagJobAccountIdentity": scope.AccountIdentity}}
}

func TestTagJobProbePortStreamsFastRepositoryAndLocalHistoryIsReadOnly(t *testing.T) {
	a, scope, operations := tagJobProbeFixture(t)
	release := make(chan struct{})
	started := make(chan string, 2)
	a.tagJobManager().probe = func(ctx context.Context, _ domain.AppConfig, _ []domain.TaskManifest, _ bool, ids []string, emit func(string, domain.TagOperation)) (bool, error) {
		var selected domain.TagOperation
		for _, operation := range operations {
			if operation.OperationID == ids[0] {
				selected = operation
			}
		}
		started <- selected.ServiceName
		if selected.ServiceName == "slow" {
			select {
			case <-release:
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		selected.GenbuStatus.Build = domain.GenbuBuilding
		saveTagProbeFixture(t, scope.TaskPath, selected)
		emit(scope.TaskPath, selected)
		return true, nil
	}
	var local []TagRecord
	if _, err := a.DispatchTagProbe(context.Background(), tagProbeFixtureAction(scope, "tag.history", ""), func(record TagRecord) { local = append(local, record) }); err != nil || len(local) != 2 {
		t.Fatalf("local history failed: %v", err)
	}
	select {
	case <-started:
		t.Fatal("local-only history queried Genbu")
	default:
	}
	accepted := make(chan TagRecord, 4)
	done := make(chan error, 1)
	go func() {
		_, err := a.DispatchTagProbe(context.Background(), tagProbeFixtureAction(scope, "tag.genbu.refresh", ""), func(record TagRecord) { accepted <- record })
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("independent probe lanes did not start")
		}
	}
	select {
	case record := <-accepted:
		if record.ServiceID != "fast" {
			t.Fatal("slow probe crossed barrier")
		}
	case <-time.After(time.Second):
		t.Fatal("fast record waited for slow")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTagJobProbePortCoalescesOverlapsAndCloseJoins(t *testing.T) {
	a, scope, operations := tagJobProbeFixture(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	a.tagJobManager().probe = func(ctx context.Context, _ domain.AppConfig, _ []domain.TaskManifest, _ bool, _ []string, emit func(string, domain.TagOperation)) (bool, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		emit(scope.TaskPath, operations["slow"])
		return true, nil
	}
	action := tagProbeFixtureAction(scope, "tag.status", operations["slow"].OperationID)
	done := make(chan error, 2)
	go func() { _, err := a.DispatchTagProbe(context.Background(), action, nil); done <- err }()
	<-started
	go func() { _, err := a.DispatchTagProbe(context.Background(), action, nil); done <- err }()
	select {
	case <-started:
		t.Fatal("same repository overlapped")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	count := calls
	mu.Unlock()
	if count != 1 {
		t.Fatalf("overlapping identical queries not coalesced: %d", count)
	}
	// A new in-flight query is owned by App shutdown, independently of a UI
	// request context that may still be alive during native window teardown.
	a.tagJobManager().probe = func(ctx context.Context, _ domain.AppConfig, _ []domain.TaskManifest, _ bool, _ []string, _ func(string, domain.TagOperation)) (bool, error) {
		started <- struct{}{}
		<-ctx.Done()
		return false, ctx.Err()
	}
	go func() { _, err := a.DispatchTagProbe(context.Background(), action, nil); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.CloseTagJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown did not cancel probe: %v", err)
	}
}

func TestTagJobProbeIdentityAndLocalPhaseOwnership(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, f := scopes["synthetic"], tasks["synthetic"]
	f.resources["slow"] = []string{"repo"}
	f.releases["slow"] = make(chan struct{})
	initial := startTagFixture(t, a, scope, "slow")
	receiveTagStart(t, f)
	record := *initial.Items[0].Record
	record.GenbuStatus.Build = domain.GenbuBuilding
	m := a.tagJobManager()
	m.mergeProbeRecord(f.config, []domain.TaskManifest{f.manifest}, "wrong-account", record)
	snapshot, _ := a.TagJobSnapshot(context.Background(), scope)
	if snapshot.Items[0].Record.GenbuStatus.Build == domain.GenbuBuilding {
		t.Fatal("wrong account updated job status")
	}
	m.mergeProbeRecord(f.config, []domain.TaskManifest{f.manifest}, scope.AccountIdentity, record)
	snapshot, _ = a.TagJobSnapshot(context.Background(), scope)
	if snapshot.Items[0].Record.GenbuStatus.Build != domain.GenbuBuilding || snapshot.Items[0].Done {
		t.Fatal("accepted Genbu status lost or finalized local phase")
	}
	close(f.releases["slow"])
	waitTagJob(t, a, scope, initial.ID)
}

func TestTagJobLocalProgressPreservesAcceptedGenbu(t *testing.T) {
	m := &tagJobManager{}
	w := &tagJobWork{job: &tagJob{state: TagJobSnapshot{Scope: TagJobScope{TaskID: "synthetic"}}}}
	tag, checked := "1.0.1", "2026-10-15 18:00:01"
	operation := domain.TagOperation{OperationID: delivery.NewTagJobID(), State: domain.TagOperationTagPushed, Tag: &tag}
	m.setRecordLocked(w, operation)
	w.view.Record.GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding, CheckedAt: &checked}
	operation.State = domain.TagOperationSuccess
	m.setRecordLocked(w, operation)
	if w.view.Record.GenbuStatus.Build != domain.GenbuBuilding || w.view.Record.State != string(domain.TagOperationSuccess) {
		t.Fatal("late local completion overwrote the accepted probe")
	}
	older := "2026-10-15 18:00:00"
	if !tagStatusNewer(w.view.Record.GenbuStatus, domain.GenbuTagProbeStatus{CheckedAt: &older}) {
		t.Fatal("older local history could reset newer status")
	}
}
