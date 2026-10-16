package application

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type tagJobFakeTask struct {
	config     domain.AppConfig
	manifest   domain.TaskManifest
	directory  string
	locks      *workspace.OperationLocks
	mu         sync.Mutex
	resources  map[string][]string
	records    map[string]domain.TagOperation
	releases   map[string]chan struct{}
	started    chan string
	locksTaken int
	fail       map[string]bool
}

func (f *tagJobFakeTask) Config() domain.AppConfig      { return f.config }
func (f *tagJobFakeTask) Manifest() domain.TaskManifest { return f.manifest }
func (f *tagJobFakeTask) Directory() string             { return f.directory }
func (f *tagJobFakeTask) WithLock(run func() error) error {
	f.mu.Lock()
	f.locksTaken++
	f.mu.Unlock()
	return f.locks.WithTask(f.directory, run)
}
func (f *tagJobFakeTask) SelectionKeys(keys []string, _ bool) ([]string, error) { return keys, nil }
func (f *tagJobFakeTask) Plan(_ context.Context, key, id, batch string) (delivery.TagTaskItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	operation := f.records[id]
	if id != "" {
		if operation.OperationID == "" {
			return delivery.TagTaskItem{}, errors.New("missing synthetic operation")
		}
		key = operation.GroupServiceID
	}
	if id == "" {
		operation = domain.TagOperation{OperationID: delivery.NewTagJobID(), GroupServiceID: key, ModuleID: "module", ServiceName: key, SourceBranch: "feature/synthetic", State: domain.TagOperationCreated, CreatedAt: "2026-10-15 18:00:00", UpdatedAt: "2026-10-15 18:00:00", BatchID: &batch}
	}
	return delivery.TagTaskItem{SelectionKey: key, WorkspacePath: filepath.Join(f.directory, key), Resources: f.resources[key], Operation: operation}, nil
}
func (f *tagJobFakeTask) Initialize(_ context.Context, item delivery.TagTaskItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[item.Operation.OperationID] = item.Operation
	return nil
}
func (f *tagJobFakeTask) Run(ctx context.Context, kind string, item delivery.TagTaskItem, progress func(delivery.TagProgress)) (domain.TagOperation, *delivery.TagWorkspaceCheckReport, error) {
	f.started <- kind + ":" + item.SelectionKey
	if err := f.locks.WithTask(f.directory, func() error { return nil }); err == nil {
		return item.Operation, nil, errors.New("task lease not retained")
	}
	f.mu.Lock()
	release := f.releases[item.SelectionKey]
	failed := f.fail[item.SelectionKey]
	f.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			operation := item.Operation
			operation.State = domain.TagOperationPartial
			tag := "1.0.1"
			operation.Tag = &tag
			progress(delivery.TagProgress{FailureKind: TagJobFailureCanceled, Operation: &operation})
			return operation, nil, nil
		}
	}
	operation := item.Operation
	if kind == "tag.workspace-check" {
		return operation, &delivery.TagWorkspaceCheckReport{Clean: true}, nil
	}
	operation.State = domain.TagOperationSuccess
	if failed {
		operation.State = domain.TagOperationFailed
		message := "synthetic dirty tree"
		operation.Message = &message
		progress(delivery.TagProgress{Phase: "failed", FailureKind: "uncommitted", Operation: &operation})
	} else {
		progress(delivery.TagProgress{Phase: "pushing-tag", Step: "推送测试Tag", Operation: &operation})
	}
	f.mu.Lock()
	f.records[operation.OperationID] = operation
	f.mu.Unlock()
	return operation, nil, nil
}
func (f *tagJobFakeTask) CancelPending(item delivery.TagTaskItem) (domain.TagOperation, error) {
	operation := item.Operation
	operation.State = domain.TagOperationFailed
	f.mu.Lock()
	f.records[operation.OperationID] = operation
	f.mu.Unlock()
	return operation, nil
}

