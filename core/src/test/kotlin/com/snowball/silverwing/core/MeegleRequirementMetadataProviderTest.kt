package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertNull
import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration

class MeegleRequirementMetadataProviderTest {
    @Test
    fun `null titles do not hide a usable fallback title`() {
        val outputs = listOf(
            """{"name":null,"work_item_name":"正确标题"}""",
            """{"name":null,"work_item_name":null,"work_item_attribute":{"name":null,"work_item_name":"正确标题"}}""",
        )
        outputs.forEach { output ->
            val provider = MeegleRequirementMetadataProvider(
                RecordingRunner(CommandResult(0, output, "")),
                isWindows = false,
            )

            assertEquals("正确标题", provider.fetch("https://project.feishu.cn/obt/userstory/detail/1")?.title)
        }
        val empty = MeegleRequirementMetadataProvider(
            RecordingRunner(CommandResult(0, """{"name":null,"work_item_name":null}""", "")),
            isWindows = false,
        )
        assertNull(empty.fetch("https://project.feishu.cn/obt/userstory/detail/1"))
    }

    @Test
    fun `null or malformed optional containers preserve the title`() {
        val outputs = listOf(
            """{"name":"正确标题","work_item_attribute":null}""",
            """{"name":"正确标题","work_item_attribute":[]}""",
            """{"name":"正确标题","work_item_attribute":{"work_item_status":null,"role_members":null}}""",
            """{"name":"正确标题","work_item_attribute":{"work_item_status":[],"role_members":{}}}""",
        )
        outputs.forEach { output ->
            val provider = MeegleRequirementMetadataProvider(
                RecordingRunner(CommandResult(0, output, "")),
                isWindows = false,
            )

            assertEquals(
                RequirementMetadata(title = "正确标题", status = null),
                provider.fetch("https://project.feishu.cn/obt/userstory/detail/1"),
            )
        }
    }

    @Test
    fun `null status and malformed members do not discard valid participants`() {
        val provider = MeegleRequirementMetadataProvider(
            RecordingRunner(CommandResult(0, """
                {
                  "work_item_attribute": {
                    "work_item_status": {"name":null},
                    "role_members": [
                      null,
                      {"name":"QC Owner","members":null},
                      {"name":"QC Owner","members":{}},
                      {"name":"QC Owner","members":[null,{"name":null},{"name":{}},{"name":" 测试 ","email":null}]}
                    ]
                  }
                }
            """.trimIndent(), "")),
            isWindows = false,
        )

        assertEquals(
            RequirementMetadata(
                status = null,
                participants = RequirementParticipants(qcOwners = listOf(RequirementPerson("测试"))),
            ),
            provider.fetch("https://project.feishu.cn/obt/userstory/detail/1"),
        )
    }

    @Test
    fun `reads title using documented compatibility priority`() {
        val topLevel = MeegleRequirementMetadataProvider(
            RecordingRunner(CommandResult(0, """{"name":"顶层标题","work_item_name":"备用","work_item_attribute":{"name":"嵌套"}}""", "")),
            isWindows = false,
        )
        assertEquals("顶层标题", topLevel.fetch("https://project.feishu.cn/obt/userstory/detail/1")?.title)

        val nested = MeegleRequirementMetadataProvider(
            RecordingRunner(CommandResult(0, """{"work_item_attribute":{"work_item_name":"嵌套标题"}}""", "")),
            isWindows = false,
        )
        assertEquals("嵌套标题", nested.fetch("https://project.feishu.cn/obt/userstory/detail/2")?.title)
    }

