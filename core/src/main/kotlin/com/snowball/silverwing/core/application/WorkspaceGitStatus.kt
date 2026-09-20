package com.snowball.silverwing.core

import java.nio.file.Path
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.supervisorScope

fun interface WorkspaceGitStatusReader {
    fun read(workspace: ServiceWorkspace): WorkspaceGitHealth
}

/** Deduplicates shared physical worktrees before invoking Git. */
class WorkspaceGitStatusService(
    private val reader: WorkspaceGitStatusReader,
    parallelism: Int = 4,
) {
    private val dispatcher = Dispatchers.IO.limitedParallelism(parallelism.also {
        require(it > 0) { "Git status parallelism must be greater than zero" }
    })

    suspend fun inspect(
        workspaces: List<ServiceWorkspace>,
        onResult: suspend (Path, WorkspaceGitHealth) -> Unit = { _, _ -> },
    ): Map<Path, WorkspaceGitHealth> = supervisorScope {
        workspaces
            .groupBy { Path.of(it.worktreePath).toAbsolutePath().normalize() }
            .map { (path, shared) ->
                async {
                    val representative = shared.first()
                    val definitions = shared.map { listOf(it.repositoryId, it.repositoryPath, it.branch, it.strategy.name) }.distinct()
                    val health = if (definitions.size != 1) {
                        WorkspaceGitHealth(
                            state = WorkspaceGitHealthState.FAILED,
                            issue = WorkspaceGitIssue.IDENTITY_MISMATCH,
                            expectedBranch = representative.branch,
                            message = "同一物理工作区在任务清单中存在互相冲突的仓库或分支定义",
                        )
                    } else {
                        try {
                            runInterruptible(dispatcher) { reader.read(representative) }
                        } catch (error: CancellationException) {
                            throw error
                        } catch (error: Throwable) {
                            WorkspaceGitHealth(
                                state = WorkspaceGitHealthState.FAILED,
                                issue = WorkspaceGitIssue.INSPECTION_FAILED,
                                expectedBranch = representative.branch,
                                message = error.message ?: "Git 状态检查失败",
                            )
                        }
                    }
                    onResult(path, health)
                    path to health
                }
            }
            .awaitAll()
            .toMap()
    }
}
