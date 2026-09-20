package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.util.Comparator
import kotlin.io.path.exists

enum class WorkspaceRepairAction {
    REATTACH_WORKTREE,
    TRACK_REMOTE_AND_ATTACH,
    BACKUP_AND_RECREATE_WORKTREE,
    RECLONE,
    SWITCH_BRANCH,
    MANUAL,
}

data class WorkspaceRepairPreview(
    val workspacePath: String,
    val serviceName: String,
    val issue: WorkspaceGitIssue,
    val action: WorkspaceRepairAction,
    val canRepair: Boolean,
    val expectedBranch: String,
    val actualBranch: String? = null,
    val remote: String? = null,
    val remoteBranchExists: Boolean = false,
    val remoteBranchSha: String? = null,
    val requiresRemoteReuseConfirmation: Boolean = false,
    val requiresSharedBranchConfirmation: Boolean = false,
    val occupiedWorktreePaths: List<String> = emptyList(),
    val lockedWorktreePaths: List<String> = emptyList(),
    val backupPath: String? = null,
    val runsBootstrap: Boolean = false,
    val steps: List<String>,
    val message: String,
    val stateFingerprint: String,
)

data class WorkspaceRepairConfirmation(
    val reuseRemoteBranch: Boolean = false,
    val shareCheckedOutBranch: Boolean = false,
)

data class WorkspaceRepairResult(
    val workspacePath: String,
    val serviceName: String,
    val backupPath: String? = null,
    val warnings: List<String> = emptyList(),
    val message: String,
)

