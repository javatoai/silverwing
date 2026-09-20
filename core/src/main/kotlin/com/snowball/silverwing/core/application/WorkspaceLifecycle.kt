package com.snowball.silverwing.core

import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.StandardCopyOption
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.attribute.BasicFileAttributes
import kotlin.io.path.exists

interface WorkspaceLifecycle {
    fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<DeleteRisk>
    fun requireArchiveSafe(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean)
    fun removeAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): WorkspaceRemovalResult
    fun restoreAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<ServiceWorkspace>
    fun validateForMutation(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
    ): WorkspaceMutationTarget
}

data class WorkspaceRemovalResult(val retainedBackupPaths: List<String> = emptyList())

data class WorkspaceMutationTarget(
    val repository: Path,
    val worktree: Path,
)

/** Git/file-system implementation of the physical workspace lifecycle safety boundary. */
class GitWorkspaceLifecycle(
    private val git: GitClient = GitClient(),
    private val bootstrap: BootstrapService = BootstrapService(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
) : WorkspaceLifecycle {
    override fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<DeleteRisk> =
        physicalWorkspaces(manifest).also { validateTaskDirectory(config, taskDirectory, manifest) }.mapNotNull { workspace ->
            val target = runCatching { validateTarget(taskDirectory, workspace) }.getOrElse { error ->
                return@mapNotNull DeleteRisk(
                    serviceName = workspace.serviceName,
                    staged = false,
                    unstaged = false,
                    untracked = false,
                    operationInProgress = null,
                    statusCheckError = "工作区路径校验失败：${error.message ?: "路径不安全"}",
                )
            }
            if (!target.exists()) return@mapNotNull null
            runCatching { validateExistingIdentity(config = config, workspace = workspace, target = target) }
                .onFailure { error ->
                    return@mapNotNull DeleteRisk(
                        serviceName = workspace.serviceName,
                        staged = false,
                        unstaged = false,
                        untracked = false,
                        operationInProgress = null,
                        statusCheckError = "工作区 Git 身份校验失败：${error.message ?: "不是预期 Git 工作区"}",
                    )
                }
            val status = runCatching { git.status(target) }.getOrElse { error ->
                return@mapNotNull DeleteRisk(
                    workspace.serviceName,
                    staged = false,
                    unstaged = false,
                    untracked = false,
                    operationInProgress = null,
                    statusCheckError = "无法读取工作区 Git 状态：${error.message ?: "检查失败"}",
                )
            }
            val allBranchUnpushed = if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
                runCatching { git.localOnlyCommitCount(target) }.getOrElse { error ->
                    return@mapNotNull DeleteRisk(
                        workspace.serviceName,
                        status.staged,
                        status.unstaged,
                        status.untracked,
                        status.operationInProgress,
                        statusCheckError = error.message,
                    )
                }.coerceAtLeast(status.unpushedCommits)
            } else {
                0
            }
            if (!status.hasUncommittedChanges && allBranchUnpushed == 0) return@mapNotNull null
            DeleteRisk(
                workspace.serviceName,
                status.staged,
                status.unstaged,
                status.untracked,
                status.operationInProgress,
                unpushedCommits = allBranchUnpushed,
            )
        }

    override fun requireArchiveSafe(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean) {
        validateTaskDirectory(config, taskDirectory, manifest)
        validateRemovalCandidates(config, taskDirectory, manifest)
        if (force) return
        val unsafe = physicalWorkspaces(manifest).mapNotNull { workspace ->
            val target = validateTarget(taskDirectory, workspace)
            if (!target.exists()) return@mapNotNull null
            val status = git.status(target)
            val cloneHasLocalOnlyCommits = workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE &&
                git.localOnlyCommitCount(target) > 0
            workspace.takeUnless { status.safeToArchive && !cloneHasLocalOnlyCommits }
        }
        require(unsafe.isEmpty()) {
            "存在未提交或未推送的工作区，无法归档：${unsafe.joinToString { it.serviceName }}"
        }
    }

    override fun removeAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): WorkspaceRemovalResult {
        validateTaskDirectory(config, taskDirectory, manifest)
        validateRemovalCandidates(config, taskDirectory, manifest)
        val configuredStandardRepositories = config.group(manifest.groupId).services
            .map(GroupServiceConfig::repositoryId)
            .distinct()
            .map { repositoryId ->
                val configured = config.repositories.firstOrNull { it.id == repositoryId }
                requireNotNull(configured) { "仓库配置不存在：$repositoryId" }
                Path.of(configured.gitCommonDirectory).toAbsolutePath().normalize()
            }
        val lockRoots = (physicalWorkspaces(manifest).map { workspace ->
            when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE ->
                    git.commonDirectory(validateConfiguredRepository(config, workspace)).toAbsolutePath().normalize()
                WorkspaceStrategy.INDEPENDENT_CLONE -> {
                    // A clone remains independently removable when its source checkout is offline.
                    // The saved repository identity is only a logical lock shared with clone repair;
                    // an existing clone also receives its own real common-directory lock below.
                    val configured = config.repositories.firstOrNull { it.id == workspace.repositoryId }
                    requireNotNull(configured) { "仓库配置不存在：${workspace.repositoryId}" }
                    Path.of(configured.gitCommonDirectory).toAbsolutePath().normalize()
                }
            }
        } + configuredStandardRepositories).distinctBy { it.toString().lowercase() }.sortedBy { it.toString().lowercase() }
        return withRepositoryLocks(lockRoots) {
            val workspaces = physicalWorkspaces(manifest).filter { validateTarget(taskDirectory, it).exists() }
            val cloneLockRoots = workspaces
                .filter { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }
                .map { git.commonDirectory(validateTarget(taskDirectory, it)).toAbsolutePath().normalize() }
                .filterNot { candidate -> lockRoots.any { it.toString().equals(candidate.toString(), ignoreCase = true) } }
                .distinctBy { it.toString().lowercase() }
                .sortedBy { it.toString().lowercase() }
            withRepositoryLocks(cloneLockRoots) {
                // Revalidate and enumerate only after every repository lock has been acquired. A path
                // that appeared while waiting is therefore included in clean checks and staging.
                validateRemovalCandidates(config, taskDirectory, manifest)
                requireNoUntrackedRegisteredWorktrees(config, taskDirectory, manifest)
                val lockedWorkspaces = physicalWorkspaces(manifest).filter { validateTarget(taskDirectory, it).exists() }
                requireNoGitOperationInProgress(lockedWorkspaces, taskDirectory)
                if (!force) requireDeleteClean(lockedWorkspaces, taskDirectory)
                stageAndRemoveAll(config, taskDirectory, lockedWorkspaces)
            }
        }
    }

    private fun requireNoGitOperationInProgress(workspaces: List<ServiceWorkspace>, taskDirectory: Path) {
        val active = workspaces.mapNotNull { workspace ->
            git.status(validateTarget(taskDirectory, workspace)).operationInProgress?.let { operation ->
                workspace to operation
            }
        }
        require(active.isEmpty()) {
            "存在进行中的 Git 操作，不能删除工作区：" + active.joinToString { (workspace, operation) ->
                "${workspace.moduleName.ifBlank { workspace.serviceName }} ($operation)"
            }
        }
    }

    private fun requireNoUntrackedRegisteredWorktrees(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) {
        val task = taskDirectory.toAbsolutePath().normalize()
        val recordedRoots = physicalWorkspaces(manifest)
            .map { validateTarget(taskDirectory, it).toAbsolutePath().normalize() }
        val recorded = physicalWorkspaces(manifest)
            .filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }
            .map { validateTarget(taskDirectory, it).toAbsolutePath().normalize().toString().lowercase() }
            .toSet()
        val manifestRepositoryIds = physicalWorkspaces(manifest).map(ServiceWorkspace::repositoryId)
        val configuredRepositoryIds = config.group(manifest.groupId).services
            .map(GroupServiceConfig::repositoryId)
        val repositoryIds = (manifestRepositoryIds + configuredRepositoryIds).distinct()
        val unknown = repositoryIds.flatMap { repositoryId ->
            val configured = config.repositories.first { it.id == repositoryId }
            val repository = Path.of(configured.rootPath).toAbsolutePath().normalize()
            if (!repository.exists()) emptyList()
            else git.worktrees(repository).map(WorktreeRecord::path)
        }.map(Path::toAbsolutePath).map(Path::normalize).filter { candidate ->
            candidate.startsWith(task) && candidate.toString().lowercase() !in recorded
        }.distinctBy { it.toString().lowercase() }
        val cloneRegistered = physicalWorkspaces(manifest)
            .filter { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }
            .map { validateTarget(taskDirectory, it) }
            .filter(Path::exists)
            .flatMap { clone ->
                val cloneRoot = clone.toAbsolutePath().normalize()
                git.worktrees(clone).map(WorktreeRecord::path)
                    .filter { it.toAbsolutePath().normalize() != cloneRoot }
            }
            .map(Path::toAbsolutePath)
            .map(Path::normalize)
            .distinctBy { it.toString().lowercase() }
        val unknownGitCheckouts = findUnrecordedGitRoots(task, recordedRoots)
        val unsafe = (unknown + cloneRegistered + unknownGitCheckouts).distinctBy { it.toString().lowercase() }
        require(unsafe.isEmpty()) {
            "任务目录中存在未记录的 Linked Worktree，请先人工处理后再删除任务：${unsafe.joinToString()}"
        }
    }

    private fun findUnrecordedGitRoots(task: Path, recordedRoots: List<Path>): List<Path> {
        val found = mutableListOf<Path>()
        Files.walkFileTree(task, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(dir: Path, attrs: BasicFileAttributes): FileVisitResult {
                val candidate = dir.toAbsolutePath().normalize()
                // Git metadata may contain complete-looking repositories (notably
                // .git/modules/<submodule>). It is metadata for the enclosing checkout,
                // never an independently removable workspace, so do not descend into it.
                if (candidate.fileName?.toString() == ".git") return FileVisitResult.SKIP_SUBTREE
                if (candidate !in recordedRoots && isUnrecordedGitRoot(candidate)) {
                    found.add(candidate)
                    return FileVisitResult.SKIP_SUBTREE
                }
                return FileVisitResult.CONTINUE
            }
        })
        return found
    }

    private fun isUnrecordedGitRoot(candidate: Path): Boolean {
        val dotGit = candidate.resolve(".git")
        if (Files.isDirectory(dotGit)) return true
        if (Files.isRegularFile(dotGit)) return isLinkedWorktree(candidate)
        return Files.isRegularFile(candidate.resolve("HEAD")) &&
            Files.isDirectory(candidate.resolve("objects")) &&
            Files.isDirectory(candidate.resolve("refs"))
    }

    private fun isLinkedWorktree(candidate: Path): Boolean = runCatching {
        val gitDirectory = Path.of(git.run(candidate, "rev-parse", "--absolute-git-dir").stdout.trim())
            .toAbsolutePath().normalize()
        git.commonDirectory(candidate).canonicalOrNormalized() != gitDirectory.canonicalOrNormalized()
    }.getOrDefault(true)

    private fun requireDeleteClean(workspaces: List<ServiceWorkspace>, taskDirectory: Path) {
        val unsafe = workspaces.filter { workspace ->
            val target = validateTarget(taskDirectory, workspace)
            val status = git.status(target)
            status.hasUncommittedChanges || (
                workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE && git.localOnlyCommitCount(target) > 0
            )
        }
        require(unsafe.isEmpty()) {
            "工作区状态在删除确认后发生变化，已停止删除：${unsafe.joinToString { it.moduleName.ifBlank { it.serviceName } }}"
        }
    }

    private data class StagedWorkspaceRemoval(
        val workspace: ServiceWorkspace,
        val original: Path,
        val backup: Path,
        val repository: Path? = null,
        val ownership: String? = null,
    )

    private fun stageAndRemoveAll(
        config: AppConfig,
        taskDirectory: Path,
        workspaces: List<ServiceWorkspace>,
    ): WorkspaceRemovalResult {
        val transactionRoot = Files.createTempDirectory(
            taskDirectory.toAbsolutePath().normalize().parent,
            ".${taskDirectory.fileName}.silverwing-delete-",
        )
        val metadataBackup = transactionRoot.resolve("task-metadata")
        val metadataRecovery = transactionRoot.resolve("task-metadata-recovery")
        val metadataOwnership = java.util.UUID.randomUUID().toString()
        val metadataOwnerMarker = transactionRoot.resolve("metadata-owner")
        Files.writeString(metadataOwnerMarker, metadataOwnership)
        var metadataRecoveryComplete = false
        var metadataDeleteStarted = false
        val staged = mutableListOf<StagedWorkspaceRemoval>()
        try {
            workspaces.forEachIndexed { index, workspace ->
                val original = validateTarget(taskDirectory, workspace)
                val backup = transactionRoot.resolve("$index-${original.fileName}")
                when (workspace.strategy) {
                    WorkspaceStrategy.STANDARD_WORKTREE -> {
                        val repository = validateConfiguredRepository(config, workspace)
                        git.moveWorktree(repository, original, backup)
                        staged += StagedWorkspaceRemoval(workspace, original, backup, repository = repository)
                    }
                    WorkspaceStrategy.INDEPENDENT_CLONE -> {
                        val ownership = IndependentCloneWorkspaceSafety.ownership(
                            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                        )
                        IndependentCloneWorkspaceSafety.requireOwned(original, ownership)
                        Files.move(original, backup)
                        staged += StagedWorkspaceRemoval(workspace, original, backup, ownership = ownership)
                    }
                }
            }
            // Task metadata is part of the same transaction as its workspaces. Moving it is
            // reversible; recursively deleting it before the workspace transaction commits is not.
            Files.move(taskDirectory, metadataBackup)
            copyRecursively(metadataBackup, metadataRecovery)
            metadataRecoveryComplete = true
            // Prove metadata is deletable while all workspaces are still staged and recoverable.
            metadataDeleteStarted = true
            deleteRecursively(metadataBackup)
        } catch (error: Throwable) {
            if (!taskDirectory.exists()) {
                val recovery = if (metadataRecoveryComplete && metadataDeleteStarted) {
                    metadataRecovery.takeIf(Path::exists)
                } else {
                    metadataBackup.takeIf(Path::exists)
                }
                if (recovery != null) {
                    runCatching { Files.move(recovery, taskDirectory) }.onFailure(error::addSuppressed)
                }
            }
            staged.asReversed().forEach { item ->
                runCatching {
                    when (item.workspace.strategy) {
                        WorkspaceStrategy.STANDARD_WORKTREE -> git.moveWorktree(requireNotNull(item.repository), item.backup, item.original)
                        WorkspaceStrategy.INDEPENDENT_CLONE -> Files.move(item.backup, item.original)
                    }
                }.onFailure(error::addSuppressed)
            }
            runCatching { Files.deleteIfExists(metadataOwnerMarker) }.onFailure(error::addSuppressed)
            runCatching { Files.deleteIfExists(transactionRoot) }.onFailure(error::addSuppressed)
            throw error
        }
        val retained = mutableListOf<String>()
        staged.forEach { item ->
            runCatching {
                when (item.workspace.strategy) {
                    WorkspaceStrategy.STANDARD_WORKTREE -> {
                        git.removeWorktree(requireNotNull(item.repository), item.backup, force = true)
                        git.run(item.repository, "worktree", "prune", check = false)
                    }
                    WorkspaceStrategy.INDEPENDENT_CLONE -> IndependentCloneWorkspaceSafety.deleteOwned(
                        transactionRoot, item.backup, requireNotNull(item.ownership),
                    )
                }
            }.onFailure { retained += item.backup.toString() }
        }
        runCatching {
            require(metadataOwnerMarker.exists() && Files.readString(metadataOwnerMarker) == metadataOwnership) {
                "任务元数据删除事务所有权已变化，已保留备份：$metadataBackup"
            }
            deleteRecursively(metadataRecovery)
        }.onFailure { retained += metadataRecovery.toString() }
        if (retained.isEmpty()) {
            Files.deleteIfExists(metadataOwnerMarker)
            Files.deleteIfExists(transactionRoot)
        }
        return WorkspaceRemovalResult(retained)
    }

    private fun copyRecursively(source: Path, target: Path) {
        Files.walk(source).use { entries ->
            entries.forEach { entry ->
                val destination = target.resolve(source.relativize(entry))
                if (Files.isSymbolicLink(entry)) {
                    Files.createSymbolicLink(destination, Files.readSymbolicLink(entry))
                } else if (Files.isDirectory(entry, NOFOLLOW_LINKS)) {
                    Files.createDirectories(destination)
                } else {
                    Files.copy(entry, destination, StandardCopyOption.REPLACE_EXISTING)
                }
            }
        }
    }

    private fun <T> withRepositoryLocks(roots: List<Path>, index: Int = 0, block: () -> T): T =
        if (index >= roots.size) block() else repositoryLock.withLock(roots[index]) {
            withRepositoryLocks(roots, index + 1, block)
        }

    override fun restoreAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<ServiceWorkspace> {
        validateTaskDirectory(config, taskDirectory, manifest)
        val restoredByPath = linkedMapOf<String, ServiceWorkspace>()
        val restoredCloneOwnership = mutableMapOf<String, String>()
        try {
            physicalWorkspaces(manifest).forEach { workspace ->
                val target = validateTarget(taskDirectory, workspace)
                require(!target.exists()) { "恢复目标已存在：$target" }
                val restored = when (workspace.strategy) {
                    WorkspaceStrategy.STANDARD_WORKTREE -> {
                        val repository = validateConfiguredRepository(config, workspace)
                        git.addExistingWorktree(
                            repository,
                            target,
                            workspace.branch,
                            force = workspace.forceWorktreeAttach,
                        )
                        restoredByPath[target.toString()] = workspace.copy(health = WorkspaceHealth.READY, warnings = emptyList())
                        val service = config.group(manifest.groupId).services.firstOrNull { it.id == workspace.groupServiceId }
                        val initialized = service?.let { bootstrap.initialize(repository, target, it.bootstrap) }
                        workspace.copy(
                            health = if (initialized == null || initialized.succeeded) {
                                WorkspaceHealth.READY
                            } else {
                                WorkspaceHealth.READY_WITH_WARNINGS
                            },
                            warnings = initialized?.warnings.orEmpty(),
                        )
                    }
                    WorkspaceStrategy.INDEPENDENT_CLONE -> {
                        validateCloneConfig(config, workspace)
                        val ownership = IndependentCloneWorkspaceSafety.ownership(
                            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                        )
                        IndependentCloneWorkspaceSafety.cloneIntoPlace(taskDirectory, target, ownership) { staging ->
                            git.cloneRepository(workspace.originUrl!!, staging, workspace.branch)
                        }
                        restoredCloneOwnership[target.toString()] = ownership
                        val service = config.group(manifest.groupId).services.firstOrNull { it.id == workspace.groupServiceId }
                        val source = config.repositories.firstOrNull { it.id == workspace.repositoryId }
                            ?.rootPath?.let(Path::of)
                        val initialized = if (service != null && source != null) {
                            bootstrap.initialize(source, target, service.bootstrap)
                        } else null
                        workspace.copy(
                            health = if (initialized == null || initialized.succeeded) WorkspaceHealth.READY else WorkspaceHealth.READY_WITH_WARNINGS,
                            warnings = initialized?.warnings.orEmpty(),
                        )
                    }
                }
                restoredByPath[target.toString()] = restored
            }
        } catch (error: Throwable) {
            val cleanupFailures = mutableListOf<Throwable>()
            restoredByPath.values.toList().asReversed().forEach { restored ->
                runCatching {
                    val ownership = restoredCloneOwnership[Path.of(restored.worktreePath).toAbsolutePath().normalize().toString()]
                    if (ownership != null) {
                        IndependentCloneWorkspaceSafety.deleteOwned(taskDirectory, Path.of(restored.worktreePath), ownership)
                    } else {
                        removeOne(config, taskDirectory, restored, force = true)
                    }
                }
                    .onFailure(cleanupFailures::add)
            }
            if (cleanupFailures.isNotEmpty()) {
                throw IllegalStateException(
                    "工作区恢复失败，且自动回滚未完成；已保留现场供人工检查：$taskDirectory",
                    error,
                ).apply { cleanupFailures.forEach(::addSuppressed) }
            }
            throw error
        }
        return manifest.services.map { workspace ->
            val restored = restoredByPath.getValue(Path.of(workspace.worktreePath).toAbsolutePath().normalize().toString())
            workspace.copy(health = restored.health, warnings = restored.warnings)
        }
    }


    override fun validateForMutation(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
    ): WorkspaceMutationTarget {
        validateTaskDirectory(config, taskDirectory, manifest)
        val target = validateTarget(taskDirectory, workspace)
        require(target.exists()) { "任务工作区不存在：$target" }
        val actualBranch = git.currentBranch(target)
        require(actualBranch == workspace.branch) {
            "工作区当前分支与任务记录不一致：期望 ${workspace.branch}，实际 ${actualBranch ?: "detached"}"
        }
        return when (workspace.strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> {
                val repository = validateConfiguredRepository(config, workspace)
                validateExistingIdentity(config, workspace, target)
                WorkspaceMutationTarget(repository, target)
            }
            WorkspaceStrategy.INDEPENDENT_CLONE -> {
                validateCloneConfig(config, workspace)
                validateExistingIdentity(config, workspace, target)
                WorkspaceMutationTarget(target, target)
            }
        }
    }

    private fun removeOne(config: AppConfig, taskDirectory: Path, workspace: ServiceWorkspace, force: Boolean) {
        val target = validateTarget(taskDirectory, workspace)
        if (!target.exists()) return
        when (workspace.strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> {
                val repository = validateConfiguredRepository(config, workspace)
                validateExistingIdentity(config, workspace, target)
                git.removeWorktree(repository, target, force)
                git.run(repository, "worktree", "prune", check = false)
            }
            WorkspaceStrategy.INDEPENDENT_CLONE -> {
                validateCloneConfig(config, workspace)
                validateExistingIdentity(config, workspace, target)
                IndependentCloneWorkspaceSafety.deleteOwned(
                    taskDirectory,
                    target,
                    IndependentCloneWorkspaceSafety.ownership(
                        taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                    ),
                )
            }
        }
    }

    /** Validate every candidate before deleting the first physical workspace. */
    private fun validateRemovalCandidates(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) {
        physicalWorkspaces(manifest).forEach { workspace ->
            val target = validateTarget(taskDirectory, workspace)
            if (!target.exists()) return@forEach
            when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> validateConfiguredRepository(config, workspace)
                WorkspaceStrategy.INDEPENDENT_CLONE -> {
                    validateCloneConfig(config, workspace)
                    IndependentCloneWorkspaceSafety.requireOwned(
                        target,
                        IndependentCloneWorkspaceSafety.ownership(
                            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                        ),
                    )
                }
            }
            validateExistingIdentity(config, workspace, target)
        }
    }

    private fun validateTarget(taskDirectory: Path, workspace: ServiceWorkspace): Path {
        val task = taskDirectory.toAbsolutePath().normalize()
        require(task.parent != null && task != task.root) { "拒绝对文件系统根目录执行任务操作" }
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        require(target.parent == task) { "工作区路径不在任务目录的直接子级，已拒绝操作：$target" }
        return target
    }

    private fun validateConfiguredRepository(config: AppConfig, workspace: ServiceWorkspace): Path {
        val configured = config.repositories.firstOrNull { it.id == workspace.repositoryId }
        requireNotNull(configured) { "仓库配置不存在：${workspace.repositoryId}" }
        val repository = Path.of(configured.rootPath).toAbsolutePath().normalize()
        require(
            git.commonDirectory(repository).canonicalOrNormalized() ==
                Path.of(configured.gitCommonDirectory).canonicalOrNormalized(),
        ) {
            "主仓库 Git 身份与配置不匹配"
        }
        return repository
    }

    private fun validateTaskDirectory(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) {
        val root = config.taskRoot?.let(Path::of)?.toAbsolutePath()?.normalize()
            ?: error("尚未配置任务根目录")
        val task = taskDirectory.toAbsolutePath().normalize()
        require(task.parent == root && task.fileName.toString() == manifest.taskDirectoryName) {
            "任务目录与配置或任务清单不匹配：$task"
        }
    }

    private fun validateCloneConfig(config: AppConfig, workspace: ServiceWorkspace) {
        require(config.repositories.any { it.id == workspace.repositoryId }) {
            "仓库配置不存在：${workspace.repositoryId}"
        }
        require(!workspace.originUrl.isNullOrBlank()) { "独立克隆缺少任务记录的 origin URL" }
    }

    private fun validateExistingIdentity(config: AppConfig?, workspace: ServiceWorkspace, target: Path) {
        require(Files.isDirectory(target.resolve(".git")) || workspace.strategy == WorkspaceStrategy.STANDARD_WORKTREE) {
            "独立克隆不是常规 Git checkout：$target"
        }
        // Git may canonicalize a worktree path (for example through macOS's
        // /var -> /private/var link). Compare resolved paths so an equivalent
        // spelling cannot make a valid checkout fail identity validation.
        require(git.topLevel(target).canonicalOrNormalized() == target.canonicalOrNormalized()) {
            "Git 顶层目录不匹配：$target"
        }
        if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
            val actualOrigin = git.remoteUrl(target)?.trim().orEmpty()
            require(actualOrigin == workspace.originUrl?.trim().orEmpty()) { "独立克隆 origin 与任务记录不匹配" }
        } else if (config != null) {
            val expectedCommon = config.repositories.firstOrNull { it.id == workspace.repositoryId }
                ?.let { Path.of(it.gitCommonDirectory).toAbsolutePath().normalize() }
                ?: git.commonDirectory(Path.of(workspace.repositoryPath)).toAbsolutePath().normalize()
            // Git resolves linked worktrees through the filesystem, while a persisted
            // repository path may have been entered through a symlink (notably /var on
            // macOS). Compare the canonical paths on both sides so the identity check
            // validates the repository rather than the spelling of its parent path.
            require(git.commonDirectory(target).canonicalOrNormalized() == expectedCommon.canonicalOrNormalized()) {
                "Linked Worktree 不属于配置的主仓库"
            }
        }
    }

    private fun physicalWorkspaces(manifest: TaskManifest): List<ServiceWorkspace> =
        manifest.services.distinctBy { Path.of(it.worktreePath).toAbsolutePath().normalize().toString() }

    private fun deleteRecursively(path: Path) {
        Files.walkFileTree(path, object : SimpleFileVisitor<Path>() {
            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                deleteWritable(file)
                return FileVisitResult.CONTINUE
            }

            override fun postVisitDirectory(dir: Path, exc: java.io.IOException?): FileVisitResult {
                if (exc != null) throw exc
                deleteWritable(dir)
                return FileVisitResult.CONTINUE
            }
        })
    }

    private fun deleteWritable(path: Path) {
        try {
            Files.deleteIfExists(path)
        } catch (denied: java.nio.file.AccessDeniedException) {
            // Git object files copied/cloned on Windows may retain DOS read-only.
            // The target has already passed task containment and Git identity checks.
            path.toFile().setWritable(true)
            try {
                Files.deleteIfExists(path)
            } catch (retry: Throwable) {
                retry.addSuppressed(denied)
                throw retry
            }
        }
    }
}
