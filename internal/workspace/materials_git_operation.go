package workspace

import (
	"context"

	"path/filepath"
	"strings"
	"time"
)

func gitOperationInProgressContext(ctx context.Context, git *GitClient, repository string) (string, error) {
	result, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDirectory := strings.TrimSpace(result.Stdout)
	for _, entry := range []struct{ name, kind string }{
		{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"BISECT_START", "bisect"},
	} {
		if fileExists(filepath.Join(gitDirectory, entry.name)) {
			return entry.kind, nil
		}
	}
	return "", nil
}
