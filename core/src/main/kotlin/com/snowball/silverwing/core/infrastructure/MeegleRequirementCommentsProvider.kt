package com.snowball.silverwing.core

import kotlinx.serialization.json.*
import java.time.Duration

/** Read all pages before returning: a partial response must never replace a complete local cache. */
class MeegleRequirementCommentsProvider(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val executable: MeegleExecutable = MeegleExecutable.pathFallback(),
) {
    fun read(item: ParticipatedWorkItem): RequirementComments {
        val command = executable.resolve()
        val environment = executable.environment()
        fun execute(arguments: List<String>): JsonElement {
            val result = runner.run(listOf(command) + arguments + listOf("--format", "json"),
                timeout = Duration.ofSeconds(20), environment = environment)
            check(result.succeeded) { "读取需求评论失败：${result.stderr.ifBlank { result.stdout }.ifBlank { "退出码 ${result.exitCode}" }.take(300)}" }
            return Json.parseToJsonElement(result.stdout)
        }
        val rows = linkedMapOf<String, RequirementComment>()
        val pages = mutableSetOf<JsonArray>()
        var page = 1
        var expectedTotal: Int? = null
        while (true) {
            require(page <= 1_000) { "评论分页超过读取上限，请在浏览器中查看" }
            val response = execute(listOf("comment", "list", "--project-key", item.projectKey,
                "--work-item-id", item.id, "--page-num", page.toString())) as? JsonObject
                ?: error("评论响应格式无效")
            val comments = response["comments"] as? JsonArray ?: error("评论列表缺失")
            val pagination = response["pagination"] as? JsonObject ?: error("评论分页信息缺失")
            pagination.number("total")?.let { total ->
                require(total >= 0 && (expectedTotal == null || expectedTotal == total)) { "评论在分页期间发生变化，请刷新重试" }
                expectedTotal = total
            }
            require(pagination.number("page_num") == page) { "评论分页页码未前进" }
            val totalPages = pagination.number("total_pages")
            val explicitMore = (pagination["has_more"] as? JsonPrimitive)?.booleanOrNull
            require(totalPages != null || explicitMore != null) { "评论分页状态缺失" }
            require(totalPages == null || totalPages >= 0) { "评论分页状态无效" }
            val hasMore = explicitMore ?: (page < requireNotNull(totalPages))
            if (comments.isNotEmpty()) require(pages.add(comments)) { "评论分页内容重复，未缓存不完整结果" }
            comments.forEach { raw ->
                val row = raw as? JsonObject ?: error("评论条目格式无效")
                val id = row.text("comment_id") ?: error("评论缺少编号")
                val author = row["creator"]
                val authorKey = (author as? JsonPrimitive)?.contentOrNull
                    ?: (author as? JsonObject)?.text("user_key").orEmpty()
                val name = (author as? JsonObject)?.let { it.text("name_cn") ?: it.text("name") ?: it.text("name_en") }
                    ?: authorKey.ifBlank { "未知用户" }
                val content = (row["content"] as? JsonPrimitive)?.contentOrNull ?: error("评论正文格式无效")
                rows[id] = RequirementComment(id, content, authorKey, name, row.text("created_at").orEmpty(),
                    row.text("updated_at"), row.text("parent_id")?.takeUnless { it == "0" }, row.text("file_url"))
            }
            require(rows.size <= 20_000) { "评论数量超过读取上限，请在浏览器中查看" }
            if (!hasMore) {
                require(expectedTotal == null || expectedTotal == rows.size) { "评论未读取完整，请刷新重试" }
                break
            }
            require(comments.isNotEmpty()) { "评论分页未返回下一页内容" }
            page++
        }
        val names = mutableMapOf<String, String>()
        var lookupFailed = false
        rows.values.filter { it.authorKey.isNotBlank() && it.authorName == it.authorKey }.map { it.authorKey }.distinct()
            .chunked(20).forEach { users ->
                try {
                    val arguments = buildList {
                        addAll(listOf("user", "search", "--project-key", item.projectKey, "--need-all-status"))
                        users.forEach { addAll(listOf("--user-keys", it)) }
                    }
                    val people = execute(arguments) as? JsonArray ?: error("评论作者响应格式无效")
                    people.forEach personLoop@ { raw ->
                        val person = raw as? JsonObject ?: return@personLoop
                        val key = person.text("user_key") ?: return@personLoop
                        val name = person.text("name_cn") ?: person.text("name_en") ?: return@personLoop
                        names[key] = name
                    }
                } catch (interrupted: InterruptedException) { throw interrupted }
                catch (cancelled: java.util.concurrent.CancellationException) { throw cancelled }
                catch (_: Exception) { lookupFailed = true }
            }
        return RequirementComments(rows.values.map { it.copy(authorName = names[it.authorKey] ?: it.authorName) },
            if (lookupFailed) listOf("部分评论作者名称未读取，暂显示用户编号；刷新可重试") else emptyList())
    }

    private fun JsonObject.text(key: String) = (get(key) as? JsonPrimitive)?.contentOrNull?.takeIf { it.isNotBlank() }
    private fun JsonObject.number(key: String) = (get(key) as? JsonPrimitive)?.intOrNull
}
