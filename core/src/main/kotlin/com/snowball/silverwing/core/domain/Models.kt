@file:OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)

package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName
import kotlinx.serialization.json.JsonNames
import java.net.URI

/** Persisted data uses explicit strict schema versions independent of the product build version. */
const val CURRENT_PRODUCT_VERSION = "2.0.1"
const val CURRENT_APP_CONFIG_SCHEMA_VERSION = "2.0.0"
const val CURRENT_TASK_MANIFEST_SCHEMA_VERSION = "2.0.0"
const val DEFAULT_GROUP_ID = "default"
const val DEFAULT_GROUP_NAME = "默认组"
const val DEFAULT_TAG_HISTORY_MAX_GROUPS = 20
const val MAX_TAG_HISTORY_GROUPS = 1000

@Serializable
enum class DevelopmentToolType {
    INTELLIJ_IDEA,
    WEBSTORM,
    PYCHARM,
    VISUAL_STUDIO_CODE,
    ANDROID_STUDIO,
    DEVECO_STUDIO,
}

@Serializable
data class DevelopmentToolConfig(
    val type: DevelopmentToolType,
    val path: String,
) {
    init {
        require(path.isNotBlank()) { "开发工具路径不能为空" }
    }
}

@Serializable
enum class ThemePreference {
    SYSTEM,
    LIGHT,
    DARK,
}

/** One explicitly configured Feishu Project space used to discover requirement links. */
@Serializable
data class MeegleProjectConfig(
    @SerialName("project_key")
    val projectKey: String,
    @SerialName("simple_name")
    val simpleName: String,
) {
    init {
        require(projectKey.matches(Regex("[A-Za-z0-9_-]+"))) { "Meegle 空间 Key 只能包含字母、数字、下划线和连字符" }
        require(simpleName.matches(Regex("[A-Za-z0-9_-]+"))) { "Meegle 空间短名只能包含字母、数字、下划线和连字符" }
    }
}

/** A remote Git Marketplace source registered with the local Codex CLI. */
@Serializable
data class CodexPluginMarketplaceSource(
    val id: String,
    val name: String,
    val repositoryUrl: String,
    val ref: String? = null,
    /** Marketplace sources only support the repository root; legacy child paths need migration. */
    val marketplaceDirectory: String = "plugins",
) {
    init {
        validateExtensionSourceId(id, "插件来源")
        require(name.isNotBlank() && name == name.trim()) { "插件来源名称不能为空且不能包含首尾空格" }
        validateRemoteGitUrl(repositoryUrl)
        validateGitRef(ref)
        validateExtensionRelativePath(marketplaceDirectory, "插件 Marketplace 目录")
    }
}

/** A configured Marketplace stored with a child directory from an older SilverWing release. */
fun CodexPluginMarketplaceSource.requiresRootMarketplaceMigration(): Boolean = marketplaceDirectory != "."

/** A remote Git repository from which standalone Codex Skills can be installed. */
@Serializable
data class SkillSource(
    val id: String,
    val name: String,
    val repositoryUrl: String,
    val ref: String? = null,
    /** Null means discover both `.agents/skills` and `skills` at the repository root. */
    val skillRoot: String? = null,
) {
    init {
        validateExtensionSourceId(id, "Skill 来源")
        require(name.isNotBlank() && name == name.trim()) { "Skill 来源名称不能为空且不能包含首尾空格" }
        validateRemoteGitUrl(repositoryUrl)
        validateGitRef(ref)
        skillRoot?.let { validateExtensionRelativePath(it, "Skill 目录") }
    }
}

