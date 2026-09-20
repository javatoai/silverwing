package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration

data class DevelopmentToolDetectionRoots(
    val osName: String,
    val userHome: Path,
    val programFiles: Path? = null,
    val programFilesX86: Path? = null,
    val localAppData: Path? = null,
    val macApplicationDirectories: List<Path> = emptyList(),
) {
    companion object {
        fun systemDefault(
            osName: String = System.getProperty("os.name"),
            userHome: String = System.getProperty("user.home"),
            environment: (String) -> String? = System::getenv,
        ): DevelopmentToolDetectionRoots {
            val home = Path.of(userHome).toAbsolutePath().normalize()
            return DevelopmentToolDetectionRoots(
                osName = osName,
                userHome = home,
                programFiles = environment("ProgramFiles")?.takeIf(String::isNotBlank)?.let(Path::of),
                programFilesX86 = environment("ProgramFiles(x86)")?.takeIf(String::isNotBlank)?.let(Path::of),
                localAppData = environment("LOCALAPPDATA")?.takeIf(String::isNotBlank)?.let(Path::of),
                macApplicationDirectories = if (osName.startsWith("Mac", ignoreCase = true)) {
                    listOf(Path.of("/Applications"), home.resolve("Applications"))
                } else {
                    emptyList()
                },
            )
        }
    }
}

fun interface DevelopmentToolPathLookup {
    fun find(type: DevelopmentToolType): Path?
}

fun interface DevelopmentToolPathDetector {
    /** Returns existing local application paths only for the requested types. */
    fun detect(types: Set<DevelopmentToolType>): Map<DevelopmentToolType, Path>
}

class CommandDevelopmentToolPathLookup(
    private val osName: String = System.getProperty("os.name"),
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val timeout: Duration = Duration.ofSeconds(3),
) : DevelopmentToolPathLookup {
    override fun find(type: DevelopmentToolType): Path? {
        return pathCommandNames(type).firstNotNullOfOrNull { command ->
            val probe = probeCommand(command) ?: return@firstNotNullOfOrNull null
            runCatching { runner.run(probe, timeout = timeout) }
                .getOrNull()
                ?.takeIf(CommandResult::succeeded)
                ?.stdout
                ?.lineSequence()
                ?.map(String::trim)
                ?.filter(String::isNotBlank)
                ?.map { it.trim('"') }
                ?.mapNotNull { runCatching { Path.of(it).toAbsolutePath().normalize() }.getOrNull() }
                ?.firstOrNull(Files::exists)
        }
    }

    private fun probeCommand(command: String): List<String>? = when {
        osName.startsWith("Windows", ignoreCase = true) -> listOf("where.exe", command)
        // A desktop app launched by Finder does not inherit the user's login
        // shell PATH.  Run the lookup in zsh so Homebrew and other paths from
        // ~/.zprofile remain visible to development-tool auto detection.
        osName.startsWith("Mac", ignoreCase = true) -> listOf("/bin/zsh", "-lc", "command -v $command")
        else -> null
    }
}

