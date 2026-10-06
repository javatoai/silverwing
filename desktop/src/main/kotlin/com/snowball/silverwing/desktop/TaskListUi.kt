package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.selection.selectable
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.graphics.Color
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowForward
import androidx.compose.material.icons.outlined.Archive
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.Workspaces
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.TaskLifecycleStatus
import com.snowball.silverwing.core.TaskManifest

@Composable
internal fun TasksScreen(controller: DesktopApplication, archived: Boolean) {
    if (controller.needsTaskRoot) {
        EmptyState("请先配置任务根目录", "设置完成后即可创建第一个研发任务", "前往设置") {
            controller.navigation = NavigationItem.SETTINGS
        }
        return
    }
    val visibleTasks = controller.tasks.filter { (it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) == archived }
    LaunchedEffect(archived, visibleTasks.joinToString { "${it.taskDirectoryName}:${it.requirementLink}" }) {
        controller.requirementController.refreshAll()
    }
    val session = controller.taskBrowsingSession
    val selectedTask = controller.selectedTask?.takeIf { (it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) == archived }
    TaskWorkspaceLayout(
        session = session,
        taskName = selectedTask?.folderName,
        listPane = { modifier, onSelected ->
            TaskListPane(controller, visibleTasks, archived,
                onTaskSelected = { controller.selectTask(it); onSelected() },
                onSwitchTaskCategory = { controller.navigation = if (archived) NavigationItem.TASKS else NavigationItem.ARCHIVED },
                modifier = modifier)
        },
        content = { modifier ->
            if (selectedTask == null) TaskListEmptyDetail(archived, modifier)
            else when (session.view) {
                TaskContentView.DETAIL -> TaskDetail(controller, selectedTask, modifier)
                TaskContentView.REQUIREMENT -> TaskRequirementPage(controller, selectedTask, modifier)
                TaskContentView.NOTES -> TaskNotesPage(controller, selectedTask, modifier)
                TaskContentView.MATERIALS -> {
                    if (selectedTask.requirementMaterials.status == com.snowball.silverwing.core.RequirementMaterialsStatus.READY) {
                        RequirementMaterialsBrowser(controller, selectedTask, modifier,
                            browserState = session.materialsFor(controller.taskPath(selectedTask), selectedTask.requirementMaterials.writeRoot))
                    } else TaskMaterialsUnavailable(controller, selectedTask, modifier)
                }
            }
        },
    )
}

@Composable
private fun TaskMaterialsUnavailable(controller: DesktopApplication, task: TaskManifest, modifier: Modifier) {
    Column(modifier.verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        val failed = task.requirementMaterials.status == com.snowball.silverwing.core.RequirementMaterialsStatus.FAILED
        Text(if (failed) "任务资料关联失败" else "尚未关联任务资料", style = MaterialTheme.typography.titleMedium)
        Text(when {
            task.requirementLink.isBlank() -> "该任务没有需求链接，暂时无法关联资料目录。"
            !controller.config.requirementMaterialsConfigured -> "请先在设置中配置任务资料目录。"
            failed -> task.requirementMaterials.failureReason?.takeIf(String::isNotBlank)
                ?: "可以重新关联，也可以切换到其他任务查看资料。"
            else -> "关联资料目录后，即可在这里浏览和预览文件。"
        }, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (task.requirementLink.isBlank()) TextButton(onClick = { controller.taskBrowsingSession.view = TaskContentView.DETAIL }) { Text("查看任务详情") }
        else if (!controller.config.requirementMaterialsConfigured) TextButton(onClick = { controller.navigation = NavigationItem.SETTINGS }) { Text("配置任务资料") }
        else TextButton(onClick = { controller.retryRequirementMaterials(task) }, enabled = !controller.busy) { Text(if (failed) "重试关联" else "关联任务资料") }
    }
}
/**
 * 窄窗口通过抽屉选择任务，让详情和任务资料保持可阅读的宽度。
 */
internal enum class TaskMasterDetailLayout { SPLIT_PANE, FOCUSED_PANE }

internal fun taskMasterDetailLayout(availableContentWidthDp: Float): TaskMasterDetailLayout =
    if (availableContentWidthDp < TASK_MASTER_DETAIL_FOCUSED_MAX_WIDTH_DP) {
        TaskMasterDetailLayout.FOCUSED_PANE
    } else {
        TaskMasterDetailLayout.SPLIT_PANE
    }

@Composable
private fun TaskCategorySwitchButton(
    archived: Boolean,
    destinationTaskCount: Int,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    TextButton(onClick = onClick, modifier = modifier) {
        Icon(
            if (archived) Icons.Outlined.Workspaces else Icons.Outlined.Archive,
            null,
            Modifier.size(18.dp),
        )
        Spacer(Modifier.width(6.dp))
        Text(if (archived) "返回研发任务" else "查看归档 ($destinationTaskCount)")
    }
}

@Composable
private fun TaskListEmptyDetail(archived: Boolean, modifier: Modifier = Modifier) {
    Box(modifier, contentAlignment = Alignment.Center) {
        Text(
            if (archived) "从左侧选择一项已归档任务" else "从左侧任务列表选择任务",
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodyLarge,
        )
    }
}

private fun destinationTaskCount(tasks: List<TaskManifest>, archived: Boolean): Int =
    tasks.count { (it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) != archived }

@Composable
private fun CategoryEmptyMessage(archived: Boolean) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(
            if (archived) "还没有已归档任务" else "还没有研发任务",
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (!archived) {
            Text("请在需求列表中创建任务", color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodySmall)
        }
    }
}

