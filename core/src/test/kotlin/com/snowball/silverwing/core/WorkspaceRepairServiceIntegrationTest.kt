package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class WorkspaceRepairServiceIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `missing worktree requires remote reuse confirmation and tracks remote branch`() {
        val fixture = fixture("remote-reuse", WorkspaceStrategy.STANDARD_WORKTREE)
        GitTestSupport.run(fixture.seed, "switch", "-c", "feature/remote", "master")
        GitTestSupport.run(fixture.seed, "push", "origin", "feature/remote")
        val workspace = fixture.workspace("feature/remote", fixture.taskDirectory.resolve("service"))
        fixture.save(workspace)
        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)

        assertEquals(WorkspaceRepairAction.TRACK_REMOTE_AND_ATTACH, preview.action)
        assertTrue(preview.requiresRemoteReuseConfirmation)
        assertFailsWith<IllegalArgumentException> {
            fixture.repairs.repair(fixture.config, fixture.taskDirectory, preview, WorkspaceRepairConfirmation())
        }

        fixture.repairs.repair(
            fixture.config,
            fixture.taskDirectory,
            preview,
            WorkspaceRepairConfirmation(reuseRemoteBranch = true),
        )
        assertEquals("feature/remote", GitTestSupport.run(Path.of(workspace.worktreePath), "branch", "--show-current"))
        assertEquals("origin/feature/remote", GitTestSupport.run(Path.of(workspace.worktreePath), "rev-parse", "--abbrev-ref", "@{upstream}"))
    }

    @Test
    fun `invalid worktree directory is retained as backup before recreation`() {
        val fixture = fixture("backup", WorkspaceStrategy.STANDARD_WORKTREE)
        GitTestSupport.run(fixture.repository.rootPath.let(Path::of), "branch", "feature/local", "origin/master")
        val target = fixture.taskDirectory.resolve("service")
        Files.createDirectories(target)
        Files.writeString(target.resolve("keep.txt"), "keep me")
        val workspace = fixture.workspace("feature/local", target)
        fixture.save(workspace)
        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)

        assertEquals(WorkspaceRepairAction.BACKUP_AND_RECREATE_WORKTREE, preview.action)
        val backup = Path.of(assertNotNull(preview.backupPath))
        fixture.repairs.repair(fixture.config, fixture.taskDirectory, preview, WorkspaceRepairConfirmation())

        assertTrue(Files.exists(backup.resolve("keep.txt")))
        assertEquals("feature/local", GitTestSupport.run(target, "branch", "--show-current"))
    }

    @Test
    fun `missing independent clone is recreated from its recorded remote branch`() {
        val fixture = fixture("clone", WorkspaceStrategy.INDEPENDENT_CLONE)
        val target = fixture.taskDirectory.resolve("service-master")
        val workspace = fixture.workspace("master", target)
        fixture.save(workspace)

        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)
        assertEquals(WorkspaceRepairAction.RECLONE, preview.action)
        fixture.repairs.repair(fixture.config, fixture.taskDirectory, preview, WorkspaceRepairConfirmation())

        assertEquals("master", GitTestSupport.run(target, "branch", "--show-current"))
    }

    @Test
    fun `invalid existing independent clone is backed up before reclone`() {
        val fixture = fixture("invalid-clone", WorkspaceStrategy.INDEPENDENT_CLONE)
        val target = fixture.taskDirectory.resolve("service")
        Files.createDirectories(target)
        Files.writeString(target.resolve("keep.txt"), "must be backed up")
        val workspace = fixture.workspace("master", target)
        fixture.save(workspace)

        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)

        assertEquals(WorkspaceRepairAction.RECLONE, preview.action)
        assertTrue(preview.canRepair)
        val backup = Path.of(assertNotNull(preview.backupPath))
        fixture.repairs.repair(fixture.config, fixture.taskDirectory, preview, WorkspaceRepairConfirmation())

        assertTrue(Files.exists(backup.resolve("keep.txt")))
        assertEquals("master", GitTestSupport.run(target, "branch", "--show-current"))
    }

    @Test
    fun `wrong branch with local changes is reported but never switched`() {
        val fixture = fixture("dirty-mismatch", WorkspaceStrategy.STANDARD_WORKTREE)
        val repositoryPath = Path.of(fixture.repository.rootPath)
        GitTestSupport.run(repositoryPath, "branch", "feature/expected", "origin/master")
        GitTestSupport.run(repositoryPath, "branch", "feature/wrong", "origin/master")
        val target = fixture.taskDirectory.resolve("service")
        Files.createDirectories(fixture.taskDirectory)
        GitTestSupport.run(repositoryPath, "worktree", "add", target.toString(), "feature/wrong")
        Files.writeString(target.resolve("dirty.txt"), "local")
        val workspace = fixture.workspace("feature/expected", target)
        fixture.save(workspace)

        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)

        assertEquals(WorkspaceRepairAction.MANUAL, preview.action)
        assertTrue(!preview.canRepair)
        assertEquals("feature/wrong", GitTestSupport.run(target, "branch", "--show-current"))
    }

    @Test
    fun `repair inspection rejects a workspace path outside the task directory`() {
        val fixture = fixture("outside-workspace", WorkspaceStrategy.STANDARD_WORKTREE)
        val repositoryPath = Path.of(fixture.repository.rootPath)
        GitTestSupport.run(repositoryPath, "branch", "feature/outside", "origin/master")
        val outside = temporary.resolve("must-not-be-created")
        val workspace = fixture.workspace("feature/outside", outside)
        fixture.save(workspace)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)
        }

        assertTrue(error.message.orEmpty().contains("任务目录的直接子级"))
        assertTrue(Files.notExists(outside))
    }

    @Test
    fun `repair revalidates the task root inside the lock before creating a workspace`() {
        val fixture = fixture("changed-task-root", WorkspaceStrategy.STANDARD_WORKTREE)
        val repositoryPath = Path.of(fixture.repository.rootPath)
        GitTestSupport.run(repositoryPath, "branch", "feature/local", "origin/master")
        val target = fixture.taskDirectory.resolve("service")
        val workspace = fixture.workspace("feature/local", target)
        fixture.save(workspace)
        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)
        val changedConfig = fixture.config.copy(taskRoot = temporary.resolve("different-task-root").toString())

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.repairs.repair(changedConfig, fixture.taskDirectory, preview, WorkspaceRepairConfirmation())
        }

        assertTrue(error.message.orEmpty().contains("任务根目录"))
        assertTrue(Files.notExists(target))
    }

    @Test
    fun `repair uses the shared common directory repository lock`() {
        val fixture = fixture("shared-repository-lock", WorkspaceStrategy.STANDARD_WORKTREE)
        val repositoryPath = Path.of(fixture.repository.rootPath)
        GitTestSupport.run(repositoryPath, "branch", "feature/locked", "origin/master")
        val target = fixture.taskDirectory.resolve("service")
        val workspace = fixture.workspace("feature/locked", target)
        fixture.save(workspace)
        val preview = fixture.repairs.inspect(fixture.config, fixture.taskDirectory, workspace.worktreePath)

        fixture.repositoryLock.withLock(GitClient().commonDirectory(repositoryPath)) {
            assertFailsWith<IllegalStateException> {
                fixture.repairs.repair(fixture.config, fixture.taskDirectory, preview, WorkspaceRepairConfirmation())
            }
        }

        assertTrue(Files.notExists(target))
    }

    private fun fixture(name: String, strategy: WorkspaceStrategy): Fixture {
        val root = temporary.resolve(name)
        Files.createDirectories(root)
        val (remote, seed) = GitTestSupport.createRemoteWithSeed(root.resolve("git"))
        val repositoryPath = GitTestSupport.clone(remote, root.resolve("repository"))
        val git = GitClient()
        val repository = RepositoryConfig(
            id = "repo",
            name = "repo",
            rootPath = repositoryPath.toString(),
            gitCommonDirectory = git.commonDirectory(repositoryPath).toString(),
            originUrl = remote.toString(),
        )
        val service = when (strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> GroupServiceConfig.standard("service", "repo", "Service")
            WorkspaceStrategy.INDEPENDENT_CLONE -> GroupServiceConfig(
                id = "service", repositoryId = "repo", displayName = "Service",
                modules = listOf(ServiceModuleConfig("default", strategy = strategy, baseRef = "origin/master")),
            )
        }
        val taskRoot = root.resolve("tasks")
        val taskDirectory = taskRoot.resolve("task")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(repository),
            groups = listOf(GroupConfig("default", "Default", services = listOf(service))),
        )
        val manifests = ManifestStore()
        val paths = ApplicationPaths(root.resolve("home"))
        val repositoryLock = RepositoryOperationLock(paths)
        val repairs = WorkspaceRepairService(
            manifests = manifests,
            agentDocuments = NoOpAgentDocuments,
            taskLock = NoOpTaskOperationLock,
            repositoryLock = repositoryLock,
            git = git,
            clock = Clock.fixed(Instant.parse("2026-08-11T00:00:00Z"), ZoneOffset.UTC),
        )
        return Fixture(seed, repository, service, config, taskDirectory, manifests, repairs, repositoryLock)
    }

    private data class Fixture(
        val seed: Path,
        val repository: RepositoryConfig,
        val service: GroupServiceConfig,
        val config: AppConfig,
        val taskDirectory: Path,
        val manifests: ManifestStore,
        val repairs: WorkspaceRepairService,
        val repositoryLock: RepositoryOperationLock,
    ) {
        fun workspace(branch: String, path: Path) = ServiceWorkspace(
            repositoryId = repository.id,
            serviceName = service.displayName,
            repositoryPath = if (service.modules.single().strategy == WorkspaceStrategy.INDEPENDENT_CLONE) path.toString() else repository.rootPath,
            worktreePath = path.toAbsolutePath().normalize().toString(),
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = branch,
            groupServiceId = service.id,
            strategy = service.modules.single().strategy,
            originUrl = repository.originUrl,
            baseRef = "origin/master",
            pushRemote = "origin",
        )

        fun save(workspace: ServiceWorkspace) {
            manifests.save(
                taskDirectory,
                TaskManifest(
                    folderName = "task",
                    taskDirectoryName = "task",
                    featureBranch = workspace.branch,
                    createdAt = "2026-08-11T00:00:00Z",
                    updatedAt = "2026-08-11T00:00:00Z",
                    services = listOf(workspace),
                ),
            )
        }
    }

    private object NoOpAgentDocuments : AgentDocuments {
        override fun readGlobal() = ""
        override fun saveGlobal(content: String) = Unit
        override fun readGroup(groupId: String) = ""
        override fun saveGroup(groupId: String, content: String) = Unit
        override fun writeTaskDocument(taskDirectory: Path, manifest: TaskManifest, repositories: List<RepositoryInfo>, taskNotes: String?) =
            taskDirectory.resolve("AGENTS.md")
    }
}
