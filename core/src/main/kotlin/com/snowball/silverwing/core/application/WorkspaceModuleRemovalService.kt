package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.security.MessageDigest
import java.time.Clock
import java.time.Instant
import kotlin.io.path.exists

data class WorkspaceModuleRemovalPreview(
    val workspacePath: String,
    val serviceId: String,
    val moduleId: String,
    val serviceName: String,
    val moduleName: String,
    val branch: String,
    val baseRef: String?,
    val staged: Boolean,
    val unstaged: Boolean,
    val untracked: Boolean,
    val changedFiles: List<String>,
    val commitsAheadOfBase: Int,
    val commitsBehindBase: Int,
    val unpushedCommits: Int,
    val pathMissing: Boolean,
    val fingerprint: String,
) {
    val requiresRiskConfirmation: Boolean
        get() = staged || unstaged || untracked || commitsAheadOfBase > 0 || unpushedCommits > 0
}

data class WorkspaceModuleRemovalConfirmation(
    val fingerprint: String,
    val acknowledgeDataLoss: Boolean = false,
)

data class WorkspaceModuleRemovalResult(
    val manifest: TaskManifest,
    val retainedBackupPath: String? = null,
    val cleanupError: String? = null,
)

class WorkspaceModuleRemovalService(
    private val manifests: TaskManifestRepository = ManifestStore(),
    private val agentDocuments: AgentDocuments = AgentDocumentService(),
    private val taskLock: TaskOperationLock = FileTaskOperationLock(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
    private val git: GitClient = GitClient(),
    private val clock: Clock = Clock.systemUTC(),
) {
    fun inspect(config: AppConfig, taskDirectory: Path, workspacePath: String): WorkspaceModuleRemovalPreview =
        taskLock.withLock(taskDirectory) {
            val manifest = manifests.load(taskDirectory)
            requireTaskDirectory(config, taskDirectory, manifest)
            inspectUnlocked(config, taskDirectory, manifest, workspacePath)
        }

    fun remove(
        config: AppConfig,
        taskDirectory: Path,
        preview: WorkspaceModuleRemovalPreview,
        confirmation: WorkspaceModuleRemovalConfirmation,
    ): WorkspaceModuleRemovalResult = taskLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        requireTaskDirectory(config, taskDirectory, manifest)
        require(manifest.services.size > 1) { "不能删除任务的最后一个工作区，请使用删除任务流程" }
        val workspace = manifest.services.single { workspaceKey(it) == preview.workspacePath }
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        requireTaskChild(taskDirectory, target)
        val execute = {
            val current = inspectUnlocked(config, taskDirectory, manifest, preview.workspacePath, repositoryAlreadyLocked = target.exists())
            require(current.fingerprint == preview.fingerprint && confirmation.fingerprint == preview.fingerprint) {
                "工作区状态已变化，请重新检查后确认"
            }
            require(!current.requiresRiskConfirmation || confirmation.acknowledgeDataLoss) {
                "该模块存在未提交文件、提交差异或未推送提交，必须确认永久丢失风险"
            }
            removeUnlocked(config, taskDirectory, manifest, workspace, current.pathMissing)
        }
        val repository = config.repositories.firstOrNull { it.id == workspace.repositoryId }
            ?: error("模块仓库配置不存在：${workspace.repositoryId}")
        if (!target.exists() && workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
            execute()
        } else {
            val lockRoot = when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> Path.of(repository.gitCommonDirectory).toAbsolutePath().normalize()
                WorkspaceStrategy.INDEPENDENT_CLONE -> git.commonDirectory(target).toAbsolutePath().normalize()
            }
            repositoryLock.withLock(lockRoot) { execute() }
        }
    }

    private fun inspectUnlocked(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspacePath: String,
        repositoryAlreadyLocked: Boolean = false,
    ): WorkspaceModuleRemovalPreview {
        val workspace = manifest.services.singleOrNull { workspaceKey(it) == workspacePath }
            ?: error("任务中不存在指定模块：$workspacePath")
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        requireTaskChild(taskDirectory, target)
        if (!target.exists()) return missingPreview(workspace)
        val repository = config.repositories.firstOrNull { it.id == workspace.repositoryId }
            ?: error("模块仓库配置不存在：${workspace.repositoryId}")
        val lockRoot = when (workspace.strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> Path.of(repository.gitCommonDirectory).toAbsolutePath().normalize()
            WorkspaceStrategy.INDEPENDENT_CLONE -> git.commonDirectory(target).toAbsolutePath().normalize()
        }
        val inspectRepository = {
            require(git.topLevel(target).canonicalOrNormalized() == target.canonicalOrNormalized()) {
                "路径存在但不是有效的 Git 工作区，禁止自动删除：$target"
            }
            when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> {
                    val repositoryRoot = Path.of(repository.rootPath).toAbsolutePath().normalize()
                    require(
                        git.commonDirectory(target).canonicalOrNormalized() ==
                            git.commonDirectory(repositoryRoot).canonicalOrNormalized(),
                    ) { "Worktree Git 身份与任务记录不一致" }
                    git.pruneWorktrees(repositoryRoot)
                    val record = git.worktrees(repositoryRoot).firstOrNull {
                        it.path.canonicalOrNormalized() == target.canonicalOrNormalized()
                    }
                    require(record != null) { "Git 未登记该 Worktree，禁止自动删除" }
                    require(!record.locked) { "Worktree 已锁定，请先手工 unlock" }
                }
                WorkspaceStrategy.INDEPENDENT_CLONE -> {
                    require(git.remoteUrl(target)?.trim() == workspace.originUrl?.trim()) {
                        "独立克隆 origin 与任务记录不一致"
                    }
                    IndependentCloneWorkspaceSafety.requireOwned(
                        target,
                        IndependentCloneWorkspaceSafety.ownership(
                            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                        ),
                    )
                }
            }
            val actualBranch = git.currentBranch(target) ?: error("Detached HEAD 工作区不能自动删除")
            require(actualBranch == workspace.branch) { "当前分支与任务记录不一致：$actualBranch != ${workspace.branch}" }
            require(git.status(target).operationInProgress == null) { "存在进行中的 Git 操作，完成或中止后再删除" }
            val baseRef = workspace.baseRef ?: error("任务记录缺少基础分支，无法可靠检查删除风险")
            val parsedBase = RemoteBranchRef.parse(baseRef)
            git.fetch(target, parsedBase.remote)
            require(git.refExists(target, "refs/remotes/$baseRef")) { "远程基础分支不存在：$baseRef" }
            val counts = git.run(target, "rev-list", "--left-right", "--count", "$baseRef...HEAD").stdout.trim()
                .split(Regex("\\s+"))
            require(counts.size == 2) { "无法读取当前 HEAD 相对基础分支的差异" }
            val status = git.status(target)
            val files = git.run(target, "status", "--short", "--untracked-files=all").stdout
                .lineSequence().filter(String::isNotBlank).toList()
            val fingerprint = fingerprint(
                workspace,
                git.resolve(target, "HEAD"),
                counts,
                status,
                files,
            )
            WorkspaceModuleRemovalPreview(
                workspacePath = workspaceKey(workspace),
                serviceId = workspace.groupServiceId,
                moduleId = workspace.moduleId,
                serviceName = workspace.serviceName,
                moduleName = workspace.moduleName,
                branch = actualBranch,
                baseRef = baseRef,
                staged = status.staged,
                unstaged = status.unstaged,
                untracked = status.untracked,
                changedFiles = files,
                commitsAheadOfBase = counts[1].toInt(),
                commitsBehindBase = counts[0].toInt(),
                unpushedCommits = status.unpushedCommits,
                pathMissing = false,
                fingerprint = fingerprint,
            )
        }
        return if (repositoryAlreadyLocked) inspectRepository() else repositoryLock.withLock(lockRoot) { inspectRepository() }
    }

    private fun removeUnlocked(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
        workspace: ServiceWorkspace,
        expectedPathMissing: Boolean,
    ): WorkspaceModuleRemovalResult {
        val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        val backup = if (expectedPathMissing) {
            require(!target.exists()) { "工作区状态已变化，请重新检查后确认：$target" }
            null
        } else {
            require(target.exists()) { "工作区状态已变化，请重新检查后确认：$target" }
            target.resolveSibling("${target.fileName}.silverwing-delete-${Instant.now(clock).toEpochMilli()}")
        }
        val standardRepository = if (workspace.strategy == WorkspaceStrategy.STANDARD_WORKTREE) {
            config.repositories.single { it.id == workspace.repositoryId }
                .let { Path.of(it.rootPath).toAbsolutePath().normalize() }
        } else {
            null
        }
        if (backup != null) {
            if (standardRepository != null) git.moveWorktree(standardRepository, target, backup)
            else Files.move(target, backup)
        }
        val updated = manifest.copy(
            updatedAt = SilverWingTime.format(Instant.now(clock)),
            services = manifest.services.filterNot { workspaceKey(it) == workspaceKey(workspace) },
        )
        try {
            manifests.save(taskDirectory, updated)
            agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
        } catch (error: Throwable) {
            runCatching {
                manifests.save(taskDirectory, manifest)
                agentDocuments.writeTaskDocument(taskDirectory, manifest, config.repositories.map(RepositoryConfig::toInfo))
            }.onFailure(error::addSuppressed)
            if (backup != null && backup.exists()) {
                runCatching {
                    if (standardRepository != null) git.moveWorktree(standardRepository, backup, target)
                    else Files.move(backup, target)
                }.onFailure(error::addSuppressed)
            }
            throw error
        }
        if (standardRepository != null) {
            val cleanup = runCatching {
                git.pruneWorktrees(standardRepository)
                if (backup != null) git.removeWorktree(standardRepository, backup, force = true)
                git.pruneWorktrees(standardRepository)
            }
            if (cleanup.isFailure) {
                return WorkspaceModuleRemovalResult(
                    updated,
                    backup?.toString(),
                    cleanup.exceptionOrNull()?.message ?: "Worktree 登记清理失败",
                )
            }
            return WorkspaceModuleRemovalResult(updated)
        }
        if (backup == null) return WorkspaceModuleRemovalResult(updated)
        return runCatching {
            if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
                IndependentCloneWorkspaceSafety.deleteOwned(
                    taskDirectory,
                    backup,
                    IndependentCloneWorkspaceSafety.ownership(
                        taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
                    ),
                )
            } else {
                deleteRecursively(backup)
            }
            WorkspaceModuleRemovalResult(updated)
        }.getOrElse { cleanup ->
            WorkspaceModuleRemovalResult(updated, backup.toString(), cleanup.message ?: "临时备份清理失败")
        }
    }

    private fun missingPreview(workspace: ServiceWorkspace): WorkspaceModuleRemovalPreview {
        val value = "missing|${workspaceKey(workspace)}|${workspace.branch}"
        return WorkspaceModuleRemovalPreview(
            workspacePath = workspaceKey(workspace),
            serviceId = workspace.groupServiceId,
            moduleId = workspace.moduleId,
            serviceName = workspace.serviceName,
            moduleName = workspace.moduleName,
            branch = workspace.branch,
            baseRef = workspace.baseRef,
            staged = false,
            unstaged = false,
            untracked = false,
            changedFiles = emptyList(),
            commitsAheadOfBase = 0,
            commitsBehindBase = 0,
            unpushedCommits = 0,
            pathMissing = true,
            fingerprint = sha256(value),
        )
    }

    private fun fingerprint(
        workspace: ServiceWorkspace,
        head: String,
        counts: List<String>,
        status: RepositoryStatus,
        files: List<String>,
    ): String = sha256(
        listOf(
            workspaceKey(workspace), workspace.branch, workspace.baseRef.orEmpty(), head,
            counts.joinToString(","), status.toString(), files.joinToString("\n"),
        ).joinToString("\u0000"),
    )

    private fun workspaceKey(workspace: ServiceWorkspace): String =
        Path.of(workspace.worktreePath).toAbsolutePath().normalize().toString()

    private fun requireTaskChild(taskDirectory: Path, target: Path) {
        val normalizedTask = taskDirectory.toAbsolutePath().normalize()
        require(target.parent == normalizedTask) { "拒绝删除任务目录之外的工作区：$target" }
    }

    private fun requireTaskDirectory(config: AppConfig, taskDirectory: Path, manifest: TaskManifest) {
        val root = config.taskRoot?.let(Path::of)?.toAbsolutePath()?.normalize()
            ?: error("尚未配置任务根目录")
        val task = taskDirectory.toAbsolutePath().normalize()
        require(task.parent == root && task.fileName.toString() == manifest.taskDirectoryName) {
            "任务目录与配置或任务清单不匹配：$task"
        }
    }

    private fun sha256(value: String): String = MessageDigest.getInstance("SHA-256")
        .digest(value.toByteArray()).joinToString("") { "%02x".format(it) }

    private fun deleteRecursively(path: Path) {
        if (!path.exists()) return
        val entries = Files.walk(path).use { it.sorted(Comparator.reverseOrder()).toList() }
        entries.forEach(Files::deleteIfExists)
    }
}
