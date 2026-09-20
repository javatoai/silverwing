package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
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
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.BootstrapCommand
import com.snowball.silverwing.core.BootstrapConfig
import com.snowball.silverwing.core.BootstrapCopyRule
import com.snowball.silverwing.core.BootstrapPresets
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.ServiceModuleConfig
import com.snowball.silverwing.core.StandardWorktreeModuleNaming
import com.snowball.silverwing.core.TagBuildMode
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.WorkspaceCommandConfig
import com.snowball.silverwing.core.validateWorkspaceCommand
import com.snowball.silverwing.core.validated
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.nio.file.Path
import java.util.UUID

internal data class WorkspaceCommandEditorDraft(
    val id: String,
    val name: String,
    val iconKey: String,
    val executable: String,
    val arguments: List<String>,
    val workingDirectory: String,
    val timeoutSeconds: String,
    val enabled: Boolean,
) {
    fun toConfig(): WorkspaceCommandConfig = WorkspaceCommandConfig(
        id = id,
        name = name,
        iconKey = iconKey,
        executable = executable,
        arguments = arguments,
        workingDirectory = workingDirectory,
        timeoutSeconds = timeoutSeconds.toLongOrNull() ?: 0,
        enabled = enabled,
    )
}

internal fun WorkspaceCommandConfig.toEditorDraft(): WorkspaceCommandEditorDraft = WorkspaceCommandEditorDraft(
    id = id,
    name = name,
    iconKey = iconKey,
    executable = executable,
    arguments = arguments,
    workingDirectory = workingDirectory,
    timeoutSeconds = timeoutSeconds.toString(),
    enabled = enabled,
)

internal data class ServiceModuleEditorDraft(
    val id: String,
    val name: String = StandardWorktreeModuleNaming.DEFAULT_NAME,
    val strategy: WorkspaceStrategy = WorkspaceStrategy.STANDARD_WORKTREE,
    val baseRef: String = "origin/master",
    val baseRemote: String = "origin",
    val tagEnabled: Boolean = true,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val tagTargetRef: String = "origin/release/test",
    val tagMessagePrefix: String = "Tag",
    val customCommands: List<WorkspaceCommandEditorDraft> = emptyList(),
) {
    fun toConfig(): ServiceModuleConfig = ServiceModuleConfig(
        id = id,
        name = name.trim().ifBlank { StandardWorktreeModuleNaming.DEFAULT_NAME },
        strategy = strategy,
        baseRef = normalizeBaseRefForStrategy(strategy, baseRef),
        baseRemote = baseRemote.trim(),
        tagEnabled = tagEnabled,
        tagMode = tagMode,
        tagTargetRef = if (tagMode == TagBuildMode.CURRENT_BRANCH) null else tagTargetRef.trim(),
        tagMessagePrefix = tagMessagePrefix.trim(),
        customCommands = customCommands.map(WorkspaceCommandEditorDraft::toConfig)
            .map { it.validateWorkspaceCommand() },
    )
}

internal fun normalizeBaseRefForStrategy(strategy: WorkspaceStrategy, baseRef: String): String {
    return baseRef.trim()
}

internal fun ServiceModuleConfig.toEditorDraft(): ServiceModuleEditorDraft = ServiceModuleEditorDraft(
    id = id,
    name = name,
    strategy = strategy,
    baseRef = baseRef,
    baseRemote = baseRemote,
    tagEnabled = tagEnabled,
    tagMode = tagMode,
    tagTargetRef = tagTargetRef.orEmpty(),
    tagMessagePrefix = tagMessagePrefix,
    customCommands = customCommands.map(WorkspaceCommandConfig::toEditorDraft),
)

