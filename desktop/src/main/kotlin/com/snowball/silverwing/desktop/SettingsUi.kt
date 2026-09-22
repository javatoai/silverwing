package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.FlowRowScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.automirrored.outlined.Article
import androidx.compose.material.icons.automirrored.outlined.Logout
import androidx.compose.material.icons.automirrored.outlined.Subject
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.AccountTree
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Extension
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Group
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.KeyboardArrowUp
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.MoreHoriz
import androidx.compose.material.icons.outlined.Palette
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Sell
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.Button
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.ApplicationEventClipboard
import com.snowball.silverwing.core.MeegleCommandSource
import com.snowball.silverwing.core.CodexCommandSource
import com.snowball.silverwing.core.GitCommandSource
import com.snowball.silverwing.core.GenbuCommandSource
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.DevelopmentToolConfig
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.GroupConfig
import com.snowball.silverwing.core.GitConfigValue
import com.snowball.silverwing.core.LocalGitEnvironmentSnapshot
import com.snowball.silverwing.core.CommandVersionStatus
import com.snowball.silverwing.core.MeegleCliStatus
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.LarkCliStatus
import com.snowball.silverwing.core.LarkAuthenticationState
import com.snowball.silverwing.core.LarkCommandSource
import com.snowball.silverwing.core.LARK_BUSINESS_DOMAINS
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.RequirementAiNamingModel
import com.snowball.silverwing.core.TaskRootMigrationMode
import com.snowball.silverwing.core.TaskRootMigrationPhase
import com.snowball.silverwing.core.TaskRootMigrationProgress
import com.snowball.silverwing.core.MAX_TAG_HISTORY_GROUPS

@Composable
internal fun SettingsScreen(controller: DesktopApplication) {
    var taskRoot by remember(controller.config.taskRoot) { mutableStateOf(controller.config.taskRoot.orEmpty()) }
    var requirementMaterialsRoot by remember(controller.config.requirementMaterialsRoot) {
        mutableStateOf(controller.config.requirementMaterialsRoot.orEmpty())
    }
    var requirementMaterialsSubdirectory by remember(controller.config.requirementMaterialsSubdirectory) {
        mutableStateOf(controller.config.requirementMaterialsSubdirectory.orEmpty())
    }
    val developmentToolPaths = remember(controller.config.developmentTools) {
        mutableStateMapOf<DevelopmentToolType, String>().apply {
            DevelopmentToolType.entries.forEach { type ->
                put(type, controller.config.developmentTools.firstOrNull { it.type == type }?.path.orEmpty())
            }
        }
    }
    var defaultDevelopmentTool by remember(controller.config.defaultDevelopmentTool) {
        mutableStateOf(controller.config.defaultDevelopmentTool)
    }
    var tagHistoryMaxGroupsInput by remember(controller.config.tagHistoryMaxGroups) {
        mutableStateOf(controller.config.tagHistoryMaxGroups.toString())
    }
    var aiRequirementNamingModel by remember(controller.config.aiRequirementNamingModel) {
        mutableStateOf(controller.config.aiRequirementNamingModel)
    }
    var showTaskDetailGitActionGroup by remember(controller.config.showTaskDetailGitActionGroup) {
        mutableStateOf(controller.config.showTaskDetailGitActionGroup)
    }
    var showTaskDetailPathActionGroup by remember(controller.config.showTaskDetailPathActionGroup) {
        mutableStateOf(controller.config.showTaskDetailPathActionGroup)
    }
    var showWorkspaceGitActionGroup by remember(controller.config.showWorkspaceGitActionGroup) {
        mutableStateOf(controller.config.showWorkspaceGitActionGroup)
    }
    var showWorkspacePathActionGroup by remember(controller.config.showWorkspacePathActionGroup) {
        mutableStateOf(controller.config.showWorkspacePathActionGroup)
    }
    val taskAreaCopyIconPresentation = taskAreaCopyIconPresentationFor(controller.config)
    var showTaskAreaBranchCopyIcons by remember(
        controller.config.showTaskAreaCopyIcons,
        controller.config.showTaskAreaBranchCopyIcons,
    ) {
        mutableStateOf(taskAreaCopyIconPresentation.showBranchNameCopyIcons)
    }
    var showTaskAreaRequirementCopyIcons by remember(
        controller.config.showTaskAreaCopyIcons,
        controller.config.showTaskAreaRequirementCopyIcons,
    ) {
        mutableStateOf(taskAreaCopyIconPresentation.showRequirementCopyIcons)
    }
    var showTaskAreaProjectNameCopyIcons by remember(
        controller.config.showTaskAreaCopyIcons,
        controller.config.showTaskAreaProjectNameCopyIcons,
    ) {
        mutableStateOf(taskAreaCopyIconPresentation.showProjectNameCopyIcons)
    }
    var terminal by remember(controller.config.terminalExecutable) { mutableStateOf(controller.config.terminalExecutable.orEmpty()) }
    var blockedGitBranchInput by remember { mutableStateOf("") }
    var blockedGitWriteBranches by remember(controller.config.blockedGitWriteBranches) {
        mutableStateOf(controller.config.blockedGitWriteBranches)
    }
    var genbuPath by remember(controller.config.genbuExecutablePath) {
        mutableStateOf(controller.config.genbuExecutablePath.orEmpty())
    }
    var larkPath by remember(controller.config.larkExecutablePath) {
        mutableStateOf(controller.config.larkExecutablePath.orEmpty())
    }
    val meegleProjects = remember(controller.config.meegleProjects) {
        mutableStateMapOf<Int, MeegleProjectConfig>().apply {
            controller.config.meegleProjects.forEachIndexed { index, project -> put(index, project) }
        }
    }
    var meegleMenuExpanded by remember { mutableStateOf(false) }
    var larkDomainDialog by remember { mutableStateOf(false) }
    var backupMenuExpanded by remember { mutableStateOf(false) }
    var restoreBackup by remember { mutableStateOf<ConfigStore.Backup?>(null) }
    var importPreview by remember { mutableStateOf<ConfigStore.ImportPreview?>(null) }
    var newGroup by remember { mutableStateOf(false) }
    var renameGroup by remember { mutableStateOf<GroupConfig?>(null) }
    var deleteGroupTarget by remember { mutableStateOf<GroupConfig?>(null) }
    var newTemplate by remember { mutableStateOf(false) }
    var editTemplate by remember { mutableStateOf<AgentTaskTemplate?>(null) }
    var deleteTemplateTarget by remember { mutableStateOf<AgentTaskTemplate?>(null) }
    var agentGroupId by remember(controller.config.groups) { mutableStateOf(controller.config.groups.first().id) }
    var agentScope by remember { mutableStateOf("global") }
    var globalAgents by remember { mutableStateOf("") }
    var globalAgentsLoading by remember { mutableStateOf(true) }
    var globalAgentsError by remember { mutableStateOf<String?>(null) }
    val groupAgentDrafts = remember { mutableStateMapOf<String, String>() }
    val groupAgentLoading = remember {
        mutableStateMapOf<String, Boolean>().apply {
            controller.config.groups.forEach { group -> put(group.id, true) }
        }
    }
    val groupAgentLoaded = remember {
        mutableStateMapOf<String, Boolean>().apply {
            controller.config.groups.forEach { group -> put(group.id, false) }
        }
    }
    val groupAgentErrors = remember { mutableStateMapOf<String, String>() }
    val sections = remember { settingsNavigationSections() }
    val initialSection = remember { WindowPreferences.load().settingsSection }
    var selectedSection by remember { mutableStateOf(normalizeSettingsSection(initialSection, sections.map { it.key }.toSet())) }
    fun navigateToSection(key: String) {
        selectedSection = key
        WindowPreferences.saveSettingsSection(key)
    }
    fun saving(key: String) = controller.settingsSaveState(key) == SettingsSaveState.SAVING
    fun saveTagHistoryMaxGroups() {
        val value = tagHistoryMaxGroupsInput.toIntOrNull()
        if (value == null || value !in 1..MAX_TAG_HISTORY_GROUPS) {
            controller.showError(IllegalArgumentException("Tag组保留数量必须在 1 到 $MAX_TAG_HISTORY_GROUPS 之间"))
            tagHistoryMaxGroupsInput = controller.config.tagHistoryMaxGroups.toString()
            return
        }
        controller.updateTagHistoryMaxGroups(value) {
            tagHistoryMaxGroupsInput = controller.config.tagHistoryMaxGroups.toString()
        }
    }
    fun saveAiRequirementNamingModel() {
        val normalized = aiRequirementNamingModel.trim()
        aiRequirementNamingModel = normalized
        controller.setAiRequirementNamingModel(normalized) {
            aiRequirementNamingModel = controller.config.aiRequirementNamingModel
        }
    }
    fun currentToolConfigs(): List<DevelopmentToolConfig> = DevelopmentToolType.entries.mapNotNull { type ->
        developmentToolPaths[type]?.trim()?.takeIf(String::isNotBlank)?.let { DevelopmentToolConfig(type, it) }
    }
    fun saveDevelopmentTools() {
        controller.updateDevelopmentTools(
            currentToolConfigs(),
            defaultDevelopmentTool,
            terminal,
            controller.config.allowTemporaryDevelopmentToolSelection,
        ) {
            DevelopmentToolType.entries.forEach { type ->
                developmentToolPaths[type] = controller.config.developmentTools.firstOrNull { it.type == type }?.path.orEmpty()
            }
            defaultDevelopmentTool = controller.config.defaultDevelopmentTool
            terminal = controller.config.terminalExecutable.orEmpty()
        }
    }
    fun saveTaskAreaToolGroupVisibility(
        taskGit: Boolean = showTaskDetailGitActionGroup,
        taskPath: Boolean = showTaskDetailPathActionGroup,
        workspaceGit: Boolean = showWorkspaceGitActionGroup,
        workspacePath: Boolean = showWorkspacePathActionGroup,
        branchCopyIcons: Boolean = showTaskAreaBranchCopyIcons,
        requirementCopyIcons: Boolean = showTaskAreaRequirementCopyIcons,
        projectNameCopyIcons: Boolean = showTaskAreaProjectNameCopyIcons,
    ) {
        val previousTaskGit = showTaskDetailGitActionGroup
        val previousTaskPath = showTaskDetailPathActionGroup
        val previousWorkspaceGit = showWorkspaceGitActionGroup
        val previousWorkspacePath = showWorkspacePathActionGroup
        val previousBranchCopyIcons = showTaskAreaBranchCopyIcons
        val previousRequirementCopyIcons = showTaskAreaRequirementCopyIcons
        val previousProjectNameCopyIcons = showTaskAreaProjectNameCopyIcons
        showTaskDetailGitActionGroup = taskGit
        showTaskDetailPathActionGroup = taskPath
        showWorkspaceGitActionGroup = workspaceGit
        showWorkspacePathActionGroup = workspacePath
        showTaskAreaBranchCopyIcons = branchCopyIcons
        showTaskAreaRequirementCopyIcons = requirementCopyIcons
        showTaskAreaProjectNameCopyIcons = projectNameCopyIcons
        controller.updateTaskAreaToolGroupVisibility(
            taskGit,
            taskPath,
            workspaceGit,
            workspacePath,
            branchCopyIcons,
            requirementCopyIcons,
            projectNameCopyIcons,
        ) {
            showTaskDetailGitActionGroup = previousTaskGit
            showTaskDetailPathActionGroup = previousTaskPath
            showWorkspaceGitActionGroup = previousWorkspaceGit
            showWorkspacePathActionGroup = previousWorkspacePath
            showTaskAreaBranchCopyIcons = previousBranchCopyIcons
            showTaskAreaRequirementCopyIcons = previousRequirementCopyIcons
            showTaskAreaProjectNameCopyIcons = previousProjectNameCopyIcons
        }
    }
    fun saveMeegleProjects() {
        controller.updateMeegleProjects(meegleProjects.toSortedMap().values.toList()) {
            meegleProjects.clear()
            controller.config.meegleProjects.forEachIndexed { index, project -> meegleProjects[index] = project }
        }
    }
    fun saveBlockedGitBranches(updated: List<String>) {
        val previous = blockedGitWriteBranches
        blockedGitWriteBranches = updated
        controller.settingsController.updateBlockedGitWriteBranches(updated) {
            blockedGitWriteBranches = previous
        }
    }

    LaunchedEffect(selectedSection) {
        if (selectedSection == "paths") controller.refreshConfigFileSnapshot()
        if (selectedSection == "cli") controller.refreshCliInstallationStatus()
        if (selectedSection == "codex-plugins" || selectedSection == "skills") controller.codexExtensionsController.refreshCached()
        if (selectedSection == "feishu") controller.refreshMeegleStatus()
        if (selectedSection == "lark") controller.refreshLarkStatus()
        if (selectedSection == "git") controller.refreshLocalGit()
        if (selectedSection == "genbu") controller.refreshGenbu()
    }
    LaunchedEffect(controller.agentRevision, controller.config.groups) {
        globalAgentsLoading = true
        globalAgentsError = null
        groupAgentLoading.clear()
        groupAgentLoaded.clear()
        groupAgentErrors.clear()
        controller.config.groups.forEach { group ->
            groupAgentLoading[group.id] = true
            groupAgentLoaded[group.id] = false
        }
        try {
            globalAgents = controller.readGlobalAgentsAsync()
            globalAgentsLoading = false
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            globalAgentsError = error.message ?: error::class.simpleName ?: "无法读取全局 AGENTS.md"
            controller.showError(error)
            globalAgentsLoading = false
        }
        controller.config.groups.forEach { group ->
            groupAgentLoading[group.id] = true
            try {
                groupAgentDrafts[group.id] = controller.readGroupAgentsAsync(group.id)
                groupAgentLoaded[group.id] = true
                groupAgentLoading[group.id] = false
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                groupAgentErrors[group.id] = error.message ?: error::class.simpleName ?: "无法读取组 AGENTS.md"
                controller.showError(error)
                groupAgentLoading[group.id] = false
            }
        }
    }
    DisposableEffect(selectedSection) {
        onDispose {
            if (selectedSection == "feishu") controller.cancelMeegleProjectLoad()
            if (selectedSection == "lark") controller.cancelLarkDeviceCodeLogin()
        }
    }

    Box(Modifier.fillMaxSize()) {
        Row(
            Modifier.fillMaxWidth().fillMaxHeight().align(Alignment.TopStart)
                .padding(
                    start = MAIN_CONTENT_START_PADDING_DP.dp,
                    end = MAIN_CONTENT_END_PADDING_DP.dp,
                    bottom = MAIN_CONTENT_BOTTOM_PADDING_DP.dp,
                ),
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Surface(
                Modifier.width(250.dp).fillMaxHeight(),
                shape = RoundedCornerShape(16.dp),
                color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.38f),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            ) {
                LazyColumn(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    items(sections, key = { it.key }) { section ->
                        if (section.startsGroup && section != sections.first()) {
                            HorizontalDivider(
                                Modifier.padding(vertical = 6.dp),
                                color = MaterialTheme.colorScheme.outlineVariant,
                            )
                        }
                        Surface(
                            Modifier.fillMaxWidth().clickable { navigateToSection(section.key) },
                            color = if (selectedSection == section.key) MaterialTheme.colorScheme.primaryContainer else Color.Transparent,
                            shape = RoundedCornerShape(10.dp),
                        ) {
                            Text(
                                section.label,
                                Modifier.padding(horizontal = 14.dp, vertical = 10.dp),
                                fontWeight = if (selectedSection == section.key) FontWeight.SemiBold else FontWeight.Normal,
                                color = if (selectedSection == section.key) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurface,
                            )
                        }
                    }
                }
            }
            LazyColumn(
                Modifier.weight(1f).fillMaxHeight(),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
            controller.configurationLoadError?.let { error ->
                item {
                    val snapshot = controller.configFileSnapshot
                    OutlinedCard(
                        Modifier.fillMaxWidth(),
                        colors = CardDefaults.outlinedCardColors(
                            containerColor = MaterialTheme.colorScheme.errorContainer,
                        ),
                        border = BorderStroke(1.dp, MaterialTheme.colorScheme.error),
                    ) {
                        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text("配置加载失败", color = MaterialTheme.colorScheme.onErrorContainer, fontWeight = FontWeight.SemiBold)
                            SelectionContainer {
                                Text(
                                    "未使用默认配置覆盖磁盘内容。\n位置：${snapshot.path}\n原因：$error\n请先备份该位置；确认无需保留时，可在文件管理器中手动删除它，然后重新打开 silverwing。",
                                    color = MaterialTheme.colorScheme.onErrorContainer,
                                    style = MaterialTheme.typography.bodySmall,
                                )
                            }
                            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                OutlinedButton(onClick = { controller.copyText(snapshot.path.toString(), "配置路径已复制") }) { Text("复制路径") }
                                OutlinedButton(onClick = controller::revealConfigFile, enabled = snapshot.exists) { Text("定位位置") }
                                OutlinedButton(onClick = { controller.copyText(controller.configurationRecoveryGuidance(), "恢复指引已复制") }) { Text("复制恢复指引") }
                            }
                        }
                    }
                }
            }
            if (selectedSection == "basic") item {
                SettingsBasicSection(
                    controller = controller,
                    saving = saving("basic"),
                )
            }
            if (selectedSection == "tag") item {
                SettingsTagSection(
                    controller = controller,
                    maxGroupsInput = tagHistoryMaxGroupsInput,
                    onMaxGroupsInputChange = { tagHistoryMaxGroupsInput = it },
                    onSaveMaxGroups = ::saveTagHistoryMaxGroups,
                    saving = saving("tag"),
                )
            }
            if (selectedSection == "paths") item {
                SettingsPathsSection(
                    controller = controller,
                    taskRoot = taskRoot,
                    onTaskRootChange = { taskRoot = it },
                    requirementMaterialsRoot = requirementMaterialsRoot,
                    onRequirementMaterialsRootChange = { requirementMaterialsRoot = it },
                    requirementMaterialsSubdirectory = requirementMaterialsSubdirectory,
                    onRequirementMaterialsSubdirectoryChange = { requirementMaterialsSubdirectory = it },
                    saving = saving("paths"),
                    backupMenuExpanded = backupMenuExpanded,
                    onBackupMenuExpandedChange = { backupMenuExpanded = it },
                    onRestoreBackup = { restoreBackup = it },
                    onImportPreview = { importPreview = it },
                )
            }
            if (selectedSection == "groups") item {
                SettingsGroupsSection(
                    controller = controller,
                    onNewGroup = { newGroup = true },
                    onRenameGroup = { renameGroup = it },
                    onDeleteGroup = { deleteGroupTarget = it },
                )
            }
            if (selectedSection == "agents") item {
                SettingsAgentsSection(
                    controller = controller,
                    agentScope = agentScope,
                    onAgentScopeChange = { agentScope = it },
                    agentGroupId = agentGroupId,
                    onAgentGroupIdChange = { agentGroupId = it },
                    globalAgents = globalAgents,
                    onGlobalAgentsChange = { globalAgents = it },
                    groupAgentDrafts = groupAgentDrafts,
                    globalAgentsLoading = globalAgentsLoading,
                    globalAgentsError = globalAgentsError,
                    groupAgentLoading = groupAgentLoading,
                    groupAgentLoaded = groupAgentLoaded,
                    groupAgentErrors = groupAgentErrors,
                )
            }
            if (selectedSection == "agents") item {
                SettingsTaskTemplatesSection(
                    controller = controller,
                    onNewTemplate = { newTemplate = true },
                    onEditTemplate = { editTemplate = it },
                    onDeleteTemplate = { deleteTemplateTarget = it },
                )
            }
            if (selectedSection == "tools") item {
                SettingsToolsSection(
                    controller = controller,
                    developmentToolPaths = developmentToolPaths,
                    defaultDevelopmentTool = defaultDevelopmentTool,
                    onDefaultDevelopmentToolChange = { defaultDevelopmentTool = it },
                    terminal = terminal,
                    onTerminalChange = { terminal = it },
                    saving = saving("tools"),
                    onSaveDevelopmentTools = ::saveDevelopmentTools,
                )
            }
            if (selectedSection == "task-creation") item {
                SettingsTaskCreationSection(
                    controller = controller,
                    aiRequirementNamingModel = aiRequirementNamingModel,
                    onAiRequirementNamingModelChange = { aiRequirementNamingModel = it },
                    onSaveAiRequirementNamingModel = ::saveAiRequirementNamingModel,
                    saving = saving("task-creation"),
                )
            }
            if (selectedSection == "task-area") item {
                SettingsTaskAreaSection(
                    controller = controller,
                    showTaskDetailGitActionGroup = showTaskDetailGitActionGroup,
                    onShowTaskDetailGitActionGroupChange = { saveTaskAreaToolGroupVisibility(taskGit = it) },
                    showTaskDetailPathActionGroup = showTaskDetailPathActionGroup,
                    onShowTaskDetailPathActionGroupChange = { saveTaskAreaToolGroupVisibility(taskPath = it) },
                    showWorkspaceGitActionGroup = showWorkspaceGitActionGroup,
                    onShowWorkspaceGitActionGroupChange = { saveTaskAreaToolGroupVisibility(workspaceGit = it) },
                    showWorkspacePathActionGroup = showWorkspacePathActionGroup,
                    onShowWorkspacePathActionGroupChange = { saveTaskAreaToolGroupVisibility(workspacePath = it) },
                    showTaskAreaBranchCopyIcons = showTaskAreaBranchCopyIcons,
                    onShowTaskAreaBranchCopyIconsChange = { saveTaskAreaToolGroupVisibility(branchCopyIcons = it) },
                    showTaskAreaRequirementCopyIcons = showTaskAreaRequirementCopyIcons,
                    onShowTaskAreaRequirementCopyIconsChange = { saveTaskAreaToolGroupVisibility(requirementCopyIcons = it) },
                    showTaskAreaProjectNameCopyIcons = showTaskAreaProjectNameCopyIcons,
                    onShowTaskAreaProjectNameCopyIconsChange = { saveTaskAreaToolGroupVisibility(projectNameCopyIcons = it) },
                    saving = saving("task-area"),
                )
            }
            if (selectedSection == "cli") item {
                SettingsCliSection(controller)
            }
            if (selectedSection == "codex-plugins") item {
                SettingsCodexPluginsSection(controller)
            }
            if (selectedSection == "skills") item {
                SettingsExternalSkillsSection(controller)
            }
            if (selectedSection == "git") item {
                SettingsGitSection(
                    controller = controller,
                    blockedGitBranchInput = blockedGitBranchInput,
                    onBlockedGitBranchInputChange = { blockedGitBranchInput = it },
                    blockedGitWriteBranches = blockedGitWriteBranches,
                    saving = saving("git-write-policy"),
                    onSaveBlockedGitBranches = ::saveBlockedGitBranches,
                )
            }
            if (selectedSection == "lark") item {
                SettingsLarkSection(
                    controller = controller,
                    larkPath = larkPath,
                    onLarkPathChange = { larkPath = it },
                    saving = saving("lark"),
                    onLogin = { larkDomainDialog = true },
                )
            }
            if (selectedSection == "genbu") item {
                SettingsGenbuSection(
                    controller = controller,
                    genbuPath = genbuPath,
                    onGenbuPathChange = { genbuPath = it },
                    saving = saving("genbu"),
                )
            }
            if (selectedSection == "feishu") item {
                SettingsFeishuSection(
                    controller = controller,
                    meegleProjects = meegleProjects,
                    meegleMenuExpanded = meegleMenuExpanded,
                    onMeegleMenuExpandedChange = { meegleMenuExpanded = it },
                    saving = saving("feishu"),
                    onSaveMeegleProjects = ::saveMeegleProjects,
                )
            }
            if (selectedSection == "logs") item {
                SettingsLogsSection(controller)
            }
            }
        }
    }
    when (val migration = controller.taskRootMigrationState) {
        TaskRootMigrationUiState.Idle -> Unit
        is TaskRootMigrationUiState.Preview -> TaskRootMigrationDialog(
            preview = migration.preview,
            migrating = false,
            onDismiss = {
                controller.cancelTaskRootMigration()
                taskRoot = controller.config.taskRoot.orEmpty()
            },
            onConfirm = { controller.confirmTaskRootMigration() },
        )
        is TaskRootMigrationUiState.Migrating -> TaskRootMigrationDialog(
            preview = migration.preview,
            migrating = true,
            progress = migration.progress,
            onDismiss = {},
            onConfirm = {},
        )
    }
    if (larkDomainDialog) {
        LarkBusinessDomainDialog(
            onDismiss = { larkDomainDialog = false },
            onConfirm = { domains ->
                larkDomainDialog = false
                controller.loginLark(domains)
            },
        )
    }
    if (selectedSection == "lark") {
        controller.larkDeviceCodeLoginState?.let { login ->
            LarkDeviceCodeAuthorizationDialog(
                state = login,
                onDismiss = controller::cancelLarkDeviceCodeLogin,
                onOpenAuthorizationPage = controller::openLarkDeviceCodeAuthorizationUrl,
                onCopyAuthorizationUrl = {
                    controller.copyText(login.authorizationUrl, "Lark 授权链接已复制")
                },
                onCancel = controller::cancelLarkDeviceCodeLogin,
            )
        }
    }
    if (newGroup) NameDialog("创建组", "", onDismiss = { newGroup = false }) {
        controller.addGroup(it) { newGroup = false }
    }
    renameGroup?.let { group -> NameDialog("重命名组", group.name, onDismiss = { renameGroup = null }) {
        controller.renameGroup(group.id, it) { renameGroup = null }
    } }
    deleteGroupTarget?.let { group ->
        ConfirmDialog(
            title = "删除空组？",
            message = "将删除组“${group.name}”。仅当组内没有服务且没有任务引用时才会执行，此操作不会删除任何仓库目录。",
            confirmLabel = "删除组",
            destructive = true,
            enabled = !controller.settingsBusy,
            onDismiss = { deleteGroupTarget = null },
            onConfirm = { controller.deleteGroup(group.id) { deleteGroupTarget = null } },
        )
    }
    if (newTemplate) {
        TaskTemplateDialog("新建模板", "", "", onDismiss = { newTemplate = false }) { name, content ->
            if (controller.saveAgentTaskTemplate(null, name, content)) newTemplate = false
        }
    }
    editTemplate?.let { template ->
        TaskTemplateDialog("编辑模板", template.name, template.content, onDismiss = { editTemplate = null }) { name, content ->
            if (controller.saveAgentTaskTemplate(template.id, name, content)) editTemplate = null
        }
    }
    deleteTemplateTarget?.let { template ->
        ConfirmDialog(
            title = "删除模板？",
            message = "将删除模板“${template.name}”。已创建任务中的人工说明不受影响。",
            confirmLabel = "删除模板",
            destructive = true,
            enabled = !controller.settingsBusy,
            onDismiss = { deleteTemplateTarget = null },
            onConfirm = { if (controller.deleteAgentTaskTemplate(template.id)) deleteTemplateTarget = null },
        )
    }
    restoreBackup?.let { backup ->
        ConfirmDialog(
            title = "恢复配置备份？",
            message = "将先备份当前配置，再恢复 ${backup.path.fileName}。任务目录不会被删除。",
            confirmLabel = "恢复备份",
            enabled = !controller.busy,
            onDismiss = { restoreBackup = null },
            onConfirm = { if (controller.restoreConfigBackup(backup.path.toString())) restoreBackup = null },
        )
    }
    importPreview?.let { preview ->
        ConfirmDialog(
            title = "导入此配置？",
            message = buildString {
                appendLine("来源：${preview.source}")
                if (preview.changes.isEmpty()) appendLine("配置内容没有变化") else preview.changes.forEach { appendLine("• $it") }
                if (preview.invalidDevelopmentTools.isNotEmpty()) {
                    append("当前电脑路径无效，导入后需重新选择：${preview.invalidDevelopmentTools.joinToString { it.displayName }}")
                }
            }.trim(),
            confirmLabel = "导入配置",
            enabled = !controller.busy,
            onDismiss = { importPreview = null },
            onConfirm = { if (controller.importConfig(preview.source.toString())) importPreview = null },
        )
    }
}

