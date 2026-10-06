@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.use
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** No clipboard provider or clipboard action is involved in this tab-only interaction fixture. */
class MaterialsDocumentTabsUiTest {
    @Test fun `click pin and close controls preserve selected semantics in both themes`() {
        for (theme in listOf(ThemePreference.LIGHT, ThemePreference.DARK)) {
            val state = state("方案.md", "流程.pdf")
            state.select("方案.md")
            ImageComposeScene(620, 160, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(theme) { Surface { MaterialsDocumentTabs(state) } }
            }.use { scene ->
                scene.await { scene.tab("流程.pdf") != null }
                assertEquals(2, scene.nodes().count { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab })
                assertTrue(scene.tab("方案.md")!!.selected())
                assertFalse(scene.tab("流程.pdf")!!.selected())
                scene.click(scene.tab("流程.pdf")!!)
                scene.await { state.activePath == "流程.pdf" }
                assertTrue(scene.tab("流程.pdf")!!.selected())
                assertEquals("临时预览", scene.tab("流程.pdf")!!.config[SemanticsProperties.StateDescription])
                scene.click(scene.label("固定标签：流程.pdf")!!)
                scene.await { state.temporaryPath == null && scene.tab("流程.pdf")!!.config.getOrNull(SemanticsProperties.Focused) == true }
                assertEquals("已固定", scene.tab("流程.pdf")!!.config[SemanticsProperties.StateDescription])
                scene.click(scene.label("关闭标签：流程.pdf")!!)
                scene.await { state.activePath == "方案.md" }
                assertNull(scene.tab("流程.pdf"))
                scene.capture("${theme.name.lowercase()}-tabs.png")
                scene.click(scene.label("关闭标签：方案.md")!!)
                scene.await { state.tabs.isEmpty() && scene.tab("方案.md") == null }
            }
        }
    }

    @Test fun `keyboard navigation pinning and closing move focus to a surviving tab`() {
        val state = state("a.md", "b.md", "c.md")
        ImageComposeScene(700, 160, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { MaterialsDocumentTabs(state) } }
        }.use { scene ->
            scene.await { scene.tab("b.md") != null }
            scene.tab("b.md")!!.config[SemanticsActions.OnClick].action!!.invoke()
            scene.await { scene.tab("b.md")!!.config.getOrNull(SemanticsProperties.Focused) == true }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.DirectionRight, KeyEventType.KeyDown)))
            scene.await { state.activePath == "c.md" && scene.tab("c.md")!!.config.getOrNull(SemanticsProperties.Focused) == true }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown, isCtrlPressed = true)))
            scene.await { state.temporaryPath == null }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.MoveHome, KeyEventType.KeyDown)))
            scene.await { state.activePath == "a.md" }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.MoveEnd, KeyEventType.KeyDown)))
            scene.await { state.activePath == "c.md" }
            assertFalse(scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true)), "Tabs must not intercept directory copy")
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.W, KeyEventType.KeyDown, isCtrlPressed = true)))
            scene.await { state.activePath == "b.md" && scene.tab("b.md")!!.config.getOrNull(SemanticsProperties.Focused) == true }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Delete, KeyEventType.KeyDown)))
            scene.await { state.activePath == "a.md" }
            assertEquals(listOf("a.md"), state.tabs.map { it.path })
        }
    }

    @Test fun `double click selects and pins a temporary tab`() {
        val state = state("方案.md", "流程.pdf")
        state.select("方案.md")
        ImageComposeScene(620, 160, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { MaterialsDocumentTabs(state) } }
        }.use { scene ->
            scene.await { scene.tab("流程.pdf") != null }
            val tab = scene.tab("流程.pdf")!!
            // Compose rejects a second press below its minimum double-tap interval.
            scene.click(tab, timeMillis = 100)
            scene.click(tab, timeMillis = 220)
            scene.await { state.activePath == "流程.pdf" && state.temporaryPath == null }
            assertEquals(listOf("方案.md", "流程.pdf"), state.pinnedPaths)
        }
    }

    @Test fun `narrow resized tab bars scroll active tabs into view and preserve separate task state`() {
        for (theme in listOf(ThemePreference.LIGHT, ThemePreference.DARK)) {
            val first = state("目录 A/需求资料文档一.md", "目录 A/需求资料文档二.pdf", "目录 A/需求资料文档三.txt", "目录 A/需求资料文档四.json")
            val second = state("另一个任务.md")
            val selectedState = mutableStateOf(first)
            val width = mutableStateOf(590.dp)
            ImageComposeScene(620, 160, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(theme) { Surface { Column { MaterialsDocumentTabs(selectedState.value, modifier = Modifier.width(width.value)) } } }
            }.use { scene ->
                val lastPath = first.activePath!!
                scene.await { scene.tab(lastPath) != null }
                width.value = 270.dp
                scene.await {
                    val tab = scene.tab(lastPath)!!
                    tab.boundsInRoot.width >= 180f && tab.boundsInRoot.left >= 0 && tab.boundsInRoot.right <= 270f && scene.horizontalScrollValue() > 0
                }
                val close = assertNotNull(scene.label("关闭标签：$lastPath"))
                assertTrue(close.boundsInRoot.right <= 270f)
                scene.capture("${theme.name.lowercase()}-narrow-tabs.png")
                first.select(first.tabs.first().path)
                scene.await { scene.horizontalScrollValue() == 0f }
                selectedState.value = second
                scene.await { scene.tab("另一个任务.md") != null && scene.tab(lastPath) == null }
                selectedState.value = first
                scene.await { scene.tab(first.tabs.first().path)?.selected() == true }
                assertEquals(4, first.tabs.size)
                assertEquals("另一个任务.md", second.activePath)
            }
        }
    }

    private fun state(vararg paths: String) = MaterialsDocumentTabsState().apply {
        paths.forEachIndexed { index, path -> openPreview(path); if (index != paths.lastIndex) pin() }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.tab(path: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.Role) == Role.Tab &&
            it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("任务资料标签：$path") == true
    }
    private fun ImageComposeScene.label(description: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(description) == true
    }
    private fun ImageComposeScene.horizontalScrollValue() = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.HorizontalScrollAxisRange) != null
    }?.config?.getOrNull(SemanticsProperties.HorizontalScrollAxisRange)?.value?.invoke() ?: 0f
    private fun SemanticsNode.selected() = config.getOrNull(SemanticsProperties.Selected) == true
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 6_000_000_000L
        do {
            Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode, timeMillis: Long? = null) {
        // Click the label area rather than the nested pin/close controls on a tab.
        val point = if (node.config.getOrNull(SemanticsProperties.Role) == Role.Tab)
            node.boundsInRoot.topLeft + androidx.compose.ui.geometry.Offset(24f, node.boundsInRoot.height / 2f)
        else node.boundsInRoot.center
        val startTime = timeMillis ?: System.nanoTime() / 1_000_000
        sendPointerEvent(PointerEventType.Move, point, timeMillis = startTime)
        sendPointerEvent(PointerEventType.Press, point, timeMillis = startTime + 1,
            buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, timeMillis = startTime + 2,
            buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.capture(name: String) {
        val output = Path.of("build/reports/materials-document-tabs", name)
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
