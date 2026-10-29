package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

// TreeFilesSessionContext retains an immutable file list using the same
// cancellation, close and bounded preview cache as ordinary workspace reviews.
// The caller validates/locks the repository before and after every call.
func (s *WorkspaceGitDiffService) TreeFilesSessionContext(ctx context.Context, manager *WorkspaceGitDiffSessionManager, owner, root, base, target string) (CompareFilesResult, error) {
	if manager == nil || owner == "" || !validGitObjectID(base) || !validGitObjectID(target) {
		return CompareFilesResult{}, errors.New("差异会话缺少所属身份或完整提交 SHA")
	}
	ctx, _, finish, err := manager.begin(ctx)
	if err != nil {
		return CompareFilesResult{}, err
	}
	defer finish()
	state := &compareState{service: s, root: root, mode: CompareCommits, baseTree: base, targetTree: target, baseCommit: base, targetCommit: target,
		indexEnv: map[string]string{"GIT_OPTIONAL_LOCKS": "0"}, indexRemove: gitCompareEnvironmentRemovals(s.Git.RemoveEnvironment)}
	files, err := state.files(ctx)
	if err != nil {
		state.close()
		return CompareFilesResult{}, err
	}
	id, err := manager.register(treeDiffOwnerHash(owner), state, files)
	if err != nil {
		return CompareFilesResult{}, err
	}
	if err := ctx.Err(); err != nil {
		manager.Retire(id)
		return CompareFilesResult{}, err
	}
	return CompareFilesResult{SessionID: id, Files: append([]CompareChange(nil), files...), BaseCommit: base, TargetCommit: target}, nil
}

func (s *WorkspaceGitDiffService) TreePreviewSessionContext(ctx context.Context, manager *WorkspaceGitDiffSessionManager, owner, root, base, target, id string, change CompareChange) (CompareFilesResult, *WorkspaceGitFilePreview, error) {
	if manager == nil || owner == "" {
		return CompareFilesResult{}, nil, errors.New("差异会话缺少所属身份")
	}
	ctx, session, release, err := manager.acquire(ctx, id, treeDiffOwnerHash(owner))
	if err != nil {
		return CompareFilesResult{}, nil, err
	}
	defer release()
	state := session.state
	if state.root != root || state.targetCommit != target || base != "" && base != state.baseCommit {
		return CompareFilesResult{}, nil, errors.New("差异会话所属仓库或提交已变化")
	}
	canonical, ok := matchCompareChange(session.files, change)
	if !ok {
		return CompareFilesResult{}, nil, errors.New("所选文件不属于当前提交差异")
	}
	finish, err := session.beginForeground(ctx, canonical)
	if err != nil {
		return CompareFilesResult{}, nil, err
	}
	defer finish()
	preview, _, err := session.preview(ctx, canonical)
	if err != nil {
		return CompareFilesResult{}, nil, err
	}
	// Preview replies contain only identity metadata; the list is already owned
	// by the consumer and is never re-enumerated or copied per file selection.
	return CompareFilesResult{SessionID: id, BaseCommit: state.baseCommit, TargetCommit: state.targetCommit}, &preview, nil
}

func treeDiffOwnerHash(owner string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("immutable-tree\x00"+owner)))
}
