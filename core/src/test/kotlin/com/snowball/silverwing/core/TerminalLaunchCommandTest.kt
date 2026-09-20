package com.snowball.silverwing.core

import java.nio.file.Path
import java.nio.file.Files
import java.time.Duration
import org.junit.jupiter.api.condition.EnabledOnOs
import org.junit.jupiter.api.condition.OS
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class TerminalLaunchCommandTest {
    @TempDir lateinit var temporary: Path

    @Test
    @EnabledOnOs(OS.WINDOWS)
    fun `batch terminal preserves spaces and shell metacharacters in paths`() {
        val wrapperDirectory = Files.createDirectories(temporary.resolve("wrapper & %SILVERWING_TEST_EXPANSION% ' !"))
        val wrapper = createArgumentCapturingBatch(wrapperDirectory.resolve("terminal wrapper.cmd"))
        val targets = listOf(
            "target with spaces",
            "中文任务 (工作区)",
            "target&literal",
            "target%SILVERWING_TEST_EXPANSION%",
            "target'apostrophe",
            "target!SILVERWING_TEST_EXPANSION!^literal",
        ).map { Files.createDirectories(temporary.resolve(it)) }
        val runner = ProcessCommandRunner()
        for ((index, target) in targets.withIndex()) {
            val output = temporary.resolve("arguments-$index.txt")
            val result = runner.run(
                TerminalLaunchCommand.build(wrapper.toString(), target, "Windows 11"),
                timeout = Duration.ofSeconds(15),
                environment = mapOf(
            "SILVERWING_TEST_ARGS_OUT" to output.toString(),
            "SILVERWING_TEST_EXPANSION" to "unexpected-expansion",
                ),
            )

            assertEquals(0, result.exitCode, result.stderr)
            assertEquals(listOf(target.toString(), "", ""), Files.readAllLines(output))
        }
    }

    @Test
    @EnabledOnOs(OS.WINDOWS)
    fun `batch terminal can be resolved from PATH`() {
        val wrapperDirectory = Files.createDirectories(temporary.resolve("terminal path"))
        val wrapper = createArgumentCapturingBatch(wrapperDirectory.resolve("silverwing-test-terminal.cmd"))
        val target = Files.createDirectories(temporary.resolve("target directory"))
        val output = temporary.resolve("path-arguments.txt")
        val pathEntry = System.getenv().entries.first { it.key.equals("PATH", ignoreCase = true) }
        val result = ProcessCommandRunner().run(
            TerminalLaunchCommand.build(wrapper.fileName.toString(), target, "Windows 11"),
            timeout = Duration.ofSeconds(15),
            environment = mapOf(
                pathEntry.key to "$wrapperDirectory;${pathEntry.value}",
            "SILVERWING_TEST_ARGS_OUT" to output.toString(),
            ),
        )

        assertEquals(0, result.exitCode, result.stderr)
        assertEquals(listOf(target.toString(), "", ""), Files.readAllLines(output))
    }

    @Test
    fun `terminal resolution exposes configured and automatic Windows choices`() {
        assertEquals(
            TerminalCommandResolution("自定义终端", "C:\\Tools\\terminal.exe", TerminalCommandSource.CONFIGURED),
            TerminalLaunchCommand.resolve("C:\\Tools\\terminal.exe", "Windows 11", windowsTerminalAvailable = true),
        )
        assertEquals(
            TerminalCommandResolution("Windows Terminal", "wt.exe", TerminalCommandSource.SYSTEM_DEFAULT),
            TerminalLaunchCommand.resolve(null, "Windows 11", windowsTerminalAvailable = true),
        )
        assertEquals(
            TerminalCommandResolution("Windows PowerShell", "powershell.exe", TerminalCommandSource.SYSTEM_DEFAULT),
            TerminalLaunchCommand.resolve(null, "Windows 11", windowsTerminalAvailable = false),
        )
    }

    @Test
    fun `terminal resolution exposes the macOS system terminal`() {
        assertEquals(
            TerminalCommandResolution("Terminal", "Terminal.app", TerminalCommandSource.SYSTEM_DEFAULT),
            TerminalLaunchCommand.resolve(null, "Mac OS X"),
        )
    }

    private val target = Path.of("D:/tasks/demo")

    @Test
    fun `mac application bundle is launched through open`() {
        assertEquals(
            listOf("open", "-a", "/Applications/iTerm.app", target.toAbsolutePath().normalize().toString()),
            TerminalLaunchCommand.build("/Applications/iTerm.app", target, "Mac OS X"),
        )
    }

    @Test
    fun `mac executable path is preserved`() {
        assertEquals(
            listOf("/usr/local/bin/kitty", target.toAbsolutePath().normalize().toString()),
            TerminalLaunchCommand.build("/usr/local/bin/kitty", target, "Mac OS X"),
        )
    }

    @Test
    fun `windows system terminal uses literal arguments and keeps the window open`() {
        assertEquals(
            listOf(
                "powershell.exe", "-NoProfile", "-NoExit", "-Command",
                "& 'C:/Program Files/genbu.exe' '--version' 'a''b'",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                listOf("C:/Program Files/genbu.exe", "--version", "a'b"),
                "Windows 11",
            ),
        )
    }

    @Test
    fun `mac system terminal executes a shell quoted command`() {
        assertEquals(
            listOf(
                "osascript", "-e",
                "tell application \"Terminal\" to do script \"'/Applications/Genbu Tool/genbu' '--version' 'a'\\\\''b'\"",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                listOf("/Applications/Genbu Tool/genbu", "--version", "a'b"),
                "Mac OS X",
            ),
        )
    }

    @Test
    fun `linux system terminal executes command through a shell and waits`() {
        assertEquals(
            listOf(
                "x-terminal-emulator", "-e", "/bin/sh", "-c",
                "'/opt/genbu tool' '--version' 'a'\\''b'; printf '\\nPress Enter to close...'; read -r",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                listOf("/opt/genbu tool", "--version", "a'b"),
                "Linux",
            ),
        )
    }

    @Test
    fun `copy display quotes only values that need shell protection`() {
        assertEquals(
            "'C:/Program Files/genbu.exe' --version 'a''b'",
            TerminalLaunchCommand.display(
                listOf("C:/Program Files/genbu.exe", "--version", "a'b"),
                "Windows 11",
            ),
        )
        assertEquals("git --version", TerminalLaunchCommand.display(listOf("git", "--version"), "Linux"))
    }

    @Test
    fun `empty system command is rejected`() {
        assertFailsWith<IllegalArgumentException> { TerminalLaunchCommand.buildSystemCommand(emptyList(), "Linux") }
        assertFailsWith<IllegalArgumentException> { TerminalLaunchCommand.display(emptyList(), "Linux") }
    }

    @Test
    fun windowsCliLaunchChangesToExecutableDirectoryBeforeRunningIt() {
        val workingDirectory = Path.of("D:/cli-list")
        assertEquals(
            listOf(
                "powershell.exe", "-NoProfile", "-NoExit", "-Command",
                "Set-Location -LiteralPath '" + workingDirectory.normalize() + "'; & 'D:/cli-list/genbu.exe'",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                "D:/cli-list/genbu.exe",
                workingDirectory,
                "Windows 11",
            ),
        )
    }

    @Test
    fun windowsCliLaunchUsesANewWindowsTerminalWindowWhenAvailable() {
        val workingDirectory = Path.of("D:/cli-list")
        assertEquals(
            listOf(
                "wt.exe", "-w", "new", "new-tab", "-d", workingDirectory.normalize().toString(),
                "powershell.exe", "-NoProfile", "-NoExit", "-Command", "& 'D:/cli-list/genbu.exe'",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                command = "D:/cli-list/genbu.exe",
                workingDirectory = workingDirectory,
                osName = "Windows 11",
                windowsTerminalAvailable = true,
            ),
        )
    }

    @Test
    fun windowsCliLaunchQuotesExecutableAndDirectoryIndependently() {
        val workingDirectory = Path.of("D:/Program Files/O'Brien")
        assertEquals(
            listOf(
                "powershell.exe", "-NoProfile", "-NoExit", "-Command",
                "Set-Location -LiteralPath '" +
                    workingDirectory.normalize().toString().replace("'", "''") +
                    "'; & 'D:/Program Files/O''Brien/genbu.exe'",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                "D:/Program Files/O'Brien/genbu.exe",
                workingDirectory,
                "Windows 11",
            ),
        )
    }

    @Test
    fun linuxCliLaunchChangesToExecutableDirectoryBeforeRunningIt() {
        val workingDirectory = Path.of("/opt/genbu tool")
        val normalizedDirectory = workingDirectory.toAbsolutePath().normalize()
        assertEquals(
            listOf(
                "x-terminal-emulator", "-e", "/bin/sh", "-c",
                "cd '" + normalizedDirectory + "' && '/opt/genbu tool/genbu'; printf '\\nPress Enter to close...'; read -r",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                "/opt/genbu tool/genbu",
                workingDirectory,
                "Linux",
            ),
        )
    }

    @Test
    fun macCliLaunchChangesToExecutableDirectoryBeforeRunningIt() {
        val workingDirectory = Path.of("/Applications/Genbu Tool")
        val escapedDirectory = workingDirectory.toAbsolutePath().normalize().toString().replace("\\", "\\\\")
        assertEquals(
            listOf(
                "osascript", "-e",
                "tell application \"Terminal\" to do script \"cd '" +
                    escapedDirectory +
                    "' && '/Applications/Genbu Tool/genbu'\"",
            ),
            TerminalLaunchCommand.buildSystemCommand(
                "/Applications/Genbu Tool/genbu",
                workingDirectory,
                "Mac OS X",
            ),
        )
    }

    @Test
    fun absoluteWindowsExecutablePathGetsItsParentWhileAPathCommandDoesNot() {
        assertEquals(
            Path.of("D:/cli-list"),
            TerminalLaunchCommand.parentDirectoryOfCommand("D:/cli-list/genbu.exe", "Windows 11"),
        )
        assertNull(TerminalLaunchCommand.parentDirectoryOfCommand("genbu.exe", "Windows 11"))
        assertNull(TerminalLaunchCommand.parentDirectoryOfCommand("genbu", "Linux"))
    }

    @Test
    fun blankStringSystemCommandIsRejected() {
        assertFailsWith<IllegalArgumentException> {
            TerminalLaunchCommand.buildSystemCommand(" ", osName = "Linux")
        }
    }
}

internal fun createArgumentCapturingBatch(path: Path): Path {
    Files.writeString(
        path,
        """
            @echo off
set "SILVERWING_TEST_ARG_1=%~1"
set "SILVERWING_TEST_ARG_2=%~2"
set "SILVERWING_TEST_ARG_3=%~3"
powershell.exe -NoProfile -NonInteractive -Command "[IO.File]::WriteAllLines(${'$'}env:SILVERWING_TEST_ARGS_OUT, @(${'$'}env:SILVERWING_TEST_ARG_1, ${'$'}env:SILVERWING_TEST_ARG_2, ${'$'}env:SILVERWING_TEST_ARG_3))"
            exit /b %errorlevel%
        """.trimIndent(),
    )
    return path
}
