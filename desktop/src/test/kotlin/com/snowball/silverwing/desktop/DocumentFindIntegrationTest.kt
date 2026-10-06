@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.*
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.key.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.platform.ClipboardManager
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class DocumentFindIntegrationTest {
    @TempDir lateinit var root: Path
    private val io = StandardTestDispatcher()
    @Test fun `find reveals the whole block when a renderer supplies only a segment layout`() {
        val find = DocumentFindState()
        val scroll = ScrollState(0)
        ImageComposeScene(400, 240, coroutineContext = Dispatchers.Unconfined) {
            MaterialTheme {
                DocumentFindScope(find, Modifier.fillMaxSize()) {
                    Column(Modifier.fillMaxSize().verticalScroll(scroll)) {
                        Spacer(Modifier.height(600.dp))
                        // Simulate Markdown's image promotion: the registered paragraph
                        // remains complete while the layout callback reports a text slice.
                        val (_, binding) = searchableText(AnnotatedString("target full paragraph"))
                        Text("visible segment", binding.first, onTextLayout = binding.second)
                    }
                }
            }
        }.use { scene ->
            scene.await { find.blocks.values.any { it.text == "target full paragraph" } }
            find.show(); find.change("target")
            scene.await { scroll.value > 0 }
            assertEquals(1, find.hits.size)
        }
    }

    @Test fun `refreshing a matched block reruns navigation even when the hit range is unchanged`() {
        val find = DocumentFindState()
        val navigations = mutableListOf<String>()
        find.blocks["content"] = FindBlock("match", 0) { navigations += "before" }
        ImageComposeScene(400, 240, coroutineContext = Dispatchers.Unconfined) {
            MaterialTheme { DocumentFindScope(find) { DocumentFindBar(find) } }
        }.use { scene ->
            find.show(); find.change("match")
            scene.await { navigations == listOf("before") }
            find.blocks["content"] = FindBlock("match refreshed", 0) { navigations += "after" }
            scene.await { navigations == listOf("before", "after") }
            find.close()
            find.blocks["content"] = FindBlock("match closed", 0) { navigations += "closed" }
            repeat(3) { scene.render(System.nanoTime()).close() }
            assertEquals(listOf("before", "after"), navigations)
        }
    }

    @Test fun `rendered document find covers heading paragraph code and table then switches to source`() {
        val content = "# 支付说明\n\n正文 **支付** 与 `支付`。\n\n| 文件 | 内容 |\n| --- | --- |\n| 说明 | 支付表格 |\n\n```txt\n支付代码\n```"
        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            val find = DocumentFindState()
            ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { DocumentFindScope(find, Modifier.fillMaxSize()) { MarkdownDocumentPreview(content, Modifier.fillMaxSize()) } }
                }
            }.use { scene ->
                scene.await { find.blocks.values.any { it.text.contains("支付代码") } }
                assertTrue(find.blocks.values.none { it.text.contains("**") }, "Rendered find must use displayed text")
                scene.click(scene.label("查找正文 (Ctrl+F)"))
                scene.await { find.open && scene.editable() != null }
                scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("支付"))
                scene.await { find.hits.size == 5 }
                val old = find.current
                scene.sendKeyEvent(KeyEvent(Key.F3, KeyEventType.KeyDown))
                scene.await { find.current != old }
                scene.screenshot("find-${if (dark) "dark" else "light"}-$width")
                scene.click(scene.label("关闭查找 (Esc)")); scene.await { !find.open }
                scene.click(scene.label("查看 Markdown 源码"))
                scene.await { find.blocks.values.any { it.text == content } }
                scene.clickAt(Offset(200f, 540f))
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.F, KeyEventType.KeyDown, isCtrlPressed = true)))
                scene.await { find.open }
                find.change("**支付**")
                scene.await { find.hits.size == 1 }
            }
        }
    }
    @Test fun `PDF find reaches all pages and glyph selection supports context and keyboard copy`() {
        writePreviewPdf(root.resolve("two.pdf"))
        org.apache.pdfbox.Loader.loadPDF(root.resolve("two.pdf").toFile()).use { document ->
            val outline = org.apache.pdfbox.pdmodel.interactive.documentnavigation.outline.PDDocumentOutline()
            document.documentCatalog.documentOutline = outline
            outline.addLast(org.apache.pdfbox.pdmodel.interactive.documentnavigation.outline.PDOutlineItem().apply {
                title = "第二页"
                destination = org.apache.pdfbox.pdmodel.interactive.documentnavigation.destination.PDPageFitDestination().apply { page = document.getPage(1) }
            })
            document.save(root.resolve("with-outline.pdf").toFile())
        }
        Files.move(root.resolve("with-outline.pdf"), root.resolve("two.pdf"), java.nio.file.StandardCopyOption.REPLACE_EXISTING)
        val pdf = RequirementMaterialsPdfService().open(root, "two.pdf")
        val index = pdf.textIndex()
        val selection = PdfTextSelection()
        var copied: AnnotatedString? = null
        val clipboard = object : ClipboardManager {
            override fun setText(annotatedString: AnnotatedString) { copied = annotatedString }
            override fun getText() = copied
        }
        val find = DocumentFindState()
        ImageComposeScene(700, 640, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { CompositionLocalProvider(LocalClipboardManager provides clipboard) {
                Surface { DocumentFindScope(find, Modifier.fillMaxSize()) { Column {
                    DocumentFindBar(find)
                    RequirementMaterialsPdfPreview(root, "two.pdf", 0, Modifier.weight(1f).fillMaxWidth(), io)
                } } }
            } }
        }.use { scene ->
            scene.await { find.blocks.keys.containsAll(listOf("pdf:0", "pdf:1")) }
            scene.click(scene.label("PDF 目录书签"))
            scene.await { scene.text("第二页") != null }
            scene.click(scene.text("第二页")!!)
            scene.await { scene.text("2 / 2 页") != null }
            find.show(); find.change("Requirement")
            scene.await { find.hits.size == 2 }
            find.move(1)
            scene.await { scene.text("2 / 2 页") != null && scene.labelOrNull("PDF 第 2 页，共 2 页") != null }
            scene.screenshot("pdf-find")
        }
        ImageComposeScene(600, 500, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { CompositionLocalProvider(LocalClipboardManager provides clipboard) {
                Surface { PdfTextOverlay(index, 0, selection) }
            } }
        }.use { scene ->
            scene.await { scene.labelOrNull("PDF 文字层，第 1 页") != null }
            val layer = scene.label("PDF 文字层，第 1 页")
            assertTrue(layer.config[SemanticsActions.SetSelection].action!!.invoke(0, 11, false))
            scene.await { selection.text(index) == "Requirement" }
            val first = index.pages[0].glyphs.first().bounds
            val point = Offset((first.left + first.right) / 2 * 600, (first.top + first.bottom) / 2 * 500)
            scene.clickAt(point)
            layer.config[SemanticsActions.SetSelection].action!!.invoke(0, 11, false)
            scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true))
            scene.await { copied?.text == "Requirement" }
            scene.sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isSecondaryPressed = true), button = PointerButton.Secondary)
            scene.sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Secondary)
            scene.await { scene.text("复制") != null }
            copied = null
            scene.click(scene.text("复制")!!)
            scene.await { copied?.text == "Requirement" && scene.text("复制") == null }
            scene.screenshot("pdf-selected")
        }
    }
    @Test fun `quick picker supports path matching and keyboard choice`() {
        val files = listOf(RequirementMaterialsMarkdownFile("研发/支付说明.md", 12), RequirementMaterialsMarkdownFile("设计/支付流程.pdf", 50))
        var selected: String? = null
        ImageComposeScene(700, 620, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { MaterialsQuickPicker(files, {}) { selected = it.relativePath } }
        }.use { scene ->
            scene.await { scene.editable() != null }
            scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("支付"))
            scene.await { scene.text("支付流程.pdf") != null }
            scene.await { scene.editable()!!.config.getOrNull(SemanticsProperties.Focused) == true }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.DirectionDown, KeyEventType.KeyDown)))
            repeat(3) { scene.render(System.nanoTime()).close() }
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown)))
            assertEquals("设计/支付流程.pdf", selected)
        }
        assertEquals(listOf(files[0]), filterMaterialFiles(files, "研发/"))
    }
    @Test fun `collapsed document header stays compact and file picker changes the document`() {
        Files.writeString(root.resolve("说明.md"), "# 支付说明\n\n资料内容")
        Files.writeString(root.resolve("其他.txt"), "另一份内容")
        val paths = ApplicationPaths(root.resolve("application"))
        val store = ConfigStore(paths).also { it.save(AppConfig(aiRequirementNamingEnabled = false)) }
        DesktopApplication(paths = paths, configStore = store,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) },
            systemFileOpening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication("WPS")
                override suspend fun open(path: Path) = FileOpenResult.Submitted
                override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
            }).use { app ->
            val task = TaskManifest(folderName = "支付", taskDirectoryName = "支付", featureBranch = "feat/payment", createdAt = "now", updatedAt = "now", services = emptyList(),
                requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, root.toString()))
            for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
                val state = MaterialsBrowserState().apply { selectedPath.value = "说明.md"; directoryCollapsed.value = true }
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface { RequirementMaterialsBrowser(app, task, browserState = state, ioDispatcher = io) } }
                }.use { scene ->
                    scene.await { scene.text("说明.md") != null && scene.labelOrNull("查看 Markdown 源码") != null && scene.text("支付说明") != null && scene.text("用 WPS 打开") != null }
                    assertEquals(1, scene.nodes().count { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "说明.md" } == true })
                    for (label in listOf("展开文档列表", "快速打开文件 (Ctrl+P)", "查找正文 (Ctrl+F)", "刷新任务资料目录")) {
                        val bounds = scene.label(label).boundsInRoot
                        assertTrue(bounds.left >= 0 && bounds.right <= width, "$label must fit width=$width: $bounds")
                    }
                    scene.screenshot("header-${if (dark) "dark" else "light"}-$width")
                    scene.clickAt(Offset(200f, 520f))
                    assertTrue(scene.sendKeyEvent(KeyEvent(Key.P, KeyEventType.KeyDown, isCtrlPressed = true)))
                    scene.await { scene.text("快速打开文件") != null && scene.editable() != null }
                    scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("其他"))
                    scene.await { scene.text("其他.txt") != null }
                    scene.click(scene.text("其他.txt")!!)
                    scene.await { state.selectedPath.value == "其他.txt" && scene.text("另一份内容") != null && scene.text("快速打开文件") == null }
                    // 弹窗退出与文件头异步关联查询完成后，使用最终位置点击恢复入口。
                    repeat(22) { Thread.sleep(10); scene.render(System.nanoTime()).close() }
                    scene.click(scene.label("展开文档列表"))
                    scene.await { !state.directoryCollapsed.value }
                }
            }
        }
    }
    @Test fun `prewarm failed item controls fit narrow width in light and dark themes`() = kotlinx.coroutines.runBlocking {
        val scope = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.SupervisorJob() + Dispatchers.Default)
        val item = ParticipatedWorkItem("obt", "项目", "story", "需求", "123", "支付渠道优化", "https://project.feishu.cn/obt/userstory/detail/123")
        val coordinator = RequirementAiNamingCoordinator(scope, LocalRequirementAiNamingCache(root.resolve("names.json")),
            RequirementAiContextProvider { _, _ -> error("需求正文暂时读取失败，请重试") },
            object : RequirementAiNamingService { override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) = RequirementAiNamingSuggestion("支付优化", "payment_fix") }, { "gpt-6-luna" })
        try {
            coordinator.prewarm(listOf(item), true)
            kotlinx.coroutines.withTimeout(5000) { while (coordinator.state.value.running) kotlinx.coroutines.delay(10) }
            for (dark in listOf(false, true)) ImageComposeScene(360, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) { Surface { Column { NamingPreparationControls(coordinator) } } }
            }.use { scene ->
                scene.await { scene.text("查看失败") != null }
                scene.click(scene.text("查看失败")!!)
                scene.await { scene.text("支付渠道优化") != null && scene.text("重试") != null }
                scene.screenshot("prewarm-${if (dark) "dark" else "light"}")
            }
        } finally { scope.coroutineContext[kotlinx.coroutines.Job]!!.cancel() }
    }
    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.editable() = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.EditableText) != null }
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.Text)?.any { it.text == value } == true }
    private fun ImageComposeScene.labelOrNull(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true }
    private fun ImageComposeScene.label(value: String) = labelOrNull(value) ?: error("Missing $value: " + nodes().joinToString { it.config.toString() })
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 8_000_000_000L
        do { io.scheduler.runCurrent(); Snapshot.sendApplyNotifications(); repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) = clickAt(node.boundsInRoot.center)
    private fun ImageComposeScene.clickAt(point: Offset) {
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.screenshot(name: String) {
        repeat(22) { Thread.sleep(10); render(System.nanoTime()).close() }
        val output = Path.of("build/reports/reading-optimization/$name.png")
        Files.createDirectories(output.parent)
        render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
    }
}
