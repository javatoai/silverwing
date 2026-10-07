package com.snowball.silverwing.desktop

import kotlin.test.*

class TaskBrowsingSessionTest {
    @Test fun `notes keep per task modes positions and no document content in persisted snapshots`() {
        val session = TaskBrowsingSession()
        val a = session.notesFor("C:/tasks/A")
        assertEquals(TaskNotesPageMode.READ, a.mode)
        assertEquals(TASK_NOTES_PREVIEW_PATH, a.files.selectedPath)
        a.mode = TaskNotesPageMode.EDIT
        a.files.readingFor(TASK_NOTES_PREVIEW_PATH).scrollPositions["markdown-rendered"] = 420
        val b = session.notesFor("C:/tasks/B")
        assertEquals(TaskNotesPageMode.READ, b.mode)
        assertSame(a, session.notesFor("C:/tasks/A"))
        assertEquals(TaskNotesPageMode.EDIT, a.mode)
        session.restore(ReadingSnapshot())
        assertSame(a, session.notesFor("C:/tasks/A"), "Startup restores only persisted readers, not live notes state")
        assertEquals(TaskNotesPageMode.EDIT, a.mode)
        a.files.readingFor(TASK_NOTES_PREVIEW_PATH).mode.value = MarkdownPreviewMode.SOURCE
        a.readDraft()
        assertEquals(MarkdownPreviewMode.RENDERED, a.files.readingFor(TASK_NOTES_PREVIEW_PATH).mode.value)
        assertEquals(420, a.files.readingFor(TASK_NOTES_PREVIEW_PATH).scrollPositions["markdown-rendered"])
        assertTrue(session.snapshot().materials.isEmpty())
        assertTrue(session.snapshot().requirements.isEmpty())
    }

    @Test fun `notes session evicts the least recently visited task`() {
        val session = TaskBrowsingSession()
        val oldest = session.notesFor("C:/tasks/oldest")
        repeat(MAX_READING_TASKS - 1) { session.notesFor("C:/tasks/$it") }
        val recent = session.notesFor("C:/tasks/0")
        session.notesFor("C:/tasks/new")
        assertSame(recent, session.notesFor("C:/tasks/0"))
        assertNotSame(oldest, session.notesFor("C:/tasks/oldest"))
    }
    @Test fun `switching tasks preserves their own selected file and resets changed roots`() {
        val session = TaskBrowsingSession(240f)
        session.view = TaskContentView.MATERIALS
        val a = session.materialsFor("C:/tasks/A", "C:/materials/A")
        a.selectedPath.value = "研发/方案.pdf"
        a.expandedDirectories.value = setOf("研发")
        a.readingFor("研发/方案.pdf").apply {
            zoomPercent.intValue = 150
            listPositions["pdf-pages"] = MaterialsListPosition(3, 52)
        }
        val b = session.materialsFor("C:/tasks/B", "C:/materials/B")
        assertNull(b.selectedPath.value)
        assertSame(a, session.materialsFor("C:/tasks/A", "C:/materials/A"))
        assertEquals(TaskContentView.MATERIALS, session.view)
        assertEquals(150, a.readingFor("研发/方案.pdf").zoomPercent.intValue)
        assertNotSame(a, session.materialsFor("C:/tasks/A", "C:/materials/new-root"))
    }

    @Test fun `refresh removes deleted file positions but retains the active file`() {
        val state = MaterialsBrowserState()
        state.selectedPath.value = "b.txt"
        val position = state.readingFor("b.txt").apply { scrollPositions["text"] = 300 }
        state.retainFiles(linkedSetOf("a.txt", "b.txt"))
        assertSame(position, state.readingFor("b.txt"))
        state.retainFiles(linkedSetOf("a.txt"))
        assertEquals("a.txt", state.selectedPath.value)
        assertTrue(state.readingFor("b.txt").scrollPositions.isEmpty())
        state.retainFiles(emptySet())
        assertNull(state.selectedPath.value)
    }

    @Test fun `old widths and narrow windows always retain minimum task list width`() {
        assertEquals(160f, TaskBrowsingSession(24f).preferredWidth)
        assertEquals(160f, resolveTaskListPaneWidth(0f, 1200f))
        assertEquals(392f, resolveTaskListPaneWidth(800f, 1000f))
        assertEquals(160f, resolveTaskListPaneWidth(500f, 700f))
        assertEquals(160f, resolveTaskListPaneWidth(240f, 220f))
    }

    @Test fun `requirement reading position restores per task and resets changed link`() {
        val session = TaskBrowsingSession()
        val a = session.requirementFor("C:/tasks/A", "https://project.feishu.cn/obt/userstory/detail/1")
        a.mode.value = MarkdownPreviewMode.SOURCE
        a.scrollPositions["source-vertical"] = 250
        session.requirementFor("C:/tasks/B", "https://project.feishu.cn/obt/userstory/detail/2")
        assertSame(a, session.requirementFor("C:/tasks/A", "https://project.feishu.cn/obt/userstory/detail/1"))
        assertEquals(250, a.scrollPositions["source-vertical"])
        val changed = session.requirementFor("C:/tasks/A", "https://project.feishu.cn/obt/userstory/detail/3")
        assertNotSame(a, changed)
        assertEquals(MarkdownPreviewMode.RENDERED, changed.mode.value)
    }

