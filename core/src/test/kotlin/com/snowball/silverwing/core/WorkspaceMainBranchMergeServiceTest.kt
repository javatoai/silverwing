package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class WorkspaceMainBranchMergeServiceTest {
    @TempDir lateinit var temporary: Path

    @Test fun `fetches configured remote and fast forwards the feature without touching other remotes or tags`() {
        val f = fixture()
        GitTestSupport.run(f.repository, "remote", "add", "origin", temporary.resolve("unreachable.git").toString())
        GitTestSupport.run(f.target, "tag", "local-only")
        val updated = advance(f.seed, "upstream.txt")
        val result = f.merge()
        assertEquals(WorkspaceMainBranchMergeOutcome.FAST_FORWARDED, result.outcome)
        assertEquals("github/master → feature/task", result.direction)
        assertEquals(updated, GitTestSupport.run(f.target, "rev-parse", "HEAD"))
        assertEquals("feature/task", GitTestSupport.run(f.target, "branch", "--show-current"))
        assertEquals("local-only", GitTestSupport.run(f.target, "tag", "--list"))
        assertTrue(GitTestSupport.run(f.remote, "for-each-ref", "--format=%(refname)", "refs/heads/feature").isBlank())
        assertTrue(f.commands.filter { "fetch" in it }.all { "github" in it && "--no-tags" in it })
        assertFalse(f.commands.any { it.any { argument -> argument in setOf("push", "stash", "reset", "rebase", "fetchTags") } })
    }

    @Test fun `diverged feature creates an ordinary two parent merge and latest is a no op`() {
        val f = fixture()
        commit(f.target, "feature.txt", "feature")
        val featureHead = GitTestSupport.run(f.target, "rev-parse", "HEAD")
        val upstreamHead = advance(f.seed, "upstream.txt")
        assertEquals(WorkspaceMainBranchMergeOutcome.MERGED, f.merge().outcome)
        val head = GitTestSupport.run(f.target, "rev-parse", "HEAD")
        assertEquals("$head $featureHead $upstreamHead", GitTestSupport.run(f.target, "rev-list", "--parents", "-n", "1", "HEAD"))
        assertEquals(WorkspaceMainBranchMergeOutcome.UP_TO_DATE, f.merge().outcome)
        assertEquals(head, GitTestSupport.run(f.target, "rev-parse", "HEAD"))
    }

    @Test fun `conflicts remain in feature worktree with correct direction and a second attempt cannot start another merge`() {
        val f = fixture()
        commit(f.target, "README.md", "feature\n")
        advance(f.seed, "README.md", "main\n")
        val result = f.merge()
        assertEquals(WorkspaceMainBranchMergeOutcome.CONFLICT, result.outcome)
        assertEquals(listOf("README.md"), result.conflictFiles)
        assertTrue(result.message.contains("github/master 分支合并到 feature/task"))
        assertTrue(result.message.contains("payments · backend"))
        assertEquals("merge", f.git.status(f.target).operationInProgress)
        assertTrue(Files.readString(f.target.resolve("README.md")).contains("<<<<<<<"))
        val fetchCount = f.commands.count { "fetch" in it }
        val error = assertFailsWith<IllegalArgumentException> { f.merge() }
        assertTrue(error.message.orEmpty().contains("进行中的 Git 操作"))
        assertEquals(fetchCount, f.commands.count { "fetch" in it })
        assertEquals("merge", f.git.status(f.target).operationInProgress)
    }

    @Test fun `dirty tracked staged and untracked changes each prevent fetch and remain intact`() {
        val f = fixture()
        for (mode in listOf("untracked", "unstaged", "staged")) {
            val path = if (mode == "untracked") "new.txt" else "README.md"
            Files.writeString(f.target.resolve(path), "user work\n")
            if (mode == "staged") GitTestSupport.run(f.target, "add", path)
            assertFailsWith<IllegalArgumentException> { f.merge() }
            assertEquals("user work\n", Files.readString(f.target.resolve(path)))
            if (mode == "untracked") Files.delete(f.target.resolve(path))
            else GitTestSupport.run(f.target, "restore", "--staged", "--worktree", path)
        }
        assertFalse(f.commands.any { "fetch" in it })
    }

    @Test fun `wrong actual branch and detached HEAD are rejected before fetch`() {
        val f = fixture()
        GitTestSupport.run(f.target, "switch", "-c", "feature/unexpected")
        assertFailsWith<IllegalArgumentException> { f.merge() }
        GitTestSupport.run(f.target, "switch", "--detach")
        assertFailsWith<IllegalArgumentException> { f.merge() }
        assertFalse(f.commands.any { "fetch" in it })
    }

    @Test fun `archived unready protected and stale manifest requests are rejected`() {
        val f = fixture()
        val original = f.task
        for (task in listOf(original.copy(lifecycleStatus = TaskLifecycleStatus.ARCHIVED),
            original.copy(services = listOf(f.workspace.copy(health = WorkspaceHealth.FAILED))),
            original.copy(createdAt = "changed"))) {
            ManifestStore().save(f.directory, task)
            assertFailsWith<IllegalArgumentException> { f.merge() }
        }
        ManifestStore().save(f.directory, original)
        assertFailsWith<IllegalArgumentException> { f.merge(f.config.copy(blockedGitWriteBranches = listOf("feature/task"))) }
        assertFalse(f.commands.any { "fetch" in it })
    }

    @Test fun `deleted remote branch and fetch failure never merge a stale tracking ref`() {
        val f = fixture()
        advance(f.seed, "upstream.txt")
        GitTestSupport.run(f.repository, "fetch", "github")
        val before = GitTestSupport.run(f.target, "rev-parse", "HEAD")
        GitTestSupport.run(f.remote, "config", "receive.denyDeleteCurrent", "ignore")
        GitTestSupport.run(f.seed, "push", "origin", "--delete", "master")
        assertFailsWith<GitException> { f.merge() }
        assertEquals(before, GitTestSupport.run(f.target, "rev-parse", "HEAD"))
        assertTrue(f.git.refExists(f.target, "refs/remotes/github/master"))
        GitTestSupport.run(f.repository, "remote", "set-url", "github", temporary.resolve("missing.git").toString())
        assertFailsWith<GitException> { f.merge() }
        assertEquals(before, GitTestSupport.run(f.target, "rev-parse", "HEAD"))
    }

    @Test fun `missing remote cannot fall back to origin`() {
        val f = fixture()
        GitTestSupport.run(f.repository, "remote", "rename", "github", "origin")
        advance(f.seed, "upstream.txt")
        val failure = assertFailsWith<IllegalArgumentException> { f.merge() }
        assertTrue(failure.message.orEmpty().contains("主分支远程不存在"))
        assertFalse(f.commands.any { "fetch" in it })
    }

    @Test fun `task and repository locks reject simultaneous writes before fetch`() {
        val f = fixture()
        f.taskLock.withLock(f.directory) { assertFailsWith<IllegalStateException> { f.merge() } }
        f.repositoryLock.withLock(f.git.commonDirectory(f.repository)) { assertFailsWith<IllegalStateException> { f.merge() } }
        assertFalse(f.commands.any { "fetch" in it })
    }

    @Test fun `changes made during fetch prevent a merge`() {
        val f = fixture(afterFetch = { Files.writeString(it.resolve("arrived-during-fetch.txt"), "keep") })
        advance(f.seed, "upstream.txt")
        val before = GitTestSupport.run(f.target, "rev-parse", "HEAD")
        assertFailsWith<IllegalArgumentException> { f.merge() }
        assertEquals(before, GitTestSupport.run(f.target, "rev-parse", "HEAD"))
        assertEquals("keep", Files.readString(f.target.resolve("arrived-during-fetch.txt")))
    }

    @Test fun `module master override and clone mapping are shared with history and never use tag target`() {
        val f = fixture()
        val service = GroupServiceConfig("repo", "repo", "payments", masterBranch = "github/main",
            modules = listOf(ServiceModuleConfig("default", masterBranch = "upstream/release/main", tagTargetRef = "github/release/test")))
        val config = f.config.copy(groups = listOf(GroupConfig(id = DEFAULT_GROUP_ID, name = "test", services = listOf(service))))
        val module = f.workspace.copy(baseRef = null, tagTargetRef = "origin/test")
        assertEquals(WorkspaceMainBranch("upstream/release/main", "upstream/release/main"), WorkspaceMainBranchResolver.resolve(config, f.task, module))
        assertEquals(WorkspaceMainBranch("github/master", "github/master"), WorkspaceMainBranchResolver.resolve(config, f.task, f.workspace))
        assertEquals(WorkspaceMainBranch("upstream/release/main", "origin/release/main"), WorkspaceMainBranchResolver.resolve(config, f.task, module.copy(strategy = WorkspaceStrategy.INDEPENDENT_CLONE)))
    }

    @Test fun `independent clone fetches mapped origin with narrow feature refspec and verifies ownership`() {
        val f = fixture()
        val clone = f.directory.resolve("independent")
        val original = f.workspace
        IndependentCloneWorkspaceSafety.cloneIntoPlace(f.directory, clone, IndependentCloneWorkspaceSafety.ownership(
            f.directory, original.repositoryId, original.groupServiceId, original.moduleId,
        )) { GitTestSupport.clone(f.remote, it) }
        GitTestSupport.run(clone, "switch", "-c", original.branch)
        GitTestSupport.run(clone, "config", "remote.origin.fetch", "+refs/heads/feature/*:refs/remotes/origin/feature/*")
        val workspace = original.copy(strategy = WorkspaceStrategy.INDEPENDENT_CLONE, worktreePath = clone.toString(), originUrl = f.remote.toString(), pushRemote = "origin")
        val task = f.task.copy(services = listOf(workspace))
        ManifestStore().save(f.directory, task)
        val updated = advance(f.seed, "upstream.txt")
        val service = WorkspaceMainBranchMergeService(f.git, taskLock = f.taskLock, repositoryLock = f.repositoryLock)
        val result = service.merge(f.config, f.directory, task, workspace)
        assertEquals(WorkspaceMainBranchMergeOutcome.FAST_FORWARDED, result.outcome)
        assertEquals("github/master → feature/task", result.direction)
        assertEquals(updated, GitTestSupport.run(clone, "rev-parse", "HEAD"))
        assertEquals("origin", GitTestSupport.run(clone, "remote"))
        Files.delete(clone.resolve(".git/silverwing-owner"))
        assertFailsWith<IllegalArgumentException> { service.merge(f.config, f.directory, task, workspace) }
    }

    @Test fun `task root and configured repository identity mismatches are refused`() {
        val f = fixture()
        assertFailsWith<IllegalArgumentException> { f.merge(f.config.copy(taskRoot = temporary.resolve("wrong-root").toString())) }
        assertFailsWith<IllegalArgumentException> { f.merge(f.config.copy(repositories = f.config.repositories.map {
            it.copy(gitCommonDirectory = f.seed.resolve(".git").toString())
        })) }
        assertFalse(f.commands.any { "fetch" in it })
    }

    @Test fun `module branch with slashes is fetched exactly and not replaced by test tag target`() {
        val f = fixture()
        GitTestSupport.run(f.seed, "switch", "-c", "release/stable")
        val updated = commit(f.seed, "stable.txt", "stable")
        GitTestSupport.run(f.seed, "push", "origin", "release/stable")
        val workspace = f.workspace.copy(baseRef = "github/release/stable", tagTargetRef = "github/does-not-exist")
        val task = f.task.copy(services = listOf(workspace))
        ManifestStore().save(f.directory, task)
        val result = WorkspaceMainBranchMergeService(f.git, taskLock = f.taskLock, repositoryLock = f.repositoryLock).merge(f.config, f.directory, task, workspace)
        assertEquals(WorkspaceMainBranchMergeOutcome.FAST_FORWARDED, result.outcome)
        assertEquals("github/release/stable", result.sourceRef)
        assertEquals(updated, GitTestSupport.run(f.target, "rev-parse", "HEAD"))
    }

    private fun fixture(afterFetch: (Path) -> Unit = {}): Fixture {
        val root = temporary.resolve("fixture")
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root)
        val repository = GitTestSupport.clone(remote, root.resolve("repository"))
        GitTestSupport.run(repository, "remote", "rename", "origin", "github")
        val directory = Files.createDirectories(root.resolve("tasks/task"))
        val target = directory.resolve("backend")
        GitTestSupport.run(repository, "worktree", "add", "-b", "feature/task", target.toString(), "github/master")
        val workspace = ServiceWorkspace("repo", "payments", repository.toString(), target.toString(), DevelopmentToolType.INTELLIJ_IDEA,
            "feature/task", health = WorkspaceHealth.READY, moduleName = "backend", baseRef = "github/master", pushRemote = "github")
        val task = TaskManifest(folderName = "task", taskDirectoryName = "task", featureBranch = "feature/task", createdAt = "2026-10-03T00:00:00Z", updatedAt = "2026-10-03T00:00:00Z", services = listOf(workspace))
        ManifestStore().save(directory, task)
        val commands = mutableListOf<List<String>>()
        val isolatedConfig = root.resolve("global.gitconfig").also { Files.writeString(it, "") }
        val runner = object : CommandRunner {
            val delegate = ProcessCommandRunner()
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                commands += command
                val result = delegate.run(command, workingDirectory, timeout, environment + mapOf("GIT_CONFIG_GLOBAL" to isolatedConfig.toString(), "GIT_CONFIG_NOSYSTEM" to "1", "GIT_TERMINAL_PROMPT" to "0"))
                if ("fetch" in command && result.succeeded) afterFetch(target)
                return result
            }
        }
        val git = GitClient(runner)
        val config = AppConfig(taskRoot = directory.parent.toString(), repositories = listOf(RepositoryConfig("repo", "payments", repository.toString(), git.commonDirectory(repository).toString(), remote.toString())))
        val paths = ApplicationPaths(root.resolve("app"))
        return Fixture(remote, seed, repository, directory, target, task, config, git, commands, FileTaskOperationLock(paths), RepositoryOperationLock(paths))
    }

    private data class Fixture(val remote: Path, val seed: Path, val repository: Path, val directory: Path, val target: Path,
        val task: TaskManifest, val config: AppConfig, val git: GitClient, val commands: List<List<String>>,
        val taskLock: TaskOperationLock, val repositoryLock: RepositoryOperationLock) {
        val workspace get() = task.services.single()
        fun merge(configuration: AppConfig = config) = WorkspaceMainBranchMergeService(git, taskLock = taskLock, repositoryLock = repositoryLock)
            .merge(configuration, directory, task, workspace)
    }

    private fun commit(repository: Path, name: String, content: String): String {
        Files.writeString(repository.resolve(name), content)
        GitTestSupport.run(repository, "add", name)
        GitTestSupport.run(repository, "commit", "-m", "update $name")
        return GitTestSupport.run(repository, "rev-parse", "HEAD")
    }

    private fun advance(seed: Path, name: String, content: String = "upstream\n"): String = commit(seed, name, content).also {
        GitTestSupport.run(seed, "push", "origin", "master")
    }
}
