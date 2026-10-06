package com.snowball.silverwing.desktop

import androidx.compose.runtime.*
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.serialization.builtins.serializer
import java.time.Clock
import java.util.concurrent.ConcurrentHashMap

/** 正文结果共享，页面选中项和异步任务独立；不依赖需求列表的筛选或初始化。 */
internal class RequirementBodyRepository(
    private val source: ParticipatedWorkItemsSource,
    imageParallelism: Int = 4,
    private val bodyCacheEntries: Int = 64,
    private val bodyCacheCharacters: Int = 4_000_000,
    private val cacheAccess: RequirementCacheAccess? = null,
    private val clock: Clock = Clock.systemUTC(),
) {
    private val cacheGuard = Any()
    private val bodies = LinkedHashMap<String, RequirementCachedValue<String>>(16, 0.75f, true)
    private var cachedCharacters = 0L
    private var cacheGeneration = 0L
    private val locks = ConcurrentHashMap<String, Mutex>()
    private val imageLocks = ConcurrentHashMap<Pair<String, String>, Mutex>()
    private val imagePermits = Semaphore(imageParallelism)
    init { require(bodyCacheEntries > 0 && bodyCacheCharacters > 0) }

    suspend fun read(item: ParticipatedWorkItem, force: Boolean, dispatcher: CoroutineDispatcher): String {
        return locks.getOrPut(requirementBodyIdentity(item)) { Mutex() }.withLock {
            // Resolve after waiting for the previous read, since login may change while queued.
            val identity = if (cacheAccess == null) "session" else cacheAccess.identity(dispatcher)
            val key = requirementReadKey("body", identity.orEmpty(), requirementBodyIdentity(item))
            val (cached, requestGeneration) = synchronized(cacheGuard) {
                freshBody(key) to cacheGeneration
            }
            if (!force && identity != null) {
                if (cached != null) return@withLock cached.value
                val saved = withContext(dispatcher) { cacheAccess?.cache?.read(key, String.serializer()) }
                if (saved != null) {
                    synchronized(cacheGuard) { if (cacheGeneration == requestGeneration) cacheBody(key, saved) }
                    return@withLock saved.value
                }
            }
            val content = runInterruptible(dispatcher) { source.loadBody(item).orEmpty() }
            cacheAccess?.verify(identity, dispatcher)
            currentCoroutineContext().ensureActive()
            val fetchedAt = clock.millis()
            withContext(dispatcher) {
                synchronized(cacheGuard) {
                    // Refresh failure never removes a valid old value; publish only a completed read.
                    if (identity != null && cacheGeneration == requestGeneration) {
                        cacheAccess?.cache?.write(key, String.serializer(), content, fetchedAt)
                        cacheBody(key, RequirementCachedValue(content, fetchedAt))
                    }
                }
            }
            content
        }
    }

    suspend fun image(item: ParticipatedWorkItem, url: String, retry: Boolean, dispatcher: CoroutineDispatcher) =
        imageLocks.getOrPut(item.key to url) { Mutex() }.withLock {
            // Both readers share the same on-disk cache. Serialize writes to one image,
            // and bound requests across readers rather than once per visible document.
            imagePermits.withPermit { runInterruptible(dispatcher) { source.downloadBodyImage(item, url, retry) } }
        }

    fun clearCache() = synchronized(cacheGuard) { cacheGeneration++; bodies.clear(); cachedCharacters = 0L }
    // A persistent reader must establish the current CLI identity asynchronously before using any value.
    fun cached(item: ParticipatedWorkItem): String? = if (cacheAccess != null) null else synchronized(cacheGuard) {
        freshBody(requirementReadKey("body", "session", requirementBodyIdentity(item)))?.value
    }

    private fun freshBody(key: String): RequirementCachedValue<String>? {
        val saved = bodies[key] ?: return null
        if (!RequirementReadCache.fresh(saved.fetchedAt, clock.millis())) { removeCachedBody(key); return null }
        return saved
    }
    private fun removeCachedBody(key: String) { cachedCharacters -= bodies.remove(key)?.value?.length ?: 0 }
    private fun cacheBody(key: String, saved: RequirementCachedValue<String>) {
        removeCachedBody(key)
        // The current reader still receives the whole body, even when it is too large to retain.
        if (saved.value.length > bodyCacheCharacters) return
        bodies[key] = saved
        cachedCharacters += saved.value.length
        val entries = bodies.entries.iterator()
        while (bodies.size > bodyCacheEntries || cachedCharacters > bodyCacheCharacters) {
            cachedCharacters -= entries.next().value.value.length
            entries.remove()
        }
    }
}

