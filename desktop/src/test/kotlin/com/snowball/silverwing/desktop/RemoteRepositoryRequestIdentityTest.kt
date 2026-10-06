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
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class RemoteRepositoryRequestIdentityTest {
    @TempDir lateinit var temporary: Path

    @Test fun `cancelled older remote refresh cannot overwrite its replacement`() = runTest {
        val started = CountDownLatch(1)
        val exited = CountDownLatch(1)
        val secondStarted = CountDownLatch(1)
        val release = CountDownLatch(1)
        val calls = AtomicInteger()
        val remoteCatalog = RepositoryRemoteCatalog {
            if (calls.incrementAndGet() == 1) {
                started.countDown()
                try { release.await(5, TimeUnit.SECONDS) } catch (_: InterruptedException) { }
                finally { exited.countDown() }
                listOf("old-origin")
            } else {
                secondStarted.countDown()
                release.await(5, TimeUnit.SECONDS)
                listOf("origin", "github")
            }
        }
        val child = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        val controller = controller(session(), remoteCatalog, child, Dispatchers.IO)
        try {
            controller.loadRepositoryRemotes("repo")
            testScheduler.runCurrent()
            assertTrue(started.await(2, TimeUnit.SECONDS))
            controller.loadRepositoryRemotes("repo", force = true)
            testScheduler.runCurrent()
            assertTrue(exited.await(2, TimeUnit.SECONDS))
            assertTrue(secondStarted.await(2, TimeUnit.SECONDS))
            Thread.sleep(100)
            testScheduler.runCurrent()
            assertIs<RepositoryRemotesState.Loading>(controller.repositoryRemotesState("repo"))
            release.countDown()
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(3)
            while (controller.repositoryRemotesState("repo") !is RepositoryRemotesState.Loaded && System.nanoTime() < deadline) {
                Thread.sleep(10)
                testScheduler.runCurrent()
            }
            assertEquals(listOf("origin", "github"), assertIs<RepositoryRemotesState.Loaded>(controller.repositoryRemotesState("repo")).remotes)
        } finally {
            release.countDown()
            child.cancel()
            testScheduler.runCurrent()
        }
    }

    @Test fun `repository path changes invalidate both remote names and branches without force`() = runTest {
        val session = session()
        val paths = mutableListOf<Path>()
        val dispatcher = StandardTestDispatcher(testScheduler)
        val controller = controller(session, RepositoryRemoteCatalog { paths.add(it); listOf(it.fileName.toString()) }, backgroundScope, dispatcher)
        controller.loadRepositoryRemotes("repo")
        controller.loadRemoteBranches("repo")
        testScheduler.runCurrent()
        assertEquals(listOf("repo"), assertIs<RepositoryRemotesState.Loaded>(controller.repositoryRemotesState("repo")).remotes)
        assertEquals(listOf("origin/repo"), assertIs<RemoteBranchesState.Loaded>(controller.remoteBranchState("repo", "origin")).branches)

        session.config = session.config.copy(repositories = session.config.repositories.map {
            it.copy(rootPath = temporary.resolve("replacement").toString())
        })
        assertIs<RepositoryRemotesState.Idle>(controller.repositoryRemotesState("repo"))
        assertIs<RemoteBranchesState.Idle>(controller.remoteBranchState("repo", "origin"))
        controller.loadRemoteBranches("repo")
        controller.loadRepositoryRemotes("repo")
        testScheduler.runCurrent()
        assertEquals(listOf("replacement"), assertIs<RepositoryRemotesState.Loaded>(controller.repositoryRemotesState("repo")).remotes)
        assertEquals(listOf("origin/replacement"), assertIs<RemoteBranchesState.Loaded>(controller.remoteBranchState("repo", "origin")).branches)
        assertEquals(listOf(temporary.resolve("repo"), temporary.resolve("replacement")), paths)
    }

    @Test fun `failed remote refresh can retry without disturbing other repository results`() = runTest {
        val calls = AtomicInteger()
        val dispatcher = StandardTestDispatcher(testScheduler)
        val session = session().also { it.config = it.config.copy(repositories = it.config.repositories +
            RepositoryConfig("other", "other", temporary.resolve("other").toString(), temporary.resolve("other/.git").toString())) }
        val controller = controller(session, RepositoryRemoteCatalog { path ->
            if (path.fileName.toString() == "other") listOf("upstream")
            else if (calls.incrementAndGet() == 1) error("Offline") else listOf("github")
        }, backgroundScope, dispatcher)
        controller.loadRepositoryRemotes("repo")
        controller.loadRepositoryRemotes("other")
        testScheduler.runCurrent()
        assertIs<RepositoryRemotesState.Failed>(controller.repositoryRemotesState("repo"))
        controller.loadRepositoryRemotes("repo", force = true)
        testScheduler.runCurrent()
        assertEquals(listOf("github"), assertIs<RepositoryRemotesState.Loaded>(controller.repositoryRemotesState("repo")).remotes)
        assertEquals(listOf("upstream"), assertIs<RepositoryRemotesState.Loaded>(controller.repositoryRemotesState("other")).remotes)
    }

    private fun session() = AppSessionStore(AppConfig(repositories = listOf(RepositoryConfig(
        "repo", "repo", temporary.resolve("repo").toString(), temporary.resolve("repo/.git").toString(),
    ))), emptyList())

    private fun controller(session: AppSessionStore, remotes: RepositoryRemoteCatalog, child: CoroutineScope,
        ioDispatcher: CoroutineDispatcher): SettingsController {
        val paths = ApplicationPaths(temporary.resolve("home"))
        val store = ConfigStore(paths)
        val runner = OperationRunner(OperationCoordinator(), child, ioDispatcher)
        val picker = object : NativePathPicker {
            override suspend fun pickDirectory(initialPath: String?) = null
            override suspend fun pickDirectories(initialPath: String?): List<String>? = null
            override suspend fun pickFile(initialPath: String?, extensions: List<String>) = null
            override suspend fun pickApplication(initialPath: String?) = null
        }
        return SettingsController(session = session, configStore = store, groups = GroupConfigurationService(store),
            taskRootMigrations = TaskRootMigrationService(configStore = store, paths = paths), pathPicker = picker,
            branchCatalog = object : RemoteBranchCatalog { override fun list(repository: Path, remote: String) = listOf("$remote/${repository.fileName}") },
            remoteCatalog = remotes, meegleProjectCatalog = MeegleProjectCatalog { emptyList() },
            meegleCliService = object : MeegleCliService {
                override fun status() = MeegleCliStatus(false)
                override fun logout() = Unit
                override fun beginDeviceCodeLogin(host: String) = error("No login expected")
                override fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge) = error("No login expected")
            }, localGitInspector = LocalGitEnvironmentInspector(), scope = child, ioDispatcher = ioDispatcher,
            operations = runner, settingsOperations = runner, meegleOperations = runner,
            applyConfig = {}, reloadTasks = {}, showError = { throw it }, showStatus = {})
    }
}
