@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.ParticipatedSprint
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsResult
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class ParticipatedWorkItemsControllerTest {
    private val projects = listOf(MeegleProjectConfig("project", "p"))
    private val sprint = ParticipatedSprint("project", "Project", "current", "Current", "进行中")

    @Test
    fun `source cancellation releases loading and ordinary retry reads again`() = runTest {
        var calls = 0
        val warmed = mutableListOf<List<ParticipatedWorkItem>>()
        val source = Source { _, _, _ ->
            if (++calls == 1) throw CancellationException("query cancelled")
            result(item("new"))
        }
        val controller = ParticipatedWorkItemsController(source, this, StandardTestDispatcher(testScheduler),
            onListLoaded = { warmed += it })

        controller.load(projects, sprintKey = sprint.key)
        runCurrent()
        assertFalse(controller.state.loading)
        assertFalse(controller.state.initialized)
        assertFalse(controller.state.listComplete)
        assertEquals(null, controller.state.error)
        assertTrue(warmed.isEmpty())

        controller.load(projects, sprintKey = sprint.key)
        runCurrent()
        assertEquals(2, calls)
        assertTrue(controller.state.listComplete)
        assertEquals(listOf(item("new")), controller.state.items)
        assertEquals(listOf(listOf(item("new"))), warmed)
        controller.load(projects, sprintKey = sprint.key)
        runCurrent()
        assertEquals(2, calls, "A successful result still uses the ordinary-load cache")
    }

    @Test
    fun `cancelled refresh retains previous items selection and body without prewarming again`() = runTest {
        var calls = 0
        val old = item("old")
        val warmed = mutableListOf<List<ParticipatedWorkItem>>()
        val selected = mutableListOf<ParticipatedWorkItem>()
        val source = Source { _, _, _ ->
            if (++calls == 2) throw CancellationException("refresh cancelled")
            result(old)
        }
        val controller = ParticipatedWorkItemsController(source, this, StandardTestDispatcher(testScheduler),
            onListLoaded = { warmed += it }, onSelected = { selected += it })
        controller.load(projects)
        runCurrent()
        val oldBody = controller.bodyState
        controller.load(projects, force = true)
        runCurrent()

        assertFalse(controller.state.loading)
        assertEquals(listOf(old), controller.state.items)
        assertEquals(sprint.key, controller.state.selectedSprintKey)
        assertEquals(old.key, controller.selectedKey)
        assertEquals(oldBody, controller.bodyState)
        assertEquals(1, source.bodyReads)
        assertEquals(listOf(listOf(old)), warmed)
        assertEquals(listOf(old), selected)
        controller.load(projects)
        runCurrent()
        assertEquals(3, calls)
    }

    @Test
    fun `scope cancelled before dispatch or already closed cannot leave a loading flag`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(SupervisorJob(coroutineContext[Job]) + dispatcher)
        var calls = 0
        val source = Source { _, _, _ -> calls++; result(item("never")) }
        val controller = ParticipatedWorkItemsController(source, scope, dispatcher)
        controller.load(projects)
        assertTrue(controller.state.loading)
        scope.cancel()
        runCurrent()

        assertFalse(controller.state.loading)
        assertFalse(controller.state.initialized)
        assertEquals(0, calls)
        controller.load(projects)
        assertFalse(controller.state.loading, "An already cancelled scope never enters the launch body")
        runCurrent()
        assertEquals(0, calls)
        assertEquals(null, controller.selectedKey)
    }

    @Test
    fun `scope cancellation during a suspended read clears loading and publishes no callbacks`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(SupervisorJob(coroutineContext[Job]) + dispatcher)
        var started = false
        val warmed = mutableListOf<List<ParticipatedWorkItem>>()
        val selected = mutableListOf<ParticipatedWorkItem>()
        val source = Source { _, _, _ -> started = true; awaitCancellation() }
        val controller = ParticipatedWorkItemsController(source, scope, dispatcher,
            onListLoaded = { warmed += it }, onSelected = { selected += it })
        controller.load(projects)
        runCurrent()
        assertTrue(started)
        scope.cancel()
        runCurrent()

        assertFalse(controller.state.loading)
        assertFalse(controller.state.initialized)
        assertTrue(controller.state.items.isEmpty())
        assertTrue(warmed.isEmpty())
        assertTrue(selected.isEmpty())
        assertEquals(0, source.bodyReads)
    }

    @Test
    fun `a non cooperative read finishing after scope close cannot publish its result`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(SupervisorJob(coroutineContext[Job]) + dispatcher)
        val release = CompletableDeferred<Unit>()
        val warmed = mutableListOf<List<ParticipatedWorkItem>>()
        val source = Source { _, _, _ ->
            withContext(NonCancellable) { release.await() }
            result(item("late"))
        }
        val controller = ParticipatedWorkItemsController(source, scope, dispatcher, onListLoaded = { warmed += it })
        try {
            controller.load(projects)
            runCurrent()
            scope.cancel()
            release.complete(Unit)
            runCurrent()

            assertFalse(controller.state.loading)
            assertFalse(controller.state.initialized)
            assertTrue(controller.state.items.isEmpty())
            assertTrue(warmed.isEmpty())
            assertEquals(0, source.bodyReads)
            assertEquals(null, controller.selectedKey)
        } finally { scope.cancel(); release.complete(Unit); runCurrent() }
    }

    @Test
    fun `old project or sprint cancellation never clears the replacement request or warms stale items`() = runTest {
        for (changeProject in listOf(false, true)) {
            val oldRelease = CompletableDeferred<Unit>()
            val newRelease = CompletableDeferred<Unit>()
            val oldProjects = listOf(MeegleProjectConfig("old", "old"))
            val newProjects = if (changeProject) listOf(MeegleProjectConfig("new", "new")) else oldProjects
            val oldSprint = "old:first"
            val newSprint = "${newProjects.single().projectKey}:second"
            val warmed = mutableListOf<List<ParticipatedWorkItem>>()
            val selected = mutableListOf<ParticipatedWorkItem>()
            val source = Source { requestProjects, key, _ ->
                if (key == oldSprint) withContext(NonCancellable) { oldRelease.await() }
                else newRelease.await()
                result(item(key.orEmpty(), requestProjects.single().projectKey), key)
            }
            val controller = ParticipatedWorkItemsController(source, this, StandardTestDispatcher(testScheduler),
                onListLoaded = { warmed += it }, onSelected = { selected += it })
            try {
                controller.load(oldProjects, sprintKey = oldSprint)
                runCurrent()
                controller.load(newProjects, sprintKey = newSprint)
                runCurrent()
                oldRelease.complete(Unit)
                runCurrent()
                assertTrue(controller.state.loading, "Old cancellation must leave the new request loading")
                assertEquals(newSprint, controller.state.selectedSprintKey)
                assertTrue(controller.state.items.isEmpty())
                assertTrue(warmed.isEmpty())
                newRelease.complete(Unit)
                runCurrent()

                val expected = item(newSprint, newProjects.single().projectKey)
                assertFalse(controller.state.loading)
                assertEquals(listOf(expected), controller.state.items)
                assertEquals(expected.key, controller.selectedKey)
                assertEquals(listOf(listOf(expected)), warmed)
                assertEquals(listOf(expected), selected)
            } finally { oldRelease.complete(Unit); newRelease.complete(Unit); runCurrent() }
        }
    }

    @Test
    fun `total failure retains the previous result and an ordinary retry can replace it`() = runTest {
        var calls = 0
        val old = item("old")
        val recovered = item("recovered")
        val warmed = mutableListOf<List<ParticipatedWorkItem>>()
        val source = Source { _, _, _ ->
            when (++calls) {
                1 -> result(old)
                2 -> error("temporarily unavailable")
                else -> result(recovered)
            }
        }
        val controller = ParticipatedWorkItemsController(source, this, StandardTestDispatcher(testScheduler),
            onListLoaded = { warmed += it })
        controller.load(projects)
        runCurrent()
        controller.load(projects, force = true)
        runCurrent()

        assertFalse(controller.state.loading)
        assertEquals(listOf(old), controller.state.items)
        assertEquals(old.key, controller.selectedKey)
        assertNotNull(controller.state.error)
        assertTrue(controller.state.error.orEmpty().contains("保留上次结果"))
        assertEquals(listOf(listOf(old)), warmed)
        controller.load(projects)
        runCurrent()
        assertEquals(3, calls)
        assertEquals(listOf(recovered), controller.state.items)
        assertEquals(recovered.key, controller.selectedKey)
        assertEquals(null, controller.state.error)
        assertEquals(listOf(listOf(old), listOf(recovered)), warmed)
    }

    @Test
    fun `partial refresh preserves displayed results but prewarms only the new partial payload`() = runTest {
        var calls = 0
        val old = item("old")
        val partial = item("partial")
        val warmed = mutableListOf<List<ParticipatedWorkItem>>()
        val selected = mutableListOf<ParticipatedWorkItem>()
        val source = Source { _, _, _ ->
            if (++calls == 1) result(old)
            else result(partial).copy(failures = listOf("one project failed"), completedQueries = 1)
        }
        val controller = ParticipatedWorkItemsController(source, this, StandardTestDispatcher(testScheduler),
            onListLoaded = { warmed += it }, onSelected = { selected += it })
        controller.load(projects)
        runCurrent()
        controller.load(projects, force = true)
        runCurrent()

        assertFalse(controller.state.loading)
        assertFalse(controller.state.listComplete)
        assertEquals(listOf(old), controller.state.items)
        assertNotNull(controller.state.warning)
        assertEquals(null, controller.state.error)
        assertEquals(1, source.bodyReads)
        assertEquals(listOf(listOf(old), listOf(partial)), warmed)
        assertEquals(listOf(old, old), selected)
        controller.load(projects)
        runCurrent()
        assertEquals(2, calls, "Partial-result caching continues to require an explicit refresh")
    }

    private fun item(id: String, projectKey: String = "project") = ParticipatedWorkItem(
        projectKey, "Project", "userstory", "User Story", id, "Item $id", "https://example.invalid/$id",
    )

    private fun result(item: ParticipatedWorkItem, selectedSprintKey: String? = sprint.key) = ParticipatedWorkItemsResult(
        listOf(item), completedQueries = 2, sprints = listOf(sprint), selectedSprintKey = selectedSprintKey,
    )

    private class Source(
        private val read: suspend (List<MeegleProjectConfig>, String?, String?) -> ParticipatedWorkItemsResult,
    ) : ParticipatedWorkItemsSource {
        var bodyReads = 0
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) =
            read(projects, sprintKey, defaultSprintProjectKey)
        override fun loadBody(item: ParticipatedWorkItem): String { bodyReads++; return item.title }
    }
}
