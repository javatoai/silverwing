package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.key
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import com.snowball.silverwing.core.RemoteBranchRef
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
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
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField as MaterialTextField
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
    val argumentsText: String = arguments.joinToString("\n"),
) {
    fun toConfig(): WorkspaceCommandConfig = WorkspaceCommandConfig(
        id = id,
        name = name,
        iconKey = iconKey,
        executable = executable,
        arguments = argumentsText.lines().filter(String::isNotBlank),
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
    val masterBranch: String? = null,
    val tagEnabled: Boolean = true,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val tagTargetRef: String? = null,
    val tagMessagePrefix: String = "Tag",
    val customCommands: List<WorkspaceCommandEditorDraft> = emptyList(),
) {
    fun toConfig(): ServiceModuleConfig = ServiceModuleConfig(
        id = id,
        name = name.trim().ifBlank { StandardWorktreeModuleNaming.DEFAULT_NAME },
        strategy = strategy,
        masterBranch = masterBranch?.trim(),
        tagEnabled = tagEnabled,
        tagMode = tagMode,
        tagTargetRef = if (tagMode == TagBuildMode.CURRENT_BRANCH) null else tagTargetRef?.trim(),
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
    masterBranch = masterBranch,
    tagEnabled = tagEnabled,
    tagMode = tagMode,
    tagTargetRef = tagTargetRef,
    tagMessagePrefix = tagMessagePrefix,
    customCommands = customCommands.map(WorkspaceCommandConfig::toEditorDraft),
)

private val LocalEditorEnabled = staticCompositionLocalOf { true }
private val LocalEditorIssue = staticCompositionLocalOf<ServiceEditorError?> { null }
private val LocalEditorItem = staticCompositionLocalOf<String?> { null }
private val LocalEditorPulse = staticCompositionLocalOf { 0 }
private val LocalEditorCompact = staticCompositionLocalOf { false }

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun OutlinedTextField(
    value: String, onValueChange: (String) -> Unit, modifier: Modifier = Modifier,
    label: @Composable (() -> Unit)? = null, placeholder: @Composable (() -> Unit)? = null,
    supportingText: @Composable (() -> Unit)? = null, singleLine: Boolean = false,
    minLines: Int = 1, isError: Boolean = false, errorField: String = "",
) {
    val issue = LocalEditorIssue.current?.takeIf { it.itemId == LocalEditorItem.current && it.field == errorField }
    val pulse = LocalEditorPulse.current
    val bring = remember { BringIntoViewRequester() }
    val focus = remember { FocusRequester() }
    LaunchedEffect(issue, pulse) {
        if (issue != null) { withFrameNanos { }; bring.bringIntoView(); focus.requestFocus() }
    }
    MaterialTextField(value, onValueChange, modifier.bringIntoViewRequester(bring).focusRequester(focus),
        label = label, placeholder = placeholder, supportingText = issue?.let { message -> { Text(message.message) } } ?: supportingText,
        singleLine = singleLine, minLines = minLines, isError = isError || issue != null, enabled = LocalEditorEnabled.current)
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun EditorBranchPicker(value: String, onChange: (String) -> Unit, label: String, repositoryId: String,
    controller: DesktopApplication, modifier: Modifier = Modifier, remote: String? = null, errorField: String = "masterBranch") {
    val issue = LocalEditorIssue.current?.takeIf { it.itemId == LocalEditorItem.current && it.field == errorField }
    val pulse = LocalEditorPulse.current
    val bring = remember { BringIntoViewRequester() }
    val focus = remember { FocusRequester() }
    LaunchedEffect(issue, pulse) { if (issue != null) { withFrameNanos { }; bring.bringIntoView(); focus.requestFocus() } }
    Column(modifier.bringIntoViewRequester(bring), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        RemoteBranchPicker(value, onChange, label, repositoryId, controller, Modifier.fillMaxWidth(),
            remote = remote, enabled = LocalEditorEnabled.current, isError = issue != null, supportingText = issue?.message, fieldModifier = Modifier.focusRequester(focus))
    }
}

@Composable
internal fun ServiceEditorDialog(controller: DesktopApplication, service: GroupServiceConfig, onDismiss: () -> Unit,
    onSave: (GroupServiceConfig, (Throwable) -> Unit) -> Boolean) {
    val window = LocalWindowInfo.current.containerSize
    val density = LocalDensity.current
    val bounds = serviceEditorBounds(with(density) { window.width.toDp().value }, with(density) { window.height.toDp().value })
    ServiceEditorContent(controller, service, bounds, onDismiss, onSave) { close, saving, content ->
        Dialog(onDismissRequest = close, properties = DialogProperties(usePlatformDefaultWidth = false,
            dismissOnBackPress = !saving, dismissOnClickOutside = !saving), content = content)
    }
}

/** 状态与窗口宿主分开，实际分类、草稿和忙态可在同一渲染环境验证。 */
@Composable
internal fun ServiceEditorContent(controller: DesktopApplication, service: GroupServiceConfig, bounds: ServiceEditorBounds,
    onDismiss: () -> Unit, onSave: (GroupServiceConfig, (Throwable) -> Unit) -> Boolean,
    host: @Composable (() -> Unit, Boolean, @Composable () -> Unit) -> Unit) {
    val json = remember { Json { prettyPrint = true; encodeDefaults = true } }
    var name by remember { mutableStateOf(service.displayName) }
    var enabled by remember { mutableStateOf(service.enabled) }
    var genbuProbeEnabled by remember { mutableStateOf(service.genbuProbeEnabled) }
    var genbuServiceName by remember { mutableStateOf(service.genbuServiceName) }
    var masterBranch by remember { mutableStateOf(service.masterBranch) }
    var testTagBaselineRef by remember { mutableStateOf(service.testTagBaselineRef) }
    var developmentTool by remember { mutableStateOf(service.developmentTool) }
    var commitMessageTemplate by remember { mutableStateOf(service.commitMessageTemplate) }
    val initialModules = remember(service) { service.modules.map(ServiceModuleConfig::toEditorDraft) }
    var modules by remember { mutableStateOf(initialModules) }
    val initialBootstrap = remember(service) { service.bootstrap.toEditorSession(json) }
    var bootstrap by remember { mutableStateOf(initialBootstrap) }
    var issue by remember { mutableStateOf<ServiceEditorError?>(null) }
    var pulse by remember { mutableStateOf(0) }
    var saveFailure by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }
    var examplePage by remember { mutableStateOf<BootstrapEditorPage?>(null) }
    var confirmDiscard by remember { mutableStateOf(false) }
    var selectedSection by remember { mutableStateOf("basic") }
    var expandedModules by remember { mutableStateOf(setOfNotNull(modules.firstOrNull()?.id)) }
    val focusManager = LocalFocusManager.current

    val dirty = name != service.displayName || enabled != service.enabled || genbuProbeEnabled != service.genbuProbeEnabled ||
        genbuServiceName != service.genbuServiceName || masterBranch != service.masterBranch || testTagBaselineRef != service.testTagBaselineRef ||
        developmentTool != service.developmentTool || commitMessageTemplate != service.commitMessageTemplate || modules != initialModules ||
        bootstrap.draft != initialBootstrap.draft || bootstrap.copies.text != initialBootstrap.copies.text || bootstrap.commands.text != initialBootstrap.commands.text
    val requestDismiss = { if (!saving) { if (dirty) confirmDiscard = true else onDismiss() } }
    fun report(error: Throwable) {
        issue = (error as? ServiceEditorValidationException)?.issue ?: ServiceEditorError(selectedSection, field = "general", message = error.message ?: "请检查配置")
        selectedSection = issue!!.section
        val id = issue!!.itemId
        modules.firstOrNull { it.id == id || it.customCommands.any { command -> command.id == id } }?.let { expandedModules = expandedModules + it.id }
        pulse++
    }
    fun switchMode(page: BootstrapEditorPage, mode: String) {
        runCatching { bootstrap.switchMode(json, page, mode) }.onSuccess {
            bootstrap = it; issue = null
        }.onFailure(::report)
    }
    fun save() {
        if (saving) return
        issue = null; saveFailure = null
        runCatching {
            checkEditor("basic", field = "name") { require(name.isNotBlank()) { "请填写服务名称" } }
            checkEditor("git", field = "masterBranch") { RemoteBranchRef.parse(masterBranch.trim()) }
            checkEditor("git", field = "testTagBaselineRef") { RemoteBranchRef.parse(testTagBaselineRef.trim()) }
            checkEditor("genbu", field = "genbuName") { require(!genbuProbeEnabled || genbuServiceName.isNotBlank()) { "请填写 Genbu 服务名" } }
            val normalizedModules = validateModuleDrafts(modules)
            val normalizedBootstrap = bootstrap.toConfig(json)
            service.copy(displayName = name.trim(), enabled = enabled, genbuProbeEnabled = genbuProbeEnabled,
                genbuServiceName = genbuServiceName.trim(), masterBranch = masterBranch.trim(), testTagBaselineRef = testTagBaselineRef.trim(),
                developmentTool = developmentTool, commitMessageTemplate = commitMessageTemplate.trim(), modules = normalizedModules, bootstrap = normalizedBootstrap)
        }.onSuccess { draft ->
            focusManager.clearFocus()
            saving = true
            val failed: (Throwable) -> Unit = { error -> saving = false; saveFailure = "保存失败：" + (error.message ?: "请重试") }
            val accepted = runCatching { onSave(draft, failed) }.onFailure(failed).getOrDefault(false)
            if (!accepted) saving = false
        }.onFailure(::report)
    }
    val sections = listOf("basic" to "基本信息", "tools" to "开发工具", "git" to "Git", "genbu" to "Genbu", "modules" to "工作区模块", "copies" to "复制规则", "commands" to "初始化命令")
    host(requestDismiss, saving) {
        Surface(Modifier.width(bounds.width.dp).height(bounds.height.dp), shape = RoundedCornerShape(22.dp), border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
            Column(Modifier.fillMaxSize()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = RoundedCornerShape(11.dp)) { Icon(Icons.Outlined.Settings, null, Modifier.padding(10.dp), tint = MaterialTheme.colorScheme.primary) }
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) { Text("服务配置", style = MaterialTheme.typography.titleLarge); Text(service.displayName, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    if (!bounds.compact) MetaPill(modules.count { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }.toString() + " Worktree · " + modules.count { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE } + " 克隆")
                }
                HorizontalDivider()
                CompositionLocalProvider(LocalEditorEnabled provides !saving, LocalEditorIssue provides issue?.takeIf { it.section == selectedSection }, LocalEditorPulse provides pulse, LocalEditorCompact provides bounds.compact) {
                    if (bounds.compact) FlowRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        sections.forEach { (id, title) -> FilterChip(selectedSection == id, { selectedSection = id }, label = { Text(title) }, enabled = !saving) }
                    }
                    Row(Modifier.weight(1f).padding(20.dp), horizontalArrangement = Arrangement.spacedBy(18.dp)) {
                        if (!bounds.compact) Surface(Modifier.width(160.dp).fillMaxSize(), color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = .38f), shape = RoundedCornerShape(14.dp)) {
                            Column(Modifier.padding(8.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                                sections.forEach { (id, title) -> Surface(Modifier.fillMaxWidth().clickable(enabled = !saving) { selectedSection = id },
                                    color = if (selectedSection == id) MaterialTheme.colorScheme.primaryContainer else Color.Transparent, shape = RoundedCornerShape(9.dp)) {
                                    Text(title, Modifier.padding(horizontal = 12.dp, vertical = 13.dp), fontWeight = if (selectedSection == id) FontWeight.SemiBold else FontWeight.Normal,
                                        color = if (selectedSection == id) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurface)
                                } }
                            }
                        }
                        key(selectedSection) {
                            Column(Modifier.weight(1f).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                                when (selectedSection) {
                                    "basic" -> ServiceBasicSection(name, { name = it }, enabled, { enabled = it })
                                    "genbu" -> ServiceGenbuSection(genbuProbeEnabled, { genbuProbeEnabled = it }, genbuServiceName, { genbuServiceName = it })
                                    "tools" -> ServiceToolsSection(developmentTool, { developmentTool = it })
                                    "git" -> ServiceGitSection(controller, service.repositoryId, commitMessageTemplate, { commitMessageTemplate = it }, masterBranch, { masterBranch = it }, testTagBaselineRef, { testTagBaselineRef = it })
                                    "modules" -> ServiceModulesSection(modules, { modules = it }, service.repositoryId, masterBranch, testTagBaselineRef, controller,
                                        expandedModules, { expandedModules = it })
                                    "copies", "commands" -> {
                                        val page = if (selectedSection == "copies") BootstrapEditorPage.COPIES else BootstrapEditorPage.COMMANDS
                                        val editor = bootstrap.editor(page)
                                        ServiceBootstrapSection(page, controller, controller.config.repositories.firstOrNull { it.id == service.repositoryId }?.rootPath,
                                            editor.mode, bootstrap.draft, editor.text, { switchMode(page, "form") }, { switchMode(page, "json") }, { examplePage = page },
                                            { bootstrap = bootstrap.copy(draft = it) },
                                            { bootstrap = bootstrap.withEditor(page, editor.copy(text = it)); if (issue?.section == page.section && issue?.field == "json") issue = null },
                                            { message -> issue = message?.let { ServiceEditorError(page.section, field = "general", message = it) } })
                                    }
                                }
                                issue?.takeIf { it.section == selectedSection && it.field == "general" }?.let { Text(it.message, color = MaterialTheme.colorScheme.error) }
                            }
                        }
                    }
                }
                HorizontalDivider()
                saveFailure?.let { Text(it, Modifier.padding(horizontal = 22.dp, vertical = 8.dp), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                Row(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 14.dp), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
                    Text("修改仅影响后续任务", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    TextButton(onClick = requestDismiss, enabled = !saving) { Text("取消") }
                    Spacer(Modifier.width(8.dp))
                    Button(onClick = { save() }, enabled = !saving) {
                        if (saving) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp) else Icon(Icons.Outlined.Save, null, Modifier.size(17.dp))
                        Spacer(Modifier.width(5.dp)); Text(if (saving) "正在保存" else "保存配置")
                    }
                }
            }
        }
    }
    examplePage?.let { page ->
        val preset = BootstrapPresets.example()
        val example = if (page == BootstrapEditorPage.COPIES) json.encodeToString(preset.copyRules) else json.encodeToString(preset.commands)
        BootstrapExampleDialog(page.title, example, bounds, controller) { examplePage = null }
    }
    if (confirmDiscard) DiscardChangesDialog("放弃服务配置修改？", "尚未保存的服务配置将丢失。", { confirmDiscard = false }, onDismiss)
}

@Composable
private fun BootstrapExampleDialog(title: String, example: String, bounds: ServiceEditorBounds, controller: DesktopApplication, onDismiss: () -> Unit) {
    var copied by remember { mutableStateOf(false) }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.width(bounds.width.coerceAtMost(820f).dp).heightIn(max = bounds.maxHeight.coerceAtMost(620f).dp), shape = RoundedCornerShape(22.dp)) {
            Column {
                Row(Modifier.fillMaxWidth().padding(20.dp), verticalAlignment = Alignment.CenterVertically) { Text(title + " JSON 示例", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge); if (copied) Text("已复制", color = SuccessGreen) }
                HorizontalDivider()
                SelectionContainer(Modifier.weight(1f, fill = false).padding(18.dp)) { Text(example, Modifier.verticalScroll(rememberScrollState()), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                HorizontalDivider()
                Row(Modifier.fillMaxWidth().padding(16.dp), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onDismiss, enabled = LocalEditorEnabled.current) { Text("关闭") }
                    Button(onClick = { controller.copyText(example, title + "示例已复制"); copied = true }) { Text("复制") }
                }
            }
        }
    }
}

