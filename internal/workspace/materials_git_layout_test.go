package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterialsGitUploadsRequirementsFromTotalRoot(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	seedMaterialsLayoutRemote(t, f)
	f.scope.RequirementsRoot = filepath.Join(f.root, "requirements")
	materialsGitTestFile(t, f.root, "requirements/2026/7132215081-需求/研发/note.md", "原文\r\n")
	materialsGitTestFile(t, f.root, "requirements/2025/7000000000-历史/产品/design.md", "历史资料")
	materialsGitTestFile(t, f.root, "other-team.md", "whole repository")
	materialsGitTestFile(t, f.root, ".gitignore", "/temp/\n")
	materialsGitTestFile(t, f.root, "temp/ignored.md", "ignored by user")
	f.sync()
	tracked := materialsGitTestRun(t, f.remote, "-c", "core.quotepath=false", "ls-tree", "-r", "--name-only", "master")
	for _, path := range []string{"requirements/2026/7132215081-需求/研发/note.md", "requirements/2025/7000000000-历史/产品/design.md", "other-team.md"} {
		if !strings.Contains(tracked, path) {
			t.Fatalf("missing %s: %s", path, tracked)
		}
	}
	if strings.Contains(tracked, "temp/ignored.md") {
		t.Fatal("user ignore lost")
	}
}

func TestMaterialsGitRejectsNestedCollectionBeforeInitialization(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	f.scope.RequirementsRoot = filepath.Join(f.root, "requirements")
	marker := filepath.Join(f.scope.RequirementsRoot, ".git")
	if err := os.MkdirAll(marker, 0700); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.Sync(context.Background(), f.scope, false, nil)
	if err == nil || !strings.Contains(err.Error(), "独立 Git 仓库") || !status.Paused {
		t.Fatalf("nested: %+v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("initialized parent around nested repository")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("nested metadata changed")
	}
}

func TestMaterialsGitScopeExpansionRequiresExplicitResume(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	seedMaterialsLayoutRemote(t, f)
	f.scope.RequireRootConfirmation = true
	status, err := f.service.Status(f.scope)
	if err != nil || !status.Paused {
		t.Fatalf("scope expansion not paused: %+v %v", status, err)
	}
	if _, err := f.service.Sync(context.Background(), f.scope, false, nil); err == nil {
		t.Fatal("automatic job expanded upload root")
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("automatic job initialized expanded root")
	}
	materialsGitTestFile(t, f.root, "requirements/2026/7132215081-需求/研发/note.md", "confirmed")
	if _, err := f.service.Sync(context.Background(), f.scope, true, nil); err != nil {
		t.Fatal(err)
	}
	if status, err := f.service.Status(f.scope); err != nil || status.Paused {
		t.Fatalf("confirmation not saved: %+v %v", status, err)
	}
}

func seedMaterialsLayoutRemote(t *testing.T, f *materialsGitFixture) {
	t.Helper()
	materialsGitTestFile(t, f.seed, "README.md", "isolated repository\n")
	materialsGitTestRun(t, f.seed, "add", "README.md")
	materialsGitTestRun(t, f.seed, "commit", "-m", "seed")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
}
