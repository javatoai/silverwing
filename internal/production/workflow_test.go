package production

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type fakeGenbu struct {
	versions    []string
	versionErr  error
	calls       int
	status      domain.GenbuStageStatus
	statusCalls int
	onStatus    func()
}

func (g *fakeGenbu) Versions(context.Context, domain.AppConfig, string) ([]string, error) {
	g.calls++
	return append([]string{}, g.versions...), g.versionErr
}
func (g *fakeGenbu) Status(context.Context, domain.AppConfig, string, string) (domain.GenbuTagProbeStatus, error) {
	g.statusCalls++
	if g.onStatus != nil {
		g.onStatus()
	}
	return domain.GenbuTagProbeStatus{Build: g.status, Production: domain.GenbuSuccess}, nil
}

func TestProductionConfigChangesRejectBeforeBatchMutation(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/one", "one.txt", "one\n", f.base)
	b = f.sync(f.merge(b, "feature/one", sha))
	b.Tags = []Tag{{Name: "2.3.44", Commit: b.ReleaseSHA, Status: unknownStatus()}}
	original, err := f.s.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		change    func(*domain.AppConfig)
		operation func() error
	}{
		{"merge-service-change", func(c *domain.AppConfig) { c.Groups[0].Services[0].GenbuServiceName = "changed" }, func() error {
			_, e := f.s.MergeFeature(context.Background(), b.ID, "feature/one", sha, b.ReleaseSHA)
			return e
		}},
		{"publish-main-change", func(c *domain.AppConfig) { c.Groups[0].Services[0].MasterBranch = "origin/changed" }, func() error { _, e := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token); return e }},
		{"adopt-disabled-service", func(c *domain.AppConfig) { c.Groups[0].Services[0].Enabled = false }, func() error { _, e := f.s.AcceptRemote(context.Background(), b.ID, b.ReleaseSHA); return e }},
		{"probe-disabled-tag", func(c *domain.AppConfig) { c.TagEnabled = false }, func() error { _, e := f.s.RefreshBuild(context.Background(), b.ID); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.s.Config.Save(original); err != nil {
				t.Fatal(err)
			}
			c, err := f.s.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.Store.Save(&b); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.s.Store.Directory, b.ID+".json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&c)
			if err := f.s.Config.Save(c); err != nil {
				t.Fatal(err)
			}
			if err := tc.operation(); err == nil {
				t.Error("changed target accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Error("stale operation mutated the batch")
			}
		})
	}
}

func TestProductionBuildProbeRejectsLateConfigChange(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	b.Tags = []Tag{{Name: "2.3.44", Commit: b.ReleaseSHA, Status: unknownStatus()}}
	original, err := f.s.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	f.g.onStatus = func() {
		c, err := f.s.Config.Load()
		if err != nil {
			t.Fatal(err)
		}
		c.Groups[0].Services[0].GenbuServiceName = "changed-during-probe"
		if err := f.s.Config.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending-%t", pending), func(t *testing.T) {
			if err := f.s.Config.Save(original); err != nil {
				t.Fatal(err)
			}
			b.Intent, b.Phase = nil, "READY"
			if pending {
				b.Intent, b.Phase = &Intent{Kind: "sync", Step: "main"}, "SYNC_MAIN"
			}
			if err := f.s.Store.Save(&b); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.s.Store.Directory, b.ID+".json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.RefreshBuild(context.Background(), b.ID); err == nil {
				t.Error("late status accepted after config change")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !pending && string(before) != string(after) {
				t.Error("late status changed saved batch")
			}
			stored, err := f.s.Store.Load(b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if digest(stored.Tags) != digest(b.Tags) {
				t.Error("late status was saved with pending operation")
			}
		})
	}
}