@Composable
private fun TaskListPane(
    controller: DesktopApplication,
    taskItems: List<TaskManifest>,
    archived: Boolean,
    onTaskSelected: (TaskManifest) -> Unit,
    onSwitchTaskCategory: () -> Unit,
    modifier: Modifier,
) {
    Surface(
        modifier,
        color = MaterialTheme.colorScheme.surfaceVariant,
    ) {
        Column(Modifier.fillMaxSize()) {
            // 任务选择是侧栏的主要用途，创建入口统一留在需求列表。
            Box(Modifier.weight(1f).fillMaxWidth().padding(8.dp)) {
                if (taskItems.isEmpty()) {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        CategoryEmptyMessage(archived)
                    }
                } else {
                    TaskList(
                        controller = controller,
                        taskItems = taskItems,
                        archived = archived,
                        modifier = Modifier.fillMaxSize(),
                        onSelect = onTaskSelected,
                    )
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            TaskCategorySwitchButton(
                archived = archived,
                destinationTaskCount = destinationTaskCount(controller.tasks, archived),
                onClick = onSwitchTaskCategory,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 4.dp),
            )
        }
    }
}

// 侧栏保留任务列表和底部归档入口，将主要空间留给任务详情。
internal const val DEFAULT_TASK_LIST_PANE_WIDTH_DP = 240f
internal const val MIN_TASK_LIST_PANE_WIDTH_DP = 160f
internal const val MIN_TASK_DETAIL_PANE_WIDTH_DP = 600f
internal const val TASK_LIST_PANE_HANDLE_WIDTH_DP = 8f
internal const val TASK_MASTER_DETAIL_FOCUSED_MAX_WIDTH_DP = 960f

/** Keeps the saved preferred width intact while a smaller window temporarily clamps its display. */
internal fun resolveTaskListPaneWidth(preferredWidthDp: Float?, availableWidthDp: Float): Float {
    val maximumWidth = (availableWidthDp - TASK_LIST_PANE_HANDLE_WIDTH_DP - MIN_TASK_DETAIL_PANE_WIDTH_DP)
        .coerceAtLeast(MIN_TASK_LIST_PANE_WIDTH_DP)
    return (preferredWidthDp ?: DEFAULT_TASK_LIST_PANE_WIDTH_DP)
        .coerceIn(MIN_TASK_LIST_PANE_WIDTH_DP, maximumWidth)
}

/**
 * The sidebar already provides a visual boundary, so task content only needs a
 * small gutter before its own outlined cards begin. Keeping this at 8dp also
 * gives the detail pane back useful width on a Mac window.
 */
