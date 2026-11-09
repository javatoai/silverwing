package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"silverwing-go/internal/domain"
)

// ApplyBatch consumes an immutable preflight. All items are checked and
// journaled before the first remote write, so a retry can recover by ID.
func (t *TagTask) ApplyBatch(ctx context.Context, id string, keys []string, all bool) ([]TagOperationReport, error) {
	var reports []TagOperationReport
	err := t.WithLock(func() error {
		plan, err := t.loadTagPlan(ctx, id, keys, all)
		if err != nil {
			return err
		}
		if err := t.validateTagPlan(ctx, plan); err != nil {
			return err
		}
		for _, item := range plan.Items {
			operation, err := t.createApprovedTagOperation(ctx, plan, item)
			if err != nil {
				return err
			}
			reports = append(reports, operationReport(operation, t.task.manifest.TaskDirectoryName))
		}
		if err := t.checkTagPlanScope(ctx, plan); err != nil {
			return err
		}
		plan.Started = true
		if err := t.service.saveTagPlan(plan); err != nil {
			return err
		}
		var executionErr error
		for i, item := range plan.Items {
			if err := ctx.Err(); err != nil {
				return errors.Join(executionErr, err)
			}
			op := reports[i].Operation
			err := t.runApprovedTag(ctx, plan, item, &op, false)
			reports[i] = operationReport(op, t.task.manifest.TaskDirectoryName)
			if err != nil {
				executionErr = errors.Join(executionErr, fmt.Errorf("%s：%w", item.SelectionKey, err))
			}
		}
		return executionErr
	})
	return reports, err
}

func (t *TagTask) validateTagPlan(ctx context.Context, plan TagBatchPreflight) error {
	if !plan.Ready {
		return errors.New("预检未全部通过，请处理原因并复检")
	}
	for _, item := range plan.Items {
		err := t.withTagPlanTarget(ctx, item.SelectionKey, func(target domain.EffectiveTagTarget, repo mutationTarget) error {
			pins, err := t.service.tagPlanInputs(ctx, target, repo, false)
			if err != nil {
				return err
			}
			if item.Pins != pins {
				return tagPlanChanged(item.SelectionKey, item.Pins, pins)
			}
			return t.service.checkApprovedCommit(ctx, item, target, repo.repository)
		})
		if err != nil {
			return fmt.Errorf("%s：%w", item.SelectionKey, err)
		}
	}
	return t.checkTagPlanScope(ctx, plan)
}

func (s *Service) checkApprovedCommit(ctx context.Context, item TagBatchPreflightItem, target domain.EffectiveTagTarget, repository string) error {
	if !validOperationID(item.OperationID) || !validCommitSHA(item.Commit) || !validCommitSHA(item.Pins.Source) {
		return errors.New("预检记录缺少有效提交，请重新预检")
	}
	if target.Mode == domain.TagBuildCurrentBranch {
		if item.Commit != item.Pins.Source {
			return errors.New("预检 Tag 提交与源分支不匹配")
		}
		return nil
	}
	if !validCommitSHA(item.BeforeTarget) {
		return errors.New("预检记录缺少目标分支基线")
	}
	if item.Worktree != "" {
		path, err := s.conflictWorktreePath(item.OperationID)
		if err != nil {
			return err
		}
		if !samePathSpelling(path, item.Worktree) {
			return errors.New("预检工作区路径与记录不一致")
		}
		recovered, err := s.recoverTagWorktree(ctx, repository, path, item.BeforeTarget, item.Pins.Source)
		if err != nil {
			return err
		}
		if recovered.mergedSHA != item.Commit {
			return fmt.Errorf("预检工作区的合并结果已变化，请重新预检：%s", path)
		}
		return nil
	}
	for _, parent := range []string{item.BeforeTarget, item.Pins.Source} {
		ok, err := s.isAncestor(ctx, repository, parent, item.Commit)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("预检合并结果与原分支基线不匹配")
		}
	}
	return nil
}

func (t *TagTask) createApprovedTagOperation(ctx context.Context, plan TagBatchPreflight, item TagBatchPreflightItem) (domain.TagOperation, error) {
	target, err := domain.ResolveEffectiveTagTarget(t.task.config, t.task.manifest, item.SelectionKey)
	if err != nil {
		return domain.TagOperation{}, err
	}
	w := target.Workspace
	now := formatSilverwingTime(time.Now())
	op := domain.TagOperation{OperationID: item.OperationID, FolderName: t.task.manifest.FolderName,
		ServiceName: w.ServiceName, RepositoryID: w.RepositoryID, SourceBranch: target.SourceBranch,
		Remote: target.Remote, TagMode: target.Mode, TargetBranch: target.TargetBranch,
		GroupServiceID: w.GroupServiceID, ModuleID: w.ModuleID, BatchID: &plan.ID, PreflightID: plan.ID,
		PreflightWorktree: item.Worktree,
		State:             domain.TagOperationCreated, SourceSHA: &item.Pins.Source, TargetSHA: nonEmptyPointer(item.BeforeTarget),
		CreatedAt: now, UpdatedAt: now, ConflictFiles: []string{}, GenbuStatus: initialGenbuStatus()}
	return op, t.service.store.save(ctx, t.task.directory, op)
}