@Composable
private fun TaskRootMigrationDialog(
    preview: com.snowball.silverwing.core.TaskRootMigrationPreview,
    migrating: Boolean,
    progress: TaskRootMigrationProgress? = null,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    val mode = when (preview.mode) {
        TaskRootMigrationMode.DIRECT_SWITCH -> "直接切换"
        TaskRootMigrationMode.SAME_FILE_STORE -> "同磁盘移动"
        TaskRootMigrationMode.CROSS_FILE_STORE -> "跨磁盘复制并校验"
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (migrating) "正在迁移任务目录" else "迁移任务并切换目录？") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("silverwing 将迁移全部任务，校验 Git 状态和任务清单后才更新配置。迁移期间请关闭这些工作区的 IDE 和终端。")
                Text("原目录：${preview.sourceRoot}", style = MaterialTheme.typography.bodySmall)
                Text("新目录：${preview.targetRoot}", style = MaterialTheme.typography.bodySmall)
                Text("方式：$mode · ${preview.taskCount} 个任务 · ${preview.workspaceCount} 个工作区 · ${formatByteSize(preview.totalBytes)}")
                if (migrating) {
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text(taskRootMigrationProgressLabel(progress), style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        confirmButton = {
            Button(onClick = onConfirm, enabled = !migrating) {
                Text(if (migrating) "迁移中…" else "迁移并切换")
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, enabled = !migrating) { Text("取消") }
        },
    )
}

private fun taskRootMigrationProgressLabel(progress: TaskRootMigrationProgress?): String = when (progress?.phase) {
    null, TaskRootMigrationPhase.PREPARING -> "正在准备迁移…"
    TaskRootMigrationPhase.TRANSFERRING_AND_VERIFYING -> progress.let {
        "正在迁移并校验 ${it.currentTask.orEmpty()}（${it.completedTasks + 1}/${it.totalTasks}）"
    }
    TaskRootMigrationPhase.UPDATING_CONFIG -> "任务已校验，正在更新配置…"
    TaskRootMigrationPhase.CLEANING_SOURCE -> "配置已生效，正在清理旧目录…"
    TaskRootMigrationPhase.COMPLETED -> "迁移完成"
}

private fun formatByteSize(bytes: Long): String = when {
    bytes >= 1024L * 1024L * 1024L -> "%.1f GB".format(bytes.toDouble() / (1024L * 1024L * 1024L))
    bytes >= 1024L * 1024L -> "%.1f MB".format(bytes.toDouble() / (1024L * 1024L))
    bytes >= 1024L -> "%.1f KB".format(bytes.toDouble() / 1024L)
    else -> "$bytes B"
}

internal fun normalizeSettingsSection(stored: String, supported: Set<String>): String = when (stored) {
    "advanced" -> "feishu"
    "genbu" -> if ("genbu" in supported) "genbu" else "basic"
    in supported -> stored
    else -> "basic"
}

internal enum class CliDetectionPhase { IDLE, LOADING, READY, AUTH_REQUIRED, FAILED }

/** CLI 设置页的最大阅读宽度，避免宽屏把状态信息和命令路径拉得过散。 */
private const val CLI_SETTINGS_MAX_CONTENT_WIDTH_DP = 1_040f

internal fun meegleCliDetectionPhase(state: MeegleCliState): CliDetectionPhase = when (state) {
    MeegleCliState.Idle -> CliDetectionPhase.IDLE
    is MeegleCliState.Loading -> CliDetectionPhase.LOADING
    is MeegleCliState.Ready -> when {
        !state.status.installed -> CliDetectionPhase.FAILED
        !state.status.authenticated -> CliDetectionPhase.AUTH_REQUIRED
        else -> CliDetectionPhase.READY
    }
    is MeegleCliState.Failed -> CliDetectionPhase.FAILED
}

internal fun meegleLoginActionVisible(status: MeegleCliStatus?): Boolean =
    status?.installed == true && !status.authenticated

internal fun meegleLogoutActionVisible(status: MeegleCliStatus?): Boolean =
    status?.installed == true && status.authenticated

internal fun meegleLoginActionEnabled(
    status: MeegleCliStatus?,
    cliState: MeegleCliState,
    saving: Boolean,
    busy: Boolean,
): Boolean = meegleLoginActionVisible(status) &&
    cliState !is MeegleCliState.Loading &&
    !saving &&
    !busy

internal fun meegleLogoutActionEnabled(
    status: MeegleCliStatus?,
    cliState: MeegleCliState,
    saving: Boolean,
    busy: Boolean,
): Boolean = meegleLogoutActionVisible(status) &&
    cliState !is MeegleCliState.Loading &&
    !saving &&
    !busy

/** “更多操作”只包含辅助动作；没有可用命令或页面正忙时不应打开它。 */
internal fun cliMoreActionsEnabled(
    command: String,
    saving: Boolean,
    busy: Boolean,
): Boolean = command.isNotBlank() && !saving && !busy

/** 命令路径编辑默认收起；明确的保存失败需要把恢复入口直接展示出来。 */
internal fun cliManualConfigInitiallyExpanded(pathSaveFailed: Boolean): Boolean = pathSaveFailed

/**
 * 自动发现的可执行文件仅作为编辑器默认值；只有用户主动保存后才会成为手动配置。
 * 这样既能展示当前实际使用的路径，也不会因打开设置而改写配置。
 */
internal fun cliPathEditorInitialValue(configuredPath: String, discoveredPath: String? = null): String =
    configuredPath.ifBlank { discoveredPath.orEmpty() }

internal fun cliPathEditorHasChanges(
    pathInput: String,
    configuredPath: String,
    discoveredPath: String? = null,
): Boolean = pathInput.trim() != cliPathEditorInitialValue(configuredPath, discoveredPath).trim()

internal fun codexDiscoveredExecutablePath(command: String, source: CodexCommandSource): String? = when (source) {
    CodexCommandSource.DESKTOP_APP, CodexCommandSource.PROBED -> command.ifBlank { null }
    CodexCommandSource.CONFIGURED, CodexCommandSource.PATH_FALLBACK -> null
}

private fun genbuSourceLabel(source: GenbuCommandSource): String = when (source) {
    GenbuCommandSource.CONFIGURED -> "手动配置"
    GenbuCommandSource.PROBED -> "自动探测"
    GenbuCommandSource.PATH_FALLBACK -> "PATH 回退"
}

private fun gitSourceLabel(source: GitCommandSource): String = when (source) {
    GitCommandSource.CONFIGURED -> "手动配置"
    GitCommandSource.PROBED -> "自动探测"
    GitCommandSource.PATH_FALLBACK -> "PATH 回退"
}

private fun meegleSourceLabel(source: MeegleCommandSource): String = when (source) {
    MeegleCommandSource.CONFIGURED -> "手动配置"
    MeegleCommandSource.PROBED -> "自动探测"
    MeegleCommandSource.PATH_FALLBACK -> "PATH 回退"
}

private fun codexSourceLabel(source: CodexCommandSource): String = when (source) {
    CodexCommandSource.CONFIGURED -> "手动配置"
    CodexCommandSource.DESKTOP_APP -> "Codex Desktop"
    CodexCommandSource.PROBED -> "自动探测"
    CodexCommandSource.PATH_FALLBACK -> "PATH 回退"
}

@Composable
private fun CliCommandPanel(
    controller: DesktopApplication,
    command: String,
    source: String,
    version: CommandVersionStatus?,
    phase: CliDetectionPhase,
    failure: String? = null,
    configuredPath: String,
    discoveredPath: String? = null,
    pathLabel: String,
    pathPlaceholder: String,
    saving: Boolean,
    operationBusy: Boolean = false,
    pathSaveFailed: Boolean,
    onPathChange: (String) -> Unit,
    onSavePath: (String) -> Unit,
    onChoosePath: (String) -> Unit,
    onRefresh: () -> Unit,
    toolbarSummary: (@Composable () -> Unit)? = null,
    toolbarActions: @Composable FlowRowScope.() -> Unit = {},
    separateToolbarActionGroup: Boolean = false,
    extra: @Composable ColumnScope.() -> Unit = {},
) {
    val initialPath = cliPathEditorInitialValue(configuredPath, discoveredPath)
    var pathInput by remember(pathLabel) { mutableStateOf(initialPath) }
    var pathInputEdited by remember(pathLabel) { mutableStateOf(false) }
    var pathEditorExpanded by remember(pathLabel) { mutableStateOf(cliManualConfigInitiallyExpanded(pathSaveFailed)) }
    var moreActionsExpanded by remember(pathLabel) { mutableStateOf(false) }
    LaunchedEffect(pathLabel, configuredPath, discoveredPath) {
        if (!pathInputEdited) pathInput = initialPath
    }
    LaunchedEffect(pathLabel, pathSaveFailed) {
        if (pathSaveFailed) pathEditorExpanded = true
    }
    val pathChanged = cliPathEditorHasChanges(pathInput, configuredPath, discoveredPath)
    val phaseLabel = when (phase) {
        CliDetectionPhase.IDLE -> "尚未检测"
        CliDetectionPhase.LOADING -> "检测中"
        CliDetectionPhase.READY -> "检测成功"
        CliDetectionPhase.AUTH_REQUIRED -> "未登录"
        CliDetectionPhase.FAILED -> "检测失败"
    }
    val phaseColor = when (phase) {
        CliDetectionPhase.READY -> SuccessGreen
        CliDetectionPhase.AUTH_REQUIRED -> WarningAmber
        CliDetectionPhase.FAILED -> MaterialTheme.colorScheme.error
        CliDetectionPhase.IDLE, CliDetectionPhase.LOADING -> MaterialTheme.colorScheme.onSurfaceVariant
    }
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text("连接状态", modifier = Modifier.weight(1f), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Surface(
                color = phaseColor.copy(alpha = 0.12f),
                shape = RoundedCornerShape(50),
            ) {
                Text(
                    phaseLabel,
                    modifier = Modifier.padding(horizontal = 9.dp, vertical = 4.dp),
                    style = MaterialTheme.typography.labelSmall,
                    color = phaseColor,
                )
            }
        }
        GitEnvironmentGrid(
            listOf(
                GitEnvironmentField("版本号", version?.version ?: "不可用", monospace = true),
                GitEnvironmentField("当前命令", command.ifBlank { "未解析" }, monospace = true),
                GitEnvironmentField("命令来源", source),
                GitEnvironmentField("检测状态", phaseLabel),
            ),
        )
        failure?.let { error ->
            Text(error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
        version?.error?.let { error ->
            Text("版本检测失败：$error", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
        CliActionToolbar(
            summary = toolbarSummary,
            onRefresh = onRefresh,
            refreshEnabled = phase != CliDetectionPhase.LOADING && !saving && !controller.busy && !operationBusy,
            moreEnabled = cliMoreActionsEnabled(command, saving, controller.busy || operationBusy),
            moreActionsExpanded = moreActionsExpanded,
            onMoreActionsExpandedChange = { moreActionsExpanded = it },
            onCopyCommand = { controller.copyCliCommandPath(command) },
            onRunInTerminal = { controller.runCliInTerminal(command) },
            actions = toolbarActions,
            separateActionGroup = separateToolbarActionGroup,
        )
        extra()
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text("命令位置", style = MaterialTheme.typography.titleSmall)
                SelectionContainer {
                    Text(
                        configuredPath.ifBlank { "$source · ${command.ifBlank { "自动探测" }}" },
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        fontFamily = FontFamily.Monospace,
                        maxLines = if (pathEditorExpanded) 2 else 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            TextButton(
                onClick = { pathEditorExpanded = !pathEditorExpanded },
                enabled = !controller.busy && !operationBusy && !saving,
            ) {
                Text(if (pathEditorExpanded) "收起" else "编辑")
                Icon(
                    if (pathEditorExpanded) Icons.Outlined.KeyboardArrowUp else Icons.Outlined.KeyboardArrowDown,
                    null,
                    Modifier.size(17.dp),
                )
            }
        }
        if (pathEditorExpanded) {
            BoxWithConstraints(Modifier.fillMaxWidth()) {
                val compactEditor = maxWidth < 560.dp
                val pathField: @Composable (Modifier) -> Unit = { modifier ->
                    OutlinedTextField(
                        value = pathInput,
                        onValueChange = {
                            pathInput = it
                            pathInputEdited = true
                            onPathChange(it)
                        },
                        modifier = modifier.onFocusChanged { focus ->
                            if (!focus.isFocused && pathChanged) onSavePath(pathInput)
                        },
                        label = { Text(pathLabel) },
                        placeholder = { Text(pathPlaceholder) },
                        supportingText = { Text("留空时使用自动探测或 PATH 回退。") },
                        singleLine = true,
                        readOnly = controller.busy || operationBusy || saving,
                    )
                }
                val chooseButton: @Composable (Modifier) -> Unit = { modifier ->
                    OutlinedButton(
                        onClick = { onChoosePath(pathInput) },
                        modifier = modifier,
                        enabled = !controller.pathPickerBusy && !controller.busy && !operationBusy && !saving,
                    ) {
                        Icon(Icons.Outlined.Folder, null)
                        Spacer(Modifier.width(4.dp))
                        Text("选择")
                    }
                }
                if (compactEditor) {
                    Column(
                        Modifier.fillMaxWidth(),
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                        horizontalAlignment = Alignment.End,
                    ) {
                        pathField(Modifier.fillMaxWidth())
                        chooseButton(Modifier)
                    }
                } else {
                    Row(
                        Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        pathField(Modifier.weight(1f))
                        chooseButton(Modifier)
                    }
                }
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                if (configuredPath.isNotBlank()) {
                    OutlinedButton(
                        onClick = {
                            pathInput = cliPathEditorInitialValue("", discoveredPath)
                            pathInputEdited = false
                            onPathChange("")
                            onSavePath("")
                        },
                        enabled = !controller.busy && !operationBusy && !saving,
                    ) {
                        Text("恢复自动")
                    }
                    Spacer(Modifier.width(8.dp))
                }
                Button(onClick = { onSavePath(pathInput) }, enabled = pathChanged && !controller.busy && !operationBusy && !saving) {
                    Text("保存手动配置")
                }
            }
        }
    }
}

private fun larkSourceLabel(source: LarkCommandSource): String = when (source) {
    LarkCommandSource.CONFIGURED -> "手动配置"
    LarkCommandSource.PROBED -> "自动探测"
    LarkCommandSource.PATH_FALLBACK -> "PATH 回退"
}

internal fun larkCliDetectionPhase(state: LarkCliState): CliDetectionPhase = when (state) {
    LarkCliState.Idle -> CliDetectionPhase.IDLE
    is LarkCliState.Loading -> CliDetectionPhase.LOADING
    is LarkCliState.Ready -> when {
        !state.status.installed -> CliDetectionPhase.FAILED
        state.status.authenticationState == LarkAuthenticationState.CHECK_FAILED -> CliDetectionPhase.FAILED
        !state.status.authenticated -> CliDetectionPhase.AUTH_REQUIRED
        else -> CliDetectionPhase.READY
    }
    is LarkCliState.Failed -> CliDetectionPhase.FAILED
}

internal fun larkLoginActionVisible(status: LarkCliStatus?): Boolean =
    status?.installed == true && status.authenticationState == LarkAuthenticationState.LOGIN_REQUIRED

internal fun larkLogoutActionVisible(status: LarkCliStatus?): Boolean =
    status?.installed == true && status.authenticationState == LarkAuthenticationState.AUTHENTICATED

internal fun larkTokenStatusLabel(tokenStatus: String): String = when (tokenStatus.lowercase()) {
    "valid" -> "有效"
    "needs_refresh" -> "等待自动刷新"
    "expired" -> "已过期"
    else -> "状态未知"
}

internal fun larkLoginActionEnabled(
    status: LarkCliStatus?,
    cliState: LarkCliState,
    saving: Boolean,
    busy: Boolean,
    deviceLoginActive: Boolean,
): Boolean = larkLoginActionVisible(status) &&
    cliState !is LarkCliState.Loading && !saving && !busy && !deviceLoginActive

internal fun larkLogoutActionEnabled(
    status: LarkCliStatus?,
    cliState: LarkCliState,
    saving: Boolean,
    busy: Boolean,
): Boolean = larkLogoutActionVisible(status) &&
    cliState !is LarkCliState.Loading && !saving && !busy

/**
 * CLI 的低频操作使用同一条扁平工具栏，避免每个操作各自形成一个带描边的视觉块。
 * Meegle 可传入认证摘要；宽屏时摘要与操作左右分置，窄屏时保持“状态后操作”的阅读顺序。
 */
@Composable
private fun CliActionToolbar(
    summary: (@Composable () -> Unit)?,
    onRefresh: () -> Unit,
    refreshEnabled: Boolean,
    moreEnabled: Boolean,
    moreActionsExpanded: Boolean,
    onMoreActionsExpandedChange: (Boolean) -> Unit,
    onCopyCommand: () -> Unit,
    onRunInTerminal: () -> Unit,
    actions: @Composable FlowRowScope.() -> Unit,
    separateActionGroup: Boolean,
) {
    val utilityActions: @Composable FlowRowScope.() -> Unit = {
        CliToolbarTextButton(
            label = "重新检测",
            icon = Icons.Outlined.Refresh,
            onClick = onRefresh,
            enabled = refreshEnabled,
        )
        Box {
            CliToolbarTextButton(
                label = "更多",
                icon = Icons.Outlined.MoreHoriz,
                onClick = { onMoreActionsExpandedChange(true) },
                enabled = moreEnabled,
            )
            SilverWingDropdownMenu(
                expanded = moreActionsExpanded,
                onDismissRequest = { onMoreActionsExpandedChange(false) },
            ) {
                DropdownMenuItem(
                    text = { Text("复制命令路径") },
                    onClick = {
                        onMoreActionsExpandedChange(false)
                        onCopyCommand()
                    },
                )
                DropdownMenuItem(
                    text = { Text("在终端中运行") },
                    onClick = {
                        onMoreActionsExpandedChange(false)
                        onRunInTerminal()
                    },
                )
            }
        }
    }
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val compact = maxWidth < 560.dp
        val actionRow: @Composable () -> Unit = {
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(2.dp),
                verticalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                utilityActions()
                if (separateActionGroup && !compact) Spacer(Modifier.width(8.dp))
                actions()
            }
        }
        when {
            summary == null -> Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Spacer(Modifier.weight(1f))
                actionRow()
            }
            compact -> Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                summary()
                actionRow()
            }
            else -> Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.weight(1f)) { summary() }
                actionRow()
            }
        }
    }
}

