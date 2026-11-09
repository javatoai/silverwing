package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

func (s *Service) conflictWorktreePath(id string) (string, error) {
	if !validOperationID(id) || s.paths.Home == "" {
		return "", errors.New("冲突工作区的操作标识或数据目录无效")
	}
	root := filepath.Join(canonicalPath(s.paths.Home), "tag-conflicts")
	path := filepath.Join(root, id)
	for _, part := range []string{root, path} {
		info, err := os.Lstat(part)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		real, err := filepath.EvalSymlinks(part)
		if err != nil || !info.IsDir() || !samePathSpelling(real, part) {
			return "", fmt.Errorf("冲突工作区路径已重定向或不是目录：%s", part)
		}
	}
	return path, nil
}

func samePathSpelling(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

func tagRemoteKey(url string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(NormalizeTagRemote(url))))
}

func (s *Service) tagBuildBlocker(ctx context.Context, task resolvedTask, validated mutationTarget, operation domain.TagOperation) (string, error) {
	if value(operation.BatchID) == "" || value(operation.TargetBranch) == "" {
		return "", nil
	}
	operations, err := s.store.list(task.directory)
	if err != nil {
		return "", err
	}
	for _, other := range operations {
		if other.OperationID == operation.OperationID || other.State == domain.TagOperationSuccess || other.Conflict == nil ||
			value(other.BatchID) != value(operation.BatchID) || value(other.TargetBranch) != value(operation.TargetBranch) {
			continue
		}
		remote, err := s.remoteURL(ctx, validated.repository, operation.Remote)
		if err != nil {
			return "", err
		}
		if remote != "" && tagRemoteKey(remote) == other.Conflict.RemoteKey {
			return other.OperationID, nil
		}
	}
	return "", nil
}

func (s *Service) recordTagConflict(ctx context.Context, task resolvedTask, validated mutationTarget, operation *domain.TagOperation, source, remoteSource string, conflict *mergeConflictError, endpoint tagWriteEndpoint) error {
	ctx, cancel := tagBuildCleanupContext(ctx)
	defer cancel()
	message := mergeConflictMessage(operation.ServiceName, operation.SourceBranch, operation.Remote+"/"+value(operation.TargetBranch))
	if len(conflict.files) == 0 {
		message = "已找到中断时保留的合并工作区。请检查合并结果并暂存修改后重试。"
	}
	common, commonErr := s.gitCommonDirectory(ctx, validated.repository)
	operation.Conflict = &domain.TagConflict{
		Worktree: conflict.path, Repository: canonicalPath(common), RemoteKey: tagRemoteKey(endpoint.fetch),
		RemoteIdentity: endpoint.identity(), WriteEndpointKey: endpoint.key,
		TaskCreatedAt: task.manifest.CreatedAt, RemoteSourceSHA: remoteSource, TargetBaseSHA: conflict.targetSHA,
	}
	if commonErr != nil {
		message += "\n保存仓库身份失败：" + commonErr.Error()
	}
	if err := s.transition(ctx, task.directory, operation, domain.TagOperationConflict, &source, &conflict.targetSHA, nil, &message, conflict.files); err != nil {
		return fmt.Errorf("保存冲突记录失败，现场位于 %s：%w", conflict.path, err)
	}
	return s.recordTagHistory(ctx, task.directory, *operation)
}

// OpenTagConflictWorkspace resolves a saved operation, never a caller-supplied path.
func (s *Service) OpenTagConflictWorkspace(ctx context.Context, taskFolder, operationID string) (string, error) {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return "", err
	}
	operation, err := s.store.load(task.directory, operationID)
	if err != nil {
		return "", err
	}
	if operation.Conflict == nil {
		target, err := domain.ResolveHistoricalTagTarget(task.config, task.manifest, operation)
		if err != nil {
			return "", err
		}
		validated, err := s.validateForMutation(ctx, task, target.Workspace)
		return validated.worktree, err
	}
	_, _, path, err := s.validateTagConflict(ctx, task, operation)
	return path, err
}

