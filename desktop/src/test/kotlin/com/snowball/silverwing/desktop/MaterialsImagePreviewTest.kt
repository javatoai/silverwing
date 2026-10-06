@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import org.jetbrains.skia.ColorAlphaType
import org.jetbrains.skia.ColorType
import org.jetbrains.skia.EncodedImageFormat
import org.jetbrains.skia.Image
import org.jetbrains.skia.ImageInfo
import org.junit.jupiter.api.io.TempDir
import java.awt.Color
import java.awt.image.BufferedImage
import java.nio.file.Files
import java.nio.file.Path
import javax.imageio.ImageIO
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlinx.coroutines.Dispatchers

class MaterialsImagePreviewTest {
    @TempDir
    lateinit var root: Path

    @Test
    fun `reads png jpeg webp and gif first frame with bounded dimensions`() {
        val formats = listOf("png", "jpg", "webp", "gif")
        formats.forEach { extension -> writeImage(root.resolve("sample.$extension"), extension) }
        writeImage(root.resolve("oriented.jpg"), "jpg")
        addJpegExifOrientation(root.resolve("oriented.jpg"), orientation = 6)
        val service = MaterialsImageService()

        formats.forEach { extension ->
            val image = service.read(root, "sample.$extension")
            assertEquals(4, image.width, extension)
            assertEquals(3, image.height, extension)
        }
        // EXIF orientation 6 rotates a 4x3 JPEG to the expected portrait dimensions.
        val oriented = service.read(root, "oriented.jpg")
        assertEquals(3, oriented.width)
        assertEquals(4, oriented.height)
    }

    @Test
    fun `rejects corrupt oversized unsafe and unsupported image requests`() {
        Files.write(root.resolve("broken.png"), byteArrayOf(1, 2, 3, 4))
        writeImage(root.resolve("large.png"), "png")
        Files.createDirectories(root.resolve("nested"))
        writeImage(root.resolve("nested/image.png"), "png")

        val corrupt = assertFailsWith<IllegalArgumentException> {
            MaterialsImageService().read(root, "broken.png")
        }
        assertTrue(corrupt.message.orEmpty().contains("无法解码图片"), corrupt.message)

        assertFailsWith<IllegalArgumentException> {
            MaterialsImageService(maxBytes = 4).read(root, "large.png")
        }
        assertFailsWith<IllegalArgumentException> {
            MaterialsImageService(maxPixels = 4).read(root, "large.png")
        }
        assertFailsWith<IllegalArgumentException> {
            MaterialsImageService().read(root, "../large.png")
        }
        assertFailsWith<IllegalArgumentException> {
            MaterialsImageService().read(root, "nested/image.txt")
        }
    }

