package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.RepositoryRemoteAddress
import com.snowball.silverwing.core.RepositoryRemoteAddressCatalog
import com.snowball.silverwing.core.RepositoryRemoteAddressFailure
import com.snowball.silverwing.core.RepositoryRemoteAddressFailureKind
import com.snowball.silverwing.core.RepositoryRemoteAddressReadException
import com.snowball.silverwing.core.RepositoryRemoteAddressReadStage
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.test.*
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class RepositoryRemoteAddressesTest {
    @Test fun `typed failure is queryable and refresh clears its hint before publishing success`() = runTest {
        val expected = RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.PUSH_ADDRESSES, RepositoryRemoteAddressFailureKind.TIMEOUT)
        var broken = true
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog {
            if (broken) throw RepositoryRemoteAddressReadException(expected)
            emptyList()
        }, this, StandardTestDispatcher(testScheduler))
        cache.load("r", "repo"); runCurrent()
        assertEquals(RepositoryAddressesState.Failed, cache.state("r", "repo"))
        assertEquals(expected, cache.failure("r", "repo/."))
        broken = false
        cache.refresh()
        assertEquals(RepositoryAddressesState.Loading, cache.state("r", "repo"))
        assertNull(cache.failure("r", "repo"))
        runCurrent()
        assertEquals(RepositoryAddressesState.Loaded(emptyList()), cache.state("r", "repo"))
        assertNull(cache.failure("r", "repo"))
    }

    @Test fun `unknown failures keep only fixed categories and a changed path removes the old hint`() = runTest {
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog { path ->
            if (path.endsWith("old")) error("https://user:credential@example.invalid/private.git remote-secret")
            emptyList()
        }, this, StandardTestDispatcher(testScheduler))
        cache.load("r", "old"); runCurrent()
        val failure = assertNotNull(cache.failure("r", "old"))
        assertEquals(RepositoryRemoteAddressReadStage.REMOTE_LIST, failure.stage)
        assertEquals(RepositoryRemoteAddressFailureKind.OTHER, failure.kind)
        assertFalse("credential" in failure.message)
        assertFalse("remote-secret" in failure.toString())
        cache.load("r", "new")
        assertNull(cache.failure("r", "old"))
        assertNull(cache.failure("r", "new"))
        runCurrent()
        assertEquals(RepositoryAddressesState.Loaded(emptyList()), cache.state("r", "new"))
    }

    @Test fun `late old failure cannot overwrite the replacement request hint or later success`() = runBlocking {
        val ui = Executors.newSingleThreadExecutor().asCoroutineDispatcher()
        val scope = CoroutineScope(SupervisorJob() + ui)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val finished = CountDownLatch(1)
        val calls = AtomicInteger()
        val oldFailure = RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.FETCH_ADDRESSES, RepositoryRemoteAddressFailureKind.TIMEOUT)
        val newFailure = RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.PUSH_ADDRESSES, RepositoryRemoteAddressFailureKind.OTHER)
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog {
            when (calls.incrementAndGet()) {
                1 -> {
                    entered.countDown()
                    while (release.count > 0) try { release.await() } catch (_: InterruptedException) { }
                    finished.countDown()
                    throw RepositoryRemoteAddressReadException(oldFailure)
                }
                2 -> throw RepositoryRemoteAddressReadException(newFailure)
                else -> emptyList()
            }
        }, scope, Dispatchers.IO)
        try {
            withContext(ui) { cache.load("r", "repo") }
            assertTrue(withContext(Dispatchers.IO) { entered.await(5, TimeUnit.SECONDS) })
            withContext(ui) { cache.load("r", "repo", force = true) }
            withTimeout(5_000) { while (withContext(ui) { cache.state("r", "repo") != RepositoryAddressesState.Failed }) delay(10) }
            assertEquals(newFailure, withContext(ui) { cache.failure("r", "repo") })
            release.countDown()
            assertTrue(withContext(Dispatchers.IO) { finished.await(5, TimeUnit.SECONDS) })
            delay(30)
            assertEquals(newFailure, withContext(ui) { cache.failure("r", "repo") })
            withContext(ui) { cache.load("r", "repo", force = true); assertNull(cache.failure("r", "repo")) }
            withTimeout(5_000) { while (withContext(ui) { cache.state("r", "repo") !is RepositoryAddressesState.Loaded }) delay(10) }
            assertNull(withContext(ui) { cache.failure("r", "repo") })
        } finally { release.countDown(); scope.cancel(); ui.close() }
    }

    @Test fun `cancelled source becomes retryable instead of staying loading`() = runTest {
        var calls = 0
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog {
            if (++calls == 1) throw CancellationException("cancelled")
            emptyList()
        }, this, StandardTestDispatcher(testScheduler))
        cache.load("r", "repository"); runCurrent()
        assertEquals(RepositoryAddressesState.Failed, cache.state("r", "repository"))
        cache.load("r", "repository", force = true); runCurrent()
        assertEquals(RepositoryAddressesState.Loaded(emptyList()), cache.state("r", "repository"))
        assertEquals(2, calls)
    }

    @Test fun `scope cancellation before start or while queued releases loading without invoking Git`() = runTest {
        val scope = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        var calls = 0
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog { calls++; emptyList() }, scope, StandardTestDispatcher(testScheduler))
        cache.load("r", "repository")
        scope.cancel(); runCurrent()
        assertEquals(RepositoryAddressesState.Failed, cache.state("r", "repository"))
        cache.load("r", "repository", force = true)
        assertEquals(RepositoryAddressesState.Failed, cache.state("r", "repository"))
        assertEquals(0, calls)
    }

    @Test fun `immediate completion does not retain an obsolete job during refresh`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
        var calls = 0
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog {
            listOf(RepositoryRemoteAddress("origin", "https://example.invalid/repo-${++calls}", true, true))
        }, scope, Dispatchers.Unconfined)
        try {
            repeat(3) { request ->
                cache.load("r", "repository", force = true)
                assertEquals("https://example.invalid/repo-${request + 1}",
                    assertIs<RepositoryAddressesState.Loaded>(cache.state("r", "repository")).addresses.single().gitUrl)
            }
        } finally { scope.cancel() }
    }

    @Test fun `late uninterruptible old result cannot replace new path result`() = runBlocking {
        val ui = Executors.newSingleThreadExecutor().asCoroutineDispatcher()
        val scope = CoroutineScope(SupervisorJob() + ui)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val finished = CountDownLatch(1)
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog { path ->
            if (path.endsWith("old")) {
                entered.countDown()
                while (release.count > 0) try { release.await() } catch (_: InterruptedException) { }
                finished.countDown()
            }
            listOf(RepositoryRemoteAddress("origin", "https://example.invalid/" + path.fileName, true, true))
        }, scope, Dispatchers.IO)
        try {
            withContext(ui) { cache.load("r", "old") }
            assertTrue(withContext(Dispatchers.IO) { entered.await(5, TimeUnit.SECONDS) })
            withContext(ui) { cache.load("r", "new") }
            withTimeout(5000) { while (withContext(ui) { cache.state("r", "new") !is RepositoryAddressesState.Loaded }) delay(10) }
            release.countDown()
            assertTrue(withContext(Dispatchers.IO) { finished.await(5, TimeUnit.SECONDS) })
            delay(30)
            val state = withContext(ui) { cache.state("r", "new") }
            assertEquals("https://example.invalid/new", assertIs<RepositoryAddressesState.Loaded>(state).addresses.single().gitUrl)
        } finally { release.countDown(); scope.cancel(); ui.close() }
    }

    @Test fun `shared cache retries failures and refreshes loaded repositories independently`() = runTest {
        val calls = mutableListOf<String>()
        var broken = true
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog { path ->
            calls += path.toString()
            if (path.endsWith("broken") && broken) error("read failed")
            listOf(RepositoryRemoteAddress("origin", "https://example.invalid/repo.git", true, true))
        }, this, StandardTestDispatcher(testScheduler))
        cache.load("r", "repo")
        cache.load("r", "repo/.")
        cache.load("b", "broken")
        advanceUntilIdle()
        assertEquals(2, calls.size)
        assertIs<RepositoryAddressesState.Loaded>(cache.state("r", "repo"))
        assertIs<RepositoryAddressesState.Failed>(cache.state("b", "broken"))
        cache.load("b", "broken")
        assertEquals(2, calls.size)
        broken = false
        cache.load("b", "broken", force = true)
        advanceUntilIdle()
        assertIs<RepositoryAddressesState.Loaded>(cache.state("b", "broken"))
        cache.refresh()
        advanceUntilIdle()
        assertEquals(5, calls.size)
    }

    @Test fun `new path and rapid refresh discard cancelled requests`() = runTest {
        var calls = 0
        val cache = RepositoryRemoteAddresses(RepositoryRemoteAddressCatalog { calls++; emptyList() }, this, StandardTestDispatcher(testScheduler))
        cache.load("r", "old")
        cache.load("r", "new")
        cache.load("r", "new", true)
        advanceUntilIdle()
        assertEquals(1, calls)
        assertEquals(RepositoryAddressesState.Loaded(emptyList()), cache.state("r", "new"))
        assertIs<RepositoryAddressesState.Loading>(cache.state("r", "old"))
    }
}
