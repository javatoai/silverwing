package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CodexRequirementAiNamingServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `uses an isolated structured Codex command and sends only the naming payload`() {
        val runner = RecordingInputRunner()
        val service = CodexRequirementAiNamingService(
            paths = ApplicationPaths(temporary.resolve("home")),
            runner = runner,
            codexExecutable = CodexExecutable { "codex-test" },
        )

        val suggestion = service.suggest(
            RequirementAiContext("支付超时问题", "付款后页面等待过长，需要优化超时提示。"),
            forbiddenFolderNames = setOf("支付优化"),
        )

        assertEquals(RequirementAiNamingSuggestion("支付超时优化", "payment_timeout"), suggestion)
        assertEquals("codex-test", runner.command.first())
        assertEquals("gpt-6-luna", runner.command[runner.command.indexOf("--model") + 1])
        assertTrue(runner.command.indexOf("--model") < runner.command.indexOf("exec"))
        assertTrue(runner.command.indexOf("--ask-for-approval") < runner.command.indexOf("exec"))
        assertEquals("never", runner.command[runner.command.indexOf("--ask-for-approval") + 1])
        assertTrue("--ephemeral" in runner.command)
        assertTrue("--skip-git-repo-check" in runner.command)
        assertTrue("read-only" in runner.command)
        assertTrue(runner.input.contains("支付超时问题"))
        assertTrue(runner.input.contains("付款后页面等待过长"))
        assertTrue(runner.input.contains("支付优化"))
        assertFalse(runner.input.contains("feature/"))
        assertFalse(Files.exists(temporary.resolve("home/temp/requirement-ai-naming-result.json")))
    }

    @Test
    fun `uses the configured model as the Codex top level model argument`() {
        val runner = RecordingInputRunner()
        val service = CodexRequirementAiNamingService(
            paths = ApplicationPaths(temporary.resolve("configured-model-home")),
            runner = runner,
            codexExecutable = CodexExecutable { "codex-test" },
            modelProvider = { "gpt-5.6-sol" },
        )

        service.suggest(RequirementAiContext("标题", "正文"))

        assertEquals("gpt-5.6-sol", runner.command[runner.command.indexOf("--model") + 1])
        assertTrue(runner.command.indexOf("--model") < runner.command.indexOf("exec"))
    }

    @Test
    fun `rejects additional output fields even when the command succeeds`() {
        val service = CodexRequirementAiNamingService(
            paths = ApplicationPaths(temporary.resolve("extra-field-home")),
            runner = RecordingInputRunner(
                """{"folderName":"支付超时优化","branchSuffix":"payment_timeout","other":"ignored"}""",
            ),
            codexExecutable = CodexExecutable { "codex-test" },
        )

        assertFailsWith<IllegalArgumentException> {
            service.suggest(RequirementAiContext("标题", "正文"))
        }
    }

    @Test
    fun `queued request model takes precedence over subsequently changed settings`() {
        val runner = RecordingInputRunner()
        val service = CodexRequirementAiNamingService(
            paths = ApplicationPaths(temporary.resolve("captured-model-home")), runner = runner,
            codexExecutable = CodexExecutable { "codex-test" }, modelProvider = { "new-model" },
        )
        service.suggestWithModel(RequirementAiContext("标题", "正文"), emptySet(), "captured-model")
        assertEquals("captured-model", runner.command[runner.command.indexOf("--model") + 1])
    }

    @Test
    fun `rejects numeric JSON branch suffix despite a successful process`() {
        val service = CodexRequirementAiNamingService(
            paths = ApplicationPaths(temporary.resolve("numeric-output-home")),
            runner = RecordingInputRunner("""{"folderName":"支付优化","branchSuffix":123}"""),
            codexExecutable = CodexExecutable { "codex-test" },
        )
        assertFailsWith<IllegalArgumentException> { service.suggest(RequirementAiContext("标题", "正文")) }
    }

    @Test
    fun `unsupported low reasoning falls back once and keeps configured model`() {
        val commands = mutableListOf<List<String>>()
        var notices = 0
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) = error("standard input required")
            override fun runWithInput(command: List<String>, input: String, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                commands += command.toList()
                if ("-c" in command) return CommandResult(1, "", "reasoning effort low is not supported")
                Files.writeString(Path.of(command[command.indexOf("--output-last-message") + 1]), """{"folderName":"支付优化","branchSuffix":"payment_update"}""")
                return CommandResult(0, "", "")
            }
        }
        val service = CodexRequirementAiNamingService(ApplicationPaths(temporary.resolve("fallback")), runner,
            codexExecutable = CodexExecutable { "codex-test" }, onReasoningFallback = { notices++ })
        repeat(2) { service.suggest(RequirementAiContext("支付优化", "")) }
        assertEquals(4, commands.size)
        assertEquals(1, notices)
        commands.forEach { assertEquals("gpt-6-luna", it[it.indexOf("--model") + 1]) }
        assertFalse("-c" in commands[1]); assertFalse("-c" in commands[3])
    }

    private class RecordingInputRunner(
        private val response: String = """{"folderName":"支付超时优化","branchSuffix":"payment_timeout"}""",
    ) : CommandRunner {
        var command: List<String> = emptyList()
        var input: String = ""

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult = error("Codex naming must use standard input")

        override fun runWithInput(
            command: List<String>,
            input: String,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            this.command = command
            this.input = input
            val outputIndex = command.indexOf("--output-last-message")
            val output = Path.of(command[outputIndex + 1])
            Files.writeString(output, response, StandardCharsets.UTF_8)
            return CommandResult(0, "", "")
        }
    }
}
