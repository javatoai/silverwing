package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class RequirementAiNamingCacheTest {
    @TempDir lateinit var root: Path

    @Test fun `cache survives restart isolates requirement identity and model and never stores body`() {
        val file = root.resolve("cache/names.json")
        val suggestion = RequirementAiNamingSuggestion("支付优化", "payment_channel")
        LocalRequirementAiNamingCache(file).write("obt:story:123", "gpt-6-luna", suggestion)
        val reopened = LocalRequirementAiNamingCache(file)
        assertEquals(suggestion, reopened.read("obt:story:123", "gpt-6-luna"))
        assertNull(reopened.read("another:story:123", "gpt-6-luna"))
        assertNull(reopened.read("obt:story:123", "another-model"))
        assertFalse(Files.readString(file).contains("body"))
        reopened.write("obt:story:123", "gpt-6-luna", suggestion.copy(branchSuffix = "payment_fix"))
        assertEquals("payment_fix", LocalRequirementAiNamingCache(file).read("obt:story:123", "gpt-6-luna")!!.branchSuffix)
    }

    @Test fun `broken cache reports once and is rebuilt independently of strict configuration`() {
        val paths = ApplicationPaths(root)
        val config = ConfigStore(paths).load()
        Files.createDirectories(paths.cache)
        val file = paths.cache.resolve("requirement-ai-naming.json")
        Files.writeString(file, "invalid json")
        var failures = 0
        val cache = LocalRequirementAiNamingCache(file) { failures++ }
        assertNull(cache.read("obt:story:1", "model"))
        assertNull(cache.read("obt:story:2", "model"))
        assertEquals(1, failures)
        cache.write("obt:story:1", "model", RequirementAiNamingSuggestion("支付优化", "payment_fix"))
        assertNotNull(LocalRequirementAiNamingCache(file).read("obt:story:1", "model"))
        assertEquals(config, ConfigStore(paths).load())
    }

    @Test fun `AI context truncates at fifty Unicode code points while allowing empty body`() {
        val content = "中".repeat(49) + "😀" + "不应发送".repeat(30)
        assertEquals("中".repeat(49) + "😀", RequirementAiContext.truncateBody(content))
        assertEquals("", RequirementAiContext("只有标题", "").body)
        assertFailsWith<IllegalArgumentException> { RequirementAiContext("标题", content) }
    }

    @Test fun `AI descriptions only replace explicit placeholders and number placeholders remain local`() {
        val link = "https://project.feishu.cn/obt/userstory/detail/123"
        val suggestion = RequirementAiNamingSuggestion("支付优化", "payment_fix")
        val plain = RequirementDraftState().changeRequirement(link, "feature/{num}").applyAiNaming(link, suggestion, "feature/{num}")
        assertEquals("feature/123", plain.branch)
        assertEquals("feature/payment_fix/123", plain.changeGroup("feature/{ai}/{num}").branch)
        assertTrue(BranchPrefixResolver.containsUnresolvedPlaceholder("feature/{ai}"))
        assertEquals("manual", plain.editBranch("manual").applyAiNaming(link, suggestion, "feature/{ai}").branch)
    }
}
