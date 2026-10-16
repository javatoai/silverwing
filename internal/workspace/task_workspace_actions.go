package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

// RefreshAgents regenerates the ReferencedV2 task files while preserving user
// authored notes and restoring a missing task router, matching the source
// desktop action.
func (s *TaskApplicationService) RefreshAgents(config domain.AppConfig, taskPath string) (string, error) {
	var path string
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		path, err = s.Documents.EnsureTaskDocuments(taskPath, manifest, config)
		return err
	})
	return path, err
}

// InspectAddWorkspaceBranchReuse returns conflicts for the prospective service
// or module addition while holding the task lock and validating planned paths.
func (s *TaskApplicationService) InspectAddWorkspaceBranchReuse(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest) ([]BranchReuseConflict, error) {
	return s.inspectAddWorkspaceBranchReuse(ctx, config, taskPath, request, false, false)
}

func (s *TaskApplicationService) InspectAddServicesBranchReuse(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest) ([]BranchReuseConflict, error) {
	return s.inspectAddWorkspaceBranchReuse(ctx, config, taskPath, request, true, false)
}

func (s *TaskApplicationService) InspectAddModulesBranchReuse(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest) ([]BranchReuseConflict, error) {
	return s.inspectAddWorkspaceBranchReuse(ctx, config, taskPath, request, false, true)
}

func (s *TaskApplicationService) inspectAddWorkspaceBranchReuse(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest, addServices, addModules bool) ([]BranchReuseConflict, error) {
	var conflicts []BranchReuseConflict
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if manifest.LifecycleStatus != domain.TaskActive {
			return fmt.Errorf("归档任务不能添加工作区")
		}
		group, err := config.Group(manifest.GroupID)
		if err != nil {
			return err
		}
		selections, err := selectionsForAdd(group, request, manifest.FeatureBranch)
		if err != nil {
			return err
		}
		if addServices {
			for _, selection := range selections {
				for _, old := range manifest.Services {
					if old.GroupServiceID == selection.Service.ID {
						return fmt.Errorf("服务已在任务中：%s", selection.Service.ID)
					}
				}
			}
		}
		if addModules {
			if len(selections) != 1 {
				return fmt.Errorf("一次只能为一个服务添加模块")
			}
			if err := requireNewTaskModules(manifest, taskPath, selections[0]); err != nil {
				return err
			}
		}
		if err := requireUniqueWorkspacePaths(taskPath, manifest.Services, selections); err != nil {
			return err
		}
		for _, selection := range selections {
			for _, module := range selection.Service.Modules {
				for _, old := range manifest.Services {
					if old.GroupServiceID == selection.Service.ID && strings.EqualFold(old.ModuleID, module.ID) {
						return fmt.Errorf("模块已在任务中：%s/%s", selection.Service.ID, module.ID)
					}
				}
			}
		}
		conflicts, err = s.inspectBranchReuse(ctx, config, selections, manifest.FeatureBranch)
		return err
	})
	return conflicts, err
}

