package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class MeegleRequirementCacheIdentityTest {
    private class Runner(var auth: String, var user: String) : CommandRunner {
        val commands = mutableListOf<List<String>>()
        var profile: String? = "default"
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            commands += command
            return when (command[1]) {
                "config" -> CommandResult(if (profile == null) 1 else 0, profile.orEmpty(), "")
                "auth" -> CommandResult(0, auth, "")
                "user" -> CommandResult(0, user, "")
                else -> error("Unexpected identity command")
            }
        }
    }

    @Test fun `identity contains only non-secret fields and changes with account tenant host profile and executable`() {
        val runner = Runner("""{"authenticated":true,"host":"project.feishu.cn","token":"do-not-cache"}""",
            """{"user_key":"user1","tenant_id":"tenant1","password":"also-secret","name":"Display name"}""")
        val identity = MeegleRequirementCacheIdentity(runner, MeegleExecutable { "meegle" })
        val first = assertNotNull(identity.read())
        assertFalse(first.contains("do-not-cache") || first.contains("also-secret") || first.contains("Display name"))
        runner.user = runner.user.replace("user1", "user2")
        assertNotEquals(first, identity.read())
        runner.user = runner.user.replace("user2", "user1").replace("tenant1", "tenant2")
        assertNotEquals(first, identity.read())
        runner.user = runner.user.replace("tenant2", "tenant1")
        runner.auth = runner.auth.replace("project.feishu.cn", "another-host")
        assertNotEquals(first, identity.read())
        runner.auth = runner.auth.replace("another-host", "project.feishu.cn")
        runner.profile = "staging"
        assertNotEquals(first, identity.read())
        runner.profile = "default"
        assertNotEquals(first, MeegleRequirementCacheIdentity(runner, MeegleExecutable { "another-cli" }).read())
    }

    @Test fun `logged out or missing identity cannot form a reusable cache key`() {
        val runner = Runner("""{"authenticated":false,"host":"project.feishu.cn"}""", """{"user_key":"old-user"}""")
        val identity = MeegleRequirementCacheIdentity(runner, MeegleExecutable { "meegle" })
        assertNull(identity.read())
        assertEquals(MeegleRequirementIdentityResult.LoggedOut, identity.check())
        assertEquals(4, runner.commands.size)
        runner.auth = """{"authenticated":true,"host":"project.feishu.cn"}"""
        runner.user = """{"name":"No stable identity"}"""
        assertNull(identity.read())
        assertEquals(MeegleRequirementIdentityResult.Unavailable, identity.check())
    }

    @Test fun `unavailable or malformed profile disables caching without sharing a default profile`() {
        val runner = Runner("""{"authenticated":true,"host":"project.feishu.cn"}""", """{"user_key":"user"}""")
        val identity = MeegleRequirementCacheIdentity(runner, MeegleExecutable { "meegle" })
        for (profile in listOf(null, "", "  ", "unexpected\nmultiline output")) {
            runner.profile = profile
            assertNull(identity.read())
        }
        assertTrue(runner.commands.all { it[1] == "config" })
    }
}
