package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.AgentDocumentPreview
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.RepositoryConfig
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import java.nio.file.Path

/** Drafts belong to the existing monitor; the session only retains reading controls and positions. */
@Composable
internal fun TaskNotesPage(controller: DesktopApplication, task: TaskManifest, modifier: Modifier = Modifier) {
    val taskKey = controller.taskPath(task)
    val taskDirectory = remember(taskKey) { Path.of(taskKey).toAbsolutePath().normalize() }
    val browsing = remember(taskKey) { controller.taskBrowsingSession.notesFor(taskKey) }
    val drafts = controller.agentInstructionsController
    val draft = remember(taskKey) { drafts.notesDraftFor(task) }
    val templates = controller.agentTaskTemplates
    val selectedTemplateId = selectedTemplateIdForNotes(draft.notes, templates)
    var refreshAttempt by remember(taskKey) { mutableIntStateOf(0) }
    var pendingTemplate by remember(taskKey) { mutableStateOf<Pair<AgentTaskTemplate, Long>?>(null) }
    var saving by remember(taskKey) { mutableStateOf(false) }
    val rulesSelected = browsing.files.selectedPath == TASK_NOTES_PREVIEW_PATH
    val editingRules = rulesSelected && browsing.mode == TaskNotesPageMode.EDIT

    LaunchedEffect(taskKey, task.updatedAt, controller.agentRevision, refreshAttempt) {
        drafts.loadTaskNotesDraftAsync(task)
    }
    LaunchedEffect(controller.busy) { if (!controller.busy) saving = false }
    LaunchedEffect(controller.agentRevision, draft.contentRevision, editingRules) {
        pendingTemplate = pendingTemplate?.takeIf { pending ->
            editingRules && pending.second == draft.contentRevision &&
                templates.any { it.id == pending.first.id && it.content == pending.first.content }
        }
    }

    // Keep the generated document set mounted while editing, without regenerating it for each keystroke.
    var editorPreviewDraft by remember(taskKey, refreshAttempt) { mutableStateOf<TaskNotesPreviewDraft?>(null) }
    val currentDraft = TaskNotesPreviewDraft(draft.notes, draft.contentRevision)
    val previewDraft = if (browsing.mode == TaskNotesPageMode.EDIT) editorPreviewDraft ?: currentDraft else currentDraft
    SideEffect {
        if (draft.ready && !draft.loading && (browsing.mode == TaskNotesPageMode.READ || editorPreviewDraft == null))
            editorPreviewDraft = currentDraft
    }
    val request = if (draft.ready && !draft.loading && draft.error == null)
        TaskNotesPreviewRequest(task, previewDraft.notes, previewDraft.contentRevision, controller.config.repositories,
            controller.agentRevision, refreshAttempt) else null
    val currentRequest by rememberUpdatedState(request)
    // Reset synchronously when the task or inputs change; an old document cannot flash under new tabs.
    var previewState by remember(taskKey, request) {
        mutableStateOf<TaskNotesPreviewState>(if (request == null) TaskNotesPreviewState.Idle else TaskNotesPreviewState.Loading)
    }
    LaunchedEffect(taskKey, request) {
        val captured = request ?: return@LaunchedEffect
        try {
            val result = controller.previewTaskAgentsAsync(captured.task, captured.notes)
            currentCoroutineContext().ensureActive()
            if (currentRequest == captured)
                previewState = TaskNotesPreviewState.Loaded(result)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            if (currentRequest == captured)
                previewState = TaskNotesPreviewState.Failed(failure.message ?: "无法生成文件预览")
        }
    }

    val controls: @Composable () -> Unit = {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp),
            itemVerticalAlignment = Alignment.CenterVertically) {
            if (rulesSelected) {
                FilterChip(selected = browsing.mode == TaskNotesPageMode.READ,
                    onClick = { if (browsing.mode != TaskNotesPageMode.READ) browsing.readDraft() },
                    enabled = !controller.busy, label = { Text("阅读") })
                FilterChip(selected = browsing.mode == TaskNotesPageMode.EDIT,
                    onClick = { browsing.mode = TaskNotesPageMode.EDIT },
                    enabled = !controller.busy && draft.ready, label = { Text("编辑") })
            }
            ActionIconButton("刷新需求说明", { refreshAttempt++ }, Modifier.size(32.dp),
                enabled = !controller.busy, loading = draft.loading || previewState is TaskNotesPreviewState.Loading) {
                Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp))
            }
            if (rulesSelected) Button(onClick = { saving = controller.saveTaskNotes(task, draft.notes) },
                enabled = !controller.busy && draft.ready) {
                if (saving && controller.busy) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                else Icon(Icons.Outlined.Save, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp)); Text(if (saving && controller.busy) "正在保存…" else "保存")
            }
        }
    }
    val editor: @Composable (MarkdownPreviewFile, Modifier) -> Unit = { _, editorModifier ->
        CompositionLocalProvider(LocalMaterialsReadingState provides browsing.editorPosition) {
            Column(editorModifier.verticalScroll(rememberMaterialsScrollState("task-notes-editor")),
                verticalArrangement = Arrangement.spacedBy(12.dp)) {
                if (templates.isNotEmpty()) {
                    Text("从模板填充（单选）", style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
                        templates.forEach { template ->
                            FilterChip(selected = selectedTemplateId == template.id,
                                onClick = {
                                    val currentNotes = draft.notes
                                    val selected = templates.firstOrNull { it.id == selectedTemplateIdForNotes(currentNotes, templates) }
                                    when (val result = resolveTemplateToggle(currentNotes, selected, template)) {
                                        is TemplateFillResult.Applied -> controller.markTaskNotesEdited(task, result.notes)
                                        is TemplateFillResult.NeedsConfirmation -> pendingTemplate = result.target to draft.contentRevision
                                    }
                                }, enabled = !controller.busy && draft.ready, label = { Text(template.name) })
                        }
                    }
                }
                OutlinedTextField(draft.notes, { controller.markTaskNotesEdited(task, it) },
                    Modifier.fillMaxWidth(), minLines = 8, maxLines = Int.MAX_VALUE,
                    readOnly = controller.busy || !draft.ready, label = { Text("需求说明") })
            }
        }
    }
    Column(modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        draft.error?.let { error ->
            TaskNotesFailureRow("读取失败：$error", !controller.busy) { refreshAttempt++ }
        }
        when (val preview = previewState) {
            is TaskNotesPreviewState.Loaded -> MarkdownFileTabsPreview(
                files = preview.document.files.map {
                    MarkdownPreviewFile(it.relativePath, if (it.relativePath == TASK_NOTES_PREVIEW_PATH) draft.notes else it.content)
                },
                modifier = Modifier.weight(1f).fillMaxWidth(),
                initialPath = TASK_NOTES_PREVIEW_PATH,
                state = browsing.files,
                emptyContentMessage = "尚未填写需求说明，切换到“编辑”可以开始填写。",
                onCopySource = { controller.copyText(markdownPreviewSourceCopyPayload(it), "Markdown 源码已复制") },
                sourcePathForFile = { file ->
                    taskDirectory.resolve(file.path).normalize().takeIf { it.startsWith(taskDirectory) }
                },
                onCopyPath = { controller.copyText(it.toString(), "文件路径已复制") },
                onCopyFile = controller::copyFile,
                fileCopyLabel = if (rulesSelected && draft.dirty) "复制已保存文件" else "复制文件",
                fileContent = if (editingRules) editor else null,
            )
            is TaskNotesPreviewState.Failed -> Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.TopStart) {
                TaskNotesFailureRow("预览失败：${preview.message}", !controller.busy) { refreshAttempt++ }
            }
            TaskNotesPreviewState.Idle, TaskNotesPreviewState.Loading -> Box(Modifier.weight(1f).fillMaxWidth(),
                contentAlignment = Alignment.Center) {
                if (draft.error == null) Column(horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
                    Text("正在读取需求说明…", color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodySmall)
                }
            }
        }
        if (rulesSelected) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            BoxWithConstraints(Modifier.fillMaxWidth()) {
                val status: @Composable () -> Unit = {
                    Text(if (saving && controller.busy) "正在保存…" else if (draft.dirty) "未保存" else "已保存",
                        style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (maxWidth < 460.dp) Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    status()
                    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) { controls() }
                } else Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.weight(1f)) { status() }
                    controls()
                }
            }
        }
    }

    pendingTemplate?.let { pending ->
        val template = pending.first
        ConfirmDialog(title = "替换需求说明？",
            message = "当前说明已被手动修改，应用模板“${template.name}”将替换现有内容。",
            confirmLabel = "替换说明", enabled = !controller.busy && draft.ready,
            onDismiss = { pendingTemplate = null },
            onConfirm = {
                if (pending.second == draft.contentRevision && templates.any { it.id == template.id && it.content == template.content })
                    controller.markTaskNotesEdited(task, template.content)
                pendingTemplate = null
            })
    }
}

private data class TaskNotesPreviewDraft(val notes: String, val contentRevision: Long)

@Composable
private fun TaskNotesFailureRow(message: String, retryEnabled: Boolean, onRetry: () -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(message, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        TextButton(onClick = onRetry, enabled = retryEnabled) { Text("重试") }
    }
}

private data class TaskNotesPreviewRequest(
    val task: TaskManifest,
    val notes: String,
    val contentRevision: Long,
    val repositories: List<RepositoryConfig>,
    val agentRevision: Long,
    val refreshAttempt: Int,
)

private sealed interface TaskNotesPreviewState {
    data object Idle : TaskNotesPreviewState
    data object Loading : TaskNotesPreviewState
    data class Loaded(val document: AgentDocumentPreview) : TaskNotesPreviewState
    data class Failed(val message: String) : TaskNotesPreviewState
}
