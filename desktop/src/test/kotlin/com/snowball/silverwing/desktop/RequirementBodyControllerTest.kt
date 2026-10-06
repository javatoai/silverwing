package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.nio.file.Path
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class RequirementBodyControllerTest {
    private fun item(id: String) = ParticipatedWorkItem("project", "OBT", "userstory", "需求", id,
        "需求 $id", "https://project.feishu.cn/obt/userstory/detail/$id")

    @Test fun `task reader works without loading list and shares body cache without changing list selection`() = runTest {
        val calls = mutableListOf<String>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult =
                error("Task body must not query the requirements list")
            override fun loadBody(item: ParticipatedWorkItem): String { calls += item.id; return "# 正文 ${item.id}" }
        }
        val dispatcher = StandardTestDispatcher(testScheduler)
        val repository = RequirementBodyRepository(source)
        val catalog = ParticipatedWorkItemsController(source, backgroundScope, dispatcher, repository)
        val reader = RequirementBodyController(repository, backgroundScope, dispatcher)
        catalog.select(item("1")); runCurrent()
        reader.select(item("2")); runCurrent()
        assertEquals(item("1").key, catalog.selectedKey)
        assertFalse(catalog.state.initialized)
        assertEquals(ParticipatedWorkItemBodyState.Ready(item("2").key, "# 正文 2"), reader.bodyState)
        reader.select(item("1")); runCurrent()
        assertEquals(listOf("1", "2"), calls)
        reader.select(item("1"), force = true); runCurrent()
        assertEquals(listOf("1", "2", "1"), calls)
        assertEquals(item("1").key, catalog.selectedKey)
    }

    @Test fun `empty content is successful and failed body can retry`() = runTest {
        var fail = true
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem): String { if (fail) error("权限不足"); return "" }
        }
        val reader = RequirementBodyController(RequirementBodyRepository(source), backgroundScope, StandardTestDispatcher(testScheduler))
        reader.select(item("1")); runCurrent()
        assertEquals(ParticipatedWorkItemBodyState.Failed(item("1").key, "权限不足"), reader.bodyState)
        fail = false
        reader.select(item("1"), force = true); runCurrent()
        assertEquals(ParticipatedWorkItemBodyState.Ready(item("1").key, ""), reader.bodyState)
        reader.clear()
        assertEquals(ParticipatedWorkItemBodyState.Idle, reader.bodyState)
    }

    @Test fun `source cancellation leaves current body retryable while reader scope stays active`() = runTest {
        var cancelled = true
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (cancelled) throw CancellationException("source request stopped")
                return "完整正文"
            }
        }
        val reader = RequirementBodyController(RequirementBodyRepository(source), backgroundScope, StandardTestDispatcher(testScheduler))
        reader.select(item("1")); runCurrent()
        assertIs<ParticipatedWorkItemBodyState.Failed>(reader.bodyState)
        assertTrue(backgroundScope.isActive)
        cancelled = false
        reader.select(item("1"), force = true); runCurrent()
        assertEquals(ParticipatedWorkItemBodyState.Ready(item("1").key, "完整正文"), reader.bodyState)
        reader.clear()
        assertEquals(ParticipatedWorkItemBodyState.Idle, reader.bodyState)
    }

    @Test fun `source image cancellation preserves body and enables individual retry`() = runTest {
        val url = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/cancelled-image"
        val downloads = mutableListOf<Boolean>()
        var bodyReads = 0
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem): String { bodyReads++; return "# 正文\n\n![图片]($url)" }
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                downloads += retry
                if (!retry) throw CancellationException("source download stopped")
                return Path.of("retried.png")
            }
        }
        val reader = RequirementBodyController(RequirementBodyRepository(source), backgroundScope, StandardTestDispatcher(testScheduler))
        reader.select(item("1")); runCurrent()
        assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState)
        assertIs<ParticipatedWorkItemImageState.Failed>(reader.bodyImageStates[url])
        assertTrue(backgroundScope.isActive)
        reader.retryBodyImage(item("1"), url); runCurrent()
        assertEquals(ParticipatedWorkItemImageState.Loaded(Path.of("retried.png")), reader.bodyImageStates[url])
        assertEquals(listOf(false, true), downloads)
        assertEquals(1, bodyReads)
        reader.clear()
        assertTrue(reader.bodyImageStates.isEmpty())
    }

    @Test fun `switch cancels blocked read and late result cannot replace new body`() = runBlocking {
        val started = CountDownLatch(1)
        val interrupted = CountDownLatch(1)
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (item.id == "1") {
                    started.countDown()
                    try { Thread.sleep(10_000) } catch (_: InterruptedException) { interrupted.countDown() }
                }
                return "正文 ${item.id}"
            }
        }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
        try {
            val reader = RequirementBodyController(RequirementBodyRepository(source), scope, Dispatchers.IO)
            reader.select(item("1"))
            assertTrue(started.await(2, TimeUnit.SECONDS))
            reader.select(item("2"))
            withTimeout(3000) { while (reader.bodyState !is ParticipatedWorkItemBodyState.Ready) delay(10) }
            assertTrue(interrupted.await(2, TimeUnit.SECONDS))
            assertEquals(ParticipatedWorkItemBodyState.Ready(item("2").key, "正文 2"), reader.bodyState)
        } finally { scope.cancel() }
    }

    @Test fun `task link uses configured project and unsupported links do not create a request`() {
        val task = TaskManifest(folderName = "支付任务", taskDirectoryName = "支付任务", featureBranch = "feature/pay", createdAt = "2026-10-01", updatedAt = "2026-10-01",
            services = emptyList(), requirementLink = "https://project.feishu.cn/custom/userstory/detail/7118490426")
        val config = AppConfig(meegleProjects = listOf(MeegleProjectConfig("custom-key", "custom")))
        val resolved = assertNotNull(taskRequirementItem(task, config, "支付渠道优化"))
        assertEquals("custom-key", resolved.projectKey)
        assertEquals("7118490426", resolved.id)
        assertEquals("支付渠道优化", resolved.title)
        assertNull(taskRequirementItem(task.copy(requirementLink = ""), config, null))
        assertNull(taskRequirementItem(task.copy(requirementLink = "https://example.com/需求"), config, null))
        assertNull(taskRequirementItem(task, AppConfig(), null))
    }

    @Test fun `image failure preserves body and retry only downloads the failed image`() = runTest {
        val url = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/image-token"
        var bodyReads = 0
        val retries = mutableListOf<Boolean>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem): String { bodyReads++; return "# 正文\n\n![示意图]($url)" }
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                assertEquals(url, fileUrl)
                retries += retry
                if (!retry) error("图片权限不足")
                return Path.of("image.png")
            }
        }
        val reader = RequirementBodyController(RequirementBodyRepository(source), backgroundScope, StandardTestDispatcher(testScheduler))
        reader.select(item("1")); runCurrent()
        assertIs<ParticipatedWorkItemBodyState.Ready>(reader.bodyState)
        assertEquals(ParticipatedWorkItemImageState.Failed("图片权限不足"), reader.bodyImageStates[url])
        reader.retryBodyImage(item("1"), url); runCurrent()
        assertEquals(ParticipatedWorkItemImageState.Loaded(Path.of("image.png")), reader.bodyImageStates[url])
        assertEquals(listOf(false, true), retries)
        assertEquals(1, bodyReads)
    }

    @Test fun `switching work items cancels a blocked image without publishing its old result`() = runBlocking {
        val oldUrl = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/old-image"
        val newUrl = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/new-image"
        val started = CountDownLatch(1)
        val interrupted = CountDownLatch(1)
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = ParticipatedWorkItemsResult(emptyList())
            override fun loadBody(item: ParticipatedWorkItem) = "![正文图片](${if (item.id == "1") oldUrl else newUrl})"
            override fun downloadBodyImage(item: ParticipatedWorkItem, fileUrl: String, retry: Boolean): Path {
                if (fileUrl == oldUrl) {
                    started.countDown()
                    try { Thread.sleep(10_000) } catch (_: InterruptedException) { interrupted.countDown() }
                    return Path.of("old.png")
                }
                return Path.of("new.png")
            }
        }
        val scope = CoroutineScope(coroutineContext + SupervisorJob())
        try {
            val reader = RequirementBodyController(RequirementBodyRepository(source), scope, Dispatchers.IO)
            reader.select(item("1"))
            withTimeout(3_000) { while (started.count > 0) delay(10) }
            reader.select(item("2"))
            withTimeout(3_000) { while (reader.bodyImageStates[newUrl] !is ParticipatedWorkItemImageState.Loaded) delay(10) }
            assertTrue(interrupted.await(2, TimeUnit.SECONDS))
            assertEquals(mapOf(newUrl to ParticipatedWorkItemImageState.Loaded(Path.of("new.png"))), reader.bodyImageStates)
            assertEquals(item("2").key, (reader.bodyState as ParticipatedWorkItemBodyState.Ready).itemKey)
        } finally { scope.cancel() }
    }
}
