package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertNull
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.condition.DisabledOnOs
import org.junit.jupiter.api.condition.OS
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.assertFailsWith

class MeegleExecutableTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `fallback command follows the platform`() {
        assertEquals("meegle.cmd", meegleFallbackCommand(isWindows = true))
        assertEquals("meegle", meegleFallbackCommand(isWindows = false))
    }

    @Test
    fun `probe command uses a login shell on unix and where on windows`() {
        assertEquals(listOf("where.exe", "meegle.cmd"), meegleProbeCommand("Windows 11"))
        assertEquals(listOf("/bin/zsh", "-lc", "command -v meegle"), meegleProbeCommand("Mac OS X"))
        assertEquals(listOf("/bin/bash", "-lc", "command -v meegle"), meegleProbeCommand("Linux"))
    }

    @Test
    fun `probe output parsing takes the first absolute path line`() {
        assertEquals("/opt/homebrew/bin/meegle", parseMeegleProbeOutput("/opt/homebrew/bin/meegle\n", "Mac OS X"))
        assertEquals(
            "/usr/local/bin/meegle",
            parseMeegleProbeOutput("meegle: aliased to something\n/usr/local/bin/meegle\n", "Linux"),
        )
        assertEquals("C:\\tools\\meegle.cmd", parseMeegleProbeOutput("C:\\tools\\meegle.cmd\r\n", "Windows 11"))
        assertEquals(
            "C:\\Program Files\\Meegle\\meegle.cmd",
            parseMeegleProbeOutput("C:\\Program Files\\Meegle\\meegle.cmd\r\n", "Windows 11"),
        )
        assertNull(parseMeegleProbeOutput("", "Mac OS X"))
        assertNull(parseMeegleProbeOutput("meegle not found", "Mac OS X"))
        assertNull(parseMeegleProbeOutput("meegle\n", "Windows 11"))
    }

    @Test
    fun `configured path wins and never triggers a probe`() {
        val runner = RecordingRunner(CommandResult(0, "/opt/homebrew/bin/meegle\n", ""))
        val executable = ConfiguredMeegleExecutable({ " /custom/meegle " }, runner, osName = "Mac OS X")

        assertEquals("/custom/meegle", executable.resolve())
        assertEquals(MeegleCommandSource.CONFIGURED, executable.source())
        assertEquals(0, runner.calls)
    }

    @Test
    fun `macOS executable environment combines login shell and executable directories`() {
        val executable = ConfiguredMeegleExecutable(
            configuredPath = { "/custom/meegle" },
            runner = RecordingRunner(CommandResult(0, "", "")),
            osName = "Mac OS X",
            loginShellPathProvider = { "/opt/homebrew/bin:/usr/bin" },
        )

        val path = executable.environment()["PATH"].orEmpty().split(":")

        assertTrue(path.contains("/opt/homebrew/bin"))
        assertTrue(path.contains("/usr/bin"))
        assertEquals(path.size, path.distinct().size)
    }

    @Test
    fun `non macOS executable does not override the child environment`() {
        var providerCalls = 0
        val executable = ConfiguredMeegleExecutable(
            configuredPath = { "C:\\tools\\meegle.cmd" },
            runner = RecordingRunner(CommandResult(0, "", "")),
            osName = "Windows 11",
            loginShellPathProvider = { providerCalls += 1; "ignored" },
        )

        assertEquals(emptyMap<String, String>(), executable.environment())
        assertEquals(0, providerCalls)
    }

    @Test
    fun `explicit probe retries a missing macOS login shell path while ordinary reads stay cached`() {
        listOf(null, "/custom/meegle").forEach { configuredPath ->
            var providerCalls = 0
            var shellPath: String? = null
            val executable = ConfiguredMeegleExecutable(
                configuredPath = { configuredPath },
                runner = RecordingRunner(CommandResult(0, "/custom/meegle\n", "")),
                osName = "Mac OS X",
                loginShellPathProvider = { providerCalls += 1; shellPath },
            )

            executable.resolve()
            val initialEnvironment = executable.environment()
            shellPath = "/silverwing-fixture-node/bin"
            executable.resolve()
            assertEquals(initialEnvironment, executable.environment())
            assertEquals(1, providerCalls)

            assertEquals("/custom/meegle", executable.probe())
            val refreshedEnvironment = executable.environment()
            assertTrue(refreshedEnvironment["PATH"].orEmpty().split(":").contains("/silverwing-fixture-node/bin"))
            assertEquals(2, providerCalls)
            assertEquals(refreshedEnvironment, executable.environment())
            assertEquals(2, providerCalls)
        }
    }

    @Test
    fun `unconfigured resolution probes once and caches the result`() {
        val runner = RecordingRunner(CommandResult(0, "/opt/homebrew/bin/meegle\n", ""))
        val executable = ConfiguredMeegleExecutable({ null }, runner, osName = "Mac OS X")

        assertEquals("/opt/homebrew/bin/meegle", executable.resolve())
        assertEquals("/opt/homebrew/bin/meegle", executable.resolve())
        assertEquals(MeegleCommandSource.PROBED, executable.source())
        assertEquals(1, runner.calls)
        assertEquals(listOf("/bin/zsh", "-lc", "command -v meegle"), runner.commands.single())
    }

    @Test
    fun `concurrent first resolutions share one probe`() {
        val runner = FirstCallBlockingRunner(CommandResult(0, "/opt/homebrew/bin/meegle\n", ""))
        val executable = ConfiguredMeegleExecutable({ null }, runner, osName = "Mac OS X")
        val workers = Executors.newFixedThreadPool(2)
        try {
            val first = workers.submit<String> { executable.resolve() }
            assertEquals(true, runner.firstCallStarted.await(2, TimeUnit.SECONDS))
            val second = workers.submit<String> { executable.resolve() }
            runner.releaseFirstCall.countDown()

            assertEquals("/opt/homebrew/bin/meegle", first.get(2, TimeUnit.SECONDS))
            assertEquals("/opt/homebrew/bin/meegle", second.get(2, TimeUnit.SECONDS))
            assertEquals(1, runner.calls)
        } finally {
            workers.shutdownNow()
        }
    }

    @Test
    fun `failed probe falls back to the bare command`() {
        val runner = RecordingRunner(CommandResult(1, "", "not found"))
        val executable = ConfiguredMeegleExecutable({ null }, runner, osName = "Windows 11")

        assertEquals("meegle.cmd", executable.resolve())
        assertEquals(MeegleCommandSource.PATH_FALLBACK, executable.source())
    }

    @Test
    fun `probe reruns detection and a later configuration takes over immediately`() {
        var configured: String? = null
        val runner = RecordingRunner(CommandResult(0, "/opt/homebrew/bin/meegle\n", ""))
        val executable = ConfiguredMeegleExecutable({ configured }, runner, osName = "Mac OS X")

        assertEquals("/opt/homebrew/bin/meegle", executable.probe())
        assertEquals(1, runner.calls)
        assertEquals("/opt/homebrew/bin/meegle", executable.probe())
        assertEquals(2, runner.calls)

        configured = "/custom/meegle"
        assertEquals("/custom/meegle", executable.resolve())
        assertEquals(MeegleCommandSource.CONFIGURED, executable.source())
        assertEquals(2, runner.calls)
    }

    @Test
    fun `path normalization accepts blank as auto-detect`() {
        assertNull(normalizeMeegleExecutablePath("   "))
    }

    @Test
    fun `path normalization rejects relative and missing paths`() {
        assertFailsWith<IllegalArgumentException> { normalizeMeegleExecutablePath("bin/meegle") }
        val missing = temporary.resolve("missing-meegle").toAbsolutePath().toString()
        assertFailsWith<IllegalArgumentException> { normalizeMeegleExecutablePath(missing) }
        assertFailsWith<IllegalArgumentException> { normalizeMeegleExecutablePath(temporary.toAbsolutePath().toString()) }
    }

    @Test
    @DisabledOnOs(
        value = [OS.MAC, OS.LINUX],
        disabledReason = "The fixture is created without a POSIX execute bit; Windows still covers this path.",
    )
    fun `path normalization accepts an existing executable file`() {
        val file = Files.writeString(temporary.resolve("meegle"), "#!/bin/sh\n")
        assertEquals(file.toString(), normalizeMeegleExecutablePath(" ${file.toAbsolutePath()} "))
    }

    private class RecordingRunner(
        private val result: CommandResult,
    ) : CommandRunner {
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

    private class FirstCallBlockingRunner(
        private val result: CommandResult,
    ) : CommandRunner {
        val firstCallStarted = CountDownLatch(1)
        val releaseFirstCall = CountDownLatch(1)
        var calls = 0
            private set

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            calls += 1
            if (calls == 1) {
                firstCallStarted.countDown()
                check(releaseFirstCall.await(2, TimeUnit.SECONDS)) { "test did not release the first probe" }
            }
            return result
        }
    }
}
