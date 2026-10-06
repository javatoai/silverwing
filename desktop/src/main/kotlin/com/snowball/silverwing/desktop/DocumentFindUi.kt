@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.foundation.ExperimentalFoundationApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.Alignment
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.text.*
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

internal const val DOCUMENT_FIND_MATCH_LIMIT = 10_000

internal fun literalMatches(text: String, query: String, limit: Int = DOCUMENT_FIND_MATCH_LIMIT): List<IntRange> {
    if (query.isEmpty() || limit <= 0) return emptyList()
    val hits = mutableListOf<IntRange>()
    var offset = 0
    while (offset <= text.length - query.length && hits.size < limit) {
        val start = text.indexOf(query, offset, ignoreCase = true)
        if (start < 0) break
        hits += start until start + query.length
        offset = start + query.length
    }
    return hits
}
internal data class FindBlock(val text: String, val order: Long, val navigate: suspend (IntRange) -> Unit)
internal data class FindHit(val block: String, val range: IntRange)
private data class FindResults(val hits: List<FindHit>, val truncated: Boolean)
internal class DocumentFindState {
    var open by mutableStateOf(false)
    var query by mutableStateOf("")
    var activeIndex by mutableIntStateOf(0)
    var navigation by mutableIntStateOf(0)
    var focusVersion by mutableIntStateOf(0)
    val blocks = mutableStateMapOf<String, FindBlock>()
    var quickOpen: (() -> Unit)? = null
    var fullTextSearch: (() -> Unit)? = null
    var beforeShow: (() -> Unit)? = null
    var afterClose: (() -> Unit)? = null
    private var serial = 0L
    fun newId() = (++serial).toString()
    private val results: FindResults by derivedStateOf {
        val found = mutableListOf<FindHit>()
        if (query.isNotEmpty()) {
            for ((id, block) in blocks.entries.sortedBy { it.value.order }) {
                // Fetch at most one extra result so the bound is visible and we never scan
                // or allocate every remaining block after reaching it.
                for (range in literalMatches(block.text, query, DOCUMENT_FIND_MATCH_LIMIT - found.size + 1)) {
                    if (found.size == DOCUMENT_FIND_MATCH_LIMIT) return@derivedStateOf FindResults(found, true)
                    found += FindHit(id, range)
                }
            }
        }
        FindResults(found, false)
    }
    val hits: List<FindHit> get() = results.hits
    val truncated: Boolean get() = results.truncated
    val currentIndex: Int get() = activeIndex.coerceIn(0, (hits.size - 1).coerceAtLeast(0))
    val current: FindHit? get() = hits.getOrNull(currentIndex)
    fun show() { if (!open) beforeShow?.invoke(); open = true; focusVersion++ }
    fun close() { val wasOpen = open; open = false; query = ""; activeIndex = 0; if (wasOpen) afterClose?.invoke() }
    fun change(value: String) { query = value; activeIndex = 0; navigation++ }
    fun move(delta: Int) { if (hits.isNotEmpty()) { activeIndex = Math.floorMod(currentIndex.toLong() + delta, hits.size.toLong()).toInt(); navigation++ } }
    fun key(event: KeyEvent, fromSearchField: Boolean = false): Boolean {
        if (event.type != KeyEventType.KeyDown) return false
        return when {
            event.isCtrlPressed && event.isShiftPressed && event.key == Key.F && fullTextSearch != null -> { fullTextSearch?.invoke(); true }
            event.isCtrlPressed && event.key == Key.P && quickOpen != null -> { quickOpen?.invoke(); true }
            event.isCtrlPressed && event.key == Key.F -> { show(); true }
            open && event.key == Key.Escape -> { close(); true }
            open && (event.key == Key.F3 || (fromSearchField && event.key == Key.Enter)) -> { move(if (event.isShiftPressed) -1 else 1); true }
            else -> false
        }
    }
}
internal val LocalDocumentFind = staticCompositionLocalOf<DocumentFindState?> { null }

