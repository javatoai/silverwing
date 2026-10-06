@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class SettingsProjectCatalogLifecycleTest {
    @TempDir lateinit var temporary: Path

    @Test fun `force refresh and explicit cancellation interrupt obsolete project readers and permit retry`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        val calls = AtomicInteger()
        val started = listOf(CountDownLatch(1), CountDownLatch(1))
        val interrupted = listOf(CountDownLatch(1), CountDownLatch(1))
        val release = CountDownLatch(1)
        val latest = listOf(MeegleProjectSummary("当前项目", "new-key", "NEW"))
        val catalog = MeegleProjectCatalog {
            val call = calls.incrementAndGet()
            if (call == 1 || call == 3) {
                val index = if (call == 1) 0 else 1
                started[index].countDown()
                try { check(release.await(5, TimeUnit.SECONDS)) { "Fixture reader timed out" } }
                catch (cancelled: InterruptedException) { interrupted[index].countDown(); throw cancelled }
            }
            latest
        }
        fun await(condition: () -> Boolean) {
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
            do {
                ui.scheduler.runCurrent()
                if (condition()) return
                Thread.sleep(10)
            } while (System.nanoTime() < deadline)
            assertTrue(condition(), "Project catalog state did not settle")
        }
        try {
            application(Dispatchers.IO, catalog).use { app ->
                app.loadMeegleProjects(); await { started[0].count == 0L }
                assertIs<MeegleProjectCatalogState.Loading>(app.meegleProjectCatalogState)
                app.loadMeegleProjects(force = true)
                await { interrupted[0].count == 0L && app.meegleProjectCatalogState is MeegleProjectCatalogState.Loaded }
                assertEquals(latest, assertIs<MeegleProjectCatalogState.Loaded>(app.meegleProjectCatalogState).projects)
                app.loadMeegleProjects(force = true); await { started[1].count == 0L }
                app.cancelMeegleProjectLoad()
                await { interrupted[1].count == 0L }
                assertEquals(MeegleProjectCatalogState.Idle, app.meegleProjectCatalogState)
                app.loadMeegleProjects(); await { app.meegleProjectCatalogState is MeegleProjectCatalogState.Loaded }
                assertEquals(latest, assertIs<MeegleProjectCatalogState.Loaded>(app.meegleProjectCatalogState).projects)
                assertEquals(4, calls.get())
                assertNull(app.errorMessage)
            }
        } finally { release.countDown(); Dispatchers.resetMain() }
    }

    @Test fun `source cancellation and ordinary failure leave retryable project states`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        var calls = 0
        val catalog = MeegleProjectCatalog {
            when (++calls) {
                1 -> throw CancellationException("cancelled")
                2 -> error("项目读取失败")
                else -> emptyList()
            }
        }
        try {
            application(ui, catalog).use { app ->
                app.loadMeegleProjects(); runCurrent()
                assertEquals(MeegleProjectCatalogState.Idle, app.meegleProjectCatalogState)
                app.loadMeegleProjects(); runCurrent()
                assertEquals("项目读取失败", assertIs<MeegleProjectCatalogState.Failed>(app.meegleProjectCatalogState).message)
                app.loadMeegleProjects(); runCurrent()
                assertEquals(emptyList(), assertIs<MeegleProjectCatalogState.Loaded>(app.meegleProjectCatalogState).projects)
                assertNull(app.errorMessage)
            }
        } finally { Dispatchers.resetMain() }
    }

    @Test fun `cancelled scope before project coroutine starts cannot retain loading state`() = runTest {
        val ui = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(ui)
        var calls = 0
        try {
            val app = application(ui, MeegleProjectCatalog { calls++; emptyList() })
            app.loadMeegleProjects()
            app.close(); runCurrent()
            assertEquals(0, calls)
            assertEquals(MeegleProjectCatalogState.Idle, app.meegleProjectCatalogState)
            app.loadMeegleProjects()
            assertEquals(MeegleProjectCatalogState.Idle, app.meegleProjectCatalogState)
        } finally { Dispatchers.resetMain() }
    }

    private fun application(io: CoroutineDispatcher, catalog: MeegleProjectCatalog): DesktopApplication {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths).also { it.save(AppConfig(aiRequirementNamingEnabled = false)) }
        return DesktopApplication(paths = paths, configStore = store, ioDispatcher = io, meegleProjectCatalog = catalog,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
    }
}
