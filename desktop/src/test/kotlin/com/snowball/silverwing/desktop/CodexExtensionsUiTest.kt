@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Surface
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import kotlin.test.*

class CodexExtensionsUiTest {
    @Test fun `preview failure offers a bounded retry action in both themes and narrow panels`() {
        for (dark in listOf(false, true)) for (width in listOf(280, 720)) {
            var retries = 0
            ImageComposeScene(width, 240, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface(Modifier.fillMaxSize()) {
                        Box(Modifier.padding(16.dp)) { ExtensionPreviewFailure("文档读取失败，请重试") { retries++ } }
                    }
                }
            }.use { scene ->
                repeat(5) { scene.render(System.nanoTime()).close() }
                fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
                val button = scene.semanticsOwners.flatMap { walk(it.rootSemanticsNode) }.single {
                    it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "重新读取" } == true
                }
                val bounds = button.boundsInRoot
                assertTrue(bounds.left >= 0 && bounds.right <= width)
                scene.sendPointerEvent(PointerEventType.Press, bounds.center,
                    buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                scene.sendPointerEvent(PointerEventType.Release, bounds.center,
                    buttons = PointerButtons(), button = PointerButton.Primary)
                repeat(3) { scene.render(System.nanoTime()).close() }
                assertEquals(1, retries)
            }
        }
    }
}
