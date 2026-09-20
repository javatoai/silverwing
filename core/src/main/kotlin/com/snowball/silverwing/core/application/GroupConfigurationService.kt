package com.snowball.silverwing.core

import java.nio.file.Path
import java.util.UUID

data class RepositoryAddSkip(val path: Path, val reason: String)

data class BatchRepositoryAddResult(
    val config: AppConfig,
    val added: List<Path>,
    val skipped: List<RepositoryAddSkip>,
)

val AppConfig.showsGroupUi: Boolean
    get() = groups.size > 1

/**
 * Application service for ordered group and repository configuration.
 * All mutations replace immutable arrays and are persisted through one small port,
 * so Compose never needs to understand JSON or repository identity rules.
 */
class GroupConfigurationService(
    private val configurations: ConfigurationRepository = ConfigStore(),
    private val repositoryInspector: RepositoryInspector = GitRepositoryInspector(),
    private val taskUsage: TaskGroupUsage = ManifestTaskGroupUsage(),
    private val developmentToolRecommendation: DevelopmentToolRecommendationService = RootMarkerDevelopmentToolRecommendationService(),
) {
    fun load(): AppConfig = configurations.load()

    fun addGroup(name: String): AppConfig = update { config ->
        val normalized = name.trim()
        require(normalized.isNotEmpty()) { "组名不能为空" }
        require(config.groups.none { it.name.equals(normalized, ignoreCase = true) }) { "组名已存在：$normalized" }
        config.copy(
            groups = config.groups + GroupConfig(
                id = "group-${UUID.randomUUID()}",
                name = normalized,
            ),
        )
    }

    fun renameGroup(groupId: String, name: String): AppConfig = update { config ->
        val normalized = name.trim()
        require(normalized.isNotEmpty()) { "组名不能为空" }
        require(config.groups.none { it.id != groupId && it.name.equals(normalized, ignoreCase = true) }) {
            "组名已存在：$normalized"
        }
        config.copy(groups = config.groups.replaceGroup(groupId) { it.copy(name = normalized) })
    }

    fun moveGroup(groupId: String, offset: Int): AppConfig = update { config ->
        val index = config.groups.indexOfFirst { it.id == groupId }
        require(index >= 0) { "找不到组：$groupId" }
        val target = (index + offset).coerceIn(config.groups.indices)
        if (target == index) config else config.copy(groups = config.groups.moved(index, target))
    }

    fun deleteGroup(groupId: String): AppConfig = update { config ->
        require(config.groups.size > 1) { "至少保留一个组" }
        val group = config.group(groupId)
        check(group.services.isEmpty()) { "只能删除空组，请先移除组内服务" }
        check(!taskUsage.hasTasks(config, groupId)) { "该组仍有历史研发任务，不能删除" }
        config.copy(groups = config.groups.filterNot { it.id == groupId })
    }

    fun setGroupTagEnabled(groupId: String, enabled: Boolean): AppConfig = update { config ->
        config.copy(groups = config.groups.replaceGroup(groupId) { it.copy(tagEnabled = enabled) })
    }

    fun updateGroupDefaults(
        groupId: String,
        defaultBranchPrefix: String,
        defaultWorkspaceToolIds: List<String>,
    ): AppConfig = update { config ->
        val prefix = defaultBranchPrefix.trim()
        val toolIds = defaultWorkspaceToolIds.map(String::trim).filter(String::isNotEmpty).distinct()
        config.copy(
            groups = config.groups.replaceGroup(groupId) {
                it.copy(defaultBranchPrefix = prefix, defaultWorkspaceToolIds = toolIds)
            },
        )
    }

    fun addRepository(
        groupId: String,
        selectedDirectory: Path,
        strategy: WorkspaceStrategy = WorkspaceStrategy.STANDARD_WORKTREE,
    ): AppConfig {
        val inspected = repositoryInspector.inspect(selectedDirectory)
        return update { config ->
            val repository = config.repositories.firstOrNull {
                it.gitCommonDirectory.equals(inspected.gitCommonDirectory, ignoreCase = true)
            } ?: inspected
            val group = config.group(groupId)
            require(group.services.none { it.repositoryId == repository.id }) {
                "该仓库已存在于组 ${group.name} 中"
            }
            val service = when (strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> GroupServiceConfig.standard(
                    id = "service-${repository.id.removePrefix("repo-")}",
                    repositoryId = repository.id,
                    displayName = repository.name,
                    developmentTool = config.defaultDevelopmentTool,
                    baseRef = "origin/${repository.defaultRemoteBranch ?: "master"}",
                )
                WorkspaceStrategy.INDEPENDENT_CLONE -> GroupServiceConfig(
                    id = "service-${repository.id.removePrefix("repo-")}",
                    repositoryId = repository.id,
                    displayName = repository.name,
                    developmentTool = config.defaultDevelopmentTool,
                    modules = listOf(ServiceModuleConfig(
                        id = "clone-default",
                        name = "default",
                        strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                        baseRef = repository.defaultRemoteBranch?.let { "origin/$it" }
                            ?: throw IllegalArgumentException("无法确定 origin 的默认远程分支，请先设置 origin/HEAD"),
                        baseRemote = "origin",
                        tagEnabled = false,
                    )),
                )
            }
            config.copy(
                repositories = if (config.repositories.any { it.id == repository.id }) {
                    config.repositories
                } else {
                    config.repositories + repository
                },
                groups = config.groups.replaceGroup(groupId) { it.copy(services = it.services + service) },
            )
        }
    }

    /**
     * Inspects exactly the selected directories and persists all valid additions
     * atomically. It never scans descendants or lets one invalid folder discard
     * the rest of the user's selection.
     */
    fun addRepositories(groupId: String, selectedDirectories: List<Path>): BatchRepositoryAddResult {
        val inspected = selectedDirectories.map { selected ->
            selected to runCatching { repositoryInspector.inspect(selected) }
        }
        val added = mutableListOf<Path>()
        val skipped = mutableListOf<RepositoryAddSkip>()
        val updated = configurations.update { original ->
            val group = original.group(groupId)
            val knownRepositories = original.repositories.associateBy { it.gitCommonDirectory.lowercase() }.toMutableMap()
            val services = group.services.toMutableList()
            val addedRepositories = original.repositories.toMutableList()
            val seenCommonDirectories = mutableSetOf<String>()

            inspected.forEach { (selected, result) ->
                val repositoryInspection = result.getOrElse { error ->
                    skipped += RepositoryAddSkip(selected, error.message ?: "不是可用的 Git 主仓库")
                    return@forEach
                }
                val commonKey = repositoryInspection.gitCommonDirectory.lowercase()
                if (!seenCommonDirectories.add(commonKey)) {
                    skipped += RepositoryAddSkip(selected, "所选目录指向重复的 Git 仓库")
                    return@forEach
                }
                val repository = knownRepositories[commonKey] ?: repositoryInspection.also {
                    knownRepositories[commonKey] = it
                    addedRepositories += it
                }
                if (services.any { it.repositoryId == repository.id }) {
                    skipped += RepositoryAddSkip(selected, "该仓库已存在于组 ${group.name} 中")
                    return@forEach
                }
                services += standardService(repository, original.defaultDevelopmentTool)
                added.add(selected)
            }

            if (added.isEmpty()) original else original.copy(
                repositories = addedRepositories,
                groups = original.groups.replaceGroup(groupId) { it.copy(services = services) },
            )
        }
        return BatchRepositoryAddResult(updated, added, skipped)
    }

    fun updateService(groupId: String, service: GroupServiceConfig): AppConfig = update { config ->
        val group = config.group(groupId)
        require(group.services.any { it.id == service.id }) { "组内找不到服务：${service.id}" }
        config.copy(groups = config.groups.replaceGroup(groupId) { it.copy(services = it.services.replaceService(service.id) { service }) })
    }

    fun moveService(groupId: String, serviceId: String, offset: Int): AppConfig = update { config ->
        val group = config.group(groupId)
        val index = group.services.indexOfFirst { it.id == serviceId }
        require(index >= 0) { "组内找不到服务：$serviceId" }
        val target = (index + offset).coerceIn(group.services.indices)
        config.copy(
            groups = config.groups.replaceGroup(groupId) {
                it.copy(services = if (target == index) it.services else it.services.moved(index, target))
            },
        )
    }

    fun removeService(groupId: String, serviceId: String): AppConfig = update { config ->
        val service = config.groupService(groupId, serviceId)
        check(!taskUsage.hasServiceTasks(config, groupId, serviceId)) {
            "该服务仍被研发任务引用，不能移除"
        }
        val groups = config.groups.replaceGroup(groupId) { group ->
            group.copy(services = group.services.filterNot { it.id == serviceId })
        }
        val stillUsed = groups.any { group -> group.services.any { it.repositoryId == service.repositoryId } }
        config.copy(
            groups = groups,
            repositories = if (stillUsed) config.repositories else config.repositories.filterNot { it.id == service.repositoryId },
        )
    }

    private fun update(transform: (AppConfig) -> AppConfig): AppConfig {
        return configurations.update(transform)
    }

    private fun standardService(repository: RepositoryConfig, developmentTool: DevelopmentToolType): GroupServiceConfig = GroupServiceConfig.standard(
        id = "service-${repository.id.removePrefix("repo-")}",
        repositoryId = repository.id,
        displayName = repository.name,
        developmentTool = developmentTool,
        baseRef = "origin/${repository.defaultRemoteBranch ?: "master"}",
    )
}

