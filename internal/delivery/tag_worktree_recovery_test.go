package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTagPreflightReattachesInterruptedConflictWithoutChangingFiles(t *testing.T) {
	ctx := context.Background()
	service, repository := newTagRecoveryTestRepository(t)
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(repository, "overlap.txt")
	write(file, "base\n")
	tagRecoveryGit(t, service, repository, "add", "overlap.txt")
	tagRecoveryGit(t, service, repository, "commit", "-m", "base")
	base := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	write(file, "source\n")
	tagRecoveryGit(t, service, repository, "commit", "-am", "source")
	source := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	tagRecoveryGit(t, service, repository, "checkout", "-b", "target", base)
	write(file, "target\n")
	tagRecoveryGit(t, service, repository, "commit", "-am", "target")
	target := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")

	path, err := service.conflictWorktreePath(newUUID())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.addDetachedWorktree(ctx, repository, path, target); err != nil {
		t.Fatal(err)
	}
	result, err := service.run(ctx, path, 15*time.Second, false, false, "merge", "--no-commit", "--no-ff", source)
	if err != nil || result.ExitCode == 0 {
		t.Fatalf("fixture did not stop on a merge conflict: %v", err)
	}
	resolvedFile := filepath.Join(path, "overlap.txt")
	write(resolvedFile, "in-progress resolution\n")
	var conflict *mergeConflictError
	err = service.verifyMergeInTemporaryWorktree(ctx, repository, target, source, "test", path)
	if !errors.As(err, &conflict) || conflict.path != path || len(conflict.files) != 1 || conflict.targetSHA != target {
		t.Fatalf("interrupted conflict was not recognized: %+v %v", conflict, err)
	}
	content, err := os.ReadFile(resolvedFile)
	if err != nil || string(content) != "in-progress resolution\n" {
		t.Fatalf("retry altered a manual resolution: %q %v", content, err)
	}
	tagRecoveryGit(t, service, path, "add", "overlap.txt")
	err = service.verifyMergeInTemporaryWorktree(ctx, repository, target, source, "test", path)
	if !errors.As(err, &conflict) || len(conflict.files) != 0 {
		t.Fatalf("resolved and staged merge was not recovered: %+v %v", conflict, err)
	}
	tagRecoveryGit(t, service, path, "commit", "--no-edit")
	merged := tagRecoveryGit(t, service, path, "rev-parse", "HEAD")
	write(filepath.Join(repository, ".git", "info", "exclude"), "build.ignored\n")
	ignored := filepath.Join(path, "build.ignored")
	write(ignored, "generated during resolution\n")
	recovered, err := service.recoverTagWorktree(ctx, repository, path, target, source)
	if err != nil || recovered.mergedSHA != merged {
		t.Fatalf("existing merge commit not accepted: %+v %v", recovered, err)
	}
	if err := service.removeCleanTagWorktree(ctx, repository, path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("cleanup did not protect the ignored file: %v", err)
	}
	if content, err := os.ReadFile(ignored); err != nil || string(content) != "generated during resolution\n" {
		t.Fatalf("cleanup changed the ignored file: %q %v", content, err)
	}
	if err := service.verifyMergeInTemporaryWorktree(ctx, repository, base, source, "test", path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("drifted baseline did not produce an actionable worktree path: %v", err)
	}
	if head := tagRecoveryGit(t, service, path, "rev-parse", "HEAD"); head != merged {
		t.Fatalf("retry modified existing commit: %s", head)
	}
}

func TestTagPreflightPreservesIgnoredFilesInInterruptedWorktree(t *testing.T) {
	ctx := context.Background()
	service, repository := newTagRecoveryTestRepository(t)
	tagRecoveryGit(t, service, repository, "commit", "--allow-empty", "-m", "base")
	base := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	tagRecoveryGit(t, service, repository, "commit", "--allow-empty", "-m", "source")
	source := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	path, err := service.conflictWorktreePath(newUUID())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.addDetachedWorktree(ctx, repository, path, base); err != nil {
		t.Fatal(err)
	}
	if err := service.verifyMergeInTemporaryWorktree(ctx, repository, base, source, "test", path); err != nil {
		t.Fatalf("clean interrupted worktree cannot be reused: %v", err)
	}
	if head := tagRecoveryGit(t, service, path, "rev-parse", "HEAD"); head != base {
		t.Fatalf("preflight advanced existing worktree: %s", head)
	}
	if err := os.WriteFile(filepath.Join(repository, ".git", "info", "exclude"), []byte("saved.ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(path, "saved.ignored")
	if err := os.WriteFile(ignored, []byte("keep these bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.verifyMergeInTemporaryWorktree(ctx, repository, base, source, "test", path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("uncertain contents did not produce the exact worktree path: %v", err)
	}
	if bytes, err := os.ReadFile(ignored); err != nil || string(bytes) != "keep these bytes" {
		t.Fatalf("retry discarded an ignored file: %q %v", bytes, err)
	}
}
