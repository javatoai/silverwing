package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.clickable
import androidx.compose.foundation.background
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.graphics.Color
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.snowball.silverwing.core.CodexExtensionOwnership
import com.snowball.silverwing.core.CodexPluginCatalogItem
import com.snowball.silverwing.core.CodexPluginMarketplaceSource
import com.snowball.silverwing.core.CodexPluginMarketplaceSnapshot
import com.snowball.silverwing.core.ExternalSkillCatalogItem
import com.snowball.silverwing.core.ExternalSkillSourceSnapshot
import com.snowball.silverwing.core.SkillSource
import com.snowball.silverwing.core.defaultExtensionSourceName
import com.snowball.silverwing.core.preferredRemoteGitBranch
import com.snowball.silverwing.core.requiresRootMarketplaceMigration
import java.nio.file.Path
import java.util.UUID

private data class PendingPluginAction(
    val source: CodexPluginMarketplaceSource,
    val plugin: CodexPluginCatalogItem,
    val takeOver: Boolean,
    val uninstall: Boolean,
)

private data class PendingSkillAction(
    val source: SkillSource,
    val skill: ExternalSkillCatalogItem,
    val action: SkillCatalogAction,
    val takeOver: Boolean,
)

private enum class SkillCatalogAction {
    INSTALL,
    UPDATE,
    UNINSTALL,
}

