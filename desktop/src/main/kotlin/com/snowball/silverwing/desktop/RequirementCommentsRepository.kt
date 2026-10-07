package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import java.time.Clock

internal data class RequirementCommentsReadResult(val document: CachedRequirementComments, val error: String? = null)

internal class RequirementCommentsRepository(
    private val source: ParticipatedWorkItemsSource,
    private val store: RequirementCommentsStore,
    private val scope: CoroutineScope,
    private val access: RequirementCacheAccess? = null,
    private val clock: Clock = Clock.systemUTC(),
) {
    private val guard = Any()
    private class Request(val forced: Boolean, val revision: Long?, val parent: CompletableJob) {
        lateinit var result: Deferred<RequirementCommentsReadResult>
        var consumers = 0
    }
    private val requests = mutableMapOf<String, Request>()
    private val permits = Semaphore(2)
    private val changes = MutableSharedFlow<RequirementCommentsReadResult>(extraBufferCapacity = 32)
    val updates = changes.asSharedFlow()

    suspend fun local(item: ParticipatedWorkItem, dispatcher: CoroutineDispatcher): CachedRequirementComments? {
        val identity = if (access == null) "session" else access.identity(dispatcher) ?: return null
        access?.ensureNotLoggedOut()
        val revision = access?.snapshot?.revision
        val result = withContext(dispatcher) { store.read(key(identity, item)) }
        access?.verifySession(identity, revision)
        return result
    }

    suspend fun read(item: ParticipatedWorkItem, force: Boolean, dispatcher: CoroutineDispatcher): RequirementCommentsReadResult {
        val identity = if (access == null) "session" else if (force) access.recheck(dispatcher, strict = true) else access.identity(dispatcher)
        access?.ensureNotLoggedOut()
        val revision = access?.snapshot?.revision
        val key = key(identity.orEmpty(), item)
        // Join an active refresh before consulting the older disk snapshot.
        fun Request.current() = revision == this.revision && parent.isActive && !result.isCompleted
        val active = synchronized(guard) { requests[key]?.takeIf { it.current() && (!force || it.forced) } }
        if (active == null && !force && identity != null) {
            val saved = withContext(dispatcher) { runCatching { store.read(key) }.getOrNull() }
            access?.verifySession(identity, revision)
            if (saved != null && synchronized(guard) { requests[key]?.current() != true }) return RequirementCommentsReadResult(saved)
        }
        val request = synchronized(guard) {
            requests[key]?.takeIf { it.current() && (!force || it.forced) }?.also { it.consumers++ } ?: run {
                val parent = SupervisorJob(scope.coroutineContext[Job])
                val next = Request(force, revision, parent).also { it.consumers = 1 }
                next.result = scope.async(dispatcher + parent, start = CoroutineStart.LAZY) {
                    if (!force) access?.verify(identity, dispatcher)
                    val data = permits.withPermit { runInterruptible { source.loadComments(item) } }
                    access?.verify(identity, dispatcher)
                    currentCoroutineContext().ensureActive()
                    val result = synchronized(guard) {
                        access?.verifySession(identity, revision)
                        if (requests[key] !== next) throw SupersededCommentsException()
                        val now = clock.millis()
                        if (identity == null) RequirementCommentsReadResult(CachedRequirementComments(key, data, now, null), "无法确认账户，评论尚未缓存")
                        else try { RequirementCommentsReadResult(store.write(key, data, now)) }
                        catch (error: Exception) { RequirementCommentsReadResult(CachedRequirementComments(key, data, now, null), "评论缓存失败：${error.message}") }
                    }
                    changes.tryEmit(result)
                    result
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
            val result = try { request.result.await() }
            catch (superseded: SupersededCommentsException) {
                currentCoroutineContext().ensureActive()
                return read(item, false, dispatcher)
            }
            access?.verifySession(identity, revision)
            return result
        } finally {
            synchronized(guard) { if (--request.consumers == 0 && !request.result.isCompleted) request.parent.cancel() }
        }
    }

    fun invalidateIdentity() {
        val revision = access?.snapshot?.revision
        val old = synchronized(guard) {
            requests.entries.filter { it.value.revision != revision }.map { it.key to it.value }
                .also { entries -> entries.forEach { requests.remove(it.first) } }.map { it.second }
        }
        old.forEach { it.parent.cancel() }
    }
    private fun key(identity: String, item: ParticipatedWorkItem) = requirementReadKey("comments", identity, requirementBodyIdentity(item))
    private class SupersededCommentsException : CancellationException("最新评论已刷新")
}
