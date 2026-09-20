package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.io.path.exists
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TaskRootMigrationServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `empty task root switches directly and reports progress`() {
        val paths = ApplicationPaths(temporary.resolve("direct/home"))
        val source = Files.createDirectories(temporary.resolve("direct/tasks"))
        val target = temporary.resolve("direct/new-tasks")
        val store = ConfigStore(paths).also { it.save(AppConfig(taskRoot = source.toString())) }
        val service = TaskRootMigrationService(configStore = store, paths = paths)
        val progress = mutableListOf<TaskRootMigrationProgress>()

        val preview = service.preview(store.load(), target)
        val result = service.migrate(store.load(), target, progress::add)

        assertEquals(TaskRootMigrationMode.DIRECT_SWITCH, preview.mode)
        assertEquals(0, result.migratedTasks)
        assertTrue(target.exists())
        assertTrue(source.exists())
        assertEquals(target.toAbsolutePath().normalize().toString(), store.load().taskRoot)
        assertEquals(
            listOf(TaskRootMigrationPhase.PREPARING, TaskRootMigrationPhase.UPDATING_CONFIG, TaskRootMigrationPhase.COMPLETED),
            progress.map(TaskRootMigrationProgress::phase),
        )
    }

    @Test
    fun `preview blocks nested and non-empty targets`() {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val source = Files.createDirectories(temporary.resolve("tasks"))
        val configStore = ConfigStore(paths).also { it.save(AppConfig(taskRoot = source.toString())) }
        val service = TaskRootMigrationService(configStore = configStore, paths = paths)

        val nested = service.preview(configStore.load(), source.resolve("nested"))
        assertTrue(nested.blockers.any { it.contains("不能互相包含") })

        val nonEmpty = Files.createDirectories(temporary.resolve("occupied"))
        Files.writeString(nonEmpty.resolve("keep.txt"), "keep")
        val occupied = service.preview(configStore.load(), nonEmpty)
        assertTrue(occupied.blockers.any { it.contains("必须为空") })
    }

    @Test
    fun `preview blocks unreadable manifest and workspace outside its task directory`() {
        val paths = ApplicationPaths(temporary.resolve("invalid/home"))
        val source = Files.createDirectories(temporary.resolve("invalid/tasks"))
        val badTask = Files.createDirectories(source.resolve("bad-json"))
        Files.writeString(badTask.resolve("silverwing.json"), "{ not-json")
        val store = ConfigStore(paths).also { it.save(AppConfig(taskRoot = source.toString())) }
        val service = TaskRootMigrationService(configStore = store, paths = paths)

        val invalidManifest = service.preview(store.load(), temporary.resolve("invalid/target"))

        assertTrue(invalidManifest.blockers.any { it.contains("任务清单读取失败") })

        Files.delete(badTask.resolve("silverwing.json"))
        Files.delete(badTask)
        val fixture = independentFixture("outside-workspace", "TASK-OUTSIDE")
        val original = ManifestStore().load(fixture.sourceTask)
        ManifestStore().save(
            fixture.sourceTask,
            original.copy(services = original.services.map { it.copy(worktreePath = fixture.repository.rootPath) }),
        )

        val outsideWorkspace = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
        ).preview(fixture.store.load(), fixture.targetRoot)

        assertTrue(outsideWorkspace.blockers.any { it.contains("工作区不在任务目录内") })
    }

    @Test
    fun `same file store migration repairs standard worktree and preserves dirty state`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("standard"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("standard/source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val sourceRoot = Files.createDirectories(temporary.resolve("standard/tasks"))
        val sourceTask = Files.createDirectories(sourceRoot.resolve("TASK-1"))
        val worktree = sourceTask.resolve("service/default")
        GitClient().addWorktree(repositoryPath, worktree, "feature/migrate", "origin/master")
        Files.writeString(worktree.resolve("dirty.txt"), "dirty\n")
        val manifest = manifest(
            taskName = "TASK-1",
            repository = repository,
            workspacePath = worktree,
            strategy = WorkspaceStrategy.STANDARD_WORKTREE,
            branch = "feature/migrate",
        )
        ManifestStore().save(sourceTask, manifest)
        val paths = ApplicationPaths(temporary.resolve("standard/home"))
        val configStore = ConfigStore(paths).also {
            it.save(AppConfig(taskRoot = sourceRoot.toString(), repositories = listOf(repository)))
        }
        val targetRoot = temporary.resolve("standard/new-tasks")
        val service = TaskRootMigrationService(
            configStore = configStore,
            paths = paths,
            agentDocuments = AgentDocumentService(paths),
            sameFileStore = { _, _ -> true },
        )

        val preview = service.preview(configStore.load(), targetRoot)
        assertEquals(TaskRootMigrationMode.SAME_FILE_STORE, preview.mode)
        assertTrue(preview.blockers.isEmpty())

        val result = service.migrate(configStore.load(), targetRoot)
        val migratedTask = targetRoot.resolve("TASK-1")
        val migratedWorktree = migratedTask.resolve("service/default")
        val migratedManifest = ManifestStore().load(migratedTask)

        assertEquals(1, result.migratedTasks)
        assertTrue(result.cleanupFailures.isEmpty())
        assertFalse(sourceTask.exists())
        assertTrue(migratedWorktree.resolve("dirty.txt").exists())
        assertEquals(migratedWorktree.toAbsolutePath().normalize().toString(), migratedManifest.services.single().worktreePath)
        assertTrue(Files.isSameFile(migratedWorktree, GitClient().topLevel(migratedWorktree)))
        assertEquals("feature/migrate", GitClient().currentBranch(migratedWorktree))
        assertTrue(GitClient().worktrees(repositoryPath).any { Files.isSameFile(it.path, migratedWorktree) })
        assertTrue(Files.readString(migratedTask.resolve("AGENTS.md")).contains(migratedWorktree.toString()))
        assertEquals(targetRoot.toAbsolutePath().normalize().toString(), configStore.load().taskRoot)
    }

    @Test
    fun `cross file store migration copies independent clone before removing source`() {
        val fixture = independentFixture("clone", "TASK-2")
        Files.writeString(fixture.workspace.resolve("untracked.txt"), "preserve\n")
        assertIndependentOwnership(fixture.sourceTask)
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> false },
        )

        assertEquals(TaskRootMigrationMode.CROSS_FILE_STORE, service.preview(fixture.store.load(), fixture.targetRoot).mode)
        val result = service.migrate(fixture.store.load(), fixture.targetRoot)
        val migratedTask = fixture.targetRoot.resolve("TASK-2")
        val migratedClone = migratedTask.resolve(fixture.workspace.fileName)

        assertEquals(1, result.migratedTasks)
        assertFalse(fixture.sourceTask.exists())
        assertTrue(migratedClone.resolve("untracked.txt").exists())
        assertEquals("master", GitClient().currentBranch(migratedClone))
        val migrated = ManifestStore().load(migratedTask).services.single()
        assertEquals(migratedClone.toAbsolutePath().normalize().toString(), migrated.worktreePath)
        assertEquals(migrated.worktreePath, migrated.repositoryPath)
        assertIndependentOwnership(migratedTask)
        IndependentCloneWorkspaceSafety.deleteOwned(migratedTask, migratedClone, ownership(migratedTask, migrated))
        assertFalse(migratedClone.exists())
    }

    @Test
    fun `same file store migration rebinds provisioned clone ownership for safe deletion`() {
        val fixture = independentFixture("move-clone-owner", "TASK-MOVE-OWNER")
        assertIndependentOwnership(fixture.sourceTask)
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
        )

        service.migrate(fixture.store.load(), fixture.targetRoot)

        val migratedTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        val migrated = ManifestStore().load(migratedTask).services.single()
        val migratedClone = Path.of(migrated.worktreePath)
        assertFalse(fixture.sourceTask.exists())
        assertEquals(migrated.worktreePath, migrated.repositoryPath)
        assertIndependentOwnership(migratedTask)
        IndependentCloneWorkspaceSafety.deleteOwned(migratedTask, migratedClone, ownership(migratedTask, migrated))
        assertFalse(migratedClone.exists())
    }

    @Test
    fun `migration does not grant ownership to missing or incorrect clone markers`() {
        listOf(true, false).forEach { sameStore ->
            listOf<String?>(null, "not-the-original-owner").forEachIndexed { index, marker ->
                val fixture = independentFixture("invalid-owner-$sameStore-$index", "TASK-INVALID-OWNER")
                val ownerFile = fixture.workspace.resolve(".git/silverwing-owner")
                if (marker == null) Files.delete(ownerFile) else Files.writeString(ownerFile, marker)
                val service = TaskRootMigrationService(
                    configStore = fixture.store,
                    paths = fixture.paths,
                    sameFileStore = { _, _ -> sameStore },
                )

                assertFalse(service.preview(fixture.store.load(), fixture.targetRoot).canMigrate)
                assertFailsWith<IllegalArgumentException> { service.migrate(fixture.store.load(), fixture.targetRoot) }

                assertTrue(fixture.workspace.exists())
                assertFalse(fixture.targetRoot.exists())
                assertEquals(fixture.sourceRoot.toString(), fixture.store.load().taskRoot)
                if (marker == null) assertFalse(ownerFile.exists()) else assertEquals(marker, Files.readString(ownerFile))
            }
        }
    }

    @Test
    fun `same file store rollback restores original clone ownership`() {
        val fixture = independentFixture("rollback-clone-owner", "TASK-ROLLBACK-OWNER")
        val original = ManifestStore().load(fixture.sourceTask)
        val targetTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
        )

        val failure = assertFailsWith<IllegalStateException> {
            service.migrate(fixture.store.load(), fixture.targetRoot) { progress ->
                if (progress.phase == TaskRootMigrationPhase.UPDATING_CONFIG) {
                    assertIndependentOwnership(targetTask)
                    error("simulated failure after ownership rebinding")
                }
            }
        }

        assertTrue(failure.message.orEmpty().contains("已恢复原目录"))
        assertFalse(targetTask.exists())
        assertEquals(original, ManifestStore().load(fixture.sourceTask))
        assertIndependentOwnership(fixture.sourceTask)
        IndependentCloneWorkspaceSafety.deleteOwned(
            fixture.sourceTask, fixture.workspace, ownership(fixture.sourceTask, original.services.single()),
        )
        assertFalse(fixture.workspace.exists())
    }

    @Test
    fun `journal recovery restores clone ownership after interrupted move rollback`() {
        val fixture = independentFixture("recover-clone-owner", "TASK-RECOVER-OWNER")
        val targetTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        val journal = fixture.paths.home.resolve("migrations/task-root.json")
        val failing = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
            moveTaskDirectory = { source, target ->
                check(source == fixture.sourceTask) { "simulated locked target during rollback" }
                Files.move(source, target)
            },
        )

        assertFailsWith<IllegalStateException> {
            failing.migrate(fixture.store.load(), fixture.targetRoot) { progress ->
                if (progress.phase == TaskRootMigrationPhase.UPDATING_CONFIG) error("simulated pre-commit failure")
            }
        }
        assertTrue(journal.exists())
        assertIndependentOwnership(targetTask)

        val recovered = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
        ).recoverInterruptedMigration(fixture.store.load())

        assertTrue(recovered.isEmpty())
        assertFalse(targetTask.exists())
        assertFalse(journal.exists())
        assertIndependentOwnership(fixture.sourceTask)
    }

    @Test
    fun `rollback preserves an invalid clone marker instead of granting ownership`() {
        val fixture = independentFixture("rollback-invalid-owner", "TASK-ROLLBACK-INVALID")
        val targetTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
        )

        val failure = assertFailsWith<IllegalStateException> {
            service.migrate(fixture.store.load(), fixture.targetRoot) { progress ->
                if (progress.phase == TaskRootMigrationPhase.UPDATING_CONFIG) {
                    Files.writeString(targetTask.resolve(fixture.workspace.fileName).resolve(".git/silverwing-owner"), "changed-owner")
                    error("simulated marker change before rollback")
                }
            }
        }

        assertTrue(failure.message.orEmpty().contains("自动回滚未完成"))
        assertEquals("changed-owner", Files.readString(fixture.workspace.resolve(".git/silverwing-owner")))
        assertTrue(fixture.paths.home.resolve("migrations/task-root.json").exists())
        assertFailsWith<IllegalArgumentException> { assertIndependentOwnership(fixture.sourceTask) }
    }

    @Test
    fun `cross file store migration repairs copied standard worktree`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("cross-standard"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("cross-standard/source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val sourceRoot = Files.createDirectories(temporary.resolve("cross-standard/tasks"))
        val sourceTask = Files.createDirectories(sourceRoot.resolve("TASK-3"))
        val worktree = sourceTask.resolve("service/default")
        GitClient().addWorktree(repositoryPath, worktree, "feature/cross", "origin/master")
        GitTestSupport.configureIdentity(worktree)
        Files.writeString(worktree.resolve("staged.txt"), "staged\n")
        GitTestSupport.run(worktree, "add", "staged.txt")
        ManifestStore().save(
            sourceTask,
            manifest("TASK-3", repository, worktree, WorkspaceStrategy.STANDARD_WORKTREE, "feature/cross"),
        )
        val paths = ApplicationPaths(temporary.resolve("cross-standard/home"))
        val configStore = ConfigStore(paths).also {
            it.save(AppConfig(taskRoot = sourceRoot.toString(), repositories = listOf(repository)))
        }
        val targetRoot = temporary.resolve("cross-standard/new-tasks")
        val before = GitClient().readOnly(worktree, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all").stdout
        val service = TaskRootMigrationService(
            configStore = configStore,
            paths = paths,
            agentDocuments = AgentDocumentService(paths),
            sameFileStore = { _, _ -> false },
        )

        val result = service.migrate(configStore.load(), targetRoot)
        val migrated = targetRoot.resolve("TASK-3/service/default")

        assertEquals(1, result.migratedTasks)
        assertFalse(sourceTask.exists())
        assertTrue(migrated.resolve("staged.txt").exists())
        assertEquals(before, GitClient().readOnly(migrated, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all").stdout)
        assertTrue(GitClient().worktrees(repositoryPath).any { Files.isSameFile(it.path, migrated) })
    }

    @Test
    fun `failed target worktree repair restores source registration and configuration`() {
        val (remote, _) = GitTestSupport.createRemoteWithSeed(temporary.resolve("repair-failure"))
        val repositoryPath = GitTestSupport.clone(remote, temporary.resolve("repair-failure/source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val sourceRoot = Files.createDirectories(temporary.resolve("repair-failure/tasks"))
        val sourceTask = Files.createDirectories(sourceRoot.resolve("TASK-4"))
        val worktree = sourceTask.resolve("service/default")
        GitClient().addWorktree(repositoryPath, worktree, "feature/repair-failure", "origin/master")
        val manifest = manifest("TASK-4", repository, worktree, WorkspaceStrategy.STANDARD_WORKTREE, "feature/repair-failure")
        ManifestStore().save(sourceTask, manifest)
        val paths = ApplicationPaths(temporary.resolve("repair-failure/home"))
        val store = ConfigStore(paths).also {
            it.save(AppConfig(taskRoot = sourceRoot.toString(), repositories = listOf(repository)))
        }
        val targetRoot = temporary.resolve("repair-failure/new-tasks")
        val git = GitClient()
        val service = TaskRootMigrationService(
            configStore = store,
            paths = paths,
            agentDocuments = AgentDocumentService(paths),
            sameFileStore = { _, _ -> true },
            repairWorktree = { repositoryRoot, worktreePath ->
                if (worktreePath.startsWith(targetRoot)) error("simulated repair failure")
                git.repairWorktree(repositoryRoot, worktreePath)
            },
        )

        val error = assertFailsWith<IllegalStateException> { service.migrate(store.load(), targetRoot) }

        assertTrue(error.message.orEmpty().contains("已恢复原目录"))
        assertTrue(worktree.exists())
        assertFalse(targetRoot.resolve("TASK-4").exists())
        assertEquals(sourceRoot.toAbsolutePath().normalize().toString(), store.load().taskRoot)
        assertTrue(git.worktrees(repositoryPath).any { Files.isSameFile(it.path, worktree) })
        assertFalse(paths.home.resolve("migrations/task-root.json").exists())
    }

    @Test
    fun `configuration save failure rolls back copied task and regenerated documents`() {
        val fixture = independentFixture("config-failure", "TASK-5")
        val original = ManifestStore().load(fixture.sourceTask)
        val documents = AgentDocumentService(fixture.paths)
        documents.writeTaskDocument(
            fixture.sourceTask,
            original,
            listOf(fixture.repository.toInfo()),
            taskNotes = "保留人工说明",
        )
        val failingStore = object : ConfigurationRepository {
            override fun load(): AppConfig = fixture.store.load()
            override fun save(config: AppConfig) {
                if (config.taskRoot == fixture.targetRoot.toAbsolutePath().normalize().toString()) {
                    error("simulated config save failure")
                }
                fixture.store.save(config)
            }
        }
        val service = TaskRootMigrationService(
            configStore = failingStore,
            paths = fixture.paths,
            agentDocuments = documents,
            sameFileStore = { _, _ -> false },
        )

        assertFailsWith<IllegalStateException> { service.migrate(failingStore.load(), fixture.targetRoot) }

        assertTrue(fixture.workspace.exists())
        assertFalse(fixture.targetRoot.resolve(fixture.sourceTask.fileName).exists())
        assertEquals(fixture.sourceRoot.toAbsolutePath().normalize().toString(), fixture.store.load().taskRoot)
        val restoredAgents = Files.readString(fixture.sourceTask.resolve("AGENTS.md"))
        assertTrue(restoredAgents.contains(fixture.workspace.toString()))
        assertTrue(restoredAgents.contains("保留人工说明"))
        assertIndependentOwnership(fixture.sourceTask)
    }

    @Test
    fun `failure after configuration commit keeps target authoritative for journal recovery`() {
        val fixture = independentFixture("post-commit-failure", "TASK-POST-COMMIT")
        Files.writeString(fixture.workspace.resolve("user-work.txt"), "keep after commit")
        val journal = fixture.paths.home.resolve("migrations/task-root.json")
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
        )

        val error = assertFailsWith<IllegalStateException> {
            service.migrate(fixture.store.load(), fixture.targetRoot) { progress ->
                if (progress.phase == TaskRootMigrationPhase.CLEANING_SOURCE) {
                    error("synthetic post-commit failure")
                }
            }
        }

        val targetTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        assertTrue(error.message.orEmpty().contains("已提交"))
        assertFalse(fixture.sourceTask.exists())
        assertTrue(targetTask.resolve(fixture.workspace.fileName).resolve("user-work.txt").exists())
        assertEquals(fixture.targetRoot.toAbsolutePath().normalize().toString(), fixture.store.load().taskRoot)
        assertTrue(journal.exists())

        val recovered = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
        ).recoverInterruptedMigration(fixture.store.load())

        assertTrue(recovered.isEmpty())
        assertFalse(journal.exists())
        assertTrue(targetTask.resolve(fixture.workspace.fileName).resolve("user-work.txt").exists())
        assertIndependentOwnership(targetTask)
    }

    @Test
    fun `config write failure with unreadable state keeps migration journal for recovery`() {
        val fixture = independentFixture("uncertain-commit", "TASK-UNCERTAIN-COMMIT")
        Files.writeString(fixture.workspace.resolve("user-work.txt"), "keep after uncertain commit")
        var persisted = fixture.store.load()
        var failReads = false
        val uncertainStore = object : ConfigurationRepository {
            override fun load(): AppConfig {
                check(!failReads) { "simulated config read failure" }
                return persisted
            }

            override fun save(config: AppConfig) {
                persisted = config
                failReads = true
                error("simulated config write acknowledgement failure")
            }
        }
        val journal = fixture.paths.home.resolve("migrations/task-root.json")
        val service = TaskRootMigrationService(
            configStore = uncertainStore,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
        )

        val error = assertFailsWith<IllegalStateException> {
            service.migrate(persisted, fixture.targetRoot)
        }

        val targetTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        assertTrue(error.message.orEmpty().contains("无法确认提交状态"))
        assertFalse(fixture.sourceTask.exists())
        assertTrue(targetTask.resolve(fixture.workspace.fileName).resolve("user-work.txt").exists())
        assertTrue(journal.exists())

        failReads = false
        val recovered = TaskRootMigrationService(
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
        ).recoverInterruptedMigration(uncertainStore.load())

        assertTrue(recovered.isEmpty())
        assertFalse(journal.exists())
        assertTrue(targetTask.resolve(fixture.workspace.fileName).resolve("user-work.txt").exists())
        assertIndependentOwnership(targetTask)
    }

    @Test
    fun `completed progress failure after cleanup reports completed state`() {
        val fixture = independentFixture("completed-callback-failure", "TASK-COMPLETED-CALLBACK")
        val journal = fixture.paths.home.resolve("migrations/task-root.json")
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> true },
        )

        val error = assertFailsWith<IllegalStateException> {
            service.migrate(fixture.store.load(), fixture.targetRoot) { progress ->
                if (progress.phase == TaskRootMigrationPhase.COMPLETED) {
                    throw IllegalStateException("synthetic completion callback failure")
                }
            }
        }

        assertTrue(error.message.orEmpty().contains("已完成"))
        assertFalse(error.message.orEmpty().contains("待清理"))
        assertFalse(fixture.sourceTask.exists())
        assertTrue(fixture.targetRoot.resolve(fixture.sourceTask.fileName).exists())
        assertFalse(journal.exists())
    }

    @Test
    fun `interrupted rollback is completed from the journal on next startup`() {
        val fixture = independentFixture("interrupted-rollback", "TASK-6")
        val targetTask = fixture.targetRoot.resolve(fixture.sourceTask.fileName)
        val failing = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> false },
            copyTaskDirectory = { _, target ->
                Files.createDirectories(target)
                Files.writeString(target.resolve("partial.txt"), "partial")
                error("simulated copy failure")
            },
            deleteTaskDirectory = { error("simulated rollback cleanup failure") },
        )

        val error = assertFailsWith<IllegalStateException> { failing.migrate(fixture.store.load(), fixture.targetRoot) }
        assertTrue(error.message.orEmpty().contains("自动回滚未完成"))
        assertTrue(targetTask.exists())
        assertTrue(fixture.paths.home.resolve("migrations/task-root.json").exists())

        val recovered = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> false },
        ).recoverInterruptedMigration(fixture.store.load())

        assertTrue(recovered.isEmpty())
        assertTrue(fixture.sourceTask.exists())
        assertFalse(targetTask.exists())
        assertFalse(fixture.paths.home.resolve("migrations/task-root.json").exists())
        assertIndependentOwnership(fixture.sourceTask)
    }

    @Test
    fun `source cleanup failure keeps journal and recovery finishes cleanup`() {
        val fixture = independentFixture("cleanup-failure", "TASK-7")
        val service = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> false },
            deleteTaskDirectory = { path ->
                if (path == fixture.sourceTask) error("simulated locked directory")
                error("unexpected cleanup path: $path")
            },
        )

        val result = service.migrate(fixture.store.load(), fixture.targetRoot)

        assertEquals(listOf(fixture.sourceTask), result.cleanupFailures)
        assertTrue(fixture.sourceTask.exists())
        assertTrue(fixture.targetRoot.resolve(fixture.sourceTask.fileName).exists())
        assertEquals(fixture.targetRoot.toAbsolutePath().normalize().toString(), fixture.store.load().taskRoot)
        assertTrue(fixture.paths.home.resolve("migrations/task-root.json").exists())

        val recovered = TaskRootMigrationService(
            configStore = fixture.store,
            paths = fixture.paths,
            agentDocuments = AgentDocumentService(fixture.paths),
            sameFileStore = { _, _ -> false },
        ).recoverInterruptedMigration(fixture.store.load())

        assertTrue(recovered.isEmpty())
        assertFalse(fixture.sourceTask.exists())
        assertFalse(fixture.paths.home.resolve("migrations/task-root.json").exists())
    }

    private fun independentFixture(prefix: String, taskName: String): IndependentFixture {
        val base = temporary.resolve(prefix)
        val (remote, _) = GitTestSupport.createRemoteWithSeed(base)
        val repositoryPath = GitTestSupport.clone(remote, base.resolve("source"))
        val repository = GitRepositoryInspector().inspect(repositoryPath)
        val sourceRoot = Files.createDirectories(base.resolve("tasks"))
        val sourceTask = Files.createDirectories(sourceRoot.resolve(taskName))
        val module = ServiceModuleConfig(
            "clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, baseRef = "origin/master", tagEnabled = false,
        )
        val service = GroupServiceConfig("service-clone", repository.id, "Clone", modules = listOf(module))
        val provisioned = IndependentCloneProvisioner().provision(
            WorkspaceProvisionRequest(sourceTask, repository, service, moduleBranches = mapOf("clone" to "")),
        ).single()
        val workspace = Path.of(provisioned.worktreePath)
        ManifestStore().save(
            sourceTask,
            manifest(taskName, repository, workspace, WorkspaceStrategy.INDEPENDENT_CLONE, "master")
                .copy(services = listOf(provisioned)),
        )
        val paths = ApplicationPaths(base.resolve("home"))
        val store = ConfigStore(paths).also {
            it.save(AppConfig(taskRoot = sourceRoot.toString(), repositories = listOf(repository)))
        }
        return IndependentFixture(paths, store, repository, sourceRoot, sourceTask, workspace, base.resolve("new-tasks"))
    }

    private fun assertIndependentOwnership(taskDirectory: Path) {
        val workspace = ManifestStore().load(taskDirectory).services.single()
        IndependentCloneWorkspaceSafety.requireOwned(Path.of(workspace.worktreePath), ownership(taskDirectory, workspace))
    }

    private fun ownership(taskDirectory: Path, workspace: ServiceWorkspace): String =
        IndependentCloneWorkspaceSafety.ownership(
            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
        )

    private data class IndependentFixture(
        val paths: ApplicationPaths,
        val store: ConfigStore,
        val repository: RepositoryConfig,
        val sourceRoot: Path,
        val sourceTask: Path,
        val workspace: Path,
        val targetRoot: Path,
    )

    private fun manifest(
        taskName: String,
        repository: RepositoryConfig,
        workspacePath: Path,
        strategy: WorkspaceStrategy,
        branch: String,
    ): TaskManifest = TaskManifest(
        folderName = taskName,
        taskDirectoryName = taskName,
        featureBranch = branch,
        createdAt = "2026-08-29 00:00:00",
        updatedAt = "2026-08-29 00:00:00",
        services = listOf(
            ServiceWorkspace(
                repositoryId = repository.id,
                serviceName = "service",
                repositoryPath = repository.rootPath,
                worktreePath = workspacePath.toAbsolutePath().normalize().toString(),
                developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
                branch = branch,
                health = WorkspaceHealth.READY,
                strategy = strategy,
                originUrl = repository.originUrl,
            ),
        ),
    )
}
