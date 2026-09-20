package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path

data class WorkspaceProvisionRequest(
    val taskDirectory: Path,
    val repository: RepositoryConfig,
    val service: GroupServiceConfig,
    val requestedFeatureBranch: String? = null,
    val confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
    val moduleBranches: Map<String, String> = emptyMap(),
    val moduleSources: Map<String, TaskModuleSource> = emptyMap(),
    val moduleDisplayNames: Map<String, String> = emptyMap(),
)

data class BranchReuseKey(
    val repositoryId: String,
    val branch: String,
    val stateFingerprint: String? = null,
)

private fun branchReuseFingerprint(
    localSha: String?,
    remoteRefs: List<Pair<String, String>>,
    worktrees: List<WorktreeRecord>,
): String = listOf(
    "local=${localSha.orEmpty()}",
    "remotes=${remoteRefs.sortedBy(Pair<String, String>::first).joinToString(",") { "${it.first}@${it.second}" }}",
    "worktrees=${worktrees.sortedBy { it.path.toString() }.joinToString(",") { "${it.path}|${it.locked}" }}",
).joinToString(";")

data class BranchReuseConflict(
    val key: BranchReuseKey,
    val serviceId: String,
    val serviceName: String,
    val moduleName: String,
    val localExists: Boolean,
    val remoteRefs: List<String>,
    val occupiedWorktreePaths: List<String>,
    val lockedWorktreePaths: List<String> = emptyList(),
    val strategy: WorkspaceStrategy = WorkspaceStrategy.STANDARD_WORKTREE,
    val reusesRemoteCloneTarget: Boolean = false,
    private val forceAttachRequired: Boolean = occupiedWorktreePaths.isNotEmpty(),
) {
    val requiresForceAttach: Boolean get() = forceAttachRequired
}

interface WorkspaceProvisioner {
    val strategy: WorkspaceStrategy
    fun provision(request: WorkspaceProvisionRequest): List<ServiceWorkspace>
    fun rollback(request: WorkspaceProvisionRequest, workspaces: List<ServiceWorkspace>) = Unit
}

object WorkspaceLayout {
    private fun serviceDirectoryBase(service: GroupServiceConfig): String = TaskNaming.directoryName(service.displayName)

    /** 0.8 paths do not depend on the current module count. */
    fun moduleDirectoryName(service: GroupServiceConfig, module: ServiceModuleConfig): String =
        "${serviceDirectoryBase(service)}-${StandardWorktreeModuleNaming.directorySegment(StandardWorktreeModuleNaming.effectiveName(module))}"
}

private fun defaultTargetBranch(
    requestedBranch: String,
    modules: List<ServiceModuleConfig>,
    module: ServiceModuleConfig,
): String = if (modules.size == 1) requestedBranch else "$requestedBranch-${StandardWorktreeModuleNaming.effectiveName(module)}"

private fun resolvedModuleTargets(request: WorkspaceProvisionRequest): Map<String, String> =
    request.service.modules.associate { module ->
        val explicit = request.moduleBranches[module.id]
        val target = when {
            explicit != null -> explicit.trim()
            module.strategy == WorkspaceStrategy.STANDARD_WORKTREE -> defaultTargetBranch(
                request.requestedFeatureBranch?.trim().orEmpty(),
                request.service.modules,
                module,
            )
            else -> ""
        }
        module.id to target
    }

private fun resolvedModuleDisplayNames(request: WorkspaceProvisionRequest): Map<String, String> =
    request.service.modules.associate { module ->
        module.id to (
            request.moduleDisplayNames[module.id]
                ?: ModuleDisplayNaming.resolve(
                    module.name,
                    request.service.displayName,
                    module.baseRef,
                    request.service.modules.size,
                )
            )
    }

private fun confirmedReuse(request: WorkspaceProvisionRequest, key: BranchReuseKey): Boolean =
    request.confirmedBranchReuseKeys.any { confirmed ->
        confirmed.repositoryId == key.repositoryId && confirmed.branch == key.branch &&
            (confirmed.stateFingerprint == null || confirmed.stateFingerprint == key.stateFingerprint)
    }

private fun sourceRemoteUrl(git: GitClient, repository: RepositoryConfig, remote: String): String {
    val root = Path.of(repository.rootPath).toAbsolutePath().normalize()
    return git.remoteUrl(root, remote)?.trim()?.takeIf(String::isNotBlank)
        ?: repository.originUrl?.trim()?.takeIf { remote == "origin" && it.isNotBlank() }
        ?: error("仓库未配置可用于独立克隆的远程：$remote")
}