@Composable
private fun CliToolbarTextButton(
    label: String,
    icon: ImageVector,
    onClick: () -> Unit,
    enabled: Boolean,
    error: Boolean = false,
) {
    TextButton(
        onClick = onClick,
        enabled = enabled,
        colors = if (error) {
            ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error)
        } else {
            ButtonDefaults.textButtonColors()
        },
    ) {
        Icon(icon, null, Modifier.size(18.dp))
        Spacer(Modifier.width(5.dp))
        Text(label)
    }
}

/**
 * CLI 页面在宽屏上保持可读的行长，窄窗口时仍随父容器收缩。
 *
 * 这层只负责页面宽度，不引入新的视觉边框；具体内容继续由同级的 SettingsCard 承载。
 */
@Composable
private fun CliSettingsContent(content: @Composable ColumnScope.() -> Unit) {
    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.TopCenter) {
        Column(
            modifier = Modifier
                .widthIn(max = CLI_SETTINGS_MAX_CONTENT_WIDTH_DP.dp)
                .fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(16.dp),
            content = content,
        )
    }
}

@Composable
private fun SettingsBasicSection(
    controller: DesktopApplication,
    saving: Boolean,
) {
    SettingsCard("外观", "调整本机界面的显示方式。") {
        AutoSaveStatus(controller, "basic")
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text("界面主题", style = MaterialTheme.typography.titleSmall)
            Spacer(Modifier.width(14.dp))
            FlowRow(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ThemePreference.entries.forEach { theme ->
                    FilterChip(
                        controller.config.theme == theme,
                        { controller.setTheme(theme) },
                        label = { Text(theme.displayName) },
                        enabled = !controller.busy && !saving,
                    )
                }
            }
        }
    }
}

