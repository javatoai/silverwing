@file:OptIn(androidx.compose.foundation.ExperimentalFoundationApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.hoverable
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ChevronLeft
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupProperties
import java.awt.Cursor
import kotlin.math.roundToInt

/** 四种右侧视图共用同一侧栏；窄窗口仅改变导航呈现，不改变正在阅读的任务。 */
@Composable
internal fun TaskWorkspaceLayout(
    session: TaskBrowsingSession,
    taskName: String?,
    modifier: Modifier = Modifier,
    onPersistLayout: (Int) -> Unit = WindowPreferences::saveTaskListPaneWidth,
    listPane: @Composable (Modifier, () -> Unit) -> Unit,
    content: @Composable (Modifier) -> Unit,
) {
    val inputMode = androidx.compose.ui.platform.LocalInputModeManager.current
    BoxWithConstraints(modifier.fillMaxSize().padding(8.dp).onPreviewKeyEvent { event ->
        if (event.type == KeyEventType.KeyDown && event.key == Key.Tab) inputMode.requestInputMode(androidx.compose.ui.input.InputMode.Keyboard)
        false
    }) {
        val availableWidth = maxWidth.value
        var dragWidth by remember { mutableStateOf<Float?>(null) }
        val normalWidth = resolveTaskListPaneWidth(session.preferredWidth, availableWidth)
        LaunchedEffect(availableWidth) { dragWidth = null }
        val startDrag = { dragWidth = normalWidth }
        val resize: (Float) -> Unit = { delta -> dragWidth = resolveTaskListPaneWidth((dragWidth ?: normalWidth) + delta, availableWidth) }
        val endDrag = {
            session.preferredWidth = (dragWidth ?: normalWidth).coerceAtLeast(MIN_TASK_LIST_PANE_WIDTH_DP)
            onPersistLayout(session.preferredWidth.roundToInt())
            dragWidth = null
        }
        Row(Modifier.fillMaxSize()) {
            // 所有窗口尺寸均保留任务列表，拖动只调整合法宽度，不再进入收起状态。
            Box(Modifier.width((dragWidth ?: normalWidth).dp).fillMaxHeight().clipToBounds()) { listPane(Modifier.fillMaxSize()) { } }
            TaskPaneDragHandle(Modifier.width(TASK_LIST_PANE_HANDLE_WIDTH_DP.dp), startDrag, resize, endDrag, { dragWidth = null })
            Column(Modifier.weight(1f).fillMaxHeight()) {
                TaskWorkspaceToolbar(session.view) { session.view = it }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                content(Modifier.weight(1f).fillMaxWidth())
            }
        }
    }
}

@Composable
private fun TaskWorkspaceToolbar(view: TaskContentView, onViewChange: (TaskContentView) -> Unit) {
    var width by remember { mutableIntStateOf(0) }
    Row(Modifier.fillMaxWidth().onSizeChanged { width = it.width }.padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
        val tabPadding = if (with(LocalDensity.current) { width.toDp() } < 340.dp) 4.dp else 10.dp
        Row(Modifier.weight(1f).horizontalScroll(rememberScrollState()).selectableGroup(), horizontalArrangement = Arrangement.spacedBy(2.dp)) {
            TaskContentView.entries.forEach { item ->
                val selected = view == item
                val bring = remember { BringIntoViewRequester() }
                LaunchedEffect(selected, width) { if (selected) { withFrameNanos { }; bring.bringIntoView() } }
                val interaction = remember(item) { MutableInteractionSource() }
                val focused by interaction.collectIsFocusedAsState()
                Surface(shape = MaterialTheme.shapes.small,
                    color = if (selected) MaterialTheme.colorScheme.primary.copy(alpha = 0.10f) else Color.Transparent) {
                    Text(item.label, Modifier.bringIntoViewRequester(bring).border(1.dp, if (focused) MaterialTheme.colorScheme.primary else Color.Transparent, MaterialTheme.shapes.small)
                        .selectable(selected, interactionSource = interaction, indication = null, role = Role.Tab, onClick = { onViewChange(item) })
                        .padding(horizontal = tabPadding, vertical = 8.dp), style = MaterialTheme.typography.bodyMedium, maxLines = 1, softWrap = false,
                        color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

@Composable
private fun TaskPaneDragHandle(
    modifier: Modifier,
    onStart: () -> Unit,
    onDrag: (Float) -> Unit,
    onEnd: () -> Unit,
    onCancel: () -> Unit,
) {
    val start by rememberUpdatedState(onStart)
    val drag by rememberUpdatedState(onDrag)
    val end by rememberUpdatedState(onEnd)
    val cancel by rememberUpdatedState(onCancel)
    val density = LocalDensity.current
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()
    Box(modifier.fillMaxHeight()
        .semantics { contentDescription = "拖动调整任务列表宽度" }
        .pointerHoverIcon(PointerIcon(Cursor.getPredefinedCursor(Cursor.E_RESIZE_CURSOR)))
        .hoverable(interaction)
        .pointerInput(density) {
            detectHorizontalDragGestures(onDragStart = { start() }, onHorizontalDrag = { change, amount ->
                change.consume(); drag(with(density) { amount.toDp().value })
            }, onDragEnd = { end() }, onDragCancel = { cancel() })
        }, contentAlignment = Alignment.Center) {
        Box(Modifier.fillMaxHeight().padding(vertical = 8.dp).width(1.dp)
            .background(if (hovered) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant))
    }
}
