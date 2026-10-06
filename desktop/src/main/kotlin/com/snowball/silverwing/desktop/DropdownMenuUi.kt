package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.foundation.layout.widthIn
import androidx.compose.ui.input.key.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupPositionProvider
import androidx.compose.ui.window.PopupProperties

/** 浅色主题的所有下拉统一使用纯白背景，深色主题沿用深色底以保证文字可读。 */
@Composable
internal fun silverWingMenuContainerColor(): Color =
    MaterialTheme.colorScheme.surface.let { if (it.luminance() > .5f) Color.White else it }

/** 统一菜单背景，关闭 Material 默认的色调叠加，避免白底再次染色。 */
@Composable
internal fun SilverWingDropdownMenu(
    expanded: Boolean,
    onDismissRequest: () -> Unit,
    modifier: Modifier = Modifier,
    offset: androidx.compose.ui.unit.DpOffset = androidx.compose.ui.unit.DpOffset.Zero,
    content: @Composable ColumnScope.() -> Unit,
) {
    DropdownMenu(
        expanded = expanded,
        onDismissRequest = onDismissRequest,
        modifier = modifier,
        offset = offset,
        containerColor = silverWingMenuContainerColor(),
        tonalElevation = 0.dp,
        shadowElevation = 4.dp,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        content = content,
    )
}

/** 右键菜单以鼠标位置为锚点；弹出层直接处理 Esc，不依赖正文或某个菜单项的焦点。 */
@Composable
internal fun SilverWingContextMenu(
    expanded: Boolean, onDismissRequest: () -> Unit, position: IntOffset,
    content: @Composable ColumnScope.() -> Unit,
) {
    if (!expanded) return
    val provider = remember(position) { object : PopupPositionProvider {
        override fun calculatePosition(anchorBounds: IntRect, windowSize: IntSize, layoutDirection: LayoutDirection, popupContentSize: IntSize): IntOffset =
            IntOffset((anchorBounds.left + position.x).coerceIn(0, (windowSize.width - popupContentSize.width).coerceAtLeast(0)),
                (anchorBounds.top + position.y).coerceIn(0, (windowSize.height - popupContentSize.height).coerceAtLeast(0)))
    } }
    Popup(popupPositionProvider = provider, onDismissRequest = onDismissRequest, properties = PopupProperties(focusable = true),
        onPreviewKeyEvent = { event -> if (event.type == KeyEventType.KeyDown && event.key == Key.Escape) { onDismissRequest(); true } else false }) {
        Surface(color = silverWingMenuContainerColor(), shape = MaterialTheme.shapes.extraSmall,
            shadowElevation = 4.dp, border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
            androidx.compose.foundation.layout.Column(Modifier.width(200.dp).padding(vertical = 8.dp), content = content)
        }
    }
}

/**
 * Branch menus cannot use Material [DropdownMenu]: it asks its children for intrinsic
 * measurements, while LazyColumn is a SubcomposeLayout and rejects that request.
 */
@Composable
internal fun SilverWingBranchPopup(
    expanded: Boolean,
    onDismissRequest: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    if (!expanded) return
    val positionProvider = remember { AnchoredBranchPopupPositionProvider() }
    val density = LocalDensity.current
    val maxWidth = with(density) { LocalWindowInfo.current.containerSize.width.toDp() } - 24.dp
    Popup(
        popupPositionProvider = positionProvider,
        onDismissRequest = onDismissRequest,
        properties = PopupProperties(focusable = true),
    ) {
        Surface(
            modifier = modifier.widthIn(max = maxWidth.coerceAtLeast(160.dp)).width(720.dp),
            color = silverWingMenuContainerColor(),
            shape = MaterialTheme.shapes.medium,
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            shadowElevation = 4.dp,
        ) {
            androidx.compose.foundation.layout.Column(content = content)
        }
    }
}

internal class AnchoredBranchPopupPositionProvider : PopupPositionProvider {
    override fun calculatePosition(
        anchorBounds: IntRect,
        windowSize: IntSize,
        layoutDirection: LayoutDirection,
        popupContentSize: IntSize,
    ): IntOffset {
        val preferredX = if (layoutDirection == LayoutDirection.Ltr) {
            anchorBounds.left
        } else {
            anchorBounds.right - popupContentSize.width
        }
        val x = preferredX.coerceIn(0, (windowSize.width - popupContentSize.width).coerceAtLeast(0))
        val below = anchorBounds.bottom
        val above = anchorBounds.top - popupContentSize.height
        val y = if (below + popupContentSize.height <= windowSize.height) below else above.coerceAtLeast(0)
        return IntOffset(x, y)
    }
}
