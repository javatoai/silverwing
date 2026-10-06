package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LocalSkillFileEntry
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import kotlin.test.*

class MaterialsDirectorySortingTest {
    @TempDir lateinit var root: Path

    @Test fun `catalog records modification times and sorting preserves folders and deterministic ties`() {
        Files.createDirectories(root.resolve("alpha"))
        Files.createDirectories(root.resolve("zeta"))
        val times = linkedMapOf("a.md" to 1L, "b.md" to 3L, "c.md" to 3L, "alpha/a.txt" to 1L, "alpha/z.txt" to 2L)
        times.forEach { (path, time) ->
            Files.writeString(root.resolve(path), path)
            Files.setLastModifiedTime(root.resolve(path), stamp(time))
        }
        Files.setLastModifiedTime(root.resolve("alpha"), stamp(1))
        Files.setLastModifiedTime(root.resolve("zeta"), stamp(2))
        val catalog = RequirementMaterialsMarkdownService().list(root)
        assertEquals(times.mapValues { stamp(it.value).toMillis() }, catalog.files.associate { it.relativePath to it.modifiedAtMillis })
        assertEquals(stamp(2).toMillis(), catalog.directoryModifiedAtMillis["zeta"])
        val tree = buildLocalSkillFileTree("任务资料", catalog.files.map { LocalSkillFileEntry(it.relativePath, it.sizeBytes, it.markdown) }, catalog.directories)
        fun ordered(order: MaterialsSortOrder) = visibleLocalSkillFileTreeRows(sortMaterialsFileTree(tree, order, catalog), setOf("alpha", "zeta"))
            .drop(1).map { it.key }
        assertEquals(listOf("directory:alpha", "file:alpha/a.txt", "file:alpha/z.txt", "directory:zeta", "file:a.md", "file:b.md", "file:c.md"), ordered(MaterialsSortOrder.NAME))
        assertEquals(listOf("directory:zeta", "directory:alpha", "file:alpha/z.txt", "file:alpha/a.txt", "file:b.md", "file:c.md", "file:a.md"), ordered(MaterialsSortOrder.MODIFIED_DESC))
    }

    @Test fun `sort preference round trips per task while old and unknown values use name order`() {
        val task = root.resolve("task").toString()
        val materials = root.resolve("materials").toString()
        val session = TaskBrowsingSession()
        session.materialsFor(task, materials).apply {
            sortOrder.value = MaterialsSortOrder.MODIFIED_DESC
            selectedPath.value = "chosen.md"
        }
        session.materialsFor(root.resolve("other-task").toString(), materials)
        val store = ReadingStateStore(root.resolve("reading.json"))
        assertTrue(store.save(session.snapshot(task)))
        val restored = TaskBrowsingSession().apply { restore(store.load()) }
        assertEquals(MaterialsSortOrder.MODIFIED_DESC, restored.materialsFor(task, materials).sortOrder.value)
        assertEquals("chosen.md", restored.materialsFor(task, materials).selectedPath.value)
        assertEquals(MaterialsSortOrder.NAME, restored.materialsFor(root.resolve("other-task").toString(), materials).sortOrder.value)
        for (saved in listOf(MaterialsSnapshot(task, materials), MaterialsSnapshot(task, materials, sortOrder = "future"))) {
            restored.restore(ReadingSnapshot(materials = listOf(saved)))
            assertEquals(MaterialsSortOrder.NAME, restored.materialsFor(task, materials).sortOrder.value)
        }
    }

    private fun stamp(order: Long) = FileTime.fromMillis(1_700_000_000_000L + order * 1_000)
}
