@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.material3.Surface
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
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
import com.snowball.silverwing.core.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class TaskTagTargetUiTest {
    @TempDir lateinit var root: Path

    @Test fun `failed save retains draft and a successful retry disables duplicate edits and refreshes the task`() = withFixture { f ->
        var closed = 0
        var failed = true
        f.validation = { if (failed) error("远程连接失败") }
        ImageComposeScene(760, 620, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskTagTargetDialog(f.app, f.task, f.workspace) { closed++ } } }
        }.use { scene ->
            f.pump(scene)
            scene.edit("github/release/B")
            f.pump(scene, false)
            scene.click("保存")
            f.pump(scene, false)
            assertTrue(f.app.busy)
            assertTrue(scene.button("取消").config.contains(SemanticsProperties.Disabled))
            assertTrue(scene.button("正在验证并保存…").config.contains(SemanticsProperties.Disabled))
            assertTrue(scene.field().config.contains(SemanticsProperties.Disabled))
            scene.click("正在验证并保存…")
            scene.click("取消")
            scene.sendKeyEvent(KeyEvent(Key.Escape, KeyEventType.KeyDown))
            f.pump(scene)
            assertEquals(1, f.savedTargets.size)
            assertEquals(0, closed)
            assertEquals("github/release/B", scene.field().config[SemanticsProperties.EditableText].text)
            assertTrue(scene.hasText("远程连接失败"))
            assertEquals(null, f.app.errorMessage)
            assertEquals(f.task, ManifestStore().load(f.directory))
            failed = false
            scene.field().config[SemanticsActions.RequestFocus].action!!.invoke()
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown, isCtrlPressed = true)))
            f.pump(scene)
            assertEquals(2, f.savedTargets.size)
            assertEquals(1, closed)
            val updated = f.app.tasks.single().services.single()
            assertEquals("github/release/B", updated.tagTargetRef)
            assertTrue(workspaceTagActionLabel(updated).contains("github/release/B"))
        }
    }

    @Test fun `picker switches remotes without querying other remotes and refresh errors preserve selected draft`() = withFixture { f ->
        ImageComposeScene(800, 800, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) { Surface { TaskTagTargetDialog(f.app, f.task, f.workspace) {} } }
        }.use { scene ->
            f.pump(scene)
            assertEquals(emptyList(), f.queries)
            scene.click("搜索并选择远程分支")
            f.pump(scene)
            assertEquals(listOf("origin"), f.queries)
            scene.click("远程仓库：origin")
            f.pump(scene)
            scene.click("github")
            f.pump(scene)
            assertEquals(listOf("origin", "github"), f.queries)
            // The nested remote menu keeps its popup until its exit animation completes.
            repeat(20) { Thread.sleep(10); f.pump(scene) }
            scene.nodes().last { it.config.getOrNull(SemanticsProperties.EditableText) != null }
                .config[SemanticsActions.RequestFocus].action!!.invoke()
            assertTrue(scene.sendKeyEvent(KeyEvent(Key.Enter, KeyEventType.KeyDown)))
            f.pump(scene)
            assertEquals("github/release/B", scene.field().config[SemanticsProperties.EditableText].text)
            scene.click("搜索并选择远程分支")
            f.pump(scene)
            f.failQueries = true
            scene.click("刷新远程分支")
            f.pump(scene)
            assertEquals(listOf("origin", "github", "github"), f.queries)
            assertTrue(scene.hasText("刷新失败，以下为上次结果"))
            assertTrue(scene.nodes().any { it.config.getOrNull(SemanticsProperties.EditableText)?.text == "github/release/B" })
        }
    }

    @Test fun `long target dialog fits narrow and wide windows in both themes and invalid formats disable save`() {
        for (dark in listOf(false, true)) for (width in listOf(360, 1000)) withFixture { f ->
            val longWorkspace = f.workspace.copy(tagTargetRef = "origin/release/测试分支-" + "long-name-".repeat(18))
            ImageComposeScene(width, 680, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { TaskTagTargetDialog(f.app, f.task, longWorkspace) {} }
                }
            }.use { scene ->
                f.pump(scene)
                for (node in listOf(scene.button("保存"), scene.button("取消"), scene.field())) {
                    val bounds = node.boundsInRoot
                    assertTrue(bounds.width > 0 && bounds.left >= 0 && bounds.right <= width && bounds.bottom <= 680,
                        "Clipped target editor at $width: $bounds")
                }
                val image = Path.of("build/reports/task-tag-target/${if (dark) "dark" else "light"}-$width.png")
                Files.createDirectories(image.parent)
                repeat(20) { Thread.sleep(10); f.pump(scene) }
                scene.render(System.nanoTime()).use { rendered -> rendered.encodeToData()!!.use { Files.write(image, it.bytes) } }
                scene.edit("missing-remote-prefix")
                f.pump(scene, false)
                assertTrue(scene.button("保存").config.contains(SemanticsProperties.Disabled))
                scene.click("保存")
                f.pump(scene)
                assertEquals(emptyList(), f.savedTargets)
            }
        }
    }

    @Test fun `target menu is available only for supported merge mode and archived tasks stay read only`() = withFixture { f ->
        assertTrue(workspaceToolbarPresentationFor(f.workspace, true, false, f.app.config).showTagTargetAction)
        assertFalse(workspaceToolbarPresentationFor(f.workspace, true, false, AppConfig()).showTagTargetAction)
        assertFalse(workspaceToolbarPresentationFor(f.workspace, true, false, f.app.config.copy(tagEnabled = false)).showTagTargetAction)
        assertFalse(workspaceToolbarPresentationFor(f.workspace.copy(tagMode = TagBuildMode.CURRENT_BRANCH), true, false, f.app.config).showTagTargetAction)
        assertFalse(workspaceToolbarPresentationFor(f.workspace, false, false, f.app.config).showTagTargetAction)
        assertFalse(f.app.canBuildTag(f.task.copy(lifecycleStatus = TaskLifecycleStatus.ARCHIVED), f.workspace))
    }

    @Test fun `tag setting persists and hides the whole more button until explicitly enabled`() {
        for (dark in listOf(false, true)) for (width in listOf(360, 1000)) withFixture(allowEditing = false) { f ->
            ImageComposeScene(width, 1000, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        SettingsTagSection(f.app, f.app.config.tagHistoryMaxGroups.toString(), {}, {},
                            f.app.settingsSaveState("tag") == SettingsSaveState.SAVING)
                        WorkspaceCard(f.app, f.task, f.workspace, false, {}, {})
                    } }
                }
            }.use { scene ->
                val setting = "允许在任务详情修改测试目标分支"
                f.pump(scene)
                assertFalse(f.app.config.allowTaskTagTargetEditing)
                assertFalse(scene.hasLabel("服务更多操作"))
                assertTrue(scene.hasLabel(workspaceTagActionLabel(f.workspace)), "Normal Tag building stays available")
                scene.click(setting)
                f.pump(scene)
                assertTrue(f.app.config.allowTaskTagTargetEditing)
                assertTrue(ConfigStore(f.paths).load().allowTaskTagTargetEditing)
                assertTrue(scene.hasLabel("服务更多操作"))
                scene.screenshot(f, "setting-${if (dark) "dark" else "light"}-$width-enabled")
                scene.click("服务更多操作")
                f.pump(scene)
                scene.click("设置测试目标分支…")
                f.pump(scene)
                assertTrue(scene.hasText("设置测试目标分支"))
                // Let the outgoing More popup release pointer input before interacting with the dialog.
                repeat(20) { Thread.sleep(10); f.pump(scene) }
                scene.click("取消")
                repeat(20) { Thread.sleep(10); f.pump(scene) }
                assertFalse(scene.hasText("设置测试目标分支"), "The target dialog must close before returning to settings")
                scene.click(setting)
                f.pump(scene)
                assertFalse(f.app.config.allowTaskTagTargetEditing)
                assertFalse(ConfigStore(f.paths).load().allowTaskTagTargetEditing)
                assertFalse(scene.hasLabel("服务更多操作"))
                assertTrue(scene.hasLabel(workspaceTagActionLabel(f.workspace)))
                assertEquals(f.task, ManifestStore().load(f.directory))
                scene.screenshot(f, "setting-${if (dark) "dark" else "light"}-$width-disabled")
            }
        }
    }

    @Test fun `disabled task target editing rejects a stale save without changing the manifest`() = withFixture(allowEditing = false) { f ->
        var failure: Throwable? = null
        f.app.taskController.updateTagTarget(f.task, f.workspace, "github/release/B",
            onFailure = { failure = it }, onCancelled = {}, onCompleted = { fail("Saving must be blocked") })
        f.io.scheduler.runCurrent()
        assertTrue(failure?.message.orEmpty().contains("Tag 设置"))
        assertEquals(emptyList(), f.savedTargets)
        assertEquals(f.task, ManifestStore().load(f.directory))
        assertNull(f.app.errorMessage)
    }

    private fun withFixture(allowEditing: Boolean = true, block: (Fixture) -> Unit) {
        val io = StandardTestDispatcher()
        Dispatchers.setMain(io)
        try { Fixture(io, allowEditing).use(block) } finally { Dispatchers.resetMain() }
    }

    private inner class Fixture(val io: kotlinx.coroutines.test.TestDispatcher, allowEditing: Boolean) : AutoCloseable {
        val home = Files.createTempDirectory(root, "fixture-")
        val paths = ApplicationPaths(home.resolve("home"))
        val directory = home.resolve("tasks/task")
        val workspace = ServiceWorkspace("repo", "Service", home.resolve("repo").toString(), directory.resolve("repo").toString(),
            DevelopmentToolType.INTELLIJ_IDEA, "feature/task", health = WorkspaceHealth.READY, groupServiceId = "service",
            tagEnabled = true, tagTargetRef = "origin/release/A")
        val task = TaskManifest(folderName = "task", taskDirectoryName = "task", featureBranch = workspace.branch,
            createdAt = "2026-10-03 08:00:00", updatedAt = "2026-10-03 08:00:00", services = listOf(workspace))
        val savedTargets = mutableListOf<String>()
        val queries = mutableListOf<String>()
        var validation: () -> Unit = {}
        var failQueries = false
        val app: DesktopApplication
        init {
            val config = AppConfig(taskRoot = directory.parent.toString(), aiRequirementNamingEnabled = false,
                allowTaskTagTargetEditing = allowEditing,
                repositories = listOf(RepositoryConfig("repo", "Repo", workspace.repositoryPath, home.resolve("repo/.git").toString())),
                groups = listOf(GroupConfig(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME, services = listOf(GroupServiceConfig.standard("service", "repo", "Service")))))
            ManifestStore().save(directory, task)
            val store = ConfigStore(paths).apply { save(config) }
            val lifecycle = object : WorkspaceLifecycle {
                override fun inspectDeleteRisks(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<DeleteRisk> = error("unused")
                override fun requireArchiveSafe(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): Unit = error("unused")
                override fun removeAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest, force: Boolean): WorkspaceRemovalResult = error("unused")
                override fun restoreAll(config: AppConfig, taskDirectory: Path, manifest: TaskManifest): List<ServiceWorkspace> = error("unused")
                override fun validateForMutation(config: AppConfig, taskDirectory: Path, manifest: TaskManifest,
                    workspace: ServiceWorkspace) = WorkspaceMutationTarget(Path.of(workspace.repositoryPath), Path.of(workspace.worktreePath))
            }
            app = DesktopApplication(paths = paths, configStore = store, ioDispatcher = io,
                tasksApplication = TaskApplicationService(lifecycle = lifecycle, operationLock = FileTaskOperationLock(paths),
                    tagTargetValidator = TaskTagTargetValidator { _, target -> savedTargets += target.toString(); validation() }),
                remoteBranchCatalog = object : RemoteBranchCatalog {
                    override fun list(repository: Path, remote: String): List<String> {
                        queries += remote
                        if (failQueries) error("offline")
                        return listOf(if (remote == "origin") "origin/release/A" else "github/release/B")
                    }
                },
                repositoryRemoteCatalog = RepositoryRemoteCatalog { listOf("origin", "github") },
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) })
        }
        fun pump(scene: ImageComposeScene, runIo: Boolean = true) {
            repeat(6) {
                if (runIo) io.scheduler.runCurrent()
                Snapshot.sendApplyNotifications()
                scene.render(System.nanoTime()).close()
            }
        }
        override fun close() = app.close()
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.button(label: String): SemanticsNode = nodes().first {
        it.config.getOrNull(SemanticsActions.OnClick) != null &&
            (it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == label } == true ||
                it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true)
    }
    private fun ImageComposeScene.hasText(text: String): Boolean = nodes().any {
        it.config.getOrNull(SemanticsProperties.Text)?.any { value -> value.text == text } == true
    }
    private fun ImageComposeScene.hasLabel(label: String): Boolean = nodes().any {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(label) == true
    }
    private fun ImageComposeScene.screenshot(f: Fixture, name: String) {
        repeat(20) { Thread.sleep(10); f.pump(this) }
        val path = Path.of("build/reports/task-tag-target/$name.png")
        Files.createDirectories(path.parent)
        render(System.nanoTime()).use { rendered -> rendered.encodeToData()!!.use { Files.write(path, it.bytes) } }
    }
    private fun ImageComposeScene.field(): SemanticsNode = nodes().first { it.config.getOrNull(SemanticsProperties.EditableText) != null }
    private fun ImageComposeScene.edit(value: String) {
        assertTrue(field().config[SemanticsActions.SetText].action!!.invoke(AnnotatedString(value)))
    }
    private fun ImageComposeScene.click(label: String) {
        val point = button(label).boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, buttons = PointerButtons(), button = PointerButton.Primary)
    }
}
