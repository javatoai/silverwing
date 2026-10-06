@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.selection.LocalTextSelectionColors
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.text.selection.TextSelectionColors
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.use
import com.mikepenz.markdown.compose.extendedspans.drawBehind
import org.jetbrains.skia.Bitmap
import kotlin.math.roundToInt
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class MarkdownSelectionTest {
    @Test
    fun `inline code retains its background and shows selection across wrapped lines in both themes`() {
        for (dark in listOf(false, true)) {
            for (width in listOf(500, 190)) {
                val surface = if (dark) Color(0xFF171A21) else Color.White
                val codeBackground = if (dark) Color(0xFF343944) else Color(0xFFE8EBF2)
                val primary = if (dark) Color(0xFFAFC5FF) else Color(0xFF3565D4)
                val selection = primary.copy(alpha = 0.22f)
                val code = "  code segment  "
                val content = buildAnnotatedString {
                    append("before ")
                    pushStyle(SpanStyle(background = codeBackground, fontFamily = FontFamily.Monospace))
                    append(code)
                    pop()
                    append(" after")
                }
                val codeStart = content.text.indexOf(code)
                var layout: TextLayoutResult? = null
                var origin = Offset.Zero
                ImageComposeScene(width = width, height = 240) {
                    CompositionLocalProvider(
                        LocalTextSelectionColors provides TextSelectionColors(primary, selection),
                    ) {
                        Box(Modifier.fillMaxSize().background(surface).padding(16.dp)) {
                            SelectionContainer {
                                val spans = remember { markdownSelectionSpans() }
                                val extended = remember(content) { spans.extend(content) }
                                BasicText(
                                    text = extended,
                                    modifier = Modifier.drawBehind(spans).onGloballyPositioned {
                                        origin = it.positionInRoot()
                                    },
                                    style = TextStyle(
                                        color = if (dark) Color.White else Color.Black,
                                        fontSize = 24.sp,
                                        lineHeight = 32.sp,
                                    ),
                                    onTextLayout = { layout = it; spans.onTextLayout(it) },
                                )
                            }
                        }
                    }
                }.use { scene ->
                    // 使用离屏场景真实拖选和检查像素，不操作用户正在使用的窗口。
                    repeat(3) { scene.render().close() }
                    val textLayout = assertNotNull(layout)
                    if (width == 190) assertTrue(textLayout.lineCount > 1)
                    val codeOffsets = listOf(codeStart + 2, codeStart + code.length - 3)
                    fun sample(offset: Int): Offset = origin + textLayout.getBoundingBox(offset).let {
                        Offset(it.center.x, it.top + 1)
                    }
                    fun colors(): List<Int> = scene.render().use { image ->
                        Bitmap.makeFromImage(image).use { bitmap ->
                            codeOffsets.map { sample(it).let { point -> bitmap.getColor(point.x.roundToInt(), point.y.roundToInt()) } }
                        }
                    }
                    val label = "dark=" + dark + ", width=" + width
                    assertEquals(List(2) { codeBackground.toArgb() }, colors(), "Unselected code background: " + label)

                    val start = origin + textLayout.getBoundingBox(0).let { Offset(it.left + 1, it.center.y) }
                    val end = origin + textLayout.getBoundingBox(content.length - 1).let { Offset(it.right - 1, it.center.y) }
                    scene.sendPointerEvent(PointerEventType.Move, start, timeMillis = 1)
                    scene.sendPointerEvent(PointerEventType.Press, start, timeMillis = 2,
                        buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                    scene.sendPointerEvent(PointerEventType.Move, end, timeMillis = 100,
                        buttons = PointerButtons(isPrimaryPressed = true))
                    scene.sendPointerEvent(PointerEventType.Release, end, timeMillis = 110,
                        buttons = PointerButtons(), button = PointerButton.Primary)
                    repeat(3) { scene.render().close() }
                    val expected = selection.compositeOver(codeBackground).toArgb()
                    colors().forEach { actual ->
                        // Skia 的颜色混合与浮点运算可能相差一个通道刻度。
                        for (shift in listOf(0, 8, 16, 24)) {
                            val difference = ((expected ushr shift) and 255) - ((actual ushr shift) and 255)
                            assertTrue(kotlin.math.abs(difference) <= 1, "Selected code background: " + label)
                        }
                    }
                }
            }
        }
    }

    @Test
    fun `moving code background behind selection preserves text styles and clickable links`() {
        val link = LinkAnnotation.Url("https://example.com/docs")
        val source = buildAnnotatedString {
            append("Read ")
            pushStyle(SpanStyle(background = Color.LightGray, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Bold))
            pushLink(link)
            append("docs")
            pop()
            pop()
            append(" now")
        }
        val extended = markdownSelectionSpans().extend(source)
        assertEquals(source.text, extended.text)
        assertEquals(source.getLinkAnnotations(0, source.length), extended.getLinkAnnotations(0, extended.length))
        assertEquals(FontFamily.Monospace, extended.spanStyles.single().item.fontFamily)
        assertEquals(FontWeight.Bold, extended.spanStyles.single().item.fontWeight)
    }
}
