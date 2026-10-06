package com.snowball.silverwing.desktop

import androidx.compose.foundation.Image
import androidx.compose.foundation.HorizontalScrollbar
import androidx.compose.foundation.VerticalScrollbar
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.rememberScrollbarAdapter
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.NavigateBefore
import androidx.compose.material.icons.automirrored.outlined.NavigateNext
import androidx.compose.material.icons.outlined.ZoomIn
import androidx.compose.material.icons.outlined.ZoomOut
import androidx.compose.material.icons.outlined.List
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.size
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.isCtrlPressed
import androidx.compose.ui.input.pointer.onPointerEvent
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.nio.file.Path
import kotlin.math.roundToInt

@Composable
@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
internal fun RequirementMaterialsPdfPreview(
    root: Path,
    relativePath: String,
    refreshKey: Any,
    modifier: Modifier = Modifier,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
    if (LocalDocumentFind.current == null) {
        val find = remember(root, relativePath) { DocumentFindState() }
        DocumentFindScope(find, modifier) {
            Column(Modifier.fillMaxSize()) {
                Row(Modifier.fillMaxWidth()) { FindDocumentButton() }
                DocumentFindBar(find)
                RequirementMaterialsPdfPreview(root, relativePath, refreshKey, Modifier.weight(1f).fillMaxWidth(), ioDispatcher)
            }
        }
        return
    }
    // 页面和文档仅属于当前预览，阅读位置由外层会话提供，切换文件后释放渲染资源。
    key(root, relativePath) {
        val service = remember { RequirementMaterialsPdfService() }
        val renderMutex = remember { Mutex() }
        val scope = rememberCoroutineScope()
        val density = LocalDensity.current
        var zoomPercent by (LocalMaterialsReadingState.current?.zoomPercent ?: remember { mutableIntStateOf(100) })
        var retry by remember { mutableIntStateOf(0) }
        var document by remember { mutableStateOf<RequirementMaterialsPdfDocument?>(null) }
        var loading by remember { mutableStateOf(true) }
        var error by remember { mutableStateOf<String?>(null) }
        var textIndex by remember { mutableStateOf<PdfTextIndex?>(null) }
        var textError by remember { mutableStateOf<String?>(null) }
        var textRetry by remember { mutableIntStateOf(0) }
        val listState = rememberMaterialsLazyListState("pdf-pages", ready = !loading && document != null)
        val horizontal = rememberMaterialsScrollState("pdf-horizontal", ready = !loading && document != null)
        val clipboard = LocalClipboardManager.current
        val selection = remember(document) { PdfTextSelection() }
        LaunchedEffect(document, textRetry) {
            textIndex = null; textError = null
            document?.let { pdf ->
                try { textIndex = runInterruptible(ioDispatcher) { pdf.textIndex() } }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { textError = failure.message ?: "PDF 文字读取失败" }
            }
        }

        LaunchedEffect(refreshKey, retry) {
            loading = true
            error = null
            document = null
            try {
                document = runInterruptible(ioDispatcher) { service.open(root, relativePath) }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                error = failure.message ?: "无法预览 PDF，请重试。"
            } finally {
                loading = false
            }
        }
        BoxWithConstraints(modifier.onPreviewKeyEvent { event ->
            val index = textIndex
            if (event.type == KeyEventType.KeyDown && event.isCtrlPressed && event.key == Key.C && index != null && selection.hasText(index)) {
                clipboard.setText(AnnotatedString(selection.text(index)))
                true
            } else false
        }.focusable()) {
            val fitWidth = (maxWidth - 32.dp).coerceAtLeast(64.dp)
            val displayWidth = fitWidth * (zoomPercent / 100f)
            val contentWidth = maxOf(maxWidth, displayWidth + 32.dp)
            val renderWidth = with(density) { displayWidth.toPx().roundToInt().coerceAtLeast(1) }
            val currentDocument = document
            val count = currentDocument?.pageCount ?: 0
            val lastPage = (count - 1).coerceAtLeast(0)
            // 最后一页短于视口时，滚动会在文档底部停止，此时仍应显示最后一页的页码。
            val pageIndex = if (!listState.canScrollForward && listState.canScrollBackward) lastPage
                else listState.firstVisibleItemIndex.coerceIn(0, lastPage)
            fun changeZoom(requestedPercent: Int) {
                val percent = requestedPercent.coerceIn(25, 200)
                if (percent == zoomPercent || loading || document == null) return
                // 手势与按钮可能复用回调，触发时读取当前页面，避免拿到之前的页码或加载状态。
                val index = if (!listState.canScrollForward && listState.canScrollBackward)
                    (document!!.pageCount - 1).coerceAtLeast(0) else listState.firstVisibleItemIndex
                val offset = if (index == listState.firstVisibleItemIndex)
                    (listState.firstVisibleItemScrollOffset * (percent.toFloat() / zoomPercent)).roundToInt() else 0
                zoomPercent = percent
                // 将定位交给下一次布局，避免按旧页面高度截断位置后跳回前一页。
                listState.requestScrollToItem(index, offset)
            }
            val find = LocalDocumentFind.current
            val currentWidth by rememberUpdatedState(renderWidth)
            val currentContentWidth by rememberUpdatedState(with(density) { contentWidth.toPx() })
            val currentViewportWidth by rememberUpdatedState(with(density) { maxWidth.toPx() })
            val activeTextIndex = textIndex
            DisposableEffect(activeTextIndex, currentDocument, find) {
                activeTextIndex?.pages?.forEachIndexed { index, page ->
                    val aspect = currentDocument?.takeIf { index < it.pageCount }?.aspectRatio(index) ?: return@forEachIndexed
                    find?.blocks?.set("pdf:$index", FindBlock(page.text, index.toLong()) { range ->
                        val bounds = page.glyphs.firstOrNull { it.range.first <= range.last && it.range.last >= range.first }?.bounds
                        val top = bounds?.top ?: 0f
                        listState.scrollToItem(index, ((currentWidth / aspect) * top).roundToInt())
                        bounds?.let {
                            val left = (currentContentWidth - currentWidth) / 2f + it.left * currentWidth
                            val right = (currentContentWidth - currentWidth) / 2f + it.right * currentWidth
                            val position = pdfFindHorizontalOffset(left, right, horizontal.value, currentViewportWidth)
                            horizontal.scrollTo(position)
                        }
                    })
                }
                onDispose { activeTextIndex?.pages?.indices?.forEach { find?.blocks?.remove("pdf:$it") } }
            }
            Column(Modifier.fillMaxSize()) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    PdfOutlineMenu(textIndex?.outline.orEmpty(), textIndex != null) { index -> scope.launch { listState.scrollToItem(index) } }
                    Box(Modifier.weight(1f)) { PdfPreviewControls(
                    pageIndex = pageIndex,
                    pageCount = count,
                    zoomPercent = zoomPercent,
                    enabled = !loading && currentDocument != null,
                    onPageChange = { index -> scope.launch { listState.scrollToItem(index) } },
                    onZoomChange = ::changeZoom,
                ) }
                }
                textError?.let { message -> Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(message, Modifier.weight(1f), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = { textRetry++ }) { Text("重试文字读取") }
                } }
                if (loading) LinearProgressIndicator(Modifier.fillMaxWidth().height(4.dp))
                else Box(Modifier.fillMaxWidth().height(4.dp))
                Box(
                    Modifier.weight(1f).fillMaxWidth().background(MaterialTheme.colorScheme.surfaceVariant)
                        .onPointerEvent(PointerEventType.Scroll, pass = PointerEventPass.Initial) { event ->
                            if (event.keyboardModifiers.isCtrlPressed) {
                                val delta = event.changes.sumOf { it.scrollDelta.y.toDouble() }
                                if (delta.isFinite() && delta != 0.0) {
                                    // 先消费 Ctrl 滚轮，防止缩放时同时触发连续翻页；到缩放边界也照样消费。
                                    event.changes.forEach { it.consume() }
                                    changeZoom(zoomPercent + if (delta < 0) 25 else -25)
                                }
                            }
                        },
                ) {
                    val currentError = error
                    when {
                        currentError != null -> PdfPreviewError(currentError, onRetry = { retry++ })
                        loading -> Text(
                            "正在加载 PDF…", Modifier.align(Alignment.Center).padding(16.dp),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        currentDocument != null -> {
                            Box(Modifier.fillMaxSize().horizontalScroll(horizontal)) {
                                LazyColumn(
                                    state = listState,
                                    modifier = Modifier.width(contentWidth).fillMaxHeight(),
                                    contentPadding = PaddingValues(vertical = 16.dp),
                                    verticalArrangement = Arrangement.spacedBy(16.dp),
                                ) {
                                    items(count = currentDocument.pageCount, key = { it }) { index ->
                                        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.TopCenter) {
                                            PdfPreviewPage(
                                                document = currentDocument,
                                                pageIndex = index,
                                                renderWidth = renderWidth,
                                                renderMutex = renderMutex,
                                                ioDispatcher = ioDispatcher,
                                                textIndex = textIndex,
                                                selection = selection,
                                                modifier = Modifier.width(displayWidth).aspectRatio(currentDocument.aspectRatio(index)),
                                            )
                                        }
                                    }
                                }
                            }
                            VerticalScrollbar(rememberScrollbarAdapter(listState), Modifier.align(Alignment.CenterEnd).padding(bottom = 12.dp))
                            HorizontalScrollbar(rememberScrollbarAdapter(horizontal), Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(end = 12.dp))
                        }
                    }
                }
            }
        }
    }
}

