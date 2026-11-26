package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type parallelTaskFixture struct {
	root, remote, taskRoot string
	app                    *TaskApplicationService
	config                 domain.AppConfig
	request                CreateTaskRequest
}

func newParallelTaskFixture(t *testing.T, count int) *parallelTaskFixture {
	t.Helper()
	root := t.TempDir()
	f := &parallelTaskFixture{
		root: root, remote: filepath.Join(root, "remote.git"), taskRoot: filepath.Join(root, "tasks"),
		config: domain.DefaultAppConfig(), request: CreateTaskRequest{FolderName: "parallel-task", FeatureBranch: "feature/parallel-task"},
	}
	f.config.TaskRoot = &f.taskRoot
	f.request.GroupID = f.config.Groups[0].ID
	seed := filepath.Join(root, "seed")
	gitCompare(t, root, "init", "--bare", "--initial-branch=main", f.remote)
	gitCompare(t, root, "init", "--initial-branch=main", seed)
	gitCompare(t, seed, "config", "user.name", "Task Creation Test")
	gitCompare(t, seed, "config", "user.email", "task@example.invalid")
	writeCompareFile(t, seed, "tracked.txt", "base\n")
	commitCompare(t, seed, "base")
	gitCompare(t, seed, "push", f.remote, "main")
	gitCompare(t, f.remote, "update-server-info")
	globalConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0600); err != nil {
		t.Fatal(err)
	}
	git := NewGitClient(nil, "git")
	git.Environment = map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": globalConfig, "GIT_TERMINAL_PROMPT": "0", "NO_PROXY": "127.0.0.1", "no_proxy": "127.0.0.1",
	}
	git.RemoveEnvironment = []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG", "GIT_TEMPLATE_DIR", "GIT_EXEC_PATH", "GIT_SSH_COMMAND", "GIT_SSH",
	}
	f.app = NewTaskApplicationService(nil, git, storage.NewPaths(filepath.Join(root, "profile")))
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("repo-%02d", index)
		path := filepath.Join(root, id)
		gitCompare(t, root, "clone", f.remote, path)
		f.selectService(id, path, filepath.Join(path, ".git"), "origin/main")
	}
	return f
}

func (f *parallelTaskFixture) selectService(id, path, common, base string) {
	f.config.Repositories = append(f.config.Repositories, domain.RepositoryConfig{ID: id, Name: id, RootPath: path, GitCommonDirectory: common})
	f.config.Groups[0].Services = append(f.config.Groups[0].Services, domain.GroupServiceConfig{
		ID: id, RepositoryID: id, DisplayName: id, Enabled: true, MasterBranch: base,
		DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		Modules:         []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch}},
	})
	f.request.ServiceIDs = append(f.request.ServiceIDs, id)
}

type parallelTaskRun struct {
	done     chan struct{}
	manifest domain.TaskManifest
	err      error
}

func startParallelTaskRun(run func() (domain.TaskManifest, error)) *parallelTaskRun {
	result := &parallelTaskRun{done: make(chan struct{})}
	go func() {
		defer close(result.done)
		result.manifest, result.err = run()
	}()
	return result
}

func (r *parallelTaskRun) wait(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-r.done:
	case <-ctx.Done():
		t.Fatalf("task run did not finish: %v", ctx.Err())
	}
}

type parallelFetchBarrier struct {
	arrivals chan string
	release  chan struct{}
	seen     sync.Map
}

func newParallelFetchBarrier(count int) *parallelFetchBarrier {
	return &parallelFetchBarrier{arrivals: make(chan string, count), release: make(chan struct{})}
}

func (b *parallelFetchBarrier) waitForRepositories(t *testing.T, ctx context.Context, run *parallelTaskRun, repositories []string) {
	t.Helper()
	for range repositories {
		select {
		case id := <-b.arrivals:
			if !slices.Contains(repositories, id) {
				t.Fatalf("unexpected repository %s entered this wave", id)
			}
		case <-run.done:
			t.Fatalf("task run finished before all %d repositories began: %v", len(repositories), run.err)
		case <-ctx.Done():
			t.Fatalf("waiting for %d concurrent repositories: %v", len(repositories), ctx.Err())
		}
	}
}

