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
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.Dispatchers
import org.jetbrains.skia.EncodedImageFormat
import kotlin.test.*

class DeleteRiskFailureUiTest {
    @Test fun `delete inspection retry is visible and respects busy state in both themes and narrow dialogs`() {
        for (dark in listOf(false, true)) for (width in listOf(280, 720)) for (busy in listOf(false, true)) {
            var retries = 0
            ImageComposeScene(width, 200, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface(Modifier.fillMaxSize()) {
                        Box(Modifier.padding(16.dp)) {
                            DeleteRiskFailure("工作区 Git 状态暂时无法读取，请重新检查后再删除。", enabled = !busy) { retries++ }
                        }
                    }
                }
            }.use { scene ->
                repeat(5) { scene.render(System.nanoTime()).close() }
                fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
                val button = scene.semanticsOwners.flatMap { walk(it.rootSemanticsNode) }.single {
                    it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "重新检查" } == true
                }
                val bounds = button.boundsInRoot
                assertTrue(bounds.left >= 0 && bounds.right <= width && bounds.top >= 0 && bounds.bottom <= 200)
                assertEquals(busy, button.config.contains(SemanticsProperties.Disabled))
                if (!busy) {
                    val screenshot = Path.of("build/reports/delete-risk/${if (dark) "dark" else "light"}-$width.png")
                    Files.createDirectories(screenshot.parent)
                    scene.render(System.nanoTime()).use { rendered ->
                        rendered.encodeToData(EncodedImageFormat.PNG)!!.use { Files.write(screenshot, it.bytes) }
                    }
                }
                scene.sendPointerEvent(PointerEventType.Press, bounds.center,
                    buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                scene.sendPointerEvent(PointerEventType.Release, bounds.center,
                    buttons = PointerButtons(), button = PointerButton.Primary)
                repeat(3) { scene.render(System.nanoTime()).close() }
                assertEquals(if (busy) 0 else 1, retries)
            }
        }
    }
}
