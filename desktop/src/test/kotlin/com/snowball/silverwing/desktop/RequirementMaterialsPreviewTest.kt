package com.snowball.silverwing.desktop

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue
import org.junit.jupiter.api.io.TempDir

class RequirementMaterialsPreviewTest {
    @TempDir lateinit var temporaryDirectory: Path

    @Test
    fun `pretty JSON and invalid fallback`() {
        assertTrue(prettyJsonOrNull("{\"name\":\"a\",\"items\":[1,2]}")!!.contains("\n"))
        assertNull(prettyJsonOrNull("{broken"))
    }

    @Test
    fun `CSV parser and selected fragment preserve commas quotes and line breaks`() {
        val rows = parseCsvOrNull("name,value\r\nalpha,\"a,b\"\r\nbeta,\"line 1\nline 2\"\r\ngamma,\"say \"\"hi\"\"\"")!!
        assertEquals("line 1\nline 2", rows[2][1])
        assertEquals("\"a,b\"\r\n\"line 1\nline 2\"\r\n\"say \"\"hi\"\"\"", csvFragment(rows, 1, 3, 1, 1))
        assertNull(parseCsvOrNull("a,\"unterminated"))
    }

    @Test
    fun `Markdown table fragment carries selected headers and alignment`() {
        val table = parsePreviewMarkdownTable("| Name | Time | Value |\n| :--- | ---: | :---: |\n| one | 10 | x |\n| two | 20 | y |")!!
        assertEquals("| Time | Value |\n| ---: | :---: |\n| 10 | x |\n| 20 | y |", markdownTableFragment(table, 1, 2, 1, 2))
    }

    @Test
    fun `frontmatter renders simple properties and leaves complex blocks raw`() {
        val simple = parseFrontMatterProperties(listOf("tags:", "  - 类型/慢SQL分析", "  - 环境/prod", "created_at: \"2026-09-15\""))!!
        assertEquals(listOf("类型/慢SQL分析", "环境/prod"), simple[0].values)
        assertEquals("2026-09-15", simple[1].values.single())
        assertNull(parseFrontMatterProperties(listOf("details:", "  nested: value")))
    }

    @Test
    fun `mixed file and folder selection supports Shift ranges and Ctrl toggles`() {
        val visible = listOf("directory:design", "file:design/a.md", "file:design/b.json", "directory:review")
        val range = selectMaterialEntryKeys(emptySet(), visible, visible[0], visible[2], ctrl = false, shift = true)
        assertEquals(visible.take(3).toSet(), range)
        assertEquals(setOf(visible[0], visible[2]), selectMaterialEntryKeys(range, visible, visible[0], visible[1], ctrl = true, shift = false))
    }

    @Test
    fun `default expansion covers two folder levels only when enabled`() {
        val directories = listOf("设计", "设计/SQL", "设计/SQL/归档", "测试")
        assertEquals(emptySet(), initialExpandedMaterialsDirectories(directories, false))
        assertEquals(setOf("设计", "设计/SQL", "测试"), initialExpandedMaterialsDirectories(directories, true))
    }

    @Test
    fun `relative links and images stay within the materials root`() {
        val root = Files.createDirectory(temporaryDirectory.resolve("root"))
        val docs = Files.createDirectory(root.resolve("docs"))
        val current = Files.writeString(docs.resolve("current.md"), "current")
        val target = Files.writeString(root.resolve("target.md"), "# Section")
        val image = Files.write(root.resolve("image.png"), byteArrayOf(1, 2, 3))
        val outside = Files.writeString(temporaryDirectory.resolve("outside.md"), "outside")
        assertEquals(target to "section", resolveLocalMarkdownDestination(current, root, "../target.md#section"))
        assertEquals(current to "current", resolveLocalMarkdownDestination(current, root, "#current"))
        assertNull(resolveLocalMarkdownDestination(current, root, "../../outside.md"))
        assertTrue(markdownWithRelativeImagePaths("![](../image.png)", current, root).contains(image.toUri().toASCIIString()))
        assertTrue(markdownWithRelativeImagePaths("![](../../outside.png)", current, root).contains("about:blank"))
    }
}
