@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.use
import androidx.compose.ui.semantics.*
import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class RequirementBodyUiTest {
    @TempDir lateinit var root: Path
    private val io = StandardTestDispatcher()
    @BeforeEach fun useControlledUiDispatcher() { Dispatchers.setMain(io) }
    @AfterEach fun restoreUiDispatcher() { Dispatchers.resetMain() }

    @Test fun `cached requirement copy menu includes image folder and stays disabled until images finish in all layouts`() {
        val item = ParticipatedWorkItem("project", "OBT", "userstory", "需求", "711849", "支付优化", "https://project.feishu.cn/obt/userstory/detail/711849")
        val url = "https://example.com/image.png"
        for (dark in listOf(false, true)) for (width in listOf(320, 900)) {
            val cacheRoot = Files.createTempDirectory(root, "缓存 中文-")
            val image = cacheRoot.resolve("下载.image").also { Files.write(it, java.util.Base64.getDecoder().decode(
                "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l1sAAAAASUVORK5CYII=")) }
            var fail = true; var reads = 0
            val source = object : ParticipatedWorkItemsSource {
                override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
                override fun loadBody(item: ParticipatedWorkItem): String { reads++; return "# 完整需求正文\n\n| 一 | 二 |\n|---|---|\n| 三 | 四 |\n\n![图]($url)" }
                override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path { if (fail) error("图片网络失败"); return image }
            }
            val repository = RequirementBodyRepository(source,
                cacheAccess = RequirementCacheAccess(RequirementReadCache(cacheRoot.resolve("old"))) { "account" },
                documentStore = RequirementDocumentStore(cacheRoot.resolve("docs")))
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
            val reader = RequirementBodyController(repository, scope, io)
            val position = MaterialsReadingState().apply { mode.value = MarkdownPreviewMode.SOURCE }
            val paths = mutableListOf<Path>(); val payloads = mutableListOf<List<Path>>()
            try {
                app(source).use { app ->
                    reader.select(item); io.scheduler.runCurrent()
                    ImageComposeScene(width, 400, coroutineContext = Dispatchers.Unconfined) {
                        SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                            Surface { CompositionLocalProvider(LocalMaterialsReadingState provides position) {
                                Column(Modifier.fillMaxSize()) {
                                    RequirementDocumentPreparationStatus(RequirementDocumentPreparationState(3, 2, 1), {})
                                    RequirementBodyContent(app, reader, item, Modifier.weight(1f),
                                        onCopyDocument = { payloads.add(it.copyPaths()) }, onCopyDocumentPath = { paths.add(it) })
                                }
                            } }
                        }
                    }.use { scene ->
                        scene.await { scene.text("缺少 1 张本地图片，暂不可复制需求文件") != null }
                        assertNotNull(scene.text("全文缓存 2/3"))
                        scene.clickLabel("复制…")
                        scene.await { scene.text("复制需求文件（含图片）") != null }
                        assertNotNull(scene.text("复制需求文件（含图片）")!!.config.getOrNull(SemanticsProperties.Disabled))
                        assertNull(scene.text("复制Markdown源码")!!.config.getOrNull(SemanticsProperties.Disabled))
                        scene.clickText("复制文件路径")
                        assertEquals(listOf(reader.document!!.markdownPath), paths)
                        fail = false; scene.clickText("重试")
                        scene.await { reader.document?.complete == true && scene.text("缺少 1 张本地图片，暂不可复制需求文件") == null }
                        assertEquals(1, reads)
                        scene.clickLabel("复制…")
                        scene.await { scene.text("复制需求文件（含图片）") != null }
                        assertNull(scene.text("复制需求文件（含图片）")!!.config.getOrNull(SemanticsProperties.Disabled))
                        scene.clickText("复制需求文件（含图片）")
                        val document = reader.document!!
                        assertEquals(listOf(document.markdownPath, document.assetsDirectory), payloads.single())
                        assertTrue(scene.nodes().filter { it.boundsInRoot.width > 0 }.all { it.boundsInRoot.right <= width + 1f })
                        val output = Path.of("build/reports/requirement-document/${if (dark) "dark" else "light"}-$width.png")
                        Files.createDirectories(output.parent)
                        scene.render(System.nanoTime()).use { rendered -> rendered.encodeToData()!!.use { Files.write(output, it.bytes) } }
                    }
                }
            } finally { reader.clear(); scope.cancel() }
        }
    }

    @Test fun `many image failures scroll separately preserve readable body and retry only one image`() {
        val item = ParticipatedWorkItem("project", "OBT", "userstory", "需求", "1", "正文", "https://project.feishu.cn/obt/userstory/detail/1")
        val urls = (1..20).map { "https://project.feishu.cn/goapi/v5/platform/file/stream/download/image-$it" }
        val content = "# 正文仍可读\n\n" + urls.joinToString("\n\n") { "![正文图片]($it)" }
        for (dark in listOf(false, true)) for (width in listOf(320, 900)) {
            var bodyReads = 0
            val retries = mutableListOf<String>()
            val source = object : ParticipatedWorkItemsSource {
                override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult =
                    error("The shared body reader must not load the requirements list")
                override fun loadBody(item: ParticipatedWorkItem): String { bodyReads++; return content }
                override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                    if (retry) { retries += fileUrl; return root.resolve("retried.png") }
                    error("图片权限不足 ${fileUrl.substringAfterLast('/')}")
                }
            }
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
            val reader = RequirementBodyController(RequirementBodyRepository(source), scope, io)
            val position = MaterialsReadingState().apply { mode.value = MarkdownPreviewMode.SOURCE }
            try {
                app(source).use { app ->
                    reader.select(item)
                    io.scheduler.runCurrent()
                    // Source mode exercises the error layout without initiating renderer image requests.
                    ImageComposeScene(width, 360, coroutineContext = Dispatchers.Unconfined) {
                        SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                            Surface { CompositionLocalProvider(LocalMaterialsReadingState provides position) {
                                RequirementBodyContent(app, reader, item, Modifier.fillMaxSize())
                            } }
                        }
                    }.use { scene ->
                        scene.await { scene.body() != null && reader.bodyImageStates.size == 20 }
                        assertEquals(20, reader.bodyImageStates.values.count { it is ParticipatedWorkItemImageState.Failed })
                        val bodyBounds = scene.body()!!.boundsInRoot
                        assertTrue(bodyBounds.height >= 100f, "Failed images must leave the body visibly readable: $bodyBounds")
                        assertTrue(bodyBounds.bottom <= 360f)
                        val errors = scene.nodes().first {
                            it.config.getOrNull(SemanticsProperties.VerticalScrollAxisRange)?.maxValue?.invoke()?.let { max -> max > 0 } == true &&
                                it.boundsInRoot.top < bodyBounds.top && it.boundsInRoot.height <= 144f
                        }
                        assertTrue(errors.config[SemanticsActions.ScrollBy].action!!(0f, 600f))
                        scene.await { errors.config[SemanticsProperties.VerticalScrollAxisRange].value() > 0f }
                        assertEquals(bodyBounds.top, scene.body()!!.boundsInRoot.top, 1f, "Scrolling image errors must not move the body")
                        val retry = scene.nodes().first {
                            it.config.getOrNull(SemanticsActions.OnClick)?.action != null &&
                                it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "重试" } == true &&
                                it.boundsInRoot.height > 20f
                        }
                        assertTrue(retry.config[SemanticsActions.OnClick].action!!())
                        scene.await { reader.bodyImageStates.values.count { it is ParticipatedWorkItemImageState.Loaded } == 1 }
                        assertEquals(1, retries.size)
                        assertEquals(19, reader.bodyImageStates.values.count { it is ParticipatedWorkItemImageState.Failed })
                        assertEquals(1, bodyReads)
                        assertTrue(scene.body()!!.boundsInRoot.height >= 100f)
                        val output = Path.of("build/reports/requirement-body/${if (dark) "dark" else "light"}-$width-image-failures.png")
                        Files.createDirectories(output.parent)
                        scene.render(System.nanoTime()).use { image -> image.encodeToData()!!.use { Files.write(output, it.bytes) } }
                    }
                }
            } finally { reader.clear(); scope.cancel() }
        }
    }

    private fun app(source: ParticipatedWorkItemsSource): DesktopApplication {
        val paths = ApplicationPaths(Files.createTempDirectory(root, "app-"))
        val store = ConfigStore(paths).also { it.save(AppConfig(aiRequirementNamingEnabled = false)) }
        return DesktopApplication(paths = paths, configStore = store, participatedWorkItemsSource = source,
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
    private fun ImageComposeScene.clickText(value: String) {
        val target = nodes().first { it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true }
        assertTrue(target.config[SemanticsActions.OnClick].action!!())
    }
    private fun ImageComposeScene.clickLabel(value: String) {
        val target = nodes().first { it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true }
        assertTrue(target.config[SemanticsActions.OnClick].action!!())
    }
    private fun ImageComposeScene.body() = nodes().firstOrNull {
        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text.contains("正文仍可读") } == true
    }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val deadline = System.nanoTime() + 3_000_000_000L
        do {
            io.scheduler.runCurrent(); Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < deadline)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
}