fun validateRemoteGitUrl(value: String): String {
    val normalized = value.trim()
    require(normalized == value && normalized.isNotBlank()) { "Git 仓库地址不能为空且不能包含首尾空格" }
    require(normalized.none(Char::isWhitespace)) { "Git 仓库地址不能包含空白字符" }
    if (normalized.matches(Regex("git@[A-Za-z0-9.-]+:[^\\s]+"))) return normalized
    val uri = runCatching { URI(normalized) }.getOrElse { throw IllegalArgumentException("Git 仓库地址格式不合法") }
    val scheme = uri.scheme?.lowercase()
    require(scheme == "http" || scheme == "https" || scheme == "ssh") { "仅支持 HTTP、HTTPS 或 SSH Git 仓库地址" }
    require(!uri.host.isNullOrBlank() && !uri.path.isNullOrBlank() && uri.path != "/") { "Git 仓库地址必须包含主机和仓库路径" }
    require(uri.rawQuery == null && uri.rawFragment == null) { "Git 仓库地址不能包含查询参数或片段" }
    require(scheme !in setOf("http", "https") || uri.rawUserInfo.isNullOrBlank()) { "HTTP(S) Git 仓库地址不能包含账号、Token 或密码" }
    require(scheme != "ssh" || uri.rawUserInfo.isNullOrBlank() || uri.rawUserInfo.matches(Regex("[A-Za-z0-9._-]+"))) {
        "SSH Git 仓库地址中的账号不合法"
    }
    return normalized
}

fun validateGitRef(value: String?): String? {
    if (value == null || value.isEmpty()) return null
    val normalized = value.trim()
    require(normalized == value) { "Git ref 不能包含首尾空格" }
    require(normalized.matches(Regex("[A-Za-z0-9][A-Za-z0-9._/@-]*"))) { "Git ref 不合法" }
    require(
        !normalized.contains("..") &&
            !normalized.contains("//") &&
            !normalized.contains("@{") &&
            !normalized.endsWith('.') &&
            !normalized.endsWith(".lock"),
    ) { "Git ref 不合法" }
    return normalized
}

fun validateExtensionRelativePath(value: String, label: String): String {
    val normalized = value.trim()
    require(normalized == value && normalized.isNotEmpty()) { "$label 不能为空且不能包含首尾空格" }
    require(!normalized.contains('\\') && !normalized.startsWith('/') && !normalized.contains(':')) { "$label 必须是仓库内相对路径" }
    require(normalized.split('/').all { it.isNotEmpty() && it != "." && it != ".." } || normalized == ".") {
        "$label 不能包含 .、.. 或空路径段"
    }
    return normalized
}

private fun validateExtensionSourceId(value: String, label: String) {
    require(value.matches(Regex("[A-Za-z0-9][A-Za-z0-9._-]{0,63}"))) { "$label ID 不合法" }
}

@Serializable
enum class WorkspaceStrategy {
    STANDARD_WORKTREE,
    INDEPENDENT_CLONE,
}

@Serializable
data class BootstrapCopyRule(
    val source: String,
    val target: String = source,
    val overwrite: Boolean = true,
)

@Serializable
data class BootstrapCommand(
    val name: String,
    val executable: String,
    val arguments: List<String> = emptyList(),
    val workingDirectory: String = ".",
    val timeoutSeconds: Long = 600,
    val enabled: Boolean = true,
)

@Serializable
data class BootstrapConfig(
    val copyRules: List<BootstrapCopyRule> = emptyList(),
    val commands: List<BootstrapCommand> = emptyList(),
)

/** One service-owned command exposed as a shortcut on its task workspaces. */
@Serializable
data class WorkspaceCommandConfig(
    val id: String,
    val name: String,
    val iconKey: String = "build",
    val executable: String,
    val arguments: List<String> = emptyList(),
    val workingDirectory: String = ".",
    val timeoutSeconds: Long = 1_800,
    val enabled: Boolean = true,
) {
    init {
        require(id.isNotBlank()) { "快捷命令 ID 不能为空" }
        require(name.isNotBlank()) { "快捷命令名称不能为空" }
        require(iconKey.isNotBlank()) { "快捷命令图标不能为空" }
        require(executable.isNotBlank()) { "快捷命令可执行程序不能为空" }
        require(timeoutSeconds > 0) { "快捷命令超时必须大于 0" }
    }
}

/** A persisted physical repository selected explicitly by the user. */
@Serializable
data class RepositoryConfig(
    val id: String,
    val name: String,
    val rootPath: String,
    val gitCommonDirectory: String,
    val originUrl: String? = null,
    val currentBranch: String? = null,
    val defaultRemoteBranch: String? = null,
) {
    init {
        require(id.isNotBlank()) { "仓库 ID 不能为空" }
        require(name.isNotBlank()) { "仓库名称不能为空" }
        require(rootPath.isNotBlank()) { "仓库根目录不能为空" }
        require(gitCommonDirectory.isNotBlank()) { "仓库 git-common-dir 不能为空" }
    }
}

