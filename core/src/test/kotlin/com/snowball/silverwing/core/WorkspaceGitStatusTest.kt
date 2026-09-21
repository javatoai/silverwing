package com.snowball.silverwing.core

import java.nio.file.Path
import java.nio.file.Files
import java.time.Duration
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.withTimeout
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

class WorkspaceGitStatusTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `porcelain parser reports file change kinds and counts rename once`() {
        val output = listOf(
            "1 M. N... 100644 100644 100644 a a staged.txt",
            "1 .M N... 100644 100644 100644 a a unstaged name.txt",
            "1 A. N... 100644 100644 100644 a a added.txt",
            "1 D. N... 100644 100644 100644 a a deleted.txt",
            "1 T. N... 100644 100644 100644 a a type-changed.txt",
            "u UU N... 100644 100644 100644 100644 a a a conflict.txt",
            "? untracked.txt",
            "2 R. N... 100644 100644 100644 a a R100 renamed.txt",
            "old.txt",
            "2 C. N... 100644 100644 100644 a a C100 copied.txt",
            "source.txt",
            "! ignored.txt",
            "",
        ).joinToString("\u0000")
        assertEquals(
            listOf(
                WorkspaceGitFileChange("staged.txt", WorkspaceGitFileChangeKind.MODIFIED),
                WorkspaceGitFileChange("unstaged name.txt", WorkspaceGitFileChangeKind.MODIFIED),
                WorkspaceGitFileChange("added.txt", WorkspaceGitFileChangeKind.ADDED),
                WorkspaceGitFileChange("deleted.txt", WorkspaceGitFileChangeKind.DELETED),
                WorkspaceGitFileChange("type-changed.txt", WorkspaceGitFileChangeKind.TYPE_CHANGED),
                WorkspaceGitFileChange("conflict.txt", WorkspaceGitFileChangeKind.CONFLICTED),
                WorkspaceGitFileChange("untracked.txt", WorkspaceGitFileChangeKind.UNTRACKED),
                WorkspaceGitFileChange("renamed.txt", WorkspaceGitFileChangeKind.RENAMED),
                WorkspaceGitFileChange("copied.txt", WorkspaceGitFileChangeKind.COPIED),
            ),
            PorcelainV2Parser.changedFiles(output),
        )
        assertEquals(
            setOf("staged.txt", "unstaged name.txt", "added.txt", "deleted.txt", "type-changed.txt", "conflict.txt", "untracked.txt", "renamed.txt", "copied.txt"),
            PorcelainV2Parser.changedPaths(output),
        )
    }

    @Test
    fun `shared physical worktree is inspected once`() {
        var reads = 0
        val service = WorkspaceGitStatusService(reader = WorkspaceGitStatusReader { workspace ->
            reads++
            WorkspaceGitHealth(WorkspaceGitHealthState.READY, message = workspace.worktreePath)
        })
        val path = Path.of("build/shared").toAbsolutePath().normalize().toString()
        val workspace = ServiceWorkspace("repo", "service", "repo", path, DevelopmentToolType.INTELLIJ_IDEA, "feature/x")
        val result = runBlocking { service.inspect(listOf(workspace, workspace.copy(moduleId = "other"))) }
        assertEquals(1, reads)
        assertEquals(1, result.size)
    }

    @Test
    fun `workspace inspection runs at most four physical worktrees concurrently and isolates failures`() = runBlocking {
        val active = AtomicInteger()
        val maximum = AtomicInteger()
        val started = CountDownLatch(4)
        val release = CountDownLatch(1)
        val service = WorkspaceGitStatusService(reader = WorkspaceGitStatusReader { workspace ->
            val current = active.incrementAndGet()
            maximum.accumulateAndGet(current, ::maxOf)
            started.countDown()
            check(started.await(2, TimeUnit.SECONDS)) { "four checks did not start concurrently" }
            release.await(2, TimeUnit.SECONDS)
            active.decrementAndGet()
            if (workspace.serviceName == "broken") error("broken repository")
            WorkspaceGitHealth(WorkspaceGitHealthState.READY, actualBranch = workspace.branch)
        })
        val workspaces = (1..8).map { index ->
            ServiceWorkspace(
                "repo-$index",
                if (index == 8) "broken" else "service-$index",
                "repo-$index",
                Path.of("build/status-$index").toAbsolutePath().normalize().toString(),
                DevelopmentToolType.INTELLIJ_IDEA,
                "feature/$index",
            )
        }
        val releaser = Thread {
            check(started.await(2, TimeUnit.SECONDS))
            release.countDown()
        }.apply { start() }

        val result = service.inspect(workspaces)
        releaser.join()

        assertEquals(4, maximum.get())
        assertEquals(8, result.size)
        assertEquals(WorkspaceGitIssue.INSPECTION_FAILED, result.values.single { it.expectedBranch == "feature/8" }.issue)
    }

    @Test
    fun `workspace inspection publishes fast results before slow worktrees finish and supports cancellation`() = runBlocking {
        val slowStarted = CountDownLatch(1)
        val releaseSlow = CountDownLatch(1)
        val interrupted = CountDownLatch(1)
        val firstPublished = CompletableDeferred<String>()
        val service = WorkspaceGitStatusService(reader = WorkspaceGitStatusReader { workspace ->
            if (workspace.serviceName == "slow") {
                slowStarted.countDown()
                try {
                    releaseSlow.await()
                } catch (error: InterruptedException) {
                    interrupted.countDown()
                    throw error
                }
            }
            WorkspaceGitHealth(WorkspaceGitHealthState.READY, actualBranch = workspace.branch)
        })
        fun workspace(name: String, index: Int) = ServiceWorkspace(
            "repo-$index",
            name,
            "repo-$index",
            Path.of("build/progressive-$index").toAbsolutePath().normalize().toString(),
            DevelopmentToolType.INTELLIJ_IDEA,
            "feature/$index",
        )
        val job = async(Dispatchers.Default) {
            service.inspect(listOf(workspace("slow", 1), workspace("fast", 2))) { path, _ ->
                firstPublished.complete(path.fileName.toString())
            }
        }

        assertTrue(slowStarted.await(2, TimeUnit.SECONDS))
        assertEquals("progressive-2", withTimeout(2_000) { firstPublished.await() })
        job.cancelAndJoin()
        assertTrue(interrupted.await(2, TimeUnit.SECONDS))
        releaseSlow.countDown()
    }

    @Test
    fun `reader reports untracked files and local commits without contacting remote`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("source"))
        val repository = GitTestSupport.clone(remote, temporary.resolve("clone"))
        val reader = GitWorkspaceGitStatusReader()

        val workspace = ServiceWorkspace("repo", "service", repository.toString(), repository.toString(), DevelopmentToolType.INTELLIJ_IDEA, "master", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, originUrl = remote.toString())
        assertEquals(LocalPushState.PUSHED, reader.read(workspace).pushState)
        Files.writeString(repository.resolve("untracked.txt"), "local")
        val dirty = reader.read(workspace)
        assertEquals(1, dirty.dirtyFileCount)
        assertEquals(
            listOf(WorkspaceGitFileChange("untracked.txt", WorkspaceGitFileChangeKind.UNTRACKED)),
            dirty.dirtyFiles,
        )

        GitTestSupport.run(repository, "add", "untracked.txt")
        GitTestSupport.run(repository, "commit", "-m", "local commit")
        val ahead = reader.read(workspace)
        assertEquals(LocalPushState.AHEAD, ahead.pushState)
        assertEquals(1, ahead.unpushedCommitCount)
        assertEquals(0, ahead.dirtyFileCount)
        assertTrue(ahead.dirtyFiles.isEmpty())

        GitTestSupport.run(repository, "switch", "-c", "feature/no-upstream")
        val featureWorkspace = workspace.copy(branch = "feature/no-upstream")
        assertEquals(LocalPushState.NO_UPSTREAM, reader.read(featureWorkspace).pushState)

        GitTestSupport.run(repository, "config", "branch.feature/no-upstream.remote", "origin")
        GitTestSupport.run(repository, "config", "branch.feature/no-upstream.merge", "refs/heads/missing")
        assertEquals(LocalPushState.REMOTE_BRANCH_MISSING, reader.read(featureWorkspace).pushState)
    }

    @Test
    fun `reader distinguishes missing non git and wrong branch`() {
        val reader = GitWorkspaceGitStatusReader()
        val missing = temporary.resolve("missing")
        val missingWorkspace = ServiceWorkspace("repo", "service", missing.toString(), missing.toString(), DevelopmentToolType.INTELLIJ_IDEA, "feature/x")
        assertEquals(WorkspaceGitIssue.MISSING, reader.read(missingWorkspace).issue)

        val plain = temporary.resolve("plain")
        Files.createDirectories(plain)
        assertEquals(WorkspaceGitIssue.NOT_GIT, reader.read(missingWorkspace.copy(repositoryPath = plain.toString(), worktreePath = plain.toString())).issue)

        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("wrong-branch-source"))
        val repository = GitTestSupport.clone(remote, temporary.resolve("wrong-branch-clone"))
        GitTestSupport.run(repository, "switch", "-c", "other")
        val health = reader.read(
            missingWorkspace.copy(
                repositoryPath = repository.toString(),
                worktreePath = repository.toString(),
                strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                originUrl = remote.toString(),
            ),
        )
        assertEquals(WorkspaceGitIssue.BRANCH_MISMATCH, health.issue)
        assertEquals("other", health.actualBranch)
        assertTrue(health.message.orEmpty().contains("feature/x"))
    }

    @Test
    fun `reader gathers healthy worktree state with three read only git processes`() {
        val repository = temporary.resolve("repository")
        val worktree = temporary.resolve("worktree")
        Files.createDirectories(repository.resolve(".git"))
        Files.createDirectories(worktree)
        val branch = "feature/fast-status"
        val invocations = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                invocations += command
                val repositoryArgument = command[command.indexOf("-C") + 1]
                val output = when {
                    "--show-toplevel" in command -> listOf(
                        worktree.toAbsolutePath().normalize(),
                        worktree.resolve(".git").toAbsolutePath().normalize(),
                        repository.resolve(".git").toAbsolutePath().normalize(),
                    ).joinToString("\n")
                    "--git-common-dir" in command -> repository.resolve(".git").toAbsolutePath().normalize().toString()
                    "--porcelain=v2" in command -> listOf(
                        "# branch.oid 0123456789012345678901234567890123456789",
                        "# branch.head $branch",
                        "# branch.upstream origin/$branch",
                        "# branch.ab +2 -0",
                        "? untracked.txt",
                        "",
                    ).joinToString("\u0000")
                    else -> error("Unexpected Git command for $repositoryArgument: ${command.joinToString(" ")}")
                }
                return CommandResult(0, output, "")
            }
        }
        val reader = GitWorkspaceGitStatusReader(GitClient(runner))
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "service",
            repositoryPath = repository.toString(),
            worktreePath = worktree.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = branch,
        )

        val health = reader.read(workspace)

        assertEquals(WorkspaceGitHealthState.READY, health.state)
        assertEquals(1, health.dirtyFileCount)
        assertEquals(
            listOf(WorkspaceGitFileChange("untracked.txt", WorkspaceGitFileChangeKind.UNTRACKED)),
            health.dirtyFiles,
        )
        assertEquals(LocalPushState.AHEAD, health.pushState)
        assertEquals(2, health.unpushedCommitCount)
        assertEquals(3, invocations.size)
        assertTrue(invocations.all { "--no-optional-locks" in it })
    }

    @Test
    fun `reader reports a source repository probe failure instead of an identity mismatch`() {
        val repository = Files.createDirectories(temporary.resolve("source-probe-repository"))
        val worktree = Files.createDirectories(temporary.resolve("source-probe-worktree"))
        val branch = "feature/source-probe"
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                val commandRepository = command[command.indexOf("-C") + 1]
                return when {
                    "--show-toplevel" in command -> CommandResult(
                        0,
                        listOf(
                            worktree.toAbsolutePath().normalize(),
                            worktree.resolve(".git").toAbsolutePath().normalize(),
                            repository.resolve(".git").toAbsolutePath().normalize(),
                        ).joinToString("\n"),
                        "",
                    )
                    commandRepository == repository.toString() && "--git-common-dir" in command ->
                        CommandResult(124, "", "injected source repository probe failure")
                    else -> error("Unexpected Git command: ${command.joinToString(" ")}")
                }
            }
        }
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "service",
            repositoryPath = repository.toString(),
            worktreePath = worktree.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = branch,
        )

        val health = GitWorkspaceGitStatusReader(GitClient(runner)).read(workspace)

        assertEquals(WorkspaceGitIssue.INSPECTION_FAILED, health.issue)
        assertTrue(health.message.orEmpty().contains("injected source repository probe failure"))
    }

    @Test
    fun `reader preserves interruption while reading the source repository identity`() {
        val repository = Files.createDirectories(temporary.resolve("cancelled-source-repository"))
        val worktree = Files.createDirectories(temporary.resolve("cancelled-source-worktree"))
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                val commandRepository = command[command.indexOf("-C") + 1]
                return when {
                    "--show-toplevel" in command -> CommandResult(
                        0,
                        listOf(
                            worktree.toAbsolutePath().normalize(),
                            worktree.resolve(".git").toAbsolutePath().normalize(),
                            repository.resolve(".git").toAbsolutePath().normalize(),
                        ).joinToString("\n"),
                        "",
                    )
                    commandRepository == repository.toString() && "--git-common-dir" in command ->
                        throw InterruptedException("injected cancellation")
                    else -> error("Unexpected Git command: ${command.joinToString(" ")}")
                }
            }
        }
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "service",
            repositoryPath = repository.toString(),
            worktreePath = worktree.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/cancelled-source-probe",
        )

        assertFailsWith<InterruptedException> {
            GitWorkspaceGitStatusReader(GitClient(runner)).read(workspace)
        }
    }

    @Test
    fun `reader accepts filesystem aliases returned by Git`() {
        val real = Files.createDirectories(temporary.resolve("alias-real"))
        val checkout = Files.createDirectories(real.resolve("checkout"))
        Files.createDirectories(checkout.resolve(".git"))
        val aliasRoot = temporary.resolve("alias-root")
        DirectoryAliasTestSupport.create(temporary, aliasRoot, real)
        val aliasedCheckout = aliasRoot.resolve("checkout")
        val branch = "feature/alias"
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult = when {
                "--show-toplevel" in command -> CommandResult(
                    0,
                    listOf(
                        checkout.toAbsolutePath().normalize(),
                        checkout.resolve(".git").toAbsolutePath().normalize(),
                        checkout.resolve(".git").toAbsolutePath().normalize(),
                    ).joinToString("\n"),
                    "",
                )
                "--get" in command -> CommandResult(0, "origin-url\n", "")
                "--porcelain=v2" in command -> CommandResult(
                    0,
                    listOf(
                        "# branch.head $branch",
                        "# branch.upstream origin/$branch",
                        "# branch.ab +0 -0",
                        "",
                    ).joinToString("\u0000"),
                    "",
                )
                else -> error("unexpected Git command: ${command.joinToString(" ")}")
            }
        }
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "service",
            repositoryPath = aliasedCheckout.toString(),
            worktreePath = aliasedCheckout.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = branch,
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            originUrl = "origin-url",
        )

        val health = GitWorkspaceGitStatusReader(GitClient(runner)).read(workspace)

        assertEquals(WorkspaceGitHealthState.READY, health.state)
        assertEquals(branch, health.actualBranch)
    }
}