@Composable internal fun DocumentFindScope(find: DocumentFindState, modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    val focus = remember { FocusRequester() }
    DisposableEffect(find, focus) {
        find.beforeShow = { focus.saveFocusedChild() }
        find.afterClose = { if (!focus.restoreFocusedChild()) focus.requestFocus() }
        onDispose { find.beforeShow = null; find.afterClose = null }
    }
    val hit = find.current
    val block = hit?.let { find.blocks[it.block] }
    LaunchedEffect(find.open, find.navigation, hit, block) {
        if (find.open && hit != null) block?.navigate?.invoke(hit.range)
    }
    CompositionLocalProvider(LocalDocumentFind provides find) {
        Box(modifier.focusRequester(focus).onPreviewKeyEvent { find.key(it) }.focusable()
            .onPointerEvent(PointerEventType.Press, pass = PointerEventPass.Initial) {
                if (!find.open && it.buttons.isPrimaryPressed) focus.requestFocus()
            }, content = content)
    }
}
@Composable internal fun DocumentFindBar(find: DocumentFindState) {
    if (!find.open) return
    val focus = remember { FocusRequester() }
    LaunchedEffect(find.focusVersion) { focus.requestFocus() }
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(4.dp), verticalAlignment = Alignment.CenterVertically) {
        OutlinedTextField(find.query, find::change, label = { Text("查找正文") }, singleLine = true,
            modifier = Modifier.weight(1f).focusRequester(focus).onPreviewKeyEvent { find.key(it, fromSearchField = true) })
        val count = find.hits.size
        Text(if (count == 0) "0 / 0" else "${find.currentIndex + 1} / $count${if (find.truncated) "+" else ""}", style = MaterialTheme.typography.labelSmall)
        ActionIconButton("上一个结果 (Shift+F3)", { find.move(-1) }, Modifier.size(28.dp), enabled = count > 0) { Icon(Icons.Outlined.KeyboardArrowUp, null) }
        ActionIconButton("下一个结果 (F3)", { find.move(1) }, Modifier.size(28.dp), enabled = count > 0) { Icon(Icons.Outlined.KeyboardArrowDown, null) }
        ActionIconButton("关闭查找 (Esc)", find::close, Modifier.size(28.dp)) { Icon(Icons.Outlined.Close, null) }
    }
}
@Composable internal fun FindDocumentButton(modifier: Modifier = Modifier) {
    val find = LocalDocumentFind.current ?: return
    ActionIconButton("查找正文 (Ctrl+F)", find::show, modifier.size(30.dp)) { Icon(Icons.Outlined.Search, null, Modifier.size(17.dp)) }
}
/** 注册当前真实显示的文字，而非 Markdown 源码；查找不会破坏链接、样式和文本选择。 */
@Composable internal fun searchableText(value: AnnotatedString, modifier: Modifier = Modifier, sourceOrder: Long? = null): Pair<AnnotatedString, Pair<Modifier, (TextLayoutResult) -> Unit>> {
    val find = LocalDocumentFind.current
    if (find == null) return value to (modifier to {})
    val id = remember(find) { find.newId() }
    val bring = remember { BringIntoViewRequester() }
    var layout by remember(value.text) { mutableStateOf<TextLayoutResult?>(null) }
    DisposableEffect(find, id, value.text, sourceOrder) {
        find.blocks[id] = FindBlock(value.text, sourceOrder ?: id.toLong()) { range ->
            fun matchingLayout() = layout?.takeIf {
                it.layoutInput.text.text == value.text && range.first in it.layoutInput.text.indices
            }
            var result = matchingLayout()
            repeat(2) {
                if (result == null) { withFrameNanos { }; result = matchingLayout() }
            }
            // The Markdown renderer can split an image paragraph into several text
            // layouts. If no full layout arrives, reveal the block instead of waiting
            // forever; ordinary text still navigates to the exact character.
            bring.bringIntoView(result?.getBoundingBox(range.first))
        }
        onDispose { find.blocks.remove(id) }
    }
    val query = if (find.open) find.query else ""
    val active = find.current
    val highlight = MaterialTheme.colorScheme.primary.copy(alpha = 0.16f)
    val activeColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.30f)
    val highlighted = remember(value, query, active, highlight, activeColor) { buildAnnotatedString {
        append(value)
        literalMatches(value.text, query).forEach { range -> addStyle(SpanStyle(background = if (active == FindHit(id, range)) activeColor else highlight), range.first, range.last + 1) }
    } }
    return highlighted to (modifier.bringIntoViewRequester(bring) to { result -> layout = result })
}
@Composable internal fun SearchableText(text: String, modifier: Modifier = Modifier, style: TextStyle = MaterialTheme.typography.bodyLarge,
    color: Color = Color.Unspecified, softWrap: Boolean = true, maxLines: Int = Int.MAX_VALUE, sourceOrder: Long? = null) = SearchableText(AnnotatedString(text), modifier, style, color, softWrap, maxLines, sourceOrder)
@Composable internal fun SearchableText(text: AnnotatedString, modifier: Modifier = Modifier, style: TextStyle = MaterialTheme.typography.bodyLarge,
    color: Color = Color.Unspecified, softWrap: Boolean = true, maxLines: Int = Int.MAX_VALUE, sourceOrder: Long? = null) {
    val (value, binding) = searchableText(text, modifier, sourceOrder)
    Text(value, binding.first, style = style, color = color, softWrap = softWrap, maxLines = maxLines,
        overflow = if (maxLines == Int.MAX_VALUE) TextOverflow.Clip else TextOverflow.Ellipsis, onTextLayout = binding.second)
}