/**
 * A standard service module maps one base ref to one independent writable worktree.
 * Modules never share a newly provisioned physical worktree, even when base refs match.
 */
@Serializable
enum class TagBuildMode {
    MERGE_TO_TARGET_BRANCH,
    CURRENT_BRANCH,
}

@Serializable
data class ServiceModuleConfig(
    val id: String,
    val name: String = "default",
    val baseRef: String = "origin/master",
    val baseRemote: String = "origin",
    val strategy: WorkspaceStrategy = WorkspaceStrategy.STANDARD_WORKTREE,
    @JsonNames("uatTagEnabled")
    val tagEnabled: Boolean = true,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    @JsonNames("uatRef")
    val tagTargetRef: String? = "origin/release/test",
    val tagMessagePrefix: String = "Tag",
    val customCommands: List<WorkspaceCommandConfig> = emptyList(),
) {
    init {
        require(id.isNotBlank()) { "模块 ID 不能为空" }
        require(baseRef.isNotBlank()) { "模块基础分支不能为空" }
        require(baseRemote.isNotBlank()) { "模块基础远程不能为空" }
        val parsedBase = RemoteBranchRef.parse(baseRef)
        require(parsedBase.remote == baseRemote) {
            "基础分支远程必须与基础远程一致：${parsedBase.remote} != $baseRemote"
        }
        if (tagEnabled && tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
            require(!tagTargetRef.isNullOrBlank()) { "合并到目标分支模式必须配置测试Tag目标分支" }
            RemoteBranchRef.parse(tagTargetRef)
        }
        require(customCommands.map { it.id.lowercase() }.distinct().size == customCommands.size) {
            "同一模块内快捷命令 ID 不能重复（忽略大小写）"
        }
    }
}

/** A repository's independently configurable membership inside one ordered group. */
@Serializable
data class GroupServiceConfig(
    val id: String,
    val repositoryId: String,
    val displayName: String,
    val enabled: Boolean = true,
    /** Enables read-only UAT build/release checks for this service's successful Tags. */
    val genbuProbeEnabled: Boolean = false,
    /** Genbu service name; defaults to the service display name used by existing configurations. */
    val genbuServiceName: String = displayName,
    val developmentTool: DevelopmentToolType = DevelopmentToolType.INTELLIJ_IDEA,
    val modules: List<ServiceModuleConfig> = listOf(
        ServiceModuleConfig(id = "default"),
    ),
    val bootstrap: BootstrapConfig = BootstrapConfig(),
    val commitMessageTemplate: String = "",
) {
    init {
        require(id.isNotBlank()) { "组内服务 ID 不能为空" }
        require(TaskNaming.directoryName(id) == id) { "组内服务 ID 必须是安全且稳定的目录片段" }
        require(repositoryId.isNotBlank()) { "仓库 ID 不能为空" }
        require(displayName.isNotBlank()) { "服务名称不能为空" }
        require(!genbuProbeEnabled || genbuServiceName.isNotBlank()) { "启用 Genbu 探测时必须配置 Genbu 服务名" }
        require(modules.isNotEmpty()) { "服务至少需要一个工作区模块" }
        require(modules.map { it.id.lowercase() }.distinct().size == modules.size) { "同一服务内模块 ID 不能重复（忽略大小写）" }
        StandardWorktreeModuleNaming.requireValid(modules)
    }

    companion object {
        fun standard(
            id: String,
            repositoryId: String,
            displayName: String,
            developmentTool: DevelopmentToolType = DevelopmentToolType.INTELLIJ_IDEA,
            baseRef: String = "origin/master",
        ): GroupServiceConfig = GroupServiceConfig(
            id = id,
            repositoryId = repositoryId,
            displayName = displayName,
            developmentTool = developmentTool,
            modules = listOf(ServiceModuleConfig(id = "default", baseRef = baseRef)),
        )
    }
}

