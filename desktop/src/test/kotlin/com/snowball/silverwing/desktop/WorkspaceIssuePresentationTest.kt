package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.WorkspaceGitHealth
import com.snowball.silverwing.core.WorkspaceGitHealthState
import com.snowball.silverwing.core.WorkspaceGitIssue
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class WorkspaceIssuePresentationTest {
    @Test
    fun `abnormal workspace status uses a dedicated third row`() {
        val health = WorkspaceGitHealth(
            state = WorkspaceGitHealthState.FAILED,
            issue = WorkspaceGitIssue.BRANCH_MISMATCH,
        )

        assertEquals(WorkspaceStatusPlacement.THIRD_ROW, workspaceStatusPlacement(health))
    }

    @Test
    fun `healthy workspace status uses the normal compact presentation`() {
        val health = WorkspaceGitHealth(state = WorkspaceGitHealthState.READY)

        assertEquals(WorkspaceStatusPlacement.SECOND_ROW, workspaceStatusPlacement(health))
    }

    @Test
    fun `branch mismatch detail is not rendered below its complete status label`() {
        val health = WorkspaceGitHealth(
            state = WorkspaceGitHealthState.FAILED,
            issue = WorkspaceGitIssue.BRANCH_MISMATCH,
            actualBranch = "feature/current",
            expectedBranch = "feature/expected",
            message = "分支不一致：当前 feature/current，期望 feature/expected",
        )

        assertNull(workspaceIssueDetail(health))
    }
}
