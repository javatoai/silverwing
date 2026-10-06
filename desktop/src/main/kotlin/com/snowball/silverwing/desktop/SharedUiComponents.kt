package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.CloudUpload
import androidx.compose.material.icons.outlined.Commit
import androidx.compose.material.icons.outlined.Publish
import androidx.compose.material.icons.outlined.Workspaces
import androidx.compose.material3.Button
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.WorkspaceStrategy

@Composable
internal fun EmptyState(
    title: String,
    subtitle: String,
    actionLabel: String? = null,
    action: (() -> Unit)? = null,
) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Surface(
            color = MaterialTheme.colorScheme.surface,
            shape = RoundedCornerShape(20.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        ) {
            Column(
                Modifier.padding(horizontal = 48.dp, vertical = 38.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(11.dp),
            ) {
                Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = RoundedCornerShape(18.dp)) {
                    Icon(Icons.Outlined.Workspaces, null, Modifier.padding(15.dp).size(34.dp), tint = MaterialTheme.colorScheme.primary)
                }
                Text(title, style = MaterialTheme.typography.titleLarge)
                Text(subtitle, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (action != null && actionLabel != null) {
                    Spacer(Modifier.height(4.dp))
                    Button(onClick = action) { Text(actionLabel) }
                }
            }
        }
    }
}

/** Visible hover help for compact desktop icon actions. */
@Composable
@OptIn(ExperimentalMaterial3Api::class)
internal fun ActionIconButton(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    loading: Boolean = false,
    content: @Composable () -> Unit,
) {
    val tooltipState = rememberTooltipState()
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        // Remove dismissed content immediately: a fading popup can cover the next desktop action.
        tooltip = { if (tooltipState.isVisible) PlainTooltip { Text(label) } },
        state = tooltipState,
        focusable = false,
    ) {
        IconButton(onClick = { tooltipState.dismiss(); onClick() }, modifier = modifier.semantics { contentDescription = label }, enabled = enabled && !loading) {
            if (loading) {
                CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            } else {
                content()
            }
        }
    }
}

/**
 * Compact visual grouping for related icon-only desktop actions.
 *
 * Keep the group in shared UI so task-level and workspace-level toolbars present the
 * same affordance instead of each recreating subtly different bordered containers.
 */
@Composable
internal fun IconActionGroup(content: @Composable RowScope.() -> Unit) {
    Surface(
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(12.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Row(
            Modifier.padding(horizontal = 3.dp, vertical = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
            content = content,
        )
    }
}

/** Filled primary "+" affordance so every create-task entry point looks the same. */
@Composable
@OptIn(ExperimentalMaterial3Api::class)
internal fun PrimaryAddIconButton(
    label: String,
    onClick: () -> Unit,
    size: Dp = 36.dp,
    iconSize: Dp = 20.dp,
    cornerRadius: Dp = 12.dp,
    modifier: Modifier = Modifier,
) {
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(label) } },
        state = rememberTooltipState(),
    ) {
        Surface(
            modifier = modifier.size(size),
            color = MaterialTheme.colorScheme.primary,
            shape = RoundedCornerShape(cornerRadius),
        ) {
            IconButton(onClick = onClick, modifier = Modifier.fillMaxSize()) {
                Icon(Icons.Outlined.Add, label, Modifier.size(iconSize), tint = MaterialTheme.colorScheme.onPrimary)
            }
        }
    }
}

/** Shared Git action cluster used wherever a commit/push trio is offered. */
@Composable
internal fun GitActionIconGroup(
    enabled: Boolean,
    scopeLabel: String,
    loading: Boolean = false,
    onCommit: () -> Unit,
    onCommitAndPush: () -> Unit,
    onPush: () -> Unit,
    trailingActions: @Composable () -> Unit = {},
) {
    IconActionGroup {
        ActionIconButton("提交 $scopeLabel", onCommit, Modifier.size(34.dp), enabled, loading) {
            Icon(Icons.Outlined.Commit, "提交", Modifier.size(18.dp))
        }
        ActionIconButton("提交并推送 $scopeLabel", onCommitAndPush, Modifier.size(34.dp), enabled, loading) {
            Icon(Icons.Outlined.Publish, "提交并推送", Modifier.size(18.dp))
        }
        ActionIconButton("推送 $scopeLabel", onPush, Modifier.size(34.dp), enabled, loading) {
            Icon(Icons.Outlined.CloudUpload, "推送", Modifier.size(18.dp))
        }
        trailingActions()
    }
}

@Composable
internal fun StatusPill(text: String) {
    val color = MaterialTheme.colorScheme.statusColor(text)
    Surface(color = color.copy(alpha = 0.12f), shape = RoundedCornerShape(50), border = BorderStroke(1.dp, color.copy(alpha = 0.18f))) {
        Text(
            statusLabel(text),
            Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
            color = color,
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            softWrap = false,
        )
    }
}

private fun statusLabel(status: String): String = when (status) {
    "CREATING" -> "创建中"
    "READY" -> "就绪"
    "READY_WITH_WARNINGS" -> "有警告"
    "FAILED" -> "失败"
    "ARCHIVED" -> "已归档"
    "SUCCESS" -> "成功"
    "CONFLICT" -> "有冲突"
    "PARTIAL" -> "部分完成"
    "CREATED", "PREFLIGHT_PASSED", "SOURCE_BRANCH_PUSHED" -> "构建已中断"
    else -> status
}

