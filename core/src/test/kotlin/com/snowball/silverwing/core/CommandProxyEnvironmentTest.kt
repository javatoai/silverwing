package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

class CommandProxyEnvironmentTest {
    @Test
    fun `normalizes only unauthenticated HTTP host port endpoints`() {
        assertEquals("http://127.0.0.1:7890", normalizeCommandProxyUrl(" http://127.0.0.1:7890/ "))
        assertEquals("https://proxy.example.test:443", normalizeCommandProxyUrl("https://proxy.example.test:443"))
        assertEquals("socks5://127.0.0.1:1080", normalizeCommandProxyUrl("socks5h://127.0.0.1:1080"))
        assertNull(normalizeCommandProxyUrl("   "))

        listOf(
            "socks4://127.0.0.1:7890",
            "http://user:secret@127.0.0.1:7890",
            "http://127.0.0.1:7890/path",
            "http://127.0.0.1:7890?debug=true",
            "http://127.0.0.1",
        ).forEach { value ->
            assertFailsWith<IllegalArgumentException>(value) { normalizeCommandProxyUrl(value) }
        }
    }

    @Test
    fun `proxy form validates host port exclusions and authentication as separate values`() {
        assertEquals(
            CommandProxyEndpoint(CommandProxyProtocol.HTTPS, "proxy.example.test", 443),
            commandProxyEndpoint("https://proxy.example.test:443"),
        )
        assertEquals(
            "http://127.0.0.1:7890",
            buildCommandProxyUrl(CommandProxyProtocol.HTTP, "127.0.0.1", "7890"),
        )
        assertEquals(
            "socks5://127.0.0.1:1080",
            buildCommandProxyUrl(CommandProxyProtocol.SOCKS5, "127.0.0.1", "1080"),
        )
        assertEquals("localhost,*.example.test,192.168.*", normalizeCommandProxyNoProxy(" localhost, *.example.test, 192.168.* "))
        assertEquals(
            "https://proxy%20user:p%40ss%3Aword@proxy.example.test:443",
            authenticatedCommandProxyUrl(
                "https://proxy.example.test:443",
                commandProxyAuthentication("proxy user", "p@ss:word"),
            ),
        )

        assertFailsWith<IllegalArgumentException> { buildCommandProxyUrl(CommandProxyProtocol.HTTP, "proxy.example.test", "70000") }
        assertFailsWith<IllegalArgumentException> { normalizeCommandProxyNoProxy("http://localhost:8080") }
        assertFailsWith<IllegalArgumentException> { commandProxyAuthentication("proxy-user", "") }
        assertFailsWith<IllegalArgumentException> { commandProxyAuthentication("", "secret") }
    }

    @Test
    fun `only selected command targets receive both proxy casings`() {
        val endpoint = AtomicReference<String?>("http://127.0.0.1:7890")
        val targets = AtomicReference(setOf(CommandProxyTarget.CODEX, CommandProxyTarget.GIT))
        val provider = CommandProxyEnvironmentProvider(endpoint::get, targets::get)

        CommandProxyTarget.entries.forEach { target ->
            val environment = provider.forTarget(target)
            assertEquals(target in targets.get(), environment.enabled, target.name)
            assertEquals(PROXY_ENVIRONMENT_VARIABLES, environment.removals)
            if (environment.enabled) {
                assertEquals("http://127.0.0.1:7890", environment.additions.getValue("HTTP_PROXY"))
                assertEquals("http://127.0.0.1:7890", environment.additions.getValue("http_proxy"))
                assertEquals("http://127.0.0.1:7890", environment.additions.getValue("ALL_PROXY"))
            } else {
                assertTrue(environment.additions.isEmpty())
            }
        }
    }

    @Test
    fun `targeted runner strips inherited proxy values when the target is unchecked`() {
        val targets = AtomicReference(emptySet<CommandProxyTarget>())
        val provider = CommandProxyEnvironmentProvider({ "http://127.0.0.1:7890" }, targets::get)
        val delegate = CapturingRunner()
        val runner = ProxyTargetCommandRunner(delegate, CommandProxyTarget.MEEGLE, provider)

        runner.run(
            command = listOf("meegle", "version"),
            environment = mapOf("PATH" to "test-path", "NO_PROXY" to "localhost", "http_proxy" to "old"),
        )

        assertFalse(delegate.environment.keys.any { it.equals("NO_PROXY", ignoreCase = true) })
        assertFalse(delegate.environment.keys.any { it.equals("HTTP_PROXY", ignoreCase = true) })
        assertEquals("test-path", delegate.environment["PATH"])
        assertEquals(PROXY_ENVIRONMENT_VARIABLES, delegate.removals)
    }

