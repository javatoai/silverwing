package workspace

import (
	"context"
	"errors"
)

// CompareTreesContext reuses the bounded file/blob diff readers for two
// immutable commits. The caller owns repository identity validation and locking;
// no task, working-tree snapshot, temporary index, fetch or Git write is needed.
func (s *WorkspaceGitDiffService) CompareTreesContext(ctx context.Context, root, base, target string, change *CompareChange) (CompareFilesResult, *WorkspaceGitFilePreview, error) {
	if !validGitObjectID(base) || !validGitObjectID(target) {
		return CompareFilesResult{}, nil, errors.New("差异预览必须绑定完整提交 SHA")
	}
	state := &compareState{service: s, root: root, mode: CompareCommits, baseTree: base, targetTree: target, baseCommit: base, targetCommit: target,
		indexEnv: map[string]string{"GIT_OPTIONAL_LOCKS": "0"}, indexRemove: gitCompareEnvironmentRemovals(s.Git.RemoveEnvironment)}
	files, err := state.files(ctx)
	if err != nil {
		return CompareFilesResult{}, nil, err
	}
	result := CompareFilesResult{Files: files, BaseCommit: base, TargetCommit: target}
	if change == nil {
		return result, nil, nil
	}
	canonical, ok := matchCompareChange(files, *change)
	if !ok {
		return result, nil, errors.New("所选文件不属于当前提交差异")
	}
	preview, err := state.preview(ctx, canonical)
	return result, &preview, err
}
