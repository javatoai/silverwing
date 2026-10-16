package workspace

import (
	"context"
	"errors"
	"testing"

	"silverwing-go/internal/domain"
)

func TestGitMutationCancellationReachesValidation(t *testing.T) {
	service := NewWorkspaceGitOperationService(NewGitClient(nil, "git"), NewOperationLocks(t.TempDir()))
	ws := domain.ServiceWorkspace{WorktreePath: t.TempDir(), Branch: "feature", Strategy: domain.WorkspaceStandardWorktree}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() (WorkspaceGitOperationResult, error)
	}{
		{"commit", func() (WorkspaceGitOperationResult, error) { return service.Commit(ctx, ws, "message", "", nil) }},
		{"push", func() (WorkspaceGitOperationResult, error) { return service.Push(ctx, ws, nil) }},
		{"commit and push", func() (WorkspaceGitOperationResult, error) { return service.CommitAndPush(ctx, ws, "message", "", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation started validation: %v", err)
			}
		})
	}
}
