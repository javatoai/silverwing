package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicBoolean
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith

class WorkspaceGitOperationServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `commit and push creates missing same named remote branch`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("git-ops"))
        val checkout = GitTestSupport.clone(remote, temporary.resolve("git-ops").resolve("checkout"))
        GitTestSupport.run(checkout, "switch", "-c", "feature/ops")
        Files.writeString(checkout.resolve("change.txt"), "change\n")
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "service",
            repositoryPath = checkout.toString(),
            worktreePath = checkout.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/ops",
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            originUrl = remote.toString(),
            pushRemote = "origin",
        )

        WorkspaceGitOperationService().commitAndPush(workspace, "feat: test push")

        assertTrue(GitTestSupport.run(checkout, "ls-remote", "--heads", "origin", "refs/heads/feature/ops").contains("refs/heads/feature/ops"))
        assertEquals("origin/feature/ops", GitTestSupport.run(checkout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"))
    }

    @Test
    fun `commit and push rechecks write policy immediately before push`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("git-policy-after-commit"))
        val checkout = GitTestSupport.clone(remote, temporary.resolve("git-policy-after-commit/checkout"))
        GitTestSupport.run(checkout, "switch", "-c", "feature/protected-after-commit")
        Files.writeString(checkout.resolve("change.txt"), "change")
        val workspace = workspace(checkout, remote, "feature/protected-after-commit", "service")
        val blocked = mutableListOf<String>()
        val delegate = ProcessCommandRunner()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                val result = delegate.run(command, workingDirectory, timeout, environment)
                if (result.succeeded && "commit" in command) blocked += "feature/protected-after-commit"
                return result
            }
        }

        assertFailsWith<IllegalArgumentException> {
            WorkspaceGitOperationService(GitClient(runner)).commitAndPush(
                workspace,
                "feat: policy changes after commit",
                blockedBranches = blocked,
            )
        }

        assertTrue(GitTestSupport.run(checkout, "ls-remote", "--heads", "origin", "refs/heads/feature/protected-after-commit").isBlank())
    }
    @Test
    fun `commit template replaces requirement number and normalizes whitespace`() {
        assertEquals("feat: 7019951954 完成开发", CommitMessageTemplate.render("feat: {num}  完成开发", "https://project/detail/7019951954"))
        assertEquals("feat: 完成开发", CommitMessageTemplate.render("feat: {num} 完成开发", ""))
    }

    @Test
    fun `requirement number extraction is shared by commit templates and clipboard actions`() {
        assertEquals("7035269559", RequirementReference.number("https://project.feishu.cn/obt/userstory/detail/7035269559"))
        assertEquals("7035269559", RequirementReference.number("REQ-42 https://project/detail/7035269559?from=20260812"))
        assertEquals(null, RequirementReference.number("https://project.feishu.cn/no-number"))
    }

    @Test
    fun `push command creates same named remote branch without force`() {
        val command = WorkspacePushCommand.build("origin", "feature/task", setUpstream = true)
        assertEquals(listOf("push", "-u", "origin", "HEAD:refs/heads/feature/task"), command)
        assertFalse(command.any { it.contains("force") || it.startsWith("+") })
    }

    @Test
    fun `commit message must not be blank`() {
        val error = runCatching { CommitMessageTemplate.requireValid("  ") }.exceptionOrNull()
        assertTrue(error is IllegalArgumentException)
    }

    @Test
    fun `commit uses the shared common directory repository lock`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("git-lock"))
        val checkout = GitTestSupport.clone(remote, temporary.resolve("git-lock/checkout"))
        GitTestSupport.run(checkout, "switch", "-c", "feature/locked")
        Files.writeString(checkout.resolve("change.txt"), "change")
        val workspace = workspace(checkout, remote, "feature/locked", "locked-service")
        val lock = RepositoryOperationLock(ApplicationPaths(temporary.resolve("git-lock-home")))
        val service = WorkspaceGitOperationService(repositoryLock = lock)

        lock.withLock(GitClient().commonDirectory(checkout)) {
            assertFailsWith<IllegalStateException> {
                service.commit(workspace, "feat: must wait for repository lock")
            }
        }

        assertTrue(GitTestSupport.run(checkout, "status", "--porcelain").contains("change.txt"))
        assertEquals("1", GitTestSupport.run(checkout, "rev-list", "--count", "HEAD"))
    }

    @Test
    fun `batch commit and push skips clean commit and pushes every workspace`() {
        val (remoteA, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("batch-a"))
        val (remoteB, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("batch-b"))
        val checkoutA = GitTestSupport.clone(remoteA, temporary.resolve("batch-a").resolve("checkout"))
        val checkoutB = GitTestSupport.clone(remoteB, temporary.resolve("batch-b").resolve("checkout"))
        GitTestSupport.run(checkoutA, "switch", "-c", "feature/a")
        GitTestSupport.run(checkoutB, "switch", "-c", "feature/b")
        Files.writeString(checkoutA.resolve("change.txt"), "change\n")
        val workspaceA = workspace(checkoutA, remoteA, "feature/a", "service-a")
        val workspaceB = workspace(checkoutB, remoteB, "feature/b", "service-b")

        val result = WorkspaceGitOperationService().batch(
            listOf(workspaceA, workspaceB),
            WorkspaceGitBatchMode.COMMIT_AND_PUSH,
            mapOf(
                WorkspaceGitOperationService.workspacePathKey(workspaceA) to "feat: batch a",
                WorkspaceGitOperationService.workspacePathKey(workspaceB) to "feat: batch b",
            ),
        )

        assertEquals(WorkspaceGitStepState.SUCCESS, result.items[0].commitState)
        assertEquals(WorkspaceGitStepState.SUCCESS, result.items[0].pushState)
        assertEquals(WorkspaceGitStepState.SKIPPED, result.items[1].commitState)
        assertEquals(WorkspaceGitStepState.SUCCESS, result.items[1].pushState)
        assertTrue(GitTestSupport.run(checkoutA, "ls-remote", "--heads", "origin", "refs/heads/feature/a").contains("refs/heads/feature/a"))
        assertTrue(GitTestSupport.run(checkoutB, "ls-remote", "--heads", "origin", "refs/heads/feature/b").contains("refs/heads/feature/b"))
    }

    @Test
    fun `batch preflight failure leaves earlier workspace untouched`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("batch-preflight"))
        val checkoutA = GitTestSupport.clone(remote, temporary.resolve("batch-preflight").resolve("checkout-a"))
        val checkoutB = GitTestSupport.clone(remote, temporary.resolve("batch-preflight").resolve("checkout-b"))
        GitTestSupport.run(checkoutA, "switch", "-c", "feature/a")
        GitTestSupport.run(checkoutB, "switch", "-c", "feature/b")
        Files.writeString(checkoutA.resolve("change.txt"), "change\n")
        val workspaceA = workspace(checkoutA, remote, "feature/a", "service-a")
        val invalidB = workspace(checkoutB, remote, "feature/not-current", "service-b")
        val before = GitTestSupport.run(checkoutA, "rev-list", "--count", "HEAD")

        assertFailsWith<IllegalArgumentException> {
            WorkspaceGitOperationService().batch(
                listOf(workspaceA, invalidB),
                WorkspaceGitBatchMode.COMMIT,
                mapOf(WorkspaceGitOperationService.workspacePathKey(workspaceA) to "feat: should not commit"),
            )
        }

        assertEquals(before, GitTestSupport.run(checkoutA, "rev-list", "--count", "HEAD"))
        assertTrue(Files.exists(checkoutA.resolve("change.txt")))
        assertTrue(GitTestSupport.run(checkoutA, "status", "--porcelain").isNotBlank())
    }

    @Test
    fun `preview lists files and stale fingerprint blocks the first write`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("preview"))
        val checkout = GitTestSupport.clone(remote, temporary.resolve("preview/checkout"))
        GitTestSupport.run(checkout, "switch", "-c", "feature/preview")
        val workspace = workspace(checkout, remote, "feature/preview", "preview-service")
        Files.writeString(checkout.resolve("first.txt"), "first")
        val service = WorkspaceGitOperationService()
        val preview = service.preview(workspace)
        assertTrue(preview.files.any { it.contains("first.txt") })

        Files.writeString(checkout.resolve("second.txt"), "second")
        assertFailsWith<IllegalArgumentException> {
            service.batch(
                listOf(workspace),
                WorkspaceGitBatchMode.COMMIT,
                mapOf(WorkspaceGitOperationService.workspacePathKey(workspace) to "feat: preview"),
                mapOf(WorkspaceGitOperationService.workspacePathKey(workspace) to preview.fingerprint),
            )
        }

        assertTrue(GitTestSupport.run(checkout, "status", "--porcelain").contains("first.txt"))
        assertEquals("1", GitTestSupport.run(checkout, "rev-list", "--count", "HEAD"))
    }

    @Test
    fun `batch rechecks each fingerprint immediately before its first write`() {
        val fixture = sharedRepositoryWorkspaces("batch-race")
        Files.writeString(fixture.worktreeA.resolve("planned.txt"), "planned a")
        Files.writeString(fixture.worktreeB.resolve("planned.txt"), "planned b")
        val delegate = ProcessCommandRunner()
        val changedSecondWorkspace = AtomicBoolean()
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                if (
                    "commit" in command && fixture.worktreeA.toString() in command &&
                    changedSecondWorkspace.compareAndSet(false, true)
                ) {
                    Files.writeString(fixture.worktreeB.resolve("late.txt"), "arrived after batch confirmation")
                }
                return delegate.run(command, workingDirectory, timeout, environment)
            }
        }
        val service = WorkspaceGitOperationService(
            git = GitClient(runner),
            repositoryLock = RepositoryOperationLock(ApplicationPaths(temporary.resolve("batch-race-home"))),
        )
        val previewA = service.preview(fixture.workspaceA)
        val previewB = service.preview(fixture.workspaceB)

        val result = service.batch(
            listOf(fixture.workspaceA, fixture.workspaceB),
            WorkspaceGitBatchMode.COMMIT,
            commitMessages = mapOf(
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceA) to "feat: planned a",
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceB) to "feat: planned b",
            ),
            expectedFingerprints = mapOf(
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceA) to previewA.fingerprint,
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceB) to previewB.fingerprint,
            ),
        )

        assertEquals(WorkspaceGitStepState.SUCCESS, result.items[0].commitState)
        assertEquals(WorkspaceGitStepState.FAILED, result.items[1].commitState)
        assertTrue(result.items[1].message.contains("状态已变化"))
        assertEquals("1", GitTestSupport.run(fixture.worktreeB, "rev-list", "--count", "HEAD"))
        assertTrue(GitTestSupport.run(fixture.worktreeB, "status", "--porcelain").contains("late.txt"))
    }

    @Test
    fun `batch rechecks write policy inside repository lock before writing`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("batch-policy-race"))
        val checkout = GitTestSupport.clone(remote, temporary.resolve("batch-policy-race/checkout"))
        GitTestSupport.run(checkout, "switch", "-c", "feature/protected")
        Files.writeString(checkout.resolve("change.txt"), "change")
        val workspace = workspace(checkout, remote, "feature/protected", "service")
        val iterations = AtomicBoolean()
        val policyChangesAfterPreflight = object : AbstractCollection<String>() {
            override val size: Int get() = if (iterations.get()) 1 else 0
            override fun iterator(): Iterator<String> =
                if (iterations.compareAndSet(false, true)) emptyList<String>().iterator()
                else listOf("feature/protected").iterator()
        }

        val result = WorkspaceGitOperationService().batch(
            listOf(workspace),
            WorkspaceGitBatchMode.COMMIT,
            commitMessages = mapOf(WorkspaceGitOperationService.workspacePathKey(workspace) to "feat: blocked"),
            blockedBranches = policyChangesAfterPreflight,
        )

        assertEquals(WorkspaceGitStepState.FAILED, result.items.single().commitState)
        assertTrue(result.items.single().message.contains("Git 写保护"))
        assertEquals("1", GitTestSupport.run(checkout, "rev-list", "--count", "HEAD"))
        assertTrue(GitTestSupport.run(checkout, "status", "--porcelain").contains("change.txt"))
    }

    @Test
    fun `batch push refuses a head changed after confirmation`() {
        val fixture = sharedRepositoryWorkspaces("batch-push-race")
        val delegate = ProcessCommandRunner()
        val changedSecond = AtomicBoolean()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                if ("push" in command && fixture.worktreeA.toString() in command && changedSecond.compareAndSet(false, true)) {
                    Files.writeString(fixture.worktreeB.resolve("late.txt"), "late")
                    GitTestSupport.run(fixture.worktreeB, "add", "late.txt")
                    GitTestSupport.run(fixture.worktreeB, "commit", "-m", "late external commit")
                }
                return delegate.run(command, workingDirectory, timeout, environment)
            }
        }
        val service = WorkspaceGitOperationService(
            git = GitClient(runner),
            repositoryLock = RepositoryOperationLock(ApplicationPaths(temporary.resolve("batch-push-race-home"))),
        )
        val previewA = service.preview(fixture.workspaceA)
        val previewB = service.preview(fixture.workspaceB)

        val result = service.batch(
            listOf(fixture.workspaceA, fixture.workspaceB),
            WorkspaceGitBatchMode.PUSH,
            expectedFingerprints = mapOf(
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceA) to previewA.fingerprint,
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceB) to previewB.fingerprint,
            ),
        )

        assertEquals(WorkspaceGitStepState.FAILED, result.items[1].pushState)
        assertTrue(result.items[1].message.contains("状态已变化"))
        assertTrue(GitTestSupport.run(fixture.main, "ls-remote", "--heads", "origin", "refs/heads/feature/b").isBlank())
    }

    @Test
    fun `batch commit and push rechecks a clean workspace before push`() {
        val fixture = sharedRepositoryWorkspaces("batch-clean-cp-race")
        val delegate = ProcessCommandRunner()
        val changedSecond = AtomicBoolean()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                if ("push" in command && fixture.worktreeA.toString() in command && changedSecond.compareAndSet(false, true)) {
                    Files.writeString(fixture.worktreeB.resolve("late.txt"), "late")
                    GitTestSupport.run(fixture.worktreeB, "add", "late.txt")
                    GitTestSupport.run(fixture.worktreeB, "commit", "-m", "late external commit")
                }
                return delegate.run(command, workingDirectory, timeout, environment)
            }
        }
        val service = WorkspaceGitOperationService(
            git = GitClient(runner),
            repositoryLock = RepositoryOperationLock(ApplicationPaths(temporary.resolve("batch-clean-cp-race-home"))),
        )
        val previewA = service.preview(fixture.workspaceA)
        val previewB = service.preview(fixture.workspaceB)

        val result = service.batch(
            listOf(fixture.workspaceA, fixture.workspaceB),
            WorkspaceGitBatchMode.COMMIT_AND_PUSH,
            expectedFingerprints = mapOf(
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceA) to previewA.fingerprint,
                WorkspaceGitOperationService.workspacePathKey(fixture.workspaceB) to previewB.fingerprint,
            ),
        )

        assertEquals(WorkspaceGitStepState.SKIPPED, result.items[1].commitState)
        assertEquals(WorkspaceGitStepState.FAILED, result.items[1].pushState)
        assertTrue(result.items[1].message.contains("状态已变化"))
        assertTrue(GitTestSupport.run(fixture.main, "ls-remote", "--heads", "origin", "refs/heads/feature/b").isBlank())
    }

    private class SharedRepositoryWorkspaces(
        val main: Path,
        val worktreeA: Path,
        val worktreeB: Path,
        val workspaceA: ServiceWorkspace,
        val workspaceB: ServiceWorkspace,
    )

    /** Two worktrees of one repository, so batch execution serializes them through the shared lock. */
    private fun sharedRepositoryWorkspaces(name: String): SharedRepositoryWorkspaces {
        val root = temporary.resolve(name)
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root)
        val main = GitTestSupport.clone(remote, root.resolve("main"))
        GitTestSupport.run(main, "branch", "feature/a")
        GitTestSupport.run(main, "branch", "feature/b")
        val worktreeA = root.resolve("worktree-a")
        val worktreeB = root.resolve("worktree-b")
        GitTestSupport.run(main, "worktree", "add", worktreeA.toString(), "feature/a")
        GitTestSupport.run(main, "worktree", "add", worktreeB.toString(), "feature/b")
        return SharedRepositoryWorkspaces(
            main = main,
            worktreeA = worktreeA,
            worktreeB = worktreeB,
            workspaceA = worktree(main, worktreeA, "feature/a", "service-a"),
            workspaceB = worktree(main, worktreeB, "feature/b", "service-b"),
        )
    }

    private fun worktree(repository: Path, worktreePath: Path, branch: String, service: String) = ServiceWorkspace(
        repositoryId = service,
        serviceName = service,
        repositoryPath = repository.toString(),
        worktreePath = worktreePath.toString(),
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = branch,
        strategy = WorkspaceStrategy.STANDARD_WORKTREE,
        pushRemote = "origin",
    )

    private fun workspace(path: Path, remote: Path, branch: String, service: String) = ServiceWorkspace(
        repositoryId = service,
        serviceName = service,
        repositoryPath = path.toString(),
        worktreePath = path.toString(),
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = branch,
        strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
        originUrl = remote.toString(),
        pushRemote = "origin",
    )
}
