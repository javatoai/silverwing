package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class CodexExtensionsApplicationServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `coordinates configured sources and local Codex effects outside the desktop layer`() {
        val configurations = InMemoryExtensionsConfigurationRepository(AppConfig())
        val runner = ExtensionsApplicationRunner()
        val application = CodexExtensionsApplicationService(
            configurations = configurations,
            extensions = CodexExtensionsService(
                paths = ApplicationPaths(temporary.resolve("home")),
                runner = runner,
                codexExecutable = { "codex-test" },
            ),
            branchCatalog = RemoteGitBranchCatalog(runner, gitExecutable = { "git-test" }),
        )
        val source = CodexPluginMarketplaceSource(
            id = "team",
            name = "团队插件",
            repositoryUrl = "https://example.test/team/plugins.git",
            marketplaceDirectory = ".",
        )

        val added = application.addMarketplace(source)

        assertEquals(listOf(source), added.codexPluginMarketplaceSources)
        assertEquals(listOf(source), configurations.load().codexPluginMarketplaceSources)
        assertEquals(setOf(source.id), application.snapshot().marketplaces.keys)
        assertEquals(listOf("main", "release/1.0"), application.loadRemoteBranches(source.repositoryUrl))

        val removed = application.removeMarketplace(source)

        assertTrue(removed.codexPluginMarketplaceSources.isEmpty())
        assertTrue(configurations.load().codexPluginMarketplaceSources.isEmpty())
        assertTrue(runner.commands.any { it == listOf("codex-test", "plugin", "marketplace", "remove", "team-marketplace", "--json") })
    }

    private class InMemoryExtensionsConfigurationRepository(
        private var config: AppConfig,
    ) : ConfigurationRepository {
        override fun load(): AppConfig = config

        override fun save(config: AppConfig) {
            this.config = config
        }
    }

    private class ExtensionsApplicationRunner : CommandRunner {
        val commands = mutableListOf<List<String>>()
        private var registered = false

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            return when {
                command.first() == "git-test" -> CommandResult(
                    exitCode = 0,
                    stdout = "one\trefs/heads/main\ntwo\trefs/heads/release/1.0\n",
                    stderr = "",
                )
                command.drop(1).take(3) == listOf("plugin", "marketplace", "list") -> CommandResult(
                    exitCode = 0,
                    stdout = if (registered) {
                        """{"marketplaces":[{"name":"team-marketplace","source":"https://example.test/team/plugins.git"}]}"""
                    } else {
                        """{"marketplaces":[]}"""
                    },
                    stderr = "",
                )
                command.drop(1).take(3) == listOf("plugin", "marketplace", "add") -> {
                    registered = true
                    CommandResult(0, """{"name":"team-marketplace"}""", "")
                }
                command.drop(1).take(2) == listOf("plugin", "list") -> CommandResult(0, """{"plugins":[]}""", "")
                else -> CommandResult(0, "{}", "")
            }
        }
    }
}
