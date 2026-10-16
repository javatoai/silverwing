package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTagValidationStopsCanceledRequestBeforeGit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Service{git: workspace.NewGitClient(nil, filepath.Join(t.TempDir(), "must-not-run"))}
	_, err := s.validateForMutation(ctx, resolvedTask{}, domain.ServiceWorkspace{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validation ignored cancellation: %v", err)
	}
}

type cancelAfterValidationStarts struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelAfterValidationStarts) Err() error {
	err := c.Context.Err()
	c.cancel()
	return err
}

func TestTagValidationForwardsCancellationToGit(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "task")
	worktree := filepath.Join(directory, "service")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The initial validation check succeeds, then cancellation arrives before
	// Git starts. An unrelated background context would try the non-repository.
	ctx := cancelAfterValidationStarts{Context: parent, cancel: cancel}
	s := &Service{git: workspace.NewGitClient(nil, "git")}
	_, err := s.validateForMutation(ctx, resolvedTask{config: domain.AppConfig{TaskRoot: &root}, directory: directory, manifest: domain.TaskManifest{TaskDirectoryName: "task"}}, domain.ServiceWorkspace{WorktreePath: worktree, Branch: "feature"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Git validation ignored cancellation: %v", err)
	}
}
