package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertNull
import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration

class GitExecutableTest {
    @Test
    fun `probe command and display follow the platform`() {
        assertEquals(listOf("where.exe", "git.exe"), gitProbeCommand("Windows 11"))
        assertEquals("where.exe git.exe", gitProbeCommandDisplay("Windows 11"))
        assertEquals(listOf("/bin/zsh", "-lc", "command -v git"), gitProbeCommand("Mac OS X"))
        assertEquals("/bin/zsh -lc 'command -v git'", gitProbeCommandDisplay("Mac OS X"))
        assertEquals(listOf("/bin/bash", "-lc", "command -v git"), gitProbeCommand("Linux"))
    }

    @Test
    fun `configured path wins and never probes`() {
        val runner = RecordingRunner(CommandResult(0, "C:\\Program Files\\Git\\cmd\\git.exe\n", ""))
        val executable = ConfiguredGitExecutable({ " C:\\tools\\git.exe " }, runner, osName = "Windows 11")

        assertEquals("C:\\tools\\git.exe", executable.resolve())
        assertEquals(GitCommandSource.CONFIGURED, executable.source())
        assertEquals(0, runner.calls)
    }

    @Test
    fun `unconfigured resolution probes once and caches an absolute path`() {
        val runner = RecordingRunner(CommandResult(0, "C:\\Program Files\\Git\\cmd\\git.exe\n", ""))
        val executable = ConfiguredGitExecutable({ null }, runner, osName = "Windows 11")

        assertEquals("C:\\Program Files\\Git\\cmd\\git.exe", executable.resolve())
        assertEquals("C:\\Program Files\\Git\\cmd\\git.exe", executable.resolve())
        assertEquals(GitCommandSource.PROBED, executable.source())
        assertEquals(1, runner.calls)
        assertEquals(listOf("where.exe", "git.exe"), runner.commands.single())
    }

    @Test
    fun `failed probe preserves the historical bare Git fallback`() {
        val executable = ConfiguredGitExecutable({ null }, RecordingRunner(CommandResult(1, "", "not found")), osName = "Windows 11")

        assertEquals("git", executable.resolve())
        assertEquals(GitCommandSource.PATH_FALLBACK, executable.source())
        assertNull(parseGitProbeOutput("git\n", "Windows 11"))
    }

    @Test
    fun `Git client uses the configured executable for repository commands`() {
        val runner = RecordingRunner(CommandResult(0, "", ""))
        val executable = ConfiguredGitExecutable({ "C:\\tools\\git.exe" }, runner, osName = "Windows 11")
        val client = GitClient(runner, executable)

        client.run(Path.of("C:\\repository"), "status")

        assertEquals("C:\\tools\\git.exe", runner.commands.single().first())
    }

    private class RecordingRunner(private val result: CommandResult) : CommandRunner {
        val commands = mutableListOf<List<String>>()
        val calls: Int get() = commands.size

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            return result
        }
    }
}
