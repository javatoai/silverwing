package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

type materialsGitFixture struct {
	t                  *testing.T
	remote, seed, root string
	scope              MaterialsGitScope
	service            *MaterialsGitSyncService
}

func materialsGitTestRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "missing"), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=SW Test", "GIT_AUTHOR_EMAIL=sw@example.invalid", "GIT_COMMITTER_NAME=SW Test", "GIT_COMMITTER_EMAIL=sw@example.invalid")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, data, err)
	}
	return strings.TrimSpace(string(data))
}

func materialsGitTestFile(t *testing.T, root, relative, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newMaterialsGitFixture(t *testing.T, populated bool) *materialsGitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	f := &materialsGitFixture{t: t, remote: filepath.Join(base, "remote.git"), seed: filepath.Join(base, "seed"), root: filepath.Join(base, "资料")}
	for _, path := range []string{f.remote, f.seed, f.root} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	materialsGitTestRun(t, f.remote, "init", "--bare", "--initial-branch=master")
	materialsGitTestRun(t, f.seed, "init", "--initial-branch=master")
	materialsGitTestRun(t, f.seed, "remote", "add", "origin", f.remote)
	if populated {
		materialsGitTestFile(t, f.seed, "2026/123-需求/研发/remote.md", "remote\n")
		materialsGitTestFile(t, f.seed, "2026/123-需求/产品/design.md", "product\n")
		materialsGitTestRun(t, f.seed, "add", ".")
		materialsGitTestRun(t, f.seed, "commit", "-m", "seed")
		materialsGitTestRun(t, f.seed, "push", "origin", "master")
	}
	git := NewGitClient(nil, "git")
	git.Environment = map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(base, "missing"), "GIT_AUTHOR_NAME": "SW Test", "GIT_AUTHOR_EMAIL": "sw@example.invalid", "GIT_COMMITTER_NAME": "SW Test", "GIT_COMMITTER_EMAIL": "sw@example.invalid", "GIT_TERMINAL_PROMPT": "0"}
	// Explicit local identity is deliberately provided through command config,
	// just as the desktop requires an existing system/repository Git identity.
	git.Environment["GIT_CONFIG_COUNT"] = "2"
	git.Environment["GIT_CONFIG_KEY_0"] = "user.name"
	git.Environment["GIT_CONFIG_VALUE_0"] = "SW Test"
	git.Environment["GIT_CONFIG_KEY_1"] = "user.email"
	git.Environment["GIT_CONFIG_VALUE_1"] = "sw@example.invalid"
	paths := storage.NewPaths(filepath.Join(base, "home"))
	if err := os.MkdirAll(paths.Locks, 0o755); err != nil {
		t.Fatal(err)
	}
	f.service = &MaterialsGitSyncService{Git: git, Paths: paths}
	f.scope = MaterialsGitScope{Home: paths.Home, Root: f.root, Subdirectory: "研发", URL: f.remote, Branch: "master"}
	return f
}

func (f *materialsGitFixture) sync() MaterialsGitSyncStatus {
	f.t.Helper()
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err != nil {
		f.t.Fatalf("sync: %v; status=%+v", err, status)
	}
	if status.Paused || status.LastSuccess == "" {
		f.t.Fatalf("status=%+v", status)
	}
	return status
}

func TestMaterialsGitInitialJoinKeepsBothSidesAndScope(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/local [draft].md", "local\n")
	materialsGitTestFile(t, f.root, "2025/456-另一需求/研发/image.bin", "\x00\x01\x02")
	materialsGitTestFile(t, f.root, "outside.txt", "outside")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/.silverwing-import-temp", "private")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/ignored.log", "ignored")
	materialsGitTestFile(t, f.root, ".gitignore", "*.log\n")
	f.sync()
	for _, p := range []string{"2026/123-需求/研发/remote.md", "2026/123-需求/研发/local [draft].md", "2026/123-需求/产品/design.md"} {
		if _, err := os.Stat(filepath.Join(f.root, p)); err != nil {
			t.Fatal(p, err)
		}
	}
	tracked := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "master")
	for _, p := range []string{"outside.txt", ".silverwing-import-temp", "ignored.log"} {
		if strings.Contains(tracked, p) {
			t.Fatalf("unexpected upload %s: %s", p, tracked)
		}
	}
	head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	f.sync()
	if materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != head {
		t.Fatal("no-op sync created a commit")
	}
	if err := os.Remove(filepath.Join(f.root, "2026/123-需求/研发/local [draft].md")); err != nil {
		t.Fatal(err)
	}
	f.sync()
	if strings.Contains(materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "master"), "local [draft].md") {
		t.Fatal("deletion not synced")
	}
}