func TestTaskCreationParallelGitRepositories(t *testing.T) {
	const count = 11
	f := newParallelTaskFixture(t, count)
	// The adjacent checkout has a separate configured ID and path, but its Git
	// common directory is the same as the first repository's.
	main := f.config.Repositories[0]
	linked := filepath.Join(f.root, "linked")
	gitCompare(t, main.RootPath, "worktree", "add", "-b", "linked-base", linked)
	f.selectService("linked", linked, main.GitCommonDirectory, "linked/main")
	f.request.BaseOverrides = []ModuleBaseOverride{{ServiceID: "linked", ModuleID: "default", BaseRef: "linked/main", TargetBranch: "feature/linked"}}
	f.config.Groups[0].Services[count-1].Modules[0].Strategy = domain.WorkspaceIndependentClone
	firstWave := f.request.ServiceIDs[:count]

	var barrier atomic.Pointer[parallelFetchBarrier]
	fileServer := http.FileServer(http.Dir(f.root))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		wave := barrier.Load()
		if wave != nil && path == "remote.git/info/refs" {
			if _, seen := wave.seen.LoadOrStore(id, struct{}{}); !seen {
				wave.arrivals <- id
			}
			select {
			case <-wave.release:
			case <-r.Context().Done():
				return
			}
		}
		request := r.Clone(r.Context())
		request.URL.Path = "/" + path
		fileServer.ServeHTTP(w, request)
	}))
	defer server.Close()
	for _, repository := range f.config.Repositories[:count] {
		gitCompare(t, repository.RootPath, "remote", "set-url", "origin", server.URL+"/"+repository.ID+"/remote.git")
	}
	gitCompare(t, linked, "remote", "add", "linked", server.URL+"/linked/remote.git")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	preflight := newParallelFetchBarrier(count + 1)
	barrier.Store(preflight)
	preview := startParallelTaskRun(func() (domain.TaskManifest, error) {
		conflicts, err := f.app.InspectCreateBranchReuse(ctx, f.config, f.request)
		if err == nil && len(conflicts) != 0 {
			err = fmt.Errorf("unexpected preflight conflicts: %v", conflicts)
		}
		return domain.TaskManifest{}, err
	})
	preflight.waitForRepositories(t, ctx, preview, firstWave)
	close(preflight.release)
	preview.wait(t, ctx)
	if preview.err != nil {
		t.Fatal(preview.err)
	}
	if _, seen := preflight.seen.Load("linked"); !seen {
		t.Fatal("the later checkout sharing the first Git common directory was skipped")
	}

	recheck, provision := newParallelFetchBarrier(count+1), newParallelFetchBarrier(count+1)
	barrier.Store(recheck)
	created := startParallelTaskRun(func() (domain.TaskManifest, error) {
		return f.app.CreateTaskWithProgress(ctx, f.config, f.request, func(event TaskCreationProgress) {
			if event.Phase == TaskCreationPhasePreparingWorktree && event.ServiceID == "" && event.Status == TaskCreationProgressCompleted {
				barrier.Store(provision)
			}
		})
	})
	recheck.waitForRepositories(t, ctx, created, firstWave)
	close(recheck.release)
	provision.waitForRepositories(t, ctx, created, firstWave)
	close(provision.release)
	created.wait(t, ctx)
	if created.err != nil {
		t.Fatal(created.err)
	}
	if _, seen := provision.seen.Load("linked"); !seen {
		t.Fatal("the later checkout sharing the first Git common directory was skipped during creation")
	}
	if got := parallelTaskManifestServices(created.manifest); !slices.Equal(got, f.request.ServiceIDs) {
		t.Fatalf("manifest services = %v, want selected order %v", got, f.request.ServiceIDs)
	}
	saved, err := f.app.Manifests.Load(filepath.Join(f.taskRoot, f.request.FolderName))
	if err != nil {
		t.Fatal(err)
	}
	if got := parallelTaskManifestServices(saved); !slices.Equal(got, f.request.ServiceIDs) {
		t.Fatalf("saved manifest services = %v, want selected order %v", got, f.request.ServiceIDs)
	}
	for index, workspace := range saved.Services {
		strategy := domain.WorkspaceStandardWorktree
		branch := f.request.FeatureBranch
		if index == count-1 {
			strategy = domain.WorkspaceIndependentClone
		} else if index == count {
			branch = "feature/linked"
		}
		if workspace.Strategy != strategy || gitCompareOutput(t, workspace.WorktreePath, "branch", "--show-current") != branch {
			t.Fatalf("unexpected checkout for %s: %+v", workspace.GroupServiceID, workspace)
		}
	}
}