@Serializable
data class GroupConfig(
    val id: String,
    val name: String,
    @JsonNames("uatTagEnabled")
    val tagEnabled: Boolean = true,
    val services: List<GroupServiceConfig> = emptyList(),
    val defaultBranchPrefix: String = "",
    val defaultWorkspaceToolIds: List<String> = emptyList(),
) {
    init {
        require(id.isNotBlank()) { "组 ID 不能为空" }
        require(id.matches(Regex("[A-Za-z0-9._-]+"))) { "组 ID 只能包含字母、数字、点、下划线和连字符" }
        require(TaskNaming.directoryName(id) == id) { "组 ID 必须是安全且稳定的目录片段" }
        require(name.isNotBlank()) { "组名称不能为空" }
        require(defaultBranchPrefix.none(Char::isWhitespace)) { "默认分支名前缀不能包含空白字符" }
        require(defaultWorkspaceToolIds.all(String::isNotBlank)) { "工作区工具 ID 不能为空" }
        require(defaultWorkspaceToolIds.distinct().size == defaultWorkspaceToolIds.size) {
            "同一组内的默认工作区工具不能重复"
        }
        require(services.map { it.repositoryId }.distinct().size == services.size) {
            "同一仓库在一个组内只能出现一次"
        }
        require(services.map { it.id }.distinct().size == services.size) { "同一组内服务 ID 不能重复" }
    }
}

