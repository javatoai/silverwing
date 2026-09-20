package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertFalse
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.condition.EnabledOnOs
import org.junit.jupiter.api.condition.OS
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration

class RequirementMaterialsServiceTest {
    @TempDir
    lateinit var root: Path

    @Test
    fun `preview reuses the only matching directory without creating subdirectory or lock`() {
        val existing = Files.createDirectories(root.resolve("Sprint-8").resolve("123-existing"))
        val runner = RecordingRunner { error("CLI must not be called for a reused directory") }

        val result = service(runner).preview(
            RequirementMaterialsRequest("123", "新名称", root.toString(), "研发", projects()),
        )

        val ready = result as RequirementMaterialsResult.Ready
        assertEquals(RequirementMaterialsResult.Ready.Status.REUSED, ready.status)
        assertEquals(existing.resolve("研发"), ready.writeRoot)
        assertFalse(Files.exists(ready.writeRoot))
        assertFalse(Files.exists(root.resolve(".silverwing-requirement-materials.lock")))
        assertEquals(0, runner.commands.size)
    }

    @Test
    fun `preview rejects a reused directory whose process manifest belongs to another requirement`() {
        val existing = Files.createDirectories(root.resolve("Sprint-8").resolve("123-existing"))
        Files.createDirectories(existing.resolve("研发")).resolve(".silverwing-requirement.json").toFile().writeText(
            """{"identity":{"space":"obt","kind":"userstory","workItemId":"999"}}""",
        )
        val runner = RecordingRunner { error("CLI must not be called after an identity mismatch") }

        val result = service(runner).preview(
            RequirementMaterialsRequest("123", "task", root.toString(), "研发", projects()),
        )

        assertTrue(result is RequirementMaterialsResult.Failed)
        assertTrue((result as RequirementMaterialsResult.Failed).reason.contains("需求编号不一致"))
        assertFalse(Files.exists(root.resolve(".silverwing-requirement-materials.lock")))
        assertEquals(0, runner.commands.size)
    }

    @Test
    fun `preview resolves a new sprint directory without creating any directory or lock`() {
        val runner = RecordingRunner { command ->
            when {
                command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
                command.contains("--fields") -> CommandResult(
                    0,
                    """{"work_item_fields":[{"key":"field_sprint","value":[{"id":"sprint-1"}]}]}""",
                    "",
                )
                command.contains("workitem") && command.contains("get") -> CommandResult(
                    0,
                    """{"work_item_id":"123","work_item_attribute":{"work_item_type":{"key":"User Story"}}}""",
                    "",
                )
                command.contains("query") -> CommandResult(
                    0,
                    """{"data":{"1":[{"moql_field_list":[{"key":"work_item_id","value":{"long_value":"sprint-1"}},{"key":"name","value":{"string_value":"Sprint 8"}},{"key":"work_item_status","value":{"key_label_value_list":[{"key":"in_progress","label":"进行中"}]}},{"key":"archiving_status","value":{"bool_value":false}}]}]}}""",
                    "",
                )
                else -> error("unexpected command: $command")
            }
        }

        val result = service(runner).preview(
            RequirementMaterialsRequest("123", "task", root.toString(), "研发", projects()),
        )

        val ready = result as RequirementMaterialsResult.Ready
        assertEquals(RequirementMaterialsResult.Ready.Status.CREATED, ready.status)
        assertEquals(root.resolve("Sprint 8").resolve("123-task").resolve("研发"), ready.writeRoot)
        assertFalse(Files.exists(root.resolve("Sprint 8")))
        assertFalse(Files.exists(root.resolve(".silverwing-requirement-materials.lock")))
    }

    @Test
    fun `reuses the only matching directory and creates requested subdirectory`() {
        val existing = Files.createDirectories(root.resolve("Sprint-8").resolve("123-existing"))
        val runner = RecordingRunner { error("CLI must not be called for a reused directory") }

        val result = service(runner).ensure("123", "新名称", root.toString(), "研发", projects())

        assertEquals(
            RequirementMaterialsResult.Ready.Status.REUSED,
            (result as RequirementMaterialsResult.Ready).status,
        )
        assertEquals(existing, result.requirementPath)
        assertEquals(existing.resolve("研发"), result.writeRoot)
        assertTrue(Files.isDirectory(result.writeRoot))
        assertEquals(0, runner.commands.size)
    }

    @Test
    fun `rejects a reused directory whose process manifest belongs to another requirement`() {
        val existing = Files.createDirectories(root.resolve("Sprint-8").resolve("123-existing"))
        Files.createDirectories(existing.resolve("研发")).resolve(".silverwing-requirement.json").toFile().writeText(
            """{"identity":{"space":"obt","kind":"userstory","workItemId":"999"}}""",
        )
        val runner = RecordingRunner { error("CLI must not be called after an identity mismatch") }

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        assertTrue(result is RequirementMaterialsResult.Failed)
        assertTrue((result as RequirementMaterialsResult.Failed).reason.contains("需求编号不一致"))
        assertEquals(0, runner.commands.size)
    }

