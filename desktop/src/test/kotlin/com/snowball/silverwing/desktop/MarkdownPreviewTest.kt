package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.flavours.gfm.GFMElementTypes
import org.intellij.markdown.flavours.gfm.GFMFlavourDescriptor
import org.intellij.markdown.parser.MarkdownParser

class MarkdownPreviewTest {
    @Test
    fun `selecting Markdown file honors the requested path and falls back to the first file`() {
        val root = MarkdownPreviewFile("AGENTS.md", "router")
        val scope = MarkdownPreviewFile(".workspace/agent/WORKTREE-SCOPE.md", "scope")
        val files = listOf(root, scope)

        assertEquals(scope, selectMarkdownPreviewFile(files, scope.path))
        assertEquals(root, selectMarkdownPreviewFile(files, "missing.md"))
        assertEquals(root, selectMarkdownPreviewFile(files, null))
        assertNull(selectMarkdownPreviewFile(emptyList(), "AGENTS.md"))
    }

    @Test
    fun `Markdown preview exposes the opposite mode through one compact action`() {
        assertEquals(MarkdownPreviewMode.RENDERED, initialMarkdownPreviewMode())
        assertEquals(
            MarkdownPreviewTogglePresentation(
                targetMode = MarkdownPreviewMode.SOURCE,
                label = "查看 Markdown 源码",
                icon = MarkdownPreviewToggleIcon.CODE,
            ),
            markdownPreviewTogglePresentation(MarkdownPreviewMode.RENDERED),
        )
        assertEquals(
            MarkdownPreviewTogglePresentation(
                targetMode = MarkdownPreviewMode.RENDERED,
                label = "查看 Markdown 预览",
                icon = MarkdownPreviewToggleIcon.VISIBILITY,
            ),
            markdownPreviewTogglePresentation(MarkdownPreviewMode.SOURCE),
        )
    }

    @Test
    fun `Markdown copy payload is always the selected raw source`() {
        val file = MarkdownPreviewFile(".workspace/agent/TASK-RULES.md", "# 标题\n\n`原始 Markdown`")

        assertEquals("# 标题\n\n`原始 Markdown`", markdownPreviewSourceCopyPayload(file))
    }

    @Test
    fun `preview recognizes tables with Windows and Unix line endings while copy retains the source`() {
        val lines = listOf(
            "## 实施范围", "", "本次分流仅覆盖现有订阅：", "",
            "| 项目 | 范围 |", "| --- | --- |",
            "| Topic | `TOPIC-FP-IOS-THIRD-MINI-APP` |",
            "| 分流识别 | 字符串 `appCode` 命中名单 |", "",
        )
        for (lineEnding in listOf("\n", "\r\n", "\r")) {
            val source = lines.joinToString(lineEnding)
            val rendered = markdownPreviewRenderInput(source)
            val ast = MarkdownParser(GFMFlavourDescriptor()).buildMarkdownTreeFromString(rendered)
            fun hasTable(node: ASTNode): Boolean = node.type == GFMElementTypes.TABLE || node.children.any(::hasTable)
            assertTrue(hasTable(ast), "Table not recognized for line ending ${lineEnding.toByteArray().toList()}")
            assertEquals(source, markdownPreviewSourceCopyPayload(MarkdownPreviewFile("实施计划.md", source)))
        }
    }
}
