package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Duration

/** The displayed source remains the task snapshot; independent clones name that source origin. */
data class WorkspaceMainBranch(val displayRef: String, val localRef: String) {
    val local: RemoteBranchRef get() = RemoteBranchRef.parse(localRef)
}

object WorkspaceMainBranchResolver {
    fun resolve(workspace: ServiceWorkspace, configuredMasterBranch: String?): WorkspaceMainBranch? {
        val saved = workspace.baseRef ?: configuredMasterBranch ?: return null
        val source = RemoteBranchRef.parse(saved)
        val local = if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
            RemoteBranchRef("origin", source.branch)
        } else source
        return WorkspaceMainBranch(source.toString(), local.toString())
    }

    fun resolve(config: AppConfig, task: TaskManifest, workspace: ServiceWorkspace): WorkspaceMainBranch? {
        val service = config.groups.firstOrNull { it.id == task.groupId }
            ?.services?.firstOrNull { it.id == workspace.groupServiceId }
        val module = service?.modules?.firstOrNull { it.id == workspace.moduleId }
        return resolve(workspace, module?.let { service.effectiveMasterBranch(it) } ?: service?.masterBranch)
    }
}

enum class WorkspaceMainBranchMergeOutcome { UP_TO_DATE, FAST_FORWARDED, MERGED, CONFLICT, FAILED }

data class WorkspaceMainBranchMergeResult(
    val workspacePath: String,
    val serviceName: String,
    val moduleName: String,
    val sourceRef: String,
    val targetBranch: String,
    val outcome: WorkspaceMainBranchMergeOutcome,
    val message: String,
    val conflictFiles: List<String> = emptyList(),
) {
    val direction: String get() = "$sourceRef → $targetBranch"
}

fun interface WorkspaceMainBranchMerger {
    fun merge(config: AppConfig, taskDirectory: Path, expectedTask: TaskManifest, requestedWorkspace: ServiceWorkspace): WorkspaceMainBranchMergeResult
}

