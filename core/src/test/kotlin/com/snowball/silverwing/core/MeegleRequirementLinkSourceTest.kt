package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test
import kotlinx.coroutines.runBlocking
import java.time.Duration
import java.util.concurrent.atomic.AtomicInteger

class MeegleRequirementLinkSourceTest {
    @Test fun `maps deduplicated bug links from active sprint`() = runBlocking {
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: java.nio.file.Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                if (command.contains("version")) return CommandResult(0, "1", "")
                val mql = command[command.indexOf("--mql") + 1]
                val json = when {
                    mql.contains(".`Bug`") -> typedIdResponse("22")
                    mql.contains(".`Sprint`") -> typedIdResponse("11")
                    else -> "{}"
                }
                return CommandResult(0, json, "")
            }
        }
        val source = MeegleRequirementLinkSource(runner, RequirementMetadataProvider { RequirementMetadata("标题", null) }, false)
        val result = source.load(listOf(MeegleProjectConfig("obt", "obt")))
        assertEquals(listOf("https://project.feishu.cn/obt/bug/detail/22"), result.candidates.map { it.url })
        assertEquals("标题", result.candidates.single().title)
    }

    @Test
    fun `passes the configured project key when reading custom-space titles`() = runBlocking {
        var observedProjectKey: String? = null
        val provider = object : ProjectScopedRequirementMetadataProvider {
            override fun fetch(requirementLink: String): RequirementMetadata? = null
            override fun fetch(requirementLink: String, projectKey: String): RequirementMetadata? {
                observedProjectKey = projectKey
                return RequirementMetadata("自定义标题", null)
            }
        }
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: java.nio.file.Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                val mql = command[command.indexOf("--mql") + 1]
                return CommandResult(0, if (mql.contains(".`Bug`")) typedIdResponse("22") else if (mql.contains(".`Sprint`")) typedIdResponse("11") else "{}", "")
            }
        }

        val result = MeegleRequirementLinkSource(runner, provider, false)
            .load(listOf(MeegleProjectConfig("project-payment", "payment")))

        assertEquals("project-payment", observedProjectKey)
        assertEquals("自定义标题", result.candidates.single().title)
    }

    @Test
    fun `loads Meegle calls with at most four concurrent requests`() = runBlocking {
        val tracker = ConcurrentCallTracker()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: java.nio.file.Path?, timeout: Duration, environment: Map<String, String>): CommandResult = tracker.track {
                val mql = command[command.indexOf("--mql") + 1]
                val projectNumber = command[command.indexOf("--project-key") + 1].substringAfterLast('-')
                val response = when {
                    mql.contains(".`Sprint`") -> typedIdResponse("1")
                    mql.contains(".`Bug`") -> typedIdResponse(projectNumber)
                    else -> "{}"
                }
                CommandResult(0, response, "")
            }
        }
        val source = MeegleRequirementLinkSource(
            runner = runner,
            metadata = RequirementMetadataProvider { link -> tracker.track { RequirementMetadata(link, null) } },
            isWindows = false,
        )

        val result = source.load((1..5).map { MeegleProjectConfig("project-$it", "space-$it") })

        assertEquals(5, result.candidates.size)
        assertEquals(4, tracker.maximum.get())
    }

    private fun typedIdResponse(id: String) = """
        {"data":{"1":[{"moql_field_list":[{"key":"work_item_id","name":"Item Id","value":{"long_value":$id}}]}]}}
    """.trimIndent()

    private class ConcurrentCallTracker {
        private val active = AtomicInteger()
        val maximum = AtomicInteger()

        fun <T> track(action: () -> T): T {
            val concurrent = active.incrementAndGet()
            maximum.updateAndGet { maxOf(it, concurrent) }
            try {
                Thread.sleep(100)
                return action()
            } finally {
                active.decrementAndGet()
            }
        }
    }
}
