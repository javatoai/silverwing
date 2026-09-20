package com.snowball.silverwing.core

import java.awt.Desktop
import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import java.net.URI
import java.nio.file.Files
import java.nio.file.Path
import java.nio.charset.StandardCharsets
import java.util.Base64

class DesktopIntegration(
    private val runner: CommandRunner = ProcessCommandRunner(),
) {
    fun copyPath(path: Path) {
        copyText(path.toString())
    }

    fun copyText(value: String) {
        Toolkit.getDefaultToolkit().systemClipboard.setContents(StringSelection(value), null)
    }

    /** Copies a shell-safe representation of an arbitrary command. */
    fun copyCommand(command: List<String>, osName: String = System.getProperty("os.name")) {
        require(command.isNotEmpty()) { "命令不能为空" }
        copyText(TerminalLaunchCommand.display(command, osName))
    }

    /** Copies the currently resolved CLI executable exactly as it is displayed. */
    fun copyCommand(command: String) {
        require(command.isNotBlank()) { "命令不能为空" }
        copyText(command.trim())
    }

    fun openUrl(url: String) {
        require(isHttpUrl(url)) { "不是有效的 http(s) 链接：$url" }
        val uri = URI(url.trim())
        val os = System.getProperty("os.name")
        when {
            Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.BROWSE) ->
                Desktop.getDesktop().browse(uri)
            os.startsWith("Windows", ignoreCase = true) ->
                runner.run(listOf("rundll32", "url.dll,FileProtocolHandler", uri.toString()))
            os.startsWith("Mac", ignoreCase = true) ->
                runner.run(listOf("open", uri.toString()))
            else ->
                runner.run(listOf("xdg-open", uri.toString()))
        }
    }

    fun reveal(path: Path) {
        val os = System.getProperty("os.name")
        when {
            os.startsWith("Windows", ignoreCase = true) ->
                runner.run(listOf("explorer.exe", "/select,${path.toAbsolutePath()}"))
            os.startsWith("Mac", ignoreCase = true) ->
                runner.run(listOf("open", "-R", path.toAbsolutePath().toString()))
            Desktop.isDesktopSupported() -> Desktop.getDesktop().open(path.toFile())
            else -> error("当前系统不支持打开文件管理器")
        }
    }

    fun openDirectory(path: Path) {
        require(path.toFile().isDirectory) { "目录不存在：$path" }
        val os = System.getProperty("os.name")
        when {
            Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.OPEN) ->
                Desktop.getDesktop().open(path.toFile())
            os.startsWith("Windows", ignoreCase = true) ->
                runner.run(listOf("explorer.exe", path.toAbsolutePath().toString()))
            os.startsWith("Mac", ignoreCase = true) ->
                runner.run(listOf("open", path.toAbsolutePath().toString()))
            else -> runner.run(listOf("xdg-open", path.toAbsolutePath().toString()))
        }
    }

    fun openTerminal(path: Path, configuredExecutable: String? = null) {
        val osName = System.getProperty("os.name")
        val resolution = terminalResolution(configuredExecutable)
        val command = TerminalLaunchCommand.build(
            configuredExecutable = configuredExecutable,
            target = path,
            osName = osName,
            windowsTerminalAvailable = resolution.command == "wt.exe",
        )
        launchDetached(command)
    }

    fun terminalResolution(configuredExecutable: String? = null): TerminalCommandResolution {
        val osName = System.getProperty("os.name")
        val resolution = TerminalLaunchCommand.resolve(
            configuredExecutable = configuredExecutable,
            osName = osName,
            windowsTerminalAvailable = configuredExecutable.isNullOrBlank() &&
                osName.startsWith("Windows", ignoreCase = true) && commandExists("wt.exe"),
        )
        val configured = configuredExecutable?.trim()?.takeIf(String::isNotBlank) ?: return resolution
        val available = runCatching {
            val path = Path.of(configured)
            if (path.isAbsolute) Files.exists(path) else commandExists(configured)
        }.getOrDefault(false)
        return resolution.copy(available = available)
    }

    /**
     * Opens the system terminal, executes the supplied command and leaves the
     * terminal window open so the user can inspect its output.
     */
    fun openSystemTerminalCommand(command: List<String>) {
        require(command.isNotEmpty()) { "命令不能为空" }
        launchSystemTerminal(command)
    }

    /**
     * Opens a system terminal in the CLI executable's directory and runs the
     * executable without adding arguments. Bare PATH commands are left bare
     * and run from the terminal's default working directory.
     */
    fun openCliInSystemTerminal(command: String) {
        val normalizedCommand = command.trim()
        require(normalizedCommand.isNotEmpty()) { "命令不能为空" }
        val workingDirectory = TerminalLaunchCommand.parentDirectoryOfCommand(
            normalizedCommand,
            System.getProperty("os.name"),
        )
        launchSystemTerminal(listOf(normalizedCommand), workingDirectory)
    }

    fun openDevelopmentTool(path: Path, type: DevelopmentToolType, configuredPath: String) {
        require(Files.isDirectory(path)) { "项目目录不存在：$path" }
        val application = Path.of(configuredPath).toAbsolutePath().normalize()
        require(Files.exists(application)) { "开发工具路径不存在：$application" }
        launchDetached(DevelopmentToolLaunchCommand.build(type, application.toString(), path))
    }

    private fun commandExists(command: String): Boolean {
        val result = if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) {
            runner.run(listOf("where.exe", command))
        } else {
            runner.run(listOf("which", command))
        }
        return result.succeeded
    }

    private fun launchSystemTerminal(command: List<String>, workingDirectory: Path? = null) {
        val osName = System.getProperty("os.name")
        val windowsTerminalAvailable = osName.startsWith("Windows", ignoreCase = true) && commandExists("wt.exe")
        launchDetached(
            TerminalLaunchCommand.buildSystemCommand(
                command = command,
                osName = osName,
                workingDirectory = workingDirectory,
                windowsTerminalAvailable = windowsTerminalAvailable,
            ),
        )
    }

    private fun launchDetached(command: List<String>) {
        ProcessBuilder(command)
            .redirectOutput(ProcessBuilder.Redirect.DISCARD)
            .redirectError(ProcessBuilder.Redirect.DISCARD)
            .start()
    }
}

