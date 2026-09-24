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
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

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

/** Keeps list and lazily loaded body in memory for this application session only. */
internal class ParticipatedWorkItemsController(
    private val source: ParticipatedWorkItemsSource,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
) {
    var state by mutableStateOf(ParticipatedWorkItemsUiState())
        private set
    var selectedKey by mutableStateOf<String?>(null)
        private set
    var bodyState by mutableStateOf<ParticipatedWorkItemBodyState>(ParticipatedWorkItemBodyState.Idle)
        private set

    private var projectKeys: List<String>? = null
    private var defaultProjectKey: String? = null
    private var listGeneration = 0L
    private var bodyGeneration = 0L
    private var listJob: Job? = null
    private val bodyCache = mutableMapOf<String, String>()

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
        if (!force && sameSelection && sameDefaultProject && (state.initialized || state.loading)) return
        projectKeys = signature
        defaultProjectKey = defaultSprintProjectKey
        val generation = ++listGeneration
        listJob?.cancel()
        val oldItems = if (sameSelection) state.items else emptyList()
        if (!sameSelection) clearSelection()
        state = state.copy(
            items = oldItems,
            loading = true,
            error = null,
            warning = null,
            sprints = if (sameProjects) state.sprints else emptyList(),
            selectedSprintKey = requestedSprintKey,
        )
        listJob = scope.launch {
            val result = try {
                withContext(ioDispatcher) { source.load(snapshot, requestedSprintKey, defaultSprintProjectKey) }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                ParticipatedWorkItemsResult(emptyList(), listOf(error.message.orEmpty().ifBlank { "查询失败" }))
            }
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
            if (!hasIncompleteResult) bodyCache.clear()
            val next = items.firstOrNull { it.key == selectedKey } ?: items.firstOrNull()
            if (next == null) clearSelection()
            else select(next, forceBody = !hasIncompleteResult && force)
        }
    }

    private fun clearSelection() {
        bodyGeneration++
        bodyCache.clear()
        selectedKey = null
        bodyState = ParticipatedWorkItemBodyState.Idle
    }

    fun select(item: ParticipatedWorkItem, forceBody: Boolean = false) {
        if (selectedKey == item.key && !forceBody && bodyState !is ParticipatedWorkItemBodyState.Idle) return
        selectedKey = item.key
        val generation = ++bodyGeneration
        if (!forceBody) bodyCache[item.key]?.let {
            bodyState = ParticipatedWorkItemBodyState.Ready(item.key, it)
            return
        }
        bodyState = ParticipatedWorkItemBodyState.Loading(item.key)
        scope.launch {
            val result = try {
                Result.success(withContext(ioDispatcher) { source.loadBody(item).orEmpty() })
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                Result.failure(error)
            }
            if (generation != bodyGeneration || selectedKey != item.key) return@launch
            result.fold(
                onSuccess = { content ->
                    bodyCache[item.key] = content
                    bodyState = ParticipatedWorkItemBodyState.Ready(item.key, content)
                },
                onFailure = { error ->
                    bodyState = ParticipatedWorkItemBodyState.Failed(
                        item.key,
                        error.message.orEmpty().ifBlank { "正文读取失败" },
                    )
                },
            )
        }
    }
}
