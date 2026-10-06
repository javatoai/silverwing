package com.snowball.silverwing.desktop

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.ClosedWatchServiceException
import java.nio.file.Path
import java.nio.file.WatchService
import java.nio.file.attribute.FileTime
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class MaterialsLiveRefreshTest {
    @TempDir
    lateinit var temporaryDirectory: Path

    @Test
    fun `watch event refreshes selected content even when size and timestamp are preserved`() = runBlocking {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val file = Files.writeString(root.resolve("selected.md"), "before")
        val modified = Files.getLastModifiedTime(file)
        val monitor = startMonitor(root, "selected.md")
        try {
            Files.writeString(file, "after!")
            Files.setLastModifiedTime(file, modified)
            val change = withTimeout(5_000) { monitor.changes.receive() }
            assertTrue(change.selectedFileChanged)
            assertFalse(change.catalogChanged)
        } finally { monitor.close() }
    }

    @Test
    fun `new nested directories and selected file deletion refresh the catalog`() = runBlocking {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val file = Files.writeString(root.resolve("selected.md"), "selected")
        val monitor = startMonitor(root, "selected.md")
        try {
            Files.createDirectories(root.resolve("new/deep"))
            Files.writeString(root.resolve("new/deep/other.md"), "new")
            assertTrue(withTimeout(5_000) { monitor.changes.receive() }.catalogChanged)
            Files.delete(file)
            val deleted = withTimeout(5_000) {
                var result = monitor.changes.receive()
                while (!result.selectedFileChanged) result = monitor.changes.receive()
                result
            }
            assertTrue(deleted.catalogChanged)
            assertTrue(deleted.selectedFileChanged)
        } finally { monitor.close() }
    }

    @Test
    fun `activation check recovers missed modifications without a filesystem watcher`() = runBlocking {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val file = Files.writeString(root.resolve("selected.md"), "before")
        val monitor = startMonitor(root, "selected.md", focusOnlyService())
        try {
            Files.writeString(file, "updated text")
            monitor.activationChecks.emit(Unit)
            val change = withTimeout(5_000) { monitor.changes.receive() }
            assertTrue(change.catalogChanged)
            assertTrue(change.selectedFileChanged)
        } finally { monitor.close() }
    }

    @Test
    fun `unselected same size edits refresh catalog times without reporting selected content changed`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val selected = Files.writeString(root.resolve("selected.md"), "keep")
        val other = Files.writeString(root.resolve("other.md"), "old")
        val before = captureMaterialsLiveRefreshSnapshot(root)
        Files.writeString(other, "new")
        Files.setLastModifiedTime(other, FileTime.fromMillis(Files.getLastModifiedTime(other).toMillis() + 2_000))

        val change = materialsLiveRefreshChange(before, captureMaterialsLiveRefreshSnapshot(root), "selected.md")

        assertTrue(change.catalogChanged)
        assertFalse(change.selectedFileChanged)
        assertEquals("keep", Files.readString(selected))
    }

    @Test
    fun `activation detects missing root and its later recreation`() = runBlocking {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val file = Files.writeString(root.resolve("selected.md"), "selected")
        val monitor = startMonitor(root, "selected.md", focusOnlyService())
        try {
            Files.delete(file)
            Files.delete(root)
            monitor.activationChecks.emit(Unit)
            val removed = withTimeout(5_000) { monitor.changes.receive() }
            assertTrue(removed.catalogChanged)
            assertTrue(removed.selectedFileChanged)

            Files.createDirectory(root)
            Files.createDirectory(root.resolve("empty-folder"))
            monitor.activationChecks.emit(Unit)
            val recreated = withTimeout(5_000) { monitor.changes.receive() }
            assertTrue(recreated.catalogChanged)
        } finally { monitor.close() }
    }

    @Test
    fun `cancelling an idle monitor closes its blocked watcher promptly`() = runBlocking {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        Files.writeString(root.resolve("selected.md"), "selected")
        lateinit var watcher: WatchService
        val service = MaterialsLiveRefreshService(Dispatchers.IO, debounceMillis = 40) { path ->
            path.fileSystem.newWatchService().also { watcher = it }
        }
        val monitor = startMonitor(root, "selected.md", service)

        withTimeout(2_000) { monitor.close() }

        assertFailsWith<ClosedWatchServiceException> { watcher.poll() }
        Files.writeString(root.resolve("selected.md"), "after close")
        assertNull(withTimeoutOrNull(150) { monitor.changes.receive() })
    }

    @Test
    @OptIn(ExperimentalCoroutinesApi::class)
    fun `injected test dispatcher can drain while watcher waits on real IO`() = runTest {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        Files.writeString(root.resolve("selected.md"), "selected")
        val selections = MutableStateFlow<String?>("selected.md")
        val activationChecks = MutableSharedFlow<Unit>()
        val service = MaterialsLiveRefreshService(StandardTestDispatcher(testScheduler))
        val observer = backgroundScope.launch { service.changes(root, selections, activationChecks).collect { } }

        runCurrent()

        assertEquals(1, selections.subscriptionCount.value)
        assertFalse(observer.isCompleted)
        observer.cancel()
        runCurrent()
    }

    @Test
    fun `snapshots exclude git metadata and unsafe selected paths`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        Files.writeString(root.resolve(" selected.md"), "spaces are significant")
        Files.createDirectories(root.resolve("nested/.GIT"))
        Files.writeString(root.resolve("nested/.GIT/config"), "metadata")
        Files.writeString(root.resolve(".git"), "worktree metadata")
        val before = captureMaterialsLiveRefreshSnapshot(root)
        assertEquals(setOf(" selected.md"), before.files.keys)
        assertFalse(before.directories.any { it.fileName.toString().equals(".git", ignoreCase = true) })
        Files.writeString(root.resolve(" selected.md"), "external update")
        val after = captureMaterialsLiveRefreshSnapshot(root)
        val unsafePaths = listOf("../ selected.md", root.resolve(" selected.md").toString(),
            ".git/config", "nested/.GIT/config", "nested/../ selected.md", "C:/outside.md")
        unsafePaths.forEach { selected ->
            assertFalse(materialsLiveRefreshChange(before, after, selected, selectedTouched = true).selectedFileChanged)
        }
        assertTrue(materialsLiveRefreshChange(before, after, " selected.md").selectedFileChanged)
    }

    @Test
    fun `symbolic links never enter snapshots including roots with linked ancestors`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val external = Files.createDirectory(temporaryDirectory.resolve("external"))
        val externalFile = Files.writeString(external.resolve("outside.md"), "outside")
        val created = runCatching {
            Files.createSymbolicLink(root.resolve("linked"), external)
            Files.createSymbolicLink(root.resolve("linked.md"), externalFile)
            Files.createSymbolicLink(temporaryDirectory.resolve("linked-root"), root)
        }.isSuccess
        assumeTrue(created, "Symbolic links are unavailable in this environment")
        Files.createDirectory(external.resolve("nested"))
        val snapshot = captureMaterialsLiveRefreshSnapshot(root)

        assertTrue(snapshot.files.isEmpty())
        assertEquals(setOf(root), snapshot.directories)
        assertEquals(MaterialsLiveRefreshRootState.UNSAFE,
            captureMaterialsLiveRefreshSnapshot(temporaryDirectory.resolve("linked-root")).rootState)
        assertEquals(MaterialsLiveRefreshRootState.UNSAFE,
            captureMaterialsLiveRefreshSnapshot(root.resolve("linked/nested")).rootState)
    }

    @Test
    fun `burst of saves produces one final refresh after the quiet period`() = runBlocking {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val file = Files.writeString(root.resolve("selected.md"), "initial")
        val monitor = startMonitor(root, "selected.md", focusOnlyService())
        try {
            repeat(20) { attempt ->
                Files.writeString(file, "content $attempt")
                monitor.activationChecks.emit(Unit)
            }
            val refreshed = withTimeout(5_000) { monitor.changes.receive() }
            assertTrue(refreshed.selectedFileChanged)
            assertEquals("content 19", Files.readString(file))
            assertNull(withTimeoutOrNull(150) { monitor.changes.receive() })
        } finally { monitor.close() }
    }

    private fun focusOnlyService() = MaterialsLiveRefreshService(Dispatchers.IO, debounceMillis = 40) { null }

    private suspend fun CoroutineScope.startMonitor(
        root: Path,
        selected: String?,
        service: MaterialsLiveRefreshService = MaterialsLiveRefreshService(Dispatchers.IO, debounceMillis = 40),
    ): Monitor {
        val selections = MutableStateFlow(selected)
        val activationChecks = MutableSharedFlow<Unit>(extraBufferCapacity = 1)
        val changes = Channel<MaterialsLiveRefreshChange>(Channel.UNLIMITED)
        val job = launch { service.changes(root, selections, activationChecks).collect { changes.send(it) } }
        // Subscription begins only after the baseline scan and watcher registration complete.
        withTimeout(5_000) {
            selections.subscriptionCount.first { it > 0 }
            activationChecks.subscriptionCount.first { it > 0 }
        }
        return Monitor(job, activationChecks, changes)
    }

    private data class Monitor(
        val job: Job,
        val activationChecks: MutableSharedFlow<Unit>,
        val changes: Channel<MaterialsLiveRefreshChange>,
    ) {
        suspend fun close() { job.cancelAndJoin() }
    }

}