func parallelTaskManifestServices(manifest domain.TaskManifest) []string {
	ids := make([]string, 0, len(manifest.Services))
	for _, workspace := range manifest.Services {
		ids = append(ids, workspace.GroupServiceID)
	}
	return ids
}

type testParallelProvisioner struct {
	*StandardWorktreeProvisioner
	before func(context.Context, WorkspaceProvisionRequest) error
	after  func(context.Context, WorkspaceProvisionRequest)
}

func (p *testParallelProvisioner) ProvisionWithProgress(ctx context.Context, request WorkspaceProvisionRequest, output func(process.StreamEvent), progress func(TaskCreationProgress)) ([]domain.ServiceWorkspace, error) {
	if p.before != nil {
		if err := p.before(ctx, request); err != nil {
			return nil, err
		}
	}
	result, err := p.StandardWorktreeProvisioner.ProvisionWithProgress(ctx, request, output, progress)
	if err == nil && p.after != nil {
		p.after(ctx, request)
	}
	return result, err
}

func parallelTaskWorktree(f *parallelTaskFixture) *StandardWorktreeProvisioner {
	return &StandardWorktreeProvisioner{Git: f.app.Git, Bootstrap: f.app.Bootstrap, Locks: f.app.Locks}
}

func waitForParallelTaskSignal(t *testing.T, ctx context.Context, run *parallelTaskRun, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-run.done:
		t.Fatalf("task ended before %s: %v", operation, run.err)
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", operation, ctx.Err())
	}
}