    @Test
    fun `image preview keeps controls usable in light dark and narrow layouts`() {
        writeImage(root.resolve("one.png"), "png")
        writeImage(root.resolve("two.jpg"), "jpg")
        val images = listOf("one.png", "two.jpg")

        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            val active = mutableStateOf("one.png")
            ImageComposeScene(width, 540, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    MaterialsImagePreview(
                        root = root,
                        relativePath = active.value,
                        refreshKey = 0,
                        images = images,
                        onNavigate = { active.value = it },
                        modifier = Modifier.fillMaxSize(),
                        ioDispatcher = Dispatchers.Unconfined,
                    )
                }
            }.use { scene ->
                scene.await { scene.text("1 / 2 · 4 × 3") != null }
                scene.screenshot("${if (dark) "dark" else "light"}-$width")

                scene.await { scene.percentValues().isNotEmpty() }
                val beforeZoom = scene.percentValues().first()
                scene.sendPointerEvent(
                    PointerEventType.Scroll,
                    Offset(width / 2f, 320f),
                    scrollDelta = Offset(0f, 1f),
                )
                scene.await { scene.percentValues().firstOrNull() != beforeZoom }
                val center = Offset(width / 2f, 320f)
                scene.sendPointerEvent(PointerEventType.Move, center)
                scene.sendPointerEvent(
                    PointerEventType.Press,
                    center,
                    buttons = PointerButtons(isPrimaryPressed = true),
                    button = PointerButton.Primary,
                )
                scene.sendPointerEvent(
                    PointerEventType.Move,
                    center + Offset(18f, 12f),
                    buttons = PointerButtons(isPrimaryPressed = true),
                )
                scene.sendPointerEvent(
                    PointerEventType.Release,
                    center + Offset(18f, 12f),
                    buttons = PointerButtons(),
                    button = PointerButton.Primary,
                )

                scene.click(scene.description("下一张图片"))
                scene.await { scene.text("2 / 2 · 4 × 3") != null }
                assertEquals("two.jpg", active.value)

                scene.click(scene.description("放大查看图片"))
                scene.await { scene.description("关闭图片") != null }
                assertTrue(scene.sendKeyEvent(androidx.compose.ui.input.key.KeyEvent(
                    androidx.compose.ui.input.key.Key.Escape,
                    androidx.compose.ui.input.key.KeyEventType.KeyDown,
                )))
                scene.await { scene.descriptionOrNull("关闭图片") == null }
            }
        }
    }

    private fun writeImage(path: Path, format: String) {
        if (format == "webp" || format == "jpg") {
            val width = 4
            val height = 3
            val pixels = ByteArray(width * height * 4) { index ->
                when (index % 4) {
                    0 -> 0x20
                    1 -> 0x80.toByte()
                    2 -> 0xE0.toByte()
                    else -> 0xFF.toByte()
                }
            }
            Image.makeRaster(
                ImageInfo(width, height, ColorType.RGBA_8888, ColorAlphaType.UNPREMUL),
                pixels,
                width * 4,
            ).use { image ->
                image.encodeToData(if (format == "webp") EncodedImageFormat.WEBP else EncodedImageFormat.JPEG, 90)!!.use { Files.write(path, it.bytes) }
            }
            return
        }
        val buffered = BufferedImage(4, 3, BufferedImage.TYPE_INT_ARGB)
        val graphics = buffered.createGraphics()
        try {
            graphics.color = Color(32, 128, 224, 255)
            graphics.fillRect(0, 0, 4, 3)
        } finally {
            graphics.dispose()
        }
        assertTrue(ImageIO.write(buffered, format, path.toFile()), "No ImageIO writer for $format")
        buffered.flush()
    }

    private fun addJpegExifOrientation(path: Path, orientation: Int) {
        require(orientation in 1..8)
        val tiff = byteArrayOf(
            0x49, 0x49, 0x2A, 0x00, // little-endian TIFF header
            0x08, 0x00, 0x00, 0x00, // first IFD offset
            0x01, 0x00, // one directory entry
            0x12, 0x01, // Orientation tag
            0x03, 0x00, // SHORT
            0x01, 0x00, 0x00, 0x00, // one value
            orientation.toByte(), 0x00, 0x00, 0x00,
            0x00, 0x00, 0x00, 0x00, // no next IFD
        )
        val payload = byteArrayOf(0x45, 0x78, 0x69, 0x66, 0x00, 0x00) + tiff
        val segmentLength = payload.size + 2
        val app1 = byteArrayOf(
            0xFF.toByte(), 0xE1.toByte(),
            (segmentLength ushr 8).toByte(), segmentLength.toByte(),
        ) + payload
        val jpeg = Files.readAllBytes(path)
        require(jpeg.size >= 2 && jpeg[0] == 0xFF.toByte() && jpeg[1] == 0xD8.toByte())
        Files.write(path, jpeg.copyOfRange(0, 2) + app1 + jpeg.copyOfRange(2, jpeg.size))
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }

    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }

    private fun ImageComposeScene.percentValues() = nodes().flatMap {
        it.config.getOrNull(SemanticsProperties.Text).orEmpty().map { text -> text.text }
    }.filter { it.endsWith('%') }

    private fun ImageComposeScene.description(value: String): SemanticsNode = nodes().first {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.any { text -> text == value } == true
    }

    private fun ImageComposeScene.descriptionOrNull(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.any { text -> text == value } == true
    }

    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(
            PointerEventType.Press,
            point,
            buttons = PointerButtons(isPrimaryPressed = true),
            button = PointerButton.Primary,
        )
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }

    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 8_000_000_000L
        do {
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }

    private fun ImageComposeScene.screenshot(name: String) {
        val output = Path.of("build/reports/materials-image/$name.png")
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