func tagJobManagerFixture(t *testing.T, names ...string) (*App, map[string]TagJobScope, map[string]*tagJobFakeTask) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.TagEnabled = true
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]TagJobScope{}
	tasks := map[string]*tagJobFakeTask{}
	for _, name := range names {
		manifest := taskIdentityFixture(t, a, paths.Tasks, name, name)
		directory := filepath.Join(paths.Tasks, name)
		scopes[name] = TagJobScope{Home: paths.Home, TaskRoot: paths.Tasks, TaskID: name, TaskPath: directory, TaskCreatedAt: manifest.CreatedAt, ConfigIdentity: configTagIdentity(config), AccountIdentity: "synthetic-account"}
		tasks[name] = &tagJobFakeTask{config: config, manifest: manifest, directory: directory, locks: workspace.NewOperationLocks(paths.Locks), resources: map[string][]string{}, records: map[string]domain.TagOperation{}, releases: map[string]chan struct{}{}, started: make(chan string, 32), fail: map[string]bool{}}
	}
	a.tagJobManager().openTask = func(_ domain.AppConfig, id string) (tagTaskRunner, error) { return tasks[id], nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := a.CloseTagJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	return a, scopes, tasks
}

func receiveTagStart(t *testing.T, f *tagJobFakeTask) string {
	t.Helper()
	select {
	case value := <-f.started:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic job did not start")
		return ""
	}
}
func waitTagJob(t *testing.T, a *App, scope TagJobScope, id string) TagJobSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var last TagJobSnapshot
	if err := a.WatchTagJob(ctx, scope, id, 0, func(snapshot TagJobSnapshot) { last = snapshot }); err != nil {
		t.Fatal(err)
	}
	return last
}
func startTagFixture(t *testing.T, a *App, scope TagJobScope, keys ...string) TagJobSnapshot {
	t.Helper()
	snapshot, err := a.StartTagJob(context.Background(), scope, Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": keys}})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestTagJobsAllIndependentLanesStartAndSnapshotIsolation(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	f := tasks["synthetic"]
	keys := []string{"one", "two", "three", "four", "five", "six"}
	for _, key := range keys {
		f.resources[key] = []string{"repo:" + key}
		f.releases[key] = make(chan struct{})
	}
	initial := startTagFixture(t, a, scopes["synthetic"], keys...)
	if len(initial.Items) != len(keys) {
		t.Fatal("not all selected items were registered")
	}
	seen := map[string]bool{}
	for range keys {
		seen[receiveTagStart(t, f)] = true
	}
	if len(seen) != len(keys) {
		t.Fatal("independent repos were capped")
	}
	initial.Items[0].Record.Detail = "caller mutation"
	snapshot, err := a.TagJobSnapshot(context.Background(), scopes["synthetic"])
	if err != nil || snapshot.Items[0].Record.Detail == "caller mutation" {
		t.Fatal("snapshot aliases manager records")
	}
	for _, key := range keys {
		close(f.releases[key])
	}
	final := waitTagJob(t, a, scopes["synthetic"], initial.ID)
	if final.Busy || final.Sequence <= initial.Sequence {
		t.Fatal("job did not finish monotonically")
	}
	for _, item := range final.Items {
		if !item.Done || item.Phase != TagJobPhaseCompleted {
			t.Fatalf("unexpected terminal item %+v", item)
		}
	}
}

func TestTagJobsSerializeCommonDirectoryAndRemoteAcrossTasks(t *testing.T) {
	for _, resource := range []string{"git:synthetic-common", "remote:host/synthetic"} {
		t.Run(resource, func(t *testing.T) {
			a, scopes, tasks := tagJobManagerFixture(t, "task-one", "task-two")
			one, two := tasks["task-one"], tasks["task-two"]
			one.resources["first"] = []string{resource}
			one.releases["first"] = make(chan struct{})
			two.resources["second"] = []string{resource}
			first := startTagFixture(t, a, scopes["task-one"], "first")
			receiveTagStart(t, one)
			second := startTagFixture(t, a, scopes["task-two"], "second")
			select {
			case <-two.started:
				t.Fatal("conflicting task ran concurrently")
			case <-time.After(25 * time.Millisecond):
			}
			close(one.releases["first"])
			receiveTagStart(t, two)
			waitTagJob(t, a, scopes["task-one"], first.ID)
			waitTagJob(t, a, scopes["task-two"], second.ID)
		})
	}
}

func TestTagJobsAppendSharesTaskLeaseAndFailureDoesNotBlockOtherRepo(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope := scopes["synthetic"]
	f := tasks[scope.TaskID]
	f.resources["slow"] = []string{"same"}
	f.resources["failed"] = []string{"other"}
	f.releases["slow"] = make(chan struct{})
	f.fail["failed"] = true
	first := startTagFixture(t, a, scope, "slow", "failed")
	receiveTagStart(t, f)
	receiveTagStart(t, f)
	var slowID, failedID string
	for _, item := range first.Items {
		if item.SelectionKey == "slow" {
			slowID = item.OperationID
		} else {
			failedID = item.OperationID
		}
	}
	for _, id := range []string{slowID, failedID} {
		if _, err := a.AppendTagJob(context.Background(), scope, first.ID, Action{Kind: "tag.workspace-check", TaskID: scope.TaskID, Payload: map[string]any{"operationId": id}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := receiveTagStart(t, f); got != "tag.workspace-check:failed" {
		t.Fatalf("free repository check did not run immediately: %s", got)
	}
	close(f.releases["slow"])
	if got := receiveTagStart(t, f); got != "tag.workspace-check:slow" {
		t.Fatal(got)
	}
	final := waitTagJob(t, a, scope, first.ID)
	if f.locksTaken != 1 || len(final.Items) != 4 {
		t.Fatal("append reacquired task lock or lost items")
	}
	for _, item := range final.Items {
		if item.SelectionKey == "failed" && item.Kind == "tag.build" && item.FailureKind != TagJobFailureUncommitted {
			t.Fatal("typed failure lost")
		}
	}
}

func TestTagJobsCancellationPartialAndWatcherLifecycle(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope := scopes["synthetic"]
	f := tasks[scope.TaskID]
	f.resources["active"] = []string{"shared"}
	f.resources["queued"] = []string{"shared"}
	f.releases["active"] = make(chan struct{})
	initial := startTagFixture(t, a, scope, "active", "queued")
	receiveTagStart(t, f)
	watchCtx, stopWatch := context.WithCancel(context.Background())
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- a.WatchTagJob(watchCtx, scope, initial.ID, initial.Sequence, func(TagJobSnapshot) {})
	}()
	stopWatch()
	if err := <-watchDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	snapshot, _ := a.TagJobSnapshot(context.Background(), scope)
	if !snapshot.Busy {
		t.Fatal("leaving page canceled the job")
	}
	if err := a.CancelTagJob(context.Background(), scope, initial.ID); err != nil {
		t.Fatal(err)
	}
	final := waitTagJob(t, a, scope, initial.ID)
	if final.Busy {
		t.Fatal("cancel did not settle")
	}
	for _, item := range final.Items {
		if item.Phase != TagJobPhaseCanceled {
			t.Fatalf("cancel phase %+v", item)
		}
		if item.SelectionKey == "active" && (item.Record.State != "PARTIAL" || item.Record.Tag != "1.0.1") {
			t.Fatal("partial remote result was lost")
		}
	}
	m := a.tagJobManager()
	m.mu.Lock()
	j := m.jobs[tagJobScopeKey(scope)]
	watchers, resources := len(j.watchers), len(m.resources)
	m.mu.Unlock()
	if watchers != 0 || resources != 0 {
		t.Fatal("watchers or resource reservations leaked")
	}
	wrong := scope
	wrong.AccountIdentity = "other"
	if _, err := a.TagJobSnapshot(context.Background(), wrong); err == nil {
		t.Fatal("foreign account read the job")
	}
	if _, err := a.AppendTagJob(context.Background(), wrong, initial.ID, Action{Kind: "tag.retry", TaskID: scope.TaskID, Payload: map[string]any{"operationId": initial.Items[0].OperationID}}); err == nil {
		t.Fatal("foreign account appended retry")
	}
}

func TestTagJobsConcurrentAdmissionAppendsAndDeduplicatesBuilds(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, f := scopes["synthetic"], tasks["synthetic"]
	for _, key := range []string{"one", "two", "three"} {
		f.resources[key] = []string{key}
		f.releases[key] = make(chan struct{})
	}
	first := startTagFixture(t, a, scope, "one")
	receiveTagStart(t, f)
	results := make(chan TagJobSnapshot, 8)
	failures := make(chan error, 8)
	var writers sync.WaitGroup
	for i := range 8 {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			key := "two"
			if i%2 == 0 {
				key = "three"
			}
			action := Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"one", key}}}
			var snapshot TagJobSnapshot
			var err error
			if i%3 == 0 {
				snapshot, err = a.AppendTagJob(context.Background(), scope, first.ID, action)
			} else {
				snapshot, err = a.StartTagJob(context.Background(), scope, action)
			}
			results <- snapshot
			failures <- err
		}(i)
	}
	writers.Wait()
	for range 8 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
		if snapshot := <-results; snapshot.ID != first.ID {
			t.Fatal("concurrent admission created another task owner")
		}
	}
	receiveTagStart(t, f)
	receiveTagStart(t, f)
	snapshot, err := a.TagJobSnapshot(context.Background(), scope)
	if err != nil || len(snapshot.Items) != 3 {
		t.Fatalf("admission lost a selection or duplicated writes: %v %+v", err, snapshot)
	}
	for _, item := range snapshot.Items {
		if item.Record.BatchID != first.BatchID {
			t.Fatal("appended item escaped its batch")
		}
	}
	for _, key := range []string{"one", "two", "three"} {
		close(f.releases[key])
	}
	final := waitTagJob(t, a, scope, first.ID)
	if len(final.Items) != 3 || f.locksTaken != 1 {
		t.Fatal("append reacquired task lease")
	}
}