/** Version 2.0.0 is an aggregate boundary; persisted runtime settings use strict shard schema 1. */
@Serializable
data class AppConfig(
    val schemaVersion: String = CURRENT_APP_CONFIG_SCHEMA_VERSION,
    val taskRoot: String? = null,
    val repositories: List<RepositoryConfig> = emptyList(),
    val groups: List<GroupConfig> = listOf(
        GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME),
    ),
    val theme: ThemePreference = ThemePreference.SYSTEM,
    /** Enables the desktop Tag page, Tag actions, and Tag operation entry points. */
    val tagEnabled: Boolean = true,
    /** Maximum number of grouped Tag history cards retained across all known tasks. */
    val tagHistoryMaxGroups: Int = DEFAULT_TAG_HISTORY_MAX_GROUPS,
    val terminalExecutable: String? = null,
    val developmentTools: List<DevelopmentToolConfig> = emptyList(),
    val defaultDevelopmentTool: DevelopmentToolType = DevelopmentToolType.INTELLIJ_IDEA,
    /** Shows temporary IDE selectors beside the normal default-tool open actions. */
    val allowTemporaryDevelopmentToolSelection: Boolean = false,
    /** Shows the batch Git action group in the task-detail toolbar. */
    val showTaskDetailGitActionGroup: Boolean = false,
    /** Shows the task-directory action group in the task-detail toolbar. */
    val showTaskDetailPathActionGroup: Boolean = false,
    /** Shows the Git action group in each Worktree card. */
    val showWorkspaceGitActionGroup: Boolean = false,
    /** Shows the directory action group in each Worktree card. */
    val showWorkspacePathActionGroup: Boolean = false,
    /** Compatibility fallback for the former single inline-copy visibility switch. */
    val showTaskAreaCopyIcons: Boolean = true,
    /** Shows inline branch-name copy affordances in task details and Worktree cards. */
    val showTaskAreaBranchCopyIcons: Boolean = true,
    /** Shows inline requirement-link and requirement-number copy affordances in task details. */
    val showTaskAreaRequirementCopyIcons: Boolean = true,
    /** Shows inline project-name copy affordances in Worktree cards. */
    val showTaskAreaProjectNameCopyIcons: Boolean = true,
    /** Exact local branch names on which SILVERWING refuses every commit or branch push. */
    val blockedGitWriteBranches: List<String> = listOf("master", "main"),
    val meegleProjects: List<MeegleProjectConfig> = emptyList(),
    /** Absolute path to the Meegle CLI executable; null means auto-detect. */
    val meegleExecutablePath: String? = null,
    /** Absolute path to the Git executable; null means auto-detect. */
    val gitExecutablePath: String? = null,
    /** Absolute path to the Genbu CLI executable; null means auto-detect. */
    val genbuExecutablePath: String? = null,
    /** True only when SILVERWING persisted the path found by automatic Genbu detection. */
    val genbuExecutableAutoDetected: Boolean = false,
    /** Absolute root for requirement research materials; null/blank disables the integration. */
    val requirementMaterialsRoot: String? = null,
    /** One safe child directory segment under each requirement directory; null/blank disables the integration. */
    val requirementMaterialsSubdirectory: String? = null,
    /** Codex Marketplace sources; runtime discovery/cache data remains outside exported configuration. */
    val codexPluginMarketplaceSources: List<CodexPluginMarketplaceSource> = emptyList(),
    /** Git sources providing standalone, user-scoped Codex Skills. */
    val skillSources: List<SkillSource> = emptyList(),
) {
    init {
        requirementMaterialsSubdirectory?.let(::validateRequirementMaterialsSubdirectory)
        require(groups.isNotEmpty()) { "至少需要一个组" }
        require(groups.map { it.id }.distinct().size == groups.size) { "组 ID 不能重复" }
        require(repositories.map { it.id }.distinct().size == repositories.size) { "仓库 ID 不能重复" }
        require(repositories.map { it.gitCommonDirectory.lowercase() }.distinct().size == repositories.size) {
            "同一 Git common directory 只能配置一次"
        }
        require(meegleProjects.map(MeegleProjectConfig::projectKey).distinct().size == meegleProjects.size) {
            "Meegle 空间 Key 不能重复"
        }
        require(codexPluginMarketplaceSources.map { it.id.lowercase() }.distinct().size == codexPluginMarketplaceSources.size) {
            "Codex 插件来源 ID 不能重复（忽略大小写）"
        }
        require(codexPluginMarketplaceSources.map { source ->
            extensionSourceKey(source.repositoryUrl, source.ref, source.marketplaceDirectory)
        }.distinct().size == codexPluginMarketplaceSources.size) {
            "Codex 插件来源不能重复（仓库、ref 和 Marketplace 目录相同）"
        }
        require(skillSources.map { it.id.lowercase() }.distinct().size == skillSources.size) {
            "Skill 来源 ID 不能重复（忽略大小写）"
        }
        require(skillSources.map { source ->
            extensionSourceKey(source.repositoryUrl, source.ref, source.skillRoot.orEmpty())
        }.distinct().size == skillSources.size) {
            "Skill 来源不能重复（仓库、ref 和 Skill 目录相同）"
        }
        require(tagHistoryMaxGroups in 1..MAX_TAG_HISTORY_GROUPS) {
            "Tag组保留数量必须在 1 到 $MAX_TAG_HISTORY_GROUPS 之间"
        }
        require(developmentTools.map(DevelopmentToolConfig::type).distinct().size == developmentTools.size) {
            "同一种开发工具只能配置一次"
        }
        require(blockedGitWriteBranches.all { it.isNotBlank() && it == it.trim() }) {
            "Git 写保护分支不能包含空值或首尾空格"
        }
        require(blockedGitWriteBranches.map(String::lowercase).distinct().size == blockedGitWriteBranches.size) {
            "Git 写保护分支不能重复（忽略大小写）"
        }
        val repositoryById = repositories.associateBy(RepositoryConfig::id)
        groups.flatMap(GroupConfig::services).forEach { service ->
            val repository = repositoryById[service.repositoryId]
                ?: throw IllegalArgumentException("服务 ${service.displayName} 引用了不存在的仓库：${service.repositoryId}")
            service.modules.forEach { module ->
                module.tagTargetRef?.let(RemoteBranchRef::parse)
            }
        }
    }

    fun group(groupId: String): GroupConfig =
        groups.firstOrNull { it.id == groupId }
            ?: throw IllegalArgumentException("找不到组：$groupId")

    fun groupService(groupId: String, serviceId: String): GroupServiceConfig =
        group(groupId).services.firstOrNull { it.id == serviceId }
            ?: throw IllegalArgumentException("组中找不到服务：$serviceId")

    /** True only when both user-provided material directory settings are non-blank. */
    val requirementMaterialsConfigured: Boolean
        get() = !requirementMaterialsRoot.isNullOrBlank() && !requirementMaterialsSubdirectory.isNullOrBlank()
}

private fun extensionSourceKey(repositoryUrl: String, ref: String?, path: String): String =
    listOf(repositoryUrl.removeSuffix("/").removeSuffix(".git").lowercase(), ref.orEmpty(), path).joinToString("\u0000")