    @Test
    fun `blocks ambiguous matching directories`() {
        Files.createDirectories(root.resolve("a").resolve("123-one"))
        Files.createDirectories(root.resolve("b").resolve("123-two"))

        val result = service(RecordingRunner { error("CLI must not be called") })
            .ensure("123", "task", root.toString(), "研发", projects())

        assertTrue(result is RequirementMaterialsResult.Failed)
        assertEquals(2, (result as RequirementMaterialsResult.Failed).existingPaths.size)
    }

    @Test
    fun `blocks empty project configuration without invoking cli`() {
        val runner = RecordingRunner { error("CLI must not be called") }

        val result = service(runner).ensure("123", "task", root.toString(), "研发", emptyList())

        assertEquals("未配置 Meegle 项目", (result as RequirementMaterialsResult.Failed).reason)
        assertEquals(0, runner.commands.size)
    }

    @Test
    fun `creates sprint requirement and write directory after unique cli lookup`() {
        val runner = RecordingRunner { command ->
            when {
                command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
                command.contains("--fields") -> CommandResult(
                    0,
                    """{"work_item_fields":[{"key":"field_sprint","value":[{"id":"sprint-1"}]}]}""",
                    "",
                )
                command.contains("workitem") && command.contains("get") -> CommandResult(
                    0,
                    """{"work_item_id":"123","work_item_attribute":{"work_item_type":{"key":"User Story"}}}""",
                    "",
                )
                command.contains("query") -> CommandResult(
                    0,
                    """{"data":{"1":[{"moql_field_list":[{"key":"work_item_id","value":{"long_value":"sprint-1"}},{"key":"name","value":{"string_value":"Sprint 8"}},{"key":"work_item_status","value":{"key_label_value_list":[{"key":"in_progress","label":"进行中"}]}},{"key":"archiving_status","value":{"bool_value":false}}]}]}}""",
                    "",
                )
                else -> error("unexpected command: $command")
            }
        }

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        val ready = result as RequirementMaterialsResult.Ready
        assertEquals(RequirementMaterialsResult.Ready.Status.CREATED, ready.status)
        assertEquals(root.resolve("Sprint 8").resolve("123-task"), ready.requirementPath)
        assertEquals(ready.requirementPath.resolve("研发"), ready.writeRoot)
        assertTrue(Files.isDirectory(ready.writeRoot))
    }

    @Test
    fun `numeric input probes configured projects and skips a missing work item`() {
        val runner = RecordingRunner { command ->
            val projectKey = command.getOrNull(command.indexOf("--project-key") + 1)
            when {
                command.contains("workitem") && command.contains("get") && projectKey == "project-a" ->
                    CommandResult(1, "", "work item not found")
                command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
                command.contains("--fields") -> CommandResult(
                    0,
                    """{"work_item_fields":[{"key":"field_sprint","value":[{"id":"sprint-1"}]}]}""",
                    "",
                )
                command.contains("workitem") && command.contains("get") -> CommandResult(
                    0,
                    """{"work_item_id":{"long_value":"123"},"work_item_attribute":{"work_item_type":{"key":"User Story"}}}""",
                    "",
                )
                command.contains("query") -> CommandResult(
                    0,
                    """{"data":{"1":[{"moql_field_list":[{"key":"work_item_id","value":{"long_value":"sprint-1"}},{"key":"name","value":{"string_value":"Sprint 8"}},{"key":"work_item_status","value":{"key_label_value_list":[{"key":"in_progress","label":"进行中"}]}},{"key":"archiving_status","value":{"bool_value":false}}]}]}}""",
                    "",
                )
                else -> error("unexpected command: $command")
            }
        }

        val result = service(runner).ensure(
            "123",
            "task",
            root.toString(),
            "研发",
            listOf(MeegleProjectConfig("project-a", "obt"), MeegleProjectConfig("project-b", "rta")),
        )

        assertEquals(RequirementMaterialsResult.Ready.Status.CREATED, (result as RequirementMaterialsResult.Ready).status)
        assertEquals(1, runner.commands.count { it.contains("project-a") })
        assertTrue(runner.commands.any { it.contains("project-b") })
    }

