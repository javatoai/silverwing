package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.HorizontalScrollbar
import androidx.compose.foundation.clickable
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollbarAdapter
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.AccountTree
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Sell
import androidx.compose.material.icons.outlined.MoreHoriz
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.input.pointer.onPointerEvent
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.draw.clip
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.SilverWingTime
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.LocalPushState
import com.snowball.silverwing.core.RemoteBranchRef
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TagBuildMode
import com.snowball.silverwing.core.WorkspaceGitFileChange
import com.snowball.silverwing.core.WorkspaceGitFileChangeKind
import com.snowball.silverwing.core.WorkspaceGitCommit
import com.snowball.silverwing.core.WorkspaceGitFilePreview
import com.snowball.silverwing.core.WorkspaceGitHealth
import com.snowball.silverwing.core.WorkspaceGitHealthState
import com.snowball.silverwing.core.WorkspaceGitIssue
import com.snowball.silverwing.core.WorkspaceCommandConfig
import com.snowball.silverwing.core.WorkspaceFileComparison
import com.snowball.silverwing.core.WorkspaceFileComparisonLine
import com.snowball.silverwing.core.WorkspaceFileComparisonLineKind
import com.snowball.silverwing.core.WorkspaceFileComparisonRow
import com.snowball.silverwing.core.WorkspaceFileContentOrigin
import com.snowball.silverwing.core.WorkspaceFileLanguage
import com.snowball.silverwing.core.WorkspaceFilePreviewMode
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.WorkspaceMainBranchResolver
import com.snowball.silverwing.core.health
import kotlinx.coroutines.CancellationException

private val WorkspaceCardSideActionChromeWidth = 86.dp
internal const val WORKSPACE_CARD_SUMMARY_MIN_WIDTH_DP = 240f
private const val WORKSPACE_CARD_ACTION_BUTTON_WIDTH_DP = 34f
private const val WORKSPACE_CARD_ACTION_GROUP_HORIZONTAL_PADDING_DP = 6f
private const val WORKSPACE_CARD_ACTION_GROUP_SPACING_DP = 8f

internal enum class WorkspaceCardLayout { SIDE_BY_SIDE, STACKED }

internal enum class WorkspaceStatusPlacement { SECOND_ROW, THIRD_ROW }

/** Wide cards place their action groups against the card's right edge. */
internal fun workspaceCardActionsAlignToEnd(layout: WorkspaceCardLayout): Boolean =
    layout == WorkspaceCardLayout.SIDE_BY_SIDE

/** Keeps the project/service identity visible when one service has multiple modules. */
internal fun workspaceCardTitle(serviceName: String, moduleName: String): String {
    val project = serviceName.trim()
    val module = moduleName.trim()
    return when {
        project.isBlank() -> module
        module.isBlank() || module == project -> project
        else -> "$project · $module"
    }
}

/** The title may include a module, but copying the project always uses the service name alone. */
internal fun workspaceProjectNameForCopy(serviceName: String): String = serviceName.trim()

internal data class WorkspaceBranchPresentation(val displayText: String, val copyText: String)

/** Show only the branch name; the workspace strategy belongs to the identity icon. */
internal fun workspaceBranchPresentation(workspace: ServiceWorkspace, actualBranch: String?): WorkspaceBranchPresentation {
    val verifiedBranch = actualBranch?.takeIf(String::isNotBlank)
    val branch = verifiedBranch ?: workspace.branch
    return WorkspaceBranchPresentation(
        displayText = if (verifiedBranch == null) "$branch（未验证）" else branch,
        copyText = branch,
    )
}

internal data class WorkspaceHistoryBaseline(val displayRef: String, val localRef: String)

/** A clone renames its selected source remote to origin, while the task keeps the original master snapshot. */
internal fun workspaceHistoryBaseline(workspace: ServiceWorkspace, serviceMasterBranch: String?): WorkspaceHistoryBaseline? {
    return WorkspaceMainBranchResolver.resolve(workspace, serviceMasterBranch)?.let {
        WorkspaceHistoryBaseline(it.displayRef, it.localRef)
    }
}

internal fun workspaceCardActionGroupButtonCounts(
    presentation: WorkspaceToolbarPresentation,
    customCommandCount: Int,
): List<Int> = buildList {
    if (presentation.showPathActionGroup) add(3)
    if (customCommandCount > 0) add(customCommandCount)
    if (presentation.showGitActionGroup) add(4)
    if (presentation.showTagAction || presentation.showTagTargetAction) {
        add((if (presentation.showTagAction) 1 else 0) + (if (presentation.showTagTargetAction) 1 else 0))
    }
    add(if (presentation.showAddModuleAction) 2 else 1)
    add(1)
    if (presentation.showRetryAction) add(1)
}

/** Width for the visible controls plus a readable summary; measured from the current toolbar. */
internal fun workspaceCardSideBySideMinWidthDp(actionGroupButtonCounts: List<Int>): Float {
    val groupWidths = actionGroupButtonCounts.fold(0f) { totalWidth, buttonCount ->
        totalWidth + WORKSPACE_CARD_ACTION_GROUP_HORIZONTAL_PADDING_DP +
            buttonCount.coerceAtLeast(0) * WORKSPACE_CARD_ACTION_BUTTON_WIDTH_DP
    }
    val groupGaps = (actionGroupButtonCounts.size - 1).coerceAtLeast(0) * WORKSPACE_CARD_ACTION_GROUP_SPACING_DP
    return WorkspaceCardSideActionChromeWidth.value +
        WORKSPACE_CARD_SUMMARY_MIN_WIDTH_DP +
        groupWidths +
        groupGaps
}

internal fun workspaceCardLayout(
    availableWidthDp: Float,
    actionGroupButtonCounts: List<Int>,
): WorkspaceCardLayout = when {
    availableWidthDp >= workspaceCardSideBySideMinWidthDp(actionGroupButtonCounts) -> WorkspaceCardLayout.SIDE_BY_SIDE
    else -> WorkspaceCardLayout.STACKED
}

internal fun workspaceStatusPlacement(health: WorkspaceGitHealth?): WorkspaceStatusPlacement =
    if (health?.state in setOf(WorkspaceGitHealthState.MISSING, WorkspaceGitHealthState.FAILED)) {
        WorkspaceStatusPlacement.THIRD_ROW
    } else {
        WorkspaceStatusPlacement.SECOND_ROW
    }

/** The card exposes one direct development-tool action: the workspace's own selection. */
internal data class WorkspaceToolbarPresentation(
    val showPathActionGroup: Boolean,
    val showGitActionGroup: Boolean,
    val showTagAction: Boolean,
    val showAddModuleAction: Boolean,
    val showRetryAction: Boolean,
    val developmentTool: DevelopmentToolType,
    val showTagTargetAction: Boolean = false,
) {
    val developmentToolActionLabel: String
        get() = "使用 ${developmentTool.displayName} 打开"
}

internal fun workspaceToolbarPresentationFor(
    workspace: ServiceWorkspace,
    canBuildTag: Boolean,
    showAddModule: Boolean,
    config: AppConfig,
): WorkspaceToolbarPresentation = WorkspaceToolbarPresentation(
    showPathActionGroup = config.showWorkspacePathActionGroup,
    showGitActionGroup = config.showWorkspaceGitActionGroup,
    showTagAction = config.tagEnabled && canBuildTag,
    showAddModuleAction = showAddModule,
    showRetryAction = workspace.health == WorkspaceHealth.FAILED && workspace.groupServiceId.isNotBlank(),
    developmentTool = workspace.developmentTool,
    showTagTargetAction = config.tagEnabled && config.allowTaskTagTargetEditing && canBuildTag &&
        workspace.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH,
)