@Composable
internal fun SettingsCodexPluginsSection(controller: DesktopApplication) {
    val extensions = controller.codexExtensionsController
    val snapshot = extensions.state.snapshot
    var adding by remember { mutableStateOf(false) }
    var viewing by remember { mutableStateOf<CodexPluginMarketplaceSource?>(null) }
    var removing by remember { mutableStateOf<CodexPluginMarketplaceSource?>(null) }
    var pendingAction by remember { mutableStateOf<PendingPluginAction?>(null) }

    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text("插件来源", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(
                    "${controller.config.codexPluginMarketplaceSources.size} 个来源",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Button(onClick = { adding = true }, enabled = !controller.settingsBusy) {
                Icon(Icons.Outlined.Add, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text("添加来源")
            }
        }
        if (controller.settingsBusy) LinearProgressIndicator(Modifier.fillMaxWidth())

        if (controller.config.codexPluginMarketplaceSources.isEmpty()) {
            SettingsEmptyState("还没有插件来源。仓库中需包含 Codex Marketplace JSON 清单。")
        } else {
            controller.config.codexPluginMarketplaceSources.forEach { source ->
                val status = snapshot.marketplaces[source.id]
                ExtensionSourceCard(
                    title = source.name,
                    repositoryUrl = source.repositoryUrl,
                    metadataLines = pluginSourceMetadataLines(source, status),
                    error = status?.error,
                    countLabel = extensionSourceCountLabel(status?.plugins?.size, "插件"),
                    onView = { viewing = source },
                    onRefresh = { extensions.refreshMarketplace(source) },
                    onRemove = { removing = source },
                    enabled = !controller.settingsBusy,
                )
            }
        }
    }

    if (adding) PluginSourceEditorDialog(
        extensions = extensions,
        onDismiss = { adding = false },
        onSave = { source ->
            extensions.addMarketplace(source)
            adding = false
        },
    )
    viewing?.let { source ->
        PluginCatalogDialog(
            source = source,
            plugins = snapshot.marketplaces[source.id]?.plugins.orEmpty(),
            error = snapshot.marketplaces[source.id]?.error,
            extensions = extensions,
            ownership = { extensions.pluginOwnership(source.id, it) },
            enabled = !controller.settingsBusy,
            onCopySource = { content -> controller.copyText(content, "Markdown 源码已复制") },
            onCopyCode = { content -> controller.copyText(content, "代码已复制") },
            onCopyPath = { path -> controller.copyText(path.toAbsolutePath().toString(), "文件路径已复制") },
            onCopyFile = controller::copyFile,
            onDismiss = { viewing = null },
            onAction = { plugin, ownership ->
                pendingAction = PendingPluginAction(
                    source = source,
                    plugin = plugin,
                    takeOver = ownership == CodexExtensionOwnership.EXTERNAL,
                    uninstall = ownership == CodexExtensionOwnership.MANAGED,
                )
            },
        )
    }
    removing?.let { source ->
        ConfirmExtensionDialog(
            title = "移除插件来源？",
            message = "将从 Codex 移除 Marketplace “${source.name}”并删除 SilverWing 的本机缓存。已安装插件不会自动卸载。",
            confirmLabel = "移除来源",
            onDismiss = { removing = null },
            onConfirm = {
                extensions.removeMarketplace(source)
                removing = null
            },
        )
    }
    pendingAction?.let { action ->
        val title = when {
            action.uninstall -> "卸载 ${action.plugin.name}？"
            action.takeOver -> "接管 ${action.plugin.name}？"
            else -> "安装 ${action.plugin.name}？"
        }
        val message = when {
            action.uninstall -> "将通过 Codex 卸载这个由 SilverWing 管理的插件。"
            action.takeOver -> "该插件已由外部方式安装。接管后，SilverWing 才能卸载它；不会重新下载插件。"
            else -> "将通过本机 Codex CLI 安装该插件。完成后请新建或重启 Codex 会话。"
        }
        ConfirmExtensionDialog(
            title = title,
            message = message,
            confirmLabel = if (action.uninstall) "卸载" else if (action.takeOver) "接管" else "安装",
            destructive = action.uninstall,
            onDismiss = { pendingAction = null },
            onConfirm = {
                if (action.uninstall) extensions.uninstallPlugin(action.source, action.plugin)
                else extensions.installPlugin(action.source, action.plugin, action.takeOver)
                pendingAction = null
            },
        )
    }
}

/** Settings-only management surface for remote Skill sources and their installations. */
@Composable
internal fun SettingsSkillSourcesSection(controller: DesktopApplication) {
    val extensions = controller.codexExtensionsController
    val snapshot = extensions.state.snapshot
    var adding by remember { mutableStateOf(false) }
    var viewing by remember { mutableStateOf<SkillSource?>(null) }
    var removing by remember { mutableStateOf<SkillSource?>(null) }
    var pendingAction by remember { mutableStateOf<PendingSkillAction?>(null) }
    // 安装状态存于本机扩展服务；操作成功后递增版本，确保已打开的弹窗会
    // 重新读取归属状态，而不是继续显示打开瞬间的旧按钮。
    var ownershipVersion by remember { mutableStateOf(0) }

    LaunchedEffect(Unit) {
        extensions.refreshCached()
    }

    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text("Skill 安装", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(
                    "管理 Git 来源，并将发现的 Skill 安装到本机。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Button(
                onClick = { adding = true },
                enabled = !controller.settingsBusy,
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary),
            ) {
                Icon(Icons.Outlined.Add, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text("添加来源")
            }
        }
        if (controller.settingsBusy) LinearProgressIndicator(Modifier.fillMaxWidth())

        if (controller.config.skillSources.isEmpty()) {
            SettingsEmptyState("还没有 Skill 来源。可添加包含 SKILL.md 的 Git 仓库。")
        } else {
            controller.config.skillSources.forEach { source ->
                val status = snapshot.skillSources[source.id]
                ExtensionSourceCard(
                    title = source.name,
                    repositoryUrl = source.repositoryUrl,
                    metadataLines = skillSourceMetadataLines(source, status),
                    error = status?.error,
                    countLabel = extensionSourceCountLabel(status?.skills?.size, "Skill"),
                    onView = { viewing = source },
                    onRefresh = { extensions.refreshSkillSource(source) },
                    onRemove = { removing = source },
                    enabled = !controller.settingsBusy,
                )
            }
        }
    }

    if (adding) SkillSourceEditorDialog(
        extensions = extensions,
        onDismiss = { adding = false },
        onSave = { source ->
            extensions.addSkillSource(source)
            adding = false
        },
    )
    viewing?.let { source ->
        SkillCatalogDialog(
            source = source,
            skills = snapshot.skillSources[source.id]?.skills.orEmpty(),
            error = snapshot.skillSources[source.id]?.error,
            extensions = extensions,
            ownership = { skill -> ownershipVersion.let { extensions.skillOwnership(source.id, skill) } },
            enabled = !controller.settingsBusy,
            onCopySource = { content -> controller.copyText(content, "Markdown 源码已复制") },
            onCopyCode = { content -> controller.copyText(content, "代码已复制") },
            onCopyPath = { path -> controller.copyText(path.toAbsolutePath().toString(), "文件路径已复制") },
            onCopyFile = controller::copyFile,
            onDismiss = { viewing = null },
            onAction = { skill, ownership, action ->
                pendingAction = PendingSkillAction(
                    source = source,
                    skill = skill,
                    action = action,
                    takeOver = ownership == CodexExtensionOwnership.EXTERNAL,
                )
            },
        )
    }
    removing?.let { source ->
        ConfirmExtensionDialog(
            title = "移除 Skill 来源？",
            message = "将删除 SilverWing 缓存的仓库与发现记录；已安装到 ~/.agents/skills 的 Skill 不会被卸载。",
            confirmLabel = "移除来源",
            onDismiss = { removing = null },
            onConfirm = {
                extensions.removeSkillSource(source)
                removing = null
            },
        )
    }
    pendingAction?.let { action ->
        val title = when {
            action.action == SkillCatalogAction.UNINSTALL -> "卸载 ${action.skill.name}？"
            action.action == SkillCatalogAction.UPDATE -> "更新 ${action.skill.name}？"
            action.takeOver -> "接管 ${action.skill.name}？"
            else -> "安装 ${action.skill.name}？"
        }
        val message = when {
            action.action == SkillCatalogAction.UNINSTALL -> "将删除 ~/.agents/skills/${action.skill.name}，并在 SilverWing 本机目录保留恢复备份。"
            action.action == SkillCatalogAction.UPDATE -> "将先备份当前 ~/.agents/skills/${action.skill.name}，再用该来源的已加载版本完整覆盖。"
            action.takeOver -> "该 Skill 已在 ~/.agents/skills 中存在。接管后会先备份再覆盖，并由 SilverWing 管理后续卸载。"
            else -> "将完整复制 Skill 目录到 ~/.agents/skills/${action.skill.name}。完成后请新建或重启 Codex 会话。"
        }
        ConfirmExtensionDialog(
            title = title,
            message = message,
            confirmLabel = when {
                action.action == SkillCatalogAction.UNINSTALL -> "卸载"
                action.action == SkillCatalogAction.UPDATE -> "更新"
                action.takeOver -> "接管并安装"
                else -> "安装"
            },
            destructive = action.action == SkillCatalogAction.UNINSTALL,
            onDismiss = { pendingAction = null },
            onConfirm = {
                when (action.action) {
                    SkillCatalogAction.UNINSTALL -> extensions.uninstallSkill(action.source, action.skill)
                    SkillCatalogAction.UPDATE -> extensions.updateSkill(action.source, action.skill)
                    SkillCatalogAction.INSTALL -> extensions.installSkill(action.source, action.skill, action.takeOver)
                }
                pendingAction = null
                ownershipVersion++
            },
        )
    }
}

