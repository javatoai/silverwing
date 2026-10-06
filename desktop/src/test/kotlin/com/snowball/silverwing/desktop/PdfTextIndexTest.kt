package com.snowball.silverwing.desktop

import androidx.compose.ui.graphics.toPixelMap
import org.apache.pdfbox.pdmodel.*
import org.apache.pdfbox.pdmodel.common.PDRectangle
import org.apache.pdfbox.pdmodel.font.*
import org.apache.pdfbox.pdmodel.interactive.documentnavigation.outline.*
import org.apache.pdfbox.pdmodel.interactive.documentnavigation.destination.PDPageFitDestination
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.awt.Color
import org.apache.pdfbox.text.TextPosition
import org.apache.pdfbox.util.Matrix
import kotlin.test.*

class PdfTextIndexTest {
    @TempDir lateinit var root: Path
    @Test fun `text glyph bounds follow cropped and rotated rendered pages and bookmarks navigate pages`() {
        val path = root.resolve("text.pdf")
        PDDocument().use { document ->
            val outline = PDDocumentOutline(); document.documentCatalog.documentOutline = outline
            listOf(0, 90, 180, 270).forEachIndexed { index, rotation ->
                val page = PDPage(PDRectangle(400f, 500f)).apply { cropBox = PDRectangle(20f, 40f, 320f, 400f); this.rotation = rotation }
                document.addPage(page)
                PDPageContentStream(document, page).use { stream ->
                    stream.beginText(); stream.setFont(PDType1Font(Standard14Fonts.FontName.HELVETICA), 20f)
                    stream.newLineAtOffset(50f, 400f); stream.showText("Hello PDF $index"); stream.endText()
                }
                outline.addLast(PDOutlineItem().apply { title = "Page ${index + 1}"; destination = PDPageFitDestination().apply { this.page = page } })
            }
            document.save(path.toFile())
        }
        val document = RequirementMaterialsPdfService().open(root, "text.pdf")
        val index = document.textIndex()
        assertEquals((0..3).toList(), index.outline.map { it.page })
        assertEquals(listOf("Page 1", "Page 2", "Page 3", "Page 4"), index.outline.map { it.title })
        index.pages.forEachIndexed { page, text ->
            assertEquals("Hello PDF $page", text.text)
            assertTrue(text.glyphs.size >= 10)
            val rect = text.glyphs.first().bounds
            assertTrue(rect.left >= 0 && rect.right <= 1 && rect.top >= 0 && rect.bottom <= 1)
            val rendered = document.render(page, 800).image
            val pixels = rendered.toPixelMap()
            var ink = 0
            for (y in (rect.top * rendered.height).toInt().coerceAtLeast(0)..(rect.bottom * rendered.height).toInt().coerceAtMost(rendered.height - 1))
                for (x in (rect.left * rendered.width).toInt().coerceAtLeast(0)..(rect.right * rendered.width).toInt().coerceAtMost(rendered.width - 1))
                    if (pixels[x,y].red < 0.5f) ink++
            assertTrue(ink > 5, "Text bounds must coincide with actual ink for rotation $page: $rect")
        }
        val selection = PdfTextSelection().apply { anchor = PdfTextPoint(0, 6); extent = PdfTextPoint(1, 4) }
        assertEquals("PDF 0\nHello", selection.text(index))
        assertEquals(6..10, selection.range(0, index.pages[0].text.length))
    }
    @Test fun `scanned page has no selectable text without affecting preview`() {
        PDDocument().use { doc -> doc.addPage(PDPage()); doc.save(root.resolve("scan.pdf").toFile()) }
        val pdf = RequirementMaterialsPdfService().open(root, "scan.pdf")
        assertTrue(pdf.textIndex().pages.single().glyphs.isEmpty())
        assertTrue(pdf.render(0, 300).image.width > 0)
    }
    @Test fun `slanted text bounds use the full baseline rather than projecting its width twice`() {
        val page = PDPage(PDRectangle(500f, 500f))
        val axis = kotlin.math.sqrt(0.5f)
        val matrix = Matrix(20f * axis, 20f * axis, -20f * axis, 20f * axis, 100f, 100f)
        val position = TextPosition(0, 500f, 500f, matrix, 100f + 80f * axis, 100f + 80f * axis,
            20f, 80f, 10f, "A", intArrayOf(65), PDType1Font(Standard14Fonts.FontName.HELVETICA), 20f, 20)
        val bounds = pdfGlyphBounds(position, page)
        assertEquals((100f + 80f * axis) / 500f, bounds.right, 0.00001f)
        assertEquals((500f - 100f - 100f * axis) / 500f, bounds.top, 0.00001f)
    }

    @Test fun `text extraction is cancellable even when a document contains no text`() {
        PDDocument().use { document ->
            document.addPage(PDPage())
            Thread.currentThread().interrupt()
            try { assertFailsWith<InterruptedException> { readPdfTextIndex(document) } }
            finally { Thread.interrupted() }
        }
    }

    @Test fun `dense text is rejected before PDFBox retains an unbounded page of glyphs`() {
        PDDocument().use { document ->
            val page = PDPage()
            document.addPage(page)
            PDPageContentStream(document, page).use { stream ->
                stream.beginText()
                stream.setFont(PDType1Font(Standard14Fonts.FontName.HELVETICA), 10f)
                stream.newLineAtOffset(10f, 100f)
                stream.showText("A".repeat(100_001))
                stream.endText()
            }
            val failure = assertFailsWith<IllegalArgumentException> { readPdfTextIndex(document) }
            assertTrue(failure.message.orEmpty().contains("文字较多"))
        }
    }
}
