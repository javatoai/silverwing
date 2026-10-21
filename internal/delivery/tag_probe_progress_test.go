package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type tagProbeBarrierRunner struct {
	mu      sync.Mutex
	calls   map[string]int
	started chan string
	slow    chan struct{}
}

func (r *tagProbeBarrierRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	if len(spec.Arguments) != 4 || spec.Arguments[0] != "query-tag" || spec.Arguments[1] != "--json" {
		return process.Result{}, errors.New("unexpected synthetic command")
	}
	key := spec.Arguments[3]
	r.mu.Lock()
	r.calls[key]++
	r.mu.Unlock()
	r.started <- key
	if key == "slow" {
		select {
		case <-r.slow:
		case <-ctx.Done():
			return process.Result{}, ctx.Err()
		}
	}
	return process.Result{ExitCode: 0, Stdout: `{"build_status":"构建中","uat_status":"初始","production_status":"初始"}`}, nil
}

func tagProbeProgressFixture(t *testing.T) (*Service, domain.AppConfig, []domain.TaskManifest, []domain.TagOperation, *tagProbeBarrierRunner) {
	t.Helper()
	task, _ := tagProgressTaskFixture(t)
	service := task.service
	runner := &tagProbeBarrierRunner{calls: map[string]int{}, started: make(chan string, 10), slow: make(chan struct{})}
	service.runner = runner
	config := task.Config()
	executable := filepath.Join(t.TempDir(), "synthetic-genbu.exe")
	if err := os.WriteFile(executable, []byte("synthetic fixture only"), 0600); err != nil {
		t.Fatal(err)
	}
	config.GenbuExecutablePath = &executable
	config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "fast-service", GenbuServiceName: "fast-service", GenbuProbeEnabled: true}, {ID: "slow-service", GenbuServiceName: "slow-service", GenbuProbeEnabled: true}}
	var operations []domain.TagOperation
	for i, key := range []string{"fast", "slow", "fast"} {
		operation := domain.TagOperation{OperationID: NewTagJobID(), FolderName: task.Manifest().FolderName, ServiceName: key, RepositoryID: "repository", SourceBranch: "feature/synthetic", Remote: "origin", TagMode: domain.TagBuildCurrentBranch, State: domain.TagOperationSuccess, CreatedAt: formatSilverwingTime(time.Now().Add(time.Duration(i) * time.Second)), UpdatedAt: formatSilverwingTime(time.Now()), Tag: &key, GroupServiceID: key + "-service", ModuleID: "module", ConflictFiles: []string{}, GenbuStatus: initialGenbuStatus()}
		if err := service.store.save(context.Background(), task.Directory(), operation); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, operation)
	}
	return service, config, []domain.TaskManifest{task.Manifest()}, operations, runner
}

func TestTagProbeStreamsFastCASBeforeSlowAndDeduplicatesTag(t *testing.T) {
	service, config, tasks, operations, runner := tagProbeProgressFixture(t)
	accepted := make(chan domain.TagOperation, 4)
	done := make(chan error, 1)
	go func() {
		_, err := service.ProbeWithProgress(context.Background(), config, tasks, false, nil, func(directory string, operation domain.TagOperation) {
			stored, readErr := service.store.load(directory, operation.OperationID)
			if readErr != nil || stored.GenbuStatus.Build != domain.GenbuBuilding {
				done <- errors.New("probe callback preceded CAS")
			}
			accepted <- operation
		})
		done <- err
	}()
	for range 2 {
		select {
		case <-runner.started:
		case <-time.After(time.Second):
			t.Fatal("different services did not start independently")
		}
	}
	select {
	case operation := <-accepted:
		if value(operation.Tag) != "fast" {
			t.Fatal("slow result arrived without barrier release")
		}
	case <-time.After(time.Second):
		t.Fatal("fast item waited for slow repository")
	}
	runner.mu.Lock()
	fastCalls := runner.calls["fast"]
	runner.mu.Unlock()
	if fastCalls != 1 {
		t.Fatal("identical service/tag was queried repeatedly")
	}
	close(runner.slow)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		stored, err := service.store.load(filepath.Join(value(config.TaskRoot), tasks[0].TaskDirectoryName), operation.OperationID)
		if err != nil || stored.GenbuStatus.Build != domain.GenbuBuilding {
			t.Fatal("not every operation received accepted status")
		}
	}
}

func TestTagProbeSkipsUnpushedAndRejectsStaleCAS(t *testing.T) {
	service, config, tasks, operations, runner := tagProbeProgressFixture(t)
	directory := filepath.Join(value(config.TaskRoot), tasks[0].TaskDirectoryName)
	unpushed := operations[0]
	unpushed.State = domain.TagOperationLocalTagCreated
	if err := service.store.save(context.Background(), directory, unpushed); err != nil {
		t.Fatal(err)
	}
	changed := make(chan string, 4)
	done := make(chan error, 1)
	go func() {
		_, err := service.ProbeWithProgress(context.Background(), config, tasks, false, []string{unpushed.OperationID, operations[1].OperationID}, func(_ string, operation domain.TagOperation) { changed <- operation.OperationID })
		done <- err
	}()
	select {
	case key := <-runner.started:
		if key != "slow" {
			t.Fatal("an unpushed tag was probed")
		}
	case <-time.After(time.Second):
		t.Fatal("slow query did not start")
	}
	newer := operations[1]
	newTag := "replacement"
	newer.Tag = &newTag
	if err := service.store.save(context.Background(), directory, newer); err != nil {
		t.Fatal(err)
	}
	close(runner.slow)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("stale probe emitted a record after CAS rejected it")
	default:
	}
}

func TestTagProbeNotFoundGraceAndIdleNeverPermanentlyStops(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name         string
		age, checked time.Duration
		want         bool
	}{{"recent", 110 * time.Second, 0, true}, {"old-throttled", 3 * time.Minute, 0, false}, {"old-retries", 3 * time.Minute, 31 * time.Second, true}} {
		t.Run(test.name, func(t *testing.T) {
			checked := formatSilverwingTime(now.Add(-test.checked))
			tag := "synthetic-tag"
			operation := domain.TagOperation{State: domain.TagOperationSuccess, Tag: &tag, UpdatedAt: formatSilverwingTime(now.Add(-test.age)), GenbuStatus: domain.GenbuTagProbeStatus{NotFound: true, CheckedAt: &checked}}
			if got := TagProbeDue(operation, now); got != test.want {
				t.Fatalf("NotFound cadence got %v", got)
			}
			operation.GenbuStatus.UAT = domain.GenbuSuccess
			if TagProbeDue(operation, now) {
				t.Fatal("terminal UAT success restarted")
			}
		})
	}
}
