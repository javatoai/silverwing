@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LocalSkillCatalogApplicationService
import com.snowball.silverwing.core.LocalSkillCatalogItem
import com.snowball.silverwing.core.LocalSkillCatalogService
import com.snowball.silverwing.core.LocalSkillFileEntry
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.*

class LocalSkillsControllerTest {
    @TempDir lateinit var root: Path
    private val a = LocalSkillCatalogItem("a", "a", "Alpha")
    private val b = LocalSkillCatalogItem("b", "b", "Beta")
    private val skillFile = LocalSkillFileEntry("SKILL.md", 10, markdown = true)

    @Test fun `refresh interrupts obsolete catalog read and newest catalog remains loaded`() {
        fixture().use { fixture ->
            val obsolete = fixture.blockNextRead()
            fixture.controller.refresh()
            fixture.awaitStarted(obsolete)
            fixture.controller.refresh()
            fixture.await { obsolete.interrupted.count == 0L && fixture.controller.catalogState is LocalSkillCatalogLoadState.Loaded }
            assertEquals(listOf("a", "b"), (fixture.controller.catalogState as LocalSkillCatalogLoadState.Loaded).catalog.skills.map { it.directoryName })
            assertEquals(LocalSkillFilesState.Empty, fixture.controller.filesState)
            assertEquals(LocalSkillFilePreviewState.Empty, fixture.controller.previewState)
        }
    }

    @Test fun `rapid skill and file switches cancel obsolete readers and never display previous selection`() {
        fixture().use { fixture ->
            val oldFiles = fixture.blockNextRead()
            fixture.controller.loadFiles(a)
            fixture.awaitStarted(oldFiles)
            fixture.controller.loadFiles(b)
            fixture.await { oldFiles.interrupted.count == 0L && fixture.controller.filesState is LocalSkillFilesState.Loaded }
            assertEquals("b", (fixture.controller.filesState as LocalSkillFilesState.Loaded).catalog.directoryName)

            val oldPreview = fixture.blockNextRead()
            fixture.controller.preview(a, skillFile)
            fixture.awaitStarted(oldPreview)
            fixture.controller.preview(b, skillFile)
            fixture.await { oldPreview.interrupted.count == 0L && fixture.controller.previewState is LocalSkillFilePreviewState.Loaded }
            val preview = fixture.controller.previewState as LocalSkillFilePreviewState.Loaded
            assertEquals("b", preview.directoryName)
            assertEquals("Beta first", preview.content)

            val anotherPreview = fixture.blockNextRead()
            fixture.controller.preview(b, skillFile)
            fixture.awaitStarted(anotherPreview)
            fixture.controller.loadFiles(a)
            fixture.await { anotherPreview.interrupted.count == 0L && fixture.controller.filesState is LocalSkillFilesState.Loaded }
            assertEquals(LocalSkillFilePreviewState.Empty, fixture.controller.previewState)
            assertEquals("a", (fixture.controller.filesState as LocalSkillFilesState.Loaded).catalog.directoryName)
        }
    }

    @Test fun `cancelled reads clear loading state and allow subsequent retry`() {
        fixture().use { fixture ->
            fixture.cancelNextRead.set(true)
            fixture.controller.refresh()
            fixture.await { fixture.controller.catalogState == LocalSkillCatalogLoadState.Idle }
            fixture.controller.refresh()
            fixture.await { fixture.controller.catalogState is LocalSkillCatalogLoadState.Loaded }
            fixture.cancelNextRead.set(true)
            fixture.controller.loadFiles(a)
            fixture.await { fixture.controller.filesState == LocalSkillFilesState.Empty }
            fixture.controller.loadFiles(a)
            fixture.await { fixture.controller.filesState is LocalSkillFilesState.Loaded }
            fixture.cancelNextRead.set(true)
            fixture.controller.preview(a, skillFile)
            fixture.await { fixture.controller.previewState == LocalSkillFilePreviewState.Empty }
            fixture.controller.preview(a, skillFile)
            fixture.await { fixture.controller.previewState is LocalSkillFilePreviewState.Loaded }
            assertEquals("Alpha first", (fixture.controller.previewState as LocalSkillFilePreviewState.Loaded).content)
        }
    }

