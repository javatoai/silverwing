package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.DeliveryTarget
import com.snowball.silverwing.core.GenbuStageStatus
import com.snowball.silverwing.core.GenbuTagProbeService
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TagOperation
import com.snowball.silverwing.core.TagHistoryItem
import com.snowball.silverwing.core.TagWorkspaceCheck
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.GitTagDeliveryAdapter
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.selectionKey
import java.nio.file.Path
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

private val retryableInterruptedTagStates = setOf(
    com.snowball.silverwing.core.TagOperationState.CREATED,
    com.snowball.silverwing.core.TagOperationState.PREFLIGHT_PASSED,
    com.snowball.silverwing.core.TagOperationState.SOURCE_BRANCH_PUSHED,
)

data class DeliveryUiState(
    val history: List<TagOperation>,
    val historyItems: List<TagHistoryItem>,
)

/** Identifies the one workspace whose persisted Tag stage belongs to the active desktop operation. */
private data class ActiveTagTarget(
    val folderName: String,
    val groupServiceId: String,
    val moduleId: String,
)

/** Tag delivery use cases isolated from task lifecycle and desktop platform actions. */
class DeliveryController internal constructor(
    private val session: AppSessionStore,
    private val adapter: GitTagDeliveryAdapter,
    private val operations: OperationRunner,
    private val taskDirectory: (TaskManifest) -> Path,
    private val refreshGitStatus: () -> Unit,
    private val genbuTagProbes: GenbuTagProbeService,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val onError: (Throwable) -> Unit = {},
) {
    private var historyItems by mutableStateOf(loadHistory())
    private var history by mutableStateOf(historyItems.flatMap(::operationsIn))
    private var genbuProbeJob: Job? = null
    private var genbuRefreshJob: Job? = null
    private var genbuProbeRefreshing by mutableStateOf(false)
    private var workspaceChecks by mutableStateOf<Map<String, TagWorkspaceCheck>>(emptyMap())
    private var activeTagTargets by mutableStateOf<Set<ActiveTagTarget>>(emptySet())
    private val genbuProbeMutex = Mutex()

    val state: DeliveryUiState get() = DeliveryUiState(history, historyItems)
    val isGenbuProbeRefreshing: Boolean get() = genbuProbeRefreshing
    fun workspaceCheck(operationId: String): TagWorkspaceCheck? = workspaceChecks[operationId]
    fun isTagBuildActive(operation: TagOperation): Boolean = operation.activeTarget() in activeTagTargets

    fun canBuild(task: TaskManifest, workspace: ServiceWorkspace): Boolean {
        if (task.lifecycleStatus != com.snowball.silverwing.core.TaskLifecycleStatus.ACTIVE) return false
        val group = session.config.groups.firstOrNull { it.id == task.groupId } ?: return false
        if (!session.config.tagEnabled || !group.tagEnabled || workspace.health !in setOf(WorkspaceHealth.READY, WorkspaceHealth.READY_WITH_WARNINGS)) return false
        if (!workspace.tagEnabled) return false
        // The read-only Tag preflight determines whether a branch write is needed.
        // Core policy enforcement runs after that preflight and before every write.
        return true
    }

    fun build(task: TaskManifest, workspace: ServiceWorkspace): Boolean = runTagOperation(
        targets = setOf(task.activeTarget(workspace)),
        activeMessage = "正在构建 ${workspace.moduleName.ifBlank { workspace.serviceName }} 测试Tag…",
        successMessage = "测试Tag操作已完成",
        block = { adapter.executeTag(DeliveryTarget(session.config, taskDirectory(task), workspace.selectionKey)) },
        onSuccess = { reloadHistory(); refreshGitStatus() },
    )

    fun buildBatch(task: TaskManifest, workspaces: List<ServiceWorkspace>): Boolean = runTagOperation(
        targets = workspaces.map { task.activeTarget(it) }.toSet(),
        activeMessage = "正在批量构建测试Tag…",
        successMessage = "批量测试Tag操作已完成",
        block = { adapter.executeBatch(session.config, taskDirectory(task), workspaces.map(ServiceWorkspace::selectionKey)) },
        onSuccess = { reloadHistory(); refreshGitStatus() },
    )

    /**
     * Retries a conflict using the original operation id so the core service
     * updates the existing record instead of creating a second history row.
     * The workspace is resolved by the caller; the core adapter performs the
     * authoritative state and branch checks before any Git write.
     */
    fun retryConflict(task: TaskManifest, operation: TagOperation): Boolean {
        require(operation.state == com.snowball.silverwing.core.TagOperationState.CONFLICT) { "只有冲突测试Tag可以重试" }
        return runTagOperation(
            targets = setOf(task.activeTarget(operation)),
            activeMessage = "正在重试 ${operation.serviceName} 测试Tag…",
            successMessage = "测试Tag重试已完成",
            block = {
                adapter.resumeConflict(
                    DeliveryTarget(session.config, taskDirectory(task), "${operation.groupServiceId}:${operation.moduleId}"),
                    operation.operationId,
                )
            },
            onSuccess = { reloadHistory(); refreshGitStatus() },
        )
    }

    /** Checks only the current feature worktree before a conflict retry. */
    fun inspectConflictWorkspace(task: TaskManifest, operation: TagOperation): Boolean {
        require(tagOperationCanInspectWorkspace(operation)) { "当前测试Tag无需检测工作区" }
        return operations.run(
            "正在检测 ${operation.serviceName} 工作区…",
            "工作区检测完成",
            cancellable = true,
            block = {
                adapter.inspectWorkspace(
                    DeliveryTarget(session.config, taskDirectory(task), "${operation.groupServiceId}:${operation.moduleId}"),
                )
            },
            onSuccess = { check -> workspaceChecks = workspaceChecks + (operation.operationId to check) },
        )
    }

    /** Re-runs a safely interrupted operation while retaining its history row. */
    fun retryInterrupted(task: TaskManifest, operation: TagOperation): Boolean {
        require(operation.state in retryableInterruptedTagStates) { "只有构建中断的测试Tag可以重试" }
        return runTagOperation(
            targets = setOf(task.activeTarget(operation)),
            activeMessage = "正在重新构建 ${operation.serviceName} 测试Tag…",
            successMessage = "测试Tag重试已完成",
            block = {
                adapter.resumeInterrupted(
                    DeliveryTarget(session.config, taskDirectory(task), "${operation.groupServiceId}:${operation.moduleId}"),
                    operation.operationId,
                )
            },
            onSuccess = { reloadHistory(); refreshGitStatus() },
        )
    }

    /** Retries a locally failed build on the same history record. */
    fun retryFailed(task: TaskManifest, operation: TagOperation): Boolean {
        require(operation.state == com.snowball.silverwing.core.TagOperationState.FAILED) { "只有失败的测试Tag可以重试" }
        return runTagOperation(
            targets = setOf(task.activeTarget(operation)),
            activeMessage = "正在重试 ${operation.serviceName} 测试Tag…",
            successMessage = "测试Tag重试已完成",
            block = {
                adapter.resumeFailed(
                    DeliveryTarget(session.config, taskDirectory(task), "${operation.groupServiceId}:${operation.moduleId}"),
                    operation.operationId,
                )
            },
            onSuccess = { reloadHistory(); refreshGitStatus() },
        )
    }

    /** Pushes the already-created local Tag of a partially completed build. */
    fun resumePartial(task: TaskManifest, operation: TagOperation): Boolean {
        require(operation.state == com.snowball.silverwing.core.TagOperationState.PARTIAL) { "只有部分完成的测试Tag可以继续构建" }
        return runTagOperation(
            targets = setOf(task.activeTarget(operation)),
            activeMessage = "正在继续构建 ${operation.serviceName} 测试Tag…",
            successMessage = "测试Tag继续构建已完成",
            block = {
                adapter.resumePartial(
                    DeliveryTarget(session.config, taskDirectory(task), "${operation.groupServiceId}:${operation.moduleId}"),
                    operation.operationId,
                )
            },
            onSuccess = { reloadHistory(); refreshGitStatus() },
        )
    }

    /** Rebuilds a Genbu-failed Tag with the next version on the same history record. */
    fun retag(task: TaskManifest, operation: TagOperation): Boolean {
        require(
            com.snowball.silverwing.core.tagRetryKind(operation) == com.snowball.silverwing.core.TagRetryKind.RETAG,
        ) { "只有已确认 Genbu 构建失败且状态查询正常的测试Tag可以重新打Tag" }
        return runTagOperation(
            targets = setOf(task.activeTarget(operation)),
            activeMessage = "正在重新打 ${operation.serviceName} 测试Tag…",
            successMessage = "重新打Tag已完成",
            block = {
                adapter.retag(
                    DeliveryTarget(session.config, taskDirectory(task), "${operation.groupServiceId}:${operation.moduleId}"),
                    operation.operationId,
                )
            },
            onSuccess = { reloadHistory(); refreshGitStatus() },
        )
    }

    fun reloadHistory() {
        historyItems = loadHistory()
        history = historyItems.flatMap(::operationsIn)
    }

    private fun loadHistory(): List<TagHistoryItem> {
        runCatching { adapter.enforceHistoryRetention(session.config, session.tasks) }
            .onFailure(onError)
        return adapter.historyItems(session.config, session.tasks)
    }

    /**
     * The core state machine persists intermediate stages so a real app crash can be resumed.
     * While this process is still running, keep the exact workspace marked active so the UI does
     * not present that durable checkpoint as an interruption.
     */
    private fun <T> runTagOperation(
        targets: Set<ActiveTagTarget>,
        activeMessage: String,
        successMessage: String,
        block: () -> T,
        onSuccess: (T) -> Unit,
    ): Boolean {
        // A rejected duplicate does not own the marker of the already-running build.
        val newlyActiveTargets = targets - activeTagTargets
        activeTagTargets = activeTagTargets + newlyActiveTargets
        val finished = { activeTagTargets = activeTagTargets - newlyActiveTargets }
        val started = operations.run(
            activeMessage = activeMessage,
            successMessage = successMessage,
            block = block,
            onFailure = { finished() },
            onCancelled = finished,
            onSuccess = { value ->
                try {
                    onSuccess(value)
                } finally {
                    finished()
                }
            },
        )
        if (!started) finished()
        return started
    }

    fun clearHistory(): Boolean = operations.run(
        "正在清除Tag构建历史…",
        "Tag构建历史已清除",
        block = { adapter.clearHistory(session.config, session.tasks) },
        onSuccess = {
            reloadHistory()
        },
    )

    /** Deletes only explicit local history records while keeping Genbu probe writes serialized. */
    fun deleteHistory(operationIds: Set<String>): Boolean {
        if (operationIds.isEmpty()) return false
        return operations.run(
            "正在删除Tag构建记录…",
            "已删除 ${operationIds.size} 条Tag构建记录",
            block = {
                runBlocking {
                    genbuProbeMutex.withLock {
                        adapter.deleteHistory(session.config, session.tasks, operationIds)
                    }
                }
            },
            onSuccess = {
                reloadHistory()
            },
        )
    }

    /** Poll only while the Tag page is visible; completed or superseded records are skipped in core policy. */
    fun setGenbuTagProbeVisible(visible: Boolean) {
        if (!visible) {
            genbuProbeJob?.cancel()
            genbuProbeJob = null
            return
        }
        if (!scope.isActive) return
        if (genbuProbeJob?.isActive == true) return
        val job = scope.launch(start = CoroutineStart.LAZY) {
            while (isActive) {
                runGenbuProbe(force = false)
                delay(GENBU_PROBE_INTERVAL_MILLIS)
            }
        }
        genbuProbeJob = job
        job.invokeOnCompletion { if (genbuProbeJob === job) genbuProbeJob = null }
        job.start()
    }

    fun refreshGenbuTagProbes(): Boolean {
        if (!scope.isActive || genbuProbeRefreshing) return false
        genbuProbeRefreshing = true
        val job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                runGenbuProbe(force = true)
            } finally {
                if (genbuRefreshJob === coroutineContext[Job]) {
                    genbuRefreshJob = null
                    genbuProbeRefreshing = false
                }
            }
        }
        genbuRefreshJob = job
        job.invokeOnCompletion {
            if (genbuRefreshJob === job) {
                genbuRefreshJob = null
                genbuProbeRefreshing = false
            }
        }
        job.start()
        return true
    }

    private suspend fun runGenbuProbe(force: Boolean) {
        val changed = genbuProbeMutex.withLock {
            val config = session.config
            val tasks = session.tasks
            try {
                runInterruptible(ioDispatcher) { genbuTagProbes.probe(config, tasks, force) }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (interrupted: InterruptedException) {
                throw CancellationException("Genbu 探测已取消", interrupted)
            } catch (error: Throwable) {
                runCatching { onError(error) }
                false
            }
        }
        if (changed) reloadHistory()
    }

    companion object {
        private const val GENBU_PROBE_INTERVAL_MILLIS = 30_000L

        private fun operationsIn(item: TagHistoryItem): List<TagOperation> = item.operations

        private fun TaskManifest.activeTarget(workspace: ServiceWorkspace) = ActiveTagTarget(
            folderName = folderName,
            groupServiceId = workspace.groupServiceId,
            moduleId = workspace.moduleId,
        )

        private fun TaskManifest.activeTarget(operation: TagOperation) = ActiveTagTarget(
            folderName = folderName,
            groupServiceId = operation.groupServiceId,
            moduleId = operation.moduleId,
        )

        private fun TagOperation.activeTarget() = ActiveTagTarget(
            folderName = folderName,
            groupServiceId = groupServiceId,
            moduleId = moduleId,
        )
    }
}