// RetryFailedServices re-provisions only selected failed service entries. The
// successful workspaces remain untouched; on later failure it restores the
// previous manifest and rolls back only this retry's newly created workspaces.
func (s *TaskApplicationService) RetryFailedServices(ctx context.Context, config domain.AppConfig, taskPath string, serviceIDs []string) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		failed := map[string]bool{}
		for _, ws := range manifest.Services {
			if ws.Health == domain.WorkspaceFailed && strings.TrimSpace(ws.GroupServiceID) != "" {
				failed[ws.GroupServiceID] = true
			}
		}
		selected := append([]string(nil), serviceIDs...)
		if serviceIDs == nil {
			for _, ws := range manifest.Services {
				if ws.Health == domain.WorkspaceFailed && failed[ws.GroupServiceID] {
					selected = append(selected, ws.GroupServiceID)
				}
			}
		}
		if len(selected) == 0 {
			return fmt.Errorf("没有可重试的失败服务")
		}
		selected = uniqueStrings(selected)
		for _, id := range selected {
			if !failed[id] {
				return fmt.Errorf("没有可重试的失败服务")
			}
		}
		group, err := config.Group(manifest.GroupID)
		if err != nil {
			return err
		}
		created := []domain.ServiceWorkspace{}
		requests := []WorkspaceProvisionRequest{}
		rollback := func(primary error, committed bool) error {
			failures := []error{}
			if committed {
				if saveErr := s.Manifests.Save(taskPath, manifest); saveErr != nil {
					failures = append(failures, saveErr)
				} else if _, docErr := s.Documents.WriteTaskDocument(taskPath, manifest, config); docErr != nil {
					failures = append(failures, docErr)
				}
			}
			if err := rollbackProvisionRequests(ctx, s.Provisioning, requests, created); err != nil {
				failures = append(failures, err)
			}
			if len(failures) != 0 {
				return fmt.Errorf("重试失败，且自动回滚未完整完成；已保留任务数据供人工检查：%s: %w", taskPath, errors.Join(append([]error{primary}, failures...)...))
			}
			return primary
		}
		for _, serviceID := range selected {
			var configured *domain.GroupServiceConfig
			for i := range group.Services {
				if group.Services[i].ID == serviceID {
					configured = &group.Services[i]
					break
				}
			}
			if configured == nil {
				return rollback(fmt.Errorf("失败服务已不在组配置中：%s", serviceID), false)
			}
			service := *configured
			service.Modules = []domain.ServiceModuleConfig{}
			branches := map[string]string{}
			sources := map[string]domain.TaskModuleSource{}
			for _, ws := range manifest.Services {
				if ws.GroupServiceID != serviceID {
					continue
				}
				base := ""
				if ws.BaseRef != nil {
					base = *ws.BaseRef
				}
				if base == "" {
					return rollback(fmt.Errorf("失败模块缺少主分支快照：%s", ws.ModuleName), false)
				}
				service.Modules = append(service.Modules, domain.ServiceModuleConfig{ID: ws.ModuleID, Name: ws.ModuleName, Strategy: ws.Strategy, MasterBranch: &base, TagEnabled: ws.TagEnabled, TagMode: ws.TagMode, TagTargetRef: ws.TagTargetRef, TagMessagePrefix: ws.TagMessagePrefix})
				branches[ws.ModuleID] = targetBranchValue(ws)
				sources[ws.ModuleID] = ws.ModuleSource
			}
			repository, ok := repositoryByID(config, service.RepositoryID)
			if !ok {
				return rollback(fmt.Errorf("服务 %s 的仓库配置不存在", service.DisplayName), false)
			}
			request := WorkspaceProvisionRequest{TaskDirectory: taskPath, Repository: repository, Service: service, RequestedFeatureBranch: manifest.FeatureBranch, ModuleBranches: branches, ModuleSources: sources}
			provisioned, provisionErr := s.Provisioning.Provision(ctx, request, s.OnOutput)
			if provisionErr != nil {
				return rollback(provisionErr, false)
			}
			requests = append(requests, request)
			created = append(created, provisioned...)
		}
		replacements := append([]domain.ServiceWorkspace(nil), created...)
		retained := make([]domain.ServiceWorkspace, 0, len(manifest.Services))
		for _, ws := range manifest.Services {
			found := false
			for _, id := range selected {
				if ws.GroupServiceID == id {
					found = true
					break
				}
			}
			if !found {
				retained = append(retained, ws)
			}
		}
		updated := manifest
		updated.Services = append(retained, replacements...)
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return rollback(err, false)
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			return rollback(err, true)
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *TaskApplicationService) ClearWorkspaceWarnings(config domain.AppConfig, taskPath, workspacePath string) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		updated := manifest
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		updated.Services = append([]domain.ServiceWorkspace(nil), manifest.Services...)
		for i := range updated.Services {
			if !samePath(updated.Services[i].WorktreePath, workspacePath) {
				continue
			}
			updated.Services[i].Warnings = []string{}
			if updated.Services[i].Health == domain.WorkspaceReadyWithWarnings {
				updated.Services[i].Health = domain.WorkspaceReady
			}
		}
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return err
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *TaskApplicationService) RerunWorkspaceBootstrap(ctx context.Context, config domain.AppConfig, taskPath, workspacePath string) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		index := -1
		for i := range manifest.Services {
			if samePath(manifest.Services[i].WorktreePath, workspacePath) {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("任务中不存在工作区：%s", workspacePath)
		}
		ws := manifest.Services[index]
		if ws.Health != domain.WorkspaceReady && ws.Health != domain.WorkspaceReadyWithWarnings {
			return fmt.Errorf("仅就绪的工作区可以重新执行 Bootstrap")
		}
		validated, err := s.Lifecycle.ValidateForMutationContext(ctx, config, taskPath, manifest, ws)
		if err != nil {
			return err
		}
		target := validated.Worktree
		group, err := config.Group(manifest.GroupID)
		if err != nil {
			return err
		}
		var service *domain.GroupServiceConfig
		for i := range group.Services {
			if group.Services[i].ID == ws.GroupServiceID {
				service = &group.Services[i]
				break
			}
		}
		if service == nil {
			return fmt.Errorf("服务配置已经不存在，无法重新执行 Bootstrap")
		}
		source := validated.Repository
		if ws.Strategy == domain.WorkspaceIndependentClone {
			repository, ok := repositoryByID(config, ws.RepositoryID)
			if !ok {
				return fmt.Errorf("仓库配置已经不存在，无法重新执行 Bootstrap")
			}
			source = repository.RootPath
		}
		bootstrap, err := s.Bootstrap.Initialize(ctx, source, target, service.Bootstrap, s.OnOutput)
		if err != nil {
			return err
		}
		updated := manifest
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		updated.Services = append([]domain.ServiceWorkspace(nil), manifest.Services...)
		updated.Services[index].Warnings = append([]string(nil), bootstrap.Warnings...)
		if len(bootstrap.Warnings) == 0 {
			updated.Services[index].Health = domain.WorkspaceReady
		} else {
			updated.Services[index].Health = domain.WorkspaceReadyWithWarnings
		}
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return err
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *TaskApplicationService) UpdateTagTarget(config domain.AppConfig, taskPath, groupServiceID, moduleID string, expectedTargetRef *string, targetRef string) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if manifest.LifecycleStatus != domain.TaskActive {
			return fmt.Errorf("归档任务不能修改测试目标分支")
		}
		var index = -1
		for i := range manifest.Services {
			if manifest.Services[i].GroupServiceID == groupServiceID && manifest.Services[i].ModuleID == moduleID {
				if index >= 0 {
					return fmt.Errorf("测试Tag目标不唯一，请选择具体模块")
				}
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("任务中不存在测试Tag目标：%s:%s", groupServiceID, moduleID)
		}
		ws := manifest.Services[index]
		if !config.TagEnabled || !ws.TagEnabled {
			return fmt.Errorf("当前工作区未启用测试Tag")
		}
		if _, err := config.Group(manifest.GroupID); err != nil {
			return err
		}
		if ws.TagMode != domain.TagBuildMergeToTargetBranch {
			return fmt.Errorf("当前分支模式不支持设置测试目标分支")
		}
		if !equalOptionalString(ws.TagTargetRef, expectedTargetRef) {
			return fmt.Errorf("测试目标分支已被其他操作修改，请关闭后重新打开设置")
		}
		target, err := domain.ParseRemoteBranchRef(targetRef)
		if err != nil {
			return err
		}
		if strings.HasPrefix(target.Remote, "-") {
			return fmt.Errorf("测试目标远程名称不能以 - 开头：%s", target.Remote)
		}
		validated, err := s.Lifecycle.ValidateForMutation(config, taskPath, manifest, ws)
		if err != nil {
			return err
		}
		if !s.BranchValidator.IsValid(target.Branch) {
			return fmt.Errorf("测试目标分支格式不合法：%s", target.Branch)
		}
		remotes, err := s.Git.RemoteNames(validated.Repository)
		if err != nil {
			return err
		}
		foundRemote := false
		for _, remote := range remotes {
			if remote == target.Remote {
				foundRemote = true
				break
			}
		}
		if !foundRemote {
			return fmt.Errorf("仓库中不存在远程：%s", target.Remote)
		}
		ref := "refs/heads/" + target.Branch
		listing, err := s.Git.RunContext(context.Background(), validated.Repository, 20*time.Second, false, true, "ls-remote", "--heads", target.Remote, ref)
		if err != nil {
			return err
		}
		foundBranch := false
		for _, line := range strings.Split(strings.TrimSpace(listing.Stdout), "\n") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[1]) == ref {
				foundBranch = true
				break
			}
		}
		if !foundBranch {
			return fmt.Errorf("远程分支不存在：%s", target.String())
		}
		updated := manifest
		updated.UpdatedAt = s.now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		updated.Services = append([]domain.ServiceWorkspace(nil), manifest.Services...)
		value := target.String()
		updated.Services[index].TagTargetRef = &value
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
