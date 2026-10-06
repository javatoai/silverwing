package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MeegleRequirementAiContextProviderTest {
    @Test
    fun `reads standard description field without querying metadata`() {
        val runner = SequenceRunner(
            listOf(
                CommandResult(
                    0,
                    """
                    {
                      "work_item_attribute": {
                        "work_item_name": "通卡对账优化",
                        "work_item_type": {"key": "story"}
                      },
                      "work_item_fields": [
                        {"key": "description", "name": "Description", "value": "按通卡配置输出对账文件。"}
                      ]
                    }
                    """.trimIndent(),
                    "",
                ),
            ),
        )
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val context = provider.fetch(
            "https://project.feishu.cn/obt/userstory/detail/123",
            "project-payment",
        )

        assertEquals("通卡对账优化", context.title)
        assertEquals("按通卡配置输出对账文件。", context.body)
        assertEquals(1, runner.commands.size)
        assertFalse(runner.commands.single().contains("meta-fields"))
        assertFalse(runner.commands.single().contains("--fields"))
    }

    @Test
    fun `reads rich text and array values from standard content field`() {
        val runner = SequenceRunner(
            listOf(
                CommandResult(
                    0,
                    """
                    {
                      "name": "富文本需求",
                      "work_item_fields": [
                        {
                          "key": "content",
                          "name": "Content",
                          "value": [
                            {"text": "第一段"},
                            {"content": {"text": "第二段"}}
                          ]
                        }
                      ]
                    }
                    """.trimIndent(),
                    "",
                ),
            ),
        )
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val context = provider.fetch(
            "https://project.feishu.cn/obt/userstory/detail/123",
            "project-payment",
        )

        assertEquals("第一段\n第二段", context.body)
        assertEquals(1, runner.commands.size)
    }

    @Test
    fun `falls back to configured description field when standard description is blank`() {
        val runner = SequenceRunner(
            listOf(
                CommandResult(
                    0,
                    """
                    {
                      "name": "自定义正文需求",
                      "work_item_attribute": {"work_item_type": {"key": "story"}},
                      "work_item_fields": [{"key": "description", "value": "  "}]
                    }
                    """.trimIndent(),
                    "",
                ),
                CommandResult(0, """{"list":[{"field_name":"Description","field_key":"field_description"}]}""", ""),
                CommandResult(
                    0,
                    """{"work_item_fields":[{"key":"field_description","value":"来自自定义字段的正文"}]}""",
                    "",
                ),
            ),
        )
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val context = provider.fetch(
            "https://project.feishu.cn/obt/userstory/detail/123",
            "project-payment",
        )

        assertEquals("来自自定义字段的正文", context.body)
        assertTrue(runner.commands.any { it.contains("meta-fields") })
        assertTrue(runner.commands.last().contains("--fields"))
    }

    @Test
    fun `reads title and configured description field without exposing unrelated fields`() {
        val runner = SequenceRunner(
            listOf(
                CommandResult(
                    0,
                    """{"name":"支付超时优化","work_item_attribute":{"work_item_type":{"key":"story"}}}""",
                    "",
                ),
                CommandResult(0, """{"list":[{"field_name":"需求描述","field_key":"field_description"}]}""", ""),
                CommandResult(
                    0,
                    """{"work_item_fields":[{"key":"field_description","value":{"text":"付款后等待过长","children":[{"text":"需要优化超时提示"}]}}]}""",
                    "",
                ),
            ),
        )
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val context = provider.fetch(
            "https://project.feishu.cn/obt/userstory/detail/123",
            "project-payment",
        )

        assertEquals("支付超时优化", context.title)
        assertTrue(context.body.contains("付款后等待过长"))
        assertTrue(context.body.contains("需要优化超时提示"))
        assertEquals("project-payment", runner.commands.first().valueAfter("--project-key"))
        assertTrue(runner.commands.last().contains("--fields"))
    }

    @Test
    fun `title-only requirement allows an empty body after successful field discovery`() {
        val runner = SequenceRunner(
            listOf(CommandResult(0, """{"name":"只有标题"}""", ""),
                *Array(4) { CommandResult(0, """{"list":[]}""", "") }),
        )
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val context = provider.fetch("https://project.feishu.cn/obt/userstory/detail/123", "project-payment")
        assertEquals("只有标题", context.title)
        assertEquals("", context.body)
    }

    @Test
    fun `truncates only the readable body to fifty Unicode characters`() {
        val body = "字".repeat(4_001)
        val runner = SequenceRunner(
            listOf(CommandResult(0, """{"name":"长正文需求","description":"$body"}""", "")),
        )
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val context = provider.fetch(
            "https://project.feishu.cn/obt/userstory/detail/123",
            "project-payment",
        )

        assertEquals(50, context.body.codePointCount(0, context.body.length))
        assertEquals("长正文需求", context.title)
    }

    @Test
    fun `rich text containers omit unknown identifiers and metadata values`() {
        val runner = SequenceRunner(listOf(CommandResult(0,
            """{"name":"支付优化","description":{"type":"document","id":"PRIVATE-ID","url":"https://private.invalid/secret","children":[{"text":"可读正文","id":"PRIVATE-CHILD","marks":[{"token":"PRIVATE-TOKEN"}]}]}}""", "")))
        val context = MeegleRequirementAiContextProvider(runner, isWindows = false)
            .fetch("https://project.feishu.cn/obt/userstory/detail/123", "project-payment")
        assertEquals("可读正文", context.body)
        assertFalse(context.body.contains("PRIVATE"))
    }

    @Test
    fun `full body preview does not truncate or remove Markdown line breaks`() {
        val body = "# 标题\\n\\n" + "字".repeat(4_100)
        val runner = SequenceRunner(listOf(CommandResult(0, """{"name":"长正文","description":"$body"}""", "")))
        val provider = MeegleRequirementAiContextProvider(runner, isWindows = false)

        val content = provider.fetchFullBody("https://project.feishu.cn/obt/userstory/detail/123", "project-payment")

        assertEquals("# 标题\n\n" + "字".repeat(4_100), content)
    }

    @Test
    fun `full body preview distinguishes empty content from command failure`() {
        val noContent = SequenceRunner(listOf(
            CommandResult(0, """{"name":"无正文"}""", ""),
            *Array(4) { CommandResult(0, """{"list":[]}""", "") },
        ))
        val provider = MeegleRequirementAiContextProvider(noContent, isWindows = false)

        assertEquals(null, provider.fetchFullBody("https://project.feishu.cn/obt/bug/detail/123", "project-payment"))
        assertTrue(noContent.commands.any { "meta-fields" in it })
    }

    private class SequenceRunner(
        private val results: List<CommandResult>,
    ) : CommandRunner {
        val commands = mutableListOf<List<String>>()

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            return results.getOrElse(commands.lastIndex) { CommandResult(1, "", "unexpected command") }
        }
    }

    private fun List<String>.valueAfter(flag: String): String? =
        indexOf(flag).takeIf { it >= 0 }?.let { getOrNull(it + 1) }
}