    @Test fun `file and task caches evict least recently used positions and protect selected file`() {
        val browser = MaterialsBrowserState()
        browser.selectedPath.value = "selected.pdf"
        val selected = browser.readingFor("selected.pdf")
        val revisited = browser.readingFor("recent.md")
        repeat(MAX_READING_FILES_PER_TASK - 2) { browser.readingFor("old-$it.md") }
        browser.readingFor("recent.md")
        repeat(5) { browser.readingFor("new-$it.md") }
        assertEquals(MAX_READING_FILES_PER_TASK, browser.files.size)
        assertSame(selected, browser.readingFor("selected.pdf"))
        assertSame(revisited, browser.readingFor("recent.md"))
        assertFalse(browser.files.containsKey("old-0.md"))

        val session = TaskBrowsingSession()
        val active = session.materialsFor("C:/tasks/active", "C:/materials/active")
        repeat(MAX_READING_TASKS - 1) { session.materialsFor("C:/tasks/$it", "C:/materials/$it") }
        session.materialsFor("C:/tasks/active", "C:/materials/active")
        session.materialsFor("C:/tasks/new", "C:/materials/new")
        assertSame(active, session.materialsFor("C:/tasks/active", "C:/materials/active"))
        assertEquals(MAX_READING_TASKS, session.snapshot().materials.size)
    }

    @Test fun `restoring replacement cache clears removed records and invalid positions`() {
        val session = TaskBrowsingSession()
        session.materialsFor("C:/tasks/old", "C:/materials/old").selectedPath.value = "old.pdf"
        session.requirementFor("C:/tasks/old", "https://example.test/old").scrollPositions["old"] = 50
        session.restore(ReadingSnapshot(view = "unknown", materials = listOf(MaterialsSnapshot("C:/tasks/new", "C:/materials/new",
            selectedPath = "../outside.pdf", expandedDirectories = listOf("研发", "../outside"),
            files = mapOf("valid.pdf" to ReadingPositionSnapshot("invalid", -50, mapOf("horizontal" to -10), mapOf("pdf-pages" to listOf(-2, Int.MAX_VALUE, 8))))))))
        assertEquals(1, session.snapshot().materials.size)
        assertTrue(session.snapshot().requirements.isEmpty())
        assertEquals(TaskContentView.DETAIL, session.view)
        val browser = session.materialsFor("C:/tasks/new", "C:/materials/new")
        assertNull(browser.selectedPath.value)
        assertEquals(setOf("研发"), browser.expandedDirectories.value)
        val reading = browser.readingFor("valid.pdf")
        assertEquals(25, reading.zoomPercent.intValue)
        assertEquals(0, reading.scrollPositions["horizontal"])
        assertEquals(MaterialsListPosition(0, 100_000_000), reading.listPositions["pdf-pages"])
        reading.restore(ReadingPositionSnapshot())
        assertTrue(reading.scrollPositions.isEmpty())
        assertTrue(reading.listPositions.isEmpty())
    }

    @Test fun `non finite task pane widths cannot reach layout even after reassignment`() {
        for (invalid in listOf(Float.NaN, Float.POSITIVE_INFINITY, Float.NEGATIVE_INFINITY)) {
            val session = TaskBrowsingSession(invalid)
            assertEquals(DEFAULT_TASK_LIST_PANE_WIDTH_DP, session.preferredWidth)
            session.preferredWidth = invalid
            assertTrue(session.preferredWidth.isFinite())
            assertEquals(DEFAULT_TASK_LIST_PANE_WIDTH_DP, session.preferredWidth)
        }
        val session = TaskBrowsingSession()
        session.preferredWidth = -1f
        assertEquals(MIN_TASK_LIST_PANE_WIDTH_DP, session.preferredWidth)
    }

    @Test fun `restored deleted file is removed and changed root never inherits previous reading`() {
        val task = "C:/tasks/A"
        val oldRoot = "C:/materials/A"
        val session = TaskBrowsingSession().apply { restore(ReadingSnapshot(materials = listOf(MaterialsSnapshot(task, oldRoot,
            selectedPath = "deleted.pdf", files = mapOf("deleted.pdf" to ReadingPositionSnapshot(zoom = 175,
                lists = mapOf("pdf-pages" to listOf(6, 40))), "remaining.md" to ReadingPositionSnapshot(mode = "SOURCE", scrolls = mapOf("source-vertical" to 700))))))) }
        val browser = session.materialsFor(task, oldRoot)
        browser.retainFiles(linkedSetOf("remaining.md"))
        assertEquals("remaining.md", browser.selectedPath.value)
        assertFalse("deleted.pdf" in browser.fileSnapshots())
        assertEquals(700, browser.readingFor("remaining.md").scrollPositions["source-vertical"])
        val newBrowser = session.materialsFor(task, "C:/materials/new")
        assertNull(newBrowser.selectedPath.value)
        assertTrue(newBrowser.fileSnapshots().isEmpty())
        assertEquals(1, session.snapshot().materials.size)
        assertTrue(session.snapshot().materials.single().files.isEmpty())
    }

    @Test fun `cache normalization retains selected backslash path beyond file count limit`() {
        val files = linkedMapOf("研发\\selected.pdf" to ReadingPositionSnapshot(zoom = 175, lists = mapOf("pdf-pages" to listOf(7, 80))))
        repeat(MAX_READING_FILES_PER_TASK + 1) { files["other-$it.md"] = ReadingPositionSnapshot() }
        val saved = ReadingSnapshot(materials = listOf(MaterialsSnapshot("C:/tasks/A", "C:/materials/A",
            selectedPath = "研发\\selected.pdf", files = files))).bounded()
        val record = saved.materials.single()
        assertEquals("研发/selected.pdf", record.selectedPath)
        assertEquals(MAX_READING_FILES_PER_TASK, record.files.size)
        assertEquals(175, record.files.getValue("研发/selected.pdf").zoom)
        val session = TaskBrowsingSession().apply { restore(saved) }
        val browser = session.materialsFor("C:/tasks/A", "C:/materials/A")
        assertEquals(MaterialsListPosition(7, 80), browser.readingFor("研发/selected.pdf").listPositions["pdf-pages"])
    }
}
