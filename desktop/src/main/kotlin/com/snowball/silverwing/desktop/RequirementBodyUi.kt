package com.snowball.silverwing.desktop

import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.*

/** 从任务链接直接解析正文身份，优先使用已配置项目映射，不加载或筛选需求列表。 */
internal fun taskRequirementItem(task: TaskManifest, config: AppConfig, title: String?): ParticipatedWorkItem? {
    val link = FeishuWorkItemLink.parse(task.requirementLink) ?: return null
    val project = config.meegleProjects.firstOrNull { it.simpleName.equals(link.space, ignoreCase = true) }
    val projectKey = project?.projectKey ?: link.projectKey ?: return null
    return ParticipatedWorkItem(projectKey, project?.simpleName ?: link.space, link.kind, link.kind,
        link.workItemId, title?.takeIf(String::isNotBlank) ?: task.folderName, task.requirementLink.trim())
}

@Composable
internal fun TaskRequirementPage(controller: DesktopApplication, task: TaskManifest, modifier: Modifier = Modifier) {
    val metadata = controller.requirementController.loadedMetadataFor(task)
    val item = taskRequirementItem(task, controller.config, metadata?.title)
    val reader = controller.taskRequirementBodyController
    val taskKey = controller.taskPath(task)
    val position = controller.taskBrowsingSession.requirementFor(taskKey, task.requirementLink)
    DisposableEffect(taskKey, task.requirementLink, item?.key) {
        reader.clear()
        if (item != null) reader.select(item)
        onDispose { reader.clear() }
    }
    Column(modifier.fillMaxSize()) {
        Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(item?.title ?: metadata?.title ?: task.folderName, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                if (item != null) ActionIconButton("刷新需求正文", { reader.select(item, force = true) }, Modifier.size(32.dp),
                    enabled = reader.bodyState !is ParticipatedWorkItemBodyState.Loading) {
                    Icon(Icons.Outlined.Refresh, "刷新需求正文", Modifier.size(18.dp))
                }
            }
            if (task.requirementLink.isNotBlank()) TooltipText(task.requirementLink, Modifier.fillMaxWidth(),
                MaterialTheme.typography.bodySmall, MaterialTheme.colorScheme.primary,
                onClick = { controller.openUrl(task.requirementLink) })
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        if (item == null) Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
            Text(if (task.requirementLink.isBlank()) "该任务尚未填写需求链接" else "当前需求链接无法内置读取，请点击上方链接在浏览器中查看",
                Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
        } else key(taskKey, task.requirementLink) {
            CompositionLocalProvider(LocalMaterialsReadingState provides position) {
                RequirementBodyContent(controller, reader, item, Modifier.weight(1f).fillMaxWidth())
            }
        }
    }
}

/** 需求列表与任务正文使用同一阅读组件，图片错误独立于正文显示。 */
@Composable
internal fun RequirementBodyContent(
    controller: DesktopApplication,
    reader: RequirementBodyController,
    item: ParticipatedWorkItem,
    modifier: Modifier = Modifier,
) {
    // Refreshing the same work item replaces its body and images; close a painter
    // from the previous body rather than leaving it over the loading/failed page.
    var viewingImage by remember(item.key, reader.bodyState) { mutableStateOf<androidx.compose.ui.graphics.painter.Painter?>(null) }
    viewingImage?.let { RequirementImageViewer(it) { viewingImage = null } }
    when (val body = reader.bodyState) {
        is ParticipatedWorkItemBodyState.Ready -> if (body.itemKey == item.key) {
            if (body.content.isBlank()) Box(modifier, contentAlignment = Alignment.Center) {
                Text("该工作项没有可读取的正文", color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                val images = reader.bodyImageStates.mapNotNull { (url, state) ->
                    (state as? ParticipatedWorkItemImageState.Loaded)?.let { url to it.path }
                }.toMap()
                val pending = reader.bodyImageStates.count { it.value is ParticipatedWorkItemImageState.Loading }
                val failures = reader.bodyImageStates.filterValues { it is ParticipatedWorkItemImageState.Failed }
                val failureScroll = rememberScrollState()
                BoxWithConstraints(modifier) {
                    val failureHeight = minOf(160.dp, maxHeight * 0.4f)
                    Column(Modifier.fillMaxSize()) {
                        if (pending > 0) Row(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 6.dp),
                            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                            Text("正在加载 $pending 张图片", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        if (failures.isNotEmpty()) Column(
                            Modifier.fillMaxWidth().heightIn(max = failureHeight).verticalScroll(failureScroll),
                        ) {
                            failures.forEach { (url, value) ->
                                val state = value as ParticipatedWorkItemImageState.Failed
                                Row(
                                    Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 4.dp),
                                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    Text("图片加载失败：${state.message}", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                                    OutlinedButton(onClick = { reader.retryBodyImage(item, url) }) { Text("重试") }
                                }
                            }
                        }
                        MarkdownDocumentPreview(markdownWithLocalImagePaths(body.content, images), Modifier.weight(1f).fillMaxWidth(),
                            documentKey = item.key, onCopySource = { controller.copyText(body.content, "Markdown 源码已复制") },
                            onCopyCode = { controller.copyText(it, "代码已复制") }, onImageClick = { viewingImage = it })
                    }
                }
            }
        } else Box(modifier)
        is ParticipatedWorkItemBodyState.Failed -> if (body.itemKey == item.key) Column(modifier,
            verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
            Text("正文读取失败：${body.message}", Modifier.padding(16.dp), color = MaterialTheme.colorScheme.error)
            OutlinedButton(onClick = { reader.select(item, force = true) }) { Text("重试") }
        } else Box(modifier)
        else -> Box(modifier, contentAlignment = Alignment.Center) { CircularProgressIndicator() }
    }
}
