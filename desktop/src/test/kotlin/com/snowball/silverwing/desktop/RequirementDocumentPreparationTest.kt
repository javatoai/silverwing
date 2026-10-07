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

class RequirementDocumentPreparationTest {
    @TempDir lateinit var root: Path
    private fun item(id: String) = ParticipatedWorkItem("project", "obt", "userstory", "需求", id, "需求 $id", "https://project.feishu.cn/obt/userstory/detail/$id")
    private fun repository(source: ParticipatedWorkItemsSource) = RequirementBodyRepository(source,
        cacheAccess = RequirementCacheAccess(RequirementReadCache(root.resolve("old"))) { "account" },
        documentStore = RequirementDocumentStore(root.resolve("docs")))
    private abstract class Source : ParticipatedWorkItemsSource {
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
    }

    @Test fun `all returned items cache with selected priority and failure retry is independent of AI`() = runTest {
        val reads = mutableListOf<String>(); var fail = true; var remote = "old"
        val source = object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                reads += item.id
                if (item.id == "2" && fail) error("no permission")
                return "$remote-${item.id}"
            }
        }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val repo = repository(source)
        val preparation = RequirementDocumentPreparation(repo, backgroundScope, dispatcher)
        preparation.prioritize(item("3")); preparation.start(listOf(item("1"), item("2"), item("3")), false); runCurrent()
        assertEquals("3", reads.first())
        assertEquals(RequirementDocumentPreparationState(3, 2, 1, false), preparation.state.value)
        fail = false; preparation.retryFailed(); runCurrent()
        assertEquals(RequirementDocumentPreparationState(3, 3, 0, false), preparation.state.value)
        assertEquals(listOf("3", "1", "2", "2"), reads)
        assertEquals("old-2", repo.read(item("2"), false, dispatcher))
        remote = "latest"; preparation.start(listOf(item("1"), item("2"), item("3")), true); runCurrent()
        assertEquals(3, preparation.state.value.completed)
        assertEquals("latest-1", repo.read(item("1"), false, dispatcher))
        assertEquals("latest-3", repo.read(item("3"), false, dispatcher))
        assertEquals(7, reads.size)
    }

    @Test fun `body batch has two workers and a new batch cancels old reads`() = runBlocking {
        val active = AtomicInteger(); val peak = AtomicInteger(); val interrupted = AtomicInteger(); val started = CountDownLatch(2)
        val source = object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (item.id != "new") {
                    val count = active.incrementAndGet(); peak.updateAndGet { maxOf(it, count) }; started.countDown()
                    try { Thread.sleep(5000) } catch (_: InterruptedException) { interrupted.incrementAndGet() }
                    finally { active.decrementAndGet() }
                }
                return item.id
            }
        }
        val scope = CoroutineScope(coroutineContext + SupervisorJob())
        val repo = repository(source)
        val preparation = RequirementDocumentPreparation(repo, scope, Dispatchers.IO)
        try {
            preparation.start((1..6).map { item(it.toString()) }, false)
            withTimeout(3000) { while (started.count > 0) delay(10) }
            assertEquals(2, peak.get())
            preparation.start(listOf(item("new")), false)
            withTimeout(3000) { while (preparation.state.value.running || interrupted.get() < 2) delay(10) }
            assertEquals(RequirementDocumentPreparationState(1, 1, 0, false), preparation.state.value)
            assertEquals("new", repo.read(item("new"), false, Dispatchers.IO))
            val files = Files.walk(root.resolve("docs")).use { paths -> paths.filter { it.fileName.toString().endsWith(".md") }.toList() }
            assertEquals(1, files.size)
        } finally { preparation.cancel(); scope.cancel() }
    }

    @Test fun `failed image retries without body fetch and list refresh updates image bytes`() = runTest {
        val url = "https://example.com/image"; var fail = true; var reads = 0; var imageVersion = 1
        val png = byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10)
        val download = root.resolve("download")
        val retryFlags = mutableListOf<Boolean>()
        val source = object : Source() {
            override fun loadBody(item: ParticipatedWorkItem): String { reads++; return "![图]($url)" }
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                retryFlags += retry; if (fail) error("image unavailable")
                Files.write(download, png + imageVersion.toByte()); return download
            }
        }
        val dispatcher = StandardTestDispatcher(testScheduler); val repo = repository(source)
        val preparation = RequirementDocumentPreparation(repo, backgroundScope, dispatcher)
        preparation.start(listOf(item("1")), false); runCurrent()
        assertEquals(1, preparation.state.value.failures)
        fail = false; preparation.retryFailed(); runCurrent()
        assertEquals(1, preparation.state.value.completed); assertEquals(1, reads)
        val old = repo.readDocument(item("1"), false, dispatcher).document!!
        imageVersion = 2; preparation.start(listOf(item("1")), true); runCurrent()
        val latest = repo.readDocument(item("1"), false, dispatcher).document!!
        assertNotEquals(old.images[url], latest.images[url]); assertEquals(2, reads)
        assertEquals(listOf(false, true, true), retryFlags)
    }

    @Test fun `background images across documents share four download slots`() = runBlocking {
        val release = CountDownLatch(1); val active = AtomicInteger(); val peak = AtomicInteger()
        val source = object : Source() {
            override fun loadBody(item: ParticipatedWorkItem) = (1..6).joinToString("\n\n") { "![图](https://example.com/${item.id}/$it)" }
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                val count = active.incrementAndGet(); peak.updateAndGet { maxOf(it, count) }
                try {
                    check(release.await(5, TimeUnit.SECONDS))
                    return root.resolve(requirementDocumentDigest(fileUrl)).also { Files.write(it, byteArrayOf(-119, 80, 78, 71, 13, 10, 26, 10)) }
                } finally { active.decrementAndGet() }
            }
        }
        val scope = CoroutineScope(coroutineContext + SupervisorJob())
        val preparation = RequirementDocumentPreparation(repository(source), scope, Dispatchers.IO)
        try {
            preparation.start(listOf(item("1"), item("2")), false)
            withTimeout(3000) { while (active.get() < 4) delay(10) }
            assertEquals(4, peak.get()); release.countDown()
            withTimeout(3000) { while (preparation.state.value.running) delay(10) }
            assertEquals(RequirementDocumentPreparationState(2, 2, 0, false), preparation.state.value)
            assertEquals(4, peak.get())
        } finally { release.countDown(); preparation.cancel(); scope.cancel() }
    }

    @Test fun `retry of failed forced body refresh reads remote again instead of accepting old Markdown`() = runTest {
        var content = "old"; var fail = false; var calls = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String {
            calls++; if (fail) error("remote failed"); return content
        } }
        val dispatcher = StandardTestDispatcher(testScheduler); val repo = repository(source)
        val preparation = RequirementDocumentPreparation(repo, backgroundScope, dispatcher)
        preparation.start(listOf(item("1")), false); runCurrent()
        fail = true; preparation.start(listOf(item("1")), true); runCurrent()
        assertEquals(1, preparation.state.value.failures)
        assertEquals("old", repo.read(item("1"), false, dispatcher))
        fail = false; content = "new"; preparation.retryFailed(); runCurrent()
        assertEquals(RequirementDocumentPreparationState(1, 1, 0, false), preparation.state.value)
        assertEquals("new", repo.read(item("1"), false, dispatcher)); assertEquals(3, calls)
    }
}
