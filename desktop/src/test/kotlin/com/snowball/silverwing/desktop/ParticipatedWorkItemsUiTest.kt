package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsResult
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.yield
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class ParticipatedWorkItemsUiTest {
    @Test
    fun `search matches title type project id and link`() {
        val items = listOf(item("101", "结算优化"), item("202", "登录异常", "bug", "缺陷"))

        assertEquals(items, filterParticipatedWorkItems(items, " "))
        assertEquals(listOf(items[0]), filterParticipatedWorkItems(items, "结算"))
        assertEquals(listOf(items[1]), filterParticipatedWorkItems(items, "缺陷"))
        assertEquals(listOf(items[1]), filterParticipatedWorkItems(items, "202"))
        assertEquals(items, filterParticipatedWorkItems(items, "obt"))
        assertEquals(listOf(items[1]), filterParticipatedWorkItems(items, "/bug/"))
        assertEquals(items[0], selectedParticipatedWorkItem(items, null))
        assertEquals(items[1], selectedParticipatedWorkItem(items, items[1].key))
    }

    @Test
    fun `narrow window uses focused list and detail while wide window remains split`() {
        assertEquals(RequirementsPageLayout.FOCUSED, requirementsPageLayout(799f))
        assertEquals(RequirementsPageLayout.SPLIT, requirementsPageLayout(800f))
    }

    @Test
    fun `controller keeps prior results on refresh failure and reads body only for selected item`() = runBlocking {
        val first = item("101", "第一个")
        val second = item("202", "第二个")
        var queryCount = 0
        val requestedBodies = mutableListOf<String>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>): ParticipatedWorkItemsResult {
                queryCount++
                return if (queryCount == 1) ParticipatedWorkItemsResult(listOf(first, second), completedQueries = 4)
                else ParticipatedWorkItemsResult(emptyList(), listOf("network error"))
            }

            override fun loadBody(item: ParticipatedWorkItem): String? {
                requestedBodies += item.key
                return "# ${item.title}"
            }
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))

        controller.load(projects)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        assertEquals(first.key, controller.selectedKey)
        assertEquals(listOf(first.key), requestedBodies)
        controller.select(second)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready && controller.selectedKey == second.key }
        assertEquals(listOf(first.key, second.key), requestedBodies)

        controller.load(projects, force = true)
        await { !controller.state.loading }
        assertEquals(listOf(first, second), controller.state.items)
        assertNotNull(controller.state.error)
        assertEquals(second.key, controller.selectedKey)
        controller.select(first)
        assertTrue(controller.bodyState is ParticipatedWorkItemBodyState.Ready)
        assertEquals(listOf(first.key, second.key), requestedBodies)
    }

    @Test
    fun `partial load exposes successful items and an incomplete warning`() = runBlocking {
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>) =
                ParticipatedWorkItemsResult(listOf(item("101", "保留结果")), listOf("Bug 查询失败"), 3)
            override fun loadBody(item: ParticipatedWorkItem): String? = null
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)

        controller.load(listOf(MeegleProjectConfig("project-key", "obt")))
        await { controller.state.initialized }

        assertEquals(1, controller.state.items.size)
        assertTrue(controller.state.warning.orEmpty().contains("不完整"))
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        assertEquals("", (controller.bodyState as ParticipatedWorkItemBodyState.Ready).content)
    }

    @Test
    fun `changing configured projects does not keep items from the previous project`() = runBlocking {
        var calls = 0
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>): ParticipatedWorkItemsResult {
                calls++
                return if (calls == 1) ParticipatedWorkItemsResult(listOf(item("101", "旧项目")), completedQueries = 4)
                else ParticipatedWorkItemsResult(emptyList(), listOf("新项目不可用"))
            }
            override fun loadBody(item: ParticipatedWorkItem) = "正文"
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)

        controller.load(listOf(MeegleProjectConfig("old-project", "obt")))
        await { controller.state.initialized }
        controller.load(listOf(MeegleProjectConfig("new-project", "rta")))
        await { !controller.state.loading && controller.state.error != null }

        assertTrue(controller.state.items.isEmpty())
        assertEquals(null, controller.selectedKey)
    }

    private suspend fun await(predicate: () -> Boolean) {
        withTimeout(2_000) { while (!predicate()) yield() }
    }

    private fun item(id: String, title: String, type: String = "userstory", label: String = "需求") =
        ParticipatedWorkItem(
            projectKey = "project-key",
            projectName = "obt",
            type = type,
            typeLabel = label,
            id = id,
            title = title,
            url = "https://project.feishu.cn/obt/$type/detail/$id",
        )
}
