package com.snowball.silverwing.core

import kotlinx.serialization.SerializationException
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertFalse
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.zip.ZipInputStream
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.test.assertFailsWith

class ConfigStoreTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `system default stores runtime shards under silverwing without reading SILVERWING`() {
        val legacy = temporary.resolve("SILVERWING")
        Files.createDirectories(legacy)
        Files.writeString(legacy.resolve("config.json"), "{ malformed legacy config }")

        val paths = ApplicationPaths.systemDefault(temporary.toString())
        val config = ConfigStore(paths).load()

        assertEquals(temporary.resolve("silverwing"), paths.home)
        assertEquals(paths.home.resolve("config"), paths.config)
        assertEquals(paths.tasks.toAbsolutePath().normalize().toString(), config.taskRoot)
        assertTrue(Files.exists(legacy.resolve("config.json")))
    }

    @Test
    fun `missing configuration initializes all strict schema six shards`() {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths)

        val config = store.load()

        assertTrue(store.exists())
        assertTrue(Files.isDirectory(paths.tasks))
        assertEquals(listOf(DEFAULT_GROUP_NAME), config.groups.map { it.name })
        assertFalse(config.aiRequirementNamingEnabled)
        assertEquals(RequirementAiNamingModel.DEFAULT, config.aiRequirementNamingModel)
        assertEquals(null, config.meegleDefaultSprintProjectKey)
        assertEquals(null, config.commandProxyUrl)
        assertEquals(null, config.commandProxyNoProxy)
        assertEquals(null, config.commandProxyUsername)
        assertEquals(null, config.commandProxyPassword)
        assertTrue(config.commandProxyTargets.isEmpty())
        assertEquals(EXPECTED_SHARDS, shardNames(paths))
        EXPECTED_SHARDS.forEach { name ->
            assertTrue(Files.readString(paths.config.resolve(name)).contains("\"schema\": 6"), name)
        }
    }

    @Test
    fun `aggregate round trip preserves groups services and every config field`() {
        val paths = ApplicationPaths(temporary.resolve("round-trip"))
        val repository = RepositoryConfig(
            id = "repo-api",
            name = "api",
            rootPath = "D:/code/api",
            gitCommonDirectory = "D:/code/api/.git",
        )
        val expected = AppConfig(
            taskRoot = "D:/tasks",
            defaultBranchPrefix = "feature/pay-",
            repositories = listOf(repository),
            groups = listOf(
                GroupConfig(
                    id = "payments",
                    name = "支付",
                    tagEnabled = false,
                    services = listOf(GroupServiceConfig.standard("api", repository.id, "支付 API").copy(
                        masterBranch = "origin/develop",
                        testTagBaselineRef = "origin/qa",
                    )),
                ),
            ),
            theme = ThemePreference.DARK,
            tagEnabled = false,
            tagHistoryMaxGroups = 7,
            terminalExecutable = "wt.exe",
            developmentTools = listOf(DevelopmentToolConfig(DevelopmentToolType.VISUAL_STUDIO_CODE, "D:/tools/Code.exe")),
            defaultDevelopmentTool = DevelopmentToolType.VISUAL_STUDIO_CODE,
            allowTemporaryDevelopmentToolSelection = true,
            defaultWorkspaceToolIds = listOf("codex"),
            showTaskDetailGitActionGroup = true,
            showTaskDetailPathActionGroup = true,
            showWorkspaceGitActionGroup = true,
            showWorkspacePathActionGroup = true,
            showTaskAreaCopyIcons = false,
            showTaskAreaBranchCopyIcons = false,
            showTaskAreaRequirementCopyIcons = false,
            showTaskAreaProjectNameCopyIcons = false,
            blockedGitWriteBranches = listOf("main"),
            meegleProjects = listOf(MeegleProjectConfig("fixture-pay", "pay")),
            meegleDefaultSprintProjectKey = "fixture-pay",
            meegleExecutablePath = "D:/tools/meegle.exe",
            codexExecutablePath = "D:/tools/codex.exe",
            commandProxyUrl = "http://127.0.0.1:7890",
            commandProxyNoProxy = "localhost,*.internal",
            commandProxyUsername = "proxy-user",
            commandProxyPassword = "proxy-password",
            commandProxyTargets = setOf(CommandProxyTarget.CODEX, CommandProxyTarget.GIT),
            larkExecutablePath = "D:/tools/lark-cli.cmd",
            gitExecutablePath = "D:/tools/git.exe",
            genbuExecutablePath = "D:/tools/genbu.exe",
            genbuExecutableAutoDetected = true,
            requirementMaterialsRoot = "D:/materials",
            requirementMaterialsSubdirectory = "研发",
            codexPluginMarketplaceSources = listOf(
                CodexPluginMarketplaceSource("team-marketplace", "团队插件", "https://example.test/team/plugins.git", "main", "plugins"),
            ),
            skillSources = listOf(
                SkillSource("team-skills", "团队 Skills", "git@example.test:team/skills.git", "main", "skills"),
            ),
            aiRequirementNamingEnabled = true,
            aiRequirementNamingModel = "gpt-5.6-sol",
        )

        ConfigStore(paths).save(expected)

        assertEquals(expected, ConfigStore(paths).load())
        assertTrue(Files.readString(paths.config.resolve("layout.json")).contains("payments"))
        assertFalse(Files.readString(paths.config.resolve("layout.json")).contains("defaultBranchPrefix"))
        assertFalse(Files.readString(paths.config.resolve("layout.json")).contains("defaultWorkspaceToolIds"))
        assertTrue(Files.readString(paths.config.resolve("workspace.json")).contains("\"defaultBranchPrefix\": \"feature/pay-\""))
        assertTrue(Files.readString(paths.config.resolve("tools.json")).contains("\"defaultWorkspaceToolIds\": ["))
        assertTrue(Files.readString(paths.config.resolve("services.json")).contains("支付 API"))
        assertTrue(Files.readString(paths.config.resolve("services.json")).contains("\"masterBranch\": \"origin/develop\""))
        assertTrue(Files.readString(paths.config.resolve("services.json")).contains("\"testTagBaselineRef\": \"origin/qa\""))
        assertTrue(Files.readString(paths.config.resolve("services.json")).contains("\"masterBranch\": null"))
        assertFalse(Files.readString(paths.config.resolve("services.json")).contains("\"historyBaselineRef\""))
        assertFalse(Files.readString(paths.config.resolve("services.json")).contains("\"baseRef\""))
        assertFalse(Files.readString(paths.config.resolve("services.json")).contains("\"baseRemote\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("team-marketplace"))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"meegleDefaultSprintProjectKey\": \"fixture-pay\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"aiRequirementNamingEnabled\": true"))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"aiRequirementNamingModel\": \"gpt-5.6-sol\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"codexExecutablePath\": \"D:/tools/codex.exe\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"commandProxyUrl\": \"http://127.0.0.1:7890\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"commandProxyNoProxy\": \"localhost,*.internal\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"commandProxyUsername\": \"proxy-user\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"commandProxyPassword\": \"proxy-password\""))
        assertTrue(Files.readString(paths.config.resolve("integrations.json")).contains("\"commandProxyTargets\""))
    }

    @Test
    fun `existing integrations without a default Sprint project load as unspecified without rewrite`() {
        val paths = ApplicationPaths(temporary.resolve("legacy-sprint-default"))
        val store = ConfigStore(paths)
        val expected = AppConfig(meegleProjects = listOf(MeegleProjectConfig("fixture-alpha", "alpha")))
        store.save(expected)
        val integrations = paths.config.resolve("integrations.json")
        val original = Files.readString(integrations)
        val withoutDefault = original.lineSequence()
            .filterNot { it.contains("\"meegleDefaultSprintProjectKey\"") }
            .joinToString("\n")
        assertFalse(original == withoutDefault)
        Files.writeString(integrations, withoutDefault)

        assertEquals(null, AppConfig().meegleDefaultSprintProjectKey)
        assertEquals(expected, store.load())
        assertEquals(null, store.load().meegleDefaultSprintProjectKey)
        assertEquals(withoutDefault, Files.readString(integrations))
        assertTrue(store.backups().isEmpty())
    }

    @Test
    fun `default Sprint project updates only integrations and preserves all other settings`() {
        val paths = ApplicationPaths(temporary.resolve("sprint-default-only"))
        val store = ConfigStore(paths)
        val initial = AppConfig(
            meegleProjects = listOf(MeegleProjectConfig("fixture-alpha", "alpha")),
            meegleExecutablePath = "D:/fixture/meegle.exe",
            commandProxyUrl = "http://127.0.0.1:7890",
            commandProxyTargets = setOf(CommandProxyTarget.MEEGLE),
            theme = ThemePreference.DARK,
            defaultBranchPrefix = "fixture/",
        )
        store.save(initial)
        val before = shardBytes(paths)

        val selected = store.update { it.copy(meegleDefaultSprintProjectKey = "fixture-alpha") }

        assertEquals(initial.copy(meegleDefaultSprintProjectKey = "fixture-alpha"), selected)
        assertEquals(selected, ConfigStore(paths).load())
        EXPECTED_SHARDS.filterNot { it == "integrations.json" }.forEach { name ->
            assertTrue(before.getValue(name).contentEquals(Files.readAllBytes(paths.config.resolve(name))), name)
        }
        assertFalse(before.getValue("integrations.json").contentEquals(Files.readAllBytes(paths.config.resolve("integrations.json"))))
        assertEquals(initial, store.update { it.copy(meegleDefaultSprintProjectKey = null) })
        assertEquals(initial, ConfigStore(paths).load())
    }

    @Test
    fun `save writes only changed shards and archives complete prior configuration`() {
        val paths = ApplicationPaths(temporary.resolve("changed-shards"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/tasks"))
        val before = shardBytes(paths)

        store.save(store.load().copy(theme = ThemePreference.DARK))

        assertEquals(1, store.backups().size)
        EXPECTED_SHARDS.filterNot { it == "appearance.json" }.forEach { name ->
            assertTrue(before.getValue(name).contentEquals(Files.readAllBytes(paths.config.resolve(name))), name)
        }
        assertFalse(before.getValue("appearance.json").contentEquals(Files.readAllBytes(paths.config.resolve("appearance.json"))))
        assertEquals(EXPECTED_SHARDS, zipEntries(store.backups().single().path))
    }

    @Test
    fun `failed multi shard replacement restores the complete previous configuration`() {
        val paths = ApplicationPaths(temporary.resolve("rollback"))
        val initial = ConfigStore(paths)
        initial.save(AppConfig(taskRoot = "D:/before"))
        val before = shardBytes(paths)
        var replacementCount = 0
        val failing = ConfigStore(paths, replaceShard = { source, target ->
            assertEquals(paths.config.toAbsolutePath().normalize(), source.parent.toAbsolutePath().normalize())
            replacementCount++
            if (replacementCount == 2) error("simulated shard replacement failure")
            Files.move(source, target, StandardCopyOption.REPLACE_EXISTING)
        })

        assertFailsWith<IllegalStateException> {
            failing.save(initial.load().copy(taskRoot = "D:/after", theme = ThemePreference.DARK))
        }

        assertEquals(EXPECTED_SHARDS, shardNames(paths))
        before.forEach { (name, bytes) ->
            assertTrue(bytes.contentEquals(Files.readAllBytes(paths.config.resolve(name))), name)
        }
        assertEquals(initial.load(), failing.load())
    }

    @Test
    fun `strict shard schema unknown fields and missing shards are rejected without rewrite`() {
        val paths = ApplicationPaths(temporary.resolve("strict"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/tasks"))

        val tag = paths.config.resolve("tag.json")
        val original = Files.readString(tag)
        Files.writeString(tag, original.replace("\"schema\": 6", "\"schema\": 5"))
        assertFailsWith<UnsupportedConfigVersionException> { store.load() }
        assertEquals(original.replace("\"schema\": 6", "\"schema\": 5"), Files.readString(tag))

        Files.writeString(tag, original.replace("\n}", ",\n  \"unknown\": true\n}"))
        assertThrows(SerializationException::class.java) { store.load() }

        Files.write(tag, original.toByteArray())
        val integrations = paths.config.resolve("integrations.json")
        val originalIntegrations = Files.readString(integrations)
        val missingAiNamingField = originalIntegrations.replace(
            Regex("\\s*\\\"aiRequirementNamingEnabled\\\": false,\\r?\\n"),
            "",
        )
        Files.writeString(integrations, missingAiNamingField)
        assertThrows(SerializationException::class.java) { store.load() }
        assertEquals(missingAiNamingField, Files.readString(integrations))

        Files.writeString(integrations, originalIntegrations)
        val missingProxyFields = originalIntegrations.replace(
            Regex("\\s*\\\"commandProxyUrl\\\": null,\\r?\\n"),
            "",
        ).replace(
            Regex("\\s*\\\"commandProxyTargets\\\": \\[\\],\\r?\\n"),
            "",
        )
        Files.writeString(integrations, missingProxyFields)
        assertFailsWith<IllegalArgumentException> { store.load() }
        assertEquals(missingProxyFields, Files.readString(integrations))

        Files.writeString(integrations, originalIntegrations)
        val workspace = paths.config.resolve("workspace.json")
        val originalWorkspace = Files.readString(workspace)
        val missingBranchPrefix = originalWorkspace.replace(
            "\"defaultBranchPrefix\"",
            "\"missingBranchPrefix\"",
        )
        Files.writeString(workspace, missingBranchPrefix)
        assertFailsWith<IllegalArgumentException> { store.load() }
        assertEquals(missingBranchPrefix, Files.readString(workspace))

        Files.writeString(workspace, originalWorkspace)
        val tools = paths.config.resolve("tools.json")
        val originalTools = Files.readString(tools)
        val missingDefaultWorkspaceTools = originalTools.replace(
            "\"defaultWorkspaceToolIds\"",
            "\"missingDefaultWorkspaceToolIds\"",
        )
        Files.writeString(tools, missingDefaultWorkspaceTools)
        assertFailsWith<IllegalArgumentException> { store.load() }
        assertEquals(missingDefaultWorkspaceTools, Files.readString(tools))

        Files.writeString(tools, originalTools)
        val services = paths.config.resolve("services.json")
        val withService = store.load().copy(
            repositories = listOf(RepositoryConfig("repo", "仓库", "D:/repo", "D:/repo/.git")),
            groups = listOf(GroupConfig(
                id = "test",
                name = "测试",
                services = listOf(GroupServiceConfig.standard("service", "repo", "服务")),
            )),
        )
        store.save(withService)
        val serviceJson = Files.readString(services)
        val missingServiceBaseline = serviceJson.replace(Regex("\\s*\\\"masterBranch\\\": \\\"origin/master\\\",\\r?\\n"), "")
        Files.writeString(services, missingServiceBaseline)
        assertFailsWith<SerializationException> { store.load() }
        assertEquals(missingServiceBaseline, Files.readString(services))

        val missingModuleMaster = serviceJson.replace(Regex("\\s*\\\"masterBranch\\\": null,\\r?\\n"), "")
        Files.writeString(services, missingModuleMaster)
        assertFailsWith<SerializationException> { store.load() }
        assertEquals(missingModuleMaster, Files.readString(services))

        val missingTestBaseline = serviceJson.replace(Regex("\\s*\\\"testTagBaselineRef\\\": \\\"origin/release/test\\\",\\r?\\n"), "")
        Files.writeString(services, missingTestBaseline)
        assertFailsWith<SerializationException> { store.load() }
        assertEquals(missingTestBaseline, Files.readString(services))

        Files.writeString(services, serviceJson)
        Files.delete(paths.config.resolve("tools.json"))
        assertFailsWith<IllegalArgumentException> { store.load() }
    }

    @Test
    fun `export import and preview use a complete standard zip`() {
        val source = ConfigStore(ApplicationPaths(temporary.resolve("source")))
        val expected = AppConfig(
            taskRoot = "D:/tasks",
            requirementMaterialsRoot = "D:/materials",
            requirementMaterialsSubdirectory = "研发",
            codexPluginMarketplaceSources = listOf(
                CodexPluginMarketplaceSource("plugins", "Plugins", "https://example.test/plugins.git", marketplaceDirectory = ".agents/plugins"),
            ),
            skillSources = listOf(
                SkillSource("skills", "Skills", "git@example.test:team/skills.git", skillRoot = "skills"),
            ),
        )
        source.save(expected)
        val archive = source.exportTo(temporary.resolve("shared/config.zip"))
        val target = ConfigStore(ApplicationPaths(temporary.resolve("target")))

        assertEquals(EXPECTED_SHARDS, zipEntries(archive))
        assertTrue(target.previewImport(archive).changes.any { it.startsWith("任务路径") })
        assertEquals(expected, target.importFrom(archive))
        assertFailsWith<IllegalArgumentException> { source.exportTo(temporary.resolve("shared/config.json")) }
        assertFailsWith<IllegalArgumentException> { target.importFrom(temporary.resolve("shared/config.json")) }
    }

    @Test
    fun `schema five archives are rejected without modifying current shards`() {
        val paths = ApplicationPaths(temporary.resolve("archive-version"))
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = "D:/tasks"))
        val before = shardBytes(paths)
        val archive = store.exportTo(temporary.resolve("schema-six.zip"))
        val oldArchive = temporary.resolve("schema-five.zip")
        ZipInputStream(Files.newInputStream(archive)).use { input ->
            ZipOutputStream(Files.newOutputStream(oldArchive)).use { output ->
                while (true) {
                    val entry = input.nextEntry ?: break
                    val content = input.readAllBytes().toString(Charsets.UTF_8)
                    output.putNextEntry(ZipEntry(entry.name))
                    output.write(content.replace("\"schema\": 6", "\"schema\": 5").toByteArray())
                    output.closeEntry()
                    input.closeEntry()
                }
            }
        }
        assertFailsWith<UnsupportedConfigVersionException> { store.previewImport(oldArchive) }
        assertFailsWith<UnsupportedConfigVersionException> { store.importFrom(oldArchive) }
        before.forEach { (name, bytes) ->
            assertTrue(bytes.contentEquals(Files.readAllBytes(paths.config.resolve(name))), name)
        }
    }

    @Test
    fun `file snapshot exposes every shard without parsing`() {
        val paths = ApplicationPaths(temporary.resolve("snapshot"))
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val tag = paths.config.resolve("tag.json")
        Files.writeString(tag, "{ damaged")

        val snapshot = store.fileSnapshot()

        assertEquals(paths.config.toAbsolutePath().normalize(), snapshot.path)
        assertTrue(snapshot.exists)
        assertTrue(snapshot.content.orEmpty().contains("[tag.json]\n{ damaged"))
        assertEquals("{ damaged", Files.readString(tag))
    }

    @Test
    fun `save rejects an aggregate config from another release`() {
        val store = ConfigStore(ApplicationPaths(temporary.resolve("version")))

        assertFailsWith<UnsupportedConfigVersionException> {
            store.save(AppConfig(schemaVersion = "5.0.0"))
        }
        assertFalse(store.exists())
    }

    private fun shardNames(paths: ApplicationPaths): List<String> = Files.list(paths.config).use { entries ->
        entries.map { it.fileName.toString() }.sorted().toList()
    }

    private fun shardBytes(paths: ApplicationPaths): Map<String, ByteArray> =
        EXPECTED_SHARDS.associateWith { Files.readAllBytes(paths.config.resolve(it)) }

    private fun zipEntries(path: Path): List<String> = ZipInputStream(Files.newInputStream(path)).use { zip ->
        buildList {
            while (true) {
                val entry = zip.nextEntry ?: break
                add(entry.name)
                zip.closeEntry()
            }
        }.sorted()
    }

    private companion object {
        val EXPECTED_SHARDS = listOf(
            "appearance.json",
            "git.json",
            "integrations.json",
            "layout.json",
            "services.json",
            "tag.json",
            "tools.json",
            "workspace.json",
        )
    }
}
