package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals

class RemoteBranchPickerPresentationTest {
    @Test
    fun `selected remote is retained while its branch draft is incomplete or invalid`() {
        assertEquals("github", branchPickerRemote("github/master"))
        assertEquals("github", branchPickerRemote("github/"))
        assertEquals("github", branchPickerRemote("github/feature/"))
        assertEquals("github", branchPickerRemote("github/invalid..branch"))
        assertEquals("upstream", branchPickerRemote("github/master", "upstream"))
        assertEquals("origin", branchPickerRemote(""))
        assertEquals("origin", branchPickerRemote("master"))
    }

    @Test
    fun `refresh and failure retain the last usable branch options`() {
        val branches = listOf("origin/main", "origin/release/test")

        assertEquals(branches, remoteBranchOptions(RemoteBranchesState.Loading(branches)))
        assertEquals(branches, remoteBranchOptions(RemoteBranchesState.Failed("offline", branches)))
        assertEquals(branches, remoteBranchOptions(RemoteBranchesState.Loaded(branches)))
        assertEquals(emptyList(), remoteBranchOptions(RemoteBranchesState.Idle))
    }

    @Test
    fun `recent branches are moved first without retaining unavailable entries`() {
        assertEquals(
            listOf("origin/release", "origin/main", "origin/develop"),
            mergeRecentBranches(
                recent = listOf("origin/release", "origin/deleted", "origin/main"),
                available = listOf("origin/main", "origin/develop", "origin/release"),
            ),
        )
    }
}
