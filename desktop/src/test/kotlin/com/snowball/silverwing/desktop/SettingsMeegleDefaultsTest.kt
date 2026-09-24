package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.time.Duration
import java.util.ArrayDeque
import kotlin.coroutines.CoroutineContext
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir

class SettingsMeegleDefaultsTest {
    @TempDir lateinit var temporary: Path

    @Test
    fun `default selection publishes only after save and preserves newer unrelated settings`() {
        fixture().use { fixture ->
            val initial = fixture.session.config
            val newer = fixture.store.update { it.copy(defaultBranchPrefix = "fixture/newer-") }

            assertTrue(fixture.controller.updateMeegleDefaultSprintProjectKey("fixture-alpha"))
            assertEquals(SettingsSaveState.SAVING, fixture.controller.saveState("feishu"))
            assertEquals(initial, fixture.session.config)
            fixture.ui.runAll()
            fixture.io.runAll()
            val expected = newer.copy(meegleDefaultSprintProjectKey = "fixture-alpha")
            assertEquals(expected, fixture.store.load())
            assertEquals(initial, fixture.session.config)
            fixture.ui.runAll()

            fixture.assertConfig(expected)
            assertEquals(SettingsSaveState.SAVED, fixture.controller.saveState("feishu"))
            assertTrue(fixture.controller.updateMeegleDefaultSprintProjectKey(null))
            fixture.complete()
            fixture.assertConfig(newer)
            assertEquals(SettingsSaveState.SAVED, fixture.controller.saveState("feishu"))
        }
    }

    @Test
    fun `adding and deleting other projects preserve default while deleting default clears it atomically`() {
        fixture("fixture-alpha").use { fixture ->
            val initial = fixture.session.config
            val expanded = initial.copy(meegleProjects = initial.meegleProjects + MeegleProjectConfig("fixture-gamma", "gamma"))
            assertTrue(fixture.controller.updateMeegleProjects(expanded.meegleProjects))
            fixture.complete()
            fixture.assertConfig(expanded)

            val withoutOther = expanded.copy(meegleProjects = expanded.meegleProjects.filterNot { it.projectKey == "fixture-beta" })
            assertTrue(fixture.controller.updateMeegleProjects(withoutOther.meegleProjects))
            fixture.complete()
            fixture.assertConfig(withoutOther)

            val withoutDefault = withoutOther.copy(
                meegleProjects = withoutOther.meegleProjects.filterNot { it.projectKey == "fixture-alpha" },
                meegleDefaultSprintProjectKey = null,
            )
            val backupsBefore = fixture.store.backups().size
            assertTrue(fixture.controller.updateMeegleProjects(withoutDefault.meegleProjects))
            fixture.complete()
            fixture.assertConfig(withoutDefault)
            assertEquals(backupsBefore + 1, fixture.store.backups().size)
            assertEquals(SettingsSaveState.SAVED, fixture.controller.saveState("feishu"))
        }
    }

    @Test
    fun `unknown project keys and Sprint IDs cannot be saved as the default`() {
        fixture("fixture-alpha").use { fixture ->
            val initial = fixture.session.config
            listOf("fixture-unknown", "", "987654321").forEach { key ->
                var failure: Throwable? = null
                assertTrue(fixture.controller.updateMeegleDefaultSprintProjectKey(key) { failure = it })
                fixture.complete()
                assertIs<IllegalArgumentException>(failure)
                fixture.assertConfig(initial)
                assertEquals(SettingsSaveState.FAILED, fixture.controller.saveState("feishu"))
            }
            assertTrue(fixture.store.backups().isEmpty())
        }
    }

    @Test
    fun `default validation uses current persisted projects instead of stale session projects`() {
        fixture().use { fixture ->
            val initial = fixture.session.config
            val newer = fixture.store.update { config ->
                config.copy(meegleProjects = config.meegleProjects.filterNot { it.projectKey == "fixture-alpha" })
            }
            var failure: Throwable? = null

            assertTrue(fixture.controller.updateMeegleDefaultSprintProjectKey("fixture-alpha") { failure = it })
            fixture.complete()

            assertIs<IllegalArgumentException>(failure)
            assertEquals(newer, fixture.store.load())
            assertEquals(initial, fixture.session.config)
            assertEquals(SettingsSaveState.FAILED, fixture.controller.saveState("feishu"))
        }
    }

    @Test
    fun `failed default save and project deletion leave disk and visible config unchanged`() {
        fixture("fixture-alpha").use { fixture ->
            val initial = fixture.session.config
            val integrations = fixture.paths.config.resolve("integrations.json")
            val before = Files.readAllBytes(integrations)
            var failure: Throwable? = null
            fixture.failWrites = true

            assertTrue(fixture.controller.updateMeegleDefaultSprintProjectKey("fixture-beta") { failure = it })
            fixture.complete()
            assertIs<IllegalStateException>(failure)
            fixture.assertConfig(initial)
            assertTrue(before.contentEquals(Files.readAllBytes(integrations)))
            assertEquals(SettingsSaveState.FAILED, fixture.controller.saveState("feishu"))

            failure = null
            assertTrue(fixture.controller.updateMeegleProjects(emptyList()) { failure = it })
            fixture.complete()
            assertIs<IllegalStateException>(failure)
            fixture.assertConfig(initial)
            assertTrue(before.contentEquals(Files.readAllBytes(integrations)))
            assertEquals(SettingsSaveState.FAILED, fixture.controller.saveState("feishu"))
        }
    }

