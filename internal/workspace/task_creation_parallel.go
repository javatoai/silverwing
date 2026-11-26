package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"silverwing-go/internal/domain"
)

type taskCreationService struct {
	effectiveTaskService
	repository domain.RepositoryConfig
	key        string
}

func (s *TaskApplicationService) taskCreationServices(ctx context.Context, config domain.AppConfig, selections []effectiveTaskService) ([]taskCreationService, error) {
	services := make([]taskCreationService, 0, len(selections))
	commonByRoot := make(map[string]string, len(selections))
	for _, selection := range selections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		repository, ok := repositoryByID(config, selection.Service.RepositoryID)
		if !ok {
			return nil, fmt.Errorf("服务 %s 的仓库配置不存在", selection.Service.DisplayName)
		}
		common, ok := commonByRoot[repository.RootPath]
		if !ok {
			var err error
			common, err = gitCommonDirectoryContext(ctx, s.Git, repository.RootPath)
			if err != nil {
				return nil, fmt.Errorf("检查服务 %s 的 Git 公共目录失败：%w", selection.Service.DisplayName, err)
			}
			commonByRoot[repository.RootPath] = common
		}
		if !samePath(common, repository.GitCommonDirectory) {
			return nil, fmt.Errorf("服务 %s 的仓库 Git 身份已变化：%s", selection.Service.DisplayName, repository.RootPath)
		}
		services = append(services, taskCreationService{selection, repository, stablePathHash(common)})
	}
	return services, nil
}

// One worker owns each Git common directory. The caller collects results by
// selection order after all workers, including any local rollback, return.
func runTaskCreationRepositories(ctx context.Context, services []taskCreationService, run func(context.Context, int) error) error {
	groups := make(map[string][]int, len(services))
	for index, service := range services {
		groups[service.key] = append(groups[service.key], index)
	}
	batch, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var workers sync.WaitGroup
	for _, group := range groups {
		if batch.Err() != nil {
			break
		}
		workers.Go(func() {
			for _, index := range group {
				if batch.Err() != nil {
					return
				}
				if err := run(batch, index); err != nil {
					cancel(err)
					return
				}
			}
		})
	}
	workers.Wait()
	return context.Cause(batch)
}

func (s *TaskApplicationService) inspectNewTaskBranchReuse(ctx context.Context, config domain.AppConfig, selections []effectiveTaskService, branch string, onProgress func(TaskCreationProgress)) ([]BranchReuseConflict, error) {
	services, err := s.taskCreationServices(ctx, config, selections)
	if err != nil {
		return nil, err
	}
	results := make([][]BranchReuseConflict, len(services))
	err = runTaskCreationRepositories(ctx, services, func(ctx context.Context, index int) error {
		service := services[index]
		var inspectErr error
		results[index], inspectErr = s.inspectServiceBranchReuse(ctx, service.repository, service.effectiveTaskService, branch, taskCreationWorkerProgress(ctx, onProgress))
		return inspectErr
	})
	if err != nil {
		return nil, err
	}
	var conflicts []BranchReuseConflict
	for _, result := range results {
		conflicts = append(conflicts, result...)
	}
	return uniqueBranchReuseConflicts(conflicts), nil
}

func (s *TaskApplicationService) provisionNewTask(ctx context.Context, config domain.AppConfig, selections []effectiveTaskService, request CreateTaskRequest, taskPath, branch string, owned os.FileInfo, onProgress func(TaskCreationProgress)) (workspaces []domain.ServiceWorkspace, cleanupErr error, err error) {
	services, err := s.taskCreationServices(ctx, config, selections)
	if err != nil {
		return nil, nil, err
	}
	results := make([][]domain.ServiceWorkspace, len(services))
	cleanupFailures := make([]error, len(services))
	onOutput := serialTaskCreationObserver(s.OnOutput)
	provision := func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyOwnedTaskDirectory(taskPath, owned); err != nil {
			return err
		}
		service := services[index]
		created, err := s.Provisioning.ProvisionWithProgress(ctx, WorkspaceProvisionRequest{
			TaskDirectory: taskPath, Repository: service.repository, Service: service.Service,
			RequestedFeatureBranch: branch, ConfirmedBranchReuseKeys: request.ConfirmedBranchReuseKeys,
			ModuleBranches: service.ModuleBranches, ModuleSources: service.ModuleSources,
			taskDirectoryOwner: owned, deferWorkspaceRollback: true,
		}, onOutput, taskCreationWorkerProgress(ctx, onProgress))
		results[index] = created
		var cleanup *workspaceProvisionCleanupError
		if errors.As(err, &cleanup) {
			cleanupFailures[index] = fmt.Errorf("服务 %s：%w", service.Service.DisplayName, err)
		}
		return err
	}
	for start := 0; start < len(services) && err == nil; {
		if taskCreationHasInitialization(services[start].Service.Bootstrap) {
			// User initialization keeps the order relative to all other services.
			err = provision(ctx, start)
			start++
			continue
		}
		end := start + 1
		for end < len(services) && !taskCreationHasInitialization(services[end].Service.Bootstrap) {
			end++
		}
		err = runTaskCreationRepositories(ctx, services[start:end], func(ctx context.Context, index int) error {
			return provision(ctx, start+index)
		})
		start = end
	}
	for _, result := range results {
		workspaces = append(workspaces, result...)
	}
	return workspaces, errors.Join(cleanupFailures...), err
}

func taskCreationHasInitialization(bootstrap domain.BootstrapConfig) bool {
	if len(bootstrap.CopyRules) > 0 {
		return true
	}
	for _, command := range bootstrap.Commands {
		if command.Enabled {
			return true
		}
	}
	return false
}

func serialTaskCreationObserver[T any](observe func(T)) func(T) {
	if observe == nil {
		return nil
	}
	var lock sync.Mutex
	return func(event T) {
		lock.Lock()
		defer lock.Unlock()
		observe(event)
	}
}

func taskCreationWorkerProgress(ctx context.Context, observe func(TaskCreationProgress)) func(TaskCreationProgress) {
	if observe == nil {
		return nil
	}
	return func(event TaskCreationProgress) {
		if event.Status == TaskCreationProgressFailed && event.Phase != TaskCreationPhaseRollback && ctx.Err() != nil {
			event.Status = TaskCreationProgressCancelled
			event.Message = "已中断"
		}
		observe(event)
	}
}
