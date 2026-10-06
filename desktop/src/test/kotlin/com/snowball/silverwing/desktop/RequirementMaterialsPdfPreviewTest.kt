@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.PointerKeyboardModifiers
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import org.apache.pdfbox.pdmodel.PDDocument
import org.apache.pdfbox.pdmodel.PDPage
import org.apache.pdfbox.pdmodel.common.PDRectangle
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class RequirementMaterialsPdfPreviewTest {
    private val ioDispatcher = StandardTestDispatcher()
    @TempDir
    lateinit var root: Path

    @Test
    fun `preview pages zoom and toolbar work in narrow and wide windows in both themes`() {
        writePreviewPdf(root.resolve("two.pdf"))
        for (dark in listOf(false, true)) for (width in listOf(320, 900)) {
            ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { RequirementMaterialsPdfPreview(root, "two.pdf", 0, ioDispatcher = ioDispatcher) }
                }
            }.use { scene ->
                scene.awaitText("1 / 2 页")
                scene.awaitPage("PDF 第 1 页，共 2 页")
                assertFalse(scene.enabled("上一页"))
                assertTrue(scene.enabled("下一页"))
                for (label in listOf("上一页", "下一页", "放大", "缩小")) {
                    val bounds = scene.button(label).boundsInRoot
                    assertTrue(bounds.left >= 0 && bounds.right <= width && bounds.bottom < 140,
                        "$label must fit width=$width: $bounds")
                }
                val firstWidth = scene.page().boundsInRoot.width
                scene.click("下一页")
                scene.awaitText("2 / 2 页")
                scene.awaitPage("PDF 第 2 页，共 2 页")
                assertFalse(scene.enabled("下一页"))
                assertTrue(scene.enabled("上一页"))
                scene.click("放大")
                scene.awaitText("125%")
                scene.awaitPage("PDF 第 2 页，共 2 页")
                assertTrue(scene.page("PDF 第 2 页，共 2 页").boundsInRoot.width > firstWidth)
                scene.clickText("适应宽度")
                scene.awaitText("100%")
                scene.awaitText("2 / 2 页")
                scene.awaitPage("PDF 第 2 页，共 2 页")
                scene.click("上一页")
                scene.awaitText("1 / 2 页")
                scene.awaitPage("PDF 第 1 页，共 2 页")
                scene.sendPointerEvent(PointerEventType.Move, Offset(-100f, -100f))
                // 集中输出同一批实际 Compose 页面，供界面检查，不打开用户桌面窗口。
                val output = Path.of("build/reports/pdf-preview/${if (dark) "dark" else "light"}-$width.png")
                Files.createDirectories(output.parent)
                scene.render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
            }
        }
    }

    @Test
    fun `mouse wheel reads consecutive pages and scrolls back without pressing page buttons`() {
        PDDocument().use { document ->
            repeat(5) { document.addPage(PDPage(PDRectangle(300f, 420f))) }
            document.save(root.resolve("continuous.pdf").toFile())
        }
        ImageComposeScene(320, 420, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                Surface { RequirementMaterialsPdfPreview(root, "continuous.pdf", 0, ioDispatcher = ioDispatcher) }
            }
        }.use { scene ->
            scene.awaitPage("PDF 第 1 页，共 5 页")
            scene.awaitText("1 / 5 页")
            fun wheel(amount: Float) {
                scene.sendPointerEvent(PointerEventType.Move, Offset(160f, 260f))
                scene.sendPointerEvent(PointerEventType.Scroll, Offset(160f, 260f), scrollDelta = Offset(0f, amount))
            }
            wheel(500f)
            scene.await { scene.nodes().none { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "1 / 5 页" } == true } }
            repeat(8) { wheel(500f); repeat(3) { scene.render(System.nanoTime()).close() } }
            scene.awaitText("5 / 5 页")
            scene.awaitPage("PDF 第 5 页，共 5 页")
            assertFalse(scene.enabled("下一页"))
            val output = Path.of("build/reports/pdf-preview/continuous-wheel.png")
            Files.createDirectories(output.parent)
            scene.render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
            repeat(8) { wheel(-500f); repeat(3) { scene.render(System.nanoTime()).close() } }
            scene.awaitText("1 / 5 页")
            scene.awaitPage("PDF 第 1 页，共 5 页")
            assertFalse(scene.enabled("上一页"))
        }
    }

    @Test
    fun `Ctrl wheel zooms without page scrolling and respects limits in both themes`() {
        PDDocument().use { document ->
            repeat(5) { document.addPage(PDPage(PDRectangle(300f, 420f))) }
            document.save(root.resolve("ctrl-wheel.pdf").toFile())
        }
        for (dark in listOf(false, true)) {
            ImageComposeScene(480, 520, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { RequirementMaterialsPdfPreview(root, "ctrl-wheel.pdf", 0, ioDispatcher = ioDispatcher) }
                }
            }.use { scene ->
                scene.awaitPage("PDF 第 1 页，共 5 页")
                scene.click("下一页")
                scene.awaitText("2 / 5 页")
                scene.awaitPage("PDF 第 2 页，共 5 页")
                fun wheel(amount: Float, ctrl: Boolean = true, position: Offset = Offset(200f, 300f)) {
                    val modifiers = PointerKeyboardModifiers(isCtrlPressed = ctrl)
                    scene.sendPointerEvent(PointerEventType.Move, position, keyboardModifiers = modifiers)
                    scene.sendPointerEvent(PointerEventType.Scroll, position, scrollDelta = Offset(0f, amount), keyboardModifiers = modifiers)
                }
                val originalTop = scene.page("PDF 第 2 页，共 5 页").boundsInRoot.top
                wheel(-500f)
                scene.awaitText("125%")
                scene.awaitText("2 / 5 页")
                scene.awaitPage("PDF 第 2 页，共 5 页")
                assertEquals(originalTop, scene.page("PDF 第 2 页，共 5 页").boundsInRoot.top, 1f)
                wheel(500f)
                scene.awaitText("100%")
                scene.awaitText("2 / 5 页")
                // 横向滚动及预览区外的 Ctrl 滚轮不应修改 PDF 的缩放比例。
                scene.sendPointerEvent(PointerEventType.Scroll, Offset(200f, 300f), scrollDelta = Offset(500f, 0f),
                    keyboardModifiers = PointerKeyboardModifiers(isCtrlPressed = true))
                wheel(-500f, position = Offset(200f, 20f))
                scene.awaitText("100%")
                for (percent in listOf(125, 150, 175, 200)) {
                    wheel(-500f)
                    scene.awaitText("$percent%")
                }
                wheel(-500f)
                scene.awaitText("200%")
                scene.awaitText("2 / 5 页")
                assertFalse(scene.enabled("放大"))
                for (percent in listOf(175, 150, 125, 100, 75, 50, 25)) {
                    wheel(500f)
                    scene.awaitText("$percent%")
                }
                scene.await { scene.nodes().any { it.config.getOrNull(SemanticsProperties.ContentDescription)?.any { text -> text.startsWith("PDF 第 ") } == true } }
                val lowZoomTop = scene.page().boundsInRoot.top
                wheel(500f)
                scene.awaitText("25%")
                assertEquals(lowZoomTop, scene.page().boundsInRoot.top, 1f)
                assertFalse(scene.enabled("缩小"))
                scene.clickText("适应宽度")
                scene.awaitText("100%")
                repeat(4) { if (scene.enabled("上一页")) scene.click("上一页") }
                scene.awaitText("1 / 5 页")
                wheel(500f, ctrl = false)
                scene.await { scene.nodes().none { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "1 / 5 页" } == true } }
                scene.awaitText("100%")
            }
        }
    }

    @Test
    fun `retry refresh and switching documents keep errors recoverable and reset page state`() {
        Files.writeString(root.resolve("retry.pdf"), "broken PDF")
        PDDocument().use {
            it.addPage(PDPage(PDRectangle(200f, 200f)))
            it.save(root.resolve("single.pdf").toFile())
        }
        val selected = mutableStateOf("retry.pdf")
        val revision = mutableStateOf(0)
        ImageComposeScene(480, 520, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                Surface { RequirementMaterialsPdfPreview(root, selected.value, revision.value, ioDispatcher = ioDispatcher) }
            }
        }.use { scene ->
            scene.awaitText("无法预览 PDF")
            assertFalse(scene.enabled("下一页"))
            writePreviewPdf(root.resolve("retry.pdf"))
            scene.clickText("重试")
            scene.awaitPage("PDF 第 1 页，共 2 页")
            scene.click("下一页")
            scene.awaitText("2 / 2 页")
            scene.awaitPage("PDF 第 2 页，共 2 页")
            scene.click("放大")
            scene.awaitText("125%")
            scene.awaitPage("PDF 第 2 页，共 2 页")
            selected.value = "single.pdf"
            scene.awaitPage("PDF 第 1 页，共 1 页")
            scene.awaitText("100%")
            assertFalse(scene.enabled("上一页"))
            assertFalse(scene.enabled("下一页"))
            // 同名文件被替换后，刷新重新读取页数，不显示旧的缓存图片。
            writePreviewPdf(root.resolve("single.pdf"))
            revision.value++
            scene.awaitText("1 / 2 页")
            scene.awaitPage("PDF 第 1 页，共 2 页")
            assertTrue(scene.enabled("下一页"))
        }
    }

    @Test
    fun `loading and refreshing preserve saved PDF page and horizontal position until the real layout restores them`() {
        PDDocument().use { document ->
            repeat(5) { document.addPage(PDPage(PDRectangle(300f, 420f))) }
            document.save(root.resolve("restored.pdf").toFile())
        }
        val reading = MaterialsReadingState().apply {
            zoomPercent.intValue = 150
            listPositions["pdf-pages"] = MaterialsListPosition(3, 28)
            scrollPositions["pdf-horizontal"] = 75
        }
        val revision = mutableStateOf(0)
        ImageComposeScene(480, 520, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                CompositionLocalProvider(LocalMaterialsReadingState provides reading) {
                    Surface { RequirementMaterialsPdfPreview(root, "restored.pdf", revision.value, ioDispatcher = ioDispatcher) }
                }
            }
        }.use { scene ->
            // Keep document I/O queued while the initial loading layout is measured.
            repeat(3) { scene.render(System.nanoTime()).close() }
            assertEquals(MaterialsListPosition(3, 28), reading.listPositions["pdf-pages"])
            assertEquals(75, reading.scrollPositions["pdf-horizontal"])
            scene.awaitText("4 / 5 页")
            scene.awaitPage("PDF 第 4 页，共 5 页")
            assertEquals(3, reading.listPositions["pdf-pages"]?.index)
            assertEquals(75, reading.scrollPositions["pdf-horizontal"])
            revision.value++
            Snapshot.sendApplyNotifications()
            repeat(3) { scene.render(System.nanoTime()).close() }
            assertEquals(3, reading.listPositions["pdf-pages"]?.index)
            assertEquals(75, reading.scrollPositions["pdf-horizontal"])
            scene.awaitText("4 / 5 页")
            scene.awaitPage("PDF 第 4 页，共 5 页")
            assertEquals(3, reading.listPositions["pdf-pages"]?.index)
            assertEquals(75, reading.scrollPositions["pdf-horizontal"])
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun descend(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::descend)
        return semanticsOwners.flatMap { descend(it.rootSemanticsNode) }
    }

    private fun ImageComposeScene.button(label: String): SemanticsNode = nodes().first {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true &&
            it.config.getOrNull(SemanticsActions.OnClick) != null
    }

    private fun ImageComposeScene.enabled(label: String): Boolean = !button(label).config.contains(SemanticsProperties.Disabled)

    private fun ImageComposeScene.page(description: String? = null): SemanticsNode = nodes().first {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.any { text ->
            if (description == null) text.startsWith("PDF 第 ") else text == description
        } == true
    }

    private fun ImageComposeScene.awaitText(text: String) = await {
        nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { item -> item.text == text } == true }
    }

    private fun ImageComposeScene.awaitPage(description: String) = await {
        nodes().any { it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(description) == true }
    }

    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val deadline = System.nanoTime() + 10_000_000_000L
        do {
            ioDispatcher.scheduler.runCurrent()
            Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < deadline)
        assertTrue(condition(), "Preview did not reach expected state: " + nodes().map { it.config })
    }

    private fun ImageComposeScene.click(label: String) {
        assertTrue(enabled(label), "$label must be enabled")
        clickAt(button(label).boundsInRoot.center)
    }

    private fun ImageComposeScene.clickText(text: String) {
        val node = nodes().first {
            it.config.getOrNull(SemanticsProperties.Text)?.any { item -> item.text == text } == true &&
                it.config.getOrNull(SemanticsActions.OnClick) != null
        }
        clickAt(node.boundsInRoot.center)
    }

    private fun ImageComposeScene.clickAt(point: Offset) {
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point,
            buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
}
