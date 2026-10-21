package desktop

import (
	"errors"
	"fmt"

	"silverwing-go/internal/application"
)

// Completion runs on the UI thread before applying the refreshed snapshot.
// Backend entry alone is not proof that a migration committed or rolled back.
// Release old save keys only before dispatch or after fresh original-directory
// and recovery-checkpoint evidence. Pending recovery keeps the original lease.
func completeMarkdownTaskRootDispatch(lease *markdownTaskRootMigrationLease, result application.ActionResult, dispatched bool, snapshot application.Snapshot, snapshotErr, operationErr error) (bool, error) {
	if lease == nil {
		return false, nil
	}
	if !dispatched {
		lease.Abandon(false)
		return false, nil
	}
	_, committed, resultErr := lease.plan.committedResult(result)
	if resultErr != nil {
		pauseErr := lease.retainForTaskRootRecovery()
		return false, fmt.Errorf("无法确认迁移结果，Markdown 自动保存已暂停：%w", errors.Join(resultErr, pauseErr))
	}
	if committed {
		return true, lease.Complete(result)
	}
	if markdownTaskRootOriginalState(lease.plan, snapshot, snapshotErr) {
		lease.Abandon(false)
		if operationErr == nil {
			return false, fmt.Errorf("任务根迁移未确认提交，请重新预览")
		}
		return false, nil
	}
	pauseErr := lease.retainForTaskRootRecovery()
	if snapshotErr != nil {
		return false, fmt.Errorf("无法读取迁移后的状态，Markdown 自动保存已暂停：%w", errors.Join(snapshotErr, pauseErr))
	}
	return false, errors.Join(errors.New("任务目录恢复尚未确认完成，Markdown 自动保存已暂停；请完成目录恢复后重试"), pauseErr)
}

// Window destruction cannot mutate presentation maps. This only releases or
// fails closed the thread-safe save lease using immutable result evidence.
func abandonMarkdownTaskRootDispatch(lease *markdownTaskRootMigrationLease, result application.ActionResult, dispatched bool, snapshot application.Snapshot, snapshotErr error) {
	if lease == nil {
		return
	}
	unknownOrCommitted := dispatched
	if dispatched {
		_, committed, err := lease.plan.committedResult(result)
		if err == nil && !committed && markdownTaskRootOriginalState(lease.plan, snapshot, snapshotErr) {
			unknownOrCommitted = false
		}
	}
	lease.Abandon(unknownOrCommitted)
}
