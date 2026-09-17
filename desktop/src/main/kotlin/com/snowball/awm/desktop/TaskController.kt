package com.snowball.awm.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.awm.core.AddGroupedTaskServicesRequest
import com.snowball.awm.core.AddTaskModulesRequest
import com.snowball.awm.core.AppConfig
import com.snowball.awm.core.BranchReuseConflict
import com.snowball.awm.core.BranchReuseKey
import com.snowball.awm.core.ConfigStore
import com.snowball.awm.core.CURRENT_PRODUCT_VERSION
import com.snowball.awm.core.CreateGroupedTaskRequest
import com.snowball.awm.core.DeleteRisk
import com.snowball.awm.core.ManifestStore
import com.snowball.awm.core.ModuleBaseOverride
import com.snowball.awm.core.RepositoryConfig
import com.snowball.awm.core.RepositoryInspector
import com.snowball.awm.core.ServiceWorkspace
import com.snowball.awm.core.TaskApplicationService
import com.snowball.awm.core.TaskManifest
import com.snowball.awm.core.TaskServiceSelection
import com.snowball.awm.core.TaskBranchCatalog
import com.snowball.awm.core.TaskBranchCatalogResult
import com.snowball.awm.core.TaskBranchCatalogProgress
import com.snowball.awm.core.WorkspaceGitHealth
import com.snowball.awm.core.WorkspaceGitFileChange
import com.snowball.awm.core.WorkspaceGitFilePreviewService
import com.snowball.awm.core.WorkspaceGitFilePreview
import com.snowball.awm.core.WorkspaceGitHealthState
import com.snowball.awm.core.WorkspaceGitCommit
import com.snowball.awm.core.WorkspaceGitHistoryService
import com.snowball.awm.core.WorkspaceGitStatusService
import com.snowball.awm.core.WorkspaceCommandConfig
import com.snowball.awm.core.WorkspaceCommandService
import com.snowball.awm.core.CommandOutputLine
import com.snowball.awm.core.WorkspaceToolLaunchService
import com.snowball.awm.core.WorkspaceGitOperationService
import com.snowball.awm.core.WorkspaceGitBatchMode
import com.snowball.awm.core.WorkspaceGitBatchResult
import com.snowball.awm.core.WorkspaceGitChangePreview
import com.snowball.awm.core.WorkspaceRepairConfirmation
import com.snowball.awm.core.WorkspaceRepairPreview
import com.snowball.awm.core.WorkspaceRepairResult
import com.snowball.awm.core.WorkspaceModuleRemovalConfirmation
import com.snowball.awm.core.WorkspaceModuleRemovalPreview
import com.snowball.awm.core.WorkspaceModuleRemovalResult
import com.snowball.awm.core.WorkspaceHealth
import com.snowball.awm.core.CommitMessageTemplate
import com.snowball.awm.core.EventSink
import com.snowball.awm.core.NoOpEventSink
import com.snowball.awm.core.info
import com.snowball.awm.core.toInfo
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withContext
import java.nio.file.Path

enum class WorkspaceCommandExecutionStatus {
    RUNNING,
    SUCCEEDED,
    FAILED,
    CANCELLED,
}

data class WorkspaceCommandExecutionState(
    val taskKey: String,
    val workspaceKey: String,
    val workspaceLabel: String,
    val command: WorkspaceCommandConfig,
    val workingDirectory: String,
    val status: WorkspaceCommandExecutionStatus,
    val lines: List<CommandOutputLine> = emptyList(),
    val truncated: Boolean = false,
    val exitCode: Int? = null,
    val error: String? = null,
    val startedAtMillis: Long = System.currentTimeMillis(),
    val durationMillis: Long? = null,
)

/** Immutable state exposed by task use cases to the presentation layer. */
data class TaskUiState(
    val config: AppConfig,
    val tasks: List<TaskManifest>,
    val selectedTask: TaskManifest?,
    val gitHealth: Map<String, WorkspaceGitHealth>,
    val deleteRisks: Map<String, DeleteRiskInspection>,
    val busy: Boolean,
)

sealed interface TaskBranchCandidatesState {
    data object Idle : TaskBranchCandidatesState
    data class Loading(val completed: Int = 0, val total: Int = 0) : TaskBranchCandidatesState
    data class Loaded(val result: TaskBranchCatalogResult) : TaskBranchCandidatesState
    data class Failed(val message: String) : TaskBranchCandidatesState
}

