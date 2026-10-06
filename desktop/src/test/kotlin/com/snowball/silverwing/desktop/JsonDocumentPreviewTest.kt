package com.snowball.silverwing.desktop

import androidx.compose.ui.graphics.Color
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

class JsonDocumentPreviewTest {
    @Test fun `JSON highlighting preserves escaped strings and classifies only whole tokens`() {
        val source = """{"key": "value\"quoted\\tail", "flag": true, "count": -12.5e+2, "empty": null}"""
        val pretty = assertNotNull(prettyJsonOrNull(source))
        val result = highlightedJson(pretty, Color.Blue, Color.Green, Color.Red)
        assertEquals(pretty, result.text)
        assertEquals(listOf(Color.Blue, Color.Green, Color.Blue, Color.Red, Color.Blue, Color.Red, Color.Blue, Color.Red),
            result.spanStyles.map { it.item.color })
    }

    @Test fun `JSON highlighting handles long strings without regex stack overflow`() {
        val source = "\"" + "x".repeat(200_000) + "\""
        val result = highlightedJson(source, Color.Blue, Color.Green, Color.Red)
        assertEquals(source, result.text)
        assertEquals(1, result.spanStyles.size)
        assertEquals(Color.Green, result.spanStyles.single().item.color)
    }
}
