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
	"time"
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

func TestMaterialsGitReadBudgetRejectsTruncatedRefAndPreservesCancellation(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	args := []string{"ls-remote", "--heads", "--", f.scope.URL, "refs/heads/master"}
	result, err := f.service.Git.runReadBudget(context.Background(), f.scope.Root, time.Minute, 8, args...)
	if !errors.Is(err, errGitReadOutputLimit) || !result.StdoutTruncated || len(result.Stdout) > 8 {
		t.Fatalf("truncated ref became success or escaped the budget: %+v, %v", result, err)
	}
	result, err = f.service.Git.runReadBudget(context.Background(), f.scope.Root, time.Minute, 4096, args...)
	if err != nil || result.StdoutTruncated || !strings.Contains(result.Stdout, "refs/heads/master") {
		t.Fatalf("bounded exact-ref read failed: %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.Git.runReadBudget(ctx, f.scope.Root, time.Minute, 4096, args...); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded read lost cancellation: %v", err)
	}
}