class LocalDevelopmentToolPathDetector(
    private val roots: DevelopmentToolDetectionRoots = DevelopmentToolDetectionRoots.systemDefault(),
    private val pathLookup: DevelopmentToolPathLookup = CommandDevelopmentToolPathLookup(roots.osName),
) : DevelopmentToolPathDetector {
    override fun detect(types: Set<DevelopmentToolType>): Map<DevelopmentToolType, Path> {
        if (!isWindows && !isMac) return emptyMap()
        return types.sortedBy(DevelopmentToolType::ordinal).mapNotNull { type ->
            val detected = standardCandidates(type).firstOrNull(::isApplication)
                ?: toolboxCandidates(type).firstOrNull(::isApplication)
                ?: pathLookup.find(type)?.takeIf(::isApplication)
            detected?.toAbsolutePath()?.normalize()?.let { type to it }
        }.toMap(linkedMapOf())
    }

    private fun standardCandidates(type: DevelopmentToolType): List<Path> = if (isWindows) {
        windowsStandardCandidates(type)
    } else {
        roots.macApplicationDirectories.flatMap { directory ->
            macApplicationNames(type).map(directory::resolve)
        }
    }

    private fun windowsStandardCandidates(type: DevelopmentToolType): List<Path> {
        val localPrograms = roots.localAppData?.resolve("Programs")
        return when (type) {
            DevelopmentToolType.INTELLIJ_IDEA,
            DevelopmentToolType.WEBSTORM,
            DevelopmentToolType.PYCHARM,
            -> windowsJetBrainsInstallCandidates(type, listOfNotNull(roots.programFiles, roots.programFilesX86, localPrograms))
            DevelopmentToolType.VISUAL_STUDIO_CODE -> listOfNotNull(
                localPrograms?.resolve("Microsoft VS Code/Code.exe"),
                roots.programFiles?.resolve("Microsoft VS Code/Code.exe"),
                roots.programFilesX86?.resolve("Microsoft VS Code/Code.exe"),
            )
            DevelopmentToolType.ANDROID_STUDIO -> listOfNotNull(
                roots.programFiles?.resolve("Android/Android Studio/bin/studio64.exe"),
                roots.programFilesX86?.resolve("Android/Android Studio/bin/studio64.exe"),
                localPrograms?.resolve("Android Studio/bin/studio64.exe"),
                localPrograms?.resolve("Google/Android Studio/bin/studio64.exe"),
            )
            DevelopmentToolType.DEVECO_STUDIO -> listOfNotNull(
                roots.programFiles?.resolve("Huawei/DevEco Studio/bin/devecostudio64.exe"),
                roots.programFilesX86?.resolve("Huawei/DevEco Studio/bin/devecostudio64.exe"),
                localPrograms?.resolve("Huawei/DevEco Studio/bin/devecostudio64.exe"),
                localPrograms?.resolve("DevEco Studio/bin/devecostudio64.exe"),
            )
        }
    }

    private fun windowsJetBrainsInstallCandidates(type: DevelopmentToolType, bases: List<Path>): List<Path> {
        val prefix = when (type) {
            DevelopmentToolType.INTELLIJ_IDEA -> "IntelliJ IDEA"
            DevelopmentToolType.WEBSTORM -> "WebStorm"
            DevelopmentToolType.PYCHARM -> "PyCharm"
            else -> return emptyList()
        }
        val executable = windowsExecutableName(type)
        return bases.flatMap { base ->
            val vendor = base.resolve("JetBrains")
            children(vendor)
                .filter { Files.isDirectory(it) && it.fileName.toString().startsWith(prefix, ignoreCase = true) }
                .sortedWith(candidateDirectoryOrder)
                .map { it.resolve("bin").resolve(executable) }
        }
    }

    private fun toolboxCandidates(type: DevelopmentToolType): List<Path> {
        val aliases = toolboxProductAliases(type)
        if (aliases.isEmpty()) return emptyList()
        val toolboxRoot = if (isWindows) {
            roots.localAppData?.resolve("JetBrains/Toolbox/apps")
        } else {
            roots.userHome.resolve("Library/Application Support/JetBrains/Toolbox/apps")
        } ?: return emptyList()
        return aliases.flatMap { alias ->
            val stableChannel = toolboxRoot.resolve(alias).resolve("ch-0")
            children(stableChannel)
                .filter(Files::isDirectory)
                .flatMap { build ->
                    if (isWindows) {
                        listOf(build.resolve("bin").resolve(windowsExecutableName(type)))
                    } else {
                        macApplicationNames(type).map(build::resolve)
                    }
                }
        }.sortedWith(
            compareByDescending<Path> { candidateModifiedAt(toolboxBuildDirectory(it)) }
                .thenByDescending { it.toString() },
        )
    }

    private fun toolboxBuildDirectory(candidate: Path): Path = if (isWindows) {
        candidate.parent?.parent ?: candidate
    } else {
        candidate.parent ?: candidate
    }

    private fun isApplication(path: Path): Boolean = if (isMac && path.fileName.toString().endsWith(".app", true)) {
        Files.isDirectory(path)
    } else {
        Files.isRegularFile(path)
    }

    private fun children(directory: Path): List<Path> = if (!Files.isDirectory(directory)) {
        emptyList()
    } else {
        runCatching { Files.list(directory).use { it.toList() } }.getOrDefault(emptyList())
    }

    private fun candidateModifiedAt(path: Path): Long =
        runCatching { Files.getLastModifiedTime(path).toMillis() }.getOrDefault(Long.MIN_VALUE)

    private val candidateDirectoryOrder = compareByDescending<Path>(::candidateModifiedAt)
        .thenByDescending { it.fileName.toString() }

    private val isWindows: Boolean get() = roots.osName.startsWith("Windows", ignoreCase = true)
    private val isMac: Boolean get() = roots.osName.startsWith("Mac", ignoreCase = true)
}

data class DevelopmentToolAutoDetectionResult(
    val config: AppConfig,
    val addedTypes: Set<DevelopmentToolType>,
)

