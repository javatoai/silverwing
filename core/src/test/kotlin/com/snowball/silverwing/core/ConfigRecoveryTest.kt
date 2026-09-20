package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ConfigRecoveryTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `import validates every zip entry before replacing current shards`() {
        val paths = ApplicationPaths(temporary.resolve("target"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/before"))
        val before = shardBytes(paths)
        val invalid = temporary.resolve("missing.zip")
        writeTextZip(invalid, mapOf("layout.json" to "{\"schema\":1,\"groups\":[]}"))

        assertFailsWith<IllegalArgumentException> { store.importFrom(invalid) }

        before.forEach { (name, bytes) ->
            assertContentEquals(bytes, Files.readAllBytes(paths.config.resolve(name)), name)
        }
        assertTrue(store.backups().isEmpty())
    }

    @Test
    fun `zip slip and unknown entries are rejected without replacement`() {
        val paths = ApplicationPaths(temporary.resolve("target"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/before"))
        val valid = exported(AppConfig(taskRoot = "D:/after"))
        val entries = readZip(valid).toMutableMap()
        entries["../layout.json"] = entries.remove("layout.json")!!
        val slip = temporary.resolve("slip.zip")
        writeZip(slip, entries)

        assertFailsWith<IllegalArgumentException> { store.importFrom(slip) }
        assertEquals("D:/before", store.load().taskRoot)

        val unknown = temporary.resolve("unknown.zip")
        writeZip(unknown, readZip(valid) + ("extra.json" to "{}".toByteArray()))
        assertFailsWith<IllegalArgumentException> { store.previewImport(unknown) }
        assertEquals("D:/before", store.load().taskRoot)
    }

    @Test
    fun `explicit import recovers a malformed complete configuration by archiving all raw shards`() {
        val paths = ApplicationPaths(temporary.resolve("target"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/before"))
        val corrupted = "{ malformed tag shard\r\n".toByteArray()
        Files.write(paths.config.resolve("tag.json"), corrupted)
        val import = exported(AppConfig(taskRoot = "D:/after"))

        assertTrue(store.previewImport(import).changes.any { it.contains("已损坏") })
        assertEquals("D:/after", store.importFrom(import).taskRoot)
        val backup = store.backups().single()

        assertEquals(EXPECTED_SHARDS, zipNames(backup.path))
        assertContentEquals(corrupted, readZip(backup.path).getValue("tag.json"))
        assertEquals("D:/after", store.load().taskRoot)
    }

    @Test
    fun `restore accepts only owned zip backups and archives current configuration first`() {
        val paths = ApplicationPaths(temporary.resolve("restore"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/first"))
        store.save(AppConfig(taskRoot = "D:/second"))
        val backup = store.backups().single().path

        assertEquals("D:/first", store.restore(backup).taskRoot)
        assertEquals("D:/first", store.load().taskRoot)
        assertEquals(2, store.backups().size)
        assertFailsWith<IllegalArgumentException> { store.restore(temporary.resolve("outside.zip")) }
    }

    @Test
    fun `backups retain the newest ten complete zip archives`() {
        val store = ConfigStore(ApplicationPaths(temporary.resolve("retention")))
        repeat(12) { index -> store.save(AppConfig(taskRoot = "D:/tasks-$index")) }

        assertEquals(10, store.backups().size)
        store.backups().forEach { backup -> assertEquals(EXPECTED_SHARDS, zipNames(backup.path)) }
    }

    private fun exported(config: AppConfig): Path {
        val source = ConfigStore(ApplicationPaths(temporary.resolve("source-${System.nanoTime()}")))
        source.save(config)
        return source.exportTo(temporary.resolve("source-${System.nanoTime()}.zip"))
    }

    private fun shardBytes(paths: ApplicationPaths): Map<String, ByteArray> =
        EXPECTED_SHARDS.associateWith { Files.readAllBytes(paths.config.resolve(it)) }

    private fun writeZip(path: Path, entries: Map<String, ByteArray>) {
        Files.newOutputStream(path).use { output ->
            ZipOutputStream(output).use { zip ->
                entries.forEach { (name, bytes) ->
                    zip.putNextEntry(ZipEntry(name))
                    zip.write(bytes)
                    zip.closeEntry()
                }
            }
        }
    }

    private fun writeTextZip(path: Path, entries: Map<String, String>) =
        writeZip(path, entries.mapValues { it.value.toByteArray() })

    private fun readZip(path: Path): Map<String, ByteArray> = ZipInputStream(Files.newInputStream(path)).use { zip ->
        buildMap {
            while (true) {
                val entry = zip.nextEntry ?: break
                put(entry.name, zip.readAllBytes())
                zip.closeEntry()
            }
        }
    }

    private fun zipNames(path: Path): List<String> = readZip(path).keys.sorted()

    private companion object {
        val EXPECTED_SHARDS = listOf(
            "appearance.json",
            "git.json",
            "integrations.json",
            "layout.json",
            "services.json",
            "tag.json",
            "tools.json",
            "workspace.json",
        )
    }
}
