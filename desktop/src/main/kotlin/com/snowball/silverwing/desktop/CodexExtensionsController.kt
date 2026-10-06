package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.CodexExtensionOwnership
import com.snowball.silverwing.core.CodexExtensionsApplicationService
import com.snowball.silverwing.core.CodexExtensionsSnapshot
import com.snowball.silverwing.core.CodexPluginCatalogItem
import com.snowball.silverwing.core.CodexPluginPreview
import com.snowball.silverwing.core.CodexPluginMarketplaceSource
import com.snowball.silverwing.core.ExternalSkillCatalogItem
import com.snowball.silverwing.core.SkillSource
import java.nio.file.Path
import kotlinx.coroutines.Job
import kotlinx.coroutines.CancellationException

internal sealed interface ExtensionSourceSaveState {
    data object Idle : ExtensionSourceSaveState
    data object Saving : ExtensionSourceSaveState
    data object Saved : ExtensionSourceSaveState
    data class Failed(val message: String) : ExtensionSourceSaveState
}

internal data class CodexExtensionsUiState(
    val snapshot: CodexExtensionsSnapshot,
)

/** 统一表示来源编辑器的远程分支查询状态。 */
internal sealed interface RemoteBranchLoadState {
    data object Idle : RemoteBranchLoadState
    data object Loading : RemoteBranchLoadState
    data class Loaded(val branches: List<String>) : RemoteBranchLoadState
    data class Failed(val message: String) : RemoteBranchLoadState
}

/** 右侧 Skill 文档预览的异步读取状态。 */
internal sealed interface SkillPreviewState {
    data object Empty : SkillPreviewState
    data object Loading : SkillPreviewState
    data class Loaded(val content: String) : SkillPreviewState
    data class Failed(val message: String) : SkillPreviewState
}

/** 右侧插件概览与内置 Skill 文档预览的异步读取状态。 */
internal sealed interface PluginPreviewState {
    data object Empty : PluginPreviewState
    data object Loading : PluginPreviewState
    data class Loaded(val content: CodexPluginPreview) : PluginPreviewState
    data class Failed(val message: String) : PluginPreviewState
}

