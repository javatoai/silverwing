package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import kotlin.io.path.exists

data class CreateGroupedTaskRequest(
    val folderName: String,
    val featureBranch: String,
    val groupId: String,
    val serviceIds: List<String> = emptyList(),
    val requirementLink: String = "",
    val taskNotes: String = "",
    val confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
    val baseOverrides: List<ModuleBaseOverride> = emptyList(),
    val serviceSelections: List<TaskServiceSelection> = emptyList(),
    /** Set exclusively by the Agent CLI operation service. */
    val agentContext: AgentTaskContext? = null,
    /** Set together with [agentContext] so the task is never returned without a handoff. */
    val agentHandoffMarkdown: String? = null,
)

data class AddGroupedTaskServicesRequest(
    val serviceIds: List<String> = emptyList(),
    val confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
    val baseOverrides: List<ModuleBaseOverride> = emptyList(),
    val serviceSelections: List<TaskServiceSelection> = emptyList(),
)

data class AddTaskModulesRequest(
    val serviceId: String,
    val modules: List<TaskModuleSelection>,
    val confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
)

data class TaskServiceSelection(
    val serviceId: String,
    val modules: List<TaskModuleSelection>,
) {
    init {
        require(serviceId.isNotBlank()) { "服务 ID 不能为空" }
        require(modules.isNotEmpty()) { "服务至少需要一个任务模块" }
    }
}

data class TaskModuleSelection(
    val id: String,
    val name: String,
    val strategy: WorkspaceStrategy,
    val baseRef: String,
    val baseRemote: String = "origin",
    val targetBranch: String? = null,
    val source: TaskModuleSource = TaskModuleSource.CONFIGURED,
    val tagEnabled: Boolean = false,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val tagTargetRef: String? = null,
    val tagMessagePrefix: String = "Tag",
) {
    fun toConfig(): ServiceModuleConfig = ServiceModuleConfig(
        id = id,
        name = name,
        strategy = strategy,
        masterBranch = baseRef,
        tagEnabled = tagEnabled && source == TaskModuleSource.CONFIGURED,
        tagMode = tagMode,
        tagTargetRef = tagTargetRef,
        tagMessagePrefix = tagMessagePrefix,
    )

    companion object {
        fun configured(service: GroupServiceConfig, module: ServiceModuleConfig, targetBranch: String?): TaskModuleSelection = TaskModuleSelection(
            id = module.id,
            name = module.name,
            strategy = module.strategy,
            baseRef = service.effectiveMasterBranch(module),
            baseRemote = service.effectiveMasterRemote(module),
            targetBranch = targetBranch,
            tagEnabled = module.tagEnabled,
            tagMode = module.tagMode,
            tagTargetRef = module.tagTargetRef,
            tagMessagePrefix = module.tagMessagePrefix,
        )
    }
}

data class ModuleBaseOverride(
    val serviceId: String,
    val moduleId: String,
    val baseRef: String,
    val targetBranch: String? = null,
) {
    init {
        require(serviceId.isNotBlank() && moduleId.isNotBlank() && baseRef.isNotBlank()) { "主分支覆盖项不能为空" }
        require(targetBranch == null || targetBranch.isNotBlank()) { "创建后分支不能为空" }
    }
}

private data class EffectiveServiceConfiguration(
    val service: GroupServiceConfig,
    val moduleBranches: Map<String, String>,
    val moduleSources: Map<String, TaskModuleSource>,
)

private data class ResolvedRequirementMaterials(
    val requirementId: String?,
    val directory: RequirementMaterialsDirectory,
)

data class StartupSnapshot(
    val config: AppConfig,
    val tasks: List<TaskManifest>,
    val unsupportedTaskDirectories: List<Path> = emptyList(),
)

/** Loads only persisted JSON. Git and remote integrations are deliberately absent from this startup path. */
class StartupSnapshotLoader(
    private val configurations: ConfigurationRepository = ConfigStore(),
    private val manifests: TaskManifestRepository = ManifestStore(),
) {
    fun load(): StartupSnapshot {
        val config = configurations.load()
        val scan = config.taskRoot?.let(Path::of)?.let(manifests::scan)
        val tasks = scan?.current
            ?.map { it.second }
            ?.sortedByDescending(TaskManifest::updatedAt)
            .orEmpty()
        return StartupSnapshot(config, tasks, scan?.unsupportedDirectories.orEmpty())
    }
}

/**
 * Coordinates task use cases through strategy and storage ports. It owns no Git
 * command construction and no JSON details, which keeps Desktop state orchestration testable.
 */