internal fun taskScreenHorizontalPadding(): Float = MAIN_CONTENT_START_PADDING_DP

@Composable
private fun TaskList(
    controller: DesktopApplication,
    taskItems: List<TaskManifest>,
    archived: Boolean,
    modifier: Modifier,
    onSelect: (TaskManifest) -> Unit,
) {
    val state = controller.taskBrowsingSession.taskIndexFor(archived)
    val expanded = state.expandedGroups
    CompositionLocalProvider(LocalMaterialsReadingState provides state.position) {
    LazyColumn(modifier, state = rememberMaterialsLazyListState("tasks"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        if (controller.config.groups.size == 1) {
            items(taskItems, key = { it.folderName }) { TaskCard(controller, it, it == controller.selectedTask, onSelect) }
        } else {
            controller.config.groups.forEach { group ->
                val grouped = taskItems.filter { it.groupId == group.id }
                item(key = "header-${group.id}") {
                    GroupHeader(group.name, grouped.size, expanded[group.id] != false) {
                        expanded[group.id] = expanded[group.id] == false
                    }
                }
                if (expanded[group.id] != false) items(grouped, key = { "${group.id}-${it.folderName}" }) {
                    TaskCard(controller, it, it == controller.selectedTask, onSelect)
                }
            }
        }
    }
    }
}

@Composable
private fun GroupHeader(name: String, count: Int, expanded: Boolean, onToggle: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onToggle).padding(horizontal = 8.dp, vertical = 9.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(8.dp)) {
            Icon(if (expanded) Icons.Outlined.KeyboardArrowDown else Icons.AutoMirrored.Outlined.ArrowForward, null, Modifier.padding(3.dp).size(16.dp))
        }
        Spacer(Modifier.width(9.dp))
        Text(name, style = MaterialTheme.typography.titleSmall)
        Spacer(Modifier.weight(1f))
        Text("$count", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun TaskCard(controller: DesktopApplication, task: TaskManifest, selected: Boolean, onSelect: (TaskManifest) -> Unit) {
    val shape = RoundedCornerShape(8.dp)
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()
    val focused by interaction.collectIsFocusedAsState()
    val containerColor = if (selected) {
        MaterialTheme.colorScheme.primary.copy(alpha = 0.10f)
            .compositeOver(MaterialTheme.colorScheme.surface)
    } else {
        if (hovered) MaterialTheme.colorScheme.primary.copy(alpha = 0.04f).compositeOver(MaterialTheme.colorScheme.surfaceVariant)
        else MaterialTheme.colorScheme.surfaceVariant
    }
    Surface(
        modifier = Modifier.fillMaxWidth().clip(shape)
            .border(1.dp, if (focused) MaterialTheme.colorScheme.primary else Color.Transparent, shape)
            .selectable(selected, interactionSource = interaction, indication = null, role = Role.Button) { onSelect(task) },
        shape = shape,
        color = containerColor,
    ) {
        BoxWithConstraints(Modifier.fillMaxWidth()) {
            val statusMaxWidth = ((maxWidth - 24.dp) / 2f).coerceIn(0.dp, 100.dp)
            Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min).background(containerColor)) {
                if (selected) Surface(Modifier.width(1.dp).fillMaxHeight(), color = MaterialTheme.colorScheme.primary) {}
                Row(Modifier.padding(horizontal = 12.dp, vertical = 8.dp).weight(1f), verticalAlignment = Alignment.CenterVertically) {
                    // 权重放在 Tooltip 外层，保证长名称不会抢占右侧状态的空间。
                    Box(Modifier.weight(1f)) {
                        TooltipText(task.folderName, Modifier.fillMaxWidth(), MaterialTheme.typography.bodyMedium.copy(
                            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal))
                    }
                    if (task.requirementLink.isNotBlank()) {
                        Spacer(Modifier.width(5.dp))
                        // 状态最多占内容的一半，窄列表也为任务名称保留可读空间。
                        Box(Modifier.widthIn(max = statusMaxWidth)) {
                            RequirementStatePill(controller.requirementController.stateFor(task))
                        }
                    }
                }
            }
        }
    }
}