@Composable
internal fun ServiceEditorDialog(controller: DesktopApplication, service: GroupServiceConfig, onDismiss: () -> Unit, onSave: (GroupServiceConfig) -> Unit) {
    val widthPolicy = serviceEditorDialogWidthPolicy()
    val json = remember { Json { prettyPrint = true; encodeDefaults = true } }
    val initialBootstrapText = remember(service) { json.encodeToString(service.bootstrap) }
    var name by remember { mutableStateOf(service.displayName) }
    var enabled by remember { mutableStateOf(service.enabled) }
    var genbuProbeEnabled by remember { mutableStateOf(service.genbuProbeEnabled) }
    var genbuServiceName by remember { mutableStateOf(service.genbuServiceName) }
    var developmentTool by remember { mutableStateOf(service.developmentTool) }
    var commitMessageTemplate by remember { mutableStateOf(service.commitMessageTemplate) }
    val initialModuleDrafts = remember(service) { service.modules.map(ServiceModuleConfig::toEditorDraft) }
    var modules by remember(service) { mutableStateOf(initialModuleDrafts) }
    var bootstrapConfig by remember(service) { mutableStateOf(service.bootstrap) }
    var bootstrapText by remember { mutableStateOf(initialBootstrapText) }
    var bootstrapMode by remember { mutableStateOf("form") }
    var bootstrapError by remember { mutableStateOf<String?>(null) }
    var serviceValidationError by remember { mutableStateOf<String?>(null) }
    var showBootstrapExample by remember { mutableStateOf(false) }
    var bootstrapCopied by remember { mutableStateOf(false) }
    var confirmDiscard by remember { mutableStateOf(false) }
    var selectedSection by remember { mutableStateOf("basic") }
    val hasDraftChanges = name != service.displayName || enabled != service.enabled || genbuProbeEnabled != service.genbuProbeEnabled ||
        genbuServiceName != service.genbuServiceName || developmentTool != service.developmentTool || commitMessageTemplate != service.commitMessageTemplate ||
        modules != initialModuleDrafts ||
        bootstrapConfig != service.bootstrap || bootstrapText != initialBootstrapText
    val requestDismiss = { if (hasDraftChanges) confirmDiscard = true else onDismiss() }
    fun switchBootstrapToForm() {
        val parsed = runCatching { json.decodeFromString<BootstrapConfig>(bootstrapText).validated() }
            .getOrElse { error -> bootstrapError = "JSON 格式错误：${error.message}"; return }
        bootstrapConfig = parsed
        bootstrapMode = "form"
        bootstrapError = null
    }
    fun switchBootstrapToJson() {
        bootstrapText = json.encodeToString(bootstrapConfig)
        bootstrapMode = "json"
        bootstrapError = null
    }
    val sections = listOf(
        "basic" to "基本信息",
        "tools" to "工具与提交",
        "modules" to "工作区模块",
        "bootstrap" to "Bootstrap",
    )
    Dialog(onDismissRequest = requestDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(
            Modifier
                .fillMaxWidth(widthPolicy.fillFraction)
                .fillMaxHeight(0.90f)
                .widthIn(min = widthPolicy.minWidthDp.dp, max = widthPolicy.maxWidthDp.dp)
                .heightIn(min = 620.dp, max = 900.dp),
            shape = RoundedCornerShape(22.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.fillMaxSize()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 17.dp), verticalAlignment = Alignment.CenterVertically) {
                    Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = RoundedCornerShape(11.dp)) {
                        Icon(Icons.Outlined.Settings, null, Modifier.padding(10.dp), tint = MaterialTheme.colorScheme.primary)
                    }
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text("服务配置", style = MaterialTheme.typography.titleLarge)
                        Text(service.displayName, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    MetaPill("${modules.count { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }} Worktree · ${modules.count { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }} 克隆")
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(
                    Modifier.weight(1f).padding(20.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                ) {
                    Surface(
                        Modifier.width(200.dp).fillMaxHeight(),
                        shape = RoundedCornerShape(16.dp),
                        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.38f),
                        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
                    ) {
                        Column(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            sections.forEach { (key, label) ->
                                Surface(
                                    Modifier.fillMaxWidth().clickable { selectedSection = key },
                                    color = if (selectedSection == key) MaterialTheme.colorScheme.primaryContainer else Color.Transparent,
                                    shape = RoundedCornerShape(10.dp),
                                ) {
                                    Text(
                                        label,
                                        Modifier.padding(horizontal = 14.dp, vertical = 12.dp),
                                        fontWeight = if (selectedSection == key) FontWeight.SemiBold else FontWeight.Normal,
                                        color = if (selectedSection == key) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurface,
                                    )
                                }
                            }
                        }
                    }
                    Column(
                        Modifier.weight(1f).fillMaxHeight().verticalScroll(rememberScrollState()),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        when (selectedSection) {
                            "basic" -> ServiceBasicSection(
                                name = name,
                                onNameChange = { name = it },
                                enabled = enabled,
                                onEnabledChange = { enabled = it },
                                genbuProbeEnabled = genbuProbeEnabled,
                                onGenbuProbeEnabledChange = { genbuProbeEnabled = it },
                                genbuServiceName = genbuServiceName,
                                onGenbuServiceNameChange = { genbuServiceName = it },
                            )
                            "tools" -> ServiceToolsSection(
                                developmentTool = developmentTool,
                                onDevelopmentToolChange = { developmentTool = it },
                                commitMessageTemplate = commitMessageTemplate,
                                onCommitMessageTemplateChange = { commitMessageTemplate = it },
                            )
                            "modules" -> ServiceModulesSection(
                                modules = modules,
                                onModulesChange = { modules = it },
                                repositoryId = service.repositoryId,
                                controller = controller,
                            )
                            "bootstrap" -> ServiceBootstrapSection(
                                controller = controller,
                                repositoryRoot = controller.config.repositories.firstOrNull { it.id == service.repositoryId }?.rootPath,
                                mode = bootstrapMode,
                                config = bootstrapConfig,
                                text = bootstrapText,
                                error = bootstrapError,
                                onSwitchToForm = { switchBootstrapToForm() },
                                onSwitchToJson = { switchBootstrapToJson() },
                                onShowExample = { bootstrapCopied = false; showBootstrapExample = true },
                                onConfigChange = {
                                    bootstrapConfig = it
                                    bootstrapText = json.encodeToString(it)
                                    bootstrapError = null
                                },
                                onTextChange = { bootstrapText = it; bootstrapError = null },
                                onError = { bootstrapError = it },
                            )
                        }
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 14.dp), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
                    Text("修改仅影响后续任务", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    TextButton(onClick = requestDismiss) { Text("取消") }
                    Spacer(Modifier.width(8.dp))
                    Button(onClick = {
                        val bootstrap = if (bootstrapMode == "json") {
                            runCatching { json.decodeFromString<BootstrapConfig>(bootstrapText).validated() }
                                .getOrElse { error -> bootstrapError = "JSON 格式错误：${error.message}"; return@Button }
                        } else bootstrapConfig
                        runCatching {
                            val normalizedModules = modules.map(ServiceModuleEditorDraft::toConfig)
                            validateServiceWorkspaceModules(normalizedModules)
                            service.copy(
                                displayName = name.trim(), enabled = enabled,
                                genbuProbeEnabled = genbuProbeEnabled,
                                genbuServiceName = genbuServiceName.trim(),
                                developmentTool = developmentTool,
                                commitMessageTemplate = commitMessageTemplate.trim(),
                                modules = normalizedModules,
                                bootstrap = bootstrap,
                            )
                        }.onSuccess(onSave).onFailure { serviceValidationError = OperationFailureDetails.format(it) }
                    }, enabled = name.isNotBlank() && modules.isNotEmpty() && modules.all { it.baseRef.isNotBlank() }) { Icon(Icons.Outlined.Save, null, Modifier.size(17.dp)); Spacer(Modifier.width(5.dp)); Text("保存配置") }
                }
            }
        }
    }
    if (showBootstrapExample) {
        val example = remember { json.encodeToString(BootstrapPresets.example()) }
        Dialog(
            onDismissRequest = { showBootstrapExample = false },
            properties = DialogProperties(usePlatformDefaultWidth = false),
        ) {
            Surface(
                Modifier.width(820.dp).height(620.dp),
                shape = RoundedCornerShape(22.dp),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            ) {
                Column(Modifier.fillMaxSize()) {
                    Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("Bootstrap JSON 示例", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge)
                        if (bootstrapCopied) Text("已复制", color = SuccessGreen, style = MaterialTheme.typography.labelMedium)
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Surface(
                        Modifier.weight(1f).fillMaxWidth().padding(18.dp),
                        color = MaterialTheme.colorScheme.surfaceVariant,
                        shape = RoundedCornerShape(12.dp),
                    ) {
                        Text(
                            example,
                            Modifier.fillMaxSize().padding(16.dp).verticalScroll(rememberScrollState()),
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 14.dp), horizontalArrangement = Arrangement.End) {
                        TextButton(onClick = { showBootstrapExample = false }) { Text("关闭") }
                        Spacer(Modifier.width(8.dp))
                        Button(onClick = {
                            controller.copyText(example, "Bootstrap 示例已复制")
                            bootstrapCopied = true
                        }) {
                            Icon(Icons.Outlined.ContentCopy, null)
                            Spacer(Modifier.width(5.dp))
                            Text("复制")
                        }
                    }
                }
            }
        }
    }
    serviceValidationError?.let { details ->
        TagResultDialog(
            title = "服务配置无法保存",
            content = details,
            onDismiss = { serviceValidationError = null },
            onCopy = { controller.copyText(details, "错误详情已复制") },
        )
    }
    if (confirmDiscard) {
        DiscardChangesDialog(
            title = "放弃服务配置修改？",
            message = "尚未保存的模块、分支、测试Tag和 Bootstrap 配置将丢失。",
            onDismiss = { confirmDiscard = false },
            onDiscard = onDismiss,
        )
    }
}

