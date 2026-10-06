@file:OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CancellationException
import java.nio.file.Path

@Composable internal fun CodexMcpManagementSection(controller: DesktopApplication) {
    val ai = controller.codexAiController
    var taskScope by remember { mutableStateOf(false) }
    val task = controller.selectedTask
    val cwd = if (taskScope && task != null) Path.of(controller.taskPath(task)) else null
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(!taskScope, { taskScope = false }, label = { Text("全局配置") }, enabled = !ai.busy)
            FilterChip(taskScope, { taskScope = true }, label = { Text("当前任务") }, enabled = task != null && !ai.busy)
        }
        McpManagementContent(controller, cwd)
    }
}

@Composable internal fun McpManagementContent(controller: DesktopApplication, cwd: Path?) {
    val ai = controller.codexAiController; val key = ai.key(cwd); val snapshot = ai.snapshots[key]
    var editing by remember(cwd) { mutableStateOf(false) }
    var existing by remember(cwd) { mutableStateOf<McpConfiguration?>(null) }
    var deleting by remember(cwd) { mutableStateOf<McpConfiguration?>(null) }
    var source by remember(cwd) { mutableStateOf<McpScope?>(null) }
    LaunchedEffect(cwd) { ai.refresh(cwd, cwd != null) }
    DisposableEffect(cwd) { onDispose { ai.cancelRead(cwd) } }
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("MCP 服务", Modifier.align(Alignment.CenterVertically), style = MaterialTheme.typography.titleMedium)
            OutlinedButton({ ai.refresh(cwd, cwd != null) }, enabled = !ai.busy && ai.loading[key] != true) { Icon(Icons.Outlined.Refresh, null, Modifier.size(16.dp)); Spacer(Modifier.width(6.dp)); Text("刷新") }
            OutlinedButton({ ai.detect(cwd) }, enabled = snapshot != null && !ai.busy) { Text("检测连接") }
            Button({ existing = null; editing = true }, enabled = snapshot != null && !ai.busy) { Icon(Icons.Outlined.Add, null, Modifier.size(16.dp)); Text("添加 MCP") }
        }
        Text(cwd?.let { "任务目录：$it" } ?: "全局配置：${snapshot?.userFile ?: "正在读取…"}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text("保存后，新建或重启 Codex 会话生效。检测连接会初始化当前配置中的服务，不调用业务工具。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (ai.loading[key] == true) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (!editing) AiOperationStatus(ai, cwd)
        snapshot?.warnings?.forEach { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
        if (snapshot != null) {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                FilterChip(source == null, { source = null }, label = { Text("全部") })
                McpScope.entries.filter { scope -> snapshot.servers.any { it.scope == scope } }.forEach { scope -> FilterChip(source == scope, { source = scope }, label = { Text(scope.label) }) }
            }
            val rows = snapshot.servers.filter { source == null || it.scope == source }
            if (rows.isEmpty()) Text("还没有 MCP 服务。添加本地命令或 HTTP 服务后即可使用。", color = MaterialTheme.colorScheme.onSurfaceVariant)
            rows.forEach { server ->
                val checked = ai.connections[key]
                val connection = if (server.pluginId == null) checked?.get(server.name) ?: checked?.let {
                    McpConnectionResult("未就绪：服务未返回状态，请检查配置后重试", false)
                } else null
                Column(Modifier.fillMaxWidth().padding(vertical = 6.dp), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) { Text(server.name, style = MaterialTheme.typography.titleSmall)
                            Text("${server.scope.label}${server.pluginId?.let { " · $it" }.orEmpty()} · ${server.type}", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                        if (server.editable || server.pluginId != null) Switch(server.enabled, { ai.enable(snapshot, server, it) }, modifier = Modifier.semantics { contentDescription = "启用 MCP ${server.name}" }, enabled = !ai.busy)
                    }
                    Text(server.endpoint, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    server.file?.let { Text(it.toString(), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    Text(if (!server.enabled) "已停用" else if (!server.active) server.note ?: "未生效" else "已启用 · ${connection?.label ?: if (server.pluginId != null && checked != null) "结果见连接检测清单" else "连接尚未检测"}", style = MaterialTheme.typography.bodySmall,
                        color = if (connection?.success == false && server.active && server.enabled) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (server.editable) {
                            TextButton({ existing = server; editing = true }, enabled = !ai.busy) { Text("编辑") }
                            TextButton({ deleting = server }, enabled = !ai.busy) { Text("删除", color = MaterialTheme.colorScheme.error) }
                        }
                        if (server.pluginId == null && server.raw.text("url") != null && server.active && server.enabled) {
                            TextButton({ ai.auth(cwd, server, true) }, enabled = !ai.busy) { Text("认证") }
                            TextButton({ ai.auth(cwd, server, false) }, enabled = !ai.busy) { Text("退出认证") }
                        }
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
            }
            ai.connections[key]?.takeIf { it.isNotEmpty() }?.let { result ->
                Text("最近连接检测", style = MaterialTheme.typography.titleSmall)
                result.forEach { (name, state) -> Text("$name · ${state.label}", style = MaterialTheme.typography.bodySmall,
                    color = if (state.success) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.error) }
            }
        }
    }
    if (editing && snapshot != null) McpEditorDialog(controller, snapshot, existing, { if (!ai.busy) editing = false }, { editing = false })
    deleting?.let { server -> AlertDialog(onDismissRequest = { if (!ai.busy) deleting = null }, title = { Text("删除 MCP：${server.name}") }, text = { Text("从${server.scope.label}配置移除此服务。") },
        confirmButton = { TextButton({ snapshot?.let { ai.save(it, requireNotNull(server.file), server.name, null) { deleting = null } } }, enabled = !ai.busy) { Text("删除") } },
        dismissButton = { TextButton({ deleting = null }, enabled = !ai.busy) { Text("取消") } }) }
}

@Composable internal fun AiOperationStatus(ai: CodexAiController, cwd: Path?) {
    if (ai.busy) Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) { Text(ai.busyLabel, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall); TextButton(ai::cancelOperation) { Text("取消操作") } }
    ai.errors[ai.key(cwd)]?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
}

@Composable internal fun TaskAiConfigurationDialog(controller: DesktopApplication, task: TaskManifest, onDismiss: () -> Unit) {
    val ai = controller.codexAiController; val cwd = remember(task) { Path.of(controller.taskPath(task)) }; val key = ai.key(cwd)
    var tab by remember(cwd) { mutableStateOf("MCP") }; var manage by remember { mutableStateOf(false) }; var preview by remember(cwd) { mutableStateOf<Path?>(null) }
    LaunchedEffect(cwd) { ai.refresh(cwd, true) }; DisposableEffect(cwd) { onDispose { ai.cancelRead(cwd) } }
    val snapshot = ai.snapshots[key]
    AiBoundedDialog("任务 AI 配置 · ${task.folderName}", onDismiss, ai.busy, footer = { TextButton(onDismiss, enabled = !ai.busy) { Text("关闭") } }) {
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                listOf("MCP", "Skills", "说明文件").forEach { label -> FilterChip(tab == label, { tab = label }, label = { Text(label) }) }
                OutlinedButton({ ai.refresh(cwd, true) }, enabled = !ai.busy && ai.loading[key] != true) { Text("刷新") }
            }
            if (ai.loading[key] == true) LinearProgressIndicator(Modifier.fillMaxWidth()); if (tab != "MCP" || !manage) AiOperationStatus(ai, cwd)
            Column(Modifier.weight(1f).fillMaxWidth().verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                when (tab) {
                    "MCP" -> if (!manage) {
                        Text("按任务目录读取的配置；会话内临时覆盖不在这里显示。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Button({ manage = true }, enabled = snapshot != null && !ai.busy) { Text("管理 MCP") }
                        snapshot?.servers?.forEach { server -> Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(server.name, style = MaterialTheme.typography.titleSmall)
                            Text("${server.scope.label} · ${if (server.enabled) "已启用" else "已停用"}${server.note?.let { " · $it" }.orEmpty()}", style = MaterialTheme.typography.bodySmall)
                            Text(server.file?.toString() ?: server.pluginId.orEmpty(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant); HorizontalDivider()
                        } }; if (snapshot?.servers?.isEmpty() == true) Text("当前任务没有配置 MCP")
                    } else McpManagementContent(controller, cwd)
                    "Skills" -> {
                        snapshot?.warnings?.forEach { Text(it, color = MaterialTheme.colorScheme.error) }
                        snapshot?.skills?.forEach { skill -> AiFileRow(skill.name, "${aiSkillScopeLabel(skill.scope)} · ${if (skill.enabled) "已启用" else "已停用"}", skill.file,
                            { preview = skill.file }, { controller.openAiConfigurationFile(skill.file) }) }
                        if (snapshot?.skills?.isEmpty() == true) Text("该任务目录没有可用 Skill，可在设置或 Skills 页面安装")
                    }
                    else -> snapshot?.files?.forEach { file -> AiFileRow(file.title, "${file.source}${if (!file.exists) " · 文件缺失" else ""}", file.file,
                        if (file.exists) ({ preview = file.file }) else null, if (file.exists) ({ controller.openAiConfigurationFile(file.file) }) else null) }
                }
            }
        }
    }; preview?.let { file -> AiFilePreview(controller, file) { preview = null } }
}

@Composable private fun AiFileRow(title: String, source: String, file: Path, onRead: (() -> Unit)?, onOpen: (() -> Unit)?) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(title, style = MaterialTheme.typography.titleSmall); Text(source, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(file.toString(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) { onRead?.let { TextButton(it, Modifier.semantics { contentDescription = "阅读文件 $title" }) { Text("阅读") } }; onOpen?.let { TextButton(it) { Text("外部编辑") } } }; HorizontalDivider()
    }
}

@Composable private fun AiFilePreview(controller: DesktopApplication, file: Path, onDismiss: () -> Unit) {
    var content by remember(file) { mutableStateOf<String?>(null) }; var error by remember(file) { mutableStateOf<String?>(null) }; var revision by remember { mutableIntStateOf(0) }
    LaunchedEffect(file, revision) { try { content = controller.codexAiController.fileContent(file); error = null }
        catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { error = failure.message ?: "读取文件失败" } }
    AiBoundedDialog(file.fileName.toString(), onDismiss, false, footer = { TextButton(onDismiss) { Text("关闭") } }) {
        if (content != null) MarkdownFileTabsPreview(listOf(MarkdownPreviewFile(file.fileName.toString(), content!!)), Modifier.fillMaxSize(), onCopySource = { controller.copyText(it.content, "原文已复制") })
        else Column { if (error == null) LinearProgressIndicator(Modifier.fillMaxWidth()) else { Text(error!!, color = MaterialTheme.colorScheme.error); TextButton({ revision++ }) { Text("重试") } } }
    }
}

@Composable internal fun CodexTaskAssociationDialog(controller: DesktopApplication, task: TaskManifest, onDismiss: () -> Unit) {
    val ai = controller.codexAiController; val cwd = remember(task) { Path.of(controller.taskPath(task)) }; val key = ai.key(cwd); var chosen by remember(cwd) { mutableStateOf<String?>(null) }
    LaunchedEffect(cwd) { ai.refreshThreads(cwd) }; LaunchedEffect(ai.boundThreads[key]) { chosen = ai.boundThreads[key] }; DisposableEffect(cwd) { onDispose { ai.cancelThreadRead(cwd) } }
    AiBoundedDialog("关联 Codex 会话", onDismiss, ai.busy, footer = {
        TextButton(onDismiss, enabled = !ai.busy) { Text("关闭") }
        TextButton({ ai.bind(cwd, null, onDismiss) }, enabled = !ai.busy && ai.boundThreads[key] != null) { Text("解除关联") }
        Button({ ai.bind(cwd, chosen, onDismiss) }, enabled = !ai.busy && chosen != null && ai.threadLoading[key] != true) { Text("保存关联") }
    }) {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("选择当前任务目录已有的会话。关联后，“在 Codex 中打开”会返回该会话。", style = MaterialTheme.typography.bodySmall)
            Text(cwd.toString(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedButton({ ai.refreshThreads(cwd) }, enabled = !ai.busy && ai.threadLoading[key] != true) { Text("刷新会话") }
            if (ai.threadLoading[key] == true) LinearProgressIndicator(Modifier.fillMaxWidth()); ai.threadErrors[key]?.let { Text(it, color = MaterialTheme.colorScheme.error) }; AiOperationStatus(ai, cwd)
            if (ai.threads[key]?.isEmpty() == true) { Text("还没有该目录的 Codex 会话，先打开任务目录后再刷新。"); TextButton({ controller.openTaskInCodex(task, forceNew = true) }, enabled = !controller.busy) { Text("在 Codex 中新建会话") } }
            ai.threads[key].orEmpty().forEach { thread -> Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                RadioButton(chosen == thread.id, { chosen = thread.id }, modifier = Modifier.semantics { contentDescription = "关联会话 ${thread.title}" }, enabled = !ai.busy)
                Column(Modifier.weight(1f)) { Text(thread.title, maxLines = 2, overflow = TextOverflow.Ellipsis); Text(thread.id, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            } }
        }
    }
}

private fun aiSkillScopeLabel(scope: String): String = when (scope.lowercase()) {
    "user" -> "用户"; "repo" -> "当前项目"; "system" -> "系统"; "admin" -> "受管理"; else -> scope
}

@Composable internal fun AiBoundedDialog(title: String, onDismiss: () -> Unit, busy: Boolean, footer: @Composable RowScope.() -> Unit, content: @Composable () -> Unit) {
    val window = LocalWindowInfo.current.containerSize; val density = LocalDensity.current
    val width = with(density) { window.width.toDp() }.minus(32.dp).coerceIn(1.dp, 980.dp); val height = with(density) { window.height.toDp() }.minus(32.dp).coerceIn(1.dp, 720.dp)
    Dialog({ if (!busy) onDismiss() }, properties = DialogProperties(usePlatformDefaultWidth = false, dismissOnBackPress = !busy, dismissOnClickOutside = !busy)) {
        Surface(Modifier.width(width).height(height), shape = MaterialTheme.shapes.large, border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
            Column(Modifier.fillMaxSize()) {
                Text(title, Modifier.fillMaxWidth().padding(20.dp), style = MaterialTheme.typography.titleLarge, maxLines = 2, overflow = TextOverflow.Ellipsis); HorizontalDivider()
                Box(Modifier.weight(1f).fillMaxWidth().padding(20.dp)) { content() }; HorizontalDivider()
                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End), content = footer)
            }
        }
    }
}