func (s *Service) validateTagConflict(ctx context.Context, task resolvedTask, operation domain.TagOperation) (domain.EffectiveTagTarget, mutationTarget, string, error) {
	target, err := domain.ResolveHistoricalTagTarget(task.config, task.manifest, operation)
	if err != nil {
		return target, mutationTarget{}, "", err
	}
	validated, err := s.validateForMutation(ctx, task, target.Workspace)
	if err != nil {
		return target, validated, "", err
	}
	saved := operation.Conflict
	path, err := s.conflictWorktreePath(operation.OperationID)
	if err != nil {
		return target, validated, "", err
	}
	if saved == nil || saved.TaskCreatedAt != task.manifest.CreatedAt || !samePathSpelling(saved.Worktree, path) ||
		target.Mode != domain.TagBuildMergeToTargetBranch || !validCommitSHA(value(operation.SourceSHA)) ||
		!validCommitSHA(saved.TargetBaseSHA) || saved.RemoteIdentity == "" {
		return target, validated, "", errors.New("冲突记录与当前任务、路径或合并基线不匹配")
	}
	common, err := s.gitCommonDirectory(ctx, validated.repository)
	if err != nil {
		return target, validated, "", err
	}
	actual, err := s.gitCommonDirectory(ctx, path)
	if err != nil {
		return target, validated, "", err
	}
	top, err := s.gitTopLevel(ctx, path)
	if err != nil {
		return target, validated, "", err
	}
	branch, err := s.currentBranch(ctx, path)
	if err != nil {
		return target, validated, "", err
	}
	if branch != "" || !samePath(common, saved.Repository) || !samePath(actual, common) || !samePath(top, path) {
		return target, validated, "", errors.New("冲突工作区必须是原仓库中的 detached worktree")
	}
	return target, validated, path, nil
}

func validCommitSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func (s *Service) checkTagConflict(ctx context.Context, task resolvedTask, operation domain.TagOperation, report *TagWorkspaceCheckReport) error {
	_, _, path, err := s.validateTagConflict(ctx, task, operation)
	if err != nil {
		return err
	}
	result, err := s.run(ctx, path, 2*time.Minute, true, true, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	// A resolved merge is ready with staged changes. Unmerged, unstaged and
	// untracked paths still need the user's attention.
	changes := []string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if len(line) < 3 {
			continue
		}
		code := line[:2]
		if code[1] != ' ' || code[0] == 'U' {
			changes = append(changes, tagWorkspaceChanges(line)...)
		}
	}
	*report = TagWorkspaceCheckReport{Clean: len(changes) == 0, Changes: changes}
	return nil
}

func (s *Service) resumeTagConflict(ctx context.Context, task resolvedTask, operation domain.TagOperation) (domain.TagOperation, error) {
	target, validated, path, err := s.validateTagConflict(ctx, task, operation)
	if err != nil {
		return operation, err
	}
	err = s.withRepositoryLock(ctx, validated.repository, func() error {
		return s.finishTagConflict(ctx, task, target, validated, path, &operation)
	})
	return operation, err
}

