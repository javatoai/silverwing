package com.snowball.silverwing.desktop

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class MaterialsFullTextSearchServiceTest {
    @TempDir lateinit var root: Path

    @Test fun `Chinese queries search all preview formats with source line column and occurrence`() = runBlocking {
        Files.createDirectory(root.resolve("研发"))
        val markdown = "\uFEFF# 说明\r\n这里有支付要求\r\n第二处支付"
        Files.writeString(root.resolve("研发/说明.md"), markdown)
        Files.writeString(root.resolve("流程.py"), "value = '支付'\n")
        Files.writeString(root.resolve("接口.TSX"), "// 支付代码")
        Files.writeString(root.resolve("office.docx"), "支付")
        Files.writeString(root.resolve("图.png"), "支付")
        val result = MaterialsFullTextSearchService().search(root, "支付")
        assertEquals(4, result.hits.size)
        assertEquals(3, result.searchedFiles)
        assertEquals(2, result.unsupportedFiles)
        assertTrue(result.failures.isEmpty())
        val markdownHits = result.hits.filter { it.relativePath == "研发/说明.md" }
        assertEquals(listOf(2, 3), markdownHits.map { it.line })
        assertEquals(listOf(4, 4), markdownHits.map { it.column })
        assertEquals(listOf(0, 1), markdownHits.map { it.occurrenceIndex })
        assertEquals(markdown.removePrefix("\uFEFF").indexOf("支付"), markdownHits.first().offset)
        result.hits.forEach { hit ->
            assertNull(hit.pageIndex)
            assertEquals("支付", hit.snippet.substring(hit.snippetMatchRange))
            assertEquals(hit.offset until hit.offset + 2, hit.range)
        }
    }

    @Test fun `case insensitive search treats regex punctuation and spaces literally`() = runBlocking {
        Files.writeString(root.resolve("literal.txt"), "[A+B] aab [a+b] a.b A.B  x  y\n")
        val service = MaterialsFullTextSearchService()
        assertEquals(listOf(0, 10), service.search(root, "[a+b]").hits.map { it.offset })
        assertEquals(2, service.search(root, "a.b").hits.size)
        assertEquals(1, service.search(root, "x  y").hits.size)
        assertTrue(service.search(root, "x y").hits.isEmpty())
        assertTrue(service.search(root, "").hits.isEmpty())
    }

    @Test fun `PDF result positions match the reader index and page numbering`() = runBlocking {
        val source = root.resolve("two.pdf")
        writePreviewPdf(source)
        val index = RequirementMaterialsPdfService().open(root, "two.pdf").textIndex()
        val result = MaterialsFullTextSearchService().search(root, "rEQUIREMENT")
        assertEquals(listOf(0, 1), result.hits.map { it.pageIndex })
        assertEquals(listOf(0, 1), result.hits.map { it.occurrenceIndex })
        assertEquals(listOf(0, 0), result.hits.map { it.pageOccurrenceIndex })
        assertEquals(listOf("第 1 页", "第 2 页"), result.hits.map { it.locationLabel })
        result.hits.forEach { hit ->
            assertNull(hit.line)
            assertEquals(index.pages[hit.pageIndex!!].text.indexOf("Requirement"), hit.offset)
            assertEquals("Requirement", hit.snippet.substring(hit.snippetMatchRange))
        }
        // On Windows this proves the PDF parser and file input no longer retain a handle.
        Files.move(source, root.resolve("previous.pdf"))
        Files.delete(root.resolve("previous.pdf"))
    }

    @Test fun `one corrupt or invalid UTF8 file does not hide matches in other files`() = runBlocking {
        Files.writeString(root.resolve("a-corrupt.pdf"), "not PDF")
        Files.write(root.resolve("b-invalid.txt"), byteArrayOf(0xC3.toByte(), 0x28))
        Files.writeString(root.resolve("c-readable.md"), "支付成功")
        val result = MaterialsFullTextSearchService().search(root, "支付")
        assertEquals("c-readable.md", result.hits.single().relativePath)
        assertEquals(1, result.searchedFiles)
        assertEquals(2, result.skippedFiles)
        assertEquals(2, result.failureCount)
        assertTrue(result.failures.any { it.relativePath == "a-corrupt.pdf" && it.message.contains("损坏") })
        assertTrue(result.failures.any { it.relativePath == "b-invalid.txt" && it.message.contains("UTF-8") })
        Files.delete(root.resolve("a-corrupt.pdf"))
    }

    @Test fun `result and read limits are visible and long snippets stay bounded`() = runBlocking {
        Files.writeString(root.resolve("a-match.txt"), "match match match")
        Files.writeString(root.resolve("z-large.txt"), "match".repeat(200))
        val service = MaterialsFullTextSearchService(MaterialsFullTextSearchLimits(maxResults = 2, maxTextFileBytes = 128))
        val result = service.search(root, "MATCH")
        assertEquals(2, result.hits.size)
        assertTrue(result.truncated)
        val readResult = MaterialsFullTextSearchService(MaterialsFullTextSearchLimits(maxTextFileBytes = 128)).search(root, "match")
        assertEquals(3, readResult.hits.size)
        assertEquals(1, readResult.skippedFiles)
        assertTrue(readResult.failures.single().message.contains("大小上限"))
        val longText = "前".repeat(600) + "match" + "后".repeat(600)
        val snippet = scanMaterialSearchText("long.txt", "match", longText).single()
        assertTrue(snippet.snippet.length <= 242)
        assertEquals("match", snippet.snippet.substring(snippet.snippetMatchRange))
    }

    @Test fun `file count total bytes and failure list are bounded`() = runBlocking {
        repeat(4) { Files.writeString(root.resolve("$it.txt"), "match") }
        val catalog = MaterialsFullTextSearchService(MaterialsFullTextSearchLimits(maxFiles = 1)).search(root, "match")
        assertEquals(1, catalog.totalFiles)
        assertTrue(catalog.truncated)
        val bytes = MaterialsFullTextSearchService(MaterialsFullTextSearchLimits(maxTotalBytes = 6, maxFailures = 1)).search(root, "match")
        assertEquals(1, bytes.hits.size)
        assertEquals(5L, bytes.readBytes)
        assertEquals(3, bytes.failureCount)
        assertEquals(1, bytes.failures.size)
        assertTrue(bytes.truncated)
    }

    @Test fun `cancelling a running search interrupts the worker and prevents later reads`() = runBlocking {
        repeat(20) { Files.writeString(root.resolve("$it.txt"), "match") }
        val started = CompletableDeferred<Unit>()
        val pause = CountDownLatch(1)
        val enteredFiles = AtomicInteger()
        val finished = AtomicBoolean(false)
        val search = launch {
            MaterialsFullTextSearchService().search(root, "match") { progress ->
                if (progress.currentPath != null) {
                    enteredFiles.incrementAndGet()
                    started.complete(Unit)
                    pause.await()
                }
            }
            finished.set(true)
        }
        try {
            withTimeout(5_000) { started.await(); search.cancelAndJoin() }
            assertTrue(search.isCancelled)
            assertFalse(finished.get())
            assertEquals(1, enteredFiles.get())
        } finally {
            pause.countDown()
            search.cancelAndJoin()
        }
        Files.delete(root.resolve("0.txt"))
    }

    @Test fun `search never follows a symlink to material outside the task directory`() = runBlocking {
        val outside = Files.createTempDirectory("silverwing-search-outside")
        try {
            Files.writeString(outside.resolve("private.txt"), "match")
            // Creating symlinks requires privileges on some Windows test hosts.
            if (runCatching { Files.createSymbolicLink(root.resolve("linked"), outside) }.isFailure) return@runBlocking
            Files.writeString(root.resolve("ordinary.txt"), "match")
            val result = MaterialsFullTextSearchService().search(root, "match")
            assertEquals(listOf("ordinary.txt"), result.hits.map { it.relativePath })
        } finally {
            Files.deleteIfExists(root.resolve("linked"))
            Files.deleteIfExists(outside.resolve("private.txt"))
            Files.deleteIfExists(outside)
        }
    }

    @Test fun `missing root is a recoverable failure and CR only source lines are counted`() = runBlocking {
        assertFailsWith<IllegalArgumentException> { MaterialsFullTextSearchService().search(root.resolve("missing"), "match") }
        val hit = scanMaterialSearchText("old.txt", "match", "first\rmatch").single()
        assertEquals(2, hit.line)
        assertEquals(1, hit.column)
    }
}
