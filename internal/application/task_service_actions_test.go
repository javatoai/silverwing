package application

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type taskServicesFake struct {
	pods   func(context.Context, string, string) (production.ServicePods, error)
	config func(context.Context, string, string, string) (string, error)
}

func (f taskServicesFake) Pods(ctx context.Context, _ domain.AppConfig, env, service string) (production.ServicePods, error) {
	if f.pods != nil {
		return f.pods(ctx, env, service)
	}
	return production.ServicePods{Service: service, Environment: env, Pods: []production.RuntimePod{}}, nil
}
func (f taskServicesFake) ServiceConfig(ctx context.Context, _ domain.AppConfig, env, service, ns string) (string, error) {
	if f.config != nil {
		return f.config(ctx, env, service, ns)
	}
	return "password: <redacted>\n", nil
}

func taskServicesFixture(t *testing.T) (*App, Action) {
	t.Helper()
	a, manifest, old := taskFilesActionFixture(t)
	cfg, err := a.Config.LoadForPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "synthetic", RootPath: a.Paths.Tasks, GitCommonDirectory: a.Paths.Tasks + "/.git"}}
	cfg.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "service", GenbuServiceName: "alias", Enabled: true, MasterBranch: "origin/main", TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Modules: []domain.ServiceModuleConfig{{ID: "one", Name: "one", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}, {ID: "two", Name: "two", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}}}}
	if err = a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	setTaskServicesProbe(t, a, true)
	cfg, err = a.Config.LoadForPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest.GroupID = domain.DefaultGroupID
	manifest.Services = []domain.ServiceWorkspace{{GroupServiceID: "service", ServiceName: "service", ModuleID: "one"}, {GroupServiceID: "service", ServiceName: "service", ModuleID: "two"}}
	for i := range manifest.Services {
		w := &manifest.Services[i]
		w.RepositoryID = "repo"
		w.DevelopmentTool = domain.DevelopmentToolVisualStudioCode
		w.Health = domain.WorkspaceReady
		w.Strategy = domain.WorkspaceStandardWorktree
		w.ModuleSource = domain.TaskModuleConfigured
		w.TagMode = domain.TagBuildCurrentBranch
		w.RepositoryPath = a.Paths.Tasks
		w.WorktreePath = filesafeServicePath(a.Paths.Tasks, w.ModuleID)
	}
	files := old.Payload["taskFilesContext"].(TaskFilesContext)
	if err = a.Manifests.Save(files.TaskPath, manifest); err != nil {
		t.Fatal(err)
	}
	return a, Action{Kind: "task.services.pods", TaskID: old.TaskID, ServiceID: "service", Payload: map[string]any{"environment": "uat", "taskServicesContext": TaskServicesContext{TaskFilesContext: files, GroupID: domain.DefaultGroupID, GenbuServiceName: "alias", QueryIdentity: TaskServiceQueryIdentity(cfg)}}}
}

func filesafeServicePath(root, module string) string { return root + "/task/" + module }

func TestTaskServicesReadonlyEmptyFailureAndConfiguration(t *testing.T) {
	a, action := taskServicesFixture(t)
	before := taskFilesBytes(t, a.Paths.Home)
	result, err := a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{})
	if err != nil || len(result.Data.(TaskServicePodsResult).Pods) != 0 {
		t.Fatal(result, err)
	}
	_, err = a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{pods: func(context.Context, string, string) (production.ServicePods, error) {
		return production.ServicePods{}, errors.New("offline")
	}})
	if err == nil {
		t.Fatal("failure became zero pods")
	}
	action.Kind = "task.services.config"
	action.Payload["namespace"] = "team"
	result, err = a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{config: func(_ context.Context, env, service, ns string) (string, error) {
		if env != "uat" || service != "alias" || ns != "team" {
			t.Fatal(env, service, ns)
		}
		return "safe: <redacted>\n", nil
	}})
	if err != nil || result.Data.(TaskServiceConfigResult).Namespace != "team" {
		t.Fatal(result, err)
	}
	if after := taskFilesBytes(t, a.Paths.Home); !reflect.DeepEqual(before, after) {
		t.Fatal("read modified config/task/materials/drafts")
	}
	for _, kind := range []string{"task.services.pods", "task.services.config"} {
		policy := DescribeAction(kind)
		if !policy.Known || !policy.DedicatedResult || !policy.QuietSuccess || !policy.Cancellable || policy.ReloadSnapshot || policy.RefreshOnError {
			t.Fatal(kind, policy)
		}
	}
}

func TestTaskServicesRejectStaleOwnershipMappingAndCancelledResult(t *testing.T) {
	for _, change := range []string{"remove", "mapping", "cli", "cancel", "task-identity"} {
		t.Run(change, func(t *testing.T) {
			a, action := taskServicesFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := taskServicesFake{pods: func(context.Context, string, string) (production.ServicePods, error) {
				switch change {
				case "remove", "task-identity":
					manifest, err := a.Manifests.Load(action.Payload["taskServicesContext"].(TaskServicesContext).TaskPath)
					if err != nil {
						t.Fatal(err)
					}
					if change == "remove" {
						manifest.Services = nil
					} else {
						manifest.CreatedAt = "new-incarnation"
					}
					if err = a.Manifests.Save(action.Payload["taskServicesContext"].(TaskServicesContext).TaskPath, manifest); err != nil {
						t.Fatal(err)
					}
				case "mapping", "cli":
					cfg, err := a.Config.LoadForPreview(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if change == "mapping" {
						cfg.Groups[0].Services[0].GenbuServiceName = "new-alias"
					} else {
						p := "other.exe"
						cfg.GenbuExecutablePath = &p
					}
					if err = a.Config.Save(cfg); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
				return production.ServicePods{}, nil
			}}
			if _, err := a.taskServicesActionWithReader(ctx, action, fake); err == nil {
				t.Fatal("stale/cancelled result accepted")
			}
		})
	}
}

func TestTaskServicesNamespaceValidationIsConfigurationOnly(t *testing.T) {
	a, action := taskServicesFixture(t)
	action.Payload["namespace"] = "bad_namespace"
	if _, err := a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{}); err != nil {
		t.Fatal("Pod query depended on namespace draft", err)
	}
	action.Kind = "task.services.config"
	if _, err := a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{}); err == nil {
		t.Fatal("invalid configuration namespace accepted")
	}
	action.Payload["namespace"] = " team "
	result, err := a.taskServicesActionWithReader(context.Background(), action, taskServicesFake{})
	if err != nil || result.Data.(TaskServiceConfigResult).Namespace != "team" {
		t.Fatal(result, err)
	}
}

func TestTaskServicesGlobalFourConcurrentQueriesAndCancellation(t *testing.T) {
	a, action := taskServicesFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := make(chan struct{})
	started := make(chan struct{}, 8)
	var running, peak atomic.Int32
	fake := taskServicesFake{pods: func(ctx context.Context, _, _ string) (production.ServicePods, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-gate:
			return production.ServicePods{}, nil
		case <-ctx.Done():
			return production.ServicePods{}, ctx.Err()
		}
	}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.taskServicesActionWithReader(ctx, action, fake) }()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("queries did not enter")
		}
	}
	select {
	case <-started:
		t.Fatal("more than four CLI queries started")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	wg.Wait()
	if peak.Load() != 4 || running.Load() != 0 {
		t.Fatal(peak.Load(), running.Load())
	}
}
