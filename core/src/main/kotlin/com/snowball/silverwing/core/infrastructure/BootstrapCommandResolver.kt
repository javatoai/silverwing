package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.util.Locale

/**
 * Resolves a Bootstrap executable to a file that Windows can start directly.
 *
 * PowerShell and cmd.exe apply PATHEXT when a user types a command such as
 * `codegraph`, but ProcessBuilder does not reliably apply that lookup to npm's
 * `.cmd` shims.  Resolving the shim from PATH keeps existing Bootstrap JSON
 * (which stores the portable `codegraph` name) working without shell parsing.
 */
internal class BootstrapCommandResolver(
    private val osName: String = System.getProperty("os.name"),
    private val environment: Map<String, String> = System.getenv(),
) {
    fun resolve(executable: String): String {
        val command = executable.trim()
        if (!isWindows() || command.isEmpty() || command.contains('/') || command.contains('\\')) {
            return command
        }

        val fileName = Path.of(command).fileName.toString()
        val candidates = if (fileName.substringAfterLast('.', "").isNotEmpty()) {
            listOf(fileName)
        } else {
            windowsExecutableExtensions().map { extension -> "$fileName$extension" }
        }
        val pathEntries = environmentValue("PATH")
            .orEmpty()
            .split(';')
            .map(String::trim)
            .filter(String::isNotEmpty)

        return pathEntries.asSequence()
            .flatMap { directory -> candidates.asSequence().map { candidate -> Path.of(directory).resolve(candidate) } }
            .map { it.toAbsolutePath().normalize() }
            .firstOrNull(Files::isRegularFile)
            ?.let { path -> runCatching { path.toRealPath() }.getOrDefault(path).toString() }
            ?: command
    }

    private fun windowsExecutableExtensions(): List<String> =
        environmentValue("PATHEXT")
            .orEmpty()
            .split(';')
            .map(String::trim)
            .filter(String::isNotEmpty)
            .map { extension -> if (extension.startsWith('.')) extension else ".${extension}" }
            .ifEmpty { listOf(".COM", ".EXE", ".BAT", ".CMD") }

    private fun environmentValue(name: String): String? =
        environment.entries.firstOrNull { (key, _) -> key.equals(name, ignoreCase = true) }?.value

    private fun isWindows(): Boolean = osName.lowercase(Locale.ROOT).contains("win")
}
