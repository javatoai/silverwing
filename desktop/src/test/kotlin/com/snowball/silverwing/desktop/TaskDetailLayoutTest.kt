package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LocalPushState
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.WorkspaceGitCommit
import com.snowball.silverwing.core.WorkspaceFileComparison
import com.snowball.silverwing.core.WorkspaceFileComparisonLine
import com.snowball.silverwing.core.WorkspaceFileComparisonLineKind
import com.snowball.silverwing.core.WorkspaceFileComparisonRow
import com.snowball.silverwing.core.WorkspaceFileContentOrigin
import com.snowball.silverwing.core.WorkspaceFileLanguage
import com.snowball.silverwing.core.WorkspaceFilePreviewContent
import com.snowball.silverwing.core.WorkspaceFilePreviewMode
import com.snowball.silverwing.core.WorkspaceGitFileChange
import com.snowball.silverwing.core.WorkspaceGitFileChangeKind
import com.snowball.silverwing.core.WorkspaceGitFilePreview
import com.snowball.silverwing.core.WorkspaceGitHealth
import com.snowball.silverwing.core.WorkspaceGitHealthState
import com.snowball.silverwing.core.WorkspaceStrategy
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import java.nio.file.Files
import java.time.Instant

class TaskDetailLayoutTest {
    @Test
    fun `ready materials actions belong to the work data group`() {
        val directory = Files.createTempDirectory("SILVERWING-materials-actions-").toFile()
        try {
            assertEquals(RequirementMaterialsActionGroup.WORK_DATA, requirementMaterialsActionGroupFor(directory.absolutePath))
            Files.delete(directory.toPath())
            assertNull(requirementMaterialsActionGroupFor(directory.absolutePath))
        } finally {
            directory.deleteRecursively()
        }
        assertNull(requirementMaterialsActionGroupFor(null))
        assertNull(requirementMaterialsActionGroupFor("   "))
    }

    @Test
    fun `workspace card stays side by side until its visible controls meet the summary minimum`() {
        val actionGroups = listOf(1, 2, 1)
        val minimumWidth = workspaceCardSideBySideMinWidthDp(actionGroups)

        assertEquals(496f, minimumWidth)
        assertEquals(WorkspaceCardLayout.STACKED, workspaceCardLayout(minimumWidth - 0.1f, actionGroups))
        assertEquals(WorkspaceCardLayout.SIDE_BY_SIDE, workspaceCardLayout(minimumWidth, actionGroups))
        assertEquals(WorkspaceCardLayout.SIDE_BY_SIDE, workspaceCardLayout(720f, actionGroups))
    }

    @Test
    fun `workspace card breakpoint only counts currently visible action groups`() {
        val hidden = WorkspaceToolbarPresentation(
            showPathActionGroup = false,
            showGitActionGroup = false,
            showTagAction = false,
            showAddModuleAction = false,
            showRetryAction = false,
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        )
        val allVisible = hidden.copy(
            showPathActionGroup = true,
            showGitActionGroup = true,
            showTagAction = true,
            showAddModuleAction = true,
            showRetryAction = true,
        )

        assertEquals(listOf(1, 1), workspaceCardActionGroupButtonCounts(hidden, customCommandCount = 0))
        assertEquals(listOf(3, 2, 3, 1, 2, 1, 1), workspaceCardActionGroupButtonCounts(allVisible, customCommandCount = 2))
        assertTrue(
            workspaceCardSideBySideMinWidthDp(workspaceCardActionGroupButtonCounts(allVisible, 2)) >
                workspaceCardSideBySideMinWidthDp(workspaceCardActionGroupButtonCounts(hidden, 0)),
        )
    }

    @Test
    fun `wide workspace cards right align actions while stacked cards retain their reading order`() {
        assertEquals(true, workspaceCardActionsAlignToEnd(WorkspaceCardLayout.SIDE_BY_SIDE))
        assertEquals(false, workspaceCardActionsAlignToEnd(WorkspaceCardLayout.STACKED))
    }

