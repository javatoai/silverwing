package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.ArrowDropDown
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.ParticipatedSprint
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.RequirementPerson
import java.math.BigDecimal

internal enum class RequirementsPageLayout { SPLIT, FOCUSED }

internal fun requirementsPageLayout(availableWidthDp: Float): RequirementsPageLayout =
    if (availableWidthDp < 800f) RequirementsPageLayout.FOCUSED else RequirementsPageLayout.SPLIT

internal fun selectedParticipatedWorkItem(
    items: List<ParticipatedWorkItem>,
    selectedKey: String?,
): ParticipatedWorkItem? = items.firstOrNull { it.key == selectedKey } ?: items.firstOrNull()

internal data class ParticipatedWorkItemGroup(
    val type: String,
    val label: String,
    val items: List<ParticipatedWorkItem>,
)

private val participatedWorkItemGroupLabels = linkedMapOf(
    "userstory" to "User Story",
    "technical" to "Tech Improvement",
    "othertask" to "Task",
    "bug" to "Bug",
)

internal fun participatedWorkItemGroups(items: List<ParticipatedWorkItem>): List<ParticipatedWorkItemGroup> {
    val grouped = items.groupBy(ParticipatedWorkItem::type)
    // Types outside the fixed order stay visible instead of silently disappearing from the list.
    val orderedTypes = participatedWorkItemGroupLabels.keys.filter(grouped::containsKey) +
        grouped.keys.filterNot(participatedWorkItemGroupLabels::containsKey)
    return orderedTypes.map { type ->
        val group = grouped.getValue(type)
        ParticipatedWorkItemGroup(type = type, label = participatedWorkItemGroupLabels[type] ?: group.first().typeLabel,
            items = group)
    }
}

internal fun formatRequirementPeople(people: List<RequirementPerson>): String =
    people.mapNotNull { person ->
        person.name.trim().ifBlank { person.email.orEmpty().trim() }.takeIf { it.isNotEmpty() }
    }.joinToString("、").ifBlank { "—" }

internal fun formatParticipatedWorkItemRoles(item: ParticipatedWorkItem): String =
    "研发：${formatRequirementPeople(item.developers)} · 测试：${formatRequirementPeople(item.qcOwners)} · " +
        "产品：${formatRequirementPeople(item.productManagers)}"

internal fun formatMySprintEstimateNumber(days: BigDecimal?): String =
    days?.stripTrailingZeros()?.toPlainString() ?: "—"

internal fun formatMySprintEstimateDays(days: BigDecimal?): String =
    "所选 Sprint 我的估分：" + formatMySprintEstimateNumber(days) + if (days == null) "" else " 天"

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun MySprintEstimateBadge(days: BigDecimal?) {
    val description = formatMySprintEstimateDays(days)
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(description) } },
        state = rememberTooltipState(),
    ) {
        Surface(
            modifier = Modifier.clearAndSetSemantics { contentDescription = description },
            color = MaterialTheme.colorScheme.secondaryContainer,
            contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
            shape = RoundedCornerShape(8.dp),
        ) {
            Text(formatMySprintEstimateNumber(days),
                Modifier.widthIn(min = 28.dp).padding(horizontal = 7.dp, vertical = 3.dp),
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center,
                maxLines = 1, softWrap = false)
        }
    }
}

@Composable
internal fun ParticipatedWorkItemsScreen(
    controller: DesktopApplication,
    onCreateTask: (ParticipatedWorkItem) -> Unit,
) {
    val projects = controller.config.meegleProjects
    val defaultProjectKey = controller.config.meegleDefaultSprintProjectKey
    val catalog = controller.participatedWorkItemsController
    LaunchedEffect(projects, defaultProjectKey) {
        catalog.load(projects, defaultSprintProjectKey = defaultProjectKey)
    }

    if (projects.isEmpty()) {
        EmptyState(
            "尚未配置 Meegle 项目",
            "先在 Meegle CLI 设置中选择要展示的项目。",
            "前往 Meegle CLI 设置",
            controller::openMeegleSettings,
        )
    } else RequirementsContent(controller, catalog, onCreateTask)
}

internal fun requirementsEmptyMessage(state: ParticipatedWorkItemsUiState): String = when {
    state.sprints.isEmpty() -> "没有进行中或未开始的可选 Sprint"
    state.selectedSprintKey == null -> "请选择要查看的 Sprint"
    else -> "该 Sprint 中没有我参与的工作项"
}

