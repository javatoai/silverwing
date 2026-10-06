@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import java.awt.datatransfer.StringSelection
import java.nio.file.Path

/** Preview first; its explicit copy action writes only to the user's clipboard. */
@Composable
internal fun AiContextCopyDialog(
    task: TaskManifest,
    onDismiss: () -> Unit,
    requirementTitle: String? = null,
    requirementBody: String? = null,
    materialsRoot: Path? = null,
    selectedMaterialPaths: List<String> = emptyList(),
    currentBranches: Map<String, String> = emptyMap(),
    loadRequirementBody: (suspend () -> String?)? = null,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    onCopied: (String) -> Unit = {},
) {
    val scope = rememberCoroutineScope()
    // Desktop Dialog installs its own platform locals. Preserve the app's clipboard,
    // including an injected in-memory clipboard, across that new composition root.
    val inheritedClipboard = LocalClipboard.current
    val service = remember { AiContextCopyService() }
    val currentBodyLoader by rememberUpdatedState(loadRequirementBody)
    val currentDispatcher by rememberUpdatedState(ioDispatcher)
    val reader = remember { AiContextCopyController(scope) { service.load(it, currentBodyLoader, currentDispatcher) } }
    val request = remember(task, requirementTitle, requirementBody, materialsRoot, selectedMaterialPaths, currentBranches) {
        AiContextCopyRequest(task, requirementTitle, requirementBody, materialsRoot, selectedMaterialPaths.toList(), currentBranches.toMap())
    }
    DisposableEffect(request) { reader.load(request); onDispose { reader.clear() } }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.widthIn(max = 900.dp).fillMaxWidth(0.94f).fillMaxHeight(0.92f),
            shape = MaterialTheme.shapes.extraLarge, tonalElevation = 6.dp) {
            Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("复制 AI 上下文", style = MaterialTheme.typography.headlineSmall)
                Text("先检查要复制的内容，可勾选内容或直接删改预览。", style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                when (val state = reader.state) {
                    AiContextCopyLoadState.Idle, is AiContextCopyLoadState.Loading -> {
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                        Text("正在读取需求正文和所选资料…", style = MaterialTheme.typography.bodyMedium)
                        Spacer(Modifier.weight(1f))
                        TextButton(onClick = onDismiss, modifier = Modifier.align(Alignment.End)) { Text("取消") }
                    }
                    is AiContextCopyLoadState.Failed -> {
                        Text(state.message, color = MaterialTheme.colorScheme.error)
                        Spacer(Modifier.weight(1f))
                        Row(Modifier.align(Alignment.End), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            TextButton(onClick = onDismiss) { Text("取消") }
                            Button(onClick = { reader.load(request) }) { Text("重试读取") }
                        }
                    }
                    is AiContextCopyLoadState.Ready -> if (state.request == request) {
                        CompositionLocalProvider(LocalClipboard provides inheritedClipboard) {
                            AiContextCopyEditor(state.document, onDismiss, onCopied, Modifier.weight(1f))
                        }
                    }
                }
            }
        }
    }
}

@Composable
internal fun AiContextCopyEditor(
    document: AiContextCopyDocument,
    onDismiss: () -> Unit,
    onCopied: (String) -> Unit = {},
    modifier: Modifier = Modifier,
) {
    var selection by remember(document) { mutableStateOf(document.defaultSelection) }
    val generated = remember(document, selection) { buildAiContextCopyPreview(document, selection) }
    var preview by remember(document) { mutableStateOf(generated.text) }
    var editing by remember(document) { mutableStateOf(false) }
    var copyBusy by remember(document) { mutableStateOf(false) }
    var copyError by remember(document) { mutableStateOf<String?>(null) }
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        if (!editing) Column(Modifier.fillMaxWidth().heightIn(max = 128.dp).verticalScroll(rememberScrollState())) {
            document.sections.forEach { section ->
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(section.id in selection, { checked ->
                        selection = if (checked) selection + section.id else selection - section.id
                        preview = buildAiContextCopyPreview(document, selection).text
                        copyError = null
                    }, enabled = !copyBusy, modifier = Modifier.semantics { contentDescription = "包含${section.title}" })
                    Text(section.title, style = MaterialTheme.typography.bodyMedium)
                }
            }
        } else Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("预览已修改，复制将使用下方内容。", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            TextButton(onClick = { preview = generated.text; editing = false; copyError = null }, enabled = !copyBusy) { Text("恢复勾选内容") }
        }
        if (document.notices.isNotEmpty()) Column(Modifier.fillMaxWidth().heightIn(max = 76.dp).verticalScroll(rememberScrollState())) {
            document.notices.forEach { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        OutlinedTextField(value = preview, onValueChange = {
            preview = limitAiContextText(it, AI_CONTEXT_COPY_CHARACTERS)
            editing = preview != generated.text
            copyError = null
        }, label = { Text("即将复制的内容") }, modifier = Modifier.fillMaxWidth().weight(1f)
            .semantics { contentDescription = "AI 上下文预览" }, enabled = !copyBusy,
            textStyle = MaterialTheme.typography.bodyMedium, maxLines = Int.MAX_VALUE)
        if (generated.truncated && !editing) Text("内容超过 $AI_CONTEXT_COPY_CHARACTERS 字符，预览末尾已标记截断；可取消部分资料后重新生成。",
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        copyError?.let { Text("复制失败：$it。请重试。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("${preview.length} 字符", Modifier.weight(1f), style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Medium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            TextButton(onClick = onDismiss, enabled = !copyBusy) { Text("取消") }
            Button(onClick = {
                val captured = preview
                copyBusy = true
                scope.launch {
                    try {
                        clipboard.setClipEntry(ClipEntry(StringSelection(captured)))
                        onCopied(captured)
                        onDismiss()
                    } catch (cancelled: CancellationException) { throw cancelled }
                    catch (error: Exception) { copyError = error.message.orEmpty().ifBlank { "剪贴板暂不可用" } }
                    finally { copyBusy = false }
                }
            }, enabled = preview.isNotBlank() && !copyBusy) { Text(if (copyBusy) "正在复制…" else "复制到剪贴板") }
        }
    }
}