func TestProductionChangedMixedRuntimeInvalidatesManualConfirmation(t *testing.T) {
	f := newFixture(t)
	f.g.versions = []string{"2.3.43", "2.3.42"}
	p, err := f.s.Inspect(context.Background(), f.selection, "2.3.43")
	if err != nil {
		t.Fatal(err)
	}
	f.g.versions = []string{"2.3.43", "2.3.41"}
	if _, err := f.s.Create(context.Background(), p, "2.3.44", ""); err == nil {
		t.Fatal("changed mixed runtime accepted stale confirmation")
	}
	if batches, err := f.s.Store.List(); err != nil || len(batches) != 0 {
		t.Fatalf("stale confirmation created batch: %d / %v", len(batches), err)
	}
	p, err = f.s.Inspect(context.Background(), f.selection, "2.3.43")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.s.Create(context.Background(), p, "2.3.44", "")
	if err != nil {
		t.Fatal(err)
	}
	sha := f.feature("feature/manual", "manual.txt", "manual\n", f.base)
	b = f.merge(b, "feature/manual", sha)
	_, p, err = f.s.InspectBatch(context.Background(), b.ID, "2.3.43")
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.s.CheckForTag(context.Background(), b.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	f.g.versions = []string{"2.3.43", "2.3.40"}
	if _, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token); err == nil {
		t.Fatal("changed mixed runtime bypassed final Tag gate")
	}
	if refs, err := f.s.remoteRefs(context.Background(), b.Target); err != nil || refs["refs/tags/2.3.44"] != "" {
		t.Fatal("stale mixed runtime pushed Tag")
	}
}

type fixture struct {
	t                              *testing.T
	root, remote, repo, seed, base string
	s                              *Service
	g                              *fakeGenbu
	git                            *workspace.GitClient
	selection                      Selection
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	empty := filepath.Join(root, "empty-config")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "Synthetic")
	t.Setenv("GIT_AUTHOR_EMAIL", "synthetic@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Synthetic")
	t.Setenv("GIT_COMMITTER_EMAIL", "synthetic@example.invalid")
	f := &fixture{t: t, root: root, remote: filepath.Join(root, "remote.git"), repo: filepath.Join(root, "repo space"), seed: filepath.Join(root, "seed"), git: workspace.NewGitClient(nil, "git"), selection: Selection{domain.DefaultGroupID, "service"}}
	f.run(root, "init", "--bare", "--initial-branch=master", f.remote)
	f.run(root, "init", "--initial-branch=master", f.seed)
	f.write(f.seed, "base.txt", "base\n")
	f.run(f.seed, "add", ".")
	f.run(f.seed, "commit", "-m", "base")
	f.base = f.run(f.seed, "rev-parse", "HEAD")
	f.run(f.seed, "remote", "add", "origin", f.remote)
	f.run(f.seed, "tag", "-a", "2.3.43", "-m", "production")
	f.run(f.seed, "push", "origin", "master", "--tags")
	f.run(root, "clone", f.remote, f.repo)
	paths := storage.NewPaths(filepath.Join(root, "app home"))
	store := storage.NewConfigStore(paths)
	c := domain.DefaultAppConfig()
	tasks := filepath.Join(root, "tasks")
	c.TaskRoot = &tasks
	c.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "synthetic", RootPath: f.repo, GitCommonDirectory: filepath.Join(f.repo, ".git")}}
	c.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "synthetic-service", Enabled: true, MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", GenbuProbeEnabled: true, GenbuServiceName: "synthetic", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}}}}
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	f.s = New(paths, store, f.git, process.NewRunner())
	f.g = &fakeGenbu{versions: []string{"2.3.43"}, status: domain.GenbuBuilding}
	f.s.Genbu = f.g
	return f
}

