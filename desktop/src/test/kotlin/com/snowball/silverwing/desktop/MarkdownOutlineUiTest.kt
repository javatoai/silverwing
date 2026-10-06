@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.ClipboardManager
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.asAwtTransferable
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.StringSelection
import java.nio.file.Files
import java.nio.file.Path
import kotlin.math.abs
import kotlin.test.*

class MarkdownOutlineUiTest {
    private val content = buildString {
        append("# 阅读指南\n\n## 重复标题\n\n第一段。\n\n")
        repeat(25) { append("导航前的内容 $it，保留正文查找与选择。\n\n") }
        append("## 重复标题\n\n第二个重复标题的正文。\n\n")
        append("Setext 章节\n==============\n\n")
        append("```python\ndef greet():\n    return \"中文\"\n```\n\n")
        repeat(25) { append("导航后的内容 $it。\n\n") }
    }

    @Test
    fun `compact outline selects the correct duplicate in light dark and narrow readers`() {
        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            val reading = MaterialsReadingState()
            val clipboard = MemoryClipboard()
            val find = DocumentFindState()
            ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    CompositionLocalProvider(LocalClipboardManager provides clipboard, LocalClipboard provides clipboard,
                        LocalMaterialsReadingState provides reading) {
                        Surface { DocumentFindScope(find, Modifier.fillMaxSize()) { MarkdownDocumentPreview(content, Modifier.fillMaxSize()) } }
                    }
                }
            }.use { scene ->
                scene.await { scene.heading("重复标题-1") != null }
                val button = scene.label("文档目录")
                assertTrue(button.boundsInRoot.left >= 0 && button.boundsInRoot.right <= width)
                assertTrue(scene.heading("重复标题-1")!!.boundsInRoot.height == 0f, "Second duplicate starts below the viewport")
                scene.click(button)
                scene.await { scene.outlineRows("目录 H2 重复标题").size == 2 }
                val rows = scene.outlineRows("目录 H2 重复标题")
                assertTrue(rows.all { it.boundsInRoot.left >= 0 && it.boundsInRoot.right <= width })
                scene.capture("outline-${if (dark) "dark" else "light"}-$width")
                scene.click(rows[1])
                scene.await {
                    scene.outlineRows("目录 H2 重复标题").isEmpty() &&
                        (reading.scrollPositions["markdown-rendered"] ?: 0) > 500 &&
                        scene.heading("重复标题-1")!!.boundsInRoot.let { it.height > 0 && it.top < 100 }
                }
                assertTrue(scene.heading("重复标题")!!.boundsInRoot.height == 0f, "First duplicate must remain above the selected one")
                scene.capture("outline-target-${if (dark) "dark" else "light"}-$width")
                val savedOffset = reading.scrollPositions["markdown-rendered"]!!
                scene.click(scene.label("查看 Markdown 源码"))
                scene.await { find.blocks.values.any { it.text == content } }
                assertEquals(savedOffset, reading.scrollPositions["markdown-rendered"], "Source mode must preserve the rendered position")
                scene.click(scene.label("查看 Markdown 预览"))
                scene.await { scene.heading("重复标题-1")?.boundsInRoot?.height?.let { it > 0 } == true }
                scene.await { abs((reading.scrollPositions["markdown-rendered"] ?: 0) - savedOffset) < 3 }
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.F, KeyEventType.KeyDown, isCtrlPressed = true)))
                scene.await { find.open }
                find.change("第二个重复标题")
                scene.await { find.hits.size == 1 }
                assertNull(clipboard.capturedText)
            }
        }
    }

    @Test
    fun `outline keyboard selects setext from source and Escape returns toolbar focus`() {
        val reading = MaterialsReadingState().apply { mode.value = MarkdownPreviewMode.SOURCE }
        val clipboard = MemoryClipboard()
        val find = DocumentFindState()
        ImageComposeScene(360, 640, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.DARK) {
                CompositionLocalProvider(LocalClipboardManager provides clipboard, LocalClipboard provides clipboard,
                    LocalMaterialsReadingState provides reading) {
                    Surface { DocumentFindScope(find, Modifier.fillMaxSize()) { MarkdownDocumentPreview(content, Modifier.fillMaxSize()) } }
                }
            }
        }.use { scene ->
            scene.await { scene.labelOrNull("文档目录") != null && find.blocks.values.any { it.text == content } }
            scene.click(scene.label("文档目录"))
            scene.await { scene.outlineRows("目录 H1 Setext 章节").isNotEmpty() }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown)))
            scene.await { scene.outlineRows("目录 H1 Setext 章节").isEmpty() && scene.label("文档目录").config.getOrNull(SemanticsProperties.Focused) == true }
            assertEquals(MarkdownPreviewMode.SOURCE, reading.mode.value)
            scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown))
            scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyUp))
            scene.await { scene.outlineRows("目录 H1 Setext 章节").isNotEmpty() }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.MoveEnd, KeyEventType.KeyDown)))
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown)))
            scene.await { reading.mode.value == MarkdownPreviewMode.RENDERED && scene.heading("setext-章节")?.boundsInRoot?.let { it.height > 0 && it.top < 100 } == true }
            assertTrue(find.blocks.values.any { it.text.contains("def greet()") }, "Rendered code and find registration survive outline navigation")
            scene.click(scene.label("复制代码"))
            assertEquals("def greet():\n    return \"中文\"", clipboard.capturedText?.text)
            scene.capture("outline-keyboard-setext-dark-360")
        }
    }

    @Test
    fun `caller supplied duplicate anchor reaches the same heading as the outline`() {
        val reading = MaterialsReadingState()
        val clipboard = MemoryClipboard()
        ImageComposeScene(640, 480, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                CompositionLocalProvider(LocalClipboardManager provides clipboard, LocalClipboard provides clipboard,
                    LocalMaterialsReadingState provides reading) {
                    Surface {
                        val outline = rememberMarkdownOutlineState(content, "materials-document")
                        Column(Modifier.fillMaxSize()) {
                            MarkdownPreviewToolbarActions(MarkdownPreviewMode.RENDERED, {}, outlineState = outline)
                            MarkdownDocumentPreview(content, MarkdownPreviewMode.RENDERED, Modifier.weight(1f).fillMaxWidth(),
                                headingAnchor = "重复标题-1", outlineState = outline)
                        }
                    }
                }
            }
        }.use { scene ->
            scene.await { (reading.scrollPositions["markdown-rendered"] ?: 0) > 500 &&
                scene.heading("重复标题-1")?.boundsInRoot?.let { it.height > 0 && it.top < 120 } == true }
            assertTrue(scene.heading("重复标题")!!.boundsInRoot.height == 0f)
            assertNull(clipboard.capturedText)
        }
    }

    @Test
    fun `file tabs reset outlines and files without headings expose a disabled action`() {
        val clipboard = MemoryClipboard()
        val files = listOf(MarkdownPreviewFile("AGENTS.md", "# Agent 规则\n\n## 操作范围"), MarkdownPreviewFile("README.md", "普通正文"))
        ImageComposeScene(360, 360, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                CompositionLocalProvider(LocalClipboardManager provides clipboard, LocalClipboard provides clipboard) {
                    Surface { MarkdownFileTabsPreview(files, Modifier.fillMaxSize(), onCopySource = { clipboard.setText(AnnotatedString(it.content)) }) }
                }
            }
        }.use { scene ->
            scene.await { scene.heading("agent-规则") != null }
            scene.click(scene.label("文档目录"))
            scene.await { scene.outlineRows("目录 H1 Agent 规则").isNotEmpty() }
            scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown))
            scene.await { scene.outlineRows("目录 H1 Agent 规则").isEmpty() }
            scene.click(scene.text("README.md")!!)
            scene.await { scene.text("普通正文") != null && scene.label("文档目录").config.contains(SemanticsProperties.Disabled) }
            assertNull(clipboard.capturedText)
        }
    }

    private class MemoryClipboard : ClipboardManager, Clipboard {
        var capturedText: AnnotatedString? = null
        private var entry: ClipEntry? = null
        override val nativeClipboard = java.awt.datatransfer.Clipboard("markdown-outline-test")
        override fun getText() = capturedText
        override fun setText(annotatedString: AnnotatedString) { capturedText = annotatedString }
        override suspend fun getClipEntry(): ClipEntry? = entry ?: capturedText?.let { ClipEntry(StringSelection(it.text)) }
        override suspend fun setClipEntry(clipEntry: ClipEntry?) {
            entry = clipEntry
            val transferable = clipEntry?.asAwtTransferable
            capturedText = (transferable?.getTransferData(DataFlavor.stringFlavor) as? String)?.let(::AnnotatedString)
            if (transferable != null) nativeClipboard.setContents(transferable, null)
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.labelOrNull(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true
    }
    private fun ImageComposeScene.label(value: String) = labelOrNull(value) ?: error("Missing $value")
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.Text)?.any { it.text == value } == true }
    private fun ImageComposeScene.heading(anchor: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.TestTag) == "markdown-heading-$anchor" }
    private fun ImageComposeScene.outlineRows(value: String) = nodes().filter {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true
    }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 8_000_000_000L
        do {
            Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.capture(name: String) {
        repeat(20) { Thread.sleep(10); render(System.nanoTime()).close() }
        val output = Path.of("build/reports/markdown-outline/$name.png")
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
