package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import java.time.Instant
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DevelopmentToolPathDetectionTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `Windows standard candidates cover all supported tools before PATH`() {
        val roots = windowsRoots("windows-standard")
        val programFiles = requireNotNull(roots.programFiles)
        val localAppData = requireNotNull(roots.localAppData)
        val expected = linkedMapOf(
            DevelopmentToolType.INTELLIJ_IDEA to file(programFiles.resolve("JetBrains/IntelliJ IDEA 2026.1/bin/idea64.exe")),
            DevelopmentToolType.WEBSTORM to file(programFiles.resolve("JetBrains/WebStorm 2026.1/bin/webstorm64.exe")),
            DevelopmentToolType.PYCHARM to file(localAppData.resolve("Programs/JetBrains/PyCharm 2026.1/bin/pycharm64.exe")),
            DevelopmentToolType.VISUAL_STUDIO_CODE to file(localAppData.resolve("Programs/Microsoft VS Code/Code.exe")),
            DevelopmentToolType.ANDROID_STUDIO to file(programFiles.resolve("Android/Android Studio/bin/studio64.exe")),
            DevelopmentToolType.DEVECO_STUDIO to file(programFiles.resolve("Huawei/DevEco Studio/bin/devecostudio64.exe")),
        )
        val fallback = file(temporary.resolve("path/fallback.exe"))
        val detector = LocalDevelopmentToolPathDetector(
            roots,
            DevelopmentToolPathLookup { fallback },
        )

        val detected = detector.detect(DevelopmentToolType.entries.toSet())

        assertEquals(expected.mapValues { it.value.toAbsolutePath().normalize() }, detected)
    }

    @Test
    fun `macOS standard application candidates cover all supported tools`() {
        val base = temporary.resolve("mac")
        val applications = Files.createDirectories(base.resolve("Applications"))
        val userApplications = Files.createDirectories(base.resolve("Users/alice/Applications"))
        val roots = DevelopmentToolDetectionRoots(
            osName = "Mac OS X",
            userHome = base.resolve("Users/alice"),
            macApplicationDirectories = listOf(applications, userApplications),
        )
        val expected = linkedMapOf(
            DevelopmentToolType.INTELLIJ_IDEA to application(applications.resolve("IntelliJ IDEA.app")),
            DevelopmentToolType.WEBSTORM to application(applications.resolve("WebStorm.app")),
            DevelopmentToolType.PYCHARM to application(userApplications.resolve("PyCharm.app")),
            DevelopmentToolType.VISUAL_STUDIO_CODE to application(applications.resolve("Visual Studio Code.app")),
            DevelopmentToolType.ANDROID_STUDIO to application(applications.resolve("Android Studio.app")),
            DevelopmentToolType.DEVECO_STUDIO to application(userApplications.resolve("DevEco-Studio.app")),
        )
        val detector = LocalDevelopmentToolPathDetector(roots, DevelopmentToolPathLookup { null })

        val detected = detector.detect(DevelopmentToolType.entries.toSet())

        assertEquals(expected.mapValues { it.value.toAbsolutePath().normalize() }, detected)
    }

    @Test
    fun `Toolbox chooses latest stable build and remains ahead of PATH`() {
        val roots = windowsRoots("windows-toolbox")
        val product = requireNotNull(roots.localAppData).resolve("JetBrains/Toolbox/apps/IDEA-U/ch-0")
        val older = file(product.resolve("2025.3/bin/idea64.exe"))
        val latest = file(product.resolve("2026.1/bin/idea64.exe"))
        Files.setLastModifiedTime(older.parent.parent, FileTime.from(Instant.parse("2026-01-01T00:00:00Z")))
        Files.setLastModifiedTime(latest.parent.parent, FileTime.from(Instant.parse("2026-08-01T00:00:00Z")))
        val pathFallback = file(temporary.resolve("path/idea64.exe"))
        val detector = LocalDevelopmentToolPathDetector(
            roots,
            DevelopmentToolPathLookup { type -> if (type == DevelopmentToolType.INTELLIJ_IDEA) pathFallback else null },
        )

        val detected = detector.detect(setOf(DevelopmentToolType.INTELLIJ_IDEA))

        assertEquals(latest.toAbsolutePath().normalize(), detected.getValue(DevelopmentToolType.INTELLIJ_IDEA))
    }

    @Test
    fun `macOS Toolbox stable application is detected`() {
        val base = temporary.resolve("mac-toolbox")
        val home = Files.createDirectories(base.resolve("Users/alice"))
        val roots = DevelopmentToolDetectionRoots(
            osName = "Mac OS X",
            userHome = home,
            macApplicationDirectories = listOf(base.resolve("Applications"), home.resolve("Applications")),
        )
        val toolbox = home.resolve("Library/Application Support/JetBrains/Toolbox/apps/WebStorm/ch-0")
        val older = application(toolbox.resolve("2025.3/WebStorm.app"))
        val latest = application(toolbox.resolve("2026.1/WebStorm.app"))
        Files.setLastModifiedTime(older.parent, FileTime.from(Instant.parse("2026-01-01T00:00:00Z")))
        Files.setLastModifiedTime(latest.parent, FileTime.from(Instant.parse("2026-08-01T00:00:00Z")))
        val detector = LocalDevelopmentToolPathDetector(roots, DevelopmentToolPathLookup { null })

        val detected = detector.detect(setOf(DevelopmentToolType.WEBSTORM))

        assertEquals(latest.toAbsolutePath().normalize(), detected.getValue(DevelopmentToolType.WEBSTORM))
    }

    @Test
    fun `PATH is used only after local candidates are absent`() {
        val roots = windowsRoots("windows-path")
        val fallback = file(temporary.resolve("path/pycharm64.exe"))
        val detector = LocalDevelopmentToolPathDetector(
            roots,
            DevelopmentToolPathLookup { type -> if (type == DevelopmentToolType.PYCHARM) fallback else null },
        )

        val detected = detector.detect(setOf(DevelopmentToolType.PYCHARM))

        assertEquals(fallback.toAbsolutePath().normalize(), detected.getValue(DevelopmentToolType.PYCHARM))
    }

    @Test
    fun `macOS PATH lookup uses the login shell visible to GUI launches`() {
        val executable = file(temporary.resolve("homebrew/bin/code"))
        val commands = mutableListOf<List<String>>()
        val lookup = CommandDevelopmentToolPathLookup(
            osName = "Mac OS X",
            runner = object : CommandRunner {
                override fun run(
                    command: List<String>,
                    workingDirectory: Path?,
                    timeout: java.time.Duration,
                    environment: Map<String, String>,
                ): CommandResult {
                    commands += command
                    return if (command.last() == "command -v code") {
                        CommandResult(0, "${executable}\n", "")
                    } else {
                        CommandResult(1, "", "not found")
                    }
                }
            },
        )

        assertEquals(executable.toAbsolutePath().normalize(), lookup.find(DevelopmentToolType.VISUAL_STUDIO_CODE))
        assertEquals(
            listOf(
                listOf("/bin/zsh", "-lc", "command -v Code.exe"),
                listOf("/bin/zsh", "-lc", "command -v code.cmd"),
                listOf("/bin/zsh", "-lc", "command -v code"),
            ),
            commands,
        )
    }

    @Test
    fun `atomic persistence preserves existing and concurrently entered paths while filling empty types`() {
        val paths = ApplicationPaths(temporary.resolve("persist/home"))
        val store = ConfigStore(paths)
        val existingInvalid = temporary.resolve("missing/idea64.exe").toString()
        store.save(
            AppConfig(
                developmentTools = listOf(
                    DevelopmentToolConfig(DevelopmentToolType.INTELLIJ_IDEA, existingInvalid),
                ),
            ),
        )
        var updateCount = 0
        val repository = object : ConfigurationRepository {
            override fun load(): AppConfig = store.load()
            override fun save(config: AppConfig) = store.save(config)
            override fun update(transform: (AppConfig) -> AppConfig): AppConfig {
                updateCount += 1
                return store.update(transform)
            }
        }
        val manualWebStorm = temporary.resolve("manual/webstorm64.exe").toString()
        val detectedWebStorm = temporary.resolve("detected/webstorm64.exe")
        val detectedCode = temporary.resolve("detected/Code.exe")
        val detector = DevelopmentToolPathDetector { missing ->
            assertFalse(DevelopmentToolType.INTELLIJ_IDEA in missing)
            store.update { current ->
                current.copy(
                    developmentTools = current.developmentTools +
                        DevelopmentToolConfig(DevelopmentToolType.WEBSTORM, manualWebStorm),
                )
            }
            mapOf(
                DevelopmentToolType.WEBSTORM to detectedWebStorm,
                DevelopmentToolType.VISUAL_STUDIO_CODE to detectedCode,
            )
        }
        val service = DevelopmentToolAutoDetectionService(repository, detector)

        val result = service.detectAndPersist(repository.load())
        val byType = result.config.developmentTools.associateBy(DevelopmentToolConfig::type)

        assertEquals(1, updateCount)
        assertEquals(existingInvalid, byType.getValue(DevelopmentToolType.INTELLIJ_IDEA).path)
        assertEquals(manualWebStorm, byType.getValue(DevelopmentToolType.WEBSTORM).path)
        assertEquals(detectedCode.toAbsolutePath().normalize().toString(), byType.getValue(DevelopmentToolType.VISUAL_STUDIO_CODE).path)
        assertEquals(setOf(DevelopmentToolType.VISUAL_STUDIO_CODE), result.addedTypes)
        assertTrue(store.load().developmentTools == result.config.developmentTools)
    }

    @Test
    fun `no detected path leaves configuration untouched without an update`() {
        val paths = ApplicationPaths(temporary.resolve("not-found/home"))
        val store = ConfigStore(paths)
        store.save(AppConfig())
        var updateCount = 0
        val repository = object : ConfigurationRepository {
            override fun load(): AppConfig = store.load()
            override fun save(config: AppConfig) = store.save(config)
            override fun update(transform: (AppConfig) -> AppConfig): AppConfig {
                updateCount += 1
                return store.update(transform)
            }
        }
        val service = DevelopmentToolAutoDetectionService(repository, DevelopmentToolPathDetector { emptyMap() })

        val result = service.detectAndPersist(repository.load())

        assertEquals(0, updateCount)
        assertTrue(result.addedTypes.isEmpty())
        assertTrue(result.config.developmentTools.isEmpty())
    }

    private fun windowsRoots(name: String): DevelopmentToolDetectionRoots {
        val base = temporary.resolve(name)
        return DevelopmentToolDetectionRoots(
            osName = "Windows 11",
            userHome = base.resolve("Users/alice"),
            programFiles = Files.createDirectories(base.resolve("Program Files")),
            programFilesX86 = Files.createDirectories(base.resolve("Program Files (x86)")),
            localAppData = Files.createDirectories(base.resolve("Users/alice/AppData/Local")),
        )
    }

    private fun file(path: Path): Path {
        Files.createDirectories(path.parent)
        return Files.writeString(path, "stub")
    }

    private fun application(path: Path): Path = Files.createDirectories(path)
}