@Composable
private fun SettingsTagSection(
    controller: DesktopApplication,
    maxGroupsInput: String,
    onMaxGroupsInputChange: (String) -> Unit,
    onSaveMaxGroups: () -> Unit,
    saving: Boolean,
) {
    val parsedMaxGroups = maxGroupsInput.toIntOrNull()
    val maxGroupsValid = parsedMaxGroups != null && parsedMaxGroups in 1..MAX_TAG_HISTORY_GROUPS
    SettingsCard("Tag设置", "控制测试 Tag 功能及历史记录保留数量。") {
        AutoSaveStatus(controller, "tag")
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Text("全局 Tag 开关", style = MaterialTheme.typography.titleSmall)
                Text(
                    if (controller.config.tagEnabled) {
                        "已开启：显示 Tag 页面、构建入口和 Tag 配置。"
                    } else {
                        "已关闭：隐藏 Tag 页面、构建入口和其他 Tag 配置；本设置仍保留用于重新开启。"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Switch(
                checked = controller.config.tagEnabled,
                onCheckedChange = { controller.setGlobalTagEnabled(it) },
                enabled = !controller.busy && !saving,
            )
        }
        OutlinedTextField(
            value = maxGroupsInput,
            onValueChange = { value ->
                if (value.isEmpty() || value.all(Char::isDigit)) onMaxGroupsInputChange(value)
            },
            modifier = Modifier.fillMaxWidth(),
            label = { Text("最多保留 Tag 组") },
            supportingText = {
                Text("按最近更新时间保留，超过后自动清理最旧的整组记录；范围：1-$MAX_TAG_HISTORY_GROUPS。")
            },
            isError = !maxGroupsValid,
            singleLine = true,
            enabled = !controller.busy && !saving,
        )
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            Button(
                onClick = onSaveMaxGroups,
                enabled = maxGroupsValid && parsedMaxGroups != controller.config.tagHistoryMaxGroups && !controller.busy && !saving,
            ) {
                Text("保存Tag设置")
            }
        }
    }
}

@Composable
private fun SettingsGenbuSection(
    controller: DesktopApplication,
    genbuPath: String,
    onGenbuPathChange: (String) -> Unit,
    saving: Boolean,
) {
    val state = controller.genbuSettingsState
    val (command, source) = controller.genbuCommandResolution()
    val loaded = state as? GenbuSettingsState.Loaded
    val phase = when (state) {
        GenbuSettingsState.Idle -> CliDetectionPhase.IDLE
        GenbuSettingsState.Loading -> CliDetectionPhase.LOADING
        is GenbuSettingsState.Loaded -> CliDetectionPhase.READY
        is GenbuSettingsState.Failed -> CliDetectionPhase.FAILED
    }
    CliSettingsContent {
        SettingsCard("Genbu CLI", "配置并检查生产版本查询命令。", cliHeader = true) {
            AutoSaveStatus(controller, "genbu")
            CliCommandPanel(
                controller = controller,
                command = command,
                source = genbuSourceLabel(source),
                version = loaded?.version,
                phase = phase,
                failure = (state as? GenbuSettingsState.Failed)?.message,
                configuredPath = genbuPath,
                pathLabel = "Genbu 可执行文件路径",
                pathPlaceholder = if (System.getProperty("os.name").startsWith("Windows", true))
                    "例如 C:\\tools\\genbu.exe" else "例如 /usr/local/bin/genbu",
                saving = saving,
                pathSaveFailed = controller.settingsSaveState("genbu") == SettingsSaveState.FAILED,
                onPathChange = onGenbuPathChange,
                onSavePath = { raw ->
                    controller.updateGenbuExecutablePath(raw) { onGenbuPathChange(controller.config.genbuExecutablePath.orEmpty()) }
                },
                onChoosePath = { initial -> controller.chooseApplication(initial) { onGenbuPathChange(it) } },
                onRefresh = { controller.refreshGenbu(force = true) },
            )
        }
    }
}

@Composable
private fun SettingsLarkSection(
    controller: DesktopApplication,
    larkPath: String,
    onLarkPathChange: (String) -> Unit,
    saving: Boolean,
    onLogin: () -> Unit,
) {
    CliSettingsContent {
        SettingsCard("Lark CLI", "连接、授权并管理本机的 Lark 命令。", cliHeader = true) {
            AutoSaveStatus(controller, "lark")
            LarkCliStatusPanel(
                controller = controller,
                larkPath = larkPath,
                onLarkPathChange = onLarkPathChange,
                saving = saving,
                onLogin = onLogin,
            )
        }
    }
}