func TestTagJobsCloseCancelsAndNewBatchReleasesPriorWatch(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, f := scopes["synthetic"], tasks["synthetic"]
	f.resources["quick"] = []string{"repo"}
	first := startTagFixture(t, a, scope, "quick")
	waitTagJob(t, a, scope, first.ID)
	f.resources["slow"] = []string{"repo"}
	f.releases["slow"] = make(chan struct{})
	next := startTagFixture(t, a, scope, "slow")
	receiveTagStart(t, f)
	receiveTagStart(t, f)
	if next.ID == first.ID {
		t.Fatal("finished batch was reused")
	}
	if err := a.WatchTagJob(context.Background(), scope, first.ID, 0, func(TagJobSnapshot) {}); err == nil {
		t.Fatal("old batch watcher attached to replacement")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.CloseTagJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartTagJob(context.Background(), scope, Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"quick"}}}); err == nil {
		t.Fatal("closed app accepted writes")
	}
}

func TestTagJobsAdmissionIdentityAndCancellation(t *testing.T) {
	left := tagJobWork{planned: delivery.TagTaskItem{SelectionKey: "service:module", WorkspacePath: "workspace", TagMessagePrefix: "Tag", Resources: []string{"git:common"}, Operation: domain.TagOperation{SourceBranch: "feature/synthetic", Remote: "origin", TagMode: domain.TagBuildCurrentBranch}}, view: TagJobItem{Kind: "tag.build"}}
	for _, change := range []func(*tagJobWork){
		func(w *tagJobWork) { w.planned.TagMessagePrefix = "different" },
		func(w *tagJobWork) { w.planned.WorkspacePath = "another" },
		func(w *tagJobWork) { w.planned.Resources = []string{"git:another"} },
		func(w *tagJobWork) { w.planned.Operation.TagMode = domain.TagBuildMergeToTargetBranch },
		func(w *tagJobWork) { target := "synthetic-target"; w.planned.Operation.TargetBranch = &target },
	} {
		right := left
		change(&right)
		if tagWorkEquivalent(&left, &right) {
			t.Fatal("different effective build inputs were deduplicated")
		}
	}
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, f := scopes["synthetic"], tasks["synthetic"]
	f.resources["slow"] = []string{"repo"}
	f.releases["slow"] = make(chan struct{})
	initial := startTagFixture(t, a, scope, "slow")
	receiveTagStart(t, f)
	m := a.tagJobManager()
	m.mu.Lock()
	m.jobs[tagJobScopeKey(scope)].state.Cancelling = true
	m.mu.Unlock()
	action := Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"slow"}}}
	if _, err := a.StartTagJob(context.Background(), scope, action); err == nil {
		t.Fatal("Start admitted work while cancellation was pending")
	}
	if _, err := a.AppendTagJob(context.Background(), scope, initial.ID, action); err == nil {
		t.Fatal("Append admitted work while cancellation was pending")
	}
	close(f.releases["slow"])
	waitTagJob(t, a, scope, initial.ID)
}
