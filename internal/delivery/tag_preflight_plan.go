package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

// TagBatchPreflight reserves the operation IDs before any worktree is created.
// A successful plan is immutable. The same ID becomes the real batch ID.
type TagBatchPreflight struct {
	ID           string
	TaskIdentity string
	AllServices  bool
	Ready        bool
	Started      bool
	Items        []TagBatchPreflightItem
}

type TagBatchPreflightItem struct {
	SelectionKey string
	OperationID  string
	Preview      TagPreflight
	Worktree     string
	BlockedBy    string
	Error        string

	Pins         tagPlanPins
	BeforeSource string
	BeforeTarget string
	Commit       string
	DependsOn    []string
}

type tagPlanPins struct {
	Repository   string
	Worktree     string
	RemoteKey    string
	Identity     string
	Source       string
	RemoteSource string
	RemoteTarget string
}

type tagPlanHead struct {
	sha, repository, operation string
}

func (t *TagTask) PrepareBatch(ctx context.Context, id string, keys []string, all bool) (TagBatchPreflight, error) {
	var plan TagBatchPreflight
	err := t.WithLock(func() error {
		var err error
		if id == "" {
			plan = TagBatchPreflight{ID: newUUID(), TaskIdentity: tagPlanTaskIdentity(t.task), AllServices: all}
			for _, key := range keys {
				target, err := domain.ResolveEffectiveTagTarget(t.task.config, t.task.manifest, key)
				if err != nil {
					return err
				}
				plan.Items = append(plan.Items, TagBatchPreflightItem{SelectionKey: key, OperationID: newUUID(),
					Preview: tagPlanPreview(t.task.manifest, target)})
			}
			if err := t.checkTagPlanScope(ctx, plan); err != nil {
				return err
			}
			if err := t.service.saveTagPlan(plan); err != nil {
				return err
			}
		} else {
			plan, err = t.loadTagPlan(ctx, id, keys, all)
			if err != nil {
				return err
			}
			if plan.Ready {
				if err := t.validateTagPlan(ctx, plan); err != nil {
					plan.Items = nil
					return err
				}
				return nil
			}
		}
		heads := map[string]tagPlanHead{}
		plan.Ready = true
		for i := range plan.Items {
			item := &plan.Items[i]
			item.Error, item.BlockedBy = "", ""
			item.Preview.ConflictFiles = nil
			if err := t.prepareTagPlanItem(ctx, item, heads); err != nil {
				item.Error = err.Error()
				plan.Ready = false
			}
			if ctx.Err() != nil {
				plan.Ready = false
				return errors.Join(ctx.Err(), t.service.saveTagPlan(plan))
			}
		}
		if err := t.checkTagPlanScope(ctx, plan); err != nil {
			plan.Ready = false
			return errors.Join(err, t.service.saveTagPlan(plan))
		}
		return t.service.saveTagPlan(plan)
	})
	return plan, err
}

