package integrations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
)

// The actual gateway, remote checkpoint, local source and receipt IO are
// injectable boundaries. Every implementation uses the same coordinator.
type materialsResourceWriter interface {
	materialsOperations(domain.MaterialsSyncPlan, localMaterialScan, remoteMaterialsState) (materialsResourceOperations, error)
}

func (s *Service) applyMaterialsResourceWrites(
	ctx context.Context,
	gateway materialsResourceWriter,
	config domain.AppConfig,
	plan domain.MaterialsSyncPlan,
	approval domain.MaterialsSyncApproval,
	scan localMaterialScan,
	state remoteMaterialsState,
	result *domain.MaterialsSyncResult,
	notify func(),
) (remoteMaterialsState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if notify == nil {
		notify = func() {}
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	ops, err := gateway.materialsOperations(plan, scan, state)
	if err != nil {
		return state, err
	}
	coordinator := &materialsWriteCoordinator{ctx: ctx, service: s, config: config, plan: plan, approval: approval,
		scan: scan, state: remoteMaterialsState{control: state.control, revision: state.revision, root: state.root, value: cloneMaterialsManifest(state.value)},
		result: result, notify: notify, ops: ops, failed: map[string]bool{}, completed: map[string]bool{}, created: map[string]bool{}}
	err = coordinator.run()
	result.Progress.ActivePaths = []string{}
	if err != nil {
		if materialsSyncCancellation(err, ctx.Err(), err) {
			result.Cancelled = true
			result.Messages = append(result.Messages, "同步已取消；已完成记录和本地回执已保留，下次可继续")
		} else if materialsFatalWrite(err) {
			coordinator.failure(result.Progress.CurrentPath, result.Progress.Phase, err)
			result.Messages = append(result.Messages, "同步状态或 CLI 退出无法确认，已停止后续写入和删除；请重试恢复检查。")
		}
	}
	result.Progress.Phase = domain.MaterialsSyncComplete
	result.Progress.CurrentPath = ""
	result.Progress.Sequence++
	notify()
	return coordinator.state, err
}

func (c *materialsWriteCoordinator) run() error {
	binding, exists := c.state.value.Bindings[c.plan.SourceID]
	if !exists || binding.Entries == nil {
		return materialsManifestStateFailure(errors.New("同步绑定或登记清单不存在"))
	}
	if _, exists := binding.Entries[materialsFilesRoot]; !exists {
		c.result.Progress.CurrentDepth = -1
		if err := c.createLevel([]localMaterial{{path: materialsFilesRoot, kind: domain.MaterialsSyncDirectory}}); err != nil {
			return err
		}
	}
	executable := map[string]domain.MaterialsSyncItem{}
	for _, item := range c.plan.Items {
		_, overwrite := c.approval.OverwritePaths[item.Path]
		if item.Path != materialsRequirementRoot && item.Path != materialsFilesRoot &&
			(item.Action == domain.MaterialsSyncCreate || item.Action == domain.MaterialsSyncUpdate || item.Action == domain.MaterialsSyncConflict && overwrite) {
			executable[item.Path] = item
		}
	}
	creates := map[int][]localMaterial{}
	updates := map[int][]domain.MaterialsSyncItem{}
	for _, path := range sortedKeys(executable) {
		item := executable[path]
		file, exists := c.scan.files[path]
		if !exists {
			c.failure(path, domain.MaterialsSyncVerify, errors.New("资料已没有对应的本地文件"))
			continue
		}
		depth := strings.Count(path, "/")
		if _, exists := c.state.value.Bindings[c.plan.SourceID].Entries[path]; !exists {
			creates[depth] = append(creates[depth], file)
		}
		if file.kind != domain.MaterialsSyncDirectory {
			updates[depth] = append(updates[depth], item)
		}
	}
	// All resource kinds at one depth share a creation wave. A deeper wave waits
	// for its successful parents' complete registration checkpoint.
	for _, depth := range materialsSortedDepths(creates) {
		c.result.Progress.CurrentDepth = depth
		if err := c.createLevel(creates[depth]); err != nil {
			return err
		}
	}
	// Every reference identity, including cycles, is registered before bodies.
	for _, depth := range materialsSortedDepths(updates) {
		c.result.Progress.CurrentDepth = depth
		if err := c.updateLevel(updates[depth]); err != nil {
			return err
		}
	}
	for _, item := range c.plan.Items {
		if item.Action == domain.MaterialsSyncSkip {
			c.skipped(localMaterial{path: item.Path, kind: item.Kind}, "本地内容与已登记版本一致", item.LocalHash != "")
		} else if item.Action == domain.MaterialsSyncConflict {
			if _, approved := c.approval.OverwritePaths[item.Path]; !approved {
				c.result.Progress.Conflicts++
				reason := "存在未确认的远端冲突"
				if item.Reason != nil && strings.TrimSpace(*item.Reason) != "" {
					reason = *item.Reason
				}
				c.result.Messages = append(c.result.Messages, item.Path+"："+reason)
				c.result.Progress.Sequence++
				c.notify()
			}
		}
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if err := c.ops.verifyScan(); err != nil {
		c.failure("本地资料", domain.MaterialsSyncVerify, err)
	}
	if len(c.failed) > 0 || len(c.scan.blocked) > 0 {
		c.result.Messages = append(c.result.Messages, "资料写入失败或扫描不完整，已跳过删除；已完成项和恢复记录均已保留。")
		if len(c.failures) > 0 {
			return fmt.Errorf("部分资料同步失败；已登记成功的项目会在重试时跳过：%s", strings.Join(c.failures, "；"))
		}
		return errors.New("资料存在未成功登记的依赖项或阻止同步的路径，已停止删除")
	}
	// Source intentionally keeps deletes sequential and stops on the first error.
	deletions := append([]domain.MaterialsSyncItem(nil), c.plan.Items...)
	sort.SliceStable(deletions, func(i, j int) bool {
		left, right := strings.Count(deletions[i].Path, "/"), strings.Count(deletions[j].Path, "/")
		if left != right {
			return left > right
		}
		return deletions[i].Path < deletions[j].Path
	})
	c.result.Progress.CurrentDepth = -1
	for _, item := range deletions {
		if item.Action != domain.MaterialsSyncDelete {
			continue
		}
		if _, approved := c.approval.DeletionPaths[item.Path]; !approved {
			c.skipped(localMaterial{path: item.Path, kind: item.Kind}, "待确认删除", false)
			continue
		}
		c.result.Progress.Phase, c.result.Progress.CurrentPath = domain.MaterialsSyncDeletePhase, item.Path
		c.result.Progress.ActivePaths = []string{item.Path}
		c.result.Progress.Sequence++
		c.notify()
		if err := c.deleteOne(item); err != nil {
			if materialsFatalWrite(err) {
				return err
			}
			c.failure(item.Path, domain.MaterialsSyncDeletePhase, err)
			c.result.Messages = append(c.result.Messages, "删除失败，剩余删除项已保留供重新预览。")
			return fmt.Errorf("部分资料删除失败：%w", err)
		}
		c.result.Progress.ActivePaths = []string{}
		c.result.Progress.Sequence++
		c.notify()
	}
	return c.ctx.Err()
}

// A failed manifest save may have reached the remote control document even
// when its readback failed. Do not issue another write against the stale
// revision; the next run must reopen the manifest and recover its intent.
type materialsManifestStateError struct{ cause error }

func (e *materialsManifestStateError) Error() string { return e.cause.Error() }

func (e *materialsManifestStateError) Unwrap() error { return e.cause }

func materialsManifestStateFailure(err error) error {
	if err == nil {
		return nil
	}
	var existing *materialsManifestStateError
	if errors.As(err, &existing) {
		return err
	}
	return &materialsManifestStateError{cause: err}
}

func isMaterialsManifestStateError(err error) bool {
	var stateErr *materialsManifestStateError
	return errors.As(err, &stateErr)
}

func materialsOverwriteItem(plan domain.MaterialsSyncPlan, path string) bool {
	for _, item := range plan.Items {
		if item.Path == path && domain.MaterialsSyncItemCanOverwrite(item) {
			return true
		}
	}
	return false
}

func validateMaterialsApproval(plan domain.MaterialsSyncPlan, approval domain.MaterialsSyncApproval) error {
	if approval.PlanID != plan.ID {
		return errors.New("同步确认记录与当前计划不匹配")
	}
	deletions := map[string]bool{}
	for _, item := range plan.Items {
		if item.Action == domain.MaterialsSyncDelete {
			deletions[item.Path] = true
		}
	}
	for path := range approval.DeletionPaths {
		if !deletions[path] || path == materialsRequirementRoot || path == materialsFilesRoot {
			return fmt.Errorf("删除确认项不属于本次计划：%s", path)
		}
	}
	for path := range approval.OverwritePaths {
		if !materialsOverwriteItem(plan, path) || path == materialsRequirementRoot || path == materialsFilesRoot {
			return fmt.Errorf("覆盖确认项不属于可覆盖冲突：%s", path)
		}
	}
	return nil
}