    @Test
    fun `detail link limits lookup to its configured project`() {
        val runner = RecordingRunner { command ->
            when {
                command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
                command.contains("--fields") -> CommandResult(0, """{"work_item_fields":[{"key":"field_sprint","value":[{"id":"sprint-1"}]}]}""", "")
                command.contains("workitem") && command.contains("get") -> CommandResult(0, """{"work_item_id":"123","work_item_attribute":{"work_item_type":{"key":"User Story"}}}""", "")
                command.contains("query") -> CommandResult(0, """{"data":{"1":[{"moql_field_list":[{"key":"work_item_id","value":{"long_value":"sprint-1"}},{"key":"name","value":{"string_value":"Sprint 8"}},{"key":"work_item_status","value":{"key_label_value_list":[{"key":"in_progress","label":"进行中"}]}},{"key":"archiving_status","value":{"bool_value":false}}]}]}}""", "")
                else -> error("unexpected command: $command")
            }
        }

        val result = service(runner).ensure(
            "https://project.feishu.cn/obt/userstory/detail/123",
            "task",
            root.toString(),
            "研发",
            listOf(MeegleProjectConfig("project-a", "obt"), MeegleProjectConfig("project-b", "rta")),
        )

        assertTrue(result is RequirementMaterialsResult.Ready)
        assertTrue(runner.commands.all { !it.contains("project-b") })
    }

    @Test
    fun `blocks numeric input that matches multiple configured projects`() {
        val runner = RecordingRunner { command ->
            if (command.contains("workitem") && command.contains("get")) {
                CommandResult(0, """{"work_item_id":"123"}""", "")
            } else {
                error("Sprint lookup must not run after an ambiguous project match: $command")
            }
        }

        val result = service(runner).ensure(
            "123",
            "task",
            root.toString(),
            "研发",
            listOf(MeegleProjectConfig("project-a", "obt"), MeegleProjectConfig("project-b", "rta")),
        )

        assertEquals("同一需求编号匹配到多个 Meegle 项目", (result as RequirementMaterialsResult.Failed).reason)
        assertEquals(2, runner.commands.size)
    }

    @Test
    fun `records retryable failure when the linked sprint cannot be resolved`() {
        val runner = RecordingRunner { command ->
            when {
                command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
                command.contains("--fields") -> CommandResult(0, """{"work_item_fields":[{"key":"field_sprint","value":[{"id":"sprint-1"}]}]}""", "")
                command.contains("workitem") && command.contains("get") -> CommandResult(0, """{"work_item_id":"123","work_item_attribute":{"work_item_type":{"key":"User Story"}}}""", "")
                command.contains("query") -> CommandResult(0, """{"data":{"1":[]}}""", "")
                else -> error("unexpected command: $command")
            }
        }

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        assertEquals("需求未关联可用的 Sprint，已停止创建需求资料目录", (result as RequirementMaterialsResult.Failed).reason)
    }

    @Test
    fun `creates the directory under a single linked sprint even when it is not in progress`() {
        val runner = sprintRunner(
            sprintIds = listOf("sprint-1"),
            sprintRows = listOf(sprintRow("sprint-1", "Sprint 8", "未开始")),
        )

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        val ready = result as RequirementMaterialsResult.Ready
        assertEquals(RequirementMaterialsResult.Ready.Status.CREATED, ready.status)
        assertEquals(root.resolve("Sprint 8").resolve("123-task"), ready.requirementPath)
        assertTrue(Files.isDirectory(ready.writeRoot))
    }

    @Test
    fun `picks the only in-progress sprint when several are linked`() {
        val runner = sprintRunner(
            sprintIds = listOf("sprint-1", "sprint-2"),
            sprintRows = listOf(
                sprintRow("sprint-1", "Sprint 7", "未开始"),
                sprintRow("sprint-2", "Sprint 8", "进行中"),
            ),
        )

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        val ready = result as RequirementMaterialsResult.Ready
        assertEquals(RequirementMaterialsResult.Ready.Status.CREATED, ready.status)
        assertEquals(root.resolve("Sprint 8").resolve("123-task"), ready.requirementPath)
    }

    @Test
    fun `fails when several linked sprints have none in progress`() {
        val runner = sprintRunner(
            sprintIds = listOf("sprint-1", "sprint-2"),
            sprintRows = listOf(
                sprintRow("sprint-1", "Sprint 7", "未开始"),
                sprintRow("sprint-2", "Sprint 8", "已结束"),
            ),
        )

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        assertEquals("需求关联多个 Sprint 且均不在进行中，已停止创建需求资料目录", (result as RequirementMaterialsResult.Failed).reason)
    }

    @Test
    fun `fails when several linked sprints are in progress`() {
        val runner = sprintRunner(
            sprintIds = listOf("sprint-1", "sprint-2"),
            sprintRows = listOf(
                sprintRow("sprint-1", "Sprint 7", "进行中"),
                sprintRow("sprint-2", "Sprint 8", "进行中"),
            ),
        )

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        val reason = (result as RequirementMaterialsResult.Failed).reason
        assertTrue(reason.contains("需求关联多个进行中 Sprint"))
        assertTrue(reason.contains("Sprint 7") && reason.contains("Sprint 8"))
    }

