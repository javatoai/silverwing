package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.resetMain
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.*

class ReadingOptimizationTest {
    @TempDir lateinit var root: Path
    @Test fun `reading cache restores task tab selected file directories and both text and PDF positions`() {
        val session = TaskBrowsingSession()
        session.view = TaskContentView.MATERIALS
        val task = root.resolve("task").toString(); val materials = root.resolve("materials").toString()
        val state = session.materialsFor(task, materials)
        state.selectedPath.value = "研发/说明.pdf"
        state.expandedDirectories.value = setOf("研发", "研发/附录")
        state.expansionInitialized.value = true
        state.directoryCollapsed.value = true
        state.directoryPosition.listPositions["directory"] = MaterialsListPosition(8, 50)
        state.readingFor("研发/说明.pdf").apply {
            zoomPercent.intValue = 175; listPositions["pdf-pages"] = MaterialsListPosition(5, 140); scrollPositions["pdf-horizontal"] = 200
        }
        session.requirementFor(task, "https://example.test/1").apply { mode.value = MarkdownPreviewMode.SOURCE; scrollPositions["source-vertical"] = 800 }
        val store = ReadingStateStore(root.resolve("reading.json"))
        store.save(session.snapshot(task))
        val restored = TaskBrowsingSession().apply { restore(store.load()) }
        assertEquals(task, store.load().lastTaskPath)
        assertEquals(TaskContentView.MATERIALS, restored.view)
        val value = restored.materialsFor(task, materials)
        assertEquals(state.expandedDirectories.value, value.expandedDirectories.value)
        assertEquals("研发/说明.pdf", value.selectedPath.value)
        assertTrue(value.directoryCollapsed.value)
        assertEquals(175, value.readingFor("研发/说明.pdf").zoomPercent.intValue)
        assertEquals(MaterialsListPosition(5, 140), value.readingFor("研发/说明.pdf").listPositions["pdf-pages"])
        assertEquals(200, value.readingFor("研发/说明.pdf").scrollPositions["pdf-horizontal"])
        assertEquals(800, restored.requirementFor(task, "https://example.test/1").scrollPositions["source-vertical"])
        assertEquals(MarkdownPreviewMode.SOURCE, restored.requirementFor(task, "https://example.test/1").mode.value)
        assertNull(restored.materialsFor(task, root.resolve("new-root").toString()).selectedPath.value)
        assertTrue(restored.requirementFor(task, "https://example.test/2").scrollPositions.isEmpty())
    }
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    @Test fun `application startup resumes the saved task and tab without changing configuration`() {
        val tasksRoot = Files.createDirectory(root.resolve("tasks"))
        val tasks = listOf("first", "second").map { name -> TaskManifest(folderName = name, taskDirectoryName = name,
            featureBranch = "feat/$name", createdAt = "now", updatedAt = "now", services = emptyList()) }
        tasks.forEach { ManifestStore().save(tasksRoot.resolve(it.taskDirectoryName), it) }
        val paths = ApplicationPaths(root.resolve("application"))
        val config = ConfigStore(paths).also { it.save(AppConfig(taskRoot = tasksRoot.toString(), aiRequirementNamingEnabled = false)) }
        val state = ReadingStateStore(paths.cache.resolve("reading-state.json"))
        state.save(ReadingSnapshot(lastTaskPath = tasksRoot.resolve("second").toAbsolutePath().normalize().toString(), view = "MATERIALS"))
        val dispatcher = kotlinx.coroutines.test.StandardTestDispatcher()
        Dispatchers.setMain(dispatcher)
        try {
            DesktopApplication(paths = paths, configStore = config, ioDispatcher = dispatcher,
                developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) }).use { app ->
                dispatcher.scheduler.runCurrent()
                assertEquals("second", app.selectedTask!!.folderName)
                assertEquals(TaskContentView.MATERIALS, app.taskBrowsingSession.view)
            }
        } finally {
            Dispatchers.resetMain()
        }
        assertEquals("MATERIALS", state.load().view)
        assertEquals(tasksRoot.toString(), config.load().taskRoot)
    }
    @Test fun `corrupt unsupported and oversized reading caches do not block startup`() {
        val path = root.resolve("reading.json"); val store = ReadingStateStore(path)
        for (content in listOf("[unfinished", "{\"schema\":9}", "x".repeat(4_000_001))) {
            Files.writeString(path, content); assertEquals(ReadingSnapshot(), store.load())
        }
        assertTrue(Files.list(root).use { paths -> paths.noneMatch { it.fileName.toString().endsWith(".tmp") } })
    }
    @Test fun `literal find preserves Unicode and does not treat query as a regular expression`() {
        assertEquals(listOf(0..1, 3..4), literalMatches("支付 支付", "支付"))
        assertEquals(listOf(0..2, 4..6), literalMatches("PDF pdf", "PdF"))
        assertEquals(listOf(0..1), literalMatches("[] abc", "[]"))
        assertEquals(emptyList(), literalMatches("anything", ""))
        val find = DocumentFindState()
        find.blocks["b"] = FindBlock("支付", 2) {}
        find.blocks["a"] = FindBlock("支付 支付", 1) {}
        find.change("支付"); assertEquals(3, find.hits.size); assertEquals("a", find.current!!.block)
        find.move(-1); assertEquals("b", find.current!!.block)
        find.close(); assertEquals(0, find.hits.size)
    }
    @Test fun `prewarming pauses resumes prioritizes selection and uses durable cache`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val started = CountDownLatch(1); val release = CountDownLatch(1); val calls = CopyOnWriteArrayList<String>()
        val items = (123..125).map { ParticipatedWorkItem("obt", "项目", "story", "需求", "$it", "标题$it", "https://project.feishu.cn/obt/userstory/detail/$it") }
        val coordinator = RequirementAiNamingCoordinator(scope, LocalRequirementAiNamingCache(root.resolve("names.json")),
            RequirementAiContextProvider { link, _ ->
                val id = link.substringAfterLast('/'); calls += id
                if (id == "123") { started.countDown(); check(release.await(5, TimeUnit.SECONDS)) }
                RequirementAiContext("标题", "正文")
            }, object : RequirementAiNamingService { override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) = RequirementAiNamingSuggestion("支付优化", "payment_fix") }, { "gpt-6-luna" })
        try {
            coordinator.prewarm(items, true)
            assertTrue(started.await(5, TimeUnit.SECONDS)); coordinator.pause(); coordinator.prioritize(items.last()); release.countDown()
            withTimeout(5_000) { while (coordinator.state.value.running) delay(10) }
            assertEquals(listOf("123"), calls.toList()); assertTrue(coordinator.state.value.paused)
            coordinator.resume()
            withTimeout(5_000) { while (coordinator.state.value.running) delay(10) }
            assertEquals(listOf("123", "125", "124"), calls.toList())
            assertEquals(3, coordinator.state.value.completed)
            assertTrue(coordinator.suggestWithSource(items[0].url, "obt").cached)
        } finally { release.countDown(); scope.cancel() }
    }
    @Test fun `individual failed prewarming can retry without resetting completed work`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val failing = AtomicBoolean(true)
        val item = ParticipatedWorkItem("obt", "项目", "story", "需求", "123", "失败项", "https://project.feishu.cn/obt/userstory/detail/123")
        val coordinator = RequirementAiNamingCoordinator(scope, LocalRequirementAiNamingCache(root.resolve("names.json")),
            RequirementAiContextProvider { _, _ -> if (failing.get()) error("正文读取失败") else RequirementAiContext("标题", "") },
            object : RequirementAiNamingService { override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) = RequirementAiNamingSuggestion("支付优化", "payment_fix") }, { "gpt-6-luna" })
        try {
            coordinator.prewarm(listOf(item), true)
            withTimeout(5_000) { while (coordinator.state.value.running) delay(10) }
            assertEquals("失败项", coordinator.state.value.failedItems.single().title)
            failing.set(false); coordinator.retry(item.key)
            withTimeout(5_000) { while (coordinator.state.value.retrying.isNotEmpty()) delay(10) }
            assertEquals(0, coordinator.state.value.failures); assertEquals(1, coordinator.state.value.completed)
        } finally { scope.cancel() }
    }
}