func (s *Service) finishTagConflict(ctx context.Context, task resolvedTask, target domain.EffectiveTagTarget, validated mutationTarget, path string, operation *domain.TagOperation) error {
	repository := validated.repository
	saved := *operation.Conflict
	endpoint, err := s.readTagWriteEndpoint(ctx, repository, target.Remote)
	if err != nil {
		return err
	}
	if endpoint.identity() != saved.RemoteIdentity || tagRemoteKey(endpoint.fetch) != saved.RemoteKey ||
		(saved.WriteEndpointKey != "" && endpoint.key != saved.WriteEndpointKey) {
		return changedTagWriteEndpoint()
	}
	ctx = withTagRemoteIdentity(ctx, saved.RemoteIdentity, endpoint.key)
	source := value(operation.SourceSHA)
	sourceRef, targetRef := "refs/heads/"+target.SourceBranch, "refs/heads/"+value(target.TargetBranch)
	currentSource, err := s.resolve(ctx, repository, sourceRef)
	if err != nil {
		return err
	}
	if currentSource != source {
		return changedTagBaseline("本地 "+target.SourceBranch, source, currentSource)
	}
	remoteSource, remoteTarget, err := s.tagRemoteBranchTips(ctx, repository, target.Remote, sourceRef, targetRef)
	if err != nil {
		return err
	}
	if remoteSource != saved.RemoteSourceSHA && remoteSource != source {
		return changedTagBaseline(target.Remote+"/"+target.SourceBranch, saved.RemoteSourceSHA, remoteSource)
	}
	if remoteTarget != saved.TargetBaseSHA && (saved.MergeSHA == "" || remoteTarget != saved.MergeSHA) {
		return changedTagBaseline(target.Remote+"/"+value(target.TargetBranch), saved.TargetBaseSHA, remoteTarget)
	}
	policy := domain.NewGitWritePolicy(task.config.BlockedGitWriteBranches)
	if remoteSource != source {
		if err := policy.RequireAllowed(target.SourceBranch, "测试Tag流程推送源分支"); err != nil {
			return err
		}
		if remoteSource != "" {
			ok, err := s.isAncestor(ctx, repository, remoteSource, source)
			if err != nil || !ok {
				return errors.Join(errors.New("源分支无法快进到原合并提交"), err)
			}
		}
	}
	if remoteTarget != saved.MergeSHA {
		if err := policy.RequireAllowed(value(target.TargetBranch), "测试Tag流程推送目标分支"); err != nil {
			return err
		}
	}
	merge, err := s.commitTagConflict(ctx, path, source, saved.TargetBaseSHA, saved.MergeSHA)
	if err != nil {
		return err
	}
	if saved.MergeSHA == "" || saved.WriteEndpointKey == "" {
		saved.MergeSHA = merge
		saved.WriteEndpointKey = endpoint.key
		operation.Conflict = &saved
		if err := s.transition(ctx, task.directory, operation, operation.State, nil, nil, nil, nil, nil); err != nil {
			return err
		}
	}
	if remoteSource != source {
		s.notifyTag("pushing-source", "推送原源分支提交", "", operation)
		if err := s.pushPinnedTagBranch(ctx, repository, target.Remote, saved.RemoteIdentity, sourceRef, remoteSource, source); err != nil {
			return err
		}
	}
	if remoteTarget != merge {
		s.notifyTag("pushing-target", "推送已解决冲突的合并提交", "", operation)
		if err := s.pushPinnedTagBranch(ctx, repository, target.Remote, saved.RemoteIdentity, targetRef, remoteTarget, merge); err != nil {
			return err
		}
	}
	if err := s.requireTagRemoteIdentity(ctx, repository, target.Remote, saved.RemoteIdentity); err != nil {
		return err
	}
	remoteSource, remoteTarget, err = s.tagRemoteBranchTips(ctx, repository, target.Remote, sourceRef, targetRef)
	if err != nil {
		return err
	}
	if remoteSource != source {
		return changedTagBaseline(target.Remote+"/"+target.SourceBranch, source, remoteSource)
	}
	if remoteTarget != merge {
		return changedTagBaseline(target.Remote+"/"+value(target.TargetBranch), merge, remoteTarget)
	}
	if err := s.transition(ctx, task.directory, operation, domain.TagOperationTargetBranchPushed, nil, &merge, nil, nil, nil); err != nil {
		return err
	}
	err = s.completeTagBuild(ctx, task, target, repository, operation, merge)
	if operation.State == domain.TagOperationSuccess {
		cleanup, cancel := tagBuildCleanupContext(ctx)
		defer cancel()
		if removeErr := s.removeCleanTagWorktree(cleanup, repository, path); removeErr != nil {
			return errors.Join(err, fmt.Errorf("Tag 已推送：%w", removeErr))
		}
		operation.Conflict = nil
		operation.ConflictFiles = []string{}
		err = errors.Join(err, s.transition(cleanup, task.directory, operation, operation.State, nil, nil, nil, nil, nil))
	}
	return err
}

