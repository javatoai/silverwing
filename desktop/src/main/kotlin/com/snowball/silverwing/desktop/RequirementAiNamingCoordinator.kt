package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit

internal data class NamingFailure(val key: String, val title: String, val message: String)
internal data class NamingSuggestionResult(val suggestion: RequirementAiNamingSuggestion, val cached: Boolean)
internal data class NamingPreparation(val completed: Int = 0, val total: Int = 0, val failures: Int = 0,
    val running: Boolean = false, val paused: Boolean = false, val failedItems: List<NamingFailure> = emptyList(),
    val retrying: Set<String> = emptySet()) {
    val label: String get() = (if (paused) "命名预热已暂停 · " else "") + "命名已准备 $completed/$total" + if (failures > 0) " · 失败 $failures" else ""
}

/** 会话共享请求，创建页只取消订阅；预热和失败重试串行，给前台保留另一个处理通道。 */
internal class RequirementAiNamingCoordinator(
    private val scope: CoroutineScope,
    private val cache: RequirementAiNamingCache,
    private val contexts: RequirementAiContextProvider,
    private val naming: RequirementAiNamingService,
    private val model: () -> String,
    private val dispatcher: CoroutineDispatcher = Dispatchers.IO,
    private val cachedBody: (ParticipatedWorkItem) -> String? = { null },
    private val readBody: (suspend (ParticipatedWorkItem, Boolean) -> String)? = null,
    private val lookupItem: (String) -> ParticipatedWorkItem? = { null },
) {
    private data class RequestKey(val identity: String, val model: String)
    private data class SharedRequest(val force: Boolean, val forbidden: Set<String>, val result: Deferred<NamingSuggestionResult>)
    // Failures cannot cancel the desktop scope; completion owns cleanup even when subscribers leave.
    private val requestLock = Any()
    private val slots = Semaphore(2)
    private val backgroundSlot = Semaphore(1)
    private val requests = mutableMapOf<RequestKey, SharedRequest>()
    internal val inFlightCount: Int get() = synchronized(requestLock) { requests.size }
    private var preparationJob: Job? = null
    private val preparation = MutableStateFlow(NamingPreparation())
    val state = preparation.asStateFlow()

    suspend fun suggest(link: String, projectKey: String?, force: Boolean = false,
        forbidden: Set<String> = emptySet(), item: ParticipatedWorkItem? = null, background: Boolean = false): RequirementAiNamingSuggestion =
        suggestWithSource(link, projectKey, force, forbidden, item, background).suggestion

    suspend fun suggestWithSource(link: String, projectKey: String?, force: Boolean = false,
        forbidden: Set<String> = emptySet(), item: ParticipatedWorkItem? = null, background: Boolean = false): NamingSuggestionResult {
        val parsed = requireNotNull(FeishuWorkItemLink.parse(link)) { "仅支持飞书需求链接" }
        val identity = "${projectKey ?: parsed.projectKey}:${parsed.kind}:${parsed.workItemId}"
        val selectedModel = RequirementAiNamingModel.requireValid(model())
        val key = RequestKey(identity, selectedModel)
        val excludedNames = forbidden.toSet()
        val needsFresh = force || excludedNames.isNotEmpty()
        while (true) {
            currentCoroutineContext().ensureActive()
            // Join an active refresh before consulting its older cached result.
            if (synchronized(requestLock) { requests[key] } == null && !needsFresh) {
                withContext(dispatcher) { cache.read(identity, selectedModel) }?.let { return NamingSuggestionResult(it, true) }
            }
            val request = synchronized(requestLock) {
                requests[key] ?: createRequest(key, link, projectKey, needsFresh, excludedNames, item, background)
            }
            if (!needsFresh || (request.force && request.forbidden == excludedNames)) return request.result.await()
            // Different collision histories cannot share a result. Identical refreshes can.
            try { request.result.await() }
            catch (cancelled: CancellationException) { currentCoroutineContext().ensureActive() }
            catch (_: Exception) { }
        }
    }

    /** Called under requestLock. Register before starting so fast completions cannot leak map entries. */
    private fun createRequest(key: RequestKey, link: String, projectKey: String?, force: Boolean,
        forbidden: Set<String>, item: ParticipatedWorkItem?, background: Boolean): SharedRequest {
        val parent = SupervisorJob(scope.coroutineContext[Job])
        val result = scope.async(dispatcher + parent, start = CoroutineStart.LAZY) {
            backgroundPermit(background) { slots.withPermit {
                if (!force) cache.read(key.identity, key.model)?.let { return@withPermit NamingSuggestionResult(it, true) }
                val summary = item ?: lookupItem(link)
                val body = summary?.let { selected -> if (force) readBody?.invoke(selected, true)
                    else cachedBody(selected) ?: readBody?.invoke(selected, false) }
                val context = if (body != null) RequirementAiContext(summary!!.title,
                    RequirementAiContext.truncateBody(body.lineSequence().map(String::trim).filter(String::isNotBlank).joinToString("\n")))
                    else runInterruptible { contexts.fetch(link, projectKey) }
                val suggestion = runInterruptible { naming.suggestWithModel(context, forbidden, key.model) }
                RequirementAiNamingRules.requireValid(suggestion)
                if (model() == key.model) cache.write(key.identity, key.model, suggestion)
                NamingSuggestionResult(suggestion, false)
            } }
        }
        val shared = SharedRequest(force, forbidden, result)
        requests[key] = shared
        result.invokeOnCompletion {
            synchronized(requestLock) { if (requests[key] === shared) requests.remove(key) }
            parent.complete()
        }
        result.start()
        return shared
    }

    private val queueLock = Any()
    private val pending = linkedMapOf<String, ParticipatedWorkItem>()
    private val failed = linkedMapOf<String, Pair<ParticipatedWorkItem, String>>()
    private val completedKeys = mutableSetOf<String>()
    private val retrying = mutableSetOf<String>()
    private var epoch = 0L
    private var active: String? = null
    private var priorityKey: String? = null

    private fun publish(running: Boolean = preparation.value.running, paused: Boolean = preparation.value.paused) {
        preparation.value = preparation.value.copy(completed = completedKeys.size, failures = failed.size,
            failedItems = failed.map { (key, value) -> NamingFailure(key, value.first.title, value.second) },
            retrying = retrying.toSet(), running = running, paused = paused)
    }
    fun prewarm(items: List<ParticipatedWorkItem>, enabled: Boolean) = synchronized(queueLock) {
        val keepPaused = enabled && preparation.value.paused
        epoch++; preparationJob?.cancel(); preparationJob = null; active = null
        pending.clear(); failed.clear(); completedKeys.clear(); retrying.clear()
        val unique = if (enabled) items.distinctBy { it.key }.filter { FeishuWorkItemLink.parse(it.url) != null } else emptyList()
        unique.forEach { pending[it.key] = it }
        preparation.value = NamingPreparation(total = unique.size, paused = keepPaused)
        priorityKey?.let(::promote)
        startWorker()
    }
    private fun promote(key: String) {
        val item = pending.remove(key) ?: return
        val remaining = pending.toMap(); pending.clear(); pending[key] = item; pending.putAll(remaining)
    }
    fun prioritize(item: ParticipatedWorkItem) = synchronized(queueLock) { priorityKey = item.key; promote(item.key) }
    fun pause() = synchronized(queueLock) { publish(paused = true) }
    fun resume() = synchronized(queueLock) { publish(paused = false); startWorker() }
    private fun startWorker() {
        if (!scope.isActive || preparation.value.paused || pending.isEmpty() || preparationJob?.isActive == true) return
        val generation = epoch
        publish(running = true)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                while (true) {
                    val item = synchronized(queueLock) {
                        if (generation != epoch || preparation.value.paused) null
                        else pending.entries.firstOrNull()?.let { entry -> pending.remove(entry.key); active = entry.key; entry.value }
                    } ?: break
                    try {
                        suggest(item.url, item.projectKey, item = item, background = true)
                        synchronized(queueLock) { if (generation == epoch) { completedKeys += item.key; failed.remove(item.key); publish() } }
                    } catch (cancelled: CancellationException) {
                        currentCoroutineContext().ensureActive()
                        synchronized(queueLock) { if (generation == epoch) { failed[item.key] = item to failureMessage(cancelled); publish() } }
                    }
                    catch (error: Exception) {
                        synchronized(queueLock) { if (generation == epoch) {
                            failed[item.key] = item to failureMessage(error)
                            val systemic = error.message.orEmpty().let { text -> listOf("login", "auth", "登录", "认证", "找不到 Codex").any { text.contains(it, true) } }
                            publish(paused = preparation.value.paused || systemic)
                        } }
                    }
                    synchronized(queueLock) { if (generation == epoch) { active = null; retrying.remove(item.key); publish() } }
                }
            } finally { synchronized(queueLock) { if (generation == epoch) { active = null; preparationJob = null; publish(running = false); startWorker() } } }
        }
        preparationJob = job
        job.start()
    }
    fun retry(key: String) = synchronized(queueLock) {
        val item = failed[key]?.first ?: return@synchronized
        if (active == key || key in pending || !retrying.add(key)) return@synchronized
        pending[key] = item
        promote(key)
        publish(paused = false)
        startWorker()
    }
    fun retryFailed() = synchronized(queueLock) {
        failed.forEach { (key, value) -> if (active != key && key !in pending && retrying.add(key)) pending[key] = value.first }
        priorityKey?.let(::promote)
        publish(paused = false); startWorker()
    }
    private fun failureMessage(error: Exception): String = error.message.orEmpty().lineSequence().firstOrNull().orEmpty()
        .replace(Regex("(https?://)[^/\\s]*@"), "$1[已隐藏]@").take(200).ifBlank { "命名生成失败" }

    private suspend fun <T> backgroundPermit(background: Boolean, block: suspend () -> T): T =
        if (background) backgroundSlot.withPermit { block() } else block()

    fun cancelPrewarm() = synchronized(queueLock) {
        epoch++; preparationJob?.cancel(); preparationJob = null; active = null
        pending.clear(); failed.clear(); completedKeys.clear(); retrying.clear()
        preparation.value = NamingPreparation()
    }
}
