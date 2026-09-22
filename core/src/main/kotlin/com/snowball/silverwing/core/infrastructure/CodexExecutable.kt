package com.snowball.silverwing.core

import java.io.File
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.Locale

enum class CodexCommandSource { CONFIGURED, DESKTOP_APP, PROBED, PATH_FALLBACK }

/**
 * Resolves the Codex CLI to a command that a GUI-launched JVM can execute.
 *
 * Windows desktop applications frequently inherit a stale or minimal PATH,
 * while the Codex desktop application keeps its own native CLI beneath
 * `%LOCALAPPDATA%/OpenAI/Codex/bin`. Prefer that absolute executable before
 * falling back to a platform PATH probe.
 */
fun interface CodexExecutable {
    /** The command to invoke; may trigger a first-time probe. */
    fun resolve(): String

    /** The current command without triggering a probe; safe on the UI thread. */
    fun current(): String = resolve()

    /** Environment additions required when the command was found through a macOS login shell. */
    fun environment(): Map<String, String> = emptyMap()

    /** Re-detects the command; static implementations never change. */
    fun probe(): String = resolve()

    fun source(): CodexCommandSource = CodexCommandSource.PATH_FALLBACK

    /** Best-effort local --version check for the currently resolved command. */
    fun version(
        runner: CommandRunner = ProcessCommandRunner(),
        timeout: Duration = Duration.ofSeconds(10),
    ): CommandVersionStatus = CommandVersionProbe.probe(current(), runner, timeout, environment())

    companion object {
        /** Historical behavior for installations that cannot be auto-detected. */
        fun pathFallback(): CodexExecutable = CodexExecutable(::codexFallbackCommand)
    }
}

fun codexFallbackCommand(): String = "codex"

/** Normalizes a user-entered executable path; null means "auto-detect". */
fun normalizeCodexExecutablePath(raw: String): String? {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) return null
    val path = Path.of(trimmed)
    require(path.isAbsolute) { "Codex CLI 命令路径必须是绝对路径" }
    require(Files.exists(path)) { "Codex CLI 命令路径不存在：$trimmed" }
    require(Files.isRegularFile(path)) { "Codex CLI 命令路径必须是文件：$trimmed" }
    require(Files.isExecutable(path)) { "Codex CLI 命令不可执行：$trimmed" }
    return trimmed
}

private fun codexProbeCommandsFor(osName: String): List<List<String>> {
    val os = osName.lowercase(Locale.ROOT)
    return when {
        os.contains("win") -> listOf(
            listOf("where.exe", "codex.exe"),
            listOf("where.exe", "codex.cmd"),
        )
        os.contains("mac") -> listOf(listOf("/bin/zsh", "-lc", "command -v codex"))
        else -> listOf(listOf("/bin/bash", "-lc", "command -v codex"))
    }
}

fun codexProbeCommands(osName: String): List<List<String>> = codexProbeCommandsFor(osName)

/** First output line that is an absolute path; anything else means "not found". */
fun parseCodexProbeOutput(output: String, osName: String): String? {
    val isWindows = osName.lowercase(Locale.ROOT).contains("win")
    val windowsAbsolute = Regex("""^[A-Za-z]:[\\/].+""")
    return output.lineSequence()
        .map(String::trim)
        .firstOrNull { line -> if (isWindows) windowsAbsolute.matches(line) else line.startsWith("/") }
}