func (t *TagTask) runApprovedTag(ctx context.Context, plan TagBatchPreflight, item TagBatchPreflightItem, op *domain.TagOperation, retry bool) (err error) {
	s := t.service
	defer func() {
		if err == nil || op.State == domain.TagOperationSuccess {
			return
		}
		cleanup, cancel := tagBuildCleanupContext(ctx)
		defer cancel()
		message := err.Error()
		if saveErr := s.transition(cleanup, t.task.directory, op, op.State, nil, nil, nil, &message, nil); saveErr != nil {
			err = errors.Join(err, saveErr)
		} else {
			err = errors.Join(err, s.recordTagHistory(cleanup, t.task.directory, *op))
		}
	}()
	if err := t.checkTagPlanScope(ctx, plan); err != nil {
		return err
	}
	for _, dependency := range item.DependsOn {
		previous, err := s.store.load(t.task.directory, dependency)
		if err != nil {
			return err
		}
		if previous.State != domain.TagOperationSuccess {
			op.BlockedBy = dependency
			message := "等待同一分支的前序操作完成：" + dependency
			return s.transition(ctx, t.task.directory, op, domain.TagOperationCreated, nil, nil, nil, &message, nil)
		}
	}
	op.BlockedBy = ""
	return t.withTagPlanTarget(ctx, item.SelectionKey, func(target domain.EffectiveTagTarget, repo mutationTarget) error {
		pins, err := s.tagPlanInputs(ctx, target, repo, false)
		if err != nil {
			return err
		}
		expected := item.Pins
		expected.RemoteSource, expected.RemoteTarget = item.BeforeSource, item.BeforeTarget
		actual := pins
		if retry && actual.RemoteSource == item.Pins.Source {
			actual.RemoteSource = expected.RemoteSource
		}
		if retry && target.Mode == domain.TagBuildMergeToTargetBranch && actual.RemoteTarget == item.Commit {
			actual.RemoteTarget = expected.RemoteTarget
		}
		if actual != expected {
			return tagPlanChanged(item.SelectionKey, expected, actual)
		}
		ctx = withTagRemoteIdentity(ctx, pins.Identity, pins.RemoteKey)
		if err := s.checkApprovedCommit(ctx, item, target, repo.repository); err != nil {
			return err
		}
		if err := s.requireTagBranchesAllowed(t.task.config, target, item.Preview); err != nil {
			return err
		}
		if err := s.fetchTags(ctx, repo.repository, target.Remote); err != nil {
			return err
		}
		if err := t.checkTagPlanScope(ctx, plan); err != nil {
			return err
		}
		if err := s.transition(ctx, t.task.directory, op, domain.TagOperationPreflightPassed, nil, nil, nil, stringPointer(""), nil); err != nil {
			return err
		}
		s.recordTagBuildStarted(*op)
		sourceRef, targetRef := tagPlanRefs(target)
		identity := item.Pins.Identity
		if err := s.pushPinnedTagBranch(ctx, repo.repository, target.Remote, identity, sourceRef, pins.RemoteSource, item.Pins.Source); err != nil {
			return err
		}
		if err := s.transition(ctx, t.task.directory, op, domain.TagOperationSourceBranchPushed, nil, nil, nil, nil, nil); err != nil {
			return err
		}
		if targetRef != "" {
			if err := s.pushPinnedTagBranch(ctx, repo.repository, target.Remote, identity, targetRef, pins.RemoteTarget, item.Commit); err != nil {
				return err
			}
			if err := s.transition(ctx, t.task.directory, op, domain.TagOperationTargetBranchPushed, nil, &item.Commit, nil, nil, nil); err != nil {
				return err
			}
		}
		if err := t.checkTagPlanScope(ctx, plan); err != nil {
			return err
		}
		if err := s.requireTagRemoteIdentity(ctx, repo.repository, target.Remote, identity); err != nil {
			return err
		}
		remoteSource, remoteTarget, err := s.tagRemoteBranchTips(ctx, repo.repository, target.Remote, sourceRef, targetRef)
		if err != nil {
			return err
		}
		if remoteSource != item.Pins.Source {
			return changedTagBaseline(target.Remote+"/"+target.SourceBranch, item.Pins.Source, remoteSource)
		}
		if targetRef != "" && remoteTarget != item.Commit {
			return changedTagBaseline(target.Remote+"/"+value(target.TargetBranch), item.Commit, remoteTarget)
		}
		if err := s.completeTagBuild(ctx, t.task, target, repo.repository, op, item.Commit); err != nil {
			return err
		}
		if item.Worktree != "" && op.State == domain.TagOperationSuccess {
			cleanup, cancel := tagBuildCleanupContext(ctx)
			defer cancel()
			// Only remove the still-verified, clean worktree. Preserve any
			// files the user added after confirming the preflight.
			if s.checkApprovedCommit(cleanup, item, target, repo.repository) == nil &&
				s.removeCleanTagWorktree(cleanup, repo.repository, item.Worktree) == nil {
				op.PreflightWorktree = ""
				return s.transition(cleanup, t.task.directory, op, op.State, nil, nil, nil, nil, nil)
			}
		}
		return nil
	})
}

func (s *Service) retryApprovedTag(ctx context.Context, task resolvedTask, op *domain.TagOperation) error {
	if op.State == domain.TagOperationSuccess {
		return errors.New("此操作的 Tag 已推送。如需重新打 Tag，请先创建新预检")
	}
	plan, err := s.readTagPlan(op.PreflightID)
	if err != nil {
		return err
	}
	if !plan.Started || plan.ID != value(op.BatchID) {
		return errors.New("原预检尚未确认执行，请按预检编号构建")
	}
	t := TagTask{service: s, task: task}
	for _, item := range plan.Items {
		if item.OperationID == op.OperationID {
			return t.runApprovedTag(ctx, plan, item, op, true)
		}
	}
	return errors.New("找不到此操作的原预检项目")
}