@Composable
internal fun WorkspaceCard(
    controller: DesktopApplication,
    task: TaskManifest,
    workspace: ServiceWorkspace,
    showAddModule: Boolean,
    onAddModule: () -> Unit,
    onDeleteModule: () -> Unit,
) {
    val health = controller.gitHealth(workspace)
    val branchPresentation = workspaceBranchPresentation(workspace, health?.actualBranch)
    val customCommands = controller.workspaceCommands(task, workspace)
    val toolbarPresentation = workspaceToolbarPresentationFor(
        workspace = workspace,
        canBuildTag = controller.canBuildTag(task, workspace),
        showAddModule = showAddModule,
        config = controller.config,
    )
    var commitMode by remember { mutableStateOf<String?>(null) }
    var tagTargetSettings by remember(task.taskDirectoryName, workspace.groupServiceId, workspace.moduleId) {
        mutableStateOf<ServiceWorkspace?>(null)
    }
    LaunchedEffect(toolbarPresentation.showTagTargetAction) {
        if (!toolbarPresentation.showTagTargetAction && tagTargetSettings != null) {
            tagTargetSettings = null
            controller.cancelRemoteBranchLoads()
        }
    }
    var commitMessage by remember(task, workspace) { mutableStateOf(controller.defaultCommitMessage(task, workspace)) }
    OutlinedCard(
        Modifier.fillMaxWidth(),
        // The detail page already has a background. A plain card keeps a workspace distinct
        // without adding another large tinted slab to an otherwise narrow detail pane.
        colors = CardDefaults.outlinedCardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        BoxWithConstraints(Modifier.fillMaxWidth()) {
            val actionGroupButtonCounts = workspaceCardActionGroupButtonCounts(toolbarPresentation, customCommands.size)
            val layout = workspaceCardLayout(maxWidth.value, actionGroupButtonCounts)
            val sideBySideActionMaxWidth =
                (maxWidth - WorkspaceCardSideActionChromeWidth - WORKSPACE_CARD_SUMMARY_MIN_WIDTH_DP.dp)
                    .coerceAtLeast(0.dp)
            when (layout) {
                WorkspaceCardLayout.SIDE_BY_SIDE -> {
                    Row(
                        Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        WorkspaceCardIdentity(workspace, Modifier.align(Alignment.Top))
                        Spacer(Modifier.width(12.dp))
                        WorkspaceCardSummary(
                            controller = controller,
                            task = task,
                            workspace = workspace,
                            health = health,
                            branchPresentation = branchPresentation,
                            modifier = Modifier.weight(1f).align(Alignment.Top),
                        )
                        Spacer(Modifier.width(12.dp))
                        WorkspaceCardActions(
                            controller = controller,
                            task = task,
                            workspace = workspace,
                            customCommands = customCommands,
                            toolbarPresentation = toolbarPresentation,
                            onCommit = { controller.loadBatchGitPreviews(task); commitMessage = controller.defaultCommitMessage(task, workspace); commitMode = "commit" },
                            onCommitAndPush = { controller.loadBatchGitPreviews(task); commitMessage = controller.defaultCommitMessage(task, workspace); commitMode = "commitPush" },
                            onAddModule = onAddModule,
                            canDeleteModule = task.services.size > 1,
                            onDeleteModule = onDeleteModule,
                            onEditTagTarget = { tagTargetSettings = workspace },
                            // The toolbar may use up to the available space, but should shrink
                            // to its actual icon groups so the summary receives the rest.
                            alignToEnd = workspaceCardActionsAlignToEnd(layout),
                            modifier = Modifier.widthIn(max = sideBySideActionMaxWidth),
                        )
                    }
                }

                WorkspaceCardLayout.STACKED -> {
                    Column(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 10.dp)) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Top) {
                            WorkspaceCardIdentity(workspace)
                            Spacer(Modifier.width(12.dp))
                            WorkspaceCardSummary(
                                controller = controller,
                                task = task,
                                workspace = workspace,
                                health = health,
                                branchPresentation = branchPresentation,
                                modifier = Modifier.weight(1f),
                            )
                        }
                        HorizontalDivider(Modifier.padding(top = 12.dp))
                        WorkspaceCardActions(
                            controller = controller,
                            task = task,
                            workspace = workspace,
                            customCommands = customCommands,
                            toolbarPresentation = toolbarPresentation,
                            onCommit = { controller.loadBatchGitPreviews(task); commitMessage = controller.defaultCommitMessage(task, workspace); commitMode = "commit" },
                            onCommitAndPush = { controller.loadBatchGitPreviews(task); commitMessage = controller.defaultCommitMessage(task, workspace); commitMode = "commitPush" },
                            onAddModule = onAddModule,
                            canDeleteModule = task.services.size > 1,
                            onDeleteModule = onDeleteModule,
                            onEditTagTarget = { tagTargetSettings = workspace },
                            alignToEnd = workspaceCardActionsAlignToEnd(layout),
                            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                        )
                    }
                }
            }
        }
    }
    controller.workspaceMainBranchMergeState(task, workspace)?.result?.let { result ->
        WorkspaceMainBranchMergeResultPanel(
            result = result,
            onCopy = { controller.copyText(it, "合并信息已复制") },
            onDismiss = { controller.dismissWorkspaceMainBranchMerge(task, workspace) },
            modifier = Modifier.padding(horizontal = 14.dp, vertical = 8.dp),
        )
    }
    tagTargetSettings?.takeIf { toolbarPresentation.showTagTargetAction }?.let { original ->
        TaskTagTargetDialog(controller, task, original, onDismiss = { tagTargetSettings = null })
    }
    commitMode?.let { mode ->
        val preview = (controller.batchGitPreviewState as? BatchGitPreviewState.Loaded)?.previews?.get(controller.workspaceKey(workspace))
        AlertDialog(
            onDismissRequest = { controller.cancelBatchGitPreviews(); commitMode = null },
            title = { Text(if (mode == "commitPush") "提交并推送" else "提交") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(commitMessage, { commitMessage = it }, Modifier.fillMaxWidth(), label = { Text("提交信息") }, minLines = 3)
                    when (val previewState = controller.batchGitPreviewState) {
                        BatchGitPreviewState.Idle, BatchGitPreviewState.Loading -> Text("正在读取变更预览…")
                        is BatchGitPreviewState.Failed -> SelectionContainer { Text(previewState.message, color = MaterialTheme.colorScheme.error) }
                        is BatchGitPreviewState.Loaded -> if (preview != null) {
                            Text("将提交 ${preview.files.size} 个变更文件", style = MaterialTheme.typography.labelMedium)
                            SelectionContainer { Text(preview.files.take(20).joinToString("\n"), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace) }
                            if (preview.diffStat.isNotBlank()) SelectionContainer { Text(preview.diffStat, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace) }
                        }
                    }
                }
            },
            confirmButton = {
                Button(onClick = {
                    if (controller.commitWorkspace(task, workspace, commitMessage, pushAfter = mode == "commitPush", expectedFingerprint = preview?.fingerprint)) commitMode = null
                }, enabled = commitMessage.isNotBlank() && preview != null && !controller.busy) { Text("确认") }
            },
            dismissButton = { TextButton(onClick = { controller.cancelBatchGitPreviews(); commitMode = null }) { Text("取消") } },
        )
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun WorkspaceCardIdentity(workspace: ServiceWorkspace, modifier: Modifier = Modifier) {
    val strategyName = workspace.strategy.displayName
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(strategyName) } },
        state = rememberTooltipState(),
    ) {
        Surface(modifier, color = MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(10.dp)) {
            Icon(
                if (workspace.strategy == WorkspaceStrategy.STANDARD_WORKTREE) Icons.Outlined.AccountTree else Icons.Outlined.ContentCopy,
                strategyName,
                Modifier.padding(8.dp).size(18.dp),
                tint = MaterialTheme.colorScheme.primary,
            )
        }
    }
}

