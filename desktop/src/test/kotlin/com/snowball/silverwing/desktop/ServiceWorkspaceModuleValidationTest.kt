package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ServiceModuleConfig
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.WorkspaceStrategy
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFailsWith
import kotlin.test.assertEquals

class ServiceWorkspaceModuleValidationTest {
    @Test
    fun `switching a module to clone preserves its selected source remote`() {
        assertEquals(
            "upstream/master",
            normalizeBaseRefForStrategy(WorkspaceStrategy.INDEPENDENT_CLONE, "upstream/master"),
        )
        assertEquals(
            "upstream",
            GroupServiceConfig(
                id = "service",
                repositoryId = "repo",
                displayName = "Service",
            ).effectiveMasterRemote(ServiceModuleEditorDraft(
                id = "clone",
                strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                masterBranch = "upstream/release/test",
                tagEnabled = false,
            ).toConfig()),
        )
        assertEquals(
            "upstream/release/test",
            ServiceModuleEditorDraft(
                id = "clone",
                strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                masterBranch = "upstream/release/test",
                tagEnabled = false,
            ).toConfig().masterBranch,
        )
        assertEquals(
            "upstream/develop",
            TaskModuleUiDraft(
                id = "clone",
                name = "clone",
                strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                baseRef = "upstream/develop",
                baseRemote = "upstream",
                targetBranch = "",
            ).toSelection().baseRef,
        )
        assertEquals(
            "upstream",
            TaskModuleUiDraft(
                id = "clone",
                name = "clone",
                strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                baseRef = "upstream/develop",
                baseRemote = "upstream",
                targetBranch = "",
            ).toSelection().baseRemote,
        )
    }

    @Test
    fun `duplicate clone target warning ignores blanks and case`() {
        val modules = listOf(
            TaskModuleUiDraft("one", "one", WorkspaceStrategy.INDEPENDENT_CLONE, "origin/master", "origin", targetBranch = "feature/shared"),
            TaskModuleUiDraft("two", "two", WorkspaceStrategy.INDEPENDENT_CLONE, "origin/master", "origin", targetBranch = "FEATURE/SHARED"),
            TaskModuleUiDraft("three", "three", WorkspaceStrategy.INDEPENDENT_CLONE, "origin/master", "origin", targetBranch = ""),
        )

        assertEquals(listOf("feature/shared"), duplicateCloneTargets(modules))
    }

    @Test
    fun `invalid branch input remains a draft until explicit save conversion`() {
        val cloneDraft = ServiceModuleEditorDraft(
            id = "clone",
            strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
            masterBranch = "",
            tagEnabled = false,
        )
        val tagDraft = ServiceModuleEditorDraft(id = "api", tagTargetRef = "")

        assertFailsWith<IllegalArgumentException> { cloneDraft.toConfig() }
        assertFailsWith<IllegalArgumentException> { tagDraft.toConfig() }
    }

    @Test
    fun `clone modules may share a base branch`() {
        validateServiceWorkspaceModules(
            listOf(
                ServiceModuleConfig(id = "one", name = "api", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false),
                ServiceModuleConfig(id = "two", name = "job", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, masterBranch = "origin/master", tagEnabled = false),
            ),
        )
    }

    @Test
    fun `standard module validation rejects directory collisions`() {
        val error = assertFailsWith<IllegalArgumentException> {
            validateServiceWorkspaceModules(
                listOf(
                    ServiceModuleConfig(id = "one", name = "api/v1"),
                    ServiceModuleConfig(id = "two", name = "api-v1"),
                ),
            )
        }

        assertContains(error.message.orEmpty(), "目录")
    }
}