/** Fetches one configured source and merges into the existing feature worktree, retaining conflicts. */
class WorkspaceMainBranchMergeService(
    private val git: GitClient = GitClient(),
    private val manifests: TaskManifestRepository = ManifestStore(),
    private val taskLock: TaskOperationLock = FileTaskOperationLock(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
    private val lifecycle: WorkspaceLifecycle = GitWorkspaceLifecycle(git),
) : WorkspaceMainBranchMerger {
    private val writeChecks = WorkspaceGitOperationService(git, repositoryLock)

    override fun merge(
        config: AppConfig,
        taskDirectory: Path,
        expectedTask: TaskManifest,
        requestedWorkspace: ServiceWorkspace,
    ): WorkspaceMainBranchMergeResult = taskLock.withLock(taskDirectory) {
        val task = manifests.load(taskDirectory)
        require(task.taskDirectoryName == expectedTask.taskDirectoryName && task.createdAt == expectedTask.createdAt &&
            task.groupId == expectedTask.groupId && task.featureBranch == expectedTask.featureBranch) {
            "任务身份已变化，请重新选择任务后再试"
        }
        require(task.lifecycleStatus == TaskLifecycleStatus.ACTIVE) { "归档任务不能合并远程主分支" }
        val workspace = task.services.singleOrNull {
            it.groupServiceId == requestedWorkspace.groupServiceId && it.moduleId == requestedWorkspace.moduleId
        } ?: error("任务中不存在指定服务模块")
        require(workspace == requestedWorkspace) { "任务工作区记录已变化，请刷新后再试" }
        require(workspace.health in setOf(WorkspaceHealth.READY, WorkspaceHealth.READY_WITH_WARNINGS)) { "工作区尚未就绪，不能合并远程主分支" }
        require(workspace.targetBranch != null && workspace.targetBranch == workspace.branch) { "该工作区没有任务特性分支，不能合并远程主分支" }
        val source = requireNotNull(WorkspaceMainBranchResolver.resolve(config, task, workspace)) { "任务记录和服务配置均未提供主分支" }
        val remote = source.local
        require(!remote.remote.startsWith('-')) { "远程名称不合法：${remote.remote}" }
        require(workspace.branch != remote.branch) { "当前工作区使用主分支，不能作为特性分支执行合并" }
        val validated = lifecycle.validateForMutation(config, taskDirectory, task, workspace)
        repositoryLock.withLock(git.commonDirectory(validated.repository)) merge@{
            lifecycle.validateForMutation(config, taskDirectory, task, workspace)
            val target = validated.worktree
            require(target.canonicalOrNormalized().parent == taskDirectory.canonicalOrNormalized()) {
                "工作区实际路径不属于当前任务目录，已拒绝合并"
            }
            if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
                IndependentCloneWorkspaceSafety.requireOwned(target, IndependentCloneWorkspaceSafety.ownership(
                    taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                ))
            }
            val before = writeChecks.preview(workspace)
            GitWritePolicy(config.blockedGitWriteBranches).requireAllowed(before.branch, "合并远程主分支")
            require(before.files.isEmpty()) { "工作区存在未提交或未跟踪修改，请先提交或自行处理后再合并" }
            require(git.remoteUrl(target, remote.remote) != null) { "主分支远程不存在：${remote.remote}" }
            // An explicit refspec also fails if this branch was deleted remotely, even when a stale
            // tracking ref remains locally or the repository has a narrow fetch configuration.
            git.run(target, "fetch", "--prune", "--no-tags", remote.remote,
                "refs/heads/${remote.branch}:refs/remotes/${source.localRef}", timeout = Duration.ofMinutes(5))
            lifecycle.validateForMutation(config, taskDirectory, task, workspace)
            val current = writeChecks.preview(workspace)
            GitWritePolicy(config.blockedGitWriteBranches).requireAllowed(current.branch, "合并远程主分支")
            require(current.head == before.head && current.branch == before.branch && current.files.isEmpty()) {
                "拉取期间工作区状态已变化，请检查后重新合并"
            }
            val qualified = "refs/remotes/${source.localRef}"
            require(git.refExists(target, qualified)) { "远程主分支不存在：${source.displayRef}" }
            val sourceHead = git.resolve(target, qualified)
            fun result(outcome: WorkspaceMainBranchMergeOutcome, message: String, files: List<String> = emptyList()) =
                WorkspaceMainBranchMergeResult(workspace.worktreePath, workspace.serviceName, workspace.moduleName,
                    source.displayRef, workspace.branch, outcome, message, files)
            if (git.isAncestor(target, sourceHead, before.head)) {
                return@merge result(WorkspaceMainBranchMergeOutcome.UP_TO_DATE, "已包含 ${source.displayRef} 的最新提交，无需合并")
            }
            val fastForward = git.isAncestor(target, before.head, sourceHead)
            val merged = git.run(target, "merge", "--no-edit", "--no-autostash", "--no-squash", "--ff", "--commit", qualified, check = false)
            if (!merged.succeeded) {
                val files = git.run(target, "diff", "--name-only", "--diff-filter=U", "-z").stdout
                    .split('\u0000').filter(String::isNotEmpty)
                if (files.isNotEmpty()) {
                    val label = if (workspace.moduleName == workspace.serviceName || workspace.moduleName.isBlank()) {
                        workspace.serviceName
                    } else "${workspace.serviceName} · ${workspace.moduleName}"
                    return@merge result(WorkspaceMainBranchMergeOutcome.CONFLICT,
                        tagMergeConflictMessage(label, source.displayRef, workspace.branch), files)
                }
                throw GitException("合并失败：${source.displayRef} → ${workspace.branch}；请检查当前 Git 状态", merged)
            }
            result(if (fastForward) WorkspaceMainBranchMergeOutcome.FAST_FORWARDED else WorkspaceMainBranchMergeOutcome.MERGED,
                if (fastForward) "已快进合并 ${source.displayRef} 到 ${workspace.branch}"
                else "已将 ${source.displayRef} 合并到 ${workspace.branch}，并创建合并提交")
        }
    }
}
