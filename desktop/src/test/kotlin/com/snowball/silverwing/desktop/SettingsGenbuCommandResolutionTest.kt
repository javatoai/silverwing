@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class SettingsGenbuCommandResolutionTest {
    @TempDir lateinit var temporary: Path

    @Test fun `saving a manual Genbu path updates displayed command without refocusing the window`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val old = executable("old-genbu.exe")
        val updated = executable("new-genbu.exe")
        val configured = AtomicReference<String?>(old)
        val calls = mutableListOf<List<String>>()
        val runner = object : StreamingCommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                calls.add(command)
                assertEquals(listOf("--version"), command.drop(1))
                return CommandResult(0, "genbu 1.0", "")
            }
            override fun runStreaming(command: List<String>, workingDirectory: Path?, timeout: Duration,
                environment: Map<String, String>, onOutput: (CommandOutputLine) -> Unit): CommandResult =
                run(command, workingDirectory, timeout, environment)
        }
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths).also { it.save(AppConfig(genbuExecutablePath = old, aiRequirementNamingEnabled = false)) }
        try {
            DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher,
                genbuExecutablePath = configured,
                genbuExecutable = ConfiguredGenbuExecutable(configured::get, runner, bundledDirectories = { emptyList() }),
                // This fixture records Genbu commands only; startup Meegle checks are unrelated.
                participatedWorkItemsSource = object : ParticipatedWorkItemsSource {
                    override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?,
                        defaultSprintProjectKey: String?): ParticipatedWorkItemsResult = error("Unexpected requirement list read")
                    override fun loadBody(item: ParticipatedWorkItem): String? = error("Unexpected requirement body read")
                },
                processCommandRunner = runner,
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { app ->
                assertEquals(old to GenbuCommandSource.CONFIGURED, app.genbuCommandResolution())
                assertTrue(app.updateGenbuExecutablePath(updated))
                runCurrent()
                assertEquals(updated, store.load().genbuExecutablePath)
                assertEquals(updated to GenbuCommandSource.CONFIGURED, app.genbuCommandResolution())
                assertEquals(updated, assertIs<GenbuSettingsState.Loaded>(app.genbuSettingsState).command)
                assertEquals(listOf(listOf(updated, "--version")), calls)
                assertNull(app.errorMessage)
            }
        } finally { Dispatchers.resetMain() }
    }

    private fun executable(name: String): String = Files.createFile(temporary.resolve(name)).also {
        assertTrue(it.toFile().setExecutable(true))
    }.toAbsolutePath().toString()
}
