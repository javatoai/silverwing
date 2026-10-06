package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import java.nio.file.Path

internal sealed interface MaterialsFullTextSearchUiState {
    data object Idle : MaterialsFullTextSearchUiState
    data class Loading(val progress: MaterialsFullTextSearchProgress? = null) : MaterialsFullTextSearchUiState
    data class Loaded(val result: MaterialsFullTextSearchResult) : MaterialsFullTextSearchUiState
    data class Failed(val message: String) : MaterialsFullTextSearchUiState
    data object Stopped : MaterialsFullTextSearchUiState
}

/** Ctrl+Shift+F entry point; the owner only needs to provide the task root and reader navigation. */
@Composable
internal fun MaterialsFullTextSearchDialog(
    root: Path,
    onDismiss: () -> Unit,
    onNavigate: (MaterialsFullTextSearchHit) -> Unit,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    initialQuery: String = "",
) {
    var query by remember(root) { mutableStateOf(initialQuery) }
    var retry by remember(root) { mutableIntStateOf(0) }
    // A previous request can finish its last progress callback after cancellation; it only
    // owns its previous holder, so it cannot replace the next query's state.
    val state = remember(root, query, retry) { mutableStateOf<MaterialsFullTextSearchUiState>(
        if (query.isEmpty()) MaterialsFullTextSearchUiState.Idle else MaterialsFullTextSearchUiState.Loading()) }
    var searchJob by remember(root, query, retry) { mutableStateOf<Job?>(null) }
    val service = remember(ioDispatcher) { MaterialsFullTextSearchService(ioDispatcher = ioDispatcher) }
    LaunchedEffect(root, query, retry) {
        if (query.isEmpty()) return@LaunchedEffect
        searchJob = currentCoroutineContext().job
        try {
            delay(180)
            coroutineScope {
                val updates = Channel<MaterialsFullTextSearchProgress>(Channel.CONFLATED)
                val updater = launch {
                    for (progress in updates) state.value = MaterialsFullTextSearchUiState.Loading(progress)
                }
                try {
                    val result = service.search(root, query) { updates.trySend(it) }
                    updater.cancel()
                    state.value = MaterialsFullTextSearchUiState.Loaded(result)
                } finally {
                    updates.close()
                    updater.cancel()
                }
            }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            state.value = MaterialsFullTextSearchUiState.Failed(failure.message ?: "无法搜索任务资料，请重试。")
        }
    }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.padding(16.dp).widthIn(max = 720.dp).fillMaxWidth().heightIn(max = 640.dp),
            color = MaterialTheme.colorScheme.surface, shape = RoundedCornerShape(12.dp), shadowElevation = 10.dp) {
            MaterialsFullTextSearchContent(query, { query = it }, state.value, onDismiss,
                { hit -> onNavigate(hit); onDismiss() }, { retry++ },
                onStop = { searchJob?.cancel(); state.value = MaterialsFullTextSearchUiState.Stopped })
        }
    }
}

