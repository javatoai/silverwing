package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class LocalGitEnvironmentInspectorTest {
    @Test
    fun `inspection reads only executable version and global git config`() {
        val runner = RecordingGitRunner()
        val inspector = LocalGitEnvironmentInspector(
            runner,
            ConfiguredGitExecutable({ null }, runner, osName = "Windows 11"),
        )
        val snapshot = inspector.inspect()

        assertEquals("C:\\Program Files\\Git\\cmd\\git.exe", snapshot.gitExecutable)
        assertEquals("git version 2.50.1.windows.1", snapshot.gitVersion)
        assertEquals("Alice", snapshot.globalUserName?.value)
        assertEquals("alice@example.com", snapshot.globalUserEmail?.value)
        assertEquals(listOf("manager-core"), snapshot.globalCredentialHelpers.map(GitConfigValue::value))
        assertEquals(3, runner.commands.size)
        assertFalse(runner.commands.any { command -> command.contains("-C") })
    }

    @Test
    fun `config parser retains value and source`() {
        assertEquals(
            GitConfigValue("user.email", "alice@example.com", "file:C:/Users/alice/.gitconfig"),
            LocalGitEnvironmentInspector.parseConfigValues(
                "file:C:/Users/alice/.gitconfig\tuser.email=alice@example.com",
            ).single(),
        )
    }

    private class RecordingGitRunner : CommandRunner {
        val commands = mutableListOf<List<String>>()

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            val joined = command.joinToString(" ")
            return when {
                command.first() == "where.exe" -> ok("C:\\Program Files\\Git\\cmd\\git.exe\n")
                joined == "C:\\Program Files\\Git\\cmd\\git.exe --version" -> ok("git version 2.50.1.windows.1\n")
                joined.contains("config --global --show-origin --list") -> ok(
                    "file:C:/Users/alice/.gitconfig\tuser.name=Alice\n" +
                        "file:C:/Users/alice/.gitconfig\tuser.email=alice@example.com\n" +
                        "file:C:/Users/alice/.gitconfig\tcredential.helper=manager-core\n",
                )
                else -> ok("")
            }
        }

        private fun ok(stdout: String) = CommandResult(0, stdout, "")
    }
}