internal data class ServiceEditorDialogWidthPolicy(
    val fillFraction: Float,
    val minWidthDp: Int,
    val maxWidthDp: Int,
)

internal fun serviceEditorDialogWidthPolicy(): ServiceEditorDialogWidthPolicy =
    ServiceEditorDialogWidthPolicy(
        fillFraction = 0.72f,
        minWidthDp = 860,
        maxWidthDp = 1200,
    )

@Composable
private fun ServiceBasicSection(
    name: String,
    onNameChange: (String) -> Unit,
    enabled: Boolean,
    onEnabledChange: (Boolean) -> Unit,
    genbuProbeEnabled: Boolean,
    onGenbuProbeEnabledChange: (Boolean) -> Unit,
    genbuServiceName: String,
    onGenbuServiceNameChange: (String) -> Unit,
) {
    SectionHeader("基本信息", "展示名称用于服务列表与任务界面；停用后创建任务时不再可选")
    OutlinedTextField(name, onNameChange, Modifier.fillMaxWidth(), label = { Text("展示名称") })
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("启用服务", style = MaterialTheme.typography.titleSmall)
        Switch(enabled, onEnabledChange)
    }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("启用 Genbu 探测", style = MaterialTheme.typography.titleSmall)
        Switch(genbuProbeEnabled, onGenbuProbeEnabledChange)
    }
    if (genbuProbeEnabled) {
        OutlinedTextField(
            genbuServiceName,
            onGenbuServiceNameChange,
            Modifier.fillMaxWidth(),
            label = { Text("Genbu 服务名") },
            supportingText = { Text("用于查询该服务的Tag构建与发布状态") },
            singleLine = true,
        )
    }
}

