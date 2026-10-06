package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.FormatListBulleted
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupPositionProvider
import androidx.compose.ui.window.PopupProperties
import kotlinx.coroutines.launch

/** A bounded, keyboard-operable outline anchored to the reader's compact toolbar. */
@Composable
internal fun MarkdownOutlineButton(
    outline: MarkdownOutlineState,
    onNavigate: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember(outline) { mutableStateOf(false) }
    var restoreFocus by remember(outline) { mutableStateOf(false) }
    val buttonFocus = remember { FocusRequester() }
    val currentNavigate by rememberUpdatedState(onNavigate)
    fun dismiss() { expanded = false; restoreFocus = true }
    fun navigate(entry: MarkdownOutlineEntry) {
        outline.navigateTo(entry)
        currentNavigate()
        dismiss()
    }
    LaunchedEffect(expanded, restoreFocus) {
        if (!expanded && restoreFocus) {
            withFrameNanos { }
            buttonFocus.requestFocus()
            restoreFocus = false
        }
    }
    Box {
        ActionIconButton(
            label = "文档目录",
            onClick = { expanded = true },
            modifier = modifier.focusRequester(buttonFocus),
            enabled = outline.entries.isNotEmpty(),
        ) { Icon(Icons.Outlined.FormatListBulleted, "文档目录", Modifier.size(17.dp)) }
        if (expanded) {
            val popupFocus = remember { FocusRequester() }
            val list = rememberLazyListState()
            val scope = rememberCoroutineScope()
            var currentIndex by remember { mutableIntStateOf(0) }
            val density = LocalDensity.current
            val windowSize = LocalWindowInfo.current.containerSize
            val maxWidth = with(density) { windowSize.width.toDp() }.minus(24.dp).coerceAtLeast(1.dp)
            val maxHeight = with(density) { windowSize.height.toDp() }.minus(24.dp).coerceAtLeast(1.dp)
            val popupPosition = remember(density) {
                val margin = with(density) { 12.dp.roundToPx() }
                val anchored = AnchoredBranchPopupPositionProvider()
                object : PopupPositionProvider {
                    override fun calculatePosition(anchorBounds: IntRect, windowSize: IntSize,
                        layoutDirection: LayoutDirection, popupContentSize: IntSize): IntOffset {
                        val position = anchored.calculatePosition(anchorBounds, windowSize, layoutDirection, popupContentSize)
                        return IntOffset(
                            position.x.coerceIn(margin, (windowSize.width - popupContentSize.width - margin).coerceAtLeast(margin)),
                            position.y.coerceIn(margin, (windowSize.height - popupContentSize.height - margin).coerceAtLeast(margin)),
                        )
                    }
                }
            }
            fun moveTo(index: Int) {
                currentIndex = index.coerceIn(outline.entries.indices)
                scope.launch { list.animateScrollToItem(currentIndex) }
            }
            Popup(
                popupPositionProvider = popupPosition,
                onDismissRequest = ::dismiss,
                properties = PopupProperties(focusable = true),
                onPreviewKeyEvent = { event ->
                    if (event.type != KeyEventType.KeyDown) false else when (event.key) {
                        Key.Escape -> { dismiss(); true }
                        Key.DirectionDown -> { moveTo(currentIndex + 1); true }
                        Key.DirectionUp -> { moveTo(currentIndex - 1); true }
                        Key.MoveHome -> { moveTo(0); true }
                        Key.MoveEnd -> { moveTo(outline.entries.lastIndex); true }
                        Key.Enter, Key.NumPadEnter -> { navigate(outline.entries[currentIndex]); true }
                        else -> false
                    }
                },
            ) {
                Surface(
                    modifier = Modifier.widthIn(max = maxWidth).width(320.dp).heightIn(max = minOf(420.dp, maxHeight)),
                    color = silverWingMenuContainerColor(),
                    shape = MaterialTheme.shapes.small,
                    shadowElevation = 4.dp,
                ) {
                    Column(Modifier.focusRequester(popupFocus).focusable()) {
                        Text("文档目录", Modifier.padding(horizontal = 14.dp, vertical = 10.dp),
                            style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                        LazyColumn(state = list, modifier = Modifier.weight(1f, fill = false), contentPadding = PaddingValues(vertical = 4.dp)) {
                            itemsIndexed(outline.entries, key = { _, entry -> entry.anchor }) { index, entry ->
                                DropdownMenuItem(
                                    text = {
                                        Text(entry.title, maxLines = 2, overflow = TextOverflow.Ellipsis,
                                            style = MaterialTheme.typography.bodyMedium,
                                            fontWeight = if (entry.level <= 2) FontWeight.SemiBold else FontWeight.Normal)
                                    },
                                    modifier = Modifier.fillMaxWidth()
                                        .onFocusChanged { if (it.isFocused) currentIndex = index }
                                        .background(if (index == currentIndex) MaterialTheme.colorScheme.primary.copy(alpha = 0.10f) else Color.Transparent)
                                        .semantics { contentDescription = "目录 H${entry.level} ${entry.title}"; selected = index == currentIndex },
                                    contentPadding = PaddingValues(start = (14 + (entry.level - 1) * 12).dp, end = 14.dp),
                                    onClick = { navigate(entry) },
                                )
                            }
                        }
                    }
                }
                LaunchedEffect(Unit) { popupFocus.requestFocus() }
            }
        }
    }
}