enum class TerminalCommandSource { CONFIGURED, SYSTEM_DEFAULT }

data class TerminalCommandResolution(
    val displayName: String,
    val command: String,
    val source: TerminalCommandSource,
    val available: Boolean = true,
)

/** Builds a terminal command without treating a macOS .app bundle as an executable file. */
object TerminalLaunchCommand {
    fun resolve(
        configuredExecutable: String?,
        osName: String = System.getProperty("os.name"),
        windowsTerminalAvailable: Boolean = false,
    ): TerminalCommandResolution {
        val configured = configuredExecutable?.trim()?.takeIf(String::isNotEmpty)
        return when {
            configured != null -> TerminalCommandResolution("自定义终端", configured, TerminalCommandSource.CONFIGURED)
            osName.startsWith("Windows", ignoreCase = true) && windowsTerminalAvailable ->
                TerminalCommandResolution("Windows Terminal", "wt.exe", TerminalCommandSource.SYSTEM_DEFAULT)
            osName.startsWith("Windows", ignoreCase = true) ->
                TerminalCommandResolution("Windows PowerShell", "powershell.exe", TerminalCommandSource.SYSTEM_DEFAULT)
            osName.startsWith("Mac", ignoreCase = true) ->
                TerminalCommandResolution("Terminal", "Terminal.app", TerminalCommandSource.SYSTEM_DEFAULT)
            else -> TerminalCommandResolution("系统终端", "x-terminal-emulator", TerminalCommandSource.SYSTEM_DEFAULT)
        }
    }

