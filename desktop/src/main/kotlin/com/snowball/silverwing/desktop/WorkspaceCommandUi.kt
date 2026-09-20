package com.snowball.silverwing.desktop

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.BuildCircle
import androidx.compose.material.icons.outlined.CleaningServices
import androidx.compose.material.icons.outlined.CloudUpload
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Inventory2
import androidx.compose.material.icons.outlined.MoveToInbox
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.RocketLaunch
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.graphics.vector.ImageVector
import com.snowball.silverwing.core.CommandOutputLine
import com.snowball.silverwing.core.CommandOutputStream
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.WorkspaceCommandConfig

internal const val MAX_WORKSPACE_COMMAND_LOG_LINES = 20_000

internal val workspaceCommandIconKeys = listOf(
    "build",
    "maven",
    "maven-clean",
    "maven-install",
    "maven-deploy",
    "play",
    "terminal",
    "package",
    "upload",
    "refresh",
    "code",
    "database",
    "settings",
)

internal fun workspaceCommandIcon(iconKey: String): ImageVector = when (iconKey) {
    "maven" -> Icons.Outlined.BuildCircle
    "maven-clean" -> Icons.Outlined.CleaningServices
    "maven-install" -> Icons.Outlined.MoveToInbox
    "maven-deploy" -> Icons.Outlined.RocketLaunch
    "play" -> Icons.Outlined.PlayArrow
    "terminal" -> Icons.Outlined.Terminal
    "package" -> Icons.Outlined.Inventory2
    "upload" -> Icons.Outlined.CloudUpload
    "refresh" -> Icons.Outlined.Refresh
    "code" -> Icons.Outlined.Code
    "database" -> Icons.Outlined.Storage
    "settings" -> Icons.Outlined.Settings
    else -> Icons.Outlined.Build
}

internal fun workspaceCommandDisplay(command: WorkspaceCommandConfig): String =
    (listOf(command.executable) + command.arguments).joinToString(" ", transform = ::displayCommandToken)

internal fun workspaceCommandOutputCopyText(lines: List<CommandOutputLine>): String =
    lines.joinToString("\n", transform = CommandOutputLine::text)

private fun displayCommandToken(token: String): String {
    if (token.isNotEmpty() && token.none(Char::isWhitespace) && token.none { it == '"' || it == '\'' }) return token
    return "\"${token.replace("\\", "\\\\").replace("\"", "\\\"")}\""
}

@Composable
internal fun WorkspaceCommandDialog(controller: DesktopApplication, task: TaskManifest) {
    val state = controller.workspaceCommandState?.takeIf { it.taskKey == task.taskDirectoryName } ?: return
    val logState = rememberLazyListState()
    LaunchedEffect(state.lines.size) {
        if (state.lines.isNotEmpty()) logState.scrollToItem(state.lines.lastIndex)
    }
    val running = state.status == WorkspaceCommandExecutionStatus.RUNNING
    Dialog(
        onDismissRequest = { if (!running) controller.dismissWorkspaceCommand() },
        properties = DialogProperties(
            usePlatformDefaultWidth = false,
            dismissOnClickOutside = !running,
            dismissOnBackPress = !running,
        ),
    ) {
        Surface(
            Modifier.fillMaxWidth(0.78f).fillMaxHeight(0.82f),
            shape = RoundedCornerShape(20.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.fillMaxSize()) {
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text("执行快捷命令", style = MaterialTheme.typography.titleLarge)
                        Text(
                            "${state.workspaceLabel} · ${state.command.name}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Text(commandExecutionStatusLabel(state.status), color = commandExecutionStatusColor(state.status))
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Column(
                    Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 14.dp),
                    verticalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    SelectionContainer {
                        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(workspaceCommandDisplay(state.command), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyMedium)
                            Text("工作目录：${state.workingDirectory}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
                Surface(
                    Modifier.weight(1f).fillMaxWidth().padding(horizontal = 22.dp),
                    color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.32f),
                    shape = RoundedCornerShape(12.dp),
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
                ) {
                    SelectionContainer {
                        LazyColumn(
                            state = logState,
                            modifier = Modifier.fillMaxSize().padding(horizontal = 12.dp, vertical = 10.dp),
                            verticalArrangement = Arrangement.spacedBy(3.dp),
                        ) {
                            itemsIndexed(state.lines) { _, line ->
                                Text(
                                    line.text,
                                    fontFamily = FontFamily.Monospace,
                                    style = MaterialTheme.typography.bodySmall,
                                    color = if (line.stream == CommandOutputStream.STDERR) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
                                )
                            }
                            if (state.lines.isEmpty()) {
                                item { Text("正在等待命令输出…", color = MaterialTheme.colorScheme.onSurfaceVariant) }
                            }
                        }
                    }
                }
                Column(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 10.dp)) {
                    if (state.truncated) {
                        Text("日志已截断，仅保留最近 $MAX_WORKSPACE_COMMAND_LOG_LINES 行", color = MaterialTheme.colorScheme.tertiary, style = MaterialTheme.typography.bodySmall)
                    }
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        val duration = state.durationMillis?.let(::formatCommandDuration) ?: "执行中"
                        Text(
                            buildString {
                                append("耗时：$duration")
                                state.exitCode?.let { append(" · 退出码：$it") }
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.weight(1f),
                        )
                        OutlinedButton(
                            onClick = { controller.copyText(workspaceCommandOutputCopyText(state.lines), "命令输出已复制") },
                            enabled = state.lines.isNotEmpty(),
                        ) {
                            Text("复制输出")
                        }
                        Spacer(Modifier.width(8.dp))
                        if (running) {
                            TextButton(onClick = controller::cancelWorkspaceCommand) { Text("停止执行") }
                        } else {
                            Button(onClick = controller::dismissWorkspaceCommand) { Text("关闭") }
                        }
                    }
                    state.error?.takeIf(String::isNotBlank)?.let { error ->
                        Text("结果：$error", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }
    }
}

private fun commandExecutionStatusLabel(status: WorkspaceCommandExecutionStatus): String = when (status) {
    WorkspaceCommandExecutionStatus.RUNNING -> "执行中"
    WorkspaceCommandExecutionStatus.SUCCEEDED -> "执行成功"
    WorkspaceCommandExecutionStatus.FAILED -> "执行失败"
    WorkspaceCommandExecutionStatus.CANCELLED -> "已取消"
}

@Composable
private fun commandExecutionStatusColor(status: WorkspaceCommandExecutionStatus) = when (status) {
    WorkspaceCommandExecutionStatus.RUNNING -> MaterialTheme.colorScheme.primary
    WorkspaceCommandExecutionStatus.SUCCEEDED -> SuccessGreen
    WorkspaceCommandExecutionStatus.FAILED -> MaterialTheme.colorScheme.error
    WorkspaceCommandExecutionStatus.CANCELLED -> MaterialTheme.colorScheme.onSurfaceVariant
}

private fun formatCommandDuration(durationMillis: Long): String = when {
    durationMillis < 1_000 -> "${durationMillis}ms"
    else -> "%.1fs".format(java.util.Locale.ROOT, durationMillis / 1_000.0)
}
