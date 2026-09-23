package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class ServiceMasterBranchInheritanceTest {
    @Test
    fun `module master and test tag target inherit independently until overridden`() {
        val module = ServiceModuleConfig(id = "default")
        val initial = GroupServiceConfig(
            id = "service",
            repositoryId = "repo",
            displayName = "Service",
            masterBranch = "origin/master",
            testTagBaselineRef = "origin/release/test",
            modules = listOf(module),
        )

        assertNull(module.masterBranch)
        assertNull(module.tagTargetRef)
        assertEquals("origin/master", initial.effectiveMasterBranch(module))
        assertEquals("origin", initial.effectiveMasterRemote(module))
        assertEquals("origin/release/test", initial.effectiveTagTargetRef(module))

        val changed = initial.copy(
            masterBranch = "upstream/main",
            testTagBaselineRef = "upstream/qa",
        )
        assertEquals("upstream/main", changed.effectiveMasterBranch(module))
        assertEquals("upstream", changed.effectiveMasterRemote(module))
        assertEquals("upstream/qa", changed.effectiveTagTargetRef(module))

        val customMaster = module.copy(masterBranch = "origin/develop")
        assertEquals("origin/develop", changed.effectiveMasterBranch(customMaster))
        assertEquals("upstream/qa", changed.effectiveTagTargetRef(customMaster))

        val customTag = module.copy(tagTargetRef = "origin/release/hotfix")
        assertEquals("upstream/main", changed.effectiveMasterBranch(customTag))
        assertEquals("origin/release/hotfix", changed.effectiveTagTargetRef(customTag))

        val reset = customMaster.copy(masterBranch = null, tagTargetRef = null)
        assertEquals("upstream/main", changed.effectiveMasterBranch(reset))
        assertEquals("upstream/qa", changed.effectiveTagTargetRef(reset))
    }
}
