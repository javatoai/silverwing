package com.snowball.silverwing.core

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith

class ConfigurationInteractionPolicyTest {
    @Test
    fun `group prefix follows untouched branch and preserves manual edits`() {
        assertEquals(
            "fix/",
            GroupBranchPrefixPolicy.onGroupChanged("feature/", "feature/", manuallyEdited = false, nextPrefix = "fix/"),
        )
        assertEquals(
            "custom/TASK-1",
            GroupBranchPrefixPolicy.onGroupChanged("custom/TASK-1", "feature/", manuallyEdited = true, nextPrefix = "fix/"),
        )
        assertEquals(
            "fix/",
            GroupBranchPrefixPolicy.onGroupChanged("feature/", "feature/", manuallyEdited = true, nextPrefix = "fix/"),
        )
    }
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `remote branch reference parses the first slash and round trips`() {
        val ref = RemoteBranchRef.parse("origin/release/test")

        assertEquals("origin", ref.remote)
        assertEquals("release/test", ref.branch)
        assertEquals("origin/release/test", ref.toString())
    }

    @Test
    fun `remote branch reference rejects malformed git names`() {
        listOf("origin", "origin/", "origin/a b", "origin/a..b", "origin/a.lock").forEach { value ->
            assertFailsWith<IllegalArgumentException>(value) { RemoteBranchRef.parse(value) }
        }
    }

    @Test
    fun `split configuration round trip persists module Tag references`() {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths)
        val repository = RepositoryConfig("repo", "repo", "C:/repo", "C:/repo/.git", "https://example.test/repo.git")
        val service = GroupServiceConfig(
            id = "service",
            repositoryId = repository.id,
            displayName = "Repo",
            modules = listOf(ServiceModuleConfig("main", tagTargetRef = "upstream/release/test")),
        )
        val expected = AppConfig(
            repositories = listOf(repository),
            groups = listOf(GroupConfig("g", "G", services = listOf(service))),
        )

        store.save(expected)

        assertEquals(CURRENT_APP_CONFIG_SCHEMA_VERSION, expected.schemaVersion)
        assertEquals(expected, store.load())
        val json = listOf("layout.json", "services.json", "tag.json")
            .joinToString("\n") { Files.readString(paths.config.resolve(it)) }
        // The Chinese display name changes must not rename the persisted switch or fields.
        assertTrue("\"tagEnabled\"" in json)
        assertTrue("\"tagTargetRef\"" in json)
        assertTrue("\"tagMessagePrefix\"" in json)
        assertTrue("\"schema\": 2" in json)
        assertFalse("uatRemote" in json)
        assertFalse("cloneUatBranch" in json)
    }

    @Test
    fun `Tag navigation requires both global and group gates`() {
        val standard = GroupServiceConfig.standard("standard", "repo", "Standard")
        val clone = GroupServiceConfig(
            id = "clone",
            repositoryId = "repo2",
            displayName = "Clone",
            modules = listOf(ServiceModuleConfig("clone", strategy = WorkspaceStrategy.INDEPENDENT_CLONE, baseRef = "origin/main")),
        )
        val repositories = listOf(
            RepositoryConfig("repo", "repo", "C:/repo", "C:/repo/.git"),
            RepositoryConfig("repo2", "repo2", "C:/repo2", "C:/repo2/.git", "https://example.test/repo2.git"),
        )
        val base = AppConfig(repositories = repositories, groups = listOf(GroupConfig("g", "G", services = listOf(standard, clone))))

        assertTrue(TagNavigationPolicy.isVisible(base))
        assertFalse(TagNavigationPolicy.isVisible(base.copy(tagEnabled = false)))
        assertFalse(TagNavigationPolicy.isVisible(base.copy(groups = listOf(base.groups.single().copy(tagEnabled = false)))))
        val childrenOff = base.copy(groups = listOf(base.groups.single().copy(services = listOf(
            standard.copy(modules = standard.modules.map { it.copy(tagEnabled = false) }),
            clone.copy(modules = clone.modules.map { it.copy(tagEnabled = false) }),
        ))))
        assertTrue(TagNavigationPolicy.isVisible(childrenOff))
        assertTrue(TagNavigationPolicy.isVisible(childrenOff.copy(groups = listOf(childrenOff.groups.single().copy(
            services = listOf(standard.copy(modules = standard.modules.map { it.copy(tagEnabled = false) }), clone.copy(modules = clone.modules.map { it.copy(tagEnabled = true) })),
        )))))
    }

    @Test
    fun `module display name falls back by module count`() {
        assertEquals("Orders", ModuleDisplayNaming.resolve("", "Orders", "origin/master", 1))
        assertEquals("Orders", ModuleDisplayNaming.resolve("default", "Orders", "origin/master", 1))
        assertEquals("test", ModuleDisplayNaming.resolve("", "Orders", "origin/release/test", 2))
        assertEquals("用户覆盖", ModuleDisplayNaming.resolve(" 用户覆盖 ", "Orders", "origin/master", 2))
    }

    @Test
    fun `bootstrap example is real serializable configuration`() {
        val json = Json { prettyPrint = true; encodeDefaults = true }
        val encoded = json.encodeToString(BootstrapPresets.example())
        val decoded = json.decodeFromString<BootstrapConfig>(encoded)

        assertTrue(decoded.copyRules.isNotEmpty())
        assertTrue(decoded.commands.isNotEmpty())
    }
}
