package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class CommandVersionProbeTest {
    @Test
    fun `successful output returns first non blank version line and preserves environment`() {
        var capturedCommand: List<String>? = null
        var capturedEnvironment: Map<String, String>? = null
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                capturedCommand = command
                capturedEnvironment = environment
                assertEquals(Duration.ofSeconds(3), timeout)
                return CommandResult(0, "\n  genbu 1.2.3\nextra diagnostic\n", "")
            }
        }

        val status = CommandVersionProbe.probe(
            " /opt/tools/genbu ",
            runner = runner,
            timeout = Duration.ofSeconds(3),
            environment = mapOf("PATH" to "/opt/tools"),
        )

        assertTrue(status.succeeded)
        assertEquals("/opt/tools/genbu", status.command)
        assertEquals("genbu 1.2.3", status.version)
        assertNull(status.error)
        assertEquals(listOf("/opt/tools/genbu", "--version"), capturedCommand)
        assertEquals(mapOf("PATH" to "/opt/tools"), capturedEnvironment)
        assertEquals(listOf("/opt/tools/genbu", "--version"), status.versionCommand)
    }

    @Test
    fun `failed command returns diagnostic without throwing`() {
        val status = CommandVersionProbe.probe(
            "meegle",
            runner = object : CommandRunner {
                override fun run(
                    command: List<String>,
                    workingDirectory: Path?,
                    timeout: Duration,
                    environment: Map<String, String>,
                ) = CommandResult(127, "", "not found")
            },
        )

        assertFalse(status.succeeded)
        assertNull(status.version)
        assertEquals("not found", status.error)
    }

    @Test
    fun `runner exception and blank command are represented as unavailable`() {
        val thrown = CommandVersionProbe.probe(
            "git",
            runner = object : CommandRunner {
                override fun run(
                    command: List<String>,
                    workingDirectory: Path?,
                    timeout: Duration,
                    environment: Map<String, String>,
                ): CommandResult = error("process launch failed")
            },
        )
        val blank = CommandVersionProbe.probe(" ")

        assertFalse(thrown.succeeded)
        assertEquals("process launch failed", thrown.error)
        assertFalse(blank.succeeded)
        assertEquals("命令为空", blank.error)
    }

    @Test
    fun `all executable resolvers expose best effort version check`() {
        val commands = mutableListOf<List<String>>()
        val runner = recordingRunner(commands)

        val genbu = GenbuExecutable { "genbu" }
        val git = GitExecutable { "git" }
        val meegle = MeegleExecutable { "meegle" }

        assertEquals("genbu 1.0", genbu.version(runner).version)
        assertEquals("git 2.0", git.version(runner).version)
        assertEquals("meegle 3.0", meegle.version(runner).version)
        assertEquals(
            listOf(
                listOf("genbu", "--version"),
                listOf("git", "--version"),
                listOf("meegle", "--version"),
            ),
            commands,
        )
    }

    private fun recordingRunner(commands: MutableList<List<String>>) = object : CommandRunner {
        private var invocation = 0

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            invocation += 1
            return CommandResult(0, "${command.first()} ${invocation}.0\n", "")
        }
    }
}
