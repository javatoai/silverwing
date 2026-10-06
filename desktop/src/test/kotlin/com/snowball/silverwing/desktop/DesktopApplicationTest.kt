package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.DevelopmentToolAutoDetectionResult
import com.snowball.silverwing.core.DevelopmentToolConfig
import com.snowball.silverwing.core.DevelopmentToolStartupDetection
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.CommandResult
import com.snowball.silverwing.core.CommandRunner
import com.snowball.silverwing.core.CommandOutputLine
import com.snowball.silverwing.core.ConfiguredGitExecutable
import com.snowball.silverwing.core.ConfiguredGenbuExecutable
import com.snowball.silverwing.core.ConfiguredMeegleExecutable
import com.snowball.silverwing.core.CodexCommandSource
import com.snowball.silverwing.core.GenbuCommandSource
import com.snowball.silverwing.core.GitCommandSource
import com.snowball.silverwing.core.LocalGitEnvironmentInspector
import com.snowball.silverwing.core.ManifestStore
import com.snowball.silverwing.core.MeegleCliService
import com.snowball.silverwing.core.MeegleCliStatus
import com.snowball.silverwing.core.MeegleCommandSource
import com.snowball.silverwing.core.MeegleDeviceCodeChallenge
import com.snowball.silverwing.core.MeegleDeviceCodeLoginResult
import com.snowball.silverwing.core.MeegleProjectCatalog
import com.snowball.silverwing.core.RepositoryConfig
import com.snowball.silverwing.core.RequirementMaterialsDirectory
import com.snowball.silverwing.core.RequirementMaterialsStatus
import com.snowball.silverwing.core.RepositoryInspector
import com.snowball.silverwing.core.RemoteBranchCatalog
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.GroupConfig
import com.snowball.silverwing.core.ServiceModuleConfig
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.RequirementMetadataProvider
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskWorkspaceContext
import com.snowball.silverwing.core.TaskWorkspaceToolAvailability
import com.snowball.silverwing.core.TaskWorkspaceToolDescriptor
import com.snowball.silverwing.core.TaskWorkspaceToolLauncher
import com.snowball.silverwing.core.TaskWorkspaceToolRegistry
import com.snowball.silverwing.core.StreamingCommandRunner
import com.snowball.silverwing.core.WorkspaceHealth
import com.snowball.silverwing.core.TaskLifecycleStatus
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContains
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlin.test.assertFalse

