@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class RequirementCommentsRepositoryTest {
    @TempDir lateinit var root: Path
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "123", "标题", "https://project.feishu.cn/obt/userstory/detail/123")
    private fun comments(text: String) = RequirementComments(listOf(RequirementComment("1", text, "user", "作者", "2026-10-06 12:00:00")))
    private fun source(read: (ParticipatedWorkItem) -> RequirementComments) = object : ParticipatedWorkItemsSource {
        override val supportsComments = true
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
        override fun loadBody(item: ParticipatedWorkItem): String = "# 本地正文"
        override fun loadComments(item: ParticipatedWorkItem) = read(item)
    }
    @Test fun `disk cache survives restart and repeated local reads do not verify account or fetch remotely`() = runTest {
        val io = StandardTestDispatcher(testScheduler); var checks = 0; var reads = 0
        val access = RequirementCacheAccess(RequirementReadCache(root.resolve("old"))) { checks++; "account" }
        val store = RequirementCommentsStore(root.resolve("comments"))
        val source = source { reads++; comments("完整评论\n".repeat(1000)) }
        val first = RequirementCommentsRepository(source, store, backgroundScope, access).read(item, false, io)
        val initialChecks = checks
        assertTrue(Files.isRegularFile(first.document.path)); assertEquals(1, reads)
        val restarted = RequirementCommentsRepository(source, RequirementCommentsStore(root.resolve("comments")), backgroundScope, access)
        repeat(5) { assertEquals(first.document, restarted.read(item, false, io).document) }
        assertEquals(initialChecks, checks); assertEquals(1, reads)
        val json = Files.readString(first.document.path)
        assertFalse(json.contains("email")); assertTrue(json.contains("完整评论"))
    }
    @Test fun `refresh replaces cache including empty results and failure preserves previous comments`() = runTest {
        val io = StandardTestDispatcher(testScheduler); var data = comments("旧评论"); var fail = false
        val repository = RequirementCommentsRepository(source { if (fail) error("断网"); data }, RequirementCommentsStore(root), backgroundScope)
        val reader = RequirementCommentsController(repository, backgroundScope, io)
        reader.select(item); runCurrent()
        val path = reader.document!!.path!!; val previous = Files.readString(path)
        fail = true; reader.select(item, true)
        assertEquals("旧评论", reader.document!!.data.comments.single().content)
        runCurrent(); assertTrue(reader.error!!.contains("已保留本地评论")); assertEquals(previous, Files.readString(path))
        fail = false; data = comments("新评论"); reader.select(item, true); runCurrent()
        assertNull(reader.error); assertEquals("新评论", reader.document!!.data.comments.single().content)
        data = RequirementComments(emptyList()); reader.select(item, true); runCurrent()
        assertTrue(reader.document!!.data.comments.isEmpty()); assertTrue(repository.local(item, io)!!.data.comments.isEmpty())
        reader.clear()
    }
    @Test fun `account and requirement identities isolate caches and unknown identity never guesses`() = runTest {
        val io = StandardTestDispatcher(testScheduler); var account: String? = "account-A"; var reads = 0
        val access = RequirementCacheAccess(RequirementReadCache(root.resolve("old"))) { account }
        val store = RequirementCommentsStore(root.resolve("comments"))
        val repository = RequirementCommentsRepository(source { reads++; comments("$account:${it.id}:${it.type}:${it.projectKey}") }, store, backgroundScope, access)
        val a = repository.read(item, false, io).document
        val other = repository.read(item.copy(id = "456"), false, io).document
        assertNotEquals(a.path, other.path)
        account = "account-B"; access.recheck(io)
        assertNull(repository.local(item, io))
        val b = repository.read(item, false, io).document
        assertNotEquals(a.path, b.path); assertEquals(3, reads)
        val unknown = RequirementCacheAccess(RequirementReadCache(root.resolve("unknown"))) { null }
        assertNull(RequirementCommentsRepository(source { comments("live") }, store, backgroundScope, unknown).local(item, io))
        account = "account-A"; access.recheck(io)
        assertEquals(a, repository.read(item, false, io).document); assertEquals(3, reads)
    }
    @Test fun `write failure shows remote comments without claiming a local file`() = runTest {
        val io = StandardTestDispatcher(testScheduler)
        val obstacle = root.resolve("not-directory").also { Files.writeString(it, "keep") }
        val repository = RequirementCommentsRepository(source { comments("已读取") }, RequirementCommentsStore(obstacle), backgroundScope)
        val result = repository.read(item, false, io)
        assertNull(result.document.path); assertEquals("已读取", result.document.data.comments.single().content)
        assertTrue(result.error!!.contains("评论缓存失败")); assertEquals("keep", Files.readString(obstacle))
    }
    @Test fun `invalid cache is recovered by a complete remote read`() = runTest {
        val io = StandardTestDispatcher(testScheduler); var reads = 0
        val repository = RequirementCommentsRepository(source { reads++; comments("完整") }, RequirementCommentsStore(root), backgroundScope)
        val first = repository.read(item, false, io)
        Files.writeString(first.document.path, "{invalid}")
        val recovered = repository.read(item, false, io)
        assertEquals(2, reads); assertEquals(first.document.data, recovered.document.data)
    }
    @Test fun `comments remain available when the body fails and old task comments never appear in a new task`() = runTest {
        val io = StandardTestDispatcher(testScheduler)
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem): String = error("正文不可读")
            override fun loadComments(item: ParticipatedWorkItem) = comments(item.id)
        }
        val comments = RequirementCommentsRepository(source, RequirementCommentsStore(root), backgroundScope)
        val reader = RequirementBodyController(RequirementBodyRepository(source), backgroundScope, io, comments)
        reader.select(item); runCurrent()
        assertIs<ParticipatedWorkItemBodyState.Failed>(reader.bodyState)
        assertEquals("123", reader.commentsReader!!.document!!.data.comments.single().content)
        reader.select(item.copy(id = "456"), true)
        assertNull(reader.commentsReader.document); runCurrent()
        assertEquals("456", reader.commentsReader.document!!.data.comments.single().content)
        reader.clear(); assertNull(reader.commentsReader.document)
    }
    @Test fun `shared reads survive one consumer cancellation and forced refresh cannot be overwritten by older response`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val entered = CountDownLatch(1); val release = CountDownLatch(1); val reads = AtomicInteger()
        val repository = RequirementCommentsRepository(source {
            val number = reads.incrementAndGet()
            if (number == 1) { entered.countDown(); check(release.await(5, TimeUnit.SECONDS)) }
            comments("version-$number")
        }, RequirementCommentsStore(root), scope)
        try {
            val first = async { repository.read(item, false, Dispatchers.IO) }
            assertTrue(withContext(Dispatchers.IO) { entered.await(5, TimeUnit.SECONDS) })
            val second = async(start = CoroutineStart.UNDISPATCHED) { repository.read(item, false, Dispatchers.IO) }
            first.cancelAndJoin()
            val refreshed = repository.read(item, true, Dispatchers.IO)
            assertEquals("version-2", refreshed.document.data.comments.single().content)
            release.countDown()
            assertEquals(refreshed.document.data, withTimeout(5_000) { second.await() }.document.data)
            assertEquals(refreshed.document.data, repository.local(item, Dispatchers.IO)!!.data)
            assertEquals(2, reads.get())
        } finally { release.countDown(); scope.cancel() }
    }
    @Test fun `account change during remote read prevents a stale result from being cached`() = runTest {
        val io = StandardTestDispatcher(testScheduler); var account = "old"
        val access = RequirementCacheAccess(RequirementReadCache(root.resolve("old"))) { account }
        val repository = RequirementCommentsRepository(source { account = "new"; comments("old result") }, RequirementCommentsStore(root.resolve("comments")), backgroundScope, access)
        assertFailsWith<RequirementIdentityChangedException> { repository.read(item, false, io) }
        assertNull(repository.local(item, io))
        assertFalse(Files.exists(root.resolve("comments")))
    }
}
