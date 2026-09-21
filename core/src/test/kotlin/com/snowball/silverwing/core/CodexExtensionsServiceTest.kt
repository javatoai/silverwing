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
            marketplaceDirectory = ".",
        )

        val added = service.addMarketplace(source)

        assertEquals("team-marketplace", added.marketplaceName)
        assertEquals(listOf("hello-skill"), added.plugins.single().bundledSkills)
        assertTrue(runner.commands.any { it == listOf("codex-test", "plugin", "marketplace", "add", source.repositoryUrl, "--ref", "main", "--json") })
        assertFalse(runner.commands.flatten().contains("--sparse"))

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
    fun `Marketplace refresh recognizes current Codex JSON when source was already added`() {
        val runner = MarketplaceRunner().apply {
            registered = true
            currentCodexJson = true
        }
        val service = CodexExtensionsService(
            ApplicationPaths(temporary.resolve("current-codex-home")),
            runner,
            codexExecutable = { "codex-test" },
        )
        val source = CodexPluginMarketplaceSource(
            id = "current-marketplace",
            name = "当前格式来源",
            repositoryUrl = "https://example.test/team/plugins.git",
            ref = "main",
            marketplaceDirectory = ".",
        )

        val added = service.refreshMarketplace(source)

        assertEquals("team-marketplace", added.marketplaceName)
        assertEquals(null, added.error)
        assertEquals("C:/cache/team-marketplace", added.cacheDirectory)
        assertEquals(listOf("team-tools"), added.plugins.map { it.name })
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
        assertEquals(listOf(".agents/skills", "skills"), discovered.discoveredRoots)
        assertTrue(runner.commands.any { it.first() == "git-test" && "clone" in it })
        val reloaded = CodexExtensionsService(paths, runner, gitExecutable = { "git-test" })
            .snapshot(emptyList(), listOf(source))
            .skillSources
            .getValue(source.id)
        assertEquals(discovered.discoveredRoots, reloaded.discoveredRoots)

        runner.failRefresh = true
        val failedRefresh = service.refreshSkillSource(source)
        assertEquals(discovered.skills, failedRefresh.skills)
        assertEquals(discovered.discoveredRoots, failedRefresh.discoveredRoots)
        assertContains(failedRefresh.error.orEmpty(), "temporary Git failure")
        runner.failRefresh = false

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
    fun `Skill source omits empty directories from discovered roots`() {
        val runner = SkillGitRunner()
        val service = CodexExtensionsService(
            ApplicationPaths(temporary.resolve("empty-source-home")),
            runner,
            gitExecutable = { "git-test" },
        )
        val source = SkillSource("empty", "空目录", "https://example.test/skills.git", skillRoot = "empty")

        val discovered = service.addSkillSource(source)

        assertEquals(emptyList(), discovered.skills)
        assertEquals(emptyList(), discovered.discoveredRoots)
        assertEquals(null, discovered.error)
    }

    @Test
    fun `Skill preview reads only an already discovered cached Skill`() {
        val runner = SkillGitRunner()
        val paths = ApplicationPaths(temporary.resolve("preview-home"))
        val service = CodexExtensionsService(paths, runner, gitExecutable = { "git-test" })
        val source = SkillSource("preview", "预览", "https://example.test/skills.git", skillRoot = "skills")

        service.addSkillSource(source)

        assertContains(service.previewSkill(source, "deploy"), "Deploy a service")
        assertFailsWith<IllegalArgumentException> { service.previewSkill(source, "missing") }

        val skillDirectory = paths.codex.resolve("skill-sources/preview/skills/deploy")
        Files.delete(skillDirectory.resolve("SKILL.md"))
        assertFailsWith<IllegalArgumentException> { service.previewSkill(source, "deploy") }
    }

    @Test
    fun `source validators reject path escapes and unsupported Git URLs`() {
        assertEquals(
            "http://git.example.test/team/skills.git",
            validateRemoteGitUrl("http://git.example.test/team/skills.git"),
        )
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
            SkillSource("x", "X", "http://token@example.test/skills.git")
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
    fun `remote branch parser sorts distinct heads and selects product defaults`() {
        val branches = parseRemoteGitBranches(
            listOf(
                "a\trefs/heads/release/1.0",
                "b\trefs/heads/main",
                "c\trefs/tags/v1.0",
                "d\trefs/heads/master",
                "e\trefs/heads/main",
            ).joinToString("\n"),
        )

        assertEquals(listOf("main", "master", "release/1.0"), branches)
        assertEquals("master", preferredRemoteGitBranch(branches))
        assertEquals("main", preferredRemoteGitBranch(listOf("develop", "main")))
        assertEquals(null, preferredRemoteGitBranch(listOf("develop")))
        assertEquals("skills", defaultExtensionSourceName("https://example.test/team/skills.git"))
        assertEquals("skills", defaultExtensionSourceName("git@example.test:team/skills.git"))
    }

    @Test
    fun `legacy child Marketplace is reported instead of invoking sparse registration`() {
        val runner = MarketplaceRunner()
        val service = CodexExtensionsService(ApplicationPaths(temporary.resolve("legacy")), runner, codexExecutable = { "codex-test" })
        val source = CodexPluginMarketplaceSource("legacy", "旧来源", "https://example.test/plugins.git", marketplaceDirectory = "plugins")

        val result = service.refreshMarketplace(source)

        assertContains(result.error.orEmpty(), "旧版子目录 Marketplace")
        assertFalse(runner.commands.any { "add" in it })
    }

    @Test
    fun `Codex parsers preserve plugin metadata and tolerate additive fields`() {
        val marketplaces = parseMarketplaceList("""{"marketplaces":[{"name":"team","source":{"url":"https://example.test/team.git"},"root":"C:/cache/team","future":true}]}""")
        val plugins = parseCodexPluginList("""{"plugins":[{"name":"demo","description":"Demo","version":"1.2.3","installed":true,"source":{"path":"C:/cache/team/plugins/demo"},"skills":["assist",{"name":"review"}],"future":{}}]}""")

        assertEquals("team", marketplaces.single().name)
        assertEquals("https://example.test/team.git", marketplaces.single().source)
        assertEquals("C:/cache/team", marketplaces.single().cacheDirectory)
        assertEquals("demo", plugins.single().name)
        assertTrue(plugins.single().installed)
        assertEquals("C:/cache/team/plugins/demo", plugins.single().sourcePath)
        assertEquals(listOf("assist", "review"), plugins.single().bundledSkills)
    }

    @Test
    fun `Codex parser reads current available plugin response shape`() {
        val plugins = parseCodexPluginList(
            """{"installed":[],"available":[{"pluginId":"demo@team","name":"demo","version":"1.0.0","installed":false,"enabled":false,"source":{"source":"local","path":"C:/cache/team"}}]}""",
        )

        assertEquals("demo", plugins.single().name)
        assertEquals("C:/cache/team", plugins.single().sourcePath)
        assertFalse(plugins.single().installed)
    }

    @Test
    fun `plugin preview reads only plugin files beneath the cached Marketplace`() {
        val runner = PluginPreviewRunner(temporary.resolve("marketplace-cache"))
        val service = CodexExtensionsService(ApplicationPaths(temporary.resolve("preview-home")), runner, codexExecutable = { "codex-test" })
        val source = CodexPluginMarketplaceSource("preview-marketplace", "预览插件", "https://example.test/plugins.git", marketplaceDirectory = ".")

        val status = service.addMarketplace(source)
        check(status.error == null) { status.error.orEmpty() }
        val preview = service.previewPlugin(source, "demo")

        assertEquals("Demo plugin", preview.description)
        assertEquals(listOf("assist"), preview.skills.map { it.name })
        assertContains(service.previewPluginSkill(source, "demo", "assist"), "Assist safely")
        assertFailsWith<IllegalArgumentException> { service.previewPluginSkill(source, "demo", "missing") }
        Files.delete(runner.pluginDirectory.resolve(".codex-plugin/plugin.json"))
        assertFailsWith<IllegalArgumentException> { service.previewPlugin(source, "demo") }
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
        assertEquals(listOf("."), discovered.discoveredRoots)
        assertEquals(".", discovered.skills.single().sourcePath)
        service.installSkill(source, "root-skill")
        assertTrue(Files.exists(userHome.resolve(".agents/skills/root-skill/SKILL.md")))
        assertFalse(Files.exists(userHome.resolve(".agents/skills/root-skill/.git")))
    }

    private class MarketplaceRunner : CommandRunner {
        val commands = mutableListOf<List<String>>()
        var registered = false
        var failList = false
        var currentCodexJson = false

        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            commands += command
            return when {
                command.drop(1).take(3) == listOf("plugin", "marketplace", "list") -> if (failList) {
                    CommandResult(1, "", "bad Codex config")
                } else if (registered) {
                    val json = if (currentCodexJson) {
                        """{"marketplaces":[{"name":"team-marketplace","root":"C:/cache/team-marketplace","marketplaceSource":{"sourceType":"git","source":"https://example.test/team/plugins.git"}}]}"""
                    } else {
                        """{"marketplaces":[{"name":"team-marketplace","source":"https://example.test/team/plugins.git"}]}"""
                    }
                    CommandResult(0, json, "")
                } else {
                    CommandResult(0, """{"marketplaces":[]}""", "")
                }
                command.drop(1).take(3) == listOf("plugin", "marketplace", "add") -> {
                    registered = true
                    val json = if (currentCodexJson) {
                        """{"marketplaceName":"team-marketplace","installedRoot":"C:/cache/team-marketplace","alreadyAdded":true}"""
                    } else {
                        """{"name":"team-marketplace"}"""
                    }
                    CommandResult(0, json, "")
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
        var failRefresh = false

        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            commands += command
            if (failRefresh && ("pull" in command || "fetch" in command)) {
                return CommandResult(1, "", "temporary Git failure")
            }
            if ("clone" in command) {
                val checkout = Path.of(command.last())
                Files.createDirectories(checkout.resolve(".git"))
                Files.createDirectories(checkout.resolve("empty"))
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

    private class PluginPreviewRunner(private val cache: Path) : CommandRunner {
        val pluginDirectory = cache.resolve("plugins/demo")
        val commands = mutableListOf<List<String>>()
        private var registered = false

        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            commands += command
            return when {
            command.containsAll(listOf("plugin", "list", "--available")) -> CommandResult(
                0,
                """{"plugins":[{"name":"demo","description":"Demo plugin","source":{"path":"${pluginDirectory.toString().replace('\\', '/')}"}}]}""",
                "",
            )
            command.containsAll(listOf("plugin", "marketplace", "list")) -> CommandResult(
                0,
                if (registered) """{"marketplaces":[{"name":"preview-marketplace","root":"${cache.toString().replace('\\', '/')}"}]}""" else """{"marketplaces":[]}""",
                "",
            )
            command.containsAll(listOf("plugin", "marketplace", "add")) -> {
                registered = true
                Files.createDirectories(pluginDirectory.resolve(".codex-plugin"))
                Files.createDirectories(pluginDirectory.resolve("skills/assist"))
                Files.writeString(pluginDirectory.resolve(".codex-plugin/plugin.json"), """{"name":"demo","description":"Demo plugin","skills":"./skills/","interface":{"category":"Tools","capabilities":["Read"]}}""")
                Files.writeString(pluginDirectory.resolve("README.md"), "# Demo\n")
                Files.writeString(pluginDirectory.resolve("skills/assist/SKILL.md"), "---\nname: assist\ndescription: Assist safely\n---\nInstructions\n")
                CommandResult(0, """{"name":"preview-marketplace"}""", "")
            }
                else -> CommandResult(0, "{}", "")
            }
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
