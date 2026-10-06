@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.asAwtTransferable
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.use
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.Test
import java.awt.datatransfer.DataFlavor
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class AiContextCopyUiTest {
    @Test fun `real dialog preserves the outer modern clipboard and copies the inspected edited payload`() {
        for (dark in listOf(false, true)) for (width in listOf(600, 1100)) {
            val io = StandardTestDispatcher()
            Dispatchers.setMain(io)
            try {
                val clipboard = RecordingClipboard()
                val visible = mutableStateOf(true)
                val copied = mutableListOf<String>()
                val task = TaskManifest(folderName = "弹窗上下文", taskDirectoryName = "弹窗上下文", featureBranch = "feature/dialog",
                    requirementLink = "https://project.feishu.cn/obt/userstory/detail/123", createdAt = "now", updatedAt = "now", services = emptyList())
                ImageComposeScene(width, 700, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                        // This provider is intentionally outside Dialog. The desktop Dialog
                        // used to replace it with the real platform clipboard.
                        CompositionLocalProvider(LocalClipboard provides clipboard) {
                            if (visible.value) AiContextCopyDialog(task, onDismiss = { visible.value = false },
                                requirementTitle = "复制前检查", requirementBody = "初始完整需求正文", ioDispatcher = io,
                                onCopied = { copied += it })
                        }
                    }
                }.use { scene ->
                    scene.await(io) { scene.hasText("复制 AI 上下文") && scene.editor() != null }
                    assertEquals(0, clipboard.writes)
                    assertTrue(scene.editor()!!.config[SemanticsProperties.EditableText].text.contains("初始完整需求正文"))
                    val edited = "# 用户检查后的弹窗内容\n\n只复制明确保留的需求段落。"
                    assertTrue(scene.editor()!!.config[SemanticsActions.SetText].action!!(AnnotatedString(edited)))
                    scene.await(io) { scene.button("恢复勾选内容") != null }
                    scene.capture("dialog-${if (dark) "dark" else "light"}-$width.png")
                    assertTrue(scene.button("复制到剪贴板")!!.config[SemanticsActions.OnClick].action!!())
                    scene.await(io) { !visible.value && clipboard.writes == 1 }
                    assertEquals(edited, clipboard.text)
                    assertEquals(listOf(edited), copied)
                }
            } finally { io.scheduler.runCurrent(); Dispatchers.resetMain() }
        }
    }

    @Test fun `selection and edited preview copy exactly once through modern injected clipboard in narrow and wide themes`() {
        for (dark in listOf(false, true)) for (width in listOf(320, 900)) {
            val io = StandardTestDispatcher()
            Dispatchers.setMain(io)
            try {
                val clipboard = RecordingClipboard()
                var closed = 0
                val copied = mutableListOf<String>()
                val document = AiContextCopyDocument(listOf(
                    AiContextCopySection("task", "当前任务", "任务：示例任务"),
                    AiContextCopySection("body", "需求正文", "正文需要用户检查"),
                    AiContextCopySection("material", "资料：notes.sql", "select id from orders;"),
                ), listOf("report.docx：该文件类型不支持文字提取。"))
                ImageComposeScene(width, 560, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                        CompositionLocalProvider(LocalClipboard provides clipboard) {
                            Surface { AiContextCopyEditor(document, { closed++ }, { copied += it }, Modifier.fillMaxSize()) }
                        }
                    }
                }.use { scene ->
                    scene.await(io) { scene.editor() != null }
                    assertEquals(0, clipboard.writes, "Opening a preview must not copy anything")
                    val checkbox = scene.nodes().first {
                        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("包含需求正文") == true
                    }
                    assertTrue(checkbox.config[SemanticsActions.OnClick].action!!())
                    scene.await(io) { !scene.editor()!!.config[SemanticsProperties.EditableText].text.contains("正文需要用户检查") }
                    val editorBounds = scene.editor()!!.boundsInRoot
                    assertTrue(editorBounds.width <= width)
                    assertTrue(editorBounds.height >= 100f)
                    val edited = "# 用户删改后的上下文\n\n只复制检查过的内容。"
                    assertTrue(scene.editor()!!.config[SemanticsActions.SetText].action!!(AnnotatedString(edited)))
                    scene.await(io) { scene.button("恢复勾选内容") != null }
                    assertEquals(edited, scene.editor()!!.config[SemanticsProperties.EditableText].text)
                    scene.capture("${if (dark) "dark" else "light"}-$width.png")
                    assertTrue(scene.button("复制到剪贴板")!!.config[SemanticsActions.OnClick].action!!())
                    scene.await(io) { clipboard.writes == 1 && closed == 1 }
                    assertEquals(edited, clipboard.text)
                    assertEquals(listOf(edited), copied)
                }
            } finally { io.scheduler.runCurrent(); Dispatchers.resetMain() }
        }
    }

    @Test fun `empty selection disables copy and clipboard failure retains the edited preview for retry`() {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try {
            val clipboard = RecordingClipboard().apply { failure = "剪贴板被占用" }
            var closed = false
            ImageComposeScene(600, 520, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) {
                    CompositionLocalProvider(LocalClipboard provides clipboard) { Surface {
                        AiContextCopyEditor(AiContextCopyDocument(listOf(AiContextCopySection("task", "当前任务", "task context"))),
                            { closed = true }, modifier = Modifier.fillMaxSize())
                    } }
                }
            }.use { scene ->
                scene.await(io) { scene.editor() != null }
                scene.nodes().first { it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("包含当前任务") == true }
                    .config[SemanticsActions.OnClick].action!!()
                scene.await(io) { scene.editor()!!.config[SemanticsProperties.EditableText].text.isBlank() }
                assertTrue(scene.button("复制到剪贴板")!!.config.contains(SemanticsProperties.Disabled))
                val edited = "edited after empty selection"
                scene.editor()!!.config[SemanticsActions.SetText].action!!(AnnotatedString(edited))
                scene.await(io) { !scene.button("复制到剪贴板")!!.config.contains(SemanticsProperties.Disabled) }
                scene.button("复制到剪贴板")!!.config[SemanticsActions.OnClick].action!!()
                scene.await(io) { scene.hasText("复制失败：剪贴板被占用。请重试。") }
                assertFalse(closed)
                assertEquals(edited, scene.editor()!!.config[SemanticsProperties.EditableText].text)
                clipboard.failure = null
                scene.button("复制到剪贴板")!!.config[SemanticsActions.OnClick].action!!()
                scene.await(io) { closed }
                assertEquals(edited, clipboard.text)
                assertEquals(1, clipboard.writes)
            }
        } finally { io.scheduler.runCurrent(); Dispatchers.resetMain() }
    }

    private class RecordingClipboard : Clipboard {
        override val nativeClipboard = java.awt.datatransfer.Clipboard("ai-context-test")
        private var entry: ClipEntry? = null
        var text: String? = null
        var writes = 0
        var failure: String? = null
        override suspend fun getClipEntry(): ClipEntry? = entry
        override suspend fun setClipEntry(clipEntry: ClipEntry?) {
            failure?.let { error(it) }
            entry = clipEntry
            text = clipEntry?.asAwtTransferable?.getTransferData(DataFlavor.stringFlavor) as? String
            writes++
        }
    }
    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.editor() = nodes().firstOrNull { it.config.getOrNull(SemanticsActions.SetText) != null }
    private fun ImageComposeScene.button(text: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true
    }
    private fun ImageComposeScene.hasText(text: String) = nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true }
    private fun ImageComposeScene.await(io: kotlinx.coroutines.test.TestDispatcher, condition: () -> Boolean) {
        repeat(80) {
            io.scheduler.runCurrent(); Snapshot.sendApplyNotifications()
            repeat(4) { render(System.nanoTime()).close() }
            if (condition()) return
        }
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.capture(name: String) {
        val output = Path.of("build/reports/ai-context-copy", name)
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