    fun build(
        configuredExecutable: String?,
        target: Path,
        osName: String = System.getProperty("os.name"),
        windowsTerminalAvailable: Boolean = false,
    ): List<String> {
        val normalizedTarget = target.toAbsolutePath().normalize().toString()
        val configured = configuredExecutable?.trim()?.takeIf(String::isNotEmpty)
        return when {
            configured != null && osName.startsWith("Mac", ignoreCase = true) && configured.endsWith(".app", ignoreCase = true) ->
                listOf("open", "-a", configured, normalizedTarget)
            configured != null && osName.startsWith("Windows", ignoreCase = true) &&
                (configured.endsWith(".cmd", ignoreCase = true) || configured.endsWith(".bat", ignoreCase = true)) ->
                WindowsBatchLaunchCommand.build(configured, listOf(normalizedTarget))
            configured != null -> listOf(configured, normalizedTarget)
            osName.startsWith("Windows", ignoreCase = true) && windowsTerminalAvailable ->
                listOf("wt.exe", "-d", normalizedTarget)
            osName.startsWith("Windows", ignoreCase = true) ->
                listOf("powershell.exe", "-NoExit", "-Command", "Set-Location -LiteralPath '${
                    normalizedTarget.replace("'", "''")
                }'")
            osName.startsWith("Mac", ignoreCase = true) -> listOf("open", "-a", "Terminal", normalizedTarget)
            else -> listOf("x-terminal-emulator", "--working-directory", normalizedTarget)
        }
    }

    /** Builds a command for the OS terminal, preserving the window afterwards. */
    fun buildSystemCommand(
        command: List<String>,
        osName: String = System.getProperty("os.name"),
        workingDirectory: Path? = null,
        windowsTerminalAvailable: Boolean = false,
    ): List<String> {
        require(command.isNotEmpty()) { "命令不能为空" }
        val normalizedWorkingDirectory = workingDirectory?.let {
            normalizeWorkingDirectory(it, osName)
        }
        return when {
            osName.startsWith("Windows", ignoreCase = true) -> {
                val script = command.joinToString(" ") { powerShellLiteral(it) }
                if (windowsTerminalAvailable) {
                    buildList {
                        addAll(listOf("wt.exe", "-w", "new", "new-tab"))
                        normalizedWorkingDirectory?.let { directory ->
                            addAll(listOf("-d", directory))
                        }
                        addAll(listOf("powershell.exe", "-NoProfile", "-NoExit", "-Command", "& $script"))
                    }
                } else {
                    val location = normalizedWorkingDirectory
                        ?.let { "Set-Location -LiteralPath ${powerShellLiteral(it)}; " }
                        .orEmpty()
                    listOf("powershell.exe", "-NoProfile", "-NoExit", "-Command", "$location& $script")
                }
            }
            osName.startsWith("Mac", ignoreCase = true) -> {
                val shellCommand = command.joinToString(" ") { posixShellLiteral(it) }
                val locatedCommand = normalizedWorkingDirectory
                    ?.let { "cd ${posixShellLiteral(it)} && $shellCommand" }
                    ?: shellCommand
                val appleScript = "tell application \"Terminal\" to do script \"${appleScriptLiteral(locatedCommand)}\""
                listOf("osascript", "-e", appleScript)
            }
            else -> {
                val shellCommand = command.joinToString(" ") { posixShellLiteral(it) }
                val locatedCommand = normalizedWorkingDirectory
                    ?.let { "cd ${posixShellLiteral(it)} && $shellCommand" }
                    ?: shellCommand
                val keepOpen = "$locatedCommand; printf '\\nPress Enter to close...'; read -r"
                listOf("x-terminal-emulator", "-e", "/bin/sh", "-c", keepOpen)
            }
        }
    }

    /**
     * String overload used by the CLI settings actions. The string is one
     * executable, not a shell fragment, so it is always quoted as one value.
     */
    fun buildSystemCommand(
        command: String,
        workingDirectory: Path? = null,
        osName: String = System.getProperty("os.name"),
        windowsTerminalAvailable: Boolean = false,
    ): List<String> {
        require(command.isNotBlank()) { "命令不能为空" }
        return buildSystemCommand(listOf(command.trim()), osName, workingDirectory, windowsTerminalAvailable)
    }