    @Test
    fun `busy settings runner rejects both saves and reports failure without changing config`() {
        fixture("fixture-alpha").use { fixture ->
            val initial = fixture.session.config
            assertTrue(fixture.coordinator.begin("fixture busy operation", false))
            var failure: Throwable? = null

            assertFalse(fixture.controller.updateMeegleDefaultSprintProjectKey("fixture-beta") { failure = it })
            assertIs<IllegalStateException>(failure)
            assertEquals(SettingsSaveState.FAILED, fixture.controller.saveState("feishu"))
            failure = null
            assertFalse(fixture.controller.updateMeegleProjects(emptyList()) { failure = it })
            assertIs<IllegalStateException>(failure)
            fixture.complete()

            fixture.assertConfig(initial)
            assertEquals(SettingsSaveState.FAILED, fixture.controller.saveState("feishu"))
            assertTrue(fixture.store.backups().isEmpty())
            assertNull(fixture.coordinator.statusMessage)
        }
    }

    private fun fixture(defaultProjectKey: String? = null) = Fixture(
        ApplicationPaths(temporary.resolve("home")),
        AppConfig(
            meegleProjects = listOf(MeegleProjectConfig("fixture-alpha", "alpha"), MeegleProjectConfig("fixture-beta", "beta")),
            meegleDefaultSprintProjectKey = defaultProjectKey,
            meegleExecutablePath = "D:/fixture/meegle.exe",
            commandProxyUrl = "http://127.0.0.1:7890",
            commandProxyTargets = setOf(CommandProxyTarget.MEEGLE),
            theme = ThemePreference.DARK,
        ),
    )

    private class Fixture(val paths: ApplicationPaths, initial: AppConfig) : AutoCloseable {
        var failWrites = false
        val store = ConfigStore(paths, replaceShard = { source, target ->
            check(!failWrites) { "fixture save failed" }
            Files.move(source, target, StandardCopyOption.REPLACE_EXISTING)
        }).apply { save(initial) }
        val session = AppSessionStore(initial, emptyList())
        val ui = QueuedDispatcher()
        val io = QueuedDispatcher()
        private val unexpectedFailures = mutableListOf<Throwable>()
        private val scope = CoroutineScope(SupervisorJob() + ui + CoroutineExceptionHandler { _, error -> unexpectedFailures += error })
        val coordinator = OperationCoordinator()
        private val operations = OperationRunner(coordinator, scope, io)
        private val commandRunner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult =
                error("No commands expected: $command")
        }
        val controller = SettingsController(
            session = session,
            configStore = store,
            groups = GroupConfigurationService(store),
            taskRootMigrations = TaskRootMigrationService(configStore = store, paths = paths),
            pathPicker = object : NativePathPicker {
                override suspend fun pickDirectory(initialPath: String?) = null
                override suspend fun pickDirectories(initialPath: String?): List<String>? = null
                override suspend fun pickFile(initialPath: String?, extensions: List<String>) = null
                override suspend fun pickApplication(initialPath: String?) = null
            },
            branchCatalog = object : RemoteBranchCatalog {
                override fun list(repository: Path, remote: String): List<String> = error("No branch lookup expected")
            },
            meegleProjectCatalog = MeegleProjectCatalog { error("No project lookup expected") },
            meegleCliService = object : MeegleCliService {
                override fun status(): MeegleCliStatus = error("No status lookup expected")
                override fun logout(): Unit = error("No logout expected")
                override fun beginDeviceCodeLogin(host: String): MeegleDeviceCodeChallenge = error("No login expected")
                override fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge): MeegleDeviceCodeLoginResult = error("No login expected")
            },
            cliVersionRunner = commandRunner,
            localGitInspector = LocalGitEnvironmentInspector(commandRunner, GitExecutable.pathFallback()),
            scope = scope,
            ioDispatcher = io,
            operations = operations,
            settingsOperations = operations,
            meegleOperations = operations,
            applyConfig = { session.config = it },
            reloadTasks = {},
            showError = { unexpectedFailures += it },
            showStatus = {},
        )

        fun complete() {
            ui.runAll()
            io.runAll()
            ui.runAll()
        }

        fun assertConfig(expected: AppConfig) {
            assertEquals(expected, store.load())
            assertEquals(expected, session.config)
            assertEquals(expected, controller.state.config)
        }

        override fun close() {
            scope.cancel()
            complete()
            assertTrue(unexpectedFailures.isEmpty(), unexpectedFailures.joinToString())
        }
    }

    private class QueuedDispatcher : CoroutineDispatcher() {
        private val queue = ArrayDeque<Runnable>()

        override fun dispatch(context: CoroutineContext, block: Runnable) {
            queue.addLast(block)
        }

        fun runAll() {
            while (queue.isNotEmpty()) queue.removeFirst().run()
        }
    }
}
