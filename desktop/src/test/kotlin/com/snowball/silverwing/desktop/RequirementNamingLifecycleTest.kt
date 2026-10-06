package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.test.*
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
class RequirementNamingLifecycleTest {
    private val link = "https://project.feishu.cn/obt/userstory/detail/123"
    private val item = ParticipatedWorkItem("obt", "项目", "story", "需求", "123", "支付优化", link)
    private val suggestion = RequirementAiNamingSuggestion("支付优化", "payment_fix")
    private class MemoryCache : RequirementAiNamingCache {
        val entries = mutableMapOf<Pair<String, String>, RequirementAiNamingSuggestion>()
        override fun read(identity: String, model: String) = entries[identity to model]
        override fun write(identity: String, model: String, suggestion: RequirementAiNamingSuggestion) { entries[identity to model] = suggestion }
    }

    @Test fun `cancelled last subscriber does not leak failed requests or cancel desktop scope`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(Job() + dispatcher)
        val gate = CompletableDeferred<Unit>()
        var calls = 0
        val coordinator = RequirementAiNamingCoordinator(scope, MemoryCache(),
            RequirementAiContextProvider { _, _ -> error("cached body expected") },
            object : RequirementAiNamingService {
                override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>): RequirementAiNamingSuggestion {
                    if (++calls == 1) error("命名生成失败")
                    return suggestion
                }
            }, { "model" }, dispatcher, readBody = { _, _ -> gate.await(); "正文" })
        try {
            val subscriber = backgroundScope.launch { coordinator.suggest(link, "obt", item = item) }
            runCurrent()
            assertEquals(1, coordinator.inFlightCount)
            subscriber.cancel(); gate.complete(Unit); runCurrent()
            assertTrue(scope.isActive)
            assertEquals(0, coordinator.inFlightCount)
            assertEquals(suggestion, coordinator.suggest(link, "obt", item = item))
            assertEquals(2, calls)
        } finally { scope.cancel() }
    }

    @Test fun `identical forced retries coalesce but different forbidden histories wait for a new request`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(Job() + dispatcher)
        val gate = Channel<Unit>(Channel.UNLIMITED)
        val histories = mutableListOf<Set<String>>()
        val coordinator = RequirementAiNamingCoordinator(scope, MemoryCache(),
            RequirementAiContextProvider { _, _ -> error("cached body expected") },
            object : RequirementAiNamingService {
                override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) = suggestion.also { histories += forbiddenFolderNames }
            }, { "model" }, dispatcher, readBody = { _, _ -> gate.receive(); "正文" })
        try {
            val initial = async { coordinator.suggest(link, "obt", item = item) }; runCurrent()
            val refresh = async { coordinator.suggest(link, "obt", true, setOf("旧名称"), item) }
            val repeated = async { coordinator.suggest(link, "obt", true, setOf("旧名称"), item) }
            val collision = async { coordinator.suggest(link, "obt", true, setOf("其他名称"), item) }
            runCurrent(); gate.send(Unit); runCurrent()
            assertEquals(listOf(emptySet()), histories)
            gate.send(Unit); runCurrent()
            gate.send(Unit); runCurrent()
            initial.await(); refresh.await(); repeated.await(); collision.await()
            assertEquals(listOf(emptySet(), setOf("旧名称"), setOf("其他名称")), histories)
            assertEquals(0, coordinator.inFlightCount)
        } finally { scope.cancel() }
    }

    @Test fun `bulk and single retries cannot requeue active work and paused refresh stays paused`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(Job() + dispatcher)
        val gate = CompletableDeferred<Unit>()
        var reads = 0; var calls = 0
        val coordinator = RequirementAiNamingCoordinator(scope, MemoryCache(),
            RequirementAiContextProvider { _, _ -> error("cached body expected") },
            object : RequirementAiNamingService {
                override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>): RequirementAiNamingSuggestion {
                    if (++calls == 1) error("暂时失败")
                    return suggestion
                }
            }, { "model" }, dispatcher, readBody = { _, _ -> if (++reads > 1) gate.await(); "正文" })
        try {
            coordinator.prewarm(listOf(item), true); runCurrent()
            assertEquals(1, coordinator.state.value.failures)
            coordinator.retryFailed(); runCurrent()
            coordinator.retryFailed(); coordinator.retry(item.key)
            assertEquals(setOf(item.key), coordinator.state.value.retrying)
            gate.complete(Unit); runCurrent()
            assertEquals(2, calls)
            assertEquals(1, coordinator.state.value.completed)
            assertEquals(0, coordinator.state.value.failures)
            coordinator.pause(); coordinator.prewarm(listOf(item), true); runCurrent()
            assertTrue(coordinator.state.value.paused)
            assertFalse(coordinator.state.value.running)
            coordinator.resume(); runCurrent()
            assertEquals(1, coordinator.state.value.completed)
            assertEquals(2, calls)
            coordinator.cancelPrewarm()
            assertEquals(NamingPreparation(), coordinator.state.value)
        } finally { scope.cancel() }
    }

    @Test fun `queued naming uses captured model and never writes it under a changed model`() = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = CoroutineScope(Job() + dispatcher)
        val gate = CompletableDeferred<Unit>()
        val cache = MemoryCache()
        var selectedModel = "old-model"
        val usedModels = mutableListOf<String>()
        val coordinator = RequirementAiNamingCoordinator(scope, cache,
            RequirementAiContextProvider { _, _ -> error("cached body expected") },
            object : RequirementAiNamingService {
                override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) = error("captured model required")
                override fun suggestWithModel(context: RequirementAiContext, forbiddenFolderNames: Set<String>, model: String) =
                    suggestion.also { usedModels += model }
            }, { selectedModel }, dispatcher, readBody = { _, _ -> gate.await(); "中".repeat(49) + "😀不能发送" })
        try {
            val queued = async { coordinator.suggest(link, "obt", item = item) }; runCurrent()
            selectedModel = "new-model"; gate.complete(Unit); runCurrent(); queued.await()
            assertEquals(listOf("old-model"), usedModels)
            assertTrue(cache.entries.isEmpty())
            coordinator.suggest(link, "obt", item = item)
            assertEquals(listOf("old-model", "new-model"), usedModels)
        } finally { scope.cancel() }
    }

    @Test fun `cancelled draft can explicitly restart naming and late suggestions preserve manual edits`() {
        val draft = RequirementDraftState().changeRequirement(link, "feature/{ai}/{num}")
        assertTrue(canRequestDraftAiNaming(link, draft, true))
        assertFalse(canRequestDraftAiNaming(null, draft, true))
        assertFalse(canRequestDraftAiNaming(link, draft, false))
        val edited = draft.editName("人工命名").editBranch("feature/manual")
        assertEquals(edited, edited.applyAiNaming(link, suggestion, "feature/{ai}/{num}"))
        val changed = draft.changeRequirement(link + "4", "feature/{ai}/{num}")
        assertEquals(changed, changed.applyAiNaming(link, suggestion, "feature/{ai}/{num}"))
    }
}
