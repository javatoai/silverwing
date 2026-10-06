package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.nio.charset.StandardCharsets
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import java.security.MessageDigest
import java.time.Clock
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

/** JSON request consumed by `silverwing agent plan --request <file>`. */
@Serializable
data class AgentCreateTaskRequest(
    val folderName: String,
    val featureBranch: String,
    val groupId: String,
    val serviceIds: List<String>,
    val requirementLink: String,
    /** A concise Chinese title used as the Markdown title in the requirement materials directory. */
    val requirementTitle: String? = null,
    val taskNotes: String = "",
    /** A self-contained Markdown handoff. A safe template is used when omitted. */
    val handoffMarkdown: String? = null,
    val confirmedBranchReuseKeys: List<AgentBranchReuseKey> = emptyList(),
) {
    init {
        require(folderName.isNotBlank()) { "任务目录名称不能为空" }
        require(featureBranch.isNotBlank()) { "任务分支不能为空" }
        require(groupId.isNotBlank()) { "任务组不能为空" }
        require(serviceIds.isNotEmpty()) { "至少选择一个服务" }
        require(requirementLink.isNotBlank()) { "Agent CLI 任务必须提供需求链接" }
    }
}

@Serializable
data class AgentBranchReuseKey(
    val repositoryId: String,
    val branch: String,
    val stateFingerprint: String? = null,
) {
    fun toDomain(): BranchReuseKey = BranchReuseKey(repositoryId, branch, stateFingerprint)
}

@Serializable
data class AgentBranchReuseConflict(
    val key: AgentBranchReuseKey,
    val serviceName: String,
    val moduleName: String,
    val localExists: Boolean,
    val remoteRefs: List<String>,
    val occupiedWorktreePaths: List<String>,
    val lockedWorktreePaths: List<String>,
    val requiresForceAttach: Boolean,
    val reusesRemoteCloneTarget: Boolean,
)

@Serializable
enum class AgentOperationState { PLANNED, APPLIED, FAILED, EXPIRED }

@Serializable
data class AgentOperationRecord(
    val operationId: String,
    val state: AgentOperationState,
    val nonce: String,
    val createdAt: String,
    val expiresAt: String,
    val fingerprint: String,
    val request: AgentCreateTaskRequest,
    val documentation: RequirementDocumentationPlan,
    val taskDirectory: String,
    val branchReuseConflicts: List<AgentBranchReuseConflict> = emptyList(),
    /** Set only after the caller presents a plan and supplies its nonce to apply. */
    val confirmedAt: String? = null,
    val handoffPath: String? = null,
    val message: String? = null,
)

@Serializable
data class AgentInspection(
    val taskRoot: String?,
    val requirementMaterialsRoot: String?,
    val requirementMaterialsSubdirectory: String?,
    val canPlan: Boolean,
    val configurationMessage: String? = null,
)

/** Narrow seam for testing; the production implementation delegates to the existing task transaction. */
interface AgentTaskOperations {
    fun inspectCreateBranchReuse(config: AppConfig, request: CreateGroupedTaskRequest): List<BranchReuseConflict>
    fun create(config: AppConfig, request: CreateGroupedTaskRequest): TaskManifest
}

class TaskApplicationAgentTaskOperations(
    private val tasks: TaskApplicationService = TaskApplicationService(),
) : AgentTaskOperations {
    override fun inspectCreateBranchReuse(config: AppConfig, request: CreateGroupedTaskRequest): List<BranchReuseConflict> =
        tasks.inspectCreateBranchReuse(config, request)

    override fun create(config: AppConfig, request: CreateGroupedTaskRequest): TaskManifest = tasks.create(config, request)
}

/**
 * Persisted plan/apply boundary for an agent-created task. `plan` is allowed to
 * inspect local Git/Meegle state; `apply` performs the filesystem/Git writes
 * only after it receives the exact nonce emitted by that plan.
 */