    @Test
    fun `fetches story status qc owner and product manager`() {
        val runner = RecordingRunner(
            CommandResult(
                0,
                """{"work_item_attribute":{"work_item_status":{"name":"待评审"},"role_members":[{"name":"QC Owner","members":[{"name":"靳保新","email":"derrick.jin@snowballtech.com"}]},{"name":"产品经理","members":[{"name":"黄倩","email":"ian.huang@snowballtech.com"}]}]}}""",
                "",
            ),
        )
        val client = MeegleRequirementMetadataProvider(runner, isWindows = false)

        assertEquals(
            RequirementMetadata(
                status = "待评审",
                participants = RequirementParticipants(
                    qcOwners = listOf(RequirementPerson("靳保新", "derrick.jin@snowballtech.com")),
                    productManagers = listOf(RequirementPerson("黄倩", "ian.huang@snowballtech.com")),
                ),
            ),
            client.fetch("https://project.feishu.cn/obt/userstory/detail/7060612727"),
        )
        assertEquals(
            listOf(
                "meegle",
                "workitem",
                "get",
                "--project-key",
                "67c17e40bf0d47db9549cb08",
                "--work-item-id",
                "7060612727",
                "--format",
                "json",
            ),
            runner.command,
        )
        assertEquals(Duration.ofSeconds(8), runner.timeout)
    }

    @Test
    fun `technical and bug include qc owner only while task includes no roles`() {
        val runner = RecordingRunner(
            CommandResult(
                0,
                """{"work_item_attribute":{"work_item_status":{"name":"进行中"},"role_members":[{"name":"QC Owner","members":[{"name":"测试","email":"qa@example.com"}]},{"name":"产品经理","members":[{"name":"产品","email":"pm@example.com"}]}]}}""",
                "",
            ),
        )
        val client = MeegleRequirementMetadataProvider(runner, isWindows = false)

        assertEquals(
            RequirementParticipants(qcOwners = listOf(RequirementPerson("测试", "qa@example.com"))),
            client.fetch("https://project.feishu.cn/obt/technical/detail/6996636709")!!.participants,
        )
        assertEquals(
            RequirementParticipants(qcOwners = listOf(RequirementPerson("测试", "qa@example.com"))),
            client.fetch("https://project.feishu.cn/obt/bug/detail/7066114548,")!!.participants,
        )
        assertEquals(
            RequirementParticipants(),
            client.fetch("https://project.feishu.cn/obt/othertask/detail/7055846637")!!.participants,
        )
    }

    @Test
    fun `silently omits missing role members`() {
        val client = MeegleRequirementMetadataProvider(
            RecordingRunner(CommandResult(0, """{"work_item_attribute":{"role_members":[]}}""", "")),
            isWindows = false,
        )

        assertEquals(
            RequirementParticipants(),
            client.fetch("https://project.feishu.cn/obt/bug/detail/7066114548")!!.participants,
        )
    }

    @Test
    fun `returns null without invoking cli for non feishu links`() {
        val runner = RecordingRunner(CommandResult(0, "{}", ""))

        assertNull(MeegleRequirementMetadataProvider(runner, isWindows = true).fetch("https://example.com/task"))
        assertNull(runner.command)
    }

    @Test
    fun `uses configured project key for a custom Feishu space`() {
        val runner = RecordingRunner(CommandResult(0, """{"name":"自定义空间标题"}""", ""))
        val provider = MeegleRequirementMetadataProvider(runner, isWindows = false)

        assertEquals(
            "自定义空间标题",
            provider.fetch("https://project.feishu.cn/payment/userstory/detail/123", "project-payment")?.title,
        )
        assertEquals("project-payment", runner.command?.get(runner.command!!.indexOf("--project-key") + 1))
    }

    @Test
    fun `returns null when cli fails or status is absent`() {
        assertNull(
            MeegleRequirementMetadataProvider(
                RecordingRunner(CommandResult(1, "", "not authenticated")),
                isWindows = true,
            ).fetch("https://project.feishu.cn/rta/bug/detail/123"),
        )
        assertNull(
            MeegleRequirementMetadataProvider(
                RecordingRunner(CommandResult(0, "{}", "")),
                isWindows = true,
            ).fetch("https://project.feishu.cn/rta/bug/detail/123"),
        )
    }

    private class RecordingRunner(
        private val result: CommandResult,
    ) : CommandRunner {
        var command: List<String>? = null
        var timeout: Duration? = null

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            this.command = command
            this.timeout = timeout
            return result
        }
    }
}
