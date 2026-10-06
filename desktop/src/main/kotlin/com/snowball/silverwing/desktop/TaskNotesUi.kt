package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.AgentDocumentPreview
import com.snowball.silverwing.core.TaskManifest

/** 编辑器沿用 Agent 文件监控器的草稿，切换页面不会触发保存或丢失输入。 */
@Composable
internal fun TaskNotesPage(controller: DesktopApplication, task: TaskManifest, modifier: Modifier = Modifier) {
    val taskKey = controller.taskPath(task)
    val drafts = controller.agentInstructionsController
    val draft = remember(taskKey) { drafts.notesDraftFor(task) }
    val notes = draft.notes
    var notesLoadAttempt by remember(taskKey) { mutableStateOf(0) }
    val templates = controller.agentTaskTemplates
    val selectedTemplateId = selectedTemplateIdForNotes(notes, templates)
    var pendingTemplate by remember(taskKey) { mutableStateOf<Pair<AgentTaskTemplate, Long>?>(null) }
    var agentsPreview by remember(taskKey) { mutableStateOf<AgentDocumentPreview?>(null) }
    var agentsPreviewLoading by remember(taskKey) { mutableStateOf(false) }
    var agentsPreviewError by remember(taskKey) { mutableStateOf<String?>(null) }
    var agentsPreviewRequest by remember(taskKey) { mutableStateOf<TaskNotesPreviewRequest?>(null) }
    var agentsPreviewGeneration by remember(taskKey) { mutableStateOf(0L) }
    val notesReady = draft.ready
    LaunchedEffect(taskKey, task.updatedAt, controller.agentRevision, notesLoadAttempt) {
        drafts.loadTaskNotesDraftAsync(task)
    }
    LaunchedEffect(taskKey, task, agentsPreviewRequest, draft.contentRevision) {
        val request = agentsPreviewRequest ?: return@LaunchedEffect
        val generation = ++agentsPreviewGeneration
        if (request.task != task || request.contentRevision != draft.contentRevision) {
            agentsPreviewLoading = false
            agentsPreview = null
            agentsPreviewError = null
            return@LaunchedEffect
        }
        agentsPreviewLoading = true
        agentsPreviewError = null
        agentsPreview = null
        try {
            val result = controller.previewTaskAgentsAsync(task, request.notes)
            if (generation == agentsPreviewGeneration && draft.contentRevision == request.contentRevision) agentsPreview = result
        } catch (cancelled: kotlinx.coroutines.CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            if (generation == agentsPreviewGeneration) {
                agentsPreviewError = error.message ?: error::class.simpleName ?: "无法生成 Agent 文件预览"
                controller.showError(error)
            }
        } finally {
            if (generation == agentsPreviewGeneration) agentsPreviewLoading = false
        }
    }
    LaunchedEffect(controller.agentRevision, draft.contentRevision) {
        pendingTemplate = pendingTemplate?.takeIf { pending ->
            pending.second == draft.contentRevision && templates.any { it.id == pending.first.id && it.content == pending.first.content }
        }
        if (agentsPreviewRequest?.contentRevision != draft.contentRevision) agentsPreview = null
    }
    fun applyTemplate(notesResult: TemplateFillResult.Applied) {
        controller.markTaskNotesEdited(task, notesResult.notes)
    }
    Column(modifier.verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionHeader("需求说明")
        if (templates.isNotEmpty()) {
            Text(
                "从模板填充（单选）",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
                templates.forEach { template ->
                    FilterChip(
                        selected = selectedTemplateId == template.id,
                        onClick = {
                            val currentNotes = draft.notes
                            val selected = templates.firstOrNull { it.id == selectedTemplateIdForNotes(currentNotes, templates) }
                            when (val result = resolveTemplateToggle(currentNotes, selected, template)) {
                                is TemplateFillResult.Applied -> applyTemplate(result)
                                is TemplateFillResult.NeedsConfirmation -> pendingTemplate = result.target to draft.contentRevision
                            }
                        },
                        enabled = !controller.busy && notesReady,
                        label = { Text(template.name) },
                    )
                }
            }
        }
        OutlinedTextField(notes, {
            controller.markTaskNotesEdited(task, it)
        }, Modifier.fillMaxWidth(), minLines = 4, maxLines = 6, readOnly = controller.busy || !notesReady, label = { Text("需求说明") })
        if (draft.loading || !draft.ready && draft.error == null) {
            Text("正在读取需求说明…", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        draft.error?.let { error ->
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text("读取失败：$error", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                TextButton(onClick = { notesLoadAttempt++ }, enabled = !controller.busy) { Text("重试") }
            }
        }
        agentsPreviewError?.let { error ->
            Text("预览失败：$error", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            OutlinedButton(
                onClick = { agentsPreviewRequest = TaskNotesPreviewRequest(draft.notes, draft.contentRevision, (agentsPreviewRequest?.sequence ?: 0) + 1, task) },
                enabled = !controller.busy && notesReady && !agentsPreviewLoading,
            ) {
                if (agentsPreviewLoading) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                else Icon(Icons.Outlined.Visibility, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp)); Text("预览")
            }
            Spacer(Modifier.width(8.dp))
            Button(onClick = { controller.saveTaskNotes(task, draft.notes) }, enabled = !controller.busy && notesReady) {
                Icon(Icons.Outlined.Save, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("保存")
            }
        }
    }
    pendingTemplate?.let { pending ->
        val template = pending.first
        ConfirmDialog(
            title = "替换需求说明？",
            message = "当前说明已被手动修改，应用模板“${template.name}”将替换现有内容。",
            confirmLabel = "替换说明",
            enabled = !controller.busy && notesReady,
            onDismiss = { pendingTemplate = null },
            onConfirm = {
                if (pending.second == draft.contentRevision && templates.any { it.id == template.id && it.content == template.content }) {
                    applyTemplate(TemplateFillResult.Applied(template.content, template.id))
                }
                pendingTemplate = null
            },
        )
    }
    agentsPreview?.let { preview ->
        TaskAgentsPreviewDialog(
            preview = preview,
            onCopySource = { file -> controller.copyText(markdownPreviewSourceCopyPayload(file), "Markdown 源码已复制") },
            onDismiss = { agentsPreview = null },
        )
    }
}

private data class TaskNotesPreviewRequest(val notes: String, val contentRevision: Long, val sequence: Int, val task: TaskManifest)

@Composable
private fun TaskAgentsPreviewDialog(
    preview: AgentDocumentPreview,
    onCopySource: (MarkdownPreviewFile) -> Unit,
    onDismiss: () -> Unit,
) {
    val window = LocalWindowInfo.current.containerSize
    val density = LocalDensity.current
    val width = with(density) { window.width.toDp() }.minus(32.dp).coerceAtLeast(1.dp).coerceAtMost(860.dp)
    val height = with(density) { window.height.toDp() }.minus(32.dp).coerceAtLeast(1.dp).coerceAtMost(640.dp)
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(
            Modifier.width(width).height(height),
            shape = RoundedCornerShape(22.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.fillMaxSize()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("Agent 文件预览", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge)
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Surface(
                    Modifier.weight(1f).fillMaxWidth().padding(18.dp),
                    color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.55f),
                    shape = RoundedCornerShape(12.dp),
                ) {
                    MarkdownFileTabsPreview(
                        files = preview.files.map { MarkdownPreviewFile(it.relativePath, it.content) },
                        modifier = Modifier.fillMaxSize(),
                        initialPath = preview.rootFile.relativePath,
                        onCopySource = onCopySource,
                    )
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 14.dp), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onDismiss) { Text("关闭") }
                }
            }
        }
    }
}
