package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.WorkspaceGitOperationService
import com.snowball.silverwing.core.WorkspaceGitBatchMode
import com.snowball.silverwing.core.WorkspaceGitChangePreview
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class BatchGitWorkspaceSelectionTest {
    @Test
    fun `selection operates only requested unique physical workspaces`() {
        val first = workspace("first", "D:/tasks/shared")
        val duplicate = workspace("duplicate", "D:/tasks/shared")
        val second = workspace("second", "D:/tasks/second")

        val selected = selectPhysicalWorkspaces(
            listOf(first, duplicate, second),
            setOf(WorkspaceGitOperationService.workspacePathKey(second)),
        )

        assertEquals(listOf("second"), selected.map(ServiceWorkspace::serviceName))
    }

    @Test
    fun `empty and stale selections fail before Git execution`() {
        val workspace = workspace("first", "D:/tasks/first")

        assertFailsWith<IllegalArgumentException> { selectPhysicalWorkspaces(listOf(workspace), emptySet()) }
        assertFailsWith<IllegalArgumentException> { selectPhysicalWorkspaces(listOf(workspace), setOf("D:/tasks/missing")) }
    }

    @Test
    fun `commit and push requires a message for every selected dirty preview`() {
        val dirty = preview("D:/tasks/dirty", listOf(" M source.kt"))
        val clean = preview("D:/tasks/clean", emptyList())
        val previews = mapOf(dirty.workspacePath to dirty, clean.workspacePath to clean)
        val selected = previews.keys

        assertEquals(BatchCommitDisposition.MESSAGE_REQUIRED, batchCommitDisposition(WorkspaceGitBatchMode.COMMIT_AND_PUSH, dirty))
        assertEquals(BatchCommitDisposition.PUSH_ONLY, batchCommitDisposition(WorkspaceGitBatchMode.COMMIT_AND_PUSH, clean))
        assertFalse(batchCommitMessagesValid(WorkspaceGitBatchMode.COMMIT_AND_PUSH, selected, previews, emptyMap()))
        assertTrue(
            batchCommitMessagesValid(
                WorkspaceGitBatchMode.COMMIT_AND_PUSH,
                selected,
                previews,
                mapOf(dirty.workspacePath to "feat: save dirty service"),
            ),
        )
    }

    @Test
    fun `missing preview never degrades commit and push into push only`() {
        assertEquals(BatchCommitDisposition.LOADING, batchCommitDisposition(WorkspaceGitBatchMode.COMMIT_AND_PUSH, null))
        assertFalse(
            batchCommitMessagesValid(
                WorkspaceGitBatchMode.COMMIT_AND_PUSH,
                setOf("D:/tasks/missing"),
                emptyMap(),
                mapOf("D:/tasks/missing" to "feat: should wait"),
            ),
        )
    }

    private fun preview(path: String, files: List<String>) = WorkspaceGitChangePreview(
        workspacePath = path,
        branch = "feature/test",
        head = "abc123",
        upstream = "origin/feature/test",
        files = files,
        diffStat = "",
        fingerprint = "fingerprint-$path",
    )

    private fun workspace(name: String, path: String) = ServiceWorkspace(
        repositoryId = "repo-$name",
        serviceName = name,
        repositoryPath = "D:/repos/$name",
        worktreePath = path,
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = "feature/$name",
    )
}
