package com.snowball.silverwing.desktop

import androidx.compose.ui.geometry.Rect
import org.apache.pdfbox.pdmodel.PDDocument
import org.apache.pdfbox.pdmodel.PDPage
import org.apache.pdfbox.text.PDFTextStripper
import org.apache.pdfbox.text.TextPosition
import org.apache.pdfbox.pdmodel.interactive.documentnavigation.outline.PDOutlineNode
import java.text.Normalizer
import java.text.Bidi
import kotlin.math.*

internal data class PdfGlyph(val range: IntRange, val bounds: Rect)
internal data class PdfPageText(val text: String, val glyphs: List<PdfGlyph>)
internal data class PdfOutlineEntry(val title: String, val page: Int, val depth: Int)
internal data class PdfTextIndex(val pages: List<PdfPageText>, val outline: List<PdfOutlineEntry>)

/** 索引只包含文字与归一化坐标，不含位图；从 PDF 内部读取，绝不执行书签中的外部动作。 */
internal fun readPdfTextIndex(document: PDDocument): PdfTextIndex {
    checkPdfInterrupted()
    require(document.numberOfPages <= MAX_PDF_PREVIEW_PAGES) { "PDF 页数较多，请使用外部阅读器查找文字。" }
    val strings = List(document.numberOfPages) { StringBuilder() }
    val glyphs = List(document.numberOfPages) { mutableListOf<PdfGlyph>() }
    var size = 0
    var positionCount = 0
    val stripper = object : PDFTextStripper() {
        private var page: PDPage? = null
        private var pagePositionCount = 0
        override fun startPage(page: PDPage) {
            checkPdfInterrupted()
            this.page = page
            pagePositionCount = 0
            super.startPage(page)
        }
        // PDFBox accumulates TextPositions before writeString, so bound that allocation too.
        override fun processTextPosition(position: TextPosition) {
            checkPdfInterrupted()
            require(++positionCount <= 500_000 && ++pagePositionCount <= 100_000) {
                "PDF 文字较多，请使用外部阅读器查找文字。"
            }
            super.processTextPosition(position)
        }
        private fun append(value: String) {
            checkPdfInterrupted()
            size += value.length
            require(size <= 2_000_000) { "PDF 文字较多，请使用外部阅读器查找文字。" }
            strings[currentPageNo - 1].append(value)
        }
        override fun writeString(text: String, positions: MutableList<TextPosition>) {
            val index = currentPageNo - 1
            val start = strings[index].length
            append(text)
            val ranges = pdfWordGlyphRanges(text, positions.map { it.visuallyOrderedUnicode })
            positions.forEachIndexed { offset, position ->
                checkPdfInterrupted()
                val range = ranges[offset] ?: return@forEachIndexed
                val bounds = pdfGlyphBounds(position, page!!)
                if (bounds.width > 0f && bounds.height > 0f) {
                    glyphs[index] += PdfGlyph((start + range.first)..(start + range.last), bounds)
                }
            }
        }
        override fun writeWordSeparator() = append(" ")
        override fun writeLineSeparator() = append("\n")
        override fun writeParagraphEnd() = append("\n")
    }.apply { sortByPosition = true }
    stripper.getText(document)
    checkPdfInterrupted()
    val outline = mutableListOf<PdfOutlineEntry>()
    val visited = java.util.Collections.newSetFromMap(java.util.IdentityHashMap<org.apache.pdfbox.cos.COSDictionary, Boolean>())
    val pageIndices = java.util.IdentityHashMap<org.apache.pdfbox.cos.COSDictionary, Int>()
    document.pages.forEachIndexed { index, page -> checkPdfInterrupted(); pageIndices[page.cosObject] = index }
    fun walk(node: PDOutlineNode, depth: Int) {
        if (depth > 32 || visited.size >= 2000 || outline.size >= 1000) return
        var child = runCatching { node.firstChild }.getOrNull()
        while (child != null && outline.size < 1000 && visited.size < 2000) {
            checkPdfInterrupted()
            if (!visited.add(child.cosObject)) break
            val page = runCatching { child.findDestinationPage(document) }.getOrNull()
            val index = page?.let { pageIndices[it.cosObject] } ?: -1
            if (index >= 0) outline += PdfOutlineEntry(runCatching { child.title }.getOrNull().orEmpty().take(300), index, depth)
            walk(child, depth + 1)
            child = runCatching { child.nextSibling }.getOrNull()
        }
    }
    document.documentCatalog.documentOutline?.let { walk(it, 0) }
    return PdfTextIndex(strings.indices.map { index ->
        val raw = strings[index].toString()
        val leading = raw.length - raw.trimStart().length
        val text = raw.trim()
        PdfPageText(text, glyphs[index].mapNotNull {
            val from = (it.range.first - leading).coerceAtLeast(0)
            val to = (it.range.last - leading).coerceAtMost(text.lastIndex)
            if (from > to) null else it.copy(range = from..to)
        })
    }, outline)
}