/** Kept separate from the worker so all empty/loading/error states can be rendered deterministically. */
@Composable
internal fun MaterialsFullTextSearchContent(
    query: String,
    onQueryChange: (String) -> Unit,
    state: MaterialsFullTextSearchUiState,
    onDismiss: () -> Unit,
    onNavigate: (MaterialsFullTextSearchHit) -> Unit,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
    onStop: (() -> Unit)? = null,
) {
    val hits = (state as? MaterialsFullTextSearchUiState.Loaded)?.result?.hits.orEmpty()
    var selected by remember(query) { mutableIntStateOf(0) }
    var showFailures by remember(query, state) { mutableStateOf(false) }
    val focus = remember { FocusRequester() }
    val list = rememberLazyListState()
    LaunchedEffect(Unit) { withFrameNanos { }; focus.requestFocus() }
    LaunchedEffect(hits) { selected = 0 }
    LaunchedEffect(selected, hits) { if (hits.isNotEmpty()) list.scrollToItem(selected.coerceIn(0, hits.lastIndex)) }
    Column(modifier.testTag("materials-full-text-search").padding(18.dp).onPreviewKeyEvent { event ->
        if (event.type != KeyEventType.KeyDown) false else when (event.key) {
            Key.Escape -> { onDismiss(); true }
            Key.DirectionDown -> { selected = (selected + 1).coerceAtMost(hits.lastIndex.coerceAtLeast(0)); true }
            Key.DirectionUp -> { selected = (selected - 1).coerceAtLeast(0); true }
            Key.Enter -> { hits.getOrNull(selected)?.let(onNavigate); true }
            else -> false
        }
    }, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text("搜索任务资料", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            ActionIconButton("关闭全文搜索 (Esc)", onDismiss, Modifier.size(28.dp)) { Icon(Icons.Outlined.Close, null, Modifier.size(18.dp)) }
        }
        OutlinedTextField(query, onQueryChange, singleLine = true, label = { Text("搜索当前任务的全部资料") },
            leadingIcon = { Icon(Icons.Outlined.Search, null, Modifier.size(18.dp)) },
            modifier = Modifier.fillMaxWidth().focusRequester(focus),
            textStyle = MaterialTheme.typography.bodyMedium)
        Box(Modifier.fillMaxWidth().weight(1f, fill = false).heightIn(min = 148.dp, max = 390.dp)) {
            when (state) {
                MaterialsFullTextSearchUiState.Idle -> SearchMessage("输入文字，查找当前任务的任务资料", "支持文本、代码和 PDF · 按原文匹配，忽略大小写")
                is MaterialsFullTextSearchUiState.Loading -> Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    val progress = state.progress
                    Text(if (progress == null) "正在准备搜索…" else "正在搜索 ${progress.completedFiles} / ${progress.totalFiles} 个文件…",
                        style = MaterialTheme.typography.bodyMedium)
                    progress?.currentPath?.let { Text(it, style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                    onStop?.let { TextButton(onClick = it) { Text("停止搜索") } }
                }
                is MaterialsFullTextSearchUiState.Failed -> Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    SearchMessage("无法搜索任务资料", state.message, error = true)
                    TextButton(onClick = onRetry) { Text("重试搜索") }
                }
                MaterialsFullTextSearchUiState.Stopped -> Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    SearchMessage("搜索已停止", "修改搜索文字，或重新搜索。")
                    TextButton(onClick = onRetry) { Text("重新搜索") }
                }
                is MaterialsFullTextSearchUiState.Loaded -> Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    val result = state.result
                    if (hits.isEmpty()) {
                        SearchMessage(if (result.searchedFiles == 0 && result.failureCount > 0) "资料读取失败" else "没有找到匹配内容",
                            when {
                                result.searchedFiles == 0 && result.failureCount > 0 -> "可搜索的资料均未读取成功。查看跳过原因后重试。"
                                result.totalFiles == 0 -> "此目录没有可搜索的文本、代码或 PDF 文件。"
                                else -> "已搜索 ${result.searchedFiles} 个文件。试试更短的文字。"
                            })
                    } else {
                        Text("${if (result.truncated) "前 " else ""}${hits.size} 处匹配 · ${hits.map { it.relativePath }.distinct().size} 个文件",
                            style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        LazyColumn(Modifier.weight(1f, fill = false).fillMaxWidth(), state = list) {
                            itemsIndexed(hits, key = { _, hit -> "${hit.relativePath}:${hit.pageIndex}:${hit.offset}" }) { index, hit ->
                                SearchResultRow(hit, index == selected, Modifier.testTag("materials-search-result-$index")) { selected = index; onNavigate(hit) }
                            }
                        }
                    }
                    if (result.truncated) Text("已达到本次搜索上限。输入更具体的文字后继续查找。",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    if (result.failureCount > 0) {
                        TextButton(onClick = { showFailures = !showFailures }, contentPadding = PaddingValues(horizontal = 0.dp, vertical = 4.dp)) {
                            Text("${result.failureCount} 项未能搜索 · ${if (showFailures) "收起原因" else "查看原因"}")
                        }
                        if (showFailures) LazyColumn(Modifier.fillMaxWidth().heightIn(max = 124.dp)) {
                            itemsIndexed(result.failures) { _, failure ->
                                Text("${failure.relativePath.ifEmpty { "资料目录" }}：${failure.message}", Modifier.padding(vertical = 4.dp),
                                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error, maxLines = 3, overflow = TextOverflow.Ellipsis)
                            }
                        }
                        if (result.searchedFiles == 0) TextButton(onClick = onRetry) { Text("重试搜索") }
                    }
                }
            }
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Text("↑ ↓ 选择 · Enter 打开位置 · Esc 关闭", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun SearchMessage(title: String, detail: String, error: Boolean = false) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium,
            color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface)
        Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun SearchResultRow(hit: MaterialsFullTextSearchHit, selected: Boolean, modifier: Modifier, onOpen: () -> Unit) {
    val primary = MaterialTheme.colorScheme.primary
    val annotatedSnippet = remember(hit, primary) { buildAnnotatedString {
        append(hit.snippet)
        val range = hit.snippetMatchRange
        if (!range.isEmpty()) addStyle(SpanStyle(color = primary, fontWeight = FontWeight.SemiBold), range.first, range.last + 1)
    } }
    Column(modifier.fillMaxWidth().background(if (selected) MaterialTheme.colorScheme.primaryContainer else Color.Transparent)
        .selectable(selected, onClick = onOpen, role = Role.Button).padding(horizontal = 10.dp, vertical = 9.dp),
        verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(hit.fileName, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(hit.locationLabel, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (hit.relativePath != hit.fileName) Text(hit.relativePath, style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(annotatedSnippet, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
}
