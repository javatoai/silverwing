package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Files
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.*
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class ServiceSaveFeedbackTest {
    @Test fun `service save reports failure rejects duplicate submission and retries original draft`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val paths = ApplicationPaths(Files.createTempDirectory("silverwing-service-save-test"))
        val store = ConfigStore(paths)
        val repository = RepositoryConfig("repo", "Repo", paths.home.resolve("repo").toString(), paths.home.resolve("repo/.git").toString(), "https://example.invalid/repo.git")
        val service = GroupServiceConfig.standard("service", "repo", "Service")
        var persisted = AppConfig(repositories = listOf(repository), groups = listOf(GroupConfig("group", "Group", services = listOf(service))))
        store.save(persisted)
        var fail = true
        val configurations = object : ConfigurationRepository {
            override fun load() = persisted
            override fun save(config: AppConfig) { if (fail) error("disk unavailable"); persisted = config }
        }
        val app = DesktopApplication(paths = paths, configStore = store, groupConfigurations = GroupConfigurationService(configurations),
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }, ioDispatcher = dispatcher)
        val errors = mutableListOf<String>()
        var completed = 0
        val draft = service.copy(displayName = "Edited")
        try {
            assertTrue(app.settingsController.updateService("group", draft, { errors += it.message.orEmpty() }, { completed++ }))
            assertFalse(app.settingsController.updateService("group", draft, { errors += it.message.orEmpty() }, { completed++ }))
            advanceUntilIdle()
            assertEquals(0, completed)
            assertTrue(errors.any { it == "disk unavailable" })
            assertEquals(service, persisted.groups.single().services.single())
            fail = false
            assertTrue(app.settingsController.updateService("group", draft, { errors += it.message.orEmpty() }, { completed++ }))
            advanceUntilIdle()
            assertEquals(1, completed)
            assertEquals(draft, app.config.groups.single().services.single())
        } finally { app.close(); Dispatchers.resetMain() }
    }
}