/** 离开正文页取消读取；代次和工作项身份同时校验，旧结果不能覆盖新页面。 */
internal class RequirementBodyController(
    private val repository: RequirementBodyRepository,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
) {
    var bodyState by mutableStateOf<ParticipatedWorkItemBodyState>(ParticipatedWorkItemBodyState.Idle)
        private set
    var bodyImageStates by mutableStateOf<Map<String, ParticipatedWorkItemImageState>>(emptyMap())
        private set
    private var selectedKey: String? = null
    private var selectedIdentity: String? = null
    private var generation = 0L
    private var bodyJob: Job? = null
    private val imageJobs = mutableMapOf<String, Job>()

    fun clear() {
        generation++
        bodyJob?.cancel()
        imageJobs.values.forEach(Job::cancel)
        imageJobs.clear()
        selectedKey = null
        selectedIdentity = null
        bodyState = ParticipatedWorkItemBodyState.Idle
        bodyImageStates = emptyMap()
    }

    fun select(item: ParticipatedWorkItem, force: Boolean = false) {
        if (!force && selectedIdentity == requirementBodyIdentity(item) &&
            (bodyState is ParticipatedWorkItemBodyState.Loading ||
                (bodyState is ParticipatedWorkItemBodyState.Ready && repository.cached(item) != null))) return
        clear()
        selectedKey = item.key
        selectedIdentity = requirementBodyIdentity(item)
        val request = generation
        if (!force) repository.cached(item)?.let { content ->
            publish(item, content, request)
            return
        }
        bodyState = ParticipatedWorkItemBodyState.Loading(item.key)
        bodyJob = scope.launch {
            try {
                val content = repository.read(item, force, ioDispatcher)
                if (!current(item, request)) return@launch
                publish(item, content, request)
            } catch (cancelled: CancellationException) {
                currentCoroutineContext().ensureActive()
                if (current(item, request)) bodyState = ParticipatedWorkItemBodyState.Failed(item.key, "正文读取已取消，请重试")
            } catch (error: Throwable) {
                if (current(item, request)) bodyState = ParticipatedWorkItemBodyState.Failed(item.key,
                    error.message.orEmpty().ifBlank { "正文读取失败" })
            }
        }
    }

    fun retryBodyImage(item: ParticipatedWorkItem, url: String) {
        val ready = bodyState as? ParticipatedWorkItemBodyState.Ready ?: return
        if (ready.itemKey != item.key || selectedKey != item.key || url !in bodyImageStates) return
        imageJobs.remove(url)?.cancel()
        bodyImageStates = bodyImageStates + (url to ParticipatedWorkItemImageState.Loading)
        download(item, url, generation, true)
    }

    private fun current(item: ParticipatedWorkItem, request: Long) = generation == request && selectedIdentity == requirementBodyIdentity(item)

    private fun publish(item: ParticipatedWorkItem, content: String, request: Long) {
        bodyState = ParticipatedWorkItemBodyState.Ready(item.key, content)
        val urls = meegleRichTextImageUrls(content)
        bodyImageStates = urls.associateWith { ParticipatedWorkItemImageState.Loading }
        urls.forEach { download(item, it, request, false) }
    }

    private fun download(item: ParticipatedWorkItem, url: String, request: Long, retry: Boolean) {
        // Install the job before it starts, including when the scope resumes immediately.
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val runningJob = coroutineContext[Job]
            try {
                val result = try {
                    val path = repository.image(item, url, retry, ioDispatcher)
                    if (path == null) ParticipatedWorkItemImageState.Failed("当前数据源不支持读取此图片")
                    else ParticipatedWorkItemImageState.Loaded(path)
                } catch (cancelled: CancellationException) {
                    currentCoroutineContext().ensureActive()
                    ParticipatedWorkItemImageState.Failed("图片读取已取消，请重试")
                } catch (error: Throwable) {
                    ParticipatedWorkItemImageState.Failed(error.message.orEmpty().ifBlank { "图片下载失败" })
                }
                if (current(item, request) && imageJobs[url] === runningJob) bodyImageStates = bodyImageStates + (url to result)
            } finally {
                if (imageJobs[url] === runningJob) imageJobs.remove(url)
            }
        }
        imageJobs[url] = job
        job.start()
    }
}
