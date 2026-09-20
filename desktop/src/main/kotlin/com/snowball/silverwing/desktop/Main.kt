package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.ArrowForward
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Archive
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.KeyboardArrowUp
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Restore
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Security
import androidx.compose.material.icons.outlined.Sell
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Workspaces
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowPlacement
import androidx.compose.ui.window.application
import com.snowball.silverwing.core.error
import androidx.compose.ui.window.rememberWindowState
import com.mikepenz.markdown.compose.Markdown
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography
import com.snowball.silverwing.core.AgentConflictResolution
import com.snowball.silverwing.core.BootstrapConfig
import com.snowball.silverwing.core.BootstrapPresets
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.BranchPrefixResolver
import com.snowball.silverwing.core.RepositoryConfig
import com.snowball.silverwing.core.RemoteBranchSearch
import com.snowball.silverwing.core.RemoteBranchRef
import com.snowball.silverwing.core.GroupConfig
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.ServiceModuleConfig
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.RequirementMetadata
import com.snowball.silverwing.core.TaskNaming
import com.snowball.silverwing.core.CURRENT_PRODUCT_VERSION
import com.snowball.silverwing.core.RequirementDraftState
import com.snowball.silverwing.core.WorkspaceToolLaunchStatus
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.TaskLifecycleStatus
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.health
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.WorkspaceGitHealthState
import com.snowball.silverwing.core.LocalPushState
import com.snowball.silverwing.core.isHttpUrl
import com.snowball.silverwing.core.selectionKey
import com.snowball.silverwing.desktop.generated.resources.Res
import com.snowball.silverwing.desktop.generated.resources.app_icon
import io.github.vinceglb.filekit.FileKit
import org.jetbrains.compose.resources.painterResource
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.awt.event.WindowAdapter
import java.awt.event.WindowEvent
import java.util.UUID

fun main() {
    val fatalEvents = com.snowball.silverwing.core.JsonlEventSink()
    val previousHandler = Thread.getDefaultUncaughtExceptionHandler()
    Thread.setDefaultUncaughtExceptionHandler { thread, error ->
        fatalEvents.error(
            event = "application.uncaught",
            message = error.stackTraceToString(),
            metadata = mapOf("thread" to thread.name),
        )
        previousHandler?.uncaughtException(thread, error) ?: error.printStackTrace()
    }
    FileKit.init(appId = "com.snowball.silverwing")
    application {
        val controller = remember { DesktopApplication() }
        val windowPreferences = remember { WindowPreferences.load() }
        val state = rememberWindowState(
            width = windowPreferences.width.dp,
            height = windowPreferences.height.dp,
            placement = if (windowPreferences.maximized) WindowPlacement.Maximized else WindowPlacement.Floating,
        )
        Window(
            onCloseRequest = {
                if (controller.hasActiveOperations) {
                    controller.showError(IllegalStateException("操作正在执行，完成前不能关闭应用"))
                } else {
                    controller.close()
                    exitApplication()
                }
            },
            title = "silverwing $CURRENT_PRODUCT_VERSION",
            state = state,
            icon = painterResource(Res.drawable.app_icon),
        ) {
            DisposableEffect(window) {
                fun nativeScale(): Pair<Double, Double> = window.graphicsConfiguration.defaultTransform.let { it.scaleX to it.scaleY }
                val listener = object : WindowAdapter() {
                    override fun windowGainedFocus(event: WindowEvent?) = controller.onWindowFocused()
                }
                window.addWindowFocusListener(listener)
                onDispose {
                    val maximized = window.extendedState and java.awt.Frame.MAXIMIZED_BOTH != 0
                    val (scaleX, scaleY) = nativeScale()
                    WindowPreferences.savePhysicalWindow(window.width, window.height, maximized, scaleX, scaleY)
                    window.removeWindowFocusListener(listener)
                }
            }
            SilverWingTheme(controller.config.theme) { AgentWorkspaceApp(controller) }
        }
    }
}

