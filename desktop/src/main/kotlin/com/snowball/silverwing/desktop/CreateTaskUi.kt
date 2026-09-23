package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material3.Button
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.AgentDocumentPreview
import com.snowball.silverwing.core.BranchPrefixResolver
import com.snowball.silverwing.core.BranchReuseConflict
import com.snowball.silverwing.core.BranchReuseKey
import com.snowball.silverwing.core.CreateGroupedTaskRequest
import com.snowball.silverwing.core.ModuleBaseOverride
import com.snowball.silverwing.core.RemoteBranchRef
import com.snowball.silverwing.core.RequirementMaterialsDirectory
import com.snowball.silverwing.core.RequirementMaterialsResult
import com.snowball.silverwing.core.RequirementMaterialsStatus
import com.snowball.silverwing.core.RequirementDraftState
import com.snowball.silverwing.core.TaskModuleSource
import com.snowball.silverwing.core.TaskNaming
import com.snowball.silverwing.core.TaskServiceSelection
import com.snowball.silverwing.core.WorkspaceStrategy
import java.util.UUID

internal data class TaskInformationLayout(
    val formItemSpacingDp: Int,
    val materialsLineSpacingDp: Int,
)

internal fun taskInformationLayout(): TaskInformationLayout = TaskInformationLayout(
    formItemSpacingDp = 11,
    materialsLineSpacingDp = 2,
)

internal fun taskNameSupportingMessage(error: String?): String? = error

/** 显示短暂的本机 Codex 工作状态，不与表单字段争夺视觉层级。 */
@Composable
private fun AiNamingStatusRow(message: String, loading: Boolean = false) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        if (loading) {
            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Spacer(Modifier.width(8.dp))
        }
        Text(
            message,
            style = MaterialTheme.typography.bodySmall,
            color = if (loading) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
        )
    }
}

internal typealias CreateTaskAction = (String, String, String, List<String>, String, String, List<String>, Set<BranchReuseKey>, List<TaskServiceSelection>) -> Unit

/** Owns the exact draft checked by preflight, including through a reuse-confirmation dialog. */
internal class CreateTaskSubmissionSnapshot(request: CreateGroupedTaskRequest, toolIds: List<String>) {
    val request = request.copy(
        serviceIds = request.serviceIds.toList(),
        serviceSelections = request.serviceSelections.map { it.copy(modules = it.modules.toList()) },
    )
    private val toolIds = toolIds.toList()

    fun submit(keys: Set<BranchReuseKey>, onCreate: CreateTaskAction) = onCreate(
        request.folderName,
        request.featureBranch,
        request.groupId,
        request.serviceIds,
        request.requirementLink,
        request.taskNotes,
        toolIds,
        keys,
        request.serviceSelections,
    )
}

