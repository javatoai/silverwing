package com.snowball.silverwing.desktop

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.ManageSearch
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.FindInPage
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

internal data class MaterialsHeaderControls(val reopen: (() -> Unit)?, val picker: () -> Unit,
    val refresh: () -> Unit, val loading: Boolean,
    val search: (() -> Unit)? = null, val copyContext: (() -> Unit)? = null,
    val createFolder: (() -> Unit)? = null, val createMarkdown: (() -> Unit)? = null,
    val pasteImage: (() -> Unit)? = null, val importFiles: (() -> Unit)? = null, val managingFiles: Boolean = false)
internal val LocalMaterialsHeaderControls = staticCompositionLocalOf<MaterialsHeaderControls?> { null }
@Composable internal fun MaterialsHeaderLeading() {
    LocalMaterialsHeaderControls.current?.reopen?.let { action ->
        ActionIconButton("展开文档列表", action, Modifier.size(30.dp)) { Icon(Icons.Outlined.FolderOpen, null, Modifier.size(18.dp)) }
    }
}
@Composable internal fun MaterialsHeaderActions() {
    val controls = LocalMaterialsHeaderControls.current ?: return
    ActionIconButton("快速打开文件 (Ctrl+P)", controls.picker, Modifier.size(30.dp)) { Icon(Icons.Outlined.ManageSearch, null, Modifier.size(18.dp)) }
    controls.search?.let { action -> ActionIconButton("搜索全部资料 (Ctrl+Shift+F)", action, Modifier.size(30.dp)) {
        Icon(Icons.Outlined.FindInPage, null, Modifier.size(18.dp)) } }
    ActionIconButton("刷新任务资料目录", controls.refresh, Modifier.size(30.dp), loading = controls.loading) { Icon(Icons.Outlined.Refresh, null, Modifier.size(17.dp)) }
    MaterialsMoreActions(controls)
}
@Composable internal fun MaterialsMoreActions(controls: MaterialsHeaderControls? = LocalMaterialsHeaderControls.current) {
    if (controls == null) return
    var expanded by remember { mutableStateOf(false) }
    Box {
        ActionIconButton("更多资料操作", { expanded = true }, Modifier.size(30.dp)) { Icon(Icons.Outlined.MoreVert, null, Modifier.size(18.dp)) }
        SilverWingDropdownMenu(expanded, { expanded = false }) {
            controls.copyContext?.let { action -> DropdownMenuItem(text = { Text("复制 AI 上下文…") }, onClick = { expanded = false; action() }) }
            controls.createMarkdown?.let { action -> DropdownMenuItem(text = { Text("新建 Markdown…") }, enabled = !controls.managingFiles,
                onClick = { expanded = false; action() }) }
            controls.createFolder?.let { action -> DropdownMenuItem(text = { Text("新建文件夹…") }, enabled = !controls.managingFiles,
                onClick = { expanded = false; action() }) }
            controls.importFiles?.let { action -> DropdownMenuItem(text = { Text("导入文件…") }, enabled = !controls.managingFiles,
                onClick = { expanded = false; action() }) }
            controls.pasteImage?.let { action -> DropdownMenuItem(text = { Text("粘贴截图 (Ctrl+V)") }, enabled = !controls.managingFiles,
                onClick = { expanded = false; action() }) }
        }
    }
}
internal fun filterMaterialFiles(files: List<RequirementMaterialsMarkdownFile>, query: String) = files.filter {
    it.relativePath.contains(query.trim(), ignoreCase = true)
}.take(200)
@Composable internal fun MaterialsQuickPicker(files: List<RequirementMaterialsMarkdownFile>, onDismiss: () -> Unit,
    onSelect: (RequirementMaterialsMarkdownFile) -> Unit) {
    var query by remember { mutableStateOf("") }
    var selected by remember { mutableIntStateOf(0) }
    val matches = remember(files, query) { filterMaterialFiles(files, query) }
    val focus = remember { FocusRequester() }
    val list = androidx.compose.foundation.lazy.rememberLazyListState()
    LaunchedEffect(Unit) { withFrameNanos { }; focus.requestFocus() }
    LaunchedEffect(selected, matches) { if (matches.isNotEmpty()) list.scrollToItem(selected.coerceAtMost(matches.lastIndex)) }
    AlertDialog(onDismissRequest = onDismiss, title = { Text("快速打开文件") },
        text = {
            Column(Modifier.widthIn(max = 520.dp).heightIn(max = 440.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(query, { query = it; selected = 0 }, singleLine = true, label = { Text("输入文件名或路径") },
                    modifier = Modifier.fillMaxWidth().focusRequester(focus).onPreviewKeyEvent { event ->
                        if (event.type != KeyEventType.KeyDown) false else when(event.key) {
                            Key.DirectionDown -> { selected = (selected + 1).coerceAtMost(matches.lastIndex.coerceAtLeast(0)); true }
                            Key.DirectionUp -> { selected = (selected - 1).coerceAtLeast(0); true }
                            Key.Enter -> { matches.getOrNull(selected)?.let(onSelect); true }
                            Key.Escape -> { onDismiss(); true }
                            else -> false
                        }
                    })
                if (matches.isEmpty()) Text("没有匹配的文件", color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyColumn(Modifier.weight(1f, fill = false), state = list) {
                    itemsIndexed(matches, key = { _, file -> file.relativePath }) { index, file ->
                        Surface(color = if (index == selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface,
                            shape = MaterialTheme.shapes.small) {
                            Column(Modifier.fillMaxWidth().clickable { onSelect(file) }.padding(10.dp)) {
                                Text(file.fileName, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                if (file.relativePath != file.fileName) Text(file.relativePath, style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
                Text("↑ ↓ 选择 · Enter 打开 · Esc 关闭", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }, confirmButton = {}, dismissButton = { TextButton(onClick = onDismiss) { Text("关闭") } })
}
