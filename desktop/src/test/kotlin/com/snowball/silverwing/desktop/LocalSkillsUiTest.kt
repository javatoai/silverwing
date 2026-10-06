@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.material3.Surface
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.use
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.*
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class LocalSkillsUiTest {
    @TempDir lateinit var root: Path

    @Test fun `loading retains skill and file selection but removed items and real skill changes reset safely`() {
        for (dark in listOf(false, true)) for (width in listOf(640, 1300)) {
            val io = StandardTestDispatcher()
            Dispatchers.setMain(io)
            val scope = CoroutineScope(SupervisorJob() + io)
            try {
                val home = root.resolve("${if (dark) "dark" else "light"}-$width")
                fun file(skill: String, name: String): Path = home.resolve(".agents/skills/$skill/$name")
                for (skill in listOf("a", "b")) {
                    Files.createDirectories(file(skill, "SKILL.md").parent)
                    Files.writeString(file(skill, "SKILL.md"), "Skill $skill initial")
                    Files.writeString(file(skill, "z.txt"), "Skill $skill selected reference")
                }
                var catalogFailure = false
                val local = LocalSkillsController(LocalSkillCatalogApplicationService(LocalSkillCatalogService {
                    check(!catalogFailure) { "读取 Skill 目录失败" }
                    home
                }),
                    { error("UI regression must not uninstall real skills") }, scope, io)
                val paths = ApplicationPaths(home.resolve("application"))
                val taskRoot = Files.createDirectories(home.resolve("tasks"))
                val config = ConfigStore(paths).apply { save(AppConfig(taskRoot = taskRoot.toString(), aiRequirementNamingEnabled = false)) }
                DesktopApplication(paths = paths, configStore = config, ioDispatcher = io,
                    developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { app ->
                    val viewportWidth = mutableStateOf(width)
                    ImageComposeScene(width, 700, coroutineContext = Dispatchers.Unconfined) {
                        SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                            Surface { Box(Modifier.width(viewportWidth.value.dp).fillMaxHeight()) { LocalSkillsScreen(app, local) } }
                        }
                    }.use { scene ->
                        fun pump(runIo: Boolean = true) {
                            if (runIo) io.scheduler.runCurrent()
                            Snapshot.sendApplyNotifications()
                            repeat(4) { scene.render(System.nanoTime()).close() }
                        }
                        fun await(condition: () -> Boolean) {
                            repeat(60) { pump(); if (condition()) return }
                            assertTrue(condition(), "Local Skills UI did not settle: ${local.previewState}")
                        }
                        fun preview(skill: String, path: String) = (local.previewState as? LocalSkillFilePreviewState.Loaded)
                            ?.let { it.directoryName == skill && it.relativePath == path } == true
                        fun navigation() {
                            if (viewportWidth.value >= 960) return
                            for (label in listOf("Skills", "目录", "文档")) {
                                scene.assertInside(scene.textButton(label), viewportWidth.value)
                            }
                        }
                        fun go(pane: String) {
                            if (viewportWidth.value < 960) { navigation(); scene.clickText(pane); pump(false) }
                        }
                        fun selectSkill(skill: String) { go("Skills"); scene.clickText(skill); pump(false) }
                        fun selectFile(path: String) { go("目录"); scene.clickText(path); pump(false) }
                        fun documentActions(copyLabel: String = "复制文件内容") {
                            navigation()
                            scene.assertInside(scene.descriptionButton(copyLabel), viewportWidth.value)
                            scene.assertInside(scene.descriptionButton("查找正文 (Ctrl+F)"), viewportWidth.value)
                        }
                        await { preview("a", "SKILL.md") }
                        selectSkill("b")
                        await { preview("b", "SKILL.md") }
                        selectFile("z.txt")
                        await { preview("b", "z.txt") }
                        documentActions()

                        if (width == 1300) {
                            // A wide-window file click also establishes the document destination after resize.
                            viewportWidth.value = 640; pump(false)
                            documentActions()
                            assertTrue(scene.nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "b/z.txt" } == true })
                            go("Skills"); go("文档")
                            assertTrue(preview("b", "z.txt"))
                            viewportWidth.value = width; pump(false)
                            documentActions()
                        }

                        scene.clickDescription("刷新本机 Skill"); pump(false)
                        assertIs<LocalSkillCatalogLoadState.Loading>(local.catalogState)
                        assertEquals(LocalSkillFilePreviewState.Empty, local.previewState)
                        go("Skills"); go("文档")
                        await { preview("b", "z.txt") }
                        local.loadFiles(LocalSkillCatalogItem("b", "b", "")); pump(false)
                        assertIs<LocalSkillFilesState.Loading>(local.filesState)
                        assertEquals(LocalSkillFilePreviewState.Empty, local.previewState)
                        go("Skills"); go("目录"); go("文档")
                        await { preview("b", "z.txt") }
                        catalogFailure = true
                        local.loadFiles(LocalSkillCatalogItem("b", "b", "")); pump(false)
                        await { local.filesState is LocalSkillFilesState.Failed }
                        go("目录"); go("Skills"); go("文档")
                        catalogFailure = false
                        local.loadFiles(LocalSkillCatalogItem("b", "b", "")); pump(false)
                        await { preview("b", "z.txt") }
                        catalogFailure = true
                        scene.clickDescription("刷新本机 Skill"); pump(false)
                        await { local.catalogState is LocalSkillCatalogLoadState.Failed }
                        go("Skills"); go("目录"); go("文档")
                        catalogFailure = false
                        scene.clickDescription("刷新本机 Skill"); pump(false)
                        await { preview("b", "z.txt") }
                        documentActions()
                        val screenshot = Path.of("build/reports/local-skills/${if (dark) "dark" else "light"}-$width.png")
                        Files.createDirectories(screenshot.parent)
                        scene.render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(screenshot, it.bytes) } }

                        // 同名 z.txt 存在于另一个 Skill；真正换 Skill 应回到它自己的初始文件。
                        selectSkill("a")
                        selectSkill("b")
                        await { preview("b", "SKILL.md") }
                        go("文档"); documentActions("复制…")
                        selectFile("z.txt")
                        await { preview("b", "z.txt") }
                        Files.delete(file("b", "z.txt"))
                        local.loadFiles(LocalSkillCatalogItem("b", "b", "")); pump(false)
                        await { preview("b", "SKILL.md") }
                        Files.delete(file("b", "SKILL.md"))
                        scene.clickDescription("刷新本机 Skill"); pump(false)
                        await { preview("a", "SKILL.md") }
                        Files.delete(file("a", "SKILL.md"))
                        scene.clickDescription("刷新本机 Skill"); pump(false)
                        await { (local.catalogState as? LocalSkillCatalogLoadState.Loaded)?.catalog?.skills?.isEmpty() == true }
                        assertEquals(LocalSkillFilesState.Empty, local.filesState)
                        assertEquals(LocalSkillFilePreviewState.Empty, local.previewState)
                        go("目录"); go("文档"); go("Skills")
                        navigation()
                        assertTrue(scene.nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "尚未发现本机 Skill" } == true })
                    }
                }
            } finally { scope.cancel(); io.scheduler.runCurrent(); Dispatchers.resetMain() }
        }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.textButton(text: String) = nodes().first {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true
    }
    private fun ImageComposeScene.descriptionButton(description: String) = nodes().first {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(description) == true
    }
    private fun ImageComposeScene.clickText(text: String) = click(textButton(text))
    private fun ImageComposeScene.clickDescription(description: String) = click(descriptionButton(description))
    private fun ImageComposeScene.assertInside(node: SemanticsNode, width: Int) {
        val bounds = node.boundsInRoot
        assertTrue(bounds.width > 0 && bounds.height > 0 && bounds.left >= 0 && bounds.right <= width &&
            bounds.top >= 0 && bounds.bottom <= 700, "Control must stay inside the viewport: $bounds / $width")
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
    }
}
