@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class RequirementBodyPersistentCacheTest {
    @TempDir lateinit var root: Path
    private val clock = RequirementCacheTestClock()
    private var account: String? = "host/tenant/user-a"
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "1", "Title", "https://project.feishu.cn/obt/userstory/detail/1")
    private fun access() = RequirementCacheAccess(RequirementReadCache(root, clock)) { account }
    private abstract class Source : ParticipatedWorkItemsSource {
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = error("No list read needed")
    }

    @Test fun `task and list readers share disk body across restart without querying the list`() = runTest {
        var reads = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String { reads++; return "# 内容" } }
        val dispatcher = StandardTestDispatcher(testScheduler)
        fun repository() = RequirementBodyRepository(source, cacheAccess = access(), clock = clock)
        val firstRepository = repository()
        val taskReader = RequirementBodyController(firstRepository, backgroundScope, dispatcher)
        taskReader.select(item); runCurrent()
        val restartedRepository = repository()
        val catalog = ParticipatedWorkItemsController(source, backgroundScope, dispatcher, restartedRepository)
        catalog.select(item.copy(title = "different display title", url = item.url + "?from=task#details")); runCurrent()
        assertEquals(1, reads)
        assertFalse(catalog.state.initialized)
        assertEquals(ParticipatedWorkItemBodyState.Ready(item.key, "# 内容"), catalog.bodyState)
    }

    @Test fun `Markdown survives the JSON expiry and is replaced only by explicit refresh`() = runTest {
        var reads = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = "body-${++reads}" }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val reader = RequirementBodyController(RequirementBodyRepository(source, cacheAccess = access(), clock = clock), backgroundScope, dispatcher)
        reader.select(item); runCurrent()
        clock.advance(REQUIREMENT_CACHE_TTL.minusMillis(1))
        reader.select(item); runCurrent()
        assertEquals(1, reads)
        clock.advance(Duration.ofMillis(1))
        reader.select(item); runCurrent()
        assertEquals(1, reads)
        assertEquals("body-1", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
        reader.select(item, true); runCurrent()
        assertEquals(2, reads)
        assertEquals("body-2", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
    }

    @Test fun `force reads live preserves old disk on failure and overwrites with successful empty content`() = runTest {
        var fail = false
        var content = "old"
        var reads = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String { reads++; if (fail) error("refresh failed"); return content } }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val repository = RequirementBodyRepository(source, cacheAccess = access(), clock = clock)
        val reader = RequirementBodyController(repository, backgroundScope, dispatcher)
        reader.select(item); runCurrent()
        fail = true
        reader.select(item, true); runCurrent()
        assertEquals("old", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
        assertContains(reader.cacheError!!, "refresh failed")
        assertEquals("old", RequirementBodyRepository(source, cacheAccess = access(), clock = clock).read(item, false, dispatcher))
        assertEquals(2, reads)
        fail = false; content = ""
        reader.select(item, true); runCurrent()
        assertEquals("", assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState).content)
        assertEquals("", RequirementBodyRepository(source, cacheAccess = access(), clock = clock).read(item, false, dispatcher))
        assertEquals(3, reads)
    }

    @Test fun `account project type id and link isolate bodies while unknown identity cannot hit or save`() = runTest {
        var reads = 0
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = "${account}-${++reads}" }
        val access = access()
        val repository = RequirementBodyRepository(source, cacheAccess = access, clock = clock)
        val dispatcher = StandardTestDispatcher(testScheduler)
        val original = repository.read(item, false, dispatcher)
        for (other in listOf(item.copy(projectKey = "other"), item.copy(type = "bug"), item.copy(id = "2"),
            item.copy(url = "https://project.feishu.cn/another/userstory/detail/1"))) {
            assertNotEquals(original, repository.read(other, false, dispatcher))
        }
        account = "host/tenant/user-b"
        access.recheck(dispatcher)
        assertNotEquals(original, repository.read(item, false, dispatcher))
        account = "host/other-tenant/user-a"
        access.recheck(dispatcher)
        assertNotEquals(original, repository.read(item, false, dispatcher))
        account = null
        access.invalidate()
        val unknown = repository.read(item, false, dispatcher)
        assertNotEquals(unknown, repository.read(item, false, dispatcher))
        account = "host/tenant/user-a"
        access.recheck(dispatcher)
        assertEquals(original, repository.read(item, false, dispatcher))
        assertEquals(9, reads)
    }

    @Test fun `identity changed during a body read neither publishes nor persists under the old account`() = runTest {
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem): String { account = "new-user"; return "new-user-content" } }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val repository = RequirementBodyRepository(source, cacheAccess = access(), clock = clock)
        assertFailsWith<RequirementIdentityChangedException> { repository.read(item, false, dispatcher) }
        assertEquals(0L, Files.list(root).use { it.count() })
    }

    @Test fun `disk write failure still presents live body`() = runTest {
        val obstacle = root.resolve("obstacle")
        Files.writeString(obstacle, "preserved")
        val source = object : Source() { override fun loadBody(item: ParticipatedWorkItem) = "live" }
        val repository = RequirementBodyRepository(source,
            cacheAccess = RequirementCacheAccess(RequirementReadCache(obstacle.resolve("cache"), clock)) { account }, clock = clock)
        assertEquals("live", repository.read(item, false, StandardTestDispatcher(testScheduler)))
    }
}
