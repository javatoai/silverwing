package com.snowball.silverwing.core

import kotlinx.coroutines.runBlocking
import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MeegleParticipatedWorkItemsSourceTest {
    @Test
    fun `loads every type with no sprint or status filter and asks CLI to paginate all results`() = runBlocking {
        val commands = mutableListOf<List<String>>()
        val runner = runner { command ->
            commands += command
            val mql = command.valueAfter("--mql")!!
            val type = mql.substringAfter(".`").substringBefore('`')
            val id = when (type) {
                "User Story" -> "101"
                "Tech Improvement" -> "102"
                "Bug" -> "103"
                else -> "104"
            }
            CommandResult(0, response(id, "标题 $type"), "")
        }
        val source = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(4, result.completedQueries)
        assertEquals(4, result.items.size)
        assertEquals(listOf("需求", "技术改进", "缺陷", "任务"), result.items.map { it.typeLabel })
        assertEquals("https://project.feishu.cn/obt/bug/detail/103", result.items[2].url)
        assertTrue(commands.all { "--auto-paginate" in it })
        assertTrue(commands.all { it.valueAfter("--mql")!!.contains("all_participate_persons()") })
        assertTrue(commands.none { it.valueAfter("--mql")!!.contains("LIMIT") })
        assertTrue(commands.none { it.valueAfter("--mql")!!.contains("Sprint") })
        assertTrue(commands.none { it.valueAfter("--mql")!!.contains("archiving") })
    }

    @Test
    fun `retains all auto-paginated rows and deduplicates repeated work items`() = runBlocking {
        val runner = runner { command ->
            if (command.valueAfter("--mql")!!.contains("User Story")) {
                val rows = (1..61).joinToString(",") { row(it.toString(), "需求 $it") }
                CommandResult(0, """{"data":{"1":[$rows,${row("61", "需求 61")}]}}""", "")
            } else CommandResult(0, """{"data":{"1":[]}}""", "")
        }
        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(61, result.items.size)
        assertEquals("需求 61", result.items.last().title)
        assertTrue(result.failures.isEmpty())
    }

    @Test
    fun `preserves successful types when one query fails and reports incomplete result`() = runBlocking {
        val runner = runner { command ->
            if (command.valueAfter("--mql")!!.contains("Bug")) CommandResult(1, "", "permission denied")
            else CommandResult(0, response("123", "可见需求"), "")
        }
        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(3, result.completedQueries)
        assertEquals(3, result.items.size)
        assertEquals(1, result.failures.size)
        assertTrue(result.failures.single().contains("缺陷"))
        assertFalse(result.failures.single().contains("http://"))
    }

    @Test
    fun `reports malformed successful responses instead of silently returning an empty list`() = runBlocking {
        val source = MeegleParticipatedWorkItemsSource(
            runner { CommandResult(0, "{}", "") },
            isWindows = false,
            loginStatus = ::authenticated,
        )

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(0, result.completedQueries)
        assertEquals(4, result.failures.size)
    }

    @Test
    fun `unauthenticated CLI produces a direct login hint without querying work items`() = runBlocking {
        var queryCount = 0
        val source = MeegleParticipatedWorkItemsSource(
            runner = runner { queryCount++; CommandResult(0, "{}", "") },
            isWindows = false,
            loginStatus = { MeegleCliStatus(installed = true, authenticated = false) },
        )

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(0, queryCount)
        assertEquals(0, result.completedQueries)
        assertTrue(result.failures.single().contains("未登录"))
    }

    private fun authenticated() = MeegleCliStatus(installed = true, authenticated = true)

    private fun runner(block: (List<String>) -> CommandResult): CommandRunner = object : CommandRunner {
        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult = block(command)
    }

    private fun response(id: String, title: String) = """{"data":{"1":[${row(id, title)}]}}"""

    private fun row(id: String, title: String) =
        """{"moql_field_list":[{"key":"work_item_id","value":{"long_value":$id}},{"key":"name","value":{"string_value":"$title"}}]}"""

    private fun List<String>.valueAfter(flag: String): String? =
        indexOf(flag).takeIf { it >= 0 }?.let { getOrNull(it + 1) }
}
