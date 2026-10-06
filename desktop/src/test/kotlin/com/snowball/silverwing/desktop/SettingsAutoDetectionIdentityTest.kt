package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.ArrayDeque
import kotlin.coroutines.CoroutineContext
import kotlin.test.assertEquals
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

class SettingsAutoDetectionIdentityTest {
    @TempDir lateinit var temporary: Path

    @Test
    fun `Genbu focus detection preserves newer unrelated settings`() =
        assertRefreshPreservesNewerConfig(Refresh.GENBU_FOCUS, manualPath = false)

    @Test
    fun `Genbu focus detection preserves a newer manual path`() =
        assertRefreshPreservesNewerConfig(Refresh.GENBU_FOCUS, manualPath = true)

    @Test
    fun `Genbu status detection preserves newer unrelated settings`() =
        assertRefreshPreservesNewerConfig(Refresh.GENBU_STATUS, manualPath = false)

    @Test
    fun `Genbu status detection preserves a newer manual path`() =
        assertRefreshPreservesNewerConfig(Refresh.GENBU_STATUS, manualPath = true)

    @Test
    fun `Git detection preserves newer unrelated settings`() =
        assertRefreshPreservesNewerConfig(Refresh.GIT, manualPath = false)

    @Test
    fun `Git detection preserves a newer manual path`() =
        assertRefreshPreservesNewerConfig(Refresh.GIT, manualPath = true)

    @Test
    fun `Meegle detection preserves newer unrelated settings`() =
        assertRefreshPreservesNewerConfig(Refresh.MEEGLE, manualPath = false)

    @Test
    fun `Meegle detection preserves a newer manual path`() =
        assertRefreshPreservesNewerConfig(Refresh.MEEGLE, manualPath = true)

    private fun assertRefreshPreservesNewerConfig(refresh: Refresh, manualPath: Boolean) {
        val detected = executableFixture("detected.exe")
        val manual = executableFixture("manual.exe")
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths)
        val initial = AppConfig()
        store.save(initial)
        val session = AppSessionStore(initial, emptyList())
        val ui = QueuedDispatcher()
        val io = QueuedDispatcher()
        val failures = mutableListOf<Throwable>()
        val scope = CoroutineScope(SupervisorJob() + ui + CoroutineExceptionHandler { _, error -> failures += error })
        val applied = mutableListOf<AppConfig>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult =
                when {
                    command.last() == "--version" -> CommandResult(0, "fixture 1.0\n", "")
                    command.drop(1) == listOf("config", "--global", "--show-origin", "--list") -> CommandResult(0, "", "")
                    else -> error("Unexpected fixture command: $command")
                }
        }
        val git = object : GitExecutable {
            override fun resolve() = detected
            override fun source() = GitCommandSource.PROBED
        }
        val operations = OperationRunner(OperationCoordinator(), scope, io)
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
                override fun status() = MeegleCliStatus(installed = true)
                override fun logout() = Unit
                override fun beginDeviceCodeLogin(host: String) = error("No login expected")
                override fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge) = error("No login expected")
            },
            meegleExecutable = object : MeegleExecutable {
                override fun resolve() = detected
                override fun source() = MeegleCommandSource.PROBED
            },
            gitExecutable = git,
            genbuExecutable = object : GenbuExecutable {
                override fun resolve() = detected
                override fun detect() = detected
                override fun source() = GenbuCommandSource.PROBED
            },
            cliVersionRunner = runner,
            localGitInspector = LocalGitEnvironmentInspector(runner, git),
            scope = scope,
            ioDispatcher = io,
            operations = operations,
            settingsOperations = operations,
            meegleOperations = operations,
            applyConfig = { applied += it; session.config = it },
            reloadTasks = {},
            showError = { failures += it },
            showStatus = {},
        )
        try {
            refresh.start(controller)
            ui.runAll()
            io.runAll()

            // The real controller has saved its detection snapshot, but its UI callback is pending.
            assertEquals(detected, refresh.path(store.load()))
            assertNull(refresh.path(session.config))
            assertTrue(applied.isEmpty())

            // Another completed settings update publishes a newer snapshot before that callback.
            val change: (AppConfig) -> AppConfig = { config ->
                val changed = config.copy(theme = ThemePreference.DARK)
                if (manualPath) refresh.withManualPath(changed, manual) else changed
            }
            session.config = change(session.config)
            val expected = store.update(change)
            ui.runAll()

            assertTrue(failures.isEmpty(), failures.joinToString { it.toString() })
            refresh.assertCompleted(controller)
            assertEquals(expected, session.config)
            assertEquals(expected, store.load())
            assertEquals(if (manualPath) 0 else 1, applied.size)
            if (manualPath && refresh in setOf(Refresh.GENBU_FOCUS, Refresh.GENBU_STATUS)) {
                assertEquals(manual to GenbuCommandSource.CONFIGURED, controller.genbuCommandResolution(),
                    "A pending detection must not put the previous command back in the Genbu header")
            }
        } finally {
            scope.cancel()
            io.runAll()
            ui.runAll()
        }
    }

    private fun executableFixture(name: String): String {
        val path = Files.createFile(temporary.resolve(name)).toAbsolutePath()
        assertTrue(path.toFile().setExecutable(true))
        return path.toString()
    }

    private enum class Refresh {
        GENBU_FOCUS, GENBU_STATUS, GIT, MEEGLE;

        fun start(controller: SettingsController) = when (this) {
            GENBU_FOCUS -> controller.refreshGenbuCommandResolution()
            GENBU_STATUS -> controller.refreshGenbu()
            GIT -> controller.refreshLocalGit()
            MEEGLE -> controller.refreshMeegleStatus()
        }

        fun path(config: AppConfig): String? = when (this) {
            GENBU_FOCUS, GENBU_STATUS -> config.genbuExecutablePath
            GIT -> config.gitExecutablePath
            MEEGLE -> config.meegleExecutablePath
        }

        fun withManualPath(config: AppConfig, path: String): AppConfig = when (this) {
            GENBU_FOCUS, GENBU_STATUS -> config.copy(genbuExecutablePath = path, genbuExecutableAutoDetected = false)
            GIT -> config.copy(gitExecutablePath = path)
            MEEGLE -> config.copy(meegleExecutablePath = path)
        }

        fun assertCompleted(controller: SettingsController) {
            when (this) {
                GENBU_FOCUS -> assertEquals(SettingsSaveState.SAVED, controller.saveState("genbu"))
                GENBU_STATUS -> assertIs<GenbuSettingsState.Loaded>(controller.state.genbu)
                GIT -> assertIs<LocalGitSettingsState.Loaded>(controller.state.localGit)
                MEEGLE -> assertIs<MeegleCliState.Ready>(controller.state.meegleCli)
            }
        }
    }

    /** Keeps the IO result pending independently of the UI queue without threads or sleeps. */
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