@Composable
private fun ExtensionSourceCard(
    title: String,
    repositoryUrl: String,
    metadataLines: List<String>,
    error: String?,
    countLabel: String?,
    onView: () -> Unit,
    onRefresh: () -> Unit,
    onRemove: () -> Unit,
    enabled: Boolean,
) {
    OutlinedCard(
        Modifier.fillMaxWidth(),
        colors = CardDefaults.outlinedCardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
    ) {
        Row(
            Modifier.fillMaxWidth().padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        title,
                        // Keep the count next to its source title instead of pushing it to the
                        // right edge of the card. The action group remains independently aligned.
                        Modifier.weight(1f, fill = false),
                        fontWeight = FontWeight.SemiBold,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    countLabel?.let { SourceItemCountBadge(it) }
                }
                Text(repositoryUrl, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis)
                metadataLines.filter { it.isNotBlank() }.forEach { metadata ->
                    Text(
                        metadata,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                error?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error, maxLines = 4, overflow = TextOverflow.Ellipsis)
                }
            }
            Spacer(Modifier.width(8.dp))
            IconActionGroup {
                ActionIconButton("查看来源内容", onView, Modifier.size(32.dp), enabled) {
                    Icon(Icons.Outlined.Visibility, "查看来源内容", Modifier.size(17.dp))
                }
                ActionIconButton("刷新来源", onRefresh, Modifier.size(32.dp), enabled) {
                    Icon(Icons.Outlined.Refresh, "刷新来源", Modifier.size(17.dp))
                }
                ActionIconButton("移除来源", onRemove, Modifier.size(32.dp), enabled) {
                    Icon(
                        Icons.Outlined.Delete,
                        "移除来源",
                        Modifier.size(17.dp),
                        tint = MaterialTheme.colorScheme.error,
                    )
                }
            }
        }
    }
}

@Composable
private fun SourceItemCountBadge(label: String) {
    val color = MaterialTheme.colorScheme.primary
    Surface(
        color = color.copy(alpha = 0.10f),
        shape = RoundedCornerShape(50),
        border = BorderStroke(1.dp, color.copy(alpha = 0.20f)),
    ) {
        Text(
            label,
            Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
            color = color,
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            softWrap = false,
        )
    }
}

