package com.snowball.silverwing.core

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MixedModuleAndGitWritePolicyTest {
    @Test
    fun `worktree base ref remote must match its configured base remote`() {
        assertFailsWith<IllegalArgumentException> {
            ServiceModuleConfig(
                id = "default",
                baseRef = "origin/master",
                baseRemote = "upstream",
                strategy = WorkspaceStrategy.STANDARD_WORKTREE,
            )
        }
    }

    @Test
    fun `mixed module service round trips with module-level strategies`() {
        val service = GroupServiceConfig(
            id = "mixed",
            repositoryId = "repo",
            displayName = "Mixed",
            modules = listOf(
                ServiceModuleConfig("api", "api", "origin/master", "origin", WorkspaceStrategy.STANDARD_WORKTREE),
                ServiceModuleConfig("docs", "docs", "origin/develop", "origin", WorkspaceStrategy.INDEPENDENT_CLONE, tagEnabled = false),
            ),
        )
        val json = Json { encodeDefaults = true }

        val decoded = json.decodeFromString<GroupServiceConfig>(json.encodeToString(service))

        assertEquals(listOf(WorkspaceStrategy.STANDARD_WORKTREE, WorkspaceStrategy.INDEPENDENT_CLONE), decoded.modules.map(ServiceModuleConfig::strategy))
    }

    @Test
    fun `temporary module cannot persist enabled tag snapshot`() {
        val selected = TaskModuleSelection(
            id = "temporary",
            name = "temporary",
            strategy = WorkspaceStrategy.STANDARD_WORKTREE,
            baseRef = "origin/master",
            targetBranch = "feature/x",
            source = TaskModuleSource.TEMPORARY,
            tagEnabled = true,
        )

        assertFalse(selected.toConfig().tagEnabled)
    }

    @Test
    fun `git write policy matches exact names case insensitively without wildcards`() {
        val policy = GitWritePolicy(listOf("master", "Release/Stable"))

        assertTrue(policy.isBlocked("MASTER"))
        assertTrue(policy.isBlocked("release/stable"))
        assertFalse(policy.isBlocked("feature/master"))
        assertFailsWith<IllegalArgumentException> { policy.requireAllowed("master", "提交") }
    }

    @Test
    fun `git write branch defaults are persisted in 0_8 config`() {
        val json = Json { encodeDefaults = true }
        val config = AppConfig()

        val decoded = json.decodeFromString<AppConfig>(json.encodeToString(config))

        assertEquals(CURRENT_APP_CONFIG_SCHEMA_VERSION, decoded.schemaVersion)
        assertEquals(listOf("master", "main"), decoded.blockedGitWriteBranches)
    }

    @Test
    fun `worktree targets must be unique while clone targets may be shared`() {
        val worktreeTargets = listOf("feature/x", "FEATURE/X")
        assertEquals(1, worktreeTargets.map(String::lowercase).distinct().size)
        val cloneSelections = listOf(
            TaskModuleSelection("a", "a", WorkspaceStrategy.INDEPENDENT_CLONE, "origin/master", targetBranch = "feature/shared"),
            TaskModuleSelection("b", "b", WorkspaceStrategy.INDEPENDENT_CLONE, "origin/master", targetBranch = "feature/shared"),
        )
        assertEquals(2, cloneSelections.size)
    }
}