/** 固定页面占位尺寸，后台串行渲染可见页；滚动时既不跳高，也不一次生成整份文档的位图。 */
@Composable
private fun PdfPreviewPage(
    document: RequirementMaterialsPdfDocument,
    pageIndex: Int,
    renderWidth: Int,
    renderMutex: Mutex,
    ioDispatcher: CoroutineDispatcher,
    modifier: Modifier,
    textIndex: PdfTextIndex?,
    selection: PdfTextSelection,
) {
    var page by remember(document, pageIndex, renderWidth) { mutableStateOf<RequirementMaterialsPdfPage?>(null) }
    var error by remember(document, pageIndex, renderWidth) { mutableStateOf<String?>(null) }
    var retry by remember(document, pageIndex) { mutableIntStateOf(0) }
    LaunchedEffect(document, pageIndex, renderWidth, retry) {
        error = null
        try {
            page = renderMutex.withLock {
                runInterruptible(ioDispatcher) { document.render(pageIndex, renderWidth) }
            }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            error = failure.message ?: "无法加载此页，请重试。"
        }
    }
    Box(modifier.background(Color.White)) {
        val current = page
        val currentError = error
        when {
            currentError != null -> PdfPreviewError("第 ${pageIndex + 1} 页：$currentError", onRetry = { retry++ }, onPaper = true)
            current != null -> Image(
                bitmap = current.image,
                contentDescription = "PDF 第 ${pageIndex + 1} 页，共 ${document.pageCount} 页",
                modifier = Modifier.fillMaxSize(),
            )
            else -> Text("正在加载第 ${pageIndex + 1} 页…", Modifier.padding(20.dp), color = Color(0xFF526178))
        }
        if (current != null && textIndex != null) PdfTextOverlay(textIndex, pageIndex, selection)
    }
}

