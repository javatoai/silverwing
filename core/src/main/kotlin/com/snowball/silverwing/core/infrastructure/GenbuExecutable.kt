package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.FileSystems
import java.nio.file.Path
import java.time.Duration
import java.util.Locale

enum class GenbuCommandSource { CONFIGURED, PROBED, PATH_FALLBACK }

fun interface GenbuExecutable {
    fun resolve(): String
    fun current(): String = resolve()
    fun probe(): String = resolve()

    /** Fresh bundled/PATH scan that ignores any configured path; null when nothing is found. */
    fun detect(): String? = null

    fun source(): GenbuCommandSource = GenbuCommandSource.PATH_FALLBACK

    /** Best-effort local --version check for the currently resolved command. */
    fun version(
        runner: CommandRunner = ProcessCommandRunner(),
        timeout: Duration = Duration.ofSeconds(10),
    ): CommandVersionStatus = CommandVersionProbe.probe(current(), runner, timeout)

    companion object {
        fun pathFallback(isWindows: Boolean = defaultGenbuIsWindows()): GenbuExecutable =
            GenbuExecutable { if (isWindows) "genbu.exe" else "genbu" }
    }
}

fun normalizeGenbuExecutablePath(raw: String): String? {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) return null
    val path = Path.of(trimmed)
    require(path.isAbsolute) { "Genbu 命令路径必须是绝对路径" }
    require(Files.isRegularFile(path)) { "Genbu 命令路径不存在或不是文件：$trimmed" }
    require(Files.isExecutable(path)) { "Genbu 命令不可执行：$trimmed" }
    return path.toAbsolutePath().normalize().toString()
}

class ConfiguredGenbuExecutable(
    private val configuredPath: () -> String?,
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val osName: String = System.getProperty("os.name"),
    private val probeTimeout: Duration = Duration.ofSeconds(5),
    private val bundledDirectories: () -> List<Path> = ::defaultGenbuBundledDirectories,
) : GenbuExecutable {
    @Volatile private var probedPath: String? = null
    @Volatile private var probeAttempted = false

    override fun resolve(): String = synchronized(this) {
        configured()?.let { return it }
        if (!probeAttempted) probeLocked()
        return probedPath ?: fallback()
    }

    override fun current(): String = configured() ?: probedPath ?: fallback()

    override fun probe(): String = synchronized(this) {
        configured()?.let {
            probedPath = null
            probeAttempted = false
            return it
        }
        probeAttempted = false
        probeLocked()
        return probedPath ?: fallback()
    }

    /** Re-detection must see a moved installation even while an auto-saved path still resolves. */
    override fun detect(): String? = synchronized(this) {
        probeAttempted = false
        probeLocked()
        return probedPath
    }

    override fun source(): GenbuCommandSource = when {
        configured() != null -> GenbuCommandSource.CONFIGURED
        probedPath != null -> GenbuCommandSource.PROBED
        else -> GenbuCommandSource.PATH_FALLBACK
    }

    private fun probeLocked() {
        val executableName = if (isWindows()) "genbu.exe" else "genbu"
        val bundled = bundledDirectories()
            .asSequence()
            .map { it.resolve(executableName).toAbsolutePath().normalize() }
            .firstOrNull { Files.isRegularFile(it) && Files.isExecutable(it) }
            ?.toString()
        val fromPath = if (bundled == null) runCatching {
            val command = when {
                isWindows() -> listOf("where.exe", executableName)
                // macOS installs (e.g. Homebrew) often register genbu only in the zsh login environment.
                isMac() -> listOf("/bin/zsh", "-lc", "command -v genbu")
                else -> listOf("/bin/bash", "-lc", "command -v genbu")
            }
            val result = runner.run(command, timeout = probeTimeout)
            result.takeIf(CommandResult::succeeded)?.stdout?.lineSequence()?.map(String::trim)?.firstOrNull { line ->
                runCatching {
                    val path = Path.of(line)
                    path.isAbsolute && Files.isRegularFile(path) && Files.isExecutable(path)
                }.getOrDefault(false)
            }
        }.getOrNull() else null
        probedPath = bundled ?: fromPath
        probeAttempted = true
    }

    private fun configured(): String? = configuredPath()?.trim()?.takeIf(String::isNotEmpty)?.let { raw ->
        runCatching { normalizeGenbuExecutablePath(raw) }.getOrNull()
    }
    private fun fallback(): String = if (isWindows()) "genbu.exe" else "genbu"
    private fun isWindows(): Boolean = osName.lowercase(Locale.ROOT).contains("win")
    private fun isMac(): Boolean = osName.lowercase(Locale.ROOT).contains("mac")
}

private fun defaultGenbuIsWindows(): Boolean =
    System.getProperty("os.name").lowercase(Locale.ROOT).contains("win")

private fun defaultGenbuBundledDirectories(): List<Path> = buildList {
    System.getProperty("jpackage.app-path")?.let { value -> runCatching { Path.of(value).parent }.getOrNull()?.let(::add) }
    System.getProperty("user.dir")?.let { value -> runCatching { Path.of(value) }.getOrNull()?.let(::add) }
    System.getProperty("user.home")?.let { value -> runCatching { Path.of(value).resolve("Downloads") }.getOrNull()?.let(::add) }
    // Windows engineering machines often keep portable CLIs on another drive,
    // for example T:\Downloads\genbu.exe. Checking one conventional location
    // per root is bounded and avoids a recursive disk scan.
    FileSystems.getDefault().rootDirectories.forEach { root -> add(root.resolve("Downloads")) }
}.distinct()