@Composable
private fun ServiceToolsSection(
    developmentTool: DevelopmentToolType,
    onDevelopmentToolChange: (DevelopmentToolType) -> Unit,
    commitMessageTemplate: String,
    onCommitMessageTemplateChange: (String) -> Unit,
) {
    SectionHeader("工具与提交", "IDE 是系统建议，可手工修改；保存值始终作为最终依据")
    Text("默认开发工具", style = MaterialTheme.typography.titleSmall)
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        DevelopmentToolType.entries.forEach { value ->
            FilterChip(developmentTool == value, { onDevelopmentToolChange(value) }, label = { Text(value.displayName) })
        }
    }
    OutlinedTextField(commitMessageTemplate, onCommitMessageTemplateChange, Modifier.fillMaxWidth(), label = { Text("默认提交信息模板（支持 {num}）") }, singleLine = true)
}

@Composable
private fun ModuleCustomCommandsSection(
    commands: List<WorkspaceCommandEditorDraft>,
    onCommandsChange: (List<WorkspaceCommandEditorDraft>) -> Unit,
) {
    SectionHeader("快捷命令", "仅对当前模块显示；参数每行一个，不经过 Shell 拼接")
    if (commands.isEmpty()) {
        Text("尚未配置快捷命令。可以添加 Maven clean、install、deploy 等命令。", color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    commands.forEachIndexed { index, command ->
        var argumentsText by remember(command.id) { mutableStateOf(command.arguments.joinToString("\n")) }
        OutlinedCard(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(9.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        command.name,
                        { value -> onCommandsChange(commands.replaceAt(index, command.copy(name = value))) },
                        Modifier.weight(1f),
                        label = { Text("命令名称") },
                        singleLine = true,
                    )
                    OutlinedTextField(
                        command.executable,
                        { value -> onCommandsChange(commands.replaceAt(index, command.copy(executable = value))) },
                        Modifier.weight(1f),
                        label = { Text("可执行程序") },
                        singleLine = true,
                    )
                    IconButton(onClick = { onCommandsChange(commands.filterIndexed { current, _ -> current != index }) }) {
                        Icon(Icons.Outlined.Delete, "删除快捷命令")
                    }
                }
                Text("图标", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    workspaceCommandIconKeys.forEach { iconKey ->
                        FilterChip(
                            selected = command.iconKey == iconKey,
                            onClick = { onCommandsChange(commands.replaceAt(index, command.copy(iconKey = iconKey))) },
                            leadingIcon = { Icon(workspaceCommandIcon(iconKey), null, Modifier.size(17.dp)) },
                            label = { Text(workspaceCommandIconLabel(iconKey)) },
                        )
                    }
                }
                OutlinedTextField(
                    argumentsText,
                    { value ->
                        argumentsText = value
                        onCommandsChange(commands.replaceAt(index, command.copy(arguments = value.lines().filter(String::isNotBlank))))
                    },
                    Modifier.fillMaxWidth(),
                    label = { Text("参数（每行一个）") },
                    minLines = 2,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        command.workingDirectory,
                        { value -> onCommandsChange(commands.replaceAt(index, command.copy(workingDirectory = value))) },
                        Modifier.weight(1f),
                        label = { Text("工作目录（工作区内相对路径）") },
                        singleLine = true,
                    )
                    OutlinedTextField(
                        command.timeoutSeconds,
                        { value -> onCommandsChange(commands.replaceAt(index, command.copy(timeoutSeconds = value))) },
                        Modifier.width(150.dp),
                        label = { Text("超时（秒）") },
                        singleLine = true,
                    )
                    Switch(command.enabled, { checked -> onCommandsChange(commands.replaceAt(index, command.copy(enabled = checked))) })
                    Text("启用")
                }
                val preview = runCatching { command.toConfig() }.getOrNull()?.let(::workspaceCommandDisplay)
                    ?: (listOf(command.executable) + command.arguments).joinToString(" ")
                SelectionContainer {
                    Text("预览：$preview", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
    OutlinedButton(onClick = {
        onCommandsChange(
            commands + WorkspaceCommandEditorDraft(
                id = "command-${UUID.randomUUID()}",
                name = "Maven命令",
                iconKey = "build",
                executable = "mvn",
                arguments = emptyList(),
                workingDirectory = ".",
                timeoutSeconds = "1800",
                enabled = true,
            ),
        )
    }) {
        Icon(Icons.Outlined.Add, null)
        Spacer(Modifier.width(4.dp))
        Text("添加快捷命令")
    }
}

private fun workspaceCommandIconLabel(iconKey: String): String = when (iconKey) {
    "build" -> "构建"
    "maven" -> "Maven"
    "maven-clean" -> "Maven Clean"
    "maven-install" -> "Maven Install"
    "maven-deploy" -> "Maven Deploy"
    "play" -> "执行"
    "terminal" -> "终端"
    "package" -> "打包"
    "upload" -> "上传"
    "refresh" -> "刷新"
    "code" -> "代码"
    "database" -> "数据库"
    "settings" -> "设置"
    else -> "命令"
}

@Composable
private fun ServiceModulesSection(
    modules: List<ServiceModuleEditorDraft>,
    onModulesChange: (List<ServiceModuleEditorDraft>) -> Unit,
    repositoryId: String,
    controller: DesktopApplication,
) {
    SectionHeader("工作区模块", "同一服务可同时包含 Worktree 和独立克隆模块")
    modules.forEachIndexed { index, module ->
        ModuleEditor(module, repositoryId, controller, canDelete = modules.size > 1, onChange = { changed -> onModulesChange(modules.mapIndexed { i, value -> if (i == index) changed else value }) }, onDelete = { onModulesChange(modules.filterIndexed { i, _ -> i != index }) })
    }
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        OutlinedButton(onClick = {
            onModulesChange(modules + ServiceModuleEditorDraft(id = "module-${UUID.randomUUID()}", strategy = WorkspaceStrategy.STANDARD_WORKTREE))
        }) { Icon(Icons.Outlined.Add, null); Text("添加 Worktree 模块") }
        OutlinedButton(onClick = {
            onModulesChange(modules + ServiceModuleEditorDraft(id = "clone-${UUID.randomUUID()}", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, tagEnabled = false))
        }) { Icon(Icons.Outlined.Add, null); Text("添加克隆模块") }
    }
}

@Composable
private fun ServiceBootstrapSection(
    controller: DesktopApplication,
    repositoryRoot: String?,
    mode: String,
    config: BootstrapConfig,
    text: String,
    error: String?,
    onSwitchToForm: () -> Unit,
    onSwitchToJson: () -> Unit,
    onShowExample: () -> Unit,
    onConfigChange: (BootstrapConfig) -> Unit,
    onTextChange: (String) -> Unit,
    onError: (String?) -> Unit,
) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        SectionHeader("Bootstrap", "创建工作区后复制本机文件并执行初始化命令")
        Spacer(Modifier.weight(1f))
        FilterChip(mode == "form", { onSwitchToForm() }, label = { Text("表单配置") })
        Spacer(Modifier.width(6.dp))
        FilterChip(mode == "json", { onSwitchToJson() }, label = { Text("高级 JSON") })
        TextButton(onClick = onShowExample) { Text("查看示例") }
    }
    if (mode == "form") {
        BootstrapFormEditor(
            config = config,
            repositoryRoot = repositoryRoot,
            controller = controller,
            onChange = onConfigChange,
            onError = onError,
        )
    } else {
        OutlinedTextField(
            text,
            onTextChange,
            Modifier.fillMaxWidth(),
            label = { Text("Bootstrap JSON") },
            minLines = 8,
            isError = error != null,
            supportingText = error?.let { message -> { Text(message) } },
        )
    }
    error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
}

@Composable
private fun BootstrapFormEditor(
    config: BootstrapConfig,
    repositoryRoot: String?,
    controller: DesktopApplication,
    onChange: (BootstrapConfig) -> Unit,
    onError: (String?) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("复制规则", fontWeight = FontWeight.SemiBold)
        config.copyRules.forEachIndexed { index, rule ->
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        OutlinedTextField(rule.source, { value ->
                            onChange(config.copy(copyRules = config.copyRules.replaceAt(index, rule.copy(source = value))))
                        }, Modifier.weight(1f), label = { Text("仓库内源路径") }, singleLine = true)
                        OutlinedButton(onClick = {
                            if (repositoryRoot.isNullOrBlank()) {
                                onError("请先配置原始仓库目录")
                            } else controller.chooseFile(repositoryRoot) { chosen ->
                                runCatching { repositoryRelativePath(repositoryRoot, chosen) }
                                    .onSuccess { relative ->
                                        onChange(config.copy(copyRules = config.copyRules.replaceAt(index, rule.withSelectedSource(relative))))
                                    }
                                    .onFailure { onError(it.message) }
                            }
                        }) { Text("选择文件") }
                        OutlinedButton(onClick = {
                            if (repositoryRoot.isNullOrBlank()) {
                                onError("请先配置原始仓库目录")
                            } else controller.chooseDirectory(repositoryRoot) { chosen ->
                                runCatching { repositoryRelativePath(repositoryRoot, chosen) }
                                    .onSuccess { relative ->
                                        onChange(config.copy(copyRules = config.copyRules.replaceAt(index, rule.withSelectedSource(relative))))
                                    }
                                    .onFailure { onError(it.message) }
                            }
                        }) { Text("选择目录") }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        OutlinedTextField(rule.target, { value ->
                            onChange(config.copy(copyRules = config.copyRules.replaceAt(index, rule.copy(target = value))))
                        }, Modifier.weight(1f), label = { Text("工作区目标路径") }, singleLine = true)
                        Checkbox(rule.overwrite, { checked ->
                            onChange(config.copy(copyRules = config.copyRules.replaceAt(index, rule.copy(overwrite = checked))))
                        })
                        Text("允许覆盖")
                        IconButton(onClick = { onChange(config.copy(copyRules = config.copyRules.filterIndexed { i, _ -> i != index })) }) {
                            Icon(Icons.Outlined.Delete, "删除复制规则")
                        }
                    }
                }
            }
        }
        OutlinedButton(onClick = {
            onChange(config.copy(copyRules = config.copyRules + BootstrapCopyRule(source = "", target = "")))
        }) { Icon(Icons.Outlined.Add, null); Spacer(Modifier.width(4.dp)); Text("添加复制规则") }

        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Text("初始化命令", fontWeight = FontWeight.SemiBold)
        config.commands.forEachIndexed { index, command ->
            var argumentsText by remember(command) { mutableStateOf(command.arguments.joinToString("\n")) }
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedTextField(command.name, { value -> onChange(config.copy(commands = config.commands.replaceAt(index, command.copy(name = value)))) }, Modifier.weight(1f), label = { Text("名称") }, singleLine = true)
                        OutlinedTextField(command.executable, { value -> onChange(config.copy(commands = config.commands.replaceAt(index, command.copy(executable = value)))) }, Modifier.weight(1f), label = { Text("可执行程序") }, singleLine = true)
                    }
                    OutlinedTextField(argumentsText, { value ->
                        argumentsText = value
                        onChange(config.copy(commands = config.commands.replaceAt(index, command.copy(arguments = value.lines().filter { it.isNotBlank() }))))
                    }, Modifier.fillMaxWidth(), label = { Text("参数（每行一个）") }, minLines = 2)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        OutlinedTextField(command.workingDirectory, { value -> onChange(config.copy(commands = config.commands.replaceAt(index, command.copy(workingDirectory = value)))) }, Modifier.weight(1f), label = { Text("工作目录") }, singleLine = true)
                        OutlinedTextField(command.timeoutSeconds.toString(), { value -> value.toLongOrNull()?.takeIf { it > 0 }?.let { timeout -> onChange(config.copy(commands = config.commands.replaceAt(index, command.copy(timeoutSeconds = timeout)))) } }, Modifier.width(150.dp), label = { Text("超时（秒）") }, singleLine = true)
                        Checkbox(command.enabled, { checked -> onChange(config.copy(commands = config.commands.replaceAt(index, command.copy(enabled = checked)))) })
                        Text("启用")
                        IconButton(onClick = { onChange(config.copy(commands = config.commands.filterIndexed { i, _ -> i != index })) }) { Icon(Icons.Outlined.Delete, "删除命令") }
                    }
                    val preview = (listOf(command.executable) + command.arguments).joinToString(" ") { argument -> if (argument.any(Char::isWhitespace)) "\"$argument\"" else argument }
                    SelectionContainer { Text("预览：$preview", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                }
            }
        }
        OutlinedButton(onClick = {
            onChange(config.copy(commands = config.commands + BootstrapCommand(name = "初始化", executable = "")))
        }) { Icon(Icons.Outlined.Add, null); Spacer(Modifier.width(4.dp)); Text("添加命令") }
    }
}

