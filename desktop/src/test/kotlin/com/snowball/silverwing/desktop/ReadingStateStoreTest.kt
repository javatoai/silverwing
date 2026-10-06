package com.snowball.silverwing.desktop

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.*

class ReadingStateStoreTest {
    @TempDir lateinit var root: Path

    @Test fun `reserved newest snapshot wins when delayed writes arrive out of order`() {
        val store = ReadingStateStore(root.resolve("cache/reading.json"))
        val older = store.reserveSaveRevision()
        val newer = store.reserveSaveRevision()
        assertTrue(store.save(ReadingSnapshot(view = "MATERIALS"), newer))
        assertFalse(store.save(ReadingSnapshot(view = "NOTES"), older))
        assertEquals("MATERIALS", store.load().view)
        assertTrue(Files.list(root.resolve("cache")).use { files -> files.noneMatch { it.fileName.toString().endsWith(".tmp") } })
    }

    @Test fun `expired truncated oversized and unsupported cache falls back without changing config`() {
        val path = root.resolve("reading.json")
        val now = 2_000_000_000_000L
        val store = ReadingStateStore(path) { now }
        val config = root.resolve("config.json")
        Files.writeString(config, "user configuration")
        for (content in listOf("{\"view\":", "{\"schema\":9}", "x".repeat(MAX_READING_CACHE_BYTES + 1))) {
            Files.writeString(path, content)
            assertEquals(ReadingSnapshot(), store.load())
        }
        Files.writeString(path, "{\"view\":\"MATERIALS\"}")
        Files.setLastModifiedTime(path, FileTime.fromMillis(now - READING_CACHE_MAX_AGE_MILLIS - 1))
        assertEquals(ReadingSnapshot(), store.load())
        assertTrue(store.save(ReadingSnapshot(view = "NOTES")))
        assertEquals("NOTES", store.load().view)
        assertEquals("user configuration", Files.readString(config))
    }

    @Test fun `oversized valid snapshots are compacted while preserving current task reading`() {
        val path = root.resolve("reading.json")
        val store = ReadingStateStore(path)
        val position = ReadingPositionSnapshot(scrolls = (0 until MAX_READING_POSITIONS).associate { index ->
            "position-$index-${"x".repeat(45)}" to 100 }, lists = (0 until MAX_READING_POSITIONS).associate { index ->
            "position-$index-${"y".repeat(45)}" to listOf(3, 100) })
        val files = (0 until MAX_READING_FILES_PER_TASK).associate { index -> "${"文".repeat(800)}-$index.pdf" to position }
        val selected = files.keys.last()
        val tasks = (0 until 10).map { index -> MaterialsSnapshot(root.resolve("task-$index").toString(), root.resolve("materials-$index").toString(),
            selectedPath = selected, files = files) }
        val currentTask = tasks.last().taskPath
        assertTrue(store.save(ReadingSnapshot(lastTaskPath = currentTask, view = "MATERIALS", materials = tasks)))
        assertTrue(Files.size(path) <= MAX_READING_CACHE_BYTES)
        val loaded = store.load()
        assertEquals(currentTask, loaded.lastTaskPath)
        assertEquals(selected, loaded.materials.last().selectedPath)
        assertEquals(3, loaded.materials.last().files.getValue(selected).lists.values.first()[0])
    }

    @Test fun `new generation reserved during old write prevents old snapshot replacing disk`() {
        val path = root.resolve("reading.json")
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val blockOnce = AtomicBoolean(false)
        val store = ReadingStateStore(path) {
            if (blockOnce.getAndSet(false)) { entered.countDown(); check(release.await(5, TimeUnit.SECONDS)) }
            System.currentTimeMillis()
        }
        assertTrue(store.save(ReadingSnapshot(view = "DETAIL")))
        val original = Files.readString(path)
        val executor = Executors.newSingleThreadExecutor { Thread(it, "reading-store-test").apply { isDaemon = true } }
        try {
            val old = store.reserveSaveRevision()
            blockOnce.set(true)
            val writing = executor.submit<Boolean> { store.save(ReadingSnapshot(view = "NOTES"), old) }
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            val latest = store.reserveSaveRevision()
            release.countDown()
            assertFalse(writing.get(5, TimeUnit.SECONDS))
            assertEquals(original, Files.readString(path))
            assertTrue(store.save(ReadingSnapshot(view = "MATERIALS"), latest))
            assertEquals("MATERIALS", store.load().view)
            assertTrue(Files.list(root).use { files -> files.noneMatch { it.fileName.toString().endsWith(".tmp") } })
        } finally { release.countDown(); executor.shutdownNow() }
    }

