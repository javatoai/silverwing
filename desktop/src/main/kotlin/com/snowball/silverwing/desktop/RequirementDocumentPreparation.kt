package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ParticipatedWorkItem
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

internal data class RequirementDocumentPreparationState(
    val total: Int = 0, val completed: Int = 0, val failures: Int = 0, val running: Boolean = false,
)

/** Cache every returned work item, independently of filters or AI naming settings. */
internal class RequirementDocumentPreparation(
    private val repository: RequirementBodyRepository,
    private val scope: CoroutineScope,
    private val dispatcher: CoroutineDispatcher,
) {
    private val mutableState = MutableStateFlow(RequirementDocumentPreparationState())
    val state = mutableState.asStateFlow()
    private val pending = LinkedHashMap<String, ParticipatedWorkItem>()
    private val failed = LinkedHashMap<String, ParticipatedWorkItem>()
    private val failedBodyRefreshes = mutableSetOf<String>()
    private val retryBodyRefreshes = mutableSetOf<String>()
    private var selected: String? = null
    private var generation = 0L
    private var job: Job? = null

    fun cancel() {
        generation++; job?.cancel(); job = null; pending.clear(); failed.clear()
        failedBodyRefreshes.clear(); retryBodyRefreshes.clear()
        mutableState.value = RequirementDocumentPreparationState()
    }
    fun prioritize(item: ParticipatedWorkItem) { selected = requirementBodyIdentity(item) }
    fun start(items: List<ParticipatedWorkItem>, force: Boolean) {
        cancel()
        items.forEach { pending[requirementBodyIdentity(it)] = it }
        mutableState.value = RequirementDocumentPreparationState(total = pending.size)
        run(force)
    }
    fun retryFailed() {
        if (mutableState.value.running || failed.isEmpty()) return
        pending.putAll(failed); failed.clear()
        retryBodyRefreshes.addAll(failedBodyRefreshes); failedBodyRefreshes.clear()
        mutableState.value = mutableState.value.copy(failures = 0)
        run(false)
    }
    private fun run(force: Boolean) {
        if (pending.isEmpty()) return
        val request = generation
        mutableState.value = mutableState.value.copy(running = true)
        job = scope.launch {
            try {
                coroutineScope {
                    repeat(2) {
                        launch {
                            while (request == generation && pending.isNotEmpty()) {
                                val key = selected?.takeIf { it in pending } ?: pending.keys.first()
                                val item = pending.remove(key) ?: continue
                                val refreshBody = force || retryBodyRefreshes.remove(key)
                                var retryBody = true
                                try {
                                    val result = repository.readDocument(item, refreshBody, dispatcher)
                                    retryBody = result.retryRefresh
                                    val document = result.document ?: error(result.cacheError ?: "正文尚未缓存")
                                    val imageResults = coroutineScope {
                                        document.imageUrls.map { url -> async {
                                            // Missing images and previous failures can be retried without re-reading the body.
                                            try { Result.success(repository.image(item, url, refreshBody || url in document.imageFailures, dispatcher, document)) }
                                            catch (cancelled: CancellationException) { currentCoroutineContext().ensureActive(); Result.failure(cancelled) }
                                            catch (error: Exception) { Result.failure(error) }
                                        } }.awaitAll()
                                    }
                                    val completed = repository.reload(document, dispatcher)
                                    check(completed?.complete == true && result.cacheError == null && imageResults.all { it.isSuccess }) { result.cacheError ?: "部分图片尚未缓存" }
                                    if (request == generation) mutableState.value = mutableState.value.copy(completed = mutableState.value.completed + 1)
                                } catch (cancelled: CancellationException) { currentCoroutineContext().ensureActive(); if (request == generation) failed[key] = item }
                                catch (_: Exception) { if (request == generation) failed[key] = item }
                                if (request == generation) {
                                    if (key in failed && retryBody) failedBodyRefreshes += key
                                    mutableState.value = mutableState.value.copy(failures = failed.size)
                                }
                            }
                        }
                    }
                }
            } finally { if (request == generation) mutableState.value = mutableState.value.copy(running = false) }
        }
    }
}
