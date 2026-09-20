package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals

class WorkspaceHealthAggregationTest {
    @Test
    fun `aggregate health follows failure creating warning ready precedence`() {
        assertEquals(WorkspaceHealth.READY, aggregateWorkspaceHealth(emptyList()))
        assertEquals(WorkspaceHealth.READY, aggregateWorkspaceHealth(listOf(workspace(WorkspaceHealth.READY))))
        assertEquals(
            WorkspaceHealth.READY_WITH_WARNINGS,
            aggregateWorkspaceHealth(listOf(workspace(WorkspaceHealth.READY), workspace(WorkspaceHealth.READY_WITH_WARNINGS))),
        )
        assertEquals(
            WorkspaceHealth.CREATING,
            aggregateWorkspaceHealth(listOf(workspace(WorkspaceHealth.READY_WITH_WARNINGS), workspace(WorkspaceHealth.CREATING))),
        )
        assertEquals(
            WorkspaceHealth.FAILED,
            aggregateWorkspaceHealth(listOf(workspace(WorkspaceHealth.CREATING), workspace(WorkspaceHealth.FAILED))),
        )
    }

    @Test
    fun `archived lifecycle does not alter workspace health`() {
        val task = TaskManifest(
            folderName = "archived",
            taskDirectoryName = "archived",
            featureBranch = "feature/archived",
            createdAt = "2026-08-09 00:00:00",
            updatedAt = "2026-08-09 00:00:00",
            lifecycleStatus = TaskLifecycleStatus.ARCHIVED,
            services = listOf(workspace(WorkspaceHealth.READY_WITH_WARNINGS)),
        )

        assertEquals(TaskLifecycleStatus.ARCHIVED, task.lifecycleStatus)
        assertEquals(WorkspaceHealth.READY_WITH_WARNINGS, task.health)
    }

    private fun workspace(health: WorkspaceHealth) = ServiceWorkspace(
        repositoryId = "repo-$health",
        serviceName = "service-$health",
        repositoryPath = "C:/repo-$health",
        worktreePath = "C:/task/repo-$health",
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = "feature/$health",
        health = health,
    )
}
