package com.snowball.silverwing.desktop

import java.nio.charset.StandardCharsets
import java.nio.file.Files
import kotlin.io.path.createTempDirectory
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class MaterialsMarkdownEditorTest {
    @Test
    fun savePreservesUtf8BomAndCrLf() {
        val root = createTempDirectory("markdown-editor-")
        val file = root.resolve("说明.md")
        val original = byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + "# 标题\r\n正文\r\n".toByteArray(StandardCharsets.UTF_8)
        Files.write(file, original)
        val service = MaterialsMarkdownEditorService()
        val baseline = service.snapshot(root, "说明.md")

        val saved = assertIs<MarkdownSaveResult.Saved>(service.save(root, "说明.md", "# 新标题\n第二行\n", baseline.digest))
        val actual = Files.readAllBytes(file)
        val expected = byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + "# 新标题\r\n第二行\r\n".toByteArray(StandardCharsets.UTF_8)
        assertContentEquals(expected, actual)
        assertContentEquals(expected, saved.snapshot.bytes)
        assertEquals(service.digest(expected), saved.snapshot.digest)
    }

    @Test
    fun externalChangeReturnsConflictAndDoesNotOverwrite() {
        val root = createTempDirectory("markdown-editor-")
        val file = root.resolve("说明.md")
        Files.writeString(file, "原始\n")
        val service = MaterialsMarkdownEditorService()
        val baseline = service.snapshot(root, "说明.md")
        Files.writeString(file, "外部修改\n")

        val result = service.save(root, "说明.md", "本地草稿\n", baseline.digest)
        assertIs<MarkdownSaveResult.Conflict>(result)
        assertEquals("外部修改\n", Files.readString(file))
    }

    @Test
    fun strictUtf8AndMarkdownPathAreEnforced() {
        val root = createTempDirectory("markdown-editor-")
        Files.write(root.resolve("bad.md"), byteArrayOf(0xC3.toByte(), 0x28))
        val service = MaterialsMarkdownEditorService()
        assertFailsWith<Throwable> { service.snapshot(root, "bad.md") }
        Files.writeString(root.resolve("ok.txt"), "text")
        assertFailsWith<IllegalArgumentException> { service.snapshot(root, "ok.txt") }
        assertTrue(Files.isRegularFile(root.resolve("bad.md")))
    }

    @Test
    fun gitAndDeletedPathsCannotBeEdited() {
        val root = createTempDirectory("markdown-editor-")
        val git = Files.createDirectories(root.resolve(".git"))
        Files.writeString(git.resolve("config.md"), "secret")
        val service = MaterialsMarkdownEditorService()
        assertFailsWith<IllegalArgumentException> { service.snapshot(root, ".git/config.md") }
        val deleted = root.resolve("deleted.md")
        Files.writeString(deleted, "before")
        val baseline = service.snapshot(root, "deleted.md")
        Files.delete(deleted)
        assertFailsWith<Throwable> { service.save(root, "deleted.md", "after", baseline.digest) }
    }

    @Test
    fun encodedContentIsCheckedAfterBomAndNewlineConversion() {
        val root = createTempDirectory("markdown-editor-")
        val file = root.resolve("large.md")
        Files.write(file, byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + "a\n".toByteArray())
        val service = MaterialsMarkdownEditorService(maxBytes = 8)
        val baseline = service.snapshot(root, "large.md")
        assertFailsWith<IllegalArgumentException> { service.save(root, "large.md", "1234567", baseline.digest) }
    }

    @Test
    fun symlinkedMarkdownIsRejectedWhenThePlatformAllowsCreatingOne() {
        val root = createTempDirectory("markdown-editor-")
        val outside = createTempDirectory("markdown-editor-outside-")
        Files.writeString(outside.resolve("real.md"), "outside")
        val link = root.resolve("linked.md")
        if (runCatching { Files.createSymbolicLink(link, outside.resolve("real.md")) }.isFailure) return
        assertFailsWith<IllegalArgumentException> { MaterialsMarkdownEditorService().snapshot(root, "linked.md") }
    }

    @Test
    fun draftStoreFlushesAndRestoresAcrossInstances() {
        val directory = createTempDirectory("markdown-drafts-")
        val cache = directory.resolve("drafts.json")
        val identity = MarkdownDraftIdentity("C:/tasks/a", directory.toString(), "说明.md")
        val draft = MarkdownDraft("# 草稿\n", "baseline", true, "\r\n")
        val first = MarkdownDraftStore(cache)
        first.put(identity, draft)
        first.flush()
        first.close()

        val second = MarkdownDraftStore(cache)
        second.loadFromDisk()
        assertEquals(draft, second.get(identity))
        second.close()
    }

    @Test
    fun draftStoreDoesNotSilentlyEvictWhenLimitsAreReached() {
        val cache = createTempDirectory("markdown-drafts-").resolve("drafts.json")
        val store = MarkdownDraftStore(cache, maxEntries = 1)
        val first = MarkdownDraftIdentity("task-a", "root", "a.md")
        val second = MarkdownDraftIdentity("task-b", "root", "b.md")
        store.put(first, MarkdownDraft("a", "a", false, "\n"))
        assertFailsWith<IllegalArgumentException> { store.put(second, MarkdownDraft("b", "b", false, "\n")) }
        assertNotNull(store.get(first))
        assertNull(store.get(second))
        store.close()
    }

    @Test
    fun draftIdentityMigrationHandlesDirectories() {
        val cache = createTempDirectory("markdown-drafts-").resolve("drafts.json")
        val store = MarkdownDraftStore(cache)
        val old = MarkdownDraftIdentity("task", "root", "old/note.md")
        val replacement = MarkdownDraftIdentity("task", "root", "new/note.md")
        store.put(old, MarkdownDraft("draft", "baseline", false, "\n"))
        store.rename("task", "root", "old", "new")
        assertNull(store.get(old))
        assertEquals("draft", store.get(replacement)?.content)
        store.close()
    }
}
