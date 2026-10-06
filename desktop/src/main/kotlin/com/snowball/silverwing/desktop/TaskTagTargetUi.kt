package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.RemoteBranchRef
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TagBuildMode
import com.snowball.silverwing.core.TaskManifest

internal fun workspaceTagActionLabel(workspace: ServiceWorkspace): String =
    if (workspace.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
        "构建测试Tag · 目标 ${workspace.tagTargetRef ?: "未设置"}"
    } else "构建测试Tag · 当前分支 ${workspace.branch}"

/** Keeps the original target until save completes so stale dialogs cannot overwrite a newer choice. */
@Composable
internal fun TaskTagTargetDialog(
    controller: DesktopApplication,
    task: TaskManifest,
    workspace: ServiceWorkspace,
    onDismiss: () -> Unit,
) {
    var draft by remember { mutableStateOf(workspace.tagTargetRef.orEmpty()) }
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val focus = remember { FocusRequester() }
    val valid = runCatching { RemoteBranchRef.parse(draft) }.isSuccess
    val save: () -> Unit = {
        if (!saving && !controller.busy && valid) {
            saving = true
            error = null
            val started = controller.taskController.updateTagTarget(
                task, workspace, draft,
                onFailure = { saving = false; error = it.message ?: "保存失败，请重试" },
                onCancelled = { saving = false },
                onCompleted = { saving = false; onDismiss() },
            )
            if (!started) saving = false
        }
    }
    val dismiss: () -> Unit = {
        if (!saving) {
            controller.cancelRemoteBranchLoads()
            onDismiss()
        }
    }
    AlertDialog(
        onDismissRequest = dismiss,
        modifier = Modifier.widthIn(max = 620.dp).fillMaxWidth().onPreviewKeyEvent {
            if (it.type == KeyEventType.KeyDown && it.key == Key.Enter && it.isCtrlPressed) {
                save(); true
            } else false
        },
        properties = DialogProperties(
            usePlatformDefaultWidth = false,
            dismissOnBackPress = !saving,
            dismissOnClickOutside = !saving,
        ),
        title = { Text("设置测试目标分支") },
        text = {
            Column(Modifier.fillMaxWidth().heightIn(max = 380.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("${workspace.serviceName} / ${workspace.moduleName}", style = MaterialTheme.typography.titleSmall)
                SelectionContainer { Text("当前目标：${workspace.tagTargetRef ?: "未设置"}") }
                RemoteBranchPicker(
                    value = draft,
                    onValueChange = { if (!saving) { draft = it; error = null } },
                    label = "测试目标分支",
                    repositoryId = workspace.repositoryId,
                    controller = controller,
                    enabled = !saving,
                    isError = error != null || draft.isNotBlank() && !valid,
                    supportingText = if (!valid) "请输入完整的 remote/branch，例如 origin/release/test" else null,
                    fieldModifier = Modifier.focusRequester(focus),
                )
                LaunchedEffect(Unit) { focus.requestFocus() }
                Text("仅影响当前任务此模块之后创建的测试Tag。历史操作重试沿用原目标。",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = {
            Button(onClick = save, enabled = valid && !saving && !controller.busy) {
                Text(if (saving) "正在验证并保存…" else "保存")
            }
        },
        dismissButton = { TextButton(onClick = dismiss, enabled = !saving) { Text("取消") } },
    )
}
