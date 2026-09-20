package com.snowball.silverwing.core

import java.nio.file.Path
import java.security.MessageDigest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.supervisorScope

object RequirementReference {
    private val detailNumber = Regex("/detail/(\\d{4,})(?=[/?#]|$)", RegexOption.IGNORE_CASE)
    private val fallbackNumber = Regex("(?<!\\d)\\d{4,}(?!\\d)")

    fun number(reference: String): String? =
        detailNumber.find(reference)?.groupValues?.getOrNull(1)
            ?: fallbackNumber.findAll(reference).lastOrNull()?.value
}

object CommitMessageTemplate {

    fun render(template: String, requirementLink: String): String {
        val number = RequirementReference.number(requirementLink).orEmpty()
        return template.replace("{num}", number)
            .replace(Regex("[ \\t]+"), " ")
            .replace(Regex(" *\\n *"), "\n")
            .trim()
    }

    fun requireValid(message: String): String = message.trim().also {
        require(it.isNotBlank()) { "提交信息不能为空" }
    }
}

object WorkspacePushCommand {
    fun build(remote: String, branch: String, setUpstream: Boolean): List<String> {
        require(remote.isNotBlank() && branch.isNotBlank()) { "远程和当前分支不能为空" }
        require(!remote.startsWith("-") && !branch.startsWith("-") && !branch.startsWith("+")) { "拒绝不安全的 Push 参数" }
        return buildList {
            add("push")
            if (setUpstream) add("-u")
            add(remote)
            add("HEAD:refs/heads/$branch")
        }
    }
}

data class WorkspaceGitOperationResult(val message: String)

enum class WorkspaceGitBatchMode {
    COMMIT,
    PUSH,
    COMMIT_AND_PUSH,
}

enum class WorkspaceGitStepState {
    NOT_RUN,
    SUCCESS,
    SKIPPED,
    FAILED,
}

data class WorkspaceGitBatchItemResult(
    val workspacePath: String,
    val serviceName: String,
    val branch: String,
    val commitState: WorkspaceGitStepState,
    val pushState: WorkspaceGitStepState,
    val message: String,
)

data class WorkspaceGitBatchResult(val items: List<WorkspaceGitBatchItemResult>)

data class WorkspaceGitChangePreview(
    val workspacePath: String,
    val branch: String,
    val head: String,
    val upstream: String?,
    val files: List<String>,
    val diffStat: String,
    val fingerprint: String,
)

private data class PushPlan(
    val remote: String,
    val branch: String,
    val setUpstream: Boolean,
)

private data class WorkspaceGitPreflight(
    val workspace: ServiceWorkspace,
    val target: Path,
    val commonDirectory: Path,
    val changePreview: WorkspaceGitChangePreview,
    val pushPlan: PushPlan?,
) {
    val hasChanges: Boolean get() = changePreview.files.isNotEmpty()
    val fingerprint: String get() = changePreview.fingerprint
}

