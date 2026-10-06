@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.ContextMenuItem
import androidx.compose.foundation.ContextMenuRepresentation
import androidx.compose.foundation.ContextMenuState
import androidx.compose.foundation.clickable
import androidx.compose.foundation.LocalContextMenuRepresentation
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.use
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SelectableDataGridTest {
    @Test
    fun `cell clicks reach links but selection drags cancel them`() {
        var clicks = 0
        var bounds = Rect.Zero
        ImageComposeScene(width = 300, height = 200) {
            MaterialTheme {
                SelectableDataGrid(listOf(listOf("link")), cellContent = { _, _, _ ->
                    Text("link", Modifier.clickable { clicks++ }.onGloballyPositioned { bounds = it.boundsInRoot() })
                }, onCopy = { _, _, _, _ -> })
            }
        }.use { scene ->
            fun render() { repeat(3) { scene.render().close() } }
            fun press(point: Offset) {
                scene.sendPointerEvent(PointerEventType.Move, point)
                scene.sendPointerEvent(PointerEventType.Press, point,
                    buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
            }
            fun release(point: Offset) {
                scene.sendPointerEvent(PointerEventType.Release, point,
                    buttons = PointerButtons(), button = PointerButton.Primary)
                render()
            }
            render()
            press(bounds.center); release(bounds.center)
            assertEquals(1, clicks)
            press(bounds.center)
            val end = bounds.center + Offset(60f, 0f)
            scene.sendPointerEvent(PointerEventType.Move, end, buttons = PointerButtons(isPrimaryPressed = true))
            release(end)
            assertEquals(1, clicks, "Dragging must not activate the cell link")
        }
    }

    @Test
    fun `closing find restores the table focus for keyboard copy`() {
        val find = DocumentFindState()
        var bounds = Rect.Zero
        var copies = 0
        ImageComposeScene(width = 420, height = 240) {
            MaterialTheme {
                DocumentFindScope(find) {
                    Column {
                        DocumentFindBar(find)
                        SelectableDataGrid(listOf(listOf("cell")), cellContent = { _, _, _ ->
                            Text("cell", Modifier.onGloballyPositioned { bounds = it.boundsInRoot() })
                        }, onCopy = { _, _, _, _ -> copies++ })
                    }
                }
            }
        }.use { scene ->
            fun render() { repeat(5) { scene.render().close() } }
            render()
            scene.sendPointerEvent(PointerEventType.Press, bounds.center,
                buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
            scene.sendPointerEvent(PointerEventType.Release, bounds.center,
                buttons = PointerButtons(), button = PointerButton.Primary)
            render()
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.F, KeyEventType.KeyDown, isCtrlPressed = true)))
            render(); assertTrue(find.open)
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown)))
            render(); assertFalse(find.open)
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true)))
            assertEquals(1, copies)
        }
    }

    @Test
    fun `table context copy uses its cell selection and matches Ctrl C without resetting the range`() {
        val table = parsePreviewMarkdownTable(
            "| File | Description |\n| --- | --- |\n| first.md | first |\n| second.md | second |\n| third.md | third |",
        )!!
        val copies = mutableListOf<String>()
        val cellBounds = mutableMapOf<Pair<Int, Int>, Rect>()
        val openedMenus = mutableMapOf<ContextMenuState, List<ContextMenuItem>>()
        val menuRepresentation = object : ContextMenuRepresentation {
            @Composable
            override fun Representation(state: ContextMenuState, items: () -> List<ContextMenuItem>) {
                if (state.status is ContextMenuState.Status.Open) openedMenus[state] = items()
            }
        }
        ImageComposeScene(width = 420, height = 240) {
            MaterialTheme {
                CompositionLocalProvider(LocalContextMenuRepresentation provides menuRepresentation) {
                    // 保留 Markdown 的外层文字选区，验证表格菜单不会落到它的灰色复制项。
                    SelectionContainer {
                        SelectableDataGrid(
                            rows = table.rows,
                            cellContent = { row, column, _ ->
                                Text(table.rows[row][column], Modifier.onGloballyPositioned {
                                    cellBounds[row to column] = it.boundsInRoot()
                                })
                            },
                            onCopy = { firstRow, lastRow, firstColumn, lastColumn ->
                                copies += markdownTableFragment(table, firstRow, lastRow, firstColumn, lastColumn)
                            },
                        )
                    }
                }
            }
        }.use { scene ->
            var time = 0L
            fun render() { repeat(3) { scene.render().close() } }
            fun cell(row: Int, column: Int): Offset = cellBounds.getValue(row to column).center
            fun dismiss() {
                openedMenus.keys.toList().forEach { it.status = ContextMenuState.Status.Closed }
                openedMenus.clear()
                render()
            }
            fun rightClick(point: Offset): ContextMenuItem {
                scene.sendPointerEvent(PointerEventType.Move, point, timeMillis = ++time)
                scene.sendPointerEvent(PointerEventType.Press, point, timeMillis = ++time,
                    buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
                scene.sendPointerEvent(PointerEventType.Release, point, timeMillis = ++time,
                    buttons = PointerButtons(), button = PointerButton.Secondary)
                render()
                // 只允许表格自己的菜单打开，不能同时显示外层文字菜单。
                return openedMenus.values.single().single().also { assertEquals("复制", it.label) }
            }
            fun select(first: Offset, last: Offset) {
                scene.sendPointerEvent(PointerEventType.Move, first, timeMillis = ++time)
                scene.sendPointerEvent(PointerEventType.Press, first, timeMillis = ++time,
                    buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                scene.sendPointerEvent(PointerEventType.Move, last, timeMillis = ++time,
                    buttons = PointerButtons(isPrimaryPressed = true))
                scene.sendPointerEvent(PointerEventType.Release, last, timeMillis = ++time,
                    buttons = PointerButtons(), button = PointerButton.Primary)
                render()
            }
            render()

            assertFalse(rightClick(cell(1, 0)).enabled)
            dismiss()
            scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true))
            assertTrue(copies.isEmpty())

            select(cell(1, 0), cell(2, 1))
            val expected = "| File | Description |\n| --- | --- |\n| first.md | first |\n| second.md | second |"
            val item = rightClick(cell(1, 1))
            assertTrue(item.enabled)
            item.onClick()
            assertEquals(expected, copies.single())
            dismiss()
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true)))
            scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyUp, isCtrlPressed = true))
            assertEquals(listOf(expected, expected), copies)

            // 右键点击选区外也应保留原选区，而非偷偷替换为点击的单个单元格。
            rightClick(cell(3, 0)).onClick()
            assertEquals(expected, copies.last())
            dismiss()
            select(cell(2, 1), cell(1, 0))
            rightClick(cell(2, 1)).onClick()
            assertEquals(expected, copies.last())
            dismiss()
            select(cell(3, 0), cell(3, 0))
            rightClick(cell(3, 0)).onClick()
            assertEquals("| File |\n| --- |\n| third.md |", copies.last())
        }
    }
}
