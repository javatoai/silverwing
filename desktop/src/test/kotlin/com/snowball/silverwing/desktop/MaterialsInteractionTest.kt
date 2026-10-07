package com.snowball.silverwing.desktop

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class MaterialsInteractionTest {
    @TempDir lateinit var root: Path

    @Test fun `delete targets deduplicate ancestors reject root and escaping paths`() {
        val folder = Files.createDirectory(root.resolve("中文 文件夹"))
        Files.writeString(folder.resolve("文档.txt"), "资料")
        val service = RequirementMaterialsMarkdownService()
        assertEquals(listOf(folder), service.resolveDeleteItems(root, listOf("中文 文件夹", "中文 文件夹/文档.txt")))
        assertFailsWith<IllegalArgumentException> { service.resolveDeleteItems(root, listOf(".")) }
        assertFailsWith<IllegalArgumentException> { service.resolveDeleteItems(root, listOf("../elsewhere")) }
        assertTrue(Files.exists(folder.resolve("文档.txt")))
    }

    @Test fun `task sidebar cannot be collapsed by saved width or a narrow window`() {
        for (available in listOf(220f, 580f, 700f, 1200f)) {
            assertEquals(160f, resolveTaskListPaneWidth(0f, available))
            assertEquals(160f, resolveTaskListPaneWidth(-500f, available))
            assertTrue(resolveTaskListPaneWidth(800f, available) >= 160f)
        }
    }

    @Test fun `image zoom preserves fit and supports original size for very large images`() {
        assertEquals(1f, boundedImageZoom(1f, 0.01f))
        assertEquals(100f, boundedImageZoom(100f, 0.01f))
        assertEquals(800f, boundedImageZoom(10_000f, 0.01f))
        assertEquals(0.1f, boundedImageZoom(-1f, 1f))
    }

    @Test fun `cancelled recycling never reports success or an error`() {
        val feedback = materialsTrashFeedback(listOf(FileTrashResult(root.resolve("a.txt"), cancelled = true)))
        assertNull(feedback.error)
        assertEquals("已取消移入回收站", feedback.status)
        assertEquals(MaterialsTrashFeedback(), materialsTrashFeedback(emptyList()))
    }

    @Test fun `partial recycling counts successful cancelled and failed results separately`() {
        val results = listOf(FileTrashResult(root.resolve("a.txt")),
            FileTrashResult(root.resolve("b.txt"), cancelled = true), FileTrashResult(root.resolve("c.txt"), "权限不足"))
        val feedback = materialsTrashFeedback(results)
        assertEquals("1 项已移入回收站；1 项已取消；c.txt：权限不足", feedback.error)
        assertNull(feedback.status)
        assertEquals("1 项已移入回收站；1 项已取消", materialsTrashFeedback(results.take(2)).status)
    }
}
