package com.snowball.silverwing.desktop

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.AddTaskModulesRequest
import com.snowball.silverwing.core.BranchReuseConflict
import com.snowball.silverwing.core.BranchReuseKey
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.ServiceModuleConfig
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskModuleSource
import com.snowball.silverwing.core.TaskServiceSelection
import com.snowball.silverwing.core.WorkspaceStrategy
import java.util.UUID

/** Explicitly acknowledges reuse before any task directory or worktree is created. */
@Composable
internal fun BranchReuseConfirmationDialog(
    conflicts: List<BranchReuseConflict>,
    onDismiss: () -> Unit,
    onConfirm: (Set<BranchReuseKey>) -> Unit,
) {
    var sharedBranchAcknowledged by remember(conflicts) { mutableStateOf(false) }
    val hasOccupiedWorktree = conflicts.any(BranchReuseConflict::requiresForceAttach)
    val hasLockedWorktree = conflicts.any { it.lockedWorktreePaths.isNotEmpty() }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("确认复用已有分支") },
        text = {
            Column(
                Modifier.widthIn(min = 620.dp).heightIn(max = 520.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Text(
                    "以下分支已存在。确认后会在新任务目录中复用它们，不会创建同名新分支。",
                    style = MaterialTheme.typography.bodyMedium,
                )
                conflicts.forEach { conflict ->
                    OutlinedCard(Modifier.fillMaxWidth()) {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text("${conflict.serviceName} · ${conflict.moduleName}", fontWeight = FontWeight.SemiBold)
                            Text(conflict.key.branch, style = MaterialTheme.typography.bodySmall)
                            if (conflict.localExists) {
                                Text("本地分支已存在", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            if (conflict.remoteRefs.isNotEmpty()) {
                                Text(
                                    "远程分支：${conflict.remoteRefs.joinToString()}",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            conflict.occupiedWorktreePaths.forEach { path ->
                                Text(
                                    "已被 Worktree 检出：$path",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                            conflict.lockedWorktreePaths.forEach { path ->
                                Text(
                                    "该 Worktree 已锁定，需先在 Git 中 unlock：$path",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                        }
                    }
                }
                if (hasOccupiedWorktree) {
                    HorizontalDivider()
                    Text(
                        "已检出的分支将使用 Git 强制附加到新目录。两个目录共享同一分支和提交历史，请不要并行修改。",
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(sharedBranchAcknowledged, { sharedBranchAcknowledged = it })
                        Text("我了解该分支会被两个 Worktree 共享")
                    }
                }
                if (hasLockedWorktree) {
                    Text(
                        "锁定的 Worktree 不能自动清理或强制复用。请先执行 git worktree unlock 后重新预检。",
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
            }
        },
        confirmButton = {
            Button(
                onClick = { onConfirm(conflicts.map(BranchReuseConflict::key).toSet()) },
                enabled = !hasLockedWorktree && (!hasOccupiedWorktree || sharedBranchAcknowledged),
            ) { Text("确认复用") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("返回修改") } },
    )
}

@Composable
internal fun AddTaskServicesDialog(
    controller: DesktopApplication,
    task: TaskManifest,
    onDismiss: () -> Unit,
    onAdd: (List<String>, Set<BranchReuseKey>, List<TaskServiceSelection>) -> Unit,
) {
    val services = controller.addableServices(task)
    var selected by remember(task.folderName) { mutableStateOf<Set<String>>(emptySet()) }
    var serviceSearch by remember(task.folderName) { mutableStateOf("") }
    var checkingBranchReuse by remember(task.folderName) { mutableStateOf(false) }
    var branchConflicts by remember(task.folderName) { mutableStateOf<List<BranchReuseConflict>?>(null) }
    val moduleDraftsByService = remember(task.folderName) { mutableStateMapOf<String, List<TaskModuleUiDraft>>() }
    val visibleServices = filterAddTaskServices(services, serviceSearch)
    fun effectiveSelections(): List<TaskServiceSelection> = services.filter { it.id in selected }.map { service ->
        TaskServiceSelection(
            service.id,
            (moduleDraftsByService[service.id] ?: configuredTaskModuleDrafts(service, task.featureBranch)).map(TaskModuleUiDraft::toSelection),
        )
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("添加服务") },
        text = {
            Column(Modifier.widthIn(min = 560.dp).heightIn(max = 520.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("新增服务沿用任务分支：${task.featureBranch}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedTextField(
                    value = serviceSearch,
                    onValueChange = { serviceSearch = it },
                    modifier = Modifier.fillMaxWidth(),
                    label = { Text("搜索服务") },
                    placeholder = { Text("按服务名或服务 ID 搜索") },
                    singleLine = true,
                )
                if (visibleServices.isEmpty()) {
                    Text("没有匹配的服务", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                visibleServices.forEach { service ->
                    val checked = service.id in selected
                    fun toggleService() {
                        if (checked) selected = selected - service.id else {
                            selected = selected + service.id
                            moduleDraftsByService.putIfAbsent(service.id, configuredTaskModuleDrafts(service, task.featureBranch))
                        }
                    }
                    OutlinedCard(
                        Modifier.fillMaxWidth().clickable { toggleService() },
                        colors = CardDefaults.outlinedCardColors(containerColor = if (checked) MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.5f) else MaterialTheme.colorScheme.surface),
                    ) {
                        Column(Modifier.fillMaxWidth().padding(11.dp)) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Checkbox(checked, { toggleService() })
                                Column(Modifier.weight(1f)) {
                                    Text(service.displayName, fontWeight = FontWeight.SemiBold)
                                    Text("${service.modules.size} 个模块", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                            }
                            if (checked) {
                                val moduleRefs = moduleDraftsByService[service.id] ?: configuredTaskModuleDrafts(service, task.featureBranch)
                                moduleRefs.forEachIndexed { index, module ->
                                    OutlinedCard(Modifier.fillMaxWidth().padding(start = 42.dp, top = 6.dp)) {
                                        Column(Modifier.padding(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                                OutlinedTextField(module.name, { value ->
                                                    moduleDraftsByService[service.id] = retargetUntouchedModules(moduleRefs.replaceAt(index, module.copy(name = value)), task.featureBranch)
                                                }, Modifier.weight(1f), label = { Text("模块名") }, singleLine = true)
                                                WorkspaceStrategy.entries.forEach { strategy ->
                                                    FilterChip(module.strategy == strategy, {
                                                        moduleDraftsByService[service.id] = moduleRefs.replaceAt(
                                                            index,
                                                            module.copy(
                                                                strategy = strategy,
                                                                baseRef = normalizeBaseRefForStrategy(strategy, module.baseRef),
                                                                baseRemote = module.baseRemote,
                                                            ),
                                                        )
                                                    }, label = { Text(strategy.displayName) })
                                                }
                                                ActionIconButton("删除模块", {
                                                    val changed = moduleRefs.filterIndexed { itemIndex, _ -> itemIndex != index }
                                                    if (changed.isNotEmpty()) moduleDraftsByService[service.id] = retargetUntouchedModules(changed, task.featureBranch)
                                                }, enabled = moduleRefs.size > 1) { Icon(Icons.Outlined.Delete, null) }
                                            }
                                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                            RemoteNamePicker(
                                                value = module.baseRemote,
                                                repositoryId = service.repositoryId,
                                                controller = controller,
                                                onSelected = { remote ->
                                                    val branch = module.baseRef.substringAfter('/', module.baseRef)
                                                    moduleDraftsByService[service.id] = moduleRefs.replaceAt(index, module.copy(baseRemote = remote, baseRef = "$remote/$branch"))
                                                },
                                                modifier = Modifier.width(180.dp),
                                            )
                                            RemoteBranchPicker(
                                                value = module.baseRef,
                                                onValueChange = { value -> moduleDraftsByService[service.id] = moduleRefs.replaceAt(index, module.copy(baseRef = value)) },
                                                label = "${module.name} · 本次基础分支",
                                                repositoryId = service.repositoryId,
                                                controller = controller,
                                                modifier = Modifier.weight(1f),
                                                remote = module.baseRemote,
                                            )
                                        }
                                        TaskTargetBranchField(
                                            value = module.targetBranch,
                                            onValueChange = { value ->
                                                moduleDraftsByService[service.id] = moduleRefs.replaceAt(index, module.copy(targetBranch = value, targetEdited = true))
                                            },
                                            label = if (module.strategy == WorkspaceStrategy.STANDARD_WORKTREE) "目标分支（必填）" else "目标分支（可空）",
                                            modifier = Modifier.fillMaxWidth(),
                                        )
                                    }
                                    }
                                }
                                duplicateCloneTargets(moduleRefs).takeIf { it.isNotEmpty() }?.let { duplicates ->
                                    Text(
                                        "共享远程分支风险：多个克隆模块使用相同目标分支 ${duplicates.joinToString()}；请勿并行推送不兼容提交。",
                                        Modifier.padding(start = 42.dp, top = 7.dp),
                                        color = WarningAmber,
                                        style = MaterialTheme.typography.bodySmall,
                                    )
                                }
                                FlowRow(Modifier.padding(start = 42.dp, top = 7.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    OutlinedButton(onClick = {
                                        moduleDraftsByService[service.id] = retargetUntouchedModules(moduleRefs + TaskModuleUiDraft(
                                            id = "module-${UUID.randomUUID()}", name = "module-${moduleRefs.size + 1}", strategy = WorkspaceStrategy.STANDARD_WORKTREE,
                                            baseRef = "origin/master", baseRemote = "origin", targetBranch = "", source = TaskModuleSource.TEMPORARY,
                                        ), task.featureBranch)
                                    }) { Text("添加 Worktree") }
                                    OutlinedButton(onClick = {
                                        moduleDraftsByService[service.id] = retargetUntouchedModules(moduleRefs + TaskModuleUiDraft(
                                            id = "clone-${UUID.randomUUID()}", name = "clone-${moduleRefs.size + 1}", strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                                            baseRef = "origin/master", baseRemote = "origin", targetBranch = "", source = TaskModuleSource.TEMPORARY,
                                        ), task.featureBranch)
                                    }) { Text("添加克隆") }
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    checkingBranchReuse = controller.taskController.inspectAddServicesBranchReuse(
                        task = task,
                        serviceIds = selected.toList(),
                        serviceSelections = effectiveSelections(),
                        onResolved = { conflicts ->
                            if (conflicts.isEmpty()) {
                                onAdd(selected.toList(), emptySet(), effectiveSelections())
                            } else {
                                branchConflicts = conflicts
                            }
                        },
                        onFinished = { checkingBranchReuse = false },
                    )
                },
                enabled = selected.isNotEmpty() && !controller.busy && !checkingBranchReuse,
            ) { Text("添加") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
    branchConflicts?.let { conflicts ->
        BranchReuseConfirmationDialog(
            conflicts = conflicts,
            onDismiss = { branchConflicts = null },
            onConfirm = { keys ->
                branchConflicts = null
                onAdd(selected.toList(), keys, effectiveSelections())
            },
        )
    }
}

internal fun filterAddTaskServices(
    services: List<GroupServiceConfig>,
    query: String,
): List<GroupServiceConfig> {
    val normalizedQuery = query.trim()
    if (normalizedQuery.isEmpty()) return services
    return services.filter { service ->
        service.displayName.contains(normalizedQuery, ignoreCase = true) ||
            service.id.contains(normalizedQuery, ignoreCase = true)
    }
}

@Composable
internal fun AddTaskModuleDialog(
    controller: DesktopApplication,
    task: TaskManifest,
    service: GroupServiceConfig,
    onDismiss: () -> Unit,
) {
    val existingIds = task.services.filter { it.groupServiceId == service.id }.map { it.moduleId.lowercase() }.toSet()
    val availableConfigured = service.modules.filter { it.id.lowercase() !in existingIds }
    fun addedTarget(moduleName: String) = "${task.featureBranch}-${moduleName.trim()}"
    fun configuredDraft(configured: ServiceModuleConfig) =
        configuredTaskModuleDrafts(service.copy(modules = listOf(configured)), task.featureBranch)
            .single()
            .copy(targetBranch = addedTarget(configured.name))
    var module by remember(service.id) {
        mutableStateOf(
            availableConfigured.firstOrNull()?.let(::configuredDraft)
                ?: TaskModuleUiDraft(
                    id = "module-${UUID.randomUUID()}", name = "module", strategy = WorkspaceStrategy.STANDARD_WORKTREE,
                    baseRef = "origin/master", baseRemote = "origin", targetBranch = addedTarget("module"),
                    source = TaskModuleSource.TEMPORARY,
                ),
        )
    }
    var checking by remember { mutableStateOf(false) }
    var conflicts by remember { mutableStateOf<List<BranchReuseConflict>?>(null) }
    fun request(keys: Set<BranchReuseKey> = emptySet()) = AddTaskModulesRequest(service.id, listOf(module.toSelection()), keys)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("为 ${service.displayName} 添加模块") },
        text = {
            Column(Modifier.widthIn(min = 620.dp).heightIn(max = 560.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                if (availableConfigured.isNotEmpty()) {
                    Text("服务配置中尚未使用的模块", style = MaterialTheme.typography.titleSmall)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        availableConfigured.forEach { configured ->
                            FilterChip(
                                selected = module.source == TaskModuleSource.CONFIGURED && module.id == configured.id,
                                onClick = { module = configuredDraft(configured) },
                                label = { Text(configured.name) },
                            )
                        }
                    }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = {
                        module = TaskModuleUiDraft(
                            id = "module-${UUID.randomUUID()}", name = "module", strategy = WorkspaceStrategy.STANDARD_WORKTREE,
                            baseRef = "origin/master", baseRemote = "origin", targetBranch = addedTarget("module"),
                            source = TaskModuleSource.TEMPORARY,
                        )
                    }) { Text("临时 Worktree") }
                    OutlinedButton(onClick = {
                        module = TaskModuleUiDraft(
                            id = "clone-${UUID.randomUUID()}", name = "clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                            baseRef = "origin/master", baseRemote = "origin", targetBranch = addedTarget("clone"),
                            source = TaskModuleSource.TEMPORARY,
                        )
                    }) { Text("临时克隆") }
                }
                OutlinedTextField(module.name, { value ->
                    module = module.copy(
                        name = value,
                        targetBranch = if (module.targetEdited) module.targetBranch else addedTarget(value),
                    )
                }, Modifier.fillMaxWidth(), label = { Text("模块名") }, singleLine = true)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    WorkspaceStrategy.entries.forEach { strategy ->
                        FilterChip(module.strategy == strategy, {
                            module = module.copy(
                                strategy = strategy,
                                baseRef = normalizeBaseRefForStrategy(strategy, module.baseRef),
                                baseRemote = module.baseRemote,
                            )
                        }, label = { Text(strategy.displayName) })
                    }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    RemoteNamePicker(
                        value = module.baseRemote,
                        repositoryId = service.repositoryId,
                        controller = controller,
                        onSelected = { remote ->
                            val branch = module.baseRef.substringAfter('/', module.baseRef)
                            module = module.copy(baseRemote = remote, baseRef = "$remote/$branch")
                        },
                        modifier = Modifier.width(180.dp),
                    )
                    RemoteBranchPicker(module.baseRef, { module = module.copy(baseRef = it) }, "基础分支", service.repositoryId, controller, Modifier.weight(1f), remote = module.baseRemote)
                }
                TaskTargetBranchField(
                    module.targetBranch,
                    { module = module.copy(targetBranch = it, targetEdited = true) },
                    if (module.strategy == WorkspaceStrategy.STANDARD_WORKTREE) "目标分支（必填）" else "目标分支（可空）",
                    Modifier.fillMaxWidth(),
                )
                if (
                    module.strategy == WorkspaceStrategy.INDEPENDENT_CLONE &&
                    module.targetBranch.isNotBlank() &&
                    task.services.any { existing ->
                        existing.strategy == WorkspaceStrategy.INDEPENDENT_CLONE &&
                            existing.targetBranch?.equals(module.targetBranch, ignoreCase = true) == true
                    }
                ) {
                    Text(
                        "共享远程分支风险：任务中已有克隆模块使用该目标分支；请勿并行推送不兼容提交。",
                        color = WarningAmber,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                if (controller.config.tagEnabled && module.source == TaskModuleSource.TEMPORARY) {
                    Text("临时模块默认关闭测试Tag；添加后行为会写入任务快照。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        },
        confirmButton = {
            Button(onClick = {
                checking = controller.taskController.inspectAddModulesBranchReuse(task, request(), { found ->
                    if (found.isEmpty()) controller.taskController.addModules(task, request()) { onDismiss() } else conflicts = found
                }, { checking = false })
            }, enabled = !checking && module.name.isNotBlank() && module.baseRef.isNotBlank() && (module.strategy != WorkspaceStrategy.STANDARD_WORKTREE || module.targetBranch.isNotBlank())) {
                Text("检查并添加")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
    conflicts?.let { found ->
        BranchReuseConfirmationDialog(found, { conflicts = null }) { keys ->
            conflicts = null
            controller.taskController.addModules(task, request(keys)) { onDismiss() }
        }
    }
}