internal fun extensionSourceCountLabel(count: Int?, itemName: String): String? = count?.let {
    val separator = if (itemName.firstOrNull()?.code?.let { code -> code in 0..0x7f } == true) " " else ""
    "$it 个$separator$itemName"
}

internal fun pluginSourceMetadataLines(
    source: CodexPluginMarketplaceSource,
    status: CodexPluginMarketplaceSnapshot?,
): List<String> {
    val sourceDetails = listOfNotNull(
        source.ref?.let { "分支：$it" },
        if (source.requiresRootMarketplaceMigration()) "需要迁移：旧版子目录 ${source.marketplaceDirectory}" else "Marketplace：仓库根目录",
    ).joinToString(" · ")
    val runtimeDetails = listOfNotNull(
        status?.marketplaceName
            ?.takeUnless { it.equals(source.name, ignoreCase = true) }
            ?.let { "Codex 名称：$it" },
        status?.updatedAt?.let { "最近成功：$it" },
    ).joinToString(" · ")
    return listOfNotNull(sourceDetails.takeIf { it.isNotBlank() }, runtimeDetails.takeIf { it.isNotBlank() })
}

internal fun skillSourceMetadataLines(
    source: SkillSource,
    status: ExternalSkillSourceSnapshot?,
): List<String> = listOfNotNull(
    listOfNotNull(
        source.ref?.let { "分支：$it" },
        skillSourceDirectoryStatus(source, status),
    ).joinToString(" · ").takeIf { it.isNotBlank() },
    status?.updatedAt?.let { "最近成功：$it" },
)

internal fun skillSourceDirectoryStatus(source: SkillSource, status: ExternalSkillSourceSnapshot?): String {
    val roots = effectiveSkillSourceRoots(source, status)
    return when {
        roots.isNotEmpty() -> "发现目录：${roots.joinToString("、")}"
        status?.updatedAt != null && status.error == null -> "未发现有效 Skill 目录"
        else -> "尚未发现目录"
    }
}

internal fun effectiveSkillSourceRoots(source: SkillSource, status: ExternalSkillSourceSnapshot?): List<String> {
    val persistedRoots = status?.discoveredRoots.orEmpty()
        .mapNotNull(::repositoryRelativePathOrNull)
        .distinct()
    if (persistedRoots.isNotEmpty()) return persistedRoots

    val skills = status?.skills.orEmpty()
    if (skills.isEmpty()) return emptyList()
    source.skillRoot?.let { configuredRoot ->
        return listOfNotNull(repositoryRelativePathOrNull(configuredRoot))
    }
    return skills.mapNotNull { skill ->
        val path = skill.sourcePath.replace('\\', '/')
        when {
            path == ".agents/skills" || path.startsWith(".agents/skills/") -> ".agents/skills"
            path == "skills" || path.startsWith("skills/") -> "skills"
            else -> null
        }
    }.distinct()
}

private fun repositoryRelativePathOrNull(value: String): String? {
    val normalized = value.trim().replace('\\', '/')
    if (normalized.isBlank() || normalized.startsWith('/') || Regex("^[A-Za-z]:").containsMatchIn(normalized)) return null
    val withoutLeadingCurrentDirectory = normalized.removePrefix("./").trimEnd('/')
    if (withoutLeadingCurrentDirectory.split('/').any { it.isEmpty() || it == ".." }) return null
    return withoutLeadingCurrentDirectory.ifBlank { "." }
}

