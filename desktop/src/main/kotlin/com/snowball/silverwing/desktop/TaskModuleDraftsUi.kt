package com.snowball.silverwing.desktop

import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.ModuleBaseOverride
import com.snowball.silverwing.core.TaskBranchNaming
import com.snowball.silverwing.core.TaskModuleSelection
import com.snowball.silverwing.core.TaskModuleSource
import com.snowball.silverwing.core.TagBuildMode
import com.snowball.silverwing.core.WorkspaceStrategy

internal fun <T> List<T>.replaceAt(index: Int, value: T): List<T> = mapIndexed { current, existing ->
    if (current == index) value else existing
}

internal fun duplicateCloneTargets(modules: List<TaskModuleUiDraft>): List<String> = modules
    .filter { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }
    .map { it.targetBranch.trim() }
    .filter(String::isNotBlank)
    .groupBy(String::lowercase)
    .filterValues { it.size > 1 }
    .values
    .map { it.first() }

private data class TaskModuleBranchOption(
    val id: String,
    val name: String,
    val defaultRef: String,
    val remote: String,
    val strategy: WorkspaceStrategy,
)

internal data class TaskModuleUiDraft(
    val id: String,
    val name: String,
    val strategy: WorkspaceStrategy,
    val baseRef: String,
    val baseRemote: String,
    val targetBranch: String,
    val targetEdited: Boolean = false,
    val source: TaskModuleSource = TaskModuleSource.CONFIGURED,
    val tagEnabled: Boolean = false,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val tagTargetRef: String? = "origin/release/test",
    val tagMessagePrefix: String = "Tag",
) {
    fun toSelection(): TaskModuleSelection = TaskModuleSelection(
        id = id,
        name = name,
        strategy = strategy,
        baseRef = normalizeBaseRefForStrategy(strategy, baseRef),
        baseRemote = baseRemote,
        targetBranch = targetBranch.trim().takeIf(String::isNotBlank),
        source = source,
        tagEnabled = tagEnabled,
        tagMode = tagMode,
        tagTargetRef = tagTargetRef,
        tagMessagePrefix = tagMessagePrefix,
    )
}

internal fun configuredTaskModuleDrafts(service: GroupServiceConfig, taskBranch: String): List<TaskModuleUiDraft> =
    service.modules.map { module ->
        TaskModuleUiDraft(
            id = module.id,
            name = module.name,
            strategy = module.strategy,
            baseRef = module.baseRef,
            baseRemote = module.baseRemote,
            targetBranch = defaultTaskModuleTargetBranch(taskBranch, service, module.id),
            tagEnabled = module.tagEnabled,
            tagMode = module.tagMode,
            tagTargetRef = module.tagTargetRef,
            tagMessagePrefix = module.tagMessagePrefix,
        )
    }

internal fun retargetUntouchedModules(modules: List<TaskModuleUiDraft>, taskBranch: String): List<TaskModuleUiDraft> =
    modules.map { module ->
        if (module.targetEdited) module else module.copy(
            targetBranch = if (modules.size == 1) taskBranch else "$taskBranch-${module.name.trim().ifBlank { module.id }}",
        )
    }

/** Applies a task-branch change to every selected service without overwriting explicit module edits. */
internal fun retargetServiceModuleDrafts(
    draftsByService: Map<String, List<TaskModuleUiDraft>>,
    taskBranch: String,
): Map<String, List<TaskModuleUiDraft>> =
    draftsByService.mapValues { (_, modules) -> retargetUntouchedModules(modules, taskBranch) }

internal fun defaultTaskModuleTargetBranch(
    taskBranch: String,
    service: GroupServiceConfig,
    moduleId: String,
): String = taskModuleTargetBranchDefaults(taskBranch, service).getValue(moduleId)

internal fun taskModuleTargetBranchDefaults(taskBranch: String, service: GroupServiceConfig): Map<String, String> =
    runCatching { TaskBranchNaming.derive(taskBranch, service.modules) }.getOrElse {
        if (service.modules.size == 1) {
            mapOf(service.modules.single().id to taskBranch)
        } else {
            service.modules.associate { module -> module.id to "$taskBranch-${module.name.trim().ifBlank { module.id }}" }
        }
    }

internal fun taskModuleOverrides(
    service: GroupServiceConfig,
    taskBranch: String,
    baseOverrides: Map<String, String>,
    targetOverrides: Map<String, String>,
): List<ModuleBaseOverride> {
    val keyPrefix = "${service.id}::"
    val targetDefaults = taskModuleTargetBranchDefaults(taskBranch, service)
    return service.modules.map { module ->
            val key = "$keyPrefix${module.id}"
            ModuleBaseOverride(
                serviceId = service.id,
                moduleId = module.id,
                baseRef = baseOverrides[key] ?: module.baseRef,
                targetBranch = (targetOverrides[key] ?: targetDefaults.getValue(module.id)).takeIf(String::isNotBlank),
            )
    }
}

@Composable
internal fun TaskTargetBranchField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        modifier = modifier,
        label = { Text(label) },
        singleLine = true,
        colors = branchPickerFieldColors(),
    )
}
