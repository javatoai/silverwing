package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.ArrowForward
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.TaskLifecycleStatus
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.health
import java.awt.Cursor
import kotlin.math.roundToInt

@Composable
internal fun TasksScreen(controller: DesktopApplication, archived: Boolean, onCreate: () -> Unit) {
    if (controller.needsTaskRoot) {
        EmptyState("请先配置任务根目录", "设置完成后即可创建第一个研发任务", "前往设置") {
            controller.navigation = NavigationItem.SETTINGS
        }
        return
    }
    val visibleTasks = controller.tasks.filter { (it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) == archived }
    var taskQuery by remember(archived) { mutableStateOf("") }
    fun requirementTitle(task: TaskManifest): String? =
        (controller.requirementController.stateFor(task) as? RequirementUiState.Loaded)?.metadata?.title
    val filteredTasks = filterTasks(
        tasks = visibleTasks,
        query = taskQuery,
        requirementTitle = ::requirementTitle,
    )
    LaunchedEffect(archived, visibleTasks.joinToString { "${it.taskDirectoryName}:${it.requirementLink}" }) {
        controller.requirementController.refreshAll()
    }
    if (visibleTasks.isEmpty()) {
        if (archived) {
            EmptyState("还没有已归档任务", "归档后的任务会保留在这里，可随时恢复。", "返回研发任务") {
                controller.navigation = NavigationItem.TASKS
            }
        } else {
            EmptyState("还没有研发任务", "从已配置的服务创建 Worktree 或独立克隆", "创建第一个任务", onCreate)
        }
        return
    }
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val horizontalPadding = taskScreenHorizontalPadding().dp
        val availableContentWidth = maxWidth.value - horizontalPadding.value * 2
        val layout = taskMasterDetailLayout(availableContentWidth)
        val selectedTask = controller.selectedTask?.takeIf {
            (it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) == archived
        }
        var compactPane by remember(archived) {
            mutableStateOf(if (selectedTask == null) TaskMasterDetailPane.LIST else TaskMasterDetailPane.DETAIL)
        }
        LaunchedEffect(layout, selectedTask?.folderName) {
            if (layout == TaskMasterDetailLayout.FOCUSED_PANE) {
                compactPane = if (selectedTask == null) {
                    TaskMasterDetailPane.LIST
                } else {
                    TaskMasterDetailPane.DETAIL
                }
            }
        }
        val preferredTaskListPaneWidth = remember {
            mutableFloatStateOf(
                WindowPreferences.load().taskListPaneWidth?.toFloat() ?: DEFAULT_TASK_LIST_PANE_WIDTH_DP,
            )
        }
        val displayedTaskListPaneWidth = resolveTaskListPaneWidth(
            preferredWidthDp = preferredTaskListPaneWidth.floatValue,
            availableWidthDp = availableContentWidth,
        ).dp

        when (layout) {
            TaskMasterDetailLayout.SPLIT_PANE -> {
                Row(
                    Modifier.fillMaxSize()
                        .padding(start = horizontalPadding, end = horizontalPadding, bottom = 28.dp),
                ) {
                    TaskListPane(
                        controller = controller,
                        taskItems = filteredTasks,
                        archived = archived,
                        filteredCount = filteredTasks.size,
                        visibleCount = visibleTasks.size,
                        taskQuery = taskQuery,
                        onTaskQueryChange = { taskQuery = it },
                        onTaskSelected = controller::selectTask,
                        onCreate = onCreate,
                        modifier = Modifier.width(displayedTaskListPaneWidth).fillMaxHeight(),
                    )
                    TaskListPaneResizeHandle(
                        onResizeStart = {
                            // A saved wide preference may be temporarily clamped by a narrow window.
                            // Begin a new drag from what the user can actually see.
                            preferredTaskListPaneWidth.floatValue = displayedTaskListPaneWidth.value
                        },
                        onResize = { dragAmount ->
                            preferredTaskListPaneWidth.floatValue = resolveTaskListPaneWidth(
                                preferredWidthDp = preferredTaskListPaneWidth.floatValue + dragAmount,
                                availableWidthDp = availableContentWidth,
                            )
                        },
                        onResizeEnd = {
                            WindowPreferences.saveTaskListPaneWidth(preferredTaskListPaneWidth.floatValue.roundToInt())
                        },
                    )
                    selectedTask?.let {
                        TaskDetail(controller, it, Modifier.weight(1f).fillMaxHeight())
                    }
                }
            }

            TaskMasterDetailLayout.FOCUSED_PANE -> {
                Column(
                    Modifier.fillMaxSize().padding(
                        start = horizontalPadding,
                        end = horizontalPadding,
                        bottom = 16.dp,
                    ),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    if (compactPane == TaskMasterDetailPane.DETAIL && selectedTask != null) {
                        CompactTaskListButton(
                            visibleTaskCount = visibleTasks.size,
                            onClick = { compactPane = TaskMasterDetailPane.LIST },
                            onCreate = if (taskCreateEntryVisible(archived)) onCreate else null,
                        )
                        TaskDetail(
                            controller,
                            selectedTask,
                            Modifier.weight(1f).fillMaxWidth(),
                        )
                    } else {
                        TaskListPane(
                            controller = controller,
                            taskItems = filteredTasks,
                            archived = archived,
                            filteredCount = filteredTasks.size,
                            visibleCount = visibleTasks.size,
                            taskQuery = taskQuery,
                            onTaskQueryChange = { taskQuery = it },
                            onTaskSelected = { task ->
                                controller.selectTask(task)
                                compactPane = TaskMasterDetailPane.DETAIL
                            },
                            onCreate = onCreate,
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
            }
        }
    }
}

/**
 * A narrow Mac window cannot make the application sidebar, a useful task index,
 * and a useful task detail pane fit side by side. In that case this deliberately
 * becomes a focused master/detail view instead of shrinking either pane until it
 * is unreadable.
 */
internal enum class TaskMasterDetailLayout { SPLIT_PANE, FOCUSED_PANE }

internal enum class TaskMasterDetailPane { LIST, DETAIL }

internal fun taskMasterDetailLayout(availableContentWidthDp: Float): TaskMasterDetailLayout =
    if (availableContentWidthDp < TASK_MASTER_DETAIL_FOCUSED_MAX_WIDTH_DP) {
        TaskMasterDetailLayout.FOCUSED_PANE
    } else {
        TaskMasterDetailLayout.SPLIT_PANE
    }

@Composable
private fun CompactTaskListButton(visibleTaskCount: Int, onClick: () -> Unit, onCreate: (() -> Unit)?) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
        OutlinedButton(onClick = onClick) {
            Icon(Icons.AutoMirrored.Outlined.ArrowBack, null, Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text("任务列表（$visibleTaskCount）")
        }
        if (onCreate != null) {
            PrimaryAddIconButton("创建任务", onCreate)
        }
    }
}

@Composable
private fun TaskListPane(
    controller: DesktopApplication,
    taskItems: List<TaskManifest>,
    archived: Boolean,
    filteredCount: Int,
    visibleCount: Int,
    taskQuery: String,
    onTaskQueryChange: (String) -> Unit,
    onTaskSelected: (TaskManifest) -> Unit,
    onCreate: () -> Unit,
    modifier: Modifier,
) {
    Surface(
        modifier,
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(18.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column {
            Column(
                Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 10.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Row(
                    Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        "任务列表",
                        Modifier.weight(1f),
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
                    )
                    Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(20.dp)) {
                        Text("$filteredCount/$visibleCount", Modifier.padding(horizontal = 9.dp, vertical = 4.dp), style = MaterialTheme.typography.labelMedium)
                    }
                    if (taskCreateEntryVisible(archived)) {
                        PrimaryAddIconButton("创建任务", onCreate)
                    }
                }
                OutlinedTextField(
                    taskQuery,
                    onTaskQueryChange,
                    Modifier.fillMaxWidth(),
                    label = { Text("搜索任务") },
                    placeholder = { Text("任务、需求、分支或服务") },
                    singleLine = true,
                )
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            if (taskItems.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text("没有匹配的任务", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            } else {
                TaskList(
                    controller = controller,
                    taskItems = taskItems,
                    archived = archived,
                    modifier = Modifier.fillMaxSize().padding(10.dp),
                    onSelect = onTaskSelected,
                )
            }
        }
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun TaskListPaneResizeHandle(
    onResizeStart: () -> Unit,
    onResize: (Float) -> Unit,
    onResizeEnd: () -> Unit,
) {
    val currentOnResizeStart by rememberUpdatedState(onResizeStart)
    val currentOnResize by rememberUpdatedState(onResize)
    val currentOnResizeEnd by rememberUpdatedState(onResizeEnd)
    val density = LocalDensity.current
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text("拖动调整任务列表宽度") } },
        state = rememberTooltipState(),
    ) {
        Box(
            Modifier
                .width(TASK_LIST_PANE_HANDLE_WIDTH_DP.dp)
                .fillMaxHeight()
                .pointerHoverIcon(PointerIcon(Cursor.getPredefinedCursor(Cursor.E_RESIZE_CURSOR)))
                .pointerInput(density) {
                    detectHorizontalDragGestures(
                        onDragStart = { currentOnResizeStart() },
                        onHorizontalDrag = { change, dragAmount ->
                            change.consume()
                            currentOnResize(with(density) { dragAmount.toDp().value })
                        },
                        onDragEnd = currentOnResizeEnd,
                    )
                },
            contentAlignment = Alignment.Center,
        ) {
            Box(
                Modifier
                    .fillMaxHeight()
                    .padding(vertical = 16.dp)
                    .width(2.dp)
                    .background(MaterialTheme.colorScheme.outlineVariant),
            )
        }
    }
}

// Keep the task index compact enough for Mac windows while leaving the detail
// pane as the primary working surface. 200dp still fits the list title, count,
// create action, search field, and a single-line task title without forcing text
// vertically.
internal const val DEFAULT_TASK_LIST_PANE_WIDTH_DP = 240f
internal const val MIN_TASK_LIST_PANE_WIDTH_DP = 200f
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

internal fun taskCreateEntryVisible(archived: Boolean): Boolean = !archived

@Composable
private fun TaskList(
    controller: DesktopApplication,
    taskItems: List<TaskManifest>,
    archived: Boolean,
    modifier: Modifier,
    onSelect: (TaskManifest) -> Unit,
) {
    val expanded = remember { mutableStateMapOf<String, Boolean>() }
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (controller.config.groups.size == 1) {
            items(taskItems, key = { it.folderName }) { TaskCard(controller, it, it == controller.selectedTask, archived, onSelect) }
        } else {
            controller.config.groups.forEach { group ->
                val grouped = taskItems.filter { it.groupId == group.id }
                item(key = "header-${group.id}") {
                    GroupHeader(group.name, grouped.size, expanded[group.id] != false) {
                        expanded[group.id] = expanded[group.id] == false
                    }
                }
                if (expanded[group.id] != false) items(grouped, key = { "${group.id}-${it.folderName}" }) {
                    TaskCard(controller, it, it == controller.selectedTask, archived, onSelect)
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
private fun TaskCard(controller: DesktopApplication, task: TaskManifest, selected: Boolean, archivedList: Boolean, onSelect: (TaskManifest) -> Unit) {
    val shape = RoundedCornerShape(14.dp)
    val containerColor = if (selected) {
        MaterialTheme.colorScheme.primary.copy(alpha = 0.10f)
            .compositeOver(MaterialTheme.colorScheme.surface)
    } else {
        MaterialTheme.colorScheme.surface
    }
    ElevatedCard(
        modifier = Modifier.fillMaxWidth().clip(shape).clickable { onSelect(task) },
        shape = shape,
        colors = CardDefaults.elevatedCardColors(
            containerColor = containerColor,
        ),
        elevation = CardDefaults.elevatedCardElevation(defaultElevation = if (selected) 0.dp else 1.dp),
    ) {
        Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min).background(containerColor)) {
            if (selected) Surface(Modifier.width(4.dp).fillMaxHeight(), color = MaterialTheme.colorScheme.primary) {}
            Column(Modifier.padding(horizontal = 14.dp, vertical = 12.dp).weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    TooltipText(task.folderName, Modifier.weight(1f), MaterialTheme.typography.bodyMedium.copy(fontWeight = FontWeight.SemiBold))
                    if (!archivedList && task.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) StatusPill("ARCHIVED")
                    if (task.health != WorkspaceHealth.READY) StatusPill(task.health.name)
                    if (task.requirementLink.isNotBlank()) {
                        Spacer(Modifier.width(5.dp))
                        RequirementStatePill(controller.requirementController.stateFor(task))
                    }
                }
            }
        }
    }
}
