package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

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
}
