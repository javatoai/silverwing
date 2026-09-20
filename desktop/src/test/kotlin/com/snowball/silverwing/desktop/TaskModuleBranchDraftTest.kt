package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.ServiceModuleConfig
import kotlin.test.Test
import kotlin.test.assertEquals

class TaskModuleBranchDraftTest {
    private val service = GroupServiceConfig(
        id = "backend",
        repositoryId = "repo",
        displayName = "Backend",
        modules = listOf(
            ServiceModuleConfig(id = "api", name = "api", baseRef = "origin/master"),
            ServiceModuleConfig(id = "job", name = "jobs/nightly", baseRef = "origin/master"),
        ),
    )

    @Test
    fun `multi module targets are independent even when base refs match`() {
        val overrides = taskModuleOverrides(service, "feature/REQ-1", emptyMap(), emptyMap())

        assertEquals(
            listOf("feature/REQ-1-api", "feature/REQ-1-jobs/nightly"),
            overrides.map { it.targetBranch },
        )
    }

    @Test
    fun `manual target survives task branch template changes`() {
        val overrides = taskModuleOverrides(
            service = service,
            taskBranch = "feature/REQ-2",
            baseOverrides = emptyMap(),
            targetOverrides = mapOf("backend::api" to "feature/custom-api"),
        )

        assertEquals("feature/custom-api", overrides[0].targetBranch)
        assertEquals("feature/REQ-2-jobs/nightly", overrides[1].targetBranch)
    }

    @Test
    fun `selected service targets follow a task branch edit while manual targets remain`() {
        val initial = configuredTaskModuleDrafts(service, "feature/REQ-1")
        val drafts = mapOf("backend" to initial.mapIndexed { index, module ->
            if (index == 0) module.copy(targetBranch = "feature/custom-api", targetEdited = true) else module
        })

        val updated = retargetServiceModuleDrafts(drafts, "feature/REQ-2")

        assertEquals(
            listOf("feature/custom-api", "feature/REQ-2-jobs/nightly"),
            updated.getValue("backend").map(TaskModuleUiDraft::targetBranch),
        )
    }
}