/** Keeps presentation state and operation callbacks for Codex extensions. */
internal class CodexExtensionsController(
    private val extensions: CodexExtensionsApplicationService,
    private val operations: OperationRunner,
    private val applyConfig: (AppConfig) -> Unit,
) {
    private enum class ReadKind { BRANCHES, PLUGIN, PLUGIN_SKILL, SKILL }
    private var readSerial = 0L
    private val readRequests = mutableMapOf<ReadKind, Long>()
    private val readJobs = mutableMapOf<ReadKind, Job>()
    var state by mutableStateOf(CodexExtensionsUiState(cachedSnapshot()))
        private set

    fun refreshCached() {
        state = CodexExtensionsUiState(cachedSnapshot())
    }

    fun loadRemoteBranches(repositoryUrl: String, onResult: (RemoteBranchLoadState) -> Unit): Boolean {
        return read(ReadKind.BRANCHES,
            onLoading = { onResult(RemoteBranchLoadState.Loading) },
            block = { extensions.loadRemoteBranches(repositoryUrl) },
            onSuccess = { onResult(RemoteBranchLoadState.Loaded(it)) },
            onFailure = { error -> onResult(RemoteBranchLoadState.Failed(error.message ?: "远程分支读取失败")) },
            onCancelled = { onResult(RemoteBranchLoadState.Failed("远程分支读取已取消，可重新读取")) },
        )
    }

    fun addMarketplace(source: CodexPluginMarketplaceSource, onResult: (ExtensionSourceSaveState) -> Unit = {}): Boolean {
        onResult(ExtensionSourceSaveState.Saving)
        return operations.run(
        activeMessage = "正在添加 Codex 插件来源…",
        successMessage = "插件来源已保存",
        cancellable = true,
        block = { extensions.addMarketplace(source) },
        onSuccess = { config ->
            applyConfig(config)
            refreshCached()
            onResult(ExtensionSourceSaveState.Saved)
        },
        onFailure = { error -> sourceSaveEnded(onResult, error) { config -> config.codexPluginMarketplaceSources.any { it.id == source.id } } },
        onCancelled = { sourceSaveEnded(onResult, null) { config -> config.codexPluginMarketplaceSources.any { it.id == source.id } } },
    )
    }

    fun refreshMarketplace(source: CodexPluginMarketplaceSource): Boolean = operations.run(
        activeMessage = "正在刷新 Codex 插件来源…",
        successMessage = "插件来源已刷新",
        cancellable = true,
        block = { extensions.refreshMarketplace(source) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    fun removeMarketplace(source: CodexPluginMarketplaceSource): Boolean = operations.run(
        activeMessage = "正在移除 Codex 插件来源…",
        successMessage = "插件来源已移除；已安装插件保持不变",
        cancellable = true,
        block = { extensions.removeMarketplace(source) },
        onSuccess = {
            applyConfig(it)
            refreshCached()
        },
        onFailure = ::refreshAfterFailure,
        onCancelled = { applyConfig(extensions.configurationSnapshot()); refreshCached() },
    )

    fun pluginOwnership(sourceId: String, plugin: CodexPluginCatalogItem): CodexExtensionOwnership =
        extensions.pluginOwnership(sourceId, plugin)

    fun installPlugin(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem, takeOver: Boolean): Boolean = operations.run(
        activeMessage = "正在安装 ${plugin.name}…",
        successMessage = if (takeOver && plugin.installed) "插件已接管" else "插件已安装；请新建或重启 Codex 会话后使用",
        cancellable = true,
        block = { extensions.installPlugin(source, plugin.name, takeOver) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    fun uninstallPlugin(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem): Boolean = operations.run(
        activeMessage = "正在卸载 ${plugin.name}…",
        successMessage = "插件已卸载",
        cancellable = true,
        block = { extensions.uninstallPlugin(source, plugin.name) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    fun previewPlugin(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem, onResult: (PluginPreviewState) -> Unit): Boolean {
        return read(ReadKind.PLUGIN,
            onLoading = { onResult(PluginPreviewState.Loading) },
            block = { extensions.previewPlugin(source, plugin.name) },
            onSuccess = { onResult(PluginPreviewState.Loaded(it)) },
            onFailure = { error -> onResult(PluginPreviewState.Failed(error.message ?: "插件概览读取失败")) },
            onCancelled = { onResult(PluginPreviewState.Failed("插件概览读取已取消，可重试")) },
        )
    }

    fun previewPluginSkill(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem, skillName: String, onResult: (SkillPreviewState) -> Unit): Boolean {
        return read(ReadKind.PLUGIN_SKILL,
            onLoading = { onResult(SkillPreviewState.Loading) },
            block = { extensions.previewPluginSkill(source, plugin.name, skillName) },
            onSuccess = { onResult(SkillPreviewState.Loaded(it)) },
            onFailure = { error -> onResult(SkillPreviewState.Failed(error.message ?: "内置 Skill 文档读取失败")) },
            onCancelled = { onResult(SkillPreviewState.Failed("内置 Skill 文档读取已取消，可重试")) },
        )
    }

    fun previewPluginSkillPath(source: CodexPluginMarketplaceSource, plugin: CodexPluginCatalogItem, skillName: String): Path =
        extensions.previewPluginSkillPath(source, plugin.name, skillName)

    fun addSkillSource(source: SkillSource, onResult: (ExtensionSourceSaveState) -> Unit = {}): Boolean {
        onResult(ExtensionSourceSaveState.Saving)
        return operations.run(
        activeMessage = "正在添加 Skill 来源…",
        successMessage = "Skill 来源已保存",
        cancellable = true,
        block = { extensions.addSkillSource(source) },
        onSuccess = { config ->
            applyConfig(config)
            refreshCached()
            onResult(ExtensionSourceSaveState.Saved)
        },
        onFailure = { error -> sourceSaveEnded(onResult, error) { config -> config.skillSources.any { it.id == source.id } } },
        onCancelled = { sourceSaveEnded(onResult, null) { config -> config.skillSources.any { it.id == source.id } } },
    )
    }

    fun refreshSkillSource(source: SkillSource): Boolean = operations.run(
        activeMessage = "正在刷新 Skill 来源…",
        successMessage = "Skill 来源已刷新",
        cancellable = true,
        block = { extensions.refreshSkillSource(source) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    fun removeSkillSource(source: SkillSource): Boolean = operations.run(
        activeMessage = "正在移除 Skill 来源…",
        successMessage = "Skill 来源已移除；已安装 Skill 保持不变",
        cancellable = true,
        block = { extensions.removeSkillSource(source) },
        onSuccess = {
            applyConfig(it)
            refreshCached()
        },
        onFailure = ::refreshAfterFailure,
        onCancelled = { applyConfig(extensions.configurationSnapshot()); refreshCached() },
    )

    fun skillOwnership(sourceId: String, skill: ExternalSkillCatalogItem): CodexExtensionOwnership =
        extensions.skillOwnership(sourceId, skill.name)

    fun previewSkill(source: SkillSource, skill: ExternalSkillCatalogItem, onResult: (SkillPreviewState) -> Unit): Boolean {
        return read(ReadKind.SKILL,
            onLoading = { onResult(SkillPreviewState.Loading) },
            block = { extensions.previewSkill(source, skill.name) },
            onSuccess = { onResult(SkillPreviewState.Loaded(it)) },
            onFailure = { error -> onResult(SkillPreviewState.Failed(error.message ?: "Skill 文档读取失败")) },
            onCancelled = { onResult(SkillPreviewState.Failed("Skill 文档读取已取消，可重试")) },
        )
    }

    fun previewSkillPath(source: SkillSource, skill: ExternalSkillCatalogItem): Path =
        extensions.previewSkillPath(source, skill.name)

    fun installSkill(source: SkillSource, skill: ExternalSkillCatalogItem, takeOver: Boolean): Boolean = operations.run(
        activeMessage = "正在安装 ${skill.name}…",
        successMessage = "Skill 已安装；请新建或重启 Codex 会话后使用",
        cancellable = true,
        block = { extensions.installSkill(source, skill.name, takeOver) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    fun updateSkill(source: SkillSource, skill: ExternalSkillCatalogItem): Boolean = operations.run(
        activeMessage = "正在更新 ${skill.name}…",
        successMessage = "Skill 已更新；请新建或重启 Codex 会话后使用",
        cancellable = true,
        block = { extensions.installSkill(source, skill.name, takeOver = false) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    fun uninstallSkill(source: SkillSource, skill: ExternalSkillCatalogItem): Boolean = operations.run(
        activeMessage = "正在卸载 ${skill.name}…",
        successMessage = "Skill 已卸载；恢复备份保留在 SilverWing 本机目录",
        cancellable = true,
        block = { extensions.uninstallSkill(source, skill.name) },
        onSuccess = { refreshCached() },
        onFailure = ::refreshAfterFailure,
        onCancelled = ::refreshCached,
    )

    private fun cachedSnapshot(): CodexExtensionsSnapshot = extensions.snapshot()

    fun cancelRemoteBranches() = cancelRead(ReadKind.BRANCHES)
    fun cancelPluginPreview() = cancelRead(ReadKind.PLUGIN)
    fun cancelPluginSkillPreview() = cancelRead(ReadKind.PLUGIN_SKILL)
    fun cancelSkillPreview() = cancelRead(ReadKind.SKILL)
    fun cancelSourceSave(): Boolean = operations.cancel()

    private fun cancelRead(kind: ReadKind) {
        readRequests[kind] = ++readSerial
        readJobs.remove(kind)?.cancel()
    }

    private fun <T> read(kind: ReadKind, onLoading: () -> Unit, block: () -> T,
        onSuccess: (T) -> Unit, onFailure: (Throwable) -> Unit, onCancelled: () -> Unit): Boolean {
        cancelRead(kind)
        val request = readRequests.getValue(kind)
        onLoading()
        var delivered = false
        val job = operations.read(block,
            onSuccess = { delivered = true; if (readRequests[kind] == request) onSuccess(it) },
            onFailure = { delivered = true; if (readRequests[kind] == request) onFailure(it) },
            onCancelled = { delivered = true; if (readRequests[kind] == request) onCancelled() })
        readJobs[kind] = job
        job.invokeOnCompletion { cause ->
            if (!delivered && cause is CancellationException && readRequests[kind] == request) { delivered = true; onCancelled() }
            if (readJobs[kind] === job) readJobs.remove(kind)
        }
        val started = job.start()
        return started
    }

    private fun refreshAfterFailure(error: Throwable) {
        // Rejection runs synchronously while the accepted operation may hold the service lock.
        if (error !is OperationBusyException) refreshCached()
    }

    private fun sourceSaveEnded(onResult: (ExtensionSourceSaveState) -> Unit, error: Throwable?, contains: (AppConfig) -> Boolean) {
        if (error is OperationBusyException) {
            onResult(ExtensionSourceSaveState.Failed(error.message.orEmpty()))
            return
        }
        val config = try {
            extensions.configurationSnapshot().also { applyConfig(it); refreshCached() }
        } catch (recoveryError: Throwable) {
            onResult(ExtensionSourceSaveState.Failed("无法确认来源保存状态：${recoveryError.message.orEmpty().ifBlank { "读取配置失败" }}；输入已保留"))
            throw recoveryError
        }
        if (contains(config)) onResult(ExtensionSourceSaveState.Saved)
        else onResult(ExtensionSourceSaveState.Failed(error?.message.orEmpty().ifBlank {
            if (error == null) "保存已取消，输入已保留" else "来源保存失败，输入已保留"
        }))
    }
}
