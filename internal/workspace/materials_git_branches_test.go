package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMaterialsGitRemoteBranchesReadOnlyWithoutLocalRepository(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	materialsGitTestRun(t, f.seed, "branch", "docs/team")
	materialsGitTestRun(t, f.seed, "push", "origin", "docs/team")
	before := materialsGitTestRun(t, f.remote, "show-ref")
	got, err := f.service.RemoteBranches(context.Background(), f.scope)
	if err != nil || !reflect.DeepEqual(got, []string{"docs/team", "master"}) {
		t.Fatalf("branches=%v err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("read initialized repository")
	}
	if _, err := os.Stat(f.service.statePath(f.scope)); !os.IsNotExist(err) {
		t.Fatal("read wrote sync state")
	}
	if materialsGitTestRun(t, f.remote, "show-ref") != before {
		t.Fatal("read changed remote refs")
	}
	f.service.Check = func() error { return ErrMaterialsGitScopeChanged }
	if _, err := f.service.RemoteBranches(context.Background(), f.scope); !errors.Is(err, ErrMaterialsGitScopeChanged) {
		t.Fatal(err)
	}
}

func TestMaterialsGitRemoteBranchesEmptyAndReadFailure(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	if branches, err := f.service.RemoteBranches(context.Background(), f.scope); err != nil || len(branches) != 0 {
		t.Fatalf("%v %v", branches, err)
	}
	f.scope.URL = filepath.Join(t.TempDir(), "absent.git")
	if _, err := f.service.RemoteBranches(context.Background(), f.scope); err == nil {
		t.Fatal("read failure reported as empty branches")
	}
}

func TestMaterialsGitDeletedRemoteBranchDoesNotCommitOrRecreate(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/new.md", "unsynced")
	materialsGitTestRun(t, f.remote, "update-ref", "-d", "refs/heads/master")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused {
		t.Fatalf("%+v %v", status, err)
	}
	if materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != head {
		t.Fatal("committed after remote branch deletion")
	}
	if refs := materialsGitTestRun(t, f.remote, "for-each-ref", "refs/heads"); refs != "" {
		t.Fatal("recreated branch")
	}
}

func TestMaterialsGitRemoteBranchDeletedBeforePushStops(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/new.md", "new")
	status, err := f.service.Sync(context.Background(), f.scope, true, func(phase string) {
		if phase == "推送资料" {
			materialsGitTestRun(t, f.remote, "update-ref", "-d", "refs/heads/master")
		}
	})
	if err == nil || !status.Paused {
		t.Fatalf("%+v %v", status, err)
	}
	if refs := materialsGitTestRun(t, f.remote, "for-each-ref", "refs/heads"); strings.TrimSpace(refs) != "" {
		t.Fatal("push recreated deleted branch")
	}
}
