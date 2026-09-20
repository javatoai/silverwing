package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopConfigurationServicesTest {
    @Test
    fun `development tool recommendation checks root markers and gives JVM priority`() {
        val root = Files.createTempDirectory("silverwing-ide-recommendation")
        val service = RootMarkerDevelopmentToolRecommendationService()

        assertEquals(DevelopmentToolType.INTELLIJ_IDEA, service.recommend(root))
        Files.writeString(root.resolve("package.json"), "{}")
        assertEquals(DevelopmentToolType.WEBSTORM, service.recommend(root))
        Files.writeString(root.resolve("build.gradle.kts"), "")
        assertEquals(DevelopmentToolType.INTELLIJ_IDEA, service.recommend(root))
    }

    @Test
    fun `remote branch catalog lists qualified origin heads`() {
        val commands = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                commands += command
                return CommandResult(0, "abc\trefs/heads/main\ndef\trefs/heads/release/test\nabc\trefs/heads/main\n", "")
            }
        }
        val catalog = GitRemoteBranchCatalog(GitClient(runner))

        val repository = Path.of("C:/repo")
        assertEquals(listOf("origin/main", "origin/release/test"), catalog.list(repository))
        assertEquals(
            listOf("git", "-c", "core.longpaths=true", "-C", repository.toString(), "ls-remote", "--heads", "origin"),
            commands.single(),
        )
    }

    @Test
    fun `remote branch search keeps slash names and filters case insensitively`() {
        val branches = listOf("origin/main", "origin/release/test", "origin/feature/ABC")

        assertEquals(listOf("origin/release/test"), RemoteBranchSearch.filter(branches, "TEST"))
        assertEquals(branches, RemoteBranchSearch.filter(branches, ""))
    }
}
