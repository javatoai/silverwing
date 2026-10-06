package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class MaterialsDocumentTabsStateTest {
    @Test fun `single click replaces only the preview and revisiting a pinned document keeps it open`() {
        val state = MaterialsDocumentTabsState()
        state.openPreview("研发\\说明.md")
        state.openPreview("设计/流程.pdf")
        assertEquals(listOf(MaterialsDocumentTab("设计/流程.pdf")), state.tabs)
        state.pin()
        state.openPreview("研发/说明.md")
        state.pin()
        state.openPreview("临时.txt")
        state.openPreview("设计/流程.pdf")
        assertEquals("设计/流程.pdf", state.activePath)
        assertEquals(listOf("设计/流程.pdf", "研发/说明.md"), state.pinnedPaths)
        assertEquals("临时.txt", state.temporaryPath)
        state.openPreview("新预览.txt")
        assertEquals(listOf("设计/流程.pdf", "研发/说明.md", "新预览.txt"), state.tabs.map { it.path })
        assertEquals("新预览.txt", state.activePath)
    }

    @Test fun `legacy initialization waits for a real selection and never resurrects a deliberately closed last tab`() {
        val state = MaterialsDocumentTabsState()
        state.initializeFromSelected(null)
        assertFalse(state.isInitialized)
        state.initializeFromSelected("旧选中.md")
        assertTrue(state.isInitialized)
        assertEquals("旧选中.md", state.activePath)
        state.initializeFromSelected("其他.md")
        assertEquals("旧选中.md", state.activePath)
        assertNull(state.close("旧选中.md"))
        state.initializeFromSelected("旧选中.md")
        assertTrue(state.tabs.isEmpty())
        val restored = MaterialsDocumentTabsState().apply { restore(state.snapshot()) }
        restored.initializeFromSelected("旧选中.md")
        assertTrue(restored.tabs.isEmpty())
        restored.reset()
        restored.initializeFromSelected("其他.md")
        assertEquals("其他.md", restored.activePath)
    }

    @Test fun `closing active tabs chooses right then left while closing an inactive tab preserves the active document`() {
        val state = pinned("a.md", "b.md", "c.md", "d.md")
        assertTrue(state.select("b.md"))
        assertEquals("b.md", state.close("d.md"))
        assertEquals("c.md", state.close("b.md"))
        assertEquals("a.md", state.close("c.md"))
        assertEquals("a.md", state.close("missing.md"))
        assertFalse(state.select("missing.md"))
        assertNull(state.close("a.md"))
        assertTrue(state.tabs.isEmpty())
    }

    @Test fun `catalog reconciliation removes deleted documents and keeps the closest surviving tab`() {
        val state = pinned("a.md", "b.md", "c.md", "d.md")
        state.select("b.md")
        assertEquals("d.md", state.reconcile(setOf("a.md", "d.md", "not-open.md")))
        assertEquals(listOf("a.md", "d.md"), state.pinnedPaths)
        assertEquals("a.md", state.reconcile(setOf("a.md")))
        assertNull(state.reconcile(setOf("not-open.md")))
        assertTrue(state.tabs.isEmpty(), "Refresh must not open a document the user did not choose")
        assertTrue(state.isInitialized)
    }

    @Test fun `renaming folders updates descendants and active path without touching similar folder prefixes`() {
        val state = pinned("旧目录/方案.md", "旧目录/附录/说明.pdf", "旧目录备份/其他.md")
        state.openPreview("预览.txt")
        state.select("旧目录/附录/说明.pdf")
        assertEquals("新目录/附录/说明.pdf", state.rename("旧目录", "新目录"))
        assertEquals(listOf("新目录/方案.md", "新目录/附录/说明.pdf", "旧目录备份/其他.md"), state.pinnedPaths)
        assertEquals("预览.txt", state.temporaryPath)
        state.rename("预览.txt", "新目录/方案.md")
        assertNull(state.temporaryPath)
        assertEquals(3, state.tabs.size)
        assertTrue(state.tabs.first().isPinned)
    }

    @Test fun `invalid paths cannot enter tabs and normalized selection and deletion work`() {
        val state = MaterialsDocumentTabsState()
        for (invalid in listOf("", "../outside.md", "/root.md", "C:/outside.md", "folder//file.md", "a\u0000.md")) {
            state.openPreview(invalid)
            state.pin(invalid)
            assertFalse(state.select(invalid))
        }
        assertFalse(state.isInitialized)
        assertTrue(state.tabs.isEmpty())
        state.openPreview("研发/说明.md")
        assertTrue(state.select("研发\\说明.md"))
        assertEquals("研发/说明.md", state.reconcile(setOf("研发\\说明.md")))
        assertNull(state.close("研发\\说明.md"))
    }

    @Test fun `snapshot restoration validates duplicates preview count size and active document`() {
        val tabs = listOf(MaterialsDocumentTab("../outside.md", true), MaterialsDocumentTab("研发\\说明.md"),
            MaterialsDocumentTab("研发/说明.md", true), MaterialsDocumentTab("old-preview.md"), MaterialsDocumentTab("current-preview.md")) +
            List(MAX_READING_FILES_PER_TASK + 2) { MaterialsDocumentTab("$it.md", true) }
        val state = MaterialsDocumentTabsState().apply {
            restore(MaterialsDocumentTabsSnapshot(tabs, "研发\\说明.md"))
        }
        assertEquals(MAX_READING_FILES_PER_TASK, state.tabs.size)
        assertEquals("研发/说明.md", state.activePath)
        assertTrue(state.tabs.first().isPinned)
        assertTrue(state.tabs.none { it.path == "../outside.md" || it.path == "old-preview.md" })
        assertTrue(state.tabs.count { !it.isPinned } <= 1)
        assertTrue(state.isInitialized)
        val roundTrip = MaterialsDocumentTabsState().apply { restore(state.snapshot()) }
        assertEquals(state.tabs, roundTrip.tabs)
        assertEquals(state.activePath, roundTrip.activePath)
    }

    private fun pinned(vararg paths: String) = MaterialsDocumentTabsState().apply {
        paths.forEach { openPreview(it); pin() }
    }
}
