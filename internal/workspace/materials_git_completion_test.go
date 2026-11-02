package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

func TestMaterialsGitRemoteIgnoresApplyBeforeFirstLocalCommit(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-nonempty-join", true: "existing-repository"}[existing], func(t *testing.T) {
			f := newMaterialsGitFixture(t, true)
			if existing {
				f.sync()
			}
			materialsGitTestFile(t, f.seed, ".gitignore", "*.private\ncache/\n")
			materialsGitTestRun(t, f.seed, "add", ".")
			materialsGitTestRun(t, f.seed, "commit", "-m", "remote ignore rules")
			materialsGitTestRun(t, f.seed, "push", "origin", "master")
			secret := "2026/123-需求/研发/never.private"
			materialsGitTestFile(t, f.root, secret, "retain local bytes")
			materialsGitTestFile(t, f.root, "cache/result.txt", "local cache")
			materialsGitTestFile(t, f.root, "non-annual/local.md", "upload")
			var phases []string
			if _, err := f.service.Sync(context.Background(), f.scope, true, func(phase string) { phases = append(phases, phase) }); err != nil {
				t.Fatal(err)
			}
			if strings.Index(strings.Join(phases, "|"), "拉取并合并远端更新") > strings.Index(strings.Join(phases, "|"), "提交本地资料") {
				t.Fatal("local commit preceded remote integration", phases)
			}
			tracked := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "-z", "master")
			if strings.Contains(tracked, "never.private") || strings.Contains(tracked, "cache/result.txt") || !strings.Contains(tracked, "non-annual/local.md") {
				t.Fatal("latest remote ignores did not govern first upload", tracked)
			}
			if data, err := os.ReadFile(filepath.Join(f.root, secret)); err != nil || string(data) != "retain local bytes" {
				t.Fatal("ignored bytes were lost", err)
			}
		})
	}
}

func TestMaterialsGitExistingStageAndTrackedIgnoredPathsParticipate(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	materialsGitTestFile(t, f.seed, "tracked.log", "original")
	materialsGitTestRun(t, f.seed, "add", "tracked.log")
	materialsGitTestFile(t, f.seed, ".gitignore", "*.log\n")
	materialsGitTestRun(t, f.seed, "add", ".gitignore")
	materialsGitTestRun(t, f.seed, "commit", "-m", "tracked path plus ignore")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	f.sync()
	for _, path := range []string{"tracked.log", "outside.txt", ".silverwing-persistent.json", "2026/123-需求/产品/design.md", "2026/123-需求/temp/script.txt"} {
		materialsGitTestFile(t, f.root, path, "whole repo change")
	}
	materialsGitTestRun(t, f.root, "add", "outside.txt")
	materialsGitTestFile(t, f.root, "new-ignored.log", "stay local")
	f.sync()
	for _, path := range []string{"tracked.log", "outside.txt", ".silverwing-persistent.json", "2026/123-需求/产品/design.md", "2026/123-需求/temp/script.txt"} {
		if content := materialsGitTestRun(t, f.remote, "show", "master:"+path); content != "whole repo change" {
			t.Fatal(path, content)
		}
	}
	if tracked := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "master"); strings.Contains(tracked, "new-ignored.log") || strings.Contains(tracked, ".silverwing-requirement-materials.lock") {
		t.Fatal("uploaded ignored data or runtime lock", tracked)
	}
}

func TestMaterialsGitInitialCollisionRetainsIgnoredAndUntrackedBytes(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		t.Run(map[bool]string{false: "untracked", true: "ignored"}[ignored], func(t *testing.T) {
			f := newMaterialsGitFixture(t, true)
			path := "2026/123-需求/产品/design.md"
			materialsGitTestFile(t, f.root, path, "local must survive")
			if ignored {
				materialsGitTestFile(t, f.root, ".gitignore", "*.md\n")
			}
			remote := materialsGitTestRun(t, f.remote, "rev-parse", "master")
			status, err := f.service.Sync(context.Background(), f.scope, true, nil)
			if err == nil || !status.Paused {
				t.Fatal("initial same-name collision did not pause", status, err)
			}
			if data, err := os.ReadFile(filepath.Join(f.root, path)); err != nil || string(data) != "local must survive" {
				t.Fatal("checkout overwrote local data", err)
			}
			if materialsGitTestRun(t, f.remote, "rev-parse", "master") != remote {
				t.Fatal("initial collision changed remote")
			}
		})
	}
}

func TestMaterialsGitMigratesMarkedDefaultsAndPreservesUserRules(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	ignore := "2026/123-需求/temp/.gitignore"
	materialsGitTestFile(t, f.root, ignore, "# user\r\n*.scratch\r\n# Silverwing local temporary files\n*\n")
	materialsGitTestFile(t, f.root, "2026/123-需求/temp/include.txt", "include")
	materialsGitTestFile(t, f.root, "2026/123-需求/temp/local.scratch", "private")
	materialsGitTestFile(t, f.root, ".git/info/exclude", "# user\n*.local\n# Silverwing local runtime and temporary files\n.silverwing-*\n/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/\n")
	materialsGitTestFile(t, f.root, "keep.local", "private")
	materialsGitTestFile(t, f.root, ".silverwing-visible", "persistent")
	f.sync()
	if data, err := os.ReadFile(filepath.Join(f.root, ignore)); err != nil || string(data) != "# user\r\n*.scratch\r\n" {
		t.Fatal("user ignore bytes changed", string(data), err)
	}
	tracked := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "-z", "master")
	if !strings.Contains(tracked, "include.txt") || !strings.Contains(tracked, ".silverwing-visible") || strings.Contains(tracked, "local.scratch") || strings.Contains(tracked, "keep.local") {
		t.Fatal("marked migration/user rules did not govern whole repo", tracked)
	}
}

func TestMaterialsGitMigrationFailureStopsBeforeCommit(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	path := filepath.Join(f.root, ".git", "info", "exclude")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	materialsGitTestFile(t, f.root, "pending.md", "must remain uncommitted")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	// Restore only the deliberately damaged exclude file before asking native
	// Git to inspect HEAD/index; otherwise the test oracle itself cannot run.
	if restoreErr := os.Remove(path); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if restoreErr := os.WriteFile(path, original, 0600); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil || !status.Paused || materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != head {
		t.Fatal("migration failure committed anyway", status, err)
	}
	if staged := materialsGitTestRun(t, f.root, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatal("staged before ignore migration succeeded", staged)
	}
}

func TestMaterialsGitRootLockSharedAcrossApplicationHomes(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	release, err := storage.AcquireMaterialsRepositoryLock(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	other := &MaterialsGitSyncService{Git: f.service.Git, Paths: storage.NewPaths(t.TempDir())}
	if _, err := other.Sync(context.Background(), f.scope, true, nil); !errors.Is(err, storage.ErrLockBusy) {
		t.Fatal("another application home bypassed root lock", err)
	}
}

func TestMaterialsGitCancelledBeforeStartDoesNotMigrateLocalFiles(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	legacy := filepath.Join(f.root, ".silverwing-requirement-materials.lock")
	if err := os.WriteFile(legacy, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ignore := "2026/123-需求/temp/.gitignore"
	const generated = "# Silverwing local temporary files\n*\n"
	materialsGitTestFile(t, f.root, ignore, generated)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.Sync(ctx, f.scope, true, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled sync did not stop", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("cancelled sync retired a local file", err)
	}
	if data, err := os.ReadFile(filepath.Join(f.root, ignore)); err != nil || string(data) != generated {
		t.Fatal("cancelled sync migrated ignore content", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("cancelled sync initialized repository", err)
	}
}