class AgentOperationService(
    private val configurations: ConfigurationRepository = AgentCompatibleConfigurationRepository(),
    private val documentation: RequirementDocumentationService = RequirementDocumentationService(),
    private val tasks: AgentTaskOperations = TaskApplicationAgentTaskOperations(),
    private val store: AgentOperationStore = AgentOperationStore(),
    private val clock: Clock = Clock.systemUTC(),
    private val json: Json = Json { encodeDefaults = true },
) {
    fun inspect(): AgentInspection {
        val config = configurations.load()
        val taskRoot = config.taskRoot
        val materialsRoot = config.requirementMaterialsRoot
        val materialsSubdirectory = config.requirementMaterialsSubdirectory
        val missing = buildList {
            if (taskRoot.isNullOrBlank()) add("任务根目录")
            if (materialsRoot.isNullOrBlank()) add("任务资料根目录")
            if (materialsSubdirectory.isNullOrBlank()) add("任务资料子目录")
        }
        return AgentInspection(
            taskRoot = taskRoot,
            requirementMaterialsRoot = materialsRoot,
            requirementMaterialsSubdirectory = materialsSubdirectory,
            canPlan = missing.isEmpty(),
            configurationMessage = missing.takeIf(List<String>::isNotEmpty)?.joinToString("、", postfix = "尚未配置"),
        )
    }

    fun plan(request: AgentCreateTaskRequest): AgentOperationRecord {
        val safeRequest = request.copy(handoffMarkdown = HandoffDocumentWriter.safeMarkdown(request.handoffMarkdown))
        val config = configurations.load()
        val preflight = preflight(config, safeRequest)
        val now = Instant.now(clock)
        val record = AgentOperationRecord(
            operationId = UUID.randomUUID().toString(),
            state = AgentOperationState.PLANNED,
            nonce = UUID.randomUUID().toString(),
            createdAt = now.toString(),
            expiresAt = now.plus(10, ChronoUnit.MINUTES).toString(),
            fingerprint = preflight.fingerprint,
            request = safeRequest,
            documentation = preflight.documentation,
            taskDirectory = preflight.taskDirectory.toString(),
            branchReuseConflicts = preflight.branchReuseConflicts,
            message = if (preflight.branchReuseConflicts.isEmpty()) {
                "计划已生成；向用户展示该计划后，使用 operationId 与 nonce 执行 apply。"
            } else {
                "检测到分支复用；只有用户明确确认输出中的完整 key 后，才能在新计划请求中提供 confirmedBranchReuseKeys。"
            },
        )
        store.save(record)
        return record
    }

    fun apply(operationId: String, nonce: String): AgentOperationRecord {
        return store.withOperationLock(operationId) {
            applyLocked(operationId, nonce)
        }
    }

    private fun applyLocked(operationId: String, nonce: String): AgentOperationRecord {
        val current = store.load(operationId)
        require(current.nonce == nonce) { "确认 nonce 不匹配；请使用 plan 返回的原值" }
        if (current.state == AgentOperationState.APPLIED) return current
        require(current.state == AgentOperationState.PLANNED) { "操作不处于可执行状态：${current.state}" }
        if (Instant.now(clock).isAfter(Instant.parse(current.expiresAt))) {
            val expired = current.copy(state = AgentOperationState.EXPIRED, message = "计划已过期，请重新生成计划")
            store.save(expired)
            throw IllegalStateException(expired.message)
        }
        return runCatching {
            val config = configurations.load()
            val fresh = preflight(config, current.request)
            require(fresh.fingerprint == current.fingerprint) {
                "环境指纹已变化（配置、Sprint/历史目录、目标路径或分支状态不同）；请重新生成计划并再次确认"
            }
            require(fresh.branchReuseConflicts.isEmpty()) {
                "仍存在未确认的分支复用；请重新生成计划，并将用户确认过的完整 key 写入 confirmedBranchReuseKeys"
            }
            val materialized = documentation.materialize(config, fresh.documentation)
            tasks.create(
                config,
                CreateGroupedTaskRequest(
                    folderName = current.request.folderName,
                    featureBranch = current.request.featureBranch,
                    groupId = current.request.groupId,
                    serviceIds = current.request.serviceIds,
                    requirementLink = current.request.requirementLink,
                    taskNotes = current.request.taskNotes,
                    confirmedBranchReuseKeys = current.request.confirmedBranchReuseKeys.map(AgentBranchReuseKey::toDomain).toSet(),
                    agentContext = materialized.agentContext,
                    agentHandoffMarkdown = current.request.handoffMarkdown,
                ),
            )
            current.copy(
                state = AgentOperationState.APPLIED,
                documentation = materialized.plan,
                confirmedAt = Instant.now(clock).toString(),
                handoffPath = Path.of(current.taskDirectory)
                    .resolve(HandoffDocumentWriter.DIRECTORY_NAME)
                    .resolve(HandoffDocumentWriter.FILE_NAME)
                    .toString(),
                message = "任务、AGENTS.md、${HandoffDocumentWriter.DIRECTORY_NAME}/${HandoffDocumentWriter.FILE_NAME} 与需求过程文档已创建；请重新读取状态后继续。",
            )
        }.onFailure { error ->
            val failed = current.copy(
                state = AgentOperationState.FAILED,
                confirmedAt = Instant.now(clock).toString(),
                message = error.message ?: error::class.simpleName ?: "执行失败",
            )
            store.save(failed)
        }.getOrThrow().also(store::save)
    }

    fun status(operationId: String): AgentOperationRecord = store.load(operationId)

    private fun preflight(config: AppConfig, request: AgentCreateTaskRequest): Preflight {
        val taskRoot = config.taskRoot?.let(Path::of)?.toAbsolutePath()?.normalize()
            ?: throw IllegalStateException("尚未配置任务根目录")
        val folder = TaskNaming.requireValidDirectoryName(request.folderName)
        val taskDirectory = taskRoot.resolve(folder).normalize()
        require(taskDirectory.parent == taskRoot) { "任务目录必须是任务根目录的直接子目录" }
        val documentationPlan = documentation.plan(
            config = config,
            requirementLink = request.requirementLink,
            requestedTitle = request.requirementTitle,
            directoryFolderName = folder,
        )
        val domainRequest = CreateGroupedTaskRequest(
            folderName = folder,
            featureBranch = request.featureBranch,
            groupId = request.groupId,
            serviceIds = request.serviceIds,
            requirementLink = request.requirementLink,
            taskNotes = request.taskNotes,
            confirmedBranchReuseKeys = request.confirmedBranchReuseKeys.map(AgentBranchReuseKey::toDomain).toSet(),
        )
        val conflicts = tasks.inspectCreateBranchReuse(config, domainRequest).map { conflict -> conflict.toAgentConflict() }
        val unconfirmed = conflicts.filter { conflict ->
            request.confirmedBranchReuseKeys.none { it == conflict.key }
        }
        val fingerprint = sha256(
            listOf(
                json.encodeToString(config),
                json.encodeToString(request),
                taskDirectory.toString(),
                "taskDirectoryExists=${taskDirectory.exists()}",
                json.encodeToString(conflicts),
            ).joinToString("\n"),
        )
        require(!taskDirectory.exists()) { "目标任务目录已存在：$taskDirectory" }
        return Preflight(documentationPlan, taskDirectory, fingerprint, unconfirmed)
    }

    private fun BranchReuseConflict.toAgentConflict(): AgentBranchReuseConflict = AgentBranchReuseConflict(
        key = AgentBranchReuseKey(key.repositoryId, key.branch, key.stateFingerprint),
        serviceName = serviceName,
        moduleName = moduleName,
        localExists = localExists,
        remoteRefs = remoteRefs,
        occupiedWorktreePaths = occupiedWorktreePaths,
        lockedWorktreePaths = lockedWorktreePaths,
        requiresForceAttach = requiresForceAttach,
        reusesRemoteCloneTarget = reusesRemoteCloneTarget,
    )

    private fun sha256(value: String): String = MessageDigest.getInstance("SHA-256")
        .digest(value.toByteArray(StandardCharsets.UTF_8))
        .joinToString("") { "%02x".format(it) }

    private data class Preflight(
        val documentation: RequirementDocumentationPlan,
        val taskDirectory: Path,
        val fingerprint: String,
        val branchReuseConflicts: List<AgentBranchReuseConflict>,
    )
}

