package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.ParticipatedSprint
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsResult
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import java.math.BigDecimal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.nio.file.Path
import java.time.Clock
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

internal data class ParticipatedWorkItemsUiState(
    val items: List<ParticipatedWorkItem> = emptyList(),
    val loading: Boolean = false,
    val initialized: Boolean = false,
    val listComplete: Boolean = false,
    val warning: String? = null,
    val error: String? = null,
    val sprints: List<ParticipatedSprint> = emptyList(),
    val selectedSprintKey: String? = null,
) {
    val totalMySprintEstimateDays: BigDecimal?
        get() {
            if (!initialized || loading || !listComplete || error != null) return null
            return items.fold(BigDecimal.ZERO) { total, item ->
                total + (item.mySprintEstimateDays ?: return null)
            }
        }
}

internal sealed interface ParticipatedWorkItemBodyState {
    data object Idle : ParticipatedWorkItemBodyState
    data class Loading(val itemKey: String) : ParticipatedWorkItemBodyState
    data class Ready(val itemKey: String, val content: String) : ParticipatedWorkItemBodyState
    data class Failed(val itemKey: String, val message: String) : ParticipatedWorkItemBodyState
}

internal sealed interface ParticipatedWorkItemImageState {
    data object Loading : ParticipatedWorkItemImageState
    data class Loaded(val path: Path) : ParticipatedWorkItemImageState
    data class Failed(val message: String) : ParticipatedWorkItemImageState
}

