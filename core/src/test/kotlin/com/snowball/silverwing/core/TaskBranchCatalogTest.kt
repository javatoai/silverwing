package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking

class TaskBranchCatalogTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `remote catalog restores multi base task branch candidates without fetching`() = runBlocking {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("source"))
        listOf("feature/demo-master", "feature/demo-release-test").forEach { branch ->
            GitTestSupport.run(seed, "switch", "-c", branch, "master")
            Files.writeString(seed.resolve("$branch.txt".replace('/', '-')), branch)
            GitTestSupport.run(seed, "add", ".")
            GitTestSupport.run(seed, "commit", "-m", branch)
            GitTestSupport.run(seed, "push", "origin", branch)
        }
        GitTestSupport.run(seed, "switch", "master")
        val common = GitTestSupport.run(seed, "rev-parse", "--git-common-dir")
        val repository = RepositoryConfig("repo", "repo", seed.toString(), seed.resolve(common).normalize().toString(), remote.toString())
        val service = GroupServiceConfig(
            id = "service",
            repositoryId = repository.id,
            displayName = "Service",
            modules = listOf(
                ServiceModuleConfig("master", name = "master", masterBranch = "origin/master"),
                ServiceModuleConfig("test", name = "release-test", masterBranch = "origin/release/test"),
            ),
        )
        val config = AppConfig(repositories = listOf(repository), groups = listOf(GroupConfig("default", "Default", services = listOf(service))))

        val progress = mutableListOf<TaskBranchCatalogProgress>()
        val result = GitTaskBranchCatalog().list(config, "default", setOf("service"), progress::add)

        val candidate = result.candidates.single { it.branch == "feature/demo" }
        assertEquals(2, candidate.matchedWorkspaceCount)
        assertEquals(2, candidate.totalWorkspaceCount)
        assertTrue(result.failures.isEmpty())
        assertEquals(TaskBranchCatalogProgress(0, 1), progress.first())
        assertEquals(TaskBranchCatalogProgress(1, 1), progress.last())
    }

    @Test
    fun `remote catalog counts same base modules as independent workspaces`() = runBlocking {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("same-base"))
        listOf("feature/shared-api", "feature/shared-job").forEach { branch ->
            GitTestSupport.run(seed, "switch", "-c", branch, "master")
            Files.writeString(seed.resolve("${branch.substringAfterLast('/')}.txt"), branch)
            GitTestSupport.run(seed, "add", ".")
            GitTestSupport.run(seed, "commit", "-m", branch)
            GitTestSupport.run(seed, "push", "origin", branch)
        }
        GitTestSupport.run(seed, "switch", "master")
        val common = GitTestSupport.run(seed, "rev-parse", "--git-common-dir")
        val repository = RepositoryConfig("repo", "repo", seed.toString(), seed.resolve(common).normalize().toString(), remote.toString())
        val service = GroupServiceConfig(
            id = "service",
            repositoryId = repository.id,
            displayName = "Service",
            modules = listOf(
                ServiceModuleConfig("api", name = "api", masterBranch = "origin/master"),
                ServiceModuleConfig("job", name = "job", masterBranch = "origin/master"),
            ),
        )
        val config = AppConfig(repositories = listOf(repository), groups = listOf(GroupConfig("default", "Default", services = listOf(service))))

        val result = GitTaskBranchCatalog().list(config, "default", setOf("service"))

        val candidate = result.candidates.single { it.branch == "feature/shared" }
        assertEquals(2, candidate.matchedWorkspaceCount)
        assertEquals(2, candidate.totalWorkspaceCount)
    }

    @Test
    fun `mixed service candidate keeps worktree suffix from the complete module plan`() = runBlocking {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("mixed"))
        GitTestSupport.run(seed, "switch", "-c", "feature/mixed-api", "master")
        GitTestSupport.run(seed, "push", "origin", "feature/mixed-api")
        GitTestSupport.run(seed, "switch", "master")
        val common = GitTestSupport.run(seed, "rev-parse", "--git-common-dir")
        val repository = RepositoryConfig("repo", "repo", seed.toString(), seed.resolve(common).normalize().toString(), remote.toString())
        val service = GroupServiceConfig(
            id = "service",
            repositoryId = repository.id,
            displayName = "Service",
            modules = listOf(
                ServiceModuleConfig("api", name = "api", masterBranch = "origin/master"),
                ServiceModuleConfig("docs", name = "docs", masterBranch = "origin/master", strategy = WorkspaceStrategy.INDEPENDENT_CLONE),
            ),
        )
        val config = AppConfig(repositories = listOf(repository), groups = listOf(GroupConfig("default", "Default", services = listOf(service))))

        val result = GitTaskBranchCatalog().list(config, "default", setOf("service"))

        assertEquals(listOf("feature/mixed"), result.candidates.map(TaskBranchCandidate::branch))
    }
}
