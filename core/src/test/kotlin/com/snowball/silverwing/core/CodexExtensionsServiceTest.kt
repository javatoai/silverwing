package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CodexExtensionsServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `Marketplace add refresh install uninstall use argument safe Codex commands and retain source cache`() {
        val runner = MarketplaceRunner()
        val paths = ApplicationPaths(temporary.resolve("home"))
        val service = CodexExtensionsService(paths, runner, codexExecutable = { "codex-test" })
        val source = CodexPluginMarketplaceSource(
            id = "team-marketplace",
            name = "团队插件",
            repositoryUrl = "https://example.test/team/plugins.git",
            ref = "main",
            marketplaceDirectory = "plugins",
        )

        val added = service.addMarketplace(source)

        assertEquals("team-marketplace", added.marketplaceName)
        assertEquals(listOf("hello-skill"), added.plugins.single().bundledSkills)
        assertTrue(runner.commands.any { it == listOf("codex-test", "plugin", "marketplace", "add", source.repositoryUrl, "--ref", "main", "--sparse", "plugins", "--json") })

        service.installPlugin(source, "team-tools")
        assertEquals(CodexExtensionOwnership.MANAGED, service.pluginOwnership(source.id, added.plugins.single()))
        assertTrue(runner.commands.any { it == listOf("codex-test", "plugin", "add", "team-tools", "--marketplace", "team-marketplace", "--json") })

        service.uninstallPlugin(source, "team-tools")
        assertEquals(CodexExtensionOwnership.NOT_INSTALLED, service.pluginOwnership(source.id, added.plugins.single()))
        assertTrue(runner.commands.any { it == listOf("codex-test", "plugin", "remove", "team-tools", "--marketplace", "team-marketplace", "--json") })

        runner.failList = true
        val failedRefresh = service.refreshMarketplace(source)
        assertEquals(listOf("team-tools"), failedRefresh.plugins.map { it.name })
        assertContains(failedRefresh.error.orEmpty(), "bad Codex config")
    }

    @Test
    fun `Skill source discovers standard locations and takeover keeps a backup before exact uninstall`() {
        val runner = SkillGitRunner()
        val paths = ApplicationPaths(temporary.resolve("home"))
        val userHome = temporary.resolve("user")
        val service = CodexExtensionsService(paths, runner, gitExecutable = { "git-test" }, userHome = { userHome })
        val source = SkillSource("team-skills", "团队 Skills", "https://example.test/team/skills.git", "main")

        val discovered = service.addSkillSource(source)

        assertEquals(listOf("check", "deploy"), discovered.skills.map { it.name })
        assertTrue(runner.commands.any { it.first() == "git-test" && "clone" in it })

        val external = userHome.resolve(".agents/skills/deploy")
        Files.createDirectories(external)
        Files.writeString(external.resolve("legacy.txt"), "an unrelated externally managed directory")
        val deploy = discovered.skills.first { it.name == "deploy" }
        assertFailsWith<CodexExtensionTakeoverRequiredException> { service.installSkill(source, "deploy") }

        service.installSkill(source, "deploy", takeOver = true)

        assertEquals(CodexExtensionOwnership.MANAGED, service.skillOwnership(source.id, "deploy"))
        assertContains(Files.readString(external.resolve("SKILL.md")), "Deploy a service")
        val backupRoot = paths.codex.resolve("skill-backups")
        assertTrue(Files.isDirectory(backupRoot))
        assertTrue(Files.list(backupRoot).use { backups ->
            backups.anyMatch { Files.readString(it.resolve("legacy.txt")) == "an unrelated externally managed directory" }
        })

        val cachedSource = paths.codex.resolve("skill-sources/team-skills/skills/deploy/SKILL.md")
        Files.writeString(cachedSource, "---\nname: deploy\ndescription: Deploy a service v2\n---\nInstructions\n")
        service.installSkill(source, "deploy")
        assertContains(Files.readString(external.resolve("SKILL.md")), "Deploy a service v2")

        val sibling = userHome.resolve(".agents/skills/check")
        Files.createDirectories(sibling)
        Files.writeString(sibling.resolve("SKILL.md"), "---\nname: check\ndescription: keep me\n---\n")

        service.uninstallSkill(source, "deploy")

        assertFalse(Files.exists(external))
        assertTrue(Files.exists(sibling.resolve("SKILL.md")))
    }

    @Test
    fun `source validators reject path escapes and unsupported Git URLs`() {
        assertFailsWith<IllegalArgumentException> {
            CodexPluginMarketplaceSource("x", "X", "file:///tmp/plugins", marketplaceDirectory = "plugins")
        }
        assertFailsWith<IllegalArgumentException> {
            SkillSource("x", "X", "https://example.test/skills.git", skillRoot = "../outside")
        }
        assertFailsWith<IllegalArgumentException> {
            CodexPluginMarketplaceSource("x", "X", "https://example.test/plugins.git", marketplaceDirectory = "plugins//nested")
        }
        assertFailsWith<IllegalArgumentException> {
            SkillSource("x", "X", "https://token@example.test/skills.git")
        }
        assertFailsWith<IllegalArgumentException> {
            SkillSource("x", "X", "https://example.test/skills.git", ref = "feature/../bad")
        }
        val duplicateMarketplace = CodexPluginMarketplaceSource("one", "One", "https://example.test/plugins.git")
        assertFailsWith<IllegalArgumentException> {
            AppConfig(
                codexPluginMarketplaceSources = listOf(
                    duplicateMarketplace,
                    duplicateMarketplace.copy(id = "two", name = "Two"),
                ),
            )
        }
    }

    @Test
    fun `Codex parsers preserve plugin metadata and tolerate additive fields`() {
        val marketplaces = parseMarketplaceList("""{"marketplaces":[{"name":"team","source":{"url":"https://example.test/team.git"},"root":"C:/cache/team","future":true}]}""")
        val plugins = parseCodexPluginList("""{"plugins":[{"name":"demo","description":"Demo","version":"1.2.3","installed":true,"skills":["assist",{"name":"review"}],"future":{}}]}""")

        assertEquals("team", marketplaces.single().name)
        assertEquals("https://example.test/team.git", marketplaces.single().source)
        assertEquals("C:/cache/team", marketplaces.single().cacheDirectory)
        assertEquals("demo", plugins.single().name)
        assertTrue(plugins.single().installed)
        assertEquals(listOf("assist", "review"), plugins.single().bundledSkills)
    }

    @Test
    fun `configured Skill root can itself be a Skill without installing repository Git metadata`() {
        val runner = RootSkillGitRunner()
        val paths = ApplicationPaths(temporary.resolve("home"))
        val userHome = temporary.resolve("user")
        val service = CodexExtensionsService(paths, runner, gitExecutable = { "git-test" }, userHome = { userHome })
        val source = SkillSource("root-skill", "根 Skill", "https://example.test/root-skill.git", skillRoot = ".")

        val discovered = service.addSkillSource(source)

        assertEquals(listOf("root-skill"), discovered.skills.map { it.name })
        assertEquals(".", discovered.skills.single().sourcePath)
        service.installSkill(source, "root-skill")
        assertTrue(Files.exists(userHome.resolve(".agents/skills/root-skill/SKILL.md")))
        assertFalse(Files.exists(userHome.resolve(".agents/skills/root-skill/.git")))
    }

    private class MarketplaceRunner : CommandRunner {
        val commands = mutableListOf<List<String>>()
        var registered = false
        var failList = false

        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            commands += command
            return when {
                command.drop(1).take(3) == listOf("plugin", "marketplace", "list") -> if (failList) {
                    CommandResult(1, "", "bad Codex config")
                } else if (registered) {
                    CommandResult(0, """{"marketplaces":[{"name":"team-marketplace","source":"https://example.test/team/plugins.git"}]}""", "")
                } else {
                    CommandResult(0, """{"marketplaces":[]}""", "")
                }
                command.drop(1).take(3) == listOf("plugin", "marketplace", "add") -> {
                    registered = true
                    CommandResult(0, """{"name":"team-marketplace"}""", "")
                }
                command.drop(1).take(2) == listOf("plugin", "list") && failList -> CommandResult(1, "", "bad Codex config")
                command.drop(1).take(2) == listOf("plugin", "list") -> CommandResult(
                    0,
                    """{"plugins":[{"name":"team-tools","description":"Team automation","version":"1.0.0","installed":false,"skills":["hello-skill"]}]}""",
                    "",
                )
                else -> CommandResult(0, "{}", "")
            }
        }
    }

    private class SkillGitRunner : CommandRunner {
        val commands = mutableListOf<List<String>>()

        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            commands += command
            if ("clone" in command) {
                val checkout = Path.of(command.last())
                Files.createDirectories(checkout.resolve(".git"))
                skill(checkout.resolve("skills/deploy"), "deploy", "Deploy a service")
                skill(checkout.resolve(".agents/skills/check"), "check", "Check a service")
            }
            return CommandResult(0, "", "")
        }

        private fun skill(directory: Path, name: String, description: String) {
            Files.createDirectories(directory)
            Files.writeString(directory.resolve("SKILL.md"), "---\nname: $name\ndescription: $description\n---\nInstructions\n")
        }
    }

    private class RootSkillGitRunner : CommandRunner {
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            if ("clone" in command) {
                val checkout = Path.of(command.last())
                Files.createDirectories(checkout.resolve(".git"))
                Files.writeString(checkout.resolve("SKILL.md"), "---\nname: root-skill\ndescription: Root skill\n---\nInstructions\n")
            }
            return CommandResult(0, "", "")
        }
    }
}
