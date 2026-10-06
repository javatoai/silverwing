package com.snowball.silverwing.core

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TaskTagTargetIntegrationTest {
    @TempDir lateinit var root: Path

    @Test fun `saving changes only the chosen task module metadata and persists across reloads`() {
        val f = fixture()
        val other = f.manifest.copy(folderName = "other", taskDirectoryName = "other")
        ManifestStore().save(f.directory.parent.resolve("other"), other)
        val sibling = f.manifest.services.single().copy(moduleId = "sibling", moduleName = "Sibling")
        val original = f.manifest.copy(services = f.manifest.services + sibling)
        ManifestStore().save(f.directory, original)
        val notes = f.directory.resolve("AGENTS.md")
        Files.writeString(notes, "user owned notes")
        val refs = GitTestSupport.run(f.repository, "show-ref")
        val head = GitTestSupport.run(f.worktree, "rev-parse", "HEAD")

        val updated = f.update(" secondary/qa/B ")

        assertEquals("secondary/qa/B", updated.services.first().tagTargetRef)
        assertEquals(sibling, updated.services.last())
        assertEquals(original.services.first().copy(tagTargetRef = "secondary/qa/B"), updated.services.first())
        assertEquals(updated, ManifestStore().load(f.directory))
        assertEquals(other, ManifestStore().load(f.directory.parent.resolve("other")))
        assertEquals("2026-10-03 09:00:00", updated.updatedAt)
        assertEquals(original.schemaVersion, updated.schemaVersion)
        assertEquals(refs, GitTestSupport.run(f.repository, "show-ref"))
        assertEquals(head, GitTestSupport.run(f.worktree, "rev-parse", "HEAD"))
        assertEquals("feature/task", GitTestSupport.run(f.worktree, "branch", "--show-current"))
        assertEquals("user owned notes", Files.readString(notes))
        assertEquals("origin/release/test", f.config.groups.single().services.single().testTagBaselineRef)
    }

    @Test fun `validation failures retain metadata and only the selected remote needs to be reachable`() {
        val f = fixture()
        val original = Files.readString(f.directory.resolve(ManifestStore.FILE_NAME))
        for (value in listOf("qa/B", "origin/bad..name", "missing/qa/A", "secondary/does-not-exist", "--upload-pack/qa/A")) {
            assertFailsWith<IllegalArgumentException> { f.update(value) }
            assertEquals(original, Files.readString(f.directory.resolve(ManifestStore.FILE_NAME)))
        }
        GitTestSupport.run(f.repository, "remote", "set-url", "origin", root.resolve("offline.git").toString())
        assertFailsWith<GitException> { f.update("origin/qa/A") }
        assertEquals(original, Files.readString(f.directory.resolve(ManifestStore.FILE_NAME)))
        assertEquals("secondary/qa/B", f.update("secondary/qa/B").services.single().tagTargetRef)
        assertTrue(assertFailsWith<IllegalArgumentException> { f.update("secondary/qa/A") }.message!!.contains("已被其他操作修改"))
    }

    @Test fun `save shares the task lock with build and archive and rejects archived or current branch tasks`() {
        val f = fixture()
        FileTaskOperationLock(f.paths).withLock(f.directory) {
            assertFailsWith<IllegalStateException> { f.update("secondary/qa/B") }
            assertFailsWith<IllegalStateException> { f.builder.build(f.config, f.directory, f.key) }
            assertFailsWith<IllegalStateException> { f.application.archive(f.config, f.directory) }
        }
        ManifestStore().save(f.directory, f.manifest.copy(lifecycleStatus = TaskLifecycleStatus.ARCHIVED))
        assertTrue(assertFailsWith<IllegalArgumentException> { f.update("secondary/qa/B") }.message!!.contains("归档"))
        ManifestStore().save(f.directory, f.manifest.copy(services = listOf(f.manifest.services.single().copy(tagMode = TagBuildMode.CURRENT_BRANCH))))
        assertTrue(assertFailsWith<IllegalArgumentException> { f.update("secondary/qa/B") }.message!!.contains("当前分支"))
    }

    @Test fun `new builds and batch builds resolve the latest saved destination for each module`() {
        val f = fixture()
        f.update("secondary/qa/B")
        val first = f.builder.build(f.config, f.directory, f.key)
        assertTarget(first, "secondary", "qa/B")
        assertEquals(f.baseSha, remoteHead(f, "origin", "qa/A"))
        assertEquals(f.featureSha, remoteHead(f, "secondary", "qa/B"))
        val updated = ManifestStore().load(f.directory)
        ManifestStore().save(f.directory, updated.copy(services = updated.services + f.manifest.services.single().copy(moduleId = "sibling")))
        val batch = f.builder.buildBatch(f.config, f.directory, listOf(f.key, "service:sibling"))
        assertTarget(batch.first(), "secondary", "qa/B")
        assertTarget(batch.last(), "origin", "qa/A")
        assertEquals(batch.first().batchId, batch.last().batchId)
    }

    @Test fun `all history retries and Genbu retag keep original remote branch and mode after target changes`() {
        val f = fixture()
        f.update("secondary/qa/B")
        val states = listOf(TagOperationState.FAILED, TagOperationState.CONFLICT, TagOperationState.CREATED,
            TagOperationState.SOURCE_BRANCH_PUSHED, TagOperationState.SUCCESS)
        for (state in states) {
            val old = f.operation(state).copy(operationId = state.name,
                genbuStatus = if (state == TagOperationState.SUCCESS) GenbuTagProbeStatus(build = GenbuStageStatus.FAILED) else GenbuTagProbeStatus())
            TagOperationStore().save(f.directory, old)
            val result = when (state) {
                TagOperationState.FAILED -> f.builder.resumeFailed(f.config, f.directory, old.operationId)
                TagOperationState.CONFLICT -> f.builder.resumeConflict(f.config, f.directory, old.operationId)
                TagOperationState.SUCCESS -> f.builder.retag(f.config, f.directory, old.operationId)
                else -> f.builder.resumeInterrupted(f.config, f.directory, old.operationId)
            }
            assertTarget(result, "origin", "qa/A")
            assertEquals(old.operationId, result.operationId)
            assertEquals(old.createdAt, result.createdAt)
            assertEquals(f.baseSha, remoteHead(f, "secondary", "qa/B"))
            assertTrue(GitTestSupport.run(f.repository, "ls-remote", "secondary", "refs/tags/${result.tag}").isBlank())
        }
        // A historical CURRENT_BRANCH record stays in that mode even if the task's mode differs.
        val current = f.operation(TagOperationState.FAILED).copy(operationId = "current", tagMode = TagBuildMode.CURRENT_BRANCH, targetBranch = null)
        TagOperationStore().save(f.directory, current)
        val retried = f.builder.resumeFailed(f.config, f.directory, current.operationId)
        assertEquals(TagOperationState.SUCCESS, retried.state, retried.message)
        assertEquals(TagBuildMode.CURRENT_BRANCH, retried.tagMode)
        assertEquals("origin", retried.remote)
        assertEquals(null, retried.targetBranch)
    }

    @Test fun `partial tag push resumes to original remote after task target changes`() {
        val f = fixture()
        val hook = f.remote.resolve("hooks/pre-receive")
        Files.writeString(hook, "#!/bin/sh\nwhile read old new ref; do\ncase \"${'$'}ref\" in refs/tags/*) exit 1 ;; esac\ndone\nexit 0\n")
        hook.toFile().setExecutable(true)
        val partial = f.builder.build(f.config, f.directory, f.key)
        assertEquals(TagOperationState.PARTIAL, partial.state, partial.message)
        f.update("secondary/qa/B")
        Files.delete(hook)
        val resumed = f.builder.resumePartial(f.config, f.directory, partial.operationId)
        assertTarget(resumed, "origin", "qa/A")
        assertEquals(partial.tag, resumed.tag)
        assertTrue(GitTestSupport.run(f.repository, "ls-remote", "origin", "refs/tags/${resumed.tag}").isNotBlank())
        assertTrue(GitTestSupport.run(f.repository, "ls-remote", "secondary", "refs/tags/${resumed.tag}").isBlank())
        assertEquals(f.baseSha, remoteHead(f, "secondary", "qa/B"))
    }

    @Test fun `missing historical targets are blocked without borrowing the current destination`() {
        val f = fixture()
        f.update("secondary/qa/B")
        for (old in listOf(f.operation(TagOperationState.FAILED).copy(remote = ""), f.operation(TagOperationState.FAILED).copy(targetBranch = null))) {
            TagOperationStore().save(f.directory, old)
            val result = f.builder.resumeFailed(f.config, f.directory, old.operationId)
            assertEquals(TagOperationState.FAILED, result.state)
            assertTrue(result.message.orEmpty().contains("缺少原"), result.message)
            assertEquals(old.remote, result.remote)
            assertEquals(old.targetBranch, result.targetBranch)
        }
        val partial = f.operation(TagOperationState.PARTIAL).copy(targetBranch = null, tag = "1.0.0.beta-1", sourceSha = f.featureSha)
        TagOperationStore().save(f.directory, partial)
        assertFailsWith<IllegalArgumentException> { f.builder.resumePartial(f.config, f.directory, partial.operationId) }
        assertEquals(partial, TagOperationStore().load(f.directory, partial.operationId))
        assertEquals(f.baseSha, remoteHead(f, "origin", "qa/A"))
        assertEquals(f.baseSha, remoteHead(f, "secondary", "qa/B"))
    }

    private fun assertTarget(operation: TagOperation, remote: String, branch: String) {
        assertEquals(TagOperationState.SUCCESS, operation.state, operation.message)
        assertEquals(remote, operation.remote)
        assertEquals(branch, operation.targetBranch)
        assertEquals(TagBuildMode.MERGE_TO_TARGET_BRANCH, operation.tagMode)
    }

    private fun remoteHead(f: Fixture, remote: String, branch: String): String =
        GitTestSupport.run(f.repository, "ls-remote", remote, "refs/heads/$branch").substringBefore('\t')

    private fun fixture(): Fixture {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root.resolve("A"))
        GitTestSupport.run(seed, "branch", "qa/A")
        GitTestSupport.run(seed, "branch", "qa/B")
        GitTestSupport.run(seed, "tag", "-a", "1.0.0", "-m", "base")
        GitTestSupport.run(seed, "push", "origin", "--all")
        GitTestSupport.run(seed, "push", "origin", "--tags")
        val second = root.resolve("B.git")
        GitTestSupport.run(root, "init", "--bare", second.toString())
        GitTestSupport.run(seed, "remote", "add", "secondary", second.toString())
        GitTestSupport.run(seed, "push", "secondary", "--all")
        GitTestSupport.run(seed, "push", "secondary", "--tags")
        val repository = GitTestSupport.clone(remote, root.resolve("repository"))
        GitTestSupport.run(repository, "remote", "add", "secondary", second.toString())
        val info = GitRepositoryInspector().inspect(repository)
        val directory = root.resolve("tasks/task")
        Files.createDirectories(directory)
        val worktree = directory.resolve("service")
        GitClient().addWorktree(repository, worktree, "feature/task", "origin/master")
        GitTestSupport.configureIdentity(worktree)
        Files.writeString(worktree.resolve("feature.txt"), "feature\n")
        GitTestSupport.run(worktree, "add", "feature.txt")
        GitTestSupport.run(worktree, "commit", "-m", "feature change")
        val workspace = ServiceWorkspace(info.id, "Service", repository.toString(), worktree.toString(),
            DevelopmentToolType.INTELLIJ_IDEA, "feature/task", health = WorkspaceHealth.READY,
            groupServiceId = "service", tagEnabled = true, tagTargetRef = "origin/qa/A")
        val manifest = TaskManifest(folderName = "task", taskDirectoryName = "task", featureBranch = workspace.branch,
            createdAt = "2026-10-02 08:00:00", updatedAt = "2026-10-02 08:00:00", services = listOf(workspace))
        ManifestStore().save(directory, manifest)
        val config = AppConfig(taskRoot = directory.parent.toString(), repositories = listOf(info),
            groups = listOf(GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME, services = listOf(
                GroupServiceConfig.standard("service", info.id, "Service"),
            ))))
        val paths = ApplicationPaths(root.resolve("home"))
        return Fixture(remote, repository, worktree, directory, manifest, config, paths,
            TaskApplicationService(operationLock = FileTaskOperationLock(paths), clock = Clock.fixed(Instant.parse("2026-10-03T01:00:00Z"), ZoneOffset.UTC)),
            TagBuildService(paths = paths), GitTestSupport.run(seed, "rev-parse", "HEAD"), GitTestSupport.run(worktree, "rev-parse", "HEAD"))
    }

    private data class Fixture(val remote: Path, val repository: Path, val worktree: Path, val directory: Path,
        val manifest: TaskManifest, val config: AppConfig, val paths: ApplicationPaths,
        val application: TaskApplicationService, val builder: TagBuildService, val baseSha: String, val featureSha: String) {
        val key = manifest.services.single().selectionKey
        fun update(target: String) = application.updateTagTarget(config, directory, "service", "default", "origin/qa/A", target)
        fun operation(state: TagOperationState) = TagOperation("history", manifest.folderName, "Service", manifest.services.single().repositoryId,
            "feature/task", targetBranch = "qa/A", remote = "origin", state = state, createdAt = manifest.createdAt,
            updatedAt = manifest.updatedAt, groupServiceId = "service")
    }
}
