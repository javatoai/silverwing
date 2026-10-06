@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.*
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.resetMain
import org.junit.jupiter.api.io.TempDir
import org.apache.pdfbox.pdmodel.PDDocument
import org.apache.pdfbox.pdmodel.PDPage
import org.apache.pdfbox.pdmodel.common.PDRectangle
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class TaskWorkspaceUiTest {
    @TempDir lateinit var root: Path
    private val io = StandardTestDispatcher()
    @org.junit.jupiter.api.BeforeEach fun useControlledUiDispatcher() { Dispatchers.setMain(io) }
    @org.junit.jupiter.api.AfterEach fun restoreUiDispatcher() { Dispatchers.resetMain() }

    @Test fun `task rows show only folder and status and all toolbar widths omit duplicate names`() {
        val taskRoot = Files.createDirectory(root.resolve("compact-tasks"))
        val statuses = listOf("待评审", "待排期", "研发中", "待测试", "已完成", "已取消", "未完成", "自定义状态需要保留原文", null)
        val names = listOf("支付渠道优化与很长很长的任务文件夹名称", "渠道排期", "渠道研发", "支付测试", "已完成任务", "取消任务", "未完成任务", "自定义状态任务", "读取失败任务")
        val linked = statuses.mapIndexed { index, _ -> task(names[index], root).copy(
            requirementLink = "https://project.feishu.cn/obt/userstory/detail/${index + 1}") }
        val unlinked = task("没有关联需求", root)
        val archived = task("归档文件夹", root).copy(lifecycleStatus = TaskLifecycleStatus.ARCHIVED,
            requirementLink = "https://project.feishu.cn/obt/userstory/detail/1")
        (linked + unlinked + archived).forEach { ManifestStore().save(taskRoot.resolve(it.taskDirectoryName), it) }
        for (dark in listOf(false, true)) for (width in listOf(580, 1200)) {
            val calls = java.util.concurrent.ConcurrentHashMap<String, Int>()
            val metadata = RequirementMetadataProvider { link ->
                calls.merge(link, 1, Int::plus)
                // 模拟后台读取，等列表的首帧加载状态全部提交后再返回结果。
                Thread.sleep(50)
                val index = FeishuWorkItemLink.parse(link)!!.workItemId.toInt() - 1
                statuses[index]?.let { RequirementMetadata(title = "不应显示的需求标题 $index", status = it) }
            }
            app(AppConfig(taskRoot = taskRoot.toString()), metadata = metadata).use { app ->
                app.taskBrowsingSession.apply { preferredWidth = 160f; view = TaskContentView.NOTES }
                app.selectTask(linked.first())
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface { Row {
                        Sidebar(app, navigationLayoutFor(width.toFloat())) {}
                        Box(Modifier.weight(1f)) { TasksScreen(app, app.navigation == NavigationItem.ARCHIVED) }
                    } } }
                }.use { scene ->
                    scene.await { scene.editable() != null && linked.all { app.requirementController.stateFor(it) !is RequirementUiState.Loading && app.requirementController.stateFor(it) !is RequirementUiState.NotLoaded } }
                    assertEquals(4, scene.nodes().count { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab })
                    scene.await { scene.text("需求说明")!!.boundsInRoot.right <= width - 8 }
                    if (width == 580) {
                        scene.await { scene.text("任务详情")!!.boundsInRoot.left >= scene.label("拖动调整任务列表宽度").boundsInRoot.right }
                        assertTrue(scene.nodes().filter { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab }.all {
                            it.boundsInRoot.left >= scene.label("拖动调整任务列表宽度").boundsInRoot.right && it.boundsInRoot.right <= width - 8
                        }, "All four tabs must remain fully visible in a compact workspace")
                    }
                    assertTrue(scene.textContaining(linked.first().folderName), "Task list stays visible at every window width")
                    scene.screenshot("${if (dark) "dark" else "light"}-compact-toolbar-$width")
                    scene.await { scene.text(unlinked.folderName) != null }
                    statuses.filterNotNull().forEach { assertNotNull(scene.text(it), it) }
                    assertNotNull(scene.text("读取失败"))
                    assertFalse(scene.textContaining("不应显示的需求标题"))
                    assertFalse(scene.textContaining("就绪"))
                    assertNull(scene.text("已归档"))
                    val selectedRow = scene.taskRow(linked.first().folderName)
                    assertEquals(true, selectedRow.config[SemanticsProperties.Selected])
                    assertEquals(listOf(linked.first().folderName, "待评审"), selectedRow.texts())
                    val nameBounds = scene.text(linked.first().folderName)!!.boundsInRoot
                    val statusBounds = scene.text("待评审")!!.boundsInRoot
                    assertTrue(statusBounds.right <= selectedRow.boundsInRoot.right, "Status must remain inside the task row")
                    assertTrue(nameBounds.right <= statusBounds.left, "Long folder name must reserve space for the status")
                    assertEquals(nameBounds.center.y, statusBounds.center.y, 1f, "Name and status stay on one line")
                    assertEquals(listOf(unlinked.folderName), scene.taskRow(unlinked.folderName).texts())
                    linked.forEach { task ->
                        assertTrue(scene.text(task.folderName)!!.boundsInRoot.width >= 48f,
                            "Long status must leave readable task name space at the minimum list width")
                    }
                    assertEquals(1, scene.nodes().count { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == linked.first().folderName } == true })
                    scene.screenshot("${if (dark) "dark" else "light"}-compact-task-list-$width")
                    scene.click(scene.text(linked[2].folderName)!!)
                    scene.await { app.selectedTask == linked[2] }
                    assertEquals(TaskContentView.NOTES, app.taskBrowsingSession.view)
                    scene.await { scene.text("查看归档 (1)") != null }
                    scene.click(scene.text("查看归档 (1)")!!)
                    scene.await { scene.text(archived.folderName) != null && "待评审" in scene.taskRow(archived.folderName).texts() }
                    assertEquals(listOf(archived.folderName, "待评审"), scene.taskRow(archived.folderName).texts())
                    assertNull(scene.text("已归档"))
                    assertTrue(calls.values.all { it == 1 }, "Reuse existing metadata cache, no extra title queries: $calls")
                }
            }
        }
    }

    @Test fun `wide sidebar clamps live resize and four tabs preserve task selection`() {
        val session = TaskBrowsingSession(300f)
        val chosen = mutableStateOf("任务 A")
        val saves = mutableListOf<Int>()
        ImageComposeScene(1200, 650, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                Surface { TaskWorkspaceLayout(session, chosen.value, onPersistLayout = { saves += it },
                    listPane = { modifier, selected -> Column(modifier) {
                        listOf("任务 A", "任务 B").forEach { title -> TextButton(onClick = { chosen.value = title; selected() }) { Text(title) } }
                    } }, content = { modifier -> Text("${session.view}:${chosen.value}", modifier) }) }
            }
        }.use { scene ->
            scene.await { scene.text("需求说明") != null }
            assertEquals(listOf("任务详情", "任务资料", "需求详情", "需求说明"), TaskContentView.entries.map { it.label })
            scene.click(scene.text("需求详情")!!)
            scene.click(scene.text("任务 B")!!)
            assertEquals(TaskContentView.REQUIREMENT, session.view)
            assertEquals("任务 B", chosen.value)
            scene.drag(scene.label("拖动调整任务列表宽度").boundsInRoot.center, -500f)
            scene.await { session.preferredWidth == 160f }
            assertEquals(160, saves.last())
            assertNotNull(scene.text("任务 A"))
            assertNull(scene.labelOrNull("收起任务列表"))
            assertNull(scene.labelOrNull("拖动展开任务列表"))
            scene.drag(scene.label("拖动调整任务列表宽度").boundsInRoot.center, 1000f)
            scene.await { session.preferredWidth == 576f }
            scene.click(scene.text("需求说明")!!)
            assertEquals(TaskContentView.NOTES, session.view)
            scene.screenshot("light-wide")
        }
    }

    @Test fun `narrow sidebar never collapses and window changes preserve saved wide geometry`() {
        for (dark in listOf(false, true)) {
            val session = TaskBrowsingSession(340f).apply { view = TaskContentView.MATERIALS }
            val chosen = mutableStateOf("当前任务")
            val saves = mutableListOf<Int>()
            ImageComposeScene(580, 600, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { TaskWorkspaceLayout(session, chosen.value, onPersistLayout = { saves += it },
                        listPane = { modifier, selected -> Column(modifier) { TextButton(onClick = { chosen.value = "另一个任务"; selected() }) { Text("切换任务") } } },
                        content = { modifier -> Text("资料：${chosen.value}", modifier) }) }
                }
            }.use { scene ->
                scene.await { scene.text("切换任务") != null }
                assertEquals(340f, session.preferredWidth, "A temporary narrow window must not replace saved geometry")
                assertTrue(saves.isEmpty())
                assertEquals(4, scene.nodes().count { it.config.getOrNull(SemanticsProperties.Role) == Role.Tab })
                assertNull(scene.labelOrNull("收起任务列表"))
                assertNull(scene.labelOrNull("展开任务列表"))
                scene.drag(scene.label("拖动调整任务列表宽度").boundsInRoot.center, -400f)
                scene.await { saves.lastOrNull() == 160 }
                assertNotNull(scene.text("切换任务"))
                scene.click(scene.text("切换任务")!!)
                scene.await { scene.text("资料：另一个任务") != null }
                assertEquals(TaskContentView.MATERIALS, session.view)
                assertEquals(160f, session.preferredWidth)
                assertEquals(1, scene.semanticsOwners.size)
                scene.screenshot(if (dark) "dark-narrow-inline" else "light-narrow-inline")
            }
        }
    }

    @Test fun `real materials browser restores text file mode and reading position across tasks and view disposal`() {
        val aRoot = Files.createDirectory(root.resolve("资料 A"))
        val bRoot = Files.createDirectory(root.resolve("资料 B"))
        Files.writeString(aRoot.resolve("01.txt"), "短文")
        Files.writeString(aRoot.resolve("02.txt"), (1..180).joinToString("\n") { "资料 A 第 $it 行，中文与空格路径" })
        Files.writeString(bRoot.resolve("only.txt"), "资料 B 的内容")
        val a = task("A", aRoot)
        val b = task("B", bRoot)
        val selected = mutableStateOf(a)
        val showing = mutableStateOf(true)
        val session = TaskBrowsingSession(240f)
        app().use { app ->
            ImageComposeScene(960, 560, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) {
                    Surface { if (showing.value) {
                        val task = selected.value
                        RequirementMaterialsBrowser(app, task, browserState = session.materialsFor(task.folderName, task.requirementMaterials.writeRoot), ioDispatcher = io)
                    } else Text("任务详情") }
                }
            }.use { scene ->
                scene.await { scene.text("02.txt") != null }
                scene.click(scene.text("02.txt")!!)
                scene.await { scene.textContaining("资料 A 第 180 行") }
                scene.click(scene.label("查看TXT源码"))
                scene.await { scene.labelOrNull("查看TXT预览") != null }
                scene.sendPointerEvent(PointerEventType.Scroll, Offset(650f, 380f), scrollDelta = Offset(0f, 350f))
                val state = session.materialsFor("A", aRoot.toString())
                scene.await { (state.readingFor("02.txt").scrollPositions["source-vertical"] ?: 0) > 0 }
                selected.value = b
                Snapshot.sendApplyNotifications()
                scene.render(System.nanoTime()).close()
                assertFalse(scene.textContaining("资料 A 第 180 行"), "Switch must immediately discard the previous task content")
                scene.await { scene.textContaining("资料 B 的内容") }
                val position = state.readingFor("02.txt").scrollPositions["source-vertical"]!!
                assertFalse(scene.textContaining("资料 A 第 180 行"))
                selected.value = a
                scene.await { scene.labelOrNull("查看TXT预览") != null && scene.textContaining("资料 A 第 180 行") }
                assertEquals("02.txt", state.selectedPath.value)
                assertEquals(true, scene.text("02.txt")!!.config.getOrNull(SemanticsProperties.Selected))
                assertEquals(position, state.readingFor("02.txt").scrollPositions["source-vertical"])
                showing.value = false
                scene.await { scene.text("任务详情") != null }
                showing.value = true
                scene.await { scene.labelOrNull("查看TXT预览") != null }
                assertEquals(position, state.readingFor("02.txt").scrollPositions["source-vertical"])
                Files.delete(aRoot.resolve("02.txt"))
                scene.click(scene.label("刷新任务资料目录"))
                scene.await { state.selectedPath.value == "01.txt" && scene.textContaining("短文") }
                scene.screenshot("light-materials")
            }
        }
    }

    @Test fun `PDF returns to its saved page and zoom after preview is disposed`() {
        PDDocument().use { document -> repeat(5) { document.addPage(PDPage(PDRectangle(300f, 420f))) }; document.save(root.resolve("read.pdf").toFile()) }
        val state = MaterialsReadingState()
        val showing = mutableStateOf(true)
        ImageComposeScene(580, 520, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.DARK) {
                Surface { if (showing.value) CompositionLocalProvider(LocalMaterialsReadingState provides state) {
                    RequirementMaterialsPdfPreview(root, "read.pdf", 0, ioDispatcher = io)
                } else Text("其他任务") }
            }
        }.use { scene ->
            scene.await { scene.text("1 / 5 页") != null }
            scene.click(scene.label("下一页"))
            scene.await { scene.text("2 / 5 页") != null }
            scene.click(scene.label("放大"))
            scene.await { scene.text("125%") != null }
            showing.value = false
            scene.await { scene.text("其他任务") != null }
            assertEquals(1, state.listPositions["pdf-pages"]?.index)
            showing.value = true
            scene.await { scene.text("2 / 5 页") != null && scene.text("125%") != null }
        }
    }

    @Test fun `production tasks screen shows flat three panes and keeps task selection in materials on both themes`() {
        val aRoot = Files.createDirectory(root.resolve("支付资料"))
        val bRoot = Files.createDirectory(root.resolve("对账资料"))
        Files.writeString(aRoot.resolve("方案.md"), "# 支付渠道优化\n\n需求资料与任务放在同一工作区。\n\n| 文件 | 说明 |\n| --- | --- |\n| 方案 | 研发资料 |")
        Files.writeString(bRoot.resolve("说明.txt"), "对账资料预览")
        val a = task("7118490426-支付渠道优化与长任务名称", aRoot)
        val b = task("7118490427-渠道对账", bRoot)
        val missing = task("尚未关联的任务", aRoot).copy(requirementMaterials = RequirementMaterialsDirectory())
        val tasksRoot = Files.createDirectory(root.resolve("tasks"))
        listOf(a, b, missing).forEach { ManifestStore().save(tasksRoot.resolve(it.taskDirectoryName), it) }
        for (dark in listOf(false, true)) for (width in listOf(580, 1200)) {
            app(AppConfig(taskRoot = tasksRoot.toString())).use { app ->
                app.taskBrowsingSession.apply { view = TaskContentView.MATERIALS; preferredWidth = 240f }
                app.selectTask(a)
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface { TasksScreen(app, false) } }
                }.use { scene ->
                    scene.await { scene.text("方案.md") != null }
                    assertEquals(true, scene.text("方案.md")!!.config.getOrNull(SemanticsProperties.Selected))
                    if (width < 960) {
                        // 窄窗口仍保留任务列表；资料列表与预览共用恢复入口。
                        val collapse = scene.labelOrNull("折叠文档列表")
                        if (collapse != null) scene.click(collapse) else scene.click(scene.text("方案.md")!!)
                        scene.await { scene.labelOrNull("展开文档列表") != null }
                    }
                    scene.await { scene.textContaining("需求资料与任务放在同一工作区") }
                    scene.screenshot("${if (dark) "dark" else "light"}-production-$width")
                    assertNotNull(scene.text(b.folderName))
                    scene.click(scene.text(b.folderName)!!)
                    scene.await { app.selectedTask == b && scene.text("说明.txt") != null }
                    assertEquals(TaskContentView.MATERIALS, app.taskBrowsingSession.view)
                    if (width >= 960) {
                        scene.click(scene.text(missing.folderName)!!)
                        scene.await { scene.text("尚未关联任务资料") != null }
                        assertNotNull(scene.text(b.folderName))
                        scene.click(scene.text(a.folderName)!!)
                        scene.await { scene.text("方案.md") != null }

                    }
                }
            }
        }
    }

    @Test fun `production requirement tab reads without list and restores source position on both themes and widths`() {
        val taskRoot = Files.createDirectory(root.resolve("requirements-tasks"))
        val a = task("支付需求任务", root).copy(requirementLink = "https://project.feishu.cn/obt/userstory/detail/1")
        val b = task("对账需求任务", root).copy(requirementLink = "https://project.feishu.cn/obt/userstory/detail/2")
        listOf(a, b).forEach { ManifestStore().save(taskRoot.resolve(it.taskDirectoryName), it) }
        for (dark in listOf(false, true)) for (width in listOf(580, 1200)) {
            val calls = mutableListOf<String>()
            val source = object : ParticipatedWorkItemsSource {
                override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult =
                    error("Do not initialize the requirements list")
                override fun loadBody(item: ParticipatedWorkItem): String {
                    synchronized(calls) { calls += item.id }
                    return if (item.id == "1") "# 支付渠道优化正文\n\n| 场景 | 结果 |\n| --- | --- |\n| 支付 | 成功 |\n\n" + (1..180).joinToString("\n\n") { "支付需求第 $it 段内容" }
                    else "# 对账正文\n\n独立读取需求内容"
                }
            }
            app(AppConfig(taskRoot = taskRoot.toString()), source).use { app ->
                app.selectTask(a)
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface { Row {
                        Sidebar(app, navigationLayoutFor(width.toFloat())) {}
                        Box(Modifier.weight(1f)) { TasksScreen(app, false) }
                    } } }
                }.use { scene ->
                    scene.await { scene.text("需求详情") != null }
                    assertTrue(calls.isEmpty(), "Body is lazy until its tab opens")
                assertEquals(1, scene.nodes().count { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "任务资料" } == true })
                    assertFalse(scene.textContaining("任务人工说明"))
                    scene.click(scene.text("需求详情")!!)
                    scene.await { scene.textContaining("支付渠道优化正文") }
                    assertFalse(app.participatedWorkItemsController.state.initialized)
                    assertNull(app.participatedWorkItemsController.selectedKey)
                    scene.screenshot("${if (dark) "dark" else "light"}-requirement-$width")
                    scene.click(scene.label("查看 Markdown 源码"))
                    scene.await { scene.labelOrNull("查看 Markdown 预览") != null }
                    scene.sendPointerEvent(PointerEventType.Scroll, Offset(if (width > 960) 850f else 350f, 400f), scrollDelta = Offset(0f, 300f))
                    val reading = app.taskBrowsingSession.requirementFor(app.taskPath(a), a.requirementLink)
                    scene.await { (reading.scrollPositions["source-vertical"] ?: 0) > 0 }
                    app.selectTask(b)
                    scene.await { scene.textContaining("独立读取需求内容") }
                    assertEquals(TaskContentView.REQUIREMENT, app.taskBrowsingSession.view)
                    assertFalse(scene.textContaining("支付渠道优化正文"))
                    val position = reading.scrollPositions["source-vertical"]
                    app.selectTask(a)
                    scene.await { scene.labelOrNull("查看 Markdown 预览") != null }
                    assertEquals(position, reading.scrollPositions["source-vertical"])
                    assertEquals(listOf("1", "2"), calls)
                    scene.click(scene.label("刷新需求正文"))
                    scene.await { synchronized(calls) { calls.count { it == "1" } == 2 } }
                }
            }
        }
    }

    @Test fun `notes tab retains unsaved edits across tasks previews draft and retries failed save`() {
        val tasksRoot = Files.createDirectory(root.resolve("notes-tasks"))
        val a = task("说明任务 A", root)
        val b = task("说明任务 B", root)
        listOf(a, b).forEach { ManifestStore().save(tasksRoot.resolve(it.taskDirectoryName), it) }
        app(AppConfig(taskRoot = tasksRoot.toString())).use { app ->
            app.selectTask(a)
            app.taskBrowsingSession.view = TaskContentView.NOTES
            ImageComposeScene(1200, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface { TasksScreen(app, false) } }
            }.use { scene ->
                scene.await { scene.editable() != null && scene.text("正在读取需求说明…") == null }
                assertTrue(scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(androidx.compose.ui.text.AnnotatedString("手动填写的说明草稿")))
                scene.await { scene.editableText() == "手动填写的说明草稿" }
                scene.screenshot("light-notes-1200")
                val notesFile = AgentDocumentService(ApplicationPaths(root.resolve("documents"))).taskNotesFile(tasksRoot.resolve(a.taskDirectoryName), a)
                val preview = app.previewTaskAgents(a, "手动填写的说明草稿")
                assertTrue(preview.files.any { it.content.contains("手动填写的说明草稿") })
                assertFalse(Files.exists(notesFile), "Preview must not write the task document")
                scene.click(scene.text("任务资料")!!)
                scene.await { scene.editable() == null }
                scene.click(scene.text("需求说明")!!)
                scene.await { scene.editableText() == "手动填写的说明草稿" }
                app.selectTask(b)
                scene.await { scene.editable() != null && scene.text("正在读取需求说明…") == null && scene.editableText() == "" }
                app.selectTask(a)
                scene.await { scene.editableText() == "手动填写的说明草稿" }
                Files.createDirectories(notesFile.parent)
                Files.createDirectory(notesFile)
                scene.click(scene.text("保存")!!)
                scene.await { !app.busy && app.errorMessage != null }
                assertEquals("手动填写的说明草稿", scene.editableText())
                Files.delete(notesFile)
                scene.click(scene.text("保存")!!)
                scene.await { !app.busy && Files.isRegularFile(notesFile) }
                assertTrue(Files.readString(notesFile).contains("手动填写的说明草稿"))
            }
            for (dark in listOf(false, true)) for (width in listOf(580, 1200)) {
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface { Row {
                        Sidebar(app, navigationLayoutFor(width.toFloat())) {}
                        Box(Modifier.weight(1f)) { TasksScreen(app, false) }
                    } } }
                }.use { scene ->
                    scene.await { scene.editableText() == "手动填写的说明草稿" }
                    scene.screenshot("${if (dark) "dark" else "light"}-notes-$width")
                }
            }
        }
    }

    @Test fun `material context menus keep selection and collapse restores list in both themes`() {
        val materialRoot = Files.createDirectory(root.resolve("menus"))
        Files.writeString(materialRoot.resolve("01.txt"), "第一份资料")
        Files.writeString(materialRoot.resolve("02.txt"), "第二份资料")
        Files.createDirectory(materialRoot.resolve("中文文件夹"))
        for (dark in listOf(false, true)) app().use { app ->
            val state = MaterialsBrowserState()
            ImageComposeScene(1100, 650, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface {
                    RequirementMaterialsBrowser(app, task("菜单任务", materialRoot), browserState = state, ioDispatcher = io)
                } }
            }.use { scene ->
                scene.await { scene.text("01.txt") != null }
                scene.click(scene.text("02.txt")!!)
                scene.await { scene.textContaining("第二份资料") }
                val point = scene.text("01.txt")!!.boundsInRoot.center
                scene.sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
                scene.sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Secondary)
                scene.await { scene.text("复制文件") != null }
                assertTrue(scene.textContaining("第二份资料"), "Right click must not navigate the preview")
                assertNotNull(scene.text("复制路径")); assertNotNull(scene.text("删除"))
                assertTrue(scene.text("复制文件")!!.boundsInRoot.width <= 220f)
                scene.screenshot(if (dark) "dark-material-context" else "light-material-context")
                scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown))
                scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyUp))
                scene.await { scene.text("复制文件") == null }
                scene.click(scene.label("折叠文档列表"))
                scene.await { scene.labelOrNull("展开文档列表") != null }
                assertTrue(state.directoryCollapsed.value)
                assertNull(scene.text("01.txt"))
                scene.screenshot(if (dark) "dark-material-collapsed" else "light-material-collapsed")
                scene.click(scene.label("展开文档列表"))
                scene.await { scene.text("01.txt") != null }
                assertTrue(scene.textContaining("第二份资料"))
            }
        }
    }

    @Test fun `image viewer zoom pan fit and close actions render in both themes`() {
        for (dark in listOf(false, true)) {
            var closed = false
            ImageComposeScene(900, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    RequirementImageViewerContent(androidx.compose.ui.graphics.painter.ColorPainter(androidx.compose.ui.graphics.Color(0xFFCCDDEE)), { closed = true }, Modifier.fillMaxSize())
                }
            }.use { scene ->
                scene.await { scene.text("100%") != null }
                scene.click(scene.label("放大图片"))
                scene.await { scene.text("125%") != null }
                scene.sendPointerEvent(PointerEventType.Scroll, Offset(500f, 300f), scrollDelta = Offset(0f, -1f))
                scene.await { scene.text("144%") != null }
                scene.drag(Offset(500f, 300f), 60f)
                scene.screenshot(if (dark) "dark-image-zoom" else "light-image-zoom")
                scene.click(scene.text("适配")!!)
                scene.await { scene.text("100%") != null }
                scene.click(scene.label("关闭图片"))
                assertTrue(closed)
            }
        }
    }

    @Test fun `service editor categories keep bounded height and independent JSON drafts on both themes`() {
        val service = GroupServiceConfig.standard("service", "repo", "silverwing")
        val repository = RepositoryConfig("repo", "silverwing", root.toString(), root.resolve(".git").toString())
        for (dark in listOf(false, true)) for (width in listOf(740, 1200)) app(AppConfig(repositories = listOf(repository)),
            addresses = RepositoryRemoteAddressCatalog { listOf(
                RepositoryRemoteAddress("origin", "https://gitlab.example.invalid/common/silverwing.git", true, true),
                RepositoryRemoteAddress("github", "git@github.com:javatoai/silverwing.git", true, false),
                RepositoryRemoteAddress("github", "ssh://git@github.com:2222/" + "nested/".repeat(8) + "silverwing.git", false, true)) },
            branches = object : RemoteBranchCatalog { override fun list(repository: Path, remote: String) = listOf("$remote/main", "$remote/release/test") },
            remotes = RepositoryRemoteCatalog { listOf("origin", "github") }).use { app ->
            ImageComposeScene(width, 820, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Box {
                    ServiceEditorContent(app, service, serviceEditorBounds(width.toFloat(), 820f), {}, { _, _ -> true }) { _, _, content -> content() }
                } }
            }.use { scene ->
                scene.await { scene.text("服务信息") != null }
                val footer = scene.text("保存配置")!!.boundsInRoot
                assertNull(scene.text("分支默认值"))
                scene.click(scene.text("Git")!!)
                scene.await { scene.text("分支默认值") != null }
                assertNotNull(scene.text("主分支")); assertNotNull(scene.text("测试 Tag 默认目标"))
                assertFalse(scene.textContaining("远程仓库："))
                scene.await { scene.text("github · 拉取") != null }
                scene.screenshot("${if (dark) "dark" else "light"}-service-git-$width")
                scene.click(scene.label("搜索并选择远程分支"))
                scene.await { scene.text("origin/main") != null && scene.text("远程仓库：origin") != null }
                scene.click(scene.text("远程仓库：origin")!!)
                scene.await { scene.text("github") != null }
                scene.click(scene.text("github")!!)
                scene.await { scene.text("github/main") != null }
                assertTrue(scene.nodes().any { it.config.getOrNull(SemanticsProperties.EditableText)?.text == "origin/master" })
                scene.screenshot("${if (dark) "dark" else "light"}-service-branch-$width")
                scene.click(scene.text("github/main")!!)
                scene.await { scene.text("远程仓库：github") == null }
                scene.click(scene.text("Genbu")!!)
                scene.await { scene.text("Genbu 探测") != null }
                assertNull(scene.text("Genbu 服务名"))
                assertEquals(footer, scene.text("保存配置")!!.boundsInRoot)
                scene.click(scene.text("工作区模块")!!)
                scene.await { scene.textContaining("快捷命令") }
                assertEquals(footer, scene.text("保存配置")!!.boundsInRoot)
                scene.click(scene.text("复制规则")!!)
                scene.await { scene.text("高级 JSON") != null }
                scene.click(scene.text("高级 JSON")!!)
                scene.await { scene.editable() != null }
                scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(androidx.compose.ui.text.AnnotatedString("[unfinished"))
                scene.await { scene.editableText() == "[unfinished" }
                scene.click(scene.text("初始化命令")!!)
                scene.await { scene.text("高级 JSON") != null }
                scene.click(scene.text("高级 JSON")!!)
                scene.await { scene.editableText() == "[]" }
                assertEquals(footer, scene.text("保存配置")!!.boundsInRoot)
                scene.click(scene.text("复制规则")!!)
                scene.await { scene.editableText() == "[unfinished" }
                scene.click(scene.text("保存配置")!!)
                scene.await { scene.textContaining("JSON") && scene.editableText() == "[unfinished" }
                assertEquals(footer, scene.text("保存配置")!!.boundsInRoot)
                scene.click(scene.text("开发工具")!!)
                scene.await { scene.text("IntelliJ IDEA") != null }
                scene.click(scene.text("IntelliJ IDEA")!!)
                scene.await { scene.text("WebStorm") != null }
                scene.screenshot("${if (dark) "dark" else "light"}-service-tools-$width")
            }
        }
    }

    @Test fun `rendered markdown image sends its painter to zoom viewer when clicked`() {
        val image = root.resolve("requirement-picture.png")
        val bitmap = java.awt.image.BufferedImage(100, 60, java.awt.image.BufferedImage.TYPE_INT_RGB)
        javax.imageio.ImageIO.write(bitmap, "png", image.toFile())
        var selected: androidx.compose.ui.graphics.painter.Painter? = null
        ImageComposeScene(600, 300, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                MarkdownDocumentPreview("![需求图](${image.toUri()})", MarkdownPreviewMode.RENDERED, Modifier.fillMaxSize(), onImageClick = { selected = it })
            }
        }.use { scene ->
            scene.await { scene.nodes().any { it.config.getOrNull(SemanticsActions.OnClick)?.label == "放大图片" && it.boundsInRoot.height > 10 } }
            scene.click(scene.nodes().first { it.config.getOrNull(SemanticsActions.OnClick)?.label == "放大图片" })
            assertNotNull(selected)
            assertTrue(selected!!.intrinsicSize.width > 0)
        }
    }

    private fun ImageComposeScene.editable() = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.EditableText) != null }
    private fun ImageComposeScene.editableText() = editable()?.config?.getOrNull(SemanticsProperties.EditableText)?.text

    private fun task(name: String, path: Path) = TaskManifest(folderName = name, taskDirectoryName = name, featureBranch = "feat/$name",
        createdAt = "2026-10-01", updatedAt = "2026-10-01", services = emptyList(),
        requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, path.toString()))
    private fun app(config: AppConfig = AppConfig(), source: ParticipatedWorkItemsSource? = null,
        metadata: RequirementMetadataProvider = RequirementMetadataProvider { null },
        addresses: RepositoryRemoteAddressCatalog? = null, branches: RemoteBranchCatalog? = null,
        remotes: RepositoryRemoteCatalog? = null): DesktopApplication {
        val paths = ApplicationPaths(Files.createTempDirectory(root, "app-"))
        val store = ConfigStore(paths).also { it.save(config.copy(aiRequirementNamingEnabled = false)) }
        return DesktopApplication(paths = paths, configStore = store,
            repositoryRemoteAddressCatalog = addresses ?: GitRepositoryRemoteAddressCatalog(),
            remoteBranchCatalog = branches ?: GitRemoteBranchCatalog(),
            repositoryRemoteCatalog = remotes ?: GitRepositoryRemoteCatalog(),
            requirementMetadataProvider = metadata,
            participatedWorkItemsSource = source ?: object : ParticipatedWorkItemsSource {
                override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
                override fun loadBody(item: ParticipatedWorkItem) = ""
            },
            systemFileOpening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication("WPS")
                override suspend fun open(path: Path) = FileOpenResult.Submitted
                override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
            }, developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
    }
    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.Text)?.any { it.text == value } == true }
    private fun SemanticsNode.texts(): List<String> = config.getOrNull(SemanticsProperties.Text)?.map { it.text }.orEmpty() + children.flatMap { it.texts() }
    private fun ImageComposeScene.taskRow(folder: String) = nodes().first { it.config.getOrNull(SemanticsProperties.Role) == Role.Button && folder in it.texts() }
    private fun ImageComposeScene.textContaining(value: String) = nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { it.text.contains(value) } == true }
    private fun ImageComposeScene.labelOrNull(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true }
    private fun ImageComposeScene.label(value: String) = labelOrNull(value) ?: error("Missing $value")
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 8_000_000_000L
        do {
            io.scheduler.runCurrent(); Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.drag(from: Offset, dx: Float) {
        sendPointerEvent(PointerEventType.Move, from)
        sendPointerEvent(PointerEventType.Press, from, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        for (step in 1..8) {
            sendPointerEvent(PointerEventType.Move, from + Offset(dx * step / 8, 0f), buttons = PointerButtons(isPrimaryPressed = true))
            Snapshot.sendApplyNotifications(); repeat(2) { render(System.nanoTime()).close() }
        }
        sendPointerEvent(PointerEventType.Release, from + Offset(dx, 0f), buttons = PointerButtons(), button = PointerButton.Primary)
    }
    private fun ImageComposeScene.screenshot(name: String) {
        // 输入框浮动标签、焦点环的过渡结束后再捕获，避免把中间动画帧当成布局缺陷。
        repeat(22) { Thread.sleep(10); render(System.nanoTime()).close() }
        sendPointerEvent(PointerEventType.Move, Offset(-100f, -100f))
        val output = Path.of("build/reports/task-workspace/$name.png")
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
