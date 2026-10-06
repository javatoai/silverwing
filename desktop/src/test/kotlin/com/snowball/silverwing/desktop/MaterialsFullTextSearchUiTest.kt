@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.use
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.toPixelMap
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.platform.ClipboardManager
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class MaterialsFullTextSearchUiTest {
    @TempDir lateinit var root: Path
    private val io = StandardTestDispatcher()
    private var clipboardValue: AnnotatedString? = null
    private val clipboard = object : ClipboardManager {
        override fun setText(annotatedString: AnnotatedString) { clipboardValue = annotatedString }
        override fun getText() = clipboardValue
    }

    @Test fun `dialog searches files with keyboard navigation and keeps light dark and narrow layouts usable`() {
        Files.writeString(root.resolve("a.txt"), "第一行\n支付正文")
        Files.writeString(root.resolve("b.kt"), "// 支付代码")
        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            val show = mutableStateOf(true)
            var selected: MaterialsFullTextSearchHit? = null
            var expectedSurface = Color.Unspecified
            ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    expectedSurface = MaterialTheme.colorScheme.surface
                    CompositionLocalProvider(LocalClipboardManager provides clipboard) {
                        if (show.value) MaterialsFullTextSearchDialog(root, { show.value = false }, { selected = it }, io)
                    }
                }
            }.use { scene ->
                scene.await { scene.editable() != null && scene.text("输入文字，查找当前任务的任务资料") != null }
                scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("支付"))
                scene.await { scene.text("2 处匹配 · 2 个文件") != null }
                val content = scene.tag("materials-full-text-search")
                assertTrue(content.boundsInRoot.left >= 0 && content.boundsInRoot.right <= width)
                assertTrue(content.boundsInRoot.top >= 0 && content.boundsInRoot.bottom <= 640)
                scene.screenshot("search-${if (dark) "dark" else "light"}-$width")
                scene.render(System.nanoTime()).use { image ->
                    val pixels = image.toComposeImageBitmap().toPixelMap()
                    // A Dialog owns a separate semantics root. Its local bounds cannot
                    // address the scene's raster, which also includes its placement/scrim.
                    // Require a substantial visible area of the actual theme surface.
                    var themedPixels = 0
                    for (y in 0 until image.height) for (x in 0 until image.width) {
                        val pixel = pixels[x, y]
                        if (pixel.alpha > 0.99f && kotlin.math.abs(pixel.red - expectedSurface.red) <= 1f / 255 &&
                            kotlin.math.abs(pixel.green - expectedSurface.green) <= 1f / 255 &&
                            kotlin.math.abs(pixel.blue - expectedSurface.blue) <= 1f / 255) themedPixels++
                    }
                    assertTrue(themedPixels > width * 640 / 20,
                        "The dialog must visibly inherit the selected app theme: dark=$dark width=$width themedPixels=$themedPixels expected=$expectedSurface")
                }
                scene.await { scene.editable()!!.config.getOrNull(SemanticsProperties.Focused) == true }
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.DirectionDown, KeyEventType.KeyDown)))
                scene.await { scene.tag("materials-search-result-1").config.getOrNull(SemanticsProperties.Selected) == true }
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown)))
                scene.await { !show.value }
                assertEquals("b.kt", selected?.relativePath)
                assertEquals("支付", selected?.query)
                assertEquals(1, selected?.line)
                assertEquals(3, selected?.offset)
            }
        }
        assertNull(clipboardValue, "Search must not touch the user's clipboard")
    }

    @Test fun `loading failure empty and skipped file states expose recovery actions`() {
        val loading = MaterialsFullTextSearchUiState.Loading(MaterialsFullTextSearchProgress(2, 5, "研发/说明.md"))
        var stopped = false
        contentScene(loading, onStop = { stopped = true }).use { scene ->
            scene.await { scene.text("正在搜索 2 / 5 个文件…") != null && scene.text("研发/说明.md") != null }
            scene.click(scene.text("停止搜索")!!)
            assertTrue(stopped)
        }
        var retried = false
        contentScene(MaterialsFullTextSearchUiState.Failed("目录已移动，请检查资料目录。"), onRetry = { retried = true }).use { scene ->
                scene.await { scene.text("无法搜索任务资料") != null && scene.text("目录已移动，请检查资料目录。") != null }
            scene.click(scene.text("重试搜索")!!)
            assertTrue(retried)
        }
        val empty = MaterialsFullTextSearchResult("支付", emptyList(), 2, 2, 0, emptyList(), false)
        contentScene(MaterialsFullTextSearchUiState.Loaded(empty)).use { scene ->
            scene.await { scene.text("没有找到匹配内容") != null }
            assertTrue(scene.text("已搜索 2 个文件。试试更短的文字。") != null)
        }
        val failure = MaterialsFullTextSearchResult("支付", emptyList(), 0, 1, 1,
            listOf(MaterialsFullTextSearchFailure("broken.pdf", "PDF 无法读取，文件可能已损坏。")), false)
        contentScene(MaterialsFullTextSearchUiState.Loaded(failure)).use { scene ->
            scene.await { scene.text("资料读取失败") != null }
            scene.click(scene.text("1 项未能搜索 · 查看原因")!!)
            scene.await { scene.text("broken.pdf：PDF 无法读取，文件可能已损坏。") != null }
        }
    }

    @Test fun `clicking a result preserves PDF page and exact occurrence and Escape closes`() {
        val hit = scanMaterialSearchText("设计/规范.pdf", "支付", "支付规则 支付确认", pageIndex = 3, occurrenceBase = 7)[1]
        val result = MaterialsFullTextSearchResult("支付", listOf(hit), 1, 1, 0, emptyList(), false)
        var selected: MaterialsFullTextSearchHit? = null
        contentScene(MaterialsFullTextSearchUiState.Loaded(result), onNavigate = { selected = it }).use { scene ->
            scene.await { scene.text("第 4 页") != null }
            scene.click(scene.tag("materials-search-result-0"))
            assertEquals(3, selected?.pageIndex)
            assertEquals(8, selected?.occurrenceIndex)
            assertEquals(1, selected?.pageOccurrenceIndex)
            assertEquals(5, selected?.offset)
        }
        var closed = false
        contentScene(MaterialsFullTextSearchUiState.Idle, onDismiss = { closed = true }).use { scene ->
            scene.await { scene.editable()?.config?.getOrNull(SemanticsProperties.Focused) == true }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown)))
            assertTrue(closed)
        }
    }

    private fun contentScene(
        state: MaterialsFullTextSearchUiState,
        onRetry: () -> Unit = {},
        onStop: (() -> Unit)? = null,
        onNavigate: (MaterialsFullTextSearchHit) -> Unit = {},
        onDismiss: () -> Unit = {},
    ) = ImageComposeScene(420, 640, coroutineContext = Dispatchers.Unconfined) {
        SilverWingTheme(ThemePreference.LIGHT) {
            CompositionLocalProvider(LocalClipboardManager provides clipboard) {
                Surface { MaterialsFullTextSearchContent("支付", {}, state, onDismiss, onNavigate, onRetry, Modifier.fillMaxSize(), onStop) }
            }
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.editable() = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.EditableText) != null }
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true }
    private fun ImageComposeScene.tag(value: String) = nodes().first { it.config.getOrNull(SemanticsProperties.TestTag) == value }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 8_000_000_000L
        do {
            io.scheduler.runCurrent()
            Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point: Offset = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.screenshot(name: String) {
        val output = Path.of("build/reports/materials-full-text-search/$name.png")
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
