package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertFailsWith
import java.nio.file.Path
import java.time.Duration

class LarkExecutableTest {
    @Test
    fun `fallback and probe commands match the installed executable name`() {
        assertEquals("lark-cli.cmd", larkFallbackCommand(true))
        assertEquals("lark-cli", larkFallbackCommand(false))
        assertEquals(listOf("where.exe", "lark-cli.cmd"), larkProbeCommand("Windows 11"))
        assertEquals(listOf("/bin/zsh", "-lc", "command -v lark-cli"), larkProbeCommand("Mac OS X"))
        assertEquals(listOf("/bin/bash", "-lc", "command -v lark-cli"), larkProbeCommand("Linux"))
    }

    @Test
    fun `probe parser takes first absolute path`() {
        assertEquals("C:\\tools\\lark-cli.cmd", parseLarkProbeOutput("alias\nC:\\tools\\lark-cli.cmd\n", "Windows 11"))
        assertEquals("/usr/local/bin/lark-cli", parseLarkProbeOutput("/usr/local/bin/lark-cli\n", "Linux"))
        assertNull(parseLarkProbeOutput("lark-cli not found", "Linux"))
    }

    @Test
    fun `configured command does not probe and always disables notifier noise`() {
        var calls = 0
        val executable = ConfiguredLarkExecutable(
            configuredPath = { "C:\\tools\\lark-cli.cmd" },
            runner = object : CommandRunner {
                override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                    calls += 1
                    return CommandResult(0, "", "")
                }
            },
            osName = "Windows 11",
        )

        assertEquals("C:\\tools\\lark-cli.cmd", executable.resolve())
        assertEquals(LarkCommandSource.CONFIGURED, executable.source())
        assertEquals("1", executable.environment()["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"])
        assertEquals("1", executable.environment()["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"])
        assertEquals(0, calls)
    }

    @Test
    fun `blank path means auto detection and relative path is rejected`() {
        assertNull(normalizeLarkExecutablePath("   "))
        assertFailsWith<IllegalArgumentException> { normalizeLarkExecutablePath("bin/lark-cli") }
    }
}