class WorkspaceRepairService(
    private val manifests: TaskManifestRepository = ManifestStore(),
    private val agentDocuments: AgentDocuments = AgentDocumentService(),
    private val taskLock: TaskOperationLock = FileTaskOperationLock(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
    private val git: GitClient = GitClient(),
    private val bootstrap: BootstrapService = BootstrapService(),
    private val healthReader: WorkspaceGitStatusReader = GitWorkspaceGitStatusReader(git),
    private val clock: Clock = Clock.systemUTC(),
) {
    fun inspect(config: AppConfig, taskDirectory: Path, workspacePath: String): WorkspaceRepairPreview {
        val manifest = manifests.load(taskDirectory)
        val workspace = physicalWorkspace(manifest, workspacePath)
        return inspectWorkspace(config, taskDirectory, manifest, workspace)
    }

    fun repair(
        config: AppConfig,
        taskDirectory: Path,
        requested: WorkspaceRepairPreview,
        confirmation: WorkspaceRepairConfirmation,
    ): WorkspaceRepairResult = taskLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val workspace = physicalWorkspace(manifest, requested.workspacePath)
        val repository = config.repositories.firstOrNull { it.id == workspace.repositoryId }
        val execute = {
            val current = inspectWorkspace(config, taskDirectory, manifest, workspace, requested.backupPath)
            require(current.stateFingerprint == requested.stateFingerprint) {
                "工作区状态在确认后发生变化，请重新检查修复方案"
            }
            require(current.canRepair) { current.message }
            require(!current.requiresRemoteReuseConfirmation || confirmation.reuseRemoteBranch) {
                "必须确认复用远程分支后才能修复"
            }
            require(!current.requiresSharedBranchConfirmation || confirmation.shareCheckedOutBranch) {
                "必须确认共享已检出分支的风险后才能修复"
            }
            executeRepair(config, taskDirectory, manifest, workspace, current, repository)
        }
        if (repository == null) {
            execute()
        } else {
            // Validate before deriving a fallback lock key. The same validation
            // is repeated by execute() after the repository lock is acquired.
            val target = validateRepairTarget(config, taskDirectory, manifest, workspace)
            val lockRoot = if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE && Files.exists(target)) {
                // A damaged clone has no usable Git common directory. Lock the
                // validated task-local target path until execute() re-inspects it
                // and moves it to the ownership-checked backup.
                runCatching { git.commonDirectory(target).toAbsolutePath().normalize() }
                    .getOrElse { target }
            } else {
                Path.of(repository.gitCommonDirectory).toAbsolutePath().normalize()
            }
            repositoryLock.withLock(lockRoot, execute)
        }
    }

    private fun inspectWorkspace(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
        fixedBackupPath: String? = null,
    ): WorkspaceRepairPreview {
        val target = validateRepairTarget(config, taskDirectory, manifest, workspace)
        val health = healthReader.read(workspace)
        val targetExists = Files.exists(target)
        val repository = config.repositories.firstOrNull { it.id == workspace.repositoryId }
        val backupPath = fixedBackupPath ?: target.takeIf {
            targetExists && health.issue in setOf(WorkspaceGitIssue.NOT_GIT, WorkspaceGitIssue.IDENTITY_MISMATCH)
        }?.resolveSibling("${target.fileName}.silverwing-repair-backup-${Instant.now(clock).toEpochMilli()}")?.toString()
        return when (workspace.strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> inspectWorktree(workspace, health, target, repository, backupPath)
            WorkspaceStrategy.INDEPENDENT_CLONE -> inspectClone(workspace, health, target, repository, backupPath)
        }
    }

    private fun inspectWorktree(
        workspace: ServiceWorkspace,
        health: WorkspaceGitHealth,
        target: Path,
        repository: RepositoryConfig?,
        backupPath: String?,
    ): WorkspaceRepairPreview {
        val repositoryPath = repository?.rootPath?.let(Path::of)?.toAbsolutePath()?.normalize()
            ?: return manual(workspace, health, "任务记录对应的原仓库配置不存在")
        runCatching {
            require(git.topLevel(repositoryPath).canonicalOrNormalized() == repositoryPath.canonicalOrNormalized())
            require(
                git.commonDirectory(repositoryPath).canonicalOrNormalized() ==
                    Path.of(repository.gitCommonDirectory).canonicalOrNormalized(),
            )
        }.getOrElse { return manual(workspace, health, "原仓库 Git 身份已失效，无法自动修复 Worktree") }
        val localExists = git.refExists(repositoryPath, "refs/heads/${workspace.branch}")
        val remoteSha = remoteBranchSha(repositoryPath, workspace.pushRemote, workspace.branch)
        val remoteExists = remoteSha != null
        val occupied = if (localExists) {
            git.worktrees(repositoryPath).filter {
                it.branch == workspace.branch && it.path.canonicalOrNormalized() != target.canonicalOrNormalized()
            }
        } else emptyList()
        val locked = occupied.filter(WorktreeRecord::locked)
        val fingerprint = fingerprint(workspace, health, target, localExists, remoteSha, occupied)
        if (locked.isNotEmpty()) return preview(
            workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint,
            message = "期望分支被锁定 Worktree 占用，请先执行 git worktree unlock",
            occupied = occupied, locked = locked,
        )
        if (health.issue == WorkspaceGitIssue.OPERATION_IN_PROGRESS || health.issue == WorkspaceGitIssue.INSPECTION_FAILED) {
            return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = health.message ?: "无法自动修复")
        }
        if (health.issue in setOf(WorkspaceGitIssue.BRANCH_MISMATCH, WorkspaceGitIssue.DETACHED_HEAD)) {
            val status = git.status(target)
            if (status.operationInProgress != null) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "存在进行中的 Git 操作：${status.operationInProgress}")
            if (status.hasUncommittedChanges) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "工作区存在改动，提交或清理后才能切换分支")
            if (!localExists && !remoteExists) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "期望分支在本地和远程都不存在，无法安全切换")
            return preview(
                workspace, health, WorkspaceRepairAction.SWITCH_BRANCH, true, fingerprint,
                remote = workspace.pushRemote, remoteExists = remoteExists,
                remoteSha = remoteSha,
                requireRemote = !localExists && remoteExists,
                requireShared = occupied.isNotEmpty(), occupied = occupied,
                steps = listOf("确认工作区干净", "切换到任务分支 ${workspace.branch}", "重新检查 Git 状态"),
                message = "将当前工作区切换回任务定义的分支",
            )
        }
        if (health.issue !in setOf(WorkspaceGitIssue.MISSING, WorkspaceGitIssue.NOT_GIT, WorkspaceGitIssue.IDENTITY_MISMATCH)) {
            return manual(workspace, health, "当前工作区不需要修复")
        }
        if (!localExists && !remoteExists) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "任务分支在本地和远程都不存在，无法证明应恢复到哪个提交")
        val invalidExisting = Files.exists(target)
        val action = when {
            invalidExisting -> WorkspaceRepairAction.BACKUP_AND_RECREATE_WORKTREE
            localExists -> WorkspaceRepairAction.REATTACH_WORKTREE
            else -> WorkspaceRepairAction.TRACK_REMOTE_AND_ATTACH
        }
        return preview(
            workspace, health, action, true, fingerprint,
            remote = workspace.pushRemote, remoteExists = remoteExists,
            remoteSha = remoteSha,
            requireRemote = !localExists && remoteExists,
            requireShared = occupied.isNotEmpty(), occupied = occupied,
            backupPath = backupPath, runsBootstrap = true,
            steps = buildList {
                if (invalidExisting) add("将失效目录备份到 $backupPath")
                add("清理陈旧 Worktree 登记")
                add(if (localExists) "重新附加本地分支 ${workspace.branch}" else "复用 $workspace.pushRemote/${workspace.branch} 创建本地跟踪分支")
                add("执行服务 Bootstrap")
                add("重新检查 Git 状态")
            },
            message = "将重新创建任务 Worktree",
        )
    }

    private fun inspectClone(
        workspace: ServiceWorkspace,
        health: WorkspaceGitHealth,
        target: Path,
        repository: RepositoryConfig?,
        backupPath: String?,
    ): WorkspaceRepairPreview {
        val origin = workspace.originUrl?.takeIf(String::isNotBlank)
            ?: return manual(workspace, health, "独立克隆缺少 origin URL")
        val commandRoot = repository?.rootPath?.let(Path::of)?.toAbsolutePath()?.normalize()?.takeIf(Files::isDirectory)
            ?: target.parent
        val remoteSha = remoteBranchSha(commandRoot, origin, workspace.branch)
        val remoteExists = remoteSha != null
        val localExists = Files.isDirectory(target) && runCatching { git.refExists(target, "refs/heads/${workspace.branch}") }.getOrDefault(false)
        val fingerprint = fingerprint(workspace, health, target, localExists, remoteSha, emptyList())
        if (health.issue == WorkspaceGitIssue.OPERATION_IN_PROGRESS || health.issue == WorkspaceGitIssue.INSPECTION_FAILED) {
            return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = health.message ?: "无法自动修复")
        }
        if (health.issue in setOf(WorkspaceGitIssue.BRANCH_MISMATCH, WorkspaceGitIssue.DETACHED_HEAD)) {
            val status = git.status(target)
            if (status.operationInProgress != null) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "存在进行中的 Git 操作：${status.operationInProgress}")
            if (status.hasUncommittedChanges) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "独立克隆存在改动，提交或清理后才能切换分支")
            if (!localExists && !remoteExists) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "期望分支在本地和远程都不存在")
            return preview(
                workspace, health, WorkspaceRepairAction.SWITCH_BRANCH, true, fingerprint,
                remote = workspace.pushRemote, remoteExists = remoteExists,
                remoteSha = remoteSha,
                requireRemote = !localExists && remoteExists,
                steps = listOf("确认独立克隆工作区干净", "切换到 ${workspace.branch}", "重新检查 Git 状态"),
                message = "将独立克隆切换回任务分支",
            )
        }
        if (health.issue !in setOf(WorkspaceGitIssue.MISSING, WorkspaceGitIssue.NOT_GIT, WorkspaceGitIssue.IDENTITY_MISMATCH)) {
            return manual(workspace, health, "当前工作区不需要修复")
        }
        if (!remoteExists) return preview(workspace, health, WorkspaceRepairAction.MANUAL, false, fingerprint, message = "远程分支不存在，无法重新克隆")
        val invalidExisting = Files.exists(target)
        return preview(
            workspace, health, WorkspaceRepairAction.RECLONE, true, fingerprint,
            remote = origin, remoteExists = true, backupPath = backupPath, runsBootstrap = true,
            remoteSha = remoteSha,
            steps = buildList {
                if (invalidExisting) add("将失效目录备份到 $backupPath")
                add("从远程重新克隆分支 ${workspace.branch}")
                add("执行服务 Bootstrap")
                add("重新检查 Git 状态")
            },
            message = "将重新创建独立克隆",
        )
    }

    private fun executeRepair(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
        preview: WorkspaceRepairPreview,
        repository: RepositoryConfig?,
    ): WorkspaceRepairResult {
        // Persisted manifests are untrusted input. Validate again immediately
        // before any filesystem or Git mutation, inside the task/repository locks.
        val target = validateRepairTarget(config, taskDirectory, manifest, workspace)
        val backup = preview.backupPath?.let(Path::of)
        var backupMoved = false
        var workspaceCreated = false
        var cloneOwnership: String? = null
        var previousBranch: String? = null
        var previousHead: String? = null
        var updatedManifest = false
        var warnings = emptyList<String>()
        try {
            if (backup != null && Files.exists(target)) {
                require(target.parent == taskDirectory.toAbsolutePath().normalize()) { "只能备份任务目录内的工作区" }
                require(!Files.exists(backup)) { "备份目标已经存在：$backup" }
                Files.move(target, backup)
                backupMoved = true
            }
            when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> {
                    val repositoryPath = Path.of(requireNotNull(repository).rootPath).toAbsolutePath().normalize()
                    if (preview.action == WorkspaceRepairAction.SWITCH_BRANCH) {
                        previousBranch = git.currentBranch(target)
                        previousHead = git.resolve(target, "HEAD")
                        git.fetch(repositoryPath, workspace.pushRemote)
                        switchToExpected(target, workspace, preview.requiresSharedBranchConfirmation)
                    } else {
                        git.pruneWorktrees(repositoryPath)
                        git.fetch(repositoryPath, workspace.pushRemote)
                        val local = git.refExists(repositoryPath, "refs/heads/${workspace.branch}")
                        val remote = git.refExists(repositoryPath, "refs/remotes/${workspace.pushRemote}/${workspace.branch}")
                        if (!local && remote) {
                            require(git.resolve(repositoryPath, "${workspace.pushRemote}/${workspace.branch}") == preview.remoteBranchSha) {
                                "远程任务分支状态已变化，请重新预检"
                            }
                        }
                        when {
                            local -> git.addExistingWorktree(repositoryPath, target, workspace.branch, preview.requiresSharedBranchConfirmation)
                            remote -> git.addTrackedRemoteWorktree(repositoryPath, target, workspace.branch, workspace.pushRemote)
                            else -> error("确认后任务分支已经消失，请重新预检")
                        }
                        workspaceCreated = true
                        warnings = initialize(config, manifest, workspace, repositoryPath, target)
                    }
                }
                WorkspaceStrategy.INDEPENDENT_CLONE -> {
                    if (preview.action == WorkspaceRepairAction.SWITCH_BRANCH) {
                        previousBranch = git.currentBranch(target)
                        previousHead = git.resolve(target, "HEAD")
                        git.fetch(target, workspace.pushRemote)
                        switchToExpected(target, workspace, false)
                    } else {
                        cloneOwnership = IndependentCloneWorkspaceSafety.ownership(
                            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                        )
                        IndependentCloneWorkspaceSafety.cloneIntoPlace(taskDirectory, target, requireNotNull(cloneOwnership)) { staging ->
                            git.cloneRepository(requireNotNull(workspace.originUrl), staging, workspace.branch)
                            require(git.resolve(staging, "HEAD") == preview.remoteBranchSha) {
                                "远程任务分支状态已变化，请重新预检"
                            }
                        }
                        workspaceCreated = true
                        val source = requireNotNull(repository).rootPath.let(Path::of).toAbsolutePath().normalize()
                        warnings = initialize(config, manifest, workspace, source, target)
                    }
                }
            }
            val updated = manifest.copy(
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                services = manifest.services.map { existing ->
                    if (normalize(existing.worktreePath) == normalize(workspace.worktreePath)) {
                        existing.copy(
                            health = if (warnings.isEmpty()) WorkspaceHealth.READY else WorkspaceHealth.READY_WITH_WARNINGS,
                            warnings = warnings,
                        )
                    } else existing
                },
            )
            manifests.save(taskDirectory, updated)
            updatedManifest = true
            agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
            return WorkspaceRepairResult(
                workspacePath = workspace.worktreePath,
                serviceName = workspace.moduleName.ifBlank { workspace.serviceName },
                backupPath = backup?.toString(),
                warnings = warnings,
                message = "工作区已修复并重新检查",
            )
        } catch (error: Throwable) {
            val rollbackFailures = mutableListOf<Throwable>()
            if (updatedManifest) runCatching {
                manifests.save(taskDirectory, manifest)
                agentDocuments.writeTaskDocument(taskDirectory, manifest, config.repositories.map(RepositoryConfig::toInfo))
            }.onFailure(rollbackFailures::add)
            if (previousHead != null && Files.exists(target)) runCatching {
                if (!git.status(target).hasUncommittedChanges) {
                    if (previousBranch != null) git.run(target, "switch", previousBranch) else git.run(target, "switch", "--detach", previousHead)
                }
            }.onFailure(rollbackFailures::add)
            if (workspaceCreated && Files.exists(target)) runCatching {
                when (workspace.strategy) {
                    WorkspaceStrategy.STANDARD_WORKTREE -> {
                        val repositoryPath = Path.of(requireNotNull(repository).rootPath).toAbsolutePath().normalize()
                        git.removeWorktree(repositoryPath, target, force = true)
                        git.pruneWorktrees(repositoryPath)
                    }
                    WorkspaceStrategy.INDEPENDENT_CLONE -> IndependentCloneWorkspaceSafety.deleteOwned(
                        taskDirectory, target, requireNotNull(cloneOwnership),
                    )
                }
            }.onFailure(rollbackFailures::add)
            if (backupMoved && backup != null && Files.exists(backup) && !Files.exists(target)) runCatching { Files.move(backup, target) }.onFailure(rollbackFailures::add)
            if (rollbackFailures.isNotEmpty()) throw IllegalStateException(
                "工作区修复失败，且自动回滚未完整完成：${error.message}", error,
            ).apply { rollbackFailures.forEach(::addSuppressed) }
            throw error
        }
    }

    private fun switchToExpected(target: Path, workspace: ServiceWorkspace, allowShared: Boolean) {
        if (git.currentBranch(target) == workspace.branch) return
        val local = git.refExists(target, "refs/heads/${workspace.branch}")
        val remoteRef = "${workspace.pushRemote}/${workspace.branch}"
        val remote = git.refExists(target, "refs/remotes/$remoteRef")
        when {
            local -> git.run(target, "switch", *(if (allowShared) arrayOf("--ignore-other-worktrees", workspace.branch) else arrayOf(workspace.branch)))
            remote -> git.run(target, "switch", "-c", workspace.branch, "--track", remoteRef)
            else -> error("期望分支已经不存在，请重新预检")
        }
    }

    private fun initialize(config: AppConfig, manifest: TaskManifest, workspace: ServiceWorkspace, source: Path, target: Path): List<String> {
        val service = config.group(manifest.groupId).services.firstOrNull { it.id == workspace.groupServiceId }
            ?: return listOf("服务配置已经不存在，未执行 Bootstrap")
        return bootstrap.initialize(source, target, service.bootstrap).warnings
    }

    private fun physicalWorkspace(manifest: TaskManifest, workspacePath: String): ServiceWorkspace = manifest.services
        .firstOrNull { normalize(it.worktreePath) == normalize(workspacePath) }
        ?: error("任务中不存在工作区：$workspacePath")

    private fun validateRepairTarget(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
    ): Path {
        val rootValue = requireNotNull(config.taskRoot?.takeIf(String::isNotBlank)) { "尚未配置任务根目录" }
        val root = Path.of(rootValue).toAbsolutePath().normalize()
        val task = taskDirectory.toAbsolutePath().normalize()
        require(task.parent != null && task != task.root) { "拒绝对文件系统根目录执行任务修复" }
        require(task.parent == root && task.fileName.toString() == manifest.taskDirectoryName) {
            "任务目录不属于当前任务根目录或与任务清单不一致：$task"
        }
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        require(target.parent == task) { "工作区路径不是任务目录的直接子级，已拒绝修复：$target" }
        return target
    }

    private fun remoteBranchSha(repository: Path, remote: String, branch: String): String? {
        val result = git.run(repository, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/$branch", check = false)
        if (result.succeeded) return result.stdout.lineSequence().firstOrNull { it.isNotBlank() }
            ?.substringBefore('\t')?.trim()?.ifBlank { null }
        if (result.exitCode == 2) return null
        throw GitException("远程分支检查失败：$remote/$branch", result)
    }

    private fun preview(
        workspace: ServiceWorkspace,
        health: WorkspaceGitHealth,
        action: WorkspaceRepairAction,
        canRepair: Boolean,
        fingerprint: String,
        message: String,
        remote: String? = null,
        remoteExists: Boolean = false,
        remoteSha: String? = null,
        requireRemote: Boolean = false,
        requireShared: Boolean = false,
        occupied: List<WorktreeRecord> = emptyList(),
        locked: List<WorktreeRecord> = emptyList(),
        backupPath: String? = null,
        runsBootstrap: Boolean = false,
        steps: List<String> = emptyList(),
    ) = WorkspaceRepairPreview(
        workspacePath = workspace.worktreePath,
        serviceName = workspace.moduleName.ifBlank { workspace.serviceName },
        issue = health.issue,
        action = action,
        canRepair = canRepair,
        expectedBranch = workspace.branch,
        actualBranch = health.actualBranch,
        remote = remote,
        remoteBranchExists = remoteExists,
        remoteBranchSha = remoteSha,
        requiresRemoteReuseConfirmation = requireRemote,
        requiresSharedBranchConfirmation = requireShared,
        occupiedWorktreePaths = occupied.map { it.path.toString() },
        lockedWorktreePaths = locked.map { it.path.toString() },
        backupPath = backupPath,
        runsBootstrap = runsBootstrap,
        steps = steps,
        message = message,
        stateFingerprint = fingerprint,
    )

    private fun manual(workspace: ServiceWorkspace, health: WorkspaceGitHealth, message: String): WorkspaceRepairPreview = preview(
        workspace, health, WorkspaceRepairAction.MANUAL, false,
        fingerprint(workspace, health, Path.of(workspace.worktreePath), false, null, emptyList()),
        message,
    )

    private fun fingerprint(
        workspace: ServiceWorkspace,
        health: WorkspaceGitHealth,
        target: Path,
        localExists: Boolean,
        remoteSha: String?,
        worktrees: List<WorktreeRecord>,
    ): String = listOf(
        "path=${target.toAbsolutePath().normalize()}",
        "exists=${Files.exists(target)}",
        "issue=${health.issue}",
        "actual=${health.actualBranch.orEmpty()}",
        "expected=${workspace.branch}",
        "local=$localExists",
        "remote=${remoteSha.orEmpty()}",
        "worktrees=${worktrees.sortedBy { it.path.toString() }.joinToString(",") { "${it.path}|${it.locked}" }}",
    ).joinToString(";")

    private fun deleteRecursively(target: Path, taskDirectory: Path) {
        require(target.parent == taskDirectory.toAbsolutePath().normalize()) { "拒绝删除任务目录之外的独立克隆：$target" }
        if (!target.exists()) return
        Files.walk(target).use { entries -> entries.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists) }
    }
}

internal fun normalize(path: String): String = Path.of(path).toAbsolutePath().normalize().toString()
