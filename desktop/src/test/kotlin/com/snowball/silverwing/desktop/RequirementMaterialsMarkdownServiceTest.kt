package com.snowball.silverwing.desktop

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertContentEquals
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.io.TempDir

class RequirementMaterialsMarkdownServiceTest {
    @TempDir
    lateinit var temporaryDirectory: Path

    @Test
    fun `lists nested ordinary files and ignores git metadata`() {
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
            listOf("nested/deep/notes.Md", "nested/guide.md", "README.MD", "script.sql"),
            catalog.files.map(RequirementMaterialsMarkdownFile::relativePath),
        )
        assertEquals(listOf(5L, 5L, 4L, 8L), catalog.files.map(RequirementMaterialsMarkdownFile::sizeBytes))
    }

    @Test
    fun `reads UTF-8 text and rejects traversal unsupported and oversized files`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("materials"))
        Files.writeString(root.resolve("说明.md"), "需求资料\n第二行")
        Files.writeString(root.resolve("large.md"), "1".repeat(65))
        Files.writeString(temporaryDirectory.resolve("outside.md"), "outside")
        val service = RequirementMaterialsMarkdownService(maxFileBytes = 64)

        assertEquals("需求资料\n第二行", service.read(root, "说明.md"))
        assertFailsWith<IllegalArgumentException> { service.read(root, "../outside.md") }
        assertFailsWith<IllegalArgumentException> { service.read(root, "unsupported.bin") }
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

    @Test
    fun `office images unknown and extensionless files are external and resolve without reading content`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("资料目录"))
        val names = listOf("方案.doc", "方案.docx", "预算.xls", "预算.XLSX", "设计.pptx", "截图.png", "未知.bin", "无扩展名")
        names.forEach { Files.write(root.resolve(it), byteArrayOf(0xff.toByte(), 0)) }
        val service = RequirementMaterialsMarkdownService()
        val catalog = service.list(root)
        assertEquals(names.toSet(), catalog.files.map { it.fileName }.toSet())
        catalog.files.forEach {
            assertFalse(it.textPreview || it.pdf)
            assertEquals(root.resolve(it.relativePath), service.resolveOpenFile(root, it.relativePath))
            assertFailsWith<IllegalArgumentException> { service.read(root, it.relativePath) }
        }
    }

    @Test
    fun `opening revalidates special paths deleted files folders and traversal`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("资料"))
        val relative = "中文 空格 & 引号' (1).docx"
        val file = Files.writeString(root.resolve(relative), "content")
        val service = RequirementMaterialsMarkdownService()
        assertEquals(file, service.resolveOpenFile(root, relative))
        Files.createDirectory(root.resolve("folder"))
        assertFailsWith<IllegalArgumentException> { service.resolveOpenFile(root, "folder") }
        assertFailsWith<IllegalArgumentException> { service.resolveOpenFile(root, "../outside.docx") }
        assertFailsWith<IllegalArgumentException> { service.resolveOpenFile(root, file.toString()) }
        Files.delete(file)
        assertFailsWith<java.nio.file.NoSuchFileException> { service.resolveOpenFile(root, relative) }
    }

    @Test
    fun `code files use bounded UTF-8 preview and unknown formats remain external`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("code"))
        val names = listOf("run.PY", "typing.pyi", "setup.sh", "setup.bash", "setup.zsh", "setup.ps1", "setup.psm1", "setup.bat", "setup.cmd", "Demo.java", "Demo.kt", "build.gradle.kts", "app.js", "app.ts", "query.sql", "config.yml", "page.html", "style.css", "config.properties")
        names.forEach { Files.writeString(root.resolve(it), "# 中文\nprint('hello')\n") }
        Files.write(root.resolve("broken.py"), byteArrayOf(0xc3.toByte(), 0x28))
        val service = RequirementMaterialsMarkdownService(maxFileBytes = 64)
        service.list(root).files.forEach { file ->
            kotlin.test.assertTrue(file.textPreview, file.fileName)
            if (file.fileName == "broken.py") assertFailsWith<IllegalArgumentException> { service.read(root, file.relativePath) }
            else assertEquals("# 中文\nprint('hello')\n", service.read(root, file.relativePath))
        }
        Files.writeString(root.resolve("large.py"), "1".repeat(65))
        assertFailsWith<IllegalArgumentException> { service.read(root, "large.py") }
    }

    @Test
    fun `significant leading spaces never redirect previews copies or deletion to a sibling`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("资料"))
        val ordinary = Files.writeString(root.resolve("说明.md"), "ordinary sibling")
        val spaced = Files.writeString(root.resolve(" 说明.md"), "selected spaced name")
        val pdf = Files.write(root.resolve(" 方案.pdf"), byteArrayOf(1, 2, 3))
        Files.write(root.resolve("方案.pdf"), byteArrayOf(9, 8, 7))
        val service = RequirementMaterialsMarkdownService()

        assertEquals(setOf("说明.md", " 说明.md", "方案.pdf", " 方案.pdf"), service.list(root).files.map { it.relativePath }.toSet())
        assertEquals("selected spaced name", service.read(root, " 说明.md"))
        assertEquals(spaced, service.resolveCopyItem(root, " 说明.md"))
        assertEquals(spaced, service.resolveOpenFile(root, " 说明.md"))
        assertEquals(listOf(spaced), service.resolveDeleteItems(root, listOf(" 说明.md")))
        assertEquals(pdf, service.resolveOpenFile(root, " 方案.pdf"))
        assertContentEquals(byteArrayOf(1, 2, 3), service.readPdf(root, " 方案.pdf"))
        assertEquals("ordinary sibling", Files.readString(ordinary))
    }

    @Test
    fun `significant leading directory spaces retain their full relative path`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("資料"))
        val spaced = Files.createDirectory(root.resolve(" 目录"))
        val other = Files.createDirectory(root.resolve("目录"))
        val document = Files.writeString(spaced.resolve("说明.txt"), "selected directory")
        Files.writeString(other.resolve("说明.txt"), "other directory")
        val service = RequirementMaterialsMarkdownService()
        assertEquals("selected directory", service.read(root, " 目录/说明.txt"))
        assertEquals(document, service.resolveOpenFile(root, " 目录/说明.txt"))
        assertEquals(listOf(spaced), service.resolveDeleteItems(root, listOf(" 目录", " 目录/说明.txt")))
    }
}
