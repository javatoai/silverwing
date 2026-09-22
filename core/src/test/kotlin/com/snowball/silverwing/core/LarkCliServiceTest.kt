package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import java.nio.file.Path
import java.time.Duration

class LarkCliServiceTest {
    @Test
    fun `status reads safe user identity without exposing profile details`() {
        val commands = mutableListOf<List<String>>()
        val runner = recordingRunner { command ->
            commands += command
            if (command.last() == "--version") {
                CommandResult(0, "lark-cli version 1.0.96\n", "")
            } else {
                CommandResult(
                    0,
                    """{"appId":"cli_x","brand":"feishu","identities":{"user":{"status":"ready","available":true,"openId":"secret-open-id","userName":"secret-name","tokenStatus":"valid","expiresAt":"2026-09-22T20:00:00+08:00"}}}""",
                    "",
                )
            }
        }

        val status = ProcessLarkCliService(runner, isWindows = true).status()

        assertTrue(status.installed)
        assertTrue(status.authenticated)
        assertEquals("1.0.96", status.version)
        assertEquals("feishu", status.brand)
        assertEquals("valid", status.tokenStatus)
        assertEquals("2026-09-22T20:00:00+08:00", status.expiresAt)
        assertFalse(status.authenticationError.orEmpty().contains("secret"))
        assertEquals(listOf("lark-cli.cmd", "auth", "status", "--json"), commands[1])
    }

