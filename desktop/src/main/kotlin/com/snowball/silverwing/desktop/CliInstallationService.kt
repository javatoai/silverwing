package com.snowball.silverwing.desktop

import com.sun.jna.Memory
import com.sun.jna.Native
import com.sun.jna.Pointer
import com.sun.jna.platform.win32.Advapi32Util
import com.sun.jna.platform.win32.User32
import com.sun.jna.platform.win32.WinDef
import com.sun.jna.platform.win32.WinReg
import com.sun.jna.platform.win32.WinUser
import java.nio.charset.StandardCharsets
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardCopyOption.REPLACE_EXISTING
import java.util.Comparator
import java.util.Locale

/** One portable CLI payload bundled with the desktop green package. */
internal data class PortableCliSource(
    val cliHome: Path,
    val runtimeHome: Path,
    val version: String,
)

data class CliInstallationStatus(
    val supported: Boolean,
    val bundledPayloadAvailable: Boolean,
    val installed: Boolean,
    val uninstallAvailable: Boolean = false,
    val commandPath: Path? = null,
    val version: String? = null,
    val message: String,
)

internal interface CliInstallationService {
    fun inspect(): CliInstallationStatus
    fun install(): CliInstallationStatus
    fun uninstall(): CliInstallationStatus
}

/** Selects the installer that owns the current user's command environment. */
internal fun platformCliInstallationService(
    osName: String = System.getProperty("os.name"),
): CliInstallationService = when {
    osName.startsWith("Windows", ignoreCase = true) -> WindowsCliInstallationService()
    osName.startsWith("Mac", ignoreCase = true) -> MacCliInstallationService()
    else -> UnsupportedCliInstallationService()
}

private class UnsupportedCliInstallationService : CliInstallationService {
    override fun inspect(): CliInstallationStatus = CliInstallationStatus(
        supported = false,
        bundledPayloadAvailable = false,
        installed = false,
        message = "当前系统暂不支持 silverwing CLI 的一键安装；请使用绿色包内的 bin/silverwing。",
    )

    override fun install(): CliInstallationStatus = error("当前系统不支持 silverwing CLI 的一键安装")

    override fun uninstall(): CliInstallationStatus = error("当前系统不支持 silverwing CLI 的一键卸载")
}

/**
 * Installs the CLI from a Windows green package into the current user's local
 * application-data directory. Its dedicated packaged jlink runtime is copied
 * with the CLI so `silverwing` never depends on a machine-wide JDK.
 */