@Composable
private fun RequirementsSprintSelector(
    state: ParticipatedWorkItemsUiState,
    onSelect: (ParticipatedSprint) -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember { mutableStateOf(false) }
    val selected = state.sprints.firstOrNull { it.key == state.selectedSprintKey }
    val label = selected?.let { "${it.title} · ${it.status}" }
        ?: if (state.loading) "正在读取 Sprint…" else "选择 Sprint"
    Box(modifier) {
        OutlinedButton(
            onClick = { expanded = true },
            enabled = state.sprints.isNotEmpty(),
            modifier = Modifier.widthIn(max = 520.dp),
        ) {
            Text(label, Modifier.weight(1f, fill = false), maxLines = 1, overflow = TextOverflow.Ellipsis)
            Spacer(Modifier.width(6.dp))
            Icon(Icons.Outlined.ArrowDropDown, "选择 Sprint", Modifier.size(20.dp))
        }
        SilverWingDropdownMenu(
            expanded = expanded,
            onDismissRequest = { expanded = false },
            modifier = Modifier.widthIn(min = 300.dp, max = 520.dp).heightIn(max = 360.dp),
        ) {
            state.sprints.forEach { sprint ->
                DropdownMenuItem(
                    text = {
                        Column {
                            Text(sprint.title)
                            Text("${sprint.projectName} · ${sprint.status}",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    },
                    trailingIcon = {
                        if (sprint.key == state.selectedSprintKey) Icon(Icons.Outlined.Check, "已选择", Modifier.size(18.dp))
                    },
                    onClick = { expanded = false; onSelect(sprint) },
                )
            }
        }
    }
}

@Composable
private fun RequirementsContent(
    controller: DesktopApplication,
    catalog: ParticipatedWorkItemsController,
    onCreateTask: (ParticipatedWorkItem) -> Unit,
) {
    val state = catalog.state
    val selected = selectedParticipatedWorkItem(state.items, catalog.selectedKey)
    var focusedDetail by remember(state.selectedSprintKey) { mutableStateOf(false) }

    LaunchedEffect(state.items, catalog.selectedKey) {
        if (selected != null && selected.key != catalog.selectedKey) catalog.select(selected)
    }

    BoxWithConstraints(
        Modifier.fillMaxSize().padding(
            start = MAIN_CONTENT_START_PADDING_DP.dp,
            end = MAIN_CONTENT_END_PADDING_DP.dp,
            bottom = MAIN_CONTENT_BOTTOM_PADDING_DP.dp,
        ),
    ) {
        when (requirementsPageLayout(maxWidth.value)) {
            RequirementsPageLayout.SPLIT -> Row(Modifier.fillMaxSize(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                RequirementsListSection(
                    controller, catalog, selected?.key, catalog::select,
                    Modifier.width(360.dp).fillMaxHeight(),
                )
                RequirementsDetailPane(controller, catalog, selected, onCreateTask, Modifier.weight(1f).fillMaxHeight())
            }
            RequirementsPageLayout.FOCUSED -> {
                if (focusedDetail && selected != null) {
                    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { focusedDetail = false }) {
                            Icon(Icons.AutoMirrored.Outlined.ArrowBack, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("需求列表")
                        }
                        RequirementsDetailPane(controller, catalog, selected, onCreateTask, Modifier.weight(1f).fillMaxWidth())
                    }
                } else {
                    RequirementsListSection(
                        controller, catalog, selected?.key,
                        onSelect = { item -> catalog.select(item); focusedDetail = true },
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
        }
    }
}

@Composable
private fun RequirementsListSection(
    controller: DesktopApplication,
    catalog: ParticipatedWorkItemsController,
    selectedKey: String?,
    onSelect: (ParticipatedWorkItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state = catalog.state
    Column(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            RequirementsSprintSelector(
                state = state,
                onSelect = {
                    catalog.load(controller.config.meegleProjects, sprintKey = it.key,
                        defaultSprintProjectKey = controller.config.meegleDefaultSprintProjectKey)
                },
                modifier = Modifier.weight(1f),
            )
            ActionIconButton("刷新需求列表", {
                catalog.load(controller.config.meegleProjects, force = true,
                    defaultSprintProjectKey = controller.config.meegleDefaultSprintProjectKey)
            }, enabled = !state.loading) {
                Icon(Icons.Outlined.Refresh, "刷新需求列表", Modifier.size(18.dp))
            }
        }
        if (state.loading) {
            androidx.compose.material3.LinearProgressIndicator(Modifier.fillMaxWidth())
        }
        (state.warning ?: state.error)?.let { message ->
            Surface(
                Modifier.fillMaxWidth(),
                color = MaterialTheme.colorScheme.errorContainer,
                shape = RoundedCornerShape(10.dp),
            ) {
                Column(Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 6.dp),
                    verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(message, color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall)
                    OutlinedButton(onClick = controller::openMeegleSettings) { Text("Meegle 设置") }
                }
            }
        }
        RequirementsListPane(
            items = state.items,
            selectedKey = selectedKey,
            loading = state.loading,
            emptyMessage = requirementsEmptyMessage(state),
            onSelect = onSelect,
            onOpenUrl = controller::openUrl,
            modifier = Modifier.weight(1f).fillMaxWidth(),
        )
    }
}

@Composable
private fun RequirementsGroupHeader(label: String, count: Int, modifier: Modifier = Modifier) {
    Surface(modifier.fillMaxWidth(), color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(8.dp)) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 7.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(label, style = MaterialTheme.typography.titleSmall)
            Spacer(Modifier.weight(1f))
            Text("$count", style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun RequirementsListPane(
    items: List<ParticipatedWorkItem>,
    selectedKey: String?,
    loading: Boolean,
    emptyMessage: String,
    onSelect: (ParticipatedWorkItem) -> Unit,
    onOpenUrl: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(modifier, color = MaterialTheme.colorScheme.surface, shape = RoundedCornerShape(18.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Column {
            if (items.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    if (loading) CircularProgressIndicator()
                    else Text(emptyMessage,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            } else LazyColumn(
                modifier = Modifier.fillMaxSize().padding(8.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                participatedWorkItemGroups(items).forEach { group ->
                    item(key = "requirements-header-${group.type}") {
                        RequirementsGroupHeader(group.label, group.items.size)
                    }
                    items(group.items, key = ParticipatedWorkItem::key) { item ->
                        val selected = item.key == selectedKey
                        Surface(
                            Modifier.fillMaxWidth().clickable { onSelect(item) },
                            color = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface,
                            shape = RoundedCornerShape(12.dp),
                            border = if (selected) BorderStroke(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.30f)) else null,
                        ) {
                            Column(Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
                                verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp),
                                    verticalAlignment = Alignment.Top) {
                                    Text(item.title, Modifier.weight(1f),
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = if (selected) MaterialTheme.colorScheme.onPrimaryContainer
                                            else MaterialTheme.colorScheme.onSurfaceVariant,
                                        maxLines = 2, overflow = TextOverflow.Ellipsis)
                                    Box(Modifier.widthIn(max = 100.dp)) {
                                        RequirementStatusPill(item.status?.takeIf(String::isNotBlank) ?: "未读取")
                                    }
                                    MySprintEstimateBadge(item.mySprintEstimateDays)
                                }
                                Text(item.url, Modifier.clickable { onOpenUrl(item.url) },
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.primary,
                                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun RequirementsDetailPane(
    controller: DesktopApplication,
    catalog: ParticipatedWorkItemsController,
    item: ParticipatedWorkItem?,
    onCreateTask: (ParticipatedWorkItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(modifier, color = MaterialTheme.colorScheme.surface, shape = RoundedCornerShape(18.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        if (item == null) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("选择一个工作项查看正文", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            return@Surface
        }
        Column(Modifier.fillMaxSize()) {
            Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 13.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(
                    Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.Top,
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    Text(item.title, style = MaterialTheme.typography.titleMedium,
                        modifier = Modifier.weight(1f, fill = false))
                    // Optical centering on the title's first line: the button is taller than the line box.
                    PrimaryAddIconButton(
                        "基于该需求创建研发任务",
                        { onCreateTask(item) },
                        size = 28.dp,
                        iconSize = 16.dp,
                        cornerRadius = 9.dp,
                        modifier = Modifier.offset(y = (-2).dp),
                    )
                }
                Text(formatParticipatedWorkItemRoles(item), style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            when (val body = catalog.bodyState) {
                is ParticipatedWorkItemBodyState.Loading -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                is ParticipatedWorkItemBodyState.Ready -> if (body.itemKey == item.key) {
                    if (body.content.isBlank()) Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text("该工作项没有可读取的正文", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    } else MarkdownDocumentPreview(
                        content = body.content,
                        modifier = Modifier.fillMaxSize(),
                        documentKey = item.key,
                        onCopySource = { controller.copyText(body.content, "Markdown 源码已复制") },
                    )
                }
                is ParticipatedWorkItemBodyState.Failed -> if (body.itemKey == item.key) {
                    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center,
                        horizontalAlignment = Alignment.CenterHorizontally) {
                        Text("正文读取失败：${body.message}", color = MaterialTheme.colorScheme.error)
                        Spacer(Modifier.height(8.dp))
                        OutlinedButton(onClick = { catalog.select(item, forceBody = true) }) { Text("重试") }
                    }
                }
                ParticipatedWorkItemBodyState.Idle -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text("正在准备正文预览", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}