@Composable
private fun AgentWorkspaceApp(controller: DesktopApplication) {
    val snackbar = remember { SnackbarHostState() }
    var showCreate by remember { mutableStateOf(false) }

    LaunchedEffect(controller.statusMessage) {
        val message = controller.statusMessage
        if (message != null) {
            snackbar.showSnackbar(
                message,
                withDismissAction = true,
                duration = SnackbarDuration.Short,
            )
            controller.dismissMessages()
        }
    }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        BoxWithConstraints(Modifier.fillMaxSize().padding(padding)) {
            val navigationLayout = navigationLayoutFor(maxWidth.value)
            Row(Modifier.fillMaxSize()) {
                Sidebar(controller, navigationLayout) { controller.navigation = it }
                Column(Modifier.weight(1f).fillMaxHeight()) {
                    Box(
                        Modifier.weight(1f).fillMaxWidth()
                            .padding(top = navigationContentTopPaddingFor(controller.navigation).dp),
                    ) {
                        when (controller.navigation) {
                            NavigationItem.TASKS -> TasksScreen(controller, archived = false) { showCreate = true }
                            NavigationItem.ARCHIVED -> TasksScreen(controller, archived = true) { showCreate = true }
                            NavigationItem.SERVICES -> ServicesScreen(controller)
                            NavigationItem.TAG -> TagScreen(controller)
                            NavigationItem.SETTINGS -> SettingsScreen(controller)
                        }
                    }
                }
            }
            if (controller.busy) {
                Surface(
                    Modifier.align(Alignment.TopCenter).widthIn(min = 360.dp, max = 560.dp),
                    color = MaterialTheme.colorScheme.inverseSurface,
                    shape = RoundedCornerShape(bottomStart = 12.dp, bottomEnd = 12.dp),
                ) {
                    Column(Modifier.padding(horizontal = 16.dp, vertical = 9.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text(
                                controller.activeOperation ?: "正在处理",
                                Modifier.weight(1f),
                                color = MaterialTheme.colorScheme.inverseOnSurface,
                                style = MaterialTheme.typography.labelMedium,
                            )
                            if (controller.activeOperationCancellable) {
                                TextButton(onClick = controller::cancelActiveOperation) {
                                    Text("取消", color = MaterialTheme.colorScheme.inversePrimary)
                                }
                            }
                        }
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }

    if (showCreate) {
        CreateTaskDialog(controller, onDismiss = { showCreate = false }) { name, branch, group, services, link, notes, tools, reuseKeys, selections ->
            controller.taskController.create(name, branch, group, services, link, notes, tools, reuseKeys, selections) {
                showCreate = false
            }
        }
    }
    controller.errorMessage?.let { error ->
        TagResultDialog(
            title = "操作失败",
            content = error,
            onDismiss = controller::dismissMessages,
            onCopy = { controller.copyText(error, "错误详情已复制") },
        )
    }
    controller.repositoryAddResult?.let { result ->
        AlertDialog(
            onDismissRequest = controller::clearRepositoryAddResult,
            title = { Text("仓库添加结果") },
            text = {
                Column(
                    Modifier.fillMaxWidth().heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Text("已添加 ${result.added.size} 个服务，跳过 ${result.skipped.size} 个目录。")
                    result.added.forEach { Text("✓ $it", color = SuccessGreen, style = MaterialTheme.typography.bodySmall) }
                    result.skipped.forEach { skipped ->
                        Text(
                            "跳过 ${skipped.path}\n${skipped.reason}",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            },
            confirmButton = { Button(onClick = controller::clearRepositoryAddResult) { Text("完成") } },
        )
    }
    controller.agentConflict?.let { conflict ->
        AlertDialog(
            onDismissRequest = {},
            title = { Text("Agent 文件发生冲突") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(9.dp)) {
                    Text("文件已被外部编辑器修改，当前窗口也有未保存内容。请选择要保留的版本。")
                    Text(conflict.path.toString(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            },
            confirmButton = { Button(onClick = { controller.agentInstructionsController.resolveConflict(AgentConflictResolution.USE_LOCAL) }) { Text("使用本地编辑") } },
            dismissButton = { OutlinedButton(onClick = { controller.agentInstructionsController.resolveConflict(AgentConflictResolution.USE_DISK) }) { Text("使用磁盘版本") } },
        )
    }
}

/**
 * The complete task view needs room for a task index and a readable detail pane.
 * Below this width, an icon rail gives that content back 136dp without hiding a
 * destination or requiring a separate navigation mode.
 */
internal const val COMPACT_NAVIGATION_MAX_WIDTH_DP = 1_180f
internal const val EXPANDED_SIDEBAR_WIDTH_DP = 184f
internal const val COMPACT_SIDEBAR_WIDTH_DP = 72f
internal const val MAIN_CONTENT_START_PADDING_DP = 8f
internal const val MAIN_CONTENT_END_PADDING_DP = 28f
internal const val MAIN_CONTENT_BOTTOM_PADDING_DP = 28f

internal enum class NavigationLayout {
    EXPANDED,
    COMPACT,
}

internal fun navigationLayoutFor(availableWidthDp: Float): NavigationLayout =
    if (availableWidthDp < COMPACT_NAVIGATION_MAX_WIDTH_DP) NavigationLayout.COMPACT else NavigationLayout.EXPANDED

/** Non-task pages retain a small breathing space after their large page title is removed. */
internal fun navigationContentTopPaddingFor(item: NavigationItem): Float =
    if (item == NavigationItem.TASKS) 0f else 16f

internal fun sidebarWidthFor(layout: NavigationLayout): Float = when (layout) {
    NavigationLayout.EXPANDED -> EXPANDED_SIDEBAR_WIDTH_DP
    NavigationLayout.COMPACT -> COMPACT_SIDEBAR_WIDTH_DP
}

/** Keeps badges legible in the narrow rail without spending horizontal space on large counts. */
internal fun compactNavigationCountLabel(count: Int?): String? = when {
    count == null || count <= 0 -> null
    count > 9 -> "9+"
    else -> count.toString()
}

@Composable
private fun Sidebar(
    controller: DesktopApplication,
    layout: NavigationLayout,
    onSelected: (NavigationItem) -> Unit,
) {
    when (layout) {
        NavigationLayout.EXPANDED -> ExpandedSidebar(controller, onSelected)
        NavigationLayout.COMPACT -> CompactSidebar(controller, onSelected)
    }
}

@Composable
private fun ExpandedSidebar(controller: DesktopApplication, onSelected: (NavigationItem) -> Unit) {
    Surface(
        Modifier.width(EXPANDED_SIDEBAR_WIDTH_DP.dp).fillMaxHeight().border(
            BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            RoundedCornerShape(topEnd = 16.dp, bottomEnd = 16.dp),
        ),
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(topEnd = 16.dp, bottomEnd = 16.dp),
    ) {
        Column(Modifier.padding(horizontal = 10.dp, vertical = 16.dp)) {
            Text(
                "工作空间",
                Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            visibleNavigationItems(controller).forEach { item ->
                val selectedItem = item == controller.navigation
                Surface(
                    Modifier.fillMaxWidth().clickable(onClickLabel = item.title) { onSelected(item) },
                    color = if (selectedItem) MaterialTheme.colorScheme.primaryContainer else Color.Transparent,
                    shape = RoundedCornerShape(13.dp),
                ) {
                    Row(Modifier.padding(horizontal = 8.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                        if (selectedItem) {
                            Surface(Modifier.width(3.dp).height(24.dp), color = MaterialTheme.colorScheme.primary, shape = RoundedCornerShape(3.dp)) {}
                            Spacer(Modifier.width(7.dp))
                        } else {
                            Spacer(Modifier.width(10.dp))
                        }
                        Icon(
                            navigationIcon(item),
                            null,
                            Modifier.size(20.dp),
                            tint = if (selectedItem) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.width(9.dp))
                        Column(Modifier.weight(1f)) {
                            Text(item.title, fontWeight = if (selectedItem) FontWeight.SemiBold else FontWeight.Normal)
                            Text(item.subtitle, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        navigationCount(controller, item)?.let { count ->
                            Surface(color = if (selectedItem) MaterialTheme.colorScheme.surface.copy(alpha = 0.75f) else MaterialTheme.colorScheme.surfaceVariant, shape = RoundedCornerShape(20.dp)) {
                                Text(count.toString(), Modifier.padding(horizontal = 7.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
                            }
                        }
                    }
                }
                Spacer(Modifier.height(4.dp))
            }
            Spacer(Modifier.weight(1f))
            Row(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Security, "本地安全执行", Modifier.size(20.dp), tint = SuccessGreen)
                Spacer(Modifier.width(7.dp))
                Column {
                    Text("本地安全执行", style = MaterialTheme.typography.labelMedium)
                    Text("不上传源代码", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun CompactSidebar(controller: DesktopApplication, onSelected: (NavigationItem) -> Unit) {
    Surface(
        Modifier.width(COMPACT_SIDEBAR_WIDTH_DP.dp).fillMaxHeight().border(
            BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            RoundedCornerShape(topEnd = 16.dp, bottomEnd = 16.dp),
        ),
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(topEnd = 16.dp, bottomEnd = 16.dp),
    ) {
        Column(
            Modifier.fillMaxHeight().padding(horizontal = 10.dp, vertical = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            visibleNavigationItems(controller).forEach { item ->
                CompactNavigationItem(
                    item = item,
                    count = navigationCount(controller, item),
                    selected = item == controller.navigation,
                    onSelected = { onSelected(item) },
                )
                Spacer(Modifier.height(6.dp))
            }
            Spacer(Modifier.weight(1f))
            TooltipBox(
                positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
                tooltip = { PlainTooltip { Text("本地安全执行\n不上传源代码") } },
                state = rememberTooltipState(),
            ) {
                Surface(
                    Modifier.size(44.dp).semantics { contentDescription = "本地安全执行，不上传源代码" },
                    color = SuccessGreen.copy(alpha = 0.10f),
                    shape = RoundedCornerShape(12.dp),
                ) {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Icon(Icons.Outlined.Security, null, Modifier.size(22.dp), tint = SuccessGreen)
                    }
                }
            }
        }
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun CompactNavigationItem(
    item: NavigationItem,
    count: Int?,
    selected: Boolean,
    onSelected: () -> Unit,
) {
    val countLabel = compactNavigationCountLabel(count)
    val accessibilityLabel = buildString {
        append(item.title)
        append("，")
        append(item.subtitle)
        count?.let { append("，$it 项") }
    }
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text(accessibilityLabel) } },
        state = rememberTooltipState(),
    ) {
        Surface(
            Modifier
                .size(48.dp)
                .semantics { contentDescription = accessibilityLabel }
                .clickable(onClickLabel = item.title, onClick = onSelected),
            color = if (selected) MaterialTheme.colorScheme.primaryContainer else Color.Transparent,
            shape = RoundedCornerShape(13.dp),
        ) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Icon(
                    navigationIcon(item),
                    null,
                    Modifier.size(22.dp),
                    tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
                countLabel?.let { label ->
                    Surface(
                        Modifier.align(Alignment.TopEnd).padding(top = 2.dp, end = 2.dp),
                        color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceVariant,
                        shape = RoundedCornerShape(12.dp),
                    ) {
                        Text(
                            label,
                            Modifier.padding(horizontal = 4.dp, vertical = 1.dp),
                            color = if (selected) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.labelSmall,
                            maxLines = 1,
                        )
                    }
                }
            }
        }
    }
}

private fun visibleNavigationItems(controller: DesktopApplication): List<NavigationItem> =
    NavigationItem.entries.filter { item -> item != NavigationItem.TAG || controller.showsTagNavigation }

private fun navigationIcon(item: NavigationItem): ImageVector = when (item) {
    NavigationItem.TASKS -> Icons.Outlined.Workspaces
    NavigationItem.ARCHIVED -> Icons.Outlined.Archive
    NavigationItem.SERVICES -> Icons.Outlined.Dns
    NavigationItem.TAG -> Icons.Outlined.Sell
    NavigationItem.SETTINGS -> Icons.Outlined.Settings
}

private fun navigationCount(controller: DesktopApplication, item: NavigationItem): Int? = when (item) {
    NavigationItem.TASKS -> controller.tasks.count { it.lifecycleStatus != TaskLifecycleStatus.ARCHIVED }
    NavigationItem.ARCHIVED -> controller.tasks.count { it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED }
    NavigationItem.SERVICES -> controller.config.groups.sumOf { it.services.size }
    NavigationItem.TAG, NavigationItem.SETTINGS -> null
}
