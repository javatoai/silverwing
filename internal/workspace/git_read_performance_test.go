package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func performanceGitTrace(t *testing.T) (string, func() [][]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git-trace.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", filepath.ToSlash(path))
	read := func() [][]string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var starts [][]string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var event struct {
				Event string   `json:"event"`
				Argv  []string `json:"argv"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			if event.Event == "start" {
				starts = append(starts, event.Argv)
			}
		}
		return starts
	}
	return path, read
}

func TestImmutableTreeSessionEnumeratesOnceAndCachesRevisits(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "a.txt", "before\n")
	writeCompareFile(t, repo.path, "b.txt", "before b\n")
	commitCompare(t, repo.path, "base")
	base := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	writeCompareFile(t, repo.path, "a.txt", "after\n")
	writeCompareFile(t, repo.path, "b.txt", "after b\n")
	commitCompare(t, repo.path, "target")
	target := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	_, trace := performanceGitTrace(t)
	reader, _ := repo.serviceWorkspace("main")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	files, err := reader.TreeFilesSessionContext(context.Background(), manager, "home/config/repository/pair", repo.path, base, target)
	if err != nil || len(files.Files) != 2 || files.SessionID == "" {
		t.Fatalf("tree session: %+v, %v", files, err)
	}
	for _, file := range files.Files {
		metadata, preview, err := reader.TreePreviewSessionContext(context.Background(), manager, "home/config/repository/pair", repo.path, base, target, files.SessionID, file)
		if err != nil || preview == nil || preview.Content == nil || len(metadata.Files) != 0 {
			t.Fatalf("preview re-enumerated or failed: %+v, %v", metadata, err)
		}
	}
	starts := trace()
	beforeHot := len(starts)
	for _, file := range files.Files {
		if _, _, err := reader.TreePreviewSessionContext(context.Background(), manager, "home/config/repository/pair", repo.path, base, target, files.SessionID, file); err != nil {
			t.Fatal(err)
		}
	}
	if afterHot := len(trace()); afterHot != beforeHot {
		t.Fatalf("cache revisit ran %d extra Git commands", afterHot-beforeHot)
	}
	rawLists := 0
	for _, args := range starts {
		if slices.Contains(args, "--raw") {
			rawLists++
		}
	}
	if rawLists != 1 {
		t.Fatalf("enumerated immutable file list %d times, want once", rawLists)
	}
	for _, scope := range []struct{ owner, root, base, target string }{
		{"changed-config", repo.path, base, target},
		{"home/config/repository/pair", repo.path, target, target},
		{"home/config/repository/pair", t.TempDir(), base, target},
	} {
		if _, _, err := reader.TreePreviewSessionContext(context.Background(), manager, scope.owner, scope.root, scope.base, scope.target, files.SessionID, files.Files[0]); err == nil {
			t.Fatal("cached preview accepted a changed owner/tree identity")
		}
	}
	manager.Retire(files.SessionID)
	if _, _, err := reader.TreePreviewSessionContext(context.Background(), manager, "home/config/repository/pair", repo.path, base, target, files.SessionID, files.Files[0]); err == nil {
		t.Fatal("retired tree session remained readable")
	}
}

func TestMaterialsRemoteBranchTargetsOneRefAtEveryRead(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	head := materialsGitTestRun(t, f.remote, "rev-parse", "refs/heads/master")
	for _, prefix := range []string{"refs/heads/unrelated/", "refs/tags/unrelated-"} {
		for index := 0; index < 20; index++ {
			materialsGitTestRun(t, f.remote, "update-ref", prefix+strings.Repeat("x", index+1), head)
		}
	}
	checks := 0
	f.service.Check = func() error { checks++; return nil }
	_, trace := performanceGitTrace(t)
	for range 3 {
		got, exists, err := f.service.remoteBranch(context.Background(), f.scope)
		if err != nil || !exists || got != head {
			t.Fatalf("target remote read: %s/%v/%v", got, exists, err)
		}
	}
	remoteReads := 0
	for _, args := range trace() {
		index := slices.Index(args, "ls-remote")
		if index < 0 {
			continue
		}
		remoteReads++
		if !slices.Equal(args[index:], []string{"ls-remote", "--heads", "--", f.scope.URL, "refs/heads/master"}) {
			t.Fatalf("read unfiltered remote refs: %v", args)
		}
	}
	if remoteReads != 3 || checks != 3 {
		t.Fatalf("remote confirmations/checks = %d/%d, want 3/3", remoteReads, checks)
	}
	missing := f.scope
	missing.Branch = "missing"
	if _, exists, err := f.service.remoteBranch(context.Background(), missing); err == nil || exists {
		t.Fatal("missing branch was accepted")
	}
	failed := f.scope
	failed.URL = filepath.Join(t.TempDir(), "not-a-repository")
	_, _, err := f.service.remoteBranch(context.Background(), failed)
	var commandErr *GitException
	if !errors.As(err, &commandErr) {
		t.Fatalf("command failure became an absent branch: %v", err)
	}
}
