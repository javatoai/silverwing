package application

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/systemtrash"
	"silverwing-go/internal/taskscope"
	"silverwing-go/internal/workspace"
)

func (a *App) workspaceRemovalAction(ctx context.Context, action Action, config domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, taskPath string, item domain.ServiceWorkspace) (ActionResult, error) {
	if action.Path != "" && !sameTaskContextPath(action.Path, item.WorktreePath) {
		return ActionResult{}, errors.New("工作区路径已变化，请重新检查")
	}
	ctx = a.workspaceRemovalScope(ctx, action, manifest, taskPath, item)
	if action.Kind == "workspace.remove-preview" || action.Payload["preview"] == nil {
		preview, err := service.InspectModuleRemovalContext(ctx, config, taskPath, item.WorktreePath)
		if err != nil {
			return ActionResult{}, err
		}
		if action.Kind == "workspace.remove-preview" {
			return ActionResult{Data: preview, Message: "模块回收预览已读取"}, nil
		}
		return ActionResult{Data: map[string]any{"preview": preview, "requiresConfirmation": true}, Message: "请确认模块回收预览"}, nil
	}

	var preview workspace.WorkspaceModuleRemovalPreview
	var confirmation workspace.WorkspaceModuleRemovalConfirmation
	if err := decodeField(action, "preview", &preview); err != nil {
		return ActionResult{}, err
	}
	if err := decodeField(action, "confirmation", &confirmation); err != nil {
		return ActionResult{}, err
	}
	if !sameTaskContextPath(preview.WorkspacePath, item.WorktreePath) || preview.ServiceID != item.GroupServiceID || preview.ModuleID != item.ModuleID {
		return ActionResult{}, errors.New("回收预览与选择的工作区不一致")
	}
	data, err := service.RemoveModule(ctx, config, taskPath, preview, confirmation)
	if err != nil {
		return ActionResult{}, err
	}
	message := "工作区目录已缺失，已移除模块记录"
	if data.DirectoryRecycled {
		message = "模块已移入系统" + systemtrash.Label()
	}
	if data.CleanupError != "" {
		message = "模块已移入系统" + systemtrash.Label() + "，恢复记录或 Git 登记处理未完成"
	}
	return ActionResult{Data: data, Message: message}, nil
}

func (a *App) workspaceRemovalScope(ctx context.Context, action Action, manifest domain.TaskManifest, taskPath string, item domain.ServiceWorkspace) context.Context {
	return taskscope.WithValidation(ctx, func(checkCtx context.Context, config domain.AppConfig, path string, current domain.TaskManifest) error {
		if err := taskscope.Validate(ctx, config, path, current); err != nil {
			return err
		}
		if current.CreatedAt != manifest.CreatedAt || current.TaskDirectoryName != manifest.TaskDirectoryName || !sameTaskContextPath(path, taskPath) {
			return errWorkspaceRemovalScopeChanged
		}
		if _, desktop := action.Payload["workspaceRemovalRequestID"]; desktop {
			if !sameTaskContextPath(textPayload(action, "workspaceRemovalHome"), a.Paths.Home) ||
				!sameTaskContextPath(textPayload(action, "workspaceRemovalTaskPath"), path) ||
				strings.TrimSpace(textPayload(action, "workspaceRemovalTaskCreatedAt")) != current.CreatedAt {
				return errWorkspaceRemovalScopeChanged
			}
		}
		latest, err := a.Config.Load()
		if err != nil {
			return err
		}
		if !sameTaskContextPath(value(latest.TaskRoot), value(config.TaskRoot)) {
			return errWorkspaceRemovalScopeChanged
		}
		if item.Strategy == domain.WorkspaceStandardWorktree &&
			!sameModuleRemovalRepository(config, latest, item.RepositoryID) {
			return errWorkspaceRemovalScopeChanged
		}
		for _, ws := range current.Services {
			if ws.GroupServiceID == item.GroupServiceID && ws.ModuleID == item.ModuleID && sameTaskContextPath(ws.WorktreePath, item.WorktreePath) {
				return checkCtx.Err()
			}
		}
		return errWorkspaceRemovalScopeChanged
	})
}

var errWorkspaceRemovalScopeChanged = errors.New("任务、工作区或配置已变化，请重新打开模块回收预览")

func sameModuleRemovalRepository(before, after domain.AppConfig, id string) bool {
	find := func(config domain.AppConfig) (domain.RepositoryConfig, bool) {
		for _, repository := range config.Repositories {
			if repository.ID == id {
				return repository, true
			}
		}
		return domain.RepositoryConfig{}, false
	}
	expected, found := find(before)
	actual, exists := find(after)
	if !found || !exists {
		return !found && !exists
	}
	return sameTaskContextPath(expected.RootPath, actual.RootPath) &&
		sameTaskContextPath(expected.GitCommonDirectory, actual.GitCommonDirectory) &&
		value(expected.OriginURL) == value(actual.OriginURL)
}
