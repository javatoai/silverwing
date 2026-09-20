package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Instant

class TagConflictIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `reports conflicts and resumes same operation after manual resolution`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("source"))
        GitTestSupport.run(seed, "branch", "release/test")
        GitTestSupport.run(seed, "push", "origin", "release/test")
        createAnnotatedTag(seed, "1.0.0.beta-0", "2025-01-01T00:00:00Z")
        GitTestSupport.run(seed, "push", "origin", "--tags")
        val repository = GitTestSupport.clone(remote, temporary.resolve("services").resolve("conflict-service"))
        val repositoryInfo = GitRepositoryInspector().inspect(repository)

        val featureWorktree = temporary.resolve("tasks").resolve("CONFLICT-1")
            .resolve("conflict-service")
        Files.createDirectories(featureWorktree.parent)
        GitClient().addWorktree(repository, featureWorktree, "feature/CONFLICT-1", "origin/master")
        GitTestSupport.configureIdentity(featureWorktree)
        Files.writeString(featureWorktree.resolve("README.md"), "feature version\n")
        GitTestSupport.run(featureWorktree, "add", "README.md")
        GitTestSupport.run(featureWorktree, "commit", "-m", "feature edit")
        val featureSha = GitTestSupport.run(featureWorktree, "rev-parse", "HEAD")

        GitTestSupport.run(repository, "switch", "release/test")
        Files.writeString(repository.resolve("README.md"), "test version\n")
        GitTestSupport.run(repository, "add", "README.md")
        GitTestSupport.run(repository, "commit", "-m", "test edit")
        GitTestSupport.run(repository, "push", "origin", "release/test")
        GitTestSupport.run(repository, "switch", "master")

        val taskDirectory = temporary.resolve("tasks").resolve("CONFLICT-1")
        val now = Instant.now().toString()
        ManifestStore().save(
            taskDirectory,
            TaskManifest(
                folderName = "CONFLICT-1",
                taskDirectoryName = "CONFLICT-1",
                featureBranch = "feature/CONFLICT-1",
                createdAt = now,
                updatedAt = now,
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = listOf(
                    ServiceWorkspace(
                        repositoryId = repositoryInfo.id,
                        serviceName = "conflict-service",
                        repositoryPath = repository.toString(),
                        worktreePath = featureWorktree.toString(),
                        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                        branch = "feature/CONFLICT-1",
                        health = WorkspaceHealth.READY,
                        groupServiceId = "conflict-service",
                        tagEnabled = true,
                        tagTargetRef = "origin/release/test",
                    ),
                ),
            ),
        )
        val config = AppConfig(
            taskRoot = temporary.resolve("tasks").toString(),
            repositories = listOf(repositoryInfo),
            groups = listOf(
                GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME, services = listOf(
                    GroupServiceConfig.standard(
                        id = "conflict-service",
                        repositoryId = repositoryInfo.id,
                        displayName = "conflict-service",
                    ).copy(modules = listOf(ServiceModuleConfig("default"))),
                )),
            ),
        )
        val builder = TagBuildService(
            paths = ApplicationPaths(temporary.resolve("app-home")),
        )

        val error = assertThrows(MergeConflictException::class.java) {
            builder.preflight(config, taskDirectory, repositoryInfo.id)
        }

        assertEquals(listOf("README.md"), error.files)
        assertEquals(featureSha, GitTestSupport.run(featureWorktree, "rev-parse", "HEAD"))
        assertEquals("feature version", Files.readString(featureWorktree.resolve("README.md")).trim())
        assertTrue(GitClient().worktrees(repository).none { it.path.startsWith(temporary.resolve("app-home")) })

        val conflicted = builder.buildBatch(config, taskDirectory, listOf(repositoryInfo.id)).single()
        assertEquals(TagOperationState.CONFLICT, conflicted.state)
        assertEquals(listOf("README.md"), conflicted.conflictFiles)

        val stillConflicted = builder.resumeConflict(config, taskDirectory, conflicted.operationId)
        assertEquals(TagOperationState.CONFLICT, stillConflicted.state)
        assertEquals(conflicted.operationId, stillConflicted.operationId)
        assertEquals(conflicted.batchId, stillConflicted.batchId)
        assertEquals(conflicted.createdAt, stillConflicted.createdAt)
        assertEquals(listOf("README.md"), stillConflicted.conflictFiles)

        val merge = GitClient().run(
            featureWorktree,
            "merge",
            "--no-edit",
            "origin/release/test",
            check = false,
        )
        assertTrue(!merge.succeeded)
        Files.writeString(featureWorktree.resolve("README.md"), "resolved version\n")
        GitTestSupport.run(featureWorktree, "add", "README.md")
        GitTestSupport.run(featureWorktree, "commit", "-m", "resolve tag conflict")
        GitTestSupport.run(featureWorktree, "push", "origin", "feature/CONFLICT-1")

        val resumed = builder.resumeConflict(config, taskDirectory, conflicted.operationId)
        assertEquals(TagOperationState.SUCCESS, resumed.state, resumed.message)
        assertEquals(conflicted.operationId, resumed.operationId)
        assertEquals(conflicted.batchId, resumed.batchId)
        assertEquals(conflicted.createdAt, resumed.createdAt)
        assertTrue(resumed.conflictFiles.isEmpty())
        assertEquals("1.0.0.beta-1", resumed.tag)
        assertEquals(
            resumed.operationId,
            TagOperationStore().load(taskDirectory, resumed.operationId).operationId,
        )
        assertTrue(
            GitTestSupport.run(repository, "ls-remote", "origin", "refs/tags/1.0.0.beta-1")
                .isNotBlank(),
        )
    }

    private fun createAnnotatedTag(repository: Path, tag: String, date: String) {
        val result = ProcessCommandRunner().run(
            command = listOf("git", "tag", "-a", tag, "-m", tag),
            workingDirectory = repository,
            environment = mapOf("GIT_COMMITTER_DATE" to date),
        )
        assertEquals(0, result.exitCode, result.stderr)
    }
}
