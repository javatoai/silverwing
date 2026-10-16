package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

func (s *app) dispatch(action application.Action) {
	if s.dispatchTagJobAction(action) {
		return
	}
	if s.dispatchIndependentSystemOpen(action) {
		return
	}
	if s.dispatchIndependentSettingsConfigRead(action) {
		return
	}
	if s.dispatchIndependentTaskDeleteRisks(action) {
		return
	}
	if s.dispatchIndependentSettingsExtensionRead(action) {
		return
	}
	if s.dispatchSkillsBrowserRead(action) {
		return
	}
	if s.dispatchMaterialsFeishuSyncJob(action) {
		return
	}
	if s.dispatchWorkspaceCommandJob(action) {
		return
	}
	if s.dispatchDeveloperToolJob(action) {
		return
	}
	if s.dispatchIndependentCLI(action) {
		return
	}
	if s.dispatchIndependentRequirementContent(action) {
		return
	}
	if s.dispatchIndependentWorkspaceHistory(action) {
		return
	}
	if s.dispatchIndependentMaterialsFileAction(action) {
		return
	}
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	if s.backend == nil {
		s.lastError = "应用后端不可用。"
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	var renamePlan *markdownAutosaveRenamePlan
	var renameRoot string
	if action.Kind == "document.rename" {
		name := payloadString(action.Payload, "name")
		if err := documents.ValidateFileName(name); err != nil {
			s.lastError = err.Error()
			return
		}
		task := s.taskByID(action.TaskID)
		var err error
		renameRoot = task.materialsPath
		renamePlan, err = s.captureMarkdownAutosaveRename(action.TaskID, renameRoot, action.Path, filepath.Join(filepath.Dir(action.Path), name))
		if err != nil {
			s.lastError = err.Error()
			return
		}
		payload := make(map[string]any, len(action.Payload)+3)
		for key, value := range action.Payload {
			payload[key] = value
		}
		payload["desktopRenameTaskPath"] = task.path
		payload["desktopRenameTaskCreatedAt"] = task.createdAt
		payload["desktopRenameMaterialsRoot"] = renameRoot
		action.Payload = payload
	}
	migrationPlan, captureErr := s.captureMarkdownTaskRootMigration(action)
	if captureErr != nil {
		s.lastError = captureErr.Error()
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	watchBarrier := s.pauseMaterialsLiveRefresh(action)
	fileBarrier := s.pauseMaterialsFileJobs(action)
	s.cancelOperation = cancel
	s.busy = true
	s.operation = actionLabel(action.Kind)
	s.operationKind = action.Kind
	s.operationCancelling = false
	s.lastError = ""
	s.lastMessage = ""
	if s.win != nil {
		s.win.Invalidate()
	}
	migrationProgress, finishMigrationProgress := s.taskRootMigrationProgressUpdates(ctx, action)
	taskCreationProgress := s.taskCreationProgressUpdates(ctx, action)

	go func() {
		defer finishMigrationProgress()
		var renameLease *markdownAutosaveRenameLease
		var migrationLease *markdownTaskRootMigrationLease
		err := fileBarrier.wait(ctx)
		if err == nil {
			migrationPlan, err = s.settleMaterialsBeforeRootAction(ctx, action, fileBarrier, migrationPlan)
		}
		if err == nil && renamePlan != nil {
			renameLease, err = s.prepareMarkdownAutosaveRename(ctx, renamePlan)
		}
		if err == nil && migrationPlan != nil {
			migrationLease, err = s.prepareMarkdownTaskRootMigration(ctx, migrationPlan)
		}
		if err == nil {
			err = watchBarrier.wait(ctx)
		}
		if err == nil {
			err = s.acquireBackend(ctx)
		}
		var result application.ActionResult
		var refreshed application.Snapshot
		var refreshErr error
		var backendDispatched bool
		if err == nil {
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			} else if typed, ok := s.backend.(application.TaskRootMigrationDispatcher); ok && action.Kind == "migration.apply" {
				backendDispatched = true
				result, err = typed.DispatchTaskRootMigration(ctx, action, migrationProgress)
			} else if typed, ok := s.backend.(application.TaskCreationProgressBackend); ok && action.Kind == "task.create" {
				backendDispatched = true
				result, err = typed.DispatchTaskCreation(ctx, action, taskCreationProgress)
			} else {
				backendDispatched = true
				result, err = s.backend.Dispatch(ctx, action)
			}
			if (err == nil && reloadsSnapshot(action.Kind)) || action.Kind == "codex.run" || action.Kind == "task.delete" || settingsExtensionSourceSaveAction(action.Kind) || (migrationLease != nil && backendDispatched) {
				refreshContext := ctx
				var stopRefresh context.CancelFunc
				if err != nil && (settingsExtensionSourceSaveAction(action.Kind) || migrationLease != nil) {
					// A source can be durably saved before a canceled checkout.
					// Read its committed configuration even after cancellation.
					refreshContext, stopRefresh = context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				}
				refreshed, refreshErr = s.backend.Snapshot(refreshContext)
				if stopRefresh != nil {
					stopRefresh()
				}
			}
			s.releaseBackend()
		}
		if s.win == nil || s.win.IsDestroyed() {
			fileBarrier.Abandon()
			if renameLease != nil {
				renameLease.Abandon(materialsRenameCompleted(result))
			}
			abandonMarkdownTaskRootDispatch(migrationLease, result, backendDispatched, refreshed, refreshErr)
			return
		}
		s.win.Update(func() {
			// The window can close after the worker queues this callback.
			// Shutdown drains queued updates; keep that path lease-only too.
			if s.win == nil || s.win.IsDestroyed() {
				fileBarrier.Abandon()
				if renameLease != nil {
					renameLease.Abandon(materialsRenameCompleted(result))
				}
				abandonMarkdownTaskRootDispatch(migrationLease, result, backendDispatched, refreshed, refreshErr)
				return
			}
			defer func() { s.finishSettingsAutosaveAction(action, result, errors.Join(err, refreshErr)) }()
			defer func() { s.finishMeegleProjectPickerSave(action, result, err, refreshErr) }()
			defer func() { s.finishTaskRootChangeAction(action, result, errors.Join(err, refreshErr)) }()
			defer s.resumeMaterialsLiveRefresh(watchBarrier)
			defer s.resumeMaterialsFileJobs(fileBarrier)
			s.busy = false
			s.operation = ""
			s.operationKind = ""
			s.operationCancelling = false
			s.cancelOperation = nil
			s.settingsConfigMutationFinished(action)
			if s.finishSettingsLogsAction(action, result, err) {
				return
			}
			if settingsExtensionRead(action) && !s.settingsExtensionActionRelevant(action) {
				return
			}
			if renameLease != nil {
				if leaseErr := completeMaterialsRenameLease(renameLease, renameRoot, action, result); leaseErr != nil {
					s.lastError = "名称已更新，但草稿迁移未完成：" + leaseErr.Error()
				}
			}
			migrationCommitted, migrationLeaseErr := completeMarkdownTaskRootDispatch(migrationLease, result, backendDispatched, refreshed, refreshErr, err)
			if migrationLeaseErr != nil {
				if err == nil {
					err = migrationLeaseErr
				} else {
					err = fmt.Errorf("%w；%v", err, migrationLeaseErr)
				}
			}
			if err != nil {
				if migrationCommitted && refreshErr == nil && refreshed.ConfigJSON != "" {
					s.applySnapshot(refreshed)
				}
				if settingsExtensionSourceSaveAction(action.Kind) {
					if refreshErr == nil && refreshed.ConfigJSON != "" {
						s.applySnapshot(refreshed)
						s.reconcileSettingsExtensionSourceSave(action, refreshed, err)
					} else {
						s.settingsExtensionSourceRecoveryFailure(action, err, refreshErr)
					}
					return
				}
				if s.applyTagHistoryMutationFailure(action, result, err) {
					return
				}
				if s.applyTagHistoryRefreshFailure(action, err) {
					return
				}
				if (action.Kind == "codex.run" || action.Kind == "task.delete") && refreshErr == nil && refreshed.ConfigJSON != "" {
					// A failed launcher persists its FAILED tool state; task deletion
					// may finish removal while reporting retained backup cleanup errors.
					s.applySnapshot(refreshed)
				}
				if action.Kind == "task.delete" && refreshErr != nil {
					err = fmt.Errorf("%w；刷新任务列表失败：%v", err, refreshErr)
				}
				if s.applyTaskDeleteActionFailure(action, err) {
					return
				}
				if s.applySettingsExtensionFailure(action, err) {
					return
				}
				if s.applyMaterialsFileActionFailure(action, err) {
					return
				}
				if s.applyMeegleProjectCatalogFailure(action, err) || s.applyRequirementContentFailure(action, err) {
					return
				}
				if strings.HasPrefix(action.Kind, "skills.") {
					s.ensureSkillsBrowser().handleFailure(action, err)
				}
				if s.taskCreationActionError(action, err) {
					return
				}
				if s.taskAppendActionError(action, err) {
					return
				}
				if s.applyTagWorkspaceCheckFailure(action, err) {
					return
				}
				if s.applyTagTargetFailure(action, err) {
					return
				}
				if tagStatusAction(action.Kind) {
					s.applyTagStatusFailure(action, err)
					return
				}
				if workspaceDedicatedResult(action.Kind) || workspaceRepairAction(action.Kind) {
					s.applyWorkspaceCommandFailure(action, err)
					s.applyWorkspaceMergeFailure(action, err)
					s.applyWorkspaceMergeBatchFailure(action, err)
					s.applyWorkspaceGitFlowFailure(action, err)
					s.applyWorkspaceRemovalFailure(action, err)
					s.applyWorkspaceRepairFailure(action, err)
					s.applyWorkspaceHistoryFailure(action, err)
					return
				}
				if action.Kind == "instructions.read" {
					s.applyInstructionReadFailure(action, err)
					return
				}
				if s.applyInstructionActionFailure(action, err) {
					return
				}
				s.lastError = err.Error()
				if action.Kind == "service.save" && s.serviceEditor != nil {
					s.serviceEditor.saving = false
					s.serviceEditor.error = err.Error()
				}
				if action.Kind == "repository.choices" && s.serviceEditor != nil && payloadString(action.Payload, "requestKey") == s.serviceEditor.choicesRequest {
					s.serviceEditor.choicesLoading = false
					s.serviceEditor.choicesError = err.Error()
					if s.serviceEditor.section == "git" && !s.serviceEditor.addressesLoaded {
						s.requestServiceRepositoryAddresses(false)
					}
				}
				if action.Kind == "repository.addresses" && s.serviceEditor != nil && payloadString(action.Payload, "requestKey") == s.serviceEditor.addressesRequest {
					s.serviceEditor.addressesLoading = false
					s.serviceEditor.addressesError = err.Error()
				}
				if action.Kind == "requirements.list" || action.Kind == "requirements.refresh" {
					s.requirementsLoading = false
					s.requirementsLoadError = err.Error()
				}
				if action.Kind == "document.list" && action.TaskID == s.currentTask().id {
					// Treat a failed initial read as settled so a capture cannot wait forever.
					s.documentsRequested = false
					s.documentsLoaded = true
				}
				if action.Kind == "document.read" && action.TaskID == s.currentTask().id && action.Path == s.documentPath {
					s.documentReadPending = false
				}
				return
			}
			// Reading the current materials is part of opening the page. Source
			// keeps successful reads quiet and only reports their failures.
			if action.Kind != "document.list" && action.Kind != "document.read" && action.Kind != "instructions.read" && action.Kind != "workspace.status" && action.Kind != "tag.workspace-check" && !workspaceDedicatedResult(action.Kind) && !workspaceRepairAction(action.Kind) && !tagStatusAction(action.Kind) {
				s.lastMessage = result.Message
			}
			if s.actionResultRelevant(action) {
				if !(s.page == "settings" && settingsActionHasDedicatedResult(action.Kind)) && action.Kind != "config.view" && action.Kind != "workspace.status" && action.Kind != "tag.workspace-check" && !workspaceDedicatedResult(action.Kind) && !workspaceRepairAction(action.Kind) && !strings.HasPrefix(action.Kind, "instructions.") && !tagStatusAction(action.Kind) && !tagHistoryDedicatedResult(action.Kind) && action.Kind != "workspace.bootstrap-clear" && action.Kind != "workspace.bootstrap-rerun" && !requirementContentAction(action.Kind) && action.Kind != "meegle.projects" {
					s.lastResult = resultText(result)
				}
				s.applyActionResult(action, result)
			}
			if refreshErr != nil {
				s.lastError = "操作已完成，但刷新数据失败：" + refreshErr.Error()
			} else if reloadsSnapshot(action.Kind) {
				s.applySnapshot(refreshed)
				if action.Kind == "workspace.merge-batch" {
					s.reconcileWorkspaceMergeBatchAfterSnapshot()
				}
				s.afterSettingsCLIPathSave(action)
				s.applyTaskSnapshotSelection(action, result)
				s.applyTaskLifecycleSnapshotSelection(action, result)
				s.applyCreatedRepositoryGroupSelection(action)
				if refreshesWorkspaceHealth(action.Kind) || taskAppendRefreshesWorkspaceHealth(action.Kind) {
					s.requestWorkspaceGitStatus(true)
				}
			}
		})
	}()
}

func (s *app) act(kind string, taskID, serviceID, moduleID, path, text string, payload map[string]any) {
	s.dispatch(application.Action{Kind: kind, TaskID: taskID, ServiceID: serviceID, ModuleID: moduleID, Path: path, Text: text, Payload: payload})
}

func (s *app) applyActionResult(action application.Action, result application.ActionResult) {
	if s.applyTaskDeleteActionResult(action, result) {
		return
	}
	if strings.HasPrefix(action.Kind, "skills.") {
		s.ensureSkillsBrowser().handleResult(action, result)
	}
	if s.taskCreationActionResult(action, result) {
		return
	}
	if s.taskAppendActionResult(action, result) {
		return
	}
	if strings.HasPrefix(action.Kind, "tool.") {
		s.ensureToolState().handleResult(action, result)
	}
	if s.applyTaskActionResult(action, result) {
		return
	}
	if s.applyWorkspaceActionResult(action, result) {
		return
	}
	if s.applyServiceActionResult(action, result) {
		return
	}
	if s.applyMaterialsActionResult(action, result) {
		return
	}
	if s.applyRequirementActionResult(action, result) {
		return
	}
	if s.applyDeliveryActionResult(action, result) {
		return
	}
	if s.applyInstructionActionResult(action, result) {
		return
	}
	if s.applySettingsActionResult(action, result) {
		return
	}

}

func settingsActionHasDedicatedResult(kind string) bool {
	return strings.HasPrefix(kind, "config.") || strings.HasPrefix(kind, "migration.") ||
		strings.HasPrefix(kind, "instructions.templates.") || strings.HasPrefix(kind, "skills.") ||
		strings.HasPrefix(kind, "codex.extensions/") || kind == "codex.extensions/snapshot" ||
		strings.HasPrefix(kind, "codex.ai/") ||
		strings.HasPrefix(kind, "cli.") || strings.HasPrefix(kind, "logs.") || strings.HasPrefix(kind, "development-tools/") ||
		strings.HasPrefix(kind, "git.environment") || kind == "terminal.resolve" || kind == "diagnostics.export" ||
		kind == "proxy.test" || kind == "git.version" || kind == "update.check"
}

func (s *app) actionResultRelevant(action application.Action) bool {
	if tagHistoryDedicatedResult(action.Kind) {
		return s.tagHistoryResultRelevant(action)
	}
	if settingsExtensionRead(action) {
		return s.settingsExtensionActionRelevant(action)
	}
	switch action.Kind {
	case "meegle.projects":
		return s.meegleCatalogActionRelevant(action)
	case "requirements.read", "requirements.comments", "requirements.attachment", "requirements.copy-document", "requirements.copy-comments":
		return s.requirementReaderForAction(action) != nil
	case "workspace.command":
		return s.workspaceCommandActionRelevant(action)
	case "document.list":
		return action.TaskID == s.currentTask().id
	case "document.read":
		return action.TaskID == s.currentTask().id && action.Path == s.documentPath
	case "document.save", "document.rename":
		return true
	case "instructions.read", "instructions.save":
		// Every successful instruction read must seed its autosave cache before
		// the editor's own task/tab generation guard decides whether to display it.
		return true
	case "requirements.refresh":
		if action.TaskID != "" {
			return action.TaskID == s.currentTask().id
		}
		if action.Path != "" {
			return action.Path == s.selectedRequirementURL()
		}
	}
	return true
}

func (s *app) currentNoteName() string {
	if s.noteTab < 0 || s.noteTab >= 3 {
		return ""
	}
	return []string{"AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md"}[s.noteTab]
}

func (s *app) selectedRequirementURL() string {
	if s.selectedRequirement < 0 || s.selectedRequirement >= len(s.requirements) {
		return ""
	}
	return s.requirements[s.selectedRequirement].URL
}

func firstDocument(items []application.Document) string {
	for _, item := range items {
		if item.Kind != "directory" && item.Kind != "dir" && item.Kind != "folder" {
			return item.Path
		}
		if path := firstDocument(item.Children); path != "" {
			return path
		}
	}
	return ""
}

func (s *app) selectDocument(path string) {
	s.documentLinkFragment = ""
	path = normalizeActionPath(path)
	if path == "" || path == s.documentPath || s.materialsFileReadBlocked() {
		return
	}
	if err := s.captureMaterialTextInput(); err != nil {
		s.lastError = err.Error()
		return
	}
	if err := s.captureMarkdownEditorInput(s.markdown); err != nil {
		s.lastError = err.Error()
		return
	}
	if s.documentDirty && s.documentPath != "" {
		s.documentDrafts[s.documentPath] = s.documentContent
		s.documentDirtyMap[s.documentPath] = true
	}
	s.closeNativeMedia()
	s.closeMarkdownEditor()
	s.documentPath = path
	s.revealMaterialsPath(path)
	s.documentKind = documentKindForPath(path)
	s.documentReadOnly = markdownReadingOnly && s.documentKind == "markdown"
	s.documentHash = ""
	s.documentContent = ""
	s.documentReadPending = false
	s.documentDirty = s.documentDirtyMap[path]
	s.documentEditing = false
	opened := false
	for _, tab := range s.documentTabs {
		if tab == path {
			opened = true
			break
		}
	}
	if !opened {
		s.documentTabs = append(s.documentTabs, path)
	}
	s.restoreMaterialsDraftForOpening(path)
	s.documentReadPending = true
	s.act("document.read", s.currentTask().id, "", "", path, "", nil)
}

func (s *app) loadDocuments() {
	if s.materialsFileReadBlocked() || s.backend == nil {
		return
	}
	task := s.currentTask()
	if task.id != "" && !s.documentsRequested {
		s.documentsRequested = true
		s.act("document.list", task.id, "", "", task.materialsPath, "", nil)
	}
}

func reloadsSnapshot(kind string) bool {
	switch kind {
	case "task.create", "task.archive", "task.restore", "task.delete", "task.refresh",
		"codex.run",
		"task.services-add", "task.modules-add", "task.services-retry", "task.tools-retry",
		"repository.add", "repository.remove", "service.add-repositories", "service.save", "service.remove", "service.module-save", "service.module-remove", "config.save", "config.fields.save", "config.materials-root.save", "config.task-root.save", "config.import",
		"config.restore-backup", "migration.apply", "migration.recover",
		"workspace.fetch", "workspace.commit", "workspace.commit-push", "workspace.push", "workspace.batch", "workspace.merge", "workspace.merge-batch", "workspace.repair", "workspace.remove", "workspace.add", "workspace.bootstrap-clear", "workspace.bootstrap-rerun",
		"instructions.save", "requirements.refresh", "requirements.comment", "materials.sync", "task.materials-retry",
		"tag.build", "tag.retry", "tag.target.update", "tag.history.delete", "tag.history.clear", "skills.refresh", "skills.install", "skills.remove",
		"development-tools/snapshot", "development-tools/detect":
		return true
	default:
		return false
	}
}

func actionLabel(kind string) string {
	labels := map[string]string{
		"task.create": "创建任务", "task.archive": "归档任务", "task.restore": "恢复任务", "task.delete": "删除任务", "task.delete-risks": "检查删除风险", "task.refresh": "刷新任务", "task.materials-retry": "关联任务资料",
		"repository.add": "添加仓库", "repository.remove": "移除仓库", "service.add-repositories": "添加仓库", "service.save": "保存服务", "service.remove": "移除服务", "service.module-save": "保存模块", "service.module-remove": "移除模块",
		"config.save": "保存配置", "config.fields.save": "保存设置", "config.import-preview": "预览配置", "config.import": "导入配置",
		"config.task-root.save": "切换任务目录", "development-tools/detect": "检测开发工具", "development-tools/snapshot": "读取开发工具配置",
		"workspace.status": "检查工作区", "workspace.preview": "读取提交预览", "workspace.previews": "读取 Git 变更预览", "workspace.batch": "批量 Git 操作", "workspace.fetch": "获取远程更新", "workspace.commit": "提交更改", "workspace.commit-push": "提交并推送", "workspace.push": "推送更改", "workspace.merge": "合并主分支", "workspace.merge-batch": "批量合并主分支", "workspace.repair-preview": "检查工作区修复方案", "workspace.repair": "修复工作区", "workspace.remove": "移除工作区", "workspace.add": "添加工作区", "workspace.bootstrap-clear": "清除 Bootstrap 警告", "workspace.bootstrap-rerun": "重新执行 Bootstrap", "workspace.command": "运行命令",
		"document.list": "读取资料目录", "document.read": "读取文档", "document.save": "保存文档", "document.mkdir": "创建目录", "document.create": "新建文档", "document.rename": "重命名文档", "document.trash": "移至回收站", "document.copy": "复制文档", "document.open": "打开文档", "document.reveal": "显示文档位置",
		"instructions.read": "读取任务说明", "instructions.save": "保存任务说明", "requirements.refresh": "刷新需求", "requirements.list": "读取需求列表", "requirements.read": "读取需求详情", "requirements.comments": "读取需求评论", "requirements.comment": "发表评论", "materials.sync": "同步需求资料",
		"task.services-add-preview": "预检新增服务", "task.modules-add-preview": "预检新增模块", "task.services-add": "添加服务", "task.modules-add": "添加模块", "task.services-retry": "重试失败服务", "task.tools-retry": "重试工作区工具",
		"tag.build": "构建 Tag", "tag.target.update": "保存测试目标分支", "tag.status": "查询 Tag 状态", "tag.genbu.refresh": "刷新 Genbu 状态", "tag.history": "读取 Tag 历史", "tag.history.refresh": "刷新 Tag 历史和 Genbu", "tag.history.delete": "删除所选Tag历史", "tag.history.clear": "清空Tag历史", "tag.retry": "重试 Tag 操作", "tag.workspace-check": "检查工作区",
		"skills.refresh": "刷新 Skills", "skills.install": "安装 Skill", "skills.remove": "移除 Skill", "codex.extensions": "读取 Codex 扩展", "codex.run": "在 Codex 中打开",
		"codex.extensions/snapshot": "读取 Codex 扩展",
		"tool.json":                 "处理 JSON", "tool.time": "转换时间", "tool.diff": "比较文本", "tool.ports": "查询端口", "tool.port-stop": "结束端口进程",
		"system.open": "打开路径", "system.reveal": "显示路径", "system.terminal": "打开终端", "system.ide": "启动开发工具", "diagnostics.export": "导出诊断信息",
	}
	if label := labels[kind]; label != "" {
		return label
	}
	return kind
}

func tagStatusAction(kind string) bool {
	return kind == "tag.status" || kind == "tag.genbu.refresh"
}

func resultText(result application.ActionResult) string {
	if result.Text != "" {
		return result.Text
	}
	if result.Data == nil {
		return ""
	}
	if text, ok := result.Data.(string); ok {
		return text
	}
	encoded, err := json.MarshalIndent(result.Data, "", "  ")
	if err != nil {
		return fmt.Sprint(result.Data)
	}
	return string(encoded)
}

func dataValue(data any, key string) any {
	if values, ok := data.(map[string]any); ok {
		return values[key]
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var values map[string]any
	if json.Unmarshal(encoded, &values) != nil {
		return nil
	}
	return values[key]
}

func mapData(data any) map[string]any {
	if values, ok := data.(map[string]any); ok {
		return values
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var values map[string]any
	if json.Unmarshal(encoded, &values) != nil {
		return nil
	}
	return values
}

func mapsFromData(data any) []map[string]any {
	if data == nil {
		return nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var values []map[string]any
	if json.Unmarshal(encoded, &values) == nil {
		return values
	}
	return nil
}

func upsertMapByID(items []map[string]any, value any, key string) []map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return items
	}
	var item map[string]any
	if json.Unmarshal(encoded, &item) != nil || item == nil {
		return items
	}
	id, _ := item[key].(string)
	if id == "" {
		return items
	}
	for index, existing := range items {
		if existing[key] == id {
			items[index] = item
			return items
		}
	}
	return append(items, item)
}

func upsertSkillSourceStatus(items []map[string]any, sourceID string, status any) []map[string]any {
	for index, item := range items {
		source := mapData(item["source"])
		if mapString(source, "id") == sourceID {
			item["status"] = mapData(status)
			items[index] = item
			return items
		}
	}
	return items
}

func removeMapByID(items []map[string]any, id string) []map[string]any {
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		itemID := mapString(item, "sourceId")
		if itemID == "" {
			itemID = mapString(item, "id")
		}
		if itemID != id {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func migrationPreviewText(data any) string {
	if data == nil {
		return "后端未返回迁移预览。"
	}
	lines := []string{"任务目录迁移预览"}
	if value := dataString(data, "sourceRoot"); value != "" {
		lines = append(lines, "当前目录："+value)
	}
	if value := dataString(data, "targetRoot"); value != "" {
		lines = append(lines, "目标目录："+value)
	}
	if value := dataString(data, "mode"); value != "" {
		lines = append(lines, "迁移方式："+value)
	}
	lines = append(lines, fmt.Sprintf("任务：%d 个 · 工作区：%d 个 · 数据量：%s", dataInt64(data, "taskCount"), dataInt64(data, "workspaceCount"), formatBytes(dataInt64(data, "totalBytes"))))
	blockers := mapsString(dataValue(data, "blockers"))
	if len(blockers) > 0 {
		lines = append(lines, "暂不可迁移：")
		for _, blocker := range blockers {
			lines = append(lines, "• "+blocker)
		}
	} else if dataBool(data, "canMigrate") {
		lines = append(lines, "检查通过，可以开始迁移。")
	} else {
		lines = append(lines, "当前预览未允许迁移，请检查目录状态。")
	}
	return strings.Join(lines, "\n")
}

func platformResultSummary(kind string, result application.ActionResult) string {
	data := mapData(result.Data)
	lines := []string{}
	appendField := func(label, key string) {
		if value := mapString(data, key); value != "" {
			lines = append(lines, label+value)
		}
	}
	switch kind {
	case "cli.status":
		appendField("工具：", "tool")
		appendField("命令：", "command")
		if installed, ok := data["installed"].(bool); ok {
			if installed {
				lines = append(lines, "状态：已检测到")
			} else {
				lines = append(lines, "状态：未检测到")
			}
		}
		appendField("版本：", "version")
		switch mapString(data, "authenticationState") {
		case "AUTHENTICATED":
			lines = append(lines, "登录：已登录")
		case "LOGIN_REQUIRED":
			lines = append(lines, "登录：未登录")
		case "CHECK_FAILED":
			lines = append(lines, "登录检查失败："+mapString(data, "authenticationError"))
		}
		appendField("检测错误：", "error")
	case "git.version":
		appendField("命令：", "command")
		appendField("版本：", "version")
		appendField("错误：", "error")
	case "proxy.test":
		appendField("结果：", "message")
		appendField("代理：", "endpoint")
	case "diagnostics.export":
		appendField("诊断包：", "path")
		if size := dataInt64(data, "size"); size > 0 {
			lines = append(lines, "大小："+formatBytes(size))
		}
	case "update.check":
		appendField("当前版本：", "currentVersion")
		lines = append(lines, "更新方式：前往 GitHub Releases 手动下载。")
		appendField("页面：", "releasesUrl")
	case "logs.list":
		lines = append(lines, fmt.Sprintf("最近错误记录：%d 条。", len(mapsFromData(result.Data))))
	case "logs.read":
		appendField("日志文件：", "name")
		if value := mapString(data, "text"); value != "" {
			lines = append(lines, value)
		}
	default:
		if result.Text != "" {
			return result.Text
		}
	}
	if len(lines) == 0 {
		if result.Message != "" {
			return result.Message
		}
		return "操作已完成。"
	}
	return strings.Join(lines, "\n")
}

func dataInt64(data any, key string) int64 {
	switch value := dataValue(data, key).(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, unit := range []string{"KB", "MB", "GB", "TB"} {
		value /= 1024
		if value < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}

func mapsString(value any) []string {
	items := make([]string, 0)
	encoded, err := json.Marshal(value)
	if err != nil {
		return items
	}
	_ = json.Unmarshal(encoded, &items)
	return items
}

func dataString(data any, key string) string {
	if values, ok := data.(map[string]any); ok {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var values map[string]any
	if json.Unmarshal(encoded, &values) != nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func dataBool(data any, key string) bool {
	value, _ := dataValue(data, key).(bool)
	return value
}

func (s *app) cancelAction() {
	if !s.canCancelOperation() {
		return
	}
	s.operationCancelling = true
	s.operation = "正在取消，请等待清理完成…"
	s.cancelOperation()
}

func (s *app) canCancelOperation() bool {
	return s.busy && !s.operationCancelling && s.cancelOperation != nil && cancellableOperation(s.operationKind)
}

// OperationRunner defaults to non-cancellable in the source desktop. Only
// operations that explicitly opt in share the global cancellation control.
func cancellableOperation(kind string) bool {
	switch kind {
	case "task.refresh", "task.create", "task.materials-retry", "task.services-add", "task.modules-add", "task.services-retry",
		"workspace.repair-preview", "workspace.repair", "workspace.merge-batch", "diagnostics.export",
		"codex.extensions/marketplaces/add-source", "codex.extensions/marketplaces/refresh-source", "codex.extensions/marketplaces/remove-source",
		"codex.extensions/plugin/install", "codex.extensions/plugin/uninstall",
		"codex.extensions/mcp/status", "codex.extensions/mcp/check", "codex.extensions/mcp/add", "codex.extensions/mcp/edit", "codex.extensions/mcp/set", "codex.extensions/mcp/remove", "codex.ai/mcp/authenticate",
		"skills.sources/save", "skills.sources/catalog", "skills.sources/remove", "skills.install", "skills.update", "skills.remove",
		"cli.auth.start", "cli.auth.logout", "repository.add", "service.add-repositories":
		return true
	default:
		return false
	}
}

func (s *app) setTheme(dark bool) {
	theme := "LIGHT"
	if dark {
		theme = "DARK"
	}
	s.saveConfigField("theme", theme)
}

func actionPayload(values ...string) map[string]any {
	if len(values)%2 != 0 {
		return nil
	}
	payload := make(map[string]any, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		if strings.TrimSpace(values[index]) != "" {
			payload[values[index]] = values[index+1]
		}
	}
	return payload
}