func (f *fixture) run(dir string, args ...string) string {
	f.t.Helper()
	r, err := f.git.RunContext(context.Background(), dir, time.Minute, true, false, args...)
	if err != nil {
		f.t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(r.Stdout)
}
func (f *fixture) write(dir, name, text string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) feature(branch, name, text, base string) string {
	f.t.Helper()
	f.run(f.seed, "checkout", "-B", branch, base)
	f.write(f.seed, name, text)
	f.run(f.seed, "add", name)
	f.run(f.seed, "commit", "-m", branch)
	f.run(f.seed, "push", "origin", branch)
	return f.run(f.seed, "rev-parse", "HEAD")
}
func (f *fixture) create() Batch {
	f.t.Helper()
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		f.t.Fatal(err)
	}
	b, err := f.s.Create(context.Background(), p, "2.3.44", "")
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}
func (f *fixture) merge(b Batch, branch, sha string) Batch {
	f.t.Helper()
	b, err := f.s.MergeFeature(context.Background(), b.ID, branch, sha, b.ReleaseSHA)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

// The fixture deliberately clicks the three independent operations in order.
// Production checks themselves must never perform these prerequisite merges.
func (f *fixture) sync(b Batch) Batch {
	f.t.Helper()
	ctx := context.Background()
	_, p, err := f.s.InspectBatch(ctx, b.ID, "")
	if err != nil {
		f.t.Fatal(err)
	}
	if !p.ProductionInMain {
		b, err = f.s.MergeProduction(ctx, b.ID, p)
		if err != nil {
			f.t.Fatal(err)
		}
		_, p, err = f.s.InspectBatch(ctx, b.ID, "")
		if err != nil {
			f.t.Fatal(err)
		}
	}
	if !p.MainInRelease {
		b, err = f.s.MergeMainToRelease(ctx, b.ID, p)
		if err != nil {
			f.t.Fatal(err)
		}
		_, p, err = f.s.InspectBatch(ctx, b.ID, "")
		if err != nil {
			f.t.Fatal(err)
		}
	}
	b, err = f.s.CheckForTag(ctx, b.ID, p)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fixture) publish(b Batch) Batch {
	f.t.Helper()
	if b.TagPlan == nil {
		f.t.Fatal("missing Tag plan")
	}
	b, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestProductionEveryTagRefreshesAndMergesRuntimeBaseline(t *testing.T) {
	f := newFixture(t)
	// master has extra code; initialization must retain it.
	f.feature("master", "master-only.txt", "extra\n", f.base)
	b := f.create()
	sha := f.feature("feature/one", "feature-one.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", sha)
	parents := strings.Fields(f.run(f.repo, "rev-list", "--parents", "-n", "1", b.ReleaseSHA))
	if len(parents) != 3 {
		t.Fatalf("feature merge lost --no-ff: %v", parents)
	}
	b = f.publish(f.sync(b))
	if len(b.Tags) != 1 || b.Tags[0].Name != "2.3.44" {
		t.Fatalf("first Tag: %+v", b.Tags)
	}
	// A new online hotfix is not reachable from master or the release.
	fix := f.feature("hotfix/online", "online-fix.txt", "fix\n", f.base)
	f.run(f.seed, "tag", "-a", "2.3.43.1", fix, "-m", "online fix")
	f.run(f.seed, "push", "origin", "2.3.43.1")
	f.g.versions = []string{"2.3.43.1"}
	sha = f.feature("feature/two", "feature-two.txt", "two\n", f.base)
	b = f.merge(b, "feature/two", sha)
	beforeCalls := f.g.calls
	b = f.sync(b)
	if f.g.calls <= beforeCalls {
		t.Fatal("per-Tag runtime query was skipped")
	}
	for _, head := range []string{b.MainSHA, b.ReleaseSHA} {
		ok, err := f.s.ancestor(context.Background(), b.Target, fix, head)
		if err != nil || !ok {
			t.Fatalf("online fix missing from %s: %v", head, err)
		}
	}
	b = f.publish(b)
	if b.Tags[1].Name != "2.3.44.1" {
		t.Fatalf("append Tag: %+v", b.Tags)
	}
	if got := f.run(f.remote, "cat-file", "-t", "refs/tags/2.3.44.1"); got != "tag" {
		t.Fatalf("not annotated: %s", got)
	}
	if got := f.run(f.repo, "rev-parse", "HEAD"); got != f.base {
		t.Fatal("user checkout was modified")
	}
	f.run(f.remote, "cat-file", "-e", "refs/heads/master:master-only.txt")
	unchanged := f.sync(b)
	if unchanged.TagPlan == nil || unchanged.TagPlan.Tag != "2.3.44.2" || len(unchanged.Tags) != 2 {
		t.Fatal("unchanged release must prepare the next Tag without publishing it")
	}
}

func TestProductionConflictSurvivesRestartAndRequiresCommittedResolution(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	one := f.feature("feature/one", "base.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", one)
	two := f.feature("feature/two", "base.txt", "two\n", f.base)
	paused, err := f.s.MergeFeature(context.Background(), b.ID, "feature/two", two, b.ReleaseSHA)
	if err == nil || paused.Phase != "CONFLICT" || paused.Intent.Merge.Worktree == "" {
		t.Fatalf("conflict was not retained: %+v / %v", paused, err)
	}
	worktree := paused.Intent.Merge.Worktree
	f.s = New(f.s.Paths, f.s.Config, f.git, process.NewRunner())
	f.s.Genbu = f.g
	f.write(worktree, "base.txt", "resolved\n")
	f.run(worktree, "add", "base.txt")
	if _, err := f.s.Resume(context.Background(), b.ID); err == nil {
		t.Fatal("staged but uncommitted resolution accepted")
	}
	f.run(worktree, "commit", "-m", "resolve synthetic conflict")
	completed, err := f.s.Resume(context.Background(), b.ID)
	if err != nil || completed.Intent != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if len(completed.Branches) != 2 {
		t.Fatalf("merge duplicated or lost: %+v", completed.Branches)
	}
	if f.run(f.remote, "rev-parse", "refs/heads/"+b.ReleaseBranch) != completed.ReleaseSHA {
		t.Fatal("resolution not pushed")
	}
}

func TestProductionRejectsLateProductionAndConfigChanges(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/one", "one.txt", "one\n", f.base)
	b = f.sync(f.merge(b, "feature/one", sha))
	fix := f.feature("hotfix/new", "fix.txt", "fix\n", f.base)
	f.run(f.seed, "tag", "2.3.43.1", fix)
	f.run(f.seed, "push", "origin", "2.3.43.1")
	f.g.versions = []string{"2.3.43.1"}
	if _, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token); err == nil {
		t.Fatal("late production deployment bypassed mandatory gate")
	}
	c, err := f.s.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c.Groups[0].Services[0].GenbuServiceName = "another-service"
	if err := f.s.Config.Save(c); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CheckForTag(context.Background(), b.ID, Preview{}); err == nil {
		t.Fatal("changed configuration accepted")
	}
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/tags/2.3.44"] != "" {
		t.Fatal("unexpected Tag after stale confirmation")
	}
}

