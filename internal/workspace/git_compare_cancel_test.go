package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestCompareLookupsPreserveCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := &compareState{
		service: &WorkspaceGitDiffService{Git: NewGitClient(nil, "git")},
	}

	t.Run("commit lookup", func(t *testing.T) {
		_, err := state.resolveCommit(ctx, "0123456789abcdef0123456789abcdef01234567")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("resolveCommit error = %v, want context.Canceled", err)
		}
	})

	t.Run("mainline lookup", func(t *testing.T) {
		_, err := state.localMainlineCommit(ctx, WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("localMainlineCommit error = %v, want context.Canceled", err)
		}
	})
}
