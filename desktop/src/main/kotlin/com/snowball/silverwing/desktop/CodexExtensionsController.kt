package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.CodexExtensionOwnership
import com.snowball.silverwing.core.CodexExtensionsService
import com.snowball.silverwing.core.CodexExtensionsSnapshot
import com.snowball.silverwing.core.CodexPluginCatalogItem
import com.snowball.silverwing.core.CodexPluginMarketplaceSource
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.ExternalSkillCatalogItem
import com.snowball.silverwing.core.SkillSource

internal data class CodexExtensionsUiState(
    val snapshot: CodexExtensionsSnapshot,
)

/** Keeps UI configuration writes and local Codex/Skill side effects in one deliberately serialized lane. */
internal class CodexExtensionsController(
    private val session: AppSessionStore,
    private val configStore: ConfigStore,
    private val extensions: CodexExtensionsService,
    private val operations: OperationRunner,
    private val applyConfig: (AppConfig) -> Unit,
) {
    var state by mutableStateOf(CodexExtensionsUiState(cachedSnapshot()))
        private set

    fun refreshCached() {
        state = CodexExtensionsUiState(cachedSnapshot())
    }

    fun addMarketplace(source: CodexPluginMarketplaceSource): Boolean = operations.run(
        activeMessage = "正在添加 Codex 插件来源…",
        successMessage = "插件来源已保存",
        cancellable = true,
        block = {
            val updated = configStore.update { config ->
                require(config.codexPluginMarketplaceSources.none { it.id.equals(source.id, ignoreCase = true) }) {
                    "插件来源 ID 已存在"
                }
                config.copy(codexPluginMarketplaceSources = config.codexPluginMarketplaceSources + source)
            }
            updated to extensions.addMarketplace(source)
        },
        onSuccess = { (config, _) ->
            applyConfig(config)
            refreshCached()
        },
        onFailure = { refreshCached() },
    )

    fun refreshMarketplace(source: CodexPluginMarketplaceSource): Boolean = operations.run(
        activeMessage = "正在刷新 Codex 插件来源…",
        successMessage = "插件来源已刷新",
        cancellable = true,
        block = { extensions.refreshMarketplace(source) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    fun removeMarketplace(source: CodexPluginMarketplaceSource): Boolean = operations.run(
        activeMessage = "正在移除 Codex 插件来源…",
        successMessage = "插件来源已移除；已安装插件保持不变",
        cancellable = true,
        block = {
            extensions.removeMarketplace(source)
            configStore.update { config ->
                config.copy(codexPluginMarketplaceSources = config.codexPluginMarketplaceSources.filterNot { it.id == source.id })
            }
        },
        onSuccess = {
            applyConfig(it)
            refreshCached()
        },
        onFailure = { refreshCached() },
    )

    fun pluginOwnership(sourceId: String, plugin: CodexPluginCatalogItem): CodexExtensionOwnership =
        extensions.pluginOwnership(sourceId, plugin)

    fun installPlugin(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem, takeOver: Boolean): Boolean = operations.run(
        activeMessage = "正在安装 ${plugin.name}…",
        successMessage = if (takeOver && plugin.installed) "插件已接管" else "插件已安装；请新建或重启 Codex 会话后使用",
        cancellable = true,
        block = { extensions.installPlugin(source, plugin.name, takeOver) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    fun uninstallPlugin(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem): Boolean = operations.run(
        activeMessage = "正在卸载 ${plugin.name}…",
        successMessage = "插件已卸载",
        cancellable = true,
        block = { extensions.uninstallPlugin(source, plugin.name) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    fun addSkillSource(source: SkillSource): Boolean = operations.run(
        activeMessage = "正在添加 Skill 来源…",
        successMessage = "Skill 来源已保存",
        cancellable = true,
        block = {
            val updated = configStore.update { config ->
                require(config.skillSources.none { it.id.equals(source.id, ignoreCase = true) }) { "Skill 来源 ID 已存在" }
                config.copy(skillSources = config.skillSources + source)
            }
            updated to extensions.addSkillSource(source)
        },
        onSuccess = { (config, _) ->
            applyConfig(config)
            refreshCached()
        },
        onFailure = { refreshCached() },
    )

    fun refreshSkillSource(source: SkillSource): Boolean = operations.run(
        activeMessage = "正在刷新 Skill 来源…",
        successMessage = "Skill 来源已刷新",
        cancellable = true,
        block = { extensions.refreshSkillSource(source) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    fun removeSkillSource(source: SkillSource): Boolean = operations.run(
        activeMessage = "正在移除 Skill 来源…",
        successMessage = "Skill 来源已移除；已安装 Skill 保持不变",
        cancellable = true,
        block = {
            extensions.removeSkillSource(source)
            configStore.update { config -> config.copy(skillSources = config.skillSources.filterNot { it.id == source.id }) }
        },
        onSuccess = {
            applyConfig(it)
            refreshCached()
        },
        onFailure = { refreshCached() },
    )

    fun skillOwnership(sourceId: String, skill: ExternalSkillCatalogItem): CodexExtensionOwnership =
        extensions.skillOwnership(sourceId, skill.name)

    fun installSkill(source: SkillSource, skill: ExternalSkillCatalogItem, takeOver: Boolean): Boolean = operations.run(
        activeMessage = "正在安装 ${skill.name}…",
        successMessage = "Skill 已安装；请新建或重启 Codex 会话后使用",
        cancellable = true,
        block = { extensions.installSkill(source, skill.name, takeOver) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    fun updateSkill(source: SkillSource, skill: ExternalSkillCatalogItem): Boolean = operations.run(
        activeMessage = "正在更新 ${skill.name}…",
        successMessage = "Skill 已更新；请新建或重启 Codex 会话后使用",
        cancellable = true,
        block = { extensions.installSkill(source, skill.name) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    fun uninstallSkill(source: SkillSource, skill: ExternalSkillCatalogItem): Boolean = operations.run(
        activeMessage = "正在卸载 ${skill.name}…",
        successMessage = "Skill 已卸载；恢复备份保留在 SilverWing 本机目录",
        cancellable = true,
        block = { extensions.uninstallSkill(source, skill.name) },
        onSuccess = { refreshCached() },
        onFailure = { refreshCached() },
    )

    private fun cachedSnapshot(): CodexExtensionsSnapshot =
        extensions.snapshot(session.config.codexPluginMarketplaceSources, session.config.skillSources)
}