    @Test
    fun `workspace card title retains the project and module names`() {
        assertEquals("SILVERWING-test-project · default", workspaceCardTitle("SILVERWING-test-project", "default"))
        assertEquals("SILVERWING-test-project · module-2", workspaceCardTitle("SILVERWING-test-project", "module-2"))
        assertEquals("SILVERWING-test-project", workspaceCardTitle("SILVERWING-test-project", "SILVERWING-test-project"))
    }

    @Test
    fun `project name copy omits the module name`() {
        assertEquals("SILVERWING-test-project", workspaceProjectNameForCopy(" SILVERWING-test-project "))
    }

    @Test
    fun `workspace branch row shows only the verified branch for either workspace strategy`() {
        WorkspaceStrategy.entries.forEach { strategy ->
            val workspace = branchTestWorkspace(strategy)

            assertEquals(
                WorkspaceBranchPresentation("feature/current", "feature/current"),
                workspaceBranchPresentation(workspace, "feature/current"),
            )
        }
    }

    @Test
    fun `unverified branch keeps its suffix while copying only the saved branch`() {
        val workspace = branchTestWorkspace(WorkspaceStrategy.INDEPENDENT_CLONE)
        val expected = WorkspaceBranchPresentation("feature/saved（未验证）", "feature/saved")

        assertEquals(expected, workspaceBranchPresentation(workspace, null))
        assertEquals(expected, workspaceBranchPresentation(workspace, "   "))
    }

    @Test
    fun `commit history uses task master snapshot and maps a clone source to its local origin`() {
        val standard = branchTestWorkspace(WorkspaceStrategy.STANDARD_WORKTREE).copy(baseRef = "upstream/main")
        val clone = branchTestWorkspace(WorkspaceStrategy.INDEPENDENT_CLONE).copy(baseRef = "upstream/main")

        assertEquals(WorkspaceHistoryBaseline("upstream/main", "upstream/main"), workspaceHistoryBaseline(standard, "origin/develop"))
        assertEquals(WorkspaceHistoryBaseline("upstream/main", "origin/main"), workspaceHistoryBaseline(clone, "origin/develop"))
        assertEquals(WorkspaceHistoryBaseline("origin/develop", "origin/develop"), workspaceHistoryBaseline(standard.copy(baseRef = null), "origin/develop"))
        assertNull(workspaceHistoryBaseline(standard.copy(baseRef = null), null))
    }

    @Test
    fun `short branch keeps copy directly after its natural text width`() {
        val allocation = workspaceBranchCopyAllocation(
            availableWidth = 1_000,
            naturalBranchWidth = 420,
            copyWidth = 28,
            gapWidth = 7,
        )

        assertEquals(420, allocation.branchWidth)
        assertEquals(427, allocation.copyX)
    }

    @Test
    fun `long branch reserves the copy action while its text can wrap`() {
        val allocation = workspaceBranchCopyAllocation(
            availableWidth = 500,
            naturalBranchWidth = 800,
            copyWidth = 28,
            gapWidth = 7,
        )

        assertEquals(465, allocation.branchWidth)
        assertEquals(472, allocation.copyX)
    }

    @Test
    fun `ready workspace exposes a concise git status on its third line`() {
        val health = WorkspaceGitHealth(
            state = WorkspaceGitHealthState.READY,
            dirtyFileCount = 0,
            pushState = LocalPushState.REMOTE_BRANCH_MISSING,
        )

        assertEquals(listOf("无未提交", "未发现远程分支"), workspaceGitStatusLabels(health))
    }

    @Test
    fun `commit history follows the push status instead of being separated by trailing space`() {
        assertEquals(
            listOf("无未提交", "已推送", "提交历史"),
            workspaceGitStatusLineLabels(
                WorkspaceGitHealth(
                    state = WorkspaceGitHealthState.READY,
                    pushState = LocalPushState.PUSHED,
                ),
            ),
        )
        assertEquals(
            listOf("检查中"),
            workspaceGitStatusLineLabels(WorkspaceGitHealth(state = WorkspaceGitHealthState.CHECKING)),
        )
    }

