package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import java.nio.file.Files
import java.nio.file.Path
import java.security.MessageDigest
import java.nio.charset.StandardCharsets
import java.util.Locale
import java.time.Clock
import java.time.Instant
import java.util.UUID
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

internal val interruptedRetryableTagStates = setOf(
    TagOperationState.CREATED,
    TagOperationState.PREFLIGHT_PASSED,
    TagOperationState.SOURCE_BRANCH_PUSHED,
)

@Serializable
enum class MergeMode {
    ALREADY_MERGED,
    FAST_FORWARD,
    MERGE_COMMIT
}

@Serializable
data class FeatureSyncStatus(
    val remoteExists: Boolean,
    val ahead: Int,
    val behind: Int,
) {
    val canPush: Boolean get() = behind == 0
    val pushRequired: Boolean get() = !remoteExists || ahead > 0
}

@Serializable
data class TagPreflight(
    val folderName: String,
    val serviceName: String,
    val sourceBranch: String,
    val sourceSha: String,
    val remote: String,
    val targetBranch: String?,
    val targetSha: String?,
    val sourceSync: FeatureSyncStatus,
    val tagMode: TagBuildMode,
    val mergeMode: MergeMode?,
    val commitList: List<String>,
    val diffStat: String,
    val estimatedTag: String,
)

/** Read-only status of the feature worktree used before retrying a conflicted Tag. */
data class TagWorkspaceCheck(
    val changes: List<String>,
) {
    val clean: Boolean get() = changes.isEmpty()
}

internal fun tagWorkspaceChanges(statusOutput: String): List<String> = statusOutput.lineSequence()
    .filter(String::isNotBlank)
    .map { line ->
        val code = line.take(2)
        val path = line.drop(3).trim().ifBlank { "未命名路径" }
        val kind = when {
            code == "??" -> "未跟踪"
            code.contains('U') || code in setOf("AA", "DD") -> "未解决冲突"
            code[0] != ' ' && code[1] != ' ' -> "已暂存且有未暂存修改"
            code[0] != ' ' -> "已暂存"
            code[1] != ' ' -> "未暂存"
            else -> "状态变更"
        }
        "$kind：$path"
    }
    .toList()

fun tagMergeConflictMessage(serviceName: String, sourceBranch: String, target: String): String =
    "我在将服务 $serviceName 的 $sourceBranch 分支合并到 $target 时遇到了冲突，请你解决。"