@Composable
private fun LarkCliStatusPanel(
    controller: DesktopApplication,
    larkPath: String,
    onLarkPathChange: (String) -> Unit,
    saving: Boolean,
    onLogin: () -> Unit,
) {
    val cliState = controller.larkCliState
    val status = when (cliState) {
        is LarkCliState.Ready -> cliState.status
        is LarkCliState.Loading -> cliState.previous
        LarkCliState.Idle, is LarkCliState.Failed -> null
    }
    val (command, source) = controller.larkCommandResolution()
    val phase = larkCliDetectionPhase(cliState)
    val deviceLogin = controller.larkDeviceCodeLoginState
    val failure = when (cliState) {
        is LarkCliState.Failed -> cliState.message
        is LarkCliState.Ready -> when {
            !cliState.status.installed -> "未安装或无法启动 Lark CLI"
            cliState.status.authenticationState == LarkAuthenticationState.CHECK_FAILED ->
                "登录状态检查失败：${cliState.status.authenticationError ?: "Lark CLI 返回了无法识别的登录状态"}"
            !cliState.status.authenticated -> "未登录"
            else -> null
        }
        else -> null
    }
    val toolbarSummary: (@Composable () -> Unit)? = status?.let { current ->
        {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    buildString {
                        append("品牌：${current.brand ?: "Lark"}")
                        current.expiresAt?.let { append(" · 凭据有效期：$it") }
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                current.tokenStatus?.let {
                    Text(
                        "凭据状态：${larkTokenStatusLabel(it)}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
    CliCommandPanel(
        controller = controller,
        command = command,
        source = larkSourceLabel(source),
        version = status?.version?.let { CommandVersionStatus(command = command, version = it) },
        phase = phase,
        failure = failure,
        configuredPath = larkPath,
        pathLabel = "Lark CLI 可执行文件路径",
        pathPlaceholder = if (System.getProperty("os.name").startsWith("Windows", true))
            "例如 C:\\tools\\lark-cli.cmd" else "例如 /usr/local/bin/lark-cli",
        saving = saving,
        operationBusy = controller.larkBusy,
        pathSaveFailed = controller.settingsSaveState("lark") == SettingsSaveState.FAILED,
        onPathChange = onLarkPathChange,
        onSavePath = { raw -> controller.updateLarkExecutablePath(raw) },
        onChoosePath = { initial -> controller.chooseApplication(initial) { onLarkPathChange(it) } },
        onRefresh = { controller.refreshLarkStatus(force = true) },
        toolbarSummary = toolbarSummary,
        separateToolbarActionGroup = true,
        toolbarActions = {
            status?.let { current ->
                if (larkLogoutActionVisible(current)) {
                    CliToolbarTextButton(
                        label = "退出登录",
                        icon = Icons.AutoMirrored.Outlined.Logout,
                        onClick = controller::logoutLark,
                        enabled = larkLogoutActionEnabled(current, cliState, saving, controller.larkBusy),
                        error = true,
                    )
                } else if (larkLoginActionVisible(current)) {
                    CliToolbarTextButton(
                        label = "登录 Lark CLI",
                        icon = Icons.AutoMirrored.Outlined.OpenInNew,
                        onClick = onLogin,
                        enabled = larkLoginActionEnabled(current, cliState, saving, controller.larkBusy, deviceLogin != null),
                    )
                }
            }
        },
        extra = {
            if (status?.let(::larkLoginActionVisible) == true && deviceLogin == null) {
                Text(
                    "登录时选择业务域；授权页会自动打开，完成后自动更新登录状态。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (controller.larkBusy && controller.larkOperationCancellable) {
                TextButton(onClick = controller::cancelLarkOperation) { Text("取消操作") }
            }
            controller.larkOperationError?.let { error ->
                SelectionContainer { Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
            }
        },
    )
}

@Composable
private fun LarkBusinessDomainDialog(
    onDismiss: () -> Unit,
    onConfirm: (List<String>) -> Unit,
) {
    var selected by remember { mutableStateOf(emptySet<String>()) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("选择 Lark 业务域") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(
                    "只申请所选业务域所需权限。每次登录都需要重新选择，至少选择一项。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Column(
                    Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                ) {
                    LARK_BUSINESS_DOMAINS.forEach { domain ->
                        Row(
                            Modifier.fillMaxWidth().clickable {
                                selected = if (domain in selected) selected - domain else selected + domain
                            },
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Checkbox(
                                checked = domain in selected,
                                onCheckedChange = { checked ->
                                    selected = if (checked) selected + domain else selected - domain
                                },
                            )
                            Column(Modifier.padding(vertical = 4.dp)) {
                                Text(larkBusinessDomainLabel(domain))
                                Text(domain, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            Button(onClick = { onConfirm(selected.toList()) }, enabled = selected.isNotEmpty()) { Text("开始授权") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
private fun LarkDeviceCodeAuthorizationDialog(
    state: LarkDeviceCodeLoginUiState,
    onDismiss: () -> Unit,
    onOpenAuthorizationPage: () -> Boolean,
    onCopyAuthorizationUrl: () -> Unit,
    onCancel: () -> Unit,
) {
    val expiresAt = state.expiresAtEpochMillis
        ?: (System.currentTimeMillis() + state.expiresInSeconds.coerceIn(0L, 24L * 60L * 60L) * 1_000L)
    var remainingSeconds by remember(state.authorizationUrl, state.expiresAtEpochMillis) {
        mutableStateOf(((expiresAt - System.currentTimeMillis()).coerceAtLeast(0L) / 1_000L))
    }
    LaunchedEffect(state.authorizationUrl, state.expiresAtEpochMillis) {
        while (remainingSeconds > 0) {
            delay(1_000)
            remainingSeconds = ((expiresAt - System.currentTimeMillis()).coerceAtLeast(0L) / 1_000L)
        }
    }
    val minutes = remainingSeconds / 60
    val seconds = remainingSeconds % 60
    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(if (state.polling) "等待 Lark 授权" else "Lark 授权")
        },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    if (state.polling) {
                        "授权页已自动打开，请在浏览器中完成授权。Silverwing 会自动确认登录结果。"
                    } else {
                        "正在准备授权，请稍候。"
                    },
                    style = MaterialTheme.typography.bodyMedium,
                )
                Text(
                    if (remainingSeconds > 0) {
                        "授权链接剩余 ${minutes}分${seconds.toString().padStart(2, '0')}秒"
                    } else {
                        "授权链接已过期，请取消后重新登录。"
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = if (remainingSeconds > 0) {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    } else {
                        MaterialTheme.colorScheme.error
                    },
                )
                state.error?.let { error ->
                    SelectionContainer {
                        Text(error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { onOpenAuthorizationPage() }) {
                Icon(Icons.AutoMirrored.Outlined.OpenInNew, null, Modifier.size(18.dp))
                Spacer(Modifier.width(5.dp))
                Text("重新打开授权页")
            }
        },
        dismissButton = {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                TextButton(onClick = onCopyAuthorizationUrl) {
                    Icon(Icons.Outlined.ContentCopy, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(5.dp))
                    Text("复制链接")
                }
                TextButton(onClick = onCancel) { Text("取消") }
            }
        },
    )
}

private fun larkBusinessDomainLabel(domain: String): String = when (domain) {
    "application" -> "应用"
    "approval" -> "审批"
    "apps" -> "应用管理"
    "attendance" -> "考勤"
    "base" -> "多维表格"
    "calendar" -> "日历"
    "contact" -> "通讯录"
    "docs" -> "云文档"
    "drive" -> "云空间"
    "event" -> "事件"
    "im" -> "即时消息"
    "mail" -> "邮箱"
    "markdown" -> "Markdown 文档"
    "mindnotes" -> "思维笔记"
    "minutes" -> "会议纪要"
    "note" -> "笔记"
    "okr" -> "OKR"
    "sheets" -> "电子表格"
    "slides" -> "幻灯片"
    "task" -> "任务"
    "vc" -> "视频会议"
    "wiki" -> "知识库"
    else -> domain
}

@Composable
private fun SettingsPathsSection(
    controller: DesktopApplication,
    taskRoot: String,
    onTaskRootChange: (String) -> Unit,
    requirementMaterialsRoot: String,
    onRequirementMaterialsRootChange: (String) -> Unit,
    requirementMaterialsSubdirectory: String,
    onRequirementMaterialsSubdirectoryChange: (String) -> Unit,
    saving: Boolean,
    backupMenuExpanded: Boolean,
    onBackupMenuExpandedChange: (Boolean) -> Unit,
    onRestoreBackup: (ConfigStore.Backup) -> Unit,
    onImportPreview: (ConfigStore.ImportPreview) -> Unit,
) {
    val materialsSaving = controller.settingsSaveState("requirement-materials-root") == SettingsSaveState.SAVING ||
        controller.settingsSaveState("requirement-materials-subdirectory") == SettingsSaveState.SAVING
    var backups by remember { mutableStateOf<List<ConfigStore.Backup>?>(null) }
    var backupsLoading by remember { mutableStateOf(false) }
    var backupLoadError by remember { mutableStateOf<String?>(null) }
    var importLoading by remember { mutableStateOf(false) }
    val ioScope = rememberCoroutineScope()
    LaunchedEffect(backupMenuExpanded) {
        if (!backupMenuExpanded) return@LaunchedEffect
        backupsLoading = true
        backups = null
        backupLoadError = null
        try {
            backups = controller.configBackupsAsync()
            backupsLoading = false
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            backupLoadError = error.message ?: error::class.simpleName ?: "无法读取配置备份"
            controller.showError(error)
            backupsLoading = false
        }
    }
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        SettingsCard("任务路径设置", "选择 silverwing 扫描任务的根目录。") {
            AutoSaveStatus(controller, "paths")
            PathField(
                "任务根目录",
                taskRoot,
                onTaskRootChange,
                !controller.pathPickerBusy && !controller.busy && !saving,
                Modifier.onFocusChanged { focus ->
                    if (!focus.isFocused && taskRoot.isNotBlank() && taskRoot != controller.config.taskRoot.orEmpty()) {
                        controller.updateTaskRoot(taskRoot) { onTaskRootChange(controller.config.taskRoot.orEmpty()) }
                    }
                },
            ) {
                controller.chooseDirectory(taskRoot) { selected ->
                    onTaskRootChange(selected)
                    controller.updateTaskRoot(selected) { onTaskRootChange(controller.config.taskRoot.orEmpty()) }
                }
            }
            TaskManifestIssues(controller)
        }
        SettingsCard("需求资料目录设置", "需求编号已填写且以下两项均不为空时，silverwing 会创建或复用需求资料目录；silverwing CLI 会在同一目录补充过程文档。") {
            AutoSaveStatus(controller, "requirement-materials-root")
            AutoSaveStatus(controller, "requirement-materials-subdirectory")
            PathField(
                "需求资料根目录",
                requirementMaterialsRoot,
                onRequirementMaterialsRootChange,
                !controller.pathPickerBusy && !controller.busy && !materialsSaving,
                Modifier.onFocusChanged { focus ->
                    if (!focus.isFocused && requirementMaterialsRoot != controller.config.requirementMaterialsRoot.orEmpty()) {
                        controller.updateRequirementMaterialsRoot(requirementMaterialsRoot) {
                            onRequirementMaterialsRootChange(controller.config.requirementMaterialsRoot.orEmpty())
                        }
                    }
                },
            ) {
                controller.chooseDirectory(requirementMaterialsRoot.ifBlank { null }) { selected ->
                    onRequirementMaterialsRootChange(selected)
                    controller.updateRequirementMaterialsRoot(selected) {
                        onRequirementMaterialsRootChange(controller.config.requirementMaterialsRoot.orEmpty())
                    }
                }
            }
            OutlinedTextField(
                value = requirementMaterialsSubdirectory,
                onValueChange = onRequirementMaterialsSubdirectoryChange,
                modifier = Modifier.fillMaxWidth().onFocusChanged { focus ->
                    if (!focus.isFocused && requirementMaterialsSubdirectory != controller.config.requirementMaterialsSubdirectory.orEmpty()) {
                        controller.updateRequirementMaterialsSubdirectory(requirementMaterialsSubdirectory) {
                            onRequirementMaterialsSubdirectoryChange(controller.config.requirementMaterialsSubdirectory.orEmpty())
                        }
                    }
                },
                label = { Text("需求资料子目录") },
                placeholder = { Text("例如：研发") },
                supportingText = { Text("可留空；非空时只能填写一个安全的 Windows 目录名，保存时会自动去除首尾空格。") },
                singleLine = true,
                enabled = !controller.busy && !materialsSaving,
            )
            Text(
                if (controller.config.requirementMaterialsConfigured) "需求资料目录功能已配置" else "根路径和子目录名均填写后才会启用需求资料目录功能",
                color = if (controller.config.requirementMaterialsConfigured) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodySmall,
            )
        }
        SettingsCard("配置目录", "silverwing 配置按功能拆分存储；可在此预览当前配置分片并管理配置备份。") {
            ConfigFilePreview(controller)
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Text("配置操作", style = MaterialTheme.typography.titleSmall)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = controller::exportConfig, enabled = !controller.busy) { Text("导出配置") }
                OutlinedButton(
                    onClick = {
                        controller.chooseFile(null) { selected ->
                            if (!importLoading) {
                                importLoading = true
                                ioScope.launch {
                                    try {
                                        onImportPreview(controller.previewConfigImportAsync(selected))
                                        importLoading = false
                                    } catch (cancelled: CancellationException) {
                                        throw cancelled
                                    } catch (error: Throwable) {
                                        controller.showError(error)
                                        importLoading = false
                                    }
                                }
                            }
                        }
                    },
                    enabled = !controller.busy && !controller.pathPickerBusy && !importLoading,
                ) { Text(if (importLoading) "读取中…" else "导入配置") }
                Box {
                    OutlinedButton(onClick = { onBackupMenuExpandedChange(true) }, enabled = !controller.busy) { Text("恢复备份") }
                    SilverWingDropdownMenu(backupMenuExpanded, onDismissRequest = { onBackupMenuExpandedChange(false) }) {
                        when {
                            backupLoadError != null ->
                                DropdownMenuItem(text = { Text("读取失败：$backupLoadError") }, onClick = {}, enabled = false)
                            backupsLoading || backups == null ->
                                DropdownMenuItem(text = { Text("正在读取配置备份…") }, onClick = {}, enabled = false)
                            backups!!.isEmpty() ->
                                DropdownMenuItem(text = { Text("暂无配置备份") }, onClick = {}, enabled = false)
                            else -> backups!!.forEach { backup ->
                                DropdownMenuItem(
                                    text = { Text(backup.path.fileName.toString()) },
                                    onClick = { onBackupMenuExpandedChange(false); onRestoreBackup(backup) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SettingsCliSection(controller: DesktopApplication) {
    val status = controller.cliInstallationStatus
    val tagSkillStatus = controller.tagSkillInstallationStatus
    var confirmUninstall by remember { mutableStateOf(false) }
    var confirmTagSkillOverwrite by remember { mutableStateOf(false) }
    var confirmTagSkillUninstall by remember { mutableStateOf(false) }
    SettingsCard("silverwing CLI", "将绿色包内置的 silverwing CLI 安装为当前用户可用的 silverwing 命令。") {
        Text("安装状态", style = MaterialTheme.typography.titleSmall)
        SelectionContainer {
            Text(status.message, style = MaterialTheme.typography.bodyMedium)
        }
        status.commandPath?.let { commandPath ->
            SelectionContainer {
                Text(
                    "命令入口：$commandPath",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (status.supported) {
            Text(
                "安装会将绿色包中的 CLI 与 Java 运行时复制到当前用户目录，并将 silverwing 命令加入用户终端环境；不需要管理员权限。完成后请重开终端；若从 Codex 或 IDE 打开终端，请重启对应应用。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(
                    onClick = controller::installCli,
                    enabled = status.bundledPayloadAvailable && (status.installed || !status.uninstallAvailable) && !controller.settingsBusy,
                ) {
                    Icon(Icons.Outlined.Terminal, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(5.dp))
                    Text(if (status.installed) "更新 CLI" else "安装 CLI")
                }
                OutlinedButton(onClick = controller::refreshCliInstallationStatus, enabled = !controller.settingsBusy) {
                    Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(5.dp))
                    Text("刷新状态")
                }
                OutlinedButton(
                    onClick = { confirmUninstall = true },
                    enabled = status.uninstallAvailable && !controller.settingsBusy,
                ) {
                    Icon(Icons.Outlined.Delete, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(5.dp))
                    Text("卸载 CLI")
                }
            }
        } else {
            Text(
                "当前系统暂不支持一键安装；可使用绿色包内的 bin/silverwing。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (controller.config.tagEnabled) SettingsCard("silverwing Skill", "安装仅包含测试 Tag 操作的 silverwing Skill，不包含任务创建流程。") {
        Text("安装状态", style = MaterialTheme.typography.titleSmall)
        SelectionContainer {
            Text(tagSkillStatus.message, style = MaterialTheme.typography.bodyMedium)
        }
        SelectionContainer {
            Text(
                "安装位置：${tagSkillStatus.destination}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            "安装会完整覆盖同名 Skill，以清理旧版遗留文件；不会安装或更新 silverwing CLI。完成后请重新打开或刷新 Skill 宿主。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(
                onClick = {
                    if (tagSkillStatus.destinationOccupied) confirmTagSkillOverwrite = true
                    else controller.installTagSkill()
                },
                enabled = tagSkillStatus.bundledPayloadAvailable && !controller.settingsBusy,
            ) {
                Icon(Icons.Outlined.Description, null, Modifier.size(18.dp))
                Spacer(Modifier.width(5.dp))
                Text(if (tagSkillStatus.installed) "更新 Tag Skill" else "安装 Tag Skill")
            }
            OutlinedButton(
                onClick = controller::refreshTagSkillInstallationStatus,
                enabled = !controller.settingsBusy,
            ) {
                Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp))
                Spacer(Modifier.width(5.dp))
                Text("刷新状态")
            }
            OutlinedButton(
                onClick = { confirmTagSkillUninstall = true },
                enabled = tagSkillStatus.uninstallAvailable && !controller.settingsBusy,
            ) {
                Icon(Icons.Outlined.Delete, null, Modifier.size(18.dp))
                Spacer(Modifier.width(5.dp))
                Text("卸载 Tag Skill")
            }
        }
    }
    if (confirmUninstall) {
        ConfirmDialog(
            title = "卸载 silverwing CLI？",
            message = "将删除 silverwing 安装的所有 CLI 版本和随附运行时，并从当前用户 PATH 移除 silverwing。不会删除任务、配置、项目文件或系统 Java。",
            confirmLabel = "卸载 CLI",
            destructive = true,
            enabled = !controller.settingsBusy,
            onDismiss = { confirmUninstall = false },
            onConfirm = {
                controller.uninstallCli()
                confirmUninstall = false
            },
        )
    }
    if (confirmTagSkillOverwrite) {
        ConfirmDialog(
            title = if (tagSkillStatus.installed) "更新 silverwing Skill？" else "覆盖现有 Skill？",
            message = "将完整覆盖 ${tagSkillStatus.destination} 中的现有内容，以安装当前应用内置的 silverwing Skill。该目录内未包含在新版本中的文件会被删除；silverwing CLI 不受影响。",
            confirmLabel = if (tagSkillStatus.installed) "更新 Tag Skill" else "覆盖并安装",
            destructive = true,
            enabled = !controller.settingsBusy,
            onDismiss = { confirmTagSkillOverwrite = false },
            onConfirm = {
                controller.installTagSkill()
                confirmTagSkillOverwrite = false
            },
        )
    }
    if (confirmTagSkillUninstall) {
        ConfirmDialog(
            title = "卸载 silverwing Skill？",
            message = "将删除 ${tagSkillStatus.destination} 及其全部内容。不会删除其他 Skill、silverwing CLI、任务或配置。",
            confirmLabel = "卸载 Tag Skill",
            destructive = true,
            enabled = !controller.settingsBusy,
            onDismiss = { confirmTagSkillUninstall = false },
            onConfirm = {
                controller.uninstallTagSkill()
                confirmTagSkillUninstall = false
            },
        )
    }
}

@Composable
private fun SettingsGroupsSection(
    controller: DesktopApplication,
    onNewGroup: () -> Unit,
    onRenameGroup: (GroupConfig) -> Unit,
    onDeleteGroup: (GroupConfig) -> Unit,
) {
    SettingsCard("项目组", "按项目组维护仓库和服务；只能删除没有服务和任务引用的空组。") {
        AutoSaveStatus(controller, "groups")
        controller.config.groups.forEachIndexed { index, group ->
            GroupSettingsRow(
                controller = controller,
                group = group,
                index = index,
                groupCount = controller.config.groups.size,
                onRename = { onRenameGroup(group) },
                onDelete = { onDeleteGroup(group) },
            )
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            OutlinedButton(onClick = onNewGroup) { Icon(Icons.Outlined.Add, null, Modifier.size(18.dp)); Spacer(Modifier.width(5.dp)); Text("创建组") }
        }
    }
}

@Composable
private fun SettingsAgentsSection(
    controller: DesktopApplication,
    agentScope: String,
    onAgentScopeChange: (String) -> Unit,
    agentGroupId: String,
    onAgentGroupIdChange: (String) -> Unit,
    globalAgents: String,
    onGlobalAgentsChange: (String) -> Unit,
    groupAgentDrafts: MutableMap<String, String>,
    globalAgentsLoading: Boolean,
    globalAgentsError: String?,
    groupAgentLoading: Map<String, Boolean>,
    groupAgentLoaded: Map<String, Boolean>,
    groupAgentErrors: Map<String, String>,
) {
    SettingsCard("全局与组说明", "磁盘中的全局/组 AGENTS.md 是唯一准确来源；新任务会在新 Agent 会话中读取，旧任务仍会同步。") {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(agentScope == "global", { onAgentScopeChange("global") }, label = { Text("全局") })
            controller.config.groups.forEach { group ->
                FilterChip(agentScope == group.id, {
                    onAgentScopeChange(group.id)
                    onAgentGroupIdChange(group.id)
                }, label = { Text(group.name) })
            }
        }
        val isGlobal = agentScope == "global"
        val agentPath = if (isGlobal) controller.globalAgentsPath else controller.groupAgentsPath(agentGroupId)
        Text(
            if (isGlobal) "对所有任务生效；新任务根 AGENTS.md 不会因这里的内容变更而重写。"
            else "仅对当前组生效；新任务根 AGENTS.md 不会因这里的内容变更而重写。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Surface(color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.6f), shape = RoundedCornerShape(10.dp)) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 11.dp, vertical = 7.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(agentPath, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.labelSmall)
                TextButton(onClick = {
                    if (isGlobal) controller.revealGlobalAgents() else controller.revealGroupAgents(agentGroupId)
                }) { Icon(Icons.AutoMirrored.Outlined.OpenInNew, null, Modifier.size(17.dp)); Spacer(Modifier.width(4.dp)); Text("打开位置") }
                ActionIconButton("复制 Agent 文件完整路径", { controller.copyText(agentPath, "完整路径已复制") }) {
                    Icon(Icons.Outlined.ContentCopy, "复制完整路径")
                }
            }
        }
        val currentGroupLoading = !isGlobal && groupAgentLoading[agentGroupId] != false
        val currentGroupError = if (isGlobal) null else groupAgentErrors[agentGroupId]
        val editorReady = if (isGlobal) !globalAgentsLoading && globalAgentsError == null else
            groupAgentLoaded[agentGroupId] == true && !currentGroupLoading && currentGroupError == null
        val agentLoading = if (isGlobal) globalAgentsLoading else currentGroupLoading
        val agentError = if (isGlobal) globalAgentsError else currentGroupError
        when {
            agentLoading -> LinearProgressIndicator(Modifier.fillMaxWidth())
            agentError != null -> Text(
                "无法读取 Agent 说明：$agentError",
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall,
            )
            else -> Unit
        }
        if (isGlobal) {
            OutlinedTextField(globalAgents, {
                onGlobalAgentsChange(it)
                controller.markGlobalAgentsEdited(it)
            }, Modifier.fillMaxWidth(), minLines = 10, readOnly = controller.busy || !editorReady)
        } else {
            OutlinedTextField(groupAgentDrafts[agentGroupId].orEmpty(), {
                groupAgentDrafts[agentGroupId] = it
                controller.markGroupAgentsEdited(agentGroupId, it)
            }, Modifier.fillMaxWidth(), minLines = 10, readOnly = controller.busy || !editorReady)
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            Button(
                onClick = {
                    if (isGlobal) controller.saveGlobalAgents(globalAgents)
                    else controller.saveGroupAgents(agentGroupId, groupAgentDrafts[agentGroupId].orEmpty())
                },
                enabled = !controller.busy && editorReady,
            ) { Text(if (isGlobal) "保存全局说明" else "保存组说明") }
        }
    }
}

@Composable
private fun SettingsTaskTemplatesSection(
    controller: DesktopApplication,
    onNewTemplate: () -> Unit,
    onEditTemplate: (AgentTaskTemplate) -> Unit,
    onDeleteTemplate: (AgentTaskTemplate) -> Unit,
) {
    SettingsCard("任务说明模板", "创建任务时可勾选一个模板自动填充任务人工说明；模板修改不影响已创建的任务。") {
        if (controller.agentTaskTemplates.isEmpty()) {
            Text("还没有模板。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        controller.agentTaskTemplates.forEach { template ->
            OutlinedCard(Modifier.fillMaxWidth()) {
                Row(Modifier.padding(horizontal = 12.dp, vertical = 9.dp), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(template.name, fontWeight = FontWeight.SemiBold)
                        Text(
                            template.content.lineSequence().firstOrNull().orEmpty(),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    ActionIconButton("删除模板", { onDeleteTemplate(template) }, enabled = !controller.busy) { Icon(Icons.Outlined.Delete, "删除") }
                    ActionIconButton("编辑模板", { onEditTemplate(template) }, enabled = !controller.busy) { Icon(Icons.Outlined.Edit, "编辑") }
                }
            }
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            OutlinedButton(onClick = onNewTemplate, enabled = !controller.busy) {
                Icon(Icons.Outlined.Add, null, Modifier.size(18.dp)); Spacer(Modifier.width(5.dp)); Text("新建模板")
            }
        }
    }
}

@Composable
private fun TaskTemplateDialog(
    title: String,
    initialName: String,
    initialContent: String,
    onDismiss: () -> Unit,
    onSave: (String, String) -> Unit,
) {
    var name by remember { mutableStateOf(initialName) }
    var content by remember { mutableStateOf(initialContent) }
    Dialog(onDismissRequest = onDismiss) {
        Surface(
            shape = RoundedCornerShape(18.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.padding(20.dp).widthIn(max = 560.dp).heightIn(max = 640.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text(title, style = MaterialTheme.typography.titleLarge)
                Column(Modifier.weight(1f, fill = false).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    OutlinedTextField(name, { name = it }, Modifier.fillMaxWidth(), label = { Text("模板名称") }, singleLine = true)
                    OutlinedTextField(
                        content,
                        { content = it },
                        Modifier.fillMaxWidth().heightIn(max = 320.dp),
                        label = { Text("模板内容") },
                        supportingText = { Text("创建任务勾选后填充到任务人工说明，仍可继续编辑。") },
                        minLines = 8,
                    )
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onDismiss) { Text("取消") }
                    Spacer(Modifier.width(8.dp))
                    Button(onClick = { onSave(name, content) }, enabled = name.isNotBlank() && content.isNotBlank()) { Text("保存") }
                }
            }
        }
    }
}

@Composable
private fun SettingsToolsSection(
    controller: DesktopApplication,
    developmentToolPaths: MutableMap<DevelopmentToolType, String>,
    defaultDevelopmentTool: DevelopmentToolType,
    onDefaultDevelopmentToolChange: (DevelopmentToolType) -> Unit,
    terminal: String,
    onTerminalChange: (String) -> Unit,
    saving: Boolean,
    onSaveDevelopmentTools: () -> Unit,
) {
    val pathSnapshot = developmentToolPaths.toMap()
    var existingPaths by remember { mutableStateOf<Set<DevelopmentToolType>>(emptySet()) }
    var pathCheckLoading by remember { mutableStateOf(true) }
    LaunchedEffect(pathSnapshot) {
        pathCheckLoading = true
        existingPaths = emptySet()
        try {
            existingPaths = controller.existingDevelopmentToolPathsAsync(pathSnapshot)
            pathCheckLoading = false
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Throwable) {
            pathCheckLoading = false
        }
    }
    SettingsCard(
        "开发工具",
        "启动后会在后台静默探测尚未填写的工具路径；已有路径即使失效也不会被覆盖。工作区快捷打开会使用该工作区配置的开发工具。",
    ) {
        AutoSaveStatus(controller, "tools")
        DevelopmentToolType.entries.forEach { type ->
            val value = developmentToolPaths[type].orEmpty()
            val pathChecked = type in pathSnapshot && (!pathCheckLoading || type in existingPaths)
            val valid = value.isNotBlank() && pathChecked && type in existingPaths
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(type.displayName, Modifier.weight(1f), fontWeight = FontWeight.SemiBold)
                        Text(
                            when {
                                valid -> "已配置 · 可用"
                                value.isNotBlank() && !pathChecked -> "检测中"
                                value.isNotBlank() -> "路径无效"
                                else -> "等待自动探测"
                            },
                            style = MaterialTheme.typography.labelSmall,
                            color = when {
                                valid -> SuccessGreen
                                value.isNotBlank() -> MaterialTheme.colorScheme.error
                                else -> MaterialTheme.colorScheme.onSurfaceVariant
                            },
                        )
                    }
                    Row(
                        Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        OutlinedTextField(
                            value,
                            { developmentToolPaths[type] = it },
                            Modifier.weight(1f).onFocusChanged { focus ->
                                val configured = controller.config.developmentTools.firstOrNull { it.type == type }?.path.orEmpty()
                                if (!focus.isFocused && developmentToolPaths[type].orEmpty().trim() != configured) onSaveDevelopmentTools()
                            },
                            label = { Text("应用路径") },
                            placeholder = { Text("留空时由 silverwing 自动探测") },
                            singleLine = true,
                            readOnly = controller.busy || saving,
                        )
                        OutlinedButton(
                            onClick = { controller.chooseApplication(value) { developmentToolPaths[type] = it; onSaveDevelopmentTools() } },
                            enabled = !controller.pathPickerBusy && !controller.busy && !saving,
                        ) { Icon(Icons.Outlined.Folder, null); Spacer(Modifier.width(4.dp)); Text("手动选择") }
                        OutlinedButton(
                            onClick = { controller.testDevelopmentTool(type, value) },
                            enabled = valid && !controller.busy,
                        ) { Text("测试打开") }
                        if (value.isNotBlank()) {
                            OutlinedButton(
                                onClick = { controller.resetDevelopmentToolToAutomatic(type) },
                                enabled = !controller.busy && !saving,
                            ) { Text("恢复自动") }
                        } else {
                            OutlinedButton(
                                onClick = controller::redetectDevelopmentTools,
                                enabled = !controller.busy && !saving,
                            ) { Icon(Icons.Outlined.Refresh, null, Modifier.size(17.dp)); Spacer(Modifier.width(4.dp)); Text("重新检测") }
                        }
                    }
                }
            }
        }
        Text("全局默认开发工具", style = MaterialTheme.typography.titleSmall)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            DevelopmentToolType.entries.forEach { type ->
                FilterChip(
                    selected = defaultDevelopmentTool == type,
                    onClick = { onDefaultDevelopmentToolChange(type); onSaveDevelopmentTools() },
                    label = { Text(type.displayName) },
                    enabled = !controller.busy && !saving,
                )
            }
        }
        val terminalResolution = controller.terminalCommandResolution()
        OutlinedCard(Modifier.fillMaxWidth()) {
            Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text("终端", Modifier.weight(1f), fontWeight = FontWeight.SemiBold)
                    Text(
                        when {
                            controller.config.terminalExecutable.isNullOrBlank() -> "自动"
                            terminalResolution.available -> "手动配置 · 可用"
                            else -> "路径无效"
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = if (terminalResolution.available) SuccessGreen else MaterialTheme.colorScheme.error,
                    )
                }
                Text(
                    "当前使用：${terminalResolution.displayName} · ${terminalResolution.command}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                PathField(
                    "自定义终端", terminal, onTerminalChange, !controller.pathPickerBusy && !controller.busy && !saving,
                    Modifier.onFocusChanged { focus ->
                        if (!focus.isFocused && terminal.trim() != controller.config.terminalExecutable.orEmpty()) onSaveDevelopmentTools()
                    },
                ) {
                    val selected: (String) -> Unit = { path -> onTerminalChange(path); onSaveDevelopmentTools() }
                    if (terminalUsesApplicationPicker(System.getProperty("os.name"))) {
                        controller.chooseApplication(terminal, selected)
                    } else {
                        controller.chooseFile(terminal, selected)
                    }
                }
                if (terminal.isNotBlank()) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                        OutlinedButton(
                            onClick = {
                                onTerminalChange("")
                                onSaveDevelopmentTools()
                            },
                            enabled = !controller.busy && !saving,
                        ) { Text("恢复自动") }
                    }
                }
            }
        }
    }
}

@Composable
private fun SettingsTaskCreationSection(
    controller: DesktopApplication,
    aiRequirementNamingModel: String,
    onAiRequirementNamingModelChange: (String) -> Unit,
    onSaveAiRequirementNamingModel: () -> Unit,
    saving: Boolean,
) {
    LaunchedEffect(controller.config.codexExecutablePath) { controller.detectCodexCliPath() }
    val (codexCommand, codexSource) = controller.codexCommandResolution()
    val codexPhase = when {
        controller.codexCliPathLoading -> CliDetectionPhase.LOADING
        controller.codexCliPathError != null -> CliDetectionPhase.FAILED
        codexSource == CodexCommandSource.PATH_FALLBACK -> CliDetectionPhase.IDLE
        else -> CliDetectionPhase.READY
    }
    val normalizedModel = aiRequirementNamingModel.trim()
    val modelInputError = when {
        normalizedModel.isEmpty() -> "AI 命名模型不能为空。"
        normalizedModel.any(Char::isWhitespace) -> "AI 命名模型不能包含空白字符。"
        else -> null
    }
    Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        AutoSaveStatus(controller, "task-creation")
        SettingsCard(
            "AI 分支名和文件夹名",
            "为创建任务时的需求命名设置本机 Codex CLI 行为。",
        ) {
            Row(
                Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text("开启 AI 生成分支名和文件夹名", style = MaterialTheme.typography.titleSmall)
                    Text(
                        "选中需求后，仅将标题和正文发送给本机 Codex CLI，自动补全尚未手动修改的文件夹名和分支名。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.width(12.dp))
                Switch(
                    checked = controller.config.aiRequirementNamingEnabled,
                    onCheckedChange = { controller.setAiRequirementNamingEnabled(it) },
                    enabled = !controller.busy && !saving,
                )
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            CliCommandPanel(
                controller = controller,
                command = codexCommand,
                source = codexSourceLabel(codexSource),
                version = controller.codexCliVersion,
                phase = codexPhase,
                failure = controller.codexCliPathError,
                configuredPath = controller.config.codexExecutablePath.orEmpty(),
                discoveredPath = codexDiscoveredExecutablePath(codexCommand, codexSource),
                pathLabel = "Codex CLI 可执行文件路径",
                pathPlaceholder = if (System.getProperty("os.name").startsWith("Windows", true))
                    "例如 C:\\Users\\你\\AppData\\Local\\OpenAI\\Codex\\bin\\<版本>\\codex.exe"
                else "例如 /usr/local/bin/codex",
                saving = saving,
                pathSaveFailed = controller.settingsSaveState("task-creation") == SettingsSaveState.FAILED,
                onPathChange = {},
                onSavePath = { raw -> controller.updateCodexExecutablePath(raw) },
                onChoosePath = { initial ->
                    controller.chooseApplication(initial) { selected -> controller.updateCodexExecutablePath(selected) }
                },
                onRefresh = controller::detectCodexCliPath,
            )
            OutlinedTextField(
                value = aiRequirementNamingModel,
                onValueChange = onAiRequirementNamingModelChange,
                modifier = Modifier.fillMaxWidth(),
                label = { Text("AI 命名模型") },
                placeholder = { Text(RequirementAiNamingModel.DEFAULT) },
                supportingText = {
                    Text(
                        modelInputError
                            ?: "用于生成任务文件夹名和分支名；默认 ${RequirementAiNamingModel.DEFAULT}。",
                    )
                },
                isError = modelInputError != null,
                singleLine = true,
                enabled = !controller.busy && !saving,
            )
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Button(
                    onClick = onSaveAiRequirementNamingModel,
                    enabled = !controller.busy && !saving && modelInputError == null &&
                        aiRequirementNamingModel != controller.config.aiRequirementNamingModel,
                ) {
                    Text("保存模型")
                }
            }
        }
    }
}

@Composable
private fun SettingsTaskAreaSection(
    controller: DesktopApplication,
    showTaskDetailGitActionGroup: Boolean,
    onShowTaskDetailGitActionGroupChange: (Boolean) -> Unit,
    showTaskDetailPathActionGroup: Boolean,
    onShowTaskDetailPathActionGroupChange: (Boolean) -> Unit,
    showWorkspaceGitActionGroup: Boolean,
    onShowWorkspaceGitActionGroupChange: (Boolean) -> Unit,
    showWorkspacePathActionGroup: Boolean,
    onShowWorkspacePathActionGroupChange: (Boolean) -> Unit,
    showTaskAreaBranchCopyIcons: Boolean,
    onShowTaskAreaBranchCopyIconsChange: (Boolean) -> Unit,
    showTaskAreaRequirementCopyIcons: Boolean,
    onShowTaskAreaRequirementCopyIconsChange: (Boolean) -> Unit,
    showTaskAreaProjectNameCopyIcons: Boolean,
    onShowTaskAreaProjectNameCopyIconsChange: (Boolean) -> Unit,
    saving: Boolean,
) {
    Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        AutoSaveStatus(controller, "task-area")
        SettingsCard("任务详情工具栏", "控制任务详情顶部的可选操作入口。") {
            TaskAreaToolGroupSwitchRow(
                title = "Git 工具组",
                description = "显示提交、提交并推送和推送操作。",
                checked = showTaskDetailGitActionGroup,
                onCheckedChange = onShowTaskDetailGitActionGroupChange,
                enabled = !controller.busy && !saving,
            )
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            TaskAreaToolGroupSwitchRow(
                title = "路径工具组",
                description = "显示复制任务路径、终端和打开任务目录操作。",
                checked = showTaskDetailPathActionGroup,
                onCheckedChange = onShowTaskDetailPathActionGroupChange,
                enabled = !controller.busy && !saving,
            )
        }
        SettingsCard("工作区卡片工具栏", "控制每张 Worktree 卡片的可选操作入口。") {
            TaskAreaToolGroupSwitchRow(
                title = "Git 工具组",
                description = "显示提交、提交并推送和推送操作。",
                checked = showWorkspaceGitActionGroup,
                onCheckedChange = onShowWorkspaceGitActionGroupChange,
                enabled = !controller.busy && !saving,
            )
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            TaskAreaToolGroupSwitchRow(
                title = "路径工具组",
                description = "显示终端、打开文件夹和复制路径操作。",
                checked = showWorkspacePathActionGroup,
                onCheckedChange = onShowWorkspacePathActionGroupChange,
                enabled = !controller.busy && !saving,
            )
        }
        SettingsCard("复制图标", "控制任务详情与 Worktree 卡片中的常驻信息复制操作。") {
            TaskAreaToolGroupSwitchRow(
                title = "分支名复制",
                description = "显示 Worktree 卡片中分支名旁的复制图标。",
                checked = showTaskAreaBranchCopyIcons,
                onCheckedChange = onShowTaskAreaBranchCopyIconsChange,
                enabled = !controller.busy && !saving,
            )
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            TaskAreaToolGroupSwitchRow(
                title = "需求链接和需求编号复制",
                description = "显示需求链接与需求编号旁的复制图标。",
                checked = showTaskAreaRequirementCopyIcons,
                onCheckedChange = onShowTaskAreaRequirementCopyIconsChange,
                enabled = !controller.busy && !saving,
            )
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            TaskAreaToolGroupSwitchRow(
                title = "项目名复制",
                description = "显示 Worktree 卡片项目名旁的复制图标，不会复制模块名。",
                checked = showTaskAreaProjectNameCopyIcons,
                onCheckedChange = onShowTaskAreaProjectNameCopyIconsChange,
                enabled = !controller.busy && !saving,
            )
        }
    }
}

@Composable
private fun TaskAreaToolGroupSwitchRow(
    title: String,
    description: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    enabled: Boolean,
) {
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(
                description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            enabled = enabled,
        )
    }
}

internal fun terminalUsesApplicationPicker(osName: String): Boolean = osName.startsWith("Mac", ignoreCase = true)

@Composable
private fun SettingsGitSection(
    controller: DesktopApplication,
    blockedGitBranchInput: String,
    onBlockedGitBranchInputChange: (String) -> Unit,
    blockedGitWriteBranches: List<String>,
    saving: Boolean,
    onSaveBlockedGitBranches: (List<String>) -> Unit,
) {
    CliSettingsContent {
        SettingsCard("Git 命令", "自动探测或配置 Git 命令。", cliHeader = true) {
            AutoSaveStatus(controller, "git")
            GitEnvironmentPanel(controller, controller.localGitSettingsState)
        }
        SettingsCard("Git 身份与全局配置", "查看当前用户、凭据与全局 Git 配置。", cliHeader = true) {
            GitEnvironmentDetails(controller, controller.localGitSettingsState)
        }
        SettingsCard("分支写保护", "保护指定分支，避免在 silverwing 内执行 Git 写操作。") {
            AutoSaveStatus(controller, "git-write-policy")
            Text("分支写保护", style = MaterialTheme.typography.titleSmall)
            Text("在以下实际当前分支上禁用 Commit、Push、Commit & Push，以及需要写入分支的测试Tag流程。按完整名称忽略大小写匹配。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    blockedGitBranchInput,
                    onBlockedGitBranchInputChange,
                    Modifier.weight(1f),
                    label = { Text("受保护分支") },
                    placeholder = { Text("例如 master") },
                    singleLine = true,
                )
                OutlinedButton(
                    onClick = {
                        onSaveBlockedGitBranches(blockedGitWriteBranches + blockedGitBranchInput.trim())
                        onBlockedGitBranchInputChange("")
                    },
                    enabled = blockedGitBranchInput.isNotBlank() && blockedGitWriteBranches.none { it.equals(blockedGitBranchInput.trim(), true) } && !saving,
                ) { Icon(Icons.Outlined.Add, null, Modifier.size(17.dp)); Spacer(Modifier.width(4.dp)); Text("添加") }
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                blockedGitWriteBranches.forEach { branch ->
                    Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(10.dp), border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
                        Row(Modifier.padding(start = 10.dp, end = 3.dp, top = 3.dp, bottom = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(branch)
                            ActionIconButton("删除写保护分支 $branch", { onSaveBlockedGitBranches(blockedGitWriteBranches - branch) }, Modifier.size(28.dp), enabled = !saving) {
                                Icon(Icons.Outlined.Delete, null, Modifier.size(15.dp))
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun GitEnvironmentPanel(controller: DesktopApplication, state: LocalGitSettingsState) {
    val snapshot = displayedGitSnapshot(state)
    val (command, source) = controller.gitCommandResolution()
    CliCommandPanel(
        controller = controller,
        command = command,
        source = gitSourceLabel(source),
        version = snapshot?.gitVersion?.let { CommandVersionStatus(command = command, version = it) },
        phase = when (state) {
            LocalGitSettingsState.Idle -> CliDetectionPhase.IDLE
            is LocalGitSettingsState.Loading -> CliDetectionPhase.LOADING
            is LocalGitSettingsState.Loaded -> CliDetectionPhase.READY
            is LocalGitSettingsState.Failed -> CliDetectionPhase.FAILED
        },
        failure = (state as? LocalGitSettingsState.Failed)?.message,
        configuredPath = controller.config.gitExecutablePath.orEmpty(),
        pathLabel = "Git 可执行文件路径",
        pathPlaceholder = if (System.getProperty("os.name").startsWith("Windows", true))
            "例如 C:\\Program Files\\Git\\cmd\\git.exe" else "例如 /usr/bin/git",
        saving = controller.settingsSaveState("git") == SettingsSaveState.SAVING,
        pathSaveFailed = controller.settingsSaveState("git") == SettingsSaveState.FAILED,
        onPathChange = {},
        onSavePath = { raw ->
            controller.updateGitExecutablePath(raw) { }
        },
        onChoosePath = { initial -> controller.chooseApplication(initial) { selected -> controller.updateGitExecutablePath(selected) } },
        onRefresh = { controller.refreshLocalGit(force = true) },
    )
}

@Composable
private fun GitEnvironmentDetails(controller: DesktopApplication, state: LocalGitSettingsState) {
    val snapshot = displayedGitSnapshot(state)
    snapshot?.let {
        GitEnvironmentSummary(it)
        OutlinedButton(onClick = { controller.copyText(formatLocalGitSettings(it), "Git 信息已复制") }) {
            Icon(Icons.Outlined.ContentCopy, null, Modifier.size(17.dp))
            Spacer(Modifier.width(5.dp))
            Text("复制全部 Git 信息")
        }
    }
    if (state is LocalGitSettingsState.Loading) {
        LinearProgressIndicator(Modifier.fillMaxWidth())
    }
    (state as? LocalGitSettingsState.Failed)?.let { failureState ->
        Text(failureState.message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        TextButton(onClick = { controller.copyText(failureState.message, "Git 错误已复制") }) { Text("复制错误") }
    }
    if (snapshot == null && state is LocalGitSettingsState.Idle) {
        Text("进入此页面后会读取本机 Git 环境。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}


@Composable
private fun GitEnvironmentSummary(snapshot: LocalGitEnvironmentSnapshot) {
    val credentialFields = snapshot.globalCredentialHelpers.ifEmpty {
        listOf(GitConfigValue("credential.helper", "未配置", null))
    }.map { GitEnvironmentField(it.key, it.value, it.origin) }
    val globalConfigFields = visibleGlobalGitKeyConfig(snapshot).map { GitEnvironmentField(it.key, it.value, it.origin) }
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        GitEnvironmentSection(
            "身份",
            listOf(
                GitEnvironmentField("系统用户", snapshot.systemUser.ifBlank { "未读取到" }),
                GitEnvironmentField("全局 user.name", snapshot.globalUserName?.value ?: "未配置", snapshot.globalUserName?.origin),
                GitEnvironmentField("全局 user.email", snapshot.globalUserEmail?.value ?: "未配置", snapshot.globalUserEmail?.origin),
            ),
        )
        GitEnvironmentSection("凭据与其他全局配置", credentialFields + globalConfigFields)
        snapshot.errors.forEach { error ->
            Text("全局读取错误：$error", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
    }
}

private data class GitEnvironmentField(
    val label: String,
    val value: String,
    val origin: String? = null,
    val monospace: Boolean = false,
)

@Composable
private fun GitEnvironmentSection(title: String, fields: List<GitEnvironmentField>) {
    Surface(
        Modifier.fillMaxWidth(),
        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.28f),
        shape = RoundedCornerShape(12.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            GitEnvironmentGrid(fields)
        }
    }
}

@Composable
private fun GitEnvironmentGrid(fields: List<GitEnvironmentField>) {
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        if (maxWidth < 600.dp) {
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                fields.forEach { field ->
                    GitEnvironmentValue(
                        label = field.label,
                        value = field.value,
                        origin = field.origin,
                        monospace = field.monospace,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        } else {
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                fields.chunked(2).forEach { row ->
                    Row(
                        Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(18.dp),
                        verticalAlignment = Alignment.Top,
                    ) {
                        row.forEach { field ->
                            GitEnvironmentValue(
                                label = field.label,
                                value = field.value,
                                origin = field.origin,
                                monospace = field.monospace,
                                modifier = Modifier.weight(1f),
                            )
                        }
                        if (row.size == 1) Spacer(Modifier.weight(1f))
                    }
                }
            }
        }
    }
}

@Composable
private fun GitEnvironmentValue(
    label: String,
    value: String,
    origin: String? = null,
    monospace: Boolean = false,
    modifier: Modifier = Modifier,
) {
    SelectionContainer {
        Column(modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(
                value,
                style = MaterialTheme.typography.bodyMedium,
                fontFamily = if (monospace) FontFamily.Monospace else null,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            origin?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    fontFamily = FontFamily.Monospace,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun ConfigFilePreview(controller: DesktopApplication) {
    val snapshot = controller.configFileSnapshot
    val content = snapshot.content
    SelectionContainer {
        Text(
            snapshot.path.toString(),
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedButton(onClick = controller::refreshConfigFileSnapshot, enabled = !controller.configFileSnapshotRefreshing) {
            Text(if (controller.configFileSnapshotRefreshing) "正在刷新" else "刷新预览")
        }
        OutlinedButton(onClick = { controller.copyText(snapshot.path.toString(), "配置路径已复制") }) { Text("复制路径") }
        OutlinedButton(onClick = controller::revealConfigFile, enabled = snapshot.exists) { Text("定位目录") }
    }
    when {
        !snapshot.exists -> Text(
            "配置分片尚未创建；保存任一设置后会在该目录生成。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        snapshot.readError != null -> Text(
            "无法读取配置目录：${snapshot.readError}",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        content != null -> Surface(
            Modifier.fillMaxWidth().heightIn(min = 180.dp, max = 360.dp),
            color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.55f),
            shape = RoundedCornerShape(10.dp),
        ) {
            SelectionContainer {
                Column(Modifier.padding(12.dp).verticalScroll(rememberScrollState())) {
                    Text(content, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                }
            }
        }
    }
}

@Composable
private fun TaskManifestIssues(controller: DesktopApplication) {
    val issues = controller.taskManifestIssues
    if (issues.isEmpty()) return

    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text("任务清单问题", style = MaterialTheme.typography.titleSmall)
            Text("这些文件未被 silverwing 读取或改写。请先备份后再手工修复或删除。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        TextButton(onClick = controller::refreshTaskManifestIssues) { Text("重新扫描") }
    }
    issues.forEach { issue ->
        OutlinedCard(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(issue.reason, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                SelectionContainer {
                    Text(issue.manifestPath, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(onClick = { controller.copyText(issue.manifestPath, "任务清单路径已复制") }) { Text("复制路径") }
                    TextButton(onClick = { controller.reveal(issue.manifestPath) }) { Text("定位文件") }
                    TextButton(onClick = { controller.copyText(controller.taskManifestRecoveryGuidance(issue), "恢复指引已复制") }) { Text("复制恢复指引") }
                }
            }
        }
    }
}


@Composable
private fun SettingsFeishuSection(
    controller: DesktopApplication,
    meegleProjects: MutableMap<Int, MeegleProjectConfig>,
    meegleMenuExpanded: Boolean,
    onMeegleMenuExpandedChange: (Boolean) -> Unit,
    saving: Boolean,
    onSaveMeegleProjects: () -> Unit,
) {
    CliSettingsContent {
        SettingsCard("Meegle CLI", "连接、授权并配置用于读取需求的 Meegle 命令。", cliHeader = true) {
            AutoSaveStatus(controller, "feishu")
            MeegleCliStatusPanel(controller)
        }
        SettingsCard("Meegle 项目 (${meegleProjects.size})", "创建任务时会从已配置项目读取 Meegle 需求链接。") {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    if (meegleProjects.isEmpty()) "尚未配置项目" else "${meegleProjects.size} 个已配置项目",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.weight(1f))
                Box {
                    Button(
                        onClick = {
                            onMeegleMenuExpandedChange(true)
                            controller.refreshMeegleStatus(force = true)
                        },
                        enabled = (controller.meegleCliState as? MeegleCliState.Ready)?.status?.authenticated == true &&
                            !saving &&
                            !controller.busy,
                    ) {
                        Icon(Icons.Outlined.Add, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(5.dp))
                        Text("添加项目")
                    }
                    SilverWingDropdownMenu(
                        expanded = meegleMenuExpanded,
                        onDismissRequest = { onMeegleMenuExpandedChange(false) },
                        modifier = Modifier.widthIn(max = 560.dp),
                    ) {
                        when (val state = controller.meegleProjectCatalogState) {
                            MeegleProjectCatalogState.Idle, MeegleProjectCatalogState.Loading ->
                                DropdownMenuItem(text = { Text("正在读取 Meegle 项目…") }, onClick = {}, enabled = false)
                            is MeegleProjectCatalogState.Failed ->
                                DropdownMenuItem(
                                    text = { Text("读取失败：${state.message}", color = MaterialTheme.colorScheme.error) },
                                    onClick = { controller.loadMeegleProjects(force = true) },
                                )
                            is MeegleProjectCatalogState.Loaded -> {
                                val selectedKeys = meegleProjects.values.map(MeegleProjectConfig::projectKey).toSet()
                                val available = state.projects.filterNot { it.projectKey in selectedKeys }
                                if (available.isEmpty()) {
                                    DropdownMenuItem(text = { Text("没有可添加的项目") }, onClick = {}, enabled = false)
                                }
                                available.forEach { project ->
                                    DropdownMenuItem(
                                        text = {
                                            Column {
                                                Text("${project.name} · ${project.simpleName}")
                                                Text(project.projectKey, style = MaterialTheme.typography.labelSmall)
                                            }
                                        },
                                        onClick = {
                                            val index = (meegleProjects.keys.maxOrNull() ?: -1) + 1
                                            meegleProjects[index] = MeegleProjectConfig(project.projectKey, project.simpleName)
                                            onMeegleMenuExpandedChange(false)
                                            onSaveMeegleProjects()
                                        },
                                    )
                                }
                            }
                        }
                    }
                }
            }
            if (meegleProjects.isEmpty()) {
                Text(
                    "登录后点击“添加项目”读取本机 Meegle CLI 可访问的项目。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            meegleProjects.toSortedMap().entries.forEachIndexed { listIndex, entry ->
                val index = entry.key
                val project = entry.value
                if (listIndex > 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(
                    Modifier.fillMaxWidth().padding(vertical = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(project.simpleName, fontWeight = FontWeight.SemiBold)
                        Text(project.projectKey, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    ActionIconButton("删除 Meegle 项目配置", { meegleProjects.remove(index); onSaveMeegleProjects() }, enabled = !controller.busy && !saving) {
                        Icon(Icons.Outlined.Delete, "删除项目")
                    }
                }
            }
        }
    }
}

@Composable
private fun SettingsLogsSection(controller: DesktopApplication) {
    SettingsCard("诊断与日志", "查看最近错误、打开日志目录或导出诊断包。") {
        OutlinedCard(Modifier.fillMaxWidth()) {
            Row(
                Modifier.fillMaxWidth().padding(12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Column(Modifier.weight(1f)) {
                    Text("刷新本地状态", style = MaterialTheme.typography.titleSmall)
                    Text(
                        "重新读取任务、归档、服务与本地 Git 状态；不会修改仓库内容。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                OutlinedButton(onClick = controller.taskController::refresh, enabled = !controller.busy) {
                    Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(5.dp))
                    Text("刷新状态")
                }
            }
        }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("最近错误", style = MaterialTheme.typography.titleSmall)
                Text("展示 application 日志中最近 10 条 ERROR 记录。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            TextButton(onClick = controller::refreshErrorLog) { Text("刷新") }
            TextButton(onClick = {
                controller.copyText(
                    ApplicationEventClipboard.latest(controller.recentErrors).orEmpty(),
                    "最新错误已复制",
                )
            }, enabled = controller.recentErrors.isNotEmpty()) { Text("最新") }
            TextButton(onClick = {
                controller.copyText(
                    ApplicationEventClipboard.all(controller.recentErrors),
                    "错误日志已复制",
                )
            }, enabled = controller.recentErrors.isNotEmpty()) { Text("全部") }
            TextButton(onClick = controller::openLogDirectory) { Text("目录") }
            TextButton(onClick = controller::exportDiagnostics, enabled = !controller.busy) { Text("诊断包") }
        }
        Surface(
            Modifier.fillMaxWidth().heightIn(min = 360.dp, max = 560.dp),
            color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.55f),
            shape = RoundedCornerShape(10.dp),
        ) {
            SelectionContainer {
                Column(Modifier.padding(12.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    if (controller.recentErrors.isEmpty()) {
                        Text("暂无错误记录", style = MaterialTheme.typography.bodySmall)
                    }
                    controller.recentErrors.forEach { event ->
                        Column {
                            Text("${event.timestamp} · ${event.event}", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Text(event.message, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun MeegleCliStatusPanel(controller: DesktopApplication) {
    val cliState = controller.meegleCliState
    val status = when (cliState) {
        is MeegleCliState.Ready -> cliState.status
        is MeegleCliState.Loading -> cliState.previous
        MeegleCliState.Idle, is MeegleCliState.Failed -> null
    }
    val (command, source) = controller.meegleCommandResolution()
    val saving = controller.settingsSaveState("feishu") == SettingsSaveState.SAVING
    val phase = meegleCliDetectionPhase(cliState)
    val failure = when (cliState) {
        is MeegleCliState.Failed -> cliState.message
        is MeegleCliState.Ready -> when {
            !cliState.status.installed -> "未安装或无法启动 Meegle CLI"
            !cliState.status.authenticated -> cliState.status.authenticationError?.let { "登录状态检查失败：$it" } ?: "未登录"
            else -> null
        }
        else -> null
    }
    val toolbarSummary: (@Composable () -> Unit)? = status?.let { current ->
        {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    buildString {
                        append("站点：${current.host ?: "project.feishu.cn"}")
                        current.expiresInMinutes?.let { append(" · 凭据剩余 $it 分钟") }
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (current.authenticated && (current.expiresInMinutes ?: Long.MAX_VALUE) <= 5) {
                    Text("登录凭据即将过期，建议刷新或重新登录", style = MaterialTheme.typography.labelSmall, color = WarningAmber)
                }
            }
        }
    }
    CliCommandPanel(
        controller = controller,
        command = command,
        source = meegleSourceLabel(source),
        version = status?.version?.let { CommandVersionStatus(command = command, version = it) },
        phase = phase,
        failure = failure,
        configuredPath = controller.config.meegleExecutablePath.orEmpty(),
        pathLabel = "Meegle 可执行文件路径",
        pathPlaceholder = if (System.getProperty("os.name").startsWith("Windows", true))
            "例如 C:\\tools\\meegle.cmd" else "例如 /opt/homebrew/bin/meegle",
        saving = saving,
        pathSaveFailed = controller.settingsSaveState("feishu") == SettingsSaveState.FAILED,
        onPathChange = {},
        onSavePath = { raw -> controller.updateMeegleExecutablePath(raw) },
        onChoosePath = { initial -> controller.chooseApplication(initial) { selected -> controller.updateMeegleExecutablePath(selected) } },
        onRefresh = { controller.refreshMeegleStatus(force = true) },
        toolbarSummary = toolbarSummary,
        separateToolbarActionGroup = status?.let(::meegleLoginActionVisible) == true,
        toolbarActions = {
            status?.let { current ->
                if (meegleLogoutActionVisible(current)) {
                    CliToolbarTextButton(
                        label = "退出登录",
                        icon = Icons.AutoMirrored.Outlined.Logout,
                        onClick = controller::logoutMeegle,
                        enabled = meegleLogoutActionEnabled(current, cliState, saving, controller.meegleBusy),
                        error = true,
                    )
                }
                if (meegleLoginActionVisible(current)) {
                    val deviceLogin = controller.meegleDeviceCodeLoginState
                    val loginEnabled = meegleLoginActionEnabled(current, cliState, saving, controller.meegleBusy)
                    CliToolbarTextButton(
                        label = "复制登录命令",
                        icon = Icons.Outlined.ContentCopy,
                        onClick = controller::copyMeegleLoginCommand,
                        enabled = loginEnabled,
                    )
                    CliToolbarTextButton(
                        label = if (deviceLogin == null) "登录 Meegle" else "重新生成验证码",
                        icon = Icons.AutoMirrored.Outlined.OpenInNew,
                        onClick = { controller.loginMeegle() },
                        enabled = loginEnabled,
                    )
                }
            }
        },
        extra = {
            status?.takeIf(::meegleLoginActionVisible)?.let {
                val deviceLogin = controller.meegleDeviceCodeLoginState
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        if (deviceLogin == null) {
                            Text(
                                "点击后会自动打开浏览器，并在授权完成后自动更新登录状态。",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            Surface(
                                modifier = Modifier.fillMaxWidth(),
                                color = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.48f),
                                shape = RoundedCornerShape(12.dp),
                            ) {
                                Column(
                                    modifier = Modifier.padding(12.dp),
                                    verticalArrangement = Arrangement.spacedBy(6.dp),
                                ) {
                                    Text(
                                        if (deviceLogin.polling) "正在等待浏览器授权" else "请在浏览器完成授权",
                                        style = MaterialTheme.typography.titleSmall,
                                    )
                                    Text(
                                        if (deviceLogin.polling) {
                                            "授权页已自动打开，正在检测登录结果。"
                                        } else {
                                            "自动检测没有完成。完成浏览器授权后可立即重新检测。"
                                        },
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                    Text("授权码", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    SelectionContainer {
                                        Text(
                                            deviceLogin.userCode,
                                            style = MaterialTheme.typography.titleMedium,
                                            fontFamily = FontFamily.Monospace,
                                            fontWeight = FontWeight.SemiBold,
                                        )
                                    }
                                    FlowRow(
                                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                                        verticalArrangement = Arrangement.spacedBy(4.dp),
                                    ) {
                                        OutlinedButton(onClick = controller::openMeegleDeviceCodeAuthorizationUrl) {
                                            Text("打开授权页")
                                        }
                                        OutlinedButton(
                                            onClick = { controller.copyText(deviceLogin.userCode, "Meegle 授权码已复制") },
                                        ) { Text("复制授权码") }
                                        if (!deviceLogin.polling) {
                                            OutlinedButton(onClick = { controller.completeMeegleDeviceCodeLogin() }) {
                                                Text("立即检测")
                                            }
                                        }
                                        TextButton(onClick = controller::cancelMeegleDeviceCodeLogin, enabled = !controller.meegleBusy) {
                                            Text("取消")
                                        }
                                    }
                                    Text(
                                        "验证码约 ${(deviceLogin.expiresInSeconds + 59) / 60} 分钟内有效。",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                    deviceLogin.error?.let { error ->
                                        SelectionContainer {
                                            Text(
                                                error,
                                                style = MaterialTheme.typography.labelSmall,
                                                color = MaterialTheme.colorScheme.error,
                                            )
                                        }
                                    }
                                }
                            }
                        }
                }
            }
            if (controller.meegleBusy && controller.meegleOperationCancellable) {
                TextButton(onClick = { controller.cancelMeegleOperation() }) { Text("取消操作") }
            }
            controller.meegleOperationError?.let { error ->
                SelectionContainer { Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
            }
        },
    )
}

@Composable
private fun AutoSaveStatus(controller: DesktopApplication, key: String) {
    val state = controller.settingsSaveState(key)
    if (state == SettingsSaveState.IDLE) return
    Text(
        when (state) {
            SettingsSaveState.IDLE -> ""
            SettingsSaveState.SAVING -> "正在自动保存…"
            SettingsSaveState.SAVED -> "已自动保存"
            SettingsSaveState.FAILED -> "自动保存失败，已恢复原值"
        },
        style = MaterialTheme.typography.labelSmall,
        color = if (state == SettingsSaveState.FAILED) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun GroupSettingsRow(
    controller: DesktopApplication,
    group: GroupConfig,
    index: Int,
    groupCount: Int,
    onRename: () -> Unit,
    onDelete: () -> Unit,
) {
    var branchPrefix by remember(group.id, group.defaultBranchPrefix) { mutableStateOf(group.defaultBranchPrefix) }
    var selectedToolIds by remember(group.id, group.defaultWorkspaceToolIds) {
        mutableStateOf(group.defaultWorkspaceToolIds.toSet())
    }
    val toolOptions = controller.workspaceToolOptions(group.id)
    val defaultsSaving = controller.settingsSaveState("groups") == SettingsSaveState.SAVING
    fun saveDefaults() {
        controller.updateGroupDefaults(group.id, branchPrefix, selectedToolIds.toList()) {
            val persisted = controller.config.groups.firstOrNull { it.id == group.id } ?: group
            branchPrefix = persisted.defaultBranchPrefix
            selectedToolIds = persisted.defaultWorkspaceToolIds.toSet()
        }
    }
    OutlinedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(group.name, fontWeight = FontWeight.SemiBold)
                    Text(
                        if (groupCount == 1) "任务和服务界面隐藏组层级 · ${group.services.size} 个服务" else "${group.services.size} 个服务",
                        style = MaterialTheme.typography.labelSmall,
                    )
                }
                if (controller.config.tagEnabled) {
                    Text("测试Tag")
                    Spacer(Modifier.width(8.dp))
                    Switch(group.tagEnabled, { controller.setGroupTagEnabled(group.id, it) }, enabled = !controller.busy && !defaultsSaving)
                }
                if (groupCount > 1) {
                    ActionIconButton("上移组", { controller.moveGroup(group.id, -1) }, enabled = index > 0) { Icon(Icons.Outlined.KeyboardArrowUp, "上移") }
                    ActionIconButton("下移组", { controller.moveGroup(group.id, 1) }, enabled = index < groupCount - 1) { Icon(Icons.Outlined.KeyboardArrowDown, "下移") }
                    ActionIconButton(
                        label = "删除空组",
                        onClick = onDelete,
                        enabled = group.services.isEmpty(),
                    ) { Icon(Icons.Outlined.Delete, "删除") }
                }
                ActionIconButton("重命名组", onRename) { Icon(Icons.Outlined.Edit, "重命名") }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            OutlinedTextField(
                branchPrefix,
                { branchPrefix = it },
                Modifier.fillMaxWidth().onFocusChanged { focus ->
                    if (!focus.isFocused && branchPrefix != group.defaultBranchPrefix) saveDefaults()
                },
                label = { Text("默认分支名前缀") },
                placeholder = { Text("例如 feature/zhangsan_{num}_") },
                supportingText = { Text("{num} 会从需求链接或文本的最后一段数字解析；创建页仍可继续修改。") },
                singleLine = true,
                readOnly = controller.busy || defaultsSaving,
            )
            Text("任务完成后默认打开", style = MaterialTheme.typography.titleSmall)
            if (toolOptions.isEmpty()) {
                Text("当前没有已注册的任务工作区工具。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                toolOptions.forEach { tool ->
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(
                            checked = tool.id in selectedToolIds,
                            onCheckedChange = { checked ->
                                selectedToolIds = if (checked) selectedToolIds + tool.id else selectedToolIds - tool.id
                                saveDefaults()
                            },
                            enabled = tool.available && !controller.busy && !defaultsSaving,
                        )
                        Column(Modifier.weight(1f)) {
                            Text(tool.displayName)
                            Text(
                                if (tool.available) tool.description else "当前不可用：${tool.unavailableReason}",
                                style = MaterialTheme.typography.labelSmall,
                                color = if (tool.available) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error,
                            )
                        }
                    }
                }
            }
        }
    }
}

internal fun visibleGlobalGitKeyConfig(snapshot: LocalGitEnvironmentSnapshot): List<GitConfigValue> =
    snapshot.globalKeyConfig.filterNot { it.key in setOf("user.name", "user.email") }

internal fun displayedGitSnapshot(state: LocalGitSettingsState): LocalGitEnvironmentSnapshot? = when (state) {
    is LocalGitSettingsState.Loaded -> state.snapshot
    is LocalGitSettingsState.Loading -> state.previous
    LocalGitSettingsState.Idle, is LocalGitSettingsState.Failed -> null
}

internal fun formatLocalGitSettings(snapshot: LocalGitEnvironmentSnapshot): String = buildString {
    appendLine("Git 可执行文件：${snapshot.gitExecutable ?: "未读取到"}")
    appendLine("Git 版本：${snapshot.gitVersion ?: "未读取到"}")
    appendLine("系统用户：${snapshot.systemUser.ifBlank { "未读取到" }}")
    appendLine("全局 user.name：${snapshot.globalUserName?.value ?: "未配置"}${snapshot.globalUserName?.origin?.let { "  [$it]" }.orEmpty()}")
    appendLine("全局 user.email：${snapshot.globalUserEmail?.value ?: "未配置"}${snapshot.globalUserEmail?.origin?.let { "  [$it]" }.orEmpty()}")
    appendLine("全局 credential.helper：${snapshot.globalCredentialHelpers.joinToString { it.value }.ifBlank { "未配置" }}")
    if (snapshot.globalKeyConfig.isNotEmpty()) {
        appendLine("全局关键配置：")
        snapshot.globalKeyConfig.forEach { appendLine("  ${it.key}=${it.value}${it.origin?.let { origin -> "  [$origin]" }.orEmpty()}") }
    }
    snapshot.errors.forEach { appendLine("全局读取错误：$it") }
}.trimEnd()

@Composable
internal fun SettingsCard(
    title: String,
    subtitle: String,
    cliHeader: Boolean = false,
    content: @Composable ColumnScope.() -> Unit,
) {
    OutlinedCard(
        Modifier.fillMaxWidth(),
        colors = CardDefaults.outlinedCardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Surface(
                    modifier = Modifier.size(if (cliHeader) 36.dp else 38.dp),
                    color = MaterialTheme.colorScheme.primaryContainer,
                    shape = RoundedCornerShape(10.dp),
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(
                            settingsCardIcon(title),
                            null,
                            Modifier.size(if (cliHeader) 18.dp else 20.dp),
                            tint = MaterialTheme.colorScheme.primary,
                        )
                    }
                }
                Spacer(Modifier.width(if (cliHeader) 10.dp else 11.dp))
                Column {
                    Text(title, style = MaterialTheme.typography.titleMedium)
                    Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            content()
        }
    }
}

private fun settingsCardIcon(title: String): ImageVector {
    if (title.startsWith("Meegle 项目")) return Icons.Outlined.Link
    return when (title) {
        "外观" -> Icons.Outlined.Palette
        "Tag设置" -> Icons.Outlined.Sell
        "任务路径设置" -> Icons.Outlined.Folder
        "需求资料目录设置" -> Icons.Outlined.Folder
        "配置目录" -> Icons.Outlined.Description
        "项目组" -> Icons.Outlined.Group
        "全局与组说明" -> Icons.AutoMirrored.Outlined.Article
        "任务说明模板" -> Icons.Outlined.Edit
        "开发工具" -> Icons.Outlined.Build
        "任务详情工具栏", "工作区卡片工具栏", "AI 分支名和文件夹名" -> Icons.Outlined.AccountTree
        "silverwing CLI", "silverwing Skill" -> Icons.Outlined.Terminal
        "Codex 插件", "Skills" -> Icons.Outlined.Extension
        "分支" -> Icons.Outlined.AccountTree
        "Git 环境", "Git 命令", "Git 身份与全局配置", "Genbu CLI", "Meegle CLI", "Lark CLI" -> Icons.Outlined.Terminal
        "分支写保护" -> Icons.Outlined.Lock
        "Genbu" -> Icons.Outlined.Sell
        "Meegle", "Meegle 项目" -> Icons.Outlined.Link
        "诊断与日志" -> Icons.AutoMirrored.Outlined.Subject
        else -> Icons.Outlined.Description
    }
}

internal data class PathFieldModifierTargets(
    val row: Modifier,
    val textField: Modifier,
)

internal fun pathFieldModifierTargets(modifier: Modifier): PathFieldModifierTargets =
    PathFieldModifierTargets(row = Modifier, textField = modifier)

@Composable
private fun PathField(
    label: String,
    value: String,
    onValueChange: (String) -> Unit,
    chooseEnabled: Boolean = true,
    modifier: Modifier = Modifier,
    onChoose: () -> Unit,
) {
    val modifierTargets = pathFieldModifierTargets(modifier)
    Row(modifierTargets.row, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedTextField(value, onValueChange, modifierTargets.textField.weight(1f), label = { Text(label) }, singleLine = true, enabled = chooseEnabled)
        OutlinedButton(onClick = onChoose, enabled = chooseEnabled) { Icon(Icons.Outlined.Folder, null); Text("选择") }
    }
}
