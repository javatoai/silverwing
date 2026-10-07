package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.serialization.builtins.serializer
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.util.concurrent.ConcurrentHashMap
import com.snowball.silverwing.core.MeegleParticipatedWorkItemsSource

/** Both readers, naming and background preparation use the same identity-bound Markdown. */
internal data class RequirementDocumentUpdate(val key: String, val document: CachedRequirementDocument? = null,
    val refreshing: Boolean? = null, val error: String? = null)

internal class RequirementBodyRepository(
    private val source: ParticipatedWorkItemsSource,
    imageParallelism: Int = 4,
    private val bodyCacheEntries: Int = 64,
    private val bodyCacheCharacters: Int = 4_000_000,
    private val cacheAccess: RequirementCacheAccess? = null,
    private val clock: Clock = Clock.systemUTC(),
    private val documentStore: RequirementDocumentStore? = cacheAccess?.let { RequirementDocumentStore(it.cache.documentDirectory) },
    private val requestScope: CoroutineScope? = null,
    private val downloadHttpImage: (String) -> Path = ::downloadRequirementHttpImage,
) {
    private val guard = Any()
    private val bodies = LinkedHashMap<String, RequirementCachedValue<String>>(16, 0.75f, true)
    private var cachedCharacters = 0L
    private var epoch = 0L
    val persistent: Boolean get() = documentStore != null
    private class Request(val forced: Boolean, val parent: CompletableJob, val epoch: Long) {
        lateinit var result: Deferred<RequirementBodyReadResult>
        var consumers = 0
    }
    private val requests = mutableMapOf<String, Request>()
    private val imageLocks = ConcurrentHashMap<Pair<String, String>, Mutex>()
    private val imagePermits = Semaphore(imageParallelism)
    // Naming preparation and both readers also share this bound, so they cannot add another background batch.
    private val bodyPermits = Semaphore(2)
    private val documentUpdates = MutableSharedFlow<RequirementDocumentUpdate>(extraBufferCapacity = 64)
    val updates = documentUpdates.asSharedFlow()
    val identityChanges get() = cacheAccess?.changes ?: kotlinx.coroutines.flow.emptyFlow()
    init { require(bodyCacheEntries > 0 && bodyCacheCharacters > 0) }

    suspend fun read(item: ParticipatedWorkItem, force: Boolean, dispatcher: CoroutineDispatcher): String =
        readDocument(item, force, dispatcher).content

    suspend fun readDocument(item: ParticipatedWorkItem, force: Boolean, dispatcher: CoroutineDispatcher): RequirementBodyReadResult {
        val identity = if (cacheAccess == null) "session" else if (force) cacheAccess.recheck(dispatcher, strict = true)
            else cacheAccess.identity(dispatcher)
        cacheAccess?.ensureNotLoggedOut()
        val identityRevision = cacheAccess?.snapshot?.revision
        val key = requirementReadKey("body", identity.orEmpty(), requirementBodyIdentity(item))
        val context = currentCoroutineContext()
        val request = synchronized(guard) {
            val running = requests[key]
            if (running != null && running.epoch == epoch && running.parent.isActive && !running.result.isCompleted && (!force || running.forced)) running.also { it.consumers++ }
            else {
                val parent = SupervisorJob(requestScope?.coroutineContext?.get(Job))
                val next = Request(force, parent, epoch)
                val generation = epoch
                next.consumers = 1
                next.result = CoroutineScope(context.minusKey(Job) + parent).async(start = CoroutineStart.LAZY) {
                    fun current() = synchronized(guard) { requests[key] === next && epoch == generation && next.parent.isActive &&
                        (cacheAccess == null || (cacheAccess.snapshot.identity == identity && cacheAccess.snapshot.revision == identityRevision)) }
                    if (force) documentUpdates.tryEmit(RequirementDocumentUpdate(key, refreshing = true))
                    try {
                        fetch(item, key, identity, force, dispatcher, ::current).also { result ->
                            if (current()) documentUpdates.tryEmit(RequirementDocumentUpdate(key, result.document, refreshing = false, error = result.cacheError))
                        }
                    } catch (error: Exception) {
                        if (current()) documentUpdates.tryEmit(RequirementDocumentUpdate(key, refreshing = false, error = error.takeUnless { it is CancellationException }?.message))
                        throw error
                    }
                }
                requests[key] = next
                next.result.invokeOnCompletion {
                    synchronized(guard) { if (requests[key] === next) requests.remove(key) }
                    parent.complete()
                }
                next
            }
        }
        try {
            request.result.start()
            val result = request.result.await()
            cacheAccess?.verifySession(identity, identityRevision)
            // An older consumer follows the newer refresh instead of exposing stale content without a file.
            if (documentStore != null && identity != null && result.document == null && result.cacheError == null)
                return readDocument(item, false, dispatcher)
            return result
        }
        finally { synchronized(guard) { if (--request.consumers == 0 && !request.result.isCompleted) request.parent.cancel() } }
    }

    private suspend fun fetch(item: ParticipatedWorkItem, key: String, identity: String?, force: Boolean,
        dispatcher: CoroutineDispatcher, current: () -> Boolean): RequirementBodyReadResult {
        var cacheError: String? = null
        if (!force && identity != null) {
            if (documentStore != null) {
                val local = withContext(dispatcher) { runCatching { documentStore.read(key) }.onFailure { cacheError = it.message }.getOrNull() }
                if (local != null) {
                    cacheAccess?.verifySession(identity)
                    val cleanupError = withContext(dispatcher) {
                        try {
                            val legacy = cacheAccess?.cache?.read(key, String.serializer())
                            if (legacy?.value == local.content) cacheAccess?.cache?.remove(key)
                            null
                        } catch (error: Exception) { "旧正文缓存清理失败：${error.message}" }
                    }
                    return RequirementBodyReadResult(local.content, local, cleanupError)
                }
                val legacy = withContext(dispatcher) { cacheAccess?.cache?.read(key, String.serializer()) }
                if (legacy != null) {
                    cacheAccess?.verifySession(identity)
                    return withContext(dispatcher) {
                        currentCoroutineContext().ensureActive()
                        synchronized(guard) {
                            try {
                                if (!current()) return@synchronized RequirementBodyReadResult(legacy.value)
                                val document = documentStore.write(key, item, legacy.value, legacy.fetchedAt)
                                check(documentStore.read(key)?.content == legacy.value) { "旧正文迁移校验失败" }
                                cacheAccess?.cache?.remove(key)
                                RequirementBodyReadResult(document.content, document)
                            } catch (cancelled: CancellationException) { throw cancelled }
                            catch (error: Exception) { RequirementBodyReadResult(legacy.value, cacheError = "旧正文迁移失败：${error.message}") }
                        }
                    }
                }
            } else synchronized(guard) { freshBody(key) }?.let { return RequirementBodyReadResult(it.value) }
        }
        if (!force) cacheAccess?.verify(identity, dispatcher)
        val content = bodyPermits.withPermit { runInterruptible(dispatcher) { source.loadBody(item).orEmpty() } }
        cacheAccess?.verify(identity, dispatcher)
        currentCoroutineContext().ensureActive()
        return withContext(dispatcher) {
            currentCoroutineContext().ensureActive()
            // A forced request supersedes an older read. Only its current generation may commit.
            synchronized(guard) {
                if (!current()) return@synchronized RequirementBodyReadResult(content)
                if (identity == null) return@synchronized RequirementBodyReadResult(content, cacheError = "无法确认当前账户，正文尚未缓存", retryRefresh = true)
                val fetchedAt = clock.millis()
                if (documentStore == null) {
                    cacheBody(key, RequirementCachedValue(content, fetchedAt))
                    RequirementBodyReadResult(content)
                } else try {
                    val document = documentStore.write(key, item, content, fetchedAt)
                    check(documentStore.read(key)?.content == content) { "需求文档写入校验失败" }
                    // Also retire legacy bodies on an explicit refresh, after the new file is verified.
                    cacheAccess?.cache?.remove(key)
                    RequirementBodyReadResult(document.content, document)
                } catch (error: Exception) {
                    RequirementBodyReadResult(content, cacheError = "全文缓存失败：${error.message ?: cacheError}", retryRefresh = true)
                }
            }
        }
    }

    suspend fun reload(document: CachedRequirementDocument, dispatcher: CoroutineDispatcher) = withContext(dispatcher) {
        documentStore?.read(document.key)?.takeIf { it.generation == document.generation }
    }

    suspend fun localDocument(item: ParticipatedWorkItem, dispatcher: CoroutineDispatcher): CachedRequirementDocument? {
        val identity = if (cacheAccess == null) "session" else cacheAccess.identity(dispatcher) ?: return null
        val identityRevision = cacheAccess?.snapshot?.revision
        val document = withContext(dispatcher) { documentStore?.read(requirementReadKey("body", identity, requirementBodyIdentity(item))) }
        cacheAccess?.verifySession(identity, identityRevision)
        return document
    }

    suspend fun image(item: ParticipatedWorkItem, url: String, retry: Boolean, dispatcher: CoroutineDispatcher,
        document: CachedRequirementDocument? = null): Path? =
        imageLocks.getOrPut((document?.key ?: item.key) to url) { Mutex() }.withLock {
            val latest = document?.let { reload(it, dispatcher) }
            if (document != null && latest == null) throw CancellationException("需求文档已更新")
            if (latest != null && (!retry || url in latest.refreshedImages)) latest.images[url]?.let { return@withLock it }
            try {
                imagePermits.withPermit {
                    var temporary: Path? = null
                    try {
                        val downloaded = runInterruptible(dispatcher) {
                            val external = source is MeegleParticipatedWorkItemsSource && !url.startsWith("https://project.feishu.cn/goapi/v5/platform/file/stream/download/")
                            (if (external && document != null) null else source.downloadBodyImage(item, url, retry))
                                ?: if (document != null) downloadHttpImage(url).also { temporary = it } else null
                        }
                        if (document == null) return@withPermit downloaded
                        requireNotNull(downloaded) { "当前数据源不支持读取此图片" }
                        val identity = if (cacheAccess == null) "session" else cacheAccess.recheck(dispatcher, strict = true)
                        require(identity != null && requirementReadKey("body", identity, requirementBodyIdentity(item)) == document.key) { "读取期间账户已改变，请重试" }
                        currentCoroutineContext().ensureActive()
                        val saved = withContext(dispatcher) { documentStore?.saveImage(document.key, document.generation, url, downloaded) }
                            ?: throw CancellationException("需求文档已更新")
                        documentUpdates.tryEmit(RequirementDocumentUpdate(saved.key, saved))
                        saved.images[url]
                    } finally { temporary?.let { path -> withContext(NonCancellable + dispatcher) { Files.deleteIfExists(path) } } }
                }
            } catch (cancelled: CancellationException) {
                currentCoroutineContext().ensureActive()
                recordImageFailure(document, url, "图片读取已取消，请重试", dispatcher)
                throw cancelled
            }
            catch (error: Exception) {
                recordImageFailure(document, url, error.message.orEmpty(), dispatcher)
                throw error
            }
        }

    private suspend fun recordImageFailure(document: CachedRequirementDocument?, url: String, message: String, dispatcher: CoroutineDispatcher) {
        if (document != null) withContext(dispatcher) { documentStore?.failImage(document.key, document.generation, url, message) }?.let {
            documentUpdates.tryEmit(RequirementDocumentUpdate(it.key, it))
        }
    }

    fun clearCache(cancelPending: Boolean = false) {
        val cancelled = synchronized(guard) {
            epoch++; bodies.clear(); cachedCharacters = 0L
            if (cancelPending) requests.values.toList().also { requests.clear() } else emptyList()
        }
        cancelled.forEach { it.parent.cancel() }
    }
    fun cached(item: ParticipatedWorkItem): String? = if (cacheAccess != null) null else synchronized(guard) {
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
