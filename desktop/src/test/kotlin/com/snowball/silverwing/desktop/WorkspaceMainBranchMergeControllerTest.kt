@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Path
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import kotlin.test.*

class WorkspaceMainBranchMergeControllerTest {
    private val workspace = ServiceWorkspace("repo", "payments", "repository", "tasks/task/backend", DevelopmentToolType.INTELLIJ_IDEA,
        "feature/task", health = WorkspaceHealth.READY, baseRef = "github/master")
    private val task = TaskManifest(folderName = "task", taskDirectoryName = "task", featureBranch = "feature/task", createdAt = "one", updatedAt = "one", services = listOf(workspace))
    private val result = WorkspaceMainBranchMergeResult(workspace.worktreePath, "payments", "backend", "github/master", "feature/task", WorkspaceMainBranchMergeOutcome.UP_TO_DATE, "已是最新")

    @Test fun `duplicate trigger is rejected synchronously and latest result refreshes status once`() = runTest {
        val coordinator = OperationCoordinator()
        val runner = OperationRunner(coordinator, this, StandardTestDispatcher(testScheduler))
        var calls = 0
        var refreshes = 0
        val controller = WorkspaceMainBranchMergeController({ AppConfig() }, { task }, { Path.of(it.taskDirectoryName) },
            WorkspaceMainBranchMerger { _, _, _, _ -> calls++; result }, runner, { coordinator.busy }, { refreshes++ })
        assertTrue(controller.merge(task, workspace))
        assertTrue(controller.stateFor(task, workspace)!!.running)
        assertFalse(controller.merge(task, workspace))
        testScheduler.advanceUntilIdle()
        assertEquals(1, calls)
        assertEquals(1, refreshes)
        assertEquals(result, controller.stateFor(task, workspace)?.result)
        assertFalse(coordinator.busy)
    }

    @Test fun `switching away and back cannot attach old completion to a new selection`() = runTest {
        val coordinator = OperationCoordinator()
        var selected = task
        var refreshes = 0
        val controller = WorkspaceMainBranchMergeController({ AppConfig() }, { selected }, { Path.of(it.taskDirectoryName) },
            WorkspaceMainBranchMerger { _, _, _, _ -> result }, OperationRunner(coordinator, this, StandardTestDispatcher(testScheduler)), { coordinator.busy }, { refreshes++ })
        assertTrue(controller.merge(task, workspace))
        controller.selectionChanged()
        selected = task.copy(taskDirectoryName = "other")
        controller.selectionChanged()
        selected = task
        testScheduler.advanceUntilIdle()
        assertNull(controller.stateFor(task, workspace))
        assertEquals(1, refreshes)
        assertFalse(coordinator.busy)
    }

    @Test fun `failures are shown once on the source card and still refresh git status`() = runTest {
        val coordinator = OperationCoordinator()
        var refreshes = 0
        val controller = WorkspaceMainBranchMergeController({ AppConfig() }, { task }, { Path.of(it.taskDirectoryName) },
            WorkspaceMainBranchMerger { _, _, _, _ -> error("fetch unavailable") }, OperationRunner(coordinator, this, StandardTestDispatcher(testScheduler)), { coordinator.busy }, { refreshes++ })
        assertTrue(controller.merge(task, workspace))
        testScheduler.advanceUntilIdle()
        assertEquals(WorkspaceMainBranchMergeOutcome.FAILED, controller.stateFor(task, workspace)?.result?.outcome)
        assertEquals("fetch unavailable", controller.stateFor(task, workspace)?.result?.message)
        assertNull(coordinator.errorMessage)
        assertEquals(1, refreshes)
        controller.dismiss(task, workspace)
        assertNull(controller.stateFor(task, workspace))
    }

    @Test fun `unselected and removed workspace requests never start a mutation`() = runTest {
        val coordinator = OperationCoordinator()
        val controller = WorkspaceMainBranchMergeController({ AppConfig() }, { task.copy(services = emptyList()) }, { Path.of(it.taskDirectoryName) },
            WorkspaceMainBranchMerger { _, _, _, _ -> error("must not run") }, OperationRunner(coordinator, this, StandardTestDispatcher(testScheduler)), { coordinator.busy }, {})
        assertFalse(controller.merge(task, workspace))
        assertFalse(coordinator.busy)
    }
}