@Composable
private fun ServiceBasicSection(name: String, onNameChange: (String) -> Unit, enabled: Boolean, onEnabledChange: (Boolean) -> Unit) {
    SectionHeader("服务信息")
    OutlinedTextField(name, onNameChange, Modifier.fillMaxWidth(), label = { Text("展示名称") }, singleLine = true, errorField = "name")
    Row(verticalAlignment = Alignment.CenterVertically) { Text("启用服务", Modifier.weight(1f)); Switch(enabled, onEnabledChange, enabled = LocalEditorEnabled.current) }
    Text("关闭后，新任务将无法选择此服务。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
private fun ServiceGenbuSection(enabled: Boolean, onEnabledChange: (Boolean) -> Unit, name: String, onNameChange: (String) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) { SectionHeader("Genbu 探测"); Spacer(Modifier.weight(1f)); Switch(enabled, onEnabledChange, enabled = LocalEditorEnabled.current) }
    Text("开启后查询此服务的 Tag 构建与发布状态。新服务默认关闭。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    if (enabled) OutlinedTextField(name, onNameChange, Modifier.fillMaxWidth(), label = { Text("Genbu 服务名") }, singleLine = true, errorField = "genbuName")
}

@Composable
private fun ServiceToolsSection(developmentTool: DevelopmentToolType, onDevelopmentToolChange: (DevelopmentToolType) -> Unit) {
    SectionHeader("默认开发工具")
    var expanded by remember { mutableStateOf(false) }
    Box {
        OutlinedButton(onClick = { expanded = true }, enabled = LocalEditorEnabled.current, modifier = Modifier.fillMaxWidth()) { Text(developmentTool.displayName, Modifier.weight(1f)); Text("▾") }
        SilverWingDropdownMenu(expanded, onDismissRequest = { expanded = false }) {
            DevelopmentToolType.entries.forEach { tool -> DropdownMenuItem(text = { Text(tool.displayName) }, onClick = { onDevelopmentToolChange(tool); expanded = false }) }
        }
    }
}

@Composable
private fun ServiceGitSection(controller: DesktopApplication, repositoryId: String,
    commitMessageTemplate: String, onCommitMessageTemplateChange: (String) -> Unit,
    masterBranch: String, onMasterBranchChange: (String) -> Unit, testTagBaselineRef: String, onTestTagBaselineRefChange: (String) -> Unit) {
    SectionHeader("分支默认值")
    Text("工作区模块默认继承这些分支，也可在模块内单独覆盖。展开字段后可切换远程、搜索或手动填写。",
        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    EditorBranchPicker(masterBranch, onMasterBranchChange, "主分支", repositoryId, controller, Modifier.fillMaxWidth())
    EditorBranchPicker(testTagBaselineRef, onTestTagBaselineRefChange, "测试 Tag 默认目标", repositoryId, controller,
        Modifier.fillMaxWidth(), errorField = "testTagBaselineRef")
    HorizontalDivider()
    val repository = controller.config.repositories.firstOrNull { it.id == repositoryId }
    // 只有进入 Git 页才读取地址。多个服务仍通过仓库 ID 与路径共享运行时缓存。
    LaunchedEffect(repository?.id, repository?.rootPath) {
        repository?.let { controller.loadRepositoryAddresses(it.id, it.rootPath) }
    }
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text("远程仓库", Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
        OutlinedButton(onClick = { repository?.let { controller.loadRepositoryAddresses(it.id, it.rootPath, force = true) } },
            enabled = repository != null && LocalEditorEnabled.current) { Text("刷新远程") }
    }
    if (repository == null) Text("仓库配置缺失，无法读取远程地址。", color = MaterialTheme.colorScheme.error)
    else ServiceRemoteAddresses(controller.repositoryAddressState(repository.id, repository.rootPath),
        { controller.loadRepositoryAddresses(repository.id, repository.rootPath, force = true) },
        controller::openUrl, { controller.copyText(it, "远程地址已复制") }, enabled = LocalEditorEnabled.current,
        failure = controller.repositoryAddressFailure(repository.id, repository.rootPath))
    HorizontalDivider()
    SectionHeader("提交信息模板")
    OutlinedTextField(commitMessageTemplate, onCommitMessageTemplateChange, Modifier.fillMaxWidth(), label = { Text("默认提交信息模板") }, singleLine = true)
    Text("{num} 会替换为任务编号；留空时沿用现有默认规则。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
private fun ModuleCustomCommandsSection(commands: List<WorkspaceCommandEditorDraft>, onCommandsChange: (List<WorkspaceCommandEditorDraft>) -> Unit) {
    if (commands.isEmpty()) Text("尚未配置快捷命令。可以添加 Maven clean、install、deploy 等命令。", color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodySmall)
    commands.forEachIndexed { index, command -> key(command.id) {
        CompositionLocalProvider(LocalEditorItem provides command.id) {
            fun update(changed: WorkspaceCommandEditorDraft) = onCommandsChange(commands.replaceAt(index, changed))
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(9.dp)) {
                    EditorPair({ modifier -> OutlinedTextField(command.name, { update(command.copy(name = it)) }, modifier, label = { Text("命令名称") }, singleLine = true, errorField = "name") },
                        { modifier -> OutlinedTextField(command.executable, { update(command.copy(executable = it)) }, modifier, label = { Text("可执行程序") }, singleLine = true, errorField = "executable") })
                    Text("图标", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        workspaceCommandIconKeys.forEach { icon -> FilterChip(command.iconKey == icon, { update(command.copy(iconKey = icon)) },
                            leadingIcon = { Icon(workspaceCommandIcon(icon), null, Modifier.size(17.dp)) }, label = { Text(workspaceCommandIconLabel(icon)) }, enabled = LocalEditorEnabled.current) }
                    }
                    OutlinedTextField(command.argumentsText, { update(command.copy(argumentsText = it, arguments = it.lines().filter(String::isNotBlank))) },
                        Modifier.fillMaxWidth(), label = { Text("参数（每行一个）") }, supportingText = { Text("直接传参，不经过 Shell") }, minLines = 2)
                    EditorPair({ modifier -> OutlinedTextField(command.workingDirectory, { update(command.copy(workingDirectory = it)) }, modifier,
                        label = { Text("工作目录（工作区内相对路径）") }, singleLine = true, errorField = "workingDirectory") },
                        { modifier -> OutlinedTextField(command.timeoutSeconds, { update(command.copy(timeoutSeconds = it)) }, modifier, label = { Text("超时（秒）") }, singleLine = true, errorField = "timeout") })
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Switch(command.enabled, { update(command.copy(enabled = it)) }, enabled = LocalEditorEnabled.current); Text("启用")
                        Spacer(Modifier.weight(1f))
                        ActionIconButton("删除快捷命令", { onCommandsChange(commands.filter { it.id != command.id }) }, enabled = LocalEditorEnabled.current) { Icon(Icons.Outlined.Delete, "删除快捷命令") }
                    }
                    SelectionContainer { Text("预览：" + (listOf(command.executable) + command.argumentsText.lines().filter(String::isNotBlank)).joinToString(" "), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                }
            }
        }
    } }
    OutlinedButton(onClick = { onCommandsChange(commands + WorkspaceCommandEditorDraft("command-" + UUID.randomUUID(), "Maven命令", "build", "mvn", emptyList(), ".", "1800", true)) }, enabled = LocalEditorEnabled.current) {
        Icon(Icons.Outlined.Add, null); Spacer(Modifier.width(4.dp)); Text("添加快捷命令")
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

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ServiceModulesSection(modules: List<ServiceModuleEditorDraft>, onModulesChange: (List<ServiceModuleEditorDraft>) -> Unit,
    repositoryId: String, masterBranch: String, testTagBaselineRef: String, controller: DesktopApplication,
    expanded: Set<String>, onExpanded: (Set<String>) -> Unit) {
    var addedId by remember { mutableStateOf<String?>(null) }
    SectionHeader("工作区模块 · " + modules.size)
    Text("每个模块创建独立工作区。展开模块可设置分支、测试 Tag 和快捷命令。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    modules.forEachIndexed { index, module -> key(module.id) {
        val bring = remember { BringIntoViewRequester() }
        LaunchedEffect(addedId) { if (addedId == module.id) { withFrameNanos { }; bring.bringIntoView(); addedId = null } }
        Column(Modifier.fillMaxWidth().bringIntoViewRequester(bring)) {
            CompositionLocalProvider(LocalEditorItem provides module.id) {
                ModuleEditor(module, repositoryId, masterBranch, testTagBaselineRef, controller, modules.size > 1,
                    { changed -> onModulesChange(modules.replaceAt(index, changed)) },
                    { onModulesChange(modules.filter { it.id != module.id }); onExpanded(expanded - module.id) },
                    module.id in expanded, { onExpanded(if (module.id in expanded) expanded - module.id else expanded + module.id) })
            }
        }
    } }
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        WorkspaceStrategy.entries.forEach { strategy -> OutlinedButton(onClick = {
            val module = ServiceModuleEditorDraft(id = "module-" + UUID.randomUUID(), strategy = strategy, tagEnabled = strategy == WorkspaceStrategy.STANDARD_WORKTREE)
            addedId = module.id; onExpanded(expanded + module.id); onModulesChange(modules + module)
        }, enabled = LocalEditorEnabled.current) { Icon(Icons.Outlined.Add, null); Text(if (strategy == WorkspaceStrategy.STANDARD_WORKTREE) "添加 Worktree 模块" else "添加克隆模块") } }
    }
}

@Composable
private fun ServiceBootstrapSection(page: BootstrapEditorPage, controller: DesktopApplication, repositoryRoot: String?, mode: String, draft: BootstrapEditorDraft, text: String,
    onSwitchToForm: () -> Unit, onSwitchToJson: () -> Unit, onShowExample: () -> Unit, onChange: (BootstrapEditorDraft) -> Unit,
    onTextChange: (String) -> Unit, onError: (String?) -> Unit) {
    FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        FilterChip(mode == "form", onSwitchToForm, label = { Text("表单配置") }, enabled = LocalEditorEnabled.current)
        FilterChip(mode == "json", onSwitchToJson, label = { Text("高级 JSON") }, enabled = LocalEditorEnabled.current)
        TextButton(onClick = onShowExample, enabled = LocalEditorEnabled.current) { Text("查看示例") }
    }
    Text(if (page == BootstrapEditorPage.COPIES) "创建新工作区时先复制仓库文件。没有复制需求时可留空；JSON 仅包含复制规则数组。" else "复制完成后按顺序执行初始化命令。没有初始化需求时可留空；JSON 仅包含命令数组。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    if (mode == "json") OutlinedTextField(text, onTextChange, Modifier.fillMaxWidth(), label = { Text(page.title + " JSON 数组") }, minLines = 8, errorField = "json")
    else if (page == BootstrapEditorPage.COPIES) BootstrapCopiesEditor(draft, repositoryRoot, controller, onChange, onError)
    else BootstrapCommandsEditor(draft, onChange)
}

@Composable
private fun BootstrapGroupHeader(title: String, count: Int, description: String, addLabel: String, onAdd: () -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(title + " · " + count, Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
        OutlinedButton(onClick = onAdd, enabled = LocalEditorEnabled.current) { Icon(Icons.Outlined.Add, null, Modifier.size(16.dp)); Text(addLabel) }
    }
    Text(description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
private fun EditorPair(first: @Composable (Modifier) -> Unit, second: @Composable (Modifier) -> Unit) {
    if (LocalEditorCompact.current) Column(verticalArrangement = Arrangement.spacedBy(8.dp)) { first(Modifier.fillMaxWidth()); second(Modifier.fillMaxWidth()) }
    else Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) { first(Modifier.weight(1f)); second(Modifier.weight(1f)) }
}

@Composable
private fun BootstrapCopiesEditor(draft: BootstrapEditorDraft, repositoryRoot: String?, controller: DesktopApplication,
    onChange: (BootstrapEditorDraft) -> Unit, onError: (String?) -> Unit) {
    BootstrapGroupHeader("复制规则", draft.copies.size, "将仓库内的相对路径复制到新工作区，可选择是否覆盖已有文件。", "添加规则") {
        onChange(draft.copy(copies = draft.copies + BootstrapCopyDraft()))
    }
    draft.copies.forEachIndexed { index, entry -> key(entry.key) {
        CompositionLocalProvider(LocalEditorItem provides entry.key) {
            val rule = entry.rule
            fun update(changed: BootstrapCopyRule) = onChange(draft.copy(copies = draft.copies.replaceAt(index, entry.copy(rule = changed))))
            fun choose(directory: Boolean) {
                if (repositoryRoot.isNullOrBlank()) { onError("请先配置原始仓库目录"); return }
                val selected: (String) -> Unit = { path -> runCatching { repositoryRelativePath(repositoryRoot, path) }
                    .onSuccess { update(rule.withSelectedSource(it)); onError(null) }.onFailure { onError(it.message) } }
                if (directory) controller.chooseDirectory(repositoryRoot, selected) else controller.chooseFile(repositoryRoot, selected)
            }
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    EditorPair({ modifier -> OutlinedTextField(rule.source, { update(rule.copy(source = it)) }, modifier, label = { Text("仓库内源路径") }, singleLine = true, errorField = "source") },
                        { modifier -> OutlinedTextField(rule.target, { update(rule.copy(target = it)) }, modifier, label = { Text("工作区目标路径") }, singleLine = true, errorField = "target") })
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { choose(false) }, enabled = LocalEditorEnabled.current) { Text("选择文件") }
                        OutlinedButton(onClick = { choose(true) }, enabled = LocalEditorEnabled.current) { Text("选择目录") }
                        Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(rule.overwrite, { update(rule.copy(overwrite = it)) }, enabled = LocalEditorEnabled.current); Text("允许覆盖") }
                        ActionIconButton("删除复制规则", { onChange(draft.copy(copies = draft.copies.filter { it.key != entry.key })) }, enabled = LocalEditorEnabled.current) { Icon(Icons.Outlined.Delete, "删除复制规则") }
                    }
                }
            }
        }
    } }
}

