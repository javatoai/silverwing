package com.snowball.silverwing.core

import java.io.File
import java.nio.file.Path
import java.time.Duration
import java.util.Locale

enum class LarkCommandSource { CONFIGURED, PROBED, PATH_FALLBACK }

/** Resolves the Lark CLI command used by the desktop settings page. */
fun interface LarkExecutable {
    fun resolve(): String

    fun current(): String = resolve()

    fun environment(): Map<String, String> = emptyMap()

    fun probe(): String = resolve()

    fun source(): LarkCommandSource = LarkCommandSource.PATH_FALLBACK

    companion object {
        fun pathFallback(isWindows: Boolean = defaultLarkIsWindows()): LarkExecutable =
            LarkExecutable { larkFallbackCommand(isWindows) }
    }
}

fun larkFallbackCommand(isWindows: Boolean): String = if (isWindows) "lark-cli.cmd" else "lark-cli"

private data class LarkProbeDefinition(
    val command: List<String>,
    val displayCommand: String,
)

private fun larkProbeDefinition(osName: String): LarkProbeDefinition {
    val os = osName.lowercase(Locale.ROOT)
    return when {
        os.contains("win") -> LarkProbeDefinition(
            command = listOf("where.exe", "lark-cli.cmd"),
            displayCommand = "where.exe lark-cli.cmd",
        )
        os.contains("mac") -> LarkProbeDefinition(
            command = listOf("/bin/zsh", "-lc", "command -v lark-cli"),
            displayCommand = "/bin/zsh -lc 'command -v lark-cli'",
        )
        else -> LarkProbeDefinition(
            command = listOf("/bin/bash", "-lc", "command -v lark-cli"),
            displayCommand = "/bin/bash -lc 'command -v lark-cli'",
        )
    }
}

fun larkProbeCommand(osName: String): List<String> = larkProbeDefinition(osName).command

fun larkProbeCommandDisplay(osName: String): String = larkProbeDefinition(osName).displayCommand

fun parseLarkProbeOutput(output: String, osName: String): String? {
    val isWindows = osName.lowercase(Locale.ROOT).contains("win")
    val windowsAbsolute = Regex("""^[A-Za-z]:[\\/].+""")
    return output.lineSequence()
        .map { it.trim() }
        .firstOrNull { line -> if (isWindows) windowsAbsolute.matches(line) else line.startsWith("/") }
}

private fun defaultLarkIsWindows(): Boolean =
    System.getProperty("os.name").lowercase(Locale.ROOT).contains("win")

/** Normalizes a user-entered executable path; null means auto-detect. */
fun normalizeLarkExecutablePath(raw: String): String? {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) return null
    val path = Path.of(trimmed)
    require(path.isAbsolute) { "Lark CLI 命令路径必须是绝对路径" }
    require(java.nio.file.Files.exists(path)) { "Lark CLI 命令路径不存在：$trimmed" }
    require(java.nio.file.Files.isRegularFile(path)) { "Lark CLI 命令路径必须是文件：$trimmed" }
    require(java.nio.file.Files.isExecutable(path)) { "Lark CLI 命令不可执行：$trimmed" }
    return trimmed
}

class ConfiguredLarkExecutable(
    private val configuredPath: () -> String?,
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val osName: String = System.getProperty("os.name"),
    private val probeTimeout: Duration = Duration.ofSeconds(5),
    private val loginShellPathProvider: () -> String? = ::loadLarkMacLoginShellPath,
) : LarkExecutable {
    @Volatile private var probedPath: String? = null
    @Volatile private var probeAttempted = false
    @Volatile private var loginShellPath: Lazy<String?> = lazy(loginShellPathProvider)

    override fun resolve(): String = synchronized(this) {
        configured()?.let { return it }
        if (!probeAttempted) probeLocked()
        return probedPath ?: larkFallbackCommand(isWindows())
    }

    override fun current(): String = configured() ?: probedPath ?: larkFallbackCommand(isWindows())

    override fun environment(): Map<String, String> {
        val merged = linkedMapOf(
            "LARKSUITE_CLI_NO_UPDATE_NOTIFIER" to "1",
            "LARKSUITE_CLI_NO_SKILLS_NOTIFIER" to "1",
        )
        if (isMac()) {
            val separator = File.pathSeparator
            val entries = buildList {
                executableDirectory(current())?.let(::add)
                loginShellPath.value?.split(separator)?.forEach(::add)
                System.getenv("PATH")?.split(separator)?.forEach(::add)
            }.map(String::trim).filter(String::isNotEmpty).distinct()
            if (entries.isNotEmpty()) merged["PATH"] = entries.joinToString(separator)
        }
        return merged
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
        return probedPath ?: larkFallbackCommand(isWindows())
    }

    override fun source(): LarkCommandSource = when {
        configured() != null -> LarkCommandSource.CONFIGURED
        probedPath != null -> LarkCommandSource.PROBED
        else -> LarkCommandSource.PATH_FALLBACK
    }

    private fun probeLocked() {
        val found = runCatching {
            val result = runner.run(larkProbeCommand(osName), timeout = probeTimeout)
            if (result.succeeded) parseLarkProbeOutput(result.stdout, osName) else null
        }.getOrNull()
        probedPath = found
        probeAttempted = true
    }

    private fun configured(): String? = configuredPath()?.trim()?.takeIf(String::isNotEmpty)
    private fun isWindows(): Boolean = osName.lowercase(Locale.ROOT).contains("win")
    private fun isMac(): Boolean = osName.lowercase(Locale.ROOT).contains("mac")
    private fun executableDirectory(command: String): String? = runCatching {
        Path.of(command).takeIf(Path::isAbsolute)?.parent?.toString()
    }.getOrNull()
}

private fun loadLarkMacLoginShellPath(): String? = runCatching {
    val result = ProcessCommandRunner().run(
        command = listOf("/bin/zsh", "-lc", "printf '%s\\n' \"\$PATH\""),
        timeout = Duration.ofSeconds(3),
    )
    if (!result.succeeded) return@runCatching null
    result.stdout.lineSequence().map(String::trim).filter(String::isNotEmpty).lastOrNull()
}.getOrNull()
