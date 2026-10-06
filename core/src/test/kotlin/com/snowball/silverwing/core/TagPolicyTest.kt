package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class TagPolicyTest {
    @Test
    fun `history resolution keeps merge mode and original target even when current target is absent`() {
        val workspace = workspace("repo-a", "service-a", "api").copy(tagMode = TagBuildMode.CURRENT_BRANCH, tagTargetRef = null)
        val task = manifest(workspace)
        val config = AppConfig(groups = listOf(GroupConfig("g", "G")))
        val operation = TagOperation(
            operationId = "old", folderName = task.folderName, serviceName = workspace.serviceName,
            repositoryId = workspace.repositoryId, sourceBranch = workspace.branch, targetBranch = "release/original",
            remote = "upstream", tagMode = TagBuildMode.MERGE_TO_TARGET_BRANCH, state = TagOperationState.FAILED,
            createdAt = task.createdAt, updatedAt = task.updatedAt, groupServiceId = workspace.groupServiceId,
            moduleId = workspace.moduleId,
        )
        val target = TagPolicy.resolveHistorical(config, task, operation)
        assertEquals("upstream", target.remote)
        assertEquals("release/original", target.targetBranch)
        assertEquals(TagBuildMode.MERGE_TO_TARGET_BRANCH, target.mode)
        assertFailsWith<IllegalStateException> { TagPolicy.resolveHistorical(config.copy(tagEnabled = false), task, operation) }
        assertFailsWith<IllegalStateException> {
            TagPolicy.resolveHistorical(config, task.copy(services = listOf(workspace.copy(tagEnabled = false))), operation)
        }
    }

    @Test
    fun `group and child switches both gate tag creation`() {
        val module = ServiceModuleConfig("api", "API", tagEnabled = true)
        val service = GroupServiceConfig(
            id = "service-a",
            repositoryId = "repo-a",
            displayName = "A",
            modules = listOf(module),
        )
        val workspace = workspace("repo-a", "service-a", "api")
        val manifest = manifest(workspace)
        val enabled = AppConfig(repositories = listOf(repository()), groups = listOf(GroupConfig("g", "G", true, listOf(service))))
        val disabled = enabled.copy(groups = listOf(enabled.groups.single().copy(tagEnabled = false)))
        val globallyDisabled = enabled.copy(tagEnabled = false)

        assertEquals("release/test", TagPolicy.resolve(enabled, manifest, workspace.selectionKey).targetBranch)
        assertFailsWith<IllegalStateException> { TagPolicy.resolve(disabled, manifest, workspace.selectionKey) }
        assertFailsWith<IllegalStateException> { TagPolicy.resolve(globallyDisabled, manifest, workspace.selectionKey) }
        assertEquals("release/test", TagPolicy.resolve(enabled.copy(groups = listOf(enabled.groups.single().copy(services = listOf(service.copy(modules = listOf(module.copy(tagEnabled = false))))))), manifest, workspace.selectionKey).targetBranch)
    }

    @Test
    fun `independent clone uses actual clone branch and clone tag settings`() {
        val service = GroupServiceConfig(
            id = "clone-a",
            repositoryId = "repo-a",
            displayName = "A clone",
            modules = listOf(ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = true, tagTargetRef = "origin/uat/test")),
        )
        val workspace = workspace("repo-a", "clone-a", "clone").copy(
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            branch = "release/fixed",
            tagEnabled = true,
            tagTargetRef = "origin/uat/test",
        )
        val manifest = manifest(workspace)
        val config = AppConfig(repositories = listOf(repository()), groups = listOf(GroupConfig("g", "G", true, listOf(service))))

        val target = TagPolicy.resolve(config, manifest, workspace.selectionKey)

        assertEquals("release/fixed", target.sourceBranch)
        assertEquals("uat/test", target.targetBranch)
    }
}

private fun workspace(repositoryId: String, serviceId: String, moduleId: String) = ServiceWorkspace(
    repositoryId = repositoryId,
    serviceName = serviceId,
    repositoryPath = "C:/repo",
    worktreePath = "C:/task/repo",
    developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
    branch = "feature/x",
    health = WorkspaceHealth.READY,
    groupServiceId = serviceId,
    moduleId = moduleId,
    tagEnabled = true,
    tagTargetRef = "origin/release/test",
)

private fun repository() = RepositoryConfig(
    id = "repo-a",
    name = "A",
    rootPath = "C:/repo",
    gitCommonDirectory = "C:/repo/.git",
    originUrl = "https://example.test/a.git",
)

private fun manifest(workspace: ServiceWorkspace) = TaskManifest(
    folderName = "T",
    taskDirectoryName = "T",
    featureBranch = "feature/x",
    createdAt = "2026-08-08T00:00:00Z",
    updatedAt = "2026-08-08T00:00:00Z",
    lifecycleStatus = TaskLifecycleStatus.ACTIVE,
    services = listOf(workspace),
    groupId = "g",
)