class WorkspaceGitOperationService(
    private val git: GitClient = GitClient(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
    parallelism: Int = 4,
) {
    private val dispatcher = Dispatchers.IO.limitedParallelism(parallelism.also {
        require(it > 0) { "Git operation parallelism must be greater than zero" }
    })

    fun preview(workspace: ServiceWorkspace): WorkspaceGitChangePreview {
        val target = validate(workspace)
        return previewValidated(workspace, target)
    }

    /** Parallel read-only previews for the batch dialog; results key by physical workspace path. */
    fun previews(workspaces: List<ServiceWorkspace>): Map<String, WorkspaceGitChangePreview> = runBlocking {
        supervisorScope {
            workspaces.distinctBy(::workspacePathKey).map { workspace ->
                async(dispatcher) { workspacePathKey(workspace) to preview(workspace) }
            }.awaitAll().toMap()
        }
    }

    private fun previewValidated(workspace: ServiceWorkspace, target: Path): WorkspaceGitChangePreview {
        val branch = requireNotNull(git.currentBranch(target)) { "Detached HEAD 不能执行 Git 操作" }
        val head = git.run(target, "rev-parse", "HEAD").stdout.trim()
        val upstreamResult = git.run(target, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}", check = false)
        val upstream = upstreamResult.stdout.trim().takeIf { upstreamResult.succeeded && it.isNotBlank() }
        val porcelain = git.run(target, "status", "--short", "--untracked-files=all").stdout
        val diffStat = git.run(target, "diff", "--stat", "HEAD", check = false).stdout.trim()
        return WorkspaceGitChangePreview(
            workspacePath = workspacePathKey(workspace),
            branch = branch,
            head = head,
            upstream = upstream,
            files = porcelain.lineSequence().filter(String::isNotBlank).toList(),
            diffStat = diffStat,
            fingerprint = fingerprint(branch, head, upstream, porcelain),
        )
    }
    fun commit(
        workspace: ServiceWorkspace,
        message: String,
        expectedFingerprint: String? = null,
        blockedBranches: Collection<String> = listOf("master", "main"),
    ): WorkspaceGitOperationResult = withRepositoryLock(workspace) { target ->
        requireWriteAllowed(workspace, target, blockedBranches, "提交")
        commitUnlocked(workspace, target, message, expectedFingerprint)
    }

    private fun commitUnlocked(workspace: ServiceWorkspace, target: Path, message: String, expectedFingerprint: String? = null): WorkspaceGitOperationResult {
        expectedFingerprint?.let { expected ->
            require(previewValidated(workspace, target).fingerprint == expected) { "工作区状态已变化，请重新预览后确认：${workspace.operationName()}" }
        }
        val commitMessage = CommitMessageTemplate.requireValid(message)
        git.run(target, "add", "-A")
        require(!git.run(target, "diff", "--cached", "--quiet", check = false).succeeded) { "没有可提交的变更" }
        git.run(target, "commit", "-m", commitMessage)
        return WorkspaceGitOperationResult("已提交 ${workspace.branch}")
    }

    fun push(
        workspace: ServiceWorkspace,
        blockedBranches: Collection<String> = listOf("master", "main"),
    ): WorkspaceGitOperationResult = withRepositoryLock(workspace) { target ->
        val branch = requireWriteAllowed(workspace, target, blockedBranches, "推送")
        val plan = resolvePushPlan(workspace, target, branch)
        pushPlanned(target, plan)
    }

    private fun pushPlanned(target: Path, plan: PushPlan): WorkspaceGitOperationResult {
        val command = WorkspacePushCommand.build(plan.remote, plan.branch, plan.setUpstream)
        git.run(target, *command.toTypedArray())
        return WorkspaceGitOperationResult("已推送 ${plan.remote}/${plan.branch}")
    }

    fun commitAndPush(
        workspace: ServiceWorkspace,
        message: String,
        expectedFingerprint: String? = null,
        blockedBranches: Collection<String> = listOf("master", "main"),
    ): WorkspaceGitOperationResult =
        withRepositoryLock(workspace) { target ->
            requireWriteAllowed(workspace, target, blockedBranches, "提交和推送")
            commitUnlocked(workspace, target, message, expectedFingerprint)
            // The write policy is re-read right before pushing so a change after the commit still blocks.
            val branch = requireNotNull(git.currentBranch(target)) { "Detached HEAD 不能推送" }
            GitWritePolicy(blockedBranches).requireAllowed(branch, "提交和推送")
            pushPlanned(target, resolvePushPlan(workspace, target, branch))
        }

    /**
     * Performs a complete read-only preflight in parallel before the first write; any preflight
     * failure aborts the batch untouched. Writes are then serialized per repository (the exclusive
     * file lock rejects same-repo concurrency) while different repositories proceed in parallel.
     */
    fun batch(
        workspaces: List<ServiceWorkspace>,
        mode: WorkspaceGitBatchMode,
        commitMessages: Map<String, String> = emptyMap(),
        expectedFingerprints: Map<String, String> = emptyMap(),
        blockedBranches: Collection<String> = listOf("master", "main"),
    ): WorkspaceGitBatchResult = runBlocking {
        val unique = workspaces.distinctBy(::workspacePathKey)
        val preflights = supervisorScope {
            unique.map { workspace ->
                async(dispatcher) { preflight(workspace, mode, commitMessages, expectedFingerprints, blockedBranches) }
            }.awaitAll()
        }
        val operation = operationLabel(mode)
        val byRepository = preflights.withIndex().groupBy { it.value.commonDirectory }
        val results = supervisorScope {
            byRepository.values.map { group ->
                async(dispatcher) {
                    group.map { (index, item) ->
                        index to executeBatchItem(item, mode, commitMessages[workspacePathKey(item.workspace)].orEmpty(), blockedBranches, operation)
                    }
                }
            }.awaitAll().flatten()
        }
        WorkspaceGitBatchResult(results.sortedBy { it.first }.map { it.second })
    }

    private fun preflight(
        workspace: ServiceWorkspace,
        mode: WorkspaceGitBatchMode,
        commitMessages: Map<String, String>,
        expectedFingerprints: Map<String, String>,
        blockedBranches: Collection<String>,
    ): WorkspaceGitPreflight {
        val changePreview = preview(workspace)
        GitWritePolicy(blockedBranches).requireAllowed(changePreview.branch, operationLabel(mode))
        expectedFingerprints[workspacePathKey(workspace)]?.let { expected ->
            require(changePreview.fingerprint == expected) { "工作区状态已变化，请重新预览后确认：${workspace.operationName()}" }
        }
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        val commonDirectory = lockDirectory(workspace, target)
        val pushPlan = if (mode != WorkspaceGitBatchMode.COMMIT) resolvePushPlan(workspace, target, changePreview.branch) else null
        if (changePreview.files.isNotEmpty() && mode != WorkspaceGitBatchMode.PUSH) {
            CommitMessageTemplate.requireValid(commitMessages[workspacePathKey(workspace)].orEmpty())
        }
        return WorkspaceGitPreflight(workspace, target, commonDirectory, changePreview, pushPlan)
    }

    private fun operationLabel(mode: WorkspaceGitBatchMode): String = when (mode) {
        WorkspaceGitBatchMode.COMMIT -> "提交"
        WorkspaceGitBatchMode.PUSH -> "推送"
        WorkspaceGitBatchMode.COMMIT_AND_PUSH -> "提交和推送"
    }

    private fun executeBatchItem(
        preflight: WorkspaceGitPreflight,
        mode: WorkspaceGitBatchMode,
        commitMessage: String,
        blockedBranches: Collection<String>,
        operation: String,
    ): WorkspaceGitBatchItemResult = repositoryLock.withLock(preflight.commonDirectory) {
        val workspace = preflight.workspace
        val target = preflight.target
        var commitState = WorkspaceGitStepState.NOT_RUN
        var pushState = WorkspaceGitStepState.NOT_RUN
        val messages = mutableListOf<String>()

        val writePolicyFailure = runCatching {
            requireWriteAllowedLocked(workspace, target, blockedBranches, operation)
        }.exceptionOrNull()
        if (writePolicyFailure != null) {
            when (mode) {
                WorkspaceGitBatchMode.COMMIT,
                WorkspaceGitBatchMode.COMMIT_AND_PUSH,
                -> commitState = WorkspaceGitStepState.FAILED
                WorkspaceGitBatchMode.PUSH -> pushState = WorkspaceGitStepState.FAILED
            }
            return@withLock WorkspaceGitBatchItemResult(
                workspacePath = workspacePathKey(workspace),
                serviceName = workspace.moduleName.ifBlank { workspace.serviceName },
                branch = workspace.branch,
                commitState = commitState,
                pushState = pushState,
                message = writePolicyFailure.message ?: "Git 写保护检查失败",
            )
        }

        if (mode == WorkspaceGitBatchMode.PUSH || mode == WorkspaceGitBatchMode.COMMIT_AND_PUSH && !preflight.hasChanges) {
            if (mode == WorkspaceGitBatchMode.COMMIT_AND_PUSH) commitState = WorkspaceGitStepState.SKIPPED
            val currentFingerprint = runCatching { previewValidated(workspace, target).fingerprint }.getOrElse { error ->
                pushState = WorkspaceGitStepState.FAILED
                return@withLock WorkspaceGitBatchItemResult(
                    workspacePath = workspacePathKey(workspace),
                    serviceName = workspace.moduleName.ifBlank { workspace.serviceName },
                    branch = workspace.branch,
                    commitState = commitState,
                    pushState = pushState,
                    message = error.message ?: "工作区状态复核失败",
                )
            }
            if (currentFingerprint != preflight.fingerprint) {
                pushState = WorkspaceGitStepState.FAILED
                return@withLock WorkspaceGitBatchItemResult(
                    workspacePath = workspacePathKey(workspace),
                    serviceName = workspace.moduleName.ifBlank { workspace.serviceName },
                    branch = workspace.branch,
                    commitState = commitState,
                    pushState = pushState,
                    message = "工作区状态已变化，请重新预览后确认：${workspace.operationName()}",
                )
            }
        }

        if (mode != WorkspaceGitBatchMode.PUSH) {
            if (!preflight.hasChanges) {
                commitState = WorkspaceGitStepState.SKIPPED
                messages += "没有需要提交的变更"
            } else {
                runCatching { commitUnlocked(workspace, target, commitMessage, preflight.fingerprint) }
                    .onSuccess { commitState = WorkspaceGitStepState.SUCCESS; messages += it.message }
                    .onFailure { error ->
                        commitState = WorkspaceGitStepState.FAILED
                        messages += (error.message ?: "提交失败")
                    }
            }
        }

        val shouldPush = mode == WorkspaceGitBatchMode.PUSH ||
            mode == WorkspaceGitBatchMode.COMMIT_AND_PUSH && commitState != WorkspaceGitStepState.FAILED
        if (shouldPush) {
            runCatching {
                // The write policy is re-read right before pushing so a change after the commit still blocks.
                val branch = requireNotNull(git.currentBranch(target)) { "Detached HEAD 不能推送" }
                GitWritePolicy(blockedBranches).requireAllowed(branch, operation)
                pushPlanned(target, preflight.pushPlan!!)
            }
                .onSuccess { pushState = WorkspaceGitStepState.SUCCESS; messages += it.message }
                .onFailure { error ->
                    pushState = WorkspaceGitStepState.FAILED
                    messages += (error.message ?: "推送失败")
                }
        }

        WorkspaceGitBatchItemResult(
            workspacePath = workspacePathKey(workspace),
            serviceName = workspace.moduleName.ifBlank { workspace.serviceName },
            branch = workspace.branch,
            commitState = commitState,
            pushState = pushState,
            message = messages.joinToString("；"),
        )
    }

    private fun <T> withRepositoryLock(workspace: ServiceWorkspace, block: (Path) -> T): T {
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        return repositoryLock.withLock(lockDirectory(workspace, target)) { block(target) }
    }

    private fun lockDirectory(workspace: ServiceWorkspace, target: Path): Path = when (workspace.strategy) {
        WorkspaceStrategy.STANDARD_WORKTREE ->
            git.commonDirectory(Path.of(workspace.repositoryPath).toAbsolutePath().normalize())
        WorkspaceStrategy.INDEPENDENT_CLONE -> git.commonDirectory(target)
    }.toAbsolutePath().normalize()

    /** Full validation used by single operations and batch preflight, including Git user config. */
    private fun validate(workspace: ServiceWorkspace): Path {
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        revalidateLocked(workspace, target)
        require(git.run(target, "config", "user.name", check = false).stdout.isNotBlank()) { "Git user.name 未配置" }
        require(git.run(target, "config", "user.email", check = false).stdout.isNotBlank()) { "Git user.email 未配置" }
        return target
    }

    /** In-lock recheck that skips the user config already proven during preflight. */
    private fun revalidateLocked(workspace: ServiceWorkspace, target: Path) {
        require(git.topLevel(target).canonicalOrNormalized() == target.canonicalOrNormalized()) {
            "工作区路径不再是 Git 顶层目录：$target"
        }
        val recordedRepository = Path.of(workspace.repositoryPath).toAbsolutePath().normalize()
        when (workspace.strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> require(
                git.commonDirectory(target).canonicalOrNormalized() ==
                    git.commonDirectory(recordedRepository).canonicalOrNormalized(),
            ) { "Worktree Git 身份与任务记录不一致" }
            WorkspaceStrategy.INDEPENDENT_CLONE -> require(git.remoteUrl(target)?.trim() == workspace.originUrl?.trim()) { "独立克隆 origin 与任务记录不一致" }
        }
        require(git.currentBranch(target) == workspace.branch) { "当前分支与任务记录不一致" }
        require(git.status(target).operationInProgress == null) { "存在进行中的 Git 操作，完成或中止后再试" }
    }

    private fun requireWriteAllowed(workspace: ServiceWorkspace, target: Path, blockedBranches: Collection<String>, operation: String): String {
        validate(workspace)
        val actual = requireNotNull(git.currentBranch(target)) { "Detached HEAD 不能执行 Git 操作" }
        GitWritePolicy(blockedBranches).requireAllowed(actual, operation)
        return actual
    }

    private fun requireWriteAllowedLocked(workspace: ServiceWorkspace, target: Path, blockedBranches: Collection<String>, operation: String) {
        revalidateLocked(workspace, target)
        val actual = requireNotNull(git.currentBranch(target)) { "Detached HEAD 不能执行 Git 操作" }
        GitWritePolicy(blockedBranches).requireAllowed(actual, operation)
    }

    private fun resolvePushPlan(workspace: ServiceWorkspace, target: Path, branch: String? = null): PushPlan {
        val current = branch ?: git.currentBranch(target) ?: error("Detached HEAD 不能推送")
        val upstream = git.run(target, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}", check = false)
        val upstreamName = upstream.stdout.trim().takeIf { upstream.succeeded && it.contains('/') }
        val remote = upstreamName?.substringBefore('/') ?: workspace.pushRemote.ifBlank { "origin" }
        require(git.remoteUrl(target, remote) != null) { "Push 远程不存在：$remote" }
        val remoteBranch = git.run(target, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/$current", check = false)
        if (!remoteBranch.succeeded && remoteBranch.exitCode != 2) {
            throw GitException("远程分支检查失败：$remote/$current", remoteBranch)
        }
        return PushPlan(remote, current, upstreamName != "$remote/$current" || !remoteBranch.succeeded)
    }

    companion object {
        fun workspacePathKey(workspace: ServiceWorkspace): String =
            Path.of(workspace.worktreePath).toAbsolutePath().normalize().toString()

        private fun fingerprint(branch: String, head: String, upstream: String?, porcelain: String): String {
            val bytes = "$branch\u0000$head\u0000${upstream.orEmpty()}\u0000$porcelain".toByteArray()
            return MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
        }
    }

    private fun ServiceWorkspace.operationName(): String = moduleName.ifBlank { serviceName }
}