/** The two readers share bodies; complete list snapshots are persisted with their query and account context. */
internal class ParticipatedWorkItemsController(
    private val source: ParticipatedWorkItemsSource,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val bodyRepository: RequirementBodyRepository = RequirementBodyRepository(source),
    private val onListLoaded: (List<ParticipatedWorkItem>) -> Unit = {},
    private val onSelected: (ParticipatedWorkItem) -> Unit = {},
    private val cacheAccess: RequirementCacheAccess? = null,
    private val clock: Clock = Clock.systemUTC(),
) {
    var state by mutableStateOf(ParticipatedWorkItemsUiState())
        private set
    var selectedKey by mutableStateOf<String?>(null)
        private set
    internal val bodyReader = RequirementBodyController(bodyRepository, scope, ioDispatcher)
    val bodyState get() = bodyReader.bodyState
    val bodyImageStates get() = bodyReader.bodyImageStates

    private var projectKeys: List<String>? = null
    private var defaultProjectKey: String? = null
    private var listGeneration = 0L
    private var listJob: Job? = null
    private var loadedIdentity: String? = null
    private var listFetchedAt: Long? = null

    fun load(
        projects: List<MeegleProjectConfig>,
        force: Boolean = false,
        sprintKey: String? = null,
        defaultSprintProjectKey: String? = null,
    ) {
        val snapshot = projects.toList()
        val signature = snapshot.map { "${it.projectKey}:${it.simpleName}" }
        val sameProjects = signature == projectKeys
        val sameDefaultProject = defaultSprintProjectKey == defaultProjectKey
        val requestedSprintKey = sprintKey ?: state.selectedSprintKey.takeIf { sameProjects }
        val sameSelection = sameProjects && requestedSprintKey == state.selectedSprintKey
        if (!force && sameSelection && sameDefaultProject &&
            (state.loading || (cacheAccess == null && state.initialized && state.error == null &&
                listFetchedAt?.let { RequirementReadCache.fresh(it, clock.millis()) } == true))) return
        projectKeys = signature
        defaultProjectKey = defaultSprintProjectKey
        val generation = ++listGeneration
        listJob?.cancel()
        var oldItems = if (sameSelection) state.items else emptyList()
        if (!sameSelection) clearSelection()
        state = state.copy(
            items = oldItems,
            loading = true,
            error = null,
            warning = null,
            sprints = if (sameProjects) state.sprints else emptyList(),
            selectedSprintKey = requestedSprintKey,
        )
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val result = try {
                val identity = if (cacheAccess == null) "session" else cacheAccess.identity(ioDispatcher)
                coroutineContext.ensureActive()
                if (generation != listGeneration) return@launch
                if (identity == null || identity != loadedIdentity) {
                    oldItems = emptyList()
                    clearSelection()
                    state = state.copy(items = emptyList(), sprints = emptyList())
                }
                loadedIdentity = identity
                val saved = withContext(ioDispatcher) {
                    readList(snapshot, requestedSprintKey, defaultSprintProjectKey, force, identity)
                }
                listFetchedAt = saved.fetchedAt
                saved.value
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                if (error is RequirementIdentityChangedException && generation == listGeneration) {
                    oldItems = emptyList()
                    clearSelection()
                    state = state.copy(items = emptyList(), sprints = emptyList())
                    loadedIdentity = null
                }
                ParticipatedWorkItemsResult(emptyList(), listOf(error.message.orEmpty().ifBlank { "查询失败" }))
            }
            coroutineContext.ensureActive()
            if (generation != listGeneration) return@launch
            val sprints = result.sprints ?: state.sprints
            val resolvedSprintKey = if (result.sprints == null) requestedSprintKey else result.selectedSprintKey
            val retainedItems = if (resolvedSprintKey == requestedSprintKey) oldItems else emptyList()
            if (resolvedSprintKey != requestedSprintKey) clearSelection()
            if (result.completedQueries == 0 && result.failures.isNotEmpty()) {
                state = ParticipatedWorkItemsUiState(
                    items = retainedItems,
                    initialized = true,
                    sprints = sprints,
                    selectedSprintKey = resolvedSprintKey,
                    error = "读取需求列表失败${if (retainedItems.isNotEmpty()) "；已保留上次结果" else ""}。${result.failures.first()}",
                )
                return@launch
            }
            val hasIncompleteResult = result.failures.isNotEmpty()
            val items = if (hasIncompleteResult && retainedItems.isNotEmpty()) retainedItems else result.items
            state = ParticipatedWorkItemsUiState(
                items = items,
                initialized = true,
                listComplete = !hasIncompleteResult && resolvedSprintKey != null,
                sprints = sprints,
                selectedSprintKey = resolvedSprintKey,
                warning = if (hasIncompleteResult) {
                    "部分项目或类型读取失败，${if (retainedItems.isNotEmpty()) "已保留上次结果" else "列表可能不完整"}。${result.failures.first()}"
                } else result.items.asSequence().flatMap { it.metadataWarnings.asSequence() }.firstOrNull()?.let {
                    "部分人员或估分读取失败。${it.lineSequence().first().trim().take(160)}"
                },
            )
            val next = items.firstOrNull { it.key == selectedKey } ?: items.firstOrNull()
            if (next == null) clearSelection()
            else select(next, forceBody = !hasIncompleteResult && force)
            onListLoaded(result.items)
        }
        listJob = job
        job.invokeOnCompletion { cause ->
            if (generation == listGeneration && listJob === job) {
                listJob = null
                // Completion also covers cancellation before the coroutine body starts.
                if (cause is CancellationException) {
                    state = state.copy(loading = false, initialized = false, listComplete = false)
                }
            }
        }
        job.start()
    }

    private suspend fun readList(
        projects: List<MeegleProjectConfig>, sprintKey: String?, defaultProject: String?, force: Boolean, identity: String?,
    ): RequirementCachedValue<ParticipatedWorkItemsResult> {
        // Source.load returns all pages for every supported type, so no hidden pagination/filter state is omitted.
        val projectsKey = Json.encodeToString(ListSerializer(MeegleProjectConfig.serializer()), projects)
        fun key(sprint: String?) = requirementReadKey("participated-list-all-pages-v1", identity.orEmpty(), projectsKey, sprint, defaultProject)
        val cache = cacheAccess?.cache?.takeIf { identity != null }
        if (!force) cache?.read(key(sprintKey), ParticipatedWorkItemsResult.serializer())?.let { return it }
        val result = source.load(projects, sprintKey, defaultProject)
        cacheAccess?.verify(identity, ioDispatcher)
        kotlinx.coroutines.currentCoroutineContext().ensureActive()
        val fetchedAt = clock.millis()
        if (result.failures.isEmpty() && result.items.all { it.metadataWarnings.isEmpty() }) {
            cache?.write(key(sprintKey), ParticipatedWorkItemsResult.serializer(), result, fetchedAt)
            // Auto-select and explicit selection of the same sprint are aliases of the same complete snapshot.
            if (sprintKey == null && result.selectedSprintKey != null) {
                cache?.write(key(result.selectedSprintKey), ParticipatedWorkItemsResult.serializer(), result, fetchedAt)
            } else if (force && cache?.read(key(null), ParticipatedWorkItemsResult.serializer())?.value?.selectedSprintKey == result.selectedSprintKey) {
                cache?.write(key(null), ParticipatedWorkItemsResult.serializer(), result, fetchedAt)
            }
        }
        return RequirementCachedValue(result, fetchedAt)
    }

    private fun clearSelection() {
        selectedKey = null
        bodyReader.clear()
    }

    fun select(item: ParticipatedWorkItem, forceBody: Boolean = false) {
        selectedKey = item.key
        onSelected(item)
        bodyReader.select(item, forceBody)
    }

    fun retryBodyImage(item: ParticipatedWorkItem, fileUrl: String) = bodyReader.retryBodyImage(item, fileUrl)
}