fun interface DevelopmentToolStartupDetection {
    fun detectAndPersist(initialConfig: AppConfig): DevelopmentToolAutoDetectionResult
}

class DevelopmentToolAutoDetectionService(
    private val configurations: ConfigurationRepository = ConfigStore(),
    private val detector: DevelopmentToolPathDetector = LocalDevelopmentToolPathDetector(),
) : DevelopmentToolStartupDetection {
    override fun detectAndPersist(initialConfig: AppConfig): DevelopmentToolAutoDetectionResult {
        val initiallyConfigured = initialConfig.developmentTools.map(DevelopmentToolConfig::type).toSet()
        val missing = DevelopmentToolType.entries.filterNot(initiallyConfigured::contains).toSet()
        if (missing.isEmpty()) return DevelopmentToolAutoDetectionResult(configurations.load(), emptySet())
        val detected = detector.detect(missing).filterKeys(missing::contains)
        if (detected.isEmpty()) return DevelopmentToolAutoDetectionResult(configurations.load(), emptySet())

        var addedTypes = emptySet<DevelopmentToolType>()
        val updated = configurations.update { current ->
            val configuredNow = current.developmentTools.map(DevelopmentToolConfig::type).toSet()
            val additions = DevelopmentToolType.entries.mapNotNull { type ->
                detected[type]
                    ?.takeIf { type !in configuredNow }
                    ?.toAbsolutePath()
                    ?.normalize()
                    ?.let { DevelopmentToolConfig(type, it.toString()) }
            }
            addedTypes = additions.map(DevelopmentToolConfig::type).toSet()
            if (additions.isEmpty()) current else current.copy(developmentTools = current.developmentTools + additions)
        }
        return DevelopmentToolAutoDetectionResult(updated, addedTypes)
    }
}

private fun windowsExecutableName(type: DevelopmentToolType): String = when (type) {
    DevelopmentToolType.INTELLIJ_IDEA -> "idea64.exe"
    DevelopmentToolType.WEBSTORM -> "webstorm64.exe"
    DevelopmentToolType.PYCHARM -> "pycharm64.exe"
    DevelopmentToolType.VISUAL_STUDIO_CODE -> "Code.exe"
    DevelopmentToolType.ANDROID_STUDIO -> "studio64.exe"
    DevelopmentToolType.DEVECO_STUDIO -> "devecostudio64.exe"
}

private fun macApplicationNames(type: DevelopmentToolType): List<String> = when (type) {
    DevelopmentToolType.INTELLIJ_IDEA -> listOf("IntelliJ IDEA.app", "IntelliJ IDEA CE.app")
    DevelopmentToolType.WEBSTORM -> listOf("WebStorm.app")
    DevelopmentToolType.PYCHARM -> listOf("PyCharm.app", "PyCharm CE.app")
    DevelopmentToolType.VISUAL_STUDIO_CODE -> listOf("Visual Studio Code.app")
    DevelopmentToolType.ANDROID_STUDIO -> listOf("Android Studio.app")
    DevelopmentToolType.DEVECO_STUDIO -> listOf("DevEco-Studio.app", "DevEco Studio.app")
}

private fun toolboxProductAliases(type: DevelopmentToolType): List<String> = when (type) {
    DevelopmentToolType.INTELLIJ_IDEA -> listOf("IDEA-U", "IDEA-C")
    DevelopmentToolType.WEBSTORM -> listOf("WebStorm")
    DevelopmentToolType.PYCHARM -> listOf("PyCharm-P", "PyCharm-C")
    DevelopmentToolType.VISUAL_STUDIO_CODE,
    DevelopmentToolType.ANDROID_STUDIO,
    DevelopmentToolType.DEVECO_STUDIO,
    -> emptyList()
}

private fun pathCommandNames(type: DevelopmentToolType): List<String> = when (type) {
    DevelopmentToolType.INTELLIJ_IDEA -> listOf("idea64.exe", "idea.exe", "idea")
    DevelopmentToolType.WEBSTORM -> listOf("webstorm64.exe", "webstorm.exe", "webstorm")
    DevelopmentToolType.PYCHARM -> listOf("pycharm64.exe", "pycharm.exe", "pycharm")
    DevelopmentToolType.VISUAL_STUDIO_CODE -> listOf("Code.exe", "code.cmd", "code")
    DevelopmentToolType.ANDROID_STUDIO -> listOf("studio64.exe", "studio.exe", "studio")
    DevelopmentToolType.DEVECO_STUDIO -> listOf("devecostudio64.exe", "devecostudio.exe", "devecostudio")
}
