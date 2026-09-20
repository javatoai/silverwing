package com.snowball.silverwing.core

import java.nio.file.FileVisitResult
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.StandardCopyOption
import java.nio.file.attribute.BasicFileAttributes
import java.time.Duration
import kotlin.io.path.createDirectories
import kotlin.io.path.exists
import kotlin.io.path.isDirectory
import kotlin.io.path.isSymbolicLink

data class BootstrapStepResult(
    val name: String,
    val succeeded: Boolean,
    val message: String,
)

data class BootstrapResult(
    val steps: List<BootstrapStepResult>,
    val warnings: List<String>,
) {
    val succeeded: Boolean get() = steps.all { it.succeeded }
}

fun BootstrapConfig.validated(): BootstrapConfig = apply {
    copyRules.forEach { rule ->
        require(rule.source.isNotBlank() && rule.target.isNotBlank()) { "Bootstrap 复制源和目标不能为空" }
        listOf(rule.source to "复制源", rule.target to "复制目标").forEach { (value, label) ->
            val path = Path.of(value)
            require(!path.isAbsolute) { "$label 必须使用仓库内相对路径：$value" }
            require(path.none { it.toString() == ".." }) { "$label 不能包含 ..：$value" }
            require(path.none { it.toString().equals(".git", ignoreCase = true) }) { "$label 不能访问 .git：$value" }
        }
    }
    commands.forEach { command ->
        require(command.name.isNotBlank()) { "Bootstrap 命令名称不能为空" }
        require(command.executable.isNotBlank()) { "Bootstrap 可执行程序不能为空" }
        require(command.timeoutSeconds > 0) { "Bootstrap 命令超时必须大于 0" }
        require(command.workingDirectory.isNotBlank()) { "Bootstrap 命令工作目录不能为空" }
    }
}