    @Test fun `failed file replacement preserves existing data cleans temporary file and permits later generation`() {
        val path = Files.createDirectory(root.resolve("reading.json"))
        val existing = path.resolve("existing.txt")
        Files.writeString(existing, "existing data")
        val store = ReadingStateStore(path)
        val failed = store.reserveSaveRevision()
        assertFails { store.save(ReadingSnapshot(view = "NOTES"), failed) }
        assertEquals("existing data", Files.readString(existing))
        assertTrue(Files.list(root).use { files -> files.noneMatch { it.fileName.toString().endsWith(".tmp") } })
        Files.delete(existing); Files.delete(path)
        assertTrue(store.save(ReadingSnapshot(view = "MATERIALS"), store.reserveSaveRevision()))
        assertFalse(store.save(ReadingSnapshot(view = "NOTES"), failed))
        assertEquals("MATERIALS", store.load().view)
    }

    @Test fun `task count bounding keeps active material and requirement even when initially oldest`() {
        val active = root.resolve("active").toString()
        val materials = listOf(MaterialsSnapshot(active, root.resolve("active-materials").toString(), selectedPath = "selected.pdf")) +
            (0 until MAX_READING_TASKS + 1).map { MaterialsSnapshot(root.resolve("task-$it").toString(), root.resolve("materials-$it").toString()) }
        val requirements = listOf(RequirementReadingSnapshot(active, "https://example.test/active", ReadingPositionSnapshot(scrolls = mapOf("rendered" to 900)))) +
            (0 until MAX_READING_TASKS + 1).map { RequirementReadingSnapshot(root.resolve("task-$it").toString(), "https://example.test/$it", ReadingPositionSnapshot()) }
        val bounded = ReadingSnapshot(lastTaskPath = active, materials = materials, requirements = requirements).bounded()
        assertEquals(MAX_READING_TASKS, bounded.materials.size)
        assertEquals(MAX_READING_TASKS, bounded.requirements.size)
        assertEquals(normalizedReadingPath(active), bounded.materials.last().taskPath)
        assertEquals("selected.pdf", bounded.materials.last().selectedPath)
        assertEquals(normalizedReadingPath(active), bounded.requirements.last().taskPath)
        assertEquals(900, bounded.requirements.last().reading.scrolls["rendered"])
    }

    @Test fun `large directory expansions are compacted without dropping active file or requirement`() {
        val active = root.resolve("active").toString()
        val prefix = List(30) { "文".repeat(100) }.joinToString("/")
        val directory = MaterialsSnapshot(active, root.resolve("materials").toString(), selectedPath = "selected.pdf",
            expandedDirectories = (0 until 500).map { "$prefix/dir-$it" }, expansionInitialized = true,
            files = mapOf("selected.pdf" to ReadingPositionSnapshot(zoom = 175, lists = mapOf("pdf-pages" to listOf(8, 90)))))
        val requirements = listOf(RequirementReadingSnapshot(active, "https://example.test/active", ReadingPositionSnapshot(scrolls = mapOf("source-vertical" to 750)))) +
            (0 until MAX_READING_TASKS - 1).map { RequirementReadingSnapshot(root.resolve("task-$it").toString(),
                "https://example.test/$it/${"x".repeat(8000)}", ReadingPositionSnapshot()) }
        val path = root.resolve("reading.json")
        val store = ReadingStateStore(path)
        assertTrue(store.save(ReadingSnapshot(lastTaskPath = active, view = "REQUIREMENT", materials = listOf(directory), requirements = requirements)))
        assertTrue(Files.size(path) <= MAX_READING_CACHE_BYTES)
        val loaded = store.load()
        assertEquals("selected.pdf", loaded.materials.single().selectedPath)
        assertEquals(listOf(8, 90), loaded.materials.single().files.getValue("selected.pdf").lists["pdf-pages"])
        assertEquals("https://example.test/active", loaded.requirements.last().link)
        assertEquals(750, loaded.requirements.last().reading.scrolls["source-vertical"])
        assertTrue(loaded.materials.single().expandedDirectories.size < 500)
    }
}
