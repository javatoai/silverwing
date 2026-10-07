@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.*
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class MarkdownFileTabsPreviewUiTest {
    @TempDir lateinit var directory: Path
    private val rules = MarkdownPreviewFile("agent/TASK-RULES.md", "# 规则标题\n\n" +
        (1..80).joinToString("\n\n") { "规则 $it：保留原始 Markdown 和每个文件的阅读位置。" })
    private val router = MarkdownPreviewFile("AGENTS.md", "# 路由标题\n\n`原始内容`。")

    @Test fun `caller state restores each file source mode and scroll while copy keeps exact selected source`() {
        val state = MarkdownFilesReadingState(rules.path)
        val visible = mutableStateOf(true)
        val copies = mutableListOf<MarkdownPreviewFile>()
        ImageComposeScene(600, 620, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface {
                if (visible.value) MarkdownFileTabsPreview(listOf(router, rules), Modifier.fillMaxSize(),
                    initialPath = rules.path, state = state, onCopySource = { copies += it }) else Text("其他页面")
            } }
        }.use { scene ->
            scene.await { scene.hasText("规则标题") }
            scene.activateLabel("查看 Markdown 源码")
            scene.await { scene.verticalScroll() != null && scene.label("查看 Markdown 预览") != null }
            assertTrue(scene.verticalScroll()!!.config.getOrNull(SemanticsActions.ScrollBy)!!.action!!.invoke(0f, 360f))
            scene.await { (state.readingFor(rules.path).scrollPositions["source-vertical"] ?: 0) >= 350 }
            val saved = state.readingFor(rules.path).scrollPositions["source-vertical"]!!
            scene.activate("AGENTS.md")
            scene.await { scene.hasText("路由标题") }
            assertEquals(MarkdownPreviewMode.RENDERED, state.readingFor(router.path).mode.value)
            scene.activateLabel("复制…")
            scene.await { scene.button("复制Markdown源码") != null }
            scene.activate("复制Markdown源码")
            scene.await { copies.size == 1 }
            assertEquals(router, copies.single())
            scene.activate("TASK-RULES.md")
            scene.await { scene.label("查看 Markdown 预览") != null && scene.scrollValue() >= saved - 2 }
            visible.value = false; scene.frames()
            visible.value = true
            scene.await { scene.label("查看 Markdown 预览") != null && scene.scrollValue() >= saved - 2 }
            assertEquals(rules.path, state.selectedPath)
            assertEquals(MarkdownPreviewMode.SOURCE, state.readingFor(rules.path).mode.value)
        }
    }

    @Test fun `existing callers without session state retain selected file across content refresh`() {
        val files = mutableStateOf(listOf(router, rules))
        val copies = mutableListOf<MarkdownPreviewFile>()
        ImageComposeScene(600, 620, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.DARK) { Surface {
                MarkdownFileTabsPreview(files.value, Modifier.fillMaxSize(), onCopySource = { copies += it })
            } }
        }.use { scene ->
            scene.await { scene.hasText("路由标题") }
            scene.activate("TASK-RULES.md")
            scene.await { scene.hasText("规则标题") }
            val refreshed = rules.copy(content = "# 刷新后的说明\n\n未变换的原文。")
            files.value = listOf(router, refreshed)
            scene.await { scene.hasText("刷新后的说明") }
            scene.activateLabel("复制…")
            scene.await { scene.button("复制Markdown源码") != null }
            scene.activate("复制Markdown源码")
            scene.await { copies.size == 1 }
            assertEquals(refreshed, copies.single())
        }
    }

    @Test fun `local file actions copy the selected path including empty files while missing previews remain disabled`() {
        val emptyRules = rules.copy(content = "")
        val files = mutableStateOf(listOf(router, emptyRules))
        val paths = mutableListOf<Path>()
        val copiedFiles = mutableListOf<Path>()
        for (file in files.value) {
            val path = directory.resolve(file.path)
            Files.createDirectories(path.parent)
            Files.writeString(path, file.content)
        }
        val state = MarkdownFilesReadingState(router.path)
        ImageComposeScene(600, 620, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface {
                MarkdownFileTabsPreview(files.value, Modifier.fillMaxSize(), state = state,
                    onCopySource = {}, sourcePathForFile = { directory.resolve(it.path) },
                    onCopyPath = { paths.add(it) }, onCopyFile = { copiedFiles.add(it) }, fileCopyLabel = "复制文件")
            } }
        }.use { scene ->
            scene.await { scene.hasText("路由标题") }
            for (file in listOf(router, emptyRules)) {
                scene.activate(file.fileName)
                scene.await { state.selectedPath == file.path }
                scene.activateLabel("复制…")
                scene.await { scene.button("复制文件路径") != null }
                assertFalse(scene.button("复制文件路径")!!.config.contains(SemanticsProperties.Disabled))
                assertFalse(scene.button("复制文件")!!.config.contains(SemanticsProperties.Disabled))
                if (file.content.isEmpty()) assertTrue(scene.button("复制Markdown源码")!!.config.contains(SemanticsProperties.Disabled))
                scene.activate("复制文件路径")
                scene.await { paths.lastOrNull() == directory.resolve(file.path) }
                scene.activateLabel("复制…")
                scene.await { scene.button("复制文件") != null }
                scene.activate("复制文件")
                scene.await { copiedFiles.lastOrNull() == directory.resolve(file.path) }
            }
            assertEquals(listOf(directory.resolve(router.path), directory.resolve(emptyRules.path)), paths)
            assertEquals(paths, copiedFiles)
            Files.delete(directory.resolve(emptyRules.path))
            files.value = listOf(router, emptyRules.copy(content = "draft without a saved file"))
            scene.await { scene.hasText("draft without a saved file") }
            scene.activateLabel("复制…")
            scene.await { scene.button("复制文件路径（无本地文件）") != null }
            assertTrue(scene.button("复制文件路径（无本地文件）")!!.config.contains(SemanticsProperties.Disabled))
            assertTrue(scene.button("复制文件（无本地文件）")!!.config.contains(SemanticsProperties.Disabled))
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.hasText(value: String) = nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { it.text == value } == true }
    private fun ImageComposeScene.button(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { it.text == value } == true
    }
    private fun ImageComposeScene.label(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true
    }
    private fun ImageComposeScene.activate(value: String) { assertTrue(button(value)!!.config.getOrNull(SemanticsActions.OnClick)!!.action!!.invoke()) }
    private fun ImageComposeScene.activateLabel(value: String) { assertTrue(label(value)!!.config.getOrNull(SemanticsActions.OnClick)!!.action!!.invoke()) }
    private fun ImageComposeScene.verticalScroll() = nodes().firstOrNull {
        (it.config.getOrNull(SemanticsProperties.VerticalScrollAxisRange)?.maxValue?.invoke() ?: 0f) > 400 &&
            it.config.getOrNull(SemanticsActions.ScrollBy) != null
    }
    private fun ImageComposeScene.scrollValue() = verticalScroll()?.config?.getOrNull(SemanticsProperties.VerticalScrollAxisRange)?.value?.invoke() ?: 0f
    private fun ImageComposeScene.frames() {
        Snapshot.sendApplyNotifications()
        repeat(4) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val deadline = System.nanoTime() + 8_000_000_000L
        do { frames(); if (condition()) return; Thread.sleep(5) } while (System.nanoTime() < deadline)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
}