@Composable
internal fun CreateTaskDialog(
    controller: DesktopApplication,
    onDismiss: () -> Unit,
    onCreate: CreateTaskAction,
) {
    val initialGroup = controller.config.groups.first()
    val initialBranchPrefix = remember { controller.config.defaultBranchPrefix }
    val initialDefaultToolIds = remember { controller.config.defaultWorkspaceToolIds.toSet() }
    var draft by remember {
        mutableStateOf(RequirementDraftState(branch = initialBranchPrefix))
    }
    var notes by remember { mutableStateOf("") }
    var selectedTemplateId by remember { mutableStateOf<String?>(null) }
    var pendingTemplate by remember { mutableStateOf<AgentTaskTemplate?>(null) }
    var groupId by remember { mutableStateOf(initialGroup.id) }
    // 只有从候选列表明确选中的需求才能触发自动命名；手工填写链接不应把内容发送给 Codex。
    var aiSelectedRequirementLink by remember { mutableStateOf<String?>(null) }
    var selected by remember { mutableStateOf<Set<String>>(emptySet()) }
    var selectedToolIds by remember { mutableStateOf(initialDefaultToolIds) }
    var rightTab by remember { mutableStateOf("notes") }
    var requirementMenuExpanded by remember { mutableStateOf(false) }
    var requirementSearch by remember { mutableStateOf("") }
    var serviceSearch by remember(groupId) { mutableStateOf("") }
    var confirmDiscard by remember { mutableStateOf(false) }
    var checkingBranchReuse by remember { mutableStateOf(false) }
    var branchConflicts by remember { mutableStateOf<List<BranchReuseConflict>?>(null) }
    var pendingCreation by remember { mutableStateOf<CreateTaskSubmissionSnapshot?>(null) }
    val baseOverrideValues = remember(groupId) { mutableStateMapOf<String, String>() }
    val targetBranchValues = remember(groupId) { mutableStateMapOf<String, String>() }
    val moduleDraftsByService = remember(groupId) { mutableStateMapOf<String, List<TaskModuleUiDraft>>() }
    val group = controller.config.groups.first { it.id == groupId }
    val toolOptions = controller.workspaceToolOptions()
    fun retargetSelectedServiceBranches(taskBranch: String) {
        val updated = retargetServiceModuleDrafts(moduleDraftsByService.toMap(), taskBranch)
        updated.forEach { (serviceId, modules) -> moduleDraftsByService[serviceId] = modules }
    }
    fun updateDraft(updated: RequirementDraftState) {
        val branchChanged = draft.branch != updated.branch
        draft = updated
        if (branchChanged) retargetSelectedServiceBranches(updated.branch)
    }
    fun requestAiNaming(link: String) {
        val branchPrefix = controller.config.defaultBranchPrefix
        controller.requestRequirementAiNaming(
            link = link,
            branchPrefix = branchPrefix,
            enabled = controller.config.aiRequirementNamingEnabled,
        ) { suggestion ->
            // applyAiNaming 会再次核对当前需求及两个手工编辑标记，迟到结果不会覆盖用户修改
            // 或另一个已选需求。
            updateDraft(draft.applyAiNaming(link, suggestion, branchPrefix))
        }
    }
    fun effectiveBaseOverrides(): List<ModuleBaseOverride> = group.services.filter { it.id in selected }.flatMap { service ->
        taskModuleOverrides(service, draft.branch, baseOverrideValues, targetBranchValues)
    }
    fun effectiveSelections(): List<TaskServiceSelection> = group.services.filter { it.id in selected }.map { service ->
        val modules = moduleDraftsByService[service.id] ?: configuredTaskModuleDrafts(service, draft.branch)
        TaskServiceSelection(service.id, retargetUntouchedModules(modules, draft.branch).map(TaskModuleUiDraft::toSelection))
    }
    val taskNameMissing = draft.taskName.isBlank()
    // An untouched create form is incomplete rather than erroneous. Reserve the
    // error treatment for an entered name that cannot become a safe directory.
    val taskNameError = draft.taskName.takeUnless(String::isBlank)
        ?.let(TaskNaming::directoryNameValidationError)
    val unresolvedBranch = BranchPrefixResolver.containsUnresolvedPlaceholder(draft.branch)
    val informationLayout = taskInformationLayout()
    val materialsPreview = controller.requirementMaterialsPreviewState
    val materialsDirectory = (materialsPreview as? RequirementMaterialsPreviewState.Ready)?.let {
        RequirementMaterialsDirectory(
            status = RequirementMaterialsStatus.READY,
            writeRoot = it.path,
        )
    } ?: RequirementMaterialsDirectory()
    val hasDraftChanges = draft.requirementLink.isNotBlank() || draft.taskName.isNotBlank() ||
        draft.branchEdited || notes.isNotBlank() || selected.isNotEmpty() || groupId != initialGroup.id ||
        selectedToolIds != initialDefaultToolIds
    val requestDismiss = {
        // 即使随后会弹出放弃确认，点击关闭也应先作为取消边界；若用户继续编辑，可明确点击“重新生成”。
        controller.cancelRequirementAiNaming()
        controller.taskController.cancelCreateBranchReuseInspection()
        checkingBranchReuse = false
        pendingCreation = null
        branchConflicts = null
        if (hasDraftChanges) confirmDiscard = true else onDismiss()
    }
    var preview by remember { mutableStateOf<AgentDocumentPreview?>(null) }
    var previewLoading by remember { mutableStateOf(false) }
    var previewError by remember { mutableStateOf<String?>(null) }
    val previewSelections = effectiveSelections()
    LaunchedEffect(
        draft.taskName,
        draft.branch,
        groupId,
        selected,
        draft.requirementLink,
        notes,
        previewSelections,
        materialsDirectory,
    ) {
        previewLoading = true
        previewError = null
        preview = null
        try {
            preview = controller.previewAgentsAsync(
                draft.taskName,
                draft.branch,
                groupId,
                selected,
                draft.requirementLink,
                notes,
                previewSelections,
                materialsDirectory,
            )
            previewLoading = false
        } catch (cancelled: kotlinx.coroutines.CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            previewError = error.message ?: error::class.simpleName ?: "无法生成 Agent 文件预览"
            controller.showError(error)
            previewLoading = false
        }
    }
    LaunchedEffect(draft.requirementLink) {
        val requestedLink = draft.requirementLink
        controller.requestRequirementMetadata(requestedLink) { metadata ->
            updateDraft(draft.applyMetadata(requestedLink, metadata))
        }
    }
    LaunchedEffect(
        draft.requirementLink,
        draft.taskName,
        controller.config.requirementMaterialsRoot,
        controller.config.requirementMaterialsSubdirectory,
        controller.config.meegleProjects,
    ) {
        controller.requestRequirementMaterialsPreview(draft.requirementLink, draft.taskName)
    }
    LaunchedEffect(Unit) { controller.requirementController.loadCandidates() }
    DisposableEffect(Unit) {
        onDispose {
            controller.taskController.cancelCreateBranchReuseInspection()
            pendingCreation = null
            controller.cancelRemoteBranchLoads()
            controller.cancelRequirementAiNaming()
            controller.requirementController.clearMaterialsPreview()
        }
    }
    Dialog(onDismissRequest = requestDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(
            Modifier.fillMaxWidth(0.94f).fillMaxHeight(0.90f).widthIn(max = 1540.dp),
            shape = RoundedCornerShape(22.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(Modifier.fillMaxSize()) {
                Surface(color = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.42f)) {
                    Row(Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 18.dp), verticalAlignment = Alignment.CenterVertically) {
                        Surface(color = MaterialTheme.colorScheme.primary, shape = RoundedCornerShape(12.dp)) {
                            Icon(Icons.Outlined.Add, null, Modifier.padding(10.dp), tint = MaterialTheme.colorScheme.onPrimary)
                        }
                        Spacer(Modifier.width(13.dp))
                        Text("创建研发任务", Modifier.weight(1f), style = MaterialTheme.typography.headlineSmall)
                        MetaPill("已选 ${selected.size} 个服务")
                    }
                }
                Row(Modifier.weight(1f).padding(20.dp), horizontalArrangement = Arrangement.spacedBy(18.dp)) {
                    Surface(
                        Modifier.weight(1f).fillMaxHeight(),
                        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.38f),
                        shape = RoundedCornerShape(15.dp),
                    ) {
                    Column(
                        Modifier.fillMaxSize().padding(15.dp).verticalScroll(rememberScrollState()),
                        verticalArrangement = Arrangement.spacedBy(informationLayout.formItemSpacingDp.dp),
                    ) {
                        SectionHeader("任务信息")
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Top) {
                            OutlinedTextField(
                                draft.requirementLink,
                                {
                                    // 手工输入链接只是普通表单编辑，不能触发 AI；同时使上一个候选需求的
                                    // 请求失效，避免迟到结果写入当前草稿。
                                    aiSelectedRequirementLink = null
                                    controller.cancelRequirementAiNaming()
                                    updateDraft(draft.changeRequirement(it, controller.config.defaultBranchPrefix))
                                },
                                Modifier.weight(1f),
                                label = { Text("需求编号或飞书需求链接（可选）") },
                                supportingText = {
                                    when {
                                        draft.requirementTitle != null -> Text(draft.requirementTitle!!)
                                        draft.metadataLoading -> Text("正在读取需求标题…")
                                        draft.metadataHint != null -> Text(draft.metadataHint!!)
                                    }
                                },
                                singleLine = true,
                            )
                            Spacer(Modifier.width(8.dp))
                            Box {
                                OutlinedButton(
                                    onClick = { requirementMenuExpanded = true },
                                    enabled = controller.requirementController.candidates.isNotEmpty(),
                                    modifier = Modifier.padding(top = 8.dp),
                                ) { Text("选择需求") }
                                SilverWingDropdownMenu(
                                    expanded = requirementMenuExpanded,
                                    onDismissRequest = { requirementMenuExpanded = false },
                                    modifier = Modifier.widthIn(min = 520.dp, max = 680.dp),
                                ) {
                                    OutlinedTextField(
                                        requirementSearch,
                                        { requirementSearch = it },
                                        Modifier.fillMaxWidth().padding(horizontal = 8.dp),
                                        label = { Text("搜索需求标题或链接") },
                                        singleLine = true,
                                    )
                                    val matchingLinks = controller.requirementController.candidates.filter {
                                        requirementSearch.isBlank() ||
                                            it.title.contains(requirementSearch, true) ||
                                            it.url.contains(requirementSearch, true)
                                    }
                                    Column(
                                        Modifier.fillMaxWidth().heightIn(max = 360.dp).verticalScroll(rememberScrollState()),
                                    ) {
                                        matchingLinks.forEach { candidate ->
                                            DropdownMenuItem(
                                                text = {
                                                    Column {
                                                        Text(candidate.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                                        Text(
                                                            candidate.url,
                                                            maxLines = 1,
                                                            overflow = TextOverflow.Ellipsis,
                                                            style = MaterialTheme.typography.labelSmall,
                                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                                        )
                                                    }
                                                },
                                                onClick = {
                                                    updateDraft(draft.changeRequirement(candidate.url, controller.config.defaultBranchPrefix, candidate.title))
                                                    aiSelectedRequirementLink = candidate.url
                                                    if (controller.config.aiRequirementNamingEnabled) {
                                                        requestAiNaming(candidate.url)
                                                    }
                                                    requirementMenuExpanded = false
                                                },
                                            )
                                        }
                                    }
                                }
                            }
                        }
                        if (controller.requirementController.candidatesLoading) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                                Spacer(Modifier.width(8.dp))
                                Text("正在拉取飞书需求链接", style = MaterialTheme.typography.bodySmall)
                            }
                        }
                        when (val aiNamingState = controller.requirementAiNamingState) {
                            RequirementAiNamingUiState.Idle -> Unit
                            RequirementAiNamingUiState.LoadingContext -> AiNamingStatusRow(
                                message = "正在读取需求正文…",
                                loading = true,
                            )
                            is RequirementAiNamingUiState.Generating -> AiNamingStatusRow(
                                message = if (aiNamingState.attempt == 1) {
                                    "正在生成文件夹名和分支名…"
                                } else {
                                    "文件夹名已存在，正在生成其他名称…"
                                },
                                loading = true,
                            )
                            is RequirementAiNamingUiState.Ready -> Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(6.dp),
                            ) {
                                Text(
                                    "已生成文件夹名和分支名",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.primary,
                                    modifier = Modifier.weight(1f),
                                )
                                val selectedLink = aiSelectedRequirementLink
                                if (controller.config.aiRequirementNamingEnabled && selectedLink == draft.requirementLink) {
                                    TextButton(onClick = { requestAiNaming(selectedLink) }) { Text("重新生成") }
                                }
                            }
                            is RequirementAiNamingUiState.Failed -> Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(6.dp),
                            ) {
                                Text(
                                    "AI 命名失败：${aiNamingState.reason}",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.error,
                                    modifier = Modifier.weight(1f),
                                )
                                val selectedLink = aiSelectedRequirementLink
                                if (controller.config.aiRequirementNamingEnabled && selectedLink == draft.requirementLink) {
                                    TextButton(onClick = { requestAiNaming(selectedLink) }) { Text("重新生成") }
                                }
                            }
                        }
                        OutlinedTextField(
                            value = draft.taskName,
                            onValueChange = { updateDraft(draft.editName(it)) },
                            modifier = Modifier.fillMaxWidth(),
                            label = { Text("文件夹名称") },
                            placeholder = { Text("例如：PAY-1024 支付订单优化") },
                            isError = taskNameError != null,
                            supportingText = taskNameSupportingMessage(taskNameError)?.let { message ->
                                { Text(message) }
                            },
                            singleLine = true,
                        )
                        when (val state = materialsPreview) {
                            RequirementMaterialsPreviewState.Hidden -> Unit
                            RequirementMaterialsPreviewState.Loading -> Row(verticalAlignment = Alignment.CenterVertically) {
                                CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                                Spacer(Modifier.width(8.dp))
                                Text("正在预检需求资料目录…", style = MaterialTheme.typography.bodySmall)
                            }
                            is RequirementMaterialsPreviewState.Ready -> {
                                val status = if (state.status == RequirementMaterialsResult.Ready.Status.REUSED) {
                                    "将复用"
                                } else {
                                    "预计新建"
                                }
                                Column(verticalArrangement = Arrangement.spacedBy(informationLayout.materialsLineSpacingDp.dp)) {
                                    Text("需求资料目录：$status", style = MaterialTheme.typography.bodySmall)
                                    SelectionContainer {
                                        Text(state.path, style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                            is RequirementMaterialsPreviewState.Failed -> Text(
                                "需求资料目录预检失败（不影响创建）：${state.reason}",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.error,
                            )
                        }
                        OutlinedTextField(
                            value = draft.branch,
                            onValueChange = { updateDraft(draft.editBranch(it)) },
                            modifier = Modifier.fillMaxWidth(),
                            label = { Text("任务分支") },
                            placeholder = { Text("例如：feature/PAY-1024") },
                            supportingText = if (unresolvedBranch) {
                                { Text("需求编号或链接未解析出编号，请补充输入或手工修改分支") }
                            } else null,
                            colors = branchPickerFieldColors(),
                            singleLine = true,
                        )
                        if (controller.config.groups.size > 1) {
                            Spacer(Modifier.height(2.dp))
                            SectionHeader("所属组（创建后不可迁移）")
                            FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                                controller.config.groups.forEach { candidate -> FilterChip(groupId == candidate.id, {
                                    groupId = candidate.id
                                    selected = emptySet()
                                    serviceSearch = ""
                                }, label = { Text(candidate.name) }) }
                            }
                        }
                        Spacer(Modifier.height(2.dp))
                        SectionHeader("选择服务")
                        OutlinedTextField(
                            value = serviceSearch,
                            onValueChange = { serviceSearch = it },
                            modifier = Modifier.fillMaxWidth(),
                            label = { Text("搜索服务") },
                            placeholder = { Text("按服务显示名称搜索") },
                            singleLine = true,
                        )
                        val visibleServices = group.services
                            .filter { it.enabled }
                            .filter { serviceSearch.isBlank() || it.displayName.contains(serviceSearch, ignoreCase = true) }
                        if (visibleServices.isEmpty()) {
                            Text("没有匹配的服务", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        visibleServices.forEach { service ->
                            val checked = service.id in selected
                            fun toggleService() {
                                if (checked) {
                                    selected = selected - service.id
                                } else {
                                    selected = selected + service.id
                                    moduleDraftsByService.putIfAbsent(service.id, configuredTaskModuleDrafts(service, draft.branch))
                                }
                            }
                            OutlinedCard(
                                Modifier.fillMaxWidth().clickable { toggleService() },
                                colors = CardDefaults.outlinedCardColors(
                                    containerColor = if (checked) MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.55f) else MaterialTheme.colorScheme.surface,
                                ),
                                border = BorderStroke(1.dp, if (checked) MaterialTheme.colorScheme.primary.copy(alpha = 0.45f) else MaterialTheme.colorScheme.outlineVariant),
                            ) {
                                Column(Modifier.padding(11.dp)) {
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        Checkbox(checked, { toggleService() })
                                        Column(Modifier.weight(1f)) {
                                            Text(service.displayName, style = MaterialTheme.typography.titleSmall)
                                            Text(
                                                "${service.modules.count { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }} 个 Worktree · ${service.modules.count { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }} 个克隆",
                                                style = MaterialTheme.typography.labelSmall,
                                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            )
                                        }
                                    }
                                    if (checked) {
                                        val serviceModules = moduleDraftsByService[service.id] ?: configuredTaskModuleDrafts(service, draft.branch)
                                        serviceModules.forEachIndexed { index, module ->
                                            OutlinedCard(Modifier.fillMaxWidth().padding(start = 42.dp, top = 7.dp)) {
                                                Column(Modifier.padding(9.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
                                                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                                                        OutlinedTextField(
                                                            module.name,
                                                            { value ->
                                                                val changed = serviceModules.replaceAt(index, module.copy(name = value))
                                                                moduleDraftsByService[service.id] = retargetUntouchedModules(changed, draft.branch)
                                                            },
                                                            Modifier.weight(1f),
                                                            label = { Text("模块名") },
                                                            singleLine = true,
                                                        )
                                                        WorkspaceStrategy.entries.forEach { strategy ->
                                                            FilterChip(
                                                                selected = module.strategy == strategy,
                                                                onClick = {
                                                                    moduleDraftsByService[service.id] = serviceModules.replaceAt(
                                                                        index,
                                                                        module.copy(
                                                                            strategy = strategy,
                                                                            baseRef = normalizeBaseRefForStrategy(strategy, module.baseRef),
                                                                            baseRemote = module.baseRemote,
                                                                        ),
                                                                    )
                                                                },
                                                                label = { Text(strategy.displayName) },
                                                            )
                                                        }
                                                        ActionIconButton("删除模块", {
                                                            val changed = serviceModules.filterIndexed { itemIndex, _ -> itemIndex != index }
                                                            if (changed.isNotEmpty()) moduleDraftsByService[service.id] = retargetUntouchedModules(changed, draft.branch)
                                                        }, enabled = serviceModules.size > 1) { Icon(Icons.Outlined.Delete, null) }
                                                    }
                                                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                                    RemoteNamePicker(
                                                        value = module.baseRemote,
                                                        repositoryId = service.repositoryId,
                                                        controller = controller,
                                                        onSelected = { remote ->
                                                            val branch = module.baseRef.substringAfter('/', module.baseRef)
                                                            moduleDraftsByService[service.id] = serviceModules.replaceAt(index, module.copy(baseRemote = remote, baseRef = "$remote/$branch"))
                                                        },
                                                        modifier = Modifier.width(180.dp),
                                                    )
                                                    RemoteBranchPicker(
                                                        value = module.baseRef,
                                                        onValueChange = { value -> moduleDraftsByService[service.id] = serviceModules.replaceAt(index, module.copy(baseRef = value, baseRemote = value.substringBefore('/', "").ifBlank { module.baseRemote })) },
                                                        label = "${module.name} · 本次主分支",
                                                        repositoryId = service.repositoryId,
                                                        controller = controller,
                                                        modifier = Modifier.weight(1f),
                                                        remote = module.baseRemote,
                                                    )
                                                }
                                                TaskTargetBranchField(
                                                    value = module.targetBranch,
                                                    onValueChange = { value ->
                                                        moduleDraftsByService[service.id] = serviceModules.replaceAt(index, module.copy(targetBranch = value, targetEdited = true))
                                                    },
                                                    label = if (module.strategy == WorkspaceStrategy.STANDARD_WORKTREE) "目标分支（必填）" else "目标分支（可空，空则直接检出主分支）",
                                                    modifier = Modifier.fillMaxWidth(),
                                                )
                                            }
                                            }
                                        }
                                        duplicateCloneTargets(serviceModules).takeIf { it.isNotEmpty() }?.let { duplicates ->
                                            Text(
                                                "共享远程分支风险：多个克隆模块使用相同目标分支 ${duplicates.joinToString()}；请勿并行推送不兼容提交。",
                                                Modifier.padding(start = 42.dp, top = 7.dp),
                                                color = WarningAmber,
                                                style = MaterialTheme.typography.bodySmall,
                                            )
                                        }
                                        FlowRow(Modifier.padding(start = 42.dp, top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                            OutlinedButton(onClick = {
                                                val added = serviceModules + TaskModuleUiDraft(
                                                    id = "module-${UUID.randomUUID()}", name = "module-${serviceModules.size + 1}",
                                                    strategy = WorkspaceStrategy.STANDARD_WORKTREE, baseRef = service.masterBranch, baseRemote = RemoteBranchRef.parse(service.masterBranch).remote,
                                                    targetBranch = "", source = TaskModuleSource.TEMPORARY,
                                                )
                                                moduleDraftsByService[service.id] = retargetUntouchedModules(added, draft.branch)
                                            }) { Text("添加 Worktree") }
                                            OutlinedButton(onClick = {
                                                val added = serviceModules + TaskModuleUiDraft(
                                                    id = "clone-${UUID.randomUUID()}", name = "clone-${serviceModules.size + 1}",
                                                    strategy = WorkspaceStrategy.INDEPENDENT_CLONE, baseRef = service.masterBranch, baseRemote = RemoteBranchRef.parse(service.masterBranch).remote,
                                                    targetBranch = "", source = TaskModuleSource.TEMPORARY,
                                                )
                                                moduleDraftsByService[service.id] = retargetUntouchedModules(added, draft.branch)
                                            }) { Text("添加克隆") }
                                        }
                                    }
                                }
                            }
                        }
                    }
                    }
                    Column(Modifier.weight(1f).fillMaxHeight(), verticalArrangement = Arrangement.spacedBy(9.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            FilterChip(rightTab == "notes", { rightTab = "notes" }, label = { Text("任务人工说明") })
                            Spacer(Modifier.width(8.dp))
                            FilterChip(rightTab == "preview", { rightTab = "preview" }, label = { Text("Agent 文件预览") })
                            Spacer(Modifier.weight(1f))
                            if (rightTab == "preview") MetaPill("实时更新")
                        }
                        if (rightTab == "notes") {
                            val templates = controller.agentTaskTemplates
                            if (templates.isNotEmpty()) {
                                Column(verticalArrangement = Arrangement.spacedBy(5.dp)) {
                                    Text(
                                        "从模板填充（单选）",
                                        style = MaterialTheme.typography.labelMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                    FlowRow(horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                                        templates.forEach { template ->
                                            FilterChip(
                                                selected = selectedTemplateId == template.id,
                                                onClick = {
                                                    val selected = templates.firstOrNull { it.id == selectedTemplateId }
                                                    when (val result = resolveTemplateToggle(notes, selected, template)) {
                                                        is TemplateFillResult.Applied -> {
                                                            notes = result.notes
                                                            selectedTemplateId = result.selectedTemplateId
                                                        }
                                                        is TemplateFillResult.NeedsConfirmation -> pendingTemplate = result.target
                                                    }
                                                },
                                                label = { Text(template.name) },
                                            )
                                        }
                                    }
                                }
                            }
                            OutlinedTextField(
                                notes,
                                { notes = it },
                                Modifier.fillMaxSize(),
                                label = { Text("任务人工说明") },
                                supportingText = { Text("只写入任务级 TASK-RULES.md，创建后仍可在任务详情继续编辑。") },
                            )
                        } else {
                            Surface(
                                Modifier.fillMaxSize(),
                                color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.58f),
                                shape = RoundedCornerShape(14.dp),
                                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
                            ) {
                                when {
                                    previewLoading -> Column(
                                        Modifier.fillMaxSize(),
                                        horizontalAlignment = Alignment.CenterHorizontally,
                                        verticalArrangement = Arrangement.Center,
                                    ) {
                                        CircularProgressIndicator()
                                        Spacer(Modifier.height(8.dp))
                                        Text("正在生成 Agent 文件预览…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    }
                                    previewError != null -> Text(
                                        "预览失败：$previewError",
                                        Modifier.padding(16.dp),
                                        color = MaterialTheme.colorScheme.error,
                                    )
                                    else -> preview?.let { documentPreview ->
                                        MarkdownFileTabsPreview(
                                            files = documentPreview.files.map { MarkdownPreviewFile(it.relativePath, it.content) },
                                            modifier = Modifier.fillMaxSize(),
                                            initialPath = documentPreview.rootFile.relativePath,
                                            onCopySource = { file -> controller.copyText(markdownPreviewSourceCopyPayload(file), "Markdown 源码已复制") },
                                        )
                                    } ?: Text(
                                        "尚未生成 Agent 文件预览",
                                        Modifier.padding(16.dp),
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        }
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Column(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Spacer(Modifier.weight(1f))
                        toolOptions.forEach { tool ->
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Checkbox(
                                    checked = tool.id in selectedToolIds,
                                    onCheckedChange = { checked -> selectedToolIds = if (checked) selectedToolIds + tool.id else selectedToolIds - tool.id },
                                    enabled = tool.available,
                                )
                                Text(if (tool.available) tool.displayName else "${tool.displayName}（不可用）", style = MaterialTheme.typography.bodySmall)
                            }
                        }
                    }
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            when {
                                selected.isEmpty() -> "请选择至少一个服务"
                                unresolvedBranch -> "分支中仍有未解析的 {num}"
                                else -> "将创建 ${selected.size} 个服务入口"
                            },
                            Modifier.weight(1f),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        TextButton(onClick = requestDismiss) { Text("取消") }
                        Spacer(Modifier.width(8.dp))
                        Button(
                            onClick = {
                                val submission = CreateTaskSubmissionSnapshot(
                                    request = CreateGroupedTaskRequest(
                                        folderName = draft.taskName,
                                        featureBranch = draft.branch,
                                        groupId = groupId,
                                        serviceIds = selected.toList(),
                                        requirementLink = draft.requirementLink,
                                        taskNotes = notes,
                                        serviceSelections = effectiveSelections(),
                                    ),
                                    toolIds = selectedToolIds.filter { id -> toolOptions.firstOrNull { it.id == id }?.available == true },
                                )
                                pendingCreation = submission
                                val request = submission.request
                                checkingBranchReuse = controller.taskController.inspectCreateBranchReuse(
                                    name = request.folderName,
                                    branch = request.featureBranch,
                                    groupId = request.groupId,
                                    serviceIds = request.serviceIds,
                                    link = request.requirementLink,
                                    notes = request.taskNotes,
                                    serviceSelections = request.serviceSelections,
                                    onResolved = { conflicts ->
                                        if (pendingCreation === submission) {
                                            if (conflicts.isEmpty()) {
                                                pendingCreation = null
                                                submission.submit(emptySet(), onCreate)
                                            } else {
                                                branchConflicts = conflicts
                                            }
                                        }
                                    },
                                    onFinished = {
                                        checkingBranchReuse = false
                                        if (branchConflicts == null) pendingCreation = null
                                    },
                                )
                                if (!checkingBranchReuse) pendingCreation = null
                            },
                            enabled = !taskNameMissing && taskNameError == null && draft.branch.isNotBlank() &&
                                !unresolvedBranch &&
                                selected.isNotEmpty() && !controller.busy && !checkingBranchReuse,
                        ) { Icon(Icons.Outlined.Add, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("创建任务") }
                    }
                }
            }
        }
    }
    pendingTemplate?.let { template ->
        ConfirmDialog(
            title = "替换任务人工说明？",
            message = "当前说明已被手动修改，应用模板“${template.name}”将替换现有内容。",
            confirmLabel = "替换说明",
            onDismiss = { pendingTemplate = null },
            onConfirm = {
                notes = template.content
                selectedTemplateId = template.id
                pendingTemplate = null
            },
        )
    }
    if (confirmDiscard) {
        DiscardChangesDialog(
            title = "放弃创建任务？",
            message = "已填写的任务信息、服务选择和人工说明将不会保留。",
            onDismiss = { confirmDiscard = false },
            onDiscard = onDismiss,
        )
    }
    branchConflicts?.let { conflicts ->
        BranchReuseConfirmationDialog(
            conflicts = conflicts,
            onDismiss = { branchConflicts = null; pendingCreation = null },
            onConfirm = { keys ->
                branchConflicts = null
                val submission = pendingCreation
                pendingCreation = null
                submission?.submit(keys, onCreate)
            },
        )
    }
}