    @Test fun `refresh cancels in flight file preview while external edits are visible on next read`() {
        fixture().use { fixture ->
            fixture.controller.preview(a, skillFile)
            fixture.await { fixture.controller.previewState is LocalSkillFilePreviewState.Loaded }
            Files.writeString(fixture.skillPath("a"), "Externally changed Alpha")
            val obsolete = fixture.blockNextRead()
            fixture.controller.preview(a, skillFile)
            fixture.awaitStarted(obsolete)
            fixture.controller.refresh()
            fixture.await { obsolete.interrupted.count == 0L && fixture.controller.catalogState is LocalSkillCatalogLoadState.Loaded }
            assertEquals(LocalSkillFilePreviewState.Empty, fixture.controller.previewState)
            fixture.controller.preview(a, skillFile)
            fixture.await { fixture.controller.previewState is LocalSkillFilePreviewState.Loaded }
            assertEquals("Externally changed Alpha", (fixture.controller.previewState as LocalSkillFilePreviewState.Loaded).content)
            fixture.controller.preview(a, skillFile.copy(relativePath = "missing.md"))
            fixture.await { fixture.controller.previewState is LocalSkillFilePreviewState.Failed }
            fixture.controller.preview(a, skillFile)
            fixture.await { fixture.controller.previewState is LocalSkillFilePreviewState.Loaded }
            assertEquals("Externally changed Alpha", Files.readString(fixture.skillPath("a")))
        }
    }

    @Test fun `refresh never cancels explicit uninstall and repeated uninstall is ignored while removing`() {
        val removing = ReadGate()
        val calls = AtomicInteger()
        fixture { calls.incrementAndGet(); removing.block() }.use { fixture ->
            try {
                fixture.controller.uninstall(a)
                fixture.awaitStarted(removing)
                fixture.controller.uninstall(a)
                fixture.controller.refresh()
                fixture.await { fixture.controller.catalogState is LocalSkillCatalogLoadState.Loaded }
                assertEquals(1, calls.get())
                assertEquals(1L, removing.interrupted.count)
                assertIs<LocalSkillUninstallState.Removing>(fixture.controller.uninstallState)
                removing.release.countDown()
                fixture.await { fixture.controller.uninstallState is LocalSkillUninstallState.Succeeded && fixture.controller.catalogState is LocalSkillCatalogLoadState.Loaded }
                assertEquals(1, calls.get())
                // 测试替身没有删除任何 Skill，真实读取目录始终为 TempDir。
                assertTrue(Files.isRegularFile(fixture.skillPath("a")))
            } finally { removing.release.countDown() }
        }
    }

    private fun fixture(uninstall: (String) -> Unit = {}): Fixture {
        for ((name, content) in listOf("a" to "Alpha first", "b" to "Beta first")) {
            val file = root.resolve(".agents/skills/$name/SKILL.md")
            Files.createDirectories(file.parent); Files.writeString(file, content)
        }
        val main = StandardTestDispatcher()
        val scope = CoroutineScope(SupervisorJob() + main)
        val pendingGate = AtomicReference<ReadGate?>()
        val cancelNext = AtomicBoolean(false)
        val skills = LocalSkillCatalogApplicationService(LocalSkillCatalogService {
            pendingGate.getAndSet(null)?.block()
            if (cancelNext.getAndSet(false)) throw CancellationException("cancelled test read")
            root
        })
        return Fixture(root, LocalSkillsController(skills, uninstall, scope, Dispatchers.IO), scope, main.scheduler,
            pendingGate, cancelNext)
    }

    private class ReadGate {
        val started = CountDownLatch(1)
        val release = CountDownLatch(1)
        val interrupted = CountDownLatch(1)
        fun block() {
            started.countDown()
            try { check(release.await(10, TimeUnit.SECONDS)) }
            catch (failure: InterruptedException) { interrupted.countDown(); throw failure }
        }
    }
    private class Fixture(val root: Path, val controller: LocalSkillsController, val scope: CoroutineScope,
        val scheduler: kotlinx.coroutines.test.TestCoroutineScheduler, val pendingGate: AtomicReference<ReadGate?>,
        val cancelNextRead: AtomicBoolean) : AutoCloseable {
        private val gates = mutableListOf<ReadGate>()
        fun skillPath(name: String): Path = root.resolve(".agents/skills/$name/SKILL.md")
        fun blockNextRead(): ReadGate = ReadGate().also { gates += it; pendingGate.set(it) }
        fun awaitStarted(gate: ReadGate) { scheduler.runCurrent(); assertTrue(gate.started.await(5, TimeUnit.SECONDS), "reader did not start") }
        fun await(condition: () -> Boolean) {
            val deadline = System.nanoTime() + 5_000_000_000L
            do { scheduler.runCurrent(); if (condition()) return; Thread.sleep(10) } while (System.nanoTime() < deadline)
            assertTrue(condition(), "Local Skills state did not settle")
        }
        override fun close() { scope.cancel(); gates.forEach { it.release.countDown() }; scheduler.runCurrent() }
    }
}
