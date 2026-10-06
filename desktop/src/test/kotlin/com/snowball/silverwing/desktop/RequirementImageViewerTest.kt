@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.painter.ColorPainter
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.*
import androidx.compose.ui.use
import kotlinx.coroutines.Dispatchers
import kotlin.test.*

class RequirementImageViewerTest {
    @Test fun `zoom stays finite with invalid layout measurements and preserves large image original size`() {
        for (fit in listOf(0f, -1f, Float.NaN, Float.POSITIVE_INFINITY)) {
            assertEquals(1f, boundedImageZoom(Float.NaN, fit))
            assertEquals(8f, boundedImageZoom(Float.POSITIVE_INFINITY, fit))
        }
        assertEquals(1f, fittedImageScale(Size(2_000f, 1_000f), Size.Zero))
        assertEquals(1f, fittedImageScale(Size.Unspecified, Size(500f, 400f)))
        assertEquals(0.25f, fittedImageScale(Size(2_000f, 1_000f), Size(500f, 400f)))
        assertEquals(100f, boundedImageZoom(100f, 0.01f))
    }

    @Test fun `pan is clamped to image overflow and centered again at fit`() {
        val image = Size(2_000f, 1_000f)
        val viewport = Size(500f, 400f)
        assertEquals(Offset(250f, -50f), boundedImageOffset(Offset(900f, -300f), 2f, 0.25f, image, viewport))
        assertEquals(Offset.Zero, boundedImageOffset(Offset(250f, -50f), 1f, 0.25f, image, viewport))
        assertEquals(Offset.Zero, boundedImageOffset(Offset(Float.NaN, Float.POSITIVE_INFINITY), 2f, 0.25f, image, viewport))
        assertEquals(Offset.Zero, boundedImageOffset(Offset(900f, -300f), 2f, 0.25f, image, Size.Zero))
    }

    @Test fun `image viewer has keyboard zoom fit and close while narrow toolbar stays inside the window`() {
        for (width in listOf(220, 900)) {
            var closed = false
            ImageComposeScene(width, 480, coroutineContext = Dispatchers.Unconfined) {
                MaterialTheme {
                    RequirementImageViewerContent(ColorPainter(Color(0xFFCCDDEE)), { closed = true }, Modifier.fillMaxSize())
                }
            }.use { scene ->
                scene.await { scene.text("100%") != null }
                for (label in listOf("关闭图片", "放大图片", "缩小图片")) {
                    val bounds = scene.nodes().single { it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true }.boundsInRoot
                    assertTrue(bounds.left >= 0f && bounds.right <= width, "$label exceeds width $width: $bounds")
                }
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.Plus, KeyEventType.KeyDown)))
                scene.await { scene.text("125%") != null }
                scene.sendPointerEvent(PointerEventType.Scroll, Offset(width / 2f, 240f), scrollDelta = Offset(1f, 0f))
                repeat(3) { scene.render(System.nanoTime()).close() }
                assertNotNull(scene.text("125%"), "Horizontal scrolling must not zoom the image")
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.Zero, KeyEventType.KeyDown)))
                scene.await { scene.text("100%") != null }
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown)))
                assertTrue(closed)
            }
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 3_000_000_000L
        do {
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
}
