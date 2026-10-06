@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.Dispatchers
import org.jetbrains.skia.EncodedImageFormat
import kotlin.test.*

class TagHistoryToolbarUiTest {
    @Test fun `filter refresh and selection actions fit both widths and keep their enabled behavior`() {
        for (dark in listOf(false, true)) for (width in listOf(320, 960)) {
            val query = mutableStateOf("")
            val problems = mutableStateOf(false)
            val selected = mutableStateOf(2)
            val busy = mutableStateOf(false)
            val refreshing = mutableStateOf(false)
            var refreshes = 0
            var deletes = 0
            var selects = 0
            ImageComposeScene(width, 420, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface(Modifier.fillMaxSize()) { Box(Modifier.padding(16.dp)) {
                        TagHistoryToolbar(query.value, { query.value = it }, problems.value,
                            { problems.value = !problems.value }, 12345, true, refreshing.value,
                            { refreshes++; refreshing.value = true }, selected.value, true, busy.value,
                            { selects++; selected.value = 3 }, { selected.value = 0 }, { deletes++ })
                    } }
                }
            }.use { scene ->
                fun settle() { repeat(5) { scene.render(System.nanoTime()).close() } }
                fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
                fun nodes() = scene.semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
                fun button(label: String) = nodes().first {
                    it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == label } == true
                }
                fun click(label: String) {
                    val point = button(label).boundsInRoot.center
                    scene.sendPointerEvent(PointerEventType.Move, point)
                    scene.sendPointerEvent(PointerEventType.Press, point,
                        buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                    scene.sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
                    settle()
                }
                settle()
                for (node in nodes().filter { it.config.getOrNull(SemanticsActions.OnClick) != null ||
                    it.config.getOrNull(SemanticsActions.SetText) != null }) {
                    val bounds = node.boundsInRoot
                    assertTrue(bounds.width > 0 && bounds.height > 0 && bounds.left >= 0 &&
                        bounds.right <= width && bounds.bottom <= 420, "Clipped Tag toolbar action at $width: $bounds")
                }
                val screenshot = Path.of("build/reports/tag-toolbar/${if (dark) "dark" else "light"}-$width.png")
                Files.createDirectories(screenshot.parent)
                scene.render(System.nanoTime()).use { rendered ->
                    rendered.encodeToData(EncodedImageFormat.PNG)!!.use { Files.write(screenshot, it.bytes) }
                }
                nodes().first { it.config.getOrNull(SemanticsActions.SetText) != null }
                    .config[SemanticsActions.SetText].action!!(AnnotatedString("支付优化"))
                settle()
                assertEquals("支付优化", query.value)
                click("仅问题 (12345)")
                assertTrue(problems.value)
                click("刷新 Genbu")
                assertEquals(1, refreshes)
                assertTrue(button("刷新中…").config.contains(SemanticsProperties.Disabled))
                click("刷新中…")
                assertEquals(1, refreshes)
                click("全选")
                assertEquals(1, selects)
                click("删除所选 (3)")
                assertEquals(1, deletes)
                click("全不选")
                assertEquals(0, selected.value)
                assertTrue(button("删除所选 (0)").config.contains(SemanticsProperties.Disabled))
                click("删除所选 (0)")
                assertEquals(1, deletes)
                selected.value = 2
                busy.value = true
                settle()
                for (label in listOf("全选", "全不选", "删除所选 (2)")) {
                    assertTrue(button(label).config.contains(SemanticsProperties.Disabled))
                    click(label)
                }
                assertEquals(1, deletes)
                assertEquals(1, selects)
                assertEquals(2, selected.value)
            }
        }
    }
}
