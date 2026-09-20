package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Regressions for candidate collisions before creation and during partial recovery. */
class TagCandidateCollisionRecoveryTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `local-only candidate is pruned before the next tag is created`() {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("git"))
        GitTestSupport.run(seed, "branch", "release/test")
        GitTestSupport.run(seed, "push", "origin", "release/test")
        GitTestSupport.run(seed, "tag", "-a", "1.0.0.beta-0", "-m", "base tag")
        GitTestSupport.run(seed, "push", "origin", "--tags")

        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("repository"))
        GitTestSupport.run(repositoryPath, "checkout", "-b", "unrelated-tag-branch")
        Files.writeString(repositoryPath.resolve("unrelated.txt"), "different history")
        GitTestSupport.run(repositoryPath, "add", "unrelated.txt")
        GitTestSupport.run(repositoryPath, "commit", "-m", "unrelated")
        GitTestSupport.run(repositoryPath, "tag", "-a", "1.0.0.beta-1", "-m", "collision on unrelated history")
        GitTestSupport.run(repositoryPath, "checkout", "master")

        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val taskRoot = temporary.resolve("tasks")
        val taskDirectory = Files.createDirectories(taskRoot.resolve("task-review"))
        val workspacePath = taskDirectory.resolve("service")
        val git = GitClient()
        git.addWorktree(repositoryPath, workspacePath, "feature/review", "origin/master")
        GitTestSupport.configureIdentity(workspacePath)
        Files.writeString(workspacePath.resolve("feature.txt"), "feature change")
        GitTestSupport.run(workspacePath, "add", "feature.txt")
        GitTestSupport.run(workspacePath, "commit", "-m", "feature")

        val workspace = ServiceWorkspace(
            repositoryId = repository.id,
            serviceName = "service",
            repositoryPath = repositoryPath.toString(),
            worktreePath = workspacePath.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/review",
            health = WorkspaceHealth.READY,
            groupServiceId = "service",
            tagEnabled = true,
            tagTargetRef = "origin/release/test",
        )
        val manifest = TaskManifest(
            folderName = "task-review",
            taskDirectoryName = "task-review",
            featureBranch = "feature/review",
            createdAt = "2026-09-08 12:00:00",
            updatedAt = "2026-09-08 12:00:00",
            services = listOf(workspace),
            groupId = DEFAULT_GROUP_ID,
        )
        ManifestStore().save(taskDirectory, manifest)
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(
                GroupConfig(
                    DEFAULT_GROUP_ID,
                    DEFAULT_GROUP_NAME,
                    services = listOf(GroupServiceConfig.standard("service", repository.id, "service")),
                ),
            ),
        )
        val builder = TagBuildService(paths = ApplicationPaths(temporary.resolve("home")))

        val preflight = builder.preflight(config, taskDirectory, repository.id)
        assertEquals("1.0.0.beta-1", preflight.estimatedTag)
        val operation = builder.build(config, taskDirectory, repository.id)

        assertEquals(TagOperationState.SUCCESS, operation.state, operation.message)
        assertEquals("1.0.0.beta-1", operation.tag)
        assertTrue(operation.targetSha != null)
        assertTrue(
            GitTestSupport.run(repositoryPath, "ls-remote", "origin", "refs/heads/release/test")
                .substringBefore('\t')
                .let { GitClient().isAncestor(repositoryPath, operation.sourceSha!!, it) },
        )

        assertEquals(
            operation.targetSha,
            GitTestSupport.run(repositoryPath, "rev-parse", "refs/tags/1.0.0.beta-1^{}").trim(),
        )
        assertEquals(
            operation.targetSha,
            GitTestSupport.run(repositoryPath, "ls-remote", "origin", "refs/tags/1.0.0.beta-1^{}")
                .substringBefore('\t')
                .trim(),
        )
    }

    @Test
    fun `diverged current branch tasks skip an occupied remote tag without changing it`() {
        val fixture = currentBranchFixture()
        val first = fixture.builder.build(fixture.config, fixture.firstTask, fixture.repository.id)
        assertEquals(TagOperationState.SUCCESS, first.state, first.message)
        assertEquals("1.0.0.beta-1", first.tag)
        val firstTagObject = GitTestSupport.run(fixture.repositoryPath, "rev-parse", "refs/tags/${first.tag}")

        val preview = fixture.builder.preflight(fixture.config, fixture.secondTask, fixture.repository.id)
        assertEquals("1.0.0.beta-1", preview.estimatedTag)
        val second = fixture.builder.build(fixture.config, fixture.secondTask, fixture.repository.id)

        assertEquals(TagOperationState.SUCCESS, second.state, second.message)
        assertEquals("1.0.0.beta-2", second.tag)
        assertEquals(second.sourceSha, GitClient().resolve(fixture.repositoryPath, "refs/tags/${second.tag}"))
        assertTagUnchanged(fixture, requireNotNull(first.tag), firstTagObject)
        assertEquals(
            second.sourceSha,
            GitTestSupport.run(fixture.repositoryPath, "ls-remote", "origin", "refs/tags/${second.tag}^{}")
                .substringBefore('\t'),
        )
    }

    @Test
    fun `partial recovery skips a candidate taken by the other current branch task`() {
        val fixture = currentBranchFixture()
        val first = fixture.builder.build(fixture.config, fixture.firstTask, fixture.repository.id)
        assertEquals(TagOperationState.SUCCESS, first.state, first.message)
        val hook = fixture.remote.resolve("hooks").resolve("pre-receive")
        Files.writeString(
            hook,
            """
            |#!/bin/sh
            |while read old new ref
            |do
            |  case "${'$'}ref" in
            |    refs/tags/*) exit 1 ;;
            |  esac
            |done
            |exit 0
            |
            """.trimMargin(),
        )
        hook.toFile().setExecutable(true)
        val partial = try {
            fixture.builder.build(fixture.config, fixture.secondTask, fixture.repository.id)
        } finally {
            Files.delete(hook)
        }
        assertEquals(TagOperationState.PARTIAL, partial.state, partial.message)
        assertEquals("1.0.0.beta-2", partial.tag)

        val occupied = fixture.builder.build(fixture.config, fixture.firstTask, fixture.repository.id)
        assertEquals(TagOperationState.SUCCESS, occupied.state, occupied.message)
        assertEquals(partial.tag, occupied.tag)
        val occupiedTagObject = GitTestSupport.run(fixture.repositoryPath, "rev-parse", "refs/tags/${occupied.tag}")
        val resumed = fixture.builder.resumePartial(fixture.config, fixture.secondTask, partial.operationId)

        assertEquals(TagOperationState.SUCCESS, resumed.state, resumed.message)
        assertEquals("1.0.0.beta-3", resumed.tag)
        assertEquals(partial.operationId, resumed.operationId)
        assertEquals(partial.createdAt, resumed.createdAt)
        assertEquals(partial.sourceSha, resumed.sourceSha)
        assertEquals(resumed, TagOperationStore().list(fixture.secondTask).single())
        assertTagUnchanged(fixture, requireNotNull(occupied.tag), occupiedTagObject)
        assertEquals(
            resumed.sourceSha,
            GitTestSupport.run(fixture.repositoryPath, "ls-remote", "origin", "refs/tags/${resumed.tag}^{}")
                .substringBefore('\t'),
        )
    }

    private fun assertTagUnchanged(fixture: CurrentBranchFixture, tag: String, expectedObject: String) {
        assertEquals(expectedObject, GitTestSupport.run(fixture.repositoryPath, "rev-parse", "refs/tags/$tag"))
        assertEquals(
            expectedObject,
            GitTestSupport.run(fixture.repositoryPath, "ls-remote", "origin", "refs/tags/$tag")
                .substringBefore('\t'),
        )
    }

    private fun currentBranchFixture(): CurrentBranchFixture {
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(temporary.resolve("current-branch-git"))
        GitTestSupport.run(seed, "tag", "-a", "1.0.0.beta-0", "-m", "common baseline")
        GitTestSupport.run(seed, "push", "origin", "--tags")
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("current-branch-repository"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val taskRoot = Files.createDirectories(temporary.resolve("current-branch-tasks"))
        val tasks = listOf("task-a", "task-b").map { name ->
            val task = Files.createDirectories(taskRoot.resolve(name))
            val worktree = task.resolve("service")
            val branch = "feature/$name"
            GitClient().addWorktree(repositoryPath, worktree, branch, "origin/master")
            GitTestSupport.configureIdentity(worktree)
            Files.writeString(worktree.resolve("$name.txt"), name)
            GitTestSupport.run(worktree, "add", "$name.txt")
            GitTestSupport.run(worktree, "commit", "-m", name)
            val workspace = ServiceWorkspace(
                repositoryId = repository.id,
                serviceName = "service",
                repositoryPath = repositoryPath.toString(),
                worktreePath = worktree.toString(),
                developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                branch = branch,
                health = WorkspaceHealth.READY,
                groupServiceId = "service",
                tagEnabled = true,
                tagMode = TagBuildMode.CURRENT_BRANCH,
                tagTargetRef = null,
            )
            ManifestStore().save(
                task,
                TaskManifest(
                    folderName = name,
                    taskDirectoryName = name,
                    featureBranch = branch,
                    createdAt = "2026-09-12 12:00:00",
                    updatedAt = "2026-09-12 12:00:00",
                    services = listOf(workspace),
                ),
            )
            task
        }
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(
                GroupConfig(
                    DEFAULT_GROUP_ID,
                    DEFAULT_GROUP_NAME,
                    services = listOf(
                        GroupServiceConfig.standard("service", repository.id, "service").copy(
                            modules = listOf(
                                ServiceModuleConfig("default", tagMode = TagBuildMode.CURRENT_BRANCH, tagTargetRef = null),
                            ),
                        ),
                    ),
                ),
            ),
        )
        return CurrentBranchFixture(
            remote,
            repositoryPath,
            repository,
            tasks[0],
            tasks[1],
            config,
            TagBuildService(paths = ApplicationPaths(temporary.resolve("current-branch-home"))),
        )
    }

    private data class CurrentBranchFixture(
        val remote: Path,
        val repositoryPath: Path,
        val repository: RepositoryConfig,
        val firstTask: Path,
        val secondTask: Path,
        val config: AppConfig,
        val builder: TagBuildService,
    )
}
