package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Clock
import java.time.Instant

/** Stable, tool-neutral metadata consumed by Desktop to render selections. */
data class TaskWorkspaceToolDescriptor(
    val id: String,
    val displayName: String,
    val description: String = "",
) {
    init {
        require(id.isNotBlank()) { "工作区工具 ID 不能为空" }
        require(displayName.isNotBlank()) { "工作区工具名称不能为空" }
    }
}

/** A launcher may remain registered while being unavailable on the current machine. */
sealed interface TaskWorkspaceToolAvailability {
    data object Available : TaskWorkspaceToolAvailability
    data class Unavailable(val reason: String) : TaskWorkspaceToolAvailability
}

/**
 * Minimal completed-task data exposed to external tools. It deliberately omits
 * configuration stores, Git clients and mutation services.
 */
data class TaskWorkspaceContext(
    val taskName: String,
    val taskDirectory: Path,
    val agentsFile: Path,
    val workspaces: List<ServiceWorkspace>,
)

/**
 * Adapter boundary for opening a completed task in an external development
 * tool. Implementations own all tool-specific URI, CLI and process behavior.
 */
interface TaskWorkspaceToolLauncher {
    val descriptor: TaskWorkspaceToolDescriptor
    fun availability(): TaskWorkspaceToolAvailability
    fun open(context: TaskWorkspaceContext)
}

/**
 * Immutable registry injected into application and desktop orchestration.
 * Adding a tool only adds an adapter registration; task creation and JSON stay unchanged.
 */
class TaskWorkspaceToolRegistry(
    launchers: List<TaskWorkspaceToolLauncher>,
) {
    private val launchersById = launchers.associateBy { it.descriptor.id }

    init {
        require(launchersById.size == launchers.size) { "工作区工具 ID 不能重复" }
    }

    fun descriptors(): List<TaskWorkspaceToolDescriptor> = launchersById.values.map { it.descriptor }

    fun launcher(toolId: String): TaskWorkspaceToolLauncher? = launchersById[toolId]

    fun availability(toolId: String): TaskWorkspaceToolAvailability = launchersById[toolId]
        ?.availability()
        ?: TaskWorkspaceToolAvailability.Unavailable("当前版本未注册工具：$toolId")
}

/**
 * Launches selected tools independently and persists each result. A tool
 * failure must never roll back the already-created Git workspace.
 */
class WorkspaceToolLaunchService(
    private val registry: TaskWorkspaceToolRegistry,
    private val manifests: TaskManifestRepository = ManifestStore(),
    private val clock: Clock = Clock.systemUTC(),
) {
    fun launch(
        taskDirectory: Path,
        manifest: TaskManifest,
        toolIds: List<String>,
    ): TaskManifest {
        val selected = toolIds.filter(String::isNotBlank).distinct()
        if (selected.isEmpty()) return manifest
        val pendingAt = now()
        var current = manifest.copy(
            workspaceToolLaunches = selected.map { WorkspaceToolLaunch(it, WorkspaceToolLaunchStatus.PENDING, pendingAt) },
            updatedAt = pendingAt,
        ).also { manifests.save(taskDirectory, it) }
        selected.forEach { toolId -> current = launchOne(taskDirectory, current, toolId) }
        return current
    }

    fun retry(taskDirectory: Path, manifest: TaskManifest, toolId: String): TaskManifest {
        require(toolId.isNotBlank()) { "工作区工具 ID 不能为空" }
        val pendingAt = now()
        val pending = updateResult(
            manifest,
            WorkspaceToolLaunch(toolId, WorkspaceToolLaunchStatus.PENDING, pendingAt),
        ).copy(updatedAt = pendingAt).also { manifests.save(taskDirectory, it) }
        return launchOne(taskDirectory, pending, toolId)
    }

    private fun launchOne(taskDirectory: Path, manifest: TaskManifest, toolId: String): TaskManifest {
        val launcher = registry.launcher(toolId)
        val result = when {
            launcher == null -> WorkspaceToolLaunch(
                toolId,
                WorkspaceToolLaunchStatus.FAILED,
                now(),
                "当前版本未注册工具：$toolId",
            )
            else -> when (val availability = launcher.availability()) {
                is TaskWorkspaceToolAvailability.Unavailable -> WorkspaceToolLaunch(
                    toolId,
                    WorkspaceToolLaunchStatus.FAILED,
                    now(),
                    availability.reason,
                )
                TaskWorkspaceToolAvailability.Available -> runCatching {
                    launcher.open(
                        TaskWorkspaceContext(
                            taskName = manifest.folderName,
                            taskDirectory = taskDirectory.toAbsolutePath().normalize(),
                            agentsFile = taskDirectory.resolve(AgentsMdWriter.FILE_NAME).toAbsolutePath().normalize(),
                            workspaces = manifest.services,
                        ),
                    )
                }.fold(
                    onSuccess = { WorkspaceToolLaunch(toolId, WorkspaceToolLaunchStatus.OPENED, now()) },
                    onFailure = { error ->
                        WorkspaceToolLaunch(
                            toolId,
                            WorkspaceToolLaunchStatus.FAILED,
                            now(),
                            error.message ?: error::class.simpleName ?: "打开失败",
                        )
                    },
                )
            }
        }
        val updated = updateResult(manifest, result).copy(updatedAt = result.updatedAt)
        manifests.save(taskDirectory, updated)
        return updated
    }

    private fun updateResult(manifest: TaskManifest, result: WorkspaceToolLaunch): TaskManifest {
        val existing = manifest.workspaceToolLaunches.indexOfFirst { it.toolId == result.toolId }
        val launches = manifest.workspaceToolLaunches.toMutableList()
        if (existing >= 0) launches[existing] = result else launches += result
        return manifest.copy(workspaceToolLaunches = launches)
    }

    private fun now(): String = SilverWingTime.format(Instant.now(clock))
}
