package com.snowball.silverwing.desktop

import kotlinx.coroutines.*
import kotlinx.serialization.builtins.serializer
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.ZoneOffset
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

internal class RequirementCacheTestClock(var now: Instant = Instant.parse("2026-10-03T00:00:00Z")) : Clock() {
    override fun instant(): Instant = now
    override fun getZone(): ZoneId = ZoneOffset.UTC
    override fun withZone(zone: ZoneId): Clock = this
    fun advance(duration: Duration) { now = now.plus(duration) }
}

class RequirementReadCacheTest {
    @TempDir lateinit var root: Path
    private val clock = RequirementCacheTestClock()
    private val serializer = String.serializer()
    private fun cache() = RequirementReadCache(root, clock)
    private fun files(): List<Path> = Files.list(root).use { it.toList() }

    @Test fun `files survive a new instance and hits never renew the fourteen day lifetime`() {
        val first = cache()
        first.write("body", serializer, "original")
        val fetchedAt = clock.millis()
        clock.advance(REQUIREMENT_CACHE_TTL.minusMillis(1))
        assertEquals(RequirementCachedValue("original", fetchedAt), cache().read("body", serializer))
        clock.advance(Duration.ofMillis(1))
        assertNull(first.read("body", serializer))
        assertTrue(files().isEmpty())
    }

    @Test fun `first use and hourly cleanup delete expired unrelated entries without extending fresh ones`() {
        val cache = cache()
        cache.write("older", serializer, "old")
        clock.advance(Duration.ofDays(1))
        cache.write("newer", serializer, "new")
        clock.advance(Duration.ofDays(13))
        assertEquals("new", cache.read("newer", serializer)?.value)
        assertEquals(1, files().size)
        clock.advance(Duration.ofDays(1))
        assertNull(cache().read("missing", serializer))
        assertTrue(files().isEmpty())
    }

    @Test fun `corrupt wrong version mismatched key and wrong payload recover as cache misses`() {
        val cache = cache()
        for (corrupt in listOf<(String) -> String>(
            { "not json" }, { it.replace("\"version\":1", "\"version\":999") },
            { it.replace(Regex("\"key\":\"[^\"]+\""), "\"key\":\"wrong\"") },
            { it.replace("\"value\":\"original\"", "\"value\":{}") },
        )) {
            cache.write("body", serializer, "original")
            val file = files().single()
            Files.writeString(file, corrupt(Files.readString(file)))
            assertNull(cache.read("body", serializer))
            assertFalse(Files.exists(file))
        }
        cache.write("body", serializer, "recovered")
        assertEquals("recovered", cache().read("body", serializer)?.value)
    }

    @Test fun `future timestamps are not accepted and cache keys are not path components`() {
        val cache = cache()
        cache.write("../account/secret-identity", serializer, "value", clock.millis() + 1)
        assertTrue(files().single().fileName.toString().matches(Regex("[a-f0-9]{64}\\.json")))
        assertNull(cache.read("../account/secret-identity", serializer))
    }

    @Test fun `unwritable cache cannot fail a successful application read`() {
        val parentFile = root.resolve("not-a-directory")
        Files.writeString(parentFile, "keep")
        val cache = RequirementReadCache(parentFile.resolve("requirements"), clock)
        cache.write("body", serializer, "success")
        assertNull(cache.read("body", serializer))
        assertEquals("keep", Files.readString(parentFile))
    }

    @Test fun `concurrent instances publish complete files without leaving temporary files`() = runBlocking {
        val copies = List(4) { cache() }
        (1..24).map { i -> async(Dispatchers.IO) {
            copies[i % 4].write("shared", serializer, "body-$i".repeat(200))
            assertNotNull(copies[(i + 1) % 4].read("shared", serializer))
        } }.awaitAll()
        assertTrue(cache().read("shared", serializer)!!.value.startsWith("body-"))
        assertEquals(1, files().size)
    }

    @Test fun `concurrent and later local readers share the confirmed session identity`() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val release = CountDownLatch(1)
        val calls = AtomicInteger()
        val access = RequirementCacheAccess(cache()) {
            calls.incrementAndGet(); started.complete(Unit)
            check(release.await(3, TimeUnit.SECONDS))
            "account"
        }
        val first = async(start = CoroutineStart.UNDISPATCHED) { access.identity(Dispatchers.IO) }
        try {
            withTimeout(3_000) { started.await() }
            val followers = List(4) { async(start = CoroutineStart.UNDISPATCHED) { access.identity(Dispatchers.IO) } }
            release.countDown()
            (followers + first).forEach { assertEquals("account", it.await()) }
            assertEquals(1, calls.get())
            assertEquals("account", access.identity(Dispatchers.IO))
            assertEquals(1, calls.get())
            assertEquals("account", access.recheck(Dispatchers.IO))
            assertEquals(2, calls.get())
        } finally { release.countDown(); first.cancel() }
    }
}