/** PDFBox normalizes presentation forms and reorders bidi runs, but preserves e.g. µ and fullwidth text. */
internal fun pdfWordGlyphRanges(text: String, glyphText: List<String>): List<IntRange?> {
    val normalized = glyphText.map { value -> buildString {
        value.forEach { character ->
            if (character in '\uFB00'..'\uFDFF' || character in '\uFE70'..'\uFEFF') {
                val form = Normalizer.normalize(character.toString(), Normalizer.Form.NFKC).trim()
                append(if (character >= '\uFB1D' && form.length > 1) form.reversed() else form)
            } else append(character)
        }
    } }
    val visual = normalized.joinToString("")
    if (visual.isEmpty()) return glyphText.map { null }
    if (visual.length == text.length) {
        val bidi = Bidi(visual, Bidi.DIRECTION_DEFAULT_LEFT_TO_RIGHT)
        val levels = ByteArray(bidi.runCount) { bidi.getRunLevel(it).toByte() }
        val runs = Array<Any>(bidi.runCount) { it }
        Bidi.reorderVisually(levels, 0, runs, 0, runs.size)
        val offsets = IntArray(visual.length)
        var output = 0
        runs.forEach { run ->
            val index = run as Int
            val range = bidi.getRunStart(index) until bidi.getRunLimit(index)
            val source = if ((levels[index].toInt() and 1) != 0) range.reversed() else range
            source.forEach { offsets[it] = output++ }
        }
        var source = 0
        return normalized.map { value ->
            val range = source until source + value.length
            source += value.length
            if (range.isEmpty()) null else range.minOf { offsets[it] }..range.maxOf { offsets[it] }
        }
    }
    // Unusual font mappings may have a different length; retain only offsets present in the extracted text.
    var cursor = 0
    return normalized.map { value ->
        val start = if (value.isEmpty()) -1 else text.indexOf(value, cursor)
        if (start < 0) null else (start until start + value.length).also { cursor = it.last + 1 }
    }
}

/** TextPosition 的有效矩阵已包含 CropBox 偏移；再按页面旋转映射到预览左上角坐标。 */
internal fun pdfGlyphBounds(position: TextPosition, page: PDPage): Rect {
    val matrix = position.textMatrix
    val angle = atan2(matrix.shearY.toDouble(), matrix.scaleX.toDouble())
    val dx = cos(angle).toFloat(); val dy = sin(angle).toFloat()
    val width = hypot(position.endX - matrix.translateX, position.endY - matrix.translateY).coerceAtLeast(0.1f)
    val height = position.heightDir.coerceAtLeast(1f)
    val x = matrix.translateX; val y = matrix.translateY
    val box = page.cropBox
    val rotation = Math.floorMod(page.rotation, 360)
    val rotated = rotation == 90 || rotation == 270
    val displayW = if (rotated) box.height else box.width
    val displayH = if (rotated) box.width else box.height
    fun point(px: Float, py: Float): Pair<Float, Float> = when(rotation) {
        90 -> py / displayW to px / displayH
        180 -> (box.width - px) / displayW to py / displayH
        270 -> (box.height - py) / displayW to (box.width - px) / displayH
        else -> px / displayW to (box.height - py) / displayH
    }
    val corners = listOf(point(x, y), point(x + dx * width, y + dy * width),
        point(x - dy * height, y + dx * height), point(x + dx * width - dy * height, y + dy * width + dx * height))
    return Rect(corners.minOf { it.first }.coerceIn(0f, 1f), corners.minOf { it.second }.coerceIn(0f, 1f),
        corners.maxOf { it.first }.coerceIn(0f, 1f), corners.maxOf { it.second }.coerceIn(0f, 1f))
}