    @Test
    fun `targeted runner injects authenticated proxy and configured exclusions without preserving inherited values`() {
        val provider = CommandProxyEnvironmentProvider(
            proxyUrl = { "https://proxy.example.test:443" },
            enabledTargets = { setOf(CommandProxyTarget.GIT) },
            noProxy = { "localhost,*.internal" },
            authentication = { commandProxyAuthentication("proxy-user", "secret") },
        )
        val delegate = CapturingRunner()
        val runner = ProxyTargetCommandRunner(delegate, CommandProxyTarget.GIT, provider)

        runner.run(
            command = listOf("git", "ls-remote"),
            environment = mapOf("NO_PROXY" to "localhost", "HTTP_PROXY" to "old"),
        )

        assertEquals("https://proxy-user:secret@proxy.example.test:443", delegate.environment["HTTP_PROXY"])
        assertEquals("https://proxy-user:secret@proxy.example.test:443", delegate.environment["HTTPS_PROXY"])
        assertEquals("https://proxy-user:secret@proxy.example.test:443", delegate.environment["ALL_PROXY"])
        assertEquals("localhost,*.internal", delegate.environment["NO_PROXY"])
        assertEquals("localhost,*.internal", delegate.environment["no_proxy"])
    }

    @Test
    fun `targeted runner injects SOCKS5 into every proxy environment variable`() {
        val provider = CommandProxyEnvironmentProvider(
            proxyUrl = { "socks5://127.0.0.1:1080" },
            enabledTargets = { setOf(CommandProxyTarget.CODEX) },
            authentication = { commandProxyAuthentication("proxy-user", "secret") },
        )
        val delegate = CapturingRunner()
        val runner = ProxyTargetCommandRunner(delegate, CommandProxyTarget.CODEX, provider)

        runner.run(command = listOf("codex", "--version"))

        val expected = "socks5://proxy-user:secret@127.0.0.1:1080"
        assertEquals(expected, delegate.environment["HTTP_PROXY"])
        assertEquals(expected, delegate.environment["HTTPS_PROXY"])
        assertEquals(expected, delegate.environment["ALL_PROXY"])
        assertEquals(expected, delegate.environment["http_proxy"])
        assertEquals(expected, delegate.environment["https_proxy"])
        assertEquals(expected, delegate.environment["all_proxy"])
    }

    @Test
    fun `clearing endpoint forces every target to direct execution`() {
        val endpoint = AtomicReference<String?>(null)
        val provider = CommandProxyEnvironmentProvider({ endpoint.get() }, { CommandProxyTarget.entries.toSet() })

        CommandProxyTarget.entries.forEach { target ->
            assertFalse(provider.forTarget(target).enabled, target.name)
        }
        assertFailsWith<IllegalArgumentException> {
            AppConfig(commandProxyTargets = setOf(CommandProxyTarget.CODEX))
        }
    }

    @Test
    fun `diagnostics redact the configured endpoint and credentials without changing other text`() {
        val provider = CommandProxyEnvironmentProvider(
            proxyUrl = { "http://127.0.0.1:7890" },
            enabledTargets = { emptySet() },
            authentication = { commandProxyAuthentication("proxy-user", "secret") },
        )

        val message = provider.redactConfiguredEndpoint(
            "failed to connect to http://proxy-user:secret@127.0.0.1:7890 while fetching",
        )

        assertFalse(message.contains("127.0.0.1:7890"))
        assertFalse(message.contains("proxy-user"))
        assertFalse(message.contains("secret"))
        assertTrue(message.contains("[代理地址已隐藏]"))
        assertTrue(message.contains("while fetching"))
    }

    @Test
    fun `controlled process environment removes proxy variables before optional additions`() {
        val environment = linkedMapOf(
            "PATH" to "test-path",
            "HTTP_PROXY" to "inherited-http",
            "http_proxy" to "inherited-http-lower",
            "NO_PROXY" to "localhost",
            "no_proxy" to "localhost-lower",
        )

        applyCommandEnvironment(
            processEnvironment = environment,
            additions = mapOf("HTTPS_PROXY" to "http://127.0.0.1:7890"),
            removals = PROXY_ENVIRONMENT_VARIABLES,
        )

        assertEquals("test-path", environment["PATH"])
        assertFalse(environment.keys.any { it.equals("HTTP_PROXY", ignoreCase = true) })
        assertFalse(environment.keys.any { it.equals("NO_PROXY", ignoreCase = true) })
        assertEquals("http://127.0.0.1:7890", environment["HTTPS_PROXY"])
    }

    private class CapturingRunner : StreamingCommandRunner {
        var environment: Map<String, String> = emptyMap()
        var removals: Set<String> = emptySet()

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult = CommandResult(0, "", "")

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
            environmentToRemove: Set<String>,
        ): CommandResult {
            this.environment = environment
            removals = environmentToRemove
            return CommandResult(0, "", "")
        }

        override fun runStreaming(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
            onOutput: (CommandOutputLine) -> Unit,
        ): CommandResult = CommandResult(0, "", "")
    }
}