func TestMaterialsGitEmptyDirectoryClones(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	if data, err := os.ReadFile(filepath.Join(f.root, "2026/123-需求/研发/remote.md")); err != nil || string(data) != "remote\n" {
		t.Fatalf("clone content: %q %v", data, err)
	}
	if dirty := materialsGitTestRun(t, f.root, "status", "--porcelain"); strings.Contains(dirty, "remote.md") {
		t.Fatalf("clone left deleted/index state: %s", dirty)
	}
}

func TestMaterialsGitEmptyRemoteRequiresRemoteBranch(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	f.scope.Branch = "docs/team"
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/a.md", "a")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused || !strings.Contains(err.Error(), "请先在远程仓库创建分支") {
		t.Fatalf("empty remote must pause: %+v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("empty remote initialized a local repository: %v", err)
	}
	if refs := materialsGitTestRun(t, f.remote, "for-each-ref", "refs/heads"); refs != "" {
		t.Fatal("created a remote branch")
	}
}

func TestMaterialsGitConflictPauseAndManualRecovery(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.seed, "2026/123-需求/研发/remote.md", "other edit\n")
	materialsGitTestRun(t, f.seed, "add", ".")
	materialsGitTestRun(t, f.seed, "commit", "-m", "other")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/remote.md", "my edit\n")
	remoteHead := materialsGitTestRun(t, f.remote, "rev-parse", "master")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused {
		t.Fatalf("conflict must pause: %+v %v", status, err)
	}
	materialsGitTestRun(t, f.root, "rev-parse", "MERGE_HEAD")
	status, err = f.service.Sync(context.Background(), f.scope, false, nil)
	if err == nil || !status.Paused {
		t.Fatalf("automatic retry must remain paused: %+v %v", status, err)
	}
	if materialsGitTestRun(t, f.remote, "rev-parse", "master") != remoteHead {
		t.Fatal("conflict changed remote")
	}
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/remote.md", "resolved\n")
	materialsGitTestRun(t, f.root, "add", ".")
	materialsGitTestRun(t, f.root, "commit", "-m", "resolve")
	f.sync()
	if data, err := os.ReadFile(filepath.Join(f.root, "2026/123-需求/研发/remote.md")); err != nil || string(data) != "resolved\n" {
		t.Fatal("manual resolution lost")
	}
}

func TestMaterialsGitRejectsStagedChangesWrongOriginAndMissingBranch(t *testing.T) {
	for _, kind := range []string{"staged", "origin", "branch"} {
		t.Run(kind, func(t *testing.T) {
			f := newMaterialsGitFixture(t, true)
			f.sync()
			head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
			switch kind {
			case "staged":
				materialsGitTestFile(t, f.root, "outside.txt", "other")
				materialsGitTestRun(t, f.root, "add", "outside.txt")
			case "origin":
				materialsGitTestRun(t, f.root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "different.git"))
			case "branch":
				f.scope.Branch = "missing"
			}
			status, err := f.service.Sync(context.Background(), f.scope, true, nil)
			if err == nil || !status.Paused {
				t.Fatalf("expected pause: %+v %v", status, err)
			}
			if materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != head {
				t.Fatal("rejection changed HEAD")
			}
		})
	}
}

func TestMaterialsGitLateScopeStopsBeforeMutation(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.service.Check = func() error { return ErrMaterialsGitScopeChanged }
	_, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if !errors.Is(err, ErrMaterialsGitScopeChanged) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("stale scope initialized repository")
	}
}

