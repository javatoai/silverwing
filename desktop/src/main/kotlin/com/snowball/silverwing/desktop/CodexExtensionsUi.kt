package com.snowball.silverwing.desktop

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.CodexExtensionOwnership
import com.snowball.silverwing.core.CodexPluginCatalogItem
import com.snowball.silverwing.core.CodexPluginMarketplaceSource
import com.snowball.silverwing.core.ExternalSkillCatalogItem
import com.snowball.silverwing.core.SkillSource
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
        SettingsCard(
            "Codex 插件",
            "添加带 Marketplace 清单的 Git 仓库；SilverWing 通过本机 Codex CLI 注册、刷新、安装或卸载插件。",
        ) {
            Text(
                "仅保存仓库地址、ref 和 Marketplace 相对目录。认证继续使用本机 Git/Codex 已有凭据；进入本页不会自动联网。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            OutlinedButton(onClick = { adding = true }, enabled = !controller.settingsBusy) {
                Icon(Icons.Outlined.Add, null, Modifier.size(18.dp))
                Spacer(Modifier.width(5.dp))
                Text("添加插件来源")
            }
            if (controller.settingsBusy) LinearProgressIndicator(Modifier.fillMaxWidth())
        }

        if (controller.config.codexPluginMarketplaceSources.isEmpty()) {
            SettingsEmptyState("还没有插件来源。仓库中需包含 Codex Marketplace JSON 清单。")
        } else {
            controller.config.codexPluginMarketplaceSources.forEach { source ->
                val status = snapshot.marketplaces[source.id]
                ExtensionSourceCard(
                    title = source.name,
                    repositoryUrl = source.repositoryUrl,
                    details = listOfNotNull(
                        source.ref?.let { "ref：$it" },
                        "Marketplace：${source.marketplaceDirectory}",
                        status?.marketplaceName?.let { "Codex 名称：$it" },
                        status?.cacheDirectory?.let { "缓存：$it" },
                        status?.updatedAt?.let { "最近成功：$it" },
                    ).joinToString(" · "),
                    error = status?.error,
                    countLabel = status?.plugins?.size?.let { "$it 个插件" },
                    onView = { viewing = source },
                    onRefresh = { extensions.refreshMarketplace(source) },
                    onRemove = { removing = source },
                    enabled = !controller.settingsBusy,
                )
            }
        }
    }

    if (adding) PluginSourceEditorDialog(
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
            ownership = { extensions.pluginOwnership(source.id, it) },
            enabled = !controller.settingsBusy,
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

@Composable
internal fun SettingsExternalSkillsSection(controller: DesktopApplication) {
    val extensions = controller.codexExtensionsController
    val snapshot = extensions.state.snapshot
    var adding by remember { mutableStateOf(false) }
    var viewing by remember { mutableStateOf<SkillSource?>(null) }
    var removing by remember { mutableStateOf<SkillSource?>(null) }
    var pendingAction by remember { mutableStateOf<PendingSkillAction?>(null) }

    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        SettingsCard(
            "Skills",
            "从 Git 仓库发现独立 Skill，并安装到当前用户的 ~/.agents/skills；不会执行来源仓库中的脚本。",
        ) {
            Text(
                "未填写目录时只查找仓库的 .agents/skills 和 skills。刷新只更新缓存，已安装 Skill 需手动点击更新。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            OutlinedButton(onClick = { adding = true }, enabled = !controller.settingsBusy) {
                Icon(Icons.Outlined.Add, null, Modifier.size(18.dp))
                Spacer(Modifier.width(5.dp))
                Text("添加 Skill 来源")
            }
            if (controller.settingsBusy) LinearProgressIndicator(Modifier.fillMaxWidth())
        }

        if (controller.config.skillSources.isEmpty()) {
            SettingsEmptyState("还没有 Skill 来源。可添加包含 SKILL.md 的 Git 仓库。")
        } else {
            controller.config.skillSources.forEach { source ->
                val status = snapshot.skillSources[source.id]
                ExtensionSourceCard(
                    title = source.name,
                    repositoryUrl = source.repositoryUrl,
                    details = listOfNotNull(
                        source.ref?.let { "ref：$it" },
                        "目录：${source.skillRoot ?: "自动发现"}",
                        status?.updatedAt?.let { "最近成功：$it" },
                    ).joinToString(" · "),
                    error = status?.error,
                    countLabel = status?.skills?.size?.let { "$it 个 Skill" },
                    onView = { viewing = source },
                    onRefresh = { extensions.refreshSkillSource(source) },
                    onRemove = { removing = source },
                    enabled = !controller.settingsBusy,
                )
            }
        }
    }

    if (adding) SkillSourceEditorDialog(
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
            ownership = { extensions.skillOwnership(source.id, it) },
            enabled = !controller.settingsBusy,
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
            },
        )
    }
}

@Composable
private fun ExtensionSourceCard(
    title: String,
    repositoryUrl: String,
    details: String,
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
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(title, fontWeight = FontWeight.SemiBold)
                    Text(repositoryUrl, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                countLabel?.let { Text(it, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary) }
            }
            if (details.isNotBlank()) Text(details, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            error?.let {
                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error, maxLines = 4, overflow = TextOverflow.Ellipsis)
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = onView, enabled = enabled) {
                    Icon(Icons.Outlined.Visibility, null, Modifier.size(18.dp)); Spacer(Modifier.width(5.dp)); Text("查看")
                }
                OutlinedButton(onClick = onRefresh, enabled = enabled) {
                    Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp)); Spacer(Modifier.width(5.dp)); Text("刷新")
                }
                OutlinedButton(onClick = onRemove, enabled = enabled) {
                    Icon(Icons.Outlined.Delete, null, Modifier.size(18.dp)); Spacer(Modifier.width(5.dp)); Text("移除")
                }
            }
        }
    }
}