    @Test
    fun `status retries without json when a legacy cli rejects the flag`() {
        val commands = mutableListOf<List<String>>()
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                commands += command
                when {
                    command.last() == "--version" -> CommandResult(0, "lark-cli version 1.0.41", "")
                    command.last() == "--json" -> CommandResult(1, "", "Error: unknown flag: --json")
                    else -> CommandResult(
                        0,
                        """{"brand":"feishu","identities":{"user":{"status":"ready","available":true,"tokenStatus":"valid"}}}""",
                        "",
                    )
                }
            },
            isWindows = false,
        )

        val status = service.status()

        assertTrue(status.authenticated)
        assertEquals(
            listOf(
                listOf("lark-cli", "--version"),
                listOf("lark-cli", "auth", "status", "--json"),
                listOf("lark-cli", "auth", "status"),
            ),
            commands,
        )
    }

    @Test
    fun `status accepts an available user while the cli refreshes its credential`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                if (command.last() == "--version") CommandResult(0, "1.0.41", "") else CommandResult(
                    0,
                    """{"brand":"feishu","identities":{"user":{"status":"ready","available":true,"tokenStatus":"needs_refresh"}}}""",
                    "",
                )
            },
            isWindows = false,
        )

        val status = service.status()

        assertTrue(status.authenticated)
        assertEquals("needs_refresh", status.tokenStatus)
        assertTrue(status.authenticationError == null)
    }

    @Test
    fun `status accepts the current cli success envelope`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                if (command.last() == "--version") CommandResult(0, "1.0.96", "") else CommandResult(
                    0,
                    """{"ok":true,"identity":"user","data":{"brand":"feishu","identities":{"user":{"status":"ready","available":true,"tokenStatus":"valid"}}}}""",
                    "",
                )
            },
            isWindows = false,
        )

        val status = service.status()

        assertTrue(status.authenticated)
        assertEquals("feishu", status.brand)
    }

    @Test
    fun `status reports a current cli error envelope as a check failure`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                if (command.last() == "--version") CommandResult(0, "1.0.96", "") else CommandResult(
                    0,
                    """{"ok":false,"error":{"message":"credential store is unavailable","type":"credential_error"}}""",
                    "",
                )
            },
            isWindows = false,
        )

        val status = service.status()

        assertFalse(status.authenticated)
        assertEquals(LarkAuthenticationState.CHECK_FAILED, status.authenticationState)
        assertTrue(status.authenticationError.orEmpty().contains("credential store is unavailable"))
    }

    @Test
    fun `login requires selected domains and never defaults to all`() {
        val commands = mutableListOf<List<String>>()
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                commands += command
                CommandResult(0, """{"verification_url":"https://example.test/verify","device_code":"opaque-code","expires_in":240}""", "")
            },
            isWindows = false,
        )

        assertFailsWith<IllegalArgumentException> { service.beginDeviceCodeLogin(emptyList()) }
        val challenge = service.beginDeviceCodeLogin(listOf("docs", "drive", "docs"))

        assertEquals("https://example.test/verify", challenge.verificationUrl)
        assertEquals(240, challenge.expiresInSeconds)
        assertEquals(
            listOf("lark-cli", "auth", "login", "--domain", "docs", "--domain", "drive", "--no-wait", "--json"),
            commands.single(),
        )
    }

    @Test
    fun `device code initialization accepts the current cli success envelope`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner {
                CommandResult(
                    0,
                    """{"ok":true,"data":{"verification_url":"https://example.test/verify","device_code":"opaque-code","expires_in":240}}""",
                    "",
                )
            },
            isWindows = false,
        )

        val challenge = service.beginDeviceCodeLogin(listOf("docs"))

        assertEquals("https://example.test/verify", challenge.verificationUrl)
        assertEquals(240, challenge.expiresInSeconds)
    }

    @Test
    fun `device code continuation uses opaque code only inside the process command`() {
        val commands = mutableListOf<List<String>>()
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                commands += command
                when {
                    command.contains("--version") -> CommandResult(0, "1.0.96", "")
                    command.contains("--no-wait") -> CommandResult(
                        0,
                        "{\"verification_url\":\"https://example.test/verify\",\"device_code\":\"opaque-code\",\"expires_in\":120}",
                        "",
                    )
                    else -> CommandResult(0, "{}", "")
                }
            },
            isWindows = false,
        )

        val challenge = service.beginDeviceCodeLogin(listOf("task"))
        service.completeDeviceCodeLogin(challenge)

        assertEquals("opaque-code", commands[1][commands[1].indexOf("--device-code") + 1])
        assertTrue(challenge.redactSecrets("opaque-code https://example.test/verify").contains("[已隐藏]"))
        assertFalse(challenge.redactSecrets("opaque-code https://example.test/verify").contains("opaque-code"))
    }

    @Test
    fun `logout retries without json when a legacy cli rejects the flag`() {
        val commands = mutableListOf<List<String>>()
        val service = ProcessLarkCliService(
            runner = recordingRunner { command ->
                commands += command
                if (command.last() == "--json") {
                    CommandResult(1, "", "Error: unknown flag: --json")
                } else {
                    CommandResult(0, "logged out", "")
                }
            },
            isWindows = true,
        )

        service.logout()

        assertEquals(
            listOf(
                listOf("lark-cli.cmd", "auth", "logout", "--json"),
                listOf("lark-cli.cmd", "auth", "logout"),
            ),
            commands,
        )
    }

    @Test
    fun `logout reports non compatibility failures`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner { CommandResult(1, "", "credential store is locked") },
            isWindows = true,
        )

        val error = assertFailsWith<IllegalStateException> { service.logout() }

        assertTrue(error.message.orEmpty().contains("credential store is locked"))
    }

    @Test
    fun `logout accepts a success envelope whose data is not an object`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner { CommandResult(0, """{"ok":true,"data":"logged out"}""", "") },
            isWindows = false,
        )

        service.logout()
    }

    @Test
    fun `logout accepts a success envelope without data`() {
        val service = ProcessLarkCliService(
            runner = recordingRunner { CommandResult(0, """{"ok":true}""", "") },
            isWindows = false,
        )

        service.logout()
    }

    @Test
    fun `runner exceptions are surfaced without token or authorization URL`() {
        val service = ProcessLarkCliService(
            runner = object : CommandRunner {
                override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                    if (command.last() == "--version") return CommandResult(0, "lark-cli version 1.0.96", "")
                    error("access_token=secret-token https://example.test/verify")
                }
            },
            isWindows = false,
        )

        val status = service.status()

        assertFalse(status.authenticated)
        assertEquals("access_token=[已隐藏] [链接已隐藏]", status.authenticationError)
    }

    private fun recordingRunner(onRun: (List<String>) -> CommandResult): CommandRunner = object : CommandRunner {
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult =
            onRun(command)
    }
}
