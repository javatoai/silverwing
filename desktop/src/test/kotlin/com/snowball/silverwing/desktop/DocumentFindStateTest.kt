@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DocumentFindStateTest {
    @Test fun `find has one visible global bound independent of block size`() {
        val find = DocumentFindState()
        find.blocks["source"] = FindBlock("hit ".repeat(2_501), 0) {}
        find.change("hit")
        assertEquals(2_501, find.hits.size)
        assertFalse(find.truncated)
        find.blocks["source"] = FindBlock("hit ".repeat(DOCUMENT_FIND_MATCH_LIMIT), 0) {}
        assertEquals(DOCUMENT_FIND_MATCH_LIMIT, find.hits.size)
        assertFalse(find.truncated)
        find.blocks["tail"] = FindBlock("hit", 1) {}
        assertEquals(DOCUMENT_FIND_MATCH_LIMIT, find.hits.size)
        assertTrue(find.truncated)
        assertEquals(emptyList(), literalMatches("hit", "hit", limit = 0))
    }

    @Test fun `navigation wraps from the displayed index after refresh removes results`() {
        val find = DocumentFindState()
        find.blocks["text"] = FindBlock("x ".repeat(9), 0) {}
        find.change("x")
        find.move(-1)
        assertEquals(8, find.currentIndex)
        find.blocks["text"] = FindBlock("x x", 0) {}
        assertEquals(1, find.currentIndex)
        find.move(1)
        assertEquals(0, find.currentIndex)
    }

    @Test fun `source offsets keep newly inserted blocks in reading order`() {
        val find = DocumentFindState()
        find.blocks["old"] = FindBlock("match later", 30) {}
        find.change("match")
        find.blocks["new"] = FindBlock("match first", 2) {}
        assertEquals(listOf("new", "old"), find.hits.map { it.block })
    }

    @Test fun `enter navigates only in the find input while F3 works throughout the document`() {
        val find = DocumentFindState()
        find.blocks["text"] = FindBlock("x x", 0) {}
        find.show(); find.change("x")
        val enter = KeyEvent(Key.Enter, KeyEventType.KeyDown)
        assertFalse(find.key(enter))
        assertTrue(find.key(enter, fromSearchField = true))
        assertEquals(1, find.currentIndex)
        assertTrue(find.key(KeyEvent(Key.F3, KeyEventType.KeyDown)))
        assertEquals(0, find.currentIndex)
    }
}
