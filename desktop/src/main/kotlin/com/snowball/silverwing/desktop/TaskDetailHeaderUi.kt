package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskLifecycleStatus
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.WorkspaceGitHealthState
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.health
import com.snowball.silverwing.core.isHttpUrl
import com.snowball.silverwing.core.RequirementReference
import com.snowball.silverwing.core.RequirementMaterialsStatus

internal enum class TaskLifecyclePrimaryAction { ARCHIVE, RESTORE }

internal fun taskLifecyclePrimaryAction(status: TaskLifecycleStatus): TaskLifecyclePrimaryAction =
    if (status == TaskLifecycleStatus.ARCHIVED) TaskLifecyclePrimaryAction.RESTORE else TaskLifecyclePrimaryAction.ARCHIVE

internal data class ParticipantSummary(
    val label: String,
    val details: String,
)

/** 需求链接仍保留可配置的复制图标，项目和分支直接点击文字复制。 */
internal data class TaskAreaCopyIconPresentation(
    val showRequirementCopyIcons: Boolean,
)

/** The former single toggle remains a compatibility fallback for already-saved configurations. */
internal fun taskAreaCopyIconPresentationFor(config: AppConfig): TaskAreaCopyIconPresentation =
    TaskAreaCopyIconPresentation(
        showRequirementCopyIcons = config.showTaskAreaCopyIcons && config.showTaskAreaRequirementCopyIcons,
    )

internal fun participantSummary(role: String, names: List<String>, inlineLimit: Int = 2): ParticipantSummary? {
    val normalized = names.map(String::trim).filter(String::isNotEmpty).distinct()
    if (normalized.isEmpty()) return null
    val details = "$role：${normalized.joinToString("、")}"
    val label = if (normalized.size <= inlineLimit) details else "$role ${normalized.size} 人"
    return ParticipantSummary(label, details)
}

@Composable
internal fun TaskDetailHeader(
    controller: DesktopApplication,
    task: TaskManifest,
    requirementState: RequirementUiState,
    physicalWorkspaces: List<ServiceWorkspace>,
    groupName: String?,
    showGroup: Boolean,
) {
    val abnormalCount = physicalWorkspaces.count { workspace ->
        controller.gitHealth(workspace)?.state in setOf(WorkspaceGitHealthState.MISSING, WorkspaceGitHealthState.FAILED)
    }
    val requirementNumber = task.requirementId ?: RequirementReference.number(task.requirementLink)
    TaskHeaderContent(controller, task, requirementState, groupName, showGroup, abnormalCount,
        requirementNumber, Modifier.fillMaxWidth().padding(2.dp))
}

@Composable
private fun TaskHeaderContent(
    controller: DesktopApplication,
    task: TaskManifest,
    requirementState: RequirementUiState,
    groupName: String?,
    showGroup: Boolean,
    abnormalCount: Int,
    requirementNumber: String?,
    modifier: Modifier,
) {
    val copyIcons = taskAreaCopyIconPresentationFor(controller.config)
    Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        TaskHeaderMetadata(
            task,
            requirementState,
            groupName,
            showGroup,
            abnormalCount,
            onRetryRequirement = { controller.requirementController.refresh(task, force = true) },
            modifier = Modifier.fillMaxWidth(),
        )
        if (task.requirementLink.isNotBlank()) {
            Row(
                Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(5.dp),
            ) {
                TooltipText(
                    text = task.requirementLink,
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (isHttpUrl(task.requirementLink)) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                    onClick = if (isHttpUrl(task.requirementLink)) ({ controller.openUrl(task.requirementLink) }) else null,
                )
                if (copyIcons.showRequirementCopyIcons) {
                    ActionIconButton(
                        label = "复制需求链接",
                        onClick = { controller.copyText(task.requirementLink, "需求链接已复制") },
                        modifier = Modifier.size(30.dp),
                    ) { Icon(Icons.Outlined.ContentCopy, "复制需求链接", Modifier.size(15.dp)) }
                    requirementNumber?.let { number ->
                        ActionIconButton(
                            label = "复制需求编号 $number",
                            onClick = { controller.copyText(number, "需求编号已复制") },
                            modifier = Modifier.size(30.dp),
                        ) { Icon(Icons.Outlined.ContentCopy, "复制需求编号", Modifier.size(15.dp)) }
                    }
                }
            }
        }
        RequirementMaterialsStatusNotice(controller, task)
    }
}