internal enum class RequirementStatusCategory { REVIEW, PLANNING, DEVELOPMENT, TESTING, DONE, PAUSED, UNKNOWN }

/** 先识别仍在处理的阶段及否定词，避免“未完成”或“开发完成，待测试”被标为完成。 */
internal fun requirementStatusCategory(status: String): RequirementStatusCategory {
    val normalized = status.trim().lowercase()
    fun matches(vararg values: String) = values.any { it in normalized }
    return when {
        matches("已取消", "取消", "暂停", "挂起", "拒绝", "不做", "终止") -> RequirementStatusCategory.PAUSED
        matches("评审", "待确认") -> RequirementStatusCategory.REVIEW
        matches("提测", "待测试", "测试中", "验收中", "待验收") -> RequirementStatusCategory.TESTING
        matches("开发中", "研发中", "进行中", "实现中", "编码中") || normalized in setOf("开发", "研发") -> RequirementStatusCategory.DEVELOPMENT
        matches("待排期", "排期中", "规划中", "待开始", "未开始", "待开发") -> RequirementStatusCategory.PLANNING
        matches("未完成", "未验收", "未发布", "未关闭", "not done", "not completed", "not resolved", "unresolved", "incomplete", "unfinished", "undone") -> RequirementStatusCategory.UNKNOWN
        matches("已完成", "已验收", "已发布", "已关闭") || normalized.endsWith("完成") ||
            Regex("\\b(done|closed|resolved|completed)\\b").containsMatchIn(normalized) -> RequirementStatusCategory.DONE
        else -> RequirementStatusCategory.UNKNOWN
    }
}

/** 小字号状态文字使用随主题调整的深浅色，浅色背景下不用低对比度的亮橙、亮绿。 */
internal fun ColorScheme.requirementStatusColor(status: String): Color {
    val dark = surface.luminance() < 0.5f
    return when (requirementStatusCategory(status)) {
        RequirementStatusCategory.PLANNING -> if (dark) primary else BrandBlueDark
        RequirementStatusCategory.DEVELOPMENT -> if (dark) tertiary else Color(0xFF5D469F)
        RequirementStatusCategory.REVIEW, RequirementStatusCategory.TESTING -> if (dark) Color(0xFFFBBF24) else Color(0xFF92400E)
        RequirementStatusCategory.DONE -> if (dark) Color(0xFF4ADE80) else Color(0xFF166534)
        RequirementStatusCategory.PAUSED, RequirementStatusCategory.UNKNOWN -> onSurfaceVariant
    }
}

internal fun ColorScheme.requirementFailureColor(): Color =
    if (surface.luminance() < 0.5f) error else Color(0xFFB91C1C)

@Composable
internal fun RequirementStatusPill(status: String) = ColoredRequirementPill(status, MaterialTheme.colorScheme.requirementStatusColor(status))

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun ColoredRequirementPill(text: String, color: Color) {
    TooltipBox(positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(text) } }, state = rememberTooltipState()) {
        Surface(color = color.copy(alpha = 0.12f), shape = RoundedCornerShape(50), border = BorderStroke(1.dp, color.copy(alpha = 0.18f))) {
            Text(text, Modifier.padding(horizontal = 8.dp, vertical = 3.dp), color = color,
                style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, softWrap = false)
        }
    }
}

@Composable
internal fun RequirementStatePill(state: RequirementUiState) {
    when (state) {
        RequirementUiState.NotLoaded -> NeutralRequirementPill("未读取")
        RequirementUiState.Loading -> NeutralRequirementPill("读取中")
        RequirementUiState.Failed -> ColoredRequirementPill("读取失败", MaterialTheme.colorScheme.requirementFailureColor())
        is RequirementUiState.Loaded -> state.metadata.status?.takeIf(String::isNotBlank)?.let { RequirementStatusPill(it) }
            ?: NeutralRequirementPill("未读取")
    }
}

@Composable
private fun NeutralRequirementPill(text: String) {
    val color = MaterialTheme.colorScheme.onSurfaceVariant
    Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(50),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Text(text, Modifier.padding(horizontal = 8.dp, vertical = 3.dp), color = color,
            style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, softWrap = false)
    }
}

@Composable
internal fun MetaPill(text: String) {
    Surface(color = MaterialTheme.colorScheme.surface.copy(alpha = 0.78f), shape = RoundedCornerShape(50)) {
        Text(
            text,
            Modifier.padding(horizontal = 9.dp, vertical = 4.dp),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            softWrap = false,
        )
    }
}

@Composable
internal fun SectionHeader(title: String, modifier: Modifier = Modifier) {
    Text(title, modifier, style = MaterialTheme.typography.titleMedium)
}

@Composable
internal fun MetricCard(title: String, value: String, caption: String, modifier: Modifier = Modifier) {
    Surface(
        modifier,
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(15.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column(Modifier.padding(horizontal = 16.dp, vertical = 14.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(title, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(value, fontSize = 23.sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary)
            Text(caption, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

internal val WorkspaceStrategy.displayName: String
    get() = when (this) {
        WorkspaceStrategy.STANDARD_WORKTREE -> "Worktree"
        WorkspaceStrategy.INDEPENDENT_CLONE -> "独立克隆"
    }

internal val ThemePreference.displayName: String
    get() = when (this) {
        ThemePreference.SYSTEM -> "跟随系统"
        ThemePreference.LIGHT -> "浅色"
        ThemePreference.DARK -> "深色"
    }
