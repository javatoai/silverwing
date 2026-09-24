package com.snowball.silverwing.desktop

import androidx.compose.material3.Typography
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.font.createFontFamilyResolver
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.LayoutDirection
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.ParticipatedSprint
import com.snowball.silverwing.core.ParticipatedWorkItem
import com.snowball.silverwing.core.ParticipatedWorkItemsResult
import com.snowball.silverwing.core.ParticipatedWorkItemsSource
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.job
import kotlinx.coroutines.joinAll
import com.snowball.silverwing.core.RequirementPerson
import java.math.BigDecimal
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
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
    fun `selection uses the complete list and falls back only when the selected item is absent`() {
        val items = listOf(item("101", "结算优化"), item("202", "登录异常", "bug", "缺陷"))

        assertEquals(items[0], selectedParticipatedWorkItem(items, null))
        assertEquals(items[1], selectedParticipatedWorkItem(items, items[1].key))
        assertEquals(items[0], selectedParticipatedWorkItem(items, "removed"))
        assertEquals(null, selectedParticipatedWorkItem(emptyList(), items[1].key))
    }

    @Test
    fun `metadata formats all roles in order and joins multiple names`() {
        val item = item("1", "需求").copy(
            developers = listOf(RequirementPerson("张三"), RequirementPerson("赵六")),
            qcOwners = listOf(RequirementPerson("李四"), RequirementPerson("孙七")),
            productManagers = listOf(RequirementPerson("王五"), RequirementPerson("周八")),
        )

        assertEquals("研发：张三、赵六 · 测试：李四、孙七 · 产品：王五、周八", formatParticipatedWorkItemRoles(item))
    }

    @Test
    fun `metadata uses dashes for missing roles and email when a name is missing`() {
        assertEquals("研发：— · 测试：— · 产品：—", formatParticipatedWorkItemRoles(item("1", "需求")))
        assertEquals("—", formatRequirementPeople(listOf(RequirementPerson(" "), RequirementPerson("", " "))))
        assertEquals("张三、tester@example.com", formatRequirementPeople(listOf(
            RequirementPerson(" 张三 ", "developer@example.com"),
            RequirementPerson("", " tester@example.com "),
            RequirementPerson(" "),
        )))
    }

    @Test
    fun `long role text wraps completely at card and detail widths with native typography`() {
        val people = listOf("张三", "李四", "王五", "赵六", "孙七", "周八").map { RequirementPerson(it) }
        val roles = formatParticipatedWorkItemRoles(item("1", "需求").copy(
            developers = people, qcOwners = people, productManagers = people,
        ))
        val measurer = TextMeasurer(
            defaultFontFamilyResolver = createFontFamilyResolver(),
            defaultDensity = Density(1f),
            defaultLayoutDirection = LayoutDirection.Ltr,
        )

        listOf(240, 312, 600).forEach { width ->
            listOf(1f, 1.5f).forEach { fontScale ->
                val layout = measurer.measure(
                    text = roles,
                    style = Typography().bodySmall,
                    constraints = Constraints(maxWidth = width),
                    density = Density(1f, fontScale),
                )
                assertTrue(layout.lineCount > 1, "Expected wrapping at $width px and font scale $fontScale")
                assertTrue(!layout.hasVisualOverflow)
                assertEquals(roles.length, layout.getLineEnd(layout.lineCount - 1))
                assertTrue((0 until layout.lineCount).none(layout::isLineEllipsized))
            }
        }
    }

    @Test
    fun `estimate formatting strips trailing zeros without scientific notation`() {
        assertEquals("所选 Sprint 我的估分：2.5 天", formatMySprintEstimateDays(BigDecimal("2.500")))
        assertEquals("所选 Sprint 我的估分：1000 天", formatMySprintEstimateDays(BigDecimal("1000.00")))
        assertEquals("所选 Sprint 我的估分：0.0000001 天", formatMySprintEstimateDays(BigDecimal("0.00000010")))
    }

    @Test
    fun `estimate formatting distinguishes missing estimate from zero`() {
        assertEquals("所选 Sprint 我的估分：—", formatMySprintEstimateDays(null))
        assertEquals("所选 Sprint 我的估分：0 天", formatMySprintEstimateDays(BigDecimal.ZERO))
        assertEquals("所选 Sprint 我的估分：0 天", formatMySprintEstimateDays(BigDecimal("0.00")))
    }

    @Test
    fun `narrow window uses focused list and detail while wide window remains split`() {
        assertEquals(RequirementsPageLayout.FOCUSED, requirementsPageLayout(799f))
        assertEquals(RequirementsPageLayout.SPLIT, requirementsPageLayout(800f))
    }

    @Test
    fun `groups items into the fixed display order and omits empty groups`() {
        val items = listOf(
            item("1", "缺陷一", "bug", "缺陷"),
            item("2", "技改一", "technical", "技术改进"),
            item("3", "需求一", "userstory", "需求"),
        )

        val groups = participatedWorkItemGroups(items)

        assertEquals(listOf("userstory", "technical", "bug"), groups.map { it.type })
        assertEquals(listOf("User Story", "Tech Improvement", "Bug"), groups.map { it.label })
        assertEquals(listOf(listOf(items[2]), listOf(items[1]), listOf(items[0])), groups.map { it.items })
        assertEquals(listOf("缺陷", "技术改进", "需求"), items.map { it.typeLabel })
    }

    @Test
    fun `task group precedes bug group regardless of input order`() {
        val groups = participatedWorkItemGroups(
            listOf(item("1", "缺陷一", "bug", "缺陷"), item("2", "任务一", "othertask", "任务")),
        )

        assertEquals(listOf("othertask", "bug"), groups.map { it.type })
        assertEquals(listOf("Task", "Bug"), groups.map { it.label })
    }

    @Test
    fun `unknown types are appended in first appearance order`() {
        val groups = participatedWorkItemGroups(
            listOf(
                item("1", "需求一", "userstory", "需求"),
                item("2", "文档一", "techdoc", "技术文档"),
                item("3", "缺陷一", "bug", "缺陷"),
                item("4", "自定义一", "custom", "自定义类型"),
            ),
        )

        assertEquals(listOf("userstory", "bug", "techdoc", "custom"), groups.map { it.type })
        assertEquals(listOf("User Story", "Bug", "技术文档", "自定义类型"), groups.map { it.label })
    }

    @Test
    fun `empty input produces no groups`() {
        assertTrue(participatedWorkItemGroups(emptyList()).isEmpty())
    }

    @Test
    fun `estimate badge shows only the exact number and preserves unknown versus zero`() {
        assertEquals("1", formatMySprintEstimateNumber(BigDecimal("1.00")))
        assertEquals("1.5", formatMySprintEstimateNumber(BigDecimal("1.500")))
        assertEquals("0.3", formatMySprintEstimateNumber(BigDecimal("0.1") + BigDecimal("0.2")))
        assertEquals("1000", formatMySprintEstimateNumber(BigDecimal("1000.00")))
        assertEquals("0.0000001", formatMySprintEstimateNumber(BigDecimal("0.00000010")))
        assertEquals("0", formatMySprintEstimateNumber(BigDecimal.ZERO))
        assertEquals("—", formatMySprintEstimateNumber(null))
    }

    @Test
    fun `total estimate adds every item with exact decimal precision`() {
        val items = listOf("2", "1.5", "1").mapIndexed { index, days ->
            item(index.toString(), "需求").copy(mySprintEstimateDays = BigDecimal(days))
        }
        val state = ParticipatedWorkItemsUiState(items = items, initialized = true, listComplete = true)
        assertEquals("4.5", formatMySprintEstimateNumber(state.totalMySprintEstimateDays))
        assertEquals("0.3", formatMySprintEstimateNumber(state.copy(items = listOf(
            items[0].copy(mySprintEstimateDays = BigDecimal("0.1")),
            items[1].copy(mySprintEstimateDays = BigDecimal("0.2")),
        )).totalMySprintEstimateDays))
    }

    @Test
    fun `total estimate distinguishes a complete empty list from unavailable results`() {
        val ready = ParticipatedWorkItemsUiState(initialized = true, listComplete = true)
        assertEquals(BigDecimal.ZERO, ready.totalMySprintEstimateDays)
        assertEquals(null, ParticipatedWorkItemsUiState().totalMySprintEstimateDays)
        assertEquals(null, ready.copy(loading = true).totalMySprintEstimateDays)
        assertEquals(null, ready.copy(initialized = false).totalMySprintEstimateDays)
        assertEquals(null, ready.copy(listComplete = false).totalMySprintEstimateDays)
        assertEquals(null, ready.copy(error = "读取失败").totalMySprintEstimateDays)
    }

    @Test
    fun `total estimate never publishes a partial sum but allows role only warnings`() {
        val known = item("1", "需求").copy(mySprintEstimateDays = BigDecimal("2.00"))
        val state = ParticipatedWorkItemsUiState(items = listOf(known), initialized = true, listComplete = true)
        assertEquals("2", formatMySprintEstimateNumber(state.totalMySprintEstimateDays))
        assertEquals(null, state.copy(items = listOf(known, item("2", "未知估时"))).totalMySprintEstimateDays)
        assertEquals(null, state.copy(listComplete = false).totalMySprintEstimateDays)
        assertEquals("2", formatMySprintEstimateNumber(state.copy(
            items = listOf(known.copy(metadataWarnings = listOf("人员读取失败"))),
            warning = "部分人员或估分读取失败。人员读取失败",
        ).totalMySprintEstimateDays))
    }

    @Test
    fun `controller keeps prior results on refresh failure and reads body only for selected item`() = runBlocking {
        val first = item("101", "第一个")
        val second = item("202", "第二个")
        var queryCount = 0
        val requestedBodies = mutableListOf<String>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                queryCount++
                return if (queryCount == 1) sprintResult(listOf(first, second), completedQueries = 4)
                else sprintResult(emptyList(), listOf("network error"))
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
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) =
                sprintResult(listOf(item("101", "保留结果")), listOf("Bug 查询失败"), 3)
            override fun loadBody(item: ParticipatedWorkItem): String? = null
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)

        controller.load(listOf(MeegleProjectConfig("project-key", "obt")))
        await { controller.state.initialized }

        assertEquals(1, controller.state.items.size)
        assertTrue(controller.state.warning.orEmpty().contains("不完整"))
        assertTrue(!controller.state.listComplete)
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        assertEquals("", (controller.bodyState as ParticipatedWorkItemBodyState.Ready).content)
    }

    @Test
    fun `supplemental refresh failure publishes fresh items and invalidates bodies without losing selection`() = runBlocking {
        val first = item("101", "旧标题一").copy(mySprintEstimateDays = BigDecimal("2.5"))
        val selected = item("202", "旧标题二").copy(
            developers = listOf(RequirementPerson("旧研发")),
            mySprintEstimateDays = BigDecimal.ONE,
        )
        val stale = item("303", "已移除")
        val freshFirst = first.copy(title = "新标题一", mySprintEstimateDays = null)
        val firstWarning = "估分读取失败\n详细诊断"
        val freshSelected = selected.copy(
            title = "新标题二",
            developers = listOf(RequirementPerson("新研发")),
            mySprintEstimateDays = null,
            metadataWarnings = listOf(firstWarning, "另一个补充信息错误"),
        )
        val freshItems = listOf(freshFirst, item("404", "新增"), freshSelected)
        var queries = 0
        val requestedBodies = mutableListOf<String>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                queries++
                return sprintResult(
                    if (queries == 1) listOf(first, selected, stale) else freshItems,
                    completedQueries = 4,
                )
            }
            override fun loadBody(item: ParticipatedWorkItem): String {
                requestedBodies += item.key
                return item.title
            }
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))

        controller.load(projects)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        controller.select(selected)
        await { controller.bodyState == ParticipatedWorkItemBodyState.Ready(selected.key, selected.title) }

        controller.load(projects, force = true)
        await { !controller.state.loading && controller.bodyState == ParticipatedWorkItemBodyState.Ready(selected.key, freshSelected.title) }

        assertEquals(freshItems, controller.state.items)
        assertEquals(null, controller.state.items.last().mySprintEstimateDays)
        assertEquals("部分人员或估分读取失败。估分读取失败", controller.state.warning)
        assertEquals(null, controller.state.error)
        assertEquals(selected.key, controller.selectedKey)
        assertEquals(listOf(first.key, selected.key, selected.key), requestedBodies)

        controller.select(freshFirst)
        await { controller.bodyState == ParticipatedWorkItemBodyState.Ready(first.key, freshFirst.title) }
        assertEquals(listOf(first.key, selected.key, selected.key, first.key), requestedBodies)
    }

    @Test
    fun `supplemental warnings are cached on ordinary load and retried on force`() = runBlocking {
        val incompleteMetadata = item("101", "需求").copy(metadataWarnings = listOf("人员读取失败"))
        val recovered = incompleteMetadata.copy(
            developers = listOf(RequirementPerson("张三")),
            mySprintEstimateDays = BigDecimal.ZERO,
            metadataWarnings = emptyList(),
        )
        var queries = 0
        var bodyQueries = 0
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                queries++
                return sprintResult(
                    listOf(if (queries == 1) incompleteMetadata else recovered), completedQueries = 4,
                )
            }
            override fun loadBody(item: ParticipatedWorkItem): String {
                bodyQueries++
                return "正文 $bodyQueries"
            }
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))

        controller.load(projects)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        assertEquals("部分人员或估分读取失败。人员读取失败", controller.state.warning)
        controller.load(projects)
        yield()
        assertEquals(1, queries)
        assertEquals(1, bodyQueries)
        assertEquals(listOf(incompleteMetadata), controller.state.items)
        assertTrue(controller.state.initialized)
        assertTrue(!controller.state.loading)

        controller.load(projects, force = true)
        await { !controller.state.loading && controller.bodyState == ParticipatedWorkItemBodyState.Ready(recovered.key, "正文 2") }
        assertEquals(2, queries)
        assertEquals(2, bodyQueries)
        assertEquals(listOf(recovered), controller.state.items)
        assertEquals(null, controller.state.warning)
        assertEquals(null, controller.state.error)

        controller.load(projects)
        yield()
        assertEquals(2, queries)
        assertEquals(2, bodyQueries)
    }

    @Test
    fun `base partial refresh failure still retains old items and cached body`() = runBlocking {
        val old = item("101", "旧结果").copy(mySprintEstimateDays = BigDecimal.ONE)
        val fresh = old.copy(title = "部分新结果", mySprintEstimateDays = null, metadataWarnings = listOf("估分读取失败"))
        var queries = 0
        var bodyQueries = 0
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                queries++
                return if (queries == 1) sprintResult(listOf(old), completedQueries = 4)
                else sprintResult(listOf(fresh), listOf("Bug 查询失败"), completedQueries = 3)
            }
            override fun loadBody(item: ParticipatedWorkItem): String {
                bodyQueries++
                return item.title
            }
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))

        controller.load(projects)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        assertTrue(controller.state.listComplete)
        assertEquals(BigDecimal.ONE, controller.state.totalMySprintEstimateDays)
        controller.load(projects, force = true)
        await { !controller.state.loading }

        assertTrue(!controller.state.listComplete)
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        assertEquals(listOf(old), controller.state.items)
        assertEquals("部分项目或类型读取失败，已保留上次结果。Bug 查询失败", controller.state.warning)
        assertEquals(null, controller.state.error)
        assertEquals(old.key, controller.selectedKey)
        assertEquals(ParticipatedWorkItemBodyState.Ready(old.key, old.title), controller.bodyState)
        assertEquals(1, bodyQueries)
    }

    @Test
    fun `changing configured projects does not keep items from the previous project`() = runBlocking {
        var calls = 0
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                calls++
                return if (calls == 1) sprintResult(listOf(item("101", "旧项目")), completedQueries = 4)
                else sprintResult(emptyList(), listOf("新项目不可用"))
            }
            override fun loadBody(item: ParticipatedWorkItem) = "正文"
        }
        val controller = ParticipatedWorkItemsController(source, CoroutineScope(coroutineContext + SupervisorJob()), Dispatchers.Unconfined)

        controller.load(listOf(MeegleProjectConfig("old-project", "obt")))
        await { controller.state.initialized }
        controller.load(listOf(MeegleProjectConfig("new-project", "rta")))
        await { !controller.state.loading && controller.state.error != null }

        assertTrue(controller.state.items.isEmpty())
        assertTrue(controller.state.sprints.isEmpty())
        assertEquals(null, controller.state.selectedSprintKey)
        assertEquals(null, controller.selectedKey)
        assertEquals(ParticipatedWorkItemBodyState.Idle, controller.bodyState)
    }

    @Test
    fun `Sprint switching isolates estimates and bodies even for the same work item`() = runBlocking {
        val requests = mutableListOf<String?>()
        val bodies = mutableListOf<String>()
        val currentItem = item("101", "本期正文").copy(mySprintEstimateDays = BigDecimal.ONE)
        val futureItem = currentItem.copy(title = "下期正文", mySprintEstimateDays = BigDecimal("2.5"))
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                requests += sprintKey
                val selected = sprintKey ?: currentSprint.key
                return sprintResult(
                    listOf(if (selected == currentSprint.key) currentItem else futureItem),
                    completedQueries = 4,
                    selectedSprintKey = selected,
                )
            }
            override fun loadBody(item: ParticipatedWorkItem): String {
                bodies += item.title
                return item.title
            }
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))

        controller.load(projects)
        await { controller.bodyState == ParticipatedWorkItemBodyState.Ready(currentItem.key, currentItem.title) }
        assertEquals(currentSprint.key, controller.state.selectedSprintKey)
        assertEquals(BigDecimal.ONE, controller.state.totalMySprintEstimateDays)
        controller.load(projects)
        yield()
        assertEquals(listOf<String?>(null), requests)

        controller.load(projects, sprintKey = futureSprint.key)
        assertTrue(controller.state.items.isEmpty())
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        assertEquals(ParticipatedWorkItemBodyState.Idle, controller.bodyState)
        await { controller.bodyState == ParticipatedWorkItemBodyState.Ready(futureItem.key, futureItem.title) }
        assertEquals(listOf(futureItem), controller.state.items)
        assertEquals(BigDecimal("2.5"), controller.state.totalMySprintEstimateDays)
        controller.load(projects, force = true)
        await { !controller.state.loading && bodies.size == 3 }
        assertEquals(listOf(null, futureSprint.key, futureSprint.key), requests)

        controller.load(projects, sprintKey = currentSprint.key)
        await { !controller.state.loading && bodies.size == 4 }
        assertEquals(listOf(currentItem.title, futureItem.title, futureItem.title, currentItem.title), bodies)
        assertEquals(BigDecimal.ONE, controller.state.totalMySprintEstimateDays)
    }

    @Test
    fun `failed Sprint switch never retains the previous Sprint list or body`() = runBlocking {
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) =
                if (sprintKey == futureSprint.key) ParticipatedWorkItemsResult(emptyList(), listOf("network error"))
                else sprintResult(listOf(item("101", "旧结果")), completedQueries = 4)
            override fun loadBody(item: ParticipatedWorkItem) = "旧正文"
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))
        controller.load(projects)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        controller.load(projects, sprintKey = futureSprint.key)
        await { !controller.state.loading }

        assertTrue(controller.state.items.isEmpty())
        assertEquals(listOf(currentSprint, futureSprint), controller.state.sprints)
        assertEquals(futureSprint.key, controller.state.selectedSprintKey)
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        assertEquals(null, controller.selectedKey)
        assertEquals(ParticipatedWorkItemBodyState.Idle, controller.bodyState)
        assertNotNull(controller.state.error)
        assertTrue(!controller.state.error.orEmpty().contains("保留"))
    }

    @Test
    fun `removed Sprint clears selection and body while retaining remaining choices`() = runBlocking {
        var calls = 0
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                calls++
                return if (calls == 1) sprintResult(listOf(item("101", "旧结果")), completedQueries = 4)
                else ParticipatedWorkItemsResult(
                    emptyList(), listOf("所选 Sprint 已不可选，请重新选择"), sprints = listOf(futureSprint),
                )
            }
            override fun loadBody(item: ParticipatedWorkItem) = "旧正文"
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))
        controller.load(projects)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        controller.load(projects, force = true)
        await { !controller.state.loading }

        assertEquals(listOf(futureSprint), controller.state.sprints)
        assertTrue(controller.state.items.isEmpty())
        assertEquals(null, controller.state.selectedSprintKey)
        assertEquals(null, controller.selectedKey)
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        assertEquals(ParticipatedWorkItemBodyState.Idle, controller.bodyState)
    }

    @Test
    fun `no default Sprint has unknown total while an explicitly selected empty Sprint totals zero`() = runBlocking {
        val requests = mutableListOf<String?>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                requests += sprintKey
                return ParticipatedWorkItemsResult(
                    emptyList(), completedQueries = if (sprintKey == null) 0 else 4,
                    sprints = listOf(futureSprint), selectedSprintKey = sprintKey,
                )
            }
            override fun loadBody(item: ParticipatedWorkItem): String = error("No selected item")
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))
        controller.load(projects)
        await { !controller.state.loading }
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        assertEquals("请选择要查看的 Sprint", requirementsEmptyMessage(controller.state))
        controller.load(projects, sprintKey = futureSprint.key)
        await { !controller.state.loading }
        assertEquals(BigDecimal.ZERO, controller.state.totalMySprintEstimateDays)
        assertEquals("该 Sprint 中没有我参与的工作项", requirementsEmptyMessage(controller.state))
        assertEquals(listOf(null, futureSprint.key), requests)

        val explicit = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        explicit.load(projects, sprintKey = futureSprint.key)
        await { !explicit.state.loading }
        assertEquals(futureSprint.key, explicit.state.selectedSprintKey)
        assertEquals(futureSprint.key, requests.last())
        assertEquals("没有进行中或未开始的可选 Sprint", requirementsEmptyMessage(ParticipatedWorkItemsUiState()))
    }

    @Test
    fun `a cancelled late Sprint result cannot replace the latest selection`() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val returned = CompletableDeferred<Unit>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?): ParticipatedWorkItemsResult {
                if (sprintKey == currentSprint.key) {
                    withContext(NonCancellable) {
                        started.complete(Unit)
                        release.await()
                        returned.complete(Unit)
                    }
                }
                return sprintResult(
                    listOf(item("101", sprintKey.orEmpty())), completedQueries = 4, selectedSprintKey = sprintKey,
                )
            }
            override fun loadBody(item: ParticipatedWorkItem) = item.title
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))
        controller.load(projects, sprintKey = currentSprint.key)
        started.await()
        controller.load(projects, sprintKey = futureSprint.key)
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        release.complete(Unit)
        returned.await()
        yield()

        assertEquals(futureSprint.key, controller.state.selectedSprintKey)
        assertEquals(futureSprint.key, controller.state.items.single().title)
        assertEquals(
            ParticipatedWorkItemBodyState.Ready(controller.state.items.single().key, futureSprint.key),
            controller.bodyState,
        )
    }

    @Test
    fun `late body for the same item cannot overwrite the newly selected Sprint body`() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val release = CountDownLatch(1)
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?) = sprintResult(
                listOf(item("101", sprintKey ?: currentSprint.key)), completedQueries = 4,
                selectedSprintKey = sprintKey ?: currentSprint.key,
            )
            override fun loadBody(item: ParticipatedWorkItem): String {
                if (item.title == currentSprint.key) {
                    started.complete(Unit)
                    check(release.await(5, TimeUnit.SECONDS))
                }
                return item.title
            }
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.IO)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))
        try {
            controller.load(projects)
            withTimeout(2_000) { started.await() }
            controller.load(projects, sprintKey = futureSprint.key)
            await {
                controller.bodyState == ParticipatedWorkItemBodyState.Ready(item("101", "").key, futureSprint.key)
            }
        } finally {
            release.countDown()
        }
        coroutineContext.job.children.toList().joinAll()
        assertEquals(
            ParticipatedWorkItemBodyState.Ready(item("101", "").key, futureSprint.key), controller.bodyState,
        )
        assertEquals(futureSprint.key, controller.state.selectedSprintKey)
    }

    @Test
    fun `default project change reloads an unselected catalog and preserves an existing selection`() = runBlocking {
        val requests = mutableListOf<Pair<String?, String?>>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(
                projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?,
            ): ParticipatedWorkItemsResult {
                requests += sprintKey to defaultSprintProjectKey
                val selected = sprintKey ?: currentSprint.key.takeIf { defaultSprintProjectKey == "project-key" }
                return sprintResult(emptyList(), completedQueries = if (selected == null) 0 else 4,
                    selectedSprintKey = selected)
            }
            override fun loadBody(item: ParticipatedWorkItem): String = error("No item")
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"), MeegleProjectConfig("other-project", "rta"))
        controller.load(projects)
        await { !controller.state.loading }
        assertEquals(null, controller.state.selectedSprintKey)
        controller.load(projects)
        yield()
        assertEquals(1, requests.size)

        controller.load(projects, defaultSprintProjectKey = "project-key")
        await { !controller.state.loading }
        assertEquals(currentSprint.key, controller.state.selectedSprintKey)
        assertEquals(null to "project-key", requests.last())
        assertEquals(BigDecimal.ZERO, controller.state.totalMySprintEstimateDays)
        controller.load(projects, defaultSprintProjectKey = "project-key")
        yield()
        assertEquals(2, requests.size)

        controller.load(projects, sprintKey = futureSprint.key, defaultSprintProjectKey = "project-key")
        await { !controller.state.loading }
        controller.load(projects, defaultSprintProjectKey = "other-project")
        await { !controller.state.loading }
        assertEquals(futureSprint.key to "other-project", requests.last())
        assertEquals(futureSprint.key, controller.state.selectedSprintKey)
        controller.load(projects, force = true, defaultSprintProjectKey = "other-project")
        await { !controller.state.loading }
        assertEquals(futureSprint.key to "other-project", requests.last())
        assertEquals(5, requests.size)

        controller.load(projects, defaultSprintProjectKey = null)
        await { !controller.state.loading }
        assertEquals(futureSprint.key to null, requests.last())
        assertEquals(futureSprint.key, controller.state.selectedSprintKey)
    }

    @Test
    fun `changing default project cancels an in flight automatic choice`() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val returned = CompletableDeferred<Unit>()
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(
                projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?,
            ): ParticipatedWorkItemsResult {
                assertEquals(null, sprintKey)
                if (defaultSprintProjectKey == "old-project") {
                    withContext(NonCancellable) {
                        started.complete(Unit)
                        release.await()
                        returned.complete(Unit)
                    }
                }
                val selected = if (defaultSprintProjectKey == "old-project") currentSprint else futureSprint
                return sprintResult(listOf(item("101", defaultSprintProjectKey.orEmpty())),
                    completedQueries = 4, selectedSprintKey = selected.key)
            }
            override fun loadBody(item: ParticipatedWorkItem) = item.title
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        val projects = listOf(MeegleProjectConfig("project-key", "obt"))
        controller.load(projects, defaultSprintProjectKey = "old-project")
        started.await()
        controller.load(projects, defaultSprintProjectKey = "new-project")
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        release.complete(Unit)
        returned.await()
        coroutineContext.job.children.toList().joinAll()

        assertEquals(futureSprint.key, controller.state.selectedSprintKey)
        assertEquals("new-project", controller.state.items.single().title)
        assertEquals(ParticipatedWorkItemBodyState.Ready(item("101", "").key, "new-project"), controller.bodyState)
    }

    @Test
    fun `removing all projects clears the catalog selection and total`() = runBlocking {
        val source = object : ParticipatedWorkItemsSource {
            override suspend fun load(
                projects: List<MeegleProjectConfig>, sprintKey: String?, defaultSprintProjectKey: String?,
            ) = if (projects.isEmpty()) ParticipatedWorkItemsResult(emptyList(), sprints = emptyList())
                else sprintResult(listOf(item("101", "旧结果").copy(mySprintEstimateDays = BigDecimal.ONE)),
                    completedQueries = 4)
            override fun loadBody(item: ParticipatedWorkItem) = item.title
        }
        val controller = ParticipatedWorkItemsController(source, this, Dispatchers.Unconfined)
        controller.load(listOf(MeegleProjectConfig("project-key", "obt")), defaultSprintProjectKey = "project-key")
        await { controller.bodyState is ParticipatedWorkItemBodyState.Ready }
        controller.load(emptyList())
        await { !controller.state.loading }

        assertTrue(controller.state.items.isEmpty())
        assertTrue(controller.state.sprints.isEmpty())
        assertEquals(null, controller.state.selectedSprintKey)
        assertEquals(null, controller.state.totalMySprintEstimateDays)
        assertEquals(null, controller.selectedKey)
        assertEquals(ParticipatedWorkItemBodyState.Idle, controller.bodyState)
    }

    private val currentSprint = ParticipatedSprint("project-key", "obt", "777", "Current Sprint", "进行中")
    private val futureSprint = ParticipatedSprint("project-key", "obt", "888", "Next Sprint", "未开始")

    private fun sprintResult(
        items: List<ParticipatedWorkItem>,
        failures: List<String> = emptyList(),
        completedQueries: Int = 0,
        selectedSprintKey: String? = currentSprint.key,
    ) = ParticipatedWorkItemsResult(
        items, failures, completedQueries,
        sprints = if (completedQueries == 0 && failures.isNotEmpty()) null else listOf(currentSprint, futureSprint),
        selectedSprintKey = selectedSprintKey,
    )

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
