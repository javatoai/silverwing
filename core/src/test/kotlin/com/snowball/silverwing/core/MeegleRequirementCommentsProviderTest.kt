package com.snowball.silverwing.core

import kotlinx.serialization.json.*
import org.junit.jupiter.api.Test
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class MeegleRequirementCommentsProviderTest {
    private val item = ParticipatedWorkItem("project", "obt", "userstory", "需求", "123", "测试需求", "https://project.feishu.cn/obt/userstory/detail/123")
    private fun runner(block: (List<String>) -> CommandResult) = object : CommandRunner {
        override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
            assertEquals(Duration.ofSeconds(20), timeout)
            assertEquals(listOf("--format", "json"), command.takeLast(2))
            return block(command)
        }
    }
    private fun row(id: String, text: String = "内容", author: String = "user") = buildJsonObject {
        put("comment_id", id); put("content", text); put("creator", author); put("created_at", "2026-10-06 12:34:56")
        put("file_url", "https://example.com/attachment.pdf"); put("parent_id", if (id == "2") "1" else "0")
    }
    private fun page(number: Int, totalPages: Int, total: Int, rows: List<JsonObject>) = CommandResult(0, buildJsonObject {
        put("comments", JsonArray(rows)); put("pagination", buildJsonObject {
            put("page_num", number); put("total_pages", totalPages); put("total", total)
        })
    }.toString(), "")
    @Test fun `empty comments use actual CLI response and do not query users`() {
        var calls = 0
        val result = MeegleRequirementCommentsProvider(runner { command ->
            calls++; assertTrue("comment" in command); page(1, 0, 0, emptyList())
        }).read(item)
        assertTrue(result.comments.isEmpty()); assertEquals(1, calls)
    }
    @Test fun `all pages keep full text replies attachments and resolved author names`() {
        val commands = mutableListOf<List<String>>()
        val text = "第一行\n" + "完整正文".repeat(1000) + "\n"
        val result = MeegleRequirementCommentsProvider(runner { command ->
            commands.add(command)
            if ("comment" in command) {
                val number = command[command.indexOf("--page-num") + 1].toInt()
                page(number, 2, 2, listOf(row(number.toString(), text)))
            } else CommandResult(0, """[{"user_key":"user","name_cn":"作者","email":"not-cached@example.com"}]""", "")
        }).read(item)
        assertEquals(listOf("1", "2"), result.comments.map { it.id })
        assertTrue(result.comments.all { it.authorName == "作者" && it.content == text })
        assertEquals("1", result.comments.last().parentId)
        assertEquals("https://example.com/attachment.pdf", result.comments.last().attachmentUrl)
        assertEquals(3, commands.size); assertEquals(1, commands.last().count { it == "--user-keys" })
    }
    @Test fun `author lookups batch at twenty and failure retains comments with one warning`() {
        val batches = mutableListOf<Int>()
        val result = MeegleRequirementCommentsProvider(runner { command ->
            if ("comment" in command) page(1, 1, 43, (1..43).map { row(it.toString(), author = "user$it") })
            else { batches.add(command.count { it == "--user-keys" }); CommandResult(1, "", "users unavailable") }
        }).read(item)
        assertEquals(listOf(20, 20, 3), batches)
        assertEquals(43, result.comments.size); assertEquals(1, result.warnings.size)
        assertEquals("user43", result.comments.last().authorName)
    }
    @Test fun `partial pages repeated pages and inconsistent totals cannot appear as complete comments`() {
        for (mode in listOf("failed", "repeated", "total", "missing", "page", "empty")) {
            val provider = MeegleRequirementCommentsProvider(runner { command ->
                val number = command[command.indexOf("--page-num") + 1].toInt()
                if (number == 1) page(1, 2, 2, listOf(row("1"))) else when (mode) {
                    "failed" -> CommandResult(1, "", "second page failed")
                    "repeated" -> page(2, 2, 2, listOf(row("1")))
                    "total" -> page(2, 2, 3, listOf(row("2")))
                    "page" -> page(1, 2, 2, listOf(row("2")))
                    "empty" -> page(2, 2, 2, emptyList())
                    else -> CommandResult(0, "{}", "")
                }
            })
            assertFails("mode=$mode") { provider.read(item) }
        }
    }
    @Test fun `cancelling user lookup propagates instead of caching a warning`() {
        val provider = MeegleRequirementCommentsProvider(runner { command ->
            if ("comment" in command) page(1, 1, 1, listOf(row("1")))
            else throw java.util.concurrent.CancellationException("cancelled")
        })
        assertFailsWith<java.util.concurrent.CancellationException> { provider.read(item) }
    }
}
