package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.*
import java.nio.file.Path
import kotlinx.coroutines.CancellationException

internal data class WorkspaceMainBranchMergeUiState(
    val taskDirectory: String,
    val taskCreatedAt: String,
    val workspace: ServiceWorkspace,
    val running: Boolean,
    val result: WorkspaceMainBranchMergeResult? = null,
)

/** Uses the shared mutation runner but keeps outcomes on their originating task card. */
internal class WorkspaceMainBranchMergeController(
    private val config: () -> AppConfig,
    private val selectedTask: () -> TaskManifest?,
    private val taskDirectory: (TaskManifest) -> Path,
    private val merger: WorkspaceMainBranchMerger,
    private val operations: OperationRunner,
    private val isBusy: () -> Boolean,
    private val refreshGitStatus: () -> Unit,
) {
    private var selectionRevision = 0L
    private var state by mutableStateOf<WorkspaceMainBranchMergeUiState?>(null)

    fun selectionChanged() { selectionRevision++; state = null }

    fun stateFor(task: TaskManifest, workspace: ServiceWorkspace): WorkspaceMainBranchMergeUiState? = state?.takeIf {
        it.taskDirectory == taskDirectory(task).toString() && it.taskCreatedAt == task.createdAt && it.workspace == workspace
    }

    fun dismiss(task: TaskManifest, workspace: ServiceWorkspace) {
        if (stateFor(task, workspace)?.running == false) state = null
    }

    fun merge(task: TaskManifest, workspace: ServiceWorkspace): Boolean {
        if (isBusy() || state?.running == true) return false
        val selected = selectedTask() ?: return false
        if (selected.taskDirectoryName != task.taskDirectoryName || selected.createdAt != task.createdAt ||
            selected.services.none { it == workspace }) return false
        val snapshot = config()
        val directory = taskDirectory(task)
        val revision = selectionRevision
        val request = WorkspaceMainBranchMergeUiState(directory.toString(), task.createdAt, workspace, running = true)
        fun current(): Boolean = selectionRevision == revision &&
            selectedTask()?.let { taskDirectory(it) == directory && it.createdAt == task.createdAt && workspace in it.services } == true
        state = request
        val started = operations.run(
            activeMessage = "正在合并远程主分支 · ${workspace.moduleName.ifBlank { workspace.serviceName }}…",
            successMessage = "主分支合并操作已结束",
            block = {
                try {
                    merger.merge(snapshot, directory, task, workspace)
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (error: Exception) {
                    WorkspaceMainBranchMergeResult(
                        workspacePath = workspace.worktreePath,
                        serviceName = workspace.serviceName,
                        moduleName = workspace.moduleName,
                        sourceRef = runCatching { WorkspaceMainBranchResolver.resolve(snapshot, task, workspace)?.displayRef }.getOrNull() ?: "远程主分支",
                        targetBranch = workspace.branch,
                        outcome = WorkspaceMainBranchMergeOutcome.FAILED,
                        message = OperationFailureDetails.format(error),
                    )
                }
            },
            onSuccess = { result ->
                if (current()) state = request.copy(running = false, result = result)
                refreshGitStatus()
            },
            onFailure = { if (current()) state = null; refreshGitStatus() },
            onCancelled = { if (current()) state = null; refreshGitStatus() },
        )
        if (!started && current()) state = null
        return started
    }
}