func TestProductionConflictUserAlreadyPushedIsRecognized(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	one := f.feature("feature/one", "base.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", one)
	two := f.feature("feature/two", "base.txt", "two\n", f.base)
	b, err := f.s.MergeFeature(context.Background(), b.ID, "feature/two", two, b.ReleaseSHA)
	if err == nil || b.Intent == nil {
		t.Fatal("expected conflict")
	}
	wt := b.Intent.Merge.Worktree
	f.write(wt, "base.txt", "resolved\n")
	f.run(wt, "add", "base.txt")
	f.run(wt, "commit", "-m", "user resolution")
	f.run(wt, "push", "origin", "HEAD:refs/heads/"+b.ReleaseBranch)
	b, err = f.s.Resume(context.Background(), b.ID)
	if err != nil || b.Intent != nil || len(b.Branches) != 2 {
		t.Fatalf("already-pushed resolution not recognized: %v", err)
	}
}

type afterPushFailure struct {
	Git
	fired bool
}

type beforePushHook struct {
	Git
	hook func([]string) error
}

func (g *beforePushHook) RunContext(ctx context.Context, path string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	if len(args) > 0 && args[0] == "push" && g.hook != nil {
		if err := g.hook(args); err != nil {
			return process.Result{}, err
		}
	}
	return g.Git.RunContext(ctx, path, timeout, check, readOnly, args...)
}

func TestProductionBranchPushRejectsLateRollbackAndCreation(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprintf("create-%t", create), func(t *testing.T) {
			f := newFixture(t)
			f.feature("master", "extra.txt", "keep main history\n", f.base)
			var b Batch
			if !create {
				b = f.create()
			}
			branch, sha := "feature/race", ""
			if !create {
				sha = f.feature(branch, "race.txt", "feature\n", f.base)
			}
			fired := false
			f.s.Git = &beforePushHook{Git: f.git, hook: func(args []string) error {
				if !fired {
					fired = true
					ref := strings.SplitN(args[len(args)-1], ":", 2)[1]
					f.run(f.remote, "update-ref", ref, f.base)
				}
				return nil
			}}
			var err error
			if create {
				p, e := f.s.Inspect(context.Background(), f.selection, "")
				if e != nil {
					t.Fatal(e)
				}
				b, err = f.s.Create(context.Background(), p, "2.3.44", "")
			} else {
				b, err = f.s.MergeFeature(context.Background(), b.ID, branch, sha, b.ReleaseSHA)
			}
			if !fired || err == nil {
				t.Fatal("late remote change silently accepted")
			}
			if actual := f.run(f.remote, "rev-parse", "refs/heads/"+b.ReleaseBranch); actual != f.base {
				t.Fatal("push advanced a changed target")
			}
		})
	}
}

func TestProductionBranchGuardNeverRewritesHistory(t *testing.T) {
	f := newFixture(t)
	f.feature("master", "keep.txt", "must retain this commit\n", f.base)
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.pushBranch(context.Background(), p.Target, "master", p.MainSHA, f.base); err == nil {
		t.Fatal("atomic ref guard authorized a history rewrite")
	}
	if actual := f.run(f.remote, "rev-parse", "refs/heads/master"); actual != p.MainSHA {
		t.Fatal("main history was rewritten")
	}
}

