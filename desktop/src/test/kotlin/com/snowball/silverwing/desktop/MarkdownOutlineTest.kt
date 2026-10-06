package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class MarkdownOutlineTest {
    @Test
    fun `outline uses all six rendered heading levels and ignores code fence headings`() {
        val content = """
            # 一级
            ## 二级
            ### 三级
            #### 四级
            ##### 五级
            ###### 六级

            ```markdown
            # 围栏伪标题
            围栏 setext
            ========
            ```

            ~~~~text
            ## 波浪线围栏伪标题
            ~~~~

                # 缩进代码伪标题

            #没有空格
            ####### 七个井号
        """.trimIndent()
        val entries = parseMarkdownOutline(content)
        assertEquals((1..6).toList(), entries.map { it.level })
        assertEquals(listOf("一级", "二级", "三级", "四级", "五级", "六级"), entries.map { it.title })
        assertTrue(entries.zipWithNext().all { (first, second) -> first.startOffset < second.startOffset })
    }

    @Test
    fun `setext headings and Windows input follow the render parser and omit front matter`() {
        val lines = listOf("---", "title: 示例", "tags: [设计, 开发]", "---", "", "一级 setext", "===", "", "二级 setext", "---", "", "## 正文")
        for (ending in listOf("\n", "\r\n")) {
            val entries = parseMarkdownOutline(lines.joinToString(ending))
            assertEquals(listOf(1, 2, 2), entries.map { it.level })
            assertEquals(listOf("一级 setext", "二级 setext", "正文"), entries.map { it.title })
            assertEquals(listOf("一级-setext", "二级-setext", "正文"), entries.map { it.anchor })
        }
        assertEquals(parseMarkdownOutline("# 正文\n\n## 细节"), parseMarkdownOutline("# 正文\r\r## 细节"))
    }

    @Test
    fun `duplicate titles and suffix collisions receive distinct stable anchors`() {
        val source = "# 重复标题\n\n## 重复标题\n\n### 重复标题-1\n\n## 重复标题\n\n# 重复标题-1"
        val entries = parseMarkdownOutline(source)
        assertEquals(listOf("重复标题", "重复标题-1", "重复标题-1-1", "重复标题-2", "重复标题-1-2"), entries.map { it.anchor })
        assertEquals(entries, parseMarkdownOutline(source))
        assertEquals(markdownHeadingSlug("# 重复标题"), entries.first().anchor)
        assertEquals(entries.size, entries.map { it.anchor }.toSet().size)
    }

    @Test
    fun `outline labels show inline text while anchors retain the established slug`() {
        val source = "## **实现** `demo` [API](https://example.com/docs) ##\n\n# 🚀\n\n# 🚀"
        val entries = parseMarkdownOutline(source)
        assertEquals("实现 demo API", entries.first().title)
        assertEquals(markdownHeadingSlug(source.lineSequence().first()), entries.first().anchor)
        assertEquals(listOf("section", "section-1"), entries.drop(1).map { it.anchor })
        assertTrue(parseMarkdownOutline("正文\n\n`# 行内代码`").isEmpty())
    }

    @Test
    fun `navigation can repeat the same anchor and stale completion cannot consume a new request`() {
        val state = MarkdownOutlineState(parseMarkdownOutline("# 标题"))
        state.navigateTo(state.entries.single())
        val first = state.navigationRequest!!
        state.navigateTo(state.entries.single())
        val second = state.navigationRequest!!
        assertEquals(first.anchor, second.anchor)
        assertNotEquals(first.sequence, second.sequence)
        state.finishNavigation(first)
        assertEquals(second, state.navigationRequest)
        state.finishNavigation(second)
        assertNull(state.navigationRequest)
    }
}
