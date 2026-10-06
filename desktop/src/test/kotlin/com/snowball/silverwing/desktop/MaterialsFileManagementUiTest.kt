@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import java.awt.image.BufferedImage
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class MaterialsFileManagementUiTest {
    @TempDir lateinit var temporary: Path

    @Test fun `name rename and conflict dialogs work in light dark and narrow layouts without native clipboard access`() {
        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            val io = StandardTestDispatcher()
            Dispatchers.setMain(io)
            try {
                val root = Files.createDirectory(temporary.resolve("root-$dark-$width"))
                val source = Files.writeString(temporary.resolve("source-$dark-$width.md"), "incoming")
                Files.writeString(root.resolve(source.fileName), "existing")
                val results = mutableListOf<MaterialsFileOperationResult.Completed>()
                val errors = mutableListOf<String>()
                var clipboardReads = 0
                lateinit var state: MaterialsFileManagementState
                val clipboard = MaterialsImageClipboard { clipboardReads++; BufferedImage(2, 2, BufferedImage.TYPE_INT_ARGB) }
                ImageComposeScene(width, 650, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                        Surface {
                            state = rememberMaterialsFileManagement(root, "", { results += it }, { errors += it }, io,
                                clipboard = clipboard)
                            MaterialsFileManagementDropZone(state, Modifier.fillMaxSize()) {
                                Column {
                                    TextButton(onClick = state::createMarkdown) { Text("创建 Markdown") }
                                    TextButton(onClick = { state.rename("中文 说明.md") }) { Text("改名资料") }
                                    TextButton(onClick = { state.importFiles(listOf(source)) }) { Text("导入样本") }
                                    TextButton(onClick = state::pasteImage) { Text("粘贴图片") }
                                }
                            }
                            MaterialsFileManagementDialogs(state)
                        }
                    }
                }.use { scene ->
                    scene.await(io) { scene.button("创建 Markdown") != null }
                    assertEquals(0, clipboardReads)
                    scene.click(scene.button("创建 Markdown")!!)
                    scene.await(io) { scene.button("创建") != null }
                    scene.setField("../outside")
                    scene.click(scene.button("创建")!!)
                    scene.await(io) { state.nameError != null }
                    assertNotNull(state.nameRequest)
                    assertFalse(Files.exists(temporary.resolve("outside.md")))
                    scene.setField("中文 说明")
                    scene.click(scene.button("创建")!!)
                    scene.await(io) { results.size == 1 && state.nameRequest == null && !state.busy && scene.semanticsOwners.size == 1 }
                    assertEquals("中文 说明.md", results.single().relativePath)

                    scene.click(scene.button("改名资料")!!)
                    scene.await(io) { scene.button("重命名") != null }
                    scene.setField("改名 说明.md")
                    scene.click(scene.button("重命名")!!)
                    scene.await(io) { results.size == 2 && !state.busy && scene.semanticsOwners.size == 1 }
                    assertEquals("中文 说明.md", results.last().previousRelativePath)
                    assertEquals("改名 说明.md", results.last().relativePath)

                    scene.click(scene.button("导入样本")!!)
                    scene.await(io) { scene.button("另存") != null }
                    assertEquals("existing", Files.readString(root.resolve(source.fileName)))
                    assertEquals(0, clipboardReads)
                    scene.capture("conflict-$dark-$width.png")
                    scene.setField("导入 副本.md")
                    scene.click(scene.button("另存")!!)
                    scene.await(io) { results.size == 3 && state.conflict == null && !state.busy && scene.semanticsOwners.size == 1 }
                    assertEquals("incoming", Files.readString(root.resolve("导入 副本.md")))
                    assertEquals("existing", Files.readString(root.resolve(source.fileName)))

                    scene.click(scene.button("粘贴图片")!!)
                    scene.await(io) { clipboardReads == 1 && results.size == 4 && !state.busy }
                    assertTrue(results.last().relativePath.endsWith(".png"))
                    assertTrue(errors.isEmpty(), errors.toString())
                }
            } finally { Dispatchers.resetMain() }
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.button(text: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true
    }
    private fun ImageComposeScene.setField(value: String) {
        val field = nodes().first { it.config.getOrNull(SemanticsActions.SetText) != null }
        assertTrue(field.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString(value)))
        Snapshot.sendApplyNotifications()
        repeat(4) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Primary)
        repeat(4) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.await(io: TestDispatcher, condition: () -> Boolean) {
        repeat(80) {
            io.scheduler.runCurrent()
            Snapshot.sendApplyNotifications()
            repeat(4) { render(System.nanoTime()).close() }
            if (condition()) return
        }
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.capture(name: String) {
        val path = Path.of("build/reports/materials-file-management", name)
        Files.createDirectories(path.parent)
        render(System.nanoTime()).use { image ->
            image.encodeToData(org.jetbrains.skia.EncodedImageFormat.PNG)!!.use { Files.write(path, it.bytes) }
        }
    }
}
