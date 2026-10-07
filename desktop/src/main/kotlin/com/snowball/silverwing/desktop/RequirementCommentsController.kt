package com.snowball.silverwing.desktop

import androidx.compose.runtime.*
import com.snowball.silverwing.core.ParticipatedWorkItem
import kotlinx.coroutines.*

internal class RequirementCommentsController(
    private val repository: RequirementCommentsRepository,
    private val scope: CoroutineScope,
    private val dispatcher: CoroutineDispatcher,
) {
    var document by mutableStateOf<CachedRequirementComments?>(null)
        private set
    var loading by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    private var generation = 0L
    private var job: Job? = null
    private var updates: Job? = null
    private var selectedIdentity: String? = null
    fun clear() {
        generation++; job?.cancel(); updates?.cancel(); document = null; error = null; loading = false
        selectedIdentity = null
    }
    fun select(item: ParticipatedWorkItem, force: Boolean = false) {
        val identity = requirementBodyIdentity(item)
        val old = document.takeIf { selectedIdentity == identity }
        clear()
        selectedIdentity = identity
        if (force) document = old
        val request = generation
        loading = true
        updates = scope.launch(start = CoroutineStart.UNDISPATCHED) {
            repository.updates.collect { result ->
                if (generation == request && document?.key == result.document.key) {
                    document = result.document; error = result.error
                }
            }
        }
        job = scope.launch {
            try {
                val result = repository.read(item, force, dispatcher)
                if (generation == request) { document = result.document; error = result.error }
            } catch (cancelled: CancellationException) {
                currentCoroutineContext().ensureActive()
                if (generation == request) error = "评论读取已取消，请重试"
            } catch (failure: Exception) {
                val saved = if (failure is RequirementIdentityChangedException) null
                    else try { repository.local(item, dispatcher) } catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) { null }
                if (generation == request) {
                    document = saved
                    error = if (saved != null) "刷新失败，已保留本地评论：${failure.message}" else "评论读取失败：${failure.message}"
                }
            } finally { if (generation == request) loading = false }
        }
    }
}