class TaskApplicationService(
    private val manifests: TaskManifestRepository = ManifestStore(),
    private val provisioning: WorkspaceProvisioningService = WorkspaceProvisioningService(),
    private val agentDocuments: AgentDocuments = AgentDocumentService(),
    private val requirementMaterials: RequirementMaterialsService = RequirementMaterialsService(),
    private val lifecycle: WorkspaceLifecycle = GitWorkspaceLifecycle(),
    private val operationLock: TaskOperationLock = FileTaskOperationLock(),
    private val branchValidator: BranchReferenceValidator = GitBranchReferenceValidator(),
    private val branchReuseInspector: WorkspaceBranchReuseInspector = WorkspaceBranchReuseInspector(),
    private val repairs: WorkspaceRepairService = WorkspaceRepairService(manifests, agentDocuments, operationLock),
    private val moduleRemoval: WorkspaceModuleRemovalService = WorkspaceModuleRemovalService(manifests, agentDocuments, operationLock),
    private val bootstrap: BootstrapService = BootstrapService(),
    private val clock: Clock = Clock.systemUTC(),
    private val tagTargetValidator: TaskTagTargetValidator = GitTaskTagTargetValidator(),
) {
    fun inspectModuleRemoval(config: AppConfig, taskDirectory: Path, workspacePath: String): WorkspaceModuleRemovalPreview =
        moduleRemoval.inspect(config, taskDirectory, workspacePath)

    fun removeModule(
        config: AppConfig,
        taskDirectory: Path,
        preview: WorkspaceModuleRemovalPreview,
        confirmation: WorkspaceModuleRemovalConfirmation,
    ): WorkspaceModuleRemovalResult = moduleRemoval.remove(config, taskDirectory, preview, confirmation)

    fun inspectWorkspaceRepair(config: AppConfig, taskDirectory: Path, workspacePath: String): WorkspaceRepairPreview =
        repairs.inspect(config, taskDirectory, workspacePath)

    fun repairWorkspace(
        config: AppConfig,
        taskDirectory: Path,
        preview: WorkspaceRepairPreview,
        confirmation: WorkspaceRepairConfirmation,
    ): WorkspaceRepairResult = repairs.repair(config, taskDirectory, preview, confirmation)

    /** Finds branch reuse decisions needed before task-directory creation begins. */
    fun inspectCreateBranchReuse(config: AppConfig, request: CreateGroupedTaskRequest): List<BranchReuseConflict> {
        val group = config.group(request.groupId)
        val taskRoot = config.taskRoot?.let(Path::of) ?: error("尚未配置任务根目录")
        val folderName = TaskNaming.requireValidDirectoryName(request.folderName)
        val resolved = resolveSelections(group, request.serviceIds, request.serviceSelections, request.baseOverrides, request.featureBranch)
        requireUniqueWorkspacePaths(taskRoot.resolve(folderName), emptyList(), resolved)
        return inspectBranchReuse(
            config,
            resolved,
        )
    }

    /** Finds branch reuse decisions for the same branch used by an existing task. */
    fun inspectAddServicesBranchReuse(
        config: AppConfig,
        taskDirectory: Path,
        request: AddGroupedTaskServicesRequest,
    ): List<BranchReuseConflict> = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val group = config.group(manifest.groupId)
        val existingIds = manifest.services.map(ServiceWorkspace::groupServiceId).toSet()
        val requestedIds = request.serviceSelections.map(TaskServiceSelection::serviceId).ifEmpty { request.serviceIds }
        requestedIds.forEach { id ->
            require(id !in existingIds) { "服务已在任务中：$id" }
        }
        val resolved = resolveSelections(group, request.serviceIds, request.serviceSelections, request.baseOverrides, manifest.featureBranch)
        requireUniqueWorkspacePaths(taskDirectory, manifest.services, resolved)
        inspectBranchReuse(config, resolved)
    }

    private fun inspectBranchReuse(
        config: AppConfig,
        services: List<EffectiveServiceConfiguration>,
    ): List<BranchReuseConflict> {
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        return services.flatMap { effective ->
            val service = effective.service
            val repository = repositories[service.repositoryId]
                ?: error("服务 ${service.displayName} 的仓库配置不存在")
            branchReuseInspector.inspect(
                repository = repository,
                service = service,
                requestedFeatureBranch = effective.moduleBranches.values.firstOrNull() ?: "unused",
                moduleBranches = effective.moduleBranches,
            )
        }.distinctBy(BranchReuseConflict::key)
    }

    fun create(config: AppConfig, request: CreateGroupedTaskRequest): TaskManifest {
        val taskRoot = config.taskRoot?.let(Path::of)
            ?: error("尚未配置任务根目录")
        val folderName = TaskNaming.requireValidDirectoryName(request.folderName)
        val taskDirectory = taskRoot.resolve(folderName)
        return operationLock.withLock(taskRoot) {
            operationLock.withLock(taskDirectory) {
                createUnlocked(config, request, taskRoot, taskDirectory, folderName)
            }
        }
    }

    private fun createUnlocked(
        config: AppConfig,
        request: CreateGroupedTaskRequest,
        taskRoot: Path,
        taskDirectory: Path,
        folderName: String,
    ): TaskManifest {
        val featureBranch = request.featureBranch.trim()
        require(folderName.isNotEmpty()) { "任务名称不能为空" }
        require(featureBranch.isNotEmpty() && featureBranch.none(Char::isWhitespace)) { "任务分支不能为空或包含空格" }
        require(!BranchPrefixResolver.containsUnresolvedPlaceholder(featureBranch)) {
            "任务分支仍包含未解析的 {num}"
        }
        require((request.agentContext == null) == (request.agentHandoffMarkdown == null)) {
            "Agent 任务上下文与交接文档必须同时提供"
        }
        require(branchValidator.isValid(featureBranch)) { "任务分支不是合法的 Git 分支名：$featureBranch" }
        val group = config.group(request.groupId)
        val services = resolveSelections(
            group,
            request.serviceIds,
            request.serviceSelections,
            request.baseOverrides,
            featureBranch,
        )
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        requireUniqueWorkspacePaths(taskDirectory, emptyList(), services)
        val taskDirectoryName = folderName
        val materials = resolveRequirementMaterials(config, request.requirementLink, folderName)
        taskRoot.toAbsolutePath().normalize().let { normalizedRoot ->
            Files.createDirectories(normalizedRoot)
            require(taskDirectory.toAbsolutePath().normalize().parent == normalizedRoot) { "任务目录必须是任务根目录的直接子目录" }
        }
        // Exclusive creation is a safety boundary: rollback may remove this directory,
        // therefore it must never predate the current request.
        require(!taskDirectory.exists()) { "任务目录已存在，请换一个任务名称：$taskDirectory" }
        Files.createDirectory(taskDirectory)

        val workspaces = mutableListOf<ServiceWorkspace>()
        try {
            services.forEach { effective ->
                val service = effective.service
                val repository = repositories[service.repositoryId]
                    ?: error("服务 ${service.displayName} 的仓库配置不存在")
                workspaces += provisioning.provision(
                    WorkspaceProvisionRequest(
                        taskDirectory = taskDirectory,
                        repository = repository,
                        service = service,
                        requestedFeatureBranch = featureBranch,
                        confirmedBranchReuseKeys = request.confirmedBranchReuseKeys,
                        moduleBranches = effective.moduleBranches,
                        moduleSources = effective.moduleSources,
                    ),
                )
            }
            val now = SilverWingTime.format(Instant.now(clock))
            val manifest = TaskManifest(
                folderName = folderName,
                taskDirectoryName = taskDirectoryName,
                featureBranch = featureBranch,
                requirementLink = request.requirementLink.trim(),
                requirementId = materials.requirementId,
                requirementMaterials = materials.directory,
                createdAt = now,
                updatedAt = now,
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = workspaces,
                groupId = group.id,
                agentContext = request.agentContext,
                agentDocumentLayout = AgentDocumentLayout.REFERENCED_V2,
            )
            manifests.save(taskDirectory, manifest)
            agentDocuments.createTaskDocument(
                taskDirectory,
                manifest,
                config.repositories.map(RepositoryConfig::toInfo),
                request.taskNotes,
            )
            if (request.agentContext != null) {
                HandoffDocumentWriter.write(taskDirectory, request.agentHandoffMarkdown)
            }
            return manifest
        } catch (error: Throwable) {
            // Roll back only resources created by this request. Existing repositories
            // and branches are never deleted implicitly.
            if (workspaces.isNotEmpty()) {
                val rollbackManifest = TaskManifest(
                    folderName = folderName,
                    taskDirectoryName = taskDirectoryName,
                    featureBranch = featureBranch,
                    createdAt = SilverWingTime.format(Instant.now(clock)),
                    updatedAt = SilverWingTime.format(Instant.now(clock)),
                    lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                    services = workspaces,
                    groupId = group.id,
                )
                val cleanup = runCatching {
                    rollbackProvisionedWorkspaces(config, taskDirectory, rollbackManifest)
                }
                if (cleanup.isFailure) {
                    throw IllegalStateException(
                        "任务创建失败且自动回滚未完成；为避免 Git 元数据损坏，已保留目录供人工检查：$taskDirectory",
                        cleanup.exceptionOrNull(),
                    ).apply { addSuppressed(error) }
                }
                deleteGeneratedTaskDocuments(taskDirectory)
            }
            if (taskDirectory.exists()) {
                val remaining = Files.list(taskDirectory).use { it.toList() }
                if (remaining.isEmpty()) {
                    Files.deleteIfExists(taskDirectory)
                } else {
                    throw IllegalStateException(
                        "任务创建失败且目录中仍有未能安全归属的内容；已保留现场供人工检查：$taskDirectory",
                        error,
                    )
                }
            }
            throw error
        }
    }

    /** Creation owns every generated file below a fresh task directory and may remove only those names on rollback. */
    private fun deleteGeneratedTaskDocuments(taskDirectory: Path) {
        Files.deleteIfExists(taskDirectory.resolve(ManifestStore.FILE_NAME))
        Files.deleteIfExists(taskDirectory.resolve(AgentsMdWriter.FILE_NAME))
        val silverwingDirectory = taskDirectory.resolve(HandoffDocumentWriter.DIRECTORY_NAME)
        val agentDirectory = silverwingDirectory.resolve(AgentDocumentService.TASK_AGENT_DIRECTORY_NAME)
        listOf(
            AgentDocumentService.TASK_CONTEXT_FILE_NAME,
            AgentDocumentService.WORKTREE_SCOPE_FILE_NAME,
            AgentDocumentService.RULE_SOURCES_FILE_NAME,
            AgentDocumentService.TASK_RULES_FILE_NAME,
        ).forEach { fileName -> Files.deleteIfExists(agentDirectory.resolve(fileName)) }
        Files.deleteIfExists(agentDirectory)
        Files.deleteIfExists(silverwingDirectory.resolve(HandoffDocumentWriter.FILE_NAME))
        Files.deleteIfExists(silverwingDirectory)
    }

    /** Re-attempts only the independent requirement materials directory; Git workspaces are never touched. */
    fun retryRequirementMaterials(config: AppConfig, taskDirectory: Path): TaskManifest = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        require(manifest.requirementLink.isNotBlank()) { "任务未填写需求编号或飞书需求链接" }
        val materials = resolveRequirementMaterials(config, manifest.requirementLink, manifest.folderName)
        val updated = manifest.copy(
            requirementId = materials.requirementId,
            requirementMaterials = materials.directory,
            updatedAt = SilverWingTime.format(Instant.now(clock)),
        )
        manifests.save(taskDirectory, updated)
        agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
        updated
    }

    private fun resolveRequirementMaterials(
        config: AppConfig,
        requirementInput: String,
        folderName: String,
    ): ResolvedRequirementMaterials {
        val normalizedInput = requirementInput.trim()
        if (normalizedInput.isEmpty()) {
            return ResolvedRequirementMaterials(null, RequirementMaterialsDirectory())
        }
        if (!config.requirementMaterialsConfigured) {
            return ResolvedRequirementMaterials(
                requirementId = requirementMaterials.parseRequirementId(normalizedInput),
                directory = RequirementMaterialsDirectory(),
            )
        }
        return when (
            val result = requirementMaterials.ensure(
                requirementInput = normalizedInput,
                folderName = folderName,
                materialsRoot = config.requirementMaterialsRoot,
                subdirectoryName = config.requirementMaterialsSubdirectory,
                projects = config.meegleProjects,
            )
        ) {
            is RequirementMaterialsResult.Ready -> ResolvedRequirementMaterials(
                requirementId = result.requirementId,
                directory = RequirementMaterialsDirectory(
                    status = RequirementMaterialsStatus.READY,
                    writeRoot = result.writeRoot.toString(),
                ),
            )
            is RequirementMaterialsResult.Failed -> ResolvedRequirementMaterials(
                requirementId = result.requirementId,
                directory = RequirementMaterialsDirectory(
                    status = RequirementMaterialsStatus.FAILED,
                    failureReason = result.reason,
                ),
            )
        }
    }

    /** Rolls back only paths and branches marked as created by this provisioning transaction. */
    private fun rollbackProvisionedWorkspaces(
        config: AppConfig,
        taskDirectory: Path,
        manifest: TaskManifest,
    ) {
        val group = config.group(manifest.groupId)
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        manifest.services
            .groupBy(ServiceWorkspace::groupServiceId)
            .values
            .toList()
            .asReversed()
            .forEach { workspaces ->
                val workspace = workspaces.first()
                val configuredService = group.services.firstOrNull { it.id == workspace.groupServiceId }
                    ?: error("回滚服务配置不存在：${workspace.groupServiceId}")
                val service = configuredService.copy(modules = workspaces.map { item ->
                    val base = item.baseRef ?: error("回滚模块缺少主分支快照：${item.moduleName}")
                    ServiceModuleConfig(
                        id = item.moduleId,
                        name = item.moduleName,
                        strategy = item.strategy,
                        masterBranch = base,
                        tagEnabled = item.tagEnabled,
                        tagMode = item.tagMode,
                        tagTargetRef = item.tagTargetRef,
                        tagMessagePrefix = item.tagMessagePrefix,
                    )
                })
                val repository = repositories[workspace.repositoryId]
                    ?: error("回滚仓库配置不存在：${workspace.repositoryId}")
                provisioning.rollback(
                    WorkspaceProvisionRequest(
                        taskDirectory = taskDirectory,
                        repository = repository,
                        service = service,
                        requestedFeatureBranch = manifest.featureBranch,
                        moduleBranches = workspaces.associate { it.moduleId to it.targetBranch.orEmpty() },
                        moduleSources = workspaces.associate { it.moduleId to it.moduleSource },
                    ),
                    workspaces,
                )
            }
    }

    fun refreshAgents(config: AppConfig, taskDirectory: Path): Path = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        agentDocuments.refreshTaskDocument(taskDirectory, manifest, config.repositories.map(RepositoryConfig::toInfo))
    }

    /** Adds configured services without allowing a task to move between groups. */
    fun addServices(
        config: AppConfig,
        taskDirectory: Path,
        request: AddGroupedTaskServicesRequest,
    ): TaskManifest = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val group = config.group(manifest.groupId)
        val existingIds = manifest.services.map(ServiceWorkspace::groupServiceId).toSet()
        val requestedIds = request.serviceSelections.map(TaskServiceSelection::serviceId).ifEmpty { request.serviceIds }
        requestedIds.forEach { require(it !in existingIds) { "服务已在任务中：$it" } }
        val services = resolveSelections(
            group,
            request.serviceIds,
            request.serviceSelections,
            request.baseOverrides,
            manifest.featureBranch,
        )
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        requireUniqueWorkspacePaths(taskDirectory, manifest.services, services)
        val created = mutableListOf<Pair<WorkspaceProvisionRequest, List<ServiceWorkspace>>>()
        var manifestCommitted = false
        try {
            services.forEach { effective ->
                val service = effective.service
                val repository = repositories[service.repositoryId]
                    ?: error("服务 ${service.displayName} 的仓库配置不存在")
                val provisionRequest = WorkspaceProvisionRequest(
                    taskDirectory = taskDirectory,
                    repository = repository,
                    service = service,
                    requestedFeatureBranch = manifest.featureBranch,
                    confirmedBranchReuseKeys = request.confirmedBranchReuseKeys,
                    moduleBranches = effective.moduleBranches,
                    moduleSources = effective.moduleSources,
                )
                created += provisionRequest to provisioning.provision(provisionRequest)
            }
            val added = created.flatMap { it.second }
            val updated = manifest.copy(
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                services = manifest.services + added,
            )
            manifests.save(taskDirectory, updated)
            manifestCommitted = true
            agentDocuments.writeTaskDocument(
                taskDirectory,
                updated,
                config.repositories.map(RepositoryConfig::toInfo),
            )
            updated
        } catch (error: Throwable) {
            val rollbackFailures = mutableListOf<Throwable>()
            if (manifestCommitted) {
                runCatching {
                    manifests.save(taskDirectory, manifest)
                    agentDocuments.writeTaskDocument(
                        taskDirectory,
                        manifest,
                        config.repositories.map(RepositoryConfig::toInfo),
                    )
                }.onFailure(rollbackFailures::add)
            }
            created.asReversed().forEach { (provisionRequest, workspaces) ->
                runCatching { provisioning.rollback(provisionRequest, workspaces) }
                    .onFailure(rollbackFailures::add)
            }
            if (rollbackFailures.isNotEmpty()) {
                throw IllegalStateException(
                    "追加服务失败，且自动回滚未完整完成；已保留任务数据供人工检查：$taskDirectory",
                    error,
                ).apply { rollbackFailures.forEach(::addSuppressed) }
            }
            throw error
        }
    }

    fun inspectAddModulesBranchReuse(
        config: AppConfig,
        taskDirectory: Path,
        request: AddTaskModulesRequest,
    ): List<BranchReuseConflict> = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val group = config.group(manifest.groupId)
        val configuredService = group.services.firstOrNull { it.id == request.serviceId && it.enabled }
            ?: error("组 ${group.name} 中不存在或未启用服务：${request.serviceId}")
        requireNewModules(manifest, taskDirectory, configuredService, request.modules)
        inspectBranchReuse(
            config,
            resolveSelections(
                group,
                emptyList(),
                listOf(TaskServiceSelection(request.serviceId, request.modules)),
                emptyList(),
                manifest.featureBranch,
            ),
        )
    }

    fun addModules(
        config: AppConfig,
        taskDirectory: Path,
        request: AddTaskModulesRequest,
    ): TaskManifest = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val group = config.group(manifest.groupId)
        val configuredService = group.services.firstOrNull { it.id == request.serviceId && it.enabled }
            ?: error("组 ${group.name} 中不存在或未启用服务：${request.serviceId}")
        requireNewModules(manifest, taskDirectory, configuredService, request.modules)
        val effective = resolveSelections(
            group,
            emptyList(),
            listOf(TaskServiceSelection(request.serviceId, request.modules)),
            emptyList(),
            manifest.featureBranch,
        ).single()
        val repository = config.repositories.firstOrNull { it.id == effective.service.repositoryId }
            ?: error("服务 ${effective.service.displayName} 的仓库配置不存在")
        val provisionRequest = WorkspaceProvisionRequest(
            taskDirectory = taskDirectory,
            repository = repository,
            service = effective.service,
            requestedFeatureBranch = manifest.featureBranch,
            confirmedBranchReuseKeys = request.confirmedBranchReuseKeys,
            moduleBranches = effective.moduleBranches,
            moduleSources = effective.moduleSources,
            moduleDisplayNames = effective.service.modules.associate { module ->
                module.id to StandardWorktreeModuleNaming.effectiveName(module)
            },
        )
        val added = provisioning.provision(provisionRequest)
        try {
            val updated = manifest.copy(
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                services = manifest.services + added,
            )
            manifests.save(taskDirectory, updated)
            agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
            updated
        } catch (error: Throwable) {
            runCatching { provisioning.rollback(provisionRequest, added) }.onFailure(error::addSuppressed)
            runCatching {
                manifests.save(taskDirectory, manifest)
                agentDocuments.writeTaskDocument(taskDirectory, manifest, config.repositories.map(RepositoryConfig::toInfo))
            }.onFailure(error::addSuppressed)
            throw error
        }
    }

    private fun requireNewModules(
        manifest: TaskManifest,
        taskDirectory: Path,
        configuredService: GroupServiceConfig,
        modules: List<TaskModuleSelection>,
    ) {
        require(modules.isNotEmpty()) { "至少添加一个模块" }
        val existingWorkspaces = manifest.services.filter { it.groupServiceId == configuredService.id }
        val existing = existingWorkspaces.map { it.moduleId.lowercase() }.toSet()
        require(modules.none { it.id.lowercase() in existing }) { "任务中已存在同名模块" }
        require(modules.map { it.id.lowercase() }.distinct().size == modules.size) { "新增模块 ID 不能重复（忽略大小写）" }
        val existingNames = existingWorkspaces.map(ServiceWorkspace::moduleName).map(String::lowercase).toSet()
        val newConfigs = modules.map(TaskModuleSelection::toConfig)
        val newNames = newConfigs.map(StandardWorktreeModuleNaming::effectiveName)
        require(newNames.none { it.lowercase() in existingNames }) { "新增模块名称不能与任务中已有模块重复（忽略大小写）" }
        require(newNames.map(String::lowercase).distinct().size == newNames.size) { "新增模块名称不能重复（忽略大小写）" }
        val existingPaths = manifest.services.map {
            Path.of(it.worktreePath).toAbsolutePath().normalize().toString().lowercase()
        }.toSet()
        val plannedPaths = newConfigs.map { module ->
            taskDirectory.resolve(WorkspaceLayout.moduleDirectoryName(configuredService, module))
                .toAbsolutePath().normalize().toString().lowercase()
        }
        require(plannedPaths.none(existingPaths::contains)) { "新增模块目录不能与任务中已有模块重复" }
        require(plannedPaths.distinct().size == plannedPaths.size) { "新增模块名称转换为目录后不能重复" }
        val existingWorktreeTargets = manifest.services
            .filter {
                it.strategy == WorkspaceStrategy.STANDARD_WORKTREE &&
                    it.repositoryId == configuredService.repositoryId
            }
            .map { it.targetBranch ?: it.branch }
        val newWorktreeTargets = modules
            .filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }
            .mapNotNull { it.targetBranch?.trim()?.takeIf(String::isNotBlank) }
        TaskBranchNaming.requireNoConflicts(existingWorktreeTargets + newWorktreeTargets)
    }

    /** Re-provisions complete failed service entries while keeping successful workspaces untouched. */
    fun retryFailedServices(
        config: AppConfig,
        taskDirectory: Path,
        serviceIds: List<String>? = null,
    ): TaskManifest = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val failedIds = manifest.services
            .filter { it.health == WorkspaceHealth.FAILED }
            .map(ServiceWorkspace::groupServiceId)
            .filter(String::isNotBlank)
            .toSet()
        val selected = serviceIds?.toSet() ?: failedIds
        require(selected.isNotEmpty() && selected.all { it in failedIds }) { "没有可重试的失败服务" }
        val group = config.group(manifest.groupId)
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        val created = mutableListOf<Pair<WorkspaceProvisionRequest, List<ServiceWorkspace>>>()
        var manifestCommitted = false
        try {
            selected.forEach { serviceId ->
                val configuredService = group.services.firstOrNull { it.id == serviceId }
                    ?: error("失败服务已不在组配置中：$serviceId")
                val recorded = manifest.services.filter { it.groupServiceId == serviceId }
                val service = configuredService.copy(modules = recorded.map { workspace ->
                    val base = workspace.baseRef ?: error("失败模块缺少主分支快照：${workspace.moduleName}")
                    ServiceModuleConfig(
                        id = workspace.moduleId,
                        name = workspace.moduleName,
                        strategy = workspace.strategy,
                        masterBranch = base,
                        tagEnabled = workspace.tagEnabled,
                        tagMode = workspace.tagMode,
                        tagTargetRef = workspace.tagTargetRef,
                        tagMessagePrefix = workspace.tagMessagePrefix,
                    )
                })
                val repository = repositories[service.repositoryId]
                    ?: error("服务 ${service.displayName} 的仓库配置不存在")
                val recordedBranches = recorded.associate { workspace -> workspace.moduleId to workspace.targetBranch.orEmpty() }
                val provisionRequest = WorkspaceProvisionRequest(
                    taskDirectory = taskDirectory,
                    repository = repository,
                    service = service,
                    requestedFeatureBranch = manifest.featureBranch,
                    moduleBranches = recordedBranches,
                    moduleSources = recorded.associate { it.moduleId to it.moduleSource },
                )
                created += provisionRequest to provisioning.provision(provisionRequest)
            }
            val replacements = created.flatMap { it.second }
            val retained = manifest.services.filterNot { it.groupServiceId in selected }
            val updated = manifest.copy(
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                services = retained + replacements,
            )
            manifests.save(taskDirectory, updated)
            manifestCommitted = true
            agentDocuments.writeTaskDocument(
                taskDirectory,
                updated,
                config.repositories.map(RepositoryConfig::toInfo),
            )
            updated
        } catch (error: Throwable) {
            val rollbackFailures = mutableListOf<Throwable>()
            if (manifestCommitted) {
                runCatching {
                    manifests.save(taskDirectory, manifest)
                    agentDocuments.writeTaskDocument(
                        taskDirectory,
                        manifest,
                        config.repositories.map(RepositoryConfig::toInfo),
                    )
                }.onFailure(rollbackFailures::add)
            }
            created.asReversed().forEach { (request, workspaces) ->
                runCatching { provisioning.rollback(request, workspaces) }.onFailure(rollbackFailures::add)
            }
            if (rollbackFailures.isNotEmpty()) {
                throw IllegalStateException(
                    "重试失败，且自动回滚未完整完成；已保留任务数据供人工检查：$taskDirectory",
                    error,
                ).apply { rollbackFailures.forEach(::addSuppressed) }
            }
            throw error
        }
    }

    /** Changes only this task module's destination for future Tags. */
    fun updateTagTarget(
        config: AppConfig,
        taskDirectory: Path,
        groupServiceId: String,
        moduleId: String,
        expectedTargetRef: String?,
        targetRef: String,
    ): TaskManifest = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        require(manifest.lifecycleStatus == TaskLifecycleStatus.ACTIVE) { "归档任务不能修改测试目标分支" }
        val workspace = TagPolicy.requireWorkspace(config, manifest, "$groupServiceId:$moduleId")
        require(workspace.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) { "当前分支模式不支持设置测试目标分支" }
        require(workspace.tagTargetRef == expectedTargetRef) { "测试目标分支已被其他操作修改，请关闭后重新打开设置" }
        val target = RemoteBranchRef.parse(targetRef)
        val validated = lifecycle.validateForMutation(config, taskDirectory, manifest, workspace)
        tagTargetValidator.validate(validated.repository, target)
        val updated = manifest.copy(
            updatedAt = SilverWingTime.format(Instant.now(clock)),
            services = manifest.services.map { current ->
                if (current.groupServiceId == groupServiceId && current.moduleId == moduleId) {
                    current.copy(tagTargetRef = target.toString())
                } else current
            },
        )
        manifests.save(taskDirectory, updated)
        updated
    }

    fun saveTaskNotes(config: AppConfig, taskDirectory: Path, notes: String): Path = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        agentDocuments.writeTaskDocument(
            taskDirectory,
            manifest,
            config.repositories.map(RepositoryConfig::toInfo),
            notes,
        )
    }

    /** Dismisses bootstrap warnings the user has acknowledged; only READY_WITH_WARNINGS entries flip back to READY. */
    fun clearWorkspaceWarnings(config: AppConfig, taskDirectory: Path, workspacePath: String): TaskManifest =
        operationLock.withLock(taskDirectory) {
            val manifest = manifests.load(taskDirectory)
            val normalizedTarget = normalize(workspacePath)
            val updated = manifest.copy(
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                services = manifest.services.map { existing ->
                    if (normalize(existing.worktreePath) != normalizedTarget) return@map existing
                    existing.copy(
                        warnings = emptyList(),
                        health = if (existing.health == WorkspaceHealth.READY_WITH_WARNINGS) WorkspaceHealth.READY else existing.health,
                    )
                },
            )
            manifests.save(taskDirectory, updated)
            agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
            updated
        }

    /**
     * Re-runs the current service bootstrap snapshot against an existing workspace and replaces its
     * warnings with the fresh result. Non-overwriting copy rules fail on existing targets by design.
     */
    fun rerunWorkspaceBootstrap(config: AppConfig, taskDirectory: Path, workspacePath: String): TaskManifest =
        operationLock.withLock(taskDirectory) {
            val manifest = manifests.load(taskDirectory)
            val normalizedTarget = normalize(workspacePath)
            val workspace = manifest.services.firstOrNull { normalize(it.worktreePath) == normalizedTarget }
                ?: error("任务中不存在工作区：$workspacePath")
            require(workspace.health == WorkspaceHealth.READY || workspace.health == WorkspaceHealth.READY_WITH_WARNINGS) {
                "仅就绪的工作区可以重新执行 Bootstrap"
            }
            val target = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
            require(Files.exists(target)) { "工作区目录不存在：$target" }
            val service = config.group(manifest.groupId).services.firstOrNull { it.id == workspace.groupServiceId }
                ?: error("服务配置已经不存在，无法重新执行 Bootstrap")
            val source = when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> Path.of(workspace.repositoryPath).toAbsolutePath().normalize()
                WorkspaceStrategy.INDEPENDENT_CLONE -> config.repositories.firstOrNull { it.id == workspace.repositoryId }
                    ?.let { Path.of(it.rootPath).toAbsolutePath().normalize() }
                    ?: error("仓库配置已经不存在，无法重新执行 Bootstrap")
            }
            val warnings = bootstrap.initialize(source, target, service.bootstrap).warnings
            val updated = manifest.copy(
                updatedAt = SilverWingTime.format(Instant.now(clock)),
                services = manifest.services.map { existing ->
                    if (normalize(existing.worktreePath) != normalizedTarget) return@map existing
                    existing.copy(
                        warnings = warnings,
                        health = if (warnings.isEmpty()) WorkspaceHealth.READY else WorkspaceHealth.READY_WITH_WARNINGS,
                    )
                },
            )
            manifests.save(taskDirectory, updated)
            agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
            updated
        }

    fun inspectDeleteRisk(config: AppConfig, taskDirectory: Path): List<DeleteRisk> = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        lifecycle.inspectDeleteRisks(config, taskDirectory, manifest)
    }

    fun archive(config: AppConfig, taskDirectory: Path, force: Boolean = false): TaskManifest =
        operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        require(manifest.lifecycleStatus != TaskLifecycleStatus.ARCHIVED) { "任务已经归档" }
        // Archive is a navigation classification only. Workspaces remain intact
        // so an archived task can still be opened and resumed without Git work.
        val updated = manifest.copy(
            updatedAt = SilverWingTime.format(Instant.now(clock)),
            lifecycleStatus = TaskLifecycleStatus.ARCHIVED,
            services = manifest.services,
        )
        try {
            manifests.save(taskDirectory, updated)
        } catch (saveError: Throwable) {
            throw saveError
        }
        agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
        updated
    }

    fun restore(config: AppConfig, taskDirectory: Path): TaskManifest = operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        require(manifest.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) { "只有已归档任务可以恢复" }
        val updated = manifest.copy(
            updatedAt = SilverWingTime.format(Instant.now(clock)),
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = manifest.services,
        )
        try {
            manifests.save(taskDirectory, updated)
        } catch (saveError: Throwable) {
            throw saveError
        }
        agentDocuments.writeTaskDocument(taskDirectory, updated, config.repositories.map(RepositoryConfig::toInfo))
        updated
    }

    fun delete(config: AppConfig, taskDirectory: Path, forceDiscard: Boolean = false) =
        operationLock.withLock(taskDirectory) {
        val manifest = manifests.load(taskDirectory)
        val risks = lifecycle.inspectDeleteRisks(config, taskDirectory, manifest)
        require(risks.none { it.statusCheckError != null }) {
            "存在无法安全验证的工作区，请先修复其 Git 身份或路径后再删除任务"
        }
        if (risks.isNotEmpty() && !forceDiscard) error("存在未提交改动，请确认强制丢弃后再删除")
        val removal = lifecycle.removeAll(config, taskDirectory, manifest, forceDiscard)
        if (removal.retainedBackupPaths.isNotEmpty()) {
            error("任务已删除，但部分工作区备份清理失败，已保留：${removal.retainedBackupPaths.joinToString()}")
        }
    }

    private fun compensationFailure(message: String, primary: Throwable, compensation: Throwable): Throwable =
        IllegalStateException(message, compensation).apply { addSuppressed(primary) }

    private fun deleteRecursively(path: Path) {
        if (!path.exists()) return
        val entries = Files.walk(path).use { it.sorted(Comparator.reverseOrder()).toList() }
        entries.forEach(Files::deleteIfExists)
    }

    private fun resolveSelections(
        group: GroupConfig,
        legacyServiceIds: List<String>,
        selections: List<TaskServiceSelection>,
        legacyOverrides: List<ModuleBaseOverride>,
        requestedFeatureBranch: String,
    ): List<EffectiveServiceConfiguration> {
        val effectiveSelections = if (selections.isNotEmpty()) {
            require(legacyServiceIds.isEmpty() || legacyServiceIds.toSet() == selections.map(TaskServiceSelection::serviceId).toSet()) {
                "服务选择与模块选择不一致"
            }
            selections
        } else {
            require(legacyServiceIds.isNotEmpty()) { "至少选择一个服务" }
            require(legacyOverrides.map { it.serviceId to it.moduleId }.distinct().size == legacyOverrides.size) {
                "主分支覆盖项不能重复"
            }
            legacyServiceIds.map { serviceId ->
                val service = group.services.firstOrNull { it.id == serviceId && it.enabled }
                    ?: throw IllegalArgumentException("组 ${group.name} 中不存在或未启用服务：$serviceId")
                val byModule = legacyOverrides.filter { it.serviceId == serviceId }.associateBy(ModuleBaseOverride::moduleId)
                require(byModule.keys.all { id -> service.modules.any { it.id == id } }) { "主分支覆盖引用了不存在的模块" }
                TaskServiceSelection(serviceId, service.modules.map { module ->
                    val override = byModule[module.id]
                    val baseRef = override?.baseRef ?: service.effectiveMasterBranch(module)
                    val target = override?.targetBranch ?: defaultSelectionTarget(requestedFeatureBranch, service.modules, module)
                    TaskModuleSelection.configured(service, module.copy(masterBranch = baseRef), target)
                })
            }
        }
        require(effectiveSelections.map(TaskServiceSelection::serviceId).distinct().size == effectiveSelections.size) {
            "服务不能重复选择"
        }
        return effectiveSelections.map { selection ->
            val configured = group.services.firstOrNull { it.id == selection.serviceId && it.enabled }
                ?: throw IllegalArgumentException("组 ${group.name} 中不存在或未启用服务：${selection.serviceId}")
            val modules = selection.modules.map { selected ->
                selected.toConfig().let { module ->
                    module.copy(tagTargetRef = configured.effectiveTagTargetRef(module))
                }
            }
            StandardWorktreeModuleNaming.requireValid(modules)
            require(modules.map { it.id.lowercase() }.distinct().size == modules.size) { "模块 ID 不能重复（忽略大小写）" }
            val branches = selection.modules.associate { selected ->
                val target = selected.targetBranch?.trim().orEmpty()
                if (selected.strategy == WorkspaceStrategy.STANDARD_WORKTREE) {
                    require(target.isNotBlank()) { "Worktree 模块目标分支不能为空：${selected.name}" }
                }
                if (target.isNotBlank()) validateTargetBranch(selected.id, target)
                selected.id to target
            }
            val worktreeTargets = selection.modules
                .filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }
                .mapNotNull { branches[it.id]?.takeIf(String::isNotBlank) }
            TaskBranchNaming.requireNoConflicts(worktreeTargets)
            EffectiveServiceConfiguration(
                service = configured.copy(modules = modules),
                moduleBranches = branches,
                moduleSources = selection.modules.associate { it.id to it.source },
            )
        }
    }

    private fun defaultSelectionTarget(
        requestedFeatureBranch: String,
        modules: List<ServiceModuleConfig>,
        module: ServiceModuleConfig,
    ): String = if (modules.size == 1) requestedFeatureBranch.trim() else "$requestedFeatureBranch-${StandardWorktreeModuleNaming.effectiveName(module)}"

    private fun validateTargetBranch(moduleId: String, branch: String) {
        require(branch.none(Char::isWhitespace)) { "模块 $moduleId 目标分支不能包含空格：$branch" }
        require(!BranchPrefixResolver.containsUnresolvedPlaceholder(branch)) { "模块 $moduleId 目标分支仍包含未解析的 {num}" }
        require(branchValidator.isValid(branch)) { "模块 $moduleId 目标分支不是合法的 Git 分支名：$branch" }
    }

    private fun requireUniqueWorkspacePaths(
        taskDirectory: Path,
        existing: List<ServiceWorkspace>,
        services: List<EffectiveServiceConfiguration>,
    ) {
        val existingPaths = existing.map {
            Path.of(it.worktreePath).toAbsolutePath().normalize().toString().lowercase()
        }
        val plannedPaths = services.flatMap { effective ->
            effective.service.modules.map { module ->
                taskDirectory.resolve(WorkspaceLayout.moduleDirectoryName(effective.service, module))
                    .toAbsolutePath().normalize().toString().lowercase()
            }
        }
        val duplicates = (existingPaths + plannedPaths).groupingBy(String::lowercase).eachCount().filterValues { it > 1 }.keys
        val existingTargets = existing
            .filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }
            .map { Triple(it.repositoryId, it.targetBranch ?: it.branch, it.moduleName) }
        val plannedTargets = services.flatMap { effective ->
            effective.service.modules.mapNotNull { module ->
                if (module.strategy != WorkspaceStrategy.STANDARD_WORKTREE) return@mapNotNull null
                Triple(
                    effective.service.repositoryId,
                    effective.moduleBranches[module.id].orEmpty(),
                    StandardWorktreeModuleNaming.effectiveName(module),
                )
            }
        }
        (existingTargets + plannedTargets)
            .groupBy { (repositoryId, _, _) -> repositoryId.lowercase() }
            .values
            .forEach { targets ->
                TaskBranchNaming.requireNoConflicts(targets.map { (_, branch, _) -> branch })
            }
        require(duplicates.isEmpty()) {
            "任务内工作区目录不能重复（忽略大小写）：${duplicates.joinToString()}"
        }
    }

}
