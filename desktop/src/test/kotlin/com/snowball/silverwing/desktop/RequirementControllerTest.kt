package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.RequirementMetadata
import com.snowball.silverwing.core.RequirementMetadataProvider
import com.snowball.silverwing.core.RequirementMaterialsRequest
import com.snowball.silverwing.core.RequirementMaterialsResult
import com.snowball.silverwing.core.RequirementParticipants
import com.snowball.silverwing.core.RequirementPerson
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.jupiter.api.Test
import java.time.Clock
import java.time.Instant
import java.time.ZoneId
import java.time.Duration
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class RequirementControllerTest {
    @Test
    fun `same key shares in flight request and success cache while force bypasses cache`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val calls = AtomicInteger()
        val clock = MutableClock()
        val coordinator = RequirementMetadataCoordinator(
            provider = RequirementMetadataProvider { RequirementMetadata("title", "开发中").also { calls.incrementAndGet() } },
            scope = this,
            ioDispatcher = dispatcher,
            clock = clock,
        )

        val first = async { coordinator.fetch(LINK, "project") }
        val second = async { coordinator.fetch(LINK, "project") }
        advanceUntilIdle()
        assertIs<RequirementFetchResult.Success>(first.await())
        assertIs<RequirementFetchResult.Success>(second.await())
        assertEquals(1, calls.get())

        coordinator.fetch(LINK, "project")
        assertEquals(1, calls.get())
        clock.advance(Duration.ofMinutes(4).plusSeconds(59))
        coordinator.fetch(LINK, "project")
        assertEquals(1, calls.get())
        clock.advance(Duration.ofSeconds(2))
        val expired = async { coordinator.fetch(LINK, "project") }
        advanceUntilIdle()
        expired.await()
        assertEquals(2, calls.get())
        val forced = async { coordinator.fetch(LINK, "project", force = true) }
        advanceUntilIdle()
        forced.await()
        assertEquals(3, calls.get())
    }

    @Test
    fun `failed result backs off and stale task result is discarded`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val calls = AtomicInteger()
        val clock = MutableClock()
        val session = AppSessionStore(
            AppConfig(meegleProjects = listOf(MeegleProjectConfig("project", "obt"))),
            listOf(task("one", LINK)),
        )
        val coordinator = RequirementMetadataCoordinator(
            provider = RequirementMetadataProvider { calls.incrementAndGet(); null },
            scope = this,
            ioDispatcher = dispatcher,
            clock = clock,
        )
        val controller = RequirementController(session, this, coordinator)

        controller.refresh(session.tasks.single())
        session.tasks = emptyList()
        controller.reconcileTasks()
        advanceUntilIdle()
        assertTrue(controller.states.isEmpty())

        val first = async { coordinator.fetch(LINK, "project") }
        advanceUntilIdle()
        assertIs<RequirementFetchResult.Failure>(first.await())
        coordinator.fetch(LINK, "project")
        assertEquals(1, calls.get())
        clock.advance(Duration.ofSeconds(31))
        val retried = async { coordinator.fetch(LINK, "project") }
        advanceUntilIdle()
        retried.await()
        assertEquals(2, calls.get())
    }

    @Test
    fun `action metadata fetch reuses the coordinator and returns QC owners`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val calls = AtomicInteger()
        val task = task("one", LINK)
        val session = AppSessionStore(
            AppConfig(meegleProjects = listOf(MeegleProjectConfig("project", "obt"))),
            listOf(task),
        )
        val coordinator = RequirementMetadataCoordinator(
            provider = RequirementMetadataProvider {
                calls.incrementAndGet()
                RequirementMetadata(
                    status = "测试中",
                    participants = RequirementParticipants(qcOwners = listOf(RequirementPerson("测试同事"))),
                )
            },
            scope = this,
            ioDispatcher = dispatcher,
        )
        val controller = RequirementController(session, this, coordinator)
        var first: RequirementMetadata? = null
        var second: RequirementMetadata? = null

        controller.fetchMetadata(task) { first = it }
        controller.fetchMetadata(task) { second = it }
        advanceUntilIdle()

        assertEquals(listOf("测试同事"), first?.participants?.qcOwners?.map { it.name })
        assertEquals(first, second)
        assertEquals(1, calls.get())
    }

    @Test
    fun `materials preview stays hidden when configuration or inputs are incomplete`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        var calls = 0
        val session = AppSessionStore(AppConfig(), emptyList())
        val controller = RequirementController(
            session = session,
            scope = this,
            coordinator = RequirementMetadataCoordinator(
                provider = RequirementMetadataProvider { null },
                scope = this,
                ioDispatcher = dispatcher,
            ),
            ioDispatcher = dispatcher,
            materialPreviewer = suspend {
                calls++
                RequirementMaterialsResult.Failed("1", "should not be called")
            },
        )

        controller.requestMaterialsPreview("1", "任务")
        advanceUntilIdle()

        assertIs<RequirementMaterialsPreviewState.Hidden>(controller.materialsPreviewState)
        assertEquals(0, calls)

        session.config = session.config.copy(requirementMaterialsRoot = "C:/materials")
        controller.requestMaterialsPreview("1", "任务")
        advanceUntilIdle()

        assertIs<RequirementMaterialsPreviewState.Hidden>(controller.materialsPreviewState)
        assertEquals(0, calls)

        val configured = session.config.copy(
            requirementMaterialsRoot = "C:/materials",
            requirementMaterialsSubdirectory = "研发",
        )
        session.config = configured
        controller.requestMaterialsPreview("not-a-requirement", "任务")
        advanceUntilIdle()

        assertIs<RequirementMaterialsPreviewState.Hidden>(controller.materialsPreviewState)
        assertEquals(0, calls)
    }

    @Test
    fun `materials preview exposes a non blocking failure`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val session = AppSessionStore(
            AppConfig(
                requirementMaterialsRoot = "C:/materials",
                requirementMaterialsSubdirectory = "研发",
            ),
            emptyList(),
        )
        val controller = RequirementController(
            session = session,
            scope = this,
            coordinator = RequirementMetadataCoordinator(
                provider = RequirementMetadataProvider { null },
                scope = this,
                ioDispatcher = dispatcher,
            ),
            ioDispatcher = dispatcher,
            materialPreviewer = {
                RequirementMaterialsResult.Failed("1", "需求没有关联当前进行中的 Sprint")
            },
        )

        controller.requestMaterialsPreview("1", "任务")
        advanceUntilIdle()

        val failed = assertIs<RequirementMaterialsPreviewState.Failed>(controller.materialsPreviewState)
        assertEquals("1", failed.requirementId)
        assertEquals("需求没有关联当前进行中的 Sprint", failed.reason)
    }

    @Test
    fun `materials preview discards a late result for an older input`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val session = AppSessionStore(
            AppConfig(
                requirementMaterialsRoot = "C:/materials",
                requirementMaterialsSubdirectory = "研发",
            ),
            emptyList(),
        )
        val firstStarted = CompletableDeferred<Unit>()
        val releaseFirst = CompletableDeferred<Unit>()
        val controller = RequirementController(
            session = session,
            scope = this,
            coordinator = RequirementMetadataCoordinator(
                provider = RequirementMetadataProvider { null },
                scope = this,
                ioDispatcher = dispatcher,
            ),
            ioDispatcher = dispatcher,
            materialPreviewer = suspend { request: RequirementMaterialsRequest ->
                if (request.requirementInput == "1") {
                    firstStarted.complete(Unit)
                    withContext(NonCancellable) { releaseFirst.await() }
                }
                RequirementMaterialsResult.Ready(
                    requirementId = request.requirementInput,
                    requirementPath = java.nio.file.Path.of("C:/materials/${request.requirementInput}"),
                    writeRoot = java.nio.file.Path.of("C:/materials/${request.requirementInput}/研发"),
                    status = RequirementMaterialsResult.Ready.Status.CREATED,
                )
            },
        )

        controller.requestMaterialsPreview("1", "任务一")
        advanceUntilIdle()
        firstStarted.await()
        controller.requestMaterialsPreview("2", "任务二")
        advanceUntilIdle()

        assertIs<RequirementMaterialsPreviewState.Ready>(controller.materialsPreviewState)
        assertEquals("2", (controller.materialsPreviewState as RequirementMaterialsPreviewState.Ready).requirementId)
        releaseFirst.complete(Unit)
        advanceUntilIdle()
        assertEquals("2", (controller.materialsPreviewState as RequirementMaterialsPreviewState.Ready).requirementId)
    }

    @Test
    fun `metadata coordinator limits concurrent providers to four`() = runBlocking {
        val active = AtomicInteger()
        val maximum = AtomicInteger()
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val coordinator = RequirementMetadataCoordinator(
            provider = RequirementMetadataProvider {
                val current = active.incrementAndGet()
                maximum.accumulateAndGet(current, ::maxOf)
                Thread.sleep(40)
                active.decrementAndGet()
                RequirementMetadata(status = "开发中")
            },
            scope = scope,
            ioDispatcher = Dispatchers.Default,
        )
        try {
            (1..12).map { index ->
                async { coordinator.fetch("https://project.feishu.cn/obt/bug/detail/$index", "project") }
            }.awaitAll()
            assertTrue(maximum.get() <= 4, "maximum concurrency was ${maximum.get()}")
        } finally {
            scope.cancel()
        }
    }

    private fun task(name: String, link: String) = TaskManifest(
        folderName = name,
        taskDirectoryName = name,
        featureBranch = "feature/$name",
        requirementLink = link,
        createdAt = "2026-08-09 00:00:00",
        updatedAt = "2026-08-09 00:00:00",
        services = emptyList(),
    )

    private companion object {
        const val LINK = "https://project.feishu.cn/obt/userstory/detail/7060612727"
    }

    private class MutableClock(
        private var current: Instant = Instant.parse("2026-08-09T00:00:00Z"),
    ) : Clock() {
        override fun getZone(): ZoneId = ZoneId.of("UTC")
        override fun withZone(zone: ZoneId): Clock = this
        override fun instant(): Instant = current
        fun advance(duration: Duration) { current = current.plus(duration) }
    }
}