@Composable
private fun PdfPreviewError(message: String, onRetry: () -> Unit, onPaper: Boolean = false) {
    val errorColor = if (onPaper) Color(0xFFDC2626) else MaterialTheme.colorScheme.error
    Column(Modifier.fillMaxWidth().padding(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("无法预览 PDF", style = MaterialTheme.typography.titleSmall, color = errorColor)
        Text(message, color = errorColor)
        TextButton(onClick = onRetry, colors = ButtonDefaults.textButtonColors(
            contentColor = if (onPaper) Color(0xFF356AE6) else MaterialTheme.colorScheme.primary,
        )) { Text("重试") }
    }
}

@Composable
internal fun PdfPreviewControls(
    pageIndex: Int,
    pageCount: Int,
    zoomPercent: Int,
    enabled: Boolean,
    onPageChange: (Int) -> Unit,
    onZoomChange: (Int) -> Unit,
) {
    FlowRow(Modifier.fillMaxWidth().padding(bottom = 6.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            ActionIconButton("上一页", onClick = { onPageChange(pageIndex - 1) }, enabled = enabled && pageIndex > 0) {
                Icon(Icons.AutoMirrored.Outlined.NavigateBefore, "上一页")
            }
            Text(if (pageCount > 0) "${pageIndex + 1} / $pageCount 页" else "PDF", style = MaterialTheme.typography.labelLarge)
            ActionIconButton("下一页", onClick = { onPageChange(pageIndex + 1) }, enabled = enabled && pageIndex + 1 < pageCount) {
                Icon(Icons.AutoMirrored.Outlined.NavigateNext, "下一页")
            }
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            ActionIconButton("缩小", onClick = { onZoomChange((zoomPercent - 25).coerceAtLeast(25)) }, enabled = enabled && zoomPercent > 25) {
                Icon(Icons.Outlined.ZoomOut, "缩小")
            }
            Text("$zoomPercent%", style = MaterialTheme.typography.labelLarge)
            ActionIconButton("放大", onClick = { onZoomChange((zoomPercent + 25).coerceAtMost(200)) }, enabled = enabled && zoomPercent < 200) {
                Icon(Icons.Outlined.ZoomIn, "放大")
            }
            TextButton(onClick = { onZoomChange(100) }, enabled = enabled) { Text("适应宽度") }
        }
    }
}

@Composable private fun PdfOutlineMenu(entries: List<PdfOutlineEntry>, loaded: Boolean, onNavigate: (Int) -> Unit) {
    var open by remember { mutableStateOf(false) }
    LaunchedEffect(entries, loaded) { open = false }
    Box {
        ActionIconButton(if (!loaded) "正在读取 PDF 目录" else if (entries.isEmpty()) "PDF 没有目录书签" else "PDF 目录书签",
            { open = true }, Modifier.size(30.dp), enabled = entries.isNotEmpty()) { Icon(Icons.Outlined.List, null, Modifier.size(18.dp)) }
        SilverWingDropdownMenu(open, { open = false }, modifier = Modifier.heightIn(max = 360.dp)) {
            entries.forEach { item -> DropdownMenuItem(text = { Text("${"  ".repeat(item.depth.coerceAtMost(8))}${item.title}", maxLines = 2) },
                trailingIcon = { Text("${item.page + 1}", style = MaterialTheme.typography.labelSmall) },
                onClick = { open = false; onNavigate(item.page) }) }
        }
    }
}

internal fun pdfFindHorizontalOffset(left: Float, right: Float, current: Int, viewportWidth: Float): Int = when {
    left < current -> (left - 16f).roundToInt().coerceAtLeast(0)
    right > current + viewportWidth -> (right - viewportWidth + 16f).roundToInt().coerceAtLeast(0)
    else -> current
}