sealed interface BatchGitPreviewState {
    data object Idle : BatchGitPreviewState
    data object Loading : BatchGitPreviewState
    data class Loaded(val previews: Map<String, WorkspaceGitChangePreview>) : BatchGitPreviewState
    data class Failed(val message: String) : BatchGitPreviewState
}

/**
 * Owns task lifecycle and workspace-health use cases.
 *
 * This controller depends on application services and small callbacks only; it
 * deliberately has no reference to [DesktopApplication] or another controller.
 */
class TaskController internal constructor(
    private val session: AppSessionStore,
    private val configStore: ConfigStore,
    private val manifests: ManifestStore,
    private val repositoryInspector: RepositoryInspector,
    private val tasks: TaskApplicationService,
    private val gitStatus: WorkspaceGitStatusService,
    private val gitFilePreviews: WorkspaceGitFilePreviewService,
    private val gitHistory: WorkspaceGitHistoryService,
    private val workspaceCommands: WorkspaceCommandService,
    private val workspaceTools: WorkspaceToolLaunchService,
    private val gitOperations: WorkspaceGitOperationService,
    private val taskBranchCatalog: TaskBranchCatalog,
    private val operations: OperationRunner,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val desktopActions: DesktopActions,
    private val onConfigApplied: (AppConfig) -> Unit,
    private val onTasksChanged: () -> Unit,
    private val onRequirementsRefresh: (Boolean) -> Unit,
    private val onRequirementSelected: (TaskManifest) -> Unit,
    private val onError: (Throwable) -> Unit,
    private val isBusy: () -> Boolean,
    private val events: EventSink = NoOpEventSink,
) {
    private var gitStatusRevision = 0L
    private var gitStatusJob: Job? = null
    private var branchCandidateRevision = 0L
    private var branchCandidateJob: Job? = null
    private var workspaceCommandRevision = 0L
    private var workspaceCommandJob: Job? = null
    private var workspaceCommandActive by mutableStateOf(false)
    private val createBranchReuseInspection = CreateTaskBranchReuseInspection(scope, ioDispatcher, onError)
    private var gitHealth by mutableStateOf<Map<String, WorkspaceGitHealth>>(emptyMap())
    private var deleteRisks by mutableStateOf<Map<String, DeleteRiskInspection>>(emptyMap())
    var repairPreview by mutableStateOf<WorkspaceRepairPreview?>(null)
        private set
    var repairResult by mutableStateOf<WorkspaceRepairResult?>(null)
        private set
    var branchCandidates by mutableStateOf<TaskBranchCandidatesState>(TaskBranchCandidatesState.Idle)
        private set
    var batchGitPreviews by mutableStateOf<BatchGitPreviewState>(BatchGitPreviewState.Idle)
        private set
    var workspaceCommandState by mutableStateOf<WorkspaceCommandExecutionState?>(null)
        private set

    val state: TaskUiState
        get() = TaskUiState(
            config = session.config,
            tasks = session.tasks,
            selectedTask = session.selectedTask,
            gitHealth = gitHealth,
            deleteRisks = deleteRisks,
            busy = isBusy(),
        )

    fun select(task: TaskManifest) {
        cancelWorkspaceCommandForTaskSwitch()
        session.selectedTask = task
        repairPreview = null
        repairResult = null
        batchGitPreviews = BatchGitPreviewState.Idle
        refreshGitStatus()
        onRequirementSelected(task)
    }

    /** Explicit refresh is the only task path that validates every repository. */
    fun refresh(): Boolean = operations.run("正在刷新状态…", "状态刷新完成", cancellable = true, block = {
        val latest = configStore.load()
        val validated = latest.repositories.map { persisted ->
            val inspected = repositoryInspector.inspect(Path.of(persisted.rootPath))
            require(samePathIdentity(persisted.gitCommonDirectory, inspected.gitCommonDirectory)) {
                "仓库身份已变化：${persisted.name} 当前指向 ${inspected.gitCommonDirectory}，" +
                    "与配置中的 ${persisted.gitCommonDirectory} 不一致"
            }
            inspected.copy(id = persisted.id)
        }
        val validatedById = validated.associateBy(RepositoryConfig::id)
        val snapshotById = latest.repositories.associateBy(RepositoryConfig::id)
        val refreshedConfig = configStore.update { current ->
            require(
                current.repositories.size == snapshotById.size && current.repositories.all { repository ->
                    val snapshot = snapshotById[repository.id]
                    snapshot != null &&
                        samePathIdentity(snapshot.rootPath, repository.rootPath) &&
                        samePathIdentity(snapshot.gitCommonDirectory, repository.gitCommonDirectory)
                },
            ) { "刷新期间仓库配置已经变化，请重新刷新" }
            current.copy(repositories = current.repositories.map { validatedById.getValue(it.id) })
        }
        val loaded = scanTasks(refreshedConfig)
        loaded.manifests.forEach { task ->
            tasks.refreshAgents(refreshedConfig, taskDirectory(refreshedConfig, task))
        }
        refreshedConfig to loaded
    }, onSuccess = { (updatedConfig, loaded) ->
        val selectedFolder = session.selectedTask?.folderName
        onConfigApplied(updatedConfig)
        session.replaceTasks(loaded.manifests, selectedFolder)
        onTasksChanged()
        onRequirementsRefresh(true)
        refreshGitStatus()
        loaded.warning?.let { onError(IllegalStateException(it)) }
    })

    fun create(
        name: String,
        branch: String,
        groupId: String,
        serviceIds: List<String>,
        link: String,
        notes: String,
        toolIds: List<String>,
        confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        onCompleted: () -> Unit = {},
    ): Boolean = operations.run("正在创建任务…", "任务已创建", cancellable = true, block = {
        val created = tasks.create(
            session.config,
            CreateGroupedTaskRequest(name, branch, groupId, serviceIds, link, notes, confirmedBranchReuseKeys, serviceSelections = serviceSelections),
        )
        workspaceTools.launch(taskDirectory(session.config, created), created, toolIds)
    }, onSuccess = { created ->
        reloadTasks(created.folderName)
        session.navigation = NavigationItem.TASKS
        onCompleted()
    })

    fun archive(task: TaskManifest, onCompleted: () -> Unit = {}): Boolean =
        operations.run("正在归档任务…", "任务已归档", block = {
            tasks.archive(session.config, taskDirectory(task), false)
        }, onSuccess = {
            reloadTasks(it.folderName)
            onCompleted()
        })

    fun restore(task: TaskManifest, onCompleted: () -> Unit = {}): Boolean =
        operations.run("正在恢复任务…", "任务已恢复", block = {
            tasks.restore(session.config, taskDirectory(task))
        }, onSuccess = {
            reloadTasks(it.folderName)
            session.navigation = NavigationItem.TASKS
            onCompleted()
        })

    fun requestDeleteRisk(task: TaskManifest) {
        val key = task.taskDirectoryName
        if (deleteRisks[key]?.loading == true) return
        deleteRisks = deleteRisks + (key to DeleteRiskInspection())
        val config = session.config
        scope.launch {
            val result = withContext(ioDispatcher) {
                runCatching { tasks.inspectDeleteRisk(config, taskDirectory(config, task)) }
            }
            deleteRisks = deleteRisks + (key to result.fold(
                onSuccess = { DeleteRiskInspection(loading = false, risks = it) },
                onFailure = { DeleteRiskInspection(loading = false, error = it.message ?: "删除风险检查失败") },
            ))
        }
    }

    fun clearDeleteRisk(task: TaskManifest) {
        deleteRisks = deleteRisks - task.taskDirectoryName
    }

    fun delete(task: TaskManifest, discardChanges: Boolean, onCompleted: () -> Unit = {}): Boolean =
        operations.run("正在删除任务…", "任务已删除", block = {
            tasks.delete(session.config, taskDirectory(task), discardChanges)
        }, onSuccess = { reloadTasks(); onCompleted() })

    fun retryRequirementMaterials(task: TaskManifest, onCompleted: () -> Unit = {}): Boolean =
        operations.run("正在重试需求资料目录…", "需求资料目录已更新", cancellable = true, block = {
            tasks.retryRequirementMaterials(session.config, taskDirectory(task))
        }, onSuccess = { updated ->
            reloadTasks(updated.folderName)
            onCompleted()
        })

    fun addServices(
        task: TaskManifest,
        serviceIds: List<String>,
        confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        onCompleted: () -> Unit = {},
    ): Boolean =
        operations.run("正在追加服务…", "服务已追加", cancellable = true, block = {
            tasks.addServices(
                session.config,
                taskDirectory(task),
                AddGroupedTaskServicesRequest(serviceIds, confirmedBranchReuseKeys, serviceSelections = serviceSelections),
            )
        }, onSuccess = { reloadTasks(it.folderName); onCompleted() })

    fun inspectCreateBranchReuse(
        name: String,
        branch: String,
        groupId: String,
        serviceIds: List<String>,
        link: String,
        notes: String,
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        onResolved: (List<BranchReuseConflict>) -> Unit,
        onFinished: () -> Unit,
    ): Boolean {
        if (isBusy()) return false
        val config = session.config
        createBranchReuseInspection.start(
            inspect = {
                tasks.inspectCreateBranchReuse(
                    config,
                    CreateGroupedTaskRequest(name, branch, groupId, serviceIds, link, notes, serviceSelections = serviceSelections),
                )
            },
            onResolved = onResolved,
            onFinished = onFinished,
        )
        return true
    }

    fun cancelCreateBranchReuseInspection() = createBranchReuseInspection.cancel()

    fun inspectAddServicesBranchReuse(
        task: TaskManifest,
        serviceIds: List<String>,
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        onResolved: (List<BranchReuseConflict>) -> Unit,
        onFinished: () -> Unit,
    ): Boolean {
        if (isBusy()) return false
        val config = session.config
        val directory = taskDirectory(task)
        scope.launch {
            val result = withContext(ioDispatcher) {
                runCatching {
                    tasks.inspectAddServicesBranchReuse(config, directory, AddGroupedTaskServicesRequest(serviceIds, serviceSelections = serviceSelections))
                }
            }
            result.onSuccess(onResolved).onFailure(onError)
            onFinished()
        }
        return true
    }

    fun inspectAddModulesBranchReuse(
        task: TaskManifest,
        request: AddTaskModulesRequest,
        onResolved: (List<BranchReuseConflict>) -> Unit,
        onFinished: () -> Unit,
    ): Boolean {
        if (isBusy()) return false
        val config = session.config
        val directory = taskDirectory(task)
        scope.launch {
            val result = withContext(ioDispatcher) { runCatching { tasks.inspectAddModulesBranchReuse(config, directory, request) } }
            result.onSuccess(onResolved).onFailure(onError)
            onFinished()
        }
        return true
    }

    fun addModules(task: TaskManifest, request: AddTaskModulesRequest, onCompleted: () -> Unit = {}): Boolean =
        operations.run("正在添加模块…", "模块已添加", cancellable = true, block = {
            tasks.addModules(session.config, taskDirectory(task), request)
        }, onSuccess = { reloadTasks(it.folderName); onCompleted() })

    fun inspectModuleRemoval(
        task: TaskManifest,
        workspace: ServiceWorkspace,
        onResolved: (WorkspaceModuleRemovalPreview) -> Unit,
        onFinished: () -> Unit,
    ): Boolean {
        if (isBusy()) return false
        val config = session.config
        val directory = taskDirectory(task)
        scope.launch {
            val result = withContext(ioDispatcher) {
                runCatching { tasks.inspectModuleRemoval(config, directory, workspaceKey(workspace)) }
            }
            result.onSuccess(onResolved).onFailure(onError)
            onFinished()
        }
        return true
    }

    fun removeModule(
        task: TaskManifest,
        preview: WorkspaceModuleRemovalPreview,
        acknowledgeDataLoss: Boolean,
        onCompleted: (WorkspaceModuleRemovalResult) -> Unit = {},
    ): Boolean = operations.run("正在删除模块…", "模块已删除", block = {
        tasks.removeModule(
            session.config,
            taskDirectory(task),
            preview,
            WorkspaceModuleRemovalConfirmation(preview.fingerprint, acknowledgeDataLoss),
        )
    }, onSuccess = { result ->
        reloadTasks(result.manifest.folderName)
        onCompleted(result)
        result.cleanupError?.let { message ->
            onError(
                IllegalStateException(
                    "$message\n模块已从任务中移除，但临时备份未能清理：${result.retainedBackupPath.orEmpty()}",
                ),
            )
        }
    })

    fun retry(task: TaskManifest, serviceIds: List<String>? = null): Boolean =
        operations.run("正在重试失败服务…", "失败服务已重试", cancellable = true, block = {
            tasks.retryFailedServices(session.config, taskDirectory(task), serviceIds)
        }, onSuccess = { reloadTasks(it.folderName) })

    fun retryWorkspaceTool(task: TaskManifest, toolId: String): Boolean =
        operations.run("正在重新打开工作区工具…", "工作区工具已重新打开", block = {
            workspaceTools.retry(taskDirectory(task), task, toolId)
        }, onSuccess = { reloadTasks(it.folderName) })

    fun clearWorkspaceWarnings(task: TaskManifest, workspace: ServiceWorkspace): Boolean =
        operations.run("正在清除警告…", "警告已清除", block = {
            tasks.clearWorkspaceWarnings(session.config, taskDirectory(task), workspace.worktreePath)
        }, onSuccess = { reloadTasks(it.folderName) })

    fun rerunWorkspaceBootstrap(task: TaskManifest, workspace: ServiceWorkspace): Boolean =
        operations.run("正在重新执行 Bootstrap…", "Bootstrap 已重新执行", block = {
            tasks.rerunWorkspaceBootstrap(session.config, taskDirectory(task), workspace.worktreePath)
        }, onSuccess = { reloadTasks(it.folderName) })

    fun refreshGitStatus() {
        gitStatusJob?.cancel()
        val task = session.selectedTask ?: run {
            gitHealth = emptyMap()
            return
        }
        val revision = ++gitStatusRevision
        val paths = task.services.map(::normalizedWorkspacePath).distinct()
        gitHealth = paths.associateWith { WorkspaceGitHealth(WorkspaceGitHealthState.CHECKING) }
        gitStatusJob = scope.launch {
            val inspected = gitStatus.inspect(task.services) { path, health ->
                if (revision == gitStatusRevision && session.selectedTask?.taskDirectoryName == task.taskDirectoryName) {
                    gitHealth = gitHealth + (path.toString() to health)
                }
            }
                .mapKeys { (path, _) -> path.toString() }
            if (revision == gitStatusRevision && session.selectedTask?.taskDirectoryName == task.taskDirectoryName) {
                gitHealth = inspected
            }
        }
    }

    fun gitHealth(workspace: ServiceWorkspace): WorkspaceGitHealth? = gitHealth[normalizedWorkspacePath(workspace)]

    fun loadTaskBranchCandidates(groupId: String, serviceIds: Set<String>) {
        val revision = ++branchCandidateRevision
        branchCandidateJob?.cancel()
        if (serviceIds.isEmpty()) {
            branchCandidates = TaskBranchCandidatesState.Idle
            branchCandidateJob = null
            return
        }
        branchCandidates = TaskBranchCandidatesState.Loading()
        val config = session.config
        branchCandidateJob = scope.launch {
            val result = runCatching {
                withContext(ioDispatcher) {
                    taskBranchCatalog.list(config, groupId, serviceIds) { progress: TaskBranchCatalogProgress ->
                        scope.launch {
                            if (
                                revision == branchCandidateRevision &&
                                branchCandidates is TaskBranchCandidatesState.Loading &&
                                progress.completed >= (branchCandidates as TaskBranchCandidatesState.Loading).completed
                            ) {
                                branchCandidates = TaskBranchCandidatesState.Loading(progress.completed, progress.total)
                            }
                        }
                    }
                }
            }
            if (revision != branchCandidateRevision) return@launch
            branchCandidates = result.fold(
                onSuccess = TaskBranchCandidatesState::Loaded,
                onFailure = { TaskBranchCandidatesState.Failed(it.message ?: "远程分支查询失败") },
            )
            branchCandidateJob = null
        }
    }

    fun cancelTaskBranchCandidates() {
        branchCandidateRevision++
        branchCandidateJob?.cancel()
        branchCandidateJob = null
        branchCandidates = TaskBranchCandidatesState.Idle
    }

    fun inspectRepair(task: TaskManifest, workspace: ServiceWorkspace): Boolean = operations.run(
        "正在检查工作区修复方案…",
        "修复方案已生成",
        cancellable = true,
        block = { tasks.inspectWorkspaceRepair(session.config, taskDirectory(task), workspace.worktreePath) },
        onSuccess = { repairPreview = it },
    )

    fun repairWorkspace(
        task: TaskManifest,
        preview: WorkspaceRepairPreview,
        confirmation: WorkspaceRepairConfirmation,
    ): Boolean = operations.run(
        "正在修复工作区…",
        "工作区修复完成",
        cancellable = true,
        block = { tasks.repairWorkspace(session.config, taskDirectory(task), preview, confirmation) },
        onSuccess = { result ->
            repairPreview = null
            repairResult = result
            events.info(
                event = "workspace.repair",
                message = result.message,
                metadata = mapOf(
                    "service" to result.serviceName,
                    "workspace" to result.workspacePath,
                    "backup" to result.backupPath.orEmpty(),
                    "warnings" to result.warnings.joinToString(" | "),
                ),
            )
            reloadTasks(task.folderName)
        },
    )

    fun clearRepairPreview() { repairPreview = null }
    fun clearRepairResult() { repairResult = null }

    fun openWorkData(task: TaskManifest) = desktopActions.openWorkData(taskDirectory(task))

    fun defaultCommitMessage(task: TaskManifest, workspace: ServiceWorkspace): String {
        val template = session.config.group(task.groupId).services.firstOrNull { it.id == workspace.groupServiceId }
            ?.commitMessageTemplate.orEmpty()
        return CommitMessageTemplate.render(template, task.requirementLink)
    }

    fun commit(task: TaskManifest, workspace: ServiceWorkspace, message: String, pushAfter: Boolean = false, expectedFingerprint: String? = null): Boolean =
        operations.run(
            if (pushAfter) "正在提交并推送 ${workspace.operationLabel()}…" else "正在提交 ${workspace.operationLabel()}…",
            if (pushAfter) "提交并推送完成" else "提交完成",
            block = {
                if (pushAfter) gitOperations.commitAndPush(workspace, message, expectedFingerprint, session.config.blockedGitWriteBranches)
                else gitOperations.commit(workspace, message, expectedFingerprint, session.config.blockedGitWriteBranches)
            },
            onSuccess = { refreshGitStatus() },
        )

    fun push(task: TaskManifest, workspace: ServiceWorkspace): Boolean =
        operations.run("正在推送 ${workspace.operationLabel()}…", "推送完成", block = { gitOperations.push(workspace, session.config.blockedGitWriteBranches) }, onSuccess = { refreshGitStatus() })

    fun physicalWorkspaces(task: TaskManifest): List<ServiceWorkspace> =
        task.services.distinctBy(WorkspaceGitOperationService::workspacePathKey)

    fun workspaceKey(workspace: ServiceWorkspace): String = WorkspaceGitOperationService.workspacePathKey(workspace)

    private fun configuredWorkspaceCommands(task: TaskManifest, workspace: ServiceWorkspace): List<WorkspaceCommandConfig> =
        session.config.groups
            .firstOrNull { it.id == task.groupId }
            ?.services
            ?.firstOrNull { it.id == workspace.groupServiceId }
            ?.modules
            ?.firstOrNull { it.id == workspace.moduleId }
            ?.customCommands
            .orEmpty()

    fun workspaceCommands(task: TaskManifest, workspace: ServiceWorkspace): List<WorkspaceCommandConfig> {
        if (workspace.health !in setOf(WorkspaceHealth.READY, WorkspaceHealth.READY_WITH_WARNINGS)) return emptyList()
        return configuredWorkspaceCommands(task, workspace)
            .filter(WorkspaceCommandConfig::enabled)
    }

    val workspaceCommandRunning: Boolean
        get() = workspaceCommandActive

    fun runWorkspaceCommand(
        task: TaskManifest,
        workspace: ServiceWorkspace,
        command: WorkspaceCommandConfig,
    ): Boolean {
        if (workspaceCommandRunning || workspace.health !in setOf(WorkspaceHealth.READY, WorkspaceHealth.READY_WITH_WARNINGS)) return false
        val configured = configuredWorkspaceCommands(task, workspace)
            .firstOrNull { it.id == command.id }
            ?: return false
        if (!configured.enabled) return false
        val revision = ++workspaceCommandRevision
        val workspaceKey = workspaceKey(workspace)
        val commandWorkingDirectory = Path.of(workspace.worktreePath)
            .toAbsolutePath()
            .normalize()
            .resolve(configured.workingDirectory)
            .normalize()
            .toString()
        val startedAt = System.currentTimeMillis()
        val collectedLines = java.util.Collections.synchronizedList(mutableListOf<CommandOutputLine>())
        var collectedTruncated = false
        workspaceCommandState = WorkspaceCommandExecutionState(
            taskKey = task.taskDirectoryName,
            workspaceKey = workspaceKey,
            workspaceLabel = workspace.moduleName.ifBlank { workspace.serviceName },
            command = configured,
            workingDirectory = commandWorkingDirectory,
            status = WorkspaceCommandExecutionStatus.RUNNING,
            startedAtMillis = startedAt,
        )
        workspaceCommandActive = true
        workspaceCommandJob = scope.launch {
            try {
                val result = runInterruptible(ioDispatcher) {
                    workspaceCommands.execute(Path.of(workspace.worktreePath), configured) { line ->
                        synchronized(collectedLines) {
                            if (collectedLines.size < MAX_WORKSPACE_COMMAND_LOG_LINES) collectedLines += line
                            else {
                                collectedLines.removeAt(0)
                                collectedLines += line
                                collectedTruncated = true
                            }
                        }
                        scope.launch {
                            if (revision != workspaceCommandRevision) return@launch
                            val current = workspaceCommandState ?: return@launch
                            if (current.status != WorkspaceCommandExecutionStatus.RUNNING) return@launch
                            val snapshot = synchronized(collectedLines) { collectedLines.toList() }
                            workspaceCommandState = current.copy(lines = snapshot, truncated = collectedTruncated)
                        }
                    }
                }
                if (revision != workspaceCommandRevision) return@launch
                val snapshot = synchronized(collectedLines) { collectedLines.toList() }
                workspaceCommandState = workspaceCommandState?.copy(
                    lines = snapshot,
                    truncated = collectedTruncated,
                    status = if (result.succeeded) WorkspaceCommandExecutionStatus.SUCCEEDED else WorkspaceCommandExecutionStatus.FAILED,
                    exitCode = result.exitCode,
                    durationMillis = System.currentTimeMillis() - startedAt,
                    error = if (result.succeeded) {
                        null
                    } else {
                        result.stderr.lineSequence().firstOrNull { it.isNotBlank() }
                            ?: "命令退出码：${result.exitCode}"
                    },
                )
            } catch (cancelled: CancellationException) {
                if (revision == workspaceCommandRevision) {
                    val snapshot = synchronized(collectedLines) { collectedLines.toList() }
                    workspaceCommandState = workspaceCommandState?.copy(
                        lines = snapshot,
                        truncated = collectedTruncated,
                        status = WorkspaceCommandExecutionStatus.CANCELLED,
                        durationMillis = System.currentTimeMillis() - startedAt,
                    )
                }
            } catch (error: Throwable) {
                if (revision == workspaceCommandRevision) {
                    val snapshot = synchronized(collectedLines) { collectedLines.toList() }
                    workspaceCommandState = workspaceCommandState?.copy(
                        lines = snapshot,
                        truncated = collectedTruncated,
                        status = WorkspaceCommandExecutionStatus.FAILED,
                        durationMillis = System.currentTimeMillis() - startedAt,
                        error = error.message ?: error::class.simpleName,
                    )
                }
            } finally {
                workspaceCommandJob = null
                workspaceCommandActive = false
            }
        }
        return true
    }

    fun cancelWorkspaceCommand() {
        val current = workspaceCommandState
        if (current?.status != WorkspaceCommandExecutionStatus.RUNNING) return
        ++workspaceCommandRevision
        workspaceCommandJob?.cancel()
        workspaceCommandState = current.copy(
            status = WorkspaceCommandExecutionStatus.CANCELLED,
            durationMillis = System.currentTimeMillis() - current.startedAtMillis,
        )
    }

    fun dismissWorkspaceCommand() {
        if (workspaceCommandState?.status == WorkspaceCommandExecutionStatus.RUNNING) return
        workspaceCommandState = null
    }

    private fun cancelWorkspaceCommandForTaskSwitch() {
        ++workspaceCommandRevision
        workspaceCommandJob?.cancel()
        workspaceCommandJob = null
        workspaceCommandState = null
    }

    suspend fun previewWorkspaceFile(
        worktreePath: String,
        change: WorkspaceGitFileChange,
    ): WorkspaceGitFilePreview = withContext(ioDispatcher) {
        gitFilePreviews.preview(worktreePath, change)
    }

    suspend fun workspaceGitHistory(worktreePath: String): List<WorkspaceGitCommit> = runInterruptible(ioDispatcher) {
        gitHistory.read(Path.of(worktreePath).toAbsolutePath().normalize())
    }

    fun loadBatchGitPreviews(task: TaskManifest) {
        batchGitPreviews = BatchGitPreviewState.Loading
        val taskKey = task.taskDirectoryName
        scope.launch {
            val result = withContext(ioDispatcher) {
                runCatching { gitOperations.previews(physicalWorkspaces(task)) }
            }
            if (session.selectedTask?.taskDirectoryName != taskKey) return@launch
            batchGitPreviews = result.fold(
                onSuccess = { BatchGitPreviewState.Loaded(it) },
                onFailure = { BatchGitPreviewState.Failed(it.message ?: "Git 变更预览失败") },
            )
        }
    }

    fun batchGit(
        task: TaskManifest,
        mode: WorkspaceGitBatchMode,
        selectedWorkspaceKeys: Set<String>,
        commitMessages: Map<String, String> = emptyMap(),
        expectedFingerprints: Map<String, String> = emptyMap(),
        onCompleted: (WorkspaceGitBatchResult) -> Unit,
    ): Boolean = operations.run(
        activeMessage = when (mode) {
            WorkspaceGitBatchMode.COMMIT -> "正在提交全部工作区…"
            WorkspaceGitBatchMode.PUSH -> "正在推送全部工作区…"
            WorkspaceGitBatchMode.COMMIT_AND_PUSH -> "正在提交并推送全部工作区…"
        },
        successMessage = "批量 Git 操作已完成",
        block = {
            val current = physicalWorkspaces(task)
            val selected = selectPhysicalWorkspaces(current, selectedWorkspaceKeys)
            gitOperations.batch(
                selected,
                mode,
                commitMessages.filterKeys(selectedWorkspaceKeys::contains),
                expectedFingerprints.filterKeys(selectedWorkspaceKeys::contains),
                session.config.blockedGitWriteBranches,
            )
        },
        onSuccess = { result -> refreshGitStatus(); onCompleted(result) },
    )

    private fun reloadTasks(preferredFolder: String? = session.selectedTask?.folderName) {
        val loaded = scanTasks(session.config)
        session.replaceTasks(loaded.manifests, preferredFolder)
        onTasksChanged()
        refreshGitStatus()
        loaded.warning?.let { onError(IllegalStateException(it)) }
    }

    private fun ServiceWorkspace.operationLabel(): String = moduleName.ifBlank { serviceName }

    private fun scanTasks(config: AppConfig): LoadedTaskSnapshot {
        val root = config.taskRoot?.let(Path::of) ?: return LoadedTaskSnapshot(emptyList())
        val scan = runCatching { manifests.scan(root) }.getOrElse { error ->
            return LoadedTaskSnapshot(emptyList(), "任务目录扫描失败：${error.message ?: error::class.simpleName}")
        }
        val messages = buildList {
            if (scan.unsupportedDirectories.isNotEmpty()) {
                add("已忽略 ${scan.unsupportedDirectories.size} 个与当前 AWM $CURRENT_PRODUCT_VERSION 不兼容的任务目录")
            }
            if (scan.failures.isNotEmpty()) {
                add("${scan.failures.size} 个任务清单读取失败：" + scan.failures.entries.joinToString { (path, reason) -> "${path.fileName}：$reason" })
            }
        }
        return LoadedTaskSnapshot(
            scan.current.map { it.second }.sortedByDescending(TaskManifest::updatedAt),
            messages.takeIf(List<String>::isNotEmpty)?.joinToString("；"),
        )
    }

    private fun taskDirectory(task: TaskManifest): Path = taskDirectory(session.config, task)

    private fun taskDirectory(config: AppConfig, task: TaskManifest): Path =
        Path.of(requireNotNull(config.taskRoot) { "尚未配置任务根目录" }).resolve(task.taskDirectoryName)

    private fun normalizedWorkspacePath(workspace: ServiceWorkspace): String =
        Path.of(workspace.worktreePath).toAbsolutePath().normalize().toString()

    private fun samePathIdentity(expected: String, actual: String): Boolean {
        val left = Path.of(expected).toAbsolutePath().normalize().toString()
        val right = Path.of(actual).toAbsolutePath().normalize().toString()
        return if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) left.equals(right, true) else left == right
    }
}

