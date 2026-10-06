package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.atomic.AtomicInteger
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class RequirementAiNamingCoordinatorTest {
    @TempDir lateinit var root: Path
    private val link = "https://project.feishu.cn/obt/userstory/detail/123"

    @Test fun `foreground requests join prewarming and cache avoids context reads on restart`() = runBlocking {
        val contexts = AtomicInteger()
        val generations = AtomicInteger()
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val cacheFile = root.resolve("names.json")
        val context = RequirementAiContextProvider { _, _ -> contexts.incrementAndGet(); RequirementAiContext("支付", "正文") }
        val naming = object : RequirementAiNamingService {
            override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) =
                RequirementAiNamingSuggestion("支付优化", "payment_fix").also { generations.incrementAndGet(); Thread.sleep(80) }
        }
        try {
            val coordinator = RequirementAiNamingCoordinator(scope, LocalRequirementAiNamingCache(cacheFile), context, naming, { "gpt-6-luna" })
            coordinator.prewarm(listOf(ParticipatedWorkItem("obt", "项目", "story", "需求", "123", "标题", link)), true)
            val first = async { coordinator.suggest(link, "obt") }
            val second = async { coordinator.suggest(link, "obt") }
            assertEquals(first.await(), second.await())
            assertEquals(1, generations.get())
            assertEquals(1, contexts.get())
            val reopened = RequirementAiNamingCoordinator(scope, LocalRequirementAiNamingCache(cacheFile), context, naming, { "gpt-6-luna" })
            assertEquals(first.await(), reopened.suggest(link, "obt"))
            assertEquals(1, contexts.get())
            reopened.suggest(link, "obt", force = true)
            assertEquals(2, contexts.get())
            assertEquals(2, generations.get())
        } finally { scope.cancel() }
    }

    @Test fun `prewarming skips repeated cached items and reports failures without blocking other items`() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val calls = AtomicInteger()
        val coordinator = RequirementAiNamingCoordinator(scope, LocalRequirementAiNamingCache(root.resolve("names.json")),
            RequirementAiContextProvider { url, _ -> if (url.endsWith("124")) error("正文读取失败") else RequirementAiContext("标题", "") },
            object : RequirementAiNamingService { override fun suggest(context: RequirementAiContext, forbiddenFolderNames: Set<String>) =
                RequirementAiNamingSuggestion("支付优化", "payment_fix").also { calls.incrementAndGet() } }, { "gpt-6-luna" })
        val items = (123..125).map { ParticipatedWorkItem("obt", "项目", "story", "需求", "$it", "标题", link.substringBeforeLast('/') + "/$it") }
        try {
            coordinator.prewarm(items + items, true)
            withTimeout(5_000) { while (coordinator.state.value.running) delay(10) }
            assertEquals(2, coordinator.state.value.completed)
            assertEquals(1, coordinator.state.value.failures)
            assertEquals(3, coordinator.state.value.total)
            coordinator.prewarm(items, true)
            withTimeout(5_000) { while (coordinator.state.value.running) delay(10) }
            assertEquals(2, calls.get())
            coordinator.prewarm(items, false)
            assertFalse(coordinator.state.value.running)
        } finally { scope.cancel() }
    }
}