/** Desktop-native Codex resolution, followed by a cached platform probe. */
class AutoDetectedCodexExecutable(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val osName: String = System.getProperty("os.name"),
    private val probeTimeout: Duration = Duration.ofSeconds(5),
    private val desktopCodexBinDirectory: () -> Path? = ::defaultDesktopCodexBinDirectory,
    private val loginShellPathProvider: () -> String? = ::loadCodexMacLoginShellPath,
) : CodexExecutable {
    @Volatile private var resolved: ResolvedCodex? = null
    @Volatile private var probeAttempted = false
    @Volatile private var loginShellPath: Lazy<String?> = lazy(loginShellPathProvider)

    override fun resolve(): String = synchronized(this) {
        if (!probeAttempted) probeLocked()
        return resolved?.command ?: codexFallbackCommand()
    }

    override fun current(): String = resolved?.command ?: codexFallbackCommand()

    override fun environment(): Map<String, String> {
        if (!isMac()) return emptyMap()
        val separator = File.pathSeparator
        val pathEntries = buildList {
            executableDirectory(current())?.let(::add)
            loginShellPath.value?.split(separator)?.forEach(::add)
            System.getenv("PATH")?.split(separator)?.forEach(::add)
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
        probeAttempted = false
        probeLocked()
        return resolved?.command ?: codexFallbackCommand()
    }

    override fun source(): CodexCommandSource = resolved?.source ?: CodexCommandSource.PATH_FALLBACK

    private fun probeLocked() {
        resolved = findDesktopCodexExecutable()
            ?.let { command -> ResolvedCodex(command, CodexCommandSource.DESKTOP_APP) }
            ?: findProbedCodexExecutable()
                ?.let { command -> ResolvedCodex(command, CodexCommandSource.PROBED) }
        probeAttempted = true
    }

    private fun findDesktopCodexExecutable(): String? {
        if (!isWindows()) return null
        val root = runCatching(desktopCodexBinDirectory).getOrNull() ?: return null
        if (!Files.isDirectory(root)) return null
        val candidates = buildList {
            root.resolve("codex.exe").takeIf(Files::isRegularFile)?.let(::add)
            runCatching {
                Files.list(root).use { versions ->
                    versions
                        .filter(Files::isDirectory)
                        .map { it.resolve("codex.exe") }
                        .filter(Files::isRegularFile)
                        .forEach(::add)
                }
            }
        }
        return candidates.maxWithOrNull(
            compareBy<Path> { path ->
                runCatching { Files.getLastModifiedTime(path).toMillis() }.getOrDefault(Long.MIN_VALUE)
            }.thenBy { path -> path.toString() },
        )?.let(::normalizedPath)
    }

    private fun findProbedCodexExecutable(): String? = codexProbeCommandsFor(osName)
        .asSequence()
        .mapNotNull { command ->
            runCatching {
                val result = runner.run(command, timeout = probeTimeout)
                result.takeIf { it.succeeded }
                    ?.let { parseCodexProbeOutput(it.stdout, osName) }
            }.getOrNull()
        }
        .firstOrNull()

    private fun isWindows(): Boolean = osName.lowercase(Locale.ROOT).contains("win")

    private fun isMac(): Boolean = osName.lowercase(Locale.ROOT).contains("mac")

    private fun executableDirectory(command: String): String? = runCatching {
        Path.of(command).takeIf(Path::isAbsolute)?.parent?.toString()
    }.getOrNull()

    private data class ResolvedCodex(
        val command: String,
        val source: CodexCommandSource,
    )
}

/**
 * Resolves a user-configured Codex CLI path before using desktop discovery or
 * the process PATH. The explicit setting applies consistently to requirement
 * AI naming and Codex plugin management.
 */
class ConfiguredCodexExecutable(
    private val configuredPath: () -> String?,
    private val automatic: CodexExecutable = AutoDetectedCodexExecutable(),
    private val osName: String = System.getProperty("os.name"),
) : CodexExecutable {
    override fun resolve(): String = configured() ?: automatic.resolve()

    override fun current(): String = configured() ?: automatic.current()

    override fun environment(): Map<String, String> {
        val configured = configured() ?: return automatic.environment()
        if (!isMac()) return emptyMap()
        val separator = pathSeparator()
        val pathEntries = buildList {
            executableDirectory(configured)?.let(::add)
            automatic.environment()["PATH"]?.split(separator)?.forEach(::add)
            System.getenv("PATH")?.split(separator)?.forEach(::add)
        }
            .map(String::trim)
            .filter(String::isNotEmpty)
            .distinct()
        return pathEntries.takeIf(List<String>::isNotEmpty)
            ?.let { mapOf("PATH" to it.joinToString(separator)) }
            ?: emptyMap()
    }

    override fun probe(): String = configured() ?: automatic.probe()

    override fun source(): CodexCommandSource = when {
        configured() != null -> CodexCommandSource.CONFIGURED
        else -> automatic.source()
    }

    private fun configured(): String? = configuredPath()?.trim()?.takeIf(String::isNotEmpty)

    private fun isWindows(): Boolean = osName.lowercase(Locale.ROOT).contains("win")

    private fun isMac(): Boolean = osName.lowercase(Locale.ROOT).contains("mac")

    private fun pathSeparator(): String = if (isWindows()) File.pathSeparator else ":"

    private fun executableDirectory(command: String): String? = runCatching {
        Path.of(command).takeIf(Path::isAbsolute)?.parent?.toString()
    }.getOrNull()
}

private fun defaultDesktopCodexBinDirectory(): Path? = runCatching {
    val localAppData = System.getenv().entries
        .firstOrNull { (key, _) -> key.equals("LOCALAPPDATA", ignoreCase = true) }
        ?.value
        ?.trim()
        ?.takeIf(String::isNotEmpty)
        ?.let(Path::of)
        ?: Path.of(System.getProperty("user.home"), "AppData", "Local")
    localAppData.resolve("OpenAI/Codex/bin")
}.getOrNull()

private fun normalizedPath(path: Path): String = runCatching { path.toRealPath() }
    .getOrDefault(path.toAbsolutePath().normalize())
    .toString()

private fun loadCodexMacLoginShellPath(): String? = runCatching {
    val result = ProcessCommandRunner().run(
        command = listOf("/bin/zsh", "-lc", "printf '%s\\n' \"\$PATH\""),
        timeout = Duration.ofSeconds(3),
    )
    if (!result.succeeded) return@runCatching null
    result.stdout.lineSequence().map(String::trim).filter(String::isNotEmpty).lastOrNull()
}.getOrNull()