internal fun BootstrapCopyRule.withSelectedSource(relative: String): BootstrapCopyRule = copy(
    source = relative,
    target = if (target.isBlank() || target == source) relative else target,
)

private fun repositoryRelativePath(repositoryRoot: String, selectedPath: String): String {
    val root = Path.of(repositoryRoot).toAbsolutePath().normalize()
    val selected = Path.of(selectedPath).toAbsolutePath().normalize()
    require(selected.startsWith(root)) { "只能选择原始仓库内的文件或目录" }
    val relative = root.relativize(selected)
    require(relative.none { it.toString() == ".." }) { "路径不能包含 .." }
    require(relative.none { it.toString().equals(".git", ignoreCase = true) }) { "不能选择 .git 内容" }
    require(!java.nio.file.Files.isSymbolicLink(selected)) { "不能选择符号链接" }
    return relative.toString().replace('\\', '/')
}

internal fun validateServiceWorkspaceModules(
    modules: List<ServiceModuleConfig>,
) {
    StandardWorktreeModuleNaming.requireValid(modules)
    require(modules.map { it.id.lowercase() }.distinct().size == modules.size) { "模块 ID 不能重复（忽略大小写）" }
}

@Composable
private fun ModuleEditor(module: ServiceModuleEditorDraft, repositoryId: String, controller: DesktopApplication, canDelete: Boolean, onChange: (ServiceModuleEditorDraft) -> Unit, onDelete: () -> Unit) {
    OutlinedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                WorkspaceStrategy.entries.forEach { strategy ->
                    FilterChip(
                        selected = module.strategy == strategy,
                        onClick = {
                            onChange(module.copy(
                                strategy = strategy,
                                baseRef = normalizeBaseRefForStrategy(strategy, module.baseRef),
                                baseRemote = module.baseRemote,
                            ))
                        },
                        label = { Text(strategy.displayName) },
                    )
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.Top) {
                OutlinedTextField(
                    module.name,
                    { onChange(module.copy(name = it)) },
                    Modifier.weight(1f),
                    label = { Text("模块名") },
                    placeholder = { Text(StandardWorktreeModuleNaming.DEFAULT_NAME) },
                    supportingText = { Text("仅允许英文字母、数字、-、_、/") },
                )
                ActionIconButton("删除模块", onDelete, Modifier.padding(top = 8.dp), enabled = canDelete) {
                    Icon(Icons.Outlined.Delete, "删除模块")
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                RemoteNamePicker(
                    value = module.baseRemote,
                    repositoryId = repositoryId,
                    controller = controller,
                    onSelected = { remote ->
                        val branch = module.baseRef.substringAfter('/', module.baseRef)
                        onChange(module.copy(baseRemote = remote, baseRef = "$remote/$branch"))
                    },
                    modifier = Modifier.width(160.dp),
                )
                RemoteBranchPicker(
                    module.baseRef,
                    { onChange(module.copy(baseRef = it)) },
                    "基础分支",
                    repositoryId,
                    controller,
                    Modifier.weight(1f),
                    remote = module.baseRemote,
                )
            }
            if (controller.config.tagEnabled) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("测试Tag", Modifier.weight(1f)); Switch(module.tagEnabled, { onChange(module.copy(tagEnabled = it)) })
                }
                if (module.tagEnabled) TagModeSelector(module.tagMode) { mode ->
                    onChange(module.copy(tagMode = mode, tagTargetRef = if (mode == TagBuildMode.CURRENT_BRANCH) "" else module.tagTargetRef.ifBlank { "origin/release/test" }))
                }
                if (module.tagEnabled) TagConfigurationFields(
                    targetVisible = module.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH,
                    targetRef = module.tagTargetRef,
                    onTargetRefChange = { onChange(module.copy(tagTargetRef = it)) },
                    messagePrefix = module.tagMessagePrefix,
                    onMessagePrefixChange = { onChange(module.copy(tagMessagePrefix = it)) },
                    repositoryId = repositoryId,
                    controller = controller,
                )
            }
            ModuleCustomCommandsSection(
                commands = module.customCommands,
                onCommandsChange = { onChange(module.copy(customCommands = it)) },
            )
        }
    }
}

