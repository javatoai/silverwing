@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.StandardTestDispatcher
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class RequirementMaterialsFileActionsTest {
    @TempDir lateinit var root: Path
    private val ioDispatcher = StandardTestDispatcher()
    private class FakeOpening : SystemFileOpening {
        override val chooserAvailable = true
        var info = DefaultFileApplication("WPS")
        var gate: CompletableDeferred<FileOpenResult>? = null
        var result: FileOpenResult = FileOpenResult.Submitted
        val calls = mutableListOf<Pair<Path, Boolean>>()
        override suspend fun defaultApplication(path: Path) = info
        override suspend fun open(path: Path): FileOpenResult { calls += path to false; return gate?.await() ?: result }
        override suspend fun chooseApplication(path: Path): FileOpenResult { calls += path to true; return result }
    }
    private fun controller(fake: FakeOpening): DesktopApplication {
        val paths = ApplicationPaths(Files.createDirectory(root.resolve("app-${System.nanoTime()}")))
        val store = ConfigStore(paths).also { it.save(AppConfig()) }
        return DesktopApplication(paths = paths, configStore = store, systemFileOpening = fake,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
    }

    @Test fun `external file actions fit narrow wide and both themes and menu opens selected path`() {
        val path = Files.write(root.resolve("中文 空格 & ' (1)-很长的需求资料文件名称.xlsx"), byteArrayOf(0xff.toByte()))
        val file = RequirementMaterialsMarkdownFile(path.fileName.toString(), 1048576)
        for (dark in listOf(false, true)) for (width in listOf(320, 900)) {
            val fake = FakeOpening()
            controller(fake).use { app ->
                ImageComposeScene(width, 480, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                        Surface { RequirementMaterialsDocumentPane(app, root, file,
                            RequirementMaterialsDocumentState.External(file.relativePath), {}, modifier = Modifier.fillMaxSize(), ioDispatcher = ioDispatcher) }
                    }
                }.use { scene ->
                    scene.await { scene.textButton("用 WPS 打开")?.config?.getOrNull(SemanticsProperties.Disabled) == null && scene.textButton("用 WPS 打开") != null }
                    assertTrue(scene.hasText("请使用外部应用查看"))
                    val button = scene.textButton("用 WPS 打开")!!
                    assertTrue(button.boundsInRoot.left >= 0 && button.boundsInRoot.right <= width)
                    scene.click(button)
                    scene.await { fake.calls.size == 1 && scene.textButton("用 WPS 打开")?.isEnabled() == true }
                    assertEquals(path to false, fake.calls.single())
                    scene.click(scene.labelButton("打开方式"))
                    scene.await { scene.textButton("用其他应用打开…") != null }
                    scene.settleMenus()
                    val output = Path.of("build/reports/material-file-actions/${if (dark) "dark" else "light"}-$width.png")
                    Files.createDirectories(output.parent)
                    scene.render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
                    scene.click(scene.textButton("用其他应用打开…")!!)
                    scene.await { fake.calls.size == 2 && scene.textButton("用 WPS 打开")?.isEnabled() == true }
                    assertEquals(path to true, fake.calls.last())
                    scene.await { scene.semanticsOwners.size == 1 }
                    scene.click(scene.labelButton("复制…"))
                    scene.await { scene.textButton("复制文件") != null }
                    assertFalse(scene.hasText("复制原文"))
                    assertTrue(scene.hasText("复制文件路径"))
                }
            }
        }
    }

    @Test fun `busy blocks duplicate calls failure retries and deleted file never reaches shell`() {
        val path = Files.writeString(root.resolve("方案.docx"), "binary placeholder")
        val file = RequirementMaterialsMarkdownFile("方案.docx", 1)
        val fake = FakeOpening().apply { gate = CompletableDeferred() }
        controller(fake).use { app ->
            ImageComposeScene(500, 240, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) {
                    Surface { RequirementMaterialsFileHeader(app, root, file, null, MarkdownPreviewMode.RENDERED, {}, 0, ioDispatcher = ioDispatcher) }
                }
            }.use { scene ->
                scene.await { scene.textButton("用 WPS 打开")?.isEnabled() == true }
                scene.click(scene.textButton("用 WPS 打开")!!)
                scene.await { fake.calls.size == 1 && scene.textButton("正在打开…") != null }
                assertFalse(scene.textButton("正在打开…")!!.isEnabled())
                scene.click(scene.textButton("正在打开…")!!)
                assertEquals(1, fake.calls.size)
                fake.gate!!.complete(FileOpenResult.Failed(IllegalStateException("open failed")))
                scene.await { scene.textButton("用 WPS 打开")?.isEnabled() == true }
                assertTrue(app.errorMessage?.contains("open failed") == true)
                fake.gate = null
                scene.click(scene.textButton("用 WPS 打开")!!)
                scene.await { fake.calls.size == 2 && app.statusMessage == "已交给系统打开" }
                Files.delete(path)
                scene.click(scene.textButton("用 WPS 打开")!!)
                scene.await { app.errorMessage != null }
                assertEquals(2, fake.calls.size)
            }
        }
    }

    @Test fun `missing association selects application refresh changes label and failed text keeps file copying`() {
        Files.writeString(root.resolve("broken.txt"), "hello")
        val file = RequirementMaterialsMarkdownFile("broken.txt", 5)
        val fake = FakeOpening().apply { info = DefaultFileApplication(associationMissing = true) }
        val refresh = mutableStateOf(0)
        controller(fake).use { app ->
            ImageComposeScene(320, 280, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) {
                    Surface { RequirementMaterialsDocumentPane(app, root, file,
                        RequirementMaterialsDocumentState.Failed("broken.txt", "不是有效的文本"), {}, refreshKey = refresh.value, ioDispatcher = ioDispatcher) }
                }
            }.use { scene ->
                scene.await { scene.textButton("选择应用打开")?.isEnabled() == true }
                scene.click(scene.textButton("选择应用打开")!!)
                scene.await { fake.calls.size == 1 }
                assertTrue(fake.calls.single().second)
                fake.info = DefaultFileApplication("Other App")
                refresh.value++
                scene.await { scene.textButton("用 Other App 打开")?.isEnabled() == true }
                scene.click(scene.labelButton("复制…"))
                scene.await { scene.textButton("复制文件") != null }
                assertTrue(scene.textButton("复制文件")!!.isEnabled())
                assertTrue(scene.textButton("复制文件路径")!!.isEnabled())
                assertFalse(scene.textButton("复制原文")!!.isEnabled())
            }
        }
    }

    @Test fun `same filename in a different root closes copy and opening menus before the new path is acted on`() {
        val firstRoot = Files.createDirectory(root.resolve("first-root"))
        val secondRoot = Files.createDirectory(root.resolve("second-root"))
        Files.writeString(firstRoot.resolve("same.docx"), "first document")
        val second = Files.writeString(secondRoot.resolve("same.docx"), "second document")
        val currentRoot = mutableStateOf(firstRoot)
        val file = RequirementMaterialsMarkdownFile("same.docx", 1)
        val fake = FakeOpening()
        controller(fake).use { app ->
            ImageComposeScene(500, 240, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsFileHeader(app, currentRoot.value, file, null,
                        MarkdownPreviewMode.RENDERED, {}, 0, ioDispatcher = ioDispatcher)
                } }
            }.use { scene ->
                scene.await { scene.textButton("用 WPS 打开")?.isEnabled() == true }
                scene.click(scene.labelButton("复制…"))
                scene.await { scene.textButton("复制文件") != null }
                currentRoot.value = secondRoot
                scene.await { scene.textButton("复制文件") == null && scene.semanticsOwners.size == 1 &&
                    scene.textButton("用 WPS 打开")?.isEnabled() == true }
                assertTrue(fake.calls.isEmpty())
                scene.click(scene.labelButton("打开方式"))
                scene.await { scene.textButton("用其他应用打开…") != null }
                currentRoot.value = firstRoot
                scene.await { scene.textButton("用其他应用打开…") == null && scene.semanticsOwners.size == 1 &&
                    scene.textButton("用 WPS 打开")?.isEnabled() == true }
                currentRoot.value = secondRoot
                scene.await { scene.textButton("用 WPS 打开")?.isEnabled() == true }
                scene.click(scene.textButton("用 WPS 打开")!!)
                scene.await { fake.calls.size == 1 }
                assertEquals(second to false, fake.calls.single())
            }
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.textButton(text: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { item -> item.text == text } == true
    }
    private fun ImageComposeScene.hasText(text: String) = nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { item -> item.text == text } == true }
    private fun ImageComposeScene.labelButton(label: String) = nodes().first {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true
    }
    private fun SemanticsNode.isEnabled() = !config.contains(SemanticsProperties.Disabled)
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val deadline = System.nanoTime() + 10_000_000_000L
        do {
            ioDispatcher.scheduler.runCurrent()
            Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < deadline)
        assertTrue(condition(), "Expected UI state; nodes=" + nodes().map { it.config })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.settleMenus() {
        val until = System.nanoTime() + 250_000_000L
        while (System.nanoTime() < until) { render(System.nanoTime()).close(); Thread.sleep(10) }
    }
}
