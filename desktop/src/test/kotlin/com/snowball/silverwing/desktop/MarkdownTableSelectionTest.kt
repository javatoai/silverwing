package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

class MarkdownTableSelectionTest {
    @Test fun `copy retains markdown escapes and a final backslash`() {
        val source = """| Path | Literal |
| --- | --- |
| C:\tmp\name | \*literal\* |
| escaped\|pipe | end\ |"""
        val table = assertNotNull(parsePreviewMarkdownTable(source))
        assertEquals(source, markdownTableFragment(table, 1, 2, 0, 1))
    }

    @Test fun `escaped trailing pipe remains part of a borderless table cell`() {
        val table = assertNotNull(parsePreviewMarkdownTable("Name | Value\n--- | ---\nfirst | last\\|"))
        assertEquals(listOf("first", "last|"), table.rows[1])
        assertEquals("| Name | Value |\n| --- | --- |\n| first | last\\| |", markdownTableFragment(table, 1, 1, 0, 1))
    }

    @Test fun `table rows follow the headers column count`() {
        val table = assertNotNull(parsePreviewMarkdownTable("| A | B |\n| --- | --- |\n| one |\n| two | three | extra |"))
        assertEquals(listOf(listOf("A", "B"), listOf("one", ""), listOf("two", "three")), table.rows)
    }
}
