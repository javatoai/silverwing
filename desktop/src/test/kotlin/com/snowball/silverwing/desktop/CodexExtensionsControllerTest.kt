package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class CodexExtensionsControllerTest {
    @TempDir lateinit var temporary: Path
    private val source = CodexPluginMarketplaceSource("team", "团队插件", "https://example.test/team/plugins.git", marketplaceDirectory = ".")

    private class Configurations(var config: AppConfig = AppConfig()) : ConfigurationRepository {
        var failSaving = false
        var failLoading = false
        var failReadAfterSaveFailure = false
        var writes = 0
        override fun load(): AppConfig { if (failLoading) error("恢复配置读取失败"); return config }
        override fun save(config: AppConfig) {
            if (failSaving) { if (failReadAfterSaveFailure) failLoading = true; error("配置写入失败") }
            writes++; this.config = config
        }
    }
    private class Commands(private val block: (List<String>) -> CommandResult = { CommandResult(1, "", "来源加载失败") }) : CommandRunner {
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>) = block(command)
    }
    private inner class Fixture(scope: CoroutineScope, dispatcher: CoroutineDispatcher,
        val configurations: Configurations = Configurations(), commands: CommandRunner = Commands()) {
        val coordinator = OperationCoordinator()
        val operations = OperationRunner(coordinator, scope, dispatcher)
        val applied = mutableListOf<AppConfig>()
        val controller = CodexExtensionsController(CodexExtensionsApplicationService(configurations,
            CodexExtensionsService(ApplicationPaths(temporary.resolve("app")), runner = commands,
                codexExecutable = CodexExecutable { "codex-test" }, userHome = { temporary.resolve("user") }),
            RemoteGitBranchCatalog(commands, gitExecutable = { "git-test" })), operations, applied::add)
    }

    @Test fun `failed configuration save preserves the draft and can retry`() = runTest {
        val configurations = Configurations().apply { failSaving = true }
        val fixture = Fixture(this, StandardTestDispatcher(testScheduler), configurations)
        val states = mutableListOf<ExtensionSourceSaveState>()
        assertTrue(fixture.controller.addMarketplace(source, states::add))
        advanceUntilIdle()
        assertEquals(ExtensionSourceSaveState.Failed("配置写入失败"), states.last())
        assertTrue(configurations.config.codexPluginMarketplaceSources.isEmpty())
        configurations.failSaving = false
        assertTrue(fixture.controller.addMarketplace(source, states::add))
        advanceUntilIdle()
        assertEquals(ExtensionSourceSaveState.Saved, states.last())
        assertEquals(listOf(source), fixture.applied.last().codexPluginMarketplaceSources)
        assertNotNull(fixture.controller.state.snapshot.marketplaces[source.id]?.error,
            "Saved configuration must retain the separate loading error")
    }

    @Test fun `failed recovery read still releases saving state and retains both errors`() = runTest {
        val configurations = Configurations().apply { failSaving = true; failReadAfterSaveFailure = true }
        val fixture = Fixture(this, StandardTestDispatcher(testScheduler), configurations)
        val states = mutableListOf<ExtensionSourceSaveState>()
        fixture.controller.addMarketplace(source, states::add)
        advanceUntilIdle()
        val failure = assertIs<ExtensionSourceSaveState.Failed>(states.last())
        assertTrue(failure.message.contains("输入已保留"))
        assertFalse(fixture.coordinator.busy)
        assertTrue(fixture.coordinator.errorMessage.orEmpty().contains("配置写入失败"))
        assertTrue(fixture.coordinator.errorMessage.orEmpty().contains("恢复配置读取失败"))
    }

    @Test fun `cancellation before execution does not write a source or leave saving active`() = runTest {
        val fixture = Fixture(this, StandardTestDispatcher(testScheduler))
        val states = mutableListOf<ExtensionSourceSaveState>()
        fixture.controller.addMarketplace(source, states::add)
        assertTrue(fixture.controller.cancelSourceSave())
        advanceUntilIdle()
        assertIs<ExtensionSourceSaveState.Failed>(states.last())
        assertEquals(0, fixture.configurations.writes)
        assertFalse(fixture.coordinator.busy)
        assertEquals("操作已取消", fixture.coordinator.statusMessage)
    }

    @Test fun `cancellation after persistence synchronizes config and retains cancelled feedback`() = runBlocking<Unit> {
        val started = CompletableDeferred<Unit>()
        val fixture = Fixture(this, Dispatchers.IO, commands = Commands {
            started.complete(Unit); Thread.sleep(60_000); CommandResult(0, "{}", "")
        })
        val states = mutableListOf<ExtensionSourceSaveState>()
        try {
            fixture.controller.addMarketplace(source, states::add)
            withTimeout(5_000) { started.await() }
            assertEquals(listOf(source), fixture.configurations.config.codexPluginMarketplaceSources)
            assertTrue(fixture.controller.cancelSourceSave())
            withTimeout(5_000) { while (fixture.coordinator.busy) delay(10) }
            assertEquals(ExtensionSourceSaveState.Saved, states.last())
            assertEquals(listOf(source), fixture.applied.last().codexPluginMarketplaceSources)
            assertEquals("操作已取消", fixture.coordinator.statusMessage)
            assertNotNull(fixture.controller.state.snapshot.marketplaces[source.id]?.error)
        } finally { fixture.operations.cancel() }
    }

    @Test fun `busy rejection neither discards draft nor waits for the active extension service lock`() = runBlocking {
        val release = CountDownLatch(1)
        val started = CompletableDeferred<Unit>()
        val configurations = Configurations(AppConfig(codexPluginMarketplaceSources = listOf(source)))
        val fixture = Fixture(this, Dispatchers.IO, configurations, Commands {
            started.complete(Unit); check(release.await(3, TimeUnit.SECONDS)); CommandResult(1, "", "来源读取失败")
        })
        try {
            fixture.controller.refreshMarketplace(source)
            withTimeout(5_000) { started.await() }
            val states = mutableListOf<ExtensionSourceSaveState>()
            val before = System.nanoTime()
            assertFalse(fixture.controller.addMarketplace(source.copy(id = "new"), states::add))
            assertTrue(System.nanoTime() - before < 1_000_000_000L, "Busy rejection must not query a locked snapshot")
            assertIs<ExtensionSourceSaveState.Failed>(states.last())
            assertEquals(0, configurations.writes)
        } finally { release.countDown(); fixture.operations.cancel() }
    }

    @Test fun `new branch requests cancel old reads and do not occupy the mutation runner`() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val fixture = Fixture(this, Dispatchers.IO, commands = Commands { command ->
            if (command.last().endsWith("old.git")) {
                started.complete(Unit)
                try { Thread.sleep(60_000) } catch (_: InterruptedException) { }
                CommandResult(0, "old\trefs/heads/old\n", "")
            } else CommandResult(0, "new\trefs/heads/new\n", "")
        })
        val old = mutableListOf<RemoteBranchLoadState>()
        val current = mutableListOf<RemoteBranchLoadState>()
        try {
            fixture.controller.loadRemoteBranches("https://example.test/old.git", old::add)
            withTimeout(5_000) { started.await() }
            assertFalse(fixture.coordinator.busy)
            fixture.controller.loadRemoteBranches("https://example.test/new.git", current::add)
            withTimeout(5_000) { while (current.lastOrNull() !is RemoteBranchLoadState.Loaded) delay(10) }
            assertEquals(listOf<RemoteBranchLoadState>(RemoteBranchLoadState.Loading), old)
            assertEquals(RemoteBranchLoadState.Loaded(listOf("new")), current.last())
        } finally { fixture.controller.cancelRemoteBranches() }
    }

    @Test fun `cancelled queued reads leave loading with an explicit retryable state`() = runTest {
        val child = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        val fixture = Fixture(child, StandardTestDispatcher(testScheduler))
        val states = mutableListOf<RemoteBranchLoadState>()
        fixture.controller.loadRemoteBranches("https://example.test/main.git", states::add)
        child.cancel()
        advanceUntilIdle()
        assertIs<RemoteBranchLoadState.Failed>(states.last())
        assertFalse(fixture.coordinator.busy)
    }
}
