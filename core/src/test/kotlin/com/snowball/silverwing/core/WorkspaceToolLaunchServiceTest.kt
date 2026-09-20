package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.assertEquals

class WorkspaceToolLaunchServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `registered tools launch independently and unknown tools are retained as failures`() {
        val opened = mutableListOf<String>()
        val registry = TaskWorkspaceToolRegistry(
            listOf(
                fakeLauncher("codex") { opened += it.taskName },
                fakeLauncher("cursor") { error("not installed") },
            ),
        )
        val manifests = ManifestStore()
        val taskDirectory = temporary.resolve("task")
        val task = manifest()
        manifests.save(taskDirectory, task)
        val service = WorkspaceToolLaunchService(
            registry = registry,
            manifests = manifests,
            clock = Clock.fixed(Instant.parse("2026-01-01T16:00:00Z"), ZoneOffset.UTC),
        )

        val result = service.launch(taskDirectory, task, listOf("codex", "cursor", "claude"))

        assertEquals(listOf("研发任务"), opened)
        assertEquals(
            listOf(
                "codex" to WorkspaceToolLaunchStatus.OPENED,
                "cursor" to WorkspaceToolLaunchStatus.FAILED,
                "claude" to WorkspaceToolLaunchStatus.FAILED,
            ),
            result.workspaceToolLaunches.map { it.toolId to it.status },
        )
        assertEquals("2026-01-02 00:00:00", result.workspaceToolLaunches.first().updatedAt)
        assertEquals(result, manifests.load(taskDirectory))
    }

    @Test
    fun `retry updates only the requested tool result`() {
        var attempts = 0
        val registry = TaskWorkspaceToolRegistry(listOf(fakeLauncher("codex") { attempts++ }))
        val manifests = ManifestStore()
        val taskDirectory = temporary.resolve("retry")
        val task = manifest().copy(
            workspaceToolLaunches = listOf(
                WorkspaceToolLaunch("codex", WorkspaceToolLaunchStatus.FAILED, "2026-01-01 00:00:00", "old"),
                WorkspaceToolLaunch("cursor", WorkspaceToolLaunchStatus.OPENED, "2026-01-01 00:00:00"),
            ),
        )
        manifests.save(taskDirectory, task)

        val result = WorkspaceToolLaunchService(registry, manifests).retry(taskDirectory, task, "codex")

        assertEquals(1, attempts)
        assertEquals(WorkspaceToolLaunchStatus.OPENED, result.workspaceToolLaunches.first().status)
        assertEquals(WorkspaceToolLaunchStatus.OPENED, result.workspaceToolLaunches.last().status)
    }

    private fun fakeLauncher(id: String, open: (TaskWorkspaceContext) -> Unit) = object : TaskWorkspaceToolLauncher {
        override val descriptor = TaskWorkspaceToolDescriptor(id, id.replaceFirstChar(Char::uppercase))
        override fun availability(): TaskWorkspaceToolAvailability = TaskWorkspaceToolAvailability.Available
        override fun open(context: TaskWorkspaceContext) = open.invoke(context)
    }

    private fun manifest() = TaskManifest(
        folderName = "研发任务",
        taskDirectoryName = "task",
        featureBranch = "feature/task",
        createdAt = "2026-01-01 00:00:00",
        updatedAt = "2026-01-01 00:00:00",
        lifecycleStatus = TaskLifecycleStatus.ACTIVE,
        services = emptyList(),
    )
}
