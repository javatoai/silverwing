@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class TaskNotesUiTest {
    @TempDir lateinit var root: Path

    @Test fun `three documents open inline and only task rules have editing controls in both themes and widths`() {
        for (dark in listOf(false, true)) for (width in listOf(360, 960)) fixture("layout-$dark-$width",
            "# 人工说明\n\n这是当前任务的说明。\n\n" + (1..50).joinToString("\n\n") { "需要验证的步骤 $it：保留支付渠道与验收要求。" }).use { f ->
            ImageComposeScene(width, 700, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface {
                    TaskNotesPage(f.app, f.tasks.first(), Modifier.fillMaxSize())
                } }
            }.use { scene ->
                scene.await(f.io) { scene.tabNames().size == 3 && scene.hasText("人工说明") && scene.tabVisible("TASK-RULES.md", width) }
                assertEquals(listOf("AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md"), scene.tabNames())
                assertEquals(TASK_NOTES_PREVIEW_PATH, f.browsing(f.tasks.first()).files.selectedPath)
                assertNull(scene.field())
                assertNull(scene.buttonOrNull("预览"))
                assertEquals(1, scene.semanticsOwners.size, "Reading must stay on the task page")
                for (name in listOf("阅读", "编辑", "保存")) {
                    val bounds = scene.button(name).boundsInRoot
                    assertTrue(bounds.left >= 0 && bounds.right <= width && bounds.height > 0, name)
                    assertTrue(bounds.top > 550, "$name must stay in the fixed bottom toolbar")
                }
                scene.capture("read-${if (dark) "dark" else "light"}-$width")
                scene.activateTab("TASK-CONTEXT.md")
                scene.await(f.io) { f.browsing(f.tasks.first()).files.selectedPath.endsWith("TASK-CONTEXT.md") && scene.hasText("Worktree 范围") }
                scene.activateTab("TASK-RULES.md")
                scene.await(f.io) { scene.hasText("人工说明") }
                for (fileName in listOf("AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md")) {
                    scene.activateTab(fileName)
                    scene.await(f.io) { f.browsing(f.tasks.first()).files.selectedPath.endsWith(fileName) &&
                        scene.nodes().any { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab &&
                            it.config.getOrNull(SemanticsProperties.Selected) == true &&
                            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == fileName } == true } }
                    if (fileName != "TASK-RULES.md") {
                        for (control in listOf("阅读", "编辑", "保存")) assertNull(scene.buttonOrNull(control), control)
                        assertNull(scene.field())
                        assertNull(scene.labelOrNull("刷新需求说明"))
                        assertNotNull(scene.labelOrNull("查看 Markdown 源码"))
                        scene.capture("${fileName.substringBefore('.')}-${if (dark) "dark" else "light"}-$width")
                    }
                }
                scene.activate("编辑")
                scene.await(f.io) { scene.field() != null }
                assertEquals(f.initialNotes, scene.editorText())
                scene.capture("edit-${if (dark) "dark" else "light"}-$width")
                scene.activate("阅读")
                scene.await(f.io) { scene.field() == null && scene.hasText("人工说明") }
                assertEquals(f.initialNotes, Files.readString(f.notesPath(f.tasks.first())))
            }
        }
    }

    @Test fun `editing keeps all tabs available and readonly documents hide editing actions without losing the draft`() = fixture("tab-editor").use { f ->
        val task = f.tasks.first()
        ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskNotesPage(f.app, task, Modifier.fillMaxSize()) } }
        }.use { scene ->
            scene.await(f.io) { scene.hasText("initial a") }
            scene.activate("编辑"); scene.pump(f.io, false)
            scene.edit("unsaved rules"); scene.pump(f.io, false)
            assertEquals(3, scene.tabNames().size, "Typing must not start another preview request")
            assertEquals("unsaved rules", scene.editorText())
            for (name in listOf("AGENTS.md", "TASK-CONTEXT.md")) {
                scene.activateTab(name)
                scene.pump(f.io, false)
                assertEquals(3, scene.tabNames().size)
                assertNull(scene.field())
                for (control in listOf("阅读", "编辑", "保存")) assertNull(scene.buttonOrNull(control))
                assertNull(scene.labelOrNull("刷新需求说明"))
                assertNotNull(scene.labelOrNull("查看 Markdown 源码"))
                assertEquals(TaskNotesPageMode.EDIT, f.browsing(task).mode)
            }
            scene.activateTab("TASK-RULES.md"); scene.pump(f.io, false)
            assertEquals("unsaved rules", scene.editorText())
            scene.activate("阅读")
            scene.await(f.io) { scene.hasText("unsaved rules") && scene.field() == null }
            assertEquals("initial a", Files.readString(f.notesPath(task)))
            assertTrue(f.app.agentInstructionsController.notesDraftFor(task).dirty)
        }
    }

    @Test fun `all three notes tabs expose the actual saved file actions even for empty notes`() = fixture("local-files", "").use { f ->
        val task = f.tasks.first()
        ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskNotesPage(f.app, task, Modifier.fillMaxSize()) } }
        }.use { scene ->
            scene.await(f.io) { scene.hasText("尚未填写需求说明，切换到“编辑”可以开始填写。") }
            for (name in listOf("AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md")) {
                scene.activateTab(name)
                scene.await(f.io) { f.browsing(task).files.selectedPath.endsWith(name) }
                scene.activateLabel("复制…")
                scene.await(f.io) { scene.buttonOrNull("复制文件路径") != null }
                assertFalse(scene.button("复制文件路径").config.contains(SemanticsProperties.Disabled), name)
                assertFalse(scene.button("复制文件").config.contains(SemanticsProperties.Disabled), name)
                assertFalse(scene.textNodes().any { it.contains("无本地文件") }, name)
            }
        }
        assertEquals("", Files.readString(f.notesPath(task)))
    }

    @Test fun `reading a draft never saves and repeated saves retain the existing busy guard`() = fixture("draft").use { f ->
        val task = f.tasks.first()
        ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskNotesPage(f.app, task, Modifier.fillMaxSize()) } }
        }.use { scene ->
            scene.await(f.io) { scene.hasText("initial a") }
            scene.activate("编辑"); scene.pump(f.io, false)
            scene.edit("# 未保存的说明\n\n需要回归的草稿。")
            scene.activate("阅读")
            scene.await(f.io) { scene.hasText("未保存的说明") }
            assertEquals("initial a", Files.readString(f.notesPath(task)))
            assertTrue(scene.hasText("未保存"))
            assertEquals(TASK_NOTES_PREVIEW_PATH, f.browsing(task).files.selectedPath)
            scene.activate("保存"); scene.pump(f.io, false)
            assertTrue(f.app.busy)
            assertTrue(scene.button("正在保存…").config.contains(SemanticsProperties.Disabled))
            scene.click(scene.button("正在保存…")); scene.pump(f.io, false)
            assertTrue(f.app.busy)
            assertEquals("initial a", Files.readString(f.notesPath(task)))
            scene.await(f.io) { !f.app.busy && !f.app.agentInstructionsController.notesDraftFor(task).dirty }
            assertEquals("# 未保存的说明\n\n需要回归的草稿。", Files.readString(f.notesPath(task)))
            assertFalse(scene.hasText("未保存"))
        }
    }

    @Test fun `task switches hidden pages and pending metadata updates preserve isolated drafts and reading controls`() = fixture("switch").use { f ->
        val a = f.tasks.first(); val b = f.tasks.last()
        val selected = mutableStateOf(a)
        val visible = mutableStateOf(true)
        ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface {
                if (visible.value) TaskNotesPage(f.app, selected.value, Modifier.fillMaxSize()) else Text("任务详情")
            } }
        }.use { scene ->
            scene.await(f.io) { scene.hasText("initial a") }
            scene.activateTab("TASK-CONTEXT.md")
            scene.await(f.io) { f.browsing(a).files.selectedPath.endsWith("TASK-CONTEXT.md") && scene.hasText("任务上下文") }
            scene.activateLabel("查看 Markdown 源码")
            val contextPath = f.browsing(a).files.selectedPath
            scene.await(f.io) { f.browsing(a).files.readingFor(contextPath).mode.value == MarkdownPreviewMode.SOURCE }
            assertEquals(MarkdownPreviewMode.SOURCE, f.browsing(a).files.readingFor(contextPath).mode.value)
            visible.value = false; scene.pump(f.io, false)
            visible.value = true
            scene.await(f.io) { scene.tabNames().size == 3 && scene.labelOrNull("查看 Markdown 预览") != null }
            assertEquals(contextPath, f.browsing(a).files.selectedPath)

            scene.activateTab("TASK-RULES.md"); scene.pump(f.io, false)
            scene.activate("编辑"); scene.pump(f.io, false); scene.edit("A unsaved")
            selected.value = b; scene.pump(f.io, false)
            scene.await(f.io) { scene.hasText("initial b") }
            assertNull(scene.field())
            scene.activate("编辑"); scene.pump(f.io, false); scene.edit("B unsaved")
            selected.value = a
            scene.await(f.io) { scene.editorText() == "A unsaved" }
            scene.activate("阅读"); scene.pump(f.io, false)
            selected.value = a.copy(updatedAt = "changed", requirementLink = "https://example.test/new-context")
            scene.pump(f.io, false)
            selected.value = b; scene.pump(f.io, false)
            scene.await(f.io) { scene.editorText() == "B unsaved" }
            scene.activate("阅读")
            scene.await(f.io) { scene.hasText("B unsaved") }
            assertFalse(scene.hasText("A unsaved"))
            selected.value = a.copy(updatedAt = "changed", requirementLink = "https://example.test/new-context")
            scene.await(f.io) { scene.hasText("A unsaved") }
            assertEquals("B unsaved", f.app.agentInstructionsController.notesDraftFor(b).notes)
            assertEquals("initial a", Files.readString(f.notesPath(a)))
            assertEquals("initial b", Files.readString(f.notesPath(b)))
            assertFalse(scene.hasText("Agent 文件预览"), "Reading stays inline even when a toolbar tooltip is visible")
        }
    }

    @Test fun `empty notes explain editing and preview errors stay inline and can be retried`() = fixture("failure", "").use { f ->
        val original = f.tasks.first()
        val selected = mutableStateOf(original)
        ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskNotesPage(f.app, selected.value, Modifier.fillMaxSize()) } }
        }.use { scene ->
            scene.await(f.io) { scene.hasText("尚未填写需求说明，切换到“编辑”可以开始填写。") }
            selected.value = original.copy(groupId = "invalid/group", updatedAt = "invalid")
            scene.await(f.io) { scene.textNodes().count { it.startsWith("预览失败：") } == 1 }
            assertNull(f.app.errorMessage, "Automatic previews must not also open the global error dialog")
            assertEquals(1, scene.semanticsOwners.size)
            scene.activate("重试")
            scene.await(f.io) { scene.textNodes().count { it.startsWith("预览失败：") } == 1 }
            selected.value = original.copy(updatedAt = "repaired")
            scene.await(f.io) { scene.tabNames().size == 3 && scene.hasText("尚未填写需求说明，切换到“编辑”可以开始填写。") }
            scene.activate("编辑"); scene.pump(f.io, false); scene.edit("repaired draft")
            scene.activate("阅读")
            scene.await(f.io) { scene.hasText("repaired draft") }
            assertNull(f.app.errorMessage)
        }
    }

    @Test fun `templates conflicts and failed save retry reuse the original draft workflow`() = fixture("workflow", templates = listOf(
        AgentTaskTemplate(id = "base", name = "基础模板", content = "# 模板说明\n\n模板内容。", updatedAt = "now"))).use { f ->
        val task = f.tasks.first()
        ImageComposeScene(900, 700, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskNotesPage(f.app, task, Modifier.fillMaxSize()) } }
        }.use { scene ->
            scene.await(f.io) { scene.hasText("initial a") }
            scene.activate("编辑"); scene.pump(f.io, false); scene.edit("manual draft")
            scene.activate("基础模板")
            scene.await(f.io) { scene.buttonOrNull("替换说明") != null }
            scene.activate("取消")
            scene.await(f.io) { scene.buttonOrNull("替换说明") == null }
            assertEquals("manual draft", scene.editorText())
            scene.activate("基础模板"); scene.await(f.io) { scene.buttonOrNull("替换说明") != null }
            scene.activate("替换说明")
            scene.await(f.io) { scene.editorText() == "# 模板说明\n\n模板内容。" }
            scene.activate("阅读"); scene.await(f.io) { scene.hasText("模板说明") }
            f.failGeneration = true
            scene.activate("保存")
            scene.await(f.io) { !f.app.busy && f.app.errorMessage?.contains("generation failed") == true }
            assertTrue(f.app.agentInstructionsController.notesDraftFor(task).dirty)
            assertTrue(scene.hasText("未保存"))
            f.failGeneration = false
            scene.activate("保存")
            scene.await(f.io) { !f.app.busy && !f.app.agentInstructionsController.notesDraftFor(task).dirty }
            scene.activate("编辑"); scene.pump(f.io, false); scene.edit("local conflict draft")
            Files.writeString(f.notesPath(task), "external notes")
            f.app.agentInstructionsController.onWindowFocused()
            scene.await(f.io) { f.app.agentInstructionsController.state.conflict != null }
            assertEquals("local conflict draft", scene.editorText())
            assertTrue(f.app.agentInstructionsController.resolveConflict(AgentConflictResolution.USE_DISK))
            scene.await(f.io) { scene.editorText() == "external notes" }
            scene.activate("阅读")
            scene.await(f.io) { scene.hasText("external notes") }
            assertFalse(scene.hasText("local conflict draft"))
        }
    }

    private fun fixture(name: String, initialNotes: String = "initial a", templates: List<AgentTaskTemplate> = emptyList()) =
        Fixture(root.resolve(name), initialNotes, templates)

    private class Fixture(directory: Path, val initialNotes: String, templates: List<AgentTaskTemplate>) : AutoCloseable {
        val io = StandardTestDispatcher()
        val paths = ApplicationPaths(directory.resolve("home"))
        val tasksRoot = directory.resolve("tasks")
        val tasks = listOf("a", "b").map { name -> TaskManifest(folderName = name, taskDirectoryName = name,
            featureBranch = "feature/$name", createdAt = "now", updatedAt = "now", services = emptyList(), groupId = "group") }
        val documents = AgentDocumentService(paths)
        var failGeneration = false
        val app: DesktopApplication
        init {
            Dispatchers.setMain(io)
            val store = ConfigStore(paths).apply { save(AppConfig(taskRoot = tasksRoot.toString(),
                groups = listOf(GroupConfig("group", "Group")), aiRequirementNamingEnabled = false)) }
            val manifests = ManifestStore()
            tasks.forEach { task ->
                manifests.save(tasksRoot.resolve(task.taskDirectoryName), task)
                documents.createTaskDocument(tasksRoot.resolve(task.taskDirectoryName), task, emptyList(),
                    if (task == tasks.first()) initialNotes else "initial b")
            }
            AgentTaskTemplateStore(paths).saveAll(templates)
            val writing = object : AgentDocuments by documents {
                override fun writeTaskDocument(taskDirectory: Path, manifest: TaskManifest, repositories: List<RepositoryInfo>, taskNotes: String?): Path {
                    if (failGeneration) error("generation failed")
                    return documents.writeTaskDocument(taskDirectory, manifest, repositories, taskNotes)
                }
            }
            app = DesktopApplication(paths = paths, configStore = store, ioDispatcher = io,
                tasksApplication = TaskApplicationService(manifests = manifests, agentDocuments = writing, operationLock = NoOpTaskOperationLock),
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
        }
        fun notesPath(task: TaskManifest): Path = documents.taskNotesFile(tasksRoot.resolve(task.taskDirectoryName), task)
        fun browsing(task: TaskManifest) = app.taskBrowsingSession.notesFor(app.taskPath(task))
        override fun close() { app.close(); io.scheduler.runCurrent(); Dispatchers.resetMain() }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.textNodes() = nodes().flatMap { it.config.getOrNull(SemanticsProperties.Text).orEmpty().map { text -> text.text } }
    private fun ImageComposeScene.hasText(value: String) = textNodes().any { it == value }
    private fun ImageComposeScene.buttonOrNull(label: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == label } == true
    }
    private fun ImageComposeScene.button(label: String) = buttonOrNull(label) ?: error("Missing $label")
    private fun ImageComposeScene.labelOrNull(label: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true
    }
    private fun ImageComposeScene.field() = nodes().firstOrNull { it.config.getOrNull(SemanticsActions.SetText) != null }
    private fun ImageComposeScene.editorText() = field()?.config?.getOrNull(SemanticsProperties.EditableText)?.text
    private fun ImageComposeScene.tabNames() = nodes().filter { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab }
        .flatMap { it.config.getOrNull(SemanticsProperties.Text).orEmpty().map { text -> text.text } }
    private fun ImageComposeScene.tabVisible(label: String, width: Int) = nodes().any {
        it.config.getOrNull(SemanticsProperties.Role) == Role.Tab &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == label } == true &&
            it.size.width > 0 && it.boundsInRoot.width >= it.size.width - 1 &&
            it.boundsInRoot.left >= 16 && it.boundsInRoot.right <= width - 16
    }
    private fun ImageComposeScene.activate(label: String) { assertTrue(button(label).config.getOrNull(SemanticsActions.OnClick)!!.action!!.invoke()) }
    private fun ImageComposeScene.activateLabel(label: String) { click(labelOrNull(label)!!) }
    private fun ImageComposeScene.activateTab(label: String) {
        val node = nodes().first { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab &&
            it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == label } == true }
        assertTrue(node.config.getOrNull(SemanticsActions.OnClick)!!.action!!.invoke())
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Move, androidx.compose.ui.geometry.Offset(-1f, -1f))
    }
    private fun ImageComposeScene.edit(value: String) { assertTrue(field()!!.config.getOrNull(SemanticsActions.SetText)!!.action!!.invoke(AnnotatedString(value))) }
    private fun ImageComposeScene.pump(io: TestDispatcher, runIo: Boolean = true) {
        if (runIo) io.scheduler.runCurrent()
        Snapshot.sendApplyNotifications()
        repeat(4) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.await(io: TestDispatcher, condition: () -> Boolean) {
        val deadline = System.nanoTime() + 8_000_000_000L
        do { pump(io); if (condition()) return; Thread.sleep(5) } while (System.nanoTime() < deadline)
        assertTrue(condition(), textNodes().joinToString("\n") + "\n" + nodes().filter {
            it.config.getOrNull(SemanticsProperties.Role) == Role.Tab ||
                it.config.getOrNull(SemanticsProperties.ContentDescription)?.any { label -> label.contains("Markdown") } == true
        }.joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.capture(name: String) {
        val output = Path.of("build/reports/task-notes-inline/$name.png")
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
