@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.unit.IntOffset
import kotlin.math.roundToInt

internal data class PdfTextPoint(val page: Int, val offset: Int) : Comparable<PdfTextPoint> {
    override fun compareTo(other: PdfTextPoint) = compareValuesBy(this, other, PdfTextPoint::page, PdfTextPoint::offset)
}
internal class PdfTextSelection {
    var anchor by mutableStateOf<PdfTextPoint?>(null)
    var extent by mutableStateOf<PdfTextPoint?>(null)
    private var anchorGlyph: Pair<PdfTextPoint, PdfTextPoint>? = null
    private val pageBounds = mutableMapOf<Int, Rect>()
    fun registerPage(page: Int, bounds: Rect) { pageBounds[page] = bounds }
    fun unregisterPage(page: Int) { pageBounds.remove(page) }
    fun clear() { anchor = null; extent = null; anchorGlyph = null }
    fun begin(page: Int, glyph: PdfGlyph, extending: Boolean) {
        if (!extending || anchor == null) {
            anchorGlyph = PdfTextPoint(page, glyph.range.first) to PdfTextPoint(page, glyph.range.last)
            anchor = anchorGlyph!!.first
        }
        extend(page, glyph)
    }
    fun extend(page: Int, glyph: PdfGlyph) {
        val start = PdfTextPoint(page, glyph.range.first)
        val origin = anchorGlyph
        val backwards = start < (origin?.first ?: anchor ?: return)
        if (origin != null) anchor = if (backwards) origin.second else origin.first
        extent = PdfTextPoint(page, if (backwards) glyph.range.first else glyph.range.last)
    }
    fun extendAt(index: PdfTextIndex, pointInRoot: Offset) {
        val (page, bounds) = pageBounds.entries.filter { index.pages.getOrNull(it.key)?.glyphs?.isNotEmpty() == true }
            .minByOrNull { distanceToPdfRect(it.value, pointInRoot) } ?: return
        if (bounds.width <= 0f || bounds.height <= 0f) return
        val local = Offset((pointInRoot.x - bounds.left) / bounds.width, (pointInRoot.y - bounds.top) / bounds.height)
        nearestPdfGlyph(index.pages[page], local)?.let { extend(page, it) }
    }
    fun range(page: Int, length: Int): IntRange? {
        val a = anchor ?: return null; val b = extent ?: return null
        val from = minOf(a, b); val to = maxOf(a, b)
        if (page !in from.page..to.page || length == 0) return null
        val start = (if (page == from.page) from.offset else 0).coerceIn(0, length)
        val end = (if (page == to.page) to.offset else length - 1).coerceIn(-1, length - 1)
        return if (start <= end) start..end else null
    }
    private fun selectedPages(index: PdfTextIndex): IntRange {
        val a = anchor ?: return IntRange.EMPTY
        val b = extent ?: return IntRange.EMPTY
        return maxOf(minOf(a.page, b.page), 0)..minOf(maxOf(a.page, b.page), index.pages.lastIndex)
    }
    fun hasText(index: PdfTextIndex) = selectedPages(index).any { range(it, index.pages[it].text.length) != null }
    fun text(index: PdfTextIndex): String = selectedPages(index).mapNotNull { page ->
        val value = index.pages[page]
        range(page, value.text.length)?.let { value.text.substring(it.first, it.last + 1) }
    }.joinToString("\n")
}
@Composable internal fun PdfTextOverlay(index: PdfTextIndex, pageIndex: Int, selection: PdfTextSelection, modifier: Modifier = Modifier) {
    val page = index.pages[pageIndex]
    val clipboard = LocalClipboardManager.current
    val find = LocalDocumentFind.current
    val focus = remember { FocusRequester() }
    var dragging by remember { mutableStateOf(false) }
    var rootOrigin by remember { mutableStateOf(Offset.Zero) }
    var menuPosition by remember { mutableStateOf<IntOffset?>(null) }
    val hasSelection = selection.hasText(index)
    fun copy() { selection.text(index).takeIf(String::isNotEmpty)?.let { clipboard.setText(AnnotatedString(it)) } }
    val selected = selection.range(pageIndex, page.text.length)
    val query = if (find?.open == true) find.query else ""
    val hits = remember(page.text, query) { literalMatches(page.text, query) }
    val active = find?.takeIf { it.open }?.current?.takeIf { it.block == "pdf:$pageIndex" }?.range
    DisposableEffect(selection, pageIndex) { onDispose { selection.unregisterPage(pageIndex) } }
    Box(modifier.fillMaxSize()) {
        Canvas(Modifier.fillMaxSize().focusRequester(focus).focusable()
            .onGloballyPositioned { coordinates ->
                rootOrigin = coordinates.positionInRoot()
                selection.registerPage(pageIndex, Rect(rootOrigin, Size(coordinates.size.width.toFloat(), coordinates.size.height.toFloat())))
            }
            .pointerHoverIcon(PointerIcon.Text)
            .semantics {
                contentDescription = "PDF 文字层，第 ${pageIndex + 1} 页"
                text = AnnotatedString(page.text)
                textSelectionRange = selected?.let { TextRange(it.first, it.last + 1) } ?: TextRange.Zero
                copyText { if (!hasSelection) false else { copy(); true } }
                setSelection { start, end, _ ->
                    if (start < 0 || end <= start || end > page.text.length) false else {
                        selection.clear(); selection.anchor = PdfTextPoint(pageIndex, start); selection.extent = PdfTextPoint(pageIndex, end - 1); true
                    }
                }
            }
            .onKeyEvent { event ->
                if (event.type == KeyEventType.KeyDown && event.isCtrlPressed && event.key == Key.C && hasSelection) { copy(); true } else false
            }
            .onPointerEvent(PointerEventType.Press) { event ->
                if (event.buttons.isSecondaryPressed) {
                    focus.requestFocus()
                    event.changes.firstOrNull()?.position?.let { menuPosition = IntOffset(it.x.roundToInt(), it.y.roundToInt()) }
                    event.changes.forEach { it.consume() }
                }
                if (event.buttons.isPrimaryPressed) {
                    focus.requestFocus()
                    val point = event.changes.firstOrNull()?.position
                    val size = this.size
                    val glyph = point?.let { nearestPdfGlyph(page, Offset(it.x / size.width, it.y / size.height)) }
                    if (glyph != null) {
                        selection.begin(pageIndex, glyph, event.keyboardModifiers.isShiftPressed)
                        dragging = true
                        event.changes.forEach { it.consume() }
                    } else { selection.clear() }
                }
            }
            .onPointerEvent(PointerEventType.Move) { event ->
                if (dragging && event.buttons.isPrimaryPressed) {
                    val point = event.changes.firstOrNull()?.position
                    point?.let { selection.extendAt(index, rootOrigin + it) }
                    event.changes.forEach { it.consume() }
                }
            }
            .onPointerEvent(PointerEventType.Release) { dragging = false }) {
            for (glyph in page.glyphs) {
                val bounds = glyph.bounds
                val color = when {
                    selected?.let { glyph.range.first <= it.last && glyph.range.last >= it.first } == true -> Color(0x66356AE6)
                    active?.let { glyph.range.first <= it.last && glyph.range.last >= it.first } == true -> Color(0xAAFFB74D)
                    pdfRangeIntersectsHits(glyph.range, hits) -> Color(0x66FFEB3B)
                    else -> continue
                }
                drawRect(color, Offset(bounds.left * size.width, bounds.top * size.height), Size(bounds.width * size.width, bounds.height * size.height))
            }
        }
        SilverWingContextMenu(menuPosition != null, { menuPosition = null }, menuPosition ?: IntOffset.Zero) {
            DropdownMenuItem(text = { Text("复制") }, enabled = hasSelection, onClick = {
                copy(); menuPosition = null; focus.requestFocus()
            })
        }
    }
}
internal fun nearestPdfGlyph(page: PdfPageText, point: Offset): PdfGlyph? = page.glyphs.minByOrNull { glyph ->
    distanceToPdfRect(glyph.bounds, point)
}
private fun distanceToPdfRect(box: Rect, point: Offset): Float {
    val dx = maxOf(box.left - point.x, 0f, point.x - box.right)
    val dy = maxOf(box.top - point.y, 0f, point.y - box.bottom)
    return dx * dx + dy * dy * 4f
}
/** Search hits are sorted and disjoint; avoid scanning every hit for every visible glyph. */
internal fun pdfRangeIntersectsHits(range: IntRange, hits: List<IntRange>): Boolean {
    var low = 0
    var high = hits.lastIndex
    while (low <= high) {
        val middle = (low + high) ushr 1
        val hit = hits[middle]
        when {
            hit.last < range.first -> low = middle + 1
            hit.first > range.last -> high = middle - 1
            else -> return true
        }
    }
    return false
}
