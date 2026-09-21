package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.AccountTree
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Sell
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.AgentDocumentPreview
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.RequirementMaterialsStatus
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.WorkspaceGitBatchMode
import com.snowball.silverwing.core.WorkspaceGitBatchResult
import com.snowball.silverwing.core.WorkspaceGitStepState
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.WorkspaceModuleRemovalPreview
import com.snowball.silverwing.core.WorkspaceRepairConfirmation
import com.snowball.silverwing.core.WorkspaceRepairPreview
import com.snowball.silverwing.core.WorkspaceToolLaunchStatus
import com.snowball.silverwing.core.health
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path

internal enum class RequirementMaterialsActionGroup {
    TASK_DIRECTORY,
    WORK_DATA,
}

internal fun requirementMaterialsActionGroupFor(directory: String?): RequirementMaterialsActionGroup? =
    directory
        ?.takeIf(String::isNotBlank)
        ?.takeIf { path -> runCatching { Files.isDirectory(Path.of(path), LinkOption.NOFOLLOW_LINKS) }.getOrDefault(false) }
        ?.let { RequirementMaterialsActionGroup.WORK_DATA }

/** Visibility of the optional, task-level action groups in the detail toolbar. */
internal data class TaskDetailToolbarPresentation(
    val showPathActionGroup: Boolean,
    val showGitActionGroup: Boolean,
)

internal fun taskDetailToolbarPresentationFor(config: AppConfig): TaskDetailToolbarPresentation =
    TaskDetailToolbarPresentation(
        showPathActionGroup = config.showTaskDetailPathActionGroup,
        showGitActionGroup = config.showTaskDetailGitActionGroup,
    )