    @Test
    fun `only a ready workspace with dirty files exposes the file details action`() {
        assertTrue(
            workspaceGitStatusHasDirtyFiles(
                WorkspaceGitHealth(state = WorkspaceGitHealthState.READY, dirtyFileCount = 1),
            ),
        )
        assertFalse(workspaceGitStatusHasDirtyFiles(WorkspaceGitHealth(state = WorkspaceGitHealthState.READY)))
        assertFalse(workspaceGitStatusHasDirtyFiles(WorkspaceGitHealth(state = WorkspaceGitHealthState.CHECKING, dirtyFileCount = 1)))
        assertFalse(workspaceGitStatusHasDirtyFiles(WorkspaceGitHealth(state = WorkspaceGitHealthState.FAILED, dirtyFileCount = 1)))
        assertFalse(workspaceGitStatusHasDirtyFiles(null))
    }

    @Test
    fun `only a ready workspace exposes the independent commit history action`() {
        assertTrue(workspaceGitStatusHasHistory(WorkspaceGitHealth(state = WorkspaceGitHealthState.READY)))
        assertTrue(workspaceGitStatusHasHistory(WorkspaceGitHealth(state = WorkspaceGitHealthState.READY, dirtyFileCount = 3)))
        assertFalse(workspaceGitStatusHasHistory(WorkspaceGitHealth(state = WorkspaceGitHealthState.CHECKING)))
        assertFalse(workspaceGitStatusHasHistory(WorkspaceGitHealth(state = WorkspaceGitHealthState.FAILED)))
        assertFalse(workspaceGitStatusHasHistory(WorkspaceGitHealth(state = WorkspaceGitHealthState.MISSING)))
        assertFalse(workspaceGitStatusHasHistory(null))
    }

    @Test
    fun `clean status keeps no uncommitted text non actionable while history remains available`() {
        val clean = WorkspaceGitHealth(state = WorkspaceGitHealthState.READY, dirtyFileCount = 0)

        assertEquals(listOf("无未提交", "检查失败"), workspaceGitStatusLabels(clean))
        assertFalse(workspaceGitStatusHasDirtyFiles(clean))
        assertTrue(workspaceGitStatusHasHistory(clean))
    }

    @Test
    fun `commit history summary prioritizes local time committer and subject`() {
        assertEquals(
            "2026-09-14 15:58:05 · Committer · subject",
            workspaceGitCommitHeader(
                WorkspaceGitCommit(
                    shortHash = "abcdef1",
                    committedAt = Instant.parse("2026-09-14T07:58:05Z"),
                    message = "subject\n\nbody",
                    authorName = "Author",
                    committerName = "Committer",
                ),
            ),
        )
    }

    @Test
    fun `commit history exposes committer label and separates subject from body`() {
        val commit = WorkspaceGitCommit(
            shortHash = "abcdef1",
            committedAt = Instant.parse("2026-09-14T07:58:05Z"),
            message = "subject\n\nbody line",
            authorName = "silverwing Tests",
        )

        assertEquals("提交人：silverwing Tests", workspaceGitCommitterLabel(commit))
        assertEquals(
            WorkspaceGitCommitMessageParts("subject", "body line"),
            workspaceGitCommitMessageParts(commit.message),
        )
        assertEquals(
            "未知提交人",
            workspaceGitCommitterName(commit.copy(committerName = "  ")),
        )
        assertEquals(WorkspaceCommitHistoryLayout.STACKED, workspaceCommitHistoryLayout(500f))
        assertEquals(WorkspaceCommitHistoryLayout.HORIZONTAL, workspaceCommitHistoryLayout(900f))
    }

