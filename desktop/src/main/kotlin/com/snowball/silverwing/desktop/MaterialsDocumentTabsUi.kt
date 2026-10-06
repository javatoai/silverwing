package com.snowball.silverwing.desktop

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.HorizontalScrollbar
import androidx.compose.foundation.LocalIndication
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.rememberScrollbarAdapter
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Text
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch

/** The caller synchronizes selection with its active preview; directory action selection is untouched. */
@Composable
@OptIn(ExperimentalFoundationApi::class, ExperimentalMaterial3Api::class)
internal fun MaterialsDocumentTabs(
    state: MaterialsDocumentTabsState,
    onSelect: (String) -> Unit = { state.select(it) },
    onClose: (String) -> Unit = { state.close(it) },
    onPin: (String) -> Unit = { state.pin(it) },
    modifier: Modifier = Modifier,
) {
    if (state.tabs.isEmpty()) return
    val scroll = rememberScrollState()
    val scope = rememberCoroutineScope()
    val focusRequesters = remember(state) { mutableMapOf<String, FocusRequester>() }
    var width by remember { mutableIntStateOf(0) }
    Column(modifier.onSizeChanged { width = it.width }.background(MaterialTheme.colorScheme.surfaceContainerLow).testTag("materials-document-tabs")) {
        Row(Modifier.fillMaxWidth().horizontalScroll(scroll).selectableGroup().padding(top = 3.dp, start = 4.dp, end = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(2.dp)) {
            state.tabs.forEach { tab -> key(tab.path) {
                val active = tab.path == state.activePath
                val focus = remember { FocusRequester() }
                val bring = remember { BringIntoViewRequester() }
                val interactions = remember { MutableInteractionSource() }
                val focused by interactions.collectIsFocusedAsState()
                val hovered by interactions.collectIsHoveredAsState()
                DisposableEffect(tab.path, focus) {
                    focusRequesters[tab.path] = focus
                    onDispose { focusRequesters.remove(tab.path) }
                }
                LaunchedEffect(active, tab.path, width) {
                    if (active) {
                        withFrameNanos { }
                        // Keep the leading gutter when returning to the first document.
                        if (state.tabs.firstOrNull()?.path == tab.path) scroll.scrollTo(0)
                        else bring.bringIntoView()
                    }
                }
                val shape = RoundedCornerShape(topStart = 5.dp, topEnd = 5.dp)
                val colors = MaterialTheme.colorScheme
                val background = when { active -> colors.primaryContainer; hovered -> colors.surfaceContainerHigh; else -> colors.surfaceContainerLow }
                val foreground = if (active) colors.onPrimaryContainer else colors.onSurfaceVariant
                fun selectAndFocus(path: String?) {
                    if (path != null) { onSelect(path); focusRequesters[path]?.requestFocus() }
                }
                fun closeAndFocus() {
                    val next = state.adjacentPath(tab.path, 1)?.takeUnless { it == tab.path }
                        ?: state.adjacentPath(tab.path, -1)?.takeUnless { it == tab.path }
                    onClose(tab.path)
                    next?.let { focusRequesters[it]?.requestFocus() }
                }
                fun pinAndFocus() {
                    onPin(tab.path)
                    // The pin button disappears; restore focus after its focus target is disposed.
                    scope.launch {
                        withFrameNanos { }
                        focusRequesters[tab.path]?.requestFocus()
                    }
                }
                TooltipBox(positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
                    tooltip = { PlainTooltip { Text(if (tab.isPinned) tab.path else "${tab.path}\n双击或固定按钮保留标签") } },
                    state = rememberTooltipState()) {
                    Row(Modifier.widthIn(min = 132.dp, max = 248.dp).height(38.dp)
                        .clip(shape).background(background)
                        .then(if (focused) Modifier.border(1.dp, colors.primary, shape) else Modifier)
                        .bringIntoViewRequester(bring).focusRequester(focus)
                        .onPreviewKeyEvent { event ->
                            if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            when {
                                event.key == Key.DirectionLeft -> { selectAndFocus(state.adjacentPath(tab.path, -1)); true }
                                event.key == Key.DirectionRight -> { selectAndFocus(state.adjacentPath(tab.path, 1)); true }
                                event.key == Key.MoveHome -> { selectAndFocus(state.tabs.firstOrNull()?.path); true }
                                event.key == Key.MoveEnd -> { selectAndFocus(state.tabs.lastOrNull()?.path); true }
                                event.key == Key.Delete || (event.isCtrlPressed && event.key == Key.W) -> { closeAndFocus(); true }
                                event.isCtrlPressed && event.key == Key.Enter -> { pinAndFocus(); true }
                                event.key == Key.Enter || event.key == Key.Spacebar -> { selectAndFocus(tab.path); true }
                                else -> false
                            }
                        }
                        .combinedClickable(interactionSource = interactions, indication = LocalIndication.current,
                            role = Role.Tab, onClick = { selectAndFocus(tab.path) }, onDoubleClick = { selectAndFocus(tab.path); pinAndFocus() })
                        .semantics {
                            selected = active
                            contentDescription = "任务资料标签：${tab.path}"
                            stateDescription = if (tab.isPinned) "已固定" else "临时预览"
                            customActions = listOfNotNull(
                                if (!tab.isPinned) CustomAccessibilityAction("固定标签") { pinAndFocus(); true } else null,
                                CustomAccessibilityAction("关闭标签") { closeAndFocus(); true },
                            )
                        }.testTag("materials-tab:${tab.path}").padding(start = 10.dp, end = 4.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        Text(tab.path.substringAfterLast('/'), Modifier.widthIn(max = 164.dp), color = foreground,
                            style = MaterialTheme.typography.labelMedium,
                            fontWeight = if (active) FontWeight.SemiBold else FontWeight.Normal,
                            fontStyle = if (tab.isPinned) FontStyle.Normal else FontStyle.Italic,
                            maxLines = 1, overflow = TextOverflow.Ellipsis, softWrap = false)
                        Spacer(Modifier.width(5.dp))
                        if (!tab.isPinned) IconButton(onClick = { pinAndFocus() }, modifier = Modifier.size(28.dp)) {
                            Icon(Icons.Outlined.PushPin, "固定标签：${tab.path}", Modifier.size(14.dp), tint = foreground)
                        }
                        IconButton(onClick = { closeAndFocus() }, modifier = Modifier.size(28.dp)) {
                            Icon(Icons.Outlined.Close, "关闭标签：${tab.path}", Modifier.size(15.dp), tint = foreground)
                        }
                    }
                }
            } }
        }
        if (scroll.maxValue > 0) HorizontalScrollbar(rememberScrollbarAdapter(scroll), Modifier.fillMaxWidth().height(5.dp))
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
    }
}
