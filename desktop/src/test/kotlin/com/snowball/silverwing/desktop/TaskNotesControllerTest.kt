@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.async
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class TaskNotesControllerTest {
    @TempDir lateinit var root: Path

    @Test fun `task drafts survive manifest refresh task switch and cancelled preview without writes`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val controller = fixture.controller
            val a = fixture.tasks.first(); val b = fixture.tasks.last()
            controller.loadTaskNotesDraftAsync(a)
            val draftA = controller.notesDraftFor(a)
            controller.markTaskNotesEdited(a, "A 本地草稿\n\n")
            controller.loadTaskNotesDraftAsync(b)
            controller.markTaskNotesEdited(b, "B 本地草稿")
            controller.loadTaskNotesDraftAsync(a.copy(updatedAt = "refreshed metadata"))
            assertSame(draftA, controller.notesDraftFor(a.copy(updatedAt = "another refresh")))
            assertEquals("A 本地草稿\n\n", draftA.notes)
            assertEquals("B 本地草稿", controller.notesDraftFor(b).notes)
            val path = fixture.notesPath(a)
            val before = Files.readString(path)
            val preview = controller.previewTaskAsync(a, draftA.notes)
            assertTrue(preview.files.any { it.relativePath.endsWith(AgentDocumentService.TASK_RULES_FILE_NAME) && "A 本地草稿" in it.content })
            val cancelledPreview = launch { controller.previewTaskAsync(a, draftA.notes) }
            cancelledPreview.cancel()
            testScheduler.runCurrent()
            assertEquals(before, Files.readString(path))
            assertTrue(draftA.dirty)
            assertEquals("A 本地草稿\n\n", draftA.notes)
        }
    }

    @Test fun `generation failure keeps notes dirty and disk resolution replaces only that task draft`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler), failGeneration = true).use { fixture ->
            val controller = fixture.controller
            val a = fixture.tasks.first(); val b = fixture.tasks.last()
            controller.loadTaskNotesDraftAsync(a)
            controller.loadTaskNotesDraftAsync(b)
            controller.markTaskNotesEdited(a, "unsaved A")
            controller.markTaskNotesEdited(b, "unsaved B")
            assertTrue(controller.saveTaskNotes(a, "unsaved A"))
            testScheduler.advanceUntilIdle()
            assertTrue(fixture.errors.any { it.message == "system generation failed" })
            val path = fixture.notesPath(a)
            assertTrue(fixture.monitor.snapshot(path)!!.dirty)
            assertTrue(controller.notesDraftFor(a).dirty)
            Files.writeString(path, "external A")
            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertEquals("external A", controller.state.conflict?.diskContent)
            controller.loadTaskNotesDraftAsync(a)
            assertEquals("unsaved A", controller.notesDraftFor(a).notes)
            assertTrue(controller.resolveConflict(AgentConflictResolution.USE_DISK))
            testScheduler.advanceUntilIdle()
            assertEquals("external A", controller.notesDraftFor(a).notes)
            assertFalse(controller.notesDraftFor(a).dirty)
            assertEquals("unsaved B", controller.notesDraftFor(b).notes)
            assertTrue(controller.notesDraftFor(b).dirty)
            assertEquals("external A", Files.readString(path))
        }
    }

    @Test fun `failed monitor registration retains exact draft across page reload`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val task = fixture.tasks.first()
            val file = fixture.notesPath(task)
            Files.delete(file)
            Files.createDirectory(file)
            fixture.controller.markTaskNotesEdited(task, "typing despite failed monitor\n\n")
            assertTrue(fixture.errors.isNotEmpty())
            fixture.controller.loadTaskNotesDraftAsync(task)
            assertEquals("typing despite failed monitor\n\n", fixture.controller.notesDraftFor(task).notes)
            assertTrue(fixture.controller.notesDraftFor(task).ready)
            assertTrue(fixture.controller.notesDraftFor(task).dirty)
        }
    }

    @Test fun `queued read fills the original path draft when the task root changes`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val task = fixture.tasks.first()
            val original = fixture.controller.notesDraftFor(task)
            val loading = async(start = CoroutineStart.UNDISPATCHED) { fixture.controller.loadTaskNotesDraftAsync(task) }
            val newRoot = root.resolve("new-tasks")
            fixture.documents.createTaskDocument(newRoot.resolve(task.taskDirectoryName), task, emptyList(), "different root notes")
            fixture.session.config = fixture.session.config.copy(taskRoot = newRoot.toString())
            testScheduler.advanceUntilIdle()
            loading.await()
            assertEquals("initial a", original.notes)
            assertTrue(original.loaded)
            assertNotSame(original, fixture.controller.notesDraftFor(task))
            assertFalse(fixture.controller.notesDraftFor(task).loaded)
        }
    }

    @Test fun `queued task preview uses the repository configuration present when requested`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val oldRepository = repository("old")
            fixture.session.config = fixture.session.config.copy(repositories = listOf(oldRepository))
            val preview = async(start = CoroutineStart.UNDISPATCHED) {
                fixture.controller.previewTaskAsync(fixture.tasks.first(), "draft")
            }
            fixture.session.config = fixture.session.config.copy(taskRoot = root.resolve("new-tasks").toString(), repositories = listOf(repository("new")))
            testScheduler.advanceUntilIdle()
            val scope = preview.await().files.first { it.relativePath.endsWith(AgentDocumentService.TASK_CONTEXT_FILE_NAME) }.content
            assertTrue(oldRepository.rootPath in scope)
            assertFalse(repository("new").rootPath in scope)
        }
    }

    @Test fun `queued save keeps its original directory notes and repository snapshot after task switch`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val task = fixture.tasks.first()
            val oldRepository = repository("old")
            fixture.session.config = fixture.session.config.copy(repositories = listOf(oldRepository))
            fixture.controller.markTaskNotesEdited(task, "saved A")
            val draft = fixture.controller.notesDraftFor(task)
            assertTrue(fixture.controller.saveTaskNotes(task, draft.notes))
            fixture.session.selectedTask = fixture.tasks.last()
            val newRoot = root.resolve("new-tasks")
            fixture.documents.createTaskDocument(newRoot.resolve(task.taskDirectoryName), task, emptyList(), "new root untouched")
            fixture.session.config = fixture.session.config.copy(taskRoot = newRoot.toString(), repositories = listOf(repository("new")))
            testScheduler.advanceUntilIdle()
            assertEquals("saved A", Files.readString(fixture.notesPath(task)))
            assertEquals("new root untouched", Files.readString(fixture.documents.taskNotesFile(newRoot.resolve(task.taskDirectoryName), task)))
            assertFalse(draft.dirty)
            val scope = Files.readString(fixture.taskRoot.resolve(task.taskDirectoryName).resolve(".workspace/agent/${AgentDocumentService.TASK_CONTEXT_FILE_NAME}"))
            assertTrue(oldRepository.rootPath in scope)
            assertFalse(repository("new").rootPath in scope)
        }
    }

    @Test fun `partial save cancellation restores later input as a monitored draft`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler), cancelGeneration = true).use { fixture ->
            val task = fixture.tasks.first()
            fixture.controller.markTaskNotesEdited(task, "saved checkpoint")
            assertTrue(fixture.controller.saveTaskNotes(task, "saved checkpoint"))
            fixture.controller.markTaskNotesEdited(task, "later user input\n\n")
            val draft = fixture.controller.notesDraftFor(task)
            val newRoot = root.resolve("new-tasks")
            fixture.documents.createTaskDocument(newRoot.resolve(task.taskDirectoryName), task, emptyList(), "new root preserved")
            fixture.session.config = fixture.session.config.copy(taskRoot = newRoot.toString())
            testScheduler.advanceUntilIdle()
            val path = fixture.notesPath(task)
            assertEquals("saved checkpoint", Files.readString(path))
            assertEquals("later user input\n\n", draft.notes)
            assertTrue(draft.dirty)
            assertEquals("new root preserved", Files.readString(fixture.documents.taskNotesFile(newRoot.resolve(task.taskDirectoryName), task)))
            assertTrue(fixture.monitor.snapshot(path)!!.dirty)
            assertEquals("later user input", fixture.monitor.snapshot(path)?.content)
            Files.writeString(path, "external edit after cancellation")
            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertEquals("external edit after cancellation", fixture.controller.state.conflict?.diskContent)
            assertEquals("later user input", fixture.controller.state.conflict?.localContent)
        }
    }

    @Test fun `queued conflict resolution writes the original task with its original configuration`() = runTest {
        fixture(this, StandardTestDispatcher(testScheduler)).use { fixture ->
            val task = fixture.tasks.first()
            val oldRepository = repository("old")
            fixture.session.config = fixture.session.config.copy(repositories = listOf(oldRepository))
            fixture.controller.markTaskNotesEdited(task, "local conflict notes")
            Files.writeString(fixture.notesPath(task), "external notes")
            fixture.monitor.checkNow()
            testScheduler.advanceUntilIdle()
            assertNotNull(fixture.controller.state.conflict)
            assertTrue(fixture.controller.resolveConflict(AgentConflictResolution.USE_LOCAL))
            val newRoot = root.resolve("new-tasks")
            fixture.documents.createTaskDocument(newRoot.resolve(task.taskDirectoryName), task, emptyList(), "new root untouched")
            fixture.session.config = fixture.session.config.copy(taskRoot = newRoot.toString(), repositories = listOf(repository("new")))
            testScheduler.advanceUntilIdle()
            assertEquals("local conflict notes", Files.readString(fixture.notesPath(task)))
            assertEquals("new root untouched", Files.readString(fixture.documents.taskNotesFile(newRoot.resolve(task.taskDirectoryName), task)))
            assertNull(fixture.controller.state.conflict)
            val scope = Files.readString(fixture.taskRoot.resolve(task.taskDirectoryName).resolve(".workspace/agent/${AgentDocumentService.TASK_CONTEXT_FILE_NAME}"))
            assertTrue(oldRepository.rootPath in scope)
        }
    }

    private fun repository(name: String) = RepositoryConfig(name, "$name repository", root.resolve("$name-repository").toString(), root.resolve("$name-repository/.git").toString())

    private fun fixture(scope: CoroutineScope, dispatcher: CoroutineDispatcher, failGeneration: Boolean = false, cancelGeneration: Boolean = false): Fixture {
        val paths = ApplicationPaths(root.resolve("home"))
        val taskRoot = root.resolve("tasks")
        val config = AppConfig(taskRoot = taskRoot.toString(), groups = listOf(GroupConfig("group", "Group")))
        val taskList = listOf("a", "b").map { name -> TaskManifest(folderName = name, taskDirectoryName = name,
            featureBranch = "feature/$name", createdAt = "now", updatedAt = "now", services = emptyList(), groupId = "group") }
        val manifests = ManifestStore()
        val documents = AgentDocumentService(paths)
        taskList.forEach { task ->
            val directory = taskRoot.resolve(task.taskDirectoryName)
            manifests.save(directory, task)
            documents.createTaskDocument(directory, task, emptyList(), "initial ${task.folderName}")
        }
        val taskDocuments = if (!failGeneration && !cancelGeneration) documents else object : AgentDocuments by documents {
            override fun writeTaskDocument(taskDirectory: Path, manifest: TaskManifest, repositories: List<RepositoryInfo>, taskNotes: String?): Path =
                if (cancelGeneration) throw CancellationException("system generation cancelled") else error("system generation failed")
        }
        val errors = mutableListOf<Throwable>()
        val coordinator = OperationCoordinator(onError = errors::add)
        val session = AppSessionStore(config, taskList)
        lateinit var controller: AgentInstructionsController
        val monitor = AgentFileMonitor({ change -> scope.launch { controller.handleFileChange(change) } }, startWatchThread = false)
        val actions = DesktopActions(DesktopIntegration(), { config }, {}, {}, errors::add)
        controller = AgentInstructionsController(session = session, paths = paths, documents = documents,
            propagation = AgentDocumentPropagationService(manifests, documents, NoOpTaskOperationLock),
            tasks = TaskApplicationService(manifests = manifests, agentDocuments = taskDocuments, operationLock = NoOpTaskOperationLock),
            monitor = monitor, operations = OperationRunner(coordinator, scope, dispatcher), scope = scope, ioDispatcher = dispatcher,
            taskDirectory = { Path.of(session.config.taskRoot!!).resolve(it.taskDirectoryName) }, desktopActions = actions, isBusy = { coordinator.busy },
            showError = errors::add, showStatus = {})
        return Fixture(controller, monitor, documents, taskRoot, taskList, errors, actions, session)
    }

    private class Fixture(val controller: AgentInstructionsController, val monitor: AgentFileMonitor,
        val documents: AgentDocumentService, val taskRoot: Path, val tasks: List<TaskManifest>, val errors: List<Throwable>,
        val actions: DesktopActions, val session: AppSessionStore) : AutoCloseable {
        fun notesPath(task: TaskManifest): Path = documents.taskNotesFile(taskRoot.resolve(task.taskDirectoryName), task)
        override fun close() { monitor.close(); actions.close() }
    }
}