private val WINDOWS_RESERVED_DIRECTORY_NAMES = buildSet {
    addAll(listOf("CON", "PRN", "AUX", "NUL"))
    addAll((1..9).map { "COM$it" })
    addAll((1..9).map { "LPT$it" })
}

/**
 * Validates one user-configured Windows directory segment.  Empty input is
 * accepted because clearing this setting deliberately disables the integration.
 * The returned value is trimmed so callers can persist the canonical segment.
 */
fun validateRequirementMaterialsSubdirectory(value: String): String {
    val normalized = value.trim()
    if (normalized.isEmpty()) return normalized
    require(normalized != "." && normalized != "..") { "需求资料子目录不能是 . 或 .." }
    require(normalized.none { it == '/' || it == '\\' }) { "需求资料子目录只能是单层目录名" }
    require(normalized.none { it.code < 0x20 || it in "<>:\"|?*" }) {
        "需求资料子目录包含 Windows 不允许的字符"
    }
    require(!normalized.endsWith('.') && !normalized.endsWith(' ')) {
        "需求资料子目录不能以点或空格结尾"
    }
    val reservedBase = normalized.substringBefore('.').uppercase()
    require(reservedBase !in WINDOWS_RESERVED_DIRECTORY_NAMES) {
        "需求资料子目录不能使用 Windows 保留名称"
    }
    return normalized
}

@Serializable
data class RepositoryInfo(
    val id: String,
    val name: String,
    val rootPath: String,
    val gitCommonDirectory: String,
    val remoteUrl: String? = null,
    val currentBranch: String? = null,
    val isBare: Boolean = false,
) {
    fun toConfig(): RepositoryConfig = RepositoryConfig(
        id = id,
        name = name,
        rootPath = rootPath,
        gitCommonDirectory = gitCommonDirectory,
        originUrl = remoteUrl,
        currentBranch = currentBranch,
        defaultRemoteBranch = null,
    )
}

fun RepositoryConfig.toInfo(): RepositoryInfo = RepositoryInfo(
    id = id,
    name = name,
    rootPath = rootPath,
    gitCommonDirectory = gitCommonDirectory,
    remoteUrl = originUrl,
    currentBranch = currentBranch,
)

@Serializable
enum class TaskLifecycleStatus {
    ACTIVE,
    ARCHIVED,
}

@Serializable
enum class WorkspaceHealth {
    CREATING,
    READY,
    READY_WITH_WARNINGS,
    FAILED,
}

/** Persisted availability of the requirement materials directory for one task. */
@Serializable
enum class RequirementMaterialsStatus {
    /** The task was intentionally created without a requirement reference. */
    NOT_REQUESTED,
    /** The independent materials directory is ready for use. */
    READY,
    /** Resolution or creation failed; the task may retry later without changing Git state. */
    FAILED,
}

@Serializable
data class RequirementMaterialsDirectory(
    val status: RequirementMaterialsStatus = RequirementMaterialsStatus.NOT_REQUESTED,
    val writeRoot: String? = null,
    val failureReason: String? = null,
)

@Serializable
enum class TaskModuleSource {
    CONFIGURED,
    TEMPORARY,
}

@Serializable
data class ServiceWorkspace(
    val repositoryId: String,
    val serviceName: String,
    val repositoryPath: String,
    val worktreePath: String,
    val developmentTool: DevelopmentToolType,
    val branch: String,
    val health: WorkspaceHealth = WorkspaceHealth.CREATING,
    val warnings: List<String> = emptyList(),
    val groupServiceId: String = repositoryId,
    val moduleId: String = "default",
    val moduleName: String = serviceName,
    val strategy: WorkspaceStrategy = WorkspaceStrategy.STANDARD_WORKTREE,
    val moduleSource: TaskModuleSource = TaskModuleSource.CONFIGURED,
    /** Origin is persisted for independent-clone restore; credentials are rejected by repository inspection. */
    val originUrl: String? = null,
    /** Base ref is diagnostic metadata for a standard module and is never re-derived during restore. */
    val baseRef: String? = null,
    /** Null means an independent clone works directly on its configured base branch. */
    val targetBranch: String? = branch,
    /** Immutable Tag behavior captured when the task module is created. */
    val tagEnabled: Boolean = false,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val tagTargetRef: String? = null,
    val tagMessagePrefix: String = "Tag",
    /** True only when this request created the local branch and may remove it during a failed transaction. */
    val branchCreatedByTask: Boolean = false,
    /** Reuses a branch checked out by another worktree and therefore needs `worktree add --force` on restore. */
    val forceWorktreeAttach: Boolean = false,
    /** Remote selected when the task was created and used when no upstream exists. */
    val pushRemote: String = "origin",
)