@Composable
private fun BootstrapCommandsEditor(draft: BootstrapEditorDraft, onChange: (BootstrapEditorDraft) -> Unit) {
    BootstrapGroupHeader("初始化命令", draft.commands.size, "按列表顺序执行。参数每行一个，工作目录默认是新工作区根目录。", "添加命令") {
        onChange(draft.copy(commands = draft.commands + BootstrapCommandDraft()))
    }
    draft.commands.forEachIndexed { index, entry -> key(entry.key) {
        CompositionLocalProvider(LocalEditorItem provides entry.key) {
            val command = entry.command
            fun update(changed: BootstrapCommandDraft) = onChange(draft.copy(commands = draft.commands.replaceAt(index, changed)))
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    EditorPair({ modifier -> OutlinedTextField(command.name, { update(entry.copy(command = command.copy(name = it))) }, modifier, label = { Text("名称") }, singleLine = true, errorField = "name") },
                        { modifier -> OutlinedTextField(command.executable, { update(entry.copy(command = command.copy(executable = it))) }, modifier, label = { Text("可执行程序") }, singleLine = true, errorField = "executable") })
                    OutlinedTextField(entry.argumentsText, { update(entry.copy(argumentsText = it)) }, Modifier.fillMaxWidth(), label = { Text("参数（每行一个）") }, minLines = 2)
                    EditorPair({ modifier -> OutlinedTextField(command.workingDirectory, { update(entry.copy(command = command.copy(workingDirectory = it))) }, modifier, label = { Text("工作目录") }, singleLine = true, errorField = "workingDirectory") },
                        { modifier -> OutlinedTextField(entry.timeoutText, { update(entry.copy(timeoutText = it)) }, modifier, label = { Text("超时（秒）") }, singleLine = true, errorField = "timeout") })
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(command.enabled, { update(entry.copy(command = command.copy(enabled = it))) }, enabled = LocalEditorEnabled.current); Text("启用")
                        Spacer(Modifier.weight(1f))
                        ActionIconButton("删除命令", { onChange(draft.copy(commands = draft.commands.filter { it.key != entry.key })) }, enabled = LocalEditorEnabled.current) { Icon(Icons.Outlined.Delete, "删除命令") }
                    }
                    SelectionContainer { Text("预览：" + (listOf(command.executable) + entry.argumentsText.lines().filter(String::isNotBlank)).joinToString(" "), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                }
            }
        }
    } }
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
private fun ModuleEditor(module: ServiceModuleEditorDraft, repositoryId: String, serviceMasterBranch: String, testTagBaselineRef: String,
    controller: DesktopApplication, canDelete: Boolean, onChange: (ServiceModuleEditorDraft) -> Unit, onDelete: () -> Unit,
    expanded: Boolean, onToggle: () -> Unit) {
    val issue = LocalEditorIssue.current
    val showTagFields = module.tagEnabled || (issue?.itemId == module.id && issue.field == "tagTarget")
    var tagExpanded by remember { mutableStateOf(false) }
    var commandsExpanded by remember { mutableStateOf(false) }
    LaunchedEffect(issue, LocalEditorPulse.current) {
        if (issue?.itemId == module.id && issue.field == "tagTarget") tagExpanded = true
        if (issue != null && module.customCommands.any { it.id == issue.itemId }) commandsExpanded = true
    }
    OutlinedCard(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().clickable(enabled = LocalEditorEnabled.current, onClick = onToggle).padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(module.name.ifBlank { StandardWorktreeModuleNaming.DEFAULT_NAME } + " · " + module.strategy.displayName, style = MaterialTheme.typography.titleSmall)
                Text("主分支：" + (module.masterBranch ?: serviceMasterBranch), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Text(if (expanded) "收起 ▴" else "展开 ▾", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelMedium)
        }
        if (expanded) Column(Modifier.padding(start = 12.dp, end = 12.dp, bottom = 12.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) { WorkspaceStrategy.entries.forEach { strategy ->
                FilterChip(module.strategy == strategy, { onChange(module.copy(strategy = strategy)) }, label = { Text(strategy.displayName) }, enabled = LocalEditorEnabled.current)
            } }
            Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(module.name, { onChange(module.copy(name = it)) }, Modifier.weight(1f), label = { Text("模块名") }, singleLine = true,
                    supportingText = { Text("仅允许英文字母、数字、-、_、/") }, errorField = "name")
                ActionIconButton("删除模块", onDelete, enabled = canDelete && LocalEditorEnabled.current) { Icon(Icons.Outlined.Delete, "删除模块") }
            }
            Row(verticalAlignment = Alignment.CenterVertically) { Text("自定义主分支", Modifier.weight(1f)); Switch(module.masterBranch != null,
                { onChange(module.copy(masterBranch = if (it) serviceMasterBranch else null)) }, enabled = LocalEditorEnabled.current) }
            if (module.masterBranch == null) Text("继承服务主分支：" + serviceMasterBranch, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodySmall)
            else {
                val selectedRemote = module.masterBranch.substringBefore('/')
                RemoteNamePicker(selectedRemote, repositoryId, controller,
                    { remote -> onChange(module.copy(masterBranch = remote + "/" + module.masterBranch.substringAfter('/', ""))) },
                    Modifier.fillMaxWidth(), enabled = LocalEditorEnabled.current)
                EditorBranchPicker(module.masterBranch, { onChange(module.copy(masterBranch = it)) }, "主分支", repositoryId, controller, Modifier.fillMaxWidth(), remote = selectedRemote)
            }
            if (controller.config.tagEnabled) {
                HorizontalDivider()
                TextButton(onClick = { tagExpanded = !tagExpanded }, enabled = LocalEditorEnabled.current) { Text("测试 Tag · " + (if (module.tagEnabled) "已启用" else "已关闭") + (if (tagExpanded) " ▴" else " ▾")) }
                if (tagExpanded) {
                    Row(verticalAlignment = Alignment.CenterVertically) { Text("启用测试 Tag", Modifier.weight(1f)); Switch(module.tagEnabled, { onChange(module.copy(tagEnabled = it)) }, enabled = LocalEditorEnabled.current) }
                    if (module.tagEnabled) TagModeSelector(module.tagMode) { onChange(module.copy(tagMode = it)) }
                    if (showTagFields && module.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
                        Row(verticalAlignment = Alignment.CenterVertically) { Text("自定义测试 Tag 目标", Modifier.weight(1f)); Switch(module.tagTargetRef != null, { onChange(module.copy(tagTargetRef = if (it) testTagBaselineRef else null)) }, enabled = LocalEditorEnabled.current) }
                        if (module.tagTargetRef == null) Text("继承服务测试 Tag 默认目标：" + testTagBaselineRef, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodySmall)
                    }
                    if (showTagFields) TagConfigurationFields(module.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH && module.tagTargetRef != null,
                        module.tagTargetRef.orEmpty(), { onChange(module.copy(tagTargetRef = it)) }, module.tagMessagePrefix, { onChange(module.copy(tagMessagePrefix = it)) }, repositoryId, controller)
                }
            }
            HorizontalDivider()
            TextButton(onClick = { commandsExpanded = !commandsExpanded }, enabled = LocalEditorEnabled.current) { Text("快捷命令 · " + module.customCommands.size + (if (commandsExpanded) " ▴" else " ▾")) }
            if (commandsExpanded) ModuleCustomCommandsSection(module.customCommands) { onChange(module.copy(customCommands = it)) }
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
         enabled = LocalEditorEnabled.current)
        FilterChip(
            selected = mode == TagBuildMode.CURRENT_BRANCH,
            onClick = { onChange(TagBuildMode.CURRENT_BRANCH) },
            label = { Text("当前分支直接构建测试Tag") },
         enabled = LocalEditorEnabled.current)
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
    if (targetVisible) {
        EditorPair({ modifier -> EditorBranchPicker(targetRef, onTargetRefChange, "测试Tag目标分支", repositoryId, controller, modifier, errorField = "tagTarget") },
            { modifier -> OutlinedTextField(messagePrefix, onMessagePrefixChange, modifier, label = { Text("Tag信息前缀") }, singleLine = true) })
    } else OutlinedTextField(messagePrefix, onMessagePrefixChange, Modifier.fillMaxWidth(), label = { Text("Tag信息前缀") }, singleLine = true)
}