internal class WindowsCliInstallationService(
    private val source: () -> PortableCliSource? = ::packagedSource,
    private val localApplicationData: () -> Path = ::defaultLocalApplicationData,
    private val userPath: UserPathStore = WindowsRegistryUserPathStore(),
    private val isWindows: () -> Boolean = { System.getProperty("os.name").startsWith("Windows", ignoreCase = true) },
    private val environmentChanged: () -> Unit = ::broadcastEnvironmentChange,
) : CliInstallationService {
    override fun inspect(): CliInstallationStatus {
        if (!isWindows()) {
            return CliInstallationStatus(
                supported = false,
                bundledPayloadAvailable = false,
                installed = false,
                message = "当前仅提供 Windows 绿色包的一键 CLI 安装；macOS/Linux 请使用包内 bin/silverwing。",
            )
        }
        val command = commandDirectory().resolve(COMMAND_FILE)
        val installedVersion = readInstalledVersion()
        val managedCommand = isManagedStableCommand(command, installedVersion)
        val managedPayload = hasManagedPayload(managedCommand, installedVersion)
        val installed = managedCommand && installedVersion != null &&
            validInstallation(cliVersionsDirectory().resolve(installedVersion))
        val uninstallAvailable = managedCommand || managedPayload
        val bundled = bundledSource()
        return when {
            installed && bundled != null -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = true,
                installed = true,
                uninstallAvailable = true,
                commandPath = command,
                version = installedVersion,
                message = "已安装 silverwing CLI $installedVersion；新开的终端可直接运行 silverwing。",
            )
            installed -> CliInstallationStatus(
                supported = true,
                bundledPayloadAvailable = false,
                installed = true,
                uninstallAvailable = true,
                commandPath = command,
                version = installedVersion,
                message = "已安装 silverwing CLI $installedVersion；当前运行的不是可更新的绿色包。",
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
                message = "绿色包内含 silverwing CLI ${bundled.version}；安装后会写入当前用户 PATH。",
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
        require(isWindows()) { "一键 CLI 安装仅支持 Windows 绿色包" }
        val bundled = requireNotNull(bundledSource()) {
            "未找到完整的绿色包 CLI 或运行时；开发模式不会复制本机 JDK。"
        }
        validateSource(bundled)
        validateStableCommandTarget()

        val versionDirectory = cliVersionsDirectory().resolve(bundled.version)
        if (Files.exists(versionDirectory)) {
            require(validInstallation(versionDirectory)) { "CLI 目标版本目录不完整：$versionDirectory；请先手工备份并删除该目录后重试" }
        } else {
            Files.createDirectories(cliVersionsDirectory())
            val staging = Files.createTempDirectory(cliVersionsDirectory(), ".${bundled.version}-")
            try {
                copyDirectory(bundled.cliHome, staging.resolve("cli"))
                copyDirectory(bundled.runtimeHome, staging.resolve("runtime"))
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
        addCommandDirectoryToUserPath()
        environmentChanged()
        return inspect()
    }

    override fun uninstall(): CliInstallationStatus {
        require(isWindows()) { "一键 CLI 卸载仅支持 Windows" }

        val command = commandDirectory().resolve(COMMAND_FILE)
        val installedVersion = readInstalledVersion()
        val managedCommand = isManagedStableCommand(command, installedVersion)
        val managedPayload = hasManagedPayload(managedCommand, installedVersion)
        if (!managedCommand && !managedPayload) return inspect()

        // Delete the payload first. If a running CLI holds a file lock, the
        // command and PATH stay available and the user can retry cleanup.
        if (managedPayload) deleteManagedPayload()
        if (managedCommand) {
            Files.deleteIfExists(command)
            Files.deleteIfExists(commandDirectory().resolve(VERSION_FILE))
            if (removeCommandDirectoryFromUserPath()) environmentChanged()
        }
        return inspect()
    }

    private fun bundledSource(): PortableCliSource? = source()?.takeIf(::isCompleteSource)

    private fun isCompleteSource(source: PortableCliSource): Boolean =
        source.version.matches(VERSION_PATTERN) &&
            Files.isRegularFile(source.cliHome.resolve("bin").resolve(PACKAGED_COMMAND_FILE)) &&
            hasCliLibraries(source.cliHome) &&
            Files.isRegularFile(source.runtimeHome.resolve("bin").resolve("java.exe"))

    private fun validateSource(source: PortableCliSource) {
        require(source.version.matches(VERSION_PATTERN)) { "CLI 版本号不合法：${source.version}" }
        require(Files.isRegularFile(source.cliHome.resolve("bin").resolve(PACKAGED_COMMAND_FILE))) {
            "绿色包缺少 CLI 启动脚本"
        }
        require(hasCliLibraries(source.cliHome)) { "绿色包缺少 CLI 依赖库" }
        require(Files.isRegularFile(source.runtimeHome.resolve("bin").resolve("java.exe"))) {
            "绿色包缺少随附 Java 运行时"
        }
    }

    private fun validInstallation(root: Path): Boolean =
        Files.isRegularFile(root.resolve("cli/bin").resolve(PACKAGED_COMMAND_FILE)) &&
            hasCliLibraries(root.resolve("cli")) &&
            Files.isRegularFile(root.resolve("runtime/bin/java.exe"))

    private fun hasCliLibraries(cliHome: Path): Boolean {
        val libraries = cliHome.resolve("lib")
        if (!Files.isDirectory(libraries)) return false
        return Files.list(libraries).use { entries -> entries.anyMatch(Files::isRegularFile) }
    }

    private fun validateStableCommandTarget() {
        val command = commandDirectory().resolve(COMMAND_FILE)
        if (!Files.exists(command)) return
        require(isManagedStableCommand(command, readInstalledVersion())) {
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

        // Keep the ownership marker until every payload file is removed. If a
        // running silverwing process holds a file lock, the next inspect still offers
        // a retry instead of hiding an incomplete uninstall.
        Files.walk(root).use { paths ->
            paths.sorted(Comparator.reverseOrder())
                .filter { entry -> entry != root && entry != marker }
                .forEach { Files.deleteIfExists(it) }
        }
        Files.deleteIfExists(marker)
        Files.deleteIfExists(root)
    }

    private fun isManagedStableCommand(command: Path, installedVersion: String?): Boolean = runCatching {
        if (!Files.isRegularFile(command)) return@runCatching false
        val content = normalizeCommandContent(Files.readString(command, StandardCharsets.UTF_8))
        content.lineSequence().any { it.trim() == "rem $STABLE_COMMAND_MARKER" } ||
            (installedVersion != null && content == normalizeCommandContent(legacyStableCommandContent(installedVersion)))
    }.getOrDefault(false)

    private fun writeStableCommand(version: String) {
        val command = commandDirectory().resolve(COMMAND_FILE)
        writeAtomically(command, stableCommandContent(version))
        writeAtomically(commandDirectory().resolve(VERSION_FILE), "$version\r\n")
    }

    private fun stableCommandContent(version: String): String = commandContent(version, "rem $STABLE_COMMAND_MARKER")

    private fun legacyStableCommandContent(version: String): String = commandContent(version, "")

    private fun commandContent(version: String, marker: String): String = buildString {
        appendLine("@echo off")
        if (marker.isNotBlank()) appendLine(marker)
        appendLine("setlocal")
        appendLine("call \"%~dp0..\\cli\\$version\\cli\\bin\\$PACKAGED_COMMAND_FILE\" %*")
        appendLine("set \"SILVERWING_EXIT_CODE=%ERRORLEVEL%\"")
        appendLine("endlocal & exit /b %SILVERWING_EXIT_CODE%")
    }.replace("\n", "\r\n")

    private fun normalizeCommandContent(content: String): String = content.replace("\r\n", "\n").trim()

    private fun addCommandDirectoryToUserPath() {
        val directory = commandDirectory().toAbsolutePath().normalize().toString()
        val entries = pathEntries(userPath.read())
        val directoryKey = pathComparisonKey(directory)
        if (entries.any { pathComparisonKey(it) == directoryKey }) return
        userPath.write((entries + directory).joinToString(";"))
    }

    private fun removeCommandDirectoryFromUserPath(): Boolean {
        val directory = commandDirectory().toAbsolutePath().normalize().toString()
        val directoryKey = pathComparisonKey(directory)
        val entries = pathEntries(userPath.read())
        val retained = entries.filterNot { pathComparisonKey(it) == directoryKey }
        if (retained.size == entries.size) return false
        userPath.write(retained.joinToString(";"))
        return true
    }

    private fun pathEntries(value: String): List<String> = value.split(';').filter { it.isNotBlank() }

    private fun pathComparisonKey(value: String): String {
        val localData = System.getenv("LOCALAPPDATA")
        val expanded = if (localData.isNullOrBlank()) value else
            value.replace(LOCAL_APPLICATION_DATA_VARIABLE, localData, ignoreCase = true)
        return expanded.trim().removeSurrounding("\"").trim().replace('/', '\\').trimEnd('\\').lowercase(Locale.ROOT)
    }

    private fun readInstalledVersion(): String? = runCatching {
        Files.readString(commandDirectory().resolve(VERSION_FILE), StandardCharsets.UTF_8).trim()
            .takeIf { it.matches(VERSION_PATTERN) }
    }.getOrNull()

    private fun applicationHome(): Path = localApplicationData().resolve("silverwing")
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
        Files.createDirectories(target.parent)
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
                    Files.createDirectories(destination.parent)
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

    companion object {
        private const val COMMAND_FILE = "silverwing.cmd"
        private const val PACKAGED_COMMAND_FILE = "silverwing.cmd"
        private const val VERSION_FILE = "silverwing.version"
        private const val PAYLOAD_MARKER_FILE = ".silverwing-cli-managed"
        private const val PAYLOAD_MARKER_CONTENT = "owner=silverwing\nformat=1\n"
        private const val STABLE_COMMAND_MARKER = "SILVERWING-CLI-MANAGED: v1"
        private const val LOCAL_APPLICATION_DATA_VARIABLE = "%LOCALAPPDATA%"
        private val VERSION_PATTERN = Regex("[0-9A-Za-z][0-9A-Za-z.+-]{0,127}")

        private fun packagedSource(): PortableCliSource? {
            val resources = System.getProperty("compose.application.resources.dir") ?: return null
            val cliHome = Path.of(resources).resolve("silverwing")
            val runtimeHome = Path.of(resources).resolve("silverwing-runtime")
            val version = runCatching { Files.readString(cliHome.resolve("VERSION"), StandardCharsets.UTF_8).trim() }.getOrNull()
                ?: return null
            return PortableCliSource(cliHome, runtimeHome, version)
        }

        private fun defaultLocalApplicationData(): Path =
            System.getenv("LOCALAPPDATA")?.takeIf(String::isNotBlank)?.let(Path::of)
                ?: Path.of(System.getProperty("user.home"), "AppData", "Local")

        /**
         * Notifies Explorer and other Windows applications that the user PATH
         * changed. Existing shells still keep their inherited environments and
         * must be reopened by their host application.
         */
        private fun broadcastEnvironmentChange() {
            val message = Memory((ENVIRONMENT_VALUE.length + 1L) * Native.WCHAR_SIZE)
            message.setWideString(0, ENVIRONMENT_VALUE)
            runCatching {
                User32.INSTANCE.SendMessageTimeout(
                    WinUser.HWND_BROADCAST,
                    WM_SETTING_CHANGE,
                    WinDef.WPARAM(0),
                    WinDef.LPARAM(Pointer.nativeValue(message)),
                    WinUser.SMTO_ABORTIFHUNG,
                    ENVIRONMENT_CHANGE_TIMEOUT_MILLIS,
                    WinDef.DWORDByReference(),
                )
            }
        }

        private const val WM_SETTING_CHANGE = 0x001A
        private const val ENVIRONMENT_CHANGE_TIMEOUT_MILLIS = 5_000
        private const val ENVIRONMENT_VALUE = "Environment"
    }
}

internal interface UserPathStore {
    fun read(): String
    fun write(value: String)
}

private class WindowsRegistryUserPathStore : UserPathStore {
    override fun read(): String {
        if (!Advapi32Util.registryValueExists(WinReg.HKEY_CURRENT_USER, ENVIRONMENT_KEY, PATH_VALUE)) return ""
        return runCatching {
            Advapi32Util.registryGetExpandableStringValue(WinReg.HKEY_CURRENT_USER, ENVIRONMENT_KEY, PATH_VALUE)
        }.getOrElse {
            Advapi32Util.registryGetStringValue(WinReg.HKEY_CURRENT_USER, ENVIRONMENT_KEY, PATH_VALUE)
        }
    }

    override fun write(value: String) {
        Advapi32Util.registrySetExpandableStringValue(WinReg.HKEY_CURRENT_USER, ENVIRONMENT_KEY, PATH_VALUE, value)
    }

    private companion object {
        const val ENVIRONMENT_KEY = "Environment"
        const val PATH_VALUE = "Path"
    }
}
