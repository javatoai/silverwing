@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Surface
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.input.key.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.compose.ui.use
import androidx.compose.ui.text.AnnotatedString
import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.resetMain
import org.junit.jupiter.api.io.TempDir
import org.jetbrains.skia.EncodedImageFormat
import kotlin.test.*

class ServiceRemoteAddressesUiTest {
    @TempDir lateinit var temporary: Path

    @Test fun `safe stage hints keep retry visible and clickable in narrow and dark layouts`() {
        val failures = listOf(
            RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.REMOTE_LIST, RepositoryRemoteAddressFailureKind.TIMEOUT),
            RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.FETCH_ADDRESSES, RepositoryRemoteAddressFailureKind.OTHER),
            RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.PUSH_ADDRESSES, RepositoryRemoteAddressFailureKind.TIMEOUT))
        for (failure in failures) for (dark in listOf(false, true)) for (width in listOf(280, 720)) {
            var retries = 0
            ImageComposeScene(width, 180, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface(Modifier.fillMaxSize()) {
                        Box(Modifier.padding(16.dp)) {
                            ServiceRemoteAddresses(RepositoryAddressesState.Failed, { retries++ }, {}, {}, failure = failure)
                        }
                    }
                }
            }.use { scene ->
                repeat(5) { scene.render(System.nanoTime()).close() }
                val nodes = scene.nodes()
                assertTrue(nodes.any { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == failure.message } == true })
                val retry = nodes.first { it.config.getOrNull(SemanticsActions.OnClick) != null &&
                    it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "重试" } == true }
                val bounds = retry.boundsInRoot
                assertTrue(bounds.width > 0 && bounds.height > 0 && bounds.left >= 0 && bounds.right <= width && bounds.bottom <= 180)
                val screenshot = Path.of("build/reports/remote-addresses/failure-${failure.stage}-${if (dark) "dark" else "light"}-$width.png")
                Files.createDirectories(screenshot.parent)
                scene.render(System.nanoTime()).use { image -> image.encodeToData(EncodedImageFormat.PNG)!!.use { Files.write(screenshot, it.bytes) } }
                scene.sendPointerEvent(PointerEventType.Press, bounds.center, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                scene.sendPointerEvent(PointerEventType.Release, bounds.center, buttons = PointerButtons(), button = PointerButton.Primary)
                repeat(3) { scene.render(System.nanoTime()).close() }
                assertEquals(1, retries)
            }
        }
    }

    @Test fun `failed remote reading does not disable commit template editing or saving`() {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try {
            val paths = ApplicationPaths(temporary.resolve("home"))
            val repository = RepositoryConfig("repo", "仓库", temporary.resolve("repo").toString(), temporary.resolve("repo/.git").toString())
            val service = GroupServiceConfig.standard("service", "repo", "服务")
            val store = ConfigStore(paths).apply { save(AppConfig(repositories = listOf(repository),
                groups = listOf(GroupConfig("group", "组", services = listOf(service))))) }
            val failure = RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.PUSH_ADDRESSES, RepositoryRemoteAddressFailureKind.TIMEOUT)
            val saved = mutableListOf<GroupServiceConfig>()
            DesktopApplication(paths = paths, configStore = store, ioDispatcher = io,
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) },
                remoteBranchCatalog = object : RemoteBranchCatalog { override fun list(repository: Path, remote: String) = listOf("$remote/master", "$remote/release/test") },
                repositoryRemoteCatalog = RepositoryRemoteCatalog { listOf("origin") },
                repositoryRemoteAddressCatalog = RepositoryRemoteAddressCatalog { throw RepositoryRemoteAddressReadException(failure) }).use { app ->
                ImageComposeScene(1100, 820, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(ThemePreference.LIGHT) {
                        ServiceEditorContent(app, service, serviceEditorBounds(1100f, 820f), {}, { draft, _ -> saved += draft; true }) { _, _, content -> content() }
                    }
                }.use { scene ->
                    fun pump() {
                        io.scheduler.runCurrent(); Snapshot.sendApplyNotifications()
                        repeat(4) { scene.render(System.nanoTime()).close() }
                    }
                    repeat(3) { pump() }
                    val git = scene.nodes().first { it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "Git" } == true }
                    assertTrue(git.config.getOrNull(SemanticsActions.OnClick)!!.action!!.invoke())
                    repeat(5) { pump() }
                    assertTrue(scene.nodes().any { it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == failure.message } == true })
                    assertFalse(app.busy)
                    val screenshot = Path.of("build/reports/remote-addresses/editor-failed-read.png")
                    Files.createDirectories(screenshot.parent)
                    scene.render(System.nanoTime()).use { image -> image.encodeToData(EncodedImageFormat.PNG)!!.use { Files.write(screenshot, it.bytes) } }
                    val template = scene.nodes().first { it.config.getOrNull(SemanticsActions.SetText) != null &&
                        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "默认提交信息模板" } == true }
                    assertFalse(template.config.contains(SemanticsProperties.Disabled))
                    assertTrue(template.config.getOrNull(SemanticsActions.SetText)!!.action!!.invoke(AnnotatedString("任务 {num} 的修改")))
                    pump()
                    val save = scene.nodes().first { it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == "保存配置" } == true }
                    assertFalse(save.config.contains(SemanticsProperties.Disabled))
                    assertTrue(save.config.getOrNull(SemanticsActions.OnClick)!!.action!!.invoke())
                    assertEquals("任务 {num} 的修改", saved.single().commitMessageTemplate)
                }
            }
        } finally { Dispatchers.resetMain() }
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }

    @Test fun `keyboard focus and Enter open the SSH repository page without its SSH port`() {
        val opened = mutableListOf<String>()
        ImageComposeScene(480, 200, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                Surface(Modifier.fillMaxSize()) {
                    Box(Modifier.padding(16.dp)) {
                        ServiceRemoteAddresses(RepositoryAddressesState.Loaded(listOf(
                            RepositoryRemoteAddress("origin", "ssh://github.com:2222/org/nested/repo.git", true, true))), {}, opened::add, {})
                    }
                }
            }
        }.use { scene ->
            fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
            fun focusedLink() = scene.semanticsOwners.flatMap { walk(it.rootSemanticsNode) }.any {
                it.config.getOrNull(SemanticsActions.OnClick)?.label == "打开远程仓库" &&
                    it.config.getOrNull(SemanticsProperties.Focused) == true
            }
            repeat(5) { scene.render(System.nanoTime()).close() }
            repeat(12) {
                if (!focusedLink()) {
                    scene.sendKeyEvent(KeyEvent(Key.Tab, KeyEventType.KeyDown))
                    scene.sendKeyEvent(KeyEvent(Key.Tab, KeyEventType.KeyUp))
                    repeat(3) { scene.render(System.nanoTime()).close() }
                }
            }
            assertTrue(focusedLink(), "Remote link must be reachable with Tab")
            scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown))
            scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyUp))
            repeat(3) { scene.render(System.nanoTime()).close() }
            assertEquals(listOf("https://github.com/org/nested/repo"), opened)
        }
    }

    @Test fun `remote links and copy remain reachable with long names and obey disabled state`() {
        val http = "https://github.com/very-long-organization/nested%2Fservice-repository-name.git"
        val ssh = "gitlab.example.invalid:organization/nested/repository.git"
        val local = "C:/本地仓库/一个很长的目录名字.git"
        val addresses = listOf(
            RepositoryRemoteAddress("origin", http, true, true),
            RepositoryRemoteAddress("mirror-with-a-very-long-remote-name-that-must-wrap", ssh, true, false),
            RepositoryRemoteAddress("local", local, false, true))
        for (dark in listOf(false, true)) for (width in listOf(280, 720)) for (enabled in listOf(true, false)) {
            val opened = mutableListOf<String>()
            val copied = mutableListOf<String>()
            ImageComposeScene(width, 560, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface(Modifier.fillMaxSize()) {
                        Box(Modifier.padding(16.dp)) { ServiceRemoteAddresses(RepositoryAddressesState.Loaded(addresses), {}, opened::add, copied::add, enabled) }
                    }
                }
            }.use { scene ->
                repeat(5) { scene.render(System.nanoTime()).close() }
                fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
                val nodes = scene.semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
                val links = nodes.filter { it.config.getOrNull(SemanticsActions.OnClick)?.label == "打开远程仓库" }
                val copies = nodes.filter {
                    it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("复制远程地址") == true
                }
                assertEquals(2, links.size)
                assertEquals(3, copies.size)
                for (node in links + copies) {
                    val bounds = node.boundsInRoot
                    assertTrue(bounds.width > 0 && bounds.height > 0 && bounds.left >= 0 && bounds.right <= width && bounds.bottom <= 560,
                        "Remote action clipped at $width: $bounds")
                    assertEquals(!enabled, node.config.contains(SemanticsProperties.Disabled))
                    scene.sendPointerEvent(PointerEventType.Move, bounds.center)
                    scene.sendPointerEvent(PointerEventType.Press, bounds.center,
                        buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
                    scene.sendPointerEvent(PointerEventType.Release, bounds.center,
                        buttons = PointerButtons(), button = PointerButton.Primary)
                    repeat(3) { scene.render(System.nanoTime()).close() }
                }
                if (enabled) {
                    val screenshot = Path.of("build/reports/remote-addresses/${if (dark) "dark" else "light"}-$width.png")
                    Files.createDirectories(screenshot.parent)
                    scene.render(System.nanoTime()).use { rendered ->
                        rendered.encodeToData(EncodedImageFormat.PNG)!!.use { Files.write(screenshot, it.bytes) }
                    }
                }
                assertEquals(if (enabled) listOf("https://github.com/very-long-organization/nested%2Fservice-repository-name", "https://gitlab.example.invalid/organization/nested/repository") else emptyList(), opened)
                assertEquals(if (enabled) listOf(http, ssh, local) else emptyList(), copied,
                    "Copy callbacks at width=$width, dark=$dark, enabled=$enabled; bounds=${copies.map { it.boundsInRoot }}; owners=${scene.semanticsOwners.size}")
            }
        }
    }
}
