package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Merge
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.*

internal fun workspaceMainBranchMergeActionLabel(source: WorkspaceMainBranch?, workspace: ServiceWorkspace): String =
    source?.let { "合并远程主分支：${it.displayRef} → ${workspace.branch}" } ?: "合并远程主分支（未配置主分支）"

internal fun workspaceMainBranchMergeEnabled(
    task: TaskManifest, workspace: ServiceWorkspace, source: WorkspaceMainBranch?, health: WorkspaceGitHealth?,
    blockedBranches: Collection<String>, busy: Boolean,
): Boolean = !busy && source != null && task.lifecycleStatus == TaskLifecycleStatus.ACTIVE &&
    workspace.health in setOf(WorkspaceHealth.READY, WorkspaceHealth.READY_WITH_WARNINGS) &&
    workspace.targetBranch != null && workspace.targetBranch == workspace.branch && workspace.branch != source.local.branch &&
    !GitWritePolicy(blockedBranches).isBlocked(workspace.branch) &&
    (health == null || health.state == WorkspaceGitHealthState.CHECKING ||
        health.state == WorkspaceGitHealthState.READY && health.actualBranch == workspace.branch ||
        health.issue == WorkspaceGitIssue.OPERATION_IN_PROGRESS)

@Composable
internal fun WorkspaceMainBranchMergeAction(
    label: String, enabled: Boolean, loading: Boolean, onClick: () -> Unit,
) {
    ActionIconButton(label, onClick, Modifier.size(34.dp), enabled = enabled, loading = loading) {
        Icon(Icons.Outlined.Merge, "合并远程主分支", Modifier.size(18.dp))
    }
}

internal fun workspaceMainBranchMergeCopyText(result: WorkspaceMainBranchMergeResult): String = buildString {
    appendLine(result.message)
    appendLine("合并方向：${result.direction}")
    appendLine("工作区：${result.workspacePath}")
    if (result.outcome == WorkspaceMainBranchMergeOutcome.CONFLICT) {
        appendLine("冲突文件：")
        result.conflictFiles.forEach { appendLine(it) }
        append("请在当前任务工作区解决冲突并完成合并，或自行中止本次合并。")
    }
}.trimEnd()

@Composable
internal fun WorkspaceMainBranchMergeResultPanel(
    result: WorkspaceMainBranchMergeResult,
    onCopy: (String) -> Unit,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val conflict = result.outcome == WorkspaceMainBranchMergeOutcome.CONFLICT
    val failure = result.outcome == WorkspaceMainBranchMergeOutcome.FAILED
    val color = if (conflict || failure) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary
    Row(modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        SelectionContainer(Modifier.weight(1f)) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(result.message, style = MaterialTheme.typography.bodySmall, color = color)
                Text(result.direction, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (conflict) {
                    Text("冲突文件：${result.conflictFiles.joinToString("、")}", style = MaterialTheme.typography.bodySmall, color = color)
                    Text("请在当前工作区解决冲突并完成合并，或自行中止后再试。", style = MaterialTheme.typography.bodySmall)
                }
            }
        }
        ActionIconButton(if (conflict) "复制冲突信息" else "复制合并结果", { onCopy(workspaceMainBranchMergeCopyText(result)) }, Modifier.size(30.dp)) {
            Icon(Icons.Outlined.ContentCopy, if (conflict) "复制冲突信息" else "复制合并结果", Modifier.size(16.dp))
        }
        ActionIconButton("关闭合并结果", onDismiss, Modifier.size(30.dp)) {
            Icon(Icons.Outlined.Close, "关闭合并结果", Modifier.size(16.dp))
        }
    }
}
