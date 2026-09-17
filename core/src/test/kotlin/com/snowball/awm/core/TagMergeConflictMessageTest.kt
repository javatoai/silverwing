package com.snowball.awm.core

import kotlin.test.Test
import kotlin.test.assertEquals

class TagMergeConflictMessageTest {
    @Test
    fun `conflict message names the actual feature to target merge direction`() {
        assertEquals(
            "我在将服务 operation-center 的 feature/task-42 分支合并到 origin/release/test 时遇到了冲突，请你解决。",
            tagMergeConflictMessage("operation-center", "feature/task-42", "origin/release/test"),
        )
    }
}
