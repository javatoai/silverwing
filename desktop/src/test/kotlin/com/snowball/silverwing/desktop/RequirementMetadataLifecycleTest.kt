package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class RequirementMetadataLifecycleTest {
    @TempDir lateinit var root: Path
    private val link = "https://project.feishu.cn/obt/userstory/detail/123"
    private val old = RequirementMetadata("旧标题", "开发中")
    private val fresh = RequirementMetadata("新标题", "测试中")

    private class Gate(val result: RequirementMetadata) {
        val started = CompletableDeferred<Unit>()
        val release = CountDownLatch(1)
    }
    private class GatedProvider(private vararg val gates: Gate) : RequirementMetadataProvider {
        val calls = AtomicInteger()
        val active = AtomicInteger()
        val maximum = AtomicInteger()
        override fun fetch(requirementLink: String): RequirementMetadata {
            val gate = gates[calls.getAndIncrement()]
            maximum.accumulateAndGet(active.incrementAndGet(), ::maxOf)
            try {
                gate.started.complete(Unit)
                gate.release.await()
                return gate.result
            } finally { active.decrementAndGet() }
        }
        fun releaseAll() = gates.forEach { it.release.countDown() }
    }

    @Test fun `one cancelled consumer does not cancel the shared provider or another consumer`() = runBlocking {
        val gate = Gate(fresh)
        val provider = GatedProvider(gate)
        val scope = CoroutineScope(Job() + Dispatchers.IO)
        val coordinator = RequirementMetadataCoordinator(provider, scope, Dispatchers.IO)
        try {
            withTimeout(5_000) {
                val first = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                gate.started.await()
                val second = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                first.cancelAndJoin()
                assertEquals(1, provider.calls.get())
                assertEquals(1, provider.active.get())
                gate.release.countDown()
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(second.await()).metadata)
                assertTrue(scope.isActive)
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(coordinator.fetch(link, "project")).metadata)
                assertEquals(1, provider.calls.get())
            }
        } finally { provider.releaseAll(); scope.cancel() }
    }

    @Test fun `last cancelled consumer still allows request completion to clean up and populate cache`() = runBlocking {
        val gate = Gate(fresh)
        val provider = GatedProvider(gate)
        val scope = CoroutineScope(Job() + Dispatchers.IO)
        val coordinator = RequirementMetadataCoordinator(provider, scope, Dispatchers.IO)
        try {
            withTimeout(5_000) {
                val subscriber = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                gate.started.await(); subscriber.cancelAndJoin(); gate.release.countDown()
                awaitRequests(scope)
                assertEquals(0, coordinator.inFlightCount)
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(coordinator.fetch(link, "project")).metadata)
                assertEquals(1, provider.calls.get())
            }
        } finally { provider.releaseAll(); scope.cancel() }
    }

    @Test fun `provider cancellation propagates without poisoning scope or failure cache`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        var calls = 0
        val coordinator = RequirementMetadataCoordinator(RequirementMetadataProvider {
            if (++calls == 1) throw CancellationException("provider cancelled independently")
            fresh
        }, this, dispatcher)
        assertFailsWith<CancellationException> { coordinator.fetch(link, "project") }
        assertTrue(isActive)
        assertEquals(0, coordinator.inFlightCount)
        assertEquals(fresh, assertIs<RequirementFetchResult.Success>(coordinator.fetch(link, "project")).metadata)
        assertEquals(2, calls)
    }

    @Test fun `force supersedes ordinary work joins repeated refreshes and prevents late cache overwrite`() = runBlocking {
        val firstGate = Gate(old)
        val refreshGate = Gate(fresh)
        val provider = GatedProvider(firstGate, refreshGate)
        val scope = CoroutineScope(Job() + Dispatchers.IO)
        val access = RequirementCacheAccess(RequirementReadCache(root.resolve("forced-metadata"))) { "account" }
        val coordinator = RequirementMetadataCoordinator(provider, scope, Dispatchers.IO, maxConcurrency = 2, cacheAccess = access)
        try {
            withTimeout(5_000) {
                val first = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                firstGate.started.await()
                val refreshed = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project", force = true) }
                refreshGate.started.await()
                val repeated = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project", force = true) }
                val follower = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                refreshGate.release.countDown()
                listOf(refreshed, repeated, follower).forEach {
                    assertEquals(fresh, assertIs<RequirementFetchResult.Success>(it.await()).metadata)
                }
                firstGate.release.countDown()
                assertEquals(old, assertIs<RequirementFetchResult.Success>(first.await()).metadata)
                awaitRequests(scope)
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(coordinator.fetch(link, "project")).metadata)
                assertEquals(2, provider.calls.get())
                assertEquals(0, coordinator.inFlightCount)
                val restarted = RequirementMetadataCoordinator(provider, scope, Dispatchers.IO, cacheAccess = access)
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(restarted.fetch(link, "project")).metadata)
                assertEquals(2, provider.calls.get(), "The superseded request must not overwrite the refreshed disk entry")
            }
        } finally { provider.releaseAll(); scope.cancel() }
    }

    @Test fun `clear detaches old requests and their results cannot repopulate a newer cache`() = runBlocking {
        val firstGate = Gate(old)
        val newGate = Gate(fresh)
        val provider = GatedProvider(firstGate, newGate)
        val scope = CoroutineScope(Job() + Dispatchers.IO)
        val coordinator = RequirementMetadataCoordinator(provider, scope, Dispatchers.IO)
        try {
            withTimeout(5_000) {
                val first = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                firstGate.started.await(); coordinator.clear()
                val next = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                newGate.started.await(); newGate.release.countDown()
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(next.await()).metadata)
                firstGate.release.countDown()
                assertEquals(old, assertIs<RequirementFetchResult.Success>(first.await()).metadata)
                awaitRequests(scope)
                assertEquals(fresh, assertIs<RequirementFetchResult.Success>(coordinator.fetch(link, "project")).metadata)
                assertEquals(2, provider.calls.get())
            }
        } finally { provider.releaseAll(); scope.cancel() }
    }

    @Test fun `forced refreshes still share the same provider concurrency limit`() = runBlocking {
        val firstGate = Gate(old)
        val refreshGate = Gate(fresh)
        val provider = GatedProvider(firstGate, refreshGate)
        val scope = CoroutineScope(Job() + Dispatchers.IO)
        val coordinator = RequirementMetadataCoordinator(provider, scope, Dispatchers.IO, maxConcurrency = 1)
        try {
            withTimeout(5_000) {
                val first = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project") }
                firstGate.started.await()
                val refreshed = async(start = CoroutineStart.UNDISPATCHED) { coordinator.fetch(link, "project", force = true) }
                assertFalse(refreshGate.started.isCompleted)
                firstGate.release.countDown(); first.await()
                refreshGate.started.await(); refreshGate.release.countDown(); refreshed.await()
                assertEquals(1, provider.maximum.get())
                assertEquals(2, provider.calls.get())
            }
        } finally { provider.releaseAll(); scope.cancel() }
    }

    @Test fun `independent cancellation ends metadata loading and later refresh can recover immediately`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        var cancelProvider = true
        var calls = 0
        val coordinator = RequirementMetadataCoordinator(RequirementMetadataProvider {
            calls++
            if (cancelProvider) throw CancellationException("provider cancelled")
            fresh
        }, this, dispatcher)
        val task = TaskManifest(folderName = "one", taskDirectoryName = "one", featureBranch = "feature/one",
            requirementLink = link, createdAt = "2026-10-03", updatedAt = "2026-10-03", services = emptyList())
        val session = AppSessionStore(AppConfig(meegleProjects = listOf(MeegleProjectConfig("project", "obt"))), listOf(task))
        val paths = ApplicationPaths(root.resolve("metadata-logs"))
        val controller = RequirementController(session, this, coordinator, failureLog = RequirementLinkFailureLog(paths))
        var actionReturned = false
        var draftReturned = false
        controller.refresh(task)
        controller.fetchMetadata(task) { actionReturned = true; assertNull(it) }
        controller.requestDraftMetadata(link) { draftReturned = true; assertNull(it) }
        advanceUntilIdle()
        assertIs<RequirementUiState.Failed>(controller.stateFor(task))
        assertTrue(actionReturned)
        assertTrue(draftReturned)
        assertEquals(0, coordinator.inFlightCount)
        assertFalse(Files.exists(paths.logs), "cancellation must not be recorded as a real metadata failure")
        val cancelledCalls = calls
        cancelProvider = false
        controller.refresh(task)
        advanceUntilIdle()
        assertEquals(fresh, assertIs<RequirementUiState.Loaded>(controller.stateFor(task)).metadata)
        assertEquals(cancelledCalls + 1, calls)
        assertFalse(Files.exists(paths.logs))
    }

    private suspend fun awaitRequests(scope: CoroutineScope) {
        scope.coroutineContext[Job]!!.children.toList().joinAll()
    }
}