@Composable
private fun SettingsEmptyState(message: String) {
    OutlinedCard(Modifier.fillMaxWidth()) {
        Text(message, Modifier.padding(20.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun PluginSourceEditorDialog(onDismiss: () -> Unit, onSave: (CodexPluginMarketplaceSource) -> Unit) {
    var name by remember { mutableStateOf("") }
    var url by remember { mutableStateOf("") }
    var ref by remember { mutableStateOf("") }
    var directory by remember { mutableStateOf("plugins") }
    var error by remember { mutableStateOf<String?>(null) }
    SourceEditorDialog(
        title = "添加 Codex 插件来源",
        fields = {
            OutlinedTextField(name, { name = it; error = null }, Modifier.fillMaxWidth(), label = { Text("显示名称") }, singleLine = true)
            OutlinedTextField(url, { url = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Git 仓库地址") }, placeholder = { Text("https://github.com/org/plugins.git 或 git@gitlab.example:team/plugins.git") }, singleLine = true)
            OutlinedTextField(ref, { ref = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Git ref（可选）") }, singleLine = true)
            OutlinedTextField(directory, { directory = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Marketplace 相对目录") }, supportingText = { Text("默认 plugins；仓库根目录填写 .") }, singleLine = true)
        },
        error = error,
        onDismiss = onDismiss,
        onConfirm = {
            runCatching {
                CodexPluginMarketplaceSource(UUID.randomUUID().toString(), name.trim(), url.trim(), ref.trim().ifBlank { null }, directory.trim())
            }.onSuccess(onSave).onFailure { error = it.message ?: "插件来源配置不合法" }
        },
    )
}

@Composable
private fun SkillSourceEditorDialog(onDismiss: () -> Unit, onSave: (SkillSource) -> Unit) {
    var name by remember { mutableStateOf("") }
    var url by remember { mutableStateOf("") }
    var ref by remember { mutableStateOf("") }
    var directory by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    SourceEditorDialog(
        title = "添加 Skill 来源",
        fields = {
            OutlinedTextField(name, { name = it; error = null }, Modifier.fillMaxWidth(), label = { Text("显示名称") }, singleLine = true)
            OutlinedTextField(url, { url = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Git 仓库地址") }, placeholder = { Text("https://github.com/org/skills.git 或 git@gitlab.example:team/skills.git") }, singleLine = true)
            OutlinedTextField(ref, { ref = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Git ref（可选）") }, singleLine = true)
            OutlinedTextField(directory, { directory = it; error = null }, Modifier.fillMaxWidth(), label = { Text("Skill 相对目录（可选）") }, supportingText = { Text("留空时自动查找 .agents/skills 和 skills") }, singleLine = true)
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
    ownership: (CodexPluginCatalogItem) -> CodexExtensionOwnership,
    enabled: Boolean,
    onDismiss: () -> Unit,
    onAction: (CodexPluginCatalogItem, CodexExtensionOwnership) -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("${source.name} 的插件（${plugins.size}）") },
        text = {
            Column(Modifier.widthIn(min = 560.dp, max = 860.dp).heightIn(max = 620.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                if (plugins.isEmpty() && error == null) Text("尚无已加载的插件。可先关闭窗口后点击“刷新”。", color = MaterialTheme.colorScheme.onSurfaceVariant)
                plugins.forEach { plugin ->
                    val pluginOwnership = ownership(plugin)
                    OutlinedCard(Modifier.fillMaxWidth()) {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Text(plugin.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
                                plugin.version?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                            }
                            plugin.description.takeIf(String::isNotBlank)?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
                            if (plugin.bundledSkills.isNotEmpty()) Text("内置 Skills：${plugin.bundledSkills.joinToString("、")}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                                Text(pluginOwnership.displayText(), style = MaterialTheme.typography.labelMedium, color = pluginOwnership.color())
                                val label = when (pluginOwnership) {
                                    CodexExtensionOwnership.NOT_INSTALLED -> "安装"
                                    CodexExtensionOwnership.EXTERNAL -> "接管"
                                    CodexExtensionOwnership.MANAGED -> "卸载"
                                }
                                OutlinedButton(onClick = { onAction(plugin, pluginOwnership) }, enabled = enabled) { Text(label) }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
    )
}

@Composable
private fun SkillCatalogDialog(
    source: SkillSource,
    skills: List<ExternalSkillCatalogItem>,
    error: String?,
    ownership: (ExternalSkillCatalogItem) -> CodexExtensionOwnership,
    enabled: Boolean,
    onDismiss: () -> Unit,
    onAction: (ExternalSkillCatalogItem, CodexExtensionOwnership, SkillCatalogAction) -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("${source.name} 的 Skills（${skills.size}）") },
        text = {
            Column(Modifier.widthIn(min = 560.dp, max = 860.dp).heightIn(max = 620.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                if (skills.isEmpty() && error == null) Text("尚无已加载的 Skill。可先关闭窗口后点击“刷新”。", color = MaterialTheme.colorScheme.onSurfaceVariant)
                skills.forEach { skill ->
                    val skillOwnership = ownership(skill)
                    OutlinedCard(Modifier.fillMaxWidth()) {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Text(skill.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
                                Text(skill.sourcePath, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                            Text(skill.description, style = MaterialTheme.typography.bodySmall)
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                                Text(skillOwnership.displayText(), style = MaterialTheme.typography.labelMedium, color = skillOwnership.color())
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
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
    )
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
