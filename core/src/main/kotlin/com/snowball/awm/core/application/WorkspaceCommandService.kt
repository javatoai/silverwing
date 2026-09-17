package com.snowball.awm.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.Locale

/** Executes one service-owned shortcut inside a task workspace. */
class WorkspaceCommandService(
    private val runner: StreamingCommandRunner = ProcessCommandRunner(),
    osName: String = System.getProperty("os.name"),
    environment: Map<String, String> = System.getenv(),
) {
    private val commandResolver = BootstrapCommandResolver(osName, environment)
    private val windows = osName.lowercase(Locale.ROOT).contains("win")

    fun execute(
        workspace: Path,
        command: WorkspaceCommandConfig,
        onOutput: (CommandOutputLine) -> Unit = {},
    ): CommandResult {
        command.validateWorkspaceCommand()
        require(command.enabled) { "快捷命令已禁用" }
        val root = workspace.toAbsolutePath().normalize()
        require(Files.isDirectory(root)) { "工作区目录不存在：$root" }
        val workingDirectory = resolveWorkingDirectory(root, command.workingDirectory)
        val executable = resolveExecutable(root, command.executable)
        return runner.runStreaming(
            command = listOf(executable) + command.arguments,
            workingDirectory = workingDirectory,
            timeout = Duration.ofSeconds(command.timeoutSeconds),
            onOutput = onOutput,
        )
    }

    private fun resolveWorkingDirectory(root: Path, rawValue: String): Path {
        val relative = Path.of(rawValue.trim().ifBlank { "." })
        require(!relative.isAbsolute) { "快捷命令工作目录必须是工作区内相对路径：$rawValue" }
        require(relative.none { it.toString() == ".." }) { "快捷命令工作目录不能包含 ..：$rawValue" }
        require(relative.none { it.toString().equals(".git", ignoreCase = true) }) {
            "快捷命令工作目录不能访问 .git：$rawValue"
        }
        val resolved = root.resolve(relative).normalize()
        require(resolved.startsWith(root)) { "快捷命令工作目录超出工作区：$rawValue" }
        require(Files.isDirectory(resolved)) { "快捷命令工作目录不存在：$resolved" }
        val rootReal = root.toRealPath()
        require(resolved.toRealPath().startsWith(rootReal)) { "快捷命令工作目录超出工作区：$rawValue" }
        return resolved
    }

    private fun resolveExecutable(root: Path, rawValue: String): String {
        val executable = rawValue.trim()
        val path = Path.of(executable)
        if (path.isAbsolute()) return path.normalize().toString()
        if (executable.contains('/') || executable.contains('\\')) {
            val resolved = root.resolve(path).normalize()
            require(resolved.startsWith(root)) { "快捷命令可执行程序超出工作区：$rawValue" }
            require(!resolved.any { it.toString().equals(".git", ignoreCase = true) }) {
                "快捷命令可执行程序不能访问 .git：$rawValue"
            }
            return resolved.toString()
        }
        val resolvedFromPath = commandResolver.resolve(executable)
        if (resolvedFromPath != executable) return resolvedFromPath
        val candidates = if (windows && Path.of(executable).fileName.toString().substringAfterLast('.', "").isEmpty()) {
            listOf(executable, "$executable.cmd", "$executable.bat", "$executable.exe")
        } else {
            listOf(executable)
        }
        val workspaceExecutable = candidates
            .asSequence()
            .map { root.resolve(it).normalize() }
            .firstOrNull { candidate -> Files.isRegularFile(candidate) }
        return workspaceExecutable?.toString() ?: executable
    }
}

fun WorkspaceCommandConfig.validateWorkspaceCommand(): WorkspaceCommandConfig = apply {
    require(workingDirectory.isNotBlank()) { "快捷命令工作目录不能为空" }
    val relative = Path.of(workingDirectory.trim())
    require(!relative.isAbsolute) { "快捷命令工作目录必须是相对路径：$workingDirectory" }
    require(relative.none { it.toString() == ".." }) { "快捷命令工作目录不能包含 ..：$workingDirectory" }
    require(relative.none { it.toString().equals(".git", ignoreCase = true) }) {
        "快捷命令工作目录不能访问 .git：$workingDirectory"
    }
}