func (t *TagTask) prepareTagPlanItem(ctx context.Context, item *TagBatchPreflightItem, heads map[string]tagPlanHead) error {
	return t.withTagPlanTarget(ctx, item.SelectionKey, func(target domain.EffectiveTagTarget, repo mutationTarget) error {
		s := t.service
		pins, err := s.tagPlanInputs(ctx, target, repo, true)
		if err != nil {
			return err
		}
		if item.Pins.Identity != "" && item.Pins != pins {
			return tagPlanChanged(item.SelectionKey, item.Pins, pins)
		}
		item.Pins = pins
		sourceKey, targetKey := tagPlanBranchKeys(pins, target)
		beforeSource := heads[sourceKey]
		beforeTarget := heads[targetKey]
		if beforeSource.operation == "" {
			beforeSource.sha = pins.RemoteSource
		}
		if beforeTarget.operation == "" {
			beforeTarget.sha = pins.RemoteTarget
		}
		item.DependsOn = nil
		for _, previous := range []tagPlanHead{beforeSource, beforeTarget} {
			if previous.operation != "" {
				item.DependsOn = appendUnique(item.DependsOn, previous.operation)
			}
		}
		// Later items on this branch cannot pass until this item has a result.
		heads[sourceKey] = tagPlanHead{operation: item.OperationID}
		if targetKey != "" {
			heads[targetKey] = tagPlanHead{operation: item.OperationID}
		}
		for _, previous := range []tagPlanHead{beforeSource, beforeTarget} {
			if previous.operation != "" && previous.sha == "" {
				item.BlockedBy = previous.operation
				return errors.New("请先完成同一分支的前序预检")
			}
			if err := s.importTagPlanHead(ctx, repo.repository, previous); err != nil {
				return err
			}
		}
		if item.Commit != "" && (item.BeforeSource != beforeSource.sha || item.BeforeTarget != beforeTarget.sha) {
			return errors.New("前序合并结果已变化，请重新预检")
		}
		item.BeforeSource, item.BeforeTarget = beforeSource.sha, beforeTarget.sha
		preview, err := s.describeTagPlan(ctx, t.task, target, repo.repository, pins.Source, item.BeforeSource, item.BeforeTarget)
		item.Preview = preview
		if err != nil {
			return err
		}
		if err := s.requireTagBranchesAllowed(t.task.config, target, preview); err != nil {
			return err
		}
		commit := pins.Source
		if targetKey != "" {
			commit, err = s.prepareTagPlanMerge(ctx, item, repo.repository)
			if err != nil {
				return err
			}
		}
		item.Commit = commit
		if err := s.checkApprovedCommit(ctx, *item, target, repo.repository); err != nil {
			return err
		}
		if targetKey != "" {
			heads[targetKey] = tagPlanHead{commit, repo.repository, item.OperationID}
		}
		heads[sourceKey] = tagPlanHead{pins.Source, repo.repository, item.OperationID}
		return nil
	})
}

func (t *TagTask) withTagPlanTarget(ctx context.Context, key string, run func(domain.EffectiveTagTarget, mutationTarget) error) error {
	target, err := domain.ResolveEffectiveTagTarget(t.task.config, t.task.manifest, key)
	if err != nil {
		return err
	}
	repo, err := t.service.validateForMutation(ctx, t.task, target.Workspace)
	if err != nil {
		return err
	}
	return t.service.withRepositoryLock(ctx, repo.repository, func() error { return run(target, repo) })
}

func (s *Service) tagPlanInputs(ctx context.Context, target domain.EffectiveTagTarget, repo mutationTarget, fetch bool) (tagPlanPins, error) {
	if err := s.ensureCleanFeatureWorktree(ctx, repo.worktree); err != nil {
		return tagPlanPins{}, err
	}
	endpoint, err := s.readTagWriteEndpoint(ctx, repo.repository, target.Remote)
	if err != nil {
		return tagPlanPins{}, err
	}
	identity := endpoint.identity()
	ctx = withTagRemoteIdentity(ctx, identity, endpoint.key)
	if fetch {
		if err := s.fetch(ctx, repo.repository, target.Remote); err != nil {
			return tagPlanPins{}, err
		}
		if err := s.fetchTags(ctx, repo.repository, target.Remote); err != nil {
			return tagPlanPins{}, err
		}
	}
	sourceRef, targetRef := tagPlanRefs(target)
	if sourceRef == targetRef {
		return tagPlanPins{}, errors.New("源分支和目标分支相同，请检查 Tag 配置")
	}
	source, err := s.resolve(ctx, repo.repository, sourceRef)
	if err != nil {
		return tagPlanPins{}, err
	}
	if err := s.requireTagRemoteIdentity(ctx, repo.repository, target.Remote, identity); err != nil {
		return tagPlanPins{}, err
	}
	remoteSource, remoteTarget, err := s.tagRemoteBranchTips(ctx, repo.repository, target.Remote, sourceRef, targetRef)
	if err != nil {
		return tagPlanPins{}, err
	}
	if targetRef != "" && remoteTarget == "" {
		return tagPlanPins{}, fmt.Errorf("目标分支 %s 不存在", value(target.TargetBranch))
	}
	common, err := s.gitCommonDirectory(ctx, repo.repository)
	return tagPlanPins{canonicalPath(common), canonicalPath(repo.worktree), endpoint.key, identity, source, remoteSource, remoteTarget}, err
}

func tagPlanRefs(target domain.EffectiveTagTarget) (source, destination string) {
	if target.TargetBranch != nil {
		destination = "refs/heads/" + *target.TargetBranch
	}
	return "refs/heads/" + target.SourceBranch, destination
}

