@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.math.BigDecimal
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class ParticipatedWorkItemsPersistentCacheTest {
    @TempDir lateinit var root: Path
    private val clock = RequirementCacheTestClock()
    private var identity: String? = "host/tenant/account"
    private val projects = listOf(MeegleProjectConfig("project", "obt"))
    private val sprint = ParticipatedSprint("project", "Project", "s1", "Sprint", "进行中")
    private val item = ParticipatedWorkItem("project", "Project", "userstory", "需求", "1", "old", "https://project.feishu.cn/obt/userstory/detail/1",
        developers = listOf(RequirementPerson("Developer", "dev@example.invalid")), mySprintEstimateDays = BigDecimal("1.250"), status = "开发中")
    private fun result(title: String = "old", selected: String = sprint.key) = ParticipatedWorkItemsResult(
        listOf(item.copy(title = title)), completedQueries = 4, sprints = listOf(sprint), selectedSprintKey = selected)
    private class Source(val read: suspend (List<MeegleProjectConfig>, String?, String?) -> ParticipatedWorkItemsResult) : ParticipatedWorkItemsSource {
        var calls = 0
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
            calls++; return read(projects, sprintKey, defaultSprintProjectKey)
        }
        override fun loadBody(item: ParticipatedWorkItem) = item.title
    }
    private fun TestScope.controller(source: Source,
        access: RequirementCacheAccess = RequirementCacheAccess(RequirementReadCache(root, clock)) { identity }) =
        ParticipatedWorkItemsController(source, backgroundScope, StandardTestDispatcher(testScheduler), cacheAccess = access, clock = clock)

    @Test fun `restart restores full list and exact estimates and expiry is based on original fetch`() = runTest {
        val source = Source { _, _, _ -> result() }
        controller(source).load(projects); runCurrent()
        val restarted = controller(source)
        clock.advance(REQUIREMENT_CACHE_TTL.minusMillis(1))
        restarted.load(projects); runCurrent()
        assertEquals(1, source.calls)
        assertEquals(listOf(item), restarted.state.items)
        assertEquals(BigDecimal("1.250"), restarted.state.totalMySprintEstimateDays)
        clock.advance(Duration.ofMillis(1))
        restarted.load(projects); runCurrent()
        assertEquals(2, source.calls)
        assertTrue(restarted.state.listComplete)
    }

    @Test fun `force refresh replaces explicit and auto selection aliases while failure preserves valid snapshot`() = runTest {
        var title = "old"
        var fail = false
        val source = Source { _, _, _ -> if (fail) ParticipatedWorkItemsResult(emptyList(), listOf("network unavailable")) else result(title) }
        val controller = controller(source)
        controller.load(projects); runCurrent()
        title = "fresh"
        controller.load(projects, force = true); runCurrent()
        assertEquals(2, source.calls)
        assertEquals("fresh", controller.state.items.single().title)
        val restarted = controller(source)
        restarted.load(projects); runCurrent()
        assertEquals(2, source.calls)
        assertEquals("fresh", restarted.state.items.single().title)
        fail = true
        controller.load(projects, force = true); runCurrent()
        assertEquals(3, source.calls)
        assertNotNull(controller.state.error)
        assertEquals("fresh", controller.state.items.single().title)
        val afterFailure = controller(source)
        afterFailure.load(projects); runCurrent()
        assertEquals(3, source.calls)
        assertNull(afterFailure.state.error)
        assertEquals("fresh", afterFailure.state.items.single().title)
    }

    @Test fun `project catalog sprint default project source and account all isolate list queries`() = runTest {
        val source = Source { _, requested, _ -> result(selected = requested ?: sprint.key) }
        fun read(projects: List<MeegleProjectConfig> = this@ParticipatedWorkItemsPersistentCacheTest.projects,
            sprintKey: String? = sprint.key, default: String? = null) {
            controller(source).load(projects, sprintKey = sprintKey, defaultSprintProjectKey = default)
            runCurrent()
        }
        read(); read()
        assertEquals(1, source.calls)
        read(sprintKey = "project:s2")
        read(default = "other")
        read(listOf(MeegleProjectConfig("other-project", "obt")))
        read(listOf(MeegleProjectConfig("project", "renamed")))
        identity = "different-host/tenant/account"; read()
        identity = "host/different-tenant/account"; read()
        identity = "host/tenant/another-account"; read()
        assertEquals(8, source.calls)
    }

    @Test fun `account switch and unknown account never retain another accounts items on a failed refresh`() = runTest {
        var fail = false
        val source = Source { _, _, _ -> if (fail) ParticipatedWorkItemsResult(emptyList(), listOf("not allowed")) else result() }
        val access = RequirementCacheAccess(RequirementReadCache(root, clock)) { identity }
        val controller = controller(source, access)
        controller.load(projects); runCurrent()
        fail = true; identity = "another-account"
        controller.load(projects, force = true); runCurrent()
        assertTrue(controller.state.items.isEmpty())
        assertNotNull(controller.state.error)
        identity = null
        access.invalidate()
        controller.load(projects); runCurrent()
        controller.load(projects); runCurrent()
        assertEquals(4, source.calls)
        identity = "host/tenant/account"
        val restored = controller(source)
        restored.load(projects); runCurrent()
        assertEquals(listOf(item), restored.state.items)
        assertEquals(4, source.calls)
    }

    @Test fun `real empty results cache but incomplete results do not replace a good disk snapshot`() = runTest {
        var response = result().copy(items = emptyList())
        val source = Source { _, _, _ -> response }
        controller(source).load(projects); runCurrent()
        controller(source).load(projects); runCurrent()
        assertEquals(1, source.calls)
        response = result("partial").copy(failures = listOf("one type failed"), completedQueries = 1)
        val controller = controller(source)
        controller.load(projects, force = true); runCurrent()
        assertNotNull(controller.state.warning)
        val restarted = controller(source)
        restarted.load(projects); runCurrent()
        assertTrue(restarted.state.items.isEmpty())
        assertEquals(2, source.calls)
    }

    @Test fun `cancelled old sprint cannot write a late snapshot or replace the new UI`() = runTest {
        val release = CompletableDeferred<Unit>()
        var blockOld = true
        val source = Source { _, requested, _ ->
            if (requested == "project:old" && blockOld) withContext(NonCancellable) { release.await() }
            result(requested.orEmpty(), requested ?: sprint.key)
        }
        val controller = controller(source)
        controller.load(projects, sprintKey = "project:old"); runCurrent()
        controller.load(projects, sprintKey = "project:new"); runCurrent()
        release.complete(Unit); runCurrent()
        assertEquals("project:new", controller.state.items.single().title)
        blockOld = false
        controller(source).load(projects, sprintKey = "project:old"); runCurrent()
        assertEquals(3, source.calls, "The cancelled result must not be written under its old cache key")
    }
}
