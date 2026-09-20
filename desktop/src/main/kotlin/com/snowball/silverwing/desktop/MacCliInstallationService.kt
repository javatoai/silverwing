package com.snowball.silverwing.desktop

import java.nio.charset.StandardCharsets
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardCopyOption.REPLACE_EXISTING
import java.util.Comparator
import java.util.Locale

/**
 * Installs the CLI bundled with the macOS application into the current user's
 * Application Support directory. The stable `silverwing` launcher is added to the
 * active shell's startup profile, so no administrator-owned directory or
 * machine-wide Java installation is needed.
 */
internal class MacCliInstallationService(
    private val source: () -> PortableCliSource? = ::packagedSource,
    private val userHome: () -> Path = { Path.of(System.getProperty("user.home")) },
    private val shellName: () -> String? = { System.getenv("SHELL") },
    private val profilePath: (() -> Path)? = null,
    private val isMacOs: () -> Boolean = {
        System.getProperty("os.name").startsWith("Mac", ignoreCase = true)
    },
) : CliInstallationService {
    override fun inspect(): CliInstallationStatus {
        if (!isMacOs()) return unsupportedStatus()

        val command = commandDirectory().resolve(COMMAND_FILE)
        val installedVersion = readInstalledVersion()
        val managedCommand = isManagedStableCommand(command)
        val managedPayload = hasManagedPayload(managedCommand, installedVersion)
        val installed = managedCommand && installedVersion != null &&
            validInstallation(cliVersionsDirectory().resolve(installedVersion))
        val shellConfigured = hasManagedShellProfileBlock()
        val uninstallAvailable = managedCommand || managedPayload || shellConfigured
        val bundled = bundledSource()

        return when {
            installed && !shellConfigured -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = bundled != null,
                installed = true,
                uninstallAvailable = true,
                commandPath = command,
                version = installedVersion,
                message = "已安装 silverwing CLI $installedVersion，但当前 Shell 启动配置未完成；点击“更新 CLI”可修复。",
            )
            installed && bundled != null -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = true,
                installed = true,
                uninstallAvailable = true,
                commandPath = command,
                version = installedVersion,
                message = "已安装 silverwing CLI $installedVersion；请打开新的终端运行 silverwing。",
            )
            installed -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = false,
                installed = true,
                uninstallAvailable = true,
                commandPath = command,
                version = installedVersion,
                message = "已安装 silverwing CLI $installedVersion；当前应用未提供可更新的内置 CLI。",
            )
            uninstallAvailable -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = bundled != null,
                installed = false,
                uninstallAvailable = true,
                commandPath = command,
                version = installedVersion,
                message = "检测到未完成的 silverwing CLI 安装或卸载；请点击“卸载 CLI”完成清理后再安装。",
            )
            bundled != null -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = true,
                installed = false,
                commandPath = command,
                version = bundled.version,
                message = "应用内含 silverwing CLI ${bundled.version}；安装后会在新终端中提供 silverwing 命令。",
            )
            else -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = false,
                installed = false,
                commandPath = command,
                message = "当前应用未提供内置的 CLI 或运行时。",
            )
        }
    }

    override fun install(): CliInstallationStatus {
        require(isMacOs()) { "一键 CLI 安装仅支持 macOS" }
        val bundled = requireNotNull(bundledSource()) {
            "未找到完整的应用内 CLI 或运行时；开发模式不会复制本机 JDK。"
        }
        validateSource(bundled)
        validateStableCommandTarget()

        val versionDirectory = cliVersionsDirectory().resolve(bundled.version)
        if (Files.exists(versionDirectory)) {
            require(validInstallation(versionDirectory)) {
                "CLI 目标版本目录不完整：$versionDirectory；请先手工备份并删除该目录后重试"
            }
        } else {
            Files.createDirectories(cliVersionsDirectory())
            val staging = Files.createTempDirectory(cliVersionsDirectory(), ".${bundled.version}-")
            try {
                copyDirectory(bundled.cliHome, staging.resolve("cli"))
                copyDirectory(bundled.runtimeHome, staging.resolve("runtime"))
                makeLaunchersExecutable(staging)
                require(validInstallation(staging)) { "打包的 CLI 文件不完整" }
                moveDirectory(staging, versionDirectory)
            } catch (error: Throwable) {
                deleteTree(staging)
                throw error
            }
        }

        writePayloadMarker()
        Files.createDirectories(commandDirectory())
        writeStableCommand(bundled.version)
        addCommandDirectoryToShellProfile()
        return inspect()
    }

    override fun uninstall(): CliInstallationStatus {
        require(isMacOs()) { "一键 CLI 卸载仅支持 macOS" }

        val command = commandDirectory().resolve(COMMAND_FILE)
        val installedVersion = readInstalledVersion()
        val managedCommand = isManagedStableCommand(command)
        val managedPayload = hasManagedPayload(managedCommand, installedVersion)
        val shellConfigured = hasManagedShellProfileBlock()
        if (!managedCommand && !managedPayload && !shellConfigured) return inspect()

        // Keep ownership markers until every managed payload file has gone, so
        // an interrupted cleanup remains visible and retryable.
        if (managedPayload) deleteManagedPayload()
        if (managedCommand) {
            Files.deleteIfExists(command)
            Files.deleteIfExists(commandDirectory().resolve(VERSION_FILE))
        }
        if (shellConfigured) removeCommandDirectoryFromShellProfile()
        return inspect()
    }

    private fun bundledSource(): PortableCliSource? = source()?.takeIf(::isCompleteSource)

    private fun isCompleteSource(source: PortableCliSource): Boolean =
        source.version.matches(VERSION_PATTERN) &&
            Files.isRegularFile(source.cliHome.resolve("bin").resolve(PACKAGED_COMMAND_FILE)) &&
            hasCliLibraries(source.cliHome) &&
            Files.isRegularFile(source.runtimeHome.resolve("bin").resolve("java"))

    private fun validateSource(source: PortableCliSource) {
        require(source.version.matches(VERSION_PATTERN)) { "CLI 版本号不合法：${source.version}" }
        require(Files.isRegularFile(source.cliHome.resolve("bin").resolve(PACKAGED_COMMAND_FILE))) {
            "应用内资源缺少 CLI 启动脚本"
        }
        require(hasCliLibraries(source.cliHome)) { "应用内资源缺少 CLI 依赖库" }
        require(Files.isRegularFile(source.runtimeHome.resolve("bin").resolve("java"))) {
            "应用内资源缺少随附 Java 运行时"
        }
    }

    private fun validInstallation(root: Path): Boolean =
        Files.isRegularFile(root.resolve("cli/bin").resolve(PACKAGED_COMMAND_FILE)) &&
            hasCliLibraries(root.resolve("cli")) &&
            Files.isRegularFile(root.resolve("runtime/bin/java"))

    private fun hasCliLibraries(cliHome: Path): Boolean {
        val libraries = cliHome.resolve("lib")
        if (!Files.isDirectory(libraries)) return false
        return Files.list(libraries).use { entries -> entries.anyMatch(Files::isRegularFile) }
    }

    private fun makeLaunchersExecutable(root: Path) {
        makeExecutable(root.resolve("cli/bin").resolve(PACKAGED_COMMAND_FILE))
        makeExecutable(root.resolve("runtime/bin/java"))
        root.resolve("runtime/lib/jspawnhelper").takeIf(Files::isRegularFile)?.let(::makeExecutable)
    }

    private fun makeExecutable(path: Path) {
        require(Files.isRegularFile(path)) { "缺少可执行文件：$path" }
        if (!path.toFile().setExecutable(true, false) && !Files.isExecutable(path)) {
            error("无法赋予可执行权限：$path")
        }
    }

    private fun validateStableCommandTarget() {
        val command = commandDirectory().resolve(COMMAND_FILE)
        if (!Files.exists(command)) return
        require(isManagedStableCommand(command)) {
            "CLI 命令入口已被其他文件占用：$command；为避免覆盖，请先手工检查或移走它"
        }
    }

    private fun hasManagedPayload(managedCommand: Boolean, installedVersion: String?): Boolean =
        hasPayloadMarker() ||
            (managedCommand && installedVersion != null && validInstallation(cliVersionsDirectory().resolve(installedVersion)))

    private fun hasPayloadMarker(): Boolean = runCatching {
        Files.readString(cliVersionsDirectory().resolve(PAYLOAD_MARKER_FILE), StandardCharsets.UTF_8).trim() ==
            PAYLOAD_MARKER_CONTENT.trim()
    }.getOrDefault(false)

    private fun writePayloadMarker() {
        writeAtomically(cliVersionsDirectory().resolve(PAYLOAD_MARKER_FILE), PAYLOAD_MARKER_CONTENT)
    }

    private fun deleteManagedPayload() {
        val root = cliVersionsDirectory()
        val marker = root.resolve(PAYLOAD_MARKER_FILE)
        if (!Files.exists(root)) return
        Files.walk(root).use { paths ->
            paths.sorted(Comparator.reverseOrder())
                .filter { entry -> entry != root && entry != marker }
                .forEach { Files.deleteIfExists(it) }
        }
        Files.deleteIfExists(marker)
        Files.deleteIfExists(root)
    }

    private fun isManagedStableCommand(command: Path): Boolean = runCatching {
        Files.isRegularFile(command) && Files.readString(command, StandardCharsets.UTF_8)
            .lineSequence()
            .any { it.trim() == "# $STABLE_COMMAND_MARKER" }
    }.getOrDefault(false)

    private fun writeStableCommand(version: String) {
        val command = commandDirectory().resolve(COMMAND_FILE)
        writeAtomically(command, stableCommandContent(version))
        makeExecutable(command)
        writeAtomically(commandDirectory().resolve(VERSION_FILE), "$version\n")
    }

    private fun stableCommandContent(version: String): String = """
        #!/usr/bin/env sh
        # $STABLE_COMMAND_MARKER
        set -eu
        SCRIPT_DIR=${'$'}(CDPATH= cd -- "${'$'}(dirname -- "${'$'}0")" && pwd)
        exec "${'$'}SCRIPT_DIR/../cli/$version/cli/bin/$PACKAGED_COMMAND_FILE" "${'$'}@"
    """.trimIndent() + "\n"

    private fun addCommandDirectoryToShellProfile() {
        val profile = shellProfile()
        Files.createDirectories(checkNotNull(profile.parent))
        val existing = if (Files.exists(profile)) Files.readString(profile, StandardCharsets.UTF_8) else ""
        require(!(existing.contains(PROFILE_BLOCK_START) && !existing.contains(PROFILE_BLOCK_END))) {
            "检测到不完整的 silverwing CLI Shell 配置标记：$profile；请先手工修复后重试"
        }
        val retained = PROFILE_BLOCK_PATTERN.replace(existing, "").trimEnd()
        val separator = if (retained.isBlank()) "" else "\n\n"
        writeAtomically(profile, retained + separator + shellProfileBlock())
    }

    private fun removeCommandDirectoryFromShellProfile() {
        val profile = shellProfile()
        if (!Files.isRegularFile(profile)) return
        val existing = Files.readString(profile, StandardCharsets.UTF_8)
        writeAtomically(profile, PROFILE_BLOCK_PATTERN.replace(existing, "").trimEnd().let { content ->
            if (content.isBlank()) "" else "$content\n"
        })
    }

    private fun hasManagedShellProfileBlock(): Boolean = runCatching {
        val content = Files.readString(shellProfile(), StandardCharsets.UTF_8)
        content.contains(PROFILE_BLOCK_START) && content.contains(PROFILE_BLOCK_END)
    }.getOrDefault(false)

    private fun shellProfileBlock(): String = buildString {
        appendLine(PROFILE_BLOCK_START)
        append("export PATH=")
        append(shellSingleQuote(commandDirectory().toAbsolutePath().normalize().toString()))
        appendLine(":\"${'$'}PATH\"")
        appendLine(PROFILE_BLOCK_END)
        appendLine()
    }

    private fun shellSingleQuote(value: String): String = "'${value.replace("'", "'\"'\"'")}'"

    private fun shellProfile(): Path = profilePath?.invoke() ?: run {
        val shell = shellName().orEmpty().substringAfterLast('/').lowercase(Locale.ROOT)
        val name = when (shell) {
            "bash" -> ".bash_profile"
            "zsh", "" -> ".zprofile"
            else -> ".profile"
        }
        userHome().resolve(name)
    }

    private fun readInstalledVersion(): String? = runCatching {
        Files.readString(commandDirectory().resolve(VERSION_FILE), StandardCharsets.UTF_8).trim()
            .takeIf { it.matches(VERSION_PATTERN) }
    }.getOrNull()

    private fun applicationHome(): Path = userHome().resolve("Library/Application Support/silverwing")
    private fun cliVersionsDirectory(): Path = applicationHome().resolve("cli")
    private fun commandDirectory(): Path = applicationHome().resolve("bin")

    private fun moveDirectory(source: Path, target: Path) {
        try {
            Files.move(source, target, ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(source, target)
        }
    }

    private fun writeAtomically(target: Path, content: String) {
        Files.createDirectories(checkNotNull(target.parent))
        val temporary = Files.createTempFile(target.parent, ".${target.fileName}-", ".tmp")
        Files.writeString(temporary, content, StandardCharsets.UTF_8)
        try {
            Files.move(temporary, target, ATOMIC_MOVE, REPLACE_EXISTING)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, REPLACE_EXISTING)
        }
    }

    private fun copyDirectory(source: Path, target: Path) {
        Files.walk(source).use { paths ->
            paths.forEach { entry ->
                val destination = target.resolve(source.relativize(entry).toString())
                if (Files.isDirectory(entry)) {
                    Files.createDirectories(destination)
                } else {
                    Files.createDirectories(checkNotNull(destination.parent))
                    Files.copy(entry, destination, REPLACE_EXISTING)
                }
            }
        }
    }

    private fun deleteTree(path: Path) {
        if (!Files.exists(path)) return
        Files.walk(path).use { paths ->
            paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) }
        }
    }

    private fun unsupportedStatus(): CliInstallationStatus = CliInstallationStatus(
        supported = false,
        bundledPayloadAvailable = false,
        installed = false,
        message = "一键 CLI 安装仅支持 macOS。",
    )

    private companion object {
        private const val COMMAND_FILE = "silverwing"
        private const val PACKAGED_COMMAND_FILE = "silverwing"
        private const val VERSION_FILE = "silverwing.version"
        private const val PAYLOAD_MARKER_FILE = ".silverwing-cli-managed"
        private const val PAYLOAD_MARKER_CONTENT = "owner=silverwing\nformat=1\n"
        private const val STABLE_COMMAND_MARKER = "SILVERWING-CLI-MANAGED: v1"
        private const val PROFILE_BLOCK_START = "# >>> silverwing CLI >>>"
        private const val PROFILE_BLOCK_END = "# <<< silverwing CLI <<<"
        private val PROFILE_BLOCK_PATTERN = Regex(
            "(?ms)^${Regex.escape(PROFILE_BLOCK_START)}\\R.*?^${Regex.escape(PROFILE_BLOCK_END)}\\R?",
        )
        private val VERSION_PATTERN = Regex("[0-9A-Za-z][0-9A-Za-z.+-]{0,127}")

        private fun packagedSource(): PortableCliSource? {
            val resources = System.getProperty("compose.application.resources.dir") ?: return null
            val cliHome = Path.of(resources).resolve("silverwing")
            val runtimeHome = Path.of(resources).resolve("silverwing-runtime")
            val version = runCatching {
                Files.readString(cliHome.resolve("VERSION"), StandardCharsets.UTF_8).trim()
            }.getOrNull() ?: return null
            return PortableCliSource(cliHome, runtimeHome, version)
        }
    }
}