func TestProductionResumeRechecksRuntimeBeforeSyncWrites(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	fix := f.feature("hotfix/new", "fix.txt", "fix\n", f.base)
	f.run(f.seed, "tag", "2.3.43.1", fix)
	f.run(f.seed, "push", "origin", "2.3.43.1")
	f.g.versions = []string{"2.3.43.1"}
	_, p, err := f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.s.Git = &beforePushHook{Git: f.git, hook: func([]string) error { return errors.New("synthetic interruption before push") }}
	b, err = f.s.MergeProduction(context.Background(), b.ID, p)
	if err == nil || b.Intent == nil || b.Intent.Merge == nil {
		t.Fatal("missing paused synchronization")
	}
	oldWorktree := b.Intent.Merge.Worktree
	f.s.Git = f.git
	f.g.versions = []string{"2.3.43"}
	if _, err = f.s.Resume(context.Background(), b.ID); err == nil {
		t.Fatal("resumed an outdated production observation")
	}
	if actual := f.run(f.remote, "rev-parse", "refs/heads/master"); actual != f.base {
		t.Fatal("old baseline pushed before revalidation")
	}
	_, p, err = f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.s.MergeProduction(context.Background(), b.ID, p)
	if err != nil {
		t.Fatalf("fresh explicit sync could not replace stale intent: %v", err)
	}
	if len(b.RetainedWorktrees) == 0 || b.RetainedWorktrees[0] != oldWorktree {
		t.Fatal("reconfirmed sync lost former worktree")
	}
}

func (g *afterPushFailure) RunContext(ctx context.Context, path string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	r, err := g.Git.RunContext(ctx, path, timeout, check, readOnly, args...)
	if err == nil && !g.fired && len(args) > 0 && args[0] == "push" {
		g.fired = true
		return r, errors.New("synthetic connection lost after server accepted push")
	}
	return r, err
}

