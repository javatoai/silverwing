package com.snowball.silverwing.core

import java.nio.file.Path
import java.nio.file.Files
import java.time.Duration
import java.nio.charset.StandardCharsets
import java.util.Base64
import org.junit.jupiter.api.condition.EnabledOnOs
import org.junit.jupiter.api.condition.OS
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals

class DevelopmentToolLaunchCommandTest {
    @TempDir lateinit var temporary: Path
    private val target = Path.of("D:/tasks/demo")

    @Test
    fun `vscode uses a new window while jetbrains tools use the Windows shell`() {
        assertEquals(
            listOf("C:/Code.exe", "--new-window", target.toAbsolutePath().normalize().toString()),
            DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.VISUAL_STUDIO_CODE,
                "C:/Code.exe",
                target,
                "Windows 11",
            ),
        )
        assertEquals(
            windowsShellLaunch("C:/idea64.exe", target.toAbsolutePath().normalize().toString()),
            DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.INTELLIJ_IDEA,
                "C:/idea64.exe",
                target,
                "Windows 11",
            ),
        )
        assertEquals(
            windowsShellLaunch("C:/pycharm64.exe", target.toAbsolutePath().normalize().toString()),
            DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.PYCHARM,
                "C:/pycharm64.exe",
                target,
                "Windows 11",
            ),
        )
        assertEquals(
            windowsShellLaunch("C:/studio64.exe", target.toAbsolutePath().normalize().toString()),
            DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.ANDROID_STUDIO,
                "C:/studio64.exe",
                target,
                "Windows 11",
            ),
        )
    }

    private fun windowsShellLaunch(application: String, directory: String): List<String> =
        listOf(
            "powershell.exe",
            "-NoProfile",
            "-EncodedCommand",
            Base64.getEncoder().encodeToString(
                "Start-Process -FilePath '$application' -ArgumentList '\"$directory\"'".toByteArray(StandardCharsets.UTF_16LE),
            ),
        )

    @Test
    @EnabledOnOs(OS.WINDOWS)
    fun `Windows shell passes spaces apostrophes and trailing backslashes as one argument`() {
        val source = temporary.resolve("CaptureArguments.cs")
        val executable = temporary.resolve("Capture Arguments.exe")
        Files.writeString(source, """
            using System;
            using System.IO;
            public class CaptureArguments {
                public static void Main(string[] args) {
                    File.WriteAllLines(Environment.GetEnvironmentVariable("SILVERWING_TEST_ARGS_OUT"), args);
                }
            }
        """.trimIndent())
        fun literal(value: Path) = "'${value.toString().replace("'", "''")}'"
        val runner = ProcessCommandRunner()
        val compilation = runner.run(listOf(
            "powershell.exe", "-NoProfile", "-Command",
            "Add-Type -Path ${literal(source)} -OutputAssembly ${literal(executable)} -OutputType ConsoleApplication",
        ), timeout = Duration.ofSeconds(30))
        assertEquals(0, compilation.exitCode, compilation.stderr)
        val workspace = Files.createDirectories(temporary.resolve("workspace with spaces and 'quotes' & symbols"))
        for ((index, directory) in listOf(workspace, workspace.root).withIndex()) {
            val output = temporary.resolve("arguments-$index.txt")
            val command = DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.INTELLIJ_IDEA, executable.toString(), directory,
            ).toMutableList()
            val script = String(Base64.getDecoder().decode(command.last()), StandardCharsets.UTF_16LE)
            command[command.lastIndex] = Base64.getEncoder().encodeToString(
                (script + " -WindowStyle Hidden -Wait").toByteArray(StandardCharsets.UTF_16LE),
            )
            val result = runner.run(command, timeout = Duration.ofSeconds(15), environment = mapOf("SILVERWING_TEST_ARGS_OUT" to output.toString()))
            assertEquals(0, result.exitCode, result.stderr)
            assertEquals(listOf(directory.toAbsolutePath().normalize().toString()), Files.readAllLines(output))
        }
    }

    @Test
    fun `mac application bundle is opened without shell interpolation`() {
        assertEquals(
            listOf("open", "-a", "/Applications/DevEco-Studio.app", target.toAbsolutePath().normalize().toString()),
            DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.DEVECO_STUDIO,
                "/Applications/DevEco-Studio.app",
                target,
                "Mac OS X",
            ),
        )
        assertEquals(
            listOf(
                "open", "-n", "-a", "/Applications/Visual Studio Code.app", "--args", "--new-window",
                target.toAbsolutePath().normalize().toString(),
            ),
            DevelopmentToolLaunchCommand.build(
                DevelopmentToolType.VISUAL_STUDIO_CODE,
                "/Applications/Visual Studio Code.app",
                target,
                "Mac OS X",
            ),
        )
    }

    @Test
    @EnabledOnOs(OS.WINDOWS)
    fun `batch development tool preserves its option and literal project path`() {
        val wrapperDirectory = Files.createDirectories(temporary.resolve("tool & %SILVERWING_TEST_EXPANSION% ' !"))
        val wrapper = createArgumentCapturingBatch(wrapperDirectory.resolve("code wrapper.bat"))
        val workspace = Files.createDirectories(temporary.resolve("workspace & %SILVERWING_TEST_EXPANSION% ' !SILVERWING_TEST_EXPANSION!"))
        for ((index, directory) in listOf(workspace, workspace.root).withIndex()) {
            val output = temporary.resolve("batch-arguments-$index.txt")
            val result = ProcessCommandRunner().run(
                DevelopmentToolLaunchCommand.build(
                    DevelopmentToolType.VISUAL_STUDIO_CODE, wrapper.toString(), directory, "Windows 11",
                ),
                timeout = Duration.ofSeconds(15),
                environment = mapOf(
                    "SILVERWING_TEST_ARGS_OUT" to output.toString(),
                    "SILVERWING_TEST_EXPANSION" to "unexpected-expansion",
                ),
            )

            assertEquals(0, result.exitCode, result.stderr)
            assertEquals(listOf("--new-window", directory.toString(), ""), Files.readAllLines(output))
        }
    }
}
