package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class RequirementBodyRepositoryTest {
    private val item = ParticipatedWorkItem("project", "OBT", "userstory", "需求", "1", "正文", "https://project.feishu.cn/obt/userstory/detail/1")
    private val imageUrl = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/image"

    private abstract class Source : ParticipatedWorkItemsSource {
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
        override fun loadBody(item: ParticipatedWorkItem): String = "正文"
    }

    @Test fun `clearing cache prevents an older in flight read from repopulating it`() = runBlocking {
        val started = CountDownLatch(1)
        val release = CountDownLatch(1)
        val calls = AtomicInteger()
        val repository = RequirementBodyRepository(object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (calls.incrementAndGet() == 1) {
                    started.countDown()
                    check(release.await(3, TimeUnit.SECONDS))
                    return "old body"
                }
                return "fresh body"
            }
        })
        val reading = async { repository.read(item, false, Dispatchers.IO) }
        try {
            withTimeout(3_000) { while (started.count > 0) delay(10) }
            repository.clearCache()
            release.countDown()
            assertEquals("old body", reading.await())
            assertNull(repository.cached(item))
            assertEquals("fresh body", repository.read(item, false, Dispatchers.IO))
            assertEquals("fresh body", repository.cached(item))
        } finally { release.countDown(); reading.cancel() }
    }

    @Test fun `a failed forced refresh preserves the valid body and reports the failure`() = runBlocking {
        var fail = false
        var content = "old body"
        val repository = RequirementBodyRepository(object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (fail) error("body changed permissions")
                return content
            }
        })
        assertEquals("old body", repository.read(item, false, Dispatchers.IO))
        fail = true
        assertFailsWith<IllegalStateException> { repository.read(item, true, Dispatchers.IO) }
        assertEquals("old body", repository.cached(item))
        fail = false; content = "fresh body"
        assertEquals("old body", repository.read(item, false, Dispatchers.IO))
        assertEquals("fresh body", repository.read(item, true, Dispatchers.IO))
    }

    @Test fun `body cache retains recent entries and evicted bodies are read again`() = runBlocking {
        val calls = mutableListOf<String>()
        val repository = RequirementBodyRepository(object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                calls += item.id
                return "body ${item.id}"
            }
        }, bodyCacheEntries = 2)
        val first = item.copy(id = "1")
        val second = item.copy(id = "2")
        val third = item.copy(id = "3")
        repository.read(first, false, Dispatchers.Unconfined)
        repository.read(second, false, Dispatchers.Unconfined)
        assertEquals("body 1", repository.cached(first))
        repository.read(third, false, Dispatchers.Unconfined)
        assertNull(repository.cached(second))
        assertEquals("body 1", repository.read(first, false, Dispatchers.Unconfined))
        assertEquals(listOf("1", "2", "3"), calls)
        assertEquals("body 2", repository.read(second, false, Dispatchers.Unconfined))
        assertEquals(listOf("1", "2", "3", "2"), calls)
    }

    @Test fun `character budget evicts old bodies without truncating large current content`() = runBlocking {
        val contents = mapOf("1" to "正文一", "2" to "正文二", "3" to "很长的完整正文必须原样呈现")
        val calls = mutableListOf<String>()
        val repository = RequirementBodyRepository(object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                calls += item.id
                return contents.getValue(item.id)
            }
        }, bodyCacheCharacters = 5)
        assertEquals(contents["1"], repository.read(item, false, Dispatchers.Unconfined))
        assertEquals(contents["2"], repository.read(item.copy(id = "2"), false, Dispatchers.Unconfined))
        assertNull(repository.cached(item))
        assertEquals(contents["2"], repository.cached(item.copy(id = "2")))
        val large = item.copy(id = "3")
        repeat(2) { assertEquals(contents["3"], repository.read(large, false, Dispatchers.Unconfined)) }
        assertNull(repository.cached(large))
        assertEquals(contents["2"], repository.cached(item.copy(id = "2")))
        assertEquals(listOf("1", "2", "3", "3"), calls)
    }

    @Test fun `image downloads across both readers share a concurrency bound`() = runBlocking {
        val release = CountDownLatch(1)
        val active = AtomicInteger()
        val peak = AtomicInteger()
        val calls = AtomicInteger()
        val urls = (1..6).map { "$imageUrl$it" }
        val repository = RequirementBodyRepository(object : Source() {
            override fun loadBody(item: ParticipatedWorkItem) = urls.joinToString("\n\n") { "![图片]($it)" }
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                val count = active.incrementAndGet()
                peak.updateAndGet { maxOf(it, count) }
                calls.incrementAndGet()
                return try {
                    check(release.await(3, TimeUnit.SECONDS))
                    Path.of("${fileUrl.substringAfterLast('/')}.png")
                } finally { active.decrementAndGet() }
            }
        }, imageParallelism = 2)
        val scope = CoroutineScope(coroutineContext + SupervisorJob())
        try {
            val first = RequirementBodyController(repository, scope, Dispatchers.IO)
            val second = RequirementBodyController(repository, scope, Dispatchers.IO)
            first.select(item); second.select(item.copy(id = "2"))
            withTimeout(3_000) { while (active.get() < 2) delay(10) }
            assertEquals(2, peak.get())
            assertEquals(2, calls.get(), "Remaining image requests must wait without starting a source call")
            release.countDown()
            withTimeout(3_000) {
                while (first.bodyImageStates.values.count { it is ParticipatedWorkItemImageState.Loaded } != 6 ||
                    second.bodyImageStates.values.count { it is ParticipatedWorkItemImageState.Loaded } != 6) delay(10)
            }
            assertEquals(2, peak.get())
            assertEquals(12, calls.get())
        } finally { release.countDown(); scope.cancel() }
    }

    @Test fun `simultaneous readers cannot write the same cached image concurrently`() = runBlocking {
        val release = CountDownLatch(1)
        val active = AtomicInteger()
        val peak = AtomicInteger()
        val calls = AtomicInteger()
        val repository = RequirementBodyRepository(object : Source() {
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                val count = active.incrementAndGet()
                peak.updateAndGet { maxOf(it, count) }
                calls.incrementAndGet()
                return try {
                    check(release.await(3, TimeUnit.SECONDS))
                    Path.of("image.png")
                } finally { active.decrementAndGet() }
            }
        })
        val first = async { repository.image(item, imageUrl, false, Dispatchers.IO) }
        val second = async { repository.image(item, imageUrl, true, Dispatchers.IO) }
        try {
            withTimeout(3_000) { while (calls.get() == 0) delay(10) }
            delay(50)
            assertEquals(1, calls.get())
            release.countDown()
            assertEquals(Path.of("image.png"), first.await())
            assertEquals(Path.of("image.png"), second.await())
            assertEquals(1, peak.get())
        } finally { release.countDown(); first.cancel(); second.cancel() }
    }
}