internal fun selectPhysicalWorkspaces(
    current: List<ServiceWorkspace>,
    selectedWorkspaceKeys: Set<String>,
): List<ServiceWorkspace> {
    require(selectedWorkspaceKeys.isNotEmpty()) { "请至少选择一个工作区" }
    val unique = current.distinctBy(WorkspaceGitOperationService::workspacePathKey)
    val byKey = unique.associateBy(WorkspaceGitOperationService::workspacePathKey)
    val unknown = selectedWorkspaceKeys - byKey.keys
    require(unknown.isEmpty()) { "所选工作区已发生变化，请重新打开批量 Git 弹窗：${unknown.joinToString()}" }
    return unique.filter { WorkspaceGitOperationService.workspacePathKey(it) in selectedWorkspaceKeys }
}

private data class LoadedTaskSnapshot(
    val manifests: List<TaskManifest>,
    val warning: String? = null,
)

/** A dismissed or replaced create request must never advance from preflight to creation. */
internal class CreateTaskBranchReuseInspection(
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val onError: (Throwable) -> Unit,
) {
    private var generation = 0L
    private var job: Job? = null

    fun start(
        inspect: suspend () -> List<BranchReuseConflict>,
        onResolved: (List<BranchReuseConflict>) -> Unit,
        onFinished: () -> Unit,
    ) {
        cancel()
        val requestGeneration = generation
        job = scope.launch {
            val result = try {
                Result.success(withContext(ioDispatcher) { inspect() })
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                Result.failure(error)
            }
            if (requestGeneration != generation) return@launch
            job = null
            result.onSuccess(onResolved).onFailure(onError)
            onFinished()
        }
    }

    fun cancel() {
        generation++
        job?.cancel()
        job = null
    }
}
