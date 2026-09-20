package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RemoteBranchCachePolicyTest {
    @Test
    fun `remote branch cache expires after thirty seconds`() {
        val loadedAt = 10_000_000_000L

        assertFalse(RemoteBranchCachePolicy.isExpired(loadedAt, loadedAt + 29_999_999_999L))
        assertTrue(RemoteBranchCachePolicy.isExpired(loadedAt, loadedAt + 30_000_000_000L))
    }
}
