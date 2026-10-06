package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import java.nio.file.Path
import java.util.Locale

/** 所有文件共用操作区；是否能复制原文独立于文件路径和系统打开能力。 */
@Composable
internal fun RequirementMaterialsFileHeader(
    controller: DesktopApplication,
    root: Path,
    file: RequirementMaterialsMarkdownFile,
    content: String?,
    mode: MarkdownPreviewMode,
    onModeChange: (MarkdownPreviewMode) -> Unit,
    refreshKey: Any,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    outlineState: MarkdownOutlineState? = null,
    showIdentity: Boolean = true,
    editing: Boolean = false,
    onEdit: (() -> Unit)? = null,
    onPreview: (() -> Unit)? = null,
    dirty: Boolean = false,
    saveBusy: Boolean = false,
    onSave: (() -> Unit)? = null,
) {
    val service = remember { RequirementMaterialsMarkdownService() }
    val scope = rememberCoroutineScope()
    var sourcePath by remember(root, file.relativePath, refreshKey) { mutableStateOf<Path?>(null) }
    var application by remember(root, file.relativePath, refreshKey) { mutableStateOf(DefaultFileApplication()) }
    // 切换文件不能绕过正在打开的状态；一次只提交一个系统打开动作。
    var localBusy by remember(root) { mutableStateOf(false) }
    val busy = localBusy || controller.desktopActions.fileOpenBusy
    var copyMenu by remember(root, file.relativePath) { mutableStateOf(false) }
    var openMenu by remember(root, file.relativePath) { mutableStateOf(false) }
    LaunchedEffect(root, file.relativePath, refreshKey) {
        sourcePath = runInterruptible(ioDispatcher) { runCatching { service.resolveOpenFile(root, file.relativePath) }.getOrNull() }
        sourcePath?.let { path ->
            application = try { controller.desktopActions.defaultFileApplication(path) }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { DefaultFileApplication() }
        }
    }
    fun perform(action: suspend (Path) -> Unit) {
        if (localBusy || controller.desktopActions.fileOpenBusy || sourcePath == null) return
        localBusy = true
        scope.launch {
            try {
                // 点击时重新检查文件，防止目录刷新之前文件被删除或替换。
                val path = runInterruptible(ioDispatcher) { service.resolveOpenFile(root, file.relativePath) }
                action(path)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                controller.showError(error)
            } finally { localBusy = false }
        }
    }
    val actions: @Composable () -> Unit = {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            MaterialsHeaderActions()
            if (file.textPreview || file.extension == "pdf") FindDocumentButton()
            outlineState?.let { outline -> MarkdownOutlineButton(outline,
                onNavigate = { onModeChange(MarkdownPreviewMode.RENDERED) }, modifier = Modifier.size(30.dp)) }
            if (file.markdown && onEdit != null && onPreview != null) {
                ActionIconButton(
                    label = if (editing) "查看任务资料预览" else "编辑 Markdown",
                    onClick = if (editing) onPreview else onEdit,
                    modifier = Modifier.size(30.dp),
                    loading = saveBusy,
                ) {
                    Icon(if (editing) Icons.Outlined.Visibility else Icons.Outlined.Edit, null, Modifier.size(16.dp))
                }
            }
            if (file.textPreview) MarkdownPreviewModeToggle(mode, onModeChange, Modifier.size(30.dp),
                if (file.markdown) "Markdown" else file.extension.uppercase(Locale.ROOT))
            Box {
                ActionIconButton("复制…", { copyMenu = true }, Modifier.size(30.dp), enabled = !busy) {
                    Icon(Icons.Outlined.ContentCopy, "复制…", Modifier.size(16.dp))
                }
                SilverWingDropdownMenu(copyMenu, { copyMenu = false }) {
                    if (file.textPreview) DropdownMenuItem(
                        text = { Text("复制原文") },
                        enabled = content != null,
                        onClick = { copyMenu = false; content?.let { controller.copyText(it, "文件原文已复制") } },
                    )
                    DropdownMenuItem(text = { Text("复制文件路径") }, enabled = sourcePath != null && !busy,
                        onClick = { copyMenu = false; perform { controller.copyText(it.toString(), "文档路径已复制") } })
                    DropdownMenuItem(text = { Text("复制文件") }, enabled = sourcePath != null && !busy,
                        onClick = { copyMenu = false; perform { controller.copyFile(it) } })
                }
            }
            if (editing && onSave != null) {
                ActionIconButton("保存 Markdown", onSave, Modifier.size(30.dp), enabled = dirty && !saveBusy, loading = saveBusy) {
                    Icon(Icons.Outlined.Save, null, Modifier.size(16.dp))
                }
            }
            Box {
                Surface(shape = MaterialTheme.shapes.small, border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
                    Row(Modifier.height(36.dp), verticalAlignment = Alignment.CenterVertically) {
                        TextButton(
                            onClick = { perform { controller.desktopActions.openFile(it, application.associationMissing && controller.desktopActions.fileApplicationChooserAvailable) } },
                            enabled = sourcePath != null && !busy,
                            modifier = Modifier.height(36.dp).widthIn(max = 190.dp),
                            contentPadding = PaddingValues(horizontal = 10.dp, vertical = 0.dp),
                        ) {
                            Text(if (busy) "正在打开…" else when {
                                application.associationMissing && controller.desktopActions.fileApplicationChooserAvailable -> "选择应用打开"
                                !application.displayName.isNullOrBlank() -> "用 ${application.displayName} 打开"
                                else -> "默认应用打开"
                            }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                        VerticalDivider(Modifier.height(18.dp))
                        IconButton(onClick = { openMenu = true }, enabled = sourcePath != null && !busy,
                            modifier = Modifier.size(width = 32.dp, height = 36.dp)) {
                            Icon(Icons.Outlined.ExpandMore, "打开方式", Modifier.size(18.dp))
                        }
                    }
                }
                SilverWingDropdownMenu(openMenu, { openMenu = false }) {
                    if (controller.desktopActions.fileApplicationChooserAvailable) DropdownMenuItem(
                        text = { Text("用其他应用打开…") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.OpenInNew, null) },
                        enabled = !busy,
                        onClick = { openMenu = false; perform { controller.desktopActions.openFile(it, true) } },
                    )
                    DropdownMenuItem(text = { Text("在文件夹中显示") }, leadingIcon = { Icon(Icons.Outlined.FolderOpen, null) },
                        enabled = !busy,
                        onClick = { openMenu = false; perform { controller.desktopActions.revealFile(it) } })
                }
            }
        }
    }
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val identity: @Composable (Modifier) -> Unit = { modifier ->
            Column(modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
                TooltipText(file.fileName, Modifier.fillMaxWidth(), style = MaterialTheme.typography.bodyMedium.copy(fontWeight = FontWeight.SemiBold))
                if (file.relativePath != file.fileName) Text(file.relativePath.substringBeforeLast('/', ""), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        if (!showIdentity) Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            MaterialsHeaderLeading()
            Box(Modifier.weight(1f), contentAlignment = Alignment.CenterEnd) { actions() }
        } else if (maxWidth < 560.dp) Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) { MaterialsHeaderLeading(); identity(Modifier.weight(1f)) }
            Box(Modifier.align(Alignment.End)) { actions() }
        } else Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            MaterialsHeaderLeading()
            identity(Modifier.weight(1f).padding(end = 12.dp))
            actions()
        }
    }
}

@Composable
internal fun RequirementMaterialsExternalFile(file: RequirementMaterialsMarkdownFile, modifier: Modifier = Modifier) {
    Box(modifier.padding(24.dp), contentAlignment = Alignment.Center) {
        Column(Modifier.widthIn(max = 440.dp), horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Icon(Icons.Outlined.Description, null, Modifier.size(48.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(file.fileName, style = MaterialTheme.typography.titleMedium)
            Text("${file.extension.uppercase(Locale.ROOT).ifBlank { "无扩展名" }} · ${materialFileSize(file.sizeBytes)}",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text("请使用外部应用查看", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

internal fun materialFileSize(bytes: Long): String = when {
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> String.format(Locale.ROOT, "%.1f KB", bytes / 1024.0)
    else -> String.format(Locale.ROOT, "%.1f MB", bytes / (1024.0 * 1024))
}
