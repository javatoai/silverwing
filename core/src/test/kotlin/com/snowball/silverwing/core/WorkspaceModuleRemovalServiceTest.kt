package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.time.Duration
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class WorkspaceModuleRemovalServiceTest {
    @Test
    fun `missing module path removes only its task record`() {
        val root = Files.createTempDirectory("module-remove-missing-")
        val taskDirectory = root.resolve("task")
        val store = ManifestStore()
        val original = manifest(taskDirectory, workspaceCount = 2)
        store.save(taskDirectory, original)
        val documents = RemovalRecordingDocuments()
        val service = WorkspaceModuleRemovalService(
            manifests = store,
            agentDocuments = documents,
            taskLock = NoOpTaskOperationLock,
            clock = Clock.fixed(Instant.parse("2026-08-13T00:00:00Z"), ZoneOffset.UTC),
        )

        val preview = service.inspect(config(root), taskDirectory, original.services.first().worktreePath)
        val result = service.remove(
            config(root),
            taskDirectory,
            preview,
            WorkspaceModuleRemovalConfirmation(preview.fingerprint),
        )

        assertTrue(preview.pathMissing)
        assertEquals(listOf("module-2"), result.manifest.services.map(ServiceWorkspace::moduleId))
        assertEquals(result.manifest, store.load(taskDirectory))
        assertEquals(1, documents.writes)
    }

    @Test
    fun `last task workspace cannot be removed even when its path is missing`() {
        val root = Files.createTempDirectory("module-remove-last-")
        val taskDirectory = root.resolve("task")
        val store = ManifestStore()
        val original = manifest(taskDirectory, workspaceCount = 1)
        store.save(taskDirectory, original)
        val service = WorkspaceModuleRemovalService(
            manifests = store,
            agentDocuments = RemovalRecordingDocuments(),
            taskLock = NoOpTaskOperationLock,
        )
        val preview = service.inspect(config(root), taskDirectory, original.services.single().worktreePath)

        assertFailsWith<IllegalArgumentException> {
            service.remove(config(root), taskDirectory, preview, WorkspaceModuleRemovalConfirmation(preview.fingerprint))
        }
        assertEquals(original, store.load(taskDirectory))
    }

    @Test
    fun `module removal refuses a workspace path outside the task directory`() {
        val root = Files.createTempDirectory("module-remove-boundary-")
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root.resolve("git"))
        val source = GitTestSupport.clone(remote, root.resolve("git/source"))
        val external = GitTestSupport.clone(remote, root.resolve("external"))
        val repository = GitRepositoryInspector().inspect(source)
        val taskDirectory = root.resolve("tasks/task")
        val store = ManifestStore()
        val externalWorkspace = ServiceWorkspace(
            repositoryId = repository.id,
            serviceName = "External",
            repositoryPath = external.toString(),
            worktreePath = external.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "master",
            groupServiceId = "service",
            moduleId = "external",
            moduleName = "external",
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            originUrl = remote.toString(),
            baseRef = "origin/master",
            targetBranch = null,
        )
        val original = TaskManifest(
            folderName = "task",
            taskDirectoryName = "task",
            featureBranch = "feature/task",
            createdAt = "2026-08-13 08:00:00",
            updatedAt = "2026-08-13 08:00:00",
            services = listOf(
                externalWorkspace,
                externalWorkspace.copy(moduleId = "missing", moduleName = "missing", worktreePath = taskDirectory.resolve("missing").toString()),
            ),
        )
        store.save(taskDirectory, original)
        val config = AppConfig(
            taskRoot = root.resolve("tasks").toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME)),
        )
        val service = WorkspaceModuleRemovalService(store, RemovalRecordingDocuments(), NoOpTaskOperationLock)
        assertFailsWith<IllegalArgumentException> {
            service.inspect(config, taskDirectory, external.toString())
        }
        assertTrue(Files.isDirectory(external))
        assertEquals(original, store.load(taskDirectory))
    }

    @Test
    fun `worktree prune failure keeps deletion backup and reports cleanup error`() {
        val root = Files.createTempDirectory("module-remove-prune-")
        val (remote, _) = GitTestSupport.createRemoteWithSeed(root.resolve("git"))
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("git/source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val taskDirectory = root.resolve("tasks/task")
        val worktree = taskDirectory.resolve("service-module")
        Files.createDirectories(taskDirectory)
        GitClient().addWorktree(repositoryPath, worktree, "feature/remove", "origin/master")
        val workspace = ServiceWorkspace(
            repositoryId = repository.id,
            serviceName = "Service",
            repositoryPath = repositoryPath.toString(),
            worktreePath = worktree.toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/remove",
            groupServiceId = "service",
            moduleId = "module",
            moduleName = "module",
            baseRef = "origin/master",
            targetBranch = "feature/remove",
        )
        val original = TaskManifest(
            folderName = "task",
            taskDirectoryName = "task",
            featureBranch = "feature/task",
            createdAt = "2026-08-13 08:00:00",
            updatedAt = "2026-08-13 08:00:00",
            services = listOf(workspace, workspace.copy(moduleId = "missing", moduleName = "missing", worktreePath = taskDirectory.resolve("missing").toString())),
        )
        val store = ManifestStore()
        store.save(taskDirectory, original)
        val pruneCalls = AtomicInteger()
        val delegate = ProcessCommandRunner()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                if (command.takeLast(2) == listOf("worktree", "prune") && pruneCalls.incrementAndGet() == 3) {
                    return CommandResult(1, "", "simulated prune failure")
                }
                return delegate.run(command, workingDirectory, timeout, environment)
            }
        }
        val config = AppConfig(
            taskRoot = root.resolve("tasks").toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME)),
        )
        val service = WorkspaceModuleRemovalService(
            manifests = store,
            agentDocuments = RemovalRecordingDocuments(),
            taskLock = NoOpTaskOperationLock,
            repositoryLock = RepositoryOperationLock(ApplicationPaths(root.resolve("app-home"))),
            git = GitClient(runner),
        )
        val preview = service.inspect(config, taskDirectory, worktree.toString())

        val result = service.remove(config, taskDirectory, preview, WorkspaceModuleRemovalConfirmation(preview.fingerprint))

        assertTrue(result.cleanupError.orEmpty().contains("simulated prune failure"))
        val backup = Path.of(requireNotNull(result.retainedBackupPath))
        assertTrue(Files.isDirectory(backup))
        assertTrue(!Files.exists(worktree))
    }

    private fun config(root: Path) = AppConfig(
        taskRoot = root.toString(),
        repositories = listOf(RepositoryConfig("repo", "Repo", root.resolve("repo").toString(), root.resolve("repo/.git").toString(), "https://example.test/repo.git")),
        groups = listOf(GroupConfig(id = "default", name = "Default")),
    )

    private fun manifest(taskDirectory: Path, workspaceCount: Int) = TaskManifest(
        folderName = "task",
        taskDirectoryName = "task",
        featureBranch = "feature/task",
        createdAt = "2026-08-13 08:00:00",
        updatedAt = "2026-08-13 08:00:00",
        services = (1..workspaceCount).map { index ->
            ServiceWorkspace(
                repositoryId = "repo",
                serviceName = "Service",
                repositoryPath = taskDirectory.resolve("missing-repo").toString(),
                worktreePath = taskDirectory.resolve("missing-$index").toString(),
                developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                branch = "feature/task-module-$index",
                groupServiceId = "service",
                moduleId = "module-$index",
                moduleName = "module-$index",
                baseRef = "origin/master",
                targetBranch = "feature/task-module-$index",
            )
        },
    )
}

private class RemovalRecordingDocuments : AgentDocuments {
    var writes = 0
    override fun readGlobal(): String = ""
    override fun saveGlobal(content: String) = Unit
    override fun readGroup(groupId: String): String = ""
    override fun saveGroup(groupId: String, content: String) = Unit
    override fun writeTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path {
        writes++
        return taskDirectory.resolve("AGENTS.md")
    }
}
