package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test

class TaskBranchInfoFormatterTest {
    @Test
    fun `formats requirement link before service branches in task order`() {
        val task = TaskManifest(
            folderName = "苹果月报",
            taskDirectoryName = "苹果月报",
            featureBranch = "feature/apple-report",
            requirementLink = "https://project.feishu.cn/obt/userstory/detail/7060612727",
            createdAt = "2026-08-05T00:00:00Z",
            updatedAt = "2026-08-05T00:00:00Z",
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = listOf(
                workspace("api-service", "feature/api"),
                workspace("job-manager", "feature/job"),
            ),
        )

        assertEquals(
            """需求链接：https://project.feishu.cn/obt/userstory/detail/7060612727

api-service：feature/api
job-manager：feature/job""",
            TaskBranchInfoFormatter.format(task),
        )
    }

    @Test
    fun `omits link heading when requirement link is blank`() {
        val task = TaskManifest(
            folderName = "任务",
            taskDirectoryName = "任务",
            featureBranch = "feature/task",
            createdAt = "2026-08-05T00:00:00Z",
            updatedAt = "2026-08-05T00:00:00Z",
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = listOf(workspace("api-service", "feature/task")),
        )

        assertEquals("api-service：feature/task", TaskBranchInfoFormatter.format(task))
    }

    @Test
    fun `formats service names with and without requirement link`() {
        val task = taskWithRequirementLink()

        assertEquals(
            "api-service\njob-manager",
            TaskBranchInfoFormatter.formatServices(task, includeRequirementLink = false),
        )
        assertEquals(
            "需求链接：https://project.feishu.cn/obt/userstory/detail/7060612727\n\napi-service\njob-manager",
            TaskBranchInfoFormatter.formatServices(task, includeRequirementLink = true),
        )
    }

    @Test
    fun `formats branch info without requirement link`() {
        val task = taskWithRequirementLink()

        assertEquals(
            "api-service：feature/api\njob-manager：feature/job",
            TaskBranchInfoFormatter.formatBranchInfo(task, includeRequirementLink = false),
        )
    }

    @Test
    fun `does not add requirement link heading when blank for either format`() {
        val task = taskWithRequirementLink(requirementLink = "   ")

        assertEquals("api-service\njob-manager", TaskBranchInfoFormatter.formatServices(task, includeRequirementLink = true))
        assertEquals(
            "api-service：feature/api\njob-manager：feature/job",
            TaskBranchInfoFormatter.formatBranchInfo(task, includeRequirementLink = true),
        )
    }

    private fun taskWithRequirementLink(requirementLink: String = "https://project.feishu.cn/obt/userstory/detail/7060612727") = TaskManifest(
        folderName = "苹果月报",
        taskDirectoryName = "苹果月报",
        featureBranch = "feature/apple-report",
        requirementLink = requirementLink,
        createdAt = "2026-08-05T00:00:00Z",
        updatedAt = "2026-08-05T00:00:00Z",
        lifecycleStatus = TaskLifecycleStatus.ACTIVE,
        services = listOf(
            workspace("api-service", "feature/api"),
            workspace("job-manager", "feature/job"),
        ),
    )

    private fun workspace(name: String, branch: String) = ServiceWorkspace(
        repositoryId = name,
        serviceName = name,
        repositoryPath = "D:/$name",
        worktreePath = "D:/task/$name",
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = branch,
        health = WorkspaceHealth.READY,
    )
}