class DesktopApplicationTest {
    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `restoring a development tool to automatic replaces only that manual path`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-development-tool-reset")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        val manualIdea = root.resolve("manual/idea.exe").toAbsolutePath().normalize().toString()
        val detectedIdea = root.resolve("detected/idea.exe").toAbsolutePath().normalize().toString()
        val code = root.resolve("Code.exe").toAbsolutePath().normalize().toString()
        store.save(
            AppConfig(
                developmentTools = listOf(
                    DevelopmentToolConfig(DevelopmentToolType.INTELLIJ_IDEA, manualIdea),
                    DevelopmentToolConfig(DevelopmentToolType.VISUAL_STUDIO_CODE, code),
                ),
            ),
        )
        var invocation = 0
        val detection = DevelopmentToolStartupDetection { initial ->
            invocation++
            if (invocation == 1) {
                DevelopmentToolAutoDetectionResult(initial, emptySet())
            } else {
                val updated = store.update { current ->
                    if (current.developmentTools.any { it.type == DevelopmentToolType.INTELLIJ_IDEA }) current
                    else current.copy(
                        developmentTools = current.developmentTools +
                            DevelopmentToolConfig(DevelopmentToolType.INTELLIJ_IDEA, detectedIdea),
                    )
                }
                DevelopmentToolAutoDetectionResult(updated, setOf(DevelopmentToolType.INTELLIJ_IDEA))
            }
        }

        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            developmentToolStartupDetection = detection,
            ioDispatcher = dispatcher,
        )
        try {
            advanceUntilIdle()
            controller.resetDevelopmentToolToAutomatic(DevelopmentToolType.INTELLIJ_IDEA)
            advanceUntilIdle()

            assertEquals(
                detectedIdea,
                controller.config.developmentTools.single { it.type == DevelopmentToolType.INTELLIJ_IDEA }.path,
            )
            assertEquals(
                code,
                controller.config.developmentTools.single { it.type == DevelopmentToolType.VISUAL_STUDIO_CODE }.path,
            )
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `development tool detection starts asynchronously and refreshes desktop configuration`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-development-tool-startup")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val code = root.resolve("Visual Studio Code/Code.exe").toAbsolutePath().normalize().toString()
        var invoked = false
        val detection = DevelopmentToolStartupDetection { initial ->
            invoked = true
            val updated = initial.copy(
                developmentTools = initial.developmentTools +
                    DevelopmentToolConfig(DevelopmentToolType.VISUAL_STUDIO_CODE, code),
            )
            DevelopmentToolAutoDetectionResult(updated, setOf(DevelopmentToolType.VISUAL_STUDIO_CODE))
        }

        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            developmentToolStartupDetection = detection,
            ioDispatcher = dispatcher,
        )
        try {
            assertFalse(invoked)
            assertTrue(controller.config.developmentTools.isEmpty())

            advanceUntilIdle()

            assertTrue(invoked)
            assertEquals(code, controller.config.developmentTools.single().path)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `development tool detection failure remains silent`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-development-tool-failure")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { error("probe failed") },
            ioDispatcher = dispatcher,
        )
        try {
            advanceUntilIdle()

            assertEquals(null, controller.errorMessage)
            assertTrue(controller.config.developmentTools.isEmpty())
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `Genbu refresh persists only the detected executable and exposes version status`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-genbu-settings")
        val paths = ApplicationPaths(root.resolve("home"))
        val detected = Files.createFile(root.resolve(if (System.getProperty("os.name").startsWith("Windows")) "genbu.exe" else "genbu"))
            .toAbsolutePath()
        detected.toFile().setExecutable(true)
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val configuredPath = AtomicReference<String?>(null)
        val runner = RecordingCommandRunner(CommandResult(0, "$detected\n", ""))
        val executable = ConfiguredGenbuExecutable(
            configuredPath::get,
            runner,
            bundledDirectories = { emptyList() },
        )
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            processCommandRunner = runner,
            genbuExecutablePath = configuredPath,
            genbuExecutable = executable,
            cliVersionRunner = runner,
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshGenbu()
            advanceUntilIdle()

            val persisted = store.load()
            assertEquals(detected.toString(), persisted.genbuExecutablePath)
            val loaded = assertIs<GenbuSettingsState.Loaded>(controller.genbuSettingsState)
            assertEquals(detected.toString(), loaded.command)
            assertEquals(GenbuCommandSource.PROBED, loaded.source)
            assertTrue(loaded.version?.succeeded == true, "Genbu version probe did not succeed: $loaded")
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @Test
    fun `configured Meegle executable is available after application initialization`() {
        val root = Files.createTempDirectory("SILVERWING-meegle-executable")
        val paths = ApplicationPaths(root.resolve("home"))
        val executable = Files.createFile(root.resolve("meegle.cmd")).toAbsolutePath().toString()
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = executable))

        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            val resolution = controller.meegleCommandResolution()
            assertEquals(executable, resolution.first)
            assertEquals(MeegleCommandSource.CONFIGURED, resolution.second)
        } finally {
            controller.close()
        }
    }

    @Test
    fun `configured Codex executable is available after application initialization`() {
        val root = Files.createTempDirectory("SILVERWING-codex-executable")
        val paths = ApplicationPaths(root.resolve("home"))
        val executable = Files.createFile(root.resolve("codex.exe")).toAbsolutePath().toString()
        val store = ConfigStore(paths)
        store.save(AppConfig(codexExecutablePath = executable))

        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            val resolution = controller.codexCommandResolution()
            assertEquals(executable, resolution.first)
            assertEquals(CodexCommandSource.CONFIGURED, resolution.second)
        } finally {
            controller.close()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `Codex detection exposes the resolved command version`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-codex-version")
        val paths = ApplicationPaths(root.resolve("home"))
        val executable = Files.createFile(root.resolve("codex.exe")).toAbsolutePath().toString()
        val store = ConfigStore(paths)
        store.save(AppConfig(codexExecutablePath = executable))
        val commands = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                commands += command
                return CommandResult(0, "codex-cli 1.2.3\n", "")
            }
        }
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            cliVersionRunner = runner,
            ioDispatcher = dispatcher,
        )
        try {
            controller.detectCodexCliPath()
            advanceUntilIdle()

            assertEquals("codex-cli 1.2.3", controller.codexCliVersion?.version)
            assertEquals(listOf(listOf(executable, "--version")), commands)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `first Meegle status check detects persists and then reuses the executable path`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-auto-save")
        val paths = ApplicationPaths(root.resolve("home"))
        val detected = Files.createFile(root.resolve(if (System.getProperty("os.name").startsWith("Windows")) "meegle.cmd" else "meegle"))
            .toAbsolutePath()
        detected.toFile().setExecutable(true)
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val configuredPath = AtomicReference<String?>(null)
        val runner = RecordingCommandRunner(CommandResult(0, "$detected\n", ""))
        val executable = ConfiguredMeegleExecutable(configuredPath::get, runner)
        val cli = RecordingMeegleCliService()
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleExecutablePath = configuredPath,
            meegleExecutable = executable,
            meegleCliService = cli,
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshMeegleStatus()
            advanceUntilIdle()

            assertEquals(detected.toString(), store.load().meegleExecutablePath)
            assertEquals(detected.toString(), controller.config.meegleExecutablePath)
            assertEquals(MeegleCommandSource.CONFIGURED, controller.meegleCommandResolution().second)
            assertEquals(1, runner.calls)
            assertEquals(1, cli.statusCalls)

            controller.refreshMeegleStatus(force = true)
            advanceUntilIdle()

            assertEquals(1, runner.calls)
            assertEquals(2, cli.statusCalls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `Meegle login opens the authorization page and completes automatically`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-login-success")
        val paths = ApplicationPaths(root.resolve("home"))
        val executablePath = root.resolve("meegle.cmd").toAbsolutePath().toString()
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = executablePath))
        var authenticated = false
        val cli = RecordingMeegleCliService(
            statusProvider = { MeegleCliStatus(installed = true, authenticated = authenticated) },
            completeAction = { authenticated = true; MeegleDeviceCodeLoginResult.AUTHORIZED },
        )
        val openedUrls = mutableListOf<String>()
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleCliService = cli,
            meegleProjectCatalog = MeegleProjectCatalog { emptyList() },
            meegleAuthorizationUrlOpener = { url ->
                openedUrls += url
                Result.success(Unit)
            },
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshMeegleStatus()
            advanceUntilIdle()
            assertFalse(assertIs<MeegleCliState.Ready>(controller.meegleCliState).status.authenticated)

            assertTrue(controller.loginMeegle())
            assertTrue(controller.meegleBusy)
            runCurrent()
            val waiting = assertNotNull(controller.meegleDeviceCodeLoginState)
            assertTrue(waiting.polling)
            assertEquals(listOf("https://project.feishu.cn/device?code=ABCD-EFGH"), openedUrls)
            assertEquals(0, cli.completeCalls)

            advanceTimeBy(5_000)
            runCurrent()

            assertTrue(assertIs<MeegleCliState.Ready>(controller.meegleCliState).status.authenticated)
            assertEquals(listOf("project.feishu.cn"), cli.loginHosts)
            assertEquals(1, cli.completeCalls)
            assertEquals(null, controller.meegleDeviceCodeLoginState)
            assertIs<MeegleProjectCatalogState.Loaded>(controller.meegleProjectCatalogState)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `temporary automatic Meegle polling failure retries until login succeeds`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-login-retry")
        val paths = ApplicationPaths(root.resolve("home"))
        val executablePath = root.resolve("meegle.cmd").toAbsolutePath().toString()
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = executablePath))
        var authenticated = false
        var failFirstPoll = true
        val cli = RecordingMeegleCliService(
            statusProvider = {
                MeegleCliStatus(
                    installed = true,
                    authenticated = authenticated,
                    authenticationError = if (authenticated) null else "not logged in",
                )
            },
            completeAction = {
                if (failFirstPoll) {
                    failFirstPoll = false
                    error("temporary TLS failure")
                } else {
                    authenticated = true
                    MeegleDeviceCodeLoginResult.AUTHORIZED
                }
            },
        )
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleCliService = cli,
            meegleProjectCatalog = MeegleProjectCatalog { emptyList() },
            meegleAuthorizationUrlOpener = { Result.success(Unit) },
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshMeegleStatus()
            advanceUntilIdle()
            val initialStatus = assertIs<MeegleCliState.Ready>(controller.meegleCliState).status

            assertTrue(controller.loginMeegle())
            runCurrent()
            assertTrue(assertNotNull(controller.meegleDeviceCodeLoginState).polling)

            advanceTimeBy(5_000)
            runCurrent()

            assertFalse(controller.meegleBusy)
            val retryingLogin = assertNotNull(controller.meegleDeviceCodeLoginState)
            assertTrue(retryingLogin.polling)
            assertContains(retryingLogin.error.orEmpty(), "自动检测")
            val failedStatus = assertIs<MeegleCliState.Ready>(controller.meegleCliState).status
            assertTrue(meegleLoginActionEnabled(failedStatus, controller.meegleCliState, saving = false, busy = false))

            advanceTimeBy(5_000)
            runCurrent()

            assertTrue(assertIs<MeegleCliState.Ready>(controller.meegleCliState).status.authenticated)
            assertEquals(1, cli.beginCalls)
            assertEquals(2, cli.completeCalls)
            assertEquals(initialStatus.authenticationError, failedStatus.authenticationError)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `Meegle logout clears the cached login and project catalog`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-logout")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = root.resolve("meegle.cmd").toAbsolutePath().toString()))
        var authenticated = true
        val cli = RecordingMeegleCliService(
            statusProvider = { MeegleCliStatus(installed = true, version = "1.0.19", authenticated = authenticated) },
            logoutAction = { authenticated = false },
        )
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleCliService = cli,
            meegleProjectCatalog = MeegleProjectCatalog { emptyList() },
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshMeegleStatus()
            advanceUntilIdle()
            controller.loadMeegleProjects()
            advanceUntilIdle()
            assertIs<MeegleProjectCatalogState.Loaded>(controller.meegleProjectCatalogState)

            assertTrue(controller.logoutMeegle())
            advanceUntilIdle()

            assertFalse(assertIs<MeegleCliState.Ready>(controller.meegleCliState).status.authenticated)
            assertIs<MeegleProjectCatalogState.Idle>(controller.meegleProjectCatalogState)
            assertEquals(1, cli.logoutCalls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `starting Meegle login invalidates a queued status refresh`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-login-refresh-race")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = root.resolve("meegle.cmd").toAbsolutePath().toString()))
        var authenticated = false
        val cli = RecordingMeegleCliService(
            statusProvider = { MeegleCliStatus(installed = true, authenticated = authenticated) },
            completeAction = { authenticated = true; MeegleDeviceCodeLoginResult.AUTHORIZED },
        )
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleCliService = cli,
            meegleProjectCatalog = MeegleProjectCatalog { emptyList() },
            meegleAuthorizationUrlOpener = { Result.success(Unit) },
            ioDispatcher = dispatcher,
        )
        try {
            // 模拟刚进入设置页就点击登录：此前排队的“未登录”查询不得晚到后覆盖成功状态。
            controller.refreshMeegleStatus()
            assertTrue(controller.loginMeegle())
            runCurrent()
            advanceTimeBy(5_000)
            runCurrent()

            assertTrue(assertIs<MeegleCliState.Ready>(controller.meegleCliState).status.authenticated)
            // 只有授权成功后的确认查询会执行；进入页面时排队的旧请求已经失效。
            assertEquals(1, cli.statusCalls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `cancelling Meegle device code login stops automatic polling`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-login-cancel")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = root.resolve("meegle.cmd").toAbsolutePath().toString()))
        val cli = RecordingMeegleCliService(
            statusProvider = { MeegleCliStatus(installed = true, authenticated = false) },
            completeAction = { MeegleDeviceCodeLoginResult.PENDING },
        )
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleCliService = cli,
            meegleAuthorizationUrlOpener = { Result.success(Unit) },
            ioDispatcher = dispatcher,
        )
        try {
            assertTrue(controller.loginMeegle())
            runCurrent()
            assertTrue(assertNotNull(controller.meegleDeviceCodeLoginState).polling)

            controller.cancelMeegleDeviceCodeLogin()
            assertNull(controller.meegleDeviceCodeLoginState)
            advanceTimeBy(5_000)
            runCurrent()

            assertEquals(0, cli.completeCalls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `manually opening the Meegle authorization page clears an automatic browser error`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-browser-retry")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig(meegleExecutablePath = root.resolve("meegle.cmd").toAbsolutePath().toString()))
        var browserOpenFails = true
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            meegleCliService = RecordingMeegleCliService(
                statusProvider = { MeegleCliStatus(installed = true, authenticated = false) },
                completeAction = { MeegleDeviceCodeLoginResult.PENDING },
            ),
            meegleAuthorizationUrlOpener = {
                if (browserOpenFails) Result.failure(IllegalStateException("browser unavailable")) else Result.success(Unit)
            },
            ioDispatcher = dispatcher,
        )
        try {
            assertTrue(controller.loginMeegle())
            runCurrent()
            assertContains(assertNotNull(controller.meegleDeviceCodeLoginState).error.orEmpty(), "未能自动打开")

            browserOpenFails = false
            assertTrue(controller.openMeegleDeviceCodeAuthorizationUrl())
            assertNull(controller.meegleDeviceCodeLoginState?.error)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `first Genbu probe detects and persists the executable path`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-genbu-auto-save")
        val paths = ApplicationPaths(root.resolve("home"))
        val detected = Files.createFile(root.resolve(if (System.getProperty("os.name").startsWith("Windows")) "genbu.exe" else "genbu"))
            .toAbsolutePath()
        detected.toFile().setExecutable(true)
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val configuredPath = AtomicReference<String?>(null)
        val runner = RecordingCommandRunner(CommandResult(0, "$detected\n", ""))
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            genbuExecutablePath = configuredPath,
            genbuExecutable = ConfiguredGenbuExecutable(configuredPath::get, runner, bundledDirectories = { emptyList() }),
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshGenbuCommandResolution()
            advanceUntilIdle()

            assertEquals(detected.toString(), store.load().genbuExecutablePath)
            assertEquals(detected.toString(), controller.config.genbuExecutablePath)
            assertEquals(GenbuCommandSource.PROBED, controller.genbuCommandResolution().second)
            assertEquals(1, runner.calls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `failed automatic Meegle path save is surfaced without changing invalid configuration`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-meegle-auto-save-failure")
        val paths = ApplicationPaths(root.resolve("home"))
        Files.createDirectories(paths.home)
        val original = "{ invalid json"
        Files.writeString(paths.config, original)
        val detected = Files.createFile(root.resolve(if (System.getProperty("os.name").startsWith("Windows")) "meegle.cmd" else "meegle"))
            .toAbsolutePath()
        detected.toFile().setExecutable(true)
        val configuredPath = AtomicReference<String?>(null)
        val runner = RecordingCommandRunner(CommandResult(0, "$detected\n", ""))
        val cli = RecordingMeegleCliService()
        val controller = DesktopApplication(
            paths = paths,
            configStore = ConfigStore(paths),
            meegleExecutablePath = configuredPath,
            meegleExecutable = ConfiguredMeegleExecutable(configuredPath::get, runner),
            meegleCliService = cli,
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshMeegleStatus()
            advanceUntilIdle()

            assertIs<MeegleCliState.Failed>(controller.meegleCliState)
            assertEquals(null, controller.config.meegleExecutablePath)
            assertEquals(original, Files.readString(paths.config))
            assertEquals(1, runner.calls)
            assertEquals(0, cli.statusCalls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `first Git refresh detects persists and then reuses the executable path`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-git-auto-save")
        val paths = ApplicationPaths(root.resolve("home"))
        val detected = Files.createFile(root.resolve(if (System.getProperty("os.name").startsWith("Windows")) "git.exe" else "git"))
            .toAbsolutePath()
        detected.toFile().setExecutable(true)
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val configuredPath = AtomicReference<String?>(null)
        val runner = RecordingGitEnvironmentRunner(detected.toString())
        val executable = ConfiguredGitExecutable(configuredPath::get, runner)
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            gitExecutablePath = configuredPath,
            gitExecutable = executable,
            localGitInspector = LocalGitEnvironmentInspector(runner, executable),
            ioDispatcher = dispatcher,
        )
        try {
            controller.refreshLocalGit()
            advanceUntilIdle()

            assertEquals(detected.toString(), store.load().gitExecutablePath)
            assertEquals(detected.toString(), controller.config.gitExecutablePath)
            assertEquals(GitCommandSource.CONFIGURED, controller.gitCommandResolution().second)
            assertEquals(1, runner.probeCalls)

            controller.refreshLocalGit(force = true)
            advanceUntilIdle()

            assertEquals(1, runner.probeCalls)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `task root saves under the paths feedback state`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-task-root-save")
        val paths = ApplicationPaths(root.resolve("home"))
        val target = root.resolve("tasks")
        val controller = DesktopApplication(paths = paths, configStore = ConfigStore(paths), ioDispatcher = dispatcher)
        try {
            controller.updateTaskRoot(target.toString())
            assertEquals(SettingsSaveState.SAVING, controller.settingsSaveState("paths"))

            advanceUntilIdle()

            assertEquals(target.toAbsolutePath().normalize().toString(), controller.config.taskRoot)
            assertEquals(SettingsSaveState.SAVED, controller.settingsSaveState("paths"))
            assertEquals(SettingsSaveState.IDLE, controller.settingsSaveState("basic"))
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `task area tool group visibility saves all seven preferences together`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-task-area-tool-groups")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        val controller = DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher)
        try {
            controller.updateTaskAreaToolGroupVisibility(
                taskGit = true,
                taskPath = false,
                workspaceGit = true,
                workspacePath = false,
                branchCopyIcons = false,
                requirementCopyIcons = true,
                projectNameCopyIcons = false,
            )
            assertEquals(SettingsSaveState.SAVING, controller.settingsSaveState("task-area"))

            advanceUntilIdle()

            assertTrue(controller.config.showTaskDetailGitActionGroup)
            assertFalse(controller.config.showTaskDetailPathActionGroup)
            assertTrue(controller.config.showWorkspaceGitActionGroup)
            assertFalse(controller.config.showWorkspacePathActionGroup)
            assertTrue(controller.config.showTaskAreaCopyIcons)
            assertFalse(controller.config.showTaskAreaBranchCopyIcons)
            assertTrue(controller.config.showTaskAreaRequirementCopyIcons)
            assertFalse(controller.config.showTaskAreaProjectNameCopyIcons)
            assertEquals(controller.config, store.load())
            assertEquals(SettingsSaveState.SAVED, controller.settingsSaveState("task-area"))
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `AI naming model saves independently in task creation settings`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-ai-naming-model")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        val controller = DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher)
        try {
            controller.setAiRequirementNamingModel("gpt-5.6-sol")
            assertEquals(SettingsSaveState.SAVING, controller.settingsSaveState("task-creation"))

            advanceUntilIdle()

            assertEquals("gpt-5.6-sol", controller.config.aiRequirementNamingModel)
            assertEquals("gpt-5.6-sol", store.load().aiRequirementNamingModel)
            assertEquals(SettingsSaveState.SAVED, controller.settingsSaveState("task-creation"))
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `requirement materials settings save independently and normalize values`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-requirement-materials-save")
        val paths = ApplicationPaths(root.resolve("home"))
        val materials = root.resolve("materials")
        val controller = DesktopApplication(paths = paths, configStore = ConfigStore(paths), ioDispatcher = dispatcher)
        try {
            controller.updateRequirementMaterialsRoot(" ${materials} ")
            assertEquals(SettingsSaveState.SAVING, controller.settingsSaveState("requirement-materials-root"))
            advanceUntilIdle()

            controller.updateRequirementMaterialsSubdirectory(" 研发 ")
            assertEquals(SettingsSaveState.SAVING, controller.settingsSaveState("requirement-materials-subdirectory"))
            advanceUntilIdle()

            assertEquals(materials.toAbsolutePath().normalize().toString(), controller.config.requirementMaterialsRoot)
            assertEquals("研发", controller.config.requirementMaterialsSubdirectory)
            assertTrue(Files.isDirectory(materials))
            assertTrue(controller.config.requirementMaterialsConfigured)
            assertEquals(SettingsSaveState.SAVED, controller.settingsSaveState("requirement-materials-root"))
            assertEquals(SettingsSaveState.SAVED, controller.settingsSaveState("requirement-materials-subdirectory"))
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @Test
    fun `invalid disk configuration remains visible and is never overwritten`() {
        val root = Files.createTempDirectory("SILVERWING-invalid-config")
        val paths = ApplicationPaths(root.resolve("home"))
        Files.createDirectories(paths.home)
        val original = "{ invalid json"
        Files.writeString(paths.config, original)

        val controller = DesktopApplication(paths = paths, configStore = ConfigStore(paths))
        try {
            assertNotNull(controller.configurationLoadError)
            assertEquals(original, Files.readString(paths.config))
            assertEquals(paths.config.toAbsolutePath().normalize(), controller.configFileSnapshot.path)
            assertEquals(original, controller.configFileSnapshot.content)
            assertContains(controller.configurationRecoveryGuidance(), paths.config.toAbsolutePath().normalize().toString())
            assertContains(controller.configurationRecoveryGuidance(), "silverwing")
        } finally {
            controller.close()
        }
    }

    @Test
    fun `incompatible configuration remains previewable with recovery guidance`() {
        val root = Files.createTempDirectory("SILVERWING-incompatible-config")
        val paths = ApplicationPaths(root.resolve("home"))
        ConfigStore(paths).save(AppConfig())
        val incompatibleShard = paths.config.resolve("layout.json")
        val original = "{\"schema\":1,\"groups\":[]}"
        Files.writeString(incompatibleShard, original)

        val controller = DesktopApplication(paths = paths, configStore = ConfigStore(paths))
        try {
            assertNotNull(controller.configurationLoadError)
            assertContains(controller.configurationLoadError.orEmpty(), "schema 不受支持")
            assertContains(controller.configFileSnapshot.content.orEmpty(), "[layout.json]")
            assertContains(controller.configFileSnapshot.content.orEmpty(), original)
            assertContains(controller.configurationRecoveryGuidance(), paths.config.toAbsolutePath().normalize().toString())
            assertContains(controller.configurationRecoveryGuidance(), "silverwing")
            assertEquals(original, Files.readString(incompatibleShard))
        } finally {
            controller.close()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `window focus refreshes the raw configuration preview`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-config-preview-focus")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(AppConfig())
        val controller = DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher)
        val externallyUpdated = "{\"schema\":1,\"tagEnabled\":false,\"tagHistoryMaxGroups\":5}"
        try {
            Files.writeString(paths.config.resolve("tag.json"), externallyUpdated)

            controller.onWindowFocused()
            assertTrue(controller.configFileSnapshotRefreshing)
            advanceUntilIdle()

            assertContains(controller.configFileSnapshot.content.orEmpty(), "[tag.json]")
            assertContains(controller.configFileSnapshot.content.orEmpty(), externallyUpdated)
            assertFalse(controller.configFileSnapshotRefreshing)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @Test
    fun `task manifest issue exposes the full path and disappears after a rescan`() {
        val root = Files.createTempDirectory("SILVERWING-task-manifest-issue")
        val paths = ApplicationPaths(root.resolve("home"))
        val taskRoot = root.resolve("tasks")
        val taskDirectory = taskRoot.resolve("legacy")
        Files.createDirectories(taskDirectory)
        val manifestPath = taskDirectory.resolve(ManifestStore.FILE_NAME)
        Files.writeString(manifestPath, "{\"schemaVersion\":\"0.8.1\"}")
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = taskRoot.toString()))
        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            val issue = controller.taskManifestIssues.single()
            assertEquals(manifestPath.toAbsolutePath().normalize().toString(), issue.manifestPath)
            assertContains(issue.reason, "版本不受支持")
            assertContains(controller.taskManifestRecoveryGuidance(issue), issue.manifestPath)

            ManifestStore().save(
                taskDirectory,
                TaskManifest(
                    folderName = "legacy",
                    taskDirectoryName = "legacy",
                    featureBranch = "feature/legacy",
                    createdAt = "2026-08-20T00:00:00Z",
                    updatedAt = "2026-08-20T00:00:00Z",
                    lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                    services = emptyList(),
                ),
            )
            controller.refreshTaskManifestIssues()

            assertEquals(emptyList(), controller.taskManifestIssues)
        } finally {
            controller.close()
        }
    }

    @Test
    fun `agents preview includes the current requirement link`() {
        val root = Files.createTempDirectory("SILVERWING-preview-link")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(
            AppConfig(
                taskRoot = root.resolve("tasks").toString(),
                repositories = listOf(
                    RepositoryConfig("repo", "Service", root.resolve("service").toString(), root.resolve("service/.git").toString()),
                ),
                groups = listOf(
                    GroupConfig("g", "G", services = listOf(GroupServiceConfig.standard("service", "repo", "Service"))),
                ),
            ),
        )
        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            val preview = controller.previewAgents(
                folderName = "支付 订单优化",
                branch = "feature/task-1",
                groupId = "g",
                serviceIds = setOf("service"),
                requirementLink = "REQ-123 raw requirement",
                notes = "notes",
            )
            val previewContent = preview.files.joinToString("\n") { it.content }

            assertContains(previewContent, "REQ-123 raw requirement")
            assertContains(previewContent, root.resolve("tasks").resolve("支付 订单优化").toString())
            assertFalse(previewContent.contains("## 任务资料目录"))
        } finally {
            controller.close()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `task root with existing tasks waits for confirmation before migrating`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-task-root-preview")
        val paths = ApplicationPaths(root.resolve("home"))
        val source = Files.createDirectories(root.resolve("source-tasks"))
        val target = root.resolve("target-tasks")
        val store = ConfigStore(paths).also { it.save(AppConfig(taskRoot = source.toString())) }
        ManifestStore().save(
            source.resolve("TASK-1"),
            TaskManifest(
                folderName = "TASK-1",
                taskDirectoryName = "TASK-1",
                featureBranch = "feature/task-root-preview",
                createdAt = "2026-08-29 00:00:00",
                updatedAt = "2026-08-29 00:00:00",
                services = emptyList(),
            ),
        )
        val controller = DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher)
        try {
            controller.updateTaskRoot(target.toString())
            advanceUntilIdle()

            val preview = assertIs<TaskRootMigrationUiState.Preview>(controller.taskRootMigrationState)
            assertEquals(1, preview.preview.taskCount)
            assertEquals(source.toAbsolutePath().normalize().toString(), controller.config.taskRoot)
            assertFalse(Files.exists(target.resolve("TASK-1")))

            controller.confirmTaskRootMigration()
            assertIs<TaskRootMigrationUiState.Migrating>(controller.taskRootMigrationState)
            advanceUntilIdle()

            assertIs<TaskRootMigrationUiState.Idle>(controller.taskRootMigrationState)
            assertEquals(target.toAbsolutePath().normalize().toString(), controller.config.taskRoot)
            assertTrue(Files.exists(target.resolve("TASK-1/silverwing.json")))
            assertFalse(Files.exists(source.resolve("TASK-1")))
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @Test
    fun `agents preview includes a ready materials path when supplied`() {
        val root = Files.createTempDirectory("SILVERWING-preview-materials")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(
            AppConfig(
                taskRoot = root.resolve("tasks").toString(),
                repositories = listOf(
                    RepositoryConfig("repo", "Service", root.resolve("service").toString(), root.resolve("service/.git").toString()),
                ),
                groups = listOf(
                    GroupConfig("g", "G", services = listOf(GroupServiceConfig.standard("service", "repo", "Service"))),
                ),
            ),
        )
        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            val materialsPath = root.resolve("materials").resolve("sprint-1").resolve("1-task").resolve("研发")
            val preview = controller.previewAgents(
                folderName = "任务",
                branch = "feature/task",
                groupId = "g",
                serviceIds = setOf("service"),
                requirementLink = "1",
                notes = "",
                requirementMaterials = RequirementMaterialsDirectory(
                    status = RequirementMaterialsStatus.READY,
                    writeRoot = materialsPath.toString(),
                ),
            )
            val previewContent = preview.files.joinToString("\n") { it.content }

            assertContains(previewContent, "## 任务资料目录")
            assertContains(previewContent, materialsPath.toAbsolutePath().normalize().toString())
        } finally {
            controller.close()
        }
    }

    @Test
    fun `desktop tool options come entirely from injected registry and preserve unknown ids`() {
        val root = Files.createTempDirectory("SILVERWING-tools")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        store.save(
            AppConfig(
                defaultWorkspaceToolIds = listOf("claude", "legacy-tool"),
                groups = listOf(
                    GroupConfig(
                        id = "g",
                        name = "G",
                    ),
                ),
            ),
        )
        val registry = TaskWorkspaceToolRegistry(listOf(tool("claude"), tool("cursor")))
        val controller = DesktopApplication(paths = paths, configStore = store, workspaceToolRegistry = registry)
        try {
            val options = controller.workspaceToolOptions().associateBy(WorkspaceToolOption::id)

            assertEquals(true, options.getValue("claude").available)
            assertEquals(true, options.getValue("cursor").available)
            assertEquals(false, options.getValue("legacy-tool").available)
        } finally {
            controller.close()
        }
    }

    @Test
    fun `startup reads persisted arrays and selects newest task without external refresh`() {
        val root = Files.createTempDirectory("SILVERWING-desktop-startup")
        val paths = ApplicationPaths(root.resolve("home"))
        val taskRoot = root.resolve("tasks")
        val configStore = ConfigStore(paths)
        val manifests = ManifestStore()
        configStore.save(
            AppConfig(
                taskRoot = taskRoot.toString(),
                repositories = listOf(
                    RepositoryConfig("repo-1", "service", root.resolve("missing").toString(), "missing"),
                ),
            ),
        )
        manifests.save(taskRoot.resolve("older"), task("older", "2026-08-01T00:00:00Z"))
        manifests.save(taskRoot.resolve("newer"), task("newer", "2026-08-02T00:00:00Z"))
        var repositoryInspections = 0
        var remoteRequests = 0
        val controller = DesktopApplication(
            paths = paths,
            configStore = configStore,
            manifests = manifests,
            repositoryInspector = RepositoryInspector {
                repositoryInspections++
                error("startup must not inspect repositories")
            },
            requirementMetadataProvider = RequirementMetadataProvider {
                remoteRequests++
                error("startup must not call Meegle")
            },
        )

        assertEquals(NavigationItem.TASKS, controller.navigation)
        assertEquals(listOf("newer", "older"), controller.tasks.map { it.folderName })
        assertEquals("newer", controller.selectedTask?.folderName)
        assertEquals(0, repositoryInspections)
        assertEquals(0, remoteRequests)
        controller.close()
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `archiving a task keeps the current active-task navigation`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-archive-navigation")
        val paths = ApplicationPaths(root.resolve("home"))
        val taskRoot = root.resolve("tasks")
        val store = ConfigStore(paths)
        store.save(AppConfig(taskRoot = taskRoot.toString()))
        val taskDirectory = taskRoot.resolve("archive-me")
        ManifestStore().save(
            taskDirectory,
            TaskManifest(
                folderName = "archive-me",
                taskDirectoryName = "archive-me",
                featureBranch = "feature/archive-me",
                createdAt = "2026-09-14 10:00:00",
                updatedAt = "2026-09-14 10:00:00",
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = emptyList(),
            ),
        )
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            ioDispatcher = dispatcher,
        )
        try {
            val selected = assertNotNull(controller.selectedTask)
            assertEquals(NavigationItem.TASKS, controller.navigation)

            assertTrue(controller.archiveTask(selected))
            advanceUntilIdle()

            assertEquals(NavigationItem.TASKS, controller.navigation)
            assertEquals(null, controller.selectedTask)
            assertEquals(
                TaskLifecycleStatus.ARCHIVED,
                ManifestStore().load(taskDirectory).lifecycleStatus,
            )
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `remote branch loading exposes loading success and failure without startup request`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-branches")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        val repository = RepositoryConfig("repo", "repo", root.resolve("repo").toString(), root.resolve("repo/.git").toString(), "https://example.test/repo.git")
        val service = GroupServiceConfig(
            id = "clone",
            repositoryId = repository.id,
            displayName = "Clone",
            modules = listOf(
                ServiceModuleConfig(
                    id = "clone",
                    strategy = WorkspaceStrategy.INDEPENDENT_CLONE,
                    masterBranch = "origin/main",
                    tagEnabled = false,
                ),
            ),
        )
        store.save(AppConfig(repositories = listOf(repository), groups = listOf(GroupConfig("g", "G", services = listOf(service)))))
        var requests = 0
        var shouldFail = false
        val controller = DesktopApplication(
            paths = paths,
            configStore = store,
            remoteBranchCatalog = object : RemoteBranchCatalog {
                override fun list(repository: Path, remote: String): List<String> {
                    requests++
                    if (shouldFail) error("offline")
                    return listOf("origin/main", "origin/release/test")
                }
            },
            ioDispatcher = dispatcher,
        )
        try {
            assertEquals(0, requests)
            controller.loadRemoteBranches("repo")
            assertIs<RemoteBranchesState.Loading>(controller.remoteBranchState("repo", "origin"))
            advanceUntilIdle()
            assertEquals(listOf("origin/main", "origin/release/test"), assertIs<RemoteBranchesState.Loaded>(controller.remoteBranchState("repo", "origin")).branches)
            shouldFail = true
            controller.loadRemoteBranches("repo", force = true)
            assertEquals(
                listOf("origin/main", "origin/release/test"),
                assertIs<RemoteBranchesState.Loading>(controller.remoteBranchState("repo", "origin")).staleBranches,
            )
            advanceUntilIdle()
            val failed = assertIs<RemoteBranchesState.Failed>(controller.remoteBranchState("repo", "origin"))
            assertEquals("offline", failed.message)
            assertEquals(listOf("origin/main", "origin/release/test"), failed.staleBranches)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `closing the last effective Tag gate returns Tag page to tasks`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-tag-navigation")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        val repository = RepositoryConfig("repo", "repo", root.resolve("repo").toString(), root.resolve("repo/.git").toString())
        store.save(AppConfig(repositories = listOf(repository), groups = listOf(
            GroupConfig("g", "G", services = listOf(GroupServiceConfig.standard("service", "repo", "Service"))),
        )))
        val controller = DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher)
        try {
            controller.navigation = NavigationItem.TAG
            controller.setGroupTagEnabled("g", false)
            advanceUntilIdle()

            assertEquals(false, controller.showsTagNavigation)
            assertEquals(NavigationItem.TASKS, controller.navigation)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `global Tag settings hide navigation and persist independently of group gates`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        Dispatchers.setMain(dispatcher)
        val root = Files.createTempDirectory("SILVERWING-global-tag-settings")
        val paths = ApplicationPaths(root.resolve("home"))
        val store = ConfigStore(paths)
        val repository = RepositoryConfig("repo", "repo", root.resolve("repo").toString(), root.resolve("repo/.git").toString())
        store.save(AppConfig(
            repositories = listOf(repository),
            groups = listOf(GroupConfig("g", "G", services = listOf(GroupServiceConfig.standard("service", "repo", "Service")))),
        ))
        val controller = DesktopApplication(paths = paths, configStore = store, ioDispatcher = dispatcher)
        try {
            controller.navigation = NavigationItem.TAG
            assertTrue(controller.showsTagNavigation)

            assertTrue(controller.setGlobalTagEnabled(false))
            advanceUntilIdle()

            assertFalse(controller.config.tagEnabled)
            assertFalse(controller.showsTagNavigation)
            assertEquals(NavigationItem.TASKS, controller.navigation)
            assertFalse(ConfigStore(paths).load().tagEnabled)

            assertTrue(controller.updateTagHistoryMaxGroups(3))
            advanceUntilIdle()
            assertEquals(3, controller.config.tagHistoryMaxGroups)
            assertEquals(3, ConfigStore(paths).load().tagHistoryMaxGroups)

            assertTrue(controller.setGlobalTagEnabled(true))
            advanceUntilIdle()
            assertTrue(controller.config.tagEnabled)
            assertTrue(controller.showsTagNavigation)
        } finally {
            controller.close()
            Dispatchers.resetMain()
        }
    }

    private fun task(name: String, updatedAt: String) = TaskManifest(
        folderName = name,
        taskDirectoryName = name,
        featureBranch = "feature/$name",
        createdAt = updatedAt,
        updatedAt = updatedAt,
        lifecycleStatus = TaskLifecycleStatus.ACTIVE,
        services = emptyList(),
    )

    private fun tool(id: String) = object : TaskWorkspaceToolLauncher {
        override val descriptor = TaskWorkspaceToolDescriptor(id, id)
        override fun availability(): TaskWorkspaceToolAvailability = TaskWorkspaceToolAvailability.Available
        override fun open(context: TaskWorkspaceContext) = Unit
    }

    private class RecordingCommandRunner(private val result: CommandResult) : StreamingCommandRunner {
        var calls = 0
            private set

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            calls++
            return result
        }

        override fun runStreaming(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
            onOutput: (CommandOutputLine) -> Unit,
        ): CommandResult = run(command, workingDirectory, timeout, environment)
    }

    private class RecordingMeegleCliService(
        private val statusProvider: () -> MeegleCliStatus = { MeegleCliStatus(installed = true) },
        private val logoutAction: () -> Unit = {},
        private val completeAction: (String) -> MeegleDeviceCodeLoginResult = { MeegleDeviceCodeLoginResult.AUTHORIZED },
    ) : MeegleCliService {
        var statusCalls = 0
            private set
        var beginCalls = 0
            private set
        var completeCalls = 0
            private set
        var logoutCalls = 0
            private set
        val loginHosts = mutableListOf<String>()

        override fun status(): MeegleCliStatus {
            statusCalls++
            return statusProvider()
        }

        override fun logout() {
            logoutCalls++
            logoutAction()
        }

        override fun beginDeviceCodeLogin(host: String): MeegleDeviceCodeChallenge {
            beginCalls++
            loginHosts += host
            return MeegleDeviceCodeChallenge(
                host,
                "https://project.feishu.cn/device",
                "https://project.feishu.cn/device?code=ABCD-EFGH",
                "ABCD-EFGH",
                600,
                "test-device-code",
                "test-client-id",
            )
        }

        override fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge): MeegleDeviceCodeLoginResult {
            completeCalls++
            return completeAction(challenge.host)
        }
    }

    private class RecordingGitEnvironmentRunner(private val detectedPath: String) : CommandRunner {
        var probeCalls = 0
            private set

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult = when {
            command.firstOrNull() == "where.exe" || command.firstOrNull() == "/bin/zsh" || command.firstOrNull() == "/bin/bash" -> {
                probeCalls++
                CommandResult(0, "$detectedPath\n", "")
            }
            command.lastOrNull() == "--version" -> CommandResult(0, "git version test\n", "")
            command.contains("config") -> CommandResult(0, "", "")
            else -> error("Unexpected Git command: ${command.joinToString(" ")}")
        }
    }
}