@Composable
internal fun TaskDetail(controller: DesktopApplication, task: TaskManifest, modifier: Modifier) {
    var notes by remember(task.folderName, task.updatedAt) { mutableStateOf("") }
    var notesLoading by remember(task.folderName, task.updatedAt) { mutableStateOf(true) }
    var notesError by remember(task.folderName, task.updatedAt) { mutableStateOf<String?>(null) }
    var notesLoadAttempt by remember(task.folderName, task.updatedAt) { mutableStateOf(0) }
    val templates = controller.agentTaskTemplates
    var selectedTemplateId by remember(task.folderName, task.updatedAt, controller.agentRevision) {
        mutableStateOf(selectedTemplateIdForNotes(notes, templates))
    }
    var pendingTemplate by remember(task.folderName) { mutableStateOf<AgentTaskTemplate?>(null) }
    var confirmArchive by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    var showAddServices by remember(task.folderName) { mutableStateOf(false) }
    var showBatchTag by remember(task.folderName) { mutableStateOf(false) }
    var showBranchInfo by remember(task.folderName) { mutableStateOf(false) }
    var batchGitMode by remember(task.folderName) { mutableStateOf<WorkspaceGitBatchMode?>(null) }
    var lastBatchGitMode by remember(task.folderName) { mutableStateOf(WorkspaceGitBatchMode.PUSH) }
    var batchGitInitialSelection by remember(task.folderName) { mutableStateOf<Set<String>?>(null) }
    var batchGitResult by remember(task.folderName) { mutableStateOf<WorkspaceGitBatchResult?>(null) }
    var addModuleServiceId by remember(task.folderName) { mutableStateOf<String?>(null) }
    var removalPreview by remember(task.folderName) { mutableStateOf<WorkspaceModuleRemovalPreview?>(null) }
    var removalChecking by remember(task.folderName) { mutableStateOf(false) }
    var agentsPreview by remember(task.folderName) { mutableStateOf<AgentDocumentPreview?>(null) }
    var agentsPreviewLoading by remember(task.folderName) { mutableStateOf(false) }
    var agentsPreviewError by remember(task.folderName) { mutableStateOf<String?>(null) }
    var agentsPreviewRequest by remember(task.folderName) { mutableStateOf(0) }
    val group = controller.config.groups.firstOrNull { it.id == task.groupId }
    val tagWorkspaces = task.services.filter { controller.canBuildTag(task, it) }
    val physicalWorkspaces = controller.physicalWorkspaces(task)
    val addableServices = controller.addableServices(task)
    val toolbarPresentation = taskDetailToolbarPresentationFor(controller.config)
    val requirementState = controller.requirementController.stateFor(task)
    val requirementMaterialsDirectory = task.requirementMaterials
        .takeIf { it.status == RequirementMaterialsStatus.READY }
        ?.writeRoot
        ?.takeIf(String::isNotBlank)
    var requirementMaterialsActionGroup by remember(task.folderName, requirementMaterialsDirectory) {
        mutableStateOf<RequirementMaterialsActionGroup?>(null)
    }
    LaunchedEffect(task.folderName, requirementMaterialsDirectory) {
        requirementMaterialsActionGroup = controller.requirementMaterialsActionGroupAsync(requirementMaterialsDirectory)
    }
    val failedTools = task.workspaceToolLaunches.filter { it.status != WorkspaceToolLaunchStatus.OPENED }
    val failedServiceIds = task.services.filter { it.health == WorkspaceHealth.FAILED }
        .map(ServiceWorkspace::groupServiceId).filter(String::isNotBlank).distinct()
    val tagOperationLoading = controller.busy && controller.activeOperation?.contains("Tag") == true
    val allWorkspacesGitLoading = controller.busy && controller.activeOperation?.contains("全部工作区") == true
    val notesReady = !notesLoading && notesError == null
    LaunchedEffect(task.folderName, task.updatedAt, controller.agentRevision, notesLoadAttempt) {
        notesLoading = true
        notesError = null
        try {
            val loaded = controller.readTaskNotesAsync(task)
            notes = loaded
            selectedTemplateId = selectedTemplateIdForNotes(loaded, templates)
            notesLoading = false
        } catch (cancelled: kotlinx.coroutines.CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            notesError = error.message ?: error::class.simpleName ?: "无法读取任务规则"
            controller.showError(error)
            notesLoading = false
        }
    }
    LaunchedEffect(task.folderName, agentsPreviewRequest) {
        if (agentsPreviewRequest == 0) return@LaunchedEffect
        agentsPreviewLoading = true
        agentsPreviewError = null
        agentsPreview = null
        try {
            agentsPreview = controller.previewTaskAgentsAsync(task, notes)
            agentsPreviewLoading = false
        } catch (cancelled: kotlinx.coroutines.CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            agentsPreviewError = error.message ?: error::class.simpleName ?: "无法生成 Agent 文件预览"
            controller.showError(error)
            agentsPreviewLoading = false
        }
    }
    LaunchedEffect(controller.agentRevision) {
        selectedTemplateId = selectedTemplateIdForNotes(notes, templates)
        pendingTemplate = pendingTemplate?.takeIf { pending ->
            templates.any { it.id == pending.id && it.content == pending.content }
        }
    }
    fun applyTemplate(notesResult: TemplateFillResult.Applied) {
        notes = notesResult.notes
        selectedTemplateId = notesResult.selectedTemplateId
        controller.markTaskNotesEdited(task, notesResult.notes)
    }
    Surface(
        modifier,
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(18.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column(Modifier.fillMaxSize().padding(18.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            TaskDetailHeader(
                controller = controller,
                task = task,
                requirementState = requirementState,
                physicalWorkspaces = physicalWorkspaces,
                groupName = group?.name,
                showGroup = controller.config.groups.size > 1,
                onArchive = { confirmArchive = true },
                onDelete = { confirmDelete = true },
            )
            FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (toolbarPresentation.showPathActionGroup) {
                    IconActionGroup {
                        ActionIconButton("复制任务完整路径", { controller.copyText(controller.taskPath(task), "任务路径已复制") }, Modifier.size(34.dp)) {
                            Icon(Icons.Outlined.ContentCopy, "复制任务路径", Modifier.size(18.dp))
                        }
                        ActionIconButton("在任务目录打开终端", { controller.terminal(controller.taskPath(task)) }, Modifier.size(34.dp)) {
                            Icon(Icons.Outlined.Terminal, "终端", Modifier.size(18.dp))
                        }
                        ActionIconButton("打开任务目录", { controller.openDirectory(controller.taskPath(task)) }, Modifier.size(34.dp)) {
                            Icon(Icons.Outlined.FolderOpen, "打开任务目录", Modifier.size(18.dp))
                        }
                    }
                }
                if (controller.config.tagEnabled && tagWorkspaces.isNotEmpty()) {
                    IconActionGroup {
                        ActionIconButton("批量测试Tag", { showBatchTag = true }, Modifier.size(34.dp), enabled = !controller.busy, loading = tagOperationLoading) {
                            Icon(Icons.Outlined.Sell, "批量测试Tag", Modifier.size(18.dp))
                        }
                    }
                }
                if (toolbarPresentation.showGitActionGroup) {
                    GitActionIconGroup(
                        // Selection and the core batch preflight enforce write protection per selected
                        // workspace. A protected workspace must not prevent choosing other workspaces.
                        enabled = !controller.busy && physicalWorkspaces.isNotEmpty(),
                        loading = allWorkspacesGitLoading,
                        scopeLabel = "全部工作区",
                        onCommit = {
                            batchGitInitialSelection = null
                            controller.loadBatchGitPreviews(task)
                            batchGitMode = WorkspaceGitBatchMode.COMMIT
                        },
                        onCommitAndPush = {
                            batchGitInitialSelection = null
                            controller.loadBatchGitPreviews(task)
                            batchGitMode = WorkspaceGitBatchMode.COMMIT_AND_PUSH
                        },
                        onPush = {
                            batchGitInitialSelection = null
                            controller.loadBatchGitPreviews(task)
                            batchGitMode = WorkspaceGitBatchMode.PUSH
                        },
                    )
                }
                IconActionGroup {
                    if (addableServices.isNotEmpty()) {
                        ActionIconButton("添加服务", { showAddServices = true }, Modifier.size(34.dp)) {
                            Icon(Icons.Outlined.Add, "添加服务", Modifier.size(18.dp))
                        }
                    }
                    ActionIconButton("查看分支信息", { showBranchInfo = true }, Modifier.size(34.dp)) {
                        Icon(Icons.Outlined.AccountTree, "分支信息", Modifier.size(18.dp))
                    }
                    ActionIconButton("打开工作数据", { controller.openWorkData(task) }, Modifier.size(34.dp)) {
                        Icon(Icons.Outlined.Folder, "工作数据", Modifier.size(18.dp))
                    }
                    if (requirementMaterialsActionGroup == RequirementMaterialsActionGroup.WORK_DATA) {
                        requirementMaterialsDirectory?.let { path ->
                            ActionIconButton("打开资料目录", { controller.openDirectory(path) }, Modifier.size(34.dp)) {
                                Icon(Icons.Outlined.FolderOpen, "打开资料目录", Modifier.size(18.dp))
                            }
                            ActionIconButton("复制资料目录路径", { controller.copyText(path, "资料目录路径已复制") }, Modifier.size(34.dp)) {
                                Icon(Icons.Outlined.ContentCopy, "复制资料目录路径", Modifier.size(18.dp))
                            }
                        }
                    }
                }
            }
            val firstWorkspaceByService = physicalWorkspaces.groupBy(ServiceWorkspace::groupServiceId).mapValues { it.value.first() }
            physicalWorkspaces.forEach { workspace ->
                WorkspaceCard(
                    controller,
                    task,
                    workspace,
                    showAddModule = firstWorkspaceByService[workspace.groupServiceId] == workspace,
                    onAddModule = { addModuleServiceId = workspace.groupServiceId },
                    onDeleteModule = {
                        removalChecking = controller.taskController.inspectModuleRemoval(task, workspace, { removalPreview = it }, { removalChecking = false })
                    },
                )
            }
            WorkspaceCommandDialog(controller, task)
            if (failedTools.isNotEmpty()) {
                SectionHeader("工作区工具打开失败", "任务创建不受影响，可单独重试失败工具")
                failedTools.forEach { launch ->
                    val option = controller.workspaceToolOptions(task.groupId).firstOrNull { it.id == launch.toolId }
                    OutlinedCard(Modifier.fillMaxWidth()) {
                        Row(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 11.dp), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(option?.displayName ?: launch.toolId, fontWeight = FontWeight.SemiBold)
                                Text(
                                    when (launch.status) {
                                        WorkspaceToolLaunchStatus.PENDING -> "等待打开"
                                        WorkspaceToolLaunchStatus.OPENED -> "已打开"
                                        WorkspaceToolLaunchStatus.FAILED -> "打开失败 · ${launch.message.orEmpty()}"
                                    },
                                    style = MaterialTheme.typography.bodySmall,
                                    color = if (launch.status == WorkspaceToolLaunchStatus.FAILED) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            if (launch.status == WorkspaceToolLaunchStatus.FAILED) {
                                OutlinedButton(
                                    onClick = { controller.retryWorkspaceTool(task, launch.toolId) },
                                    enabled = !controller.busy && option?.available == true,
                                ) { Text("重试") }
                            }
                        }
                    }
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            SectionHeader("任务人工说明", "保存到 TASK-RULES.md；需求和 Worktree 变动不会重写根 AGENTS.md")
            if (templates.isNotEmpty()) {
                Text(
                    "从模板填充（单选）",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
                    templates.forEach { template ->
                        FilterChip(
                            selected = selectedTemplateId == template.id,
                            onClick = {
                                val selected = templates.firstOrNull { it.id == selectedTemplateId }
                                when (val result = resolveTemplateToggle(notes, selected, template)) {
                                    is TemplateFillResult.Applied -> applyTemplate(result)
                                    is TemplateFillResult.NeedsConfirmation -> pendingTemplate = result.target
                                }
                            },
                            enabled = !controller.busy && notesReady,
                            label = { Text(template.name) },
                        )
                    }
                }
            }
            OutlinedTextField(notes, {
                notes = it
                controller.markTaskNotesEdited(task, it)
            }, Modifier.fillMaxWidth(), minLines = 4, maxLines = 6, readOnly = controller.busy || !notesReady, label = { Text("任务说明") })
            if (notesLoading) {
                Text("正在读取任务说明…", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            notesError?.let { error ->
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text("读取失败：$error", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = { notesLoadAttempt++ }, enabled = !controller.busy) { Text("重试") }
                }
            }
            agentsPreviewError?.let { error ->
                Text("预览失败：$error", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                OutlinedButton(
                    onClick = { agentsPreviewRequest++ },
                    enabled = !controller.busy && notesReady && !agentsPreviewLoading,
                ) {
                    if (agentsPreviewLoading) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    else Icon(Icons.Outlined.Visibility, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp)); Text("预览")
                }
                Spacer(Modifier.width(8.dp))
                Button(onClick = { controller.saveTaskNotes(task, notes) }, enabled = !controller.busy && notesReady) {
                    Icon(Icons.Outlined.Save, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("保存")
                }
                if (failedServiceIds.isNotEmpty()) {
                    Spacer(Modifier.width(8.dp))
                    OutlinedButton(onClick = { controller.retryFailedServices(task) }, enabled = !controller.busy) { Text("重试全部失败服务") }
                }
            }
        }
    }
    if (confirmArchive) ConfirmDialog(
        title = "归档任务",
        message = "任务将移至已归档，工作区和代码不会被删除。",
        confirmLabel = "归档任务",
        enabled = !controller.busy,
        onDismiss = { confirmArchive = false },
    ) {
        controller.archiveTask(task, onCompleted = { confirmArchive = false })
    }
    pendingTemplate?.let { template ->
        ConfirmDialog(
            title = "替换任务人工说明？",
            message = "当前说明已被手动修改，应用模板“${template.name}”将替换现有内容。",
            confirmLabel = "替换说明",
            enabled = !controller.busy && notesReady,
            onDismiss = { pendingTemplate = null },
            onConfirm = {
                applyTemplate(TemplateFillResult.Applied(template.content, template.id))
                pendingTemplate = null
            },
        )
    }
    if (confirmDelete) DeleteTaskDialog(controller, task) {
        controller.clearDeleteRisk(task)
        confirmDelete = false
    }
    if (showAddServices) AddTaskServicesDialog(controller, task, onDismiss = { showAddServices = false }) { ids, reuseKeys, selections ->
        controller.addServices(task, ids, reuseKeys, selections) { showAddServices = false }
    }
    addModuleServiceId?.let { serviceId ->
        group?.services?.firstOrNull { it.id == serviceId }?.let { service ->
            AddTaskModuleDialog(controller, task, service, onDismiss = { addModuleServiceId = null })
        }
    }
    removalPreview?.let { preview ->
        WorkspaceModuleRemovalDialog(
            preview,
            onDismiss = { removalPreview = null },
            onConfirm = { acknowledge ->
                controller.taskController.removeModule(task, preview, acknowledge) { _ ->
                    removalPreview = null
                }
            },
        )
    }
    if (controller.config.tagEnabled && showBatchTag) BatchTagDialog(tagWorkspaces, onDismiss = { showBatchTag = false }) { selected ->
        if (controller.buildTags(task, selected)) showBatchTag = false
    }
    if (showBranchInfo) {
        BranchInfoDialog(
            content = controller.branchInfo(task),
            hasRequirementLink = task.requirementLink.isNotBlank(),
            onDismiss = { showBranchInfo = false },
            onCopyServicesWithoutRequirementLink = {
                controller.copyText(controller.branchServices(task, includeRequirementLink = false), "服务已复制（不含需求链接）")
            },
            onCopyServicesWithRequirementLink = {
                controller.copyText(controller.branchServices(task, includeRequirementLink = true), "服务已复制（含需求链接）")
            },
            onCopyBranchInfoWithoutRequirementLink = {
                controller.copyText(controller.branchInfo(task, includeRequirementLink = false), "分支信息已复制（不含需求链接）")
            },
            onCopyBranchInfoWithRequirementLink = {
                controller.copyText(controller.branchInfo(task, includeRequirementLink = true), "分支信息已复制（含需求链接）")
            },
        )
    }
    batchGitMode?.let { mode ->
        BatchGitDialog(
            controller = controller,
            task = task,
            workspaces = physicalWorkspaces,
            mode = mode,
            initialSelection = batchGitInitialSelection,
            onDismiss = { if (!controller.busy) batchGitMode = null },
            onExecute = { selectedKeys, messages, fingerprints ->
                controller.batchGit(task, mode, selectedKeys, messages, fingerprints) { result ->
                    lastBatchGitMode = mode
                    batchGitMode = null
                    batchGitResult = result
                }
            },
        )
    }
    batchGitResult?.let { result ->
        BatchGitResultDialog(
            controller,
            result,
            onDismiss = { batchGitResult = null },
            onRetryFailed = { failed ->
                batchGitResult = null
                batchGitInitialSelection = failed
                controller.loadBatchGitPreviews(task)
                batchGitMode = lastBatchGitMode
            },
        )
    }
    controller.workspaceRepairPreview?.let { preview ->
        WorkspaceRepairDialog(
            preview = preview,
            busy = controller.busy,
            onDismiss = controller::clearWorkspaceRepairPreview,
            onRepair = { confirmation -> controller.repairWorkspace(task, preview, confirmation) },
        )
    }
    controller.workspaceRepairResult?.let { result ->
        AlertDialog(
            onDismissRequest = controller::clearWorkspaceRepairResult,
            title = { Text("工作区修复完成") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(result.serviceName, fontWeight = FontWeight.SemiBold)
                    Text(result.message)
                    result.backupPath?.let { path ->
                        Text("原目录备份：", style = MaterialTheme.typography.labelMedium)
                        SelectionContainer { Text(path, style = MaterialTheme.typography.bodySmall) }
                        OutlinedButton(onClick = { controller.openDirectory(path) }) { Text("打开备份目录") }
                    }
                    if (result.warnings.isNotEmpty()) Text(result.warnings.joinToString("\n"), color = WarningAmber)
                }
            },
            confirmButton = { Button(onClick = controller::clearWorkspaceRepairResult) { Text("关闭") } },
        )
    }
    agentsPreview?.let { preview ->
        TaskAgentsPreviewDialog(
            preview = preview,
            onCopySource = { file -> controller.copyText(markdownPreviewSourceCopyPayload(file), "Markdown 源码已复制") },
            onDismiss = { agentsPreview = null },
        )
    }
}

@Composable
private fun WorkspaceRepairDialog(
    preview: WorkspaceRepairPreview,
    busy: Boolean,
    onDismiss: () -> Unit,
    onRepair: (WorkspaceRepairConfirmation) -> Unit,
) {
    var remoteConfirmed by remember(preview.stateFingerprint) { mutableStateOf(false) }
    var sharedConfirmed by remember(preview.stateFingerprint) { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(if (preview.canRepair) "确认修复工作区" else "工作区无法自动修复") },
        text = {
            Column(
                Modifier.widthIn(min = 560.dp).heightIn(max = 560.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Text(preview.serviceName, fontWeight = FontWeight.SemiBold)
                WorkspaceProblemPill(workspaceIssueLabel(preview.issue, preview.actualBranch, preview.expectedBranch))
                SelectionContainer { Text(preview.workspacePath, style = MaterialTheme.typography.bodySmall) }
                Text(preview.message, color = if (preview.canRepair) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.error)
                preview.backupPath?.let {
                    Text("原目录会保留为备份：", fontWeight = FontWeight.SemiBold)
                    SelectionContainer { Text(it, style = MaterialTheme.typography.bodySmall) }
                }
                if (preview.steps.isNotEmpty()) {
                    HorizontalDivider()
                    preview.steps.forEachIndexed { index, step -> Text("${index + 1}. $step") }
                }
                if (preview.requiresRemoteReuseConfirmation) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(remoteConfirmed, { remoteConfirmed = it })
                        Text("确认复用远程分支 ${preview.remote}/${preview.expectedBranch}")
                    }
                }
                if (preview.requiresSharedBranchConfirmation) {
                    Text("以下 Worktree 已检出同一分支：", color = MaterialTheme.colorScheme.error)
                    preview.occupiedWorktreePaths.forEach { SelectionContainer { Text(it, style = MaterialTheme.typography.bodySmall) } }
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(sharedConfirmed, { sharedConfirmed = it })
                        Text("我了解多个 Worktree 将共享同一分支", color = MaterialTheme.colorScheme.error)
                    }
                }
            }
        },
        confirmButton = {
            if (preview.canRepair) Button(
                onClick = { onRepair(WorkspaceRepairConfirmation(remoteConfirmed, sharedConfirmed)) },
                enabled = !busy && (!preview.requiresRemoteReuseConfirmation || remoteConfirmed) &&
                    (!preview.requiresSharedBranchConfirmation || sharedConfirmed),
            ) { Text("确认修复") } else Button(onClick = onDismiss) { Text("关闭") }
        },
        dismissButton = if (preview.canRepair) ({ TextButton(onClick = onDismiss, enabled = !busy) { Text("取消") } }) else null,
    )
}

@Composable
private fun WorkspaceModuleRemovalDialog(
    preview: WorkspaceModuleRemovalPreview,
    onDismiss: () -> Unit,
    onConfirm: (Boolean) -> Unit,
) {
    var acknowledged by remember(preview.fingerprint) { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("删除模块 ${preview.moduleName}") },
        text = {
            Column(Modifier.widthIn(min = 600.dp).heightIn(max = 560.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(if (preview.pathMissing) "工作区目录已缺失，将只清理任务记录。" else "工作区将先移动到同级临时备份；任务清单和 AGENTS.md 更新成功后才永久清理。")
                SelectionContainer {
                    Text(
                        buildString {
                            appendLine("路径：${preview.workspacePath}")
                            appendLine("分支：${preview.branch}")
                            appendLine("基础分支：${preview.baseRef.orEmpty()}")
                            appendLine("相对基础分支：ahead ${preview.commitsAheadOfBase} / behind ${preview.commitsBehindBase}")
                            appendLine("未推送提交：${preview.unpushedCommits}")
                            if (preview.changedFiles.isNotEmpty()) {
                                appendLine("未提交文件：")
                                preview.changedFiles.forEach { appendLine("  $it") }
                            }
                        },
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                if (preview.requiresRiskConfirmation) {
                    Surface(color = MaterialTheme.colorScheme.errorContainer, shape = RoundedCornerShape(10.dp)) {
                        Row(Modifier.fillMaxWidth().clickable { acknowledged = !acknowledged }.padding(10.dp), verticalAlignment = Alignment.CenterVertically) {
                            Checkbox(acknowledged, { acknowledged = it })
                            Text("我确认未提交文件会永久丢失，并接受提交差异及未推送提交不再保留在该工作区中的风险。", color = MaterialTheme.colorScheme.onErrorContainer)
                        }
                    }
                }
            }
        },
        confirmButton = {
            Button(onClick = { onConfirm(acknowledged) }, enabled = !preview.requiresRiskConfirmation || acknowledged) { Text("删除模块") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

internal enum class BatchCommitDisposition {
    NOT_APPLICABLE,
    LOADING,
    MESSAGE_REQUIRED,
    NO_CHANGES,
    PUSH_ONLY,
}

internal fun batchCommitDisposition(
    mode: WorkspaceGitBatchMode,
    preview: com.snowball.silverwing.core.WorkspaceGitChangePreview?,
): BatchCommitDisposition = when {
    mode == WorkspaceGitBatchMode.PUSH -> BatchCommitDisposition.NOT_APPLICABLE
    preview == null -> BatchCommitDisposition.LOADING
    preview.files.isNotEmpty() -> BatchCommitDisposition.MESSAGE_REQUIRED
    mode == WorkspaceGitBatchMode.COMMIT_AND_PUSH -> BatchCommitDisposition.PUSH_ONLY
    else -> BatchCommitDisposition.NO_CHANGES
}

internal fun batchCommitMessagesValid(
    mode: WorkspaceGitBatchMode,
    selectedKeys: Set<String>,
    previews: Map<String, com.snowball.silverwing.core.WorkspaceGitChangePreview>,
    messages: Map<String, String>,
): Boolean {
    if (mode == WorkspaceGitBatchMode.PUSH) return true
    return selectedKeys.all { key ->
        when (batchCommitDisposition(mode, previews[key])) {
            BatchCommitDisposition.MESSAGE_REQUIRED -> !messages[key].isNullOrBlank()
            BatchCommitDisposition.NO_CHANGES, BatchCommitDisposition.PUSH_ONLY -> true
            BatchCommitDisposition.NOT_APPLICABLE, BatchCommitDisposition.LOADING -> false
        }
    }
}

@Composable
private fun BatchGitDialog(
    controller: DesktopApplication,
    task: TaskManifest,
    workspaces: List<ServiceWorkspace>,
    mode: WorkspaceGitBatchMode,
    initialSelection: Set<String>? = null,
    onDismiss: () -> Unit,
    onExecute: (Set<String>, Map<String, String>, Map<String, String>) -> Unit,
) {
    var selectedKeys by remember(task.taskDirectoryName, mode, workspaces) {
        mutableStateOf(initialSelection ?: emptySet())
    }
    val messages = remember(task.taskDirectoryName, mode) {
        mutableStateMapOf<String, String>().apply {
            workspaces.forEach { workspace ->
                put(controller.workspaceKey(workspace), controller.defaultCommitMessage(task, workspace))
            }
        }
    }
    val previews = (controller.batchGitPreviewState as? BatchGitPreviewState.Loaded)?.previews.orEmpty()
    val previewReady = controller.batchGitPreviewState is BatchGitPreviewState.Loaded
    val messagesValid = batchCommitMessagesValid(mode, selectedKeys, previews, messages)
    val selectedDispositions = selectedKeys.map { batchCommitDisposition(mode, previews[it]) }
    val commitAndPushOnlyPushes = mode == WorkspaceGitBatchMode.COMMIT_AND_PUSH &&
        selectedDispositions.isNotEmpty() && selectedDispositions.all { it == BatchCommitDisposition.PUSH_ONLY }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                when (mode) {
                    WorkspaceGitBatchMode.COMMIT -> "选择要提交的工作区"
                    WorkspaceGitBatchMode.PUSH -> "选择要推送的工作区"
                    WorkspaceGitBatchMode.COMMIT_AND_PUSH -> "选择要提交并推送的工作区"
                },
            )
        },
        text = {
            Column(
                Modifier.widthIn(min = 620.dp, max = 820.dp).heightIn(max = 560.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text("确认后仅预检并操作所选物理工作区。", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    TextButton(onClick = { selectedKeys = workspaces.map(controller::workspaceKey).toSet() }) { Text("全选") }
                    TextButton(onClick = { selectedKeys = emptySet() }) { Text("全不选") }
                }
                workspaces.forEach { workspace ->
                    val key = controller.workspaceKey(workspace)
                    val health = controller.gitHealth(workspace)
                    val preview = previews[key]
                    val disposition = batchCommitDisposition(mode, preview)
                    OutlinedCard(Modifier.fillMaxWidth()) {
                        Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Checkbox(
                                    checked = key in selectedKeys,
                                    onCheckedChange = { checked ->
                                        selectedKeys = if (checked) selectedKeys + key else selectedKeys - key
                                    },
                                    enabled = !controller.busy,
                                )
                                Column(Modifier.weight(1f)) {
                                    Text(workspace.moduleName.ifBlank { workspace.serviceName }, fontWeight = FontWeight.SemiBold)
                                    Text(
                                        "${health?.actualBranch ?: workspace.branch} · ${workspace.pushRemote} · ${workspace.worktreePath}",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        maxLines = 2,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                }
                                if (key in selectedKeys) when (disposition) {
                                    BatchCommitDisposition.PUSH_ONLY -> MetaPill("无本地变更，仅推送已有提交")
                                    BatchCommitDisposition.NO_CHANGES -> MetaPill("无本地变更，跳过提交")
                                    BatchCommitDisposition.LOADING -> MetaPill("正在确认变更")
                                    else -> Unit
                                }
                            }
                            if (key in selectedKeys && disposition == BatchCommitDisposition.MESSAGE_REQUIRED) {
                                OutlinedTextField(
                                    value = messages[key].orEmpty(),
                                    onValueChange = { messages[key] = it },
                                    modifier = Modifier.fillMaxWidth(),
                                    label = { Text("提交信息") },
                                    minLines = 2,
                                )
                            }
                            if (key in selectedKeys && preview != null && preview.files.isNotEmpty()) {
                                Text("变更文件 ${preview.files.size} 个", style = MaterialTheme.typography.labelMedium)
                                SelectionContainer {
                                    Text(
                                        preview.files.take(20).joinToString("\n") + if (preview.files.size > 20) "\n…还有 ${preview.files.size - 20} 个" else "",
                                        style = MaterialTheme.typography.bodySmall,
                                        fontFamily = FontFamily.Monospace,
                                    )
                                }
                                if (preview.diffStat.isNotBlank()) SelectionContainer {
                                    Text(preview.diffStat, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, fontFamily = FontFamily.Monospace)
                                }
                            }
                        }
                    }
                }
                if (commitAndPushOnlyPushes) {
                    Surface(
                        color = MaterialTheme.colorScheme.tertiaryContainer.copy(alpha = 0.55f),
                        shape = RoundedCornerShape(10.dp),
                    ) {
                        Text(
                            "所选工作区都没有未提交文件；本次不会创建新提交，只会推送已有的未推送提交。",
                            Modifier.fillMaxWidth().padding(11.dp),
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
                when (val previewState = controller.batchGitPreviewState) {
                    BatchGitPreviewState.Idle, BatchGitPreviewState.Loading -> Text("正在读取 Git 变更预览…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    is BatchGitPreviewState.Failed -> SelectionContainer { Text(previewState.message, color = MaterialTheme.colorScheme.error) }
                    is BatchGitPreviewState.Loaded -> Unit
                }
            }
        },
        confirmButton = {
            Button(
                onClick = { onExecute(selectedKeys, messages.toMap(), previews.mapValues { it.value.fingerprint }) },
                enabled = !controller.busy && previewReady && selectedKeys.isNotEmpty() && messagesValid,
            ) {
                Text(if (commitAndPushOnlyPushes) "确认仅推送" else "确认执行")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !controller.busy) { Text("取消") } },
    )
}

@Composable
private fun BatchGitResultDialog(controller: DesktopApplication, result: WorkspaceGitBatchResult, onDismiss: () -> Unit, onRetryFailed: (Set<String>) -> Unit) {
    val failedKeys = result.items.filter {
        it.commitState == WorkspaceGitStepState.FAILED || it.pushState == WorkspaceGitStepState.FAILED
    }.map { it.workspacePath }.toSet()
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("批量 Git 操作结果") },
        text = {
            Column(
                Modifier.widthIn(min = 620.dp, max = 820.dp).heightIn(max = 560.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(9.dp),
            ) {
                result.items.forEach { item ->
                    val failed = item.commitState == WorkspaceGitStepState.FAILED || item.pushState == WorkspaceGitStepState.FAILED
                    val skipped = item.commitState == WorkspaceGitStepState.SKIPPED && item.pushState == WorkspaceGitStepState.NOT_RUN
                    OutlinedCard(Modifier.fillMaxWidth()) {
                        Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Text(item.serviceName, Modifier.weight(1f), fontWeight = FontWeight.SemiBold)
                                StatusPill(if (failed) "FAILED" else if (skipped) "SKIPPED" else "SUCCESS")
                            }
                            Text(item.branch, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            SelectionContainer { Text(item.message, color = if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodySmall) }
                            if (failed) TextButton(onClick = { controller.openDirectory(item.workspacePath) }) { Text("打开失败工作区") }
                        }
                    }
                }
            }
        },
        confirmButton = { Button(onClick = onDismiss) { Text("关闭") } },
        dismissButton = {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                TextButton(onClick = {
                    controller.copyText(
                        result.items.joinToString("\n") { "${it.serviceName} [${it.branch}] ${it.message}" },
                        "Git 操作结果已复制",
                    )
                }) { Text("复制全部结果") }
                if (failedKeys.isNotEmpty()) OutlinedButton(onClick = { onRetryFailed(failedKeys) }) { Text("仅重试失败项") }
            }
        },
    )
}

@Composable
private fun TaskAgentsPreviewDialog(
    preview: AgentDocumentPreview,
    onCopySource: (MarkdownPreviewFile) -> Unit,
    onDismiss: () -> Unit,
) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(
            Modifier.width(860.dp).height(640.dp),
            shape = RoundedCornerShape(22.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.fillMaxSize()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("Agent 文件预览", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge)
                    MetaPill("与保存后内容一致")
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Surface(
                    Modifier.weight(1f).fillMaxWidth().padding(18.dp),
                    color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.55f),
                    shape = RoundedCornerShape(12.dp),
                ) {
                    MarkdownFileTabsPreview(
                        files = preview.files.map { MarkdownPreviewFile(it.relativePath, it.content) },
                        modifier = Modifier.fillMaxSize(),
                        initialPath = preview.rootFile.relativePath,
                        onCopySource = onCopySource,
                    )
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 14.dp), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onDismiss) { Text("关闭") }
                }
            }
        }
    }
}