func TestMaterialsGitParentRepositoryAndUnpushedBranchStaySafe(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	parent := filepath.Dir(f.root)
	materialsGitTestRun(t, parent, "init", "--initial-branch=parent")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/local.md", "local")
	f.sync()
	if materialsGitTestRun(t, parent, "branch", "--show-current") != "parent" {
		t.Fatal("parent changed")
	}
	materialsGitTestRun(t, f.root, "branch", "docs/next")
	materialsGitTestRun(t, f.root, "push", "origin", "docs/next")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/local.md", "unpublished")
	materialsGitTestRun(t, f.root, "add", ".")
	materialsGitTestRun(t, f.root, "commit", "-m", "unpublished")
	f.scope.Branch = "docs/next"
	_, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !strings.Contains(err.Error(), "未推送") {
		t.Fatal(err)
	}
	if materialsGitTestRun(t, f.root, "branch", "--show-current") != "master" {
		t.Fatal("changed branch with unpublished commits")
	}
}

func TestMaterialsGitPushRaceRetriesWithoutDuplicateLocalCommit(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/mine.md", "mine")
	var raced bool
	status, err := f.service.Sync(context.Background(), f.scope, true, func(phase string) {
		if phase != "推送资料" || raced {
			return
		}
		raced = true
		materialsGitTestFile(t, f.seed, "2025/456-另一需求/研发/theirs.md", "theirs")
		materialsGitTestRun(t, f.seed, "add", ".")
		materialsGitTestRun(t, f.seed, "commit", "-m", "race")
		materialsGitTestRun(t, f.seed, "push", "origin", "master")
	})
	if err == nil || status.Paused {
		t.Fatalf("push race should retry: %+v %v", status, err)
	}
	f.sync()
	if got := materialsGitTestRun(t, f.root, "log", "--format=%s", "--all"); strings.Count(got, "docs: sync task materials") != 1 {
		t.Fatalf("duplicate local commit: %s", got)
	}
	for _, path := range []string{"2026/123-需求/研发/mine.md", "2025/456-另一需求/研发/theirs.md"} {
		if _, err := os.Stat(filepath.Join(f.root, path)); err != nil {
			t.Fatal(path, err)
		}
	}
}

func TestMaterialsGitInterruptedStagingRecoversOnlyOwnedIndex(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/new.md", "new")
	head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	state, err := f.service.load(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	state.Staging = true
	state.StageHead = head
	state.StagePaths = []string{"2026/123-需求/研发/new.md"}
	if err := f.service.save(f.scope, state); err != nil {
		t.Fatal(err)
	}
	materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/研发/new.md")
	f.sync()
	if strings.Count(materialsGitTestRun(t, f.root, "log", "--format=%s"), "docs: sync task materials") != 1 {
		t.Fatal("staging recovery duplicated commit")
	}
}

func TestMaterialsGitBranchSwitchUsesConfiguredBranch(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestRun(t, f.root, "branch", "docs/team")
	materialsGitTestRun(t, f.root, "push", "origin", "docs/team")
	f.scope.Branch = "docs/team"
	f.sync()
	if materialsGitTestRun(t, f.root, "branch", "--show-current") != "docs/team" {
		t.Fatal("did not switch")
	}
	master := materialsGitTestRun(t, f.remote, "rev-parse", "master")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/team.md", "team")
	f.sync()
	if master != materialsGitTestRun(t, f.remote, "rev-parse", "master") {
		t.Fatal("pushed to previous branch")
	}
}

func TestMaterialsGitRetryClassification(t *testing.T) {
	for _, message := range []string{"Could not resolve host", "Failed to connect", "[rejected] (fetch first)"} {
		if !materialsGitRetryable(errors.New(message)) {
			t.Fatal(message)
		}
	}
	for _, message := range []string{"Authentication failed", "Permission denied (publickey)", "CONFLICT (content)", "index.lock already exists"} {
		if materialsGitRetryable(errors.New(message)) {
			t.Fatal(message)
		}
	}
}

func TestMaterialsGitWrongOriginPauseSurvivesStatusReload(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	f.scope.URL = filepath.Join(t.TempDir(), "new-remote.git")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused {
		t.Fatal("wrong origin not paused", status, err)
	}
	reloaded, err := f.service.Status(f.scope)
	if err != nil || !reloaded.Paused || reloaded.Error == "" || reloaded.LastSuccess != "" {
		t.Fatalf("wrong-target pause not retained: %+v %v", reloaded, err)
	}
	if materialsGitTestRun(t, f.root, "remote", "get-url", "origin") != f.remote {
		t.Fatal("origin was changed")
	}
}