func tagPlanBranchKeys(pins tagPlanPins, target domain.EffectiveTagTarget) (source, destination string) {
	sourceRef, targetRef := tagPlanRefs(target)
	if targetRef != "" {
		destination = pins.RemoteKey + "\x00" + targetRef
	}
	return pins.RemoteKey + "\x00" + sourceRef, destination
}

func (s *Service) importTagPlanHead(ctx context.Context, repository string, head tagPlanHead) error {
	if head.repository == "" || samePath(repository, head.repository) {
		return nil
	}
	if existing, err := s.resolve(ctx, repository, head.sha); err == nil && existing == head.sha {
		return nil
	}
	_, err := s.run(ctx, repository, 5*time.Minute, true, false, "fetch", "--no-tags", "--no-write-fetch-head", "--", head.repository, head.sha)
	return err
}

func (s *Service) describeTagPlan(ctx context.Context, task resolvedTask, target domain.EffectiveTagTarget, repository, source, remoteSource, base string) (TagPreflight, error) {
	sync, err := s.featureSync(ctx, repository, source, remoteSource)
	preview := tagPlanPreview(task.manifest, target)
	preview.SourceSHA, preview.TargetSHA, preview.SourceSync = source, nonEmptyPointer(base), sync
	if err != nil {
		return preview, err
	}
	if remoteSource != "" && !sync.RemoteExists {
		return preview, errors.New("远端源分支在获取期间变化，请重新预检")
	}
	if !sync.CanPush() {
		return preview, fmt.Errorf("远端源分支领先本地 %d 个提交，请先手工同步", sync.Behind)
	}
	rangeRef, tagBase := source, source
	if base != "" {
		rangeRef, tagBase = base+".."+source, base
		merged, err := s.isAncestor(ctx, repository, source, base)
		if err != nil {
			return preview, err
		}
		fastForward, err := s.isAncestor(ctx, repository, base, source)
		if err != nil {
			return preview, err
		}
		switch {
		case merged:
			preview.MergeMode = stringPointer("ALREADY_MERGED")
		case fastForward:
			preview.MergeMode = stringPointer("FAST_FORWARD")
		default:
			preview.MergeMode = stringPointer("MERGE_COMMIT")
		}
		diff, err := s.run(ctx, repository, 10*time.Minute, true, true, "diff", "--stat", base, source)
		if err != nil {
			return preview, err
		}
		preview.DiffStat = strings.TrimSpace(diff.Stdout)
	} else if remoteSource != "" {
		rangeRef = remoteSource + ".." + source
	}
	commits, err := s.run(ctx, repository, 10*time.Minute, true, true, "log", "--format=%h %s", "--no-merges", rangeRef)
	if err != nil {
		return preview, err
	}
	preview.CommitList = nonEmptyLines(commits.Stdout)
	preview.EstimatedTag, err = s.nextTag(ctx, repository, tagBase)
	return preview, err
}

func tagPlanPreview(manifest domain.TaskManifest, target domain.EffectiveTagTarget) TagPreflight {
	return TagPreflight{FolderName: manifest.FolderName, ServiceName: target.Workspace.ServiceName,
		SourceBranch: target.SourceBranch, Remote: target.Remote, TargetBranch: target.TargetBranch,
		TagMode: target.Mode, CommitList: []string{}}
}

func (s *Service) prepareTagPlanMerge(ctx context.Context, item *TagBatchPreflightItem, repository string) (string, error) {
	source, base := item.Pins.Source, item.BeforeTarget
	switch value(item.Preview.MergeMode) {
	case "ALREADY_MERGED":
		return base, nil
	case "FAST_FORWARD":
		return source, nil
	}
	path, err := s.conflictWorktreePath(item.OperationID)
	if err != nil {
		return "", err
	}
	recovered, err := s.recoverTagWorktree(ctx, repository, path, base, source)
	if err != nil {
		return "", err
	}
	if item.Worktree != "" && (!samePathSpelling(path, item.Worktree) || !recovered.exists) {
		return "", fmt.Errorf("预检工作区已变化或丢失：%s", path)
	}
	if item.Commit != "" && recovered.mergedSHA != item.Commit {
		return "", fmt.Errorf("预检工作区的合并结果已变化，请重新预检：%s", path)
	}
	var commit string
	if recovered.conflict != nil && len(recovered.conflict.files) == 0 {
		commit, err = s.commitTagConflict(ctx, path, source, base, "")
	} else if recovered.conflict != nil {
		err = recovered.conflict
	} else {
		commit, err = s.prepareTagTargetMerge(ctx, repository, path, base, source, recovered)
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		item.Worktree = path
	}
	if err != nil {
		var conflict *mergeConflictError
		if errors.As(err, &conflict) {
			item.Preview.ConflictFiles = conflict.files
			preview := item.Preview
			return "", errors.New(mergeConflictMessage(preview.ServiceName, preview.SourceBranch, preview.Remote+"/"+value(preview.TargetBranch)))
		}
		return "", fmt.Errorf("预检工作区 %s：%w", path, err)
	}
	return commit, nil
}

