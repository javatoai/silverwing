package com.snowball.awm.core

import kotlinx.serialization.Serializable
import java.nio.file.Files
import java.nio.file.Path

/** How an AI caller can move a Tag operation forward, derived from its state. */
@Serializable
enum class TagRetryKind {
    /** Merge conflict: resolve it manually, then retry. */
    RESOLVE_CONFLICT,

    /** The build stopped before the target branch or Tag changed. */
    RETRY_INTERRUPTED,

    /** The local build failed before any Tag was created. */
    RETRY_BUILD,

    /** A candidate Tag and commit are recorded, but creation or push did not finish. */
    RESUME_PARTIAL,

    /** The Tag built locally but the Genbu pipeline build failed. */
    RETAG,
    NONE,
}

/** AI-facing view of one Tag operation: the record plus actionable guidance. */
@Serializable
data class TagOperationReport(
    val operation: TagOperation,
    val retryKind: TagRetryKind,
    val guidance: String? = null,
)

/** Read-only worktree check result for conflicted or failed Tag operations. */
@Serializable
data class TagWorkspaceCheckReport(
    val clean: Boolean,
    val changes: List<String>,
)

/**
 * [taskFolder] must be the task directory name, because that is what `--task`
 * resolves; a record's own `folderName` is only a display name and may differ.
 */
fun TagOperation.toReport(taskFolder: String): TagOperationReport =
    TagOperationReport(this, tagRetryKind(this), tagOperationGuidance(this, taskFolder))

fun tagRetryKind(operation: TagOperation): TagRetryKind = when {
    operation.state == TagOperationState.CONFLICT -> TagRetryKind.RESOLVE_CONFLICT
    operation.state in interruptedRetryableTagStates -> TagRetryKind.RETRY_INTERRUPTED
    operation.state == TagOperationState.FAILED -> TagRetryKind.RETRY_BUILD
    operation.state == TagOperationState.PARTIAL -> TagRetryKind.RESUME_PARTIAL
    operation.state == TagOperationState.SUCCESS &&
        operation.genbuStatus.build == GenbuStageStatus.FAILED &&
        operation.genbuStatus.failureReason == null -> TagRetryKind.RETAG
    else -> TagRetryKind.NONE
}

internal fun tagOperationGuidance(operation: TagOperation, taskFolder: String): String? {
    val retry = "awm tag retry --task $taskFolder --operation ${operation.operationId}"
    return when (tagRetryKind(operation)) {
        TagRetryKind.RESOLVE_CONFLICT -> {
            val target = operation.targetBranch?.let { "${operation.remote}/$it" } ?: "目标分支"
            "${tagMergeConflictMessage(operation.serviceName, operation.sourceBranch, target)}\n" +
                "解决冲突后提交并推送 $target，再执行 `$retry` 重新构建"
        }
        TagRetryKind.RETRY_INTERRUPTED ->
            "上次Tag构建被中断，尚未合并目标分支或创建测试Tag；执行 `$retry` 重新构建"
        TagRetryKind.RETRY_BUILD ->
            "Tag构建失败：${operation.message.orEmpty()}；修复后执行 `$retry` 重新构建"
        TagRetryKind.RESUME_PARTIAL ->
            "测试Tag ${operation.tag.orEmpty()} 的创建或推送尚未完成；解决记录中的错误后执行 `$retry` 继续构建"
        TagRetryKind.RETAG ->
            "Genbu 构建失败；执行 `$retry` 重新打Tag，版本号将在 ${operation.tag.orEmpty()} 基础上自动 +1"
        TagRetryKind.NONE -> if (operation.genbuStatus.failureReason != null) {
            "Genbu 实时状态查询失败，暂不能确认是否需要重新打Tag；请重新查询状态后再重试"
        } else null
    }
}

/**
 * Direct CLI facade for Tag builds. Unlike the guarded agent plan/apply flow,
 * Tag operations execute immediately: the core Tag flow already enforces
 * preflight, Git write policy and repository locks, and failures are returned
 * as operation records so AI callers can read the reason and self-repair.
 */
