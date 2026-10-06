package com.snowball.silverwing.desktop

import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.toPixelMap
import org.apache.pdfbox.pdmodel.PDDocument
import org.apache.pdfbox.pdmodel.PDPage
import org.apache.pdfbox.pdmodel.PDPageContentStream
import org.apache.pdfbox.pdmodel.common.PDRectangle
import org.apache.pdfbox.pdmodel.encryption.AccessPermission
import org.apache.pdfbox.pdmodel.encryption.StandardProtectionPolicy
import org.apache.pdfbox.pdmodel.font.PDType1Font
import org.apache.pdfbox.pdmodel.font.Standard14Fonts
import org.apache.pdfbox.pdmodel.graphics.image.LosslessFactory
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.io.TempDir
import java.awt.Color
import java.awt.image.BufferedImage
import java.nio.ByteBuffer
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import kotlin.math.max
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

internal fun writePreviewPdf(path: Path, password: String? = null) {
    PDDocument().use { document ->
        for ((index, color) in listOf(Color(240, 242, 245), Color(200, 232, 220)).withIndex()) {
            val page = PDPage(PDRectangle(300f, 420f)).apply { if (index == 1) rotation = 90 }
            document.addPage(page)
            PDPageContentStream(document, page).use { stream ->
                stream.setNonStrokingColor(color)
                stream.addRect(0f, 0f, 300f, 420f)
                stream.fill()
                stream.setNonStrokingColor(Color(30, 40, 50))
                stream.beginText()
                stream.setFont(PDType1Font(Standard14Fonts.FontName.HELVETICA_BOLD), 18f)
                stream.newLineAtOffset(24f, 378f)
                stream.showText("Requirement materials / ${index + 1}")
                stream.endText()
                stream.beginText()
                stream.setFont(PDType1Font(Standard14Fonts.FontName.HELVETICA), 12f)
                stream.newLineAtOffset(24f, 344f)
                stream.showText("PDF preview: paging, zoom and fit width")
                stream.endText()
            }
        }
        if (password != null) document.protect(StandardProtectionPolicy("owner", password, AccessPermission()))
        document.save(path.toFile())
    }
}

class RequirementMaterialsPdfServiceTest {
    @TempDir
    lateinit var root: Path

    @Test
    fun `PDF catalog and binary reads preserve directory boundaries and text restrictions`() {
        val nested = Files.createDirectory(root.resolve("nested"))
        val path = nested.resolve("说明.PDF")
        writePreviewPdf(path)
        val files = RequirementMaterialsMarkdownService()
        val file = files.list(root).files.single()
        assertEquals("nested/说明.PDF", file.relativePath)
        assertTrue(file.pdf)
        assertFalse(file.markdown)
        assertTrue(files.readPdf(root, file.relativePath).size > 100)
        assertEquals(path, files.resolveCopyItem(root, file.relativePath))
        assertFailsWith<IllegalArgumentException> { files.read(root, file.relativePath) }
        for (unsafe in listOf("../outside.pdf", "/outside.pdf", "C:/outside.pdf", "nested/../说明.PDF")) {
            assertFailsWith<IllegalArgumentException> { files.readPdf(root, unsafe) }
        }
        assertFailsWith<IllegalArgumentException> { files.readPdf(root, file.relativePath, maxBytes = 8) }
        val huge = root.resolve("large.pdf")
        Files.newByteChannel(huge, StandardOpenOption.CREATE, StandardOpenOption.WRITE).use {
            it.position(64L * 1024 * 1024)
            it.write(ByteBuffer.wrap(byteArrayOf(0)))
        }
        assertTrue(assertFailsWith<IllegalArgumentException> { files.readPdf(root, "large.pdf") }.message!!.contains("64 MB"))
    }

    @Test
    fun `renders real pages with rotation zoom and releases the source after each request`() {
        val source = root.resolve("two.pdf")
        writePreviewPdf(source)
        val service = RequirementMaterialsPdfService()
        val first = service.render(root, "two.pdf", 0, 300)
        val second = service.render(root, "two.pdf", 1, 420)
        val zoomed = service.render(root, "two.pdf", 1, 840)
        assertEquals(2, first.pageCount)
        assertEquals(0, first.pageIndex)
        assertEquals(300, first.image.width)
        assertEquals(420, first.image.height)
        assertEquals(420, second.image.width)
        assertEquals(300, second.image.height)
        assertEquals(840, zoomed.image.width)
        assertEquals(600, zoomed.image.height)
        assertEquals(Color(240, 242, 245).rgb, first.image.toPixelMap()[150, 210].toArgb())
        assertEquals(Color(200, 232, 220).rgb, second.image.toPixelMap()[210, 150].toArgb())
        assertEquals(1, service.render(root, "two.pdf", 99, 100).pageIndex)
        // Windows 上移动并覆盖文件会暴露遗漏的文档句柄，刷新应读取新的文件内容。
        Files.move(source, root.resolve("previous.pdf"))
        PDDocument().use { document ->
            document.addPage(PDPage(PDRectangle(100f, 100f)))
            document.save(source.toFile())
        }
        assertEquals(1, service.render(root, "two.pdf", 0, 100).pageCount)
    }

