package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.selectionKey

@Composable
internal fun BatchTagDialog(
    workspaces: List<ServiceWorkspace>,
    onDismiss: () -> Unit,
    onBuild: (List<ServiceWorkspace>) -> Unit,
) {
    var selected by remember(workspaces) { mutableStateOf(emptySet<String>()) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("批量测试Tag") },
        text = {
            Column(Modifier.widthIn(min = 520.dp).heightIn(max = 500.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = { selected = workspaces.map(ServiceWorkspace::selectionKey).toSet() }) { Text("全选") }
                    TextButton(onClick = { selected = emptySet() }) { Text("全不选") }
                }
                workspaces.forEach { workspace ->
                    val checked = workspace.selectionKey in selected
                    Row(
                        Modifier.fillMaxWidth().clickable {
                            selected = if (checked) selected - workspace.selectionKey else selected + workspace.selectionKey
                        }.padding(vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Checkbox(checked, { selected = if (checked) selected - workspace.selectionKey else selected + workspace.selectionKey })
                        Column {
                            Text(workspace.moduleName.ifBlank { workspace.serviceName }, fontWeight = FontWeight.SemiBold)
                            Text("${workspace.serviceName} · ${workspace.branch}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
        },
        confirmButton = { Button(onClick = { onBuild(workspaces.filter { it.selectionKey in selected }) }, enabled = selected.isNotEmpty()) { Text("开始构建") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
internal fun BranchInfoDialog(
    content: String,
    hasRequirementLink: Boolean,
    onDismiss: () -> Unit,
    onCopyServicesWithoutRequirementLink: () -> Unit,
    onCopyServicesWithRequirementLink: () -> Unit,
    onCopyBranchInfoWithoutRequirementLink: () -> Unit,
    onCopyBranchInfoWithRequirementLink: () -> Unit,
) {
    AlertDialog(
        modifier = Modifier.widthIn(min = 780.dp, max = 1_040.dp),
        properties = DialogProperties(usePlatformDefaultWidth = false),
        onDismissRequest = onDismiss,
        title = { Text("分支信息") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
                Surface(Modifier.fillMaxWidth(), color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(11.dp)) {
                    SelectionContainer {
                        Text(
                            content.ifBlank { "暂无分支信息" },
                            Modifier.fillMaxWidth().heightIn(max = 420.dp).padding(13.dp).verticalScroll(rememberScrollState()),
                            fontFamily = FontFamily.Monospace,
                        )
                    }
                }
                Surface(
                    Modifier.fillMaxWidth(),
                    color = MaterialTheme.colorScheme.surface,
                    shape = RoundedCornerShape(11.dp),
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
                ) {
                    Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        Text("复制内容", style = MaterialTheme.typography.titleSmall)
                        BranchInfoCopyOptionRow(
                            label = "服务",
                            description = "仅服务名称",
                            hasRequirementLink = hasRequirementLink,
                            onCopyWithoutRequirementLink = onCopyServicesWithoutRequirementLink,
                            onCopyWithRequirementLink = onCopyServicesWithRequirementLink,
                        )
                        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                        BranchInfoCopyOptionRow(
                            label = "分支信息",
                            description = "服务名和分支",
                            hasRequirementLink = hasRequirementLink,
                            onCopyWithoutRequirementLink = onCopyBranchInfoWithoutRequirementLink,
                            onCopyWithRequirementLink = onCopyBranchInfoWithRequirementLink,
                        )
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
    )
}

@Composable
private fun BranchInfoCopyOptionRow(
    label: String,
    description: String,
    hasRequirementLink: Boolean,
    onCopyWithoutRequirementLink: () -> Unit,
    onCopyWithRequirementLink: () -> Unit,
) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.width(118.dp)) {
            Text(label, fontWeight = FontWeight.SemiBold)
            Text(description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Spacer(Modifier.width(10.dp))
        OutlinedButton(onClick = onCopyWithoutRequirementLink) {
            Icon(Icons.Outlined.ContentCopy, null, Modifier.size(16.dp))
            Spacer(Modifier.width(4.dp))
            Text("不含链接")
        }
        Spacer(Modifier.width(8.dp))
        OutlinedButton(onClick = onCopyWithRequirementLink, enabled = hasRequirementLink) {
            Icon(Icons.Outlined.ContentCopy, null, Modifier.size(16.dp))
            Spacer(Modifier.width(4.dp))
            Text("含链接")
        }
    }
}

@Composable
internal fun AddRepositoryDialog(controller: DesktopApplication, onDismiss: () -> Unit, onAdd: (List<String>) -> Unit) {
    var paths by remember { mutableStateOf<List<String>>(emptyList()) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("批量添加 Git 仓库") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("逐个校验所选目录，不递归扫描子目录；非 Git、Linked Worktree 和重复仓库会被跳过。")
                OutlinedButton(
                    onClick = { controller.chooseDirectories(paths.firstOrNull()) { paths = it } },
                    enabled = !controller.pathPickerBusy,
                ) {
                    Icon(Icons.Outlined.Folder, null)
                    Spacer(Modifier.width(6.dp))
                    Text("选择仓库目录（可多选）")
                }
                if (paths.isEmpty()) {
                    Text("尚未选择目录", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                } else {
                    Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(10.dp)) {
                        Column(Modifier.fillMaxWidth().heightIn(max = 220.dp).padding(10.dp).verticalScroll(rememberScrollState())) {
                            paths.forEach { Text(it, style = MaterialTheme.typography.bodySmall) }
                        }
                    }
                }
                Text("新增服务默认采用 Worktree；添加后可在服务配置中改为独立克隆。", style = MaterialTheme.typography.bodySmall)
            }
        },
        confirmButton = { Button(onClick = { onAdd(paths) }, enabled = paths.isNotEmpty() && !controller.busy) { Text("校验并添加") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
internal fun TagResultDialog(
    title: String,
    content: String,
    onDismiss: () -> Unit,
    onCopy: () -> Unit,
) {
    Dialog(onDismissRequest = onDismiss) {
        Surface(
            Modifier.widthIn(min = 680.dp, max = 860.dp).heightIn(min = 360.dp, max = 680.dp),
            shape = RoundedCornerShape(22.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.fillMaxSize()) {
                Text(title, Modifier.padding(horizontal = 22.dp, vertical = 18.dp), style = MaterialTheme.typography.titleLarge)
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Surface(
                    Modifier.weight(1f).fillMaxWidth().padding(18.dp),
                    color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.55f),
                    shape = RoundedCornerShape(12.dp),
                ) {
                    SelectionContainer {
                        Text(
                            content,
                            Modifier.fillMaxSize().padding(14.dp)
                                .verticalScroll(rememberScrollState())
                                .horizontalScroll(rememberScrollState()),
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                }
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 14.dp),
                    horizontalArrangement = Arrangement.End,
                ) {
                    OutlinedButton(onClick = onCopy) {
                        Icon(Icons.Outlined.ContentCopy, null, Modifier.size(17.dp))
                        Spacer(Modifier.width(5.dp))
                        Text("复制")
                    }
                    Spacer(Modifier.width(8.dp))
                    Button(onClick = onDismiss) { Text("完成") }
                }
            }
        }
    }
}

@Composable
internal fun DeleteTaskDialog(controller: DesktopApplication, task: TaskManifest, onDismiss: () -> Unit) {
    LaunchedEffect(task.taskDirectoryName) { controller.requestDeleteRisk(task) }
    DisposableEffect(controller, task.taskDirectoryName) {
        onDispose { controller.clearDeleteRisk(task) }
    }
    val inspection = controller.deleteRiskInspections[task.taskDirectoryName]
    val loading = inspection == null || inspection.loading
    val risks = inspection?.risks.orEmpty()
    val inspectionError = inspection?.error
    val safetyCheckFailed = risks.any { it.statusCheckError != null }
    var discard by remember { mutableStateOf(false) }
    var externalWindowsClosed by remember(task.taskDirectoryName) { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("删除任务") },
        text = { Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("即将永久删除“${task.folderName}”的任务目录和所有工作区，远程分支不会被修改。")
            Surface(color = MaterialTheme.colorScheme.errorContainer, shape = RoundedCornerShape(10.dp)) {
                Text(
                    deleteTaskExternalWindowWarning(),
                    Modifier.padding(12.dp),
                    color = MaterialTheme.colorScheme.onErrorContainer,
                )
            }
            Row(
                Modifier.fillMaxWidth().clickable { externalWindowsClosed = !externalWindowsClosed }.padding(vertical = 2.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Checkbox(externalWindowsClosed, { externalWindowsClosed = it })
                Text("我已关闭上述 Codex 项目、IDE 窗口和文件夹")
            }
            if (loading) {
                LinearProgressIndicator(Modifier.fillMaxWidth())
                Text("正在检查 Git 状态…", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            inspectionError?.let {
                DeleteRiskFailure(it, enabled = !controller.busy) { controller.requestDeleteRisk(task) }
            }
            risks.forEach {
                val unpushed = if (it.unpushedCommits > 0) "，${it.unpushedCommits} 个仅本地提交" else ""
                val detail = it.statusCheckError ?: "存在未提交改动、Git 操作或未推送提交$unpushed"
                Text("• ${it.serviceName}：$detail", color = MaterialTheme.colorScheme.error)
            }
            if (risks.any { it.statusCheckError == null }) Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(discard, { discard = it }); Text("确认丢弃未提交改动") }
        } },
        confirmButton = { Button(
            onClick = { controller.deleteTask(task, risks.isNotEmpty(), onCompleted = onDismiss) },
            enabled = !loading && inspectionError == null && !safetyCheckFailed && externalWindowsClosed && (risks.isEmpty() || discard) && !controller.busy,
            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
        ) { Text("永久删除任务") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
internal fun DeleteRiskFailure(message: String, enabled: Boolean, onRetry: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text("删除检查失败：$message", color = MaterialTheme.colorScheme.error)
        TextButton(onClick = onRetry, enabled = enabled) { Text("重新检查") }
    }
}

internal fun deleteTaskExternalWindowWarning(): String =
    "删除前请确认已关闭：Codex 中关联的项目或任务、IDE 中打开的此任务或工作区窗口，以及文件管理器中打开的任务目录。它们可能占用文件，导致删除失败。silverwing 不会自动关闭这些外部窗口。"

@Composable
internal fun ConfirmDialog(
    title: String,
    message: String,
    confirmLabel: String,
    destructive: Boolean = false,
    enabled: Boolean = true,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(message) },
        confirmButton = {
            Button(
                onClick = onConfirm,
                enabled = enabled,
                colors = if (destructive) {
                    ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)
                } else {
                    ButtonDefaults.buttonColors()
                },
            ) { Text(confirmLabel) }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = enabled) { Text("取消") } },
    )
}

@Composable
internal fun DiscardChangesDialog(
    title: String,
    message: String,
    onDismiss: () -> Unit,
    onDiscard: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(message) },
        confirmButton = {
            Button(
                onClick = onDiscard,
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
            ) { Text("放弃修改") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("继续编辑") } },
    )
}

@Composable
internal fun NameDialog(title: String, initial: String, onDismiss: () -> Unit, onSave: (String) -> Unit) {
    var value by remember { mutableStateOf(initial) }
    AlertDialog(onDismissRequest = onDismiss, title = { Text(title) }, text = { OutlinedTextField(value, { value = it }, label = { Text("名称") }, singleLine = true) }, confirmButton = { Button(onClick = { onSave(value) }, enabled = value.isNotBlank()) { Text("保存") } }, dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } })
}