    @Test
    fun `workspace file changes use readable Chinese labels`() {
        assertEquals("修改 · src/Main.kt", workspaceGitFileChangeRow(WorkspaceGitFileChange("src/Main.kt", WorkspaceGitFileChangeKind.MODIFIED)))
        assertEquals("新增 · src/New.kt", workspaceGitFileChangeRow(WorkspaceGitFileChange("src/New.kt", WorkspaceGitFileChangeKind.ADDED)))
        assertEquals("删除 · src/Old.kt", workspaceGitFileChangeRow(WorkspaceGitFileChange("src/Old.kt", WorkspaceGitFileChangeKind.DELETED)))
        assertEquals("重命名 · src/Renamed.kt", workspaceGitFileChangeRow(WorkspaceGitFileChange("src/Renamed.kt", WorkspaceGitFileChangeKind.RENAMED)))
        assertEquals("冲突 · src/Conflict.kt", workspaceGitFileChangeRow(WorkspaceGitFileChange("src/Conflict.kt", WorkspaceGitFileChangeKind.CONFLICTED)))
        assertEquals("未跟踪 · notes.txt", workspaceGitFileChangeRow(WorkspaceGitFileChange("notes.txt", WorkspaceGitFileChangeKind.UNTRACKED)))
    }

    @Test
    fun `workspace preview path copy preserves the complete relative path`() {
        val path = "src/main/java/com/snowballtech/operationcenter/config/socket/MessageEventHandler.java"

        assertEquals(
            path,
            workspaceRelativePathForCopy(WorkspaceGitFileChange(path, WorkspaceGitFileChangeKind.MODIFIED)),
        )
    }

    @Test
    fun `workspace file changes are grouped in the expected order and sorted by path`() {
        val groups = workspaceGitFileChangeGroups(
            listOf(
                WorkspaceGitFileChange("src/z.kt", WorkspaceGitFileChangeKind.MODIFIED),
                WorkspaceGitFileChange("src/B.kt", WorkspaceGitFileChangeKind.UNTRACKED),
                WorkspaceGitFileChange("src/a.kt", WorkspaceGitFileChangeKind.MODIFIED),
                WorkspaceGitFileChange("src/New.kt", WorkspaceGitFileChangeKind.ADDED),
                WorkspaceGitFileChange("src/Old.kt", WorkspaceGitFileChangeKind.DELETED),
                WorkspaceGitFileChange("src/Renamed.kt", WorkspaceGitFileChangeKind.RENAMED),
            ),
        )

        assertEquals(
            listOf(
                WorkspaceGitFileChangeKind.MODIFIED,
                WorkspaceGitFileChangeKind.ADDED,
                WorkspaceGitFileChangeKind.UNTRACKED,
                WorkspaceGitFileChangeKind.DELETED,
                WorkspaceGitFileChangeKind.RENAMED,
            ),
            groups.map(WorkspaceGitFileChangeGroup::kind),
        )
        assertEquals(listOf("src/a.kt", "src/z.kt"), groups[0].changes.map(WorkspaceGitFileChange::path))
        assertEquals(6, groups.sumOf { it.changes.size })
    }