    @Test
    fun `corrupt encrypted and empty PDFs have recoverable errors and do not lock files`() {
        Files.writeString(root.resolve("broken.pdf"), "not a PDF")
        writePreviewPdf(root.resolve("locked.pdf"), "secret")
        PDDocument().use { it.save(root.resolve("empty.pdf").toFile()) }
        val service = RequirementMaterialsPdfService()
        for ((name, message) in listOf("broken.pdf" to "损坏", "locked.pdf" to "密码", "empty.pdf" to "没有可预览")) {
            val error = assertFailsWith<IllegalArgumentException> { service.render(root, name, 0, 300) }
            assertTrue(error.message!!.contains(message), error.message)
            Files.delete(root.resolve(name))
        }
    }

    @Test
    fun `image bounds remain safe for large and invalid page dimensions`() {
        for ((width, height) in listOf(300f to 420f, 1000f to 100000f, 100000f to 1000f, 10000f to 10000f)) {
            val scale = pdfRenderScale(width, height, 100000)
            assertTrue(max(width, height) * scale <= 4097f)
            assertTrue(width.toDouble() * height * scale * scale <= 4_000_001)
        }
        for (bad in listOf(Float.NaN, Float.POSITIVE_INFINITY, -1f, 0f)) {
            assertFailsWith<IllegalArgumentException> { pdfRenderScale(bad, 100f, 300) }
        }
        assertFailsWith<IllegalArgumentException> { pdfRenderScale(Float.MIN_VALUE, Float.MIN_VALUE, 300) }
        assertFailsWith<IllegalArgumentException> { pdfRenderScale(Float.MAX_VALUE, Float.MIN_VALUE, 300) }
    }

    @Test
    fun `cancelled requests exit without retaining the source file`() {
        val source = root.resolve("cancel.pdf")
        writePreviewPdf(source)
        Thread.currentThread().interrupt()
        try {
            assertFailsWith<InterruptedException> { RequirementMaterialsPdfService().render(root, "cancel.pdf", 0, 300) }
        } finally {
            Thread.interrupted()
        }
        Files.delete(source)
    }

    @Test
    fun `continuous preview reuses bounded source bytes without holding an open file`() {
        val source = root.resolve("shared.pdf")
        writePreviewPdf(source)
        val document = RequirementMaterialsPdfService().open(root, "shared.pdf")
        assertEquals(2, document.pageCount)
        assertEquals(300f / 420f, document.aspectRatio(0))
        assertEquals(420f / 300f, document.aspectRatio(1))
        Files.delete(source)
        assertEquals(0, document.render(0, 300).pageIndex)
        assertEquals(1, document.render(1, 420).pageIndex)
        Thread.currentThread().interrupt()
        try { assertFailsWith<InterruptedException> { document.textIndex() } }
        finally { Thread.interrupted() }
    }

    @Test
    fun `scanned PDF pages render their embedded image without text content`() {
        val path = root.resolve("scan.pdf")
        PDDocument().use { document ->
            val scan = BufferedImage(64, 64, BufferedImage.TYPE_INT_RGB)
            val graphics = scan.createGraphics()
            try {
                graphics.color = Color(255, 235, 200)
                graphics.fillRect(0, 0, 64, 64)
                graphics.color = Color(40, 90, 180)
                graphics.fillRect(16, 16, 32, 32)
                val page = PDPage(PDRectangle(64f, 64f))
                document.addPage(page)
                PDPageContentStream(document, page).use {
                    it.drawImage(LosslessFactory.createFromImage(document, scan), 0f, 0f, 64f, 64f)
                }
                document.save(path.toFile())
            } finally {
                graphics.dispose()
                scan.flush()
            }
        }
        val page = RequirementMaterialsPdfService().render(root, "scan.pdf", 0, 192)
        assertEquals(Color(255, 235, 200).rgb, page.image.toPixelMap()[4, 4].toArgb())
        assertEquals(Color(40, 90, 180).rgb, page.image.toPixelMap()[96, 96].toArgb())
    }

    @Test
    fun `PDF links cannot escape the materials directory`() {
        val target = root.resolveSibling("outside-${root.fileName}.pdf")
        try {
            writePreviewPdf(target)
            val linked = root.resolve("linked.pdf")
            assumeTrue(runCatching { Files.createSymbolicLink(linked, target) }.isSuccess,
                "Symbolic links are unavailable in this test environment")
            val files = RequirementMaterialsMarkdownService()
            assertTrue(files.list(root).files.isEmpty())
            assertFailsWith<IllegalArgumentException> { files.readPdf(root, "linked.pdf") }
        } finally {
            Files.deleteIfExists(target)
        }
    }
}
