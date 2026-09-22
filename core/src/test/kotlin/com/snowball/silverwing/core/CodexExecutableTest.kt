package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class CodexExecutableTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `Windows desktop Codex binary wins over PATH and is cached`() {
        val desktopBin = temporary.resolve("OpenAI/Codex/bin")
        val nativeCodex = desktopBin.resolve("current/codex.exe")
        Files.createDirectories(nativeCodex.parent)
        Files.writeString(nativeCodex, "fixture")
        val runner = RecordingRunner(CommandResult(0, "C:\\tools\\codex.exe\n", ""))
        val executable = AutoDetectedCodexExecutable(
            runner = runner,
            osName = "Windows 11",
            desktopCodexBinDirectory = { desktopBin },
        )

        assertEquals(nativeCodex.toRealPath().toString(), executable.resolve())
        assertEquals(nativeCodex.toRealPath().toString(), executable.resolve())
        assertEquals(CodexCommandSource.DESKTOP_APP, executable.source())
        assertEquals(emptyList(), runner.commands)
    }

    @Test
    fun `Windows probes an absolute cmd shim when no desktop binary is available`() {
        val runner = RecordingRunner(
            CommandResult(1, "", "not found"),
            CommandResult(0, "C:\\tools\\codex.cmd\n", ""),
        )
        val executable = AutoDetectedCodexExecutable(
            runner = runner,
            osName = "Windows 11",
            desktopCodexBinDirectory = { null },
        )

        assertEquals("C:\\tools\\codex.cmd", executable.resolve())
        assertEquals("C:\\tools\\codex.cmd", executable.resolve())
        assertEquals(CodexCommandSource.PROBED, executable.source())
        assertEquals(
            listOf(
                listOf("where.exe", "codex.exe"),
                listOf("where.exe", "codex.cmd"),
            ),
            runner.commands,
        )
    }

    @Test
    fun `configured path wins over automatic discovery without probing`() {
        val automatic = object : CodexExecutable {
            override fun resolve(): String = error("automatic resolution must not run")
            override fun probe(): String = error("automatic probing must not run")
        }
        val executable = ConfiguredCodexExecutable(
            configuredPath = { " C:\\tools\\codex.exe " },
            automatic = automatic,
            osName = "Windows 11",
        )

        assertEquals("C:\\tools\\codex.exe", executable.resolve())
        assertEquals("C:\\tools\\codex.exe", executable.probe())
        assertEquals(CodexCommandSource.CONFIGURED, executable.source())
    }

    @Test
    fun `path normalization accepts blank auto detect and rejects unsafe paths`() {
        assertNull(normalizeCodexExecutablePath("   "))
        assertFailsWith<IllegalArgumentException> { normalizeCodexExecutablePath("bin/codex") }
        val missing = temporary.resolve("missing-codex").toAbsolutePath().toString()
        assertFailsWith<IllegalArgumentException> { normalizeCodexExecutablePath(missing) }
    }

    @Test
    fun `version probe uses the active configured command`() {
        val runner = RecordingRunner(CommandResult(0, "codex-cli 1.2.3\n", ""))
        val executable = ConfiguredCodexExecutable(
            configuredPath = { "C:\\tools\\codex.exe" },
            automatic = CodexExecutable { error("automatic resolution must not run") },
            osName = "Windows 11",
        )

        val version = executable.version(runner)

        assertEquals("codex-cli 1.2.3", version.version)
        assertEquals(listOf(listOf("C:\\tools\\codex.exe", "--version")), runner.commands)
    }

    private class RecordingRunner(
        vararg val results: CommandResult,
    ) : CommandRunner {
        val commands = mutableListOf<List<String>>()

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            return results[commands.lastIndex]
        }
    }
}
