@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.*
import androidx.compose.ui.use
import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class RequirementCommentsUiTest {
    @TempDir lateinit var root: Path
    private val io = StandardTestDispatcher()
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "123", "支付优化", "https://project.feishu.cn/obt/userstory/detail/123")
    @BeforeEach fun useUiDispatcher() { Dispatchers.setMain(io) }
    @AfterEach fun restoreUiDispatcher() { Dispatchers.resetMain() }
    private fun comments(text: String) = RequirementComments(listOf(RequirementComment("1", text, "user", "测试作者", "2026-10-06 12:00:00", attachmentUrl = "https://example.com/comment.pdf")))
    private fun source(read: () -> RequirementComments) = object : ParticipatedWorkItemsSource {
        override val supportsComments = true
        override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
        override fun loadBody(item: ParticipatedWorkItem) = "# 正文仍可读\n\n正文独立显示。"
        override fun loadComments(item: ParticipatedWorkItem) = read()
    }
    @Test fun `comments tab is readable in all layouts failure preserves cached text and body source mode survives switching`() {
        for (dark in listOf(false, true)) for (width in listOf(320, 900)) {
            var fail = false; var reads = 0
            val source = source { reads++; if (fail) error("网络断开"); comments("完整评论文本，正文和评论分别阅读。") }
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
            val repository = RequirementCommentsRepository(source, RequirementCommentsStore(Files.createTempDirectory(root, "comments-")), scope)
            val reader = RequirementBodyController(RequirementBodyRepository(source), scope, io, repository)
            val bodyPosition = MaterialsReadingState().apply { mode.value = MarkdownPreviewMode.SOURCE }
            try {
                app(source).use { app ->
                    reader.select(item); io.scheduler.runCurrent()
                    ImageComposeScene(width, 500, coroutineContext = Dispatchers.Unconfined) {
                        SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                            Surface { CompositionLocalProvider(LocalMaterialsReadingState provides bodyPosition) {
                                RequirementBodyContent(app, reader, item, Modifier.fillMaxSize())
                            } }
                        }
                    }.use { scene ->
                        scene.await { scene.textContaining("正文仍可读") != null && scene.text("评论（1）") != null }
                        scene.clickText("评论（1）")
                        scene.await { scene.textContaining("完整评论文本") != null }
                        assertNotNull(scene.textContaining("测试作者")); assertNotNull(scene.textContaining("2026-10-06 12:00:00"))
                        assertNull(scene.textContaining("本地缓存")); assertEquals(MarkdownPreviewMode.SOURCE, bodyPosition.mode.value)
                        fail = true; scene.clickLabel("刷新评论")
                        scene.await { scene.textContaining("已保留本地评论") != null }
                        assertNotNull(scene.textContaining("完整评论文本")); assertNotNull(scene.text("重试评论"))
                        val output = Path.of("build/reports/requirement-comments/${if (dark) "dark" else "light"}-$width.png")
                        Files.createDirectories(output.parent)
                        scene.render(System.nanoTime()).use { rendered -> rendered.encodeToData()!!.use { Files.write(output, it.bytes) } }
                        fail = false; scene.clickText("重试评论")
                        scene.await { reader.commentsReader!!.error == null && !reader.commentsReader.loading }
                        scene.clickText("正文"); scene.await { scene.textContaining("正文仍可读") != null }
                        assertEquals(MarkdownPreviewMode.SOURCE, bodyPosition.mode.value)
                        assertEquals(3, reads)
                    }
                }
            } finally { reader.clear(); scope.cancel() }
        }
    }
    @Test fun `empty comments show an empty state and copying uses actual cache path and full text`() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
        var data = RequirementComments(emptyList())
        val repository = RequirementCommentsRepository(source { data }, RequirementCommentsStore(root.resolve("comments")), scope)
        val reader = RequirementCommentsController(repository, scope, io)
        val copied = mutableListOf<String>(); val paths = mutableListOf<Path>(); val files = mutableListOf<Path>()
        try {
            reader.select(item); io.scheduler.runCurrent()
            ImageComposeScene(700, 500, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) { Surface {
                    RequirementCommentsContent(reader, item, Modifier.fillMaxSize(), copied::add, paths::add, files::add)
                } }
            }.use { scene ->
                scene.await { scene.text("暂无评论") != null }
                data = comments("首行\n" + "完整内容".repeat(200) + "\n末行")
                scene.clickLabel("刷新评论"); scene.await { scene.textContaining("首行") != null }
                scene.clickLabel("复制…"); scene.await { scene.text("复制文件路径") != null }; scene.clickText("复制文件路径")
                assertEquals(listOf(reader.document!!.path), paths)
                scene.clickLabel("复制…"); scene.await { scene.text("复制评论缓存文件") != null }; scene.clickText("复制评论缓存文件")
                assertEquals(listOf(reader.document!!.path), files)
                scene.clickLabel("复制…"); scene.await { scene.text("复制Markdown源码") != null }; scene.clickText("复制Markdown源码")
                assertTrue(copied.single().contains(data.comments.single().content))
                assertTrue(copied.single().contains("查看评论附件"))
            }
        } finally { reader.clear(); scope.cancel() }
    }
    @Test fun `comment labels are escaped and unsafe attachment protocols are never rendered as links`() {
        val data = RequirementComments(listOf(RequirementComment("1", "正文", "user", "名字 [标签]\n下一行", "时间",
            attachmentUrl = "file:///secret")))
        val markdown = requirementCommentsMarkdown(data)
        assertTrue(markdown.contains("名字 \\[标签\\] 下一行")); assertFalse(markdown.contains("](<file:"))
        assertTrue(markdown.contains("附件：file:///secret"))
    }
    private fun app(source: ParticipatedWorkItemsSource): DesktopApplication {
        val paths = ApplicationPaths(Files.createTempDirectory(root, "app-"))
        val config = ConfigStore(paths).also { it.save(AppConfig(aiRequirementNamingEnabled = false)) }
        return DesktopApplication(paths = paths, configStore = config, participatedWorkItemsSource = source,
            requirementMetadataProvider = RequirementMetadataProvider { null },
            systemFileOpening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication()
                override suspend fun open(path: Path) = FileOpenResult.Submitted
                override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
            }, developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
    }
    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.text(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true }
    private fun ImageComposeScene.textContaining(value: String) = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text.contains(value) } == true }
    private fun ImageComposeScene.clickText(value: String) {
        val target = nodes().first { it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true }
        assertTrue(target.config[SemanticsActions.OnClick].action!!())
    }
    private fun ImageComposeScene.clickLabel(value: String) {
        val target = nodes().first { it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true }
        assertTrue(target.config[SemanticsActions.OnClick].action!!())
    }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val deadline = System.nanoTime() + 3_000_000_000L
        do {
            io.scheduler.runCurrent(); Snapshot.sendApplyNotifications(); repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < deadline)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
}
