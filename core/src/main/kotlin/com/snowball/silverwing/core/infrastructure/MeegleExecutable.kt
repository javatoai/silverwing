package com.snowball.silverwing.core

import java.io.File
import java.nio.file.Path
import java.time.Duration
import java.util.Locale

enum class MeegleCommandSource { CONFIGURED, PROBED, PATH_FALLBACK }

/** Resolves the Meegle CLI command used for every invocation. */
fun interface MeegleExecutable {
    /** The command to invoke; may trigger a first-time probe. */
    fun resolve(): String

    /** The current command without triggering a probe; safe on the UI thread. */
    fun current(): String = resolve()

    /**
     * Environment additions required by the command when it is launched from
     * a GUI process.  Most executables need no additions; macOS Node-based
     * scripts use the user's login-shell PATH so `/usr/bin/env node` can find
     * the runtime even when silverwing was opened from Finder or a DMG.
     */
    fun environment(): Map<String, String> = emptyMap()

    /** Re-detects the command; static implementations never change. */
    fun probe(): String = resolve()

    fun source(): MeegleCommandSource = MeegleCommandSource.PATH_FALLBACK

    /** Best-effort local --version check for the currently resolved command. */
    fun version(
        runner: CommandRunner = ProcessCommandRunner(),
        timeout: Duration = Duration.ofSeconds(10),
    ): CommandVersionStatus = CommandVersionProbe.probe(current(), runner, timeout, environment())

    companion object {
        /** The historical behavior: rely on the process PATH with a bare command. */
        fun pathFallback(isWindows: Boolean = defaultIsWindows()): MeegleExecutable =
            MeegleExecutable { meegleFallbackCommand(isWindows) }
    }
}

fun meegleFallbackCommand(isWindows: Boolean): String = if (isWindows) "meegle.cmd" else "meegle"

/**
 * The detection command per platform. GUI processes inherit a minimal PATH on
 * macOS/Linux, so detection runs inside a login shell that sources the user's
 * profile (Homebrew puts `/opt/homebrew/bin` on PATH via ~/.zprofile).
 */
private data class MeegleProbeDefinition(
    val command: List<String>,
    val displayCommand: String,
)

private fun meegleProbeDefinition(osName: String): MeegleProbeDefinition {
    val os = osName.lowercase(Locale.ROOT)
    return when {
        os.contains("win") -> MeegleProbeDefinition(
            command = listOf("where.exe", "meegle.cmd"),
            displayCommand = "where.exe meegle.cmd",
        )
        os.contains("mac") -> MeegleProbeDefinition(
            command = listOf("/bin/zsh", "-lc", "command -v meegle"),
            displayCommand = "/bin/zsh -lc 'command -v meegle'",
        )
        else -> MeegleProbeDefinition(
            command = listOf("/bin/bash", "-lc", "command -v meegle"),
            displayCommand = "/bin/bash -lc 'command -v meegle'",
        )
    }
}

fun meegleProbeCommand(osName: String): List<String> = meegleProbeDefinition(osName).command

fun meegleProbeCommandDisplay(osName: String): String = meegleProbeDefinition(osName).displayCommand

/** First output line that is an absolute path; anything else means "not found". */
fun parseMeegleProbeOutput(output: String, osName: String): String? {
    val isWindows = osName.lowercase(Locale.ROOT).contains("win")
    val windowsAbsolute = Regex("""^[A-Za-z]:[\\/].+""")
    return output.lineSequence()
        .map { it.trim() }
        .firstOrNull { line ->
            if (isWindows) windowsAbsolute.matches(line) else line.startsWith("/")
        }
}

private fun defaultIsWindows(): Boolean =
    System.getProperty("os.name").lowercase(Locale.ROOT).contains("win")

