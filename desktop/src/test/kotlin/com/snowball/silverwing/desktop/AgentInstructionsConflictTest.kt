package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AgentConflictResolution
import com.snowball.silverwing.core.AgentDocumentConflictException
import com.snowball.silverwing.core.AgentDocumentPropagationService
import com.snowball.silverwing.core.AgentDocumentService
import com.snowball.silverwing.core.AgentFileMonitor
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.DesktopIntegration
import com.snowball.silverwing.core.GroupConfig
import com.snowball.silverwing.core.ManifestStore
import com.snowball.silverwing.core.NoOpTaskOperationLock
import com.snowball.silverwing.core.TaskApplicationService
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

class AgentInstructionsConflictTest {
    @TempDir
    lateinit var root: Path

    @Test
    fun `resolving one file keeps the next pending conflict available`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val controller = fixture.controller
            controller.readGlobal()
            controller.readGroup("group")
            controller.markGlobalEdited("local global")
            controller.markGroupEdited("group", "local group")
            Files.writeString(fixture.paths.globalAgents, "external global")
            Files.writeString(fixture.paths.groupAgents("group"), "external group")

            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertEquals(fixture.paths.globalAgents, controller.state.conflict?.path)

            assertTrue(controller.resolveConflict(AgentConflictResolution.USE_DISK))
            testScheduler.advanceUntilIdle()
            // checkNow suppresses the already pending hash; the controller must
            // retain this conflict independently of another watcher notification.
            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertEquals(fixture.paths.groupAgents("group"), controller.state.conflict?.path)

            assertTrue(controller.resolveConflict(AgentConflictResolution.USE_LOCAL))
            testScheduler.advanceUntilIdle()
            assertEquals("local group", Files.readString(fixture.paths.groupAgents("group")))
            assertNull(controller.state.conflict)
            assertTrue(fixture.errors.isEmpty())
        }
    }

    @Test
    fun `task rule resolution preserves a disk edit made after the conflict appeared`() = runTest {
        val task = task()
        fixture(this, StandardTestDispatcher(testScheduler), listOf(task)).use { fixture ->
            val controller = fixture.controller
            val directory = fixture.taskRoot.resolve(task.taskDirectoryName)
            val root = Files.readString(directory.resolve("AGENTS.md"))
            val file = fixture.documents.taskNotesFile(directory, task)
            controller.readTaskNotes(task)
            controller.markTaskNotesEdited(task, "local notes")
            fixture.documents.writeTaskDocument(directory, task, emptyList(), "first external notes")
            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertEquals(file, controller.state.conflict?.path)

            fixture.documents.writeTaskDocument(directory, task, emptyList(), "second external notes")
            assertTrue(controller.resolveConflict(AgentConflictResolution.USE_LOCAL))
            testScheduler.advanceUntilIdle()

            assertEquals("second external notes", Files.readString(file))
            assertIs<AgentDocumentConflictException>(fixture.errors.single())
            assertEquals("second external notes", controller.state.conflict!!.diskContent)
            assertEquals("local notes", controller.state.conflict!!.localContent)

            // Confirming the refreshed conflict writes only the editable rules file.
            assertTrue(controller.resolveConflict(AgentConflictResolution.USE_LOCAL))
            testScheduler.advanceUntilIdle()
            assertEquals("local notes", Files.readString(file))
            assertEquals(root, Files.readString(directory.resolve("AGENTS.md")))
            assertNull(controller.state.conflict)
            assertEquals(Files.readString(file), fixture.monitor.snapshot(file)?.content)
        }
    }

    @Test
    fun `referenced task rules are monitored and resolved without touching the root router`() = runTest {
        val task = task()
        fixture(this, StandardTestDispatcher(testScheduler), listOf(task)).use { fixture ->
            val controller = fixture.controller
            val directory = fixture.taskRoot.resolve(task.taskDirectoryName)
            val root = Files.readString(directory.resolve("AGENTS.md"))
            val rules = fixture.documents.taskNotesFile(directory, task)
            assertEquals("initial notes", controller.readTaskNotes(task))

            controller.markTaskNotesEdited(task, "local rules")
            fixture.documents.writeTaskDocument(directory, task, emptyList(), "external rules")
            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertEquals(rules, controller.state.conflict?.path)

            assertTrue(controller.resolveConflict(AgentConflictResolution.USE_LOCAL))
            testScheduler.advanceUntilIdle()

            assertEquals("local rules", Files.readString(rules))
            assertEquals(root, Files.readString(directory.resolve("AGENTS.md")))
            assertNull(controller.state.conflict)
            assertTrue(fixture.errors.isEmpty())
        }
    }

    private fun fixture(
        scope: CoroutineScope,
        dispatcher: CoroutineDispatcher,
        taskList: List<TaskManifest> = emptyList(),
    ): Fixture {
        val paths = ApplicationPaths(root.resolve("home"))
        val taskRoot = root.resolve("tasks")
        val config = AppConfig(taskRoot = taskRoot.toString(), groups = listOf(GroupConfig("group", "Group")))
        val session = AppSessionStore(config, taskList)
        val manifests = ManifestStore()
        val documents = AgentDocumentService(paths)
        documents.ensureGlobalFile()
        documents.ensureGroupFile("group")
        taskList.forEach { task ->
            val directory = taskRoot.resolve(task.taskDirectoryName)
            manifests.save(directory, task)
            documents.createTaskDocument(directory, task, emptyList(), "initial notes")
        }
        val errors = mutableListOf<Throwable>()
        val coordinator = OperationCoordinator(onError = errors::add)
        lateinit var controller: AgentInstructionsController
        val monitor = AgentFileMonitor(
            onChange = { change -> scope.launch { controller.handleFileChange(change) } },
            startWatchThread = false,
        )
        controller = AgentInstructionsController(
            session = session,
            paths = paths,
            documents = documents,
            propagation = AgentDocumentPropagationService(manifests, documents, NoOpTaskOperationLock),
            tasks = TaskApplicationService(
                manifests = manifests,
                agentDocuments = documents,
                operationLock = NoOpTaskOperationLock,
            ),
            monitor = monitor,
            operations = OperationRunner(coordinator, scope, dispatcher),
            scope = scope,
            ioDispatcher = dispatcher,
            taskDirectory = { taskRoot.resolve(it.taskDirectoryName) },
            desktopActions = DesktopActions(DesktopIntegration(), { config }, {}, {}, errors::add),
            isBusy = { coordinator.busy },
            showError = errors::add,
            showStatus = {},
        )
        return Fixture(controller, monitor, paths, documents, taskRoot, errors)
    }

    private fun task() = TaskManifest(
        folderName = "task",
        taskDirectoryName = "task",
        featureBranch = "feature/task",
        createdAt = "2026-09-12 00:00:00",
        updatedAt = "2026-09-12 00:00:00",
        services = emptyList(),
        groupId = "group",
    )

    private data class Fixture(
        val controller: AgentInstructionsController,
        val monitor: AgentFileMonitor,
        val paths: ApplicationPaths,
        val documents: AgentDocumentService,
        val taskRoot: Path,
        val errors: List<Throwable>,
    ) : AutoCloseable {
        override fun close() = monitor.close()
    }
}
