package com.snowball.silverwing.desktop

import androidx.compose.foundation.ContextMenuArea
import androidx.compose.foundation.ContextMenuItem
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.DisableSelection
import androidx.compose.foundation.text.selection.LocalTextSelectionColors
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.isPrimaryPressed
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/** Rectangular mouse selection and clipboard shortcut shared by Markdown and CSV tables. */
@Composable
internal fun SelectableDataGrid(
    rows: List<List<String>>,
    modifier: Modifier = Modifier,
    cellContent: (@Composable (Int, Int, Boolean) -> Unit)? = null,
    ownVerticalScroll: Boolean = false,
    onCopy: (Int, Int, Int, Int) -> Unit,
) {
    val focusRequester = remember { FocusRequester() }
    val currentCopy by rememberUpdatedState(onCopy)
    var anchor by remember(rows) { mutableStateOf<Pair<Int, Int>?>(null) }
    var extent by remember(rows) { mutableStateOf<Pair<Int, Int>?>(null) }
    var gridCoordinates by remember { mutableStateOf<LayoutCoordinates?>(null) }
    val cellBounds = remember(rows) { mutableStateMapOf<Pair<Int, Int>, Rect>() }
    val columns = rows.maxOfOrNull { it.size } ?: 0
    val scrollModifier = if (ownVerticalScroll) Modifier.verticalScroll(rememberMaterialsScrollState("grid-vertical")) else Modifier
    val selectionColor = LocalTextSelectionColors.current.backgroundColor
    val selectionOutline = MaterialTheme.colorScheme.primary
    fun copySelection(): Boolean {
        val a = anchor ?: return false
        val b = extent ?: return false
        currentCopy(minOf(a.first, b.first), maxOf(a.first, b.first), minOf(a.second, b.second), maxOf(a.second, b.second))
        return true
    }
    // 表格独立维护单元格选区，禁止注册到文档文字选区，避免出现两个重叠选区。
    DisableSelection {
        // 单元格选区不属于外层文字选区，菜单和快捷键统一使用表格自己的复制入口。
        ContextMenuArea(items = {
            listOf(ContextMenuItem("复制", enabled = anchor != null && extent != null) { copySelection() })
        }) {
            Column(
                modifier
                    .onPreviewKeyEvent { event ->
                        if (event.type == KeyEventType.KeyDown && event.isCtrlPressed && event.key == Key.C) {
                            copySelection()
                        } else false
                    }.focusRequester(focusRequester).focusable()
                    .onGloballyPositioned { gridCoordinates = it }
                    .pointerInput(rows) {
                        awaitEachGesture {
                            val down = awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
                            // 右键只打开菜单，保留已有的多行、多列选区。
                            if (!currentEvent.buttons.isPrimaryPressed) return@awaitEachGesture
                            focusRequester.requestFocus()
                            fun selectedCell(x: androidx.compose.ui.geometry.Offset): Pair<Int, Int>? {
                                val point = gridCoordinates?.localToRoot(x) ?: return null
                                return cellBounds.entries.firstOrNull { it.value.contains(point) }?.key
                            }
                            selectedCell(down.position)?.let { anchor = it; extent = it }
                            var dragging = false
                            while (true) {
                                val event = awaitPointerEvent(PointerEventPass.Initial)
                                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                                if ((change.position - down.position).getDistance() > viewConfiguration.touchSlop) dragging = true
                                if (dragging) {
                                    selectedCell(change.position)?.let { extent = it }
                                    // Leave clicks intact for links and images inside a cell.
                                    // A real selection drag must cancel the child click, including release.
                                    change.consume()
                                }
                                if (!change.pressed) break
                            }
                        }
                    }
                    .horizontalScroll(if (ownVerticalScroll) rememberMaterialsScrollState("grid-horizontal") else rememberScrollState()).then(scrollModifier),
            ) {
                rows.forEachIndexed { rowIndex, row ->
                    Row(Modifier.height(IntrinsicSize.Min)) {
                        repeat(columns) { columnIndex ->
                            val a = anchor
                            val b = extent
                            val selected = a != null && b != null &&
                                rowIndex in minOf(a.first, b.first)..maxOf(a.first, b.first) &&
                                columnIndex in minOf(a.second, b.second)..maxOf(a.second, b.second)
                            Box(
                                modifier = Modifier.width(160.dp).fillMaxHeight()
                                    .border(0.5.dp, MaterialTheme.colorScheme.outlineVariant)
                                    .background(if (selected) selectionColor else MaterialTheme.colorScheme.surface)
                                    .onGloballyPositioned { cellBounds[rowIndex to columnIndex] = it.boundsInRoot() }
                                    .drawWithContent {
                                        drawContent()
                                        if (selected) {
                                            val stroke = 1.5.dp.toPx()
                                            val inset = stroke / 2
                                            if (rowIndex == minOf(a.first, b.first))
                                                drawLine(selectionOutline, Offset(0f, inset), Offset(size.width, inset), stroke)
                                            if (rowIndex == maxOf(a.first, b.first))
                                                drawLine(selectionOutline, Offset(0f, size.height - inset), Offset(size.width, size.height - inset), stroke)
                                            if (columnIndex == minOf(a.second, b.second))
                                                drawLine(selectionOutline, Offset(inset, 0f), Offset(inset, size.height), stroke)
                                            if (columnIndex == maxOf(a.second, b.second))
                                                drawLine(selectionOutline, Offset(size.width - inset, 0f), Offset(size.width - inset, size.height), stroke)
                                        }
                                    }
                                    .padding(8.dp),
                            ) {
                                if (cellContent != null) cellContent(rowIndex, columnIndex, selected)
                                else SearchableText(row.getOrNull(columnIndex).orEmpty(), style = MaterialTheme.typography.bodySmall,
                                    sourceOrder = (rowIndex.toLong() shl 32) + columnIndex)
                            }
                        }
                    }
                }
            }
        }
    }
}
