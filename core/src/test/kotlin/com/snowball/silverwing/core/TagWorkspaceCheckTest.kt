package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals

class TagWorkspaceCheckTest {
    @Test
    fun `worktree check labels staged unstaged untracked and conflicted paths`() {
        assertEquals(
            listOf(
                "已暂存：src/Added.kt",
                "未暂存：src/Edited.kt",
                "未跟踪：.idea/workspace.xml",
                "未解决冲突：src/Conflict.kt",
            ),
            tagWorkspaceChanges(
                """
                A  src/Added.kt
                 M src/Edited.kt
                ?? .idea/workspace.xml
                UU src/Conflict.kt
                """.trimIndent(),
            ),
        )
    }

    @Test
    fun `worktree check reports no paths for a clean status`() {
        assertEquals(emptyList(), tagWorkspaceChanges(""))
    }
}
