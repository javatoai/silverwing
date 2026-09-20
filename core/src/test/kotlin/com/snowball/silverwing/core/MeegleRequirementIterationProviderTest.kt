package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import java.nio.file.Path
import java.time.Duration

class MeegleRequirementIterationProviderTest {
    @Test
    fun `resolves every associated Sprint with its current status`() {
        val runner = object : CommandRunner {
            val queries = mutableListOf<String>()

            override fun run(
                command: List<String>,
                workingDirectory: Path?,
                timeout: Duration,
                environment: Map<String, String>,
            ): CommandResult {
                val mql = command[command.indexOf("--mql") + 1]
                queries += mql
                return CommandResult(
                    0,
                    if (mql.contains(".`User Story`")) {
                        """{"data":{"1":[{"moql_field_list":[{"name":"Sprint","value":{"key_label_value_list":[{"key":"7070412889","label":"OBT-20260817--20260828"}]}}]}]}}"""
                    } else {
                        """{"data":{"1":[{"moql_field_list":[{"name":"Status","value":{"key_label_value_list":[{"key":"active","label":"进行中"}]}}]}]}}"""
                    },
                    "",
                )
            }
        }

        val resolved = MeegleRequirementIterationProvider(runner, isWindows = false)
            .resolve("https://project.feishu.cn/obt/userstory/detail/7064764629", "project-obt")

        assertEquals(
            listOf(RequirementSprint("7070412889", "OBT-20260817--20260828", "进行中")),
            resolved,
        )
        assertEquals(2, runner.queries.size)
        assertEquals(true, runner.queries.first().contains(".`User Story`"))
        assertEquals(true, runner.queries.last().contains(".`Sprint`"))
    }
}
