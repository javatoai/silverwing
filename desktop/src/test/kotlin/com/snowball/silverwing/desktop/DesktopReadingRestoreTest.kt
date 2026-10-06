@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.*
import java.nio.file.Path
import java.nio.file.Files
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class DesktopReadingRestoreTest {
    @TempDir lateinit var root: Path
    private val dispatcher = StandardTestDispatcher()
    @org.junit.jupiter.api.BeforeEach fun controlUi() { Dispatchers.setMain(dispatcher) }
    @org.junit.jupiter.api.AfterEach fun restoreUi() { Dispatchers.resetMain() }
    @Test fun `application restart restores archived task and document reading without retaining file content`() {
        val taskRoot = root.resolve("tasks")
        val materials = Files.createDirectory(root.resolve("研发资料"))
        val paths = ApplicationPaths(root.resolve("application"))
        val store = ConfigStore(paths).apply { save(AppConfig(taskRoot = taskRoot.toString(), aiRequirementNamingEnabled = false)) }
        val task = TaskManifest(folderName = "已归档任务", taskDirectoryName = "已归档任务", featureBranch = "payment_fix",
            createdAt = "now", updatedAt = "now", services = emptyList(), lifecycleStatus = TaskLifecycleStatus.ARCHIVED,
            requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, materials.toString()))
        val manifests = ManifestStore(); manifests.save(taskRoot.resolve(task.taskDirectoryName), task)
        fun open() = DesktopApplication(paths = paths, configStore = store,
            ioDispatcher = dispatcher,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
            .also { dispatcher.scheduler.runCurrent() }
        open().use { app ->
            app.selectTask(task); app.taskBrowsingSession.view = TaskContentView.MATERIALS
            val browser = app.taskBrowsingSession.materialsFor(app.taskPath(task), materials.toString())
            browser.selectedPath.value = "方案.pdf"; browser.expandedDirectories.value = setOf("研发")
            browser.compactShowingPreview.value = true
            browser.readingFor("方案.pdf").apply {
                zoomPercent.intValue = 175; listPositions["pdf-pages"] = MaterialsListPosition(3, 22)
                scrollPositions["pdf-horizontal"] = 80
            }
            browser.readingFor("说明.md").apply { mode.value = MarkdownPreviewMode.SOURCE; scrollPositions["source-vertical"] = 540 }
        }
        open().use { app ->
            val restored = assertNotNull(app.selectedTask)
            assertEquals(task.folderName, restored.folderName)
            assertEquals(NavigationItem.ARCHIVED, app.navigation)
            assertEquals(TaskContentView.MATERIALS, app.taskBrowsingSession.view)
            val browser = app.taskBrowsingSession.materialsFor(app.taskPath(restored), restored.requirementMaterials.writeRoot)
            assertEquals("方案.pdf", browser.selectedPath.value)
            assertTrue(browser.compactShowingPreview.value)
            assertEquals(175, browser.readingFor("方案.pdf").zoomPercent.intValue)
            assertEquals(MaterialsListPosition(3, 22), browser.readingFor("方案.pdf").listPositions["pdf-pages"])
            assertEquals(80, browser.readingFor("方案.pdf").scrollPositions["pdf-horizontal"])
            assertEquals(MarkdownPreviewMode.SOURCE, browser.readingFor("说明.md").mode.value)
            assertEquals(540, browser.readingFor("说明.md").scrollPositions["source-vertical"])
        }
        val newRoot = Files.createDirectory(root.resolve("新目录"))
        manifests.save(taskRoot.resolve(task.taskDirectoryName), task.copy(requirementMaterials = task.requirementMaterials.copy(writeRoot = newRoot.toString())))
        open().use { app ->
            val restored = assertNotNull(app.selectedTask)
            val browser = app.taskBrowsingSession.materialsFor(app.taskPath(restored), restored.requirementMaterials.writeRoot)
            assertNull(browser.selectedPath.value)
            assertFalse(browser.compactShowingPreview.value)
        }
    }

    @Test fun `user task and tab selection before cache read finishes wins over restored state`() {
        val taskRoot = Files.createDirectory(root.resolve("tasks"))
        val tasks = listOf("first", "second").map { name -> TaskManifest(folderName = name, taskDirectoryName = name,
            featureBranch = "feat/$name", createdAt = "now", updatedAt = "now", services = emptyList()) }
        val manifests = ManifestStore()
        tasks.forEach { manifests.save(taskRoot.resolve(it.taskDirectoryName), it) }
        val paths = ApplicationPaths(root.resolve("application"))
        val config = ConfigStore(paths).apply { save(AppConfig(taskRoot = taskRoot.toString(), aiRequirementNamingEnabled = false)) }
        val cache = ReadingStateStore(paths.cache.resolve("reading-state.json"))
        cache.save(ReadingSnapshot(lastTaskPath = taskRoot.resolve("second").toString(), view = "MATERIALS"))
        DesktopApplication(paths = paths, configStore = config, ioDispatcher = dispatcher,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { app ->
            app.selectTask(tasks.first())
            app.taskBrowsingSession.view = TaskContentView.NOTES
            dispatcher.scheduler.runCurrent()
            assertEquals("first", app.selectedTask?.folderName)
            assertEquals(TaskContentView.NOTES, app.taskBrowsingSession.view)
        }
        assertEquals("NOTES", cache.load().view)
        assertEquals(taskRoot.resolve("first").toString(), cache.load().lastTaskPath)
        assertEquals(taskRoot.toString(), config.load().taskRoot)
    }
}