@Serializable
enum class AgentDocumentLayout {
    /** Historical metadata retained for persisted manifests; instruction files are always referenced. */
    INLINE_V1,

    /** New tasks keep the root AGENTS.md stable and reference split instruction files. */
    REFERENCED_V2,
}

@Serializable
data class TaskManifest(
    val schemaVersion: String = CURRENT_TASK_MANIFEST_SCHEMA_VERSION,
    val folderName: String,
    val taskDirectoryName: String,
    val featureBranch: String,
    val requirementLink: String = "",
    /** Parsed numeric requirement ID when the user supplied a number or a Feishu detail link. */
    val requirementId: String? = null,
    /** Independent directory for requirement notes, SQL and scripts; never contains SILVERWING Git worktrees. */
    val requirementMaterials: RequirementMaterialsDirectory = RequirementMaterialsDirectory(),
    val createdAt: String,
    val updatedAt: String,
    val lifecycleStatus: TaskLifecycleStatus = TaskLifecycleStatus.ACTIVE,
    val services: List<ServiceWorkspace>,
    val groupId: String = DEFAULT_GROUP_ID,
    val workspaceToolLaunches: List<WorkspaceToolLaunch> = emptyList(),
    /** Present only for tasks created through the guarded Agent CLI flow. */
    val agentContext: AgentTaskContext? = null,
    /** All newly created tasks use the stable root-router and split instruction files. */
    val agentDocumentLayout: AgentDocumentLayout = AgentDocumentLayout.REFERENCED_V2,
)

/**
 * Immutable facts injected into a CLI-created task. Its absence deliberately
 * means this is a normal desktop-created task and no handoff protocol applies.
 */
@Serializable
data class AgentTaskContext(
    val protocolVersion: String = "1",
    val documentationDirectory: String,
    val iterationLabel: String,
    val handoffRelativePath: String = ".workspace/HANDOFF.md",
) {
    init {
        require(documentationDirectory.isNotBlank()) { "需求过程文档目录不能为空" }
        require(iterationLabel.isNotBlank()) { "迭代名称不能为空" }
        require(handoffRelativePath == ".workspace/HANDOFF.md") { "交接文件路径必须是 .workspace/HANDOFF.md" }
    }
}

/** Task health is derived from its workspaces and is never persisted independently. */
val TaskManifest.health: WorkspaceHealth
    get() = aggregateWorkspaceHealth(services)

fun aggregateWorkspaceHealth(workspaces: List<ServiceWorkspace>): WorkspaceHealth = when {
    workspaces.any { it.health == WorkspaceHealth.FAILED } -> WorkspaceHealth.FAILED
    workspaces.any { it.health == WorkspaceHealth.CREATING } -> WorkspaceHealth.CREATING
    workspaces.any { it.health == WorkspaceHealth.READY_WITH_WARNINGS } -> WorkspaceHealth.READY_WITH_WARNINGS
    else -> WorkspaceHealth.READY
}

@Serializable
enum class WorkspaceToolLaunchStatus {
    PENDING,
    OPENED,
    FAILED,
}

/** Persisted result of opening one external development tool for a task workspace. */
@Serializable
data class WorkspaceToolLaunch(
    val toolId: String,
    val status: WorkspaceToolLaunchStatus,
    val updatedAt: String,
    val message: String? = null,
) {
    init {
        require(toolId.isNotBlank()) { "工作区工具 ID 不能为空" }
    }
}