@Composable
private fun SettingsEmptyState(message: String) {
    OutlinedCard(Modifier.fillMaxWidth()) {
        Text(message, Modifier.padding(20.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun PluginSourceEditorDialog(
    extensions: CodexExtensionsController,
    onDismiss: () -> Unit,
    onSave: (CodexPluginMarketplaceSource) -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var url by remember { mutableStateOf("") }
    var ref by remember { mutableStateOf("") }
    var branches by remember { mutableStateOf<RemoteBranchLoadState>(RemoteBranchLoadState.Idle) }
    var error by remember { mutableStateOf<String?>(null) }
    SourceEditorDialog(
        title = "添加 Codex 插件来源",
        fields = {
            OutlinedTextField(name, { name = it; error = null }, Modifier.fillMaxWidth(), label = { Text("显示名称") }, singleLine = true)
            ExtensionRepositoryUrlField(
                url = url,
                onUrlChanged = { changed ->
                    url = changed
                    if (name.isBlank()) name = defaultExtensionSourceName(changed)
                    branches = RemoteBranchLoadState.Idle; ref = ""; error = null
                },
                onLoad = { value -> extensions.loadRemoteBranches(value) { branches = it } },
                placeholder = "http(s)://git.example/team/plugins.git 或 git@gitlab.example:team/plugins.git",
            )
            RemoteBranchPicker(
                ref = ref,
                state = branches,
                onSelected = { ref = it; error = null },
                onReload = {
                    if (runCatching { com.snowball.silverwing.core.validateRemoteGitUrl(url.trim()) }.isSuccess) {
                        extensions.loadRemoteBranches(url.trim()) { branches = it }
                    } else {
                        error = "请先填写合法的 Git 仓库地址"
                    }
                },
            )
            Text("Marketplace 固定使用仓库根目录。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        },
        error = error,
        onDismiss = onDismiss,
        onConfirm = {
            runCatching {
                CodexPluginMarketplaceSource(UUID.randomUUID().toString(), name.trim(), url.trim(), ref.trim().ifBlank { null }, ".")
            }.onSuccess(onSave).onFailure { error = it.message ?: "插件来源配置不合法" }
        },
    )
}

@Composable
private fun SkillSourceEditorDialog(
    extensions: CodexExtensionsController,
    onDismiss: () -> Unit,
    onSave: (SkillSource) -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var url by remember { mutableStateOf("") }
    var ref by remember { mutableStateOf("") }
    var directory by remember { mutableStateOf("skills") }
    var branches by remember { mutableStateOf<RemoteBranchLoadState>(RemoteBranchLoadState.Idle) }
    var error by remember { mutableStateOf<String?>(null) }
    SourceEditorDialog(
        title = "添加 Skill 来源",
        fields = {
            OutlinedTextField(name, { name = it; error = null }, Modifier.fillMaxWidth(), label = { Text("显示名称") }, singleLine = true)
            ExtensionRepositoryUrlField(
                url = url,
                onUrlChanged = { changed ->
                    url = changed
                    if (name.isBlank()) name = defaultExtensionSourceName(changed)
                    branches = RemoteBranchLoadState.Idle; ref = ""; error = null
                },
                onLoad = { value -> extensions.loadRemoteBranches(value) { branches = it } },
                placeholder = "http(s)://git.example/team/skills.git 或 git@gitlab.example:team/skills.git",
            )
            RemoteBranchPicker(
                ref = ref,
                state = branches,
                onSelected = { ref = it; error = null },
                onReload = {
                    if (runCatching { com.snowball.silverwing.core.validateRemoteGitUrl(url.trim()) }.isSuccess) {
                        extensions.loadRemoteBranches(url.trim()) { branches = it }
                    } else {
                        error = "请先填写合法的 Git 仓库地址"
                    }
                },
            )
            OutlinedTextField(directory, { directory = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Skill 相对目录") }, supportingText = { Text("默认 skills；可自行修改") }, singleLine = true)
        },
        error = error,
        onDismiss = onDismiss,
        onConfirm = {
            runCatching {
                SkillSource(UUID.randomUUID().toString(), name.trim(), url.trim(), ref.trim().ifBlank { null }, directory.trim().ifBlank { null })
            }.onSuccess(onSave).onFailure { error = it.message ?: "Skill 来源配置不合法" }
        },
    )
}

@Composable
private fun ExtensionRepositoryUrlField(
    url: String,
    onUrlChanged: (String) -> Unit,
    onLoad: (String) -> Unit,
    placeholder: String,
) {
    OutlinedTextField(
        value = url,
        onValueChange = onUrlChanged,
        modifier = Modifier.fillMaxWidth().onFocusChanged { focus ->
            if (!focus.isFocused && runCatching { com.snowball.silverwing.core.validateRemoteGitUrl(url.trim()) }.isSuccess) {
                onLoad(url.trim())
            }
        },
        label = { Text("Git 仓库地址") },
        placeholder = { Text(placeholder) },
        singleLine = true,
    )
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun RemoteBranchPicker(
    ref: String,
    state: RemoteBranchLoadState,
    onSelected: (String) -> Unit,
    onReload: () -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }
    val branches = (state as? RemoteBranchLoadState.Loaded)?.branches.orEmpty()
    androidx.compose.runtime.LaunchedEffect(branches) {
        if (ref.isBlank()) preferredRemoteGitBranch(branches)?.let(onSelected)
    }
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it && state is RemoteBranchLoadState.Loaded }) {
        OutlinedTextField(
            value = ref,
            onValueChange = onSelected,
            modifier = Modifier.menuAnchor().fillMaxWidth(),
            readOnly = false,
            enabled = state !is RemoteBranchLoadState.Loading,
            label = { Text("Git ref（可选）") },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded) },
            supportingText = {
                when (state) {
                    RemoteBranchLoadState.Idle -> Text("可手动填写；填写地址并离开输入框后会自动读取分支")
                    RemoteBranchLoadState.Loading -> Text("正在读取远程分支…")
                    is RemoteBranchLoadState.Failed -> Text("读取失败，仍可手动填写分支：${state.message}")
                    is RemoteBranchLoadState.Loaded -> if (branches.isEmpty()) Text("仓库没有可选分支；可不选择 ref")
                }
            },
            singleLine = true,
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            branches.forEach { branch ->
                DropdownMenuItem(text = { Text(branch) }, onClick = { onSelected(branch); expanded = false })
            }
        }
    }
    OutlinedButton(
        onClick = onReload,
        enabled = state !is RemoteBranchLoadState.Loading,
    ) {
        Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp))
        Spacer(Modifier.width(5.dp))
        Text(if (state is RemoteBranchLoadState.Loaded) "重新读取分支" else "读取分支")
    }
}

@Composable
private fun SourceEditorDialog(
    title: String,
    fields: @Composable () -> Unit,
    error: String?,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(Modifier.widthIn(min = 480.dp, max = 720.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                fields()
                error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = { TextButton(onClick = onConfirm) { Text("保存并加载") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
private fun PluginCatalogDialog(
    source: CodexPluginMarketplaceSource,
    plugins: List<CodexPluginCatalogItem>,
    error: String?,
    extensions: CodexExtensionsController,
    ownership: (CodexPluginCatalogItem) -> CodexExtensionOwnership,
    enabled: Boolean,
    onCopySource: (String) -> Unit,
    onCopyCode: (String) -> Unit,
    onCopyPath: (Path) -> Unit,
    onCopyFile: (Path) -> Unit,
    onDismiss: () -> Unit,
    onAction: (CodexPluginCatalogItem, CodexExtensionOwnership) -> Unit,
) {
    var selected by remember(source.id, plugins) { mutableStateOf(plugins.firstOrNull()) }
    var preview by remember(source.id, selected?.name) { mutableStateOf<PluginPreviewState>(PluginPreviewState.Empty) }
    var selectedSkill by remember(source.id, selected?.name) { mutableStateOf<String?>(null) }
    var skillPreview by remember(source.id, selected?.name, selectedSkill) { mutableStateOf<SkillPreviewState>(SkillPreviewState.Empty) }
    LaunchedEffect(source.id, selected?.name) {
        selectedSkill = null
        selected?.let { plugin -> extensions.previewPlugin(source, plugin) { preview = it } }
    }
    LaunchedEffect(source.id, selected?.name, selectedSkill) {
        val plugin = selected
        val skill = selectedSkill
        if (plugin != null && skill != null) extensions.previewPluginSkill(source, plugin, skill) { skillPreview = it }
    }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        OutlinedCard(
            Modifier.fillMaxWidth(0.80f).fillMaxHeight(0.80f).widthIn(min = 900.dp, max = 1600.dp),
            colors = CardDefaults.outlinedCardColors(containerColor = MaterialTheme.colorScheme.surface),
        ) {
            Column(Modifier.fillMaxSize().padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("${source.name} 的插件（${plugins.size}）", style = MaterialTheme.typography.titleLarge)
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                if (plugins.isEmpty() && error == null) Text("尚无已加载的插件。可先关闭窗口后点击“刷新”。", color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (plugins.isNotEmpty()) Row(Modifier.weight(1f).fillMaxWidth()) {
                    OutlinedCard(Modifier.weight(0.34f).fillMaxHeight()) {
                        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            plugins.forEach { plugin ->
                                val isSelected = selected?.name == plugin.name
                                val pluginOwnership = ownership(plugin)
                                Column(
                                    Modifier.fillMaxWidth()
                                        .background(if (isSelected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                                        .clickable { selected = plugin }
                                        .padding(10.dp),
                                    verticalArrangement = Arrangement.spacedBy(3.dp),
                                ) {
                                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                        Text(plugin.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                                        plugin.version?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                                    }
                                    Text(plugin.description.ifBlank { "暂无说明" }, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                    Text(pluginOwnership.displayText(), style = MaterialTheme.typography.labelSmall, color = pluginOwnership.color())
                                }
                            }
                        }
                    }
                    Spacer(Modifier.width(12.dp))
                    OutlinedCard(Modifier.weight(0.66f).fillMaxHeight()) {
                        Column(Modifier.fillMaxSize().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            selected?.let { plugin ->
                                val pluginOwnership = ownership(plugin)
                                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                    Column(Modifier.weight(1f)) {
                                        Text(plugin.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold)
                                        Text("${plugin.version ?: "未标注版本"} · ${pluginOwnership.displayText()}", style = MaterialTheme.typography.labelSmall, color = pluginOwnership.color())
                                    }
                                    val label = when (pluginOwnership) {
                                        CodexExtensionOwnership.NOT_INSTALLED -> "安装"
                                        CodexExtensionOwnership.EXTERNAL -> "接管"
                                        CodexExtensionOwnership.MANAGED -> "卸载"
                                    }
                                    OutlinedButton(onClick = { onAction(plugin, pluginOwnership) }, enabled = enabled) { Text(label) }
                                }
                                HorizontalDivider()
                                if (selectedSkill != null) {
                                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                        Text("内置 Skill：$selectedSkill", fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
                                        TextButton(onClick = { selectedSkill = null }) { Text("返回概览") }
                                    }
                                    when (val current = skillPreview) {
                                        SkillPreviewState.Empty -> Text("请选择一个内置 Skill", color = MaterialTheme.colorScheme.onSurfaceVariant)
                                        SkillPreviewState.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                                        is SkillPreviewState.Failed -> Text(current.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                                        is SkillPreviewState.Loaded -> Box(Modifier.weight(1f).fillMaxWidth()) {
                                            val sourcePath = remember(source.id, selected?.name, selectedSkill, current.content) {
                                                selected?.let { plugin ->
                                                    selectedSkill?.let { skill ->
                                                        runCatching { extensions.previewPluginSkillPath(source, plugin, skill) }.getOrNull()
                                                    }
                                                }
                                            }
                                            MarkdownDocumentPreview(
                                                content = current.content,
                                                modifier = Modifier.fillMaxSize(),
                                                documentKey = selectedSkill,
                                                onCopySource = { onCopySource(current.content) },
                                                sourcePath = sourcePath,
                                                onCopyPath = onCopyPath,
                                                onCopyFile = onCopyFile,
                                                onCopyCode = onCopyCode,
                                            )
                                        }
                                    }
                                } else {
                                    when (val current = preview) {
                                        PluginPreviewState.Empty -> Text("请选择一个插件", color = MaterialTheme.colorScheme.onSurfaceVariant)
                                        PluginPreviewState.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                                        is PluginPreviewState.Failed -> Text(current.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                                        is PluginPreviewState.Loaded -> PluginOverview(current.content, Modifier.weight(1f).fillMaxWidth(), onSkillSelected = { selectedSkill = it })
                                    }
                                }
                            }
                        }
                    }
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onDismiss) { Text("关闭") }
                }
            }
        }
    }
}

@Composable
private fun PluginOverview(
    preview: com.snowball.silverwing.core.CodexPluginPreview,
    modifier: Modifier,
    onSkillSelected: (String) -> Unit,
) {
    Column(modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text(preview.description.ifBlank { "该插件未提供说明。" }, style = MaterialTheme.typography.bodyMedium)
        preview.author?.let { Text("作者：$it", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        preview.category?.let { Text("分类：$it", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        if (preview.capabilities.isNotEmpty()) Text("能力：${preview.capabilities.joinToString(" · ")}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text("内置 Skills（${preview.skills.size}）", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        if (preview.skills.isEmpty()) Text("该插件没有可预览的内置 Skill。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        preview.skills.forEach { skill ->
            OutlinedCard(Modifier.fillMaxWidth().clickable { onSkillSelected(skill.name) }) {
                Column(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    Text(skill.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold)
                    skill.description.takeIf(String::isNotBlank)?.let { Text(it, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                }
            }
        }
        preview.readme?.takeIf(String::isNotBlank)?.let { Text("README 已缓存，可在后续版本单独打开预览。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}

@Composable
private fun SkillCatalogDialog(
    source: SkillSource,
    skills: List<ExternalSkillCatalogItem>,
    error: String?,
    extensions: CodexExtensionsController,
    ownership: (ExternalSkillCatalogItem) -> CodexExtensionOwnership,
    enabled: Boolean,
    onCopySource: (String) -> Unit,
    onCopyCode: (String) -> Unit,
    onCopyPath: (Path) -> Unit,
    onCopyFile: (Path) -> Unit,
    onDismiss: () -> Unit,
    onAction: (ExternalSkillCatalogItem, CodexExtensionOwnership, SkillCatalogAction) -> Unit,
) {
    var selected by remember(source.id, skills) { mutableStateOf(skills.firstOrNull()) }
    var preview by remember(source.id, selected?.name) { mutableStateOf<SkillPreviewState>(SkillPreviewState.Empty) }
    LaunchedEffect(source.id, selected?.name) {
        selected?.let { skill -> extensions.previewSkill(source, skill) { preview = it } }
    }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        OutlinedCard(
            Modifier.fillMaxWidth(0.80f).fillMaxHeight(0.80f).widthIn(min = 900.dp, max = 1600.dp),
            colors = CardDefaults.outlinedCardColors(containerColor = MaterialTheme.colorScheme.surface),
        ) {
            Column(Modifier.fillMaxSize().padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("${source.name} 的 Skills（${skills.size}）", style = MaterialTheme.typography.titleLarge)
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                if (skills.isEmpty() && error == null) Text("尚无已加载的 Skill。可先关闭窗口后点击“刷新”。", color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (skills.isNotEmpty()) Row(Modifier.weight(1f).fillMaxWidth()) {
                    OutlinedCard(Modifier.weight(0.34f).fillMaxHeight()) {
                        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            skills.forEach { skill ->
                                val isSelected = selected?.name == skill.name
                                Column(
                                    Modifier.fillMaxWidth()
                                        .background(if (isSelected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                                        .clickable { selected = skill }
                                        .padding(10.dp),
                                    verticalArrangement = Arrangement.spacedBy(3.dp),
                                ) {
                                    Text(skill.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text(skill.description, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                }
                            }
                        }
                    }
                    Spacer(Modifier.width(12.dp))
                    OutlinedCard(Modifier.weight(0.66f).fillMaxHeight()) {
                        Column(Modifier.fillMaxSize().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            selected?.let { skill ->
                                val skillOwnership = ownership(skill)
                                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                    Column(Modifier.weight(1f)) {
                                        Text(skill.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold)
                                        Text(skill.sourcePath, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    }
                                    when (skillOwnership) {
                                        CodexExtensionOwnership.MANAGED -> Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                            OutlinedButton(onClick = { onAction(skill, skillOwnership, SkillCatalogAction.UPDATE) }, enabled = enabled) { Text("更新") }
                                            OutlinedButton(onClick = { onAction(skill, skillOwnership, SkillCatalogAction.UNINSTALL) }, enabled = enabled) { Text("卸载") }
                                        }
                                        else -> {
                                            val label = if (skillOwnership == CodexExtensionOwnership.EXTERNAL) "接管" else "安装"
                                            OutlinedButton(onClick = { onAction(skill, skillOwnership, SkillCatalogAction.INSTALL) }, enabled = enabled) { Text(label) }
                                        }
                                    }
                                }
                                HorizontalDivider()
                                when (val current = preview) {
                                    SkillPreviewState.Empty -> Text("请选择一个 Skill", color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    SkillPreviewState.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                                    is SkillPreviewState.Failed -> Text(current.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                                    is SkillPreviewState.Loaded -> {
                                        val sourcePath = remember(source.id, selected?.name, current.content) {
                                            selected?.let { skill -> runCatching { extensions.previewSkillPath(source, skill) }.getOrNull() }
                                        }
                                        MarkdownDocumentPreview(
                                            content = current.content,
                                            modifier = Modifier.weight(1f).fillMaxWidth(),
                                            documentKey = selected?.name ?: current.content,
                                            onCopySource = { onCopySource(current.content) },
                                            sourcePath = sourcePath,
                                            onCopyPath = onCopyPath,
                                            onCopyFile = onCopyFile,
                                            onCopyCode = onCopyCode,
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onDismiss) { Text("关闭") }
                }
            }
        }
    }
}

@Composable
private fun ConfirmExtensionDialog(
    title: String,
    message: String,
    confirmLabel: String,
    destructive: Boolean = false,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(message) },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(confirmLabel, color = if (destructive) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

private fun CodexExtensionOwnership.displayText(): String = when (this) {
    CodexExtensionOwnership.NOT_INSTALLED -> "未安装"
    CodexExtensionOwnership.MANAGED -> "已由 SilverWing 管理"
    CodexExtensionOwnership.EXTERNAL -> "外部安装/占用"
}

@Composable
private fun CodexExtensionOwnership.color() = when (this) {
    CodexExtensionOwnership.NOT_INSTALLED -> MaterialTheme.colorScheme.onSurfaceVariant
    CodexExtensionOwnership.MANAGED -> MaterialTheme.colorScheme.primary
    CodexExtensionOwnership.EXTERNAL -> MaterialTheme.colorScheme.tertiary
}
