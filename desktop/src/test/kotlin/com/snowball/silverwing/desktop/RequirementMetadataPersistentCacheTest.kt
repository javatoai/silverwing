@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.RequirementMetadata
import com.snowball.silverwing.core.RequirementMetadataProvider
import kotlinx.coroutines.test.*
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class RequirementMetadataPersistentCacheTest {
    @TempDir lateinit var root: Path
    private val clock = RequirementCacheTestClock()
    private var account: String? = "host/tenant/user"
    private val link = "https://project.feishu.cn/obt/userstory/detail/1"
    private fun TestScope.coordinator(provider: RequirementMetadataProvider) = RequirementMetadataCoordinator(provider, backgroundScope,
        StandardTestDispatcher(testScheduler), clock, cacheAccess = RequirementCacheAccess(RequirementReadCache(root, clock)) { account })

    @Test fun `restart reuses metadata and memory hit cannot extend the persisted deadline`() = runTest {
        var reads = 0
        val provider = RequirementMetadataProvider { RequirementMetadata("title-${++reads}", "开发中") }
        coordinator(provider).fetch(link, "project")
        clock.advance(REQUIREMENT_CACHE_TTL.minusMillis(1))
        val restarted = coordinator(provider)
        assertEquals("title-1", assertIs<RequirementFetchResult.Success>(restarted.fetch(link, "project")).metadata.title)
        clock.advance(Duration.ofMillis(1))
        assertEquals("title-2", assertIs<RequirementFetchResult.Success>(restarted.fetch(link, "project")).metadata.title)
        assertEquals(2, reads)
    }

    @Test fun `forced failure is visible and preserves valid disk and memory until successful retry`() = runTest {
        var value: RequirementMetadata? = RequirementMetadata("old", "开发中")
        var reads = 0
        val provider = RequirementMetadataProvider { reads++; value }
        val coordinator = coordinator(provider)
        coordinator.fetch(link, "project")
        value = null
        assertIs<RequirementFetchResult.Failure>(coordinator.fetch(link, "project", true))
        assertEquals("old", assertIs<RequirementFetchResult.Success>(coordinator.fetch(link, "project")).metadata.title)
        assertEquals("old", assertIs<RequirementFetchResult.Success>(coordinator(provider).fetch(link, "project")).metadata.title)
        assertEquals(2, reads)
        value = RequirementMetadata("new", "完成")
        coordinator.fetch(link, "project", true)
        assertEquals("new", assertIs<RequirementFetchResult.Success>(coordinator(provider).fetch(link, "project")).metadata.title)
        assertEquals(3, reads)
    }

    @Test fun `metadata keys include account project and link and unverified identities are not cached`() = runTest {
        var reads = 0
        val provider = RequirementMetadataProvider { RequirementMetadata("${++reads}", null) }
        val coordinator = coordinator(provider)
        coordinator.fetch(link, "project")
        coordinator.fetch(link + "?from=task", "project")
        assertEquals(1, reads, "Equivalent links share persistent metadata")
        coordinator.fetch(link, "another-project")
        coordinator.fetch(link.replace("/1", "/2"), "project")
        account = "another-account"
        coordinator.fetch(link, "project")
        account = null
        coordinator.fetch(link, "project"); coordinator.fetch(link, "project")
        assertEquals(6, reads)
    }
}
