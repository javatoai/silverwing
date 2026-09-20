package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.WorkspaceStrategy
import kotlin.test.Test
import kotlin.test.assertEquals

class WorkspaceStrategyPresentationTest {
    @Test
    fun `standard worktree uses the concise visible label`() {
        assertEquals("Worktree", WorkspaceStrategy.STANDARD_WORKTREE.displayName)
    }
}
