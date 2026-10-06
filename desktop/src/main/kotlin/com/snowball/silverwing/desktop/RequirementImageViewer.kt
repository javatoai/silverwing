package com.snowball.silverwing.desktop

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ZoomIn
import androidx.compose.material.icons.outlined.ZoomOut
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlin.math.roundToInt

/** 缩放与平移只留在弹窗，正文的选择和阅读位置不受影响。 */
@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
@Composable
internal fun RequirementImageViewer(painter: Painter, onClose: () -> Unit) {
    val density = LocalDensity.current
    val bounds = LocalWindowInfo.current.containerSize
    val width = with(density) { bounds.width.toDp() }.minus(48.dp).coerceIn(1.dp, 1200.dp)
    val height = with(density) { bounds.height.toDp() }.minus(48.dp).coerceIn(1.dp, 860.dp)
    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        RequirementImageViewerContent(painter, onClose, Modifier.size(width, height))
    }
}

internal fun boundedImageZoom(relative: Float, fit: Float): Float {
    val safeFit = fit.takeIf { it.isFinite() && it > 0f } ?: 1f
    val minimum = minOf(0.1f / safeFit, 1f)
    val maximum = maxOf((8.0 / safeFit).coerceAtMost(Float.MAX_VALUE.toDouble()).toFloat(), 1f)
    return (if (relative.isNaN()) 1f else relative).coerceIn(minimum, maximum)
}

internal fun fittedImageScale(intrinsic: Size, viewport: Size): Float =
    if (intrinsic.width.isFinite() && intrinsic.height.isFinite() && intrinsic.width > 0f && intrinsic.height > 0f &&
        viewport.width.isFinite() && viewport.height.isFinite() && viewport.width > 0f && viewport.height > 0f)
        minOf(viewport.width / intrinsic.width, viewport.height / intrinsic.height).takeIf { it.isFinite() && it > 0f } ?: 1f
    else 1f

internal fun boundedImageOffset(offset: Offset, zoom: Float, fit: Float, intrinsic: Size, viewport: Size): Offset {
    if (!viewport.width.isFinite() || !viewport.height.isFinite() || viewport.width <= 0f || viewport.height <= 0f) return Offset.Zero
    val knownImage = intrinsic.width.isFinite() && intrinsic.height.isFinite() && intrinsic.width > 0f && intrinsic.height > 0f
    val width = if (knownImage) intrinsic.width * fit else viewport.width
    val height = if (knownImage) intrinsic.height * fit else viewport.height
    val horizontal = ((width * zoom - viewport.width) / 2f).takeIf(Float::isFinite)?.coerceAtLeast(0f) ?: 0f
    val vertical = ((height * zoom - viewport.height) / 2f).takeIf(Float::isFinite)?.coerceAtLeast(0f) ?: 0f
    fun component(value: Float, maximum: Float): Float =
        if (maximum == 0f || !value.isFinite() || value == 0f) 0f else value.coerceIn(-maximum, maximum)
    return Offset(component(offset.x, horizontal), component(offset.y, vertical))
}