class BootstrapService(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val git: GitClient = GitClient(runner),
    osName: String = System.getProperty("os.name"),
    environment: Map<String, String> = System.getenv(),
) {
    private val commandResolver = BootstrapCommandResolver(osName, environment)

    fun initialize(
        sourceRepository: Path,
        worktree: Path,
        config: BootstrapConfig,
    ): BootstrapResult {
        config.validated()
        val steps = mutableListOf<BootstrapStepResult>()
        val warnings = mutableListOf<String>()

        config.commands
            .filter { it.enabled }
            .forEach { command ->
                require(command.executable.isNotBlank()) { "command executable must not be blank" }
                require(command.timeoutSeconds > 0) { "command timeoutSeconds must be greater than zero" }
            }

        config.copyRules.forEach { rule ->
            val stepName = "复制 ${rule.source} → ${rule.target}"
            runCatching {
                copyRule(sourceRepository, worktree, rule, warnings)
            }.onSuccess {
                steps += BootstrapStepResult(stepName, true, "复制完成")
            }.onFailure {
                steps += BootstrapStepResult(stepName, false, it.message ?: "复制失败")
                warnings += "$stepName：${it.message}"
            }
        }

        config.commands
            .filter { it.enabled }
            .forEach { command ->
                val stepName = "执行 ${command.name}"
                runCatching {
                    val workingDirectory = resolveSafe(worktree, command.workingDirectory, "命令工作目录")
                    require(workingDirectory.exists() && workingDirectory.isDirectory()) {
                        "命令工作目录不存在：$workingDirectory"
                    }
                    runner.run(
                        command = listOf(commandResolver.resolve(command.executable)) + command.arguments,
                        workingDirectory = workingDirectory,
                        timeout = Duration.ofSeconds(command.timeoutSeconds),
                    )
                }.onSuccess { result ->
                    if (result.succeeded) {
                        steps += BootstrapStepResult(stepName, true, result.stdout.trim().takeLast(2_000))
                    } else {
                        val detail = result.stderr.ifBlank { result.stdout }.trim().takeLast(2_000)
                        steps += BootstrapStepResult(stepName, false, detail)
                        warnings += "$stepName 失败（退出码 ${result.exitCode}）"
                    }
                }.onFailure {
                    steps += BootstrapStepResult(stepName, false, it.message ?: "执行失败")
                    warnings += "$stepName：${it.message}"
                }
            }

        return BootstrapResult(steps, warnings)
    }

    private fun copyRule(
        sourceRepository: Path,
        worktree: Path,
        rule: BootstrapCopyRule,
        warnings: MutableList<String>,
    ) {
        val sourceRoot = safeRoot(sourceRepository, "复制源根目录")
        val targetRoot = safeRoot(worktree, "复制目标根目录")
        val source = resolveSafe(sourceRoot, rule.source, "复制源")
        val target = resolveSafe(targetRoot, rule.target, "复制目标")
        require(source.exists()) { "复制源不存在：$source" }
        require(source.fileName.toString() != ".git" && !source.startsWith(sourceRoot.path.resolve(".git"))) {
            "禁止复制 .git 内容"
        }
        if (target.exists() && !rule.overwrite) {
            throw IllegalStateException("目标已存在且规则禁止覆盖：$target")
        }
        if (target.exists() && isTracked(worktree, worktree.relativize(target))) {
            warnings += "初始化覆盖了 Git 已跟踪路径：${worktree.relativize(target)}"
        }

        if (source.isDirectory()) {
            copyDirectory(source, target, rule.overwrite, sourceRoot, targetRoot)
        } else {
            copyFileAtomically(source, target, rule.overwrite, sourceRoot, targetRoot)
        }
    }

    private fun copyDirectory(
        source: Path,
        target: Path,
        overwrite: Boolean,
        sourceRoot: SafeRoot,
        targetRoot: SafeRoot,
    ) {
        Files.walkFileTree(source, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(
                directory: Path,
                attributes: BasicFileAttributes,
            ): FileVisitResult {
                val destination = target.resolve(source.relativize(directory))
                validatePath(sourceRoot, directory, "复制源")
                validatePath(targetRoot, destination, "复制目标")
                destination.createDirectories()
                validatePath(targetRoot, destination, "复制目标")
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attributes: BasicFileAttributes): FileVisitResult {
                copyFileAtomically(
                    file,
                    target.resolve(source.relativize(file)),
                    overwrite,
                    sourceRoot,
                    targetRoot,
                )
                return FileVisitResult.CONTINUE
            }
        })
    }

    private fun copyFileAtomically(
        source: Path,
        target: Path,
        overwrite: Boolean,
        sourceRoot: SafeRoot,
        targetRoot: SafeRoot,
    ) {
        validatePath(sourceRoot, source, "复制源")
        validatePath(targetRoot, target, "复制目标")
        validatePath(targetRoot, target.parent, "复制目标")
        target.parent.createDirectories()
        validatePath(targetRoot, target.parent, "复制目标")
        validatePath(targetRoot, target, "复制目标")
        if (target.exists() && !overwrite) {
            throw IllegalStateException("目标已存在且规则禁止覆盖：$target")
        }
        val temporary = Files.createTempFile(target.parent, ".${target.fileName}.silverwing-", ".tmp")
        try {
            Files.copy(source, temporary, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.COPY_ATTRIBUTES)
            try {
                Files.move(
                    temporary,
                    target,
                    StandardCopyOption.REPLACE_EXISTING,
                    StandardCopyOption.ATOMIC_MOVE,
                )
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun isTracked(worktree: Path, relative: Path): Boolean {
        if (relative.toString().isBlank()) return false
        return git.run(
            worktree,
            "ls-files",
            "--error-unmatch",
            "--",
            relative.toString(),
            check = false,
        ).succeeded
    }

    private data class SafeRoot(
        val path: Path,
        val realPath: Path,
    )

    private fun safeRoot(root: Path, label: String): SafeRoot {
        val normalized = root.toAbsolutePath().normalize()
        val realPath = normalized.toRealPath()
        require(Files.isDirectory(realPath)) { "$label 必须是目录：$root" }
        return SafeRoot(normalized, realPath)
    }

    private fun resolveSafe(root: Path, relativeValue: String, label: String): Path =
        resolveSafe(safeRoot(root, label), relativeValue, label)

    private fun resolveSafe(root: SafeRoot, relativeValue: String, label: String): Path {
        val relative = Path.of(relativeValue)
        require(!relative.isAbsolute) { "$label 必须是相对路径：$relativeValue" }
        require(relative.none { it.toString() == ".." }) { "$label 禁止包含 ..：$relativeValue" }
        val resolved = root.path.resolve(relative).normalize()
        require(resolved.startsWith(root.path)) { "$label 超出仓库范围：$relativeValue" }
        require(resolved.none { it.toString() == ".git" }) { "$label 禁止访问 .git：$relativeValue" }
        val relativeResolved = root.path.relativize(resolved)
        require(relativeResolved.none { it.toString().equals(".git", ignoreCase = true) }) {
            "$label must not access .git: $relativeValue"
        }
        validatePath(root, resolved, label)
        return resolved
    }

    /**
     * Checks every existing component without following links. The root itself
     * is intentionally allowed to be a symlink (for example macOS /var), but
     * no child may be a symlink or another reparse point.
     */
    private fun validatePath(root: SafeRoot, path: Path, label: String) {
        require(path.startsWith(root.path)) { "$label 超出仓库范围：$path" }
        var current = root.path
        val relative = root.path.relativize(path)
        require(relative.none { it.toString().equals(".git", ignoreCase = true) }) {
            "$label 禁止访问 .git：$path"
        }
        for (component in relative) {
            current = current.resolve(component)
            if (!Files.exists(current, LinkOption.NOFOLLOW_LINKS)) {
                break
            }
            require(!isReparsePoint(current)) {
                "$label 不支持符号链接或 Windows 重解析点：$current"
            }
            val real = current.toRealPath()
            require(real.startsWith(root.realPath)) {
                "$label 超出仓库范围：$current"
            }
        }
    }

    private fun isReparsePoint(path: Path): Boolean =
        Files.isSymbolicLink(path) ||
            Files.readAttributes(
                path,
                BasicFileAttributes::class.java,
                LinkOption.NOFOLLOW_LINKS,
            ).isOther

}