    @Test
    @EnabledOnOs(OS.WINDOWS)
    fun `windows cmd keeps JSON quotes in the Sprint fields argument`() {
        var requestedFields: String? = null
        val runner = RecordingRunner { command ->
            when {
                command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
                command.contains("--fields") -> {
                    requestedFields = command[command.indexOf("--fields") + 1]
                    if (requestedFields == "[\\\"field_sprint\\\"]") {
                        CommandResult(0, """{"work_item_fields":[{"key":"field_sprint","value":[{"id":"sprint-1"}]}]}""", "")
                    } else {
                        CommandResult(0, """{"work_item_fields":[]}""", "")
                    }
                }
                command.contains("workitem") && command.contains("get") -> CommandResult(
                    0,
                    """{"work_item_id":"123","work_item_attribute":{"work_item_type":{"key":"User Story"}}}""",
                    "",
                )
                command.contains("query") -> CommandResult(
                    0,
                    """{"data":{"1":[{"moql_field_list":[{"key":"work_item_id","value":{"long_value":"sprint-1"}},{"key":"name","value":{"string_value":"Sprint 8"}},{"key":"work_item_status","value":{"key_label_value_list":[{"key":"in_progress","label":"进行中"}]}},{"key":"archiving_status","value":{"bool_value":false}}]}]}}""",
                    "",
                )
                else -> error("unexpected command: $command")
            }
        }
        val service = RequirementMaterialsService(
            runner = runner,
            meegleExecutable = MeegleExecutable { "C:\\tools\\meegle.cmd" },
            commandTimeout = Duration.ofSeconds(1),
        )

        val result = service.ensure("123", "task", root.toString(), "研发", projects())

        assertEquals("[\\\"field_sprint\\\"]", requestedFields)
        assertTrue(result is RequirementMaterialsResult.Ready)
    }

    @Test
    fun `returns failure when cli command fails`() {
        val runner = RecordingRunner { CommandResult(2, "", "not authenticated") }

        val result = service(runner).ensure("123", "task", root.toString(), "研发", projects())

        assertTrue((result as RequirementMaterialsResult.Failed).reason.contains("not authenticated"))
        assertEquals(
            listOf(root.resolve(".silverwing-requirement-materials.lock")),
            Files.list(root).use { it.toList() },
        )
    }

    @Test
    fun `requires both configured directory values and rejects unsafe segment`() {
        val runner = RecordingRunner { error("CLI must not be called") }
        val service = service(runner)

        assertEquals("资料根路径不能为空", (service.ensure("123", "task", "", "研发", projects()) as RequirementMaterialsResult.Failed).reason)
        assertEquals("资料子目录名不能为空", (service.ensure("123", "task", root.toString(), "", projects()) as RequirementMaterialsResult.Failed).reason)
        assertTrue((service.ensure("123", "task", root.toString(), "..", projects()) as RequirementMaterialsResult.Failed).reason.contains("不能是 . 或 .."))
    }

    private fun projects() = listOf(MeegleProjectConfig("project-key", "obt"))

    private fun sprintRunner(sprintIds: List<String>, sprintRows: List<String>) = RecordingRunner { command ->
        when {
            command.contains("meta-fields") -> CommandResult(0, """{"list":[{"field_name":"Sprint","field_key":"field_sprint"}]}""", "")
            command.contains("--fields") -> CommandResult(
                0,
                sprintIds.joinToString(
                    prefix = """{"work_item_fields":[{"key":"field_sprint","value":[""",
                    postfix = """]}]}""",
                ) { """{"id":"$it"}""" },
                "",
            )
            command.contains("workitem") && command.contains("get") -> CommandResult(
                0,
                """{"work_item_id":"123","work_item_attribute":{"work_item_type":{"key":"User Story"}}}""",
                "",
            )
            command.contains("query") -> CommandResult(
                0,
                sprintRows.joinToString(prefix = """{"data":{"1":[""", postfix = """]}}"""),
                "",
            )
            else -> error("unexpected command: $command")
        }
    }

    private fun sprintRow(id: String, name: String, status: String): String =
        """{"moql_field_list":[{"key":"work_item_id","value":{"long_value":"$id"}},{"key":"name","value":{"string_value":"$name"}},{"key":"work_item_status","value":{"key_label_value_list":[{"key":"status","label":"$status"}]}},{"key":"archiving_status","value":{"bool_value":false}}]}"""

    private fun service(runner: CommandRunner) = RequirementMaterialsService(
        runner = runner,
        meegleExecutable = MeegleExecutable { "meegle" },
        commandTimeout = Duration.ofSeconds(1),
    )

    private class RecordingRunner(
        private val handler: (List<String>) -> CommandResult,
    ) : CommandRunner {
        val commands = mutableListOf<List<String>>()

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            commands += command
            return handler(command)
        }
    }
}