func (s *Service) commitTagConflict(ctx context.Context, path, source, base, savedMerge string) (string, error) {
	head, err := s.resolve(ctx, path, "HEAD")
	if err != nil {
		return "", err
	}
	if savedMerge != "" && head != savedMerge {
		return "", changedTagBaseline("冲突工作区 HEAD", savedMerge, head)
	}
	if head == base && savedMerge == "" {
		gitDir, err := s.run(ctx, path, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return "", err
		}
		mergeHead, err := os.ReadFile(filepath.Join(strings.TrimSpace(gitDir.Stdout), "MERGE_HEAD"))
		if err != nil || strings.TrimSpace(string(mergeHead)) != source {
			return "", errors.New("合并现场已变化，要求 HEAD 为原目标提交且 MERGE_HEAD 为原源提交")
		}
		files, err := s.conflictFiles(ctx, path)
		if err != nil {
			return "", err
		}
		if len(files) > 0 {
			return "", fmt.Errorf("请先解决并暂存冲突文件：%s", strings.Join(files, ", "))
		}
		unstaged, err := s.run(ctx, path, 2*time.Minute, false, true, "diff", "--quiet", "--")
		if err != nil {
			return "", err
		}
		if unstaged.ExitCode != 0 {
			return "", errors.New("冲突工作区仍有未暂存修改，请暂存后重试")
		}
		untracked, err := s.run(ctx, path, 2*time.Minute, true, true, "ls-files", "--others", "--exclude-standard")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(untracked.Stdout) != "" {
			return "", errors.New("冲突工作区仍有未跟踪文件，请处理后重试")
		}
		if _, err := s.run(ctx, path, 10*time.Minute, true, false, "commit", "--no-edit"); err != nil {
			return "", err
		}
	}
	parents, err := s.run(ctx, path, 2*time.Minute, true, true, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(parents.Stdout)
	if len(fields) != 3 || fields[1] != base || fields[2] != source || (savedMerge != "" && fields[0] != savedMerge) {
		return "", errors.New("合并提交方向不匹配，要求第一父提交为原目标、第二父提交为原源")
	}
	if err := s.ensureCleanFeatureWorktree(ctx, path); err != nil {
		return "", err
	}
	return fields[0], nil
}

func (s *Service) tagRemoteBranchTips(ctx context.Context, repository, remote, sourceRef, targetRef string) (source, target string, err error) {
	args := []string{"ls-remote", "--refs", "--heads", "--", remote, sourceRef}
	if targetRef != "" && targetRef != sourceRef {
		args = append(args, targetRef)
	}
	result, err := s.run(ctx, repository, 2*time.Minute, false, true, args...)
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	if err != nil || result.ExitCode != 0 || result.StdoutTruncated {
		return "", "", fmt.Errorf("无法读取远端分支提交（退出码 %d）", result.ExitCode)
	}
	for _, line := range nonEmptyLines(result.Stdout) {
		sha, ref, found := strings.Cut(line, "\t")
		if !found || !validCommitSHA(sha) {
			return "", "", errors.New("无法读取远端分支提交")
		}
		if ref == sourceRef {
			source = sha
		}
		if ref == targetRef {
			target = sha
		}
	}
	return source, target, nil
}

func changedTagBaseline(ref, expected, current string) error {
	if expected == "" {
		expected = "不存在"
	}
	if current == "" {
		current = "不存在"
	}
	return fmt.Errorf("%s 基线已变化，预期 %s，当前 %s。已保留冲突现场", ref, expected, current)
}