    @Test
    fun `workspace preview defaults to side by side comparison and filters unchanged rows`() {
        val unchanged = WorkspaceFileComparisonRow(
            oldLine = WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.CONTEXT, 1, "package demo;"),
            newLine = WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.CONTEXT, 1, "package demo;"),
        )
        val replacement = WorkspaceFileComparisonRow(
            oldLine = WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.DELETED, 2, "class Old {}"),
            newLine = WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.ADDED, 2, "class New {}"),
        )
        val addition = WorkspaceFileComparisonRow(
            newLine = WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.ADDED, 3, "fun added() = Unit"),
        )
        val deletion = WorkspaceFileComparisonRow(
            oldLine = WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.DELETED, 3, "fun removed() = Unit"),
        )
        val comparison = WorkspaceFileComparison(
            rows = listOf(unchanged, replacement, addition, deletion),
            oldContent = WorkspaceFilePreviewContent("package demo;\nclass Old {}\nfun removed() = Unit", false, WorkspaceFileContentOrigin.HEAD),
            newContent = WorkspaceFilePreviewContent("package demo;\nclass New {}\nfun added() = Unit", false, WorkspaceFileContentOrigin.WORKTREE),
            truncated = false,
        )
        val preview = WorkspaceGitFilePreview(
            language = WorkspaceFileLanguage.JAVA,
            content = WorkspaceFilePreviewContent(
                text = "package demo;\nclass New {}\nfun added() = Unit",
                truncated = false,
                origin = WorkspaceFileContentOrigin.WORKTREE,
            ),
            comparison = comparison,
        )

        assertEquals(WorkspaceFilePreviewMode.COMPARISON, workspacePreviewDisplayedMode(preview, selectedMode = null))
        assertEquals(WorkspaceFilePreviewMode.CONTENT, workspacePreviewDisplayedMode(preview, WorkspaceFilePreviewMode.CONTENT))
        assertEquals(WorkspaceFilePreviewMode.CONTENT, workspacePreviewDisplayedMode(preview.copy(comparison = null), WorkspaceFilePreviewMode.COMPARISON))
        assertEquals(comparison.rows, workspaceComparisonRows(comparison, WorkspaceComparisonDisplayMode.ALL_LINES))
        assertEquals(listOf(replacement, addition, deletion), workspaceComparisonRows(comparison, WorkspaceComparisonDisplayMode.CHANGED_LINES))
    }

    @Test
    fun `workspace preview highlights representative Java properties HTML and CSS tokens`() {
        val java = workspaceSyntaxTokens(WorkspaceFileLanguage.JAVA, "@Service public class Demo { String name = \"sample\"; }")
        assertTrue(java.any { it.kind == WorkspaceSyntaxTokenKind.ANNOTATION })
        assertTrue(java.any { it.kind == WorkspaceSyntaxTokenKind.KEYWORD })
        assertTrue(java.any { it.kind == WorkspaceSyntaxTokenKind.STRING })

        val properties = workspaceSyntaxTokens(WorkspaceFileLanguage.PROPERTIES, "feature.enabled = true")
        assertTrue(properties.any { it.kind == WorkspaceSyntaxTokenKind.PROPERTY_KEY })
        assertTrue(properties.any { it.kind == WorkspaceSyntaxTokenKind.STRING })

        val html = workspaceSyntaxTokens(WorkspaceFileLanguage.HTML, "<section class=\"card\">")
        assertTrue(html.any { it.kind == WorkspaceSyntaxTokenKind.TAG })
        assertTrue(html.any { it.kind == WorkspaceSyntaxTokenKind.ATTRIBUTE })
        assertTrue(html.any { it.kind == WorkspaceSyntaxTokenKind.STRING })

        val css = workspaceSyntaxTokens(WorkspaceFileLanguage.CSS, "color: \"red\";")
        assertTrue(css.any { it.kind == WorkspaceSyntaxTokenKind.PROPERTY_KEY })
        assertTrue(css.any { it.kind == WorkspaceSyntaxTokenKind.STRING })

        val sql = workspaceSyntaxTokens(WorkspaceFileLanguage.SQL, "SELECT id FROM audit WHERE version = 12")
        assertTrue(sql.any { it.kind == WorkspaceSyntaxTokenKind.KEYWORD })
        assertTrue(sql.any { it.kind == WorkspaceSyntaxTokenKind.NUMBER })
    }

    @Test
    fun `problem workspace retains a dedicated repair row`() {
        val health = WorkspaceGitHealth(
            state = WorkspaceGitHealthState.FAILED,
            dirtyFileCount = 0,
            pushState = LocalPushState.FAILED,
        )
        assertEquals(WorkspaceStatusPlacement.THIRD_ROW, workspaceStatusPlacement(health))
    }

    private fun branchTestWorkspace(strategy: WorkspaceStrategy) = ServiceWorkspace(
        repositoryId = "repo-service",
        serviceName = "service",
        repositoryPath = "/tmp/service",
        worktreePath = "/tmp/task/service",
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = "feature/saved",
        strategy = strategy,
    )

}
