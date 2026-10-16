package application

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

type tagScopeBarrierTask struct {
	tagTaskRunner
	planKey, stage                        string
	entered, release                      chan struct{}
	initializations, executions, canceled atomic.Int32
}

func (f *tagScopeBarrierTask) wait() { close(f.entered); <-f.release }
func (f *tagScopeBarrierTask) Plan(ctx context.Context, key, id, batch string) (delivery.TagTaskItem, error) {
	item, err := f.tagTaskRunner.Plan(ctx, key, id, batch)
	if f.stage == "prepare" && key == f.planKey {
		f.wait()
	}
	return item, err
}
func (f *tagScopeBarrierTask) WithLock(run func() error) error {
	if f.stage == "lease" {
		f.wait()
	}
	return f.tagTaskRunner.WithLock(run)
}
func (f *tagScopeBarrierTask) Initialize(ctx context.Context, item delivery.TagTaskItem) error {
	f.initializations.Add(1)
	// Match the real record store's directory-creating boundary. A failed
	// freshness guard would recreate a deleted fixture and be observable here.
	if err := os.MkdirAll(filepath.Join(f.Directory(), "tag-operations"), 0700); err != nil {
		return err
	}
	return f.tagTaskRunner.Initialize(ctx, item)
}
func (f *tagScopeBarrierTask) Run(ctx context.Context, kind string, item delivery.TagTaskItem, emit func(delivery.TagProgress)) (domain.TagOperation, *delivery.TagWorkspaceCheckReport, error) {
	f.executions.Add(1)
	return f.tagTaskRunner.Run(ctx, kind, item, emit)
}
func (f *tagScopeBarrierTask) CancelPending(item delivery.TagTaskItem) (domain.TagOperation, error) {
	f.canceled.Add(1)
	return f.tagTaskRunner.CancelPending(item)
}

func changeTagScopeFixture(t *testing.T, a *App, scope TagJobScope, f *tagJobFakeTask, kind string) {
	t.Helper()
	err := f.locks.WithTask(scope.TaskPath, func() error {
		switch kind {
		case "archive":
			manifest := f.manifest
			manifest.LifecycleStatus = domain.TaskArchived
			return a.Manifests.Save(scope.TaskPath, manifest)
		case "delete", "recreated":
			if err := os.RemoveAll(scope.TaskPath); err != nil {
				return err
			}
			if kind == "recreated" {
				manifest := f.manifest
				manifest.CreatedAt = "2026-10-15 19:00:00"
				return a.Manifests.Save(scope.TaskPath, manifest)
			}
		case "config":
			config := f.config
			config.Theme = domain.ThemeDark
			return a.Config.Save(config)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTagJobsScopeFreshnessBeforeAnyWrite(t *testing.T) {
	for _, stage := range []string{"prepare", "lease"} {
		for _, change := range []string{"archive", "delete", "recreated", "config"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
				scope, original := scopes["synthetic"], tasks["synthetic"]
				original.resources["target"] = []string{"repo"}
				controlled := &tagScopeBarrierTask{tagTaskRunner: original, planKey: "target", stage: stage, entered: make(chan struct{}), release: make(chan struct{})}
				a.tagJobManager().openTask = func(domain.AppConfig, string) (tagTaskRunner, error) { return controlled, nil }
				type result struct {
					snapshot TagJobSnapshot
					err      error
				}
				started := make(chan result, 1)
				go func() {
					snapshot, err := a.StartTagJob(context.Background(), scope, Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"target"}}})
					started <- result{snapshot, err}
				}()
				select {
				case <-controlled.entered:
				case <-time.After(2 * time.Second):
					t.Fatal("controlled planning/lease boundary was not reached")
				}
				changeTagScopeFixture(t, a, scope, original, change)
				close(controlled.release)
				outcome := <-started
				if stage == "prepare" {
					if outcome.err == nil {
						t.Fatal("stale prepared request was admitted")
					}
				} else {
					if outcome.err != nil {
						t.Fatal(outcome.err)
					}
					final := waitTagJob(t, a, scope, outcome.snapshot.ID)
					if final.Busy || final.Error == "" || len(final.Items) != 1 || !final.Items[0].Done || final.Items[0].Phase != TagJobPhaseFailed {
						t.Fatalf("invalid task lease did not settle as failed: %+v", final)
					}
					if original.locksTaken != 1 {
						t.Fatal("freshness guard reacquired the task lease")
					}
				}
				if controlled.initializations.Load() != 0 || controlled.executions.Load() != 0 || controlled.canceled.Load() != 0 {
					t.Fatal("stale task reached record initialization or Git execution")
				}
				if _, err := os.Stat(filepath.Join(scope.TaskPath, "tag-operations")); !os.IsNotExist(err) {
					t.Fatal("stale task created a record directory")
				}
				if change == "delete" {
					if _, err := os.Lstat(scope.TaskPath); !os.IsNotExist(err) {
						t.Fatal("deleted task directory was recreated")
					}
				}
			})
		}
	}
}

func TestTagJobsAppendRevalidatesAfterPrepareWithoutCancelingOwner(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, original := scopes["synthetic"], tasks["synthetic"]
	original.resources["active"], original.resources["late"] = []string{"active"}, []string{"late"}
	original.releases["active"] = make(chan struct{})
	controlled := &tagScopeBarrierTask{tagTaskRunner: original, planKey: "late", stage: "prepare", entered: make(chan struct{}), release: make(chan struct{})}
	a.tagJobManager().openTask = func(domain.AppConfig, string) (tagTaskRunner, error) { return controlled, nil }
	initial := startTagFixture(t, a, scope, "active")
	receiveTagStart(t, original)
	completed := make(chan error, 1)
	go func() {
		_, err := a.AppendTagJob(context.Background(), scope, initial.ID, Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"late"}}})
		completed <- err
	}()
	select {
	case <-controlled.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("append did not reach its planning boundary")
	}
	config := original.config
	config.Theme = domain.ThemeDark
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	close(controlled.release)
	if err := <-completed; err == nil {
		t.Fatal("append used configuration that changed during Plan")
	}
	m := a.tagJobManager()
	m.mu.Lock()
	active := m.snapshotLocked(m.jobs[tagJobScopeKey(scope)])
	m.mu.Unlock()
	if !active.Busy || active.Cancelling || len(active.Items) != 1 || controlled.initializations.Load() != 1 {
		t.Fatal("rejected append altered the captured active owner")
	}
	close(original.releases["active"])
	final := waitTagJob(t, a, scope, initial.ID)
	if final.Busy || final.Items[0].Phase != TagJobPhaseCompleted || original.locksTaken != 1 || controlled.executions.Load() != 1 {
		t.Fatal("legal captured owner did not finish after config changed")
	}
}