private fun remoteHeadSha(git: GitClient, repository: RepositoryConfig, remote: String, branch: String): String? {
    val sourceUrl = sourceRemoteUrl(git, repository, remote)
    val root = Path.of(repository.rootPath).toAbsolutePath().normalize()
    val result = git.run(root, "ls-remote", "--exit-code", "--heads", sourceUrl, "refs/heads/$branch", check = false)
    if (result.exitCode !in setOf(0, 2)) throw GitException("检查远程分支失败：$remote/$branch", result)
    if (result.exitCode != 0) return null
    return result.stdout.lineSequence().firstOrNull { it.isNotBlank() }?.substringBefore('\t')?.trim()?.ifBlank { null }
}

private fun remoteHeadExists(git: GitClient, repository: RepositoryConfig, remote: String, branch: String): Boolean =
    remoteHeadSha(git, repository, remote, branch) != null

class WorkspaceBranchReuseInspector(
    private val git: GitClient = GitClient(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
) {
    fun inspect(
        repository: RepositoryConfig,
        service: GroupServiceConfig,
        requestedFeatureBranch: String,
        moduleBranches: Map<String, String> = emptyMap(),
    ): List<BranchReuseConflict> {
        val repositoryPath = Path.of(repository.rootPath).toAbsolutePath().normalize()
        val commonDirectory = git.commonDirectory(repositoryPath).toAbsolutePath().normalize()
        return repositoryLock.withLock(commonDirectory) {
            require(git.topLevel(repositoryPath).canonicalOrNormalized() == repositoryPath.canonicalOrNormalized()) {
                "配置的仓库路径已不再是 Git 顶层目录：$repositoryPath"
            }
            require(commonDirectory.canonicalOrNormalized() == Path.of(repository.gitCommonDirectory).canonicalOrNormalized()) {
                "配置的仓库 Git 身份已变化：$repositoryPath"
            }
            git.pruneWorktrees(repositoryPath)
            StandardWorktreeModuleNaming.requireValid(service.modules)
            val targets = resolvedModuleTargets(
                WorkspaceProvisionRequest(
                    taskDirectory = repositoryPath,
                    repository = repository,
                    service = service,
                    requestedFeatureBranch = requestedFeatureBranch,
                    moduleBranches = moduleBranches,
                ),
            )
            service.modules.mapNotNull { module ->
                val target = targets.getValue(module.id)
                if (module.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
                    inspectClone(repository, service, module, target)
                } else {
                    inspectWorktree(repository, service, module, target, repositoryPath)
                }
            }
        }
    }

    private fun inspectClone(
        repository: RepositoryConfig,
        service: GroupServiceConfig,
        module: ServiceModuleConfig,
        target: String,
    ): BranchReuseConflict? {
        val base = RemoteBranchRef.parse(module.baseRef)
        require(remoteHeadExists(git, repository, base.remote, base.branch)) { "远程基础分支不存在：${base.remote}/${base.branch}" }
        if (target.isBlank()) return null
        val targetSha = remoteHeadSha(git, repository, base.remote, target) ?: return null
        return BranchReuseConflict(
            key = BranchReuseKey(repository.id, target, "clone-remote=${base.remote}/$target@$targetSha"),
            serviceId = service.id,
            serviceName = service.displayName,
            moduleName = StandardWorktreeModuleNaming.effectiveName(module),
            localExists = false,
            remoteRefs = listOf("${base.remote}/$target"),
            occupiedWorktreePaths = emptyList(),
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            reusesRemoteCloneTarget = true,
        )
    }

    private fun inspectWorktree(
        repository: RepositoryConfig,
        service: GroupServiceConfig,
        module: ServiceModuleConfig,
        branch: String,
        repositoryPath: Path,
    ): BranchReuseConflict? {
        require(branch.isNotBlank()) { "Worktree 模块目标分支不能为空：${module.name}" }
        git.fetch(repositoryPath, module.baseRemote)
        val featureRemotes = branchReuseRemotes(module)
        featureRemotes.filter { it != module.baseRemote }.forEach { git.fetch(repositoryPath, it) }
        val localSha = git.run(repositoryPath, "rev-parse", "--verify", "refs/heads/$branch", check = false)
            .takeIf(CommandResult::succeeded)?.stdout?.trim()?.ifBlank { null }
        val localExists = localSha != null
        val remoteRefs = featureRemotes.mapNotNull { remote ->
            val name = "$remote/$branch"
            git.run(repositoryPath, "rev-parse", "--verify", "refs/remotes/$name", check = false)
                .takeIf(CommandResult::succeeded)?.stdout?.trim()?.ifBlank { null }?.let { name to it }
        }
        if (!localExists && remoteRefs.isEmpty()) return null
        val occupied = if (localExists) git.worktrees(repositoryPath).filter { it.branch == branch } else emptyList()
        return BranchReuseConflict(
            key = BranchReuseKey(repository.id, branch, branchReuseFingerprint(localSha, remoteRefs, occupied)),
            serviceId = service.id,
            serviceName = service.displayName,
            moduleName = ModuleDisplayNaming.resolve(module.name, service.displayName, module.baseRef, service.modules.size),
            localExists = localExists,
            remoteRefs = remoteRefs.map(Pair<String, String>::first),
            occupiedWorktreePaths = occupied.map { it.path.toString() },
            lockedWorktreePaths = occupied.filter(WorktreeRecord::locked).map { it.path.toString() },
        )
    }
}

private data class CreatedStandardWorktree(
    val target: Path,
    val branch: String,
    val branchCreatedByTask: Boolean,
)

class StandardWorktreeProvisioner(
    private val git: GitClient = GitClient(),
    private val bootstrap: BootstrapService = BootstrapService(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(),
) : WorkspaceProvisioner {
    override val strategy = WorkspaceStrategy.STANDARD_WORKTREE

    override fun provision(request: WorkspaceProvisionRequest): List<ServiceWorkspace> {
        require(request.service.modules.all { it.strategy == strategy }) { "请求包含非 Worktree 模块" }
        val repositoryPath = Path.of(request.repository.rootPath).toAbsolutePath().normalize()
        val commonDirectory = git.commonDirectory(repositoryPath).toAbsolutePath().normalize()
        return repositoryLock.withLock(commonDirectory) { provisionLocked(request, repositoryPath) }
    }

    private fun provisionLocked(request: WorkspaceProvisionRequest, repositoryPath: Path): List<ServiceWorkspace> {
        require(git.topLevel(repositoryPath).canonicalOrNormalized() == repositoryPath.canonicalOrNormalized()) {
            "配置的仓库路径已不再是 Git 顶层目录：$repositoryPath"
        }
        require(
            git.commonDirectory(repositoryPath).canonicalOrNormalized() ==
                Path.of(request.repository.gitCommonDirectory).canonicalOrNormalized(),
        ) {
            "仓库 Git 身份已变化，请先手动刷新并检查配置"
        }
        git.pruneWorktrees(repositoryPath)
        request.taskDirectory.toFile().mkdirs()
        val requestedBranch = request.requestedFeatureBranch?.trim().orEmpty()
        val branches = request.service.modules.associate { module ->
            val branch = request.moduleBranches[module.id]?.trim()
                ?: defaultTargetBranch(requestedBranch, request.service.modules, module)
            require(branch.isNotBlank()) { "Worktree 模块目标分支不能为空：${module.name}" }
            module.id to branch
        }
        require(branches.values.map(String::lowercase).distinct().size == branches.size) {
            "同一服务的 Worktree 模块目标分支不能重复"
        }
        val created = mutableListOf<CreatedStandardWorktree>()
        try {
            return request.service.modules.map { module ->
                val branch = branches.getValue(module.id)
                val target = request.taskDirectory.resolve(WorkspaceLayout.moduleDirectoryName(request.service, module)).toAbsolutePath().normalize()
                require(target.parent == request.taskDirectory.toAbsolutePath().normalize()) { "Worktree 必须位于任务目录的直接子级" }
                git.fetch(repositoryPath, module.baseRemote)
                val baseBranch = TaskBranchNaming.normalizeBaseRef(module)
                val remoteBaseRef = "${module.baseRemote}/$baseBranch"
                require(git.refExists(repositoryPath, "refs/remotes/$remoteBaseRef")) { "远程基础分支不存在：$remoteBaseRef" }
                require(git.run(repositoryPath, "check-ref-format", "--branch", branch, check = false).succeeded) { "分支名不合法：$branch" }
                val featureRemotes = branchReuseRemotes(module)
                featureRemotes.filter { it != module.baseRemote }.forEach { git.fetch(repositoryPath, it) }
                val localSha = git.run(repositoryPath, "rev-parse", "--verify", "refs/heads/$branch", check = false)
                    .takeIf(CommandResult::succeeded)?.stdout?.trim()?.ifBlank { null }
                val localExists = localSha != null
                val remoteBranches = featureRemotes.mapNotNull { remote ->
                    val name = "$remote/$branch"
                    git.run(repositoryPath, "rev-parse", "--verify", "refs/remotes/$name", check = false)
                        .takeIf(CommandResult::succeeded)?.stdout?.trim()?.ifBlank { null }?.let { name to it }
                }
                val matching = if (localExists) git.worktrees(repositoryPath).filter { it.branch == branch && it.path != target } else emptyList()
                val key = BranchReuseKey(
                    request.repository.id,
                    branch,
                    branchReuseFingerprint(localSha, remoteBranches, matching),
                )
                require(!localExists && remoteBranches.isEmpty() || confirmedReuse(request, key)) {
                    "分支复用状态已变化或尚未确认，请重新预检：$branch"
                }
                require(matching.none(WorktreeRecord::locked)) {
                    "分支被锁定 Worktree 占用，不能强制复用：${matching.filter(WorktreeRecord::locked).joinToString { it.path.toString() }}"
                }
                val forceAttach = matching.isNotEmpty()
                when {
                    localExists -> git.addExistingWorktree(repositoryPath, target, branch, forceAttach)
                    remoteBranches.isNotEmpty() -> git.addTrackedRemoteWorktree(repositoryPath, target, branch, remoteBranches.first().first.substringBefore('/'))
                    else -> git.addWorktree(repositoryPath, target, branch, remoteBaseRef, module.baseRemote)
                }
                val branchCreatedByTask = !localExists
                created += CreatedStandardWorktree(target, branch, branchCreatedByTask)
                val initialization = bootstrap.initialize(repositoryPath, target, request.service.bootstrap)
                ServiceWorkspace(
                    repositoryId = request.repository.id,
                    serviceName = request.service.displayName,
                    repositoryPath = request.repository.rootPath,
                    worktreePath = target.toString(),
                    developmentTool = request.service.developmentTool,
                    branch = branch,
                    health = if (initialization.succeeded) WorkspaceHealth.READY else WorkspaceHealth.READY_WITH_WARNINGS,
                    warnings = initialization.warnings,
                    groupServiceId = request.service.id,
                    moduleId = module.id,
                    moduleName = request.moduleDisplayNames[module.id]
                        ?: ModuleDisplayNaming.resolve(module.name, request.service.displayName, module.baseRef, request.service.modules.size),
                    strategy = strategy,
                    moduleSource = request.moduleSources[module.id] ?: TaskModuleSource.CONFIGURED,
                    originUrl = request.repository.originUrl,
                    baseRef = remoteBaseRef,
                    targetBranch = branch,
                    tagEnabled = module.tagEnabled,
                    tagMode = module.tagMode,
                    tagTargetRef = module.tagTargetRef,
                    tagMessagePrefix = module.tagMessagePrefix,
                    branchCreatedByTask = branchCreatedByTask,
                    forceWorktreeAttach = forceAttach,
                    pushRemote = module.baseRemote,
                )
            }
        } catch (error: Throwable) {
            created.asReversed().forEach { item ->
                runCatching { git.removeWorktree(repositoryPath, item.target, true) }.onFailure(error::addSuppressed)
                if (item.branchCreatedByTask) runCatching { git.run(repositoryPath, "branch", "-D", item.branch, check = false) }.onFailure(error::addSuppressed)
            }
            runCatching { git.run(repositoryPath, "worktree", "prune", check = false) }
            throw error
        }
    }

    override fun rollback(request: WorkspaceProvisionRequest, workspaces: List<ServiceWorkspace>) {
        val repository = Path.of(request.repository.rootPath).toAbsolutePath().normalize()
        val commonDirectory = git.commonDirectory(repository).toAbsolutePath().normalize()
        repositoryLock.withLock(commonDirectory) {
            val failures = mutableListOf<Throwable>()
            workspaces.distinctBy(ServiceWorkspace::worktreePath).asReversed().forEach { workspace ->
                val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
                require(target.parent == request.taskDirectory.toAbsolutePath().normalize()) { "拒绝回滚任务目录之外的 Worktree：$target" }
                runCatching {
                    git.removeWorktree(repository, target, true)
                    if (workspace.branchCreatedByTask) git.run(repository, "branch", "-D", workspace.branch, check = false)
                }.onFailure(failures::add)
            }
            runCatching { git.run(repository, "worktree", "prune", check = false) }.onFailure(failures::add)
            if (failures.isNotEmpty()) throw IllegalStateException("Worktree 回滚未完整完成：${request.service.displayName}").apply { failures.forEach(::addSuppressed) }
        }
    }
}

private fun branchReuseRemotes(module: ServiceModuleConfig): List<String> = buildList {
    add(module.baseRemote)
    if (module.tagEnabled && module.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
        add(RemoteBranchRef.parse(requireNotNull(module.tagTargetRef)).remote)
    }
}.distinct()

class IndependentCloneProvisioner(
    private val git: GitClient = GitClient(),
    private val bootstrap: BootstrapService = BootstrapService(),
) : WorkspaceProvisioner {
    override val strategy = WorkspaceStrategy.INDEPENDENT_CLONE

    override fun provision(request: WorkspaceProvisionRequest): List<ServiceWorkspace> {
        require(request.service.modules.all { it.strategy == strategy }) { "请求包含非独立克隆模块" }
        val created = mutableListOf<ServiceWorkspace>()
        val createdTargets = mutableListOf<Pair<Path, String>>()
        try {
            request.service.modules.forEach { module ->
                val base = RemoteBranchRef.parse(module.baseRef)
                val sourceUrl = sourceRemoteUrl(git, request.repository, base.remote)
                val requestedTarget = request.moduleBranches[module.id]?.trim().orEmpty().ifBlank { null }
                val target = request.taskDirectory.resolve(WorkspaceLayout.moduleDirectoryName(request.service, module)).toAbsolutePath().normalize()
                require(target.parent == request.taskDirectory.toAbsolutePath().normalize()) { "独立克隆必须位于任务目录的直接子级" }
                require(!Files.exists(target)) { "目标目录已存在：$target" }
                require(remoteHeadExists(git, request.repository, base.remote, base.branch)) {
                    "远程基础分支不存在：${base.remote}/${base.branch}"
                }
                val targetSha = requestedTarget?.let { remoteHeadSha(git, request.repository, base.remote, it) }
                val targetExists = targetSha != null
                if (requestedTarget != null && !targetExists) {
                    val previouslyConfirmed = request.confirmedBranchReuseKeys.any { confirmed ->
                        confirmed.repositoryId == request.repository.id && confirmed.branch == requestedTarget &&
                            confirmed.stateFingerprint?.startsWith("clone-remote=") == true
                    }
                    require(!previouslyConfirmed) {
                        "远程目标分支复用状态已变化，请重新预检：${base.remote}/$requestedTarget"
                    }
                }
                val ownership = IndependentCloneWorkspaceSafety.ownership(request.taskDirectory, request.repository.id, request.service.id, module.id)
                IndependentCloneWorkspaceSafety.cloneIntoPlace(request.taskDirectory, target, ownership) { staging ->
                    if (targetExists) {
                        val existingTarget = requireNotNull(requestedTarget)
                        val key = BranchReuseKey(request.repository.id, existingTarget, "clone-remote=${base.remote}/$existingTarget@$targetSha")
                        require(confirmedReuse(request, key)) {
                            "远程目标分支复用尚未确认或状态已变化，请重新预检：${base.remote}/$existingTarget"
                        }
                        git.cloneRepository(sourceUrl, staging, existingTarget)
                        require(git.resolve(staging, "HEAD") == targetSha) {
                            "远程目标分支复用状态已变化，请重新预检：${base.remote}/$existingTarget"
                        }
                    } else {
                        git.cloneRepository(sourceUrl, staging, base.branch)
                        if (requestedTarget != null) {
                            require(git.run(staging, "check-ref-format", "--branch", requestedTarget, check = false).succeeded) { "目标分支名不合法：$requestedTarget" }
                            git.run(staging, "switch", "-c", requestedTarget)
                        }
                    }
                }
                createdTargets.add(target to ownership)
                val initialization = bootstrap.initialize(Path.of(request.repository.rootPath).toAbsolutePath().normalize(), target, request.service.bootstrap)
                created += ServiceWorkspace(
                    repositoryId = request.repository.id,
                    serviceName = request.service.displayName,
                    repositoryPath = target.toString(),
                    worktreePath = target.toString(),
                    developmentTool = request.service.developmentTool,
                    branch = requestedTarget ?: base.branch,
                    health = if (initialization.succeeded) WorkspaceHealth.READY else WorkspaceHealth.READY_WITH_WARNINGS,
                    warnings = initialization.warnings,
                    groupServiceId = request.service.id,
                    moduleId = module.id,
                    moduleName = request.moduleDisplayNames[module.id]
                        ?: ModuleDisplayNaming.resolve(module.name, request.service.displayName, module.baseRef, request.service.modules.size),
                    strategy = strategy,
                    moduleSource = request.moduleSources[module.id] ?: TaskModuleSource.CONFIGURED,
                    originUrl = sourceUrl,
                    baseRef = module.baseRef,
                    targetBranch = requestedTarget,
                    tagEnabled = module.tagEnabled,
                    tagMode = module.tagMode,
                    tagTargetRef = module.tagTargetRef,
                    tagMessagePrefix = module.tagMessagePrefix,
                    // The selected source URL is deliberately named `origin` in every new clone.
                    // Source selection is preserved by baseRef/baseRemote for diagnostics and repair.
                    pushRemote = "origin",
                )
            }
            return created
        } catch (error: Throwable) {
            createdTargets.asReversed().forEach { (target, ownership) ->
                runCatching { IndependentCloneWorkspaceSafety.deleteOwned(request.taskDirectory, target, ownership) }.onFailure(error::addSuppressed)
            }
            throw error
        }
    }

    override fun rollback(request: WorkspaceProvisionRequest, workspaces: List<ServiceWorkspace>) {
        workspaces.distinctBy(ServiceWorkspace::worktreePath).asReversed().forEach { workspace ->
            val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
            val module = request.service.modules.single { it.id == workspace.moduleId }
            IndependentCloneWorkspaceSafety.deleteOwned(
                request.taskDirectory,
                target,
                IndependentCloneWorkspaceSafety.ownership(request.taskDirectory, request.repository.id, request.service.id, module.id),
            )
        }
    }

    private fun deleteWritable(path: Path) {
        try {
            Files.deleteIfExists(path)
        } catch (denied: java.nio.file.AccessDeniedException) {
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

class WorkspaceProvisioningService(
    provisioners: List<WorkspaceProvisioner> = listOf(StandardWorktreeProvisioner(), IndependentCloneProvisioner()),
) {
    private val byStrategy = provisioners.associateBy(WorkspaceProvisioner::strategy)

    fun provision(request: WorkspaceProvisionRequest): List<ServiceWorkspace> {
        val completed = mutableListOf<Pair<WorkspaceProvisionRequest, List<ServiceWorkspace>>>()
        val moduleTargets = resolvedModuleTargets(request)
        val moduleDisplayNames = resolvedModuleDisplayNames(request)
        try {
            request.service.modules.forEach { module ->
                val moduleRequest = request.copy(
                    service = request.service.copy(modules = listOf(module)),
                    moduleBranches = mapOf(module.id to moduleTargets.getValue(module.id)),
                    moduleSources = request.moduleSources.filterKeys { it == module.id },
                    moduleDisplayNames = mapOf(module.id to moduleDisplayNames.getValue(module.id)),
                )
                val workspaces = requireNotNull(byStrategy[module.strategy]) { "未注册工作区策略：${module.strategy}" }.provision(moduleRequest)
                completed += moduleRequest to workspaces
            }
            return completed.flatMap { it.second }
        } catch (error: Throwable) {
            completed.asReversed().forEach { (moduleRequest, workspaces) ->
                runCatching { byStrategy.getValue(moduleRequest.service.modules.single().strategy).rollback(moduleRequest, workspaces) }
                    .onFailure(error::addSuppressed)
            }
            throw error
        }
    }

    fun rollback(request: WorkspaceProvisionRequest, workspaces: List<ServiceWorkspace>) {
        workspaces.asReversed().forEach { workspace ->
            val module = request.service.modules.singleOrNull { it.id == workspace.moduleId }
                ?: error("回滚请求缺少模块快照：${workspace.moduleId}")
            val moduleRequest = request.copy(
                service = request.service.copy(modules = listOf(module)),
                moduleBranches = request.moduleBranches.filterKeys { it == module.id },
                moduleSources = request.moduleSources.filterKeys { it == module.id },
                moduleDisplayNames = request.moduleDisplayNames.filterKeys { it == module.id },
            )
            requireNotNull(byStrategy[workspace.strategy]) { "未注册工作区策略：${workspace.strategy}" }
                .rollback(moduleRequest, listOf(workspace))
        }
    }
}
