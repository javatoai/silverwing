package com.snowball.silverwing.core

import java.time.Duration

/**
 * Best-effort result of invoking an executable with --version.
 *
 * Version probing is deliberately separate from executable discovery and from
 * Genbu's production query parser. A broken or unavailable CLI therefore only
 * affects the diagnostic status shown by the settings screen.
 */
data class CommandVersionStatus(
    val command: String,
    val version: String? = null,
    val error: String? = null,
) {
    val succeeded: Boolean
        get() = !version.isNullOrBlank() && error == null

    val versionCommand: List<String>
        get() = commandVersionCommand(command)
}
fun commandVersionCommand(command: String): List<String> = listOf(command, "--version")

/**
 * Runs a CLI's conventional --version command without throwing. The first
 * non-empty output line is used because many CLIs append diagnostics or
 * notices after their version line.
 */
object CommandVersionProbe {
    fun probe(
        command: String,
        runner: CommandRunner = ProcessCommandRunner(),
        timeout: Duration = Duration.ofSeconds(10),
        environment: Map<String, String> = emptyMap(),
    ): CommandVersionStatus {
        val normalized = command.trim()
        if (normalized.isEmpty()) {
            return CommandVersionStatus(command = command, error = "命令为空")
        }
        val result = runCatching {
            runner.run(
                commandVersionCommand(normalized),
                timeout = timeout,
                environment = environment,
            )
        }.getOrElse { error ->
            return CommandVersionStatus(
                command = normalized,
                error = error.message?.takeIf(String::isNotBlank) ?: error::class.simpleName,
            )
        }
        val output = result.stdout.firstNonBlankLine() ?: result.stderr.firstNonBlankLine()
        if (!result.succeeded) {
            return CommandVersionStatus(
                command = normalized,
                error = result.stderr.trim().ifBlank { result.stdout.trim() }
                    .ifBlank { "退出码 ${result.exitCode}" },
            )
        }
        return CommandVersionStatus(
            command = normalized,
            version = output,
        )
    }

    private fun String.firstNonBlankLine(): String? = lineSequence()
        .map(String::trim)
        .firstOrNull(String::isNotBlank)
}
