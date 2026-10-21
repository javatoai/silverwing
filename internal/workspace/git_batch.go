package workspace

import (
	"context"
	"errors"
	"sync"

	"silverwing-go/internal/domain"
)

// Source WorkspaceGitOperationService defaults its IO dispatcher to four
// concurrent operations. Keep batch reads and independent repository groups
// under the same bound.
const workspaceGitBatchParallelism = 4

func runWorkspaceGitBatchPreflights(
	ctx context.Context,
	workspaces []domain.ServiceWorkspace,
	parallelism int,
	prepare func(context.Context, domain.ServiceWorkspace) (workspaceGitPreflight, error),
) ([]workspaceGitPreflight, error) {
	ctx = workspaceBatchContext(ctx)
	if len(workspaces) == 0 {
		return []workspaceGitPreflight{}, nil
	}
	if prepare == nil {
		return nil, errors.New("workspace Git batch preflight callback is nil")
	}
	workerCount := workspaceBatchWorkerCount(parallelism, len(workspaces))
	preflightCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int, len(workspaces))
	for index := range workspaces {
		jobs <- index
	}
	close(jobs)

	preflights := make([]workspaceGitPreflight, len(workspaces))
	var firstErr error
	var firstErrMu sync.Mutex
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if preflightCtx.Err() != nil {
					continue
				}
				preflight, err := prepare(preflightCtx, workspaces[index])
				if err != nil {
					firstErrMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					firstErrMu.Unlock()
					cancel()
					continue
				}
				preflights[index] = preflight
			}
		}()
	}
	workers.Wait()

	firstErrMu.Lock()
	preflightErr := firstErr
	firstErrMu.Unlock()
	if preflightErr != nil {
		return nil, preflightErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return preflights, nil
}

func runWorkspaceGitBatchGroups(
	ctx context.Context,
	preflights []workspaceGitPreflight,
	parallelism int,
	mode WorkspaceGitBatchMode,
	execute func(context.Context, workspaceGitPreflight) (WorkspaceGitBatchItemResult, error),
) (WorkspaceGitBatchResult, error) {
	ctx = workspaceBatchContext(ctx)
	if len(preflights) == 0 {
		return WorkspaceGitBatchResult{Items: []WorkspaceGitBatchItemResult{}}, ctx.Err()
	}
	if execute == nil {
		return WorkspaceGitBatchResult{}, errors.New("workspace Git batch execute callback is nil")
	}
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type indexedPreflight struct {
		index int
		value workspaceGitPreflight
	}
	type repositoryGroup struct {
		items []indexedPreflight
	}
	groups := make([]repositoryGroup, 0, len(preflights))
	groupIndexByLockKey := make(map[string]int, len(preflights))
	for index, preflight := range preflights {
		// This is the same canonical hash used by OperationLocks to derive its
		// cross-process repository lock path. Worktree aliases and Windows path
		// casing therefore share one in-batch serial group as well.
		lockKey := stablePathHash(preflight.Common)
		groupIndex, exists := groupIndexByLockKey[lockKey]
		if !exists {
			groupIndex = len(groups)
			groupIndexByLockKey[lockKey] = groupIndex
			groups = append(groups, repositoryGroup{})
		}
		groups[groupIndex].items = append(groups[groupIndex].items, indexedPreflight{index: index, value: preflight})
	}

	results := make([]WorkspaceGitBatchItemResult, len(preflights))
	completed := make([]bool, len(preflights))
	jobs := make(chan int, len(groups))
	for index := range groups {
		jobs <- index
	}
	close(jobs)
	workerCount := workspaceBatchWorkerCount(parallelism, len(groups))
	var firstErr error
	var firstErrMu sync.Mutex
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for groupIndex := range jobs {
				if executionCtx.Err() != nil {
					continue
				}
				group := groups[groupIndex]
				for _, indexed := range group.items {
					if executionCtx.Err() != nil {
						break
					}
					result, err := execute(executionCtx, indexed.value)
					results[indexed.index] = result
					completed[indexed.index] = true
					if err != nil {
						firstErrMu.Lock()
						if firstErr == nil {
							// Preserve an earlier caller cancellation, or the first
							// execution error before cancelling sibling work.
							firstErr = ctx.Err()
							if firstErr == nil {
								firstErr = err
							}
							cancel()
						}
						firstErrMu.Unlock()
						break
					}
				}
			}
		}()
	}
	workers.Wait()

	firstErrMu.Lock()
	executionErr := firstErr
	firstErrMu.Unlock()
	if executionErr == nil {
		executionErr = ctx.Err()
	}
	if executionErr != nil {
		for index, preflight := range preflights {
			if !completed[index] {
				results[index] = workspaceGitBatchCancelledResult(preflight.Workspace, mode)
			}
		}
		return WorkspaceGitBatchResult{Items: results}, executionErr
	}
	return WorkspaceGitBatchResult{Items: results}, nil
}

func workspaceGitBatchCancelledResult(workspace domain.ServiceWorkspace, mode WorkspaceGitBatchMode) WorkspaceGitBatchItemResult {
	result := WorkspaceGitBatchItemResult{
		WorkspacePath: WorkspaceGitPathKey(workspace),
		ServiceName:   workspaceOperationName(workspace),
		Branch:        workspace.Branch,
		CommitState:   WorkspaceGitNotRun,
		PushState:     WorkspaceGitNotRun,
		Message:       "批量 Git 操作已取消，尚未开始；可仅重试失败项",
	}
	if mode == WorkspaceGitBatchPush {
		result.PushState = WorkspaceGitFailed
	} else {
		result.CommitState = WorkspaceGitFailed
	}
	return result
}

func workspaceBatchContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func workspaceBatchWorkerCount(parallelism, jobs int) int {
	if jobs <= 0 {
		return 0
	}
	if parallelism <= 0 || parallelism > workspaceGitBatchParallelism {
		parallelism = workspaceGitBatchParallelism
	}
	if parallelism > jobs {
		parallelism = jobs
	}
	return parallelism
}
