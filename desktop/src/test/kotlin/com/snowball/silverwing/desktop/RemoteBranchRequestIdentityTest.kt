package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import kotlin.test.assertIs
import kotlin.test.assertEquals
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class RemoteBranchRequestIdentityTest {
    @TempDir lateinit var temporary: Path

    @Test
    fun `cancelled old branch request cannot clear loading for its replacement`() = runTest {
        val firstStarted = CountDownLatch(1)
        val firstExited = CountDownLatch(1)
        val secondStarted = CountDownLatch(1)
        val release = CountDownLatch(1)
        val calls = AtomicInteger()
        val catalog = object : RemoteBranchCatalog {
            override fun list(repository: Path, remote: String): List<String> {
                if (calls.incrementAndGet() == 1) {
                    firstStarted.countDown()
                    try { release.await(5, TimeUnit.SECONDS) } finally { firstExited.countDown() }
                    return listOf("origin/old")
                }
                secondStarted.countDown()
                release.await(5, TimeUnit.SECONDS)
                return listOf("origin/new")
            }
        }
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths)
        val config = AppConfig(repositories = listOf(RepositoryConfig("repo", "repo", temporary.resolve("repo").toString(), temporary.resolve("repo/.git").toString())))
        val session = AppSessionStore(config, emptyList())
        val uiDispatcher = StandardTestDispatcher(testScheduler)
        val childScope = CoroutineScope(SupervisorJob() + uiDispatcher)
        val runner = OperationRunner(OperationCoordinator(), childScope, Dispatchers.IO)
        val picker = object : NativePathPicker {
            override suspend fun pickDirectory(initialPath: String?) = null
            override suspend fun pickDirectories(initialPath: String?): List<String>? = null
            override suspend fun pickFile(initialPath: String?, extensions: List<String>) = null
            override suspend fun pickApplication(initialPath: String?) = null
        }
        val controller = SettingsController(
            session = session, configStore = store, groups = GroupConfigurationService(store),
            taskRootMigrations = TaskRootMigrationService(configStore = store, paths = paths),
            pathPicker = picker, branchCatalog = catalog, meegleProjectCatalog = MeegleProjectCatalog { emptyList() },
            meegleCliService = object : MeegleCliService {
                override fun status() = MeegleCliStatus(false)
                override fun logout() = Unit
                override fun beginDeviceCodeLogin(host: String) = error("No login expected")
                override fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge) = error("No login expected")
            }, localGitInspector = LocalGitEnvironmentInspector(), scope = childScope, ioDispatcher = Dispatchers.IO,
            operations = runner, settingsOperations = runner, meegleOperations = runner,
            applyConfig = {}, reloadTasks = {}, showError = { throw it }, showStatus = {},
        )
        try {
            controller.loadRemoteBranches("repo")
            testScheduler.runCurrent()
            assertTrue(firstStarted.await(2, TimeUnit.SECONDS))
            // Closing and reopening the branch popup cancels all loads, then starts a normal load.
            controller.cancelRemoteBranchLoads()
            controller.loadRemoteBranches("repo")
            testScheduler.runCurrent()
            assertTrue(firstExited.await(2, TimeUnit.SECONDS))
            assertTrue(secondStarted.await(2, TimeUnit.SECONDS))
            // Let the cancelled IO continuation enqueue its UI update; the replacement stays blocked.
            Thread.sleep(100)
            testScheduler.runCurrent()
            val state = controller.state.remoteBranches["repo|origin"]
            assertIs<RemoteBranchesState.Loading>(state)
            release.countDown()
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(3)
            while (controller.state.remoteBranches["repo|origin"] !is RemoteBranchesState.Loaded && System.nanoTime() < deadline) {
                Thread.sleep(10)
                testScheduler.runCurrent()
            }
            assertEquals(
                listOf("origin/new"),
                assertIs<RemoteBranchesState.Loaded>(controller.state.remoteBranches["repo|origin"]).branches,
            )
        } finally {
            release.countDown()
            childScope.cancel()
            testScheduler.runCurrent()
        }
    }
}
