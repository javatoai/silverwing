@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.PointerKeyboardModifiers
import androidx.compose.ui.platform.ClipboardManager
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class PdfInteractionRegressionTest {
    private fun page(text: String) = PdfPageText(text, text.indices.map { offset ->
        PdfGlyph(offset..offset, Rect(0.1f + offset * 0.1f, 0.2f, 0.2f + offset * 0.1f, 0.4f))
    })

    @Test fun `cross page drag and reverse Shift extension preserve glyphs and copy through both actions`() {
        val index = PdfTextIndex(listOf(page("ABC"), page("DEF")), emptyList())
        val selection = PdfTextSelection()
        var copied: AnnotatedString? = null
        val clipboard = object : ClipboardManager {
            override fun getText() = copied
            override fun setText(annotatedString: AnnotatedString) { copied = annotatedString }
        }
        for (theme in listOf(ThemePreference.LIGHT, ThemePreference.DARK)) {
            selection.clear()
            ImageComposeScene(400, 500, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(theme) { CompositionLocalProvider(LocalClipboardManager provides clipboard) {
                    Surface { Column {
                        PdfTextOverlay(index, 0, selection, Modifier.width(400.dp).height(200.dp))
                        PdfTextOverlay(index, 1, selection, Modifier.width(400.dp).height(200.dp))
                    } }
                } }
            }.use { scene ->
                fun frames() { Snapshot.sendApplyNotifications(); repeat(3) { scene.render(System.nanoTime()).close() } }
                frames()
                val start = Offset(100f, 60f) // B on page 1
                val end = Offset(100f, 260f) // E on page 2
                scene.sendPointerEvent(PointerEventType.Press, start,
                    buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                scene.sendPointerEvent(PointerEventType.Move, end, buttons = PointerButtons(isPrimaryPressed = true))
                scene.sendPointerEvent(PointerEventType.Release, end, button = PointerButton.Primary)
                frames()
                assertEquals("BC\nDE", selection.text(index))
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true)))
                assertEquals("BC\nDE", copied?.text)
                val back = Offset(60f, 60f) // Shift extends from B backwards to A
                scene.sendPointerEvent(PointerEventType.Press, back, buttons = PointerButtons(isPrimaryPressed = true),
                    keyboardModifiers = PointerKeyboardModifiers(isShiftPressed = true), button = PointerButton.Primary)
                scene.sendPointerEvent(PointerEventType.Release, back, button = PointerButton.Primary)
                frames()
                assertEquals("AB", selection.text(index))
                scene.sendPointerEvent(PointerEventType.Press, back, buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
                scene.sendPointerEvent(PointerEventType.Release, back, button = PointerButton.Secondary)
                frames()
                fun descend(node: androidx.compose.ui.semantics.SemanticsNode): List<androidx.compose.ui.semantics.SemanticsNode> =
                    listOf(node) + node.children.flatMap(::descend)
                val copy = scene.semanticsOwners.flatMap { descend(it.rootSemanticsNode) }.first {
                    it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == "复制" } == true &&
                        it.config.getOrNull(SemanticsActions.OnClick) != null
                }
                copy.config[SemanticsActions.OnClick].action!!.invoke()
                frames()
                assertEquals("AB", copied?.text)
            }
        }
    }

    @Test fun `reverse ligature selection keeps the entire anchor and endpoint glyphs`() {
        val index = PdfTextIndex(listOf(PdfPageText("office", emptyList())), emptyList())
        val selection = PdfTextSelection()
        selection.begin(0, PdfGlyph(2..3, Rect.Zero), extending = false)
        selection.begin(0, PdfGlyph(0..1, Rect.Zero), extending = true)
        assertEquals("offi", selection.text(index))
        selection.extend(0, PdfGlyph(4..5, Rect.Zero))
        assertEquals("fice", selection.text(index))
    }

    @Test fun `glyph mapping preserves compatibility symbols ligatures and bidirectional offsets`() {
        assertEquals(emptyList(), pdfWordGlyphRanges("", emptyList()))
        assertEquals(listOf(0..0, 1..1, 2..3), pdfWordGlyphRanges("µＡfi", listOf("µ", "Ａ", "\uFB01")))
        assertEquals(listOf(2..2, 1..1, 0..0), pdfWordGlyphRanges("גבא", listOf("א", "ב", "ג")))
        assertEquals(listOf(0..0, 1..1, 2..2), pdfWordGlyphRanges("ABC", listOf("A", "B", "C")))
    }

    @Test fun `horizontal find brings a zoomed match into view and leaves an already visible match in place`() {
        assertEquals(666, pdfFindHorizontalOffset(800f, 950f, 0, 300f))
        assertEquals(24, pdfFindHorizontalOffset(40f, 80f, 666, 300f))
        assertEquals(24, pdfFindHorizontalOffset(40f, 80f, 24, 300f))
    }

    @Test fun `highlight intersection handles ligatures between sorted hits and empty results`() {
        val hits = listOf(1..3, 10..12, 20..22)
        assertTrue(pdfRangeIntersectsHits(2..5, hits))
        assertTrue(pdfRangeIntersectsHits(11..11, hits))
        assertFalse(pdfRangeIntersectsHits(4..9, hits))
        assertFalse(pdfRangeIntersectsHits(30..40, hits))
        assertFalse(pdfRangeIntersectsHits(0..5, emptyList()))
    }
}
