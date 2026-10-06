@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.asAwtTransferable
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.io.TempDir
import org.jetbrains.skia.EncodedImageFormat
import org.apache.pdfbox.pdmodel.PDDocument
import org.apache.pdfbox.pdmodel.PDPage
import org.apache.pdfbox.pdmodel.PDPageContentStream
import org.apache.pdfbox.pdmodel.font.PDType1Font
import org.apache.pdfbox.pdmodel.font.Standard14Fonts
import java.awt.datatransfer.DataFlavor
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class MaterialsWorkspaceIntegrationTest {
    @TempDir lateinit var temporary: Path

    @Test fun `integrated tabs search rename outline and AI context work in both themes and narrow layout`() {
        for (theme in listOf(ThemePreference.LIGHT, ThemePreference.DARK)) for (width in listOf(540, 1200)) {
            val root = Files.createDirectory(temporary.resolve("$theme-$width"))
            Files.writeString(root.resolve("a.md"), "# Overview\n\n" + (1..40).joinToString("\n\n") { "Paragraph $it of requirement material." } + "\n\n## Target heading\n\nLast section.")
            Files.writeString(root.resolve("b.py"), (1..65).joinToString("\n") { "# line $it" } + "\nprint('needle42')")
            withApp { app, io ->
                val state = MaterialsBrowserState()
                val clipboard = MemoryClipboard()
                ImageComposeScene(width, 700, coroutineContext = Dispatchers.Unconfined) {
                    CompositionLocalProvider(LocalClipboard provides clipboard) {
                        SilverWingTheme(theme) { Surface {
                            RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io,
                                onCopyFiles = { error("No system clipboard file copy expected") },
                                onCopyPaths = { error("No system clipboard path copy expected") },
                                imageClipboard = MaterialsImageClipboard { null }, importPicker = MaterialsImportFilePicker { emptyList() })
                        } }
                    }
                }.use { scene ->
                    scene.await(io) { scene.row("a.md") != null }
                    if (width < 600) { scene.click(scene.row("a.md")!!); scene.await(io) { scene.label("文档目录") != null } }
                    else scene.await(io) { scene.label("文档目录") != null }
                    scene.click(scene.label("固定标签：a.md")!!)
                    scene.await(io) { state.documentTabs.pinnedPaths == listOf("a.md") }
                    scene.key(Key.F, ctrl = true, shift = true)
                    scene.await(io) { scene.tag("materials-full-text-query") != null || scene.nodes().any { it.config.contains(SemanticsActions.SetText) } }
                    scene.nodes().first { it.config.contains(SemanticsActions.SetText) }.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("needle42"))
                    scene.await(io) { scene.nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text.contains("needle42") } == true && it.config.contains(SemanticsProperties.Selected) } }
                    scene.key(Key.Enter)
                    scene.await(io) { state.selectedPath.value == "b.py" && scene.hasText("查找正文") && scene.semanticsOwners.size == 1 }
                    scene.await(io) { (state.readingFor("b.py").scrollPositions["source-vertical"] ?: 0) > 100 }
                    assertEquals(listOf("a.md"), state.documentTabs.pinnedPaths)
                    assertEquals("b.py", state.documentTabs.temporaryPath)
                    assertEquals(MarkdownPreviewMode.SOURCE, state.readingFor("b.py").mode.value)
                    scene.capture("workspace-$theme-$width.png")
                    scene.key(Key.Escape)
                    scene.await(io) { !scene.hasText("查找正文") }
                    scene.label("展开文档列表")?.let { scene.click(it) }
                    scene.await(io) { scene.row("b.py") != null }
                    scene.context(scene.row("b.py")!!)
                    scene.await(io) { scene.button("重命名…") != null }
                    scene.click(scene.button("重命名…")!!)
                    scene.await(io) { scene.tag("materials-entry-name") != null }
                    scene.tag("materials-entry-name")!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("renamed.py"))
                    scene.click(scene.button("重命名")!!)
                    scene.await(io) { Files.exists(root.resolve("renamed.py")) && scene.row("renamed.py") != null }
                    assertEquals("renamed.py", state.selectedPath.value)
                    assertEquals("renamed.py", state.documentTabs.temporaryPath)
                    assertEquals(MarkdownPreviewMode.SOURCE, state.readingFor("renamed.py").mode.value)
                    scene.click(scene.row("renamed.py")!!)
                    scene.await(io) { scene.label("更多资料操作") != null }
                    scene.click(scene.label("更多资料操作")!!)
                    scene.await(io) { scene.button("复制 AI 上下文…") != null }
                    scene.click(scene.button("复制 AI 上下文…")!!)
                    scene.await(io) { scene.button("复制到剪贴板") != null && scene.semanticsOwners.size == 2 }
                    scene.capture("context-$theme-$width.png")
                    scene.click(scene.button("复制到剪贴板")!!)
                    scene.await(io) { clipboard.text != null }
                    assertContains(clipboard.text!!, "needle42")
                    assertContains(clipboard.text!!, "renamed.py")
                    assertTrue(Files.exists(root.resolve("a.md")))
                    assertNull(app.errorMessage)
                }
            }
        }
    }

    @Test fun `browser tab snapshots rename and deletion preserve reading state and empty tab choice`() {
        val session = TaskBrowsingSession()
        val state = session.materialsFor(temporary.resolve("task").toString(), temporary.toString())
        state.retainFiles(linkedSetOf("a.md", "b.py", "c.txt"))
        state.documentTabs.pin("a.md")
        state.documentTabs.openPreview("b.py")
        state.selectedPath.value = "b.py"
        state.readingFor("b.py").scrollPositions["plain"] = 330
        state.renamePath("b.py", "nested/new.py")
        assertEquals(330, state.readingFor("nested/new.py").scrollPositions["plain"])
        val restored = TaskBrowsingSession().apply { restore(session.snapshot()) }.materialsFor(temporary.resolve("task").toString(), temporary.toString())
        assertEquals(listOf("a.md"), restored.documentTabs.pinnedPaths)
        assertEquals("nested/new.py", restored.documentTabs.activePath)
        restored.retainFiles(linkedSetOf("a.md", "c.txt"))
        assertEquals("a.md", restored.selectedPath.value)
        restored.retainFiles(linkedSetOf("c.txt"))
        assertEquals("c.txt", restored.documentTabs.activePath)
        restored.selectedPath.value = restored.documentTabs.close("c.txt")
        restored.retainFiles(linkedSetOf("c.txt"))
        assertNull(restored.selectedPath.value)
        assertTrue(restored.documentTabs.tabs.isEmpty())
    }

    @Test fun `external saves update active preview and directory while preserving reading position`() {
        val root = Files.createDirectory(temporary.resolve("external-materials"))
        val content = (1..90).joinToString("\n") { "Line $it of externally edited material." }
        val file = Files.writeString(root.resolve("a.txt"), content)
        withApp { app, io ->
            val state = MaterialsBrowserState().apply {
                selectedPath.value = "a.txt"
                readingFor("a.txt").apply { mode.value = MarkdownPreviewMode.SOURCE; scrollPositions["source-vertical"] = 300 }
            }
            ImageComposeScene(1200, 700, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io,
                        imageClipboard = MaterialsImageClipboard { null })
                } }
            }.use { scene ->
                scene.await(io) { scene.hasText(content) && state.readingFor("a.txt").scrollPositions["source-vertical"] == 300 }
                Files.writeString(file, "$content\nExternally updated")
                scene.await(io) { scene.hasText("$content\nExternally updated") }
                scene.await(io) { state.readingFor("a.txt").scrollPositions["source-vertical"] == 300 }
                Files.writeString(root.resolve("new.md"), "# New external document")
                scene.await(io) { scene.row("new.md") != null }
                assertEquals("a.txt", state.selectedPath.value)
                scene.capture("external-save.png")
                assertNull(app.errorMessage)
            }
        }
    }

    @Test fun `full text search opens the exact PDF page from a text document`() {
        val root = Files.createDirectory(temporary.resolve("pdf-search-materials"))
        Files.writeString(root.resolve("a.txt"), "Initial text document")
        PDDocument().use { document ->
            repeat(4) { index ->
                val page = PDPage().also(document::addPage)
                PDPageContentStream(document, page).use { stream ->
                    stream.beginText(); stream.setFont(PDType1Font(Standard14Fonts.FontName.HELVETICA), 16f)
                    stream.newLineAtOffset(60f, 700f)
                    stream.showText(if (index == 3) "Exact PDF navigation needlePDF" else "Page ${index + 1}")
                    stream.endText()
                }
            }
            document.save(root.resolve("reference.pdf").toFile())
        }
        withApp { app, io ->
            val state = MaterialsBrowserState()
            ImageComposeScene(1200, 700, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.DARK) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io,
                        imageClipboard = MaterialsImageClipboard { null })
                } }
            }.use { scene ->
                scene.await(io) { scene.label("搜索全部资料 (Ctrl+Shift+F)") != null }
                scene.click(scene.label("搜索全部资料 (Ctrl+Shift+F)")!!)
                scene.await(io) { scene.nodes().any { it.config.contains(SemanticsActions.SetText) } }
                scene.nodes().first { it.config.contains(SemanticsActions.SetText) }.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("needlePDF"))
                scene.await(io) { scene.nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text.contains("needlePDF") } == true && it.config.contains(SemanticsProperties.Selected) } }
                scene.key(Key.Enter)
                scene.await(io) { state.selectedPath.value == "reference.pdf" && scene.hasText("4 / 4 页") && scene.semanticsOwners.size == 1 }
                assertEquals(3, state.readingFor("reference.pdf").listPositions["pdf-pages"]?.index)
                scene.capture("pdf-search-target.png")
                assertNull(app.errorMessage)
            }
        }
    }

    private fun task(root: Path) = TaskManifest(folderName = "资料对照", taskDirectoryName = "资料对照", featureBranch = "feature/materials", createdAt = "now", updatedAt = "now",
        services = emptyList(), requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, root.toString()))

    private inline fun withApp(block: (DesktopApplication, TestDispatcher) -> Unit) {
        val io = StandardTestDispatcher(); Dispatchers.setMain(io)
        try {
            val paths = ApplicationPaths(temporary.resolve("app-${System.nanoTime()}"))
            val tasks = Files.createDirectory(temporary.resolve("tasks-${System.nanoTime()}"))
            val store = ConfigStore(paths).also { it.save(AppConfig(taskRoot = tasks.toString(), aiRequirementNamingEnabled = false)) }
            val opening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication("Reader")
                override suspend fun open(path: Path) = FileOpenResult.Cancelled
                override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
            }
            DesktopApplication(paths = paths, configStore = store, ioDispatcher = io, systemFileOpening = opening,
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { block(it, io) }
        } finally { io.scheduler.runCurrent(); Dispatchers.resetMain() }
    }
    private class MemoryClipboard : Clipboard {
        var value: ClipEntry? = null
        val text get() = value?.asAwtTransferable?.takeIf { it.isDataFlavorSupported(DataFlavor.stringFlavor) }?.getTransferData(DataFlavor.stringFlavor) as? String
        override suspend fun getClipEntry() = value
        override suspend fun setClipEntry(clipEntry: ClipEntry?) { value = clipEntry }
        override val nativeClipboard = java.awt.datatransfer.Clipboard("workspace-test")
    }
    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(n: SemanticsNode): List<SemanticsNode> = listOf(n) + n.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.button(text: String) = nodes().firstOrNull { it.config.contains(SemanticsActions.OnClick) && it.config.getOrNull(SemanticsProperties.Text)?.any { textNode -> textNode.text == text } == true }
    private fun ImageComposeScene.row(text: String) = nodes().firstOrNull { it.config.contains(SemanticsProperties.Selected) && it.config.contains(SemanticsActions.OnClick) && it.config.getOrNull(SemanticsProperties.Text)?.any { textNode -> textNode.text == text } == true && it.config.getOrNull(SemanticsProperties.TestTag)?.startsWith("materials-tab:") != true }
    private fun ImageComposeScene.label(text: String) = nodes().firstOrNull { it.config.contains(SemanticsActions.OnClick) && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(text) == true }
    private fun ImageComposeScene.tag(tag: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.TestTag) == tag }
    private fun ImageComposeScene.hasText(text: String) = nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { t -> t.text == text } == true }
    private fun ImageComposeScene.await(io: TestDispatcher, condition: () -> Boolean) {
        repeat(160) { io.scheduler.runCurrent(); io.scheduler.advanceTimeBy(25); Snapshot.sendApplyNotifications(); repeat(3) { render(System.nanoTime()).close() }; if (condition()) return }
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.context(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Secondary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.key(key: Key, ctrl: Boolean = false, shift: Boolean = false) {
        sendKeyEvent(KeyEvent(key, KeyEventType.KeyDown, isCtrlPressed = ctrl, isShiftPressed = shift))
        sendKeyEvent(KeyEvent(key, KeyEventType.KeyUp, isCtrlPressed = ctrl, isShiftPressed = shift))
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.capture(name: String) {
        repeat(20) { Thread.sleep(8); render(System.nanoTime()).close() }
        val path = Path.of("build/reports/materials-workspace", name); Files.createDirectories(path.parent)
        render(System.nanoTime()).use { image -> image.encodeToData(EncodedImageFormat.PNG)!!.use { Files.write(path, it.bytes) } }
    }
}