@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
@Composable
internal fun RequirementImageViewerContent(painter: Painter, onClose: (() -> Unit)?, modifier: Modifier = Modifier, title: String = "查看图片") {
    var zoom by remember(painter) { mutableFloatStateOf(1f) }
    var fittedScale by remember(painter) { mutableFloatStateOf(1f) }
    var offset by remember(painter) { mutableStateOf(Offset.Zero) }
    var viewportSize by remember(painter) { mutableStateOf(Size.Zero) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(painter) { focus.requestFocus() }
    fun setZoom(value: Float) {
        zoom = boundedImageZoom(value, fittedScale)
        offset = boundedImageOffset(offset, zoom, fittedScale, painter.intrinsicSize, viewportSize)
    }
    fun fitImage() { zoom = 1f; offset = Offset.Zero }
    fun originalSize() { setZoom(1f / fittedScale); offset = Offset.Zero }
    val controls: @Composable () -> Unit = {
        ActionIconButton("缩小图片", { setZoom(zoom / 1.25f) }, Modifier.size(32.dp)) { Icon(Icons.Outlined.ZoomOut, null) }
        Text("${(zoom * fittedScale * 100).roundToInt()}%", style = MaterialTheme.typography.labelMedium)
        ActionIconButton("放大图片", { setZoom(zoom * 1.25f) }, Modifier.size(32.dp)) { Icon(Icons.Outlined.ZoomIn, null) }
        TextButton(onClick = ::fitImage) { Text("适配") }
        TextButton(onClick = ::originalSize) { Text("原始尺寸") }
    }
        Surface(modifier.focusRequester(focus).onPreviewKeyEvent { event ->
            if (event.type != KeyEventType.KeyDown) false else when (event.key) {
                Key.Escape -> if (onClose != null) { onClose(); true } else false
                Key.Plus, Key.Equals, Key.NumPadAdd -> { setZoom(zoom * 1.25f); true }
                Key.Minus, Key.NumPadSubtract -> { setZoom(zoom / 1.25f); true }
                Key.Zero, Key.NumPad0 -> { fitImage(); true }
                Key.One, Key.NumPad1 -> { originalSize(); true }
                Key.DirectionLeft, Key.DirectionRight, Key.DirectionUp, Key.DirectionDown -> {
                    val delta = when (event.key) {
                        Key.DirectionLeft -> Offset(40f, 0f)
                        Key.DirectionRight -> Offset(-40f, 0f)
                        Key.DirectionUp -> Offset(0f, 40f)
                        else -> Offset(0f, -40f)
                    }
                    offset = boundedImageOffset(offset + delta, zoom, fittedScale, painter.intrinsicSize, viewportSize)
                    true
                }
                else -> false
            }
        }.focusable(), shape = MaterialTheme.shapes.large) {
            Column {
                BoxWithConstraints(Modifier.fillMaxWidth()) {
                    if (maxWidth < 360.dp) Column(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp)) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text(title, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                            if (onClose != null) ActionIconButton("关闭图片", onClose, Modifier.size(32.dp)) { Icon(Icons.Outlined.Close, null) }
                        }
                        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center,
                            verticalArrangement = Arrangement.spacedBy(4.dp)) { controls() }
                    } else Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(title, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                        controls()
                        if (onClose != null) ActionIconButton("关闭图片", onClose, Modifier.size(32.dp)) { Icon(Icons.Outlined.Close, null) }
                    }
                }
                HorizontalDivider()
                BoxWithConstraints(Modifier.weight(1f).fillMaxWidth().clipToBounds().background(MaterialTheme.colorScheme.surfaceVariant)) {
                    val intrinsic = painter.intrinsicSize
                    val viewport = Size(constraints.maxWidth.toFloat(), constraints.maxHeight.toFloat())
                    val fit = fittedImageScale(intrinsic, viewport)
                    SideEffect { fittedScale = fit; viewportSize = viewport }
                    LaunchedEffect(viewport, fit) {
                        zoom = boundedImageZoom(zoom, fit)
                        offset = boundedImageOffset(offset, zoom, fit, intrinsic, viewport)
                    }
                    Box(Modifier.fillMaxSize()
                        .onPointerEvent(PointerEventType.Scroll) { event ->
                            val change = event.changes.firstOrNull() ?: return@onPointerEvent
                            if (change.scrollDelta.y == 0f) return@onPointerEvent
                            val next = boundedImageZoom(zoom * if (change.scrollDelta.y < 0) 1.15f else 1f / 1.15f, fit)
                            val center = Offset(constraints.maxWidth / 2f, constraints.maxHeight / 2f)
                            offset = boundedImageOffset(change.position - center - (change.position - center - offset) * (next / zoom),
                                next, fit, intrinsic, viewport)
                            zoom = next
                            event.changes.forEach { it.consume() }
                        }.pointerInput(painter, viewport, fit) {
                            detectDragGestures { change, delta ->
                                change.consume()
                                offset = boundedImageOffset(offset + delta, zoom, fit, intrinsic, viewport)
                            }
                        }, contentAlignment = Alignment.Center) {
                        Image(painter, "需求图片", Modifier.fillMaxSize().graphicsLayer {
                            scaleX = zoom; scaleY = zoom; translationX = offset.x; translationY = offset.y
                        })
                    }
                }
                Text("滚轮或 +/− 缩放 · 拖动或方向键平移 · 0 适配 · 1 原始尺寸" + if (onClose != null) " · Esc 关闭" else "", Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
}