func tagPlanChanged(key string, before, after tagPlanPins) error {
	switch {
	case before.Source != after.Source:
		return changedTagBaseline(key+" 本地源分支", before.Source, after.Source)
	case before.RemoteSource != after.RemoteSource:
		return changedTagBaseline(key+" 远端源分支", before.RemoteSource, after.RemoteSource)
	case before.RemoteTarget != after.RemoteTarget:
		return changedTagBaseline(key+" 远端目标分支", before.RemoteTarget, after.RemoteTarget)
	default:
		return fmt.Errorf("%s 的仓库、工作区或推送地址已变化，请重新预检", key)
	}
}

func tagPlanTaskIdentity(task resolvedTask) string {
	data, _ := json.Marshal([]any{canonicalPath(task.directory), task.config, task.manifest})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (t *TagTask) checkTagPlanScope(ctx context.Context, plan TagBatchPreflight) error {
	cfg, err := t.service.config.LoadForPreview(ctx)
	if err != nil {
		return err
	}
	manifest, err := t.service.manifests.Load(t.task.directory)
	if err != nil {
		return err
	}
	current := resolvedTask{cfg, t.task.directory, manifest}
	if plan.TaskIdentity != tagPlanTaskIdentity(current) || manifest.LifecycleStatus == domain.TaskArchived {
		return errors.New("任务、服务选择或设置已变化，请重新预检")
	}
	return nil
}

func (t *TagTask) loadTagPlan(ctx context.Context, id string, keys []string, all bool) (TagBatchPreflight, error) {
	plan, err := t.service.readTagPlan(id)
	if err != nil {
		return plan, err
	}
	if err := t.checkTagPlanScope(ctx, plan); err != nil {
		return TagBatchPreflight{}, err
	}
	if plan.Started {
		return TagBatchPreflight{}, fmt.Errorf("预检编号 %s 已用于批次，请查询批次状态或重试原操作", id)
	}
	var saved []string
	for _, item := range plan.Items {
		saved = append(saved, item.SelectionKey)
	}
	if !slices.Equal(keys, saved) || all != plan.AllServices {
		return TagBatchPreflight{}, errors.New("服务选择与预检编号不一致，请重新预检")
	}
	return plan, nil
}

func (s *Service) tagPlanPath(id string) (string, error) {
	if !validOperationID(id) || s.paths.Home == "" {
		return "", errors.New("预检编号无效")
	}
	root := filepath.Join(canonicalPath(s.paths.Home), "tag-preflights")
	if info, err := os.Lstat(root); err == nil {
		real, err := filepath.EvalSymlinks(root)
		if err != nil || !info.IsDir() || !samePathSpelling(real, root) {
			return "", errors.New("预检记录目录不安全")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Join(root, id+".json"), nil
}

func (s *Service) saveTagPlan(plan TagBatchPreflight) error {
	path, err := s.tagPlanPath(plan.ID)
	if err != nil {
		return err
	}
	return writeAtomicJSON(path, plan)
}

func (s *Service) readTagPlan(id string) (TagBatchPreflight, error) {
	var plan TagBatchPreflight
	path, err := s.tagPlanPath(id)
	if err != nil {
		return plan, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return plan, fmt.Errorf("找不到有效的预检记录 %s", id)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return plan, err
	}
	if err := json.Unmarshal(data, &plan); err != nil || plan.ID != id || len(plan.Items) == 0 {
		return TagBatchPreflight{}, fmt.Errorf("预检记录 %s 已损坏", id)
	}
	return plan, nil
}