@Composable
private fun RequirementMaterialsStatusNotice(controller: DesktopApplication, task: TaskManifest) {
    when (task.requirementMaterials.status) {
        RequirementMaterialsStatus.NOT_REQUESTED -> {
            Text(
                when {
                    task.requirementLink.isBlank() -> "任务资料未关联：任务缺少需求链接"
                    !controller.config.requirementMaterialsConfigured -> "任务资料未关联：请先配置目录"
                    else -> "任务资料未关联"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        RequirementMaterialsStatus.READY -> Unit
        RequirementMaterialsStatus.FAILED -> {
            Text(
                "任务资料关联失败，请在更多操作中重试",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

@Composable
private fun TaskHeaderMetadata(
    task: TaskManifest,
    requirementState: RequirementUiState,
    groupName: String?,
    showGroup: Boolean,
    abnormalCount: Int,
    onRetryRequirement: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(5.dp)) {
        if (task.requirementLink.isNotBlank()) {
            Row(
                Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                when (requirementState) {
                    RequirementUiState.Loading -> Text(
                        "正在读取需求标题…",
                        modifier = Modifier.weight(1f),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    RequirementUiState.Failed -> Text(
                        "未读取到需求标题",
                        modifier = Modifier.weight(1f),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.error,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    is RequirementUiState.Loaded -> requirementState.metadata.title?.takeIf(String::isNotBlank)?.let { title ->
                        TooltipText(
                            text = title,
                            modifier = Modifier.weight(1f),
                            style = MaterialTheme.typography.titleSmall,
                        )
                    }
                    RequirementUiState.NotLoaded -> Spacer(Modifier.weight(1f))
                }
                if (requirementState == RequirementUiState.Failed) {
                    ActionIconButton("重试读取需求", onRetryRequirement, Modifier.size(30.dp)) {
                        Icon(Icons.Outlined.Refresh, "重试读取需求", Modifier.size(15.dp))
                    }
                }
            }
        }
        FlowRow(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            if (task.health != WorkspaceHealth.READY) StatusPill(task.health.name)
            if (task.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) StatusPill("ARCHIVED")
            if (task.requirementLink.isNotBlank()) {
                (requirementState as? RequirementUiState.Loaded)?.metadata?.participants?.let { participants ->
                    participantSummary("测试", participants.qcOwners.map { it.name })?.let { ParticipantPill(it) }
                    participantSummary("产品", participants.productManagers.map { it.name })?.let { ParticipantPill(it) }
                }
            }
            if (showGroup) MetaPill(groupName ?: task.groupId)
            if (abnormalCount > 0) WorkspaceProblemPill("$abnormalCount 个工作区异常")
        }
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
internal fun TooltipText(
    text: String,
    modifier: Modifier,
    style: TextStyle,
    color: Color = Color.Unspecified,
    onClick: (() -> Unit)? = null,
) {
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(text) } },
        state = rememberTooltipState(),
    ) {
        Text(
            text,
            modifier.then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier),
            style = style,
            color = color,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun ParticipantPill(summary: ParticipantSummary) {
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(summary.details) } },
        state = rememberTooltipState(),
    ) {
        MetaPill(summary.label)
    }
}

@Composable
internal fun WorkspaceProblemPill(text: String) {
    val color = MaterialTheme.colorScheme.error
    Surface(color = color.copy(alpha = 0.10f), shape = RoundedCornerShape(50), border = BorderStroke(1.dp, color.copy(alpha = 0.25f))) {
        Text(
            text,
            Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
            color = color,
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            softWrap = false,
        )
    }
}