@Composable
private fun WorkspaceCardSummary(
    controller: DesktopApplication,
    task: TaskManifest,
    workspace: ServiceWorkspace,
    health: WorkspaceGitHealth?,
    branchPresentation: WorkspaceBranchPresentation,
    modifier: Modifier,
) {
    val statusPlacement = workspaceStatusPlacement(health)
    val projectName = workspaceProjectNameForCopy(workspace.serviceName)
    var confirmRerunBootstrap by remember(workspace.worktreePath) { mutableStateOf(false) }
    var showDirtyFiles by remember(workspace.worktreePath) { mutableStateOf(false) }
    var showCommitHistory by remember(task.taskDirectoryName, workspace.worktreePath) { mutableStateOf(false) }
    val dirtyHealth = health?.takeIf(::workspaceGitStatusHasDirtyFiles)
    val historyHealth = health?.takeIf(::workspaceGitStatusHasHistory)
    Column(modifier) {
        Row(
            Modifier.fillMaxWidth().heightIn(min = 28.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Box(Modifier.weight(1f)) {
                WorkspaceCopyableText(
                    text = workspaceCardTitle(workspace.serviceName, workspace.moduleName),
                    copyValue = projectName,
                    actionLabel = "复制项目名",
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1,
                    onCopy = { controller.copyText(it, "项目名已复制") },
                )
            }
            if (workspace.health != WorkspaceHealth.READY) WorkspaceCardStatusPill(workspace.health.name)
        }
        WorkspaceCopyableText(
            text = branchPresentation.displayText,
            copyValue = branchPresentation.copyText,
            actionLabel = "复制分支名",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            onCopy = { controller.copyText(it, "分支已复制") },
        )
        if (statusPlacement == WorkspaceStatusPlacement.SECOND_ROW) {
            WorkspaceGitStatusLine(
                health = health,
                onShowDirtyFiles = { showDirtyFiles = true },
                onShowCommitHistory = { showCommitHistory = true },
            )
        }
        if (statusPlacement == WorkspaceStatusPlacement.THIRD_ROW && health != null) {
            FlowRow(
                Modifier.fillMaxWidth().heightIn(min = 30.dp),
                horizontalArrangement = Arrangement.spacedBy(5.dp),
                verticalArrangement = Arrangement.spacedBy(5.dp),
                itemVerticalAlignment = Alignment.CenterVertically,
            ) {
                WorkspaceCardProblemPill(workspaceIssueLabel(health))
                ActionIconButton(
                    health.message ?: "查看修复方案",
                    { controller.inspectWorkspaceRepair(task, workspace) },
                    Modifier.size(30.dp),
                    enabled = !controller.busy,
                ) { Icon(Icons.Outlined.Build, "修复工作区", Modifier.size(16.dp), tint = MaterialTheme.colorScheme.error) }
            }
        }
        workspaceIssueDetail(health)?.let { detail ->
            SelectionContainer {
                Text(detail, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
        }
        if (workspace.warnings.isNotEmpty()) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                SelectionContainer(Modifier.weight(1f)) {
                    Text(workspace.warnings.joinToString("\n"), color = WarningAmber, style = MaterialTheme.typography.bodySmall)
                }
                ActionIconButton(
                    "重新执行 Bootstrap",
                    { confirmRerunBootstrap = true },
                    Modifier.size(28.dp),
                    enabled = !controller.busy,
                ) { Icon(Icons.Outlined.Refresh, "重新执行 Bootstrap", Modifier.size(15.dp), tint = WarningAmber) }
                ActionIconButton(
                    "清除警告（确认已知晓）",
                    { controller.clearWorkspaceWarnings(task, workspace) },
                    Modifier.size(28.dp),
                    enabled = !controller.busy,
                ) { Icon(Icons.Outlined.Close, "清除警告", Modifier.size(15.dp), tint = WarningAmber) }
            }
        }
        if (controller.config.blockedGitWriteBranches.any { it.equals(branchPresentation.copyText, ignoreCase = true) }) {
            Text(
                "Git 写保护：分支 ${branchPresentation.copyText} 禁止 Commit、Push 和 Commit & Push",
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
    if (confirmRerunBootstrap) {
        ConfirmDialog(
            title = "重新执行 Bootstrap",
            message = "将按当前服务配置对该工作区重新执行 Bootstrap 复制规则与命令；已有文件按规则覆盖，命令会重复执行一次。禁止覆盖的规则会因目标已存在而报警告。",
            confirmLabel = "重新执行",
            enabled = !controller.busy,
            onDismiss = { confirmRerunBootstrap = false },
        ) {
            confirmRerunBootstrap = false
            controller.rerunWorkspaceBootstrap(task, workspace)
        }
    }
    if (showDirtyFiles && dirtyHealth != null) {
        WorkspaceDirtyFilesDialog(
            controller = controller,
            worktreePath = workspace.worktreePath,
            health = dirtyHealth,
            onDismiss = { showDirtyFiles = false },
        )
    }
    if (showCommitHistory && historyHealth != null) {
        val baseline = WorkspaceMainBranchResolver.resolve(controller.config, task, workspace)
        WorkspaceCommitHistoryDialog(
            controller = controller,
            taskKey = task.taskDirectoryName,
            worktreePath = workspace.worktreePath,
            masterBranch = baseline?.let { WorkspaceHistoryBaseline(it.displayRef, it.localRef) },
            onDismiss = { showCommitHistory = false },
        )
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class, androidx.compose.ui.ExperimentalComposeUiApi::class)
private fun WorkspaceCopyableText(
    text: String,
    copyValue: String,
    actionLabel: String,
    style: TextStyle,
    color: Color = MaterialTheme.colorScheme.onSurface,
    maxLines: Int = Int.MAX_VALUE,
    onCopy: (String) -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val hovered by interaction.collectIsHoveredAsState()
    val focused by interaction.collectIsFocusedAsState()
    val inputMode = androidx.compose.ui.platform.LocalInputModeManager.current
    val enabled = copyValue.isNotBlank()
    val shape = MaterialTheme.shapes.extraSmall
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text("点击$actionLabel\n$text") } },
        state = rememberTooltipState(),
    ) {
        // 点击与键盘使用同一复制入口；复制值不包含模块名或“未验证”等显示说明。
        Text(
            text,
            Modifier.clip(shape)
                .onPointerEvent(androidx.compose.ui.input.pointer.PointerEventType.Press) { inputMode.requestInputMode(androidx.compose.ui.input.InputMode.Touch) }
                .border(1.dp, if (focused && inputMode.inputMode == androidx.compose.ui.input.InputMode.Keyboard) MaterialTheme.colorScheme.primary else Color.Transparent, shape)
                .pointerHoverIcon(if (enabled) PointerIcon.Hand else PointerIcon.Default)
                .clickable(interactionSource = interaction, indication = null, enabled = enabled,
                    role = Role.Button, onClickLabel = actionLabel) { onCopy(copyValue) }
                .padding(horizontal = 2.dp, vertical = 2.dp),
            style = style,
            color = if (hovered && enabled) MaterialTheme.colorScheme.primary else color,
            maxLines = maxLines,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun WorkspaceGitStatusLine(
    health: WorkspaceGitHealth?,
    onShowDirtyFiles: () -> Unit,
    onShowCommitHistory: () -> Unit,
) {
    val lineLabels = workspaceGitStatusLineLabels(health)
    val hasHistory = lineLabels.lastOrNull() == "提交历史"
    val labels = if (hasHistory) lineLabels.dropLast(1) else lineLabels
    if (labels.isEmpty()) return
    val hasDirtyFiles = workspaceGitStatusHasDirtyFiles(health)
    val dirtyModifier = if (hasDirtyFiles) {
        Modifier.clickable(onClickLabel = "查看未提交文件", onClick = onShowDirtyFiles)
    } else {
        Modifier
    }
    Row(
        Modifier.fillMaxWidth().padding(top = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            labels.first(),
            modifier = dirtyModifier,
            style = MaterialTheme.typography.labelMedium,
            color = if (hasDirtyFiles) WarningAmber else MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (labels.size > 1) {
            Text(
                " · ",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.outline,
            )
            Text(
                labels.drop(1).joinToString(" · "),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (hasHistory) {
            Text(
                " · ",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.outline,
            )
            Text(
                "提交历史",
                Modifier.clickable(onClickLabel = "查看提交历史", onClick = onShowCommitHistory),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.primary,
                maxLines = 1,
            )
        }
        Spacer(Modifier.weight(1f))
    }
}

internal fun workspaceGitStatusLabels(health: WorkspaceGitHealth?): List<String> = when {
    health == null || health.state == WorkspaceGitHealthState.CHECKING -> listOf("检查中")
    health.state == WorkspaceGitHealthState.READY -> listOf(
        if (health.dirtyFileCount == 0) "无未提交" else "${health.dirtyFileCount} 个文件未提交",
        when (health.pushState) {
            LocalPushState.PUSHED -> "已推送"
            LocalPushState.AHEAD -> "${health.unpushedCommitCount} 个提交未推送"
            LocalPushState.REMOTE_BRANCH_MISSING -> "未发现远程分支"
            LocalPushState.NO_UPSTREAM -> "未关联远程"
            LocalPushState.FAILED -> "检查失败"
        },
    )

    else -> emptyList()
}

internal fun workspaceGitStatusLineLabels(health: WorkspaceGitHealth?): List<String> {
    val labels = workspaceGitStatusLabels(health)
    return if (workspaceGitStatusHasHistory(health)) labels + "提交历史" else labels
}

internal fun workspaceGitStatusHasDirtyFiles(health: WorkspaceGitHealth?): Boolean =
    health?.state == WorkspaceGitHealthState.READY && health.dirtyFileCount > 0

internal fun workspaceGitStatusHasHistory(health: WorkspaceGitHealth?): Boolean =
    health?.state == WorkspaceGitHealthState.READY

internal fun workspaceGitFileChangeLabel(kind: WorkspaceGitFileChangeKind): String = when (kind) {
    WorkspaceGitFileChangeKind.MODIFIED -> "修改"
    WorkspaceGitFileChangeKind.ADDED -> "新增"
    WorkspaceGitFileChangeKind.DELETED -> "删除"
    WorkspaceGitFileChangeKind.RENAMED -> "重命名"
    WorkspaceGitFileChangeKind.COPIED -> "复制"
    WorkspaceGitFileChangeKind.TYPE_CHANGED -> "类型变更"
    WorkspaceGitFileChangeKind.CONFLICTED -> "冲突"
    WorkspaceGitFileChangeKind.UNTRACKED -> "未跟踪"
}

internal fun workspaceGitFileChangeRow(change: WorkspaceGitFileChange): String =
    "${workspaceGitFileChangeLabel(change.kind)} · ${change.path}"

internal data class WorkspaceGitFileChangeGroup(
    val kind: WorkspaceGitFileChangeKind,
    val changes: List<WorkspaceGitFileChange>,
)

private val workspaceGitFileChangeGroupOrder = listOf(
    WorkspaceGitFileChangeKind.MODIFIED,
    WorkspaceGitFileChangeKind.ADDED,
    WorkspaceGitFileChangeKind.UNTRACKED,
    WorkspaceGitFileChangeKind.DELETED,
    WorkspaceGitFileChangeKind.RENAMED,
    WorkspaceGitFileChangeKind.COPIED,
    WorkspaceGitFileChangeKind.TYPE_CHANGED,
    WorkspaceGitFileChangeKind.CONFLICTED,
)

internal fun workspaceGitFileChangeGroups(changes: List<WorkspaceGitFileChange>): List<WorkspaceGitFileChangeGroup> {
    val grouped = changes.groupBy(WorkspaceGitFileChange::kind)
    return workspaceGitFileChangeGroupOrder.mapNotNull { kind ->
        grouped[kind]?.let {
            WorkspaceGitFileChangeGroup(
                kind = kind,
                changes = it.sortedWith(compareBy<WorkspaceGitFileChange> { change -> change.path.lowercase() }.thenBy { change -> change.path }),
            )
        }
    }
}

@Composable
private fun WorkspaceDirtyFilesDialog(
    controller: DesktopApplication,
    worktreePath: String,
    health: WorkspaceGitHealth,
    onDismiss: () -> Unit,
) {
    val groups = workspaceGitFileChangeGroups(health.dirtyFiles)
    val changes = groups.flatMap(WorkspaceGitFileChangeGroup::changes)
    var selectedPath by remember(health.dirtyFiles) { mutableStateOf(changes.firstOrNull()?.path) }
    val selectedChange = changes.firstOrNull { it.path == selectedPath }
    val selected = selectedChange
    var selectedMode by remember(worktreePath, selected?.path) { mutableStateOf<WorkspaceFilePreviewMode?>(null) }
    var comparisonDisplayMode by remember(worktreePath, selected?.path) {
        mutableStateOf(WorkspaceComparisonDisplayMode.ALL_LINES)
    }
    var previewState by remember(worktreePath) {
        mutableStateOf<WorkspaceFilePreviewState>(WorkspaceFilePreviewState.Empty)
    }
    LaunchedEffect(worktreePath, selected?.path) {
        if (selected == null) {
            previewState = WorkspaceFilePreviewState.Empty
        } else {
            previewState = WorkspaceFilePreviewState.Loading
            previewState = try {
                WorkspaceFilePreviewState.Loaded(
                    controller.previewWorkspaceFile(worktreePath, selected),
                )
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                WorkspaceFilePreviewState.Failed(error.message ?: "无法读取文件")
            }
        }
    }
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        BoxWithConstraints(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            val dialogWidth = minOf(maxWidth * 0.90f, 1_800.dp)
            val dialogHeight = minOf(maxHeight * 0.90f, 960.dp)
            val fileListWidth = if (dialogWidth >= 1_400.dp) 360.dp else 300.dp
            Surface(
                Modifier.width(dialogWidth).height(dialogHeight),
                shape = RoundedCornerShape(22.dp),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            ) {
                Column(Modifier.fillMaxSize()) {
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text("未提交文件（${health.dirtyFileCount}）", style = MaterialTheme.typography.titleLarge)
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(
                    Modifier.weight(1f).fillMaxWidth().padding(16.dp),
                    horizontalArrangement = Arrangement.spacedBy(14.dp),
                ) {
                    Surface(
                        Modifier.width(fileListWidth).fillMaxHeight(),
                        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.35f),
                        shape = RoundedCornerShape(12.dp),
                    ) {
                        Column(Modifier.fillMaxSize()) {
                            Row(
                                Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 12.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text("文件列表", Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
                                Text(
                                    "${changes.size} 个",
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                            if (groups.isEmpty()) {
                                Box(
                                    Modifier.fillMaxSize().padding(16.dp),
                                    contentAlignment = Alignment.Center,
                                ) {
                                    Text(
                                        "当前 Git 状态未返回文件明细，请刷新后重试。",
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            } else {
                                Column(
                                    Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(8.dp),
                                    verticalArrangement = Arrangement.spacedBy(10.dp),
                                ) {
                                    groups.forEach { group ->
                                        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                            Text(
                                                "${workspaceGitFileChangeLabel(group.kind)}（${group.changes.size}）",
                                                Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
                                                style = MaterialTheme.typography.labelLarge,
                                                color = MaterialTheme.colorScheme.primary,
                                            )
                                            group.changes.forEach { change ->
                                                val selectedRow = change.path == selectedPath
                                                Surface(
                                                    Modifier.fillMaxWidth().clickable { selectedPath = change.path },
                                                    color = if (selectedRow) {
                                                        MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.70f)
                                                    } else {
                                                        MaterialTheme.colorScheme.surface.copy(alpha = 0.35f)
                                                    },
                                                    shape = RoundedCornerShape(8.dp),
                                                ) {
                                                    Row(
                                                        Modifier.padding(horizontal = 10.dp, vertical = 8.dp),
                                                        verticalAlignment = Alignment.CenterVertically,
                                                    ) {
                                                        Text(
                                                            workspaceGitFileChangeLabel(change.kind),
                                                            Modifier.width(64.dp),
                                                            style = MaterialTheme.typography.labelSmall,
                                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                                            maxLines = 1,
                                                            overflow = TextOverflow.Ellipsis,
                                                        )
                                                        Text(
                                                            change.path,
                                                            Modifier.weight(1f),
                                                            style = MaterialTheme.typography.bodySmall,
                                                            maxLines = 2,
                                                            overflow = TextOverflow.Ellipsis,
                                                        )
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                    VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Surface(
                        Modifier.weight(1f).fillMaxHeight(),
                        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.35f),
                        shape = RoundedCornerShape(12.dp),
                    ) {
                        WorkspaceFilePreviewPane(
                            change = selectedChange,
                            state = previewState,
                            onCopyRelativePath = { controller.copyText(it, "相对路径已复制") },
                            selectedMode = selectedMode,
                            onModeSelected = { selectedMode = it },
                            comparisonDisplayMode = comparisonDisplayMode,
                            onComparisonDisplayModeChange = { comparisonDisplayMode = it },
                        )
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 12.dp),
                    horizontalArrangement = Arrangement.End,
                ) {
                    TextButton(onClick = onDismiss) { Text("关闭") }
                }
                }
            }
        }
    }
}

private sealed interface WorkspaceCommitHistoryState {
    data object Loading : WorkspaceCommitHistoryState
    data class Loaded(val commits: List<WorkspaceGitCommit>) : WorkspaceCommitHistoryState
    data class Failed(val message: String) : WorkspaceCommitHistoryState
}

private enum class WorkspaceCommitHistoryScope { SERVICE_BASELINE, ALL }

internal enum class WorkspaceCommitHistoryLayout { HORIZONTAL, STACKED }

internal fun workspaceCommitHistoryLayout(widthDp: Float): WorkspaceCommitHistoryLayout =
    if (widthDp >= 640f) WorkspaceCommitHistoryLayout.HORIZONTAL else WorkspaceCommitHistoryLayout.STACKED

@Composable
private fun WorkspaceCommitHistoryDialog(
    controller: DesktopApplication,
    taskKey: String,
    worktreePath: String,
    masterBranch: WorkspaceHistoryBaseline?,
    onDismiss: () -> Unit,
) {
    var scope by remember(taskKey, worktreePath) { mutableStateOf(WorkspaceCommitHistoryScope.SERVICE_BASELINE) }
    var state by remember(taskKey, worktreePath) {
        mutableStateOf<WorkspaceCommitHistoryState>(WorkspaceCommitHistoryState.Loading)
    }
    LaunchedEffect(taskKey, worktreePath, masterBranch, scope) {
        state = WorkspaceCommitHistoryState.Loading
        state = try {
            val baseline = if (scope == WorkspaceCommitHistoryScope.SERVICE_BASELINE) {
                masterBranch?.localRef ?: error("任务记录和服务配置均未提供主分支")
            } else null
            WorkspaceCommitHistoryState.Loaded(controller.workspaceGitHistory(worktreePath, baseline))
        } catch (error: CancellationException) {
            throw error
        } catch (error: Throwable) {
            WorkspaceCommitHistoryState.Failed(error.message ?: "无法读取提交历史")
        }
    }
    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        BoxWithConstraints(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            val dialogWidth = minOf(maxWidth * 0.86f, 1_360.dp)
            val dialogHeight = minOf(maxHeight * 0.90f, 960.dp)
            Surface(
                Modifier.width(dialogWidth).height(dialogHeight),
                shape = RoundedCornerShape(22.dp),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            ) {
                Column(Modifier.fillMaxSize()) {
                    val title = when (val current = state) {
                        is WorkspaceCommitHistoryState.Loaded -> "分支提交历史（${current.commits.size}）"
                        else -> "分支提交历史"
                    }
                    Column(Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 12.dp)) {
                        Text(title, style = MaterialTheme.typography.titleLarge)
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            FilterChip(
                                selected = scope == WorkspaceCommitHistoryScope.SERVICE_BASELINE,
                                onClick = { scope = WorkspaceCommitHistoryScope.SERVICE_BASELINE },
                                label = { Text("相对 ${masterBranch?.displayRef ?: "主分支"}") },
                            )
                            FilterChip(
                                selected = scope == WorkspaceCommitHistoryScope.ALL,
                                onClick = { scope = WorkspaceCommitHistoryScope.ALL },
                                label = { Text("全部历史") },
                            )
                        }
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Box(Modifier.weight(1f).fillMaxWidth().padding(16.dp)) {
                        when (val current = state) {
                            WorkspaceCommitHistoryState.Loading -> Box(
                                Modifier.fillMaxSize(),
                                contentAlignment = Alignment.Center,
                            ) {
                                CircularProgressIndicator()
                            }

                            is WorkspaceCommitHistoryState.Failed -> SelectionContainer {
                                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                                    Text(current.message, color = MaterialTheme.colorScheme.error)
                                }
                            }

                            is WorkspaceCommitHistoryState.Loaded -> {
                                if (current.commits.isEmpty()) {
                                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                                        Text(
                                            if (scope == WorkspaceCommitHistoryScope.SERVICE_BASELINE) "相对服务基线没有新增提交" else "该分支暂无提交记录",
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                } else {
                                    LazyColumn(
                                        Modifier.fillMaxSize(),
                                        verticalArrangement = Arrangement.spacedBy(10.dp),
                                    ) {
                                        itemsIndexed(
                                            current.commits,
                                            key = { index, commit -> "$index-${commit.fullHash}" },
                                        ) { _, commit ->
                                            WorkspaceCommitHistoryCard(commit) { controller.copyText(commit.fullHash, "提交 SHA 已复制") }
                                        }
                                    }
                                }
                            }
                        }
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Row(
                        Modifier.fillMaxWidth().padding(horizontal = 22.dp, vertical = 12.dp),
                        horizontalArrangement = Arrangement.End,
                    ) {
                        TextButton(onClick = onDismiss) { Text("关闭") }
                    }
                }
            }
        }
    }
}

@Composable
private fun WorkspaceCommitHistoryCard(commit: WorkspaceGitCommit, onCopySha: () -> Unit) {
    val message = workspaceGitCommitMessageParts(commit.message)
    var expanded by remember(commit.fullHash) { mutableStateOf(false) }
    OutlinedCard(
        modifier = Modifier.fillMaxWidth().clickable(onClickLabel = if (expanded) "收起提交详情" else "展开提交详情") { expanded = !expanded },
        colors = CardDefaults.outlinedCardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.32f),
        ),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Column(
            Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            BoxWithConstraints(Modifier.fillMaxWidth()) {
                if (workspaceCommitHistoryLayout(maxWidth.value) == WorkspaceCommitHistoryLayout.HORIZONTAL) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        Text(SilverWingTime.format(commit.committedAt), Modifier.width(160.dp), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, maxLines = 1)
                        Text(workspaceGitCommitterName(commit), Modifier.width(145.dp), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(message.subject, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge, maxLines = if (expanded) Int.MAX_VALUE else 2, overflow = TextOverflow.Ellipsis)
                    }
                } else {
                    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            Text(SilverWingTime.format(commit.committedAt), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
                            Text(workspaceGitCommitterName(commit), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                        Text(message.subject, style = MaterialTheme.typography.bodyLarge, maxLines = if (expanded) Int.MAX_VALUE else 2, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
            if (expanded) {
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.65f))
                message.body?.let { body ->
                    SelectionContainer {
                        Text(body, style = MaterialTheme.typography.bodyMedium)
                    }
                }
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(commit.fullHash, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                    ActionIconButton("复制完整提交 SHA", onCopySha) { Icon(Icons.Outlined.ContentCopy, "复制完整提交 SHA") }
                }
                if (commit.committerEmail.isNotBlank()) Text("提交者邮箱：${commit.committerEmail}", style = MaterialTheme.typography.bodySmall)
                if (commit.authorName != commit.committerName || commit.authorEmail != commit.committerEmail) {
                    Text("作者：${commit.authorName.ifBlank { "未知" }}${commit.authorEmail.takeIf(String::isNotBlank)?.let { " <$it>" }.orEmpty()}", style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}

internal fun workspaceGitCommitHeader(commit: WorkspaceGitCommit): String =
    "${SilverWingTime.format(commit.committedAt)} · ${workspaceGitCommitterName(commit)} · ${workspaceGitCommitMessageParts(commit.message).subject}"

internal fun workspaceGitCommitterLabel(commit: WorkspaceGitCommit): String =
    "提交人：${workspaceGitCommitterName(commit)}"

internal fun workspaceGitCommitterName(commit: WorkspaceGitCommit): String =
    commit.committerName.ifBlank { "未知提交人" }

internal data class WorkspaceGitCommitMessageParts(
    val subject: String,
    val body: String?,
)

internal fun workspaceGitCommitMessageParts(message: String): WorkspaceGitCommitMessageParts {
    val normalized = message.trim('\r', '\n')
    if (normalized.isBlank()) return WorkspaceGitCommitMessageParts("（无提交说明）", null)
    val separator = normalized.indexOf('\n')
    if (separator < 0) return WorkspaceGitCommitMessageParts(normalized, null)
    val subject = normalized.substring(0, separator).trimEnd('\r')
    val body = normalized.substring(separator + 1).trim('\r', '\n').ifBlank { null }
    return WorkspaceGitCommitMessageParts(subject.ifBlank { "（无提交说明）" }, body)
}

private sealed interface WorkspaceFilePreviewState {
    data object Empty : WorkspaceFilePreviewState
    data object Loading : WorkspaceFilePreviewState
    data class Loaded(val preview: WorkspaceGitFilePreview) : WorkspaceFilePreviewState
    data class Failed(val message: String) : WorkspaceFilePreviewState
}

@Composable
private fun WorkspaceFilePreviewPane(
    change: WorkspaceGitFileChange?,
    state: WorkspaceFilePreviewState,
    onCopyRelativePath: (String) -> Unit,
    selectedMode: WorkspaceFilePreviewMode?,
    onModeSelected: (WorkspaceFilePreviewMode) -> Unit,
    comparisonDisplayMode: WorkspaceComparisonDisplayMode,
    onComparisonDisplayModeChange: (WorkspaceComparisonDisplayMode) -> Unit,
) {
    Column(Modifier.fillMaxSize()) {
        if (change == null) {
            Text(
                "文件预览",
                Modifier.padding(horizontal = 16.dp, vertical = 14.dp),
                style = MaterialTheme.typography.titleSmall,
            )
        } else {
            Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp)) {
                Text(
                    workspaceGitFileChangeLabel(change.kind),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        change.path,
                        Modifier.weight(1f, fill = false),
                        style = MaterialTheme.typography.bodySmall,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                    ActionIconButton(
                        "复制相对路径",
                        { onCopyRelativePath(workspaceRelativePathForCopy(change)) },
                        Modifier.size(28.dp),
                    ) {
                        Icon(Icons.Outlined.ContentCopy, "复制相对路径", Modifier.size(16.dp))
                    }
                }
            }
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        when (state) {
            WorkspaceFilePreviewState.Empty -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("从左侧选择文件查看预览", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            WorkspaceFilePreviewState.Loading -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            is WorkspaceFilePreviewState.Failed -> SelectionContainer {
                Box(Modifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.Center) {
                    Text(state.message, color = MaterialTheme.colorScheme.error)
                }
            }
            is WorkspaceFilePreviewState.Loaded -> {
                WorkspaceGitPreviewContent(
                    preview = state.preview,
                    selectedMode = selectedMode,
                    onModeSelected = onModeSelected,
                    comparisonDisplayMode = comparisonDisplayMode,
                    onComparisonDisplayModeChange = onComparisonDisplayModeChange,
                )
            }
        }
    }
}

@Composable
private fun WorkspaceGitPreviewContent(
    preview: WorkspaceGitFilePreview,
    selectedMode: WorkspaceFilePreviewMode?,
    onModeSelected: (WorkspaceFilePreviewMode) -> Unit,
    comparisonDisplayMode: WorkspaceComparisonDisplayMode,
    onComparisonDisplayModeChange: (WorkspaceComparisonDisplayMode) -> Unit,
) {
    val hasComparison = preview.comparison?.rows?.isNotEmpty() == true
    val hasContent = preview.content != null
    val displayedMode = workspacePreviewDisplayedMode(preview, selectedMode)
    Column(Modifier.fillMaxSize()) {
        if (hasComparison || hasContent) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp)) {
                if (hasComparison) {
                    WorkspacePreviewModeButton(
                        label = "左右对比",
                        selected = displayedMode == WorkspaceFilePreviewMode.COMPARISON,
                        onClick = { onModeSelected(WorkspaceFilePreviewMode.COMPARISON) },
                    )
                }
                if (hasComparison && hasContent) {
                    WorkspacePreviewModeButton(
                        label = when (preview.content?.origin) {
                            WorkspaceFileContentOrigin.HEAD -> "HEAD 内容"
                            else -> "文件内容"
                        },
                        selected = displayedMode == WorkspaceFilePreviewMode.CONTENT,
                        onClick = { onModeSelected(WorkspaceFilePreviewMode.CONTENT) },
                    )
                }
                Spacer(Modifier.weight(1f))
                if (hasComparison && displayedMode == WorkspaceFilePreviewMode.COMPARISON) {
                    TextButton(
                        onClick = {
                            onComparisonDisplayModeChange(
                                if (comparisonDisplayMode == WorkspaceComparisonDisplayMode.ALL_LINES) {
                                    WorkspaceComparisonDisplayMode.CHANGED_LINES
                                } else {
                                    WorkspaceComparisonDisplayMode.ALL_LINES
                                },
                            )
                        },
                    ) {
                        Text(
                            if (comparisonDisplayMode == WorkspaceComparisonDisplayMode.ALL_LINES) "仅看改动" else "查看全部",
                        )
                    }
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        }
        preview.notice?.let { notice ->
            Text(
                notice,
                Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        val truncated = when (displayedMode) {
            WorkspaceFilePreviewMode.COMPARISON -> preview.comparison?.truncated == true
            WorkspaceFilePreviewMode.CONTENT -> preview.content?.truncated == true
        }
        if (truncated) {
            Text(
                "内容较大，预览已截断（前 512 KB）",
                Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                color = WarningAmber,
                style = MaterialTheme.typography.labelMedium,
            )
        }
        when {
            displayedMode == WorkspaceFilePreviewMode.COMPARISON && hasComparison -> WorkspaceSideBySideComparisonPreview(
                comparison = requireNotNull(preview.comparison),
                language = preview.language,
                displayMode = comparisonDisplayMode,
            )

            displayedMode == WorkspaceFilePreviewMode.CONTENT && hasContent -> WorkspaceFileContentPreview(
                preview = preview,
            )

            else -> Box(Modifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.Center) {
                Text(
                    preview.unavailableReason ?: "当前文件没有可展示的文本内容。",
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}

internal fun workspacePreviewDisplayedMode(
    preview: WorkspaceGitFilePreview,
    selectedMode: WorkspaceFilePreviewMode?,
): WorkspaceFilePreviewMode {
    val hasComparison = preview.comparison?.rows?.isNotEmpty() == true
    val hasContent = preview.content != null
    val mode = selectedMode ?: preview.defaultMode
    return when {
        mode == WorkspaceFilePreviewMode.COMPARISON && hasComparison -> WorkspaceFilePreviewMode.COMPARISON
        hasContent -> WorkspaceFilePreviewMode.CONTENT
        else -> WorkspaceFilePreviewMode.COMPARISON
    }
}

internal fun workspaceRelativePathForCopy(change: WorkspaceGitFileChange): String = change.path

internal enum class WorkspaceComparisonDisplayMode { ALL_LINES, CHANGED_LINES }

@Composable
private fun WorkspacePreviewModeButton(
    label: String,
    selected: Boolean,
    onClick: () -> Unit,
) {
    TextButton(onClick = onClick) {
        Text(
            label,
            color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ColumnScope.WorkspaceFileContentPreview(
    preview: WorkspaceGitFilePreview,
) {
    val palette = workspacePreviewPalette()
    val verticalScroll = rememberScrollState()
    val horizontalScroll = rememberScrollState()
    Surface(
        Modifier.weight(1f).fillMaxWidth().padding(12.dp),
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(10.dp),
    ) {
        Column(Modifier.fillMaxSize()) {
            SelectionContainer {
                Box(
                    Modifier.weight(1f).fillMaxWidth()
                        .verticalScroll(verticalScroll)
                        .horizontalScroll(horizontalScroll)
                        .padding(14.dp),
                ) {
                    Text(
                        workspaceContentPreviewText(preview, palette),
                        fontFamily = FontFamily.Monospace,
                        style = MaterialTheme.typography.bodySmall.copy(lineHeight = 18.sp),
                        softWrap = false,
                    )
                }
            }
            WorkspaceHorizontalScrollbar(horizontalScroll)
        }
    }
}

@Composable
private fun ColumnScope.WorkspaceSideBySideComparisonPreview(
    comparison: WorkspaceFileComparison,
    language: WorkspaceFileLanguage,
    displayMode: WorkspaceComparisonDisplayMode,
) {
    val palette = workspacePreviewPalette()
    val text = workspaceComparisonText(comparison, displayMode, language, palette)
    val verticalScroll = rememberScrollState()
    val sharedHorizontalScroll = rememberScrollState()
    var oldContentWidth by remember(text.old) { mutableStateOf(0) }
    var newContentWidth by remember(text.new) { mutableStateOf(0) }
    val sharedContentWidth = with(LocalDensity.current) { maxOf(oldContentWidth, newContentWidth).toDp() }
    Surface(
        Modifier.weight(1f).fillMaxWidth().padding(12.dp),
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(10.dp),
    ) {
        Column(Modifier.fillMaxSize()) {
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    if (comparison.oldContent == null) "HEAD（修改前，不存在）" else "HEAD（修改前）",
                    Modifier.weight(1f),
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                VerticalDivider(Modifier.height(18.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Text(
                    if (comparison.newContent == null) "工作区（当前，已删除）" else "工作区（当前）",
                    Modifier.weight(1f).padding(start = 14.dp),
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Box(Modifier.weight(1f).fillMaxWidth()) {
                Row(
                    Modifier.fillMaxSize().verticalScroll(verticalScroll),
                    verticalAlignment = Alignment.Top,
                ) {
                    Box(
                        Modifier.weight(1f).horizontalScroll(sharedHorizontalScroll).padding(14.dp),
                    ) {
                        SelectionContainer {
                            Box(Modifier.widthIn(min = sharedContentWidth)) {
                                Text(
                                    text.old,
                                    onTextLayout = { oldContentWidth = it.size.width },
                                    fontFamily = FontFamily.Monospace,
                                    style = MaterialTheme.typography.bodySmall.copy(lineHeight = 18.sp),
                                    softWrap = false,
                                )
                            }
                        }
                    }
                    VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Box(
                        Modifier.weight(1f).horizontalScroll(sharedHorizontalScroll).padding(14.dp),
                    ) {
                        SelectionContainer {
                            Box(Modifier.widthIn(min = sharedContentWidth)) {
                                Text(
                                    text.new,
                                    onTextLayout = { newContentWidth = it.size.width },
                                    fontFamily = FontFamily.Monospace,
                                    style = MaterialTheme.typography.bodySmall.copy(lineHeight = 18.sp),
                                    softWrap = false,
                                )
                            }
                        }
                    }
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            WorkspaceHorizontalScrollbar(sharedHorizontalScroll)
        }
    }
}

@Composable
private fun WorkspaceHorizontalScrollbar(
    scrollState: androidx.compose.foundation.ScrollState,
    modifier: Modifier = Modifier,
) {
    HorizontalScrollbar(
        adapter = rememberScrollbarAdapter(scrollState),
        modifier = modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 6.dp),
    )
}

internal data class WorkspacePreviewPalette(
    val foreground: Color,
    val muted: Color,
    val keyword: Color,
    val string: Color,
    val comment: Color,
    val number: Color,
    val annotation: Color,
    val property: Color,
    val tag: Color,
    val attribute: Color,
    val heading: Color,
    val addedBackground: Color,
    val deletedBackground: Color,
)

@Composable
internal fun workspacePreviewPalette(): WorkspacePreviewPalette = WorkspacePreviewPalette(
    foreground = MaterialTheme.colorScheme.onSurface,
    muted = MaterialTheme.colorScheme.onSurfaceVariant,
    keyword = MaterialTheme.colorScheme.primary,
    string = MaterialTheme.colorScheme.tertiary,
    comment = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.80f),
    number = MaterialTheme.colorScheme.secondary,
    annotation = MaterialTheme.colorScheme.secondary,
    property = MaterialTheme.colorScheme.primary,
    tag = MaterialTheme.colorScheme.primary,
    attribute = MaterialTheme.colorScheme.tertiary,
    heading = MaterialTheme.colorScheme.secondary,
    addedBackground = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.58f),
    deletedBackground = MaterialTheme.colorScheme.errorContainer.copy(alpha = 0.58f),
)

private data class WorkspaceComparisonText(
    val old: AnnotatedString,
    val new: AnnotatedString,
)

internal fun workspaceComparisonRows(
    comparison: WorkspaceFileComparison,
    displayMode: WorkspaceComparisonDisplayMode,
): List<WorkspaceFileComparisonRow> = when (displayMode) {
    WorkspaceComparisonDisplayMode.ALL_LINES -> comparison.rows
    WorkspaceComparisonDisplayMode.CHANGED_LINES -> comparison.rows.filter(WorkspaceFileComparisonRow::isChanged)
}

private fun workspaceComparisonText(
    comparison: WorkspaceFileComparison,
    displayMode: WorkspaceComparisonDisplayMode,
    language: WorkspaceFileLanguage,
    palette: WorkspacePreviewPalette,
): WorkspaceComparisonText {
    val rows = workspaceComparisonRows(comparison, displayMode)
    val oldGutterWidth = rows.maxOfOrNull { it.oldLine?.lineNumber ?: 0 }
        ?.coerceAtLeast(1)?.toString()?.length ?: 1
    val newGutterWidth = rows.maxOfOrNull { it.newLine?.lineNumber ?: 0 }
        ?.coerceAtLeast(1)?.toString()?.length ?: 1
    return WorkspaceComparisonText(
        old = buildAnnotatedString {
            rows.forEach { row ->
                appendWorkspaceComparisonLine(row.oldLine, oldGutterWidth, language, palette)
            }
        },
        new = buildAnnotatedString {
            rows.forEach { row ->
                appendWorkspaceComparisonLine(row.newLine, newGutterWidth, language, palette)
            }
        },
    )
}

private fun workspaceContentPreviewText(
    preview: WorkspaceGitFilePreview,
    palette: WorkspacePreviewPalette,
): AnnotatedString = buildAnnotatedString {
    val lines = workspacePreviewLines(preview.content?.text.orEmpty())
    val gutterWidth = lines.size.coerceAtLeast(1).toString().length
    lines.forEachIndexed { index, line -> appendWorkspaceContentLine(index + 1, gutterWidth, line, preview.language, palette) }
}

private fun AnnotatedString.Builder.appendWorkspaceComparisonLine(
    line: WorkspaceFileComparisonLine?,
    gutterWidth: Int,
    language: WorkspaceFileLanguage,
    palette: WorkspacePreviewPalette,
) {
    if (line == null) {
        append(" ".repeat(gutterWidth))
        append(" │")
        append('\n')
        return
    }
    val lineStart = length
    append("${line.lineNumber.toString().padStart(gutterWidth)} │ ")
    val contentStart = length
    appendWorkspaceSyntax(line.text, language, palette)
    addStyle(SpanStyle(color = palette.muted), lineStart, contentStart)
    when (line.kind) {
        WorkspaceFileComparisonLineKind.ADDED -> addStyle(SpanStyle(background = palette.addedBackground), lineStart, length)
        WorkspaceFileComparisonLineKind.DELETED -> addStyle(SpanStyle(background = palette.deletedBackground), lineStart, length)
        WorkspaceFileComparisonLineKind.CONTEXT -> Unit
    }
    append('\n')
}

private fun AnnotatedString.Builder.appendWorkspaceContentLine(
    lineNumber: Int,
    gutterWidth: Int,
    line: String,
    language: WorkspaceFileLanguage,
    palette: WorkspacePreviewPalette,
) {
    val lineStart = length
    append("${lineNumber.toString().padStart(gutterWidth)} │ ")
    val contentStart = length
    appendWorkspaceSyntax(line, language, palette)
    addStyle(SpanStyle(color = palette.muted), lineStart, contentStart)
    append('\n')
}

private fun AnnotatedString.Builder.appendWorkspaceSyntax(
    text: String,
    language: WorkspaceFileLanguage,
    palette: WorkspacePreviewPalette,
) {
    val start = length
    append(text)
    workspaceSyntaxTokens(language, text).forEach { token ->
        addStyle(SpanStyle(color = palette.colorFor(token.kind)), start + token.start, start + token.end)
    }
}

internal fun WorkspacePreviewPalette.colorFor(kind: WorkspaceSyntaxTokenKind): Color = when (kind) {
    WorkspaceSyntaxTokenKind.KEYWORD -> keyword
    WorkspaceSyntaxTokenKind.STRING -> string
    WorkspaceSyntaxTokenKind.COMMENT -> comment
    WorkspaceSyntaxTokenKind.NUMBER -> number
    WorkspaceSyntaxTokenKind.ANNOTATION -> annotation
    WorkspaceSyntaxTokenKind.PROPERTY_KEY -> property
    WorkspaceSyntaxTokenKind.TAG -> tag
    WorkspaceSyntaxTokenKind.ATTRIBUTE -> attribute
    WorkspaceSyntaxTokenKind.HEADING -> heading
}

private fun workspacePreviewLines(text: String): List<String> {
    if (text.isEmpty()) return emptyList()
    val lines = text.lineSequence().toList()
    return if (text.endsWith('\n') && lines.lastOrNull()?.isEmpty() == true) lines.dropLast(1) else lines
}

@Composable
private fun WorkspaceCardStatusPill(text: String, modifier: Modifier = Modifier) {
    val color = MaterialTheme.colorScheme.statusColor(text)
    val label = when (text) {
        "CREATING" -> "创建中"
        "READY" -> "就绪"
        "READY_WITH_WARNINGS" -> "有警告"
        "FAILED" -> "失败"
        "ARCHIVED" -> "已归档"
        "SUCCESS" -> "成功"
        "CONFLICT" -> "有冲突"
        "PARTIAL" -> "部分完成"
        "CREATED", "PREFLIGHT_PASSED", "SOURCE_BRANCH_PUSHED" -> "构建已中断"
        else -> text
    }
    Surface(
        modifier.widthIn(max = 180.dp),
        color = color.copy(alpha = 0.12f),
        shape = RoundedCornerShape(50),
        border = BorderStroke(1.dp, color.copy(alpha = 0.18f)),
    ) {
        Text(
            label,
            Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
            color = color,
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun WorkspaceCardProblemPill(text: String, modifier: Modifier = Modifier) {
    val color = MaterialTheme.colorScheme.error
    Surface(
        modifier.widthIn(max = 260.dp),
        color = color.copy(alpha = 0.10f),
        shape = RoundedCornerShape(50),
        border = BorderStroke(1.dp, color.copy(alpha = 0.25f)),
    ) {
        Text(
            text,
            Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
            color = color,
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun WorkspaceCardActions(
    controller: DesktopApplication,
    task: TaskManifest,
    workspace: ServiceWorkspace,
    customCommands: List<WorkspaceCommandConfig>,
    toolbarPresentation: WorkspaceToolbarPresentation,
    onCommit: () -> Unit,
    onCommitAndPush: () -> Unit,
    onAddModule: () -> Unit,
    canDeleteModule: Boolean,
    onDeleteModule: () -> Unit,
    onEditTagTarget: () -> Unit,
    alignToEnd: Boolean,
    modifier: Modifier,
) {
    val actualBranch = controller.workspaceGitHealth[controller.workspaceKey(workspace)]?.actualBranch ?: workspace.branch
    val writeBlocked = controller.config.blockedGitWriteBranches.any { it.equals(actualBranch, ignoreCase = true) }
    val workspaceLabel = workspace.moduleName.ifBlank { workspace.serviceName }
    val workspaceLoading = controller.busy && controller.activeOperation?.contains(workspaceLabel) == true
    val tagLoading = workspaceLoading && controller.activeOperation?.contains("Tag") == true
    FlowRow(
        modifier,
        horizontalArrangement = if (alignToEnd) {
            Arrangement.spacedBy(8.dp, Alignment.End)
        } else {
            Arrangement.spacedBy(8.dp)
        },
        verticalArrangement = Arrangement.spacedBy(8.dp),
        itemVerticalAlignment = Alignment.CenterVertically,
    ) {
        if (toolbarPresentation.showPathActionGroup) {
            IconActionGroup {
                ActionIconButton("在终端中打开", { controller.terminal(workspace.worktreePath) }, Modifier.size(34.dp)) {
                    Icon(Icons.Outlined.Terminal, "终端", Modifier.size(18.dp))
                }
                ActionIconButton("打开工作区文件夹", { controller.openDirectory(workspace.worktreePath) }, Modifier.size(34.dp)) {
                    Icon(Icons.Outlined.FolderOpen, "打开文件夹", Modifier.size(18.dp))
                }
                ActionIconButton("复制工作区完整路径", { controller.copyText(workspace.worktreePath, "工作区路径已复制") }, Modifier.size(34.dp)) {
                    Icon(Icons.Outlined.ContentCopy, "复制路径", Modifier.size(18.dp))
                }
            }
        }
        if (customCommands.isNotEmpty()) {
            IconActionGroup {
                customCommands.forEach { command ->
                    ActionIconButton(
                        "${command.name} · ${workspaceCommandDisplay(command)}",
                        { controller.runWorkspaceCommand(task, workspace, command) },
                        Modifier.size(34.dp),
                        enabled = !controller.busy && !controller.workspaceCommandRunning,
                    ) {
                        Icon(workspaceCommandIcon(command.iconKey), command.name, Modifier.size(18.dp))
                    }
                }
            }
        }
        if (toolbarPresentation.showGitActionGroup) {
            GitActionIconGroup(
                enabled = !controller.busy && !writeBlocked,
                scopeLabel = workspaceLabel,
                loading = workspaceLoading,
                onCommit = onCommit,
                onCommitAndPush = onCommitAndPush,
                onPush = { controller.pushWorkspace(task, workspace) },
                trailingActions = {
                    val source = runCatching { WorkspaceMainBranchResolver.resolve(controller.config, task, workspace) }.getOrNull()
                    WorkspaceMainBranchMergeAction(
                        label = workspaceMainBranchMergeActionLabel(source, workspace),
                        enabled = workspaceMainBranchMergeEnabled(task, workspace, source, controller.gitHealth(workspace), controller.config.blockedGitWriteBranches, controller.busy),
                        loading = controller.workspaceMainBranchMergeState(task, workspace)?.running == true,
                        onClick = { controller.mergeWorkspaceMainBranch(task, workspace) },
                    )
                },
            )
        }
        if (toolbarPresentation.showTagAction || toolbarPresentation.showTagTargetAction) {
            IconActionGroup {
                if (toolbarPresentation.showTagAction) {
                    ActionIconButton(
                        workspaceTagActionLabel(workspace),
                        { controller.buildTag(task, workspace) },
                        Modifier.size(34.dp),
                        enabled = !controller.busy,
                        loading = tagLoading,
                    ) {
                        Icon(Icons.Outlined.Sell, "测试Tag", Modifier.size(18.dp))
                    }
                }
                if (toolbarPresentation.showTagTargetAction) {
                    var moreExpanded by remember { mutableStateOf(false) }
                    Box {
                        ActionIconButton("服务更多操作", { moreExpanded = true }, Modifier.size(34.dp), enabled = !controller.busy) {
                            Icon(Icons.Outlined.MoreHoriz, "更多", Modifier.size(18.dp))
                        }
                        SilverWingDropdownMenu(moreExpanded, { moreExpanded = false }) {
                            DropdownMenuItem(
                                text = { Text("设置测试目标分支…") },
                                onClick = { moreExpanded = false; onEditTagTarget() },
                                enabled = !controller.busy,
                            )
                        }
                    }
                }
            }
        }
        IconActionGroup {
            if (toolbarPresentation.showAddModuleAction) {
                ActionIconButton("为服务添加模块", onAddModule, Modifier.size(34.dp), enabled = !controller.busy) {
                    Icon(Icons.Outlined.Add, "添加模块", Modifier.size(18.dp))
                }
            }
            ActionIconButton("删除当前模块", onDeleteModule, Modifier.size(34.dp), enabled = canDeleteModule && !controller.busy) {
                Icon(Icons.Outlined.Delete, "删除模块", Modifier.size(18.dp), tint = MaterialTheme.colorScheme.error)
            }
        }
        IconActionGroup {
            ActionIconButton(
                toolbarPresentation.developmentToolActionLabel,
                { controller.openWorkspace(workspace, toolbarPresentation.developmentTool) },
                Modifier.size(34.dp),
            ) {
                Icon(Icons.Outlined.Code, "打开开发工具", Modifier.size(18.dp))
            }
        }
        if (toolbarPresentation.showRetryAction) {
            IconActionGroup {
                ActionIconButton(
                    "重试创建失败的服务",
                    { controller.retryFailedServices(task, listOf(workspace.groupServiceId)) },
                    Modifier.size(34.dp),
                    enabled = !controller.busy,
                ) {
                    Icon(Icons.Outlined.Refresh, "重试", Modifier.size(18.dp))
                }
            }
        }
    }
}

internal fun workspaceIssueLabel(health: WorkspaceGitHealth): String =
    workspaceIssueLabel(health.issue, health.actualBranch, health.expectedBranch)

internal fun workspaceIssueDetail(health: WorkspaceGitHealth?): String? = health
    ?.takeIf {
        it.state in setOf(WorkspaceGitHealthState.MISSING, WorkspaceGitHealthState.FAILED) &&
            it.issue != WorkspaceGitIssue.BRANCH_MISMATCH
    }
    ?.message
    ?.takeIf(String::isNotBlank)

internal fun workspaceIssueLabel(issue: WorkspaceGitIssue, actual: String?, expected: String?): String = when (issue) {
    WorkspaceGitIssue.NONE -> "正常"
    WorkspaceGitIssue.MISSING -> "工作区不存在"
    WorkspaceGitIssue.NOT_GIT -> "不是有效的 Git 工作区"
    WorkspaceGitIssue.IDENTITY_MISMATCH -> "Git 仓库身份不匹配"
    WorkspaceGitIssue.BRANCH_MISMATCH -> "分支不一致：${actual.orEmpty()} → ${expected.orEmpty()}"
    WorkspaceGitIssue.DETACHED_HEAD -> "Detached HEAD"
    WorkspaceGitIssue.OPERATION_IN_PROGRESS -> "存在进行中的 Git 操作"
    WorkspaceGitIssue.INSPECTION_FAILED -> "Git 状态检查失败"
}
