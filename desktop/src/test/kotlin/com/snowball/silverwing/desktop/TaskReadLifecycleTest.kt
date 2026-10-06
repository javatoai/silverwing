@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestCoroutineScheduler
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withContext
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class TaskReadLifecycleTest {
    @TempDir lateinit var temporary: Path

    @Test fun `branch source cancellation is idle and ordinary failure can be retried`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        var calls = 0
        val latest = TaskBranchCatalogResult(listOf(TaskBranchCandidate("feature/latest", listOf("github"), 1, 1)))
        val catalog = catalog {
            when (++calls) { 1 -> throw CancellationException("cancelled"); 2 -> error("无法读取分支"); else -> latest }
        }
        try {
            application(ui, catalog = catalog).use { app ->
                app.taskController.loadTaskBranchCandidates("group", setOf("service")); runCurrent()
                assertEquals(TaskBranchCandidatesState.Idle, app.taskController.branchCandidates)
                app.taskController.loadTaskBranchCandidates("group", setOf("service")); runCurrent()
                assertEquals("无法读取分支", assertIs<TaskBranchCandidatesState.Failed>(app.taskController.branchCandidates).message)
                app.taskController.loadTaskBranchCandidates("group", setOf("service")); runCurrent()
                assertEquals(latest, assertIs<TaskBranchCandidatesState.Loaded>(app.taskController.branchCandidates).result)
                assertFalse(app.busy)
                assertNull(app.errorMessage)
            }
        } finally { Dispatchers.resetMain() }
    }

    @Test fun `replaced branch result and delayed progress never override the latest request`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        val old = CompletableDeferred<Unit>()
        var calls = 0
        val catalog = object : TaskBranchCatalog {
            override suspend fun list(config: AppConfig, groupId: String, serviceIds: Set<String>, onProgress: (TaskBranchCatalogProgress) -> Unit): TaskBranchCatalogResult {
                val call = ++calls
                if (call == 1) withContext(NonCancellable) {
                    old.await()
                    onProgress(TaskBranchCatalogProgress(99, 99))
                }
                return TaskBranchCatalogResult(listOf(TaskBranchCandidate("request-$call", emptyList(), 1, 1)))
            }
        }
        try {
            application(ui, catalog = catalog).use { app ->
                app.taskController.loadTaskBranchCandidates("group", setOf("service")); runCurrent()
                app.taskController.loadTaskBranchCandidates("group", setOf("service")); runCurrent()
                assertEquals("request-2", assertIs<TaskBranchCandidatesState.Loaded>(app.taskController.branchCandidates).result.candidates.single().branch)
                old.complete(Unit); runCurrent()
                assertEquals("request-2", assertIs<TaskBranchCandidatesState.Loaded>(app.taskController.branchCandidates).result.candidates.single().branch)
            }
        } finally { old.complete(Unit); Dispatchers.resetMain() }
    }

    @Test fun `queued reads and shortcut command leave no loading or busy state after application close`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        var branches = 0
        var risks = 0
        var commands = 0
        val command = WorkspaceCommandConfig("command", "测试命令", executable = "fixture.exe")
        val runner = object : StreamingCommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) = error("Unexpected command")
            override fun runStreaming(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>, onOutput: (CommandOutputLine) -> Unit): CommandResult {
                commands++; return CommandResult(0, "", "")
            }
        }
        try {
            val app = application(ui, catalog = catalog { branches++; TaskBranchCatalogResult(emptyList()) }, riskRead = { risks++; emptyList() }, command = command, runner = runner)
            val task = app.tasks.first()
            app.taskController.loadTaskBranchCandidates("group", setOf("service"))
            app.taskController.loadBatchGitPreviews(task)
            app.taskController.cancelBatchGitPreviews()
            assertEquals(BatchGitPreviewState.Idle, app.taskController.batchGitPreviews)
            app.taskController.loadBatchGitPreviews(task)
            app.taskController.requestDeleteRisk(task)
            assertTrue(app.taskController.runWorkspaceCommand(task, task.services.single(), command))
            assertTrue(app.taskController.workspaceCommandRunning)
            app.close(); runCurrent()
            assertEquals(0, branches); assertEquals(0, risks); assertEquals(0, commands)
            assertEquals(TaskBranchCandidatesState.Idle, app.taskController.branchCandidates)
            assertEquals(BatchGitPreviewState.Idle, app.taskController.batchGitPreviews)
            assertFalse(app.taskController.workspaceCommandRunning)
            assertEquals(WorkspaceCommandExecutionStatus.CANCELLED, app.taskController.workspaceCommandState?.status)
            assertTrue(app.deleteRiskInspections.values.all { !it.loading && it.error != null })
            app.taskController.loadTaskBranchCandidates("group", setOf("service"))
            app.taskController.loadBatchGitPreviews(task)
            app.taskController.requestDeleteRisk(task)
            assertEquals(TaskBranchCandidatesState.Idle, app.taskController.branchCandidates)
            assertEquals(BatchGitPreviewState.Idle, app.taskController.batchGitPreviews)
            assertTrue(app.deleteRiskInspections.values.all { !it.loading && it.error != null })
        } finally { Dispatchers.resetMain() }
    }

    @Test fun `cancelled delete source becomes a visible failure and can be retried`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        var calls = 0
        try {
            application(ui, riskRead = { if (++calls == 1) throw CancellationException("cancelled"); emptyList() }).use { app ->
                val task = app.tasks.first()
                app.taskController.requestDeleteRisk(task); runCurrent()
                val cancelled = app.deleteRiskInspections.getValue(task.taskDirectoryName)
                assertFalse(cancelled.loading)
                assertContains(assertNotNull(cancelled.error), "重新检查")
                app.taskController.requestDeleteRisk(task); runCurrent()
                val retried = app.deleteRiskInspections.getValue(task.taskDirectoryName)
                assertFalse(retried.loading)
                assertNull(retried.error)
                assertEquals(2, calls)
                assertNull(app.errorMessage)
            }
        } finally { Dispatchers.resetMain() }
    }

    @Test fun `dismissing delete inspection interrupts it and reopening uses a fresh result`() {
        val ui = StandardTestDispatcher()
        Dispatchers.setMain(ui)
        val started = CountDownLatch(1)
        val interrupted = CountDownLatch(1)
        val release = CountDownLatch(1)
        val calls = AtomicInteger()
        val newest = DeleteRisk("newest", false, false, true, null)
        try {
            application(Dispatchers.IO, riskRead = {
                if (calls.incrementAndGet() == 1) {
                    started.countDown()
                    try { check(release.await(5, TimeUnit.SECONDS)) }
                    catch (cancelled: InterruptedException) { interrupted.countDown(); throw cancelled }
                }
                listOf(newest)
            }).use { app ->
                val task = app.tasks.first()
                app.taskController.requestDeleteRisk(task)
                await(ui.scheduler) { started.count == 0L }
                app.taskController.clearDeleteRisk(task)
                assertTrue(app.deleteRiskInspections.isEmpty())
                app.taskController.requestDeleteRisk(task)
                await(ui.scheduler) { interrupted.count == 0L && app.deleteRiskInspections[task.taskDirectoryName]?.loading == false }
                assertEquals(listOf(newest), app.deleteRiskInspections.getValue(task.taskDirectoryName).risks)
                assertEquals(2, calls.get())
                assertFalse(app.busy)
            }
        } finally { release.countDown(); Dispatchers.resetMain() }
    }

    @Test fun `A to B to A cancels old Git preview and publishes only the newest fingerprint`() {
        val ui = StandardTestDispatcher()
        Dispatchers.setMain(ui)
        val started = CountDownLatch(1)
        val interrupted = CountDownLatch(1)
        val release = CountDownLatch(1)
        val heads = AtomicInteger()
        val reader = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                val root = Path.of(command[command.indexOf("-C") + 1])
                val args = command.drop(command.indexOf("-C") + 2)
                val output = when {
                    "--show-toplevel" in args -> root.toString()
                    "--git-common-dir" in args -> temporary.resolve("repository/.git").toAbsolutePath().normalize().toString()
                    "--absolute-git-dir" in args -> root.resolve(".git").toString()
                    args == listOf("branch", "--show-current") -> "feature/a"
                    args == listOf("config", "user.name") -> "Fixture"
                    args == listOf("config", "user.email") -> "fixture@example.invalid"
                    "@{upstream}" in args -> "origin/feature/a"
                    args.firstOrNull() == "rev-list" -> "0"
                    args == listOf("rev-parse", "HEAD") -> {
                        val call = heads.incrementAndGet()
                        if (call == 1) {
                            started.countDown()
                            try { check(release.await(5, TimeUnit.SECONDS)) }
                            catch (cancelled: InterruptedException) { interrupted.countDown(); throw cancelled }
                        }
                        "head-$call"
                    }
                    args.firstOrNull() in setOf("status", "diff") -> ""
                    else -> error("Unexpected Git command: $args")
                }
                return CommandResult(0, output, "")
            }
        }
        try {
            application(Dispatchers.IO, gitOperations = WorkspaceGitOperationService(GitClient(reader))).use { app ->
                val a = app.tasks.first { it.taskDirectoryName == "a" }
                val b = app.tasks.first { it.taskDirectoryName == "b" }
                app.selectTask(a)
                app.taskController.loadBatchGitPreviews(a)
                await(ui.scheduler) { started.count == 0L }
                app.selectTask(b); app.selectTask(a)
                app.taskController.loadBatchGitPreviews(a)
                await(ui.scheduler) { interrupted.count == 0L && app.taskController.batchGitPreviews is BatchGitPreviewState.Loaded }
                val latest = assertIs<BatchGitPreviewState.Loaded>(app.taskController.batchGitPreviews).previews.values.single()
                assertEquals("head-2", latest.head)
                assertFalse(app.busy)
                assertNull(app.errorMessage)
            }
        } finally { release.countDown(); Dispatchers.resetMain() }
    }

    private fun catalog(read: suspend () -> TaskBranchCatalogResult) = object : TaskBranchCatalog {
        override suspend fun list(config: AppConfig, groupId: String, serviceIds: Set<String>, onProgress: (TaskBranchCatalogProgress) -> Unit) = read()
    }

    private fun application(io: CoroutineDispatcher, catalog: TaskBranchCatalog = catalog { TaskBranchCatalogResult(emptyList()) },
        riskRead: () -> List<DeleteRisk> = { emptyList() }, command: WorkspaceCommandConfig? = null,
        runner: StreamingCommandRunner = ProcessCommandRunner(), gitOperations: WorkspaceGitOperationService = WorkspaceGitOperationService()): DesktopApplication {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val taskRoot = temporary.resolve("tasks")
        val repository = Files.createDirectories(temporary.resolve("repository"))
        val module = ServiceModuleConfig("module", "main", customCommands = listOfNotNull(command))
        val config = AppConfig(taskRoot = taskRoot.toString(), aiRequirementNamingEnabled = false,
            repositories = listOf(RepositoryConfig("repository", "fixture", repository.toString(), repository.resolve(".git").toString())),
            groups = listOf(GroupConfig("group", "Group", services = listOf(GroupServiceConfig("service", "repository", "fixture", modules = listOf(module))))))
        val store = ConfigStore(paths).also { it.save(config) }
        val manifests = ManifestStore()
        listOf("a", "b").forEach { name ->
            val workspacePath = Files.createDirectories(taskRoot.resolve(name).resolve("workspace"))
            val task = TaskManifest(folderName = name, taskDirectoryName = name, featureBranch = "feature/$name", createdAt = "now", updatedAt = "now", groupId = "group",
                services = listOf(ServiceWorkspace("repository", "fixture", repository.toString(), workspacePath.toString(), DevelopmentToolType.INTELLIJ_IDEA,
                    "feature/$name", groupServiceId = "service", moduleId = "module", health = WorkspaceHealth.READY)))
            manifests.save(taskRoot.resolve(name), task)
        }
        val lifecycle = object : WorkspaceLifecycle by GitWorkspaceLifecycle() {
            override fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) = riskRead()
        }
        val tasks = TaskApplicationService(manifests = manifests, lifecycle = lifecycle, operationLock = NoOpTaskOperationLock)
        return DesktopApplication(paths = paths, configStore = store, manifests = manifests, tasksApplication = tasks, taskBranchCatalog = catalog,
            gitOperationService = gitOperations, ioDispatcher = io, workspaceCommandService = WorkspaceCommandService(runner),
            gitStatusService = WorkspaceGitStatusService(WorkspaceGitStatusReader { WorkspaceGitHealth(WorkspaceGitHealthState.READY) }),
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
    }

    private fun await(scheduler: TestCoroutineScheduler, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        do { scheduler.runCurrent(); if (condition()) return; Thread.sleep(10) } while (System.nanoTime() < deadline)
        assertTrue(condition(), "Task reader did not settle")
    }
}
