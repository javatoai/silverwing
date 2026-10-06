@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class MaterialsDeletionUiTest {
    @TempDir lateinit var temporary: Path

    @Test fun `cancelled context deletion preserves exact file and retry deletes only the selected spaced name`() {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try {
            for (dark in listOf(false, true)) {
                val root = Files.createDirectory(temporary.resolve(if (dark) "dark" else "light"))
                val selected = Files.writeString(root.resolve(" 说明.txt"), "spaced document")
                val sibling = Files.writeString(root.resolve("说明.txt"), "ordinary document")
                val paths = ApplicationPaths(temporary.resolve("app-$dark"))
                val config = ConfigStore(paths).also { it.save(AppConfig(aiRequirementNamingEnabled = false)) }
                val trash = object : SystemFileTrash {
                    val calls = mutableListOf<List<Path>>()
                    var cancel = true
                    var gate: CompletableDeferred<Unit>? = CompletableDeferred()
                    override suspend fun moveToTrash(paths: List<Path>): List<FileTrashResult> {
                        calls += paths
                        gate?.await()
                        return paths.map { path ->
                            if (cancel) FileTrashResult(path, cancelled = true)
                            else { Files.delete(path); FileTrashResult(path) }
                        }
                    }
                }
                val opening = object : SystemFileOpening {
                    override val chooserAvailable = false
                    override suspend fun defaultApplication(path: Path) = DefaultFileApplication("WPS")
                    override suspend fun open(path: Path) = FileOpenResult.Submitted
                    override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
                }
                DesktopApplication(paths = paths, configStore = config, ioDispatcher = io, systemFileTrash = trash,
                    systemFileOpening = opening,
                    developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { app ->
                    val state = MaterialsBrowserState()
                    val task = TaskManifest(folderName = "资料操作", taskDirectoryName = "资料操作", featureBranch = "feature/materials",
                        createdAt = "now", updatedAt = "now", services = emptyList(),
                        requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, root.toString()))
                    ImageComposeScene(1100, 620, coroutineContext = Dispatchers.Unconfined) {
                        SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface {
                            RequirementMaterialsBrowser(app, task, Modifier.fillMaxSize(), state, io)
                        } }
                    }.use { scene ->
                        fun await(condition: () -> Boolean) {
                            val deadline = System.nanoTime() + 5_000_000_000L
                            do {
                                io.scheduler.runCurrent(); Snapshot.sendApplyNotifications()
                                repeat(3) { scene.render(System.nanoTime()).close() }
                                if (condition()) return
                                Thread.sleep(10)
                            } while (System.nanoTime() < deadline)
                            assertTrue(condition(), scene.nodes().joinToString { it.config.toString() })
                        }
                        fun requestDelete() {
                            await { scene.text("文档（2）") != null }
                            val point = scene.row(" 说明.txt")!!.boundsInRoot.center
                            scene.sendPointerEvent(PointerEventType.Move, point)
                            scene.sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
                            scene.sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Secondary)
                            await { scene.button("删除") != null }
                            scene.click(scene.button("删除")!!)
                            await { scene.button("移入回收站") != null }
                            scene.click(scene.button("移入回收站")!!)
                        }
                        await { scene.text(" 说明.txt") != null && scene.text("spaced document") != null }
                        assertEquals(" 说明.txt", state.selectedPath.value)
                        requestDelete()
                        await { trash.calls.size == 1 && scene.button("正在删除…") != null }
                        assertTrue(scene.button("正在删除…")!!.config.contains(SemanticsProperties.Disabled))
                        scene.click(scene.button("正在删除…")!!)
                        assertEquals(listOf(selected), trash.calls.single())
                        trash.gate!!.complete(Unit)
                        await { app.statusMessage == "已取消移入回收站" && scene.button("正在删除…") == null }
                        assertNull(app.errorMessage)
                        assertTrue(Files.exists(selected)); assertTrue(Files.exists(sibling))
                        assertEquals(" 说明.txt", state.selectedPath.value)
                        assertEquals(1, trash.calls.size)

                        trash.cancel = false; trash.gate = null
                        requestDelete()
                        await { trash.calls.size == 2 && state.selectedPath.value == "说明.txt" && scene.text("ordinary document") != null }
                        assertFalse(Files.exists(selected))
                        assertEquals("ordinary document", Files.readString(sibling))
                        assertEquals("1 项已移入回收站", app.statusMessage)
                        assertNull(app.errorMessage)
                        val output = Path.of("build/reports/material-file-actions/${if (dark) "dark" else "light"}-deletion-retry.png")
                        Files.createDirectories(output.parent)
                        scene.render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
                    }
                }
            }
        } finally { Dispatchers.resetMain() }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }
    private fun ImageComposeScene.row(value: String) = nodes().firstOrNull {
        it.config.contains(SemanticsProperties.Selected) &&
            it.config.getOrNull(SemanticsActions.OnClick) != null &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }
    private fun ImageComposeScene.button(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Primary)
    }
}