/** File-backed audit record store. Its files intentionally live under silverwing's own home, not a task directory. */
class AgentOperationStore(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val json: Json = Json { prettyPrint = true; encodeDefaults = true },
) {
    private val directory: Path get() = paths.home.resolve("agent-operations")

    fun save(record: AgentOperationRecord) {
        require(record.operationId.matches(Regex("[0-9a-fA-F-]{36}"))) { "操作 ID 不合法" }
        directory.createDirectories()
        val target = path(record.operationId)
        val temporary = Files.createTempFile(directory, ".${record.operationId}-", ".tmp")
        Files.writeString(temporary, json.encodeToString(record), StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE)
        try {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
        }
    }

    fun load(operationId: String): AgentOperationRecord {
        require(operationId.matches(Regex("[0-9a-fA-F-]{36}"))) { "操作 ID 不合法" }
        val target = path(operationId)
        require(Files.isRegularFile(target)) { "找不到 Agent 操作：$operationId" }
        return runCatching { json.decodeFromString<AgentOperationRecord>(Files.readString(target)) }
            .getOrElse { throw IllegalStateException("Agent 操作记录损坏：$target", it) }
    }

    fun <T> withOperationLock(operationId: String, block: () -> T): T {
        require(operationId.matches(Regex("[0-9a-fA-F-]{36}"))) { "操作 ID 不合法" }
        return FileLocking.withExclusiveLock(
            paths.locks.resolve("agent-operation-$operationId.lock"),
            "Agent 操作正在被另一个执行器处理：$operationId",
            block,
        )
    }

    private fun path(operationId: String): Path = directory.resolve("$operationId.json")
}