    /** Finds the parent directory of an absolute executable path on any host. */
    internal fun parentDirectoryOfCommand(command: String, osName: String): Path? {
        val normalizedCommand = command.trim()
        val localPath = runCatching { Path.of(normalizedCommand) }.getOrNull()
        if (localPath?.isAbsolute == true) return localPath.parent?.normalize()
        if (!osName.startsWith("Windows", ignoreCase = true)) return null

        // Tests and callers may construct Windows paths while running on a
        // non-Windows JVM, where java.nio.file.Path cannot recognize a drive
        // letter or UNC prefix as absolute.
        val isWindowsAbsolute = Regex("^[A-Za-z]:[\\\\/].+").matches(normalizedCommand) ||
            normalizedCommand.startsWith("\\\\")
        if (!isWindowsAbsolute) return null
        val parentEnd = maxOf(normalizedCommand.lastIndexOf('/'), normalizedCommand.lastIndexOf('\\'))
        if (parentEnd <= 0) return null
        return runCatching { Path.of(normalizedCommand.substring(0, parentEnd)).normalize() }.getOrNull()
    }

    private fun normalizeWorkingDirectory(path: Path, osName: String): String {
        val value = path.normalize().toString()
        // Keep a Windows drive/UNC path intact when tests or a caller provide
        // it on a non-Windows host. Calling toAbsolutePath() there would
        // incorrectly prefix the current POSIX working directory.
        if (osName.startsWith("Windows", ignoreCase = true) && isWindowsAbsolutePath(value)) {
            return value
        }
        if (!osName.startsWith("Windows", ignoreCase = true) && value.startsWith("/")) {
            return value
        }
        return path.toAbsolutePath().normalize().toString()
    }

    private fun isWindowsAbsolutePath(value: String): Boolean =
        Regex("^[A-Za-z]:[\\\\/].+").matches(value) || value.startsWith("\\\\")

    /** Shell-safe command text suitable for the copy-to-clipboard action. */
    fun display(
        command: List<String>,
        osName: String = System.getProperty("os.name"),
    ): String {
        require(command.isNotEmpty()) { "命令不能为空" }
        return if (osName.startsWith("Windows", ignoreCase = true)) {
            command.joinToString(" ") { displayPowerShellLiteral(it) }
        } else {
            command.joinToString(" ") { displayPosixShellLiteral(it) }
        }
    }

    private fun powerShellLiteral(value: String): String = "'${value.replace("'", "''")}'"

    private fun posixShellLiteral(value: String): String =
        if (value.isEmpty()) "''" else "'${value.replace("'", "'\\''")}'"

    private fun displayPowerShellLiteral(value: String): String =
        if (SHELL_SAFE_VALUE.matches(value)) value else powerShellLiteral(value)

    private fun displayPosixShellLiteral(value: String): String =
        if (SHELL_SAFE_VALUE.matches(value)) value else posixShellLiteral(value)

    private val SHELL_SAFE_VALUE = Regex("[A-Za-z0-9_./:@%+=-]+")

    private fun appleScriptLiteral(value: String): String = value
        .replace("\\", "\\\\")
        .replace("\"", "\\\"")
        .replace("\r", "\\r")
        .replace("\n", "\\n")
}

object DevelopmentToolLaunchCommand {
    fun build(
        type: DevelopmentToolType,
        configuredPath: String,
        target: Path,
        osName: String = System.getProperty("os.name"),
    ): List<String> {
        require(configuredPath.isNotBlank()) { "开发工具路径不能为空" }
        val normalizedTarget = target.toAbsolutePath().normalize().toString()
        val normalizedPath = configuredPath.trim()
        if (osName.startsWith("Mac", ignoreCase = true) && normalizedPath.endsWith(".app", ignoreCase = true)) {
            if (type == DevelopmentToolType.VISUAL_STUDIO_CODE) {
                return listOf("open", "-n", "-a", normalizedPath, "--args", "--new-window", normalizedTarget)
            }
            return listOf("open", "-a", normalizedPath, normalizedTarget)
        }
        val arguments = buildList {
            if (type == DevelopmentToolType.VISUAL_STUDIO_CODE) add("--new-window")
            add(normalizedTarget)
        }
        return if (osName.startsWith("Windows", ignoreCase = true) &&
            (normalizedPath.endsWith(".cmd", true) || normalizedPath.endsWith(".bat", true))
        ) {
            WindowsBatchLaunchCommand.build(normalizedPath, arguments)
        } else if (osName.startsWith("Windows", ignoreCase = true) && type in JETBRAINS_TOOL_TYPES) {
            windowsShellLaunch(normalizedPath, arguments)
        } else {
            listOf(normalizedPath) + arguments
        }
    }

