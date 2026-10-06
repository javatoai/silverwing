@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.Dispatchers
import kotlin.test.*

class WorkspaceMainBranchMergeUiTest {
    private val workspace = ServiceWorkspace("repo", "payments", "repository", "tasks/task/backend", DevelopmentToolType.INTELLIJ_IDEA,
        "feature/task", health = WorkspaceHealth.READY, baseRef = "github/master")
    private val task = TaskManifest(folderName = "task", taskDirectoryName = "task", featureBranch = "feature/task", createdAt = "one", updatedAt = "one", services = listOf(workspace))
    private val source = WorkspaceMainBranch("github/master", "github/master")

    @Test fun `merge action and complete conflict information stay usable in narrow light and dark layouts`() {
        val result = WorkspaceMainBranchMergeResult(workspace.worktreePath, "payments", "backend", "github/master", "feature/task",
            WorkspaceMainBranchMergeOutcome.CONFLICT, tagMergeConflictMessage("payments · backend", "github/master", "feature/task"),
            listOf("src/main/支付业务逻辑.kt", "README.md"))
        for (width in listOf(320, 1000)) for (theme in listOf(ThemePreference.LIGHT, ThemePreference.DARK)) {
            var clicks = 0
            var copied = ""
            var dismissed = false
            ImageComposeScene(width, 620, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(theme) { Surface { Column(Modifier.fillMaxSize().padding(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    GitActionIconGroup(enabled = true, scopeLabel = "backend", onCommit = {}, onCommitAndPush = {}, onPush = {}, trailingActions = {
                        WorkspaceMainBranchMergeAction(workspaceMainBranchMergeActionLabel(source, workspace), true, false) { clicks++ }
                    })
                    WorkspaceMainBranchMergeResultPanel(result, onCopy = { copied = it }, onDismiss = { dismissed = true })
                } } }
            }.use { scene ->
                scene.renderFrames()
                val action = scene.label(workspaceMainBranchMergeActionLabel(source, workspace))
                assertTrue(action.boundsInRoot.right <= width)
                scene.click(action)
                assertEquals(1, clicks)
                scene.click(scene.label("复制冲突信息"))
                assertEquals(workspaceMainBranchMergeCopyText(result), copied)
                assertTrue(copied.contains("github/master → feature/task"))
                assertTrue(result.conflictFiles.all(copied::contains))
                scene.click(scene.label("关闭合并结果"))
                assertTrue(dismissed)
                assertTrue(scene.nodes().filter { it.config.getOrNull(SemanticsProperties.Text)?.isNotEmpty() == true }.all {
                    it.boundsInRoot.left >= 0 && it.boundsInRoot.right <= width && it.boundsInRoot.bottom < 620
                })
                val output = Path.of("build/reports/workspace-main-branch-merge/${theme.name.lowercase()}-$width.png")
                Files.createDirectories(output.parent)
                scene.render(System.nanoTime()).use { bitmap -> bitmap.encodeToData()!!.use { Files.write(output, it.bytes) } }
            }
        }
    }

    @Test fun `loading and disabled merge controls cannot be activated by pointer`() {
        for (loading in listOf(false, true)) {
            var clicks = 0
            ImageComposeScene(200, 100, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT) {
                    WorkspaceMainBranchMergeAction("合并", enabled = false, loading = loading) { clicks++ }
                }
            }.use { scene ->
                scene.renderFrames()
                scene.click(scene.label("合并"))
                assertEquals(0, clicks)
                assertNotNull(scene.label("合并").config.getOrNull(SemanticsProperties.Disabled))
            }
        }
    }

    @Test fun `archived missing protected and mismatched workspaces disable action but existing merge remains explainable`() {
        val ready = WorkspaceGitHealth(WorkspaceGitHealthState.READY, actualBranch = workspace.branch)
        fun enabled(t: TaskManifest = task, w: ServiceWorkspace = workspace, h: WorkspaceGitHealth? = ready, blocked: List<String> = emptyList(), busy: Boolean = false) =
            workspaceMainBranchMergeEnabled(t, w, source, h, blocked, busy)
        assertTrue(enabled())
        assertFalse(enabled(t = task.copy(lifecycleStatus = TaskLifecycleStatus.ARCHIVED)))
        assertFalse(enabled(w = workspace.copy(health = WorkspaceHealth.FAILED)))
        assertFalse(enabled(h = WorkspaceGitHealth(WorkspaceGitHealthState.MISSING)))
        assertFalse(enabled(h = ready.copy(actualBranch = "feature/other")))
        assertFalse(enabled(blocked = listOf("feature/task")))
        assertFalse(enabled(busy = true))
        assertTrue(enabled(h = WorkspaceGitHealth(WorkspaceGitHealthState.FAILED, issue = WorkspaceGitIssue.OPERATION_IN_PROGRESS)))
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.label(value: String) = nodes().first {
        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true && it.config.getOrNull(SemanticsActions.OnClick) != null
    }
    private fun ImageComposeScene.renderFrames() {
        Snapshot.sendApplyNotifications()
        repeat(6) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.click(node: SemanticsNode) {
        val point = node.boundsInRoot.center
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Primary)
        renderFrames()
    }
}