class TagBuildService(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val git: GitClient = GitClient(),
    private val manifests: ManifestStore = ManifestStore(),
    private val operations: TagOperationStore = TagOperationStore(),
    private val clock: Clock = Clock.systemUTC(),
    private val events: EventSink = JsonlEventSink(paths, clock),
    private val workspaceLifecycle: WorkspaceLifecycle = GitWorkspaceLifecycle(git),
    private val taskLock: TaskOperationLock = FileTaskOperationLock(paths),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(paths),
) {
    fun buildBatch(
        config: AppConfig,
        taskDirectory: Path,
        repositoryIds: List<String>,
    ): List<TagOperation> {
        if (repositoryIds.isEmpty()) return emptyList()
        TagPolicy.requireEnabled(config)
        val batchId = UUID.randomUUID().toString()
        // Task lifecycle uses an exclusive lock. Batch operations intentionally
        // serialize within one task so archive/delete cannot interleave and a
        // non-blocking file lock does not turn sibling entries into false failures.
        return taskLock.withLock(taskDirectory) {
            repositoryIds.map { repositoryId ->
                buildSafelyUnlocked(config, taskDirectory, repositoryId, batchId)
            }
        }
    }

    private fun buildSafelyUnlocked(
        config: AppConfig,
        taskDirectory: Path,
        repositoryId: String,
        batchId: String? = null,
    ): TagOperation = runCatching {
        buildUnlocked(config, taskDirectory, repositoryId, batchId)
    }.getOrElse { error ->
        val manifest = manifests.load(taskDirectory)
        val workspace = resolveWorkspace(manifest, repositoryId)
            ?: throw error
        val target = runCatching { TagPolicy.resolve(config, manifest, repositoryId) }.getOrNull()
        val now = SilverWingTime.format(Instant.now(clock))
        TagOperation(
            operationId = UUID.randomUUID().toString(),
            folderName = manifest.folderName,
            serviceName = workspace.serviceName,
            repositoryId = workspace.repositoryId,
            sourceBranch = workspace.branch,
            targetBranch = target?.targetBranch,
            remote = target?.remote.orEmpty(),
            tagMode = target?.mode ?: TagBuildMode.MERGE_TO_TARGET_BRANCH,
            state = TagOperationState.FAILED,
            createdAt = now,
            updatedAt = now,
            message = error.message ?: error::class.simpleName ?: "构建失败",
            groupServiceId = workspace.groupServiceId,
            moduleId = workspace.moduleId,
            batchId = batchId,
        ).also { operations.save(taskDirectory, it) }
    }

    fun preflight(
        config: AppConfig,
        taskDirectory: Path,
        repositoryId: String,
    ): TagPreflight = taskLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val target = TagPolicy.resolve(config, manifest, repositoryId)
        val workspace = target.workspace
        val service = target
        val validated = workspaceLifecycle.validateForMutation(config, taskDirectory, manifest, workspace)
        val repository = validated.repository
        val worktree = validated.worktree

        withRepositoryLock(repository) {
            preflightUnlocked(service, manifest, workspace, repository, worktree).also { preview ->
                requireTagBranchesAllowed(config, service, preview)
            }
        }
    }

    /**
     * Inspects only the recorded feature worktree. It does not fetch, merge, write Git state,
     * or alter the persisted Tag operation.
     */
    fun inspectFeatureWorkspace(
        config: AppConfig,
        taskDirectory: Path,
        repositoryId: String,
    ): TagWorkspaceCheck = taskLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val target = TagPolicy.resolve(config, manifest, repositoryId)
        val validated = workspaceLifecycle.validateForMutation(config, taskDirectory, manifest, target.workspace)
        val output = git.readOnly(
            validated.worktree,
            "status",
            "--porcelain=v1",
            "--untracked-files=all",
        ).stdout
        TagWorkspaceCheck(tagWorkspaceChanges(output))
    }

    fun build(
        config: AppConfig,
        taskDirectory: Path,
        repositoryId: String,
    ): TagOperation = taskLock.withLock(taskDirectory) {
        TagPolicy.requireEnabled(config)
        buildUnlocked(config, taskDirectory, repositoryId)
    }

    /**
     * Re-runs the complete Tag flow for an operation that stopped at a merge
     * conflict. The persisted operation is intentionally reused so the Tag
     * history keeps the original batch/card entry rather than adding a second
     * record.
     */
    fun resumeConflict(
        config: AppConfig,
        taskDirectory: Path,
        operationId: String,
    ): TagOperation {
        TagPolicy.requireEnabled(config)
        return resumeExisting(config, taskDirectory, operationId) { previous ->
            require(previous.state == TagOperationState.CONFLICT) {
                "只有${TagOperationState.CONFLICT.userFacingLabel()}的测试Tag操作可以重试"
            }
        }
    }

    /**
     * Re-runs an operation interrupted before it changed the target branch or
     * created a local Tag. Re-pushing the source branch is idempotent, and the
     * complete preflight runs again before every following Git write.
     */
    fun resumeInterrupted(
        config: AppConfig,
        taskDirectory: Path,
        operationId: String,
    ): TagOperation {
        TagPolicy.requireEnabled(config)
        return resumeExisting(config, taskDirectory, operationId) { previous ->
            require(previous.state in interruptedRetryableTagStates) { "只有构建中断的测试Tag操作可以重试" }
        }
    }

    /**
     * Re-runs a locally failed build on the same record. No Tag was created for
     * a failed operation, so the retry simply recomputes the next version.
     */
    fun resumeFailed(
        config: AppConfig,
        taskDirectory: Path,
        operationId: String,
    ): TagOperation {
        TagPolicy.requireEnabled(config)
        return resumeExisting(config, taskDirectory, operationId) { previous ->
            require(previous.state == TagOperationState.FAILED) { "只有失败的测试Tag操作可以重试" }
        }
    }

    /**
     * Rebuilds a Tag whose Genbu pipeline build failed. The full Tag flow runs
     * again on the same record and the version auto-increments past the failed
     * Tag, so the history row is replaced instead of duplicated.
     */
    fun retag(
        config: AppConfig,
        taskDirectory: Path,
        operationId: String,
    ): TagOperation {
        TagPolicy.requireEnabled(config)
        return resumeExisting(config, taskDirectory, operationId) { previous ->
            require(
                previous.state == TagOperationState.SUCCESS &&
                    previous.genbuStatus.build == GenbuStageStatus.FAILED,
            ) {
                "只有 Genbu 构建失败的测试Tag可以重新打Tag"
            }
            require(previous.genbuStatus.failureReason == null) {
                "Genbu 实时状态查询失败，请刷新状态后再判断是否重新打Tag"
            }
        }
    }

    private fun resumeExisting(
        config: AppConfig,
        taskDirectory: Path,
        operationId: String,
        validate: (TagOperation) -> Unit,
    ): TagOperation = taskLock.withLock(taskDirectory) {
        val previous = operations.load(taskDirectory, operationId)
        validate(previous)
        runCatching {
            buildUnlocked(
                config = config,
                taskDirectory = taskDirectory,
                repositoryId = "${previous.groupServiceId}:${previous.moduleId}",
                existingOperation = previous,
            )
        }.getOrElse { error ->
            // Resolution/validation happens before the normal build try/catch.
            // Keep the same operation record even when the task or workspace is
            // no longer available, so the UI can report a retryable failure.
            previous.copy(
                state = TagOperationState.FAILED,
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                sourceSha = null,
                targetSha = null,
                tag = null,
                message = error.message ?: error::class.simpleName ?: "重试失败",
                conflictFiles = emptyList(),
                genbuStatus = GenbuTagProbeStatus(),
            ).also {
                operations.save(taskDirectory, it)
                recordHistory(taskDirectory, it)
            }
        }
    }

    private fun buildUnlocked(
        config: AppConfig,
        taskDirectory: Path,
        repositoryId: String,
        batchId: String? = null,
        existingOperation: TagOperation? = null,
    ): TagOperation {
        val manifest = manifests.load(taskDirectory)
        val target = existingOperation?.let { TagPolicy.resolveHistorical(config, manifest, it) }
            ?: TagPolicy.resolve(config, manifest, repositoryId)
        val workspace = target.workspace
        val service = target
        val validated = workspaceLifecycle.validateForMutation(config, taskDirectory, manifest, workspace)
        val repository = validated.repository
        val now = SilverWingTime.format(Instant.now(clock))
        var operation = existingOperation?.copy(
            folderName = manifest.folderName,
            serviceName = workspace.serviceName,
            repositoryId = workspace.repositoryId,
            sourceBranch = workspace.branch,
            targetBranch = service.targetBranch,
            remote = service.remote,
            tagMode = service.mode,
            state = TagOperationState.CREATED,
            updatedAt = now,
            sourceSha = null,
            targetSha = null,
            tag = null,
            message = null,
            conflictFiles = emptyList(),
            groupServiceId = workspace.groupServiceId,
            moduleId = workspace.moduleId,
            // A resumed/re-Tag flow produces a new Tag, so the previous Genbu
            // result no longer applies and the probe starts over.
            genbuStatus = GenbuTagProbeStatus(),
        ) ?: TagOperation(
            operationId = UUID.randomUUID().toString(),
            folderName = manifest.folderName,
            serviceName = workspace.serviceName,
            repositoryId = workspace.repositoryId,
            sourceBranch = workspace.branch,
            targetBranch = service.targetBranch,
            remote = service.remote,
            tagMode = service.mode,
            state = TagOperationState.CREATED,
            createdAt = now,
            updatedAt = now,
            groupServiceId = workspace.groupServiceId,
            moduleId = workspace.moduleId,
            batchId = batchId,
        )
        operations.save(taskDirectory, operation)
        events.info(
            event = "tag.build.started",
            message = "开始Tag构建",
            metadata = mapOf(
                "operationId" to operation.operationId,
                "folderName" to operation.folderName,
                "service" to operation.serviceName,
                "batchId" to operation.batchId.orEmpty(),
            ),
            clock = clock,
        )

        return withRepositoryLock(repository) {
            try {
                val preview = preflightUnlocked(service, manifest, workspace, validated.repository, validated.worktree)
                requireTagBranchesAllowed(config, service, preview)
                operation = transition(
                    taskDirectory,
                    operation,
                    TagOperationState.PREFLIGHT_PASSED,
                    sourceSha = preview.sourceSha,
                    targetSha = preview.targetSha,
                )
                if (preview.sourceSync.pushRequired || service.mode == TagBuildMode.CURRENT_BRANCH) {
                    git.run(
                        repository,
                        "push",
                        "--set-upstream",
                        service.remote,
                        "${workspace.branch}:refs/heads/${workspace.branch}",
                    )
                }
                operation = transition(taskDirectory, operation, TagOperationState.SOURCE_BRANCH_PUSHED)

                val tagCommit = if (service.mode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
                    val mergeResult = mergeAndPushTargetBranch(
                        repository = repository,
                        sourceSha = preview.sourceSha,
                        service = service,
                        serviceName = workspace.serviceName,
                    )
                    if (mergeResult.conflicts.isNotEmpty()) {
                        operation = transition(
                            taskDirectory,
                            operation,
                            TagOperationState.CONFLICT,
                            message = tagMergeConflictMessage(
                                workspace.serviceName,
                                workspace.branch,
                                "${service.remote}/${requireNotNull(service.targetBranch)}",
                            ),
                            conflictFiles = mergeResult.conflicts,
                        )
                        recordHistory(taskDirectory, operation)
                        return@withRepositoryLock operation
                    }
                    operation = transition(
                        taskDirectory,
                        operation,
                        TagOperationState.TARGET_BRANCH_PUSHED,
                        targetSha = mergeResult.targetSha,
                    )
                    mergeResult.targetSha
                } else {
                    preview.sourceSha
                }

                val tag = createAndPushTag(
                    repository = repository,
                    remote = service.remote,
                    initialTag = nextTag(repository, tagCommit),
                    commit = tagCommit,
                    message = auditMessage(manifest, workspace, service, preview.sourceSha, tagCommit),
                    onCandidate = { candidate ->
                        operation = transition(taskDirectory, operation, operation.state, tag = candidate)
                    },
                    onCreated = { candidate ->
                        operation = transition(taskDirectory, operation, TagOperationState.LOCAL_TAG_CREATED, tag = candidate)
                    },
                )
                operation = transition(taskDirectory, operation, TagOperationState.TAG_PUSHED)
                operation = transition(
                    taskDirectory,
                    operation,
                    TagOperationState.SUCCESS,
                    message = "${workspace.serviceName}：$tag",
                )
                recordHistory(taskDirectory, operation)
                operation
            } catch (conflict: MergeConflictException) {
                operation = transition(
                    taskDirectory,
                    operation,
                    TagOperationState.CONFLICT,
                    message = tagMergeConflictMessage(
                        workspace.serviceName,
                        workspace.branch,
                        "${service.remote}/${requireNotNull(service.targetBranch)}",
                    ),
                    conflictFiles = conflict.files,
                )
                recordHistory(taskDirectory, operation)
                operation
            } catch (error: Throwable) {
                val partial = operation.tag != null && operation.state in setOf(
                    TagOperationState.SOURCE_BRANCH_PUSHED,
                    TagOperationState.TARGET_BRANCH_PUSHED,
                    TagOperationState.LOCAL_TAG_CREATED,
                )
                operation = transition(
                    taskDirectory,
                    operation,
                    if (partial) TagOperationState.PARTIAL else TagOperationState.FAILED,
                    message = error.message ?: "构建失败",
                )
                recordHistory(taskDirectory, operation)
                operation
            }
        }
    }

    fun resumePartial(
        config: AppConfig,
        taskDirectory: Path,
        operationId: String,
    ): TagOperation = taskLock.withLock(taskDirectory) {
        TagPolicy.requireEnabled(config)
        var operation = operations.load(taskDirectory, operationId)
        require(operation.state == TagOperationState.PARTIAL) {
            "只有${TagOperationState.PARTIAL.userFacingLabel()}的测试Tag操作可以恢复"
        }
        val manifest = manifests.load(taskDirectory)
        val target = TagPolicy.resolveHistorical(config, manifest, operation)
        val service = target
        val workspace = target.workspace
        val validated = workspaceLifecycle.validateForMutation(config, taskDirectory, manifest, workspace)
        val repository = validated.repository
        val tag = operation.tag ?: throw IllegalStateException("操作没有可恢复的本地测试Tag")
        val tagCommit = operation.targetSha ?: operation.sourceSha
            ?: throw IllegalStateException("操作没有可恢复的测试Tag提交")

        withRepositoryLock(repository) {
            try {
                val pushedTag = createAndPushTag(
                    repository = repository,
                    remote = service.remote,
                    initialTag = tag,
                    commit = tagCommit,
                    message = auditMessage(manifest, workspace, service, operation.sourceSha.orEmpty(), tagCommit),
                    onCandidate = { candidate ->
                        operation = transition(taskDirectory, operation, operation.state, tag = candidate)
                    },
                    onCreated = { candidate ->
                        operation = transition(taskDirectory, operation, TagOperationState.LOCAL_TAG_CREATED, tag = candidate)
                    },
                )
                operation = transition(taskDirectory, operation, TagOperationState.TAG_PUSHED)
                operation = transition(
                    taskDirectory,
                    operation,
                    TagOperationState.SUCCESS,
                    message = "${workspace.serviceName}：$pushedTag",
                )
                recordHistory(taskDirectory, operation)
                operation
            } catch (error: Throwable) {
                operation = transition(
                    taskDirectory,
                    operation,
                    TagOperationState.PARTIAL,
                    message = error.message ?: "恢复失败",
                )
                recordHistory(taskDirectory, operation)
                operation
            }
        }
    }

    private fun preflightUnlocked(
        service: EffectiveTagTarget,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
        repository: Path,
        worktree: Path,
    ): TagPreflight {
        ensureCleanFeatureWorktree(worktree)
        git.fetch(repository, service.remote)
        git.fetchTags(repository, service.remote)
        val sourceSha = git.resolve(repository, workspace.branch)
        val remoteSourceRef = "${service.remote}/${workspace.branch}"
        val sync = featureSync(repository, sourceSha, remoteSourceRef)
        require(sync.canPush) {
            "远端当前分支领先本地 ${sync.behind} 个提交，请先手工同步；工具不会自动 pull/rebase"
        }
        if (service.mode == TagBuildMode.CURRENT_BRANCH) {
            val range = if (sync.remoteExists) "$remoteSourceRef..$sourceSha" else sourceSha
            return TagPreflight(
                folderName = manifest.folderName,
                serviceName = workspace.serviceName,
                sourceBranch = workspace.branch,
                sourceSha = sourceSha,
                remote = service.remote,
                targetBranch = null,
                targetSha = null,
                sourceSync = sync,
                tagMode = service.mode,
                mergeMode = null,
                commitList = git.run(
                    repository,
                    "log",
                    "--format=%h %s",
                    "--no-merges",
                    range,
                ).stdout.lineSequence().filter { it.isNotBlank() }.toList(),
                diffStat = "",
                estimatedTag = nextTag(repository, sourceSha),
            )
        }

        val targetBranch = requireNotNull(service.targetBranch) { "合并到目标分支模式缺少目标分支" }
        val targetRef = "${service.remote}/$targetBranch"
        val targetSha = git.resolve(repository, targetRef)
        val mergeMode = when {
            git.isAncestor(repository, sourceSha, targetSha) -> MergeMode.ALREADY_MERGED
            git.isAncestor(repository, targetSha, sourceSha) -> MergeMode.FAST_FORWARD
            else -> MergeMode.MERGE_COMMIT
        }
        verifyMergeInTemporaryWorktree(repository, targetSha, sourceSha, workspace.serviceName)
        return TagPreflight(
            folderName = manifest.folderName,
            serviceName = workspace.serviceName,
            sourceBranch = workspace.branch,
            sourceSha = sourceSha,
            remote = service.remote,
            targetBranch = targetBranch,
            targetSha = targetSha,
            sourceSync = sync,
            tagMode = service.mode,
            mergeMode = mergeMode,
            commitList = git.run(
                repository,
                "log",
                "--format=%h %s",
                "--no-merges",
                "$targetSha..$sourceSha",
            ).stdout.lineSequence().filter { it.isNotBlank() }.toList(),
            diffStat = git.run(repository, "diff", "--stat", targetSha, sourceSha).stdout.trim(),
            estimatedTag = nextTag(repository, targetSha),
        )
    }

    private fun requireTagBranchesAllowed(config: AppConfig, service: EffectiveTagTarget, preview: TagPreflight) {
        val policy = GitWritePolicy(config.blockedGitWriteBranches)
        if (preview.sourceSync.pushRequired || service.mode == TagBuildMode.CURRENT_BRANCH) {
            policy.requireAllowed(preview.sourceBranch, "测试Tag流程推送源分支")
        }
        if (service.mode == TagBuildMode.MERGE_TO_TARGET_BRANCH && preview.mergeMode != MergeMode.ALREADY_MERGED) {
            policy.requireAllowed(requireNotNull(service.targetBranch), "测试Tag流程合并并推送目标分支")
        }
    }

    private fun resolveWorkspace(manifest: TaskManifest, selection: String): ServiceWorkspace? {
        val matches = manifest.services.filter { it.selectionKey == selection || it.repositoryId == selection }
        if (matches.size > 1) throw IllegalArgumentException("测试Tag目标不唯一，请选择具体模块：$selection")
        return matches.singleOrNull()
    }

    private data class MergePushResult(
        val targetSha: String,
        val conflicts: List<String> = emptyList(),
    )

    private fun mergeAndPushTargetBranch(
        repository: Path,
        sourceSha: String,
        service: EffectiveTagTarget,
        serviceName: String,
    ): MergePushResult {
        val targetBranch = requireNotNull(service.targetBranch) { "合并到目标分支模式缺少目标分支" }
        repeat(2) { attempt ->
            git.fetch(repository, service.remote)
            git.fetchTags(repository, service.remote)
            val remoteTargetRef = "${service.remote}/$targetBranch"
            val remoteTargetSha = git.resolve(repository, remoteTargetRef)
            if (git.isAncestor(repository, sourceSha, remoteTargetSha)) {
                return MergePushResult(remoteTargetSha)
            }
            val temporary = temporaryWorktreePath(repository, "$serviceName-build-${attempt + 1}")
            try {
                git.addDetachedWorktree(repository, temporary, remoteTargetSha)
                val merge = git.run(
                    temporary,
                    "merge",
                    "--no-edit",
                    sourceSha,
                    check = false,
                )
                if (!merge.succeeded) {
                    val conflicts = conflictFiles(temporary)
                    git.run(temporary, "merge", "--abort", check = false)
                    if (conflicts.isNotEmpty()) return MergePushResult(remoteTargetSha, conflicts)
                    throw GitException("合并目标分支失败", merge)
                }
                val mergedSha = git.resolve(temporary, "HEAD")
                val push = git.run(
                    temporary,
                    "push",
                    service.remote,
                    "HEAD:refs/heads/$targetBranch",
                    check = false,
                )
                if (push.succeeded) {
                    git.fetch(repository, service.remote)
                    git.fetchTags(repository, service.remote)
                    val verified = git.resolve(repository, remoteTargetRef)
                    require(verified == mergedSha) {
                        "推送后远端 $targetBranch 的 SHA 校验失败"
                    }
                    return MergePushResult(mergedSha)
                }
                if (attempt == 1) throw GitException("目标分支在推送期间被更新，重试后仍失败", push)
            } finally {
                cleanupTemporaryWorktree(repository, temporary)
            }
        }
        error("无法更新目标分支")
    }

    private fun verifyMergeInTemporaryWorktree(
        repository: Path,
        targetSha: String,
        sourceSha: String,
        serviceName: String,
    ) {
        if (git.isAncestor(repository, sourceSha, targetSha)) return
        val temporary = temporaryWorktreePath(repository, "$serviceName-preflight")
        try {
            git.addDetachedWorktree(repository, temporary, targetSha)
            val merge = git.run(
                temporary,
                "merge",
                "--no-commit",
                "--no-ff",
                sourceSha,
                check = false,
            )
            val conflicts = conflictFiles(temporary)
            git.run(temporary, "merge", "--abort", check = false)
            if (conflicts.isNotEmpty()) {
                throw MergeConflictException(conflicts)
            }
            if (!merge.succeeded) throw GitException("合并预检失败", merge)
        } finally {
            cleanupTemporaryWorktree(repository, temporary)
        }
    }

    private fun conflictFiles(worktree: Path): List<String> =
        git.run(worktree, "diff", "--name-only", "--diff-filter=U", check = false)
            .stdout
            .lineSequence()
            .filter { it.isNotBlank() }
            .toList()

    private fun featureSync(
        repository: Path,
        sourceSha: String,
        remoteSourceRef: String,
    ): FeatureSyncStatus {
        val exists = git.run(
            repository,
            "rev-parse",
            "--verify",
            "$remoteSourceRef^{commit}",
            check = false,
        ).succeeded
        if (!exists) return FeatureSyncStatus(remoteExists = false, ahead = 0, behind = 0)
        val ahead = git.run(repository, "rev-list", "--count", "$remoteSourceRef..$sourceSha")
            .stdout.trim().toInt()
        val behind = git.run(repository, "rev-list", "--count", "$sourceSha..$remoteSourceRef")
            .stdout.trim().toInt()
        return FeatureSyncStatus(remoteExists = true, ahead = ahead, behind = behind)
    }

    private fun ensureCleanFeatureWorktree(worktree: Path) {
        require(worktree.exists()) { "特性工作区不存在：$worktree" }
        val status = git.status(worktree)
        require(!status.staged && !status.unstaged && !status.untracked) {
            "特性工作区存在未提交改动，请先提交或清理"
        }
        require(status.operationInProgress == null) {
            "特性工作区正在执行 ${status.operationInProgress}"
        }
    }

    private fun nextTag(repository: Path, commit: String): String {
        val tags = git.run(
            repository,
            "for-each-ref",
            "--merged",
            commit,
            "--format=%(creatordate:unix) %(refname:short)",
            "refs/tags",
        ).stdout.lineSequence().mapNotNull { line ->
            val timestamp = line.substringBefore(' ').toLongOrNull() ?: return@mapNotNull null
            val name = line.substringAfter(' ', missingDelimiterValue = "").trim()
            name.takeIf { it.isNotEmpty() }?.let { VersionTag(it, timestamp) }
        }.toList()
        val latest = TagVersioning.latest(tags)
        if (latest != null) return TagVersioning.next(latest)
        throw IllegalStateException("仓库没有可用的历史测试Tag，无法计算下一版本；请先在仓库创建并推送一个符合版本规则的测试Tag")
    }

    private fun createAndPushTag(
        repository: Path,
        remote: String,
        initialTag: String,
        commit: String,
        message: String,
        onCandidate: (String) -> Unit,
        onCreated: (String) -> Unit,
    ): String {
        var tag = initialTag
        for (attempt in 0..1) {
            // Save each candidate before its Git write so a failed attempt can
            // resume the same Tag. Only a confirmed collision advances it.
            onCandidate(tag)
            try {
                createOrValidateLocalTag(repository, tag, commit, message)
                onCreated(tag)
                pushTag(repository, remote, tag, commit)
                return tag
            } catch (collision: TagCollisionException) {
                if (attempt == 1) throw collision
                // The occupied ref may belong to another task. Leave it intact
                // and try the next name, including during PARTIAL recovery.
                tag = TagVersioning.next(tag)
            }
        }
        error("测试Tag推送未完成")
    }

    private fun createOrValidateLocalTag(
        repository: Path,
        tag: String,
        commit: String,
        message: String,
    ) {
        val existing = git.run(repository, "rev-parse", "--verify", "refs/tags/$tag^{commit}", check = false)
        if (existing.succeeded) {
            if (existing.stdout.trim() != commit) {
                throw TagCollisionException(tag, existing.stdout.trim(), commit, location = "本地")
            }
            return
        }
        git.run(repository, "tag", "-a", tag, commit, "-m", message)
    }

    private fun pushTag(repository: Path, remote: String, tag: String, commit: String) {
        val remoteBefore = remoteTagSha(repository, remote, tag)
        if (remoteBefore != null) {
            if (remoteBefore != commit) {
                throw TagCollisionException(tag, remoteBefore, commit)
            }
            return
        }
        val push = git.run(repository, "push", remote, "refs/tags/$tag:refs/tags/$tag", check = false)
        if (!push.succeeded) {
            val remoteAfter = remoteTagSha(repository, remote, tag)
            if (remoteAfter != null && remoteAfter != commit) {
                throw TagCollisionException(tag, remoteAfter, commit)
            }
            if (remoteAfter == commit) return
            throw GitException("推送测试Tag失败", push)
        }
    }

    private fun remoteTagSha(repository: Path, remote: String, tag: String): String? {
        val result = git.run(repository, "ls-remote", remote, "refs/tags/$tag^{}", check = false)
        val dereferenced = result.stdout.lineSequence().firstOrNull()?.substringBefore('\t')?.trim()
        if (!dereferenced.isNullOrBlank()) return dereferenced
        val direct = git.run(repository, "ls-remote", remote, "refs/tags/$tag", check = false)
        return direct.stdout.lineSequence().firstOrNull()?.substringBefore('\t')?.trim()?.ifBlank { null }
    }

    private fun auditMessage(
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
        service: EffectiveTagTarget,
        sourceSha: String,
        tagCommit: String,
    ): String = buildString {
        appendLine("${service.tagMessagePrefix} build")
        appendLine("Task: ${manifest.folderName}")
        if (manifest.requirementLink.isNotBlank()) {
            appendLine("需求链接：${manifest.requirementLink.trim()}")
        }
        appendLine("Builder: ${System.getProperty("user.name")}")
        append("时间：${SilverWingTime.format(Instant.now(clock))}")
    }

    private fun temporaryWorktreePath(repository: Path, label: String): Path {
        val repoHash = repositoryHash(git.commonDirectory(repository))
        val safeLabel = label.replace(Regex("""[^A-Za-z0-9._-]"""), "-").take(50)
        val parent = paths.temp.resolve("tag-build").resolve(repoHash)
        parent.createDirectories()
        return parent.resolve("$safeLabel-${UUID.randomUUID()}")
    }

    private fun cleanupTemporaryWorktree(repository: Path, temporary: Path) {
        if (temporary.exists()) {
            git.run(temporary, "merge", "--abort", check = false)
            git.removeWorktree(repository, temporary, force = true)
        }
        git.run(repository, "worktree", "prune", check = false)
    }

    private fun transition(
        taskDirectory: Path,
        operation: TagOperation,
        state: TagOperationState,
        sourceSha: String? = operation.sourceSha,
        targetSha: String? = operation.targetSha,
        tag: String? = operation.tag,
        message: String? = operation.message,
        conflictFiles: List<String> = operation.conflictFiles,
    ): TagOperation = operation.copy(
        state = state,
        updatedAt = SilverWingTime.format(Instant.now(clock)),
        sourceSha = sourceSha,
        targetSha = targetSha,
        tag = tag,
        message = message,
        conflictFiles = conflictFiles,
    ).also { operations.save(taskDirectory, it) }

    private fun recordHistory(taskDirectory: Path, operation: TagOperation) {
        operations.appendHistory(
            taskDirectory,
            TagBuildHistoryEntry(
                operationId = operation.operationId,
                timestamp = operation.updatedAt,
                folderName = operation.folderName,
                serviceName = operation.serviceName,
                sourceBranch = operation.sourceBranch,
                targetBranch = operation.targetBranch,
                tagMode = operation.tagMode,
                tag = operation.tag,
                state = operation.state,
                message = operation.message,
                batchId = operation.batchId,
            ),
        )
        val metadata = mapOf(
            "operationId" to operation.operationId,
            "folderName" to operation.folderName,
            "service" to operation.serviceName,
            "state" to operation.state.name,
            "tag" to operation.tag.orEmpty(),
        )
        if (operation.state == TagOperationState.SUCCESS) {
            events.info(
                event = "tag.build.completed",
                message = "Tag构建成功",
                metadata = metadata,
                clock = clock,
            )
        } else {
            events.error(
                event = "tag.build.completed",
                message = operation.message ?: "Tag构建未成功",
                metadata = metadata,
                clock = clock,
            )
        }
    }

    private fun <T> withRepositoryLock(repository: Path, block: () -> T): T {
        return repositoryLock.withLock(git.commonDirectory(repository), block)
    }

    private fun repositoryHash(path: Path): String =
        MessageDigest.getInstance("SHA-256")
            .digest(path.toAbsolutePath().normalize().toString().lowercase(Locale.ROOT).toByteArray(StandardCharsets.UTF_8))
            .take(12)
            .joinToString("") { "%02x".format(Locale.ROOT, it) }
}

class MergeConflictException(
    val files: List<String>,
) : RuntimeException("合并存在冲突：${files.joinToString(", ")}")

class TagCollisionException(
    tag: String,
    remoteCommit: String,
    expectedCommit: String,
    location: String = "远端",
) : RuntimeException("${location}测试Tag $tag 已指向 $remoteCommit，预期为 $expectedCommit")
