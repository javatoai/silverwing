@file:OptIn(
    androidx.compose.ui.ExperimentalComposeUiApi::class,
    androidx.compose.ui.InternalComposeUiApi::class,
    kotlinx.coroutines.ExperimentalCoroutinesApi::class,
)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.use
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.DevelopmentToolAutoDetectionResult
import com.snowball.silverwing.core.DevelopmentToolStartupDetection
import com.snowball.silverwing.core.RequirementMaterialsDirectory
import com.snowball.silverwing.core.RequirementMaterialsStatus
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.ThemePreference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import java.awt.Color
import java.awt.image.BufferedImage
import java.nio.file.Files
import java.nio.file.Path
import javax.imageio.ImageIO
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * End-to-end checks for the high frequency material editing workflow.
 *
 * These tests deliberately use a temporary materials root and a fake application
 * opening service. They exercise the real browser boundary, so a regression in
 * selection, draft identity, rename remapping, or the image navigator is visible
 * without depending on the host clipboard or WPS.
 */
class MaterialsEditingWorkflowTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `browser navigates to an unopened image and back in light and dark layouts`() {
        val root = Files.createDirectory(temporary.resolve("images"))
        writeImage(root.resolve("one.png"), Color(35, 110, 220))
        writeImage(root.resolve("two.png"), Color(220, 110, 35))
        val scenarios = listOf(
            ThemePreference.LIGHT to 540,
            ThemePreference.DARK to 1200,
        )

