package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.Locale

enum class GitCommandSource { CONFIGURED, PROBED, PATH_FALLBACK }

/** Resolves the Git command used for every silverwing Git invocation. */
fun interface GitExecutable {
    /** The command to invoke; may trigger a first-time probe. */
    fun resolve(): String

    /** The current command without triggering a probe; safe on the UI thread. */
    fun current(): String = resolve()

    /** Re-detects the command unless an explicit configuration already exists. */
    fun probe(): String = resolve()

    fun source(): GitCommandSource = GitCommandSource.PATH_FALLBACK

    /** Best-effort local --version check for the currently resolved command. */
    fun version(
        runner: CommandRunner = ProcessCommandRunner(),
        timeout: Duration = Duration.ofSeconds(10),
    ): CommandVersionStatus = CommandVersionProbe.probe(current(), runner, timeout)

    companion object {
        /** Historical behavior: let the process PATH resolve the bare Git command. */
        fun pathFallback(): GitExecutable = GitExecutable { gitFallbackCommand() }
    }
}

fun gitFallbackCommand(): String = "git"

private data class GitProbeDefinition(
    val command: List<String>,
    val displayCommand: String,
)

private fun gitProbeDefinition(osName: String): GitProbeDefinition {
    val os = osName.lowercase(Locale.ROOT)
    return when {
        os.contains("win") -> GitProbeDefinition(
            command = listOf("where.exe", "git.exe"),
            displayCommand = "where.exe git.exe",
        )
        os.contains("mac") -> GitProbeDefinition(
            command = listOf("/bin/zsh", "-lc", "command -v git"),
            displayCommand = "/bin/zsh -lc 'command -v git'",
        )
        else -> GitProbeDefinition(
            command = listOf("/bin/bash", "-lc", "command -v git"),
            displayCommand = "/bin/bash -lc 'command -v git'",
        )
    }
}

fun gitProbeCommand(osName: String): List<String> = gitProbeDefinition(osName).command

fun gitProbeCommandDisplay(osName: String): String = gitProbeDefinition(osName).displayCommand

/** First output line that is an absolute path; anything else means "not found". */
fun parseGitProbeOutput(output: String, osName: String): String? {
    val isWindows = osName.lowercase(Locale.ROOT).contains("win")
    val windowsAbsolute = Regex("""^[A-Za-z]:[\\/].+""")
    return output.lineSequence()
        .map(String::trim)
        .firstOrNull { line -> if (isWindows) windowsAbsolute.matches(line) else line.startsWith("/") }
}

/** Normalizes a user-entered Git executable path; null means "auto-detect". */
fun normalizeGitExecutablePath(raw: String): String? {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) return null
    val path = Path.of(trimmed)
    require(path.isAbsolute) { "Git 命令路径必须是绝对路径" }
    require(Files.isRegularFile(path)) { "Git 命令路径不是文件：$trimmed" }
    require(Files.isExecutable(path)) { "Git 命令不可执行：$trimmed" }
    return path.toString()
}

/** Explicit configuration wins over one cached platform-specific probe. */
class ConfiguredGitExecutable(
    private val configuredPath: () -> String?,
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val osName: String = System.getProperty("os.name"),
    private val probeTimeout: Duration = Duration.ofSeconds(5),
) : GitExecutable {
    @Volatile private var probedPath: String? = null
    @Volatile private var probeAttempted = false

    override fun resolve(): String = synchronized(this) {
        configured()?.let { return it }
        if (!probeAttempted) probeLocked()
        return probedPath ?: gitFallbackCommand()
    }

    override fun current(): String = configured() ?: probedPath ?: gitFallbackCommand()

    override fun probe(): String = synchronized(this) {
        configured()?.let {
            probedPath = null
            probeAttempted = false
            return it
        }
        probeAttempted = false
        probeLocked()
        return probedPath ?: gitFallbackCommand()
    }

    override fun source(): GitCommandSource = when {
        configured() != null -> GitCommandSource.CONFIGURED
        probedPath != null -> GitCommandSource.PROBED
        else -> GitCommandSource.PATH_FALLBACK
    }

    private fun probeLocked() {
        val found = runCatching {
            val result = runner.run(gitProbeCommand(osName), timeout = probeTimeout)
            if (result.succeeded) parseGitProbeOutput(result.stdout, osName) else null
        }.getOrNull()
        probedPath = found
        probeAttempted = true
    }

    private fun configured(): String? = configuredPath()?.trim()?.takeIf(String::isNotEmpty)
}