class TagOperationCliFacade(
    private val configurations: ConfigurationRepository = AgentCompatibleConfigurationRepository(),
    private val manifests: ManifestStore = ManifestStore(),
    private val builds: TagBuildService = TagBuildService(),
    private val operations: TagOperationStore = TagOperationStore(),
    private val probes: GenbuTagProbeService = GenbuTagProbeService(operations = operations),
) {
    /** Builds one or more services of a task; per-service failures come back as FAILED records. */
    fun build(taskFolder: String, selectionKeys: List<String>, allServices: Boolean): List<TagOperationReport> {
        require(!(allServices && selectionKeys.isNotEmpty())) { "--all-services 与 --service 不能同时使用" }
        val (config, taskDirectory, manifest) = resolveTask(taskFolder)
        TagPolicy.requireEnabled(config)
        val group = config.groups.firstOrNull { it.id == manifest.groupId }
            ?: throw IllegalStateException("配置中找不到任务所属组：${manifest.groupId}")
        // Same wording as TagPolicy so the group gate reads identically no
        // matter which entry point reports it.
        require(group.tagEnabled) { "组 ${group.name} 已关闭测试Tag" }
        val keys = when {
            allServices ->
                manifest.services.filter { it.tagEnabled }.map { it.selectionKey }
                    .also { require(it.isNotEmpty()) { "任务内没有可构建测试Tag的服务" } }
            selectionKeys.isNotEmpty() -> selectionKeys.map { canonicalSelection(manifest, it) }.distinct()
            else -> throw IllegalArgumentException("请指定 --service 或 --all-services")
        }
        return builds.buildBatch(config, taskDirectory, keys).map { it.toReport(manifest.taskDirectoryName) }
    }

    /**
     * Normalizes one `--service` value to the module's `selectionKey` so repeated
     * or differently spelled keys (selection key vs repository id) collapse into
     * a single build instead of Tagging the same module twice. The module half of
     * the Tag gate is checked here too: without it a disabled module would only
     * surface as a FAILED record whose `RETRY_BUILD` guidance can never succeed,
     * unlike the `--all-services` hard failure. Unknown or ambiguous keys are
     * passed through so [TagPolicy] reports them identically for both paths.
     */
    private fun canonicalSelection(manifest: TaskManifest, selectionKey: String): String {
        val service = manifest.services.singleOrNull {
            it.selectionKey == selectionKey || it.repositoryId == selectionKey
        } ?: return selectionKey
        require(service.tagEnabled) { "模块 ${service.moduleName} 已关闭测试Tag" }
        return service.selectionKey
    }

    /** Returns one record, refreshing its Genbu stages live when probing is configured. */
    fun status(taskFolder: String, operationId: String): TagOperationReport {
        val (config, _, manifest) = resolveTask(taskFolder)
        TagPolicy.requireEnabled(config)
        return probed(config, manifest, operationId).toReport(manifest.taskDirectoryName)
    }

    /** Lists every Tag record of the task, newest first. */
    fun history(taskFolder: String): List<TagOperationReport> {
        val (config, taskDirectory, manifest) = resolveTask(taskFolder)
        TagPolicy.requireEnabled(config)
        return operations.list(taskDirectory)
            .sortedWith(compareByDescending<TagOperation> { it.updatedAt }.thenByDescending { it.operationId })
            .map { it.toReport(manifest.taskDirectoryName) }
    }

    /**
     * Retries a record on the path matching its state; the same record is
     * updated in place. Genbu is refreshed first so the chosen path reflects the
     * live pipeline result rather than a stale stored one.
     */
    fun retry(taskFolder: String, operationId: String): TagOperationReport {
        val (config, taskDirectory, manifest) = resolveTask(taskFolder)
        TagPolicy.requireEnabled(config)
        // A stale failed build would otherwise re-Tag a pipeline that has since
        // succeeded, pushing a needless Tag.
        val operation = probed(config, manifest, operationId)
        val updated = when (tagRetryKind(operation)) {
            TagRetryKind.RESOLVE_CONFLICT -> builds.resumeConflict(config, taskDirectory, operationId)
            TagRetryKind.RETRY_INTERRUPTED -> builds.resumeInterrupted(config, taskDirectory, operationId)
            TagRetryKind.RETRY_BUILD -> builds.resumeFailed(config, taskDirectory, operationId)
            TagRetryKind.RESUME_PARTIAL -> builds.resumePartial(config, taskDirectory, operationId)
            TagRetryKind.RETAG -> builds.retag(config, taskDirectory, operationId)
            TagRetryKind.NONE -> throw IllegalArgumentException(
                tagOperationGuidance(operation, manifest.taskDirectoryName)
                    ?: "当前状态（${operation.state.userFacingLabel()}）的测试Tag无需重试",
            )
        }
        return updated.toReport(manifest.taskDirectoryName)
    }

    /** Read-only inspection of the feature worktree behind a conflicted or failed record. */
    fun workspaceCheck(taskFolder: String, operationId: String): TagWorkspaceCheckReport {
        val (config, taskDirectory, _) = resolveTask(taskFolder)
        TagPolicy.requireEnabled(config)
        val operation = loadOperation(taskDirectory, operationId)
        require(
            operation.state == TagOperationState.CONFLICT || operation.state == TagOperationState.FAILED,
        ) { "只有冲突或失败的测试Tag需要检测工作区（当前：${operation.state.userFacingLabel()}）" }
        val check = builds.inspectFeatureWorkspace(
            config,
            taskDirectory,
            "${operation.groupServiceId}:${operation.moduleId}",
        )
        return TagWorkspaceCheckReport(clean = check.clean, changes = check.changes)
    }

    private fun loadOperation(taskDirectory: Path, operationId: String): TagOperation =
        runCatching { operations.load(taskDirectory, operationId) }
            .getOrElse { throw IllegalArgumentException("找不到Tag构建记录：$operationId") }

    private fun probed(config: AppConfig, manifest: TaskManifest, operationId: String): TagOperation =
        probes.probeOperation(config, manifest, operationId)
            ?: throw IllegalArgumentException("找不到Tag构建记录：$operationId")

    private data class ResolvedTask(
        val config: AppConfig,
        val taskDirectory: Path,
        val manifest: TaskManifest,
    )

    private fun resolveTask(taskFolder: String): ResolvedTask {
        val folder = taskFolder.trim()
        require(folder.isNotEmpty()) { "任务文件夹名不能为空" }
        val config = configurations.load()
        val taskRoot = config.taskRoot?.takeIf(String::isNotBlank)?.let { Path.of(it).toAbsolutePath().normalize() }
            ?: throw IllegalStateException("尚未配置任务根目录")
        val taskDirectory = taskRoot.resolve(folder).normalize()
        require(taskDirectory.parent == taskRoot) { "任务目录必须是任务根目录的直接子目录" }
        require(Files.isRegularFile(taskDirectory.resolve(ManifestStore.FILE_NAME))) { "找不到研发任务：$folder" }
        val manifest = manifests.load(taskDirectory)
        // Genbu probing re-derives the task path from the manifest, so a renamed
        // directory must fail here instead of silently splitting the two paths.
        require(taskDirectory.fileName.toString() == manifest.taskDirectoryName) {
            "任务目录与任务清单名称不一致：$folder（清单记录：${manifest.taskDirectoryName}）"
        }
        return ResolvedTask(config, taskDirectory, manifest)
    }
}
