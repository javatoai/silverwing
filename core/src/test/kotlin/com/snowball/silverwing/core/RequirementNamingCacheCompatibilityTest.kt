package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class RequirementNamingCacheCompatibilityTest {
    @TempDir lateinit var root: Path
    private val suggestion = RequirementAiNamingSuggestion("支付优化", "payment_fix")

    @Test fun `switching models retains both results across restart`() {
        val file = root.resolve("names.json")
        val cache = LocalRequirementAiNamingCache(file)
        cache.write("obt:story:123", "model-a", suggestion)
        cache.write("obt:story:123", "model-b", suggestion.copy(branchSuffix = "payment_new"))
        val reopened = LocalRequirementAiNamingCache(file)
        assertEquals(suggestion, reopened.read("obt:story:123", "model-a"))
        assertEquals("payment_new", reopened.read("obt:story:123", "model-b")?.branchSuffix)
    }

    @Test fun `legacy version one cache remains readable and failure callback cannot block recovery`() {
        val file = root.resolve("names.json")
        Files.writeString(file, """{"version":1,"entries":{"obt:story:123":{"folderName":"支付优化","branchSuffix":"payment_fix","model":"model","rulesVersion":2,"generatedAt":"2026-10-01T00:00:00Z"}}}""")
        assertEquals(suggestion, LocalRequirementAiNamingCache(file).read("obt:story:123", "model"))
        Files.writeString(file, "broken")
        val cache = LocalRequirementAiNamingCache(file) { error("notification unavailable") }
        assertNull(cache.read("obt:story:123", "model"))
        cache.write("obt:story:123", "model", suggestion)
        assertEquals(suggestion, LocalRequirementAiNamingCache(file).read("obt:story:123", "model"))
    }
}