func TestTaskCreationParallelRollbackWaitsForWorkers(t *testing.T) {
	for _, failure := range []string{"cancellation", "changed branch"} {
		t.Run(failure, func(t *testing.T) {
			f := newParallelTaskFixture(t, 4)
			ids := f.request.ServiceIDs
			first, slow, changed, later := f.config.Repositories[0], f.config.Repositories[1], f.config.Repositories[2], f.config.Repositories[3]
			if failure == "cancellation" {
				f.request.ServiceIDs = []string{ids[0], ids[1], ids[3]}
			}
			services := f.config.Groups[0].Services
			services[3].Bootstrap.CopyRules = []domain.BootstrapCopyRule{{Source: "tracked.txt", Target: "bootstrap.txt"}}
			branch := f.request.FeatureBranch
			gitCompare(t, first.RootPath, "branch", branch)
			old := gitCompareOutput(t, first.RootPath, "rev-parse", branch)
			partialBranch, approvedBranch := branch+"-first", branch+"-changed"
			var changedCommit string
			if failure == "changed branch" {
				firstModule := services[2].Modules[0]
				firstModule.ID, firstModule.Name = "first", "first"
				secondModule := firstModule
				secondModule.ID, secondModule.Name = "changed", "changed"
				services[2].Modules = []domain.ServiceModuleConfig{firstModule, secondModule}
				gitCompare(t, changed.RootPath, "branch", approvedBranch)
				tree := gitCompareOutput(t, changed.RootPath, "rev-parse", "HEAD^{tree}")
				changedCommit = gitCompareOutput(t, changed.RootPath, "-c", "user.name=Task Creation Test", "-c", "user.email=task@example.invalid", "commit-tree", tree, "-p", old, "-m", "branch changed after approval")
			}
			deadline, stop := context.WithTimeout(context.Background(), 90*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(deadline)
			defer cancel()
			conflicts, err := f.app.InspectCreateBranchReuse(ctx, f.config, f.request)
			wantConflicts := 1
			if failure == "changed branch" {
				wantConflicts++
			}
			if err != nil || len(conflicts) != wantConflicts {
				t.Fatalf("unexpected preflight conflicts: %v, %v", conflicts, err)
			}
			for _, conflict := range conflicts {
				f.request.ConfirmedBranchReuseKeys = append(f.request.ConfirmedBranchReuseKeys, conflict.Key)
			}

			firstReady, slowReady, slowCanceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			releaseSlow := make(chan struct{})
			release := sync.OnceFunc(func() { close(releaseSlow) })
			defer release()
			var prematureRollback, laterStarted, slowReturned, partialCreated atomic.Bool
			f.app.Provisioning = NewWorkspaceProvisioningServiceWith([]WorkspaceProvisioner{&testParallelProvisioner{
				StandardWorktreeProvisioner: parallelTaskWorktree(f),
				before: func(ctx context.Context, request WorkspaceProvisionRequest) error {
					switch request.Service.ID {
					case later.ID:
						laterStarted.Store(true)
					case changed.ID:
						if request.Service.Modules[0].ID != "changed" {
							return nil
						}
						// Move the already-confirmed branch after the official
						// recheck and while its sibling is still in provisioning.
						for _, ready := range []<-chan struct{}{firstReady, slowReady} {
							select {
							case <-ready:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						_, err := f.app.Git.Run(changed.RootPath, "update-ref", "refs/heads/"+approvedBranch, changedCommit)
						return err
					}
					return nil
				},
				after: func(ctx context.Context, request WorkspaceProvisionRequest) {
					switch request.Service.ID {
					case first.ID:
						close(firstReady)
					case slow.ID:
						close(slowReady)
						<-ctx.Done()
						close(slowCanceled)
						<-releaseSlow
						slowReturned.Store(true)
					case changed.ID:
						if request.Service.Modules[0].ID == "first" {
							partialCreated.Store(true)
						}
					}
				},
			}})
			run := startParallelTaskRun(func() (domain.TaskManifest, error) {
				return f.app.CreateTaskWithProgress(ctx, f.config, f.request, func(event TaskCreationProgress) {
					if event.Phase == TaskCreationPhaseRollback && event.Status == TaskCreationProgressStarted && !slowReturned.Load() {
						prematureRollback.Store(true)
					}
				})
			})
			waitForParallelTaskSignal(t, deadline, run, firstReady, "creating the reused worktree")
			waitForParallelTaskSignal(t, deadline, run, slowReady, "creating a new worktree")
			if failure == "cancellation" {
				cancel()
			}
			waitForParallelTaskSignal(t, deadline, run, slowCanceled, "canceling in-flight provisioning")
			release()
			run.wait(t, deadline)
			if failure == "cancellation" {
				if !errors.Is(run.err, context.Canceled) {
					t.Fatalf("creation returned %v, want cancellation", run.err)
				}
			} else if run.err == nil || !strings.Contains(run.err.Error(), "分支复用状态已变化") {
				t.Fatalf("creation returned %v, want the branch confirmation error", run.err)
			}
			if prematureRollback.Load() || laterStarted.Load() {
				t.Fatalf("creation rolled back before the in-flight worker finished = %t, started the later service = %t", prematureRollback.Load(), laterStarted.Load())
			}
			taskPath := filepath.Join(f.taskRoot, f.request.FolderName)
			if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
				t.Fatalf("failed task directory survived rollback: %v", err)
			}
			for _, repository := range []domain.RepositoryConfig{first, slow, changed} {
				worktrees, err := f.app.Git.Worktrees(repository.RootPath)
				if err != nil || len(worktrees) != 1 || !samePath(worktrees[0].Path, repository.RootPath) {
					t.Fatalf("repository %s retained a task worktree registration: %v, %v", repository.ID, worktrees, err)
				}
			}
			if got := gitCompareOutput(t, first.RootPath, "rev-parse", branch); got != old {
				t.Fatalf("reused branch moved: %s, want %s", got, old)
			}
			if got := gitCompareOutput(t, slow.RootPath, "for-each-ref", "--format=%(refname)", "refs/heads/"+branch); got != "" {
				t.Fatalf("task-created branch survived rollback: %s", got)
			}
			if changedCommit != "" {
				if !partialCreated.Load() {
					t.Fatal("the failed service did not create its first module")
				}
				if got := gitCompareOutput(t, changed.RootPath, "for-each-ref", "--format=%(refname)", "refs/heads/"+partialBranch); got != "" {
					t.Fatalf("the failed service's first module retained its branch: %s", got)
				}
				if gitCompareOutput(t, changed.RootPath, "rev-parse", approvedBranch) != changedCommit {
					t.Fatal("rollback reverted an external update to a pre-existing branch")
				}
			}
		})
	}
}

func TestTaskCreationParallelBootstrapBoundaries(t *testing.T) {
	f := newParallelTaskFixture(t, 6)
	ids := f.request.ServiceIDs
	services := f.config.Groups[0].Services
	command := domain.BootstrapCommand{Name: "Print Git version", Executable: "git", Arguments: []string{"--version"}, WorkingDirectory: ".", TimeoutSeconds: 30}
	services[1].Bootstrap.Commands = []domain.BootstrapCommand{command}
	services[2].Bootstrap.CopyRules = []domain.BootstrapCopyRule{{Source: "tracked.txt", Target: "bootstrap.txt"}}
	command.Enabled = true
	services[3].Bootstrap.Commands = []domain.BootstrapCommand{command}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	started, finished := make(chan string, len(ids)), make(chan string, len(ids))
	permissions := make(map[string]chan struct{}, len(ids))
	for _, id := range ids {
		permissions[id] = make(chan struct{})
	}
	var completed sync.Map
	dependsOn := map[string][]string{ids[2]: ids[:2], ids[3]: ids[:3], ids[4]: ids[:4], ids[5]: ids[:4]}
	f.app.Provisioning = NewWorkspaceProvisioningServiceWith([]WorkspaceProvisioner{&testParallelProvisioner{
		StandardWorktreeProvisioner: parallelTaskWorktree(f),
		before: func(ctx context.Context, request WorkspaceProvisionRequest) error {
			id := request.Service.ID
			for _, prior := range dependsOn[id] {
				if _, ok := completed.Load(prior); !ok {
					return fmt.Errorf("%s started before bootstrap barrier service %s completed", id, prior)
				}
			}
			started <- id
			select {
			case <-permissions[id]:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		after: func(_ context.Context, request WorkspaceProvisionRequest) {
			completed.Store(request.Service.ID, struct{}{})
			finished <- request.Service.ID
		},
	}})
	run := startParallelTaskRun(func() (domain.TaskManifest, error) {
		return f.app.CreateTask(ctx, f.config, f.request)
	})
	wait := func(events <-chan string) string {
		t.Helper()
		select {
		case id := <-events:
			return id
		case <-run.done:
			select {
			case id := <-events:
				return id
			default:
				t.Fatalf("task run ended before all provisioning signals: %v", run.err)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for a provisioning signal: %v", ctx.Err())
		}
		return ""
	}
	for _, wave := range [][]string{ids[:2], ids[2:3], ids[3:4], ids[4:]} {
		arrivals := make([]string, 0, len(wave))
		for range wave {
			arrivals = append(arrivals, wait(started))
		}
		slices.Sort(arrivals)
		if !slices.Equal(arrivals, wave) {
			t.Fatalf("provisioning started for %v, want %v", arrivals, wave)
		}
		for _, id := range slices.Backward(wave) {
			close(permissions[id])
			if got := wait(finished); got != id {
				t.Fatalf("%s finished provisioning before released service %s", got, id)
			}
		}
	}
	run.wait(t, ctx)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := parallelTaskManifestServices(run.manifest); !slices.Equal(got, ids) {
		t.Fatalf("manifest uses completion order: %v, want selected order %v", got, ids)
	}
	copy := run.manifest.Services[2]
	if data, err := os.ReadFile(filepath.Join(copy.WorktreePath, "bootstrap.txt")); err != nil || strings.TrimSpace(string(data)) != "base" {
		t.Fatalf("bootstrap copy missing: %q, %v", data, err)
	}
	if configured := run.manifest.Services[3]; len(configured.Warnings) != 0 {
		t.Fatalf("configured bootstrap command failed: %v", configured.Warnings)
	}
}