interface TaskGroupUsage {
    fun hasTasks(config: AppConfig, groupId: String): Boolean
    fun hasServiceTasks(config: AppConfig, groupId: String, serviceId: String): Boolean
}

class ManifestTaskGroupUsage(
    private val manifests: TaskManifestRepository = ManifestStore(),
) : TaskGroupUsage {
    override fun hasTasks(config: AppConfig, groupId: String): Boolean = config.taskRoot
        ?.let(Path::of)
        ?.let(::scanCurrentOrFail)
        ?.any { it.second.groupId == groupId }
        ?: false

    override fun hasServiceTasks(config: AppConfig, groupId: String, serviceId: String): Boolean = config.taskRoot
        ?.let(Path::of)
        ?.let(::scanCurrentOrFail)
        ?.any { (_, task) -> task.groupId == groupId && task.services.any { it.groupServiceId == serviceId } }
        ?: false

    /** Deletion constraints fail closed when any task cannot be decoded. */
    private fun scanCurrentOrFail(taskRoot: Path): List<Pair<Path, TaskManifest>> {
        val result = manifests.scan(taskRoot)
        check(result.failures.isEmpty()) {
            "存在无法读取的任务清单，请先修复后再删除组或服务：" +
                result.failures.keys.joinToString { it.fileName.toString() }
        }
        return result.current
    }
}

private fun <T> List<T>.moved(from: Int, to: Int): List<T> = toMutableList().apply {
    add(to, removeAt(from))
}

private fun List<GroupConfig>.replaceGroup(
    groupId: String,
    transform: (GroupConfig) -> GroupConfig,
): List<GroupConfig> {
    require(any { it.id == groupId }) { "找不到组：$groupId" }
    return map { if (it.id == groupId) transform(it) else it }
}

private fun List<GroupServiceConfig>.replaceService(
    serviceId: String,
    transform: (GroupServiceConfig) -> GroupServiceConfig,
): List<GroupServiceConfig> {
    require(any { it.id == serviceId }) { "组内找不到服务：$serviceId" }
    return map { if (it.id == serviceId) transform(it) else it }
}
