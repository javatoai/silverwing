package com.snowball.silverwing.core

/**
 * Coordinates portable extension-source configuration with machine-local Codex
 * operations. Desktop callers only receive application-level results and never
 * need to compose configuration persistence with external side effects.
 */
class CodexExtensionsApplicationService(
    private val configurations: ConfigurationRepository = ConfigStore(),
    private val extensions: CodexExtensionsService = CodexExtensionsService(),
    private val branchCatalog: RemoteGitBranchCatalog = RemoteGitBranchCatalog(),
) {
    fun snapshot(): CodexExtensionsSnapshot {
        val config = configurations.load()
        return extensions.snapshot(config.codexPluginMarketplaceSources, config.skillSources)
    }

    fun loadRemoteBranches(repositoryUrl: String): List<String> = branchCatalog.load(repositoryUrl)

    fun addMarketplace(source: CodexPluginMarketplaceSource): AppConfig {
        val updated = configurations.update { config ->
            require(config.codexPluginMarketplaceSources.none { it.id.equals(source.id, ignoreCase = true) }) {
                "插件来源 ID 已存在"
            }
            config.copy(codexPluginMarketplaceSources = config.codexPluginMarketplaceSources + source)
        }
        extensions.addMarketplace(source)
        return updated
    }

    fun refreshMarketplace(source: CodexPluginMarketplaceSource): CodexPluginMarketplaceSnapshot =
        extensions.refreshMarketplace(source)

    fun removeMarketplace(source: CodexPluginMarketplaceSource): AppConfig {
        extensions.removeMarketplace(source)
        return configurations.update { config ->
            config.copy(codexPluginMarketplaceSources = config.codexPluginMarketplaceSources.filterNot { it.id == source.id })
        }
    }

    fun pluginOwnership(sourceId: String, plugin: CodexPluginCatalogItem): CodexExtensionOwnership =
        extensions.pluginOwnership(sourceId, plugin)

    fun installPlugin(
        source: CodexPluginMarketplaceSource,
        pluginName: String,
        takeOver: Boolean,
    ): CodexPluginMarketplaceSnapshot = extensions.installPlugin(source, pluginName, takeOver)

    fun uninstallPlugin(source: CodexPluginMarketplaceSource, pluginName: String): CodexPluginMarketplaceSnapshot =
        extensions.uninstallPlugin(source, pluginName)

    fun previewPlugin(source: CodexPluginMarketplaceSource, pluginName: String): CodexPluginPreview =
        extensions.previewPlugin(source, pluginName)

    fun previewPluginSkill(source: CodexPluginMarketplaceSource, pluginName: String, skillName: String): String =
        extensions.previewPluginSkill(source, pluginName, skillName)

    fun addSkillSource(source: SkillSource): AppConfig {
        val updated = configurations.update { config ->
            require(config.skillSources.none { it.id.equals(source.id, ignoreCase = true) }) { "Skill 来源 ID 已存在" }
            config.copy(skillSources = config.skillSources + source)
        }
        extensions.addSkillSource(source)
        return updated
    }

    fun refreshSkillSource(source: SkillSource): ExternalSkillSourceSnapshot = extensions.refreshSkillSource(source)

    fun removeSkillSource(source: SkillSource): AppConfig {
        extensions.removeSkillSource(source)
        return configurations.update { config ->
            config.copy(skillSources = config.skillSources.filterNot { it.id == source.id })
        }
    }

    fun skillOwnership(sourceId: String, skillName: String): CodexExtensionOwnership =
        extensions.skillOwnership(sourceId, skillName)

    fun previewSkill(source: SkillSource, skillName: String): String = extensions.previewSkill(source, skillName)

    fun installSkill(source: SkillSource, skillName: String, takeOver: Boolean): ExternalSkillSourceSnapshot =
        extensions.installSkill(source, skillName, takeOver)

    fun uninstallSkill(source: SkillSource, skillName: String): ExternalSkillSourceSnapshot =
        extensions.uninstallSkill(source, skillName)

    /** Deletes one safely identified local Skill and preserves a recoverable backup. */
    fun uninstallLocalSkill(directoryName: String) = extensions.uninstallLocalSkill(directoryName)
}
