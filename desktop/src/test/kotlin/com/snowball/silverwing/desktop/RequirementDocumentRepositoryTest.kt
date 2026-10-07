@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import kotlinx.serialization.builtins.serializer
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class RequirementDocumentRepositoryTest {
    @TempDir lateinit var root: Path
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "711849", "支付优化", "https://project.feishu.cn/obt/userstory/detail/711849")
    private val key = requirementReadKey("body", "account", requirementBodyIdentity(item))
    private val clock = RequirementCacheTestClock()
    private val access get() = RequirementCacheAccess(RequirementReadCache(root.resolve("requirements"), clock)) { "account" }
    private abstract class Source : ParticipatedWorkItemsSource {
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
    }
    private fun repo(source: Source, store: RequirementDocumentStore = RequirementDocumentStore(root.resolve("requirement-documents"))) =
        RequirementBodyRepository(source, cacheAccess = access, clock = clock, documentStore = store)

    @Test fun `legacy migration verifies Markdown removes only matching body and survives JSON cleanup`() = runTest {
        val body = "# 完整正文\n\n| 一 | 二 |\n|---|---|\n| 三 | 四 |\n\n![图](https://example.com/a)"
        val cache = access.cache
        cache.write(key, String.serializer(), body)
        val other = requirementReadKey("body", "other-account", requirementBodyIdentity(item))
        val metadata = requirementReadKey("metadata", "account", item.id)
        val list = requirementReadKey("participated-list-all-pages-v1", "account", "project")
        listOf(other, metadata, list).forEach { cache.write(it, String.serializer(), "untouched") }
        var reads = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String { reads++; error("offline") } }
        val result = repo(source).readDocument(item, false, StandardTestDispatcher(testScheduler))
        assertEquals(body, result.content); assertNotNull(result.document); assertNull(result.cacheError)
        assertNull(cache.read(key, String.serializer()))
        listOf(other, metadata, list).forEach { assertEquals("untouched", cache.read(it, String.serializer())!!.value) }
        clock.advance(Duration.ofDays(40)); cache.cleanup()
        assertEquals(body, repo(source).read(item, false, StandardTestDispatcher(testScheduler)))
        assertEquals(0, reads)
    }

    @Test fun `failed migration retains old JSON and retry succeeds without a remote body read`() = runTest {
        val cache = access.cache
        cache.write(key, String.serializer(), "旧正文")
        val obstacle = root.resolve("obstacle"); Files.writeString(obstacle, "keep")
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = error("must not fetch") }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val failed = repo(source, RequirementDocumentStore(obstacle.resolve("docs"))).readDocument(item, false, dispatcher)
        assertEquals("旧正文", failed.content); assertNull(failed.document); assertNotNull(failed.cacheError)
        assertEquals("旧正文", cache.read(key, String.serializer())!!.value)
        assertNotNull(repo(source).readDocument(item, false, dispatcher).document)
        assertNull(cache.read(key, String.serializer()))
    }

    @Test fun `new flow never writes a body JSON and image retry never re-reads the body`() = runTest {
        val url = "https://example.com/image"
        val downloaded = root.resolve("download.image").also { Files.write(it, byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10, 1)) }
        var reads = 0; var imageFails = true
        val source = object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String { reads++; return "![图]($url)" }
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path { if (imageFails) error("offline"); return downloaded }
        }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val repository = repo(source)
        val document = repository.readDocument(item, false, dispatcher).document!!
        assertNull(access.cache.read(key, String.serializer()))
        assertFails { repository.image(item, url, false, dispatcher, document) }
        assertEquals("offline", repository.reload(document, dispatcher)!!.imageFailures[url])
        imageFails = false
        val saved = repository.image(item, url, true, dispatcher, document)
        assertTrue(Files.exists(saved)); assertTrue(repository.reload(document, dispatcher)!!.complete)
        assertEquals(1, reads)
        val restarted = repo(source).readDocument(item, false, dispatcher).document!!
        assertTrue(restarted.complete); assertEquals(1, reads)
    }

    @Test fun `shared in-flight body survives one consumer leaving and newer refresh wins`() = runBlocking {
        val started = CountDownLatch(1); val release = CountDownLatch(1); val calls = AtomicInteger()
        val source = object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (calls.incrementAndGet() == 1) { started.countDown(); check(release.await(5, TimeUnit.SECONDS)); return "old" }
                return "new"
            }
        }
        val repository = repo(source)
        val first = async { repository.readDocument(item, false, Dispatchers.IO) }
        val second = async { repository.readDocument(item, false, Dispatchers.IO) }
        try {
            withTimeout(3000) { while (started.count > 0) delay(10) }
            delay(80); assertEquals(1, calls.get())
            first.cancelAndJoin()
            val refreshed = repository.readDocument(item, true, Dispatchers.IO)
            assertEquals("new", refreshed.content)
            release.countDown()
            assertEquals("new", second.await().content)
            assertEquals("new", repo(source).read(item, false, Dispatchers.IO))
            assertEquals(2, calls.get())
        } finally { release.countDown(); first.cancel(); second.cancel() }
    }

    @Test fun `both open readers follow background refresh and keep old body on refresh failure`() = runTest {
        var content = "old"; var fail = false; var reads = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String {
            reads++; if (fail) error("network failed"); return content
        } }
        val dispatcher = StandardTestDispatcher(testScheduler); val repository = repo(source)
        val listReader = RequirementBodyController(repository, backgroundScope, dispatcher)
        val taskReader = RequirementBodyController(repository, backgroundScope, dispatcher)
        listReader.select(item); taskReader.select(item); runCurrent()
        assertEquals(1, reads)
        content = "latest"
        repository.readDocument(item, true, dispatcher); runCurrent()
        for (reader in listOf(listReader, taskReader)) {
            assertEquals("latest", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
            assertEquals("latest", reader.document!!.content); assertFalse(reader.refreshing)
        }
        fail = true
        assertFails { repository.readDocument(item, true, dispatcher) }; runCurrent()
        for (reader in listOf(listReader, taskReader)) {
            assertEquals("latest", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
            assertContains(reader.cacheError!!, "network failed"); assertFalse(reader.refreshing)
        }
        listReader.clear(); taskReader.clear()
    }

    @Test fun `public image fallback removes temporary download and persists relative Markdown`() = runTest {
        val url = "https://example.com/image"
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = "![公开图片]($url)" }
        val temporary = root.resolve("http-download").also { Files.write(it, byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10)) }
        var calls = 0
        val repository = RequirementBodyRepository(source, cacheAccess = access,
            documentStore = RequirementDocumentStore(root.resolve("docs")), downloadHttpImage = { assertEquals(url, it); calls++; temporary })
        val dispatcher = StandardTestDispatcher(testScheduler)
        val document = repository.readDocument(item, false, dispatcher).document!!
        assertNotNull(repository.image(item, url, false, dispatcher, document))
        assertFalse(Files.exists(temporary)); assertEquals(1, calls)
        assertTrue(repository.reload(document, dispatcher)!!.complete)
        assertNotNull(repository.image(item, url, false, dispatcher, document)); assertEquals(1, calls)
    }
}
