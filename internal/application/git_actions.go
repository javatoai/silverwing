package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/workspace"
)

func (a *App) workspaceWorkflow(ctx context.Context, action Action, config domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, taskPath string, onOutput func(process.StreamEvent)) (ActionResult, bool, error) {
	result := func(data any, message string, err error) (ActionResult, bool, error) {
		return ActionResult{Message: message, Data: data}, true, err
	}
	switch action.Kind {
	case "task.delete-risks", "task.archive-preview":
		data, err := service.InspectDeleteRisks(ctx, config, taskPath)
		return result(data, "工作区风险已检查", err)
	case "task.restore-workspaces":
		data, err := service.RestoreWorkspaces(ctx, config, taskPath)
		return result(data, "工作区已恢复", err)
	case "task.notes-save":
		return result(nil, "任务备注已保存", service.SaveTaskNotes(config, taskPath, actionText(action)))
	case "workspace.status", "workspace.preview", "workspace.commit", "workspace.push", "workspace.commit-push", "workspace.batch", "workspace.previews":
		selected, err := selectedWorkspaces(manifest, action)
		if err != nil {
			return result(nil, "", err)
		}
		operations := workspace.NewWorkspaceGitOperationService(service.Git, service.Locks)
		if action.Kind == "workspace.status" {
			items := make(map[string]workspace.RepositoryStatus, len(selected))
			for _, item := range selected {
				status, e := service.Git.Status(item.WorktreePath)
				if e != nil {
					return result(nil, "", e)
				}
				items[workspace.WorkspaceGitPathKey(item)] = status
			}
			return result(items, "Git 状态已刷新", nil)
		}
		if action.Kind == "workspace.preview" && len(selected) == 1 {
			data, e := operations.PreviewContext(ctx, selected[0])
			return result(data, "提交预览已读取", e)
		}
		if action.Kind == "workspace.preview" || action.Kind == "workspace.previews" {
			data, e := operations.PreviewsContext(ctx, selected)
			return result(data, "提交预览已读取", e)
		}
		if len(selected) == 1 && action.Kind != "workspace.batch" {
			var data workspace.WorkspaceGitOperationResult
			message := textPayload(action, "message")
			fingerprint := textPayload(action, "fingerprint")
			if fingerprint == "" {
				fingerprint = textPayload(action, "expectedFingerprint")
			}
			switch action.Kind {
			case "workspace.commit":
				data, err = operations.Commit(ctx, selected[0], message, fingerprint, config.BlockedGitWriteBranches)
			case "workspace.push":
				data, err = operations.Push(ctx, selected[0], config.BlockedGitWriteBranches)
			case "workspace.commit-push":
				data, err = operations.CommitAndPush(ctx, selected[0], message, fingerprint, config.BlockedGitWriteBranches)
			}
			return result(data, data.Message, err)
		}
		mode := workspace.WorkspaceGitBatchMode(strings.ToUpper(textPayload(action, "mode")))
		if mode == "" {
			mode = map[string]workspace.WorkspaceGitBatchMode{"workspace.commit": workspace.WorkspaceGitBatchCommit, "workspace.push": workspace.WorkspaceGitBatchPush, "workspace.commit-push": workspace.WorkspaceGitBatchCommitAndPush}[action.Kind]
		}
		messages, fingerprints := map[string]string{}, map[string]string{}
		if err := decodeField(action, "messages", &messages); err != nil {
			return result(nil, "", err)
		}
		if err := decodeField(action, "fingerprints", &fingerprints); err != nil {
			return result(nil, "", err)
		}
		for _, item := range selected {
			key := workspace.WorkspaceGitPathKey(item)
			if _, exists := messages[key]; !exists {
				messages[key] = textPayload(action, "message")
			}
		}
		data, e := operations.Batch(ctx, selected, mode, messages, fingerprints, config.BlockedGitWriteBranches)
		return result(data, "批量 Git 操作已返回，请查看逐工作区结果", e)
	case "workspace.repair-preview", "workspace.repair", "workspace.remove-preview", "workspace.remove", "workspace.merge", "workspace.merge-batch", "workspace.history", "workspace.file-preview", "workspace.diff-files", "workspace.diff-preview", "workspace.command":
		selected, err := selectedWorkspaces(manifest, action)
		if err != nil {
			return result(nil, "", err)
		}
		if len(selected) != 1 && action.Kind != "workspace.merge-batch" {
			return result(nil, "", errors.New("请选择一个具体工作区"))
		}
		if action.Kind == "workspace.merge-batch" {
			var confirmation workspace.WorkspaceMainBranchMergeBatchConfirmation
			if err := decodeField(action, "confirmation", &confirmation); err != nil {
				return result(nil, "", err)
			}
			confirmed, err := confirmation.ConfirmedWorkspaces(config, manifest, selected)
			if err != nil {
				return result(nil, "", err)
			}
			data := workspace.NewWorkspaceMainBranchMergeService(service.Git, service.Manifests, service.Locks, service.Lifecycle).MergeBatch(ctx, config, taskPath, manifest, confirmed)
			return result(data, "批量主分支合并已返回，请查看逐模块结果", nil)
		}
		item := selected[0]
		switch action.Kind {
		case "workspace.repair-preview":
			data, e := service.InspectWorkspaceRepair(config, taskPath, item.WorktreePath)
			return result(data, "修复预览已读取", e)
		case "workspace.repair":
			if action.Payload["preview"] == nil {
				data, e := service.InspectWorkspaceRepair(config, taskPath, item.WorktreePath)
				return result(map[string]any{"preview": data, "requiresConfirmation": true}, "请确认修复预览", e)
			}
			var preview workspace.WorkspaceRepairPreview
			var confirmation workspace.WorkspaceRepairConfirmation
			if e := decodeField(action, "preview", &preview); e != nil {
				return result(nil, "", e)
			}
			if e := decodeField(action, "confirmation", &confirmation); e != nil {
				return result(nil, "", e)
			}
			if preview.WorkspacePath != item.WorktreePath {
				return result(nil, "", errors.New("修复预览与选择的工作区不一致"))
			}
			data, e := service.RepairWorkspace(ctx, config, taskPath, preview, confirmation)
			return result(data, data.Message, e)
		case "workspace.remove-preview":
			data, e := service.InspectModuleRemoval(config, taskPath, item.WorktreePath)
			return result(data, "移除预览已读取", e)
		case "workspace.remove":
			if action.Payload["preview"] == nil {
				data, e := service.InspectModuleRemoval(config, taskPath, item.WorktreePath)
				return result(map[string]any{"preview": data, "requiresConfirmation": true}, "请确认移除预览", e)
			}
			var preview workspace.WorkspaceModuleRemovalPreview
			var confirmation workspace.WorkspaceModuleRemovalConfirmation
			if e := decodeField(action, "preview", &preview); e != nil {
				return result(nil, "", e)
			}
			if e := decodeField(action, "confirmation", &confirmation); e != nil {
				return result(nil, "", e)
			}
			if preview.WorkspacePath != item.WorktreePath {
				return result(nil, "", errors.New("移除预览与选择的工作区不一致"))
			}
			data, e := service.RemoveModule(ctx, config, taskPath, preview, confirmation)
			message := "工作区已移除"
			if data.CleanupError != "" {
				message = "清单已更新，目录清理未完成；请查看保留备份和原因"
			}
			return result(data, message, e)
		case "workspace.merge":
			data, e := workspace.NewWorkspaceMainBranchMergeService(service.Git, service.Manifests, service.Locks, service.Lifecycle).Merge(ctx, config, taskPath, manifest, item)
			if e != nil {
				// Preserve the original error when the caller is cancelled. A command's
				// internal timeout remains a FAILED diagnostic while the caller is live.
				if ctx.Err() != nil {
					return result(data, "", e)
				}
				sourceRef := "远程主分支"
				if source, ok, resolveErr := workspace.ResolveWorkspaceMainBranch(config, manifest, item); resolveErr == nil && ok && strings.TrimSpace(source.DisplayRef) != "" {
					sourceRef = source.DisplayRef
				}
				failed := workspace.WorkspaceMainBranchMergeResult{
					WorkspacePath: item.WorktreePath,
					ServiceName:   item.ServiceName,
					ModuleName:    item.ModuleName,
					SourceRef:     sourceRef,
					TargetBranch:  item.Branch,
					Outcome:       workspace.MainBranchFailed,
					Message:       e.Error(),
					ConflictFiles: []string{},
				}
				return result(failed, "主分支合并失败", nil)
			}
			return result(data, "合并操作已返回，请查看工作区结果", nil)
		case "workspace.history":
			baseline, e := workspaceHistoryBaseline(config, manifest, item, action)
			if e != nil {
				return result(nil, "", e)
			}
			data, e := workspace.NewWorkspaceGitHistoryService(service.Git).ReadContext(ctx, item.WorktreePath, baseline)
			return result(data, "提交历史已读取", e)
		case "workspace.file-preview":
			var change domain.WorkspaceGitFileChange
			if e := decodeField(action, "change", &change); e != nil {
				return result(nil, "", e)
			}
			data, e := workspace.NewWorkspaceGitFilePreviewService(service.Git).PreviewContext(ctx, item.WorktreePath, change)
			return result(data, "文件差异已读取", e)
		case "workspace.diff-files", "workspace.diff-preview":
			if action.Path != "" && !sameTaskRefreshPath(action.Path, item.WorktreePath) {
				return result(nil, "", errors.New("工作区路径已变化，请重新读取差异"))
			}
			var request workspace.CompareRequest
			if e := decodeField(action, "compare", &request); e != nil {
				return result(nil, "", e)
			}
			if request.Mode == "" {
				request.Mode = workspace.CompareMainline
			}
			mainBranch := workspace.WorkspaceMainBranch{}
			if request.Mode == workspace.CompareMainline || request.Mode == workspace.CompareCommits && request.SimulateOnMainline {
				resolved, found, e := workspace.ResolveWorkspaceDiffMainBranch(config, manifest, item)
				if e != nil {
					return result(nil, "", e)
				}
				if !found {
					return result(nil, "", errors.New("任务配置中没有可用的模块或服务主分支"))
				}
				mainBranch = resolved
			}
			diff := workspace.NewWorkspaceGitDiffService(service.Git, service.Locks)
			ownerIdentity, configIdentity, e := workspaceGitDiffSessionIdentity(action.TaskID, taskPath, manifest, config)
			if e != nil {
				return result(nil, "", e)
			}
			manager := a.workspaceGitDiffSessions()
			if action.Kind == "workspace.diff-files" {
				data, e := diff.FilesSessionContext(ctx, manager, ownerIdentity, configIdentity, item, request, mainBranch)
				return result(data, "Git 差异文件已读取", e)
			}
			var change workspace.CompareChange
			if e := decodeField(action, "change", &change); e != nil {
				return result(nil, "", e)
			}
			sessionID := textPayload(action, "sessionId")
			if strings.TrimSpace(sessionID) == "" {
				return result(nil, "", errors.New("Git 差异会话已失效，请重新读取文件列表"))
			}
			data, e := diff.PreviewSessionContext(ctx, manager, sessionID, ownerIdentity, configIdentity, item, request, change, mainBranch)
			return result(data, "Git 差异内容已读取", e)
		case "workspace.command":
			if item.Health != domain.WorkspaceReady && item.Health != domain.WorkspaceReadyWithWarnings {
				return result(nil, "", errors.New("工作区尚未就绪，无法执行快捷命令"))
			}
			commandID := textPayload(action, "commandId")
			for _, group := range config.Groups {
				if group.ID != manifest.GroupID {
					continue
				}
				for _, configured := range group.Services {
					if configured.ID != item.GroupServiceID {
						continue
					}
					for _, module := range configured.Modules {
						if module.ID != item.ModuleID {
							continue
						}
						for _, command := range module.CustomCommands {
							if command.ID == commandID {
								data, e := service.Commands.Execute(ctx, config, item.WorktreePath, command, onOutput)
								return result(data, "命令已返回，请查看退出码和输出", e)
							}
						}
					}
				}
			}
			return result(nil, "", errors.New("请选择服务中配置的快捷命令"))
		}
	}
	return ActionResult{}, false, nil
}

func selectedWorkspaces(manifest domain.TaskManifest, action Action) ([]domain.ServiceWorkspace, error) {
	var keys []string
	if err := decodeField(action, "selectionKeys", &keys); err != nil {
		return nil, err
	}
	selected := make([]domain.ServiceWorkspace, 0, len(manifest.Services))
	for _, item := range manifest.Services {
		if action.ServiceID != "" && item.GroupServiceID != action.ServiceID {
			continue
		}
		if action.ModuleID != "" && item.ModuleID != action.ModuleID {
			continue
		}
		if len(keys) > 0 {
			key := item.GroupServiceID + ":" + item.ModuleID
			matched := false
			for _, requested := range keys {
				if requested == key || requested == item.WorktreePath {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		selected = append(selected, item)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("未找到选择的任务工作区")
	}
	return selected, nil
}

func decodeField(action Action, key string, target any) error {
	if action.Payload[key] == nil {
		return nil
	}
	data, err := json.Marshal(action.Payload[key])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
