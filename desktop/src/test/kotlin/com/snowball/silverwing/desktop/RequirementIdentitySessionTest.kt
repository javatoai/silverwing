@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class RequirementIdentitySessionTest {
    @TempDir lateinit var root: Path
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "1", "Title",
        "https://project.feishu.cn/obt/userstory/detail/1")
    private abstract class Source : ParticipatedWorkItemsSource {
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = error("No list")
    }

    @Test fun `startup probe survives a reader cancellation and later local reads never probe`() = runBlocking {
        val entered = CountDownLatch(1); val release = CountDownLatch(1); val calls = AtomicInteger()
        val owner = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val access = RequirementCacheAccess(RequirementReadCache(root), scope = owner) {
            calls.incrementAndGet(); entered.countDown(); check(release.await(4, TimeUnit.SECONDS)); "account"
        }
        val startup = async { access.identity(Dispatchers.IO) }
        val other = async { access.identity(Dispatchers.IO) }
        try {
            withTimeout(3_000) { while (entered.count > 0) delay(10) }
            startup.cancelAndJoin()
            release.countDown()
            assertEquals("account", other.await())
            repeat(10) { assertEquals("account", access.identity(Dispatchers.IO)) }
            assertEquals(1, calls.get())
        } finally { release.countDown(); owner.cancel(); startup.cancel(); other.cancel() }
    }

    @Test fun `offline recheck keeps verified local identity but strict refresh fails and logout clears it`() = runTest {
        var result: MeegleRequirementIdentityResult = MeegleRequirementIdentityResult.Authenticated("a")
        val dispatcher = StandardTestDispatcher(testScheduler)
        val access = RequirementCacheAccess(RequirementReadCache(root), backgroundScope, probe = { result }) { null }
        assertEquals("a", access.identity(dispatcher))
        val revision = access.snapshot.revision
        result = MeegleRequirementIdentityResult.Unavailable
        assertEquals("a", access.recheck(dispatcher))
        assertEquals("a", access.identity(dispatcher))
        assertFailsWith<IllegalStateException> { access.recheck(dispatcher, strict = true) }
        assertEquals(revision, access.snapshot.revision)
        result = MeegleRequirementIdentityResult.LoggedOut
        assertNull(access.recheck(dispatcher))
        assertTrue(access.snapshot.confirmed)
        assertTrue(access.snapshot.revision > revision)
        assertFailsWith<RequirementIdentityChangedException> { access.verifySession("a") }
        assertFailsWith<IllegalStateException> { access.ensureNotLoggedOut() }
    }

    @Test fun `same account emits no invalidation while profile edits and account changes do`() = runTest {
        var account = "a"; var profile = 1; var calls = 0
        val dispatcher = StandardTestDispatcher(testScheduler)
        val access = RequirementCacheAccess(RequirementReadCache(root), backgroundScope, contextStamp = { profile }) { calls++; account }
        val changes = mutableListOf<RequirementIdentitySnapshot>()
        backgroundScope.launch(start = CoroutineStart.UNDISPATCHED) { access.changes.collect { changes.add(it) } }
        access.identity(dispatcher); access.recheck(dispatcher); runCurrent()
        assertTrue(changes.isEmpty()); assertEquals(2, calls)
        account = "b"; access.recheck(dispatcher); runCurrent()
        assertEquals("b", changes.single().identity)
        profile++
        assertEquals("b", access.identity(dispatcher)); runCurrent()
        assertEquals(2, changes.size); assertNull(changes.last().identity)
        assertEquals(4, calls)
    }

    @Test fun `cached Markdown reopening invokes neither delayed identity commands nor remote body`() = runTest {
        var checks = 0; var remote = 0; var unavailable = false
        val dispatcher = StandardTestDispatcher(testScheduler)
        val access = RequirementCacheAccess(RequirementReadCache(root), backgroundScope, probe = {
            checks++
            if (unavailable) MeegleRequirementIdentityResult.Unavailable else MeegleRequirementIdentityResult.Authenticated("a")
        }) { null }
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String { remote++; return "# local" } }
        val repository = RequirementBodyRepository(source, cacheAccess = access)
        repository.readDocument(item, false, dispatcher)
        val initialChecks = checks
        val reader = RequirementBodyController(repository, backgroundScope, dispatcher)
        repeat(5) {
            reader.select(item); runCurrent()
            assertEquals("# local", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
            reader.clear()
        }
        assertEquals(initialChecks, checks); assertEquals(1, remote)
        unavailable = true
        access.recheck(dispatcher)
        assertEquals("# local", repository.read(item, false, dispatcher))
        assertFailsWith<IllegalStateException> { repository.read(item, true, dispatcher) }
        assertEquals("# local", repository.localDocument(item, dispatcher)!!.content)
        assertEquals(1, remote)
    }

    @Test fun `confirmed account change reloads open reader without retaining old account content`() = runTest {
        var account = "a"
        val dispatcher = StandardTestDispatcher(testScheduler)
        val access = RequirementCacheAccess(RequirementReadCache(root), backgroundScope) { account }
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = account }
        val repository = RequirementBodyRepository(source, cacheAccess = access)
        val reader = RequirementBodyController(repository, backgroundScope, dispatcher)
        reader.select(item); runCurrent()
        val old = reader.document!!
        account = "b"; access.recheck(dispatcher); runCurrent()
        assertEquals("b", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
        assertNotEquals(old.key, reader.document!!.key)
        assertEquals("a", repository.reload(old, dispatcher)!!.content)
        reader.clear()
    }

    @Test fun `initial unknown account never reads another accounts local Markdown`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val known = RequirementCacheAccess(RequirementReadCache(root), backgroundScope) { "a" }
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = "remote" }
        RequirementBodyRepository(source, cacheAccess = known).readDocument(item, false, dispatcher)
        val unknown = RequirementCacheAccess(RequirementReadCache(root), backgroundScope,
            probe = { MeegleRequirementIdentityResult.Unavailable }) { null }
        val result = RequirementBodyRepository(source, cacheAccess = unknown).readDocument(item, false, dispatcher)
        assertNull(result.document); assertNotNull(result.cacheError)
    }

    @Test fun `explicit invalidation during a probe prevents the obsolete identity being published`() = runBlocking {
        val entered = CountDownLatch(1); val release = CountDownLatch(1)
        val owner = CoroutineScope(SupervisorJob())
        val access = RequirementCacheAccess(RequirementReadCache(root), owner) {
            entered.countDown(); release.await(3, TimeUnit.SECONDS); "old"
        }
        val pending = async { access.identity(Dispatchers.IO) }
        try {
            withTimeout(3_000) { while (entered.count > 0) delay(10) }
            access.invalidate(loggedOut = true); release.countDown()
            assertFailsWith<CancellationException> { pending.await() }
            assertNull(access.snapshot.identity); assertTrue(access.snapshot.confirmed)
        } finally { release.countDown(); owner.cancel(); pending.cancel() }
    }
}
