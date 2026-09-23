package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsResult
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal data class ParticipatedWorkItemsUiState(
    val items: List<ParticipatedWorkItem> = emptyList(),
    val loading: Boolean = false,
    val initialized: Boolean = false,
    val warning: String? = null,
    val error: String? = null,
)

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
    private var listGeneration = 0L
    private var bodyGeneration = 0L
    private val bodyCache = mutableMapOf<String, String>()

    fun load(projects: List<MeegleProjectConfig>, force: Boolean = false) {
        val snapshot = projects.toList()
        val signature = snapshot.map { "${it.projectKey}:${it.simpleName}" }
        if (!force && signature == projectKeys && (state.initialized || state.loading)) return
        val sameProjects = signature == projectKeys
        projectKeys = signature
        val generation = ++listGeneration
        val oldItems = if (sameProjects) state.items else emptyList()
        if (!sameProjects) {
            bodyCache.clear()
            selectedKey = null
            bodyState = ParticipatedWorkItemBodyState.Idle
        }
        state = state.copy(items = oldItems, loading = true, error = null, warning = null)
        scope.launch {
            val result = try {
                withContext(ioDispatcher) { source.load(snapshot) }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                ParticipatedWorkItemsResult(emptyList(), listOf(error.message.orEmpty().ifBlank { "查询失败" }))
            }
            if (generation != listGeneration) return@launch
            if (result.completedQueries == 0 && result.failures.isNotEmpty()) {
                state = ParticipatedWorkItemsUiState(
                    items = oldItems,
                    initialized = true,
                    error = "读取需求列表失败${if (oldItems.isNotEmpty()) "；已保留上次结果" else ""}。${result.failures.first()}",
                )
                return@launch
            }
            val hasIncompleteResult = result.failures.isNotEmpty()
            val items = if (hasIncompleteResult && oldItems.isNotEmpty()) oldItems else result.items
            state = ParticipatedWorkItemsUiState(
                items = items,
                initialized = true,
                warning = if (hasIncompleteResult) {
                    "部分项目或类型读取失败，${if (oldItems.isNotEmpty()) "已保留上次结果" else "列表可能不完整"}。${result.failures.first()}"
                } else null,
            )
            if (!hasIncompleteResult) bodyCache.clear()
            val next = items.firstOrNull { it.key == selectedKey } ?: items.firstOrNull()
            if (next == null) {
                selectedKey = null
                bodyState = ParticipatedWorkItemBodyState.Idle
            } else select(next, forceBody = !hasIncompleteResult && force)
        }
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
