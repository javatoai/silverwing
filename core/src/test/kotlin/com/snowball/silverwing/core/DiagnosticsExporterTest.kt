package com.snowball.silverwing.core

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.zip.ZipFile
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DiagnosticsExporterTest {
    @TempDir lateinit var temporary: Path

    @Test
    fun `diagnostic zip contains redacted config system data and recent logs`() {
        val paths = ApplicationPaths(temporary.resolve("home"))
        Files.createDirectories(paths.logs)
        Files.writeString(paths.logs.resolve("application-2026-08-12.jsonl"), "log")
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) =
                CommandResult(0, "version", "")
        }
        val config = AppConfig(
            developmentTools = listOf(DevelopmentToolConfig(DevelopmentToolType.PYCHARM, "C:/secret/user/PyCharm.exe")),
            terminalExecutable = "C:/secret/terminal.exe",
            meegleExecutablePath = "C:/secret/user/meegle.cmd",
            codexExecutablePath = "C:/secret/user/codex.exe",
            larkExecutablePath = "C:/secret/user/lark-cli.cmd",
            gitExecutablePath = "C:/secret/user/git.exe",
            genbuExecutablePath = "C:/secret/user/genbu.exe",
        )

        val archive = DiagnosticsExporter(paths, runner).export(config, "broken task")

        ZipFile(archive.toFile()).use { zip ->
            val entries = zip.entries().asSequence().map { it.name }.toSet()
            assertTrue("system.txt" in entries)
            assertTrue("config-summary.json" in entries)
            assertTrue("task-scan-failures.txt" in entries)
            assertTrue(entries.any { it.startsWith("logs/") })
            val configText = zip.getInputStream(zip.getEntry("config-summary.json")).bufferedReader().readText()
            assertFalse(configText.contains("C:/secret"))
            assertTrue(configText.contains("<configured:PYCHARM>"))
            assertTrue(configText.contains("\"meegleExecutablePath\": \"<configured>\""))
            assertTrue(configText.contains("\"codexExecutablePath\": \"<configured>\""))
            assertTrue(configText.contains("\"larkExecutablePath\": \"<configured>\""))
            assertTrue(configText.contains("\"gitExecutablePath\": \"<configured>\""))
            assertTrue(configText.contains("\"genbuExecutablePath\": \"<configured>\""))
        }
    }
}