/** Normalizes a user-entered executable path; null means "auto-detect". */
fun normalizeMeegleExecutablePath(raw: String): String? {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) return null
    val path = java.nio.file.Path.of(trimmed)
    require(path.isAbsolute) { "Meegle 命令路径必须是绝对路径" }
    require(java.nio.file.Files.exists(path)) { "Meegle 命令路径不存在：$trimmed" }
    require(java.nio.file.Files.isRegularFile(path)) { "Meegle 命令路径必须是文件：$trimmed" }
    require(java.nio.file.Files.isExecutable(path)) { "Meegle 命令不可执行：$trimmed" }
    return trimmed
}

/**
 * Three-level resolution: explicit configuration, then a cached login-shell
 * probe, then the bare-command PATH fallback that preserves legacy errors.
 */
class ConfiguredMeegleExecutable(
    private val configuredPath: () -> String?,
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val osName: String = System.getProperty("os.name"),
    private val probeTimeout: Duration = Duration.ofSeconds(5),
    private val loginShellPathProvider: () -> String? = ::loadMacLoginShellPath,
) : MeegleExecutable {
    @Volatile private var probedPath: String? = null
    @Volatile private var probeAttempted = false
    @Volatile private var loginShellPath: Lazy<String?> = lazy(loginShellPathProvider)

    override fun resolve(): String = synchronized(this) {
        configured()?.let { return it }
        if (!probeAttempted) probeLocked()
        return probedPath ?: meegleFallbackCommand(isWindows())
    }

    override fun current(): String =
        configured() ?: probedPath ?: meegleFallbackCommand(isWindows())

    override fun environment(): Map<String, String> {
        if (!isMac()) return emptyMap()
        val separator = pathSeparator()
        val pathEntries = buildList {
            executableDirectory(current())?.let(::add)
            loginShellPath.value
                ?.split(separator)
                ?.forEach(::add)
            System.getenv("PATH")
                ?.split(separator)
                ?.forEach(::add)
        }
            .map(String::trim)
            .filter(String::isNotEmpty)
            .distinct()
        return pathEntries.takeIf(List<String>::isNotEmpty)
            ?.let { mapOf("PATH" to it.joinToString(separator)) }
            ?: emptyMap()
    }

    override fun probe(): String = synchronized(this) {
        loginShellPath = lazy(loginShellPathProvider)
        configured()?.let {
            probedPath = null
            probeAttempted = false
            return it
        }
        probeAttempted = false
        probeLocked()
        return probedPath ?: meegleFallbackCommand(isWindows())
    }

    private fun probeLocked() {
        val found = runCatching {
            val result = runner.run(meegleProbeCommand(osName), timeout = probeTimeout)
            if (result.succeeded) parseMeegleProbeOutput(result.stdout, osName) else null
        }.getOrNull()
        probedPath = found
        probeAttempted = true
    }

    override fun source(): MeegleCommandSource = when {
        configured() != null -> MeegleCommandSource.CONFIGURED
        probedPath != null -> MeegleCommandSource.PROBED
        else -> MeegleCommandSource.PATH_FALLBACK
    }

    private fun configured(): String? = configuredPath()?.trim()?.takeIf(String::isNotEmpty)

    private fun isWindows(): Boolean = osName.lowercase(Locale.ROOT).contains("win")

    private fun isMac(): Boolean = osName.lowercase(Locale.ROOT).contains("mac")

    private fun pathSeparator(): String = if (isWindows()) File.pathSeparator else ":"

    private fun executableDirectory(command: String): String? = runCatching {
        Path.of(command).takeIf(Path::isAbsolute)?.parent?.toString()
    }.getOrNull()
}

/**
 * Finder-launched applications do not inherit the interactive terminal PATH.
 * Cache the user's macOS login-shell PATH until explicit re-detection so Node-backed CLI scripts can
 * resolve their `#!/usr/bin/env node` interpreter.
 */
private fun loadMacLoginShellPath(): String? = runCatching {
    val result = ProcessCommandRunner().run(
        command = listOf("/bin/zsh", "-lc", "printf '%s\\n' \"\$PATH\""),
        timeout = Duration.ofSeconds(3),
    )
    if (!result.succeeded) return@runCatching null
    result.stdout.lineSequence()
        .map(String::trim)
        .filter(String::isNotEmpty)
        .lastOrNull()
}.getOrNull()
