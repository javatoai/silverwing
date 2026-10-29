package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/workspace"
)

func (a *App) workspaceAction(ctx context.Context, action Action) (ActionResult, error) {
	return a.workspaceActionWithProgress(ctx, action, nil, nil)
}

func (a *App) DispatchWorkspaceCommand(ctx context.Context, action Action, onOutput func(process.StreamEvent)) (ActionResult, error) {
	if action.Kind != "workspace.command" {
		return ActionResult{}, errors.New("流式工作区入口仅支持配置的快捷命令")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	return a.workspaceActionWithOutput(ctx, action, onOutput)
}

func (a *App) workspaceActionWithOutput(ctx context.Context, action Action, onOutput func(process.StreamEvent)) (ActionResult, error) {
	return a.workspaceActionWithProgress(ctx, action, onOutput, nil)
}

func (a *App) workspaceActionWithProgress(ctx context.Context, action Action, onOutput func(process.StreamEvent), onTaskProgress func(workspace.TaskCreationProgress)) (actionResult ActionResult, actionErr error) {
	if result, handled, err := a.dispatchTypedTaskAction(ctx, action, onTaskProgress); handled {
		return result, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	git := a.git(config)
	service := a.taskService(config)
	defer func() { appendMaterialsCatalogWarnings(&actionResult, service.Documents) }()
	if result, handled, previewErr := taskCreationPreviewAction(ctx, action, config, service); handled {
		return result, previewErr
	}
	manifest, path, err := a.taskInConfig(action, config)
	if err != nil {
		return ActionResult{}, err
	}
	if result, handled, appendErr := taskWorkspaceAppendAction(ctx, action, config, service, path); handled {
		return result, appendErr
	}
	if action.Kind == "workspace.status" {
		return workspaceStatus(ctx, git, manifest, action)
	}
	if action.Kind == "workspace.bootstrap-clear" || action.Kind == "workspace.bootstrap-rerun" {
		selected, selectionErr := selectedWorkspaces(manifest, action)
		if selectionErr != nil {
			return ActionResult{}, selectionErr
		}
		if len(selected) != 1 {
			return ActionResult{}, errors.New("请选择一个具体的任务工作区")
		}
		if action.Kind == "workspace.bootstrap-clear" {
			updated, clearErr := service.ClearWorkspaceWarnings(config, path, selected[0].WorktreePath)
			return ActionResult{Message: "工作区警告已清除", Data: updated}, clearErr
		}
		if !boolPayload(action, "confirmed") {
			return ActionResult{}, errors.New("请确认重新执行工作区 Bootstrap")
		}
		updated, bootstrapErr := service.RerunWorkspaceBootstrap(ctx, config, path, selected[0].WorktreePath)
		return ActionResult{Message: "工作区 Bootstrap 已返回，请查看警告状态", Data: updated}, bootstrapErr
	}
	if result, handled, err := a.workspaceWorkflow(ctx, action, config, service, manifest, path, onOutput); handled {
		return result, err
	}
	switch action.Kind {
	case "workspace.add":
		var req workspace.AddWorkspaceRequest
		if err = decodePayload(action.Payload, &req); err != nil {
			return ActionResult{}, err
		}
		if action.ServiceID != "" {
			req.ServiceID = action.ServiceID
		}
		if action.ModuleID != "" {
			req.ModuleID = action.ModuleID
		}
		m, e := service.AddWorkspace(ctx, config, path, req)
		return ActionResult{Message: "工作区已添加", Data: m}, e
	}
	var selected *domain.ServiceWorkspace
	for i := range manifest.Services {
		w := &manifest.Services[i]
		if w.GroupServiceID == action.ServiceID && (action.ModuleID == "" || w.ModuleID == action.ModuleID) {
			if selected != nil && action.ModuleID == "" {
				return ActionResult{}, errors.New("请选择具体模块")
			}
			selected = w
		}
	}
	if selected == nil {
		return ActionResult{}, errors.New("请选择任务工作区")
	}
	w := *selected
	if info, e := os.Stat(w.WorktreePath); e != nil || !info.IsDir() {
		return ActionResult{}, errors.New("工作区目录不可用，请先修复")
	}
	// Match the manifest identity before reading or mutating this checkout.
	root, e := git.TopLevel(w.WorktreePath)
	if e != nil {
		return ActionResult{}, e
	}
	root, e = filepath.Abs(root)
	if e != nil {
		return ActionResult{}, e
	}
	expected, e := filepath.Abs(w.WorktreePath)
	if e != nil {
		return ActionResult{}, e
	}
	if filepath.Clean(root) != filepath.Clean(expected) {
		return ActionResult{}, errors.New("工作区 Git 根目录与任务清单不一致")
	}
	switch action.Kind {
	case "workspace.fetch":
		remote := textPayload(action, "remote")
		if remote == "" {
			remote = w.PushRemote
		}
		if remote == "" {
			remote = "origin"
		}
		var resultText string
		err = workspace.NewOperationLocks(a.Paths.Locks).WithRepository(w.RepositoryPath, func() error {
			result, e := git.RunContext(ctx, w.WorktreePath, 2*time.Minute, true, false, "fetch", remote)
			resultText = result.Stdout + result.Stderr
			return e
		})
		return ActionResult{Message: "远端已刷新", Text: resultText}, err
	}
	return ActionResult{}, fmt.Errorf("工作区操作 %s 正在迁移，当前版本尚未实现", action.Kind)
}

func emitTaskCreationProgress(onProgress func(workspace.TaskCreationProgress), event workspace.TaskCreationProgress, status workspace.TaskCreationProgressStatus, message string) {
	if onProgress == nil {
		return
	}
	event.Status = status
	event.Message = message
	onProgress(event)
}

func hasSelectedWorkspaceTools(toolIDs []string) bool {
	for _, id := range toolIDs {
		if strings.TrimSpace(id) != "" {
			return true
		}
	}
	return false
}

func workspaceToolLaunchFailure(manifest domain.TaskManifest, toolIDs []string) string {
	for _, id := range toolIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		found := false
		for _, launch := range manifest.WorkspaceToolLaunches {
			if launch.ToolID != id {
				continue
			}
			found = true
			if launch.Status != domain.WorkspaceToolOpened {
				message := "工具未能成功打开：" + id
				if launch.Message != nil && strings.TrimSpace(*launch.Message) != "" {
					message += "；" + strings.TrimSpace(*launch.Message)
				}
				return message
			}
			break
		}
		if !found {
			return "工具启动状态未保存：" + id
		}
	}
	return ""
}

func workspaceStatus(ctx context.Context, git *workspace.GitClient, manifest domain.TaskManifest, action Action) (ActionResult, error) {
	selected, err := selectedWorkspaces(manifest, action)
	if err != nil {
		return ActionResult{}, err
	}
	groups := make([]workspaceStatusGroup, 0, len(selected))
	groupByPath := make(map[string]int, len(selected))
	for _, item := range selected {
		path := workspace.WorkspaceGitPathKey(item)
		identity := filepath.Clean(path)
		if runtime.GOOS == "windows" {
			identity = strings.ToLower(identity)
		}
		if index, ok := groupByPath[identity]; ok {
			if !sameWorkspaceStatusDefinition(groups[index].workspace, item) {
				groups[index].conflict = true
			}
			continue
		}
		groupByPath[identity] = len(groups)
		groups = append(groups, workspaceStatusGroup{path: path, workspace: item})
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	reader := workspace.NewWorkspaceGitStatusReader(git)
	results := make([]domain.WorkspaceGitHealth, len(groups))
	workerCount := min(4, len(groups))
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				group := groups[index]
				if group.conflict {
					results[index] = workspaceStatusConflict(group.workspace)
					continue
				}
				results[index] = reader.ReadContext(ctx, group.workspace)
			}
		}()
	}
	for index := range groups {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ActionResult{}, ctx.Err()
		case jobs <- index:
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	statuses := make(map[string]domain.WorkspaceGitHealth, len(groups))
	for index, group := range groups {
		statuses[group.path] = results[index]
	}
	return ActionResult{Message: "Git 状态已刷新", Data: statuses}, nil
}

type workspaceStatusGroup struct {
	path      string
	workspace domain.ServiceWorkspace
	conflict  bool
}

func sameWorkspaceStatusDefinition(a, b domain.ServiceWorkspace) bool {
	return a.RepositoryID == b.RepositoryID && a.RepositoryPath == b.RepositoryPath &&
		a.Branch == b.Branch && a.Strategy == b.Strategy
}

func workspaceStatusConflict(item domain.ServiceWorkspace) domain.WorkspaceGitHealth {
	message := "同一物理工作区在任务清单中存在互相冲突的仓库或分支定义"
	return domain.WorkspaceGitHealth{
		State: domain.WorkspaceGitFailed, PushState: domain.LocalPushFailed,
		Issue: domain.WorkspaceGitIssueIdentityMismatch, Message: &message,
		ExpectedBranch: &item.Branch, DirtyFiles: []domain.WorkspaceGitFileChange{},
	}
}

func decodePayload(payload map[string]any, target any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
