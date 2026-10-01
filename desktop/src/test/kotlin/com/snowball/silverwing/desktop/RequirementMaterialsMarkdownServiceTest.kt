package com.snowball.silverwing.desktop

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.io.TempDir

class RequirementMaterialsMarkdownServiceTest {
    @TempDir
    lateinit var temporaryDirectory: Path

    @Test
    fun `lists nested Markdown files and ignores other file types`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        Files.writeString(root.resolve("README.MD"), "root")
        Files.writeString(root.resolve("script.sql"), "select 1")
        Files.createDirectories(root.resolve("nested/deep"))
        Files.writeString(root.resolve("nested/guide.md"), "guide")
        Files.writeString(root.resolve("nested/deep/notes.Md"), "notes")
        Files.createDirectories(root.resolve(".git"))
        Files.writeString(root.resolve(".git/config.md"), "metadata")

        val catalog = RequirementMaterialsMarkdownService().list(root)

        assertEquals(
            listOf("nested/deep/notes.Md", "nested/guide.md", "README.MD"),
            catalog.files.map(RequirementMaterialsMarkdownFile::relativePath),
        )
        assertEquals(listOf(5L, 5L, 4L), catalog.files.map(RequirementMaterialsMarkdownFile::sizeBytes))
    }

    @Test
    fun `reads UTF-8 Markdown and rejects traversal non-Markdown and oversized files`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        Files.writeString(root.resolve("说明.md"), "需求资料\n第二行")
        Files.writeString(root.resolve("large.md"), "1".repeat(65))
        Files.writeString(temporaryDirectory.resolve("outside.md"), "outside")
        val service = RequirementMaterialsMarkdownService(maxFileBytes = 64)

        assertEquals("需求资料\n第二行", service.read(root, "说明.md"))
        assertFailsWith<IllegalArgumentException> { service.read(root, "../outside.md") }
        assertFailsWith<IllegalArgumentException> { service.read(root, "script.sql") }
        assertFailsWith<IllegalArgumentException> { service.read(root, "large.md") }
    }

    @Test
    fun `does not list or read symbolic links`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val externalDirectory = Files.createDirectory(temporaryDirectory.resolve("external"))
        val externalFile = Files.writeString(externalDirectory.resolve("outside.md"), "outside")
        val linkedDirectory = root.resolve("linked-directory")
        val linkedFile = root.resolve("linked.md")
        val symlinksCreated = runCatching {
            Files.createSymbolicLink(linkedDirectory, externalDirectory)
            Files.createSymbolicLink(linkedFile, externalFile)
        }.isSuccess
        assumeTrue(symlinksCreated, "Symbolic links are unavailable in this test environment")

        val service = RequirementMaterialsMarkdownService()
        val catalog = service.list(root)

        assertFalse(catalog.files.any { it.relativePath == "linked.md" || it.relativePath.startsWith("linked-directory/") })
        assertFailsWith<IllegalArgumentException> { service.read(root, "linked.md") }
        assertFailsWith<IllegalArgumentException> { service.read(root, "linked-directory/outside.md") }
    }

    @Test
    fun `lists empty directories and supported text files and deduplicates copied descendants`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val empty = Files.createDirectory(root.resolve("empty"))
        val nested = Files.createDirectory(root.resolve("nested"))
        val markdown = Files.writeString(nested.resolve("note.md"), "# Note")
        val json = Files.writeString(root.resolve("data.json"), "{\"ok\":true}")
        Files.writeString(root.resolve("records.csv"), "a,b\n1,2")
        Files.writeString(root.resolve("memo.txt"), "hello")

        val service = RequirementMaterialsMarkdownService()
        val catalog = service.list(root)
        assertEquals(listOf("empty", "nested"), catalog.directories)
        assertEquals(setOf("data.json", "memo.txt", "nested/note.md", "records.csv"), catalog.files.map { it.relativePath }.toSet())
        assertEquals("{\"ok\":true}", service.read(root, "data.json"))
        assertEquals(listOf(nested, json, empty), withoutCopiedDescendants(listOf(nested, markdown, json, empty)))
        assertEquals(empty, service.resolveCopyItem(root, "empty"))
    }

    @Test
    fun `rejects invalid UTF-8 while keeping the file available for native copy`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        val bad = Files.write(root.resolve("bad.txt"), byteArrayOf(0xC3.toByte(), 0x28))
        val service = RequirementMaterialsMarkdownService()
        assertFailsWith<IllegalArgumentException> { service.read(root, "bad.txt") }
        assertEquals(bad, service.resolveCopyItem(root, "bad.txt"))
    }
}