func TestProductionEndedBatchReconcilesWithoutRemoteWrites(t *testing.T) {
	for _, tag := range []bool{false, true} {
		t.Run(fmt.Sprintf("tag-%t", tag), func(t *testing.T) {
			f := newFixture(t)
			b := f.create()
			sha := f.feature("feature/one", "one.txt", "one\n", f.base)
			if tag {
				b = f.sync(f.merge(b, "feature/one", sha))
			}
			f.s.Git = &afterPushFailure{Git: f.git}
			var err error
			if tag {
				b, err = f.s.Publish(context.Background(), b.ID, b.TagPlan.Token)
			} else {
				b, err = f.s.MergeFeature(context.Background(), b.ID, "feature/one", sha, b.ReleaseSHA)
			}
			if err == nil || b.Intent == nil {
				t.Fatal("missing ambiguous push")
			}
			worktree := ""
			if b.Intent.Merge != nil {
				worktree = b.Intent.Merge.Worktree
			}
			f.s.Git = &beforePushHook{Git: f.git, hook: func([]string) error {
				t.Error("ended reconciliation attempted a push")
				return errors.New("forbidden push")
			}}
			b, err = f.s.End(context.Background(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.s.remoteRefs(context.Background(), b.Target)
			if err != nil {
				t.Fatal(err)
			}
			b, err = f.s.ReconcileEnded(context.Background(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !b.Ended || b.Phase != "ENDED" {
				t.Fatal("reconciliation resumed an ended batch")
			}
			if tag {
				if len(b.Tags) != 1 || b.Intent != nil {
					t.Fatal("accepted Tag missing from ended history")
				}
				b, err = f.s.RefreshBuild(context.Background(), b.ID)
				if err != nil || b.Tags[0].Status.Build != f.g.status {
					t.Fatalf("ended Tag status: %v", err)
				}
			} else {
				if b.Intent == nil || !b.Intent.ResultConfirmed || len(b.Branches) != 1 {
					t.Fatal("accepted merge missing from ended history")
				}
				path, _, err := f.s.ConflictWorkspace(context.Background(), b.ID)
				if err != nil || path != worktree {
					t.Fatalf("retained worktree cannot be accessed: %v", err)
				}
				if _, err := os.Stat(worktree); err != nil {
					t.Fatal("ended reconciliation removed worktree")
				}
			}
			b, err = f.s.ReconcileEnded(context.Background(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (tag && len(b.Tags) != 1) || (!tag && len(b.Branches) != 1) {
				t.Fatal("reconciliation duplicated history")
			}
			after, err := f.s.remoteRefs(context.Background(), b.Target)
			if err != nil || digest(before) != digest(after) {
				t.Fatal("ended reconciliation changed remote refs")
			}
			if _, err := f.s.Resume(context.Background(), b.ID); err == nil {
				t.Fatal("ended batch resumed writes")
			}
		})
	}
}

func TestProductionEndedUnpublishedTagStaysVisibleAndEnded(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/one", "one.txt", "one\n", f.base)
	b = f.sync(f.merge(b, "feature/one", sha))
	f.s.Git = &beforePushHook{Git: f.git, hook: func([]string) error { return errors.New("synthetic interruption before Tag push") }}
	b, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token)
	if err == nil || b.Intent == nil {
		t.Fatal("missing interrupted Tag")
	}
	f.s.Git = f.git
	b, err = f.s.End(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ReconcileEnded(context.Background(), b.ID); err == nil {
		t.Fatal("unpublished Tag was confirmed")
	}
	b, err = f.s.Store.Load(b.ID)
	if err != nil || !b.Ended || b.Phase != "ENDED" || b.Intent == nil || len(b.Tags) != 0 {
		t.Fatal("failed ended reconciliation lost pending history")
	}
	c, err := f.s.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c.Groups[0].Services = nil
	if err := f.s.Config.Save(c); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.End(context.Background(), b.ID); err != nil {
		t.Fatal("orphan batch could not remain ended")
	}
}

func TestProductionResumeVerifiesAmbiguousSuccessfulPush(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/one", "one.txt", "one\n", f.base)
	f.s.Git = &afterPushFailure{Git: f.git}
	b, err := f.s.MergeFeature(context.Background(), b.ID, "feature/one", sha, b.ReleaseSHA)
	if err == nil || b.Intent == nil {
		t.Fatal("expected interrupted push")
	}
	accepted := f.run(f.remote, "rev-parse", "refs/heads/"+b.ReleaseBranch)
	f.s.Git = f.git
	b, err = f.s.Resume(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.ReleaseSHA != accepted || len(b.Branches) != 1 {
		t.Fatal("resume repeated the merge")
	}
}

func TestProductionSingleActiveBatchAndFirstTagWithoutFeature(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Create(context.Background(), p, "2.3.45", ""); err == nil {
		t.Fatal("second active batch allowed")
	}
	b = f.sync(b)
	if b.TagPlan == nil || b.TagPlan.Tag != "2.3.44" || len(b.Tags) != 0 {
		t.Fatal("first Tag should be prepared without a feature, but still await confirmation")
	}
	if _, err := f.s.End(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Create(context.Background(), p, "2.3.45", ""); err != nil {
		t.Fatal(err)
	}
}

func TestProductionMixedPodsRequireExplicitBaseline(t *testing.T) {
	f := newFixture(t)
	f.g.versions = []string{"2.3.43", "2.3.42"}
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil || p.ProductionTag != "" {
		t.Fatalf("mixed versions auto-selected: %+v / %v", p, err)
	}
	p, err = f.s.Inspect(context.Background(), f.selection, "2.3.43")
	if err != nil || !p.Manual {
		t.Fatalf("manual baseline: %v", err)
	}
	// The agreed manual fallback may select another existing Git Tag when Pod
	// versions are mixed or unavailable; it is explicitly shown as manual.
	f.run(f.seed, "tag", "2.3.40", f.base)
	f.run(f.seed, "push", "origin", "2.3.40")
	p, err = f.s.Inspect(context.Background(), f.selection, "2.3.40")
	if err != nil || !p.Manual || !strings.Contains(p.Warning, "人工指定") {
		t.Fatalf("explicit mixed-version fallback rejected: %v", err)
	}
	f.g.versionErr = errors.New("synthetic authentication failure")
	p, err = f.s.Inspect(context.Background(), f.selection, "")
	if err != nil || !strings.Contains(p.Warning, "authentication") || p.ProductionTag != "" {
		t.Fatal("runtime query failure treated as valid baseline")
	}
}

func TestProductionVersionPolicy(t *testing.T) {
	tags := []string{"2.3.9", "2.3.10", "2.3.43.2", "9.0.0.beta-9", "release", "2.3.44-rc1"}
	if got, err := SuggestedVersion(tags); got != "2.3.44" || err != nil {
		t.Fatalf("suggestion %s %v", got, err)
	}
	if err := ValidateVersion("2.4.0", tags); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"2.3.43", "2.3.44.1", "v2.4.0", "2.04.0", "2.4"} {
		if ValidateVersion(bad, tags) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if got, err := NextTag("2.3.44", append(tags, "2.3.44", "2.3.44.9", "2.3.44.10"), false); got != "2.3.44.11" || err != nil {
		t.Fatalf("append %s %v", got, err)
	}
	if _, err := NextTag("2.3.44", []string{"2.3.44"}, true); err == nil {
		t.Fatal("occupied first Tag accepted")
	}
}

func TestProductionMainConflictAndReleaseSyncConflictKeepTheirStages(t *testing.T) {
	t.Run("main", func(t *testing.T) {
		f := newFixture(t)
		f.feature("master", "base.txt", "master change\n", f.base)
		fix := f.feature("hotfix/online", "base.txt", "online change\n", f.base)
		f.run(f.seed, "tag", "2.3.43.1", fix)
		f.run(f.seed, "push", "origin", "2.3.43.1")
		f.g.versions = []string{"2.3.43.1"}
		p, err := f.s.Inspect(context.Background(), f.selection, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := f.s.MergeProduction(context.Background(), "", p)
		if err == nil || b.Intent == nil || b.Intent.Step != "main" || b.Phase != "CONFLICT" {
			t.Fatalf("main conflict: %v / %+v", err, b)
		}
		wt := b.Intent.Merge.Worktree
		f.write(wt, "base.txt", "resolved main and production\n")
		f.run(wt, "add", "base.txt")
		f.run(wt, "commit", "-m", "resolve main")
		b, err = f.s.Resume(context.Background(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if b.Intent != nil || b.InitialSHA != "" || b.MainSHA == "" {
			t.Fatal("main conflict recovery crossed the release-creation boundary")
		}
		if f.run(f.remote, "rev-parse", "refs/heads/master") != b.MainSHA {
			t.Fatal("release not based on synchronized main")
		}
	})
	t.Run("release", func(t *testing.T) {
		f := newFixture(t)
		b := f.create()
		sha := f.feature("feature/one", "base.txt", "feature change\n", f.base)
		b = f.publish(f.sync(f.merge(b, "feature/one", sha)))
		fix := f.feature("hotfix/online", "base.txt", "online change\n", f.base)
		f.run(f.seed, "tag", "2.3.43.1", fix)
		f.run(f.seed, "push", "origin", "2.3.43.1")
		f.g.versions = []string{"2.3.43.1"}
		_, p, err := f.s.InspectBatch(context.Background(), b.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err = f.s.MergeProduction(context.Background(), b.ID, p)
		if err != nil {
			t.Fatal(err)
		}
		_, p, err = f.s.InspectBatch(context.Background(), b.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err = f.s.MergeMainToRelease(context.Background(), b.ID, p)
		if err == nil || b.Intent == nil || b.Intent.Step != "release-sync" {
			t.Fatalf("release sync conflict: %v", err)
		}
		if f.run(f.remote, "rev-parse", "refs/heads/master") != fix {
			t.Fatal("main success checkpoint was lost")
		}
		wt := b.Intent.Merge.Worktree
		f.write(wt, "base.txt", "resolved release and production\n")
		f.run(wt, "add", "base.txt")
		f.run(wt, "commit", "-m", "resolve release")
		b, err = f.s.Resume(context.Background(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if b.TagPlan != nil || b.Intent != nil {
			t.Fatal("release conflict recovery continued to Tag preparation")
		}
		b = f.sync(b)
		if b.TagPlan == nil || b.TagPlan.Tag != "2.3.44.1" {
			t.Fatal("explicit recheck did not prepare next Tag")
		}
	})
}

func TestProductionInterruptedTagPushAndWriteFailureAreIdempotent(t *testing.T) {
	f := newFixture(t)
	b := f.sync(f.create())
	f.s.Store.writeAtomic = func(string, []byte, os.FileMode) error { return errors.New("synthetic disk write failure") }
	if _, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token); err == nil {
		t.Fatal("cache failure ignored")
	}
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/tags/2.3.44"] != "" {
		t.Fatal("Tag pushed before its intent was durable")
	}
	f.s.Store.writeAtomic = nil
	f.s.Git = &afterPushFailure{Git: f.git}
	paused, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token)
	if err == nil || paused.Intent == nil {
		t.Fatal("expected ambiguous Tag push")
	}
	f.s.Git = f.git
	done, err := f.s.Resume(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Tags) != 1 || done.Intent != nil {
		t.Fatal("Tag push was repeated or lost")
	}
	if _, err = f.s.Publish(context.Background(), b.ID, b.TagPlan.Token); err == nil {
		t.Fatal("stale Tag confirmation reused")
	}
	f.g.status = domain.GenbuFailed
	done, err = f.s.RefreshBuild(context.Background(), b.ID)
	if err != nil || done.Tags[0].Status.Build != domain.GenbuFailed {
		t.Fatalf("failed build status %v", err)
	}
	done = f.sync(done)
	if done.TagPlan == nil || done.TagPlan.Tag != "2.3.44.1" || len(done.Tags) != 1 {
		t.Fatal("explicit check should prepare, but not publish, the next unchanged Tag")
	}
	f.g.status = domain.GenbuSuccess
	done, err = f.s.RefreshBuild(context.Background(), b.ID)
	if err != nil || len(done.Tags) != 1 || done.Tags[0].Status.Build != domain.GenbuSuccess {
		t.Fatal("platform rebuild did not refresh original Tag")
	}
}

func TestProductionRemoteAdvanceNeedsExplicitConfirmation(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	one := f.feature("feature/one", "base.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", one)
	two := f.feature("feature/two", "base.txt", "two\n", f.base)
	b, err := f.s.MergeFeature(context.Background(), b.ID, "feature/two", two, b.ReleaseSHA)
	if err == nil {
		t.Fatal("expected conflict")
	}
	oldWorktree := b.Intent.Merge.Worktree
	f.run(f.seed, "fetch", "origin", b.ReleaseBranch)
	f.feature(b.ReleaseBranch, "external.txt", "external change\n", "FETCH_HEAD")
	if _, err = f.s.Resume(context.Background(), b.ID); err == nil {
		t.Fatal("remote advance silently accepted")
	}
	_, p, err := f.s.PendingPreview(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Commits, b.ReleaseBranch) {
		t.Fatal("missing remote change preview")
	}
	b, err = f.s.RebasePending(context.Background(), b.ID, p.MainSHA)
	if err == nil || b.Phase != "CONFLICT" || b.Intent.Merge.Worktree == oldWorktree {
		t.Fatal("new merge did not preserve old conflict worktree")
	}
	if _, err = os.Stat(oldWorktree); err != nil {
		t.Fatal("old user worktree was removed")
	}
}

type cancelBeforePush struct {
	Git
	cancel context.CancelFunc
	fired  bool
}

func (g *cancelBeforePush) RunContext(ctx context.Context, path string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	if !g.fired && len(args) > 0 && args[0] == "push" {
		g.fired = true
		g.cancel()
		return process.Result{}, ctx.Err()
	}
	return g.Git.RunContext(ctx, path, timeout, check, readOnly, args...)
}

func TestProductionCancelledMergeResumesWithoutNewCommit(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/one", "one.txt", "one\n", f.base)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.s.Git = &cancelBeforePush{Git: f.git, cancel: cancel}
	b, err := f.s.MergeFeature(ctx, b.ID, "feature/one", sha, b.ReleaseSHA)
	if err == nil || b.Intent == nil || b.Intent.Merge.AfterSHA == "" {
		t.Fatal("cancelled operation lost its prepared merge")
	}
	after := b.Intent.Merge.AfterSHA
	f.s.Git = f.git
	b, err = f.s.Resume(context.Background(), b.ID)
	if err != nil || b.ReleaseSHA != after || len(b.Branches) != 1 {
		t.Fatalf("cancel resume: %v", err)
	}
}

func TestProductionPushScopeExcludesOtherTagsAndRejectsOtherPushRepository(t *testing.T) {
	f := newFixture(t)
	f.run(f.repo, "config", "push.followTags", "true")
	f.run(f.repo, "tag", "-a", "9.9.9", "-m", "unpublished user Tag")
	b := f.create()
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/tags/9.9.9"] != "" {
		t.Fatal("branch push published an unrelated user Tag")
	}
	f.run(f.repo, "remote", "set-url", "--push", "origin", filepath.Join(f.root, "another.git"))
	if _, err = f.s.Inspect(context.Background(), f.selection, ""); err == nil {
		t.Fatal("different push repository accepted")
	}
}
