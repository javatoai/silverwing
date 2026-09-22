package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.BranchPrefixResolver
import com.snowball.silverwing.core.BranchReferenceValidator
import com.snowball.silverwing.core.FeishuWorkItemLink
import com.snowball.silverwing.core.GitBranchReferenceValidator
import com.snowball.silverwing.core.RequirementAiContextProvider
import com.snowball.silverwing.core.RequirementAiNamingRules
import com.snowball.silverwing.core.RequirementAiNamingService
import com.snowball.silverwing.core.RequirementAiNamingSuggestion
import com.snowball.silverwing.core.RequirementMetadata
import com.snowball.silverwing.core.RequirementMetadataProvider
import com.snowball.silverwing.core.MeegleRequirementLinkSource
import com.snowball.silverwing.core.RequirementLinkCandidate
import com.snowball.silverwing.core.RequirementLinkFailure
import com.snowball.silverwing.core.RequirementLinkFailureLog
import com.snowball.silverwing.core.RequirementMaterialsRequest
import com.snowball.silverwing.core.RequirementMaterialsResult
import com.snowball.silverwing.core.RequirementMaterialsService
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskNaming
import com.snowball.silverwing.core.fetch
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Duration

/** Session-only requirement metadata state. It is never persisted into task JSON. */
sealed interface RequirementUiState {
    data object NotLoaded : RequirementUiState
    data object Loading : RequirementUiState
    data class Loaded(val metadata: RequirementMetadata) : RequirementUiState
    data object Failed : RequirementUiState
}

private data class RequirementRequestKey(val projectKey: String?, val link: String)
internal sealed interface RequirementFetchResult {
    data class Success(val metadata: RequirementMetadata) : RequirementFetchResult
    data object Failure : RequirementFetchResult
}
private data class RequirementCacheEntry(val result: RequirementFetchResult, val expiresAtMillis: Long)

/** The non-blocking state of the optional requirement materials directory preview. */
sealed interface RequirementMaterialsPreviewState {
    data object Hidden : RequirementMaterialsPreviewState
    data object Loading : RequirementMaterialsPreviewState
    data class Ready(
        val requirementId: String,
        val path: String,
        val status: RequirementMaterialsResult.Ready.Status,
    ) : RequirementMaterialsPreviewState
    data class Failed(
        val requirementId: String?,
        val reason: String,
    ) : RequirementMaterialsPreviewState
}

/** 可选本机 Codex 命名请求的会话级进度，不写入任何配置或任务文件。 */
sealed interface RequirementAiNamingUiState {
    data object Idle : RequirementAiNamingUiState
    data object LoadingContext : RequirementAiNamingUiState
    data class Generating(val attempt: Int) : RequirementAiNamingUiState
    data class Ready(val suggestion: RequirementAiNamingSuggestion) : RequirementAiNamingUiState
    data class Failed(val reason: String) : RequirementAiNamingUiState
}

/**
 * Deduplicates local Meegle calls and bounds concurrency. Failures intentionally
 * have a short cache window so an unavailable CLI cannot create a process storm.
 */
internal class RequirementMetadataCoordinator(
    private val provider: RequirementMetadataProvider,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val clock: Clock = Clock.systemUTC(),
    maxConcurrency: Int = 4,
    private val successTtl: Duration = Duration.ofMinutes(5),
    private val failureTtl: Duration = Duration.ofSeconds(30),
) {
    private val mutex = Mutex()
    private val semaphore = Semaphore(maxConcurrency)
    private val cache = mutableMapOf<RequirementRequestKey, RequirementCacheEntry>()
    private val inFlight = mutableMapOf<RequirementRequestKey, Deferred<RequirementFetchResult>>()

    suspend fun fetch(link: String, projectKey: String?, force: Boolean = false): RequirementFetchResult {
        val key = RequirementRequestKey(projectKey, link.trim())
        val now = clock.millis()
        val deferred = mutex.withLock {
            if (!force) {
                cache[key]?.takeIf { it.expiresAtMillis > now }?.let { return it.result }
            }
            inFlight[key] ?: scope.async(ioDispatcher) {
                semaphore.withPermit {
                    runCatching { provider.fetch(key.link, key.projectKey) }
                        .getOrNull()
                        ?.let(RequirementFetchResult::Success)
                        ?: RequirementFetchResult.Failure
                }
            }.also { inFlight[key] = it }
        }
        val result = deferred.await()
        mutex.withLock {
            if (inFlight[key] === deferred) inFlight.remove(key)
            val ttl = if (result is RequirementFetchResult.Success) successTtl else failureTtl
            cache[key] = RequirementCacheEntry(result, clock.millis() + ttl.toMillis())
        }
        return result
    }

    suspend fun clear() = mutex.withLock { cache.clear() }
}