@Serializable
enum class TagOperationState {
    CREATED,
    PREFLIGHT_PASSED,
    SOURCE_BRANCH_PUSHED,
    TARGET_BRANCH_PUSHED,
    LOCAL_TAG_CREATED,
    TAG_PUSHED,
    SUCCESS,
    CONFLICT,
    FAILED,
    PARTIAL,
    /** Read-only compatibility for early 0.7 operation files. */
    @Deprecated("Use SOURCE_BRANCH_PUSHED")
    FEATURE_PUSHED,
    /** Read-only compatibility for early 0.7 operation files. */
    @Deprecated("Use TARGET_BRANCH_PUSHED")
    TEST_BRANCH_PUSHED,
}

@Serializable
data class TagOperation(
    val operationId: String,
    val folderName: String,
    val serviceName: String,
    val repositoryId: String,
    @JsonNames("featureBranch")
    val sourceBranch: String,
    @JsonNames("testBranch")
    val targetBranch: String? = null,
    val remote: String,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val state: TagOperationState,
    val createdAt: String,
    val updatedAt: String,
    @JsonNames("featureSha")
    val sourceSha: String? = null,
    @JsonNames("testSha")
    val targetSha: String? = null,
    val tag: String? = null,
    val message: String? = null,
    val conflictFiles: List<String> = emptyList(),
    val groupServiceId: String = repositoryId,
    val moduleId: String = "default",
    /** Read-only Genbu build and release results kept separately from the local Git Tag state. */
    val genbuStatus: GenbuTagProbeStatus = GenbuTagProbeStatus(),
    /** Identifies the batch that created this operation; null for an individual Tag build. */
    val batchId: String? = null,
)

/** Genbu pipeline stage result mapped from `genbu query-tag --json`. */
@Serializable
enum class GenbuStageStatus {
    /** The pipeline record exists but has not run yet. */
    INITIAL,

    /** The Tag build pipeline is still running. */
    BUILDING,
    SUCCESS,
    FAILED,

    /** Missing step or an unrecognized status returned by the CLI. */
    UNKNOWN,
}

@Serializable
data class GenbuTagProbeStatus(
    val build: GenbuStageStatus = GenbuStageStatus.UNKNOWN,
    /** UAT release result returned by `genbu query-tag`. */
    val uat: GenbuStageStatus = GenbuStageStatus.UNKNOWN,
    /** Production release result returned by pipeline step=9. */
    val production: GenbuStageStatus = GenbuStageStatus.UNKNOWN,
    /** The Genbu CLI confirmed this exact Tag does not exist. */
    val notFound: Boolean = false,
    val builtCompletedAt: String? = null,
    val releasedCompletedAt: String? = null,
    val productionReleasedCompletedAt: String? = null,
    val checkedAt: String? = null,
    val failureReason: String? = null,
    /** A later Tag for the same Genbu service was released, so this older Tag is no longer polled. */
    val stoppedByNewerRelease: Boolean = false,
)

@Serializable
data class TagBuildHistoryEntry(
    val operationId: String,
    val timestamp: String,
    val folderName: String,
    val serviceName: String,
    @JsonNames("featureBranch")
    val sourceBranch: String,
    @JsonNames("testBranch")
    val targetBranch: String? = null,
    val tagMode: TagBuildMode = TagBuildMode.MERGE_TO_TARGET_BRANCH,
    val tag: String? = null,
    val state: TagOperationState,
    val message: String? = null,
    /** Identifies the batch that created this history entry; null for an individual build. */
    val batchId: String? = null,
)

/**
 * One card in the Tag history view. Operations with the same non-null batch ID
 * share a card; a legacy or single operation with no batch ID gets a one-item
 * card keyed by its operation ID. The nullable batch ID lets the UI preserve
 * the distinction without changing persisted operation JSON.
 */
data class TagHistoryItem(
    val groupId: String,
    val batchId: String?,
    val folderName: String,
    val createdAt: String,
    val updatedAt: String,
    val operations: List<TagOperation>,
)

/** A reusable preset for the per-task AGENTS.md notes section. */
@Serializable
data class AgentTaskTemplate(
    val id: String,
    val name: String,
    val content: String,
    val updatedAt: String,
) {
    init {
        require(name.isNotBlank()) { "模板名称不能为空" }
        require(content.isNotBlank()) { "模板内容不能为空" }
    }
}

@Serializable
data class CommandResult(
    val exitCode: Int,
    val stdout: String,
    val stderr: String,
) {
    val succeeded: Boolean get() = exitCode == 0
}
