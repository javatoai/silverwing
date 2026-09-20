package com.snowball.silverwing.core

import org.junit.jupiter.api.condition.EnabledOnOs
import org.junit.jupiter.api.condition.OS
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MeegleProjectCatalogTest {
    @Test
    fun `catalog requests all projects and returns their three identities`() {
        val commands = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                commands += command
                return CommandResult(0, """{"projects":[{"name":"RTA","project_key":"pk-rta","simple_name":"rta"}]}""", "")
            }
        }

        val projects = CliMeegleProjectCatalog(runner, isWindows = false).list()

        assertEquals(listOf(MeegleProjectSummary("RTA", "pk-rta", "rta")), projects)
        assertEquals(
            listOf("meegle", "project", "search", "--auto-paginate", "--format", "json"),
            commands.single(),
        )
    }

    @Test
    fun `catalog surfaces command output instead of accepting stale projects`() {
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) =
                CommandResult(1, "", "not logged in")
        }

        val error = assertFailsWith<IllegalStateException> { CliMeegleProjectCatalog(runner, isWindows = true).list() }

        kotlin.test.assertTrue(error.message.orEmpty().contains("not logged in"))
    }
}

class MeegleCliServiceTest {
    @Test
    fun `status reports version authentication host and expiry`() {
        val commands = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                commands += command
                return when (commands.size) {
                    1 -> CommandResult(0, "1.0.19\n", "")
                    else -> CommandResult(0, """{"authenticated":true,"host":"project.feishu.cn","expires_in_minutes":42}""", "")
                }
            }
        }

        val status = ProcessMeegleCliService(runner, isWindows = true).status()

        assertTrue(status.installed)
        assertTrue(status.authenticated)
        assertEquals("1.0.19", status.version)
        assertEquals("project.feishu.cn", status.host)
        assertEquals(42, status.expiresInMinutes)
        assertEquals(listOf("meegle.cmd", "--version"), commands[0])
        assertEquals(listOf("meegle.cmd", "auth", "status", "--format", "json"), commands[1])
    }

    @Test
    fun `auth status failure is represented as installed but unauthenticated with the command error`() {
        val commands = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                commands += command
                return if (commands.size == 1) {
                    CommandResult(0, "1.0.19\n", "")
                } else {
                    CommandResult(1, "", "not logged in")
                }
            }
        }

        val status = ProcessMeegleCliService(runner, isWindows = false).status()

        assertTrue(status.installed)
        assertFalse(status.authenticated)
        assertEquals("1.0.19", status.version)
        assertEquals("not logged in", status.authenticationError)
        assertEquals(2, commands.size)
    }

    @Test
    fun `version detection failure remains a status error`() {
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) =
                CommandResult(1, "", "unsupported version flag")
        }

        val error = assertFailsWith<IllegalStateException> {
            ProcessMeegleCliService(runner, isWindows = false).status()
        }

        assertTrue(error.message.orEmpty().contains("读取 Meegle CLI 版本失败"))
    }

    @Test
    fun `authentication protocol parse failure remains a status error`() {
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) =
                if (command.last() == "--version") {
                    CommandResult(0, "1.0.19\n", "")
                } else {
                    CommandResult(0, "{invalid json", "")
                }
        }

        val error = assertFailsWith<IllegalStateException> {
            ProcessMeegleCliService(runner, isWindows = false).status()
        }

        assertTrue(error.message.orEmpty().contains("Meegle 登录状态 JSON 解析失败"))
    }

    @Test
    fun `status forwards the macOS login shell environment to both CLI calls`() {
        val environments = mutableListOf<Map<String, String>>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                environments += environment
                return if (environments.size == 1) {
                    CommandResult(0, "1.0.19\n", "")
                } else {
                    CommandResult(0, "{\"authenticated\":false}", "")
                }
            }
        }
        val executable = ConfiguredMeegleExecutable(
            configuredPath = { "/opt/homebrew/bin/meegle" },
            runner = runner,
            osName = "Mac OS X",
            loginShellPathProvider = { "/opt/homebrew/bin:/usr/bin" },
        )

        ProcessMeegleCliService(runner, isWindows = false, meegleExecutable = executable).status()

        assertEquals(2, environments.size)
        environments.forEach { environment ->
            assertTrue(environment["PATH"].orEmpty().split(":").contains("/opt/homebrew/bin"))
        }
    }

    @Test
    fun `missing executable is represented as not installed`() {
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult =
                throw java.io.IOException("not found")
        }

        val status = ProcessMeegleCliService(runner, isWindows = false).status()

        assertFalse(status.installed)
        assertFalse(status.authenticated)
    }

    @Test
    fun `login uses browser oauth command and ten minute timeout`() {
        var capturedCommand = emptyList<String>()
        var capturedTimeout = Duration.ZERO
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                capturedCommand = command
                capturedTimeout = timeout
                return CommandResult(0, "{}", "")
            }
        }

        ProcessMeegleCliService(runner, isWindows = false).login()

        assertEquals(listOf("meegle", "auth", "login", "--host", "project.feishu.cn", "--format", "json"), capturedCommand)
        assertEquals(Duration.ofMinutes(10), capturedTimeout)
    }

}

class MeegleCliMacEnvironmentIntegrationTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    @EnabledOnOs(OS.MAC)
    fun `node shebang CLI runs when only the login shell exposes its runtime`() {
        val node = Files.writeString(
            temporary.resolve("node"),
            """
            #!/bin/sh
            case "${'$'}2" in
              --version) printf '0.9.7-test\n' ;;
              auth) printf '{"authenticated":false}\n' ;;
              *) exit 1 ;;
            esac
            """.trimIndent() + "\n",
        )
        val meegle = Files.writeString(temporary.resolve("meegle"), "#!/usr/bin/env node\n")
        check(node.toFile().setExecutable(true))
        check(meegle.toFile().setExecutable(true))

        val executable = ConfiguredMeegleExecutable(
            configuredPath = { meegle.toString() },
            osName = "Mac OS X",
            loginShellPathProvider = { temporary.toString() },
        )
        val status = ProcessMeegleCliService(
            isWindows = false,
            meegleExecutable = executable,
        ).status()

        assertTrue(status.installed)
        assertEquals("0.9.7-test", status.version)
        assertFalse(status.authenticated)
    }
}
