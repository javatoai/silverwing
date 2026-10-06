package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.ByteBuffer
import java.nio.file.StandardOpenOption
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class AiContextCopyServiceTest {
    @TempDir lateinit var root: Path

    @Test fun `builds requirement body link services current branch and selected materials without unrelated files`() = runTest {
        Files.writeString(root.resolve("chosen.md"), "# 已选择的说明\n\n只复制这份")
        Files.writeString(root.resolve("unselected.txt"), "这份不能自动读取")
        val task = task().copy(services = listOf(ServiceWorkspace("repo", "结算", "repo", "worktree", DevelopmentToolType.INTELLIJ_IDEA, "recorded")))
        var bodyReads = 0
        val document = AiContextCopyService().load(AiContextCopyRequest(task, "结算需求", materialsRoot = root,
            selectedMaterialPaths = listOf("chosen.md"), currentBranches = mapOf("worktree" to "feature/current")),
            loadRequirementBody = { bodyReads++; "# 需求正文\n\n改动说明" })
        val preview = buildAiContextCopyPreview(document)
        assertEquals(1, bodyReads)
        assertTrue(preview.text.contains("标题：结算需求"))
        assertTrue(preview.text.contains(task.requirementLink))
        assertTrue(preview.text.contains("改动说明"))
        assertTrue(preview.text.contains("feature/current（当前分支）"))
        assertTrue(preview.text.contains("只复制这份"))
        assertFalse(preview.text.contains("这份不能自动读取"))
        assertFalse(preview.truncated)
    }

    @Test fun `missing link does not fetch a body and leaves task service and files copyable`() = runTest {
        Files.writeString(root.resolve("notes.txt"), "独立资料")
        val document = AiContextCopyService().load(AiContextCopyRequest(task().copy(requirementLink = ""),
            materialsRoot = root, selectedMaterialPaths = listOf("notes.txt")), { error("No link must not fetch remote content") })
        assertEquals(listOf("task", "material:0:notes.txt"), document.sections.map { it.id })
        assertTrue(buildAiContextCopyPreview(document).text.contains("独立资料"))
        assertTrue(document.notices.any { it.contains("没有关联需求链接") })
    }

    @Test fun `body failure and individual binary invalid or unsupported material failures preserve successful material`() = runTest {
        Files.writeString(root.resolve("valid.sql"), "select id from orders;")
        Files.write(root.resolve("wrong.txt"), byteArrayOf(0xC3.toByte(), 0x28))
        Files.writeString(root.resolve("binary.txt"), "binary\u0000payload")
        Files.writeString(root.resolve("report.docx"), "PK office zip bytes must not become body text")
        val document = AiContextCopyService().load(AiContextCopyRequest(task(), materialsRoot = root,
            selectedMaterialPaths = listOf("valid.sql", "wrong.txt", "binary.txt", "report.docx", "gone.txt", "../outside.txt")),
            loadRequirementBody = { error("正文权限不足") })
        assertTrue(document.notices.any { it.contains("正文权限不足") })
        assertTrue(document.notices.any { it.contains("wrong.txt") && it.contains("UTF-8") })
        assertTrue(document.notices.any { it.contains("binary.txt") && it.contains("二进制") })
        assertTrue(document.notices.any { it.contains("report.docx") && it.contains("不支持文字提取") })
        assertTrue(document.notices.any { it.contains("gone.txt") })
        assertTrue(document.notices.any { it.contains("路径不安全") })
        val preview = buildAiContextCopyPreview(document)
        assertTrue(preview.text.contains("select id from orders;"))
        assertFalse(preview.text.contains("PK office zip bytes"))
        assertFalse(preview.text.contains("binary\u0000payload"))
        val unsupported = document.sections.single { it.title.contains("report.docx") }
        assertFalse(unsupported.selectedByDefault)
        assertTrue(buildAiContextCopyPreview(document, setOf(unsupported.id)).text.contains("文件：report.docx"))
    }

    @Test fun `PDF extraction copies actual text with page limits and isolates a corrupt PDF`() = runTest {
        writePreviewPdf(root.resolve("notes.pdf"))
        Files.writeString(root.resolve("bad.pdf"), "this is not a PDF")
        val document = AiContextCopyService().load(AiContextCopyRequest(task().copy(requirementLink = ""),
            materialsRoot = root, selectedMaterialPaths = listOf("bad.pdf", "notes.pdf")))
        assertTrue(document.notices.any { it.contains("bad.pdf") })
        val copied = buildAiContextCopyPreview(document).text
        assertTrue(copied.contains("Requirement materials / 1"))
        assertTrue(copied.contains("PDF preview: paging, zoom and fit width"))
        assertFalse(copied.contains("this is not a PDF"))
        Files.move(root.resolve("notes.pdf"), root.resolve("closed.pdf"))
    }

    @Test fun `body and material truncation and copy bounds are visible and do not split surrogate pairs`() = runTest {
        Files.writeString(root.resolve("long.md"), "资料".repeat(90))
        val document = AiContextCopyService(sectionCharacters = 80).load(AiContextCopyRequest(task(),
            requirementBody = "正文".repeat(90), materialsRoot = root, selectedMaterialPaths = listOf("long.md")))
        assertTrue(document.notices.any { it.contains("需求正文") && it.contains("已截断") })
        assertTrue(document.notices.any { it.contains("资料：long.md") && it.contains("已截断") })
        assertTrue(document.sections.all { it.content.length <= 80 })
        val preview = buildAiContextCopyPreview(document, maxCharacters = 100)
        assertTrue(preview.truncated)
        assertTrue(preview.text.length <= 100)
        assertTrue(preview.text.endsWith("[内容已截断]"))
        val bounded = limitAiContextText("a".repeat(13) + "\uD83D\uDE00".repeat(30), 30)
        assertFalse(bounded.substringBefore("\n\n[内容已截断]").last().isHighSurrogate())
        assertTrue(bounded.length <= 30)
    }

    @Test fun `selection only copies chosen sections and selected directory expansion is bounded`() = runTest {
        val notes = Files.createDirectory(root.resolve("notes"))
        repeat(6) { Files.writeString(notes.resolve("$it.txt"), "资料 $it") }
        val document = AiContextCopyService(materialFiles = 2).load(AiContextCopyRequest(task(), requirementBody = "正文",
            materialsRoot = root, selectedMaterialPaths = listOf("notes")))
        assertEquals(2, document.sections.count { it.id.startsWith("material:") })
        assertTrue(document.notices.any { it.contains("最多读取 2 个文件") })
        val selected = buildAiContextCopyPreview(document, setOf("requirement"))
        assertTrue(selected.text.contains(task().requirementLink))
        assertFalse(selected.text.contains("任务分支"))
        assertFalse(selected.text.contains("需求正文"))
        assertEquals("", buildAiContextCopyPreview(document, emptySet()).text)
    }

    @Test fun `cancellation never becomes a body error and no material is read afterward`() = runTest {
        val request = AiContextCopyRequest(task(), materialsRoot = root, selectedMaterialPaths = listOf("never.txt"))
        assertFailsWith<CancellationException> { AiContextCopyService().load(request, { throw CancellationException("closed") }) }
    }

    @Test fun `oversized text and PDF are skipped individually while a normal document remains available`() = runTest {
        Files.newByteChannel(root.resolve("large.txt"), StandardOpenOption.CREATE, StandardOpenOption.WRITE).use {
            it.position(2L * 1024 * 1024); it.write(ByteBuffer.wrap(byteArrayOf(0)))
        }
        Files.newByteChannel(root.resolve("large.pdf"), StandardOpenOption.CREATE, StandardOpenOption.WRITE).use {
            it.position(16L * 1024 * 1024); it.write(ByteBuffer.wrap(byteArrayOf(0)))
        }
        Files.writeString(root.resolve("normal.txt"), "有界读取仍然正常")
        val document = AiContextCopyService().load(AiContextCopyRequest(task().copy(requirementLink = ""), materialsRoot = root,
            selectedMaterialPaths = listOf("large.txt", "large.pdf", "normal.txt")))
        assertTrue(document.notices.any { it.contains("large.txt") && it.contains("超过") })
        assertTrue(document.notices.any { it.contains("large.pdf") && it.contains("16 MB") })
        assertTrue(buildAiContextCopyPreview(document).text.contains("有界读取仍然正常"))
    }

    private fun task() = TaskManifest(folderName = "上下文任务", taskDirectoryName = "上下文任务", featureBranch = "feature/context",
        requirementLink = "https://project.feishu.cn/obt/userstory/detail/123", requirementId = "123",
        createdAt = "now", updatedAt = "now", services = emptyList())
}
