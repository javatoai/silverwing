package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class DeliveryControllerTest {
    @TempDir lateinit var temporary: Path

    private class Commands(private val block: (List<String>) -> CommandResult) : CommandRunner {
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration,
            environment: Map<String, String>): CommandResult = block(command)
    }

    private inner class Fixture(scope: CoroutineScope, dispatcher: CoroutineDispatcher,
        provider: GenbuTagStatusProvider = GenbuTagStatusProvider { _, _ -> building() },
        commands: CommandRunner = Commands { error("No Git command expected") },
        operationState: TagOperationState = TagOperationState.SUCCESS) {
        val directory = temporary.resolve("tasks/task")
        val workspace = ServiceWorkspace("repo", "服务", temporary.resolve("repo").toString(),
            directory.resolve("repo").toString(), DevelopmentToolType.INTELLIJ_IDEA, "feature/task",
            health = WorkspaceHealth.READY, groupServiceId = "service", tagEnabled = true,
            tagMode = TagBuildMode.CURRENT_BRANCH)
        val task = TaskManifest(folderName = "task", taskDirectoryName = "task", featureBranch = "feature/task",
            createdAt = "2026-10-03 08:00:00", updatedAt = "2026-10-03 08:00:00", services = listOf(workspace), groupId = "group")
        val config = AppConfig(taskRoot = directory.parent.toString(), repositories = listOf(RepositoryConfig("repo", "仓库",
            temporary.resolve("repo").toString(), temporary.resolve("repo/.git").toString())), groups = listOf(GroupConfig("group", "测试",
            services = listOf(GroupServiceConfig("service", "repo", "服务", genbuProbeEnabled = true)))))
        val operation = TagOperation("operation", task.folderName, workspace.serviceName, workspace.repositoryId,
            workspace.branch, remote = "origin", state = operationState, createdAt = task.createdAt,
            updatedAt = task.updatedAt, tag = "1.0.0.1", groupServiceId = workspace.groupServiceId)
        val store = TagOperationStore()
        val coordinator = OperationCoordinator()
        val runner = OperationRunner(coordinator, scope, dispatcher)
        val errors = mutableListOf<Throwable>()
        val controller: DeliveryController
        init {
            ManifestStore().save(directory, task)
            store.save(directory, operation)
            val lifecycle = object : WorkspaceLifecycle {
                override fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<DeleteRisk> = error("unused")
                override fun requireArchiveSafe(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): Unit = error("unused")
                override fun removeAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): WorkspaceRemovalResult = error("unused")
                override fun restoreAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<ServiceWorkspace> = error("unused")
                override fun validateForMutation(config: AppConfig, taskDirectory: Path, manifest: TaskManifest,
                    workspace: ServiceWorkspace) = WorkspaceMutationTarget(temporary.resolve("repo"), Path.of(workspace.worktreePath))
            }
            val adapter = GitTagDeliveryAdapter(TagBuildService(paths = ApplicationPaths(temporary.resolve("app")),
                git = GitClient(commands, GitExecutable { "git-fake" }), workspaceLifecycle = lifecycle,
                taskLock = NoOpTaskOperationLock))
            controller = DeliveryController(AppSessionStore(config, listOf(task)), adapter, runner,
                { directory }, {}, GenbuTagProbeService(store, provider), scope, dispatcher, errors::add)
        }
    }

    @Test fun `rapid manual refresh reserves busy state before the coroutine starts and can retry`() = runTest {
        var queries = 0
        val fixture = Fixture(this, StandardTestDispatcher(testScheduler), GenbuTagStatusProvider { _, _ ->
            queries++; building()
        })
        assertTrue(fixture.controller.refreshGenbuTagProbes())
        assertTrue(fixture.controller.isGenbuProbeRefreshing)
        assertFalse(fixture.controller.refreshGenbuTagProbes())
        advanceUntilIdle()
        assertEquals(1, queries)
        assertFalse(fixture.controller.isGenbuProbeRefreshing)
        assertFalse(fixture.coordinator.busy)
        assertTrue(fixture.controller.refreshGenbuTagProbes())
        advanceUntilIdle()
        assertEquals(2, queries)
    }

    @Test fun `scope cancellation before refresh begins clears busy state and rejects future reads`() = runTest {
        val scope = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        var queries = 0
        val fixture = Fixture(scope, StandardTestDispatcher(testScheduler), GenbuTagStatusProvider { _, _ ->
            queries++; building()
        })
        assertTrue(fixture.controller.refreshGenbuTagProbes())
        scope.cancel()
        advanceUntilIdle()
        assertFalse(fixture.controller.isGenbuProbeRefreshing)
        assertFalse(fixture.controller.refreshGenbuTagProbes())
        fixture.controller.setGenbuTagProbeVisible(true)
        advanceUntilIdle()
        assertEquals(0, queries)
        assertEquals(listOf(fixture.operation), fixture.controller.state.history)
    }

    @Test fun `immediate probe completion does not retain a stale refresh job`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
        val fixture = Fixture(scope, Dispatchers.Unconfined)
        try {
            assertTrue(fixture.controller.refreshGenbuTagProbes())
            assertFalse(fixture.controller.isGenbuProbeRefreshing)
            assertTrue(fixture.controller.refreshGenbuTagProbes())
            assertFalse(fixture.controller.isGenbuProbeRefreshing)
        } finally { scope.cancel() }
    }

    @Test fun `failed query remains in history and a forced retry can replace its failure`() = runTest {
        var failing = true
        val fixture = Fixture(this, StandardTestDispatcher(testScheduler), GenbuTagStatusProvider { _, _ ->
            if (failing) error("Genbu 暂时不可用")
            building()
        })
        assertTrue(fixture.controller.refreshGenbuTagProbes())
        advanceUntilIdle()
        assertFalse(fixture.controller.isGenbuProbeRefreshing)
        assertEquals("Genbu 暂时不可用", fixture.controller.state.history.single().genbuStatus.failureReason)
        failing = false
        assertTrue(fixture.controller.refreshGenbuTagProbes())
        advanceUntilIdle()
        assertEquals(null, fixture.controller.state.history.single().genbuStatus.failureReason)
        assertEquals(GenbuStageStatus.BUILDING, fixture.controller.state.history.single().genbuStatus.build)
    }

    @Test fun `rejected duplicate build preserves the accepted build marker until scope cancellation`() = runTest {
        val scope = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        val fixture = Fixture(scope, StandardTestDispatcher(testScheduler))
        assertTrue(fixture.controller.build(fixture.task, fixture.workspace))
        assertTrue(fixture.controller.isTagBuildActive(fixture.operation))
        assertFalse(fixture.controller.build(fixture.task, fixture.workspace))
        assertTrue(fixture.controller.isTagBuildActive(fixture.operation))
        scope.cancel()
        advanceUntilIdle()
        assertFalse(fixture.controller.isTagBuildActive(fixture.operation))
        assertFalse(fixture.coordinator.busy)
        assertEquals(fixture.operation, fixture.store.load(fixture.directory, fixture.operation.operationId))
    }

    @Test fun `hiding polling interrupts a blocked probe without publishing failure and manual retry works`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val started = CompletableDeferred<Unit>()
        val interrupted = CompletableDeferred<Unit>()
        val queries = AtomicInteger()
        val fixture = Fixture(scope, Dispatchers.IO, GenbuTagStatusProvider { _, _ ->
            if (queries.incrementAndGet() == 1) {
                started.complete(Unit)
                try { Thread.sleep(60_000) } catch (error: InterruptedException) {
                    interrupted.complete(Unit); throw error
                }
            }
            building()
        })
        try {
            fixture.controller.setGenbuTagProbeVisible(true)
            withTimeout(5_000) { started.await() }
            fixture.controller.setGenbuTagProbeVisible(false)
            withTimeout(5_000) { interrupted.await() }
            assertEquals(listOf(fixture.operation), fixture.controller.state.history)
            assertEquals(fixture.operation, fixture.store.load(fixture.directory, fixture.operation.operationId))
            assertTrue(fixture.controller.refreshGenbuTagProbes())
            withTimeout(5_000) { while (fixture.controller.isGenbuProbeRefreshing) delay(10) }
            assertEquals(2, queries.get())
            assertEquals(GenbuStageStatus.BUILDING, fixture.controller.state.history.single().genbuStatus.build)
            assertEquals(null, fixture.controller.state.history.single().genbuStatus.failureReason)
            assertTrue(fixture.errors.isEmpty())
        } finally { scope.cancel() }
    }

    @Test fun `read only workspace inspection can cancel blocked Git and retry without altering history`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val started = CompletableDeferred<Unit>()
        val queries = AtomicInteger()
        val fixture = Fixture(scope, Dispatchers.IO, commands = Commands { command ->
            assertTrue("--no-optional-locks" in command)
            assertEquals(listOf("status", "--porcelain=v1", "--untracked-files=all"), command.takeLast(3))
            if (queries.incrementAndGet() == 1) { started.complete(Unit); Thread.sleep(60_000) }
            CommandResult(0, " M src/Changed.kt", "")
        }, operationState = TagOperationState.CONFLICT)
        try {
            assertTrue(fixture.controller.inspectConflictWorkspace(fixture.task, fixture.operation))
            withTimeout(5_000) { started.await() }
            assertTrue(fixture.runner.cancel())
            withTimeout(5_000) { while (fixture.coordinator.busy) delay(10) }
            assertEquals(null, fixture.controller.workspaceCheck(fixture.operation.operationId))
            assertEquals("操作已取消", fixture.coordinator.statusMessage)
            assertTrue(fixture.controller.inspectConflictWorkspace(fixture.task, fixture.operation))
            withTimeout(5_000) { while (fixture.coordinator.busy) delay(10) }
            assertEquals(listOf("未暂存：src/Changed.kt"), fixture.controller.workspaceCheck(fixture.operation.operationId)?.changes)
            assertEquals(fixture.operation, fixture.store.load(fixture.directory, fixture.operation.operationId))
            assertEquals(2, queries.get())
        } finally { scope.cancel() }
    }

    private fun building() = GenbuTagQueryResult(GenbuStageStatus.BUILDING, GenbuStageStatus.INITIAL, GenbuStageStatus.INITIAL)
}