@Composable
private fun TagModeSelector(mode: TagBuildMode, onChange: (TagBuildMode) -> Unit) {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        FilterChip(
            selected = mode == TagBuildMode.MERGE_TO_TARGET_BRANCH,
            onClick = { onChange(TagBuildMode.MERGE_TO_TARGET_BRANCH) },
            label = { Text("合并到目标分支后构建测试Tag") },
        )
        FilterChip(
            selected = mode == TagBuildMode.CURRENT_BRANCH,
            onClick = { onChange(TagBuildMode.CURRENT_BRANCH) },
            label = { Text("当前分支直接构建测试Tag") },
        )
    }
}

internal data class TagConfigurationFieldLayout(
    val heightDp: Int?,
    val singleLine: Boolean,
    val targetWeight: Float,
    val messageWeight: Float,
)

internal fun tagConfigurationFieldLayout(): TagConfigurationFieldLayout =
    TagConfigurationFieldLayout(
        heightDp = null,
        singleLine = true,
        targetWeight = 1f,
        messageWeight = 1f,
    )

private fun Modifier.tagConfigurationFieldHeight(heightDp: Int?): Modifier =
    if (heightDp == null) this else height(heightDp.dp)

@Composable
private fun TagConfigurationFields(
    targetVisible: Boolean,
    targetRef: String,
    onTargetRefChange: (String) -> Unit,
    messagePrefix: String,
    onMessagePrefixChange: (String) -> Unit,
    repositoryId: String,
    controller: DesktopApplication,
) {
    val layout = tagConfigurationFieldLayout()
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (targetVisible) {
            RemoteBranchPicker(
                targetRef,
                onTargetRefChange,
                "测试Tag目标分支",
                repositoryId,
                controller,
                Modifier.weight(layout.targetWeight).tagConfigurationFieldHeight(layout.heightDp),
            )
        }
        OutlinedTextField(
            messagePrefix,
            onMessagePrefixChange,
            Modifier.weight(layout.messageWeight).tagConfigurationFieldHeight(layout.heightDp),
            label = { Text("测试Tag消息前缀") },
            singleLine = layout.singleLine,
            colors = branchPickerFieldColors(),
        )
    }
}
