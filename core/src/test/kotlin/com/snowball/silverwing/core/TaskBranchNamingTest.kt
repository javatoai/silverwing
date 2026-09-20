package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Test

class TaskBranchNamingTest {
    @Test
    fun `single module keeps the requested branch`() {
        val modules = listOf(ServiceModuleConfig(id = "master", name = "default", baseRef = "origin/master"))

        assertEquals(
            mapOf("master" to "feature/OBT-123"),
            TaskBranchNaming.derive("feature/OBT-123", modules),
        )
    }

    @Test
    fun `multiple modules append their configured module names`() {
        val modules = listOf(
            ServiceModuleConfig(id = "master", name = "master", baseRef = "origin/master"),
            ServiceModuleConfig(id = "release", name = "release-test", baseRef = "origin/release/test"),
        )

        assertEquals(
            mapOf(
                "master" to "feature/OBT-123-master",
                "release" to "feature/OBT-123-release-test",
            ),
            TaskBranchNaming.derive("feature/OBT-123", modules),
        )
    }

    @Test
    fun `modules on the same base branch receive independent module branches`() {
        val modules = listOf(
            ServiceModuleConfig(id = "one", name = "api", baseRef = "origin/master"),
            ServiceModuleConfig(id = "two", name = "job", baseRef = "origin/master"),
        )

        assertEquals(
            mapOf(
                "one" to "feature/OBT-123-api",
                "two" to "feature/OBT-123-job",
            ),
            TaskBranchNaming.derive("feature/OBT-123", modules),
        )
    }

    @Test
    fun `multi module branch suffix preserves valid slash separated module names`() {
        val modules = listOf(
            ServiceModuleConfig(id = "front", name = "bp/web", baseRef = "origin/master"),
            ServiceModuleConfig(id = "backend", name = "bp_api", baseRef = "origin/master"),
        )

        assertEquals(
            mapOf(
                "front" to "feature/OBT-123-bp/web",
                "backend" to "feature/OBT-123-bp_api",
            ),
            TaskBranchNaming.derive("feature/OBT-123", modules),
        )
    }

    @Test
    fun `multi module names must be unique and branch safe`() {
        assertThrows(IllegalArgumentException::class.java) {
            TaskBranchNaming.derive(
                "feature/OBT-123",
                listOf(
                    ServiceModuleConfig(id = "one", name = "default", baseRef = "origin/master"),
                    ServiceModuleConfig(id = "two", name = "DEFAULT", baseRef = "origin/develop"),
                ),
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            TaskBranchNaming.derive(
                "feature/OBT-123",
                listOf(
                    ServiceModuleConfig(id = "one", name = "valid", baseRef = "origin/master"),
                    ServiceModuleConfig(id = "two", name = "invalid name", baseRef = "origin/develop"),
                ),
            )
        }
    }

    @Test
    fun `explicit module branch overrides only the selected module default`() {
        val modules = listOf(
            ServiceModuleConfig(id = "api", name = "api", baseRef = "origin/master"),
            ServiceModuleConfig(id = "job", name = "job", baseRef = "origin/master"),
        )

        assertEquals(
            mapOf(
                "api" to "feature/custom-api",
                "job" to "feature/OBT-123-job",
            ),
            TaskBranchNaming.resolve(
                requestedBranch = "feature/OBT-123",
                modules = modules,
                explicitBranches = mapOf("api" to "feature/custom-api"),
            ),
        )
    }

    @Test
    fun `explicit module target branches cannot collide`() {
        val modules = listOf(
            ServiceModuleConfig(id = "api", name = "api", baseRef = "origin/master"),
            ServiceModuleConfig(id = "job", name = "job", baseRef = "origin/master"),
        )

        assertThrows(IllegalArgumentException::class.java) {
            TaskBranchNaming.resolve(
                "feature/task",
                modules,
                mapOf("api" to "feature/shared", "job" to "FEATURE/shared"),
            )
        }
    }

    @Test
    fun `default module target branches cannot be ancestors of each other`() {
        val modules = listOf(
            ServiceModuleConfig(id = "release", name = "release"),
            ServiceModuleConfig(id = "mobile", name = "release/mobile"),
        )

        assertThrows(IllegalArgumentException::class.java) {
            TaskBranchNaming.resolve("feature/task", modules)
        }
    }

    @Test
    fun `explicit target branches reject ancestors regardless of order and case`() {
        val modules = listOf(
            ServiceModuleConfig(id = "api", name = "api"),
            ServiceModuleConfig(id = "job", name = "job"),
        )
        val branches = listOf("feature/release", "FEATURE/release/mobile")

        listOf(branches, branches.reversed()).forEach { targets ->
            assertThrows(IllegalArgumentException::class.java) {
                TaskBranchNaming.resolve("feature/task", modules, mapOf("api" to targets[0], "job" to targets[1]))
            }
        }
    }

    @Test
    fun `explicit target can resolve a conflict between default branch names`() {
        val modules = listOf(
            ServiceModuleConfig(id = "release", name = "release"),
            ServiceModuleConfig(id = "mobile", name = "release/mobile"),
        )

        assertEquals(
            mapOf("release" to "feature/task-release", "mobile" to "feature/mobile"),
            TaskBranchNaming.resolve("feature/task", modules, mapOf("mobile" to "feature/mobile")),
        )
    }

    @Test
    fun `target branches may share a textual prefix without a slash boundary`() {
        val modules = listOf(
            ServiceModuleConfig(id = "api", name = "api"),
            ServiceModuleConfig(id = "job", name = "job"),
        )
        listOf("feature/release-mobile", "feature/release2").forEach { other ->
            val targets = mapOf("api" to "feature/release", "job" to other)
            assertEquals(targets, TaskBranchNaming.resolve("feature/task", modules, targets))
        }
    }

    @Test
    fun `base ref qualification does not replace the configured module suffix`() {
        val modules = listOf(
            ServiceModuleConfig(id = "release", name = "release", baseRef = "origin/release/test"),
            ServiceModuleConfig(id = "master", name = "master", baseRef = "origin/master"),
        )

        assertEquals(
            mapOf(
                "release" to "feature/x-release",
                "master" to "feature/x-master",
            ),
            TaskBranchNaming.derive("feature/x", modules),
        )
    }
}