    /**
     * Some JetBrains launchers installed below Program Files require elevation. Launching them
     * directly with ProcessBuilder bypasses the Windows shell and fails with CreateProcess 740.
     */
    private fun windowsShellLaunch(application: String, arguments: List<String>): List<String> {
        val script = "Start-Process -FilePath ${powerShellLiteral(application)} -ArgumentList ${powerShellLiteral(arguments.joinToString(" ", transform = ::windowsArgument))}"
        // -Command undergoes another native command-line parse before PowerShell sees it.
        // Encoding the script preserves the literal quotes needed by the child launcher.
        return listOf(
            "powershell.exe",
            "-NoProfile",
            "-EncodedCommand",
            Base64.getEncoder().encodeToString(script.toByteArray(StandardCharsets.UTF_16LE)),
        )
    }

    // Start-Process joins ArgumentList into a Windows command line; PowerShell literals alone
    // do not protect argument boundaries in that second parsing step.
    private fun windowsArgument(value: String): String = buildString {
        append('"')
        var backslashes = 0
        for (character in value) {
            if (character == '\\') {
                backslashes++
            } else {
                repeat(if (character == '"') backslashes * 2 + 1 else backslashes) { append('\\') }
                append(character)
                backslashes = 0
            }
        }
        repeat(backslashes * 2) { append('\\') }
        append('"')
    }

    private fun powerShellLiteral(value: String): String = "'${value.replace("'", "''")}'"

    private val JETBRAINS_TOOL_TYPES = setOf(
        DevelopmentToolType.INTELLIJ_IDEA,
        DevelopmentToolType.WEBSTORM,
        DevelopmentToolType.PYCHARM,
        DevelopmentToolType.ANDROID_STUDIO,
        DevelopmentToolType.DEVECO_STUDIO,
    )
}

/** Preserves literal filesystem paths through cmd's separate command-line parser. */
private object WindowsBatchLaunchCommand {
    fun build(application: String, arguments: List<String>): List<String> {
        // Quoting alone does not stop cmd from expanding %NAME% in a path.
        // Expand child-only environment variables once, inside quotes, and
        // disable delayed expansion so &, %, ! and apostrophes remain data.
        val executableVariable = "SILVERWING_BATCH_EXECUTABLE"
        val argumentVariables = arguments.indices.map { "SILVERWING_BATCH_ARGUMENT_$it" }
        val command = (listOf(executableVariable) + argumentVariables)
            .joinToString(" ") { "\"%$it%\"" }
        val script = buildString {
            appendLine("${'$'}ErrorActionPreference = 'Stop'")
            appendLine("${'$'}ProgressPreference = 'SilentlyContinue'")
            appendLine("${'$'}startInfo = New-Object System.Diagnostics.ProcessStartInfo")
            appendLine("${'$'}startInfo.FileName = 'cmd.exe'")
            appendLine("${'$'}startInfo.UseShellExecute = ${'$'}false")
            appendLine("${'$'}startInfo.CreateNoWindow = ${'$'}true")
            appendLine("${'$'}startInfo.EnvironmentVariables['$executableVariable'] = ${powerShellLiteral(application)}")
            arguments.forEachIndexed { index, argument ->
                appendLine("${'$'}startInfo.EnvironmentVariables['${argumentVariables[index]}'] = ${powerShellLiteral(argument)}")
            }
            // ProcessStartInfo accepts this exact command line without another
            // PowerShell native-argument quoting pass. /s consumes the outer pair.
            appendLine("${'$'}startInfo.Arguments = ${powerShellLiteral("/d /v:off /s /c \"$command\"")}")
            appendLine("${'$'}process = [System.Diagnostics.Process]::Start(${'$'}startInfo)")
            appendLine("${'$'}process.WaitForExit()")
            appendLine("exit ${'$'}process.ExitCode")
        }
        return listOf(
            "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand",
            Base64.getEncoder().encodeToString(script.toByteArray(StandardCharsets.UTF_16LE)),
        )
    }

    private fun powerShellLiteral(value: String): String = "'${value.replace("'", "''")}'"
}