        scenarios.forEach { (theme, width) ->
            withApp(temporary.resolve("app-image-${theme.name}-$width"), temporary.resolve("tasks-image-$width")) { app, io ->
                val state = MaterialsBrowserState()
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(theme) { Surface {
                        RequirementMaterialsBrowser(
                            app,
                            task(root),
                            Modifier.fillMaxSize(),
                            state,
                            io,
                            onCopyFiles = { error("Image workflow must not invoke file clipboard") },
                            onCopyPaths = { error("Image workflow must not invoke path clipboard") },
                        )
                    } }
                }.use { scene ->
                    scene.await(io) { scene.row("one.png") != null }
                    scene.click(scene.row("one.png")!!)
                    scene.await(io) { scene.text("1 / 2 · 4 × 3") != null }
                    // The second image has not been opened by the browser yet. The
                    // navigator must change the browser selection and load it there.
                    scene.click(scene.description("下一张图片"))
                    scene.await(io) {
                        state.selectedPath.value == "two.png" && scene.text("2 / 2 · 4 × 3") != null
                    }
                    scene.click(scene.description("上一张图片"))
                    scene.await(io) {
                        state.selectedPath.value == "one.png" && scene.text("1 / 2 · 4 × 3") != null
                    }
                    scene.capture("image-${theme.name.lowercase()}-$width.png")
                }
            }
        }
    }

    @Test
    fun `markdown editor saves with ctrl s and rendered preview shows the new content`() {
        val root = Files.createDirectory(temporary.resolve("editor"))
        val file = Files.writeString(root.resolve("说明.md"), "# 原始标题\n\n原始正文\n")
        withApp(temporary.resolve("app-editor"), temporary.resolve("tasks-editor")) { app, io ->
            val state = MaterialsBrowserState()
            ImageComposeScene(540, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io)
                } }
            }.use { scene ->
                scene.await(io) { scene.row("说明.md") != null }
                scene.click(scene.row("说明.md")!!)
                scene.await(io) { scene.text("原始正文") != null && scene.buttonOrNull("编辑 Markdown") != null }
                scene.click(scene.button("编辑 Markdown"))
                scene.await(io) { scene.tag("materials-markdown-editor") != null }
                val draft = "# 已保存标题\n\n**保存后的正文**\n"
                scene.setText("materials-markdown-editor", draft)
                scene.await(io) { scene.text("有未保存修改") != null }
                scene.capture("markdown-editor-light-540.png")
                scene.key(Key.S, ctrl = true)
                scene.await(io) { Files.readString(file) == draft && scene.text("已保存") != null }
                scene.click(scene.button("查看任务资料预览"))
                scene.await(io) { scene.text("保存后的正文") != null && scene.tag("materials-markdown-editor") == null }
                assertEquals(draft, Files.readString(file))
                scene.capture("markdown-edit-preview.png")
            }
        }
    }

    @Test
    fun `markdown draft survives application close and restart`() {
        val root = Files.createDirectory(temporary.resolve("restart"))
        Files.writeString(root.resolve("draft.md"), "# 磁盘内容\n")
        val appPath = temporary.resolve("app-restart")
        val taskRoot = temporary.resolve("tasks-restart")
        withApp(appPath, taskRoot) { app, io ->
            val state = MaterialsBrowserState()
            ImageComposeScene(900, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.DARK) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io)
                } }
            }.use { scene ->
                scene.await(io) { scene.row("draft.md") != null }
                scene.click(scene.row("draft.md")!!)
                scene.await(io) { scene.buttonOrNull("编辑 Markdown") != null }
                scene.click(scene.button("编辑 Markdown"))
                scene.await(io) { scene.tag("materials-markdown-editor") != null }
                scene.setText("materials-markdown-editor", "# 未保存草稿\n\n重启后仍然可见\n")
                scene.await(io) { scene.text("有未保存修改") != null }
            }
        }

        withApp(appPath, taskRoot) { app, io ->
            val state = MaterialsBrowserState()
            ImageComposeScene(900, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.DARK) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io)
                } }
            }.use { scene ->
                scene.await(io) { scene.row("draft.md") != null }
                scene.click(scene.row("draft.md")!!)
                scene.await(io) { scene.buttonOrNull("编辑 Markdown") != null }
                scene.click(scene.button("编辑 Markdown"))
                scene.await(io) {
                    scene.tag("materials-markdown-editor") != null &&
                        !scene.hasText("正在准备编辑器…") &&
                        scene.hasText("重启后仍然可见")
                }
                assertEquals("# 磁盘内容\n", Files.readString(root.resolve("draft.md")))
                scene.capture("markdown-draft-restart.png")
            }
        }
    }

    @Test
    fun `renaming a dirty markdown file migrates its draft identity`() {
        val root = Files.createDirectory(temporary.resolve("rename"))
        Files.writeString(root.resolve("原名.md"), "# 原始\n")
        withApp(temporary.resolve("app-rename"), temporary.resolve("tasks-rename")) { app, io ->
            val state = MaterialsBrowserState()
            ImageComposeScene(900, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io)
                } }
            }.use { scene ->
                scene.await(io) { scene.row("原名.md") != null }
                scene.click(scene.row("原名.md")!!)
                scene.await(io) { scene.buttonOrNull("编辑 Markdown") != null }
                scene.click(scene.button("编辑 Markdown"))
                scene.await(io) { scene.tag("materials-markdown-editor") != null }
                scene.setText("materials-markdown-editor", "# 重命名后仍保留\n\n草稿内容\n")
                scene.await(io) { scene.text("有未保存修改") != null }

                scene.context(scene.row("原名.md")!!)
                scene.await(io) { scene.button("重命名…") != null }
                scene.click(scene.button("重命名…"))
                scene.await(io) { scene.tag("materials-entry-name") != null }
                scene.setText("materials-entry-name", "新名.md")
                scene.click(scene.button("重命名"))
                scene.await(io) {
                        scene.row("新名.md") != null &&
                        scene.tag("materials-markdown-editor") != null &&
                        scene.hasText("草稿内容")
                }
                assertTrue(Files.exists(root.resolve("新名.md")))
                assertTrue(!Files.exists(root.resolve("原名.md")))
            }
        }
    }

    @Test
    fun `external markdown change shows conflict and explicit retry overwrites it`() {
        listOf(
            ThemePreference.LIGHT to 900,
            ThemePreference.LIGHT to 540,
            ThemePreference.DARK to 900,
            ThemePreference.DARK to 540,
        ).forEach { (theme, width) ->
            val suffix = "${theme.name.lowercase()}-$width"
            val root = Files.createDirectory(temporary.resolve("conflict-$suffix"))
            val file = Files.writeString(root.resolve("冲突.md"), "# 基线\n")
            withApp(temporary.resolve("app-conflict-$suffix"), temporary.resolve("tasks-conflict-$suffix")) { app, io ->
                val state = MaterialsBrowserState()
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(theme) { Surface {
                        RequirementMaterialsBrowser(app, task(root), Modifier.fillMaxSize(), state, io)
                    } }
                }.use { scene ->
                    scene.await(io) { scene.row("冲突.md") != null }
                    scene.click(scene.row("冲突.md")!!)
                    scene.await(io) { scene.buttonOrNull("编辑 Markdown") != null }
                    scene.click(scene.button("编辑 Markdown"))
                    scene.await(io) { scene.tag("materials-markdown-editor") != null }
                    val local = "# 本地草稿\n\n应保留\n"
                    scene.setText("materials-markdown-editor", local)
                    scene.await(io) { scene.text("有未保存修改") != null }
                    Files.writeString(file, "# 外部修改\n")
                    scene.click(scene.button("保存"))
                    scene.await(io) { scene.hasText("文件已被外部修改") && scene.button("保留草稿并覆盖") != null }
                    scene.capture("markdown-conflict-$suffix.png")

                    // Capture the comparison surface before choosing which version
                    // to keep. This is the useful review state for a real user.
                    scene.buttonOrNull("查看外部内容")?.let { viewExternal ->
                        scene.click(viewExternal)
                        scene.await(io) { scene.textButtonOrNull("关闭") != null && scene.hasText("外部修改") }
                        scene.capture("markdown-conflict-external-$suffix.png")
                        scene.click(scene.textButton("关闭"))
                        // Waiting for the underlying action to exist is not enough:
                        // Compose's AlertDialog can still own the pointer during its
                        // exit transition. Wait for the dialog action to disappear,
                        // then let the exit animation settle before the next real
                        // pointer click.
                        scene.await(io) {
                            scene.textButtonOrNull("关闭") == null &&
                                scene.buttonOrNull("保留草稿并覆盖") != null
                        }
                        scene.settleForPointer()
                    }

                    assertEquals("# 外部修改\n", Files.readString(file))
                    scene.click(scene.button("保留草稿并覆盖"))
                    // The final implementation may require an explicit confirmation
                    // before replacing the externally changed file. Accept either
                    // the confirmation dialog or the direct-save flow.
                    scene.await(io) {
                        Files.readString(file) == local || scene.buttonContaining("确认覆盖") != null || scene.buttonContaining("确认") != null
                    }
                    if (Files.readString(file) != local) {
                        val confirmation = scene.buttonContaining("确认覆盖") ?: scene.buttonContaining("确认")
                        requireNotNull(confirmation) { "Missing overwrite confirmation" }
                        scene.capture("markdown-conflict-confirm-$suffix.png")
                        scene.click(confirmation)
                    }
                    scene.await(io) { Files.readString(file) == local && scene.text("已保存") != null }
                }
            }
        }
    }

    private fun task(root: Path) = TaskManifest(
        folderName = "编辑工作流",
        taskDirectoryName = "编辑工作流",
        featureBranch = "feature/materials-editing",
        createdAt = "now",
        updatedAt = "now",
        services = emptyList(),
        requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, root.toString()),
    )

    private inline fun withApp(
        appPath: Path,
        taskRoot: Path,
        block: (DesktopApplication, TestDispatcher) -> Unit,
    ) {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try {
            Files.createDirectories(taskRoot)
            val paths = ApplicationPaths(appPath)
            val config = ConfigStore(paths).also {
                it.save(AppConfig(taskRoot = taskRoot.toString(), aiRequirementNamingEnabled = false))
            }
            val opening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication("Reader")
                override suspend fun open(path: Path) = FileOpenResult.Cancelled
                override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
            }
            DesktopApplication(
                paths = paths,
                configStore = config,
                ioDispatcher = io,
                systemFileOpening = opening,
                developmentToolStartupDetection = DevelopmentToolStartupDetection {
                    DevelopmentToolAutoDetectionResult(it, emptySet())
                },
            ).use { app -> block(app, io) }
        } finally {
            io.scheduler.runCurrent()
            Dispatchers.resetMain()
        }
    }

    private fun writeImage(path: Path, color: Color) {
        val image = BufferedImage(4, 3, BufferedImage.TYPE_INT_ARGB)
        val graphics = image.createGraphics()
        try {
            graphics.color = color
            graphics.fillRect(0, 0, image.width, image.height)
        } finally {
            graphics.dispose()
        }
        assertTrue(ImageIO.write(image, "png", path.toFile()))
        image.flush()
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }

    private fun ImageComposeScene.text(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }

    private fun ImageComposeScene.hasText(value: String): Boolean = nodes().any { node ->
        node.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text.contains(value) } == true ||
            node.config.getOrNull(SemanticsProperties.EditableText)?.text?.contains(value) == true ||
            node.config.toString().contains(value)
    }

    private fun ImageComposeScene.button(value: String): SemanticsNode = nodes().first {
        it.config.contains(SemanticsActions.OnClick) &&
            (it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true ||
                it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true)
    }

    private fun ImageComposeScene.buttonOrNull(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.contains(SemanticsActions.OnClick) &&
            (it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true ||
                it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true)
    }

    private fun ImageComposeScene.textButton(value: String): SemanticsNode = nodes().first {
        it.config.contains(SemanticsActions.OnClick) &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }

    private fun ImageComposeScene.textButtonOrNull(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.contains(SemanticsActions.OnClick) &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }

    private fun ImageComposeScene.buttonContaining(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.contains(SemanticsActions.OnClick) &&
            (it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text.contains(value) } == true ||
                it.config.getOrNull(SemanticsProperties.ContentDescription)?.any { text -> text.contains(value) } == true)
    }

    private fun ImageComposeScene.description(value: String): SemanticsNode = nodes().first {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true
    }

    private fun ImageComposeScene.tag(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.TestTag) == value
    }

    private fun ImageComposeScene.row(value: String): SemanticsNode? = nodes().firstOrNull {
        it.config.contains(SemanticsActions.OnClick) &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true &&
            it.config.getOrNull(SemanticsProperties.TestTag)?.startsWith("materials-tab:") != true
    }

    private fun ImageComposeScene.setText(tag: String, value: String) {
        val node = tag(tag) ?: error("Missing editor field: $tag")
        node.config[androidx.compose.ui.semantics.SemanticsActions.RequestFocus].action?.invoke()
        node.config[SemanticsActions.SetText].action?.invoke(AnnotatedString(value))
            ?: error("Node $tag does not expose SetText")
        repeat(3) { render(System.nanoTime()).close() }
    }

    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(
            PointerEventType.Press,
            point,
            buttons = PointerButtons(isPrimaryPressed = true),
            button = PointerButton.Primary,
        )
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }

    private fun ImageComposeScene.context(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(
            PointerEventType.Press,
            point,
            buttons = PointerButtons(isSecondaryPressed = true),
            button = PointerButton.Secondary,
        )
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Secondary)
        repeat(3) { render(System.nanoTime()).close() }
    }

    private fun ImageComposeScene.key(key: Key, ctrl: Boolean = false) {
        sendKeyEvent(KeyEvent(key, KeyEventType.KeyDown, isCtrlPressed = ctrl))
        sendKeyEvent(KeyEvent(key, KeyEventType.KeyUp, isCtrlPressed = ctrl))
        repeat(3) { render(System.nanoTime()).close() }
    }

    private fun ImageComposeScene.await(io: TestDispatcher, condition: () -> Boolean) {
        repeat(240) {
            io.scheduler.runCurrent()
            io.scheduler.advanceTimeBy(25)
            androidx.compose.runtime.snapshots.Snapshot.sendApplyNotifications()
            repeat(2) { render(System.nanoTime()).close() }
            if (condition()) return
        }
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }

    private fun ImageComposeScene.capture(name: String) {
        val output = Path.of("build/reports/materials-editing-workflow", name)
        Files.createDirectories(output.parent)
        // Let Compose settle toolbar wrapping, dialog placement, and any focus
        // animation before persisting the evidence frame.
        repeat(18) {
            Thread.sleep(10)
            render(System.nanoTime()).close()
        }
        render(System.nanoTime()).use { image ->
            image.encodeToData()!!.use { Files.write(output, it.bytes) }
        }
    }

    private fun ImageComposeScene.settleForPointer() {
        repeat(18) {
            Thread.sleep(10)
            render(System.nanoTime()).close()
        }
    }
}
