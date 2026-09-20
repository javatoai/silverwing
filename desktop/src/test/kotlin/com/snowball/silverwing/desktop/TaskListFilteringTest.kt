package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.WorkspaceHealth
import kotlin.test.Test
import kotlin.test.assertEquals

class TaskListFilteringTest {
    @Test
    fun `search covers requirement title branch and service names`() {
        val task = task("支付修复", "feature/payment", "payment-service", module = "settlement-module")

        listOf("支付修复", "退款需求", "detail/123", "feature/payment", "payment-service", "settlement-module").forEach { query ->
            assertEquals(
                listOf(task),
                filterTasks(listOf(task), query, { "退款需求" }),
            )
        }
    }

    @Test
    fun `blank query keeps all tasks`() {
        val tasks = listOf(task("one", "feature/one", "service-a"), task("two", "feature/two", "service-b"))
        assertEquals(tasks, filterTasks(tasks, "  ") { null })
    }

    private fun task(
        name: String,
        branch: String,
        service: String,
        module: String = service,
        updatedAt: String = "2026-08-12 09:00:00",
    ) = TaskManifest(
        folderName = name,
        taskDirectoryName = name,
        featureBranch = branch,
        requirementLink = "https://project.feishu.cn/rta/userstory/detail/123",
        createdAt = updatedAt,
        updatedAt = updatedAt,
        services = listOf(
            ServiceWorkspace(
                repositoryId = service,
                serviceName = service,
                repositoryPath = "D:/$service",
                worktreePath = "D:/tasks/$name/$service",
                developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                branch = branch,
                health = WorkspaceHealth.READY,
                moduleName = module,
            ),
        ),
    )
}
