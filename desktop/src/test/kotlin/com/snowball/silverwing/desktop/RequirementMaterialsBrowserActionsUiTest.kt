@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class RequirementMaterialsBrowserActionsUiTest {
    @TempDir lateinit var temporary: Path

    @Test fun `path actions validate in the background and keep their captured selection while browsing remains usable`() {
        for (dark in listOf(false, true)) for (width in listOf(600, 1100)) withBrowser(dark) { app, io ->
            val root = Files.createDirectory(temporary.resolve("background-$dark-$width"))
            val first = Files.writeString(root.resolve("a.txt"), "first document")
            val second = Files.writeString(root.resolve("b.txt"), "second document")
            val payloads = mutableListOf<List<Path>>()
            val state = MaterialsBrowserState()
            ImageComposeScene(width, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io,
                        onCopyFiles = { error("Unexpected file copy") }, onCopyPaths = { payloads += it })
                } }
            }.use { scene ->
                scene.await(io) { scene.row("a.txt") != null }
                scene.context(scene.row("a.txt")!!)
                scene.await(io) { scene.button("复制路径") != null }
                scene.click(scene.button("复制路径")!!)
                assertTrue(payloads.isEmpty(), "The pointer handler must yield before filesystem validation")
                assertTrue(scene.hasText("正在检查所选项…"))
                scene.capture("checking-$dark-$width.png")
                if (width == 1100) scene.click(scene.row("b.txt")!!)
                scene.await(io) { payloads.size == 1 && !scene.hasText("正在检查所选项…") }
                assertEquals(listOf(first), payloads.single(), "Changing the active file cannot retarget an earlier copy")

                // The confirmation also waits for fresh background validation, rather than trusting the catalog.
                scene.context(scene.row("b.txt")!!)
                scene.await(io) { scene.button("删除") != null }
                scene.click(scene.button("删除")!!)
                assertNull(scene.button("移入回收站"))
                Files.delete(second)
                scene.await(io) { app.errorMessage?.contains("b.txt") == true && !scene.hasText("正在检查所选项…") }
                assertNull(scene.button("移入回收站"))
                assertEquals(listOf(listOf(first)), payloads)
            }
        }
    }

    @Test fun `root changes and refresh cancel pending path actions without old clipboard payloads or errors`() {
        withBrowser(false) { app, io ->
            val firstRoot = Files.createDirectory(temporary.resolve("pending-first"))
            val secondRoot = Files.createDirectory(temporary.resolve("pending-second"))
            val first = Files.writeString(firstRoot.resolve("same.txt"), "old document")
            val second = Files.writeString(secondRoot.resolve("same.txt"), "new document")
            val browserState = MaterialsBrowserState()
            val current = mutableStateOf(task(firstRoot))
            val payloads = mutableListOf<List<Path>>()
            ImageComposeScene(1100, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, current.value, Modifier.fillMaxSize(), browserState, io,
                        onCopyFiles = { error("Unexpected file copy") }, onCopyPaths = { payloads += it })
                } }
            }.use { scene ->
                fun copyPath() {
                    scene.context(scene.row("same.txt")!!)
                    scene.await(io) { scene.button("复制路径") != null }
                    scene.click(scene.button("复制路径")!!)
                    assertTrue(scene.hasText("正在检查所选项…"))
                }
                scene.await(io) { scene.hasText("old document") }
                copyPath()
                Files.delete(first)
                current.value = task(secondRoot)
                Snapshot.sendApplyNotifications()
                repeat(4) { scene.render(System.nanoTime()).close() }
                scene.await(io) { scene.hasText("new document") }
                assertTrue(payloads.isEmpty())
                assertNull(app.errorMessage)
                copyPath()
                scene.await(io) { payloads.size == 1 }
                assertEquals(listOf(second), payloads.single())

                copyPath()
                Files.delete(second)
                scene.click(scene.label("刷新任务资料目录"))
                scene.await(io) { scene.row("same.txt") == null && !scene.hasText("正在检查所选项…") }
                assertEquals(1, payloads.size)
                assertNull(app.errorMessage)
            }
        }
    }

    @Test fun `copy paths includes selected descendants while file references deduplicate and refresh preserves an explicit empty selection`() {
        for (dark in listOf(false, true)) withBrowser(dark) { app, io ->
            val root = Files.createDirectories(temporary.resolve("materials-$dark"))
            val directory = Files.createDirectory(root.resolve("notes"))
            val child = Files.writeString(directory.resolve("a.txt"), "selected child")
            val sibling = Files.writeString(root.resolve("z.txt"), "fallback sibling")
            val state = MaterialsBrowserState().apply {
                expansionInitialized.value = true
                expandedDirectories.value = setOf("notes")
            }
            val pathPayloads = mutableListOf<List<Path>>()
            val filePayloads = mutableListOf<List<Path>>()
            ImageComposeScene(1100, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io,
                        onCopyFiles = { filePayloads += it }, onCopyPaths = { pathPayloads += it })
                } }
            }.use { scene ->
                scene.await(io) { scene.row("a.txt") != null && scene.hasText("selected child") }
                assertTrue(scene.row("a.txt")!!.selected())
                scene.click(scene.row("notes")!!, ctrl = true)
                scene.await(io) { scene.row("notes")!!.selected() && scene.row("a.txt")!!.selected() }

                scene.context(scene.row("a.txt")!!)
                scene.await(io) { scene.button("复制路径") != null }
                scene.click(scene.button("复制路径")!!)
                scene.await(io) { pathPayloads.size == 1 && scene.semanticsOwners.size == 1 }
                assertEquals(listOf(directory, child), pathPayloads.single())
                scene.context(scene.row("a.txt")!!)
                scene.await(io) { scene.button("复制所选项") != null }
                scene.click(scene.button("复制所选项")!!)
                scene.await(io) { filePayloads.size == 1 && scene.semanticsOwners.size == 1 }
                assertEquals(listOf(directory), filePayloads.single())

                scene.click(scene.label("刷新任务资料目录"))
                scene.await(io) { scene.row("a.txt") != null && scene.hasText("selected child") }
                assertTrue(scene.row("notes")!!.selected())
                assertTrue(scene.row("a.txt")!!.selected())
                scene.click(scene.row("a.txt")!!)
                scene.click(scene.row("a.txt")!!, ctrl = true)
                scene.await(io) { !scene.row("a.txt")!!.selected() && !scene.row("notes")!!.selected() }
                scene.click(scene.label("刷新任务资料目录"))
                scene.await(io) { scene.row("a.txt") != null && scene.hasText("selected child") }
                assertFalse(scene.row("a.txt")!!.selected(), "Refresh must preserve an explicit Ctrl-clear")
                assertFalse(scene.row("notes")!!.selected())
                assertEquals("notes/a.txt", state.selectedPath.value, "Reading and action selection are independent")

                // Only an owned TempDir fixture is removed; invalid selection still repairs to a valid file.
                scene.click(scene.row("a.txt")!!)
                Files.delete(child)
                scene.click(scene.label("刷新任务资料目录"))
                scene.await(io) { scene.row("a.txt") == null && scene.hasText("fallback sibling") }
                assertEquals("z.txt", state.selectedPath.value)
                assertTrue(scene.row("z.txt")!!.selected())
                scene.context(scene.row("z.txt")!!)
                scene.await(io) { scene.button("复制路径") != null }
                scene.click(scene.button("复制路径")!!)
                scene.await(io) { pathPayloads.size == 2 }
                assertEquals(listOf(sibling), pathPayloads.last())
            }
        }
    }

    @Test fun `a reused browser state closes an old root confirmation and submitted recycling keeps its captured root`() {
        val trash = RecordingTrash()
        withBrowser(false, trash) { app, io ->
            val firstRoot = Files.createDirectory(temporary.resolve("first"))
            val secondRoot = Files.createDirectory(temporary.resolve("second"))
            val first = Files.writeString(firstRoot.resolve("same.txt"), "first root document")
            val second = Files.writeString(secondRoot.resolve("same.txt"), "second root document")
            val state = MaterialsBrowserState()
            val current = mutableStateOf(task(firstRoot))
            ImageComposeScene(1100, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    // This is the supported reusable-state component boundary; the task page normally supplies a new state.
                    RequirementMaterialsBrowser(app, current.value, Modifier.fillMaxSize(), state, io,
                        onCopyFiles = { error("Unexpected clipboard request") }, onCopyPaths = { error("Unexpected clipboard request") })
                } }
            }.use { scene ->
                fun requestDelete() {
                    scene.context(scene.row("same.txt")!!)
                    scene.await(io) { scene.button("删除") != null }
                    scene.click(scene.button("删除")!!)
                    scene.await(io) { scene.button("移入回收站") != null }
                }
                scene.await(io) { scene.hasText("first root document") }
                requestDelete()
                current.value = task(secondRoot)
                scene.await(io) { scene.hasText("second root document") && scene.button("移入回收站") == null }
                assertTrue(trash.calls.isEmpty())
                requestDelete()
                trash.failure = "recycle temporarily unavailable"
                scene.click(scene.button("移入回收站")!!)
                scene.await(io) { trash.calls.size == 1 && scene.button("正在删除…") == null }
                assertEquals(listOf(second), trash.calls.single())
                assertTrue(app.errorMessage.orEmpty().contains("recycle temporarily unavailable"))
                assertEquals("second root document", Files.readString(second))

                trash.failure = null
                current.value = task(firstRoot)
                scene.await(io) { scene.hasText("first root document") }
                requestDelete()
                trash.gate = CompletableDeferred()
                scene.click(scene.button("移入回收站")!!)
                scene.await(io) { trash.calls.size == 2 && scene.button("正在删除…") != null }
                current.value = task(secondRoot)
                scene.await(io) { scene.hasText("second root document") && scene.button("正在删除…") == null }
                trash.gate!!.complete(Unit)
                scene.await(io) { trash.completed == 2 }
                assertEquals(listOf(second), trash.calls[0])
                assertEquals(listOf(first), trash.calls[1])
                assertEquals("first root document", Files.readString(first))
                assertEquals("second root document", Files.readString(second))
                assertEquals("已取消移入回收站", app.statusMessage)
                assertFalse(scene.row("same.txt") == null)
            }
        }
    }

    @Test fun `sorting menu reorders visible files preserves selection and refreshes modification times`() {
        for (dark in listOf(false, true)) for (width in listOf(420, 1100)) withBrowser(dark) { app, io ->
            val root = Files.createDirectory(temporary.resolve("sort-$dark-$width"))
            val first = Files.writeString(root.resolve("a.txt"), "first document")
            val second = Files.writeString(root.resolve("b.txt"), "second document")
            Files.setLastModifiedTime(first, java.nio.file.attribute.FileTime.fromMillis(1_700_000_000_000L))
            Files.setLastModifiedTime(second, java.nio.file.attribute.FileTime.fromMillis(1_700_000_010_000L))
            val state = MaterialsBrowserState()
            ImageComposeScene(width, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io)
                } }
            }.use { scene ->
                scene.await(io) { scene.row("a.txt") != null && scene.row("b.txt") != null }
                assertTrue(scene.row("a.txt")!!.boundsInRoot.top < scene.row("b.txt")!!.boundsInRoot.top)
                val selected = state.selectedPath.value
                scene.click(scene.label("排序：按名称"))
                scene.await(io) { scene.button("最近修改") != null }
                repeat(4) { Thread.sleep(50); scene.render(System.nanoTime()).close() }
                scene.capture("sorting-menu-$dark-$width.png")
                scene.click(scene.button("最近修改")!!)
                scene.await(io) {
                    state.sortOrder.value == MaterialsSortOrder.MODIFIED_DESC && scene.semanticsOwners.size == 1 &&
                        scene.row("b.txt")!!.boundsInRoot.top < scene.row("a.txt")!!.boundsInRoot.top
                }
                assertEquals(selected, state.selectedPath.value)
                scene.capture("sorting-recent-$dark-$width.png")
                repeat(4) { Thread.sleep(50); scene.render(System.nanoTime()).close() }
                Files.setLastModifiedTime(first, java.nio.file.attribute.FileTime.fromMillis(1_700_000_020_000L))
                scene.click(scene.label("刷新任务资料目录"))
                scene.await(io) { scene.row("a.txt") != null && scene.row("b.txt") != null &&
                    scene.row("a.txt")!!.boundsInRoot.top < scene.row("b.txt")!!.boundsInRoot.top }
                assertEquals(MaterialsSortOrder.MODIFIED_DESC, state.sortOrder.value)
                assertEquals(selected, state.selectedPath.value)
                scene.click(scene.label("排序：最近修改"))
                scene.await(io) { scene.button("按名称") != null }
                scene.click(scene.button("按名称")!!)
                scene.await(io) { state.sortOrder.value == MaterialsSortOrder.NAME }
            }
        }
    }

    private inline fun withBrowser(dark: Boolean, trash: SystemFileTrash = RecordingTrash(), block: (DesktopApplication, TestDispatcher) -> Unit) {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try {
            val paths = ApplicationPaths(temporary.resolve("app-$dark-${System.nanoTime()}"))
            val tasks = Files.createDirectories(temporary.resolve("tasks-$dark-${System.nanoTime()}"))
            val store = ConfigStore(paths).also { it.save(AppConfig(taskRoot = tasks.toString(), aiRequirementNamingEnabled = false)) }
            val opening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication("Reader")
                override suspend fun open(path: Path) = FileOpenResult.Cancelled
                override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
            }
            DesktopApplication(paths = paths, configStore = store, ioDispatcher = io, systemFileOpening = opening,
                systemFileTrash = trash,
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use {
                block(it, io)
            }
        } finally { io.scheduler.runCurrent(); Dispatchers.resetMain() }
    }

    private fun task(root: Path) = TaskManifest(folderName = "资料操作", taskDirectoryName = "资料操作",
        featureBranch = "feature/materials", createdAt = "now", updatedAt = "now", services = emptyList(),
        requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, root.toString()))

    private class RecordingTrash : SystemFileTrash {
        val calls = mutableListOf<List<Path>>()
        var gate: CompletableDeferred<Unit>? = null
        var failure: String? = null
        var completed = 0
        override suspend fun moveToTrash(paths: List<Path>): List<FileTrashResult> {
            calls += paths
            gate?.await()
            completed++
            return paths.map { if (failure != null) FileTrashResult(it, error = failure) else FileTrashResult(it, cancelled = true) }
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.button(text: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true
    }
    private fun ImageComposeScene.row(text: String) = nodes().firstOrNull {
        it.config.contains(SemanticsProperties.Selected) && it.config.getOrNull(SemanticsActions.OnClick) != null &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true
    }
    private fun ImageComposeScene.label(label: String) = nodes().first {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true
    }
    private fun ImageComposeScene.hasText(text: String) = nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true }
    private fun ImageComposeScene.capture(name: String) {
        val output = Path.of("build/reports/materials-actions", name)
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image ->
            image.encodeToData(org.jetbrains.skia.EncodedImageFormat.PNG)!!.use { Files.write(output, it.bytes) }
        }
    }
    private fun SemanticsNode.selected() = config.getOrNull(SemanticsProperties.Selected) == true
    private fun ImageComposeScene.await(io: TestDispatcher, condition: () -> Boolean) {
        repeat(80) {
            io.scheduler.runCurrent(); Snapshot.sendApplyNotifications()
            repeat(4) { render(System.nanoTime()).close() }
            if (condition()) return
        }
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode, ctrl: Boolean = false) {
        val point = node.boundsInRoot.center
        val modifiers = PointerKeyboardModifiers(isCtrlPressed = ctrl)
        sendPointerEvent(PointerEventType.Move, point, keyboardModifiers = modifiers)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), keyboardModifiers = modifiers, button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, keyboardModifiers = modifiers, button = PointerButton.Primary)
        repeat(4) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.context(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Secondary)
        repeat(4) { render(System.nanoTime()).close() }
    }
}
