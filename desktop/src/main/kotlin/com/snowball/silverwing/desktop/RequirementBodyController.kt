package com.snowball.silverwing.desktop

import androidx.compose.runtime.*
import com.snowball.silverwing.core.ParticipatedWorkItem
import kotlinx.coroutines.*

/** 离开正文页取消读取；代次和工作项身份同时校验，旧结果不能覆盖新页面。 */
internal class RequirementBodyController(
    private val repository: RequirementBodyRepository,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    commentsRepository: RequirementCommentsRepository? = null,
) {
    internal val commentsReader = commentsRepository?.let { RequirementCommentsController(it, scope, ioDispatcher) }
    var bodyState by mutableStateOf<ParticipatedWorkItemBodyState>(ParticipatedWorkItemBodyState.Idle)
        private set
    var bodyImageStates by mutableStateOf<Map<String, ParticipatedWorkItemImageState>>(emptyMap())
        private set
    var document by mutableStateOf<CachedRequirementDocument?>(null)
        private set
    var cacheError by mutableStateOf<String?>(null)
        private set
    private var retryRefresh = false
    var refreshing by mutableStateOf(false)
        private set
    private var selectedKey: String? = null
    private var selectedIdentity: String? = null
    private var generation = 0L
    private var bodyJob: Job? = null
    private var documentJob: Job? = null
    private var identityJob: Job? = null
    private val imageJobs = mutableMapOf<String, Job>()

    fun clear() {
        clearBody()
        commentsReader?.clear()
    }

    private fun clearBody() {
        generation++
        bodyJob?.cancel()
        documentJob?.cancel()
        identityJob?.cancel()
        imageJobs.values.forEach(Job::cancel)
        imageJobs.clear()
        selectedKey = null
        selectedIdentity = null
        bodyState = ParticipatedWorkItemBodyState.Idle
        bodyImageStates = emptyMap()
        document = null
        cacheError = null
        retryRefresh = false
        refreshing = false
    }

    fun select(item: ParticipatedWorkItem, force: Boolean = false) {
        if (!force && selectedIdentity == requirementBodyIdentity(item) &&
            (bodyState is ParticipatedWorkItemBodyState.Loading ||
                (bodyState is ParticipatedWorkItemBodyState.Ready && repository.cached(item) != null))) return
        clearBody()
        commentsReader?.select(item, force)
        selectedKey = item.key
        selectedIdentity = requirementBodyIdentity(item)
        val request = generation
        identityJob = scope.launch(start = CoroutineStart.UNDISPATCHED) {
            repository.identityChanges.collect { select(item) }
        }
        documentJob = if (repository.persistent) scope.launch(start = CoroutineStart.UNDISPATCHED) {
            repository.updates.collect { update ->
                val existing = document
                if (!current(item, request) || existing == null || update.key != existing.key) return@collect
                update.refreshing?.let { refreshing = it }
                if (update.error != null) { cacheError = "全文缓存更新失败：${update.error}"; retryRefresh = true }
                val changed = update.document ?: return@collect
                if (changed.generation != existing.generation) {
                    imageJobs.values.forEach(Job::cancel); imageJobs.clear()
                    document = changed; cacheError = null; retryRefresh = false
                    publish(item, changed.content, request, force = true)
                } else {
                    document = changed
                    bodyImageStates = bodyImageStates.mapValues { (url, state) ->
                        when {
                            url in changed.imageFailures -> ParticipatedWorkItemImageState.Failed(changed.imageFailures.getValue(url))
                            url in changed.refreshedImages && url in changed.images -> ParticipatedWorkItemImageState.Loaded(changed.images.getValue(url))
                            else -> state
                        }
                    }
                }
            }
        } else null
        if (!force) repository.cached(item)?.let { content ->
            publish(item, content, request)
            return
        }
        bodyState = ParticipatedWorkItemBodyState.Loading(item.key)
        bodyJob = scope.launch {
            try {
                val result = repository.readDocument(item, force, ioDispatcher)
                if (!current(item, request)) return@launch
                document = result.document
                cacheError = result.cacheError
                retryRefresh = result.retryRefresh
                publish(item, result.content, request, force)
            } catch (cancelled: CancellationException) {
                currentCoroutineContext().ensureActive()
                if (current(item, request)) bodyState = ParticipatedWorkItemBodyState.Failed(item.key, "正文读取已取消，请重试")
            } catch (error: Throwable) {
                val saved = if (error is RequirementIdentityChangedException) null else try { repository.localDocument(item, ioDispatcher) }
                    catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) { null }
                if (current(item, request)) {
                    if (saved != null) {
                        document = saved
                        cacheError = "刷新失败，已保留本地正文：${error.message.orEmpty().ifBlank { "正文读取失败" }}"
                        retryRefresh = true
                        publish(item, saved.content, request)
                    } else bodyState = ParticipatedWorkItemBodyState.Failed(item.key, error.message.orEmpty().ifBlank { "正文读取失败" })
                }
            }
        }
    }

    fun retryCaching(item: ParticipatedWorkItem) = select(item, force = retryRefresh)

    fun retryBodyImage(item: ParticipatedWorkItem, url: String) {
        val ready = bodyState as? ParticipatedWorkItemBodyState.Ready ?: return
        if (ready.itemKey != item.key || selectedKey != item.key || url !in bodyImageStates) return
        imageJobs.remove(url)?.cancel()
        bodyImageStates = bodyImageStates + (url to ParticipatedWorkItemImageState.Loading)
        download(item, url, generation, true)
    }

    private fun current(item: ParticipatedWorkItem, request: Long) = generation == request && selectedIdentity == requirementBodyIdentity(item)

    private fun publish(item: ParticipatedWorkItem, content: String, request: Long, force: Boolean = false) {
        bodyState = ParticipatedWorkItemBodyState.Ready(item.key, content)
        val urls = document?.imageUrls ?: meegleRichTextImageUrls(content)
        bodyImageStates = urls.associateWith { url ->
            val saved = document?.images?.get(url)
            val failure = document?.imageFailures?.get(url)
            when {
                force -> ParticipatedWorkItemImageState.Loading
                failure != null -> ParticipatedWorkItemImageState.Failed(failure)
                saved != null -> ParticipatedWorkItemImageState.Loaded(saved)
                else -> ParticipatedWorkItemImageState.Loading
            }
        }
        urls.filter { bodyImageStates[it] is ParticipatedWorkItemImageState.Loading }.forEach { download(item, it, request, force) }
    }

    private fun download(item: ParticipatedWorkItem, url: String, request: Long, retry: Boolean) {
        // Install the job before it starts, including when the scope resumes immediately.
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val runningJob = coroutineContext[Job]
            val readingDocument = document
            try {
                val result = try {
                    val path = repository.image(item, url, retry, ioDispatcher, readingDocument)
                    if (path == null) ParticipatedWorkItemImageState.Failed("当前数据源不支持读取此图片")
                    else ParticipatedWorkItemImageState.Loaded(path)
                } catch (cancelled: CancellationException) {
                    currentCoroutineContext().ensureActive()
                    ParticipatedWorkItemImageState.Failed("图片读取已取消，请重试")
                } catch (error: Throwable) {
                    ParticipatedWorkItemImageState.Failed(error.message.orEmpty().ifBlank { "图片下载失败" })
                }
                val updatedDocument = readingDocument?.let { repository.reload(it, ioDispatcher) }
                if (current(item, request) && imageJobs[url] === runningJob) {
                    if (updatedDocument != null) document = updatedDocument
                    bodyImageStates = bodyImageStates + (url to result)
                }
            } finally {
                if (imageJobs[url] === runningJob) imageJobs.remove(url)
            }
        }
        imageJobs[url] = job
        job.start()
    }
}
