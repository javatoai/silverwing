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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.ParticipatedWorkItem

internal enum class RequirementsPageLayout { SPLIT, FOCUSED }

internal fun requirementsPageLayout(availableWidthDp: Float): RequirementsPageLayout =
    if (availableWidthDp < 800f) RequirementsPageLayout.FOCUSED else RequirementsPageLayout.SPLIT

internal fun filterParticipatedWorkItems(items: List<ParticipatedWorkItem>, query: String): List<ParticipatedWorkItem> {
    val needle = query.trim()
    if (needle.isEmpty()) return items
    return items.filter { item ->
        listOf(item.title, item.typeLabel, item.projectName, item.id, item.url)
            .any { it.contains(needle, ignoreCase = true) }
    }
}

internal fun selectedParticipatedWorkItem(
    items: List<ParticipatedWorkItem>,
    selectedKey: String?,
): ParticipatedWorkItem? = items.firstOrNull { it.key == selectedKey } ?: items.firstOrNull()

@Composable
internal fun ParticipatedWorkItemsScreen(controller: DesktopApplication) {
    val projects = controller.config.meegleProjects
    val catalog = controller.participatedWorkItemsController
    LaunchedEffect(projects) {
        if (projects.isNotEmpty()) catalog.load(projects)
    }

    if (projects.isEmpty()) {
        EmptyState(
            "尚未配置 Meegle 项目",
            "先在 Meegle CLI 设置中选择要展示的项目。",
            "前往 Meegle CLI 设置",
            controller::openMeegleSettings,
        )
    } else RequirementsContent(controller, catalog)
}

@Composable
private fun RequirementsContent(controller: DesktopApplication, catalog: ParticipatedWorkItemsController) {
    val state = catalog.state
    var query by remember { mutableStateOf("") }
    val filtered = filterParticipatedWorkItems(state.items, query)
    val selected = selectedParticipatedWorkItem(filtered, catalog.selectedKey)
    var focusedDetail by remember { mutableStateOf(false) }

    LaunchedEffect(query, state.items, catalog.selectedKey) {
        if (selected != null && selected.key != catalog.selectedKey) catalog.select(selected)
    }

    Column(
        Modifier.fillMaxSize().padding(
            start = MAIN_CONTENT_START_PADDING_DP.dp,
            end = MAIN_CONTENT_END_PADDING_DP.dp,
            bottom = MAIN_CONTENT_BOTTOM_PADDING_DP.dp,
        ),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text("需求列表", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge)
            Text("${state.items.size} 项", style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            ActionIconButton("刷新需求列表", { catalog.load(controller.config.meegleProjects, force = true) },
                enabled = !state.loading) {
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
                Row(Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 6.dp),
                    verticalAlignment = Alignment.CenterVertically) {
                    Text(message, Modifier.weight(1f), color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall)
                    OutlinedButton(onClick = controller::openMeegleSettings) { Text("Meegle 设置") }
                }
            }
        }
        BoxWithConstraints(Modifier.weight(1f).fillMaxWidth()) {
            val layout = requirementsPageLayout(maxWidth.value)
            when (layout) {
                RequirementsPageLayout.SPLIT -> Row(Modifier.fillMaxSize(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    RequirementsListPane(
                        items = filtered,
                        totalCount = state.items.size,
                        query = query,
                        onQueryChange = { query = it },
                        selectedKey = selected?.key,
                        loading = state.loading,
                        onSelect = catalog::select,
                        onOpenUrl = controller::openUrl,
                        modifier = Modifier.width(360.dp).fillMaxHeight(),
                    )
                    RequirementsDetailPane(controller, catalog, selected, Modifier.weight(1f).fillMaxHeight())
                }
                RequirementsPageLayout.FOCUSED -> {
                    if (focusedDetail && selected != null) {
                        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            OutlinedButton(onClick = { focusedDetail = false }) {
                                Icon(Icons.AutoMirrored.Outlined.ArrowBack, null, Modifier.size(18.dp))
                                Spacer(Modifier.width(6.dp))
                                Text("需求列表")
                            }
                            RequirementsDetailPane(controller, catalog, selected, Modifier.weight(1f).fillMaxWidth())
                        }
                    } else {
                        RequirementsListPane(
                            items = filtered,
                            totalCount = state.items.size,
                            query = query,
                            onQueryChange = { query = it },
                            selectedKey = selected?.key,
                            loading = state.loading,
                            onSelect = { item -> catalog.select(item); focusedDetail = true },
                            onOpenUrl = controller::openUrl,
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun RequirementsListPane(
    items: List<ParticipatedWorkItem>,
    totalCount: Int,
    query: String,
    onQueryChange: (String) -> Unit,
    selectedKey: String?,
    loading: Boolean,
    onSelect: (ParticipatedWorkItem) -> Unit,
    onOpenUrl: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(modifier, color = MaterialTheme.colorScheme.surface, shape = RoundedCornerShape(18.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Column {
            Column(Modifier.fillMaxWidth().padding(10.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("我参与的工作项 · ${items.size}/$totalCount", style = MaterialTheme.typography.titleMedium)
                OutlinedTextField(
                    value = query,
                    onValueChange = onQueryChange,
                    modifier = Modifier.fillMaxWidth(),
                    label = { Text("搜索需求") },
                    placeholder = { Text("名称、类型、项目或编号") },
                    singleLine = true,
                )
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            if (items.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    if (loading) CircularProgressIndicator()
                    else Text(if (query.isBlank()) "没有找到参与过的工作项" else "没有匹配的工作项",
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            } else LazyColumn(
                modifier = Modifier.fillMaxSize().padding(8.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                items(items, key = ParticipatedWorkItem::key) { item ->
                    val selected = item.key == selectedKey
                    Surface(
                        Modifier.fillMaxWidth().clickable { onSelect(item) },
                        color = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface,
                        shape = RoundedCornerShape(12.dp),
                        border = if (selected) BorderStroke(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.30f)) else null,
                    ) {
                        Column(Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
                            verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(item.title, style = MaterialTheme.typography.bodyMedium.copy(fontWeight = FontWeight.SemiBold),
                                maxLines = 2, overflow = TextOverflow.Ellipsis)
                            Text("${item.typeLabel} · ${item.projectName} · #${item.id}",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
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

@Composable
private fun RequirementsDetailPane(
    controller: DesktopApplication,
    catalog: ParticipatedWorkItemsController,
    item: ParticipatedWorkItem?,
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
                Text(item.title, style = MaterialTheme.typography.titleMedium)
                Text("${item.typeLabel} · ${item.projectName} · #${item.id}",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(item.url, Modifier.weight(1f).clickable { controller.openUrl(item.url) },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                    ActionIconButton("打开需求链接", { controller.openUrl(item.url) }) {
                        Icon(Icons.AutoMirrored.Outlined.OpenInNew, "打开需求链接", Modifier.size(16.dp))
                    }
                    ActionIconButton("复制需求链接", { controller.copyText(item.url, "需求链接已复制") }) {
                        Icon(Icons.Outlined.ContentCopy, "复制需求链接", Modifier.size(16.dp))
                    }
                }
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
