package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith

class WorkspaceLifecycleIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `same-base modules keep independent physical worktrees through archive restore and delete`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("shared"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("shared").resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val serviceConfig = GroupServiceConfig(
            id = "shared-service",
            repositoryId = repository.id,
            displayName = "shared",
            modules = listOf(
                ServiceModuleConfig("api", "api", "origin/master"),
                ServiceModuleConfig("web", "web", "origin/master"),
            ),
        )
        val taskRoot = temporary.resolve("tasks")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("shared", "Shared", services = listOf(serviceConfig))),
        )
        val application = TaskApplicationService(
            agentDocuments = AgentDocumentService(ApplicationPaths(temporary.resolve("home"))),
            operationLock = NoOpTaskOperationLock,
        )

        val created = application.create(
            config,
            CreateGroupedTaskRequest("T-1", "feature/T-1", "shared", listOf("shared-service")),
        )
        assertEquals(2, created.services.size)
        assertEquals(2, created.services.map { it.worktreePath }.distinct().size)
        assertEquals(3, GitClient().worktrees(repositoryPath).size)

        val taskDirectory = taskRoot.resolve(created.taskDirectoryName)
        val archived = application.archive(config, taskDirectory)
        assertEquals(TaskLifecycleStatus.ARCHIVED, archived.lifecycleStatus)
        assertTrue(created.services.all { Files.exists(Path.of(it.worktreePath)) })
        assertEquals(3, GitClient().worktrees(repositoryPath).size)

        val restored = application.restore(config, taskDirectory)
        assertEquals(TaskLifecycleStatus.ACTIVE, restored.lifecycleStatus)
        assertTrue(restored.services.all { it.health == WorkspaceHealth.READY })
        assertEquals(3, GitClient().worktrees(repositoryPath).size)

        application.delete(config, taskDirectory)
        assertFalse(Files.exists(taskDirectory))
        assertEquals(1, GitClient().worktrees(repositoryPath).size)
    }

    @Test
    fun `independent clone requires explicit discard for commits on any local branch`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone-risk"))
        val source = GitTestSupport.clone(remote, temporary.resolve("clone-risk").resolve("source"))
        val repository = GitRepositoryInspector().inspect(source)
        val cloneService = GroupServiceConfig(
            id = "clone-risk",
            repositoryId = repository.id,
            displayName = "clone-risk",
            modules = listOf(ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, baseRef = "origin/master")),
        )
        val taskRoot = temporary.resolve("clone-risk-tasks")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("clone-risk", "Clone Risk", services = listOf(cloneService))),
        )
        val application = TaskApplicationService(
            agentDocuments = AgentDocumentService(ApplicationPaths(temporary.resolve("clone-risk-home"))),
            operationLock = NoOpTaskOperationLock,
        )
        val created = application.create(
            config,
            CreateGroupedTaskRequest("CLONE-1", "unused", "clone-risk", listOf("clone-risk")),
        )
        val taskDirectory = taskRoot.resolve(created.taskDirectoryName)
        val clone = Path.of(created.services.single().worktreePath)
        GitTestSupport.configureIdentity(clone)
        GitTestSupport.run(clone, "switch", "-c", "local-only")
        Files.writeString(clone.resolve("local.txt"), "local only\n")
        GitTestSupport.run(clone, "add", "local.txt")
        GitTestSupport.run(clone, "commit", "-m", "local only")

        val risks = application.inspectDeleteRisk(config, taskDirectory)
        assertTrue(risks.single().unpushedCommits > 0)
        assertFailsWith<IllegalStateException> { application.delete(config, taskDirectory) }
        assertTrue(Files.exists(clone))

        application.delete(config, taskDirectory, forceDiscard = true)
        assertFalse(Files.exists(taskDirectory))
    }

    @Test
    fun `independent clone can be deleted when its configured source checkout is offline`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone-offline-source"))
        val source = GitTestSupport.clone(remote, temporary.resolve("clone-offline-source/source"))
        val repository = GitRepositoryInspector().inspect(source)
        val service = GroupServiceConfig(
            id = "offline-clone",
            repositoryId = repository.id,
            displayName = "offline-clone",
            modules = listOf(
                ServiceModuleConfig(
                    "clone",
                    strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                    baseRef = "origin/master",
                ),
            ),
        )
        val taskRoot = temporary.resolve("clone-offline-tasks")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("offline", "Offline", services = listOf(service))),
        )
        val application = TaskApplicationService(operationLock = NoOpTaskOperationLock)
        val created = application.create(
            config,
            CreateGroupedTaskRequest("OFFLINE", "feature/offline", "offline", listOf("offline-clone")),
        )
        val taskDirectory = taskRoot.resolve(created.taskDirectoryName)

        Files.move(source, source.resolveSibling("source-offline"))
        application.delete(config, taskDirectory)

        assertFalse(Files.exists(taskDirectory))
    }

    @Test
    fun `independent clone deletion ignores submodule repositories stored under git metadata`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone-submodule-metadata"))
        val source = GitTestSupport.clone(remote, temporary.resolve("clone-submodule-metadata/source"))
        val repository = GitRepositoryInspector().inspect(source)
        val service = GroupServiceConfig(
            id = "submodule-clone",
            repositoryId = repository.id,
            displayName = "submodule-clone",
            modules = listOf(
                ServiceModuleConfig(
                    "clone",
                    strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                    baseRef = "origin/master",
                ),
            ),
        )
        val taskRoot = temporary.resolve("clone-submodule-metadata-tasks")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("submodule", "Submodule", services = listOf(service))),
        )
        val application = TaskApplicationService(operationLock = NoOpTaskOperationLock)
        val created = application.create(
            config,
            CreateGroupedTaskRequest("SUBMODULE", "feature/submodule", "submodule", listOf("submodule-clone")),
        )
        val taskDirectory = taskRoot.resolve(created.taskDirectoryName)
        val clone = Path.of(created.services.single().worktreePath)
        val gitDirectory = Path.of(GitTestSupport.run(clone, "rev-parse", "--absolute-git-dir").trim())
        val submoduleMetadata = gitDirectory.resolve("modules/example")
        Files.createDirectories(submoduleMetadata.resolve("objects"))
        Files.createDirectories(submoduleMetadata.resolve("refs"))
        Files.writeString(submoduleMetadata.resolve("HEAD"), "ref: refs/heads/master\n")

        application.delete(config, taskDirectory, forceDiscard = true)

        assertFalse(Files.exists(taskDirectory))
    }

    @Test
    fun `force deletion still refuses a git bisect in progress`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone-operation"))
        val source = GitTestSupport.clone(remote, temporary.resolve("clone-operation/source"))
        val repository = GitRepositoryInspector().inspect(source)
        val service = GroupServiceConfig(
            id = "operation-clone",
            repositoryId = repository.id,
            displayName = "operation-clone",
            modules = listOf(
                ServiceModuleConfig(
                    "clone",
                    strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                    baseRef = "origin/master",
                ),
            ),
        )
        val taskRoot = temporary.resolve("clone-operation-tasks")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("operation", "Operation", services = listOf(service))),
        )
        val application = TaskApplicationService(operationLock = NoOpTaskOperationLock)
        val created = application.create(
            config,
            CreateGroupedTaskRequest("OPERATION", "feature/operation", "operation", listOf("operation-clone")),
        )
        val taskDirectory = taskRoot.resolve(created.taskDirectoryName)
        val clone = Path.of(created.services.single().worktreePath)
        val gitDirectory = Path.of(GitTestSupport.run(clone, "rev-parse", "--absolute-git-dir").trim())
        Files.writeString(gitDirectory.resolve("BISECT_LOG"), "in-progress")

        assertFailsWith<IllegalArgumentException> {
            application.delete(config, taskDirectory, forceDiscard = true)
        }

        assertTrue(Files.exists(taskDirectory))
        assertTrue(Files.exists(clone))
    }

    @Test
    fun `task deletion refuses a registered worktree backup missing from manifest`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("untracked-worktree"))
        val source = GitTestSupport.clone(remote, temporary.resolve("untracked-worktree/source"))
        val repository = GitRepositoryInspector().inspect(source)
        val taskRoot = temporary.resolve("untracked-worktree-tasks")
        val taskDirectory = taskRoot.resolve("task")
        Files.createDirectories(taskDirectory)
        val recorded = taskDirectory.resolve("service-recorded")
        val retained = taskDirectory.resolve("service-retained-backup")
        GitClient().addWorktree(source, recorded, "feature/recorded", "origin/master")
        GitClient().addWorktree(source, retained, "feature/retained", "origin/master")
        val workspace = ServiceWorkspace(
            repositoryId = repository.id,
            serviceName = "Service",
            repositoryPath = source.toString(),
            worktreePath = recorded.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/recorded",
            groupServiceId = "service",
            moduleId = "recorded",
            moduleName = "recorded",
            baseRef = "origin/master",
            targetBranch = "feature/recorded",
        )
        ManifestStore().save(
            taskDirectory,
            TaskManifest(
                folderName = "task",
                taskDirectoryName = "task",
                featureBranch = "feature/task",
                createdAt = "2026-08-14 00:00:00",
                updatedAt = "2026-08-14 00:00:00",
                services = listOf(workspace),
            ),
        )
        val service = GroupServiceConfig(
            id = "service",
            repositoryId = repository.id,
            displayName = "Service",
            modules = listOf(ServiceModuleConfig("recorded")),
        )
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME, services = listOf(service))),
        )

        assertFailsWith<IllegalArgumentException> {
            TaskApplicationService(operationLock = NoOpTaskOperationLock)
                .delete(config, taskDirectory, forceDiscard = true)
        }

        assertTrue(Files.exists(recorded))
        assertTrue(Files.exists(retained))
        assertTrue(GitClient().worktrees(source).any { it.path.toAbsolutePath().normalize() == retained })
    }

    @Test
    fun `task deletion refuses an independently cloned directory without silverwing ownership`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("clone-owner"))
        val source = GitTestSupport.clone(remote, temporary.resolve("clone-owner/source"))
        val repository = GitRepositoryInspector().inspect(source)
        val serviceConfig = GroupServiceConfig(
            id = "clone-owner",
            repositoryId = repository.id,
            displayName = "clone-owner",
            modules = listOf(ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, baseRef = "origin/master")),
        )
        val taskRoot = temporary.resolve("clone-owner-tasks")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("clone-owner", "Clone Owner", services = listOf(serviceConfig))),
        )
        val application = TaskApplicationService(operationLock = NoOpTaskOperationLock)
        val created = application.create(config, CreateGroupedTaskRequest("CLONE-OWNER", "feature/owner", "clone-owner", listOf("clone-owner")))
        val taskDirectory = taskRoot.resolve(created.taskDirectoryName)
        val target = Path.of(created.services.single().worktreePath)
        Files.move(target, target.resolveSibling("original-owned-clone"))
        GitTestSupport.clone(remote, target)
        GitTestSupport.run(target, "switch", "-c", created.services.single().branch)
        val external = target.resolve("external.txt")
        Files.writeString(external, "external")

        assertFailsWith<IllegalArgumentException> { application.delete(config, taskDirectory, forceDiscard = true) }

        assertEquals("external", Files.readString(external))
    }
}
