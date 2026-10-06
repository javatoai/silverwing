@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class TaskNotesUiTest {
    @TempDir lateinit var root: Path

    @Test fun `repeated save clicks and previews invalidated by metadata or input preserve task drafts`() {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try {
            val paths = ApplicationPaths(root.resolve("home"))
            val tasksRoot = root.resolve("tasks")
            val config = ConfigStore(paths).apply { save(AppConfig(taskRoot = tasksRoot.toString(), aiRequirementNamingEnabled = false)) }
            val tasks = listOf("a", "b").map { name -> TaskManifest(folderName = name, taskDirectoryName = name,
                featureBranch = "feature/$name", createdAt = "now", updatedAt = "now", services = emptyList()) }
            val documents = AgentDocumentService(paths)
            tasks.forEach { task ->
                ManifestStore().save(tasksRoot.resolve(task.taskDirectoryName), task)
                documents.createTaskDocument(tasksRoot.resolve(task.taskDirectoryName), task, emptyList(), "initial ${task.folderName}")
            }
            DesktopApplication(paths = paths, configStore = config, ioDispatcher = io,
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { app ->
                val selected = mutableStateOf(tasks.first())
                ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskNotesPage(app, selected.value) } }
                }.use { scene ->
                    fun pump(runIo: Boolean = true) {
                        if (runIo) io.scheduler.runCurrent()
                        Snapshot.sendApplyNotifications()
                        repeat(4) { scene.render(System.nanoTime()).close() }
                    }
                    fun await(condition: () -> Boolean) {
                        repeat(50) { pump(); if (condition()) return }
                        assertTrue(condition(), "Task notes UI did not reach expected state")
                    }
                    fun text() = scene.nodes().firstOrNull { it.config.getOrNull(SemanticsActions.SetText) != null }
                        ?.config?.getOrNull(SemanticsProperties.EditableText)?.text
                    fun edit(value: String) {
                        val field = scene.nodes().first { it.config.getOrNull(SemanticsActions.SetText) != null }
                        assertTrue(field.config.getOrNull(SemanticsActions.SetText)!!.action!!.invoke(AnnotatedString(value)))
                        pump(false)
                    }
                    await { text() == "initial a" }
                    edit("draft A")
                    scene.click("保存"); pump(false)
                    assertTrue(app.busy)
                    assertTrue(scene.button("保存").config.contains(SemanticsProperties.Disabled))
                    scene.click("保存"); pump(false)
                    await { !app.busy }
                    val pathA = documents.taskNotesFile(tasksRoot.resolve("a"), tasks.first())
                    assertEquals("draft A", Files.readString(pathA))
                    assertFalse(app.agentInstructionsController.notesDraftFor(tasks.first()).dirty)

                    scene.click("预览"); pump(false)
                    assertTrue(scene.button("预览").config.contains(SemanticsProperties.Disabled))
                    // 同一路径的需求上下文已改变，旧请求必须取消，且无需等待旧 IO 就能重试。
                    selected.value = tasks.first().copy(updatedAt = "new requirement context", requirementLink = "https://example.test/updated")
                    pump(false)
                    assertEquals("draft A", text())
                    assertFalse(scene.button("预览").config.contains(SemanticsProperties.Disabled))
                    scene.click("预览"); pump(false)
                    assertTrue(scene.button("预览").config.contains(SemanticsProperties.Disabled))
                    scene.click("预览"); pump(false)
                    edit("A changed during preview")
                    assertFalse(scene.button("预览").config.contains(SemanticsProperties.Disabled))
                    // 不执行排队的 IO，先切换任务，使旧预览的 effect 取消。
                    selected.value = tasks.last(); pump(false)
                    await { text() == "initial b" }
                    edit("draft B")
                    selected.value = tasks.first().copy(updatedAt = "metadata changed"); pump(false)
                    await { text() == "A changed during preview" }
                    assertEquals("draft B", app.agentInstructionsController.notesDraftFor(tasks.last()).notes)
                    assertEquals("draft A", Files.readString(pathA))
                    assertTrue(app.agentInstructionsController.notesDraftFor(tasks.first()).dirty)
                    assertFalse(scene.button("预览").config.contains(SemanticsProperties.Disabled))
                }
            }
        } finally { Dispatchers.resetMain() }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.button(label: String): SemanticsNode = nodes().first {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == label } == true
    }
    private fun ImageComposeScene.click(label: String) {
        val point = button(label).boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
    }
}