/**
 * Owns all Meegle metadata presentation state. Task identity and generation are
 * checked before every write so late subprocess results cannot update another task.
 */
class RequirementController internal constructor(
    private val session: AppSessionStore,
    private val scope: CoroutineScope,
    private val coordinator: RequirementMetadataCoordinator,
    private val aiContextProvider: RequirementAiContextProvider? = null,
    private val aiNamingService: RequirementAiNamingService? = null,
    private val branchValidator: BranchReferenceValidator = GitBranchReferenceValidator(),
    private val linkSource: MeegleRequirementLinkSource? = null,
    private val failureLog: RequirementLinkFailureLog? = null,
    private val ioDispatcher: CoroutineDispatcher? = null,
    private val requirementMaterials: RequirementMaterialsService = RequirementMaterialsService(),
    private val materialPreviewer: (suspend (RequirementMaterialsRequest) -> RequirementMaterialsResult)? = null,
) {
    var states by mutableStateOf<Map<String, RequirementUiState>>(emptyMap())
        private set
    private val generations = mutableMapOf<String, Long>()
    private var draftJob: Job? = null
    private var draftGeneration = 0L
    private var aiNamingJob: Job? = null
    private var aiNamingGeneration = 0L
    var aiNamingState by mutableStateOf<RequirementAiNamingUiState>(RequirementAiNamingUiState.Idle)
        private set
    var candidates by mutableStateOf<List<RequirementLinkCandidate>>(emptyList())
        private set
    var candidatesLoading by mutableStateOf(false)
        private set
    var materialsPreviewState by mutableStateOf<RequirementMaterialsPreviewState>(RequirementMaterialsPreviewState.Hidden)
        private set
    private var materialsPreviewJob: Job? = null
    private var materialsPreviewGeneration = 0L

    fun stateFor(task: TaskManifest): RequirementUiState =
        states[task.taskDirectoryName] ?: RequirementUiState.NotLoaded

    fun loadedMetadataFor(task: TaskManifest): RequirementMetadata? =
        (stateFor(task) as? RequirementUiState.Loaded)?.metadata

    /** Resolves metadata for an action without changing the task-detail loading state. */
    fun fetchMetadata(task: TaskManifest, onResult: (RequirementMetadata?) -> Unit) {
        val parsed = FeishuWorkItemLink.parse(task.requirementLink)
        if (parsed == null) {
            onResult(null)
            return
        }
        val projectKey = projectKey(parsed, session.config)
        scope.launch {
            val result = coordinator.fetch(task.requirementLink, projectKey)
            if (result is RequirementFetchResult.Failure) recordMetadataFailure(projectKey)
            onResult((result as? RequirementFetchResult.Success)?.metadata)
        }
    }

    fun refreshAll(force: Boolean = false) {
        reconcileTasks()
        session.tasks.forEach { refresh(it, force) }
    }

    fun refresh(task: TaskManifest, force: Boolean = false) {
        val parsed = FeishuWorkItemLink.parse(task.requirementLink)
        if (parsed == null) {
            states = states + (task.taskDirectoryName to RequirementUiState.NotLoaded)
            return
        }
        val identity = task.taskDirectoryName
        val generation = (generations[identity] ?: 0L) + 1L
        generations[identity] = generation
        states = states + (identity to RequirementUiState.Loading)
        val link = task.requirementLink
        val projectKey = projectKey(parsed, session.config)
        scope.launch {
            val result = coordinator.fetch(link, projectKey, force)
            val stillCurrent = generations[identity] == generation &&
                session.tasks.any { it.taskDirectoryName == identity && it.requirementLink == link }
            if (!stillCurrent) return@launch
            states = states + (identity to when (result) {
                is RequirementFetchResult.Success -> RequirementUiState.Loaded(result.metadata)
                RequirementFetchResult.Failure -> {
                    recordMetadataFailure(projectKey)
                    RequirementUiState.Failed
                }
            })
        }
    }

    fun requestDraftMetadata(link: String, onResult: (RequirementMetadata?) -> Unit) {
        val generation = ++draftGeneration
        draftJob?.cancel()
        val parsed = FeishuWorkItemLink.parse(link)
        if (parsed == null) {
            onResult(null)
            return
        }
        val projectKey = projectKey(parsed, session.config)
        draftJob = scope.launch {
            delay(250)
            val result = coordinator.fetch(link, projectKey)
            if (generation != draftGeneration) return@launch
            if (result is RequirementFetchResult.Failure) recordMetadataFailure(projectKey)
            onResult((result as? RequirementFetchResult.Success)?.metadata)
        }
    }

    /**
     * Generates folder/branch suggestions only after the caller explicitly selected a candidate.
     * A generation token and job cancellation prevent a late Codex result from updating a newer
     * requirement selection.
     */
    fun requestDraftAiNaming(
        link: String,
        branchPrefix: String,
        enabled: Boolean,
        onResult: (RequirementAiNamingSuggestion) -> Unit,
    ) {
        val generation = ++aiNamingGeneration
        aiNamingJob?.cancel()
        aiNamingJob = null
        if (!enabled) {
            aiNamingState = RequirementAiNamingUiState.Idle
            return
        }
        val parsed = FeishuWorkItemLink.parse(link)
        val contextProvider = aiContextProvider
        val namingService = aiNamingService
        if (parsed == null) {
            aiNamingState = RequirementAiNamingUiState.Failed("仅支持飞书需求链接的 AI 命名")
            return
        }
        if (contextProvider == null || namingService == null) {
            aiNamingState = RequirementAiNamingUiState.Failed("本机 AI 命名服务不可用")
            return
        }
        val projectKey = projectKey(parsed, session.config)
        aiNamingState = RequirementAiNamingUiState.LoadingContext
        aiNamingJob = scope.launch {
            try {
                val context = withContext(ioDispatcher ?: Dispatchers.IO) {
                    runInterruptible { contextProvider.fetch(link, projectKey) }
                }
                if (generation != aiNamingGeneration) return@launch
                val forbiddenNames = linkedSetOf<String>()
                repeat(MAX_AI_NAMING_ATTEMPTS) { index ->
                    val attempt = index + 1
                    aiNamingState = RequirementAiNamingUiState.Generating(attempt)
                    val suggestion = withContext(ioDispatcher ?: Dispatchers.IO) {
                        // 重试历史在服务边界处必须是不可变快照，服务不能修改控制器状态，也不能
                        // 观察到稍后重试才加入的名称。
                        runInterruptible { namingService.suggest(context, forbiddenNames.toSet()) }
                    }
                    if (generation != aiNamingGeneration) return@launch
                    val available = withContext(ioDispatcher ?: Dispatchers.IO) {
                        runInterruptible {
                            val validated = RequirementAiNamingRules.requireValid(suggestion)
                            val resolvedPrefix = BranchPrefixResolver.resolve(branchPrefix, link) ?: branchPrefix
                            require(!BranchPrefixResolver.containsUnresolvedPlaceholder(resolvedPrefix)) {
                                "无法从需求链接解析分支前缀中的编号"
                            }
                            val branch = RequirementAiNamingRules.composeBranch(resolvedPrefix, validated.branchSuffix)
                            require(branchValidator.isValid(branch)) { "AI 生成的分支名不符合 Git 规则：$branch" }
                            !folderExists(validated.folderName)
                        }
                    }
                    if (generation != aiNamingGeneration) return@launch
                    if (available) {
                        aiNamingState = RequirementAiNamingUiState.Ready(suggestion)
                        onResult(suggestion)
                        return@launch
                    }
                    forbiddenNames += suggestion.folderName
                }
                aiNamingState = RequirementAiNamingUiState.Failed("AI 生成的文件夹名已存在，请手工修改或重新生成")
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                if (generation == aiNamingGeneration) {
                    aiNamingState = RequirementAiNamingUiState.Failed(
                        error.message ?: error::class.simpleName ?: "未知错误",
                    )
                }
            }
        }
    }

    /** Starts an optional, read-only materials-directory preview away from Compose's main thread. */
    fun requestMaterialsPreview(requirementInput: String, folderName: String) {
        val generation = ++materialsPreviewGeneration
        materialsPreviewJob?.cancel()
        val config = session.config
        val input = requirementInput.trim()
        val nameError = folderName.takeUnless(String::isBlank)?.let(TaskNaming::directoryNameValidationError)
        val inputsReady = input.isNotBlank() && folderName.isNotBlank() && nameError == null &&
            requirementMaterials.parseRequirementId(input) != null
        if (!config.requirementMaterialsConfigured || !inputsReady) {
            materialsPreviewState = RequirementMaterialsPreviewState.Hidden
            return
        }

        val request = RequirementMaterialsRequest(
            requirementInput = input,
            folderName = folderName,
            materialsRoot = config.requirementMaterialsRoot,
            subdirectoryName = config.requirementMaterialsSubdirectory,
            projects = config.meegleProjects,
        )
        materialsPreviewState = RequirementMaterialsPreviewState.Loading
        val preview = materialPreviewer ?: { value: RequirementMaterialsRequest -> requirementMaterials.preview(value) }
        materialsPreviewJob = scope.launch {
            val outcome = runCatching {
                delay(250)
                withContext(ioDispatcher ?: Dispatchers.IO) { preview(request) }
            }
            if (generation != materialsPreviewGeneration) return@launch
            materialsPreviewState = outcome.fold(
                onSuccess = ::toMaterialsPreviewState,
                onFailure = { error ->
                    RequirementMaterialsPreviewState.Failed(
                        requirementId = requirementMaterials.parseRequirementId(input),
                        reason = "需求资料目录预检失败：${error.message ?: error::class.simpleName}",
                    )
                },
            )
        }
    }

    /** Runs once when the create dialog opens; failures remain non-blocking. */
    fun loadCandidates() {
        val source = linkSource ?: return
        val dispatcher = ioDispatcher ?: return
        val projects = session.config.meegleProjects
        if (projects.isEmpty() || candidatesLoading) return
        candidatesLoading = true
        scope.launch {
            try {
                val result = kotlinx.coroutines.withContext(dispatcher) { source.load(projects) }
                result.failures.forEach { failureLog?.record(it) }
                candidates = result.candidates
            } catch (error: Exception) {
                failureLog?.record(
                    RequirementLinkFailure(
                        stage = "desktop-load",
                        message = error.message ?: error::class.simpleName.orEmpty(),
                    ),
                )
            } finally {
                candidatesLoading = false
            }
        }
    }

    fun onConfigurationChanged() {
        generations.keys.forEach { generations[it] = (generations[it] ?: 0L) + 1L }
        states = emptyMap()
        candidates = emptyList()
        clearMaterialsPreview()
        cancelDraftAiNaming()
        scope.launch { coordinator.clear() }
    }

    fun reconcileTasks() {
        val valid = session.tasks.map(TaskManifest::taskDirectoryName).toSet()
        generations.keys.retainAll(valid)
        states = states.filterKeys(valid::contains)
    }

    fun close() {
        draftJob?.cancel()
        cancelDraftAiNaming()
        clearMaterialsPreview()
    }

    /** 在切换需求、关闭窗口或关闭设置开关时中断本机 Codex 进程。 */
    fun cancelDraftAiNaming() {
        aiNamingGeneration++
        aiNamingJob?.cancel()
        aiNamingJob = null
        aiNamingState = RequirementAiNamingUiState.Idle
    }

    fun clearMaterialsPreview() {
        materialsPreviewGeneration++
        materialsPreviewJob?.cancel()
        materialsPreviewJob = null
        materialsPreviewState = RequirementMaterialsPreviewState.Hidden
    }

    private fun toMaterialsPreviewState(result: RequirementMaterialsResult): RequirementMaterialsPreviewState = when (result) {
        is RequirementMaterialsResult.Ready -> RequirementMaterialsPreviewState.Ready(
            requirementId = result.requirementId,
            path = result.writeRoot.toAbsolutePath().normalize().toString(),
            status = result.status,
        )
        is RequirementMaterialsResult.Failed -> RequirementMaterialsPreviewState.Failed(
            requirementId = result.requirementId,
            reason = result.reason,
        )
    }

    private fun projectKey(workItem: FeishuWorkItemLink, config: AppConfig): String? =
        config.meegleProjects.firstOrNull { it.simpleName.equals(workItem.space, ignoreCase = true) }
            ?.projectKey
            ?: workItem.projectKey

    private fun recordMetadataFailure(projectKey: String?) {
        failureLog?.record(
            RequirementLinkFailure(
                projectKey = projectKey,
                stage = "metadata",
                message = "Meegle metadata request failed",
            ),
        )
    }

    private fun folderExists(folderName: String): Boolean {
        val root = session.config.taskRoot?.takeIf(String::isNotBlank)?.let(Path::of) ?: return false
        return Files.exists(root.resolve(folderName))
    }

    private companion object {
        const val MAX_AI_NAMING_ATTEMPTS = 3
    }
}
