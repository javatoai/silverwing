package com.snowball.silverwing.core

import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class GroupConfigurationServiceTest {
    @Test
    fun `groups retain array order and only empty groups can be deleted`() {
        val repository = InMemoryConfigurationRepository(AppConfig())
        val service = GroupConfigurationService(repository, StubRepositoryInspector())

        val alpha = service.addGroup("Alpha").groups.last()
        val beta = service.addGroup("Beta").groups.last()
        service.moveGroup(beta.id, -1)

        assertEquals(listOf(DEFAULT_GROUP_ID, beta.id, alpha.id), repository.load().groups.map { it.id })
        service.addRepository(alpha.id, Path.of("C:/repo-a"))
        assertFailsWith<IllegalStateException> { service.deleteGroup(alpha.id) }
        service.deleteGroup(beta.id)
        assertEquals(listOf(DEFAULT_GROUP_ID, alpha.id), repository.load().groups.map { it.id })
    }

    @Test
    fun `single group mode remains hidden until a second group exists`() {
        val repository = InMemoryConfigurationRepository(AppConfig())
        val service = GroupConfigurationService(repository, StubRepositoryInspector())

        assertEquals(false, service.load().showsGroupUi)
        service.addGroup("Second")
        assertEquals(true, service.load().showsGroupUi)
    }

    @Test
    fun `group defaults preserve branch prefix and unknown workspace tool ids`() {
        val repository = InMemoryConfigurationRepository(AppConfig())
        val service = GroupConfigurationService(repository, StubRepositoryInspector())

        service.updateGroupDefaults(DEFAULT_GROUP_ID, "feature/pay-", listOf("codex", "future-tool"))

        val group = repository.load().groups.single()
        assertEquals("feature/pay-", group.defaultBranchPrefix)
        assertEquals(listOf("codex", "future-tool"), group.defaultWorkspaceToolIds)
    }

    @Test
    fun `same repository is reused across groups but cannot repeat inside one group`() {
        val repository = InMemoryConfigurationRepository(AppConfig())
        val service = GroupConfigurationService(repository, StubRepositoryInspector())
        val second = service.addGroup("Second").groups.last()

        service.addRepository(DEFAULT_GROUP_ID, Path.of("C:/repo-a"))
        service.addRepository(second.id, Path.of("C:/repo-a"))

        assertEquals(1, repository.load().repositories.size)
        assertEquals("origin/main", repository.load().group(DEFAULT_GROUP_ID).services.single().modules.single().baseRef)
        assertEquals(1, repository.load().group(DEFAULT_GROUP_ID).services.size)
        assertEquals(1, repository.load().group(second.id).services.size)
        assertFailsWith<IllegalArgumentException> {
            service.addRepository(DEFAULT_GROUP_ID, Path.of("C:/repo-a"))
        }
    }

    @Test
    fun `new service starts with global default development tool while later saved choice remains authoritative`() {
        val repository = InMemoryConfigurationRepository(AppConfig(defaultDevelopmentTool = DevelopmentToolType.WEBSTORM))
        val service = GroupConfigurationService(
            repository,
            StubRepositoryInspector(),
            developmentToolRecommendation = DevelopmentToolRecommendationService { DevelopmentToolType.WEBSTORM },
        )

        val added = service.addRepository(DEFAULT_GROUP_ID, Path.of("C:/repo-web"))
            .group(DEFAULT_GROUP_ID).services.single()
        assertEquals(DevelopmentToolType.WEBSTORM, added.developmentTool)

        service.updateService(DEFAULT_GROUP_ID, added.copy(developmentTool = DevelopmentToolType.INTELLIJ_IDEA))
        assertEquals(DevelopmentToolType.INTELLIJ_IDEA, repository.load().group(DEFAULT_GROUP_ID).services.single().developmentTool)
    }

    @Test
    fun `batch add persists valid repositories once and reports invalid or duplicate folders`() {
        val repository = InMemoryConfigurationRepository(AppConfig())
        val inspector = object : RepositoryInspector {
            override fun inspect(selectedDirectory: Path): RepositoryConfig {
                if (selectedDirectory.fileName.toString() == "not-git") error("不是 Git 仓库")
                return StubRepositoryInspector().inspect(selectedDirectory)
            }
        }
        val service = GroupConfigurationService(repository, inspector)

        val result = service.addRepositories(
            DEFAULT_GROUP_ID,
            listOf(Path.of("C:/repo-a"), Path.of("C:/not-git"), Path.of("C:/repo-a"), Path.of("C:/repo-b")),
        )

        assertEquals(listOf("repo-a", "repo-b"), result.added.map { it.fileName.toString() })
        assertEquals(2, result.skipped.size)
        assertTrue(result.skipped.any { "不是 Git 仓库" in it.reason })
        assertTrue(result.skipped.any { "重复" in it.reason })
        assertEquals(1, repository.saveCount)
        assertEquals(WorkspaceStrategy.STANDARD_WORKTREE, repository.load().groups.single().services.first().modules.single().strategy)
    }

    @Test
    fun `task scan failures block destructive group changes`() {
        val extraGroup = GroupConfig(id = "extra", name = "Extra")
        val repository = InMemoryConfigurationRepository(
            AppConfig(taskRoot = "C:/tasks", groups = AppConfig().groups + extraGroup),
        )
        val failingManifests = object : TaskManifestRepository {
            override fun save(taskDirectory: Path, manifest: TaskManifest) = Unit
            override fun load(taskDirectory: Path): TaskManifest = error("unused")
            override fun scan(taskRoot: Path) = ManifestScanResult(
                current = emptyList(),
                unsupportedDirectories = emptyList(),
                failures = mapOf(taskRoot.resolve("broken") to "invalid JSON"),
            )
        }
        val service = GroupConfigurationService(
            repository,
            StubRepositoryInspector(),
            ManifestTaskGroupUsage(failingManifests),
        )

        assertFailsWith<IllegalStateException> { service.deleteGroup(extraGroup.id) }
        assertEquals(listOf(DEFAULT_GROUP_ID, extraGroup.id), repository.load().groups.map { it.id })
    }
}

private class InMemoryConfigurationRepository(
    private var value: AppConfig,
) : ConfigurationRepository {
    var saveCount: Int = 0
        private set

    override fun load(): AppConfig = value
    override fun save(config: AppConfig) {
        saveCount++
        value = config
    }
}

private class StubRepositoryInspector : RepositoryInspector {
    override fun inspect(selectedDirectory: Path): RepositoryConfig {
        val name = selectedDirectory.fileName.toString()
        return RepositoryConfig(
            id = name,
            name = name,
            rootPath = selectedDirectory.toAbsolutePath().normalize().toString(),
            gitCommonDirectory = "C:/git-common/$name",
            originUrl = "https://example.test/$name.git",
            currentBranch = "master",
            defaultRemoteBranch = "main",
        )
    }
}
