package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/documents"
)

// A tracked file at an annual ancestor can replace the local temp directory,
// even if the incoming tree contains no literal temp/file path.
func materialsGitProtectedPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) == 0 || !documents.MaterialsYear(parts[0]) {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	if !documents.MaterialsRequirementName(parts[1]) {
		return false
	}
	return len(parts) == 2 || strings.EqualFold(parts[2], "temp")
}

func (s *MaterialsGitSyncService) checkMaterialsTree(ctx context.Context, scope MaterialsGitScope, ref string) error {
	result, err := s.run(ctx, scope, true, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return err
	}
	for _, record := range splitGitNUL(result.Stdout) {
		_, path, ok := strings.Cut(record, "\t")
		if !ok {
			return fmt.Errorf("无法核对 Git 目录记录")
		}
		if materialsGitProtectedPath(path) {
			return fmt.Errorf("Git 目标包含本地 temp 或其目录祖先，已停止检出/合并：%s", path)
		}
	}
	return nil
}

func gitMutationCommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i], args[i+1:]
	}
	return "", nil
}

// Check local-only history separately from the final tree. Remote peer commits
// are excluded by ancestry; a user's manual peer commit must never hitchhike.
func (s *MaterialsGitSyncService) checkMaterialsOutgoing(ctx context.Context, scope MaterialsGitScope, remote string, history bool) error {
	if history {
		commits, err := s.run(ctx, scope, true, "rev-list", remote+"..HEAD")
		if err != nil {
			return err
		}
		for _, commit := range strings.Fields(commits.Stdout) {
			// Audit every tree, even when a later merge restores its contents.
			// A merge may inherit an old local parent's peer contents, so being
			// equal to any parent is not proof that the remote authorized them.
			if err := s.checkMaterialsTree(ctx, scope, commit); err != nil {
				return err
			}
			paths, err := s.materialsOutgoingCommitPaths(ctx, scope, remote, commit)
			if err != nil {
				return err
			}
			for _, path := range paths {
				if !materialsGitOwned(path, scope.Subdirectory) {
					return fmt.Errorf("本地未推送提交包含范围外资料，已暂停，请使用 Git 工具处理：%s", path)
				}
			}
		}
		return nil
	}
	paths, err := s.run(ctx, scope, true, "diff", "--name-only", "--no-renames", "-z", remote, "HEAD")
	if err != nil {
		return err
	}
	for _, path := range splitGitNUL(paths.Stdout) {
		if !materialsGitOwned(path, scope.Subdirectory) {
			return fmt.Errorf("待推送内容包含同事资料、temp 或范围外路径，已暂停：%s", path)
		}
	}
	return nil
}

// Only ancestry shared with the selected remote authorizes non-owned content.
// A local owned commit behind a newer remote peer edit compares with its older
// common ancestor; a merge already containing that remote edit must retain it.
func (s *MaterialsGitSyncService) materialsOutgoingCommitPaths(ctx context.Context, scope MaterialsGitScope, remote, commit string) ([]string, error) {
	bases, err := s.run(ctx, scope, true, "merge-base", "--all", remote, commit)
	if err != nil {
		var rejected *GitException
		if !errors.As(err, &rejected) || rejected.Cause != nil || rejected.Result.ExitCode != 1 {
			return nil, err
		}
		// Git exit 1 specifically means unrelated histories. Before the first
		// join, a local history with no trusted remote ancestor may own only
		// its configured paths; it cannot bring any peer tree along.
		paths, err := s.run(ctx, scope, true, "ls-tree", "-r", "--name-only", "-z", commit)
		return splitGitNUL(paths.Stdout), err
	}
	ancestors := strings.Fields(bases.Stdout)
	if len(ancestors) != 1 {
		return nil, fmt.Errorf("本地未推送提交无法确定唯一可信公共祖先，已暂停，请使用 Git 工具处理：%s", commit)
	}
	paths, err := s.run(ctx, scope, true, "diff", "--name-only", "--no-renames", "-z", ancestors[0], commit)
	return splitGitNUL(paths.Stdout), err
}
