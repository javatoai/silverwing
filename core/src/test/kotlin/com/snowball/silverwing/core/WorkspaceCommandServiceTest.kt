package com.snowball.silverwing.core

import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class WorkspaceCommandServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `keeps executable arguments and streams stdout and stderr without shell parsing`() {
        val workspace = temporary.resolve("workspace")
        Files.createDirectories(workspace.resolve("module"))
        val runner = CapturingRunner()
        val command = WorkspaceCommandConfig(
            id = "maven-install",
            name = "Install",
            iconKey = "package",
            executable = "mvn",
            arguments = listOf("-DskipTests", "-Drevision=1 2", "install"),
            workingDirectory = "module",
            timeoutSeconds = 37,
        )
        val output = mutableListOf<CommandOutputLine>()

        val result = WorkspaceCommandService(runner).execute(workspace, command, output::add)

        assertEquals(CommandResult(0, "", ""), result)
        assertTrue(runner.command.first().endsWith("mvn", ignoreCase = true) || runner.command.first().endsWith("mvn.cmd", ignoreCase = true))
        assertEquals(listOf("-DskipTests", "-Drevision=1 2", "install"), runner.command.drop(1))
        assertEquals(workspace.resolve("module").toAbsolutePath().normalize(), runner.workingDirectory)
        assertEquals(Duration.ofSeconds(37), runner.timeout)
        assertEquals(
            listOf(
                CommandOutputLine(CommandOutputStream.STDOUT, "compile"),
                CommandOutputLine(CommandOutputStream.STDERR, "warning"),
            ),
            output,
        )
    }

    @Test
    fun `rejects unsafe working directories and disabled commands`() {
        val workspace = temporary.resolve("workspace")
        Files.createDirectories(workspace)
        val service = WorkspaceCommandService(CapturingRunner())

        listOf("../outside", ".git", "nested/.git", temporary.resolve("outside").toString()).forEach { workingDirectory ->
            assertThrows(IllegalArgumentException::class.java) {
                service.execute(
                    workspace,
                    WorkspaceCommandConfig("unsafe-$workingDirectory", "Unsafe", executable = "mvn", workingDirectory = workingDirectory),
                )
            }
        }
        assertThrows(IllegalArgumentException::class.java) {
            service.execute(workspace, WorkspaceCommandConfig("disabled", "Disabled", executable = "mvn", enabled = false))
        }
        assertFailsWith<IllegalArgumentException> {
            WorkspaceCommandConfig("empty", "Empty", executable = "")
        }
    }

    @Test
    fun `module config defaults custom commands to empty and serializes them`() {
        val json = Json { encodeDefaults = true }
        val current = """
            {
              "id": "orders",
              "repositoryId": "repo-orders",
              "displayName": "Orders",
              "masterBranch": "origin/master",
              "testTagBaselineRef": "origin/release/test",
              "modules": [{"id": "default", "masterBranch": null}]
            }
        """.trimIndent()

        val decoded = json.decodeFromString<GroupServiceConfig>(current)
        assertTrue(decoded.modules.single().customCommands.isEmpty())

        val configured = decoded.copy(
            modules = decoded.modules.map { module ->
                module.copy(
                    customCommands = listOf(
                        WorkspaceCommandConfig(
                            id = "clean",
                            name = "Clean",
                            executable = "mvn",
                            arguments = listOf("clean"),
                        ),
                    ),
                )
            },
        )
        val persisted = json.encodeToString(configured)
        assertTrue("customCommands" in persisted)
        assertEquals(configured, json.decodeFromString(persisted))
    }

    @Test
    fun `resolves a Windows Maven wrapper from the workspace`() {
        val workspace = temporary.resolve("wrapper-workspace")
        Files.createDirectories(workspace)
        Files.writeString(workspace.resolve("mvnw.cmd"), "@echo off")
        val runner = CapturingRunner()

        WorkspaceCommandService(runner, osName = "Windows 11", environment = emptyMap()).execute(
            workspace,
            WorkspaceCommandConfig("wrapper", "Wrapper", executable = "mvnw", arguments = listOf("clean")),
        )

        assertTrue(runner.command.first().endsWith("mvnw.cmd", ignoreCase = true))
    }

    private class CapturingRunner : StreamingCommandRunner {
        var command: List<String> = emptyList()
        var workingDirectory: Path? = null
        var timeout: Duration? = null

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult = error("run() should not be called")

        override fun runStreaming(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
            onOutput: (CommandOutputLine) -> Unit,
        ): CommandResult {
            this.command = command
            this.workingDirectory = workingDirectory
            this.timeout = timeout
            onOutput(CommandOutputLine(CommandOutputStream.STDOUT, "compile"))
            onOutput(CommandOutputLine(CommandOutputStream.STDERR, "warning"))
            return CommandResult(0, "", "")
        }
    }
}
