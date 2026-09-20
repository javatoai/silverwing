package com.snowball.silverwing.core

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.supervisorScope
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import java.time.Duration
import java.util.Locale

/** Local Meegle CLI implementation. It deliberately receives configured spaces instead of discovering project spaces. */
class MeegleRequirementLinkSource(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val metadata: RequirementMetadataProvider = MeegleRequirementMetadataProvider(runner),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
) : RequirementLinkSource() {
    override val sourceId: String = "meegle"
    private val requestDispatcher = Dispatchers.IO.limitedParallelism(MAX_CONCURRENT_REQUESTS)
    private fun command() = meegleExecutable.resolve()
    private fun environment() = meegleExecutable.environment()
    override fun isInstalled(): Boolean = runCatching {
        runner.run(listOf(command(), "version"), timeout = Duration.ofSeconds(4), environment = environment()).succeeded
    }.getOrDefault(false)
    override suspend fun load(projects: List<MeegleProjectConfig>): RequirementLinkLoadResult = supervisorScope {
        // Resolve the executable once before scheduling concurrent work. This keeps a
        // possible first-run command probe outside the four request slots.
        val invocation = MeegleInvocation(command(), environment())
        val failures = mutableListOf<RequirementLinkFailure>()
        val sprintResults = projects.map { project ->
            async { project to query(project, "Sprint", "`Status` = '进行中'", null, invocation) }
        }.awaitAll()
        sprintResults.mapNotNullTo(failures) { it.second.failure }

        val workItemQueries = sprintResults.flatMap { (project, result) ->
            result.itemIds.flatMap { sprint ->
                types.map { (type, path) ->
                    WorkItemQuery(
                        project = project,
                        sprint = sprint,
                        type = type,
                        path = path,
                    )
                }
            }
        }
        val workItemResults = workItemQueries.map { request ->
            async {
                request to query(
                    project = request.project,
                    type = request.type,
                    where = "array_contains(`Sprint`, '<id:${request.sprint}>') AND array_contains(all_participate_persons(), current_login_user())",
                    sprint = request.sprint,
                    invocation = invocation,
                )
            }
        }.awaitAll()
        workItemResults.mapNotNullTo(failures) { it.second.failure }

        // Merge only after awaitAll(): concurrent workers never mutate shared state,
        // so URL deduplication and displayed ordering remain deterministic.
        val links = linkedMapOf<String, String>()
        workItemResults.forEach { (request, result) ->
            result.itemIds.forEach { itemId ->
                val url = "https://project.feishu.cn/${request.project.simpleName}/${request.path}/detail/$itemId"
                links.putIfAbsent(url, request.project.projectKey)
            }
        }
        val candidates = links.map { (url, projectKey) ->
            async {
                RequirementLinkCandidate(
                    title = fetchTitle(url, projectKey) ?: "未读取到需求标题",
                    url = url,
                    sourceId = sourceId,
                )
            }
        }.awaitAll()
        RequirementLinkLoadResult(candidates, failures)
    }

    private suspend fun query(
        project: MeegleProjectConfig,
        type: String,
        where: String,
        sprint: String?,
        invocation: MeegleInvocation,
    ): QueryResult {
        val mql = "SELECT `Item Id` FROM `${project.projectKey}`.`$type` WHERE $where LIMIT 100"
        val result = try {
            runInterruptible(requestDispatcher) {
                runner.run(
                    listOf(invocation.command, "workitem", "query", "--project-key", project.projectKey, "--mql", mql, "--auto-paginate", "--format", "json"),
                    timeout = Duration.ofSeconds(20),
                    environment = invocation.environment,
                )
            }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            return QueryResult(
                failure = RequirementLinkFailure("query", project.projectKey, sprint, type, error.message.orEmpty()),
            )
        }
        if (!result.succeeded) {
            return QueryResult(
                failure = RequirementLinkFailure("query", project.projectKey, sprint, type, result.stderr.take(300)),
            )
        }
        return try {
            QueryResult(itemIds(json.parseToJsonElement(result.stdout)))
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            QueryResult(
                failure = RequirementLinkFailure("parse", project.projectKey, sprint, type, error.message.orEmpty()),
            )
        }
    }

    private suspend fun fetchTitle(url: String, projectKey: String): String? = try {
        runInterruptible(requestDispatcher) { metadata.fetch(url, projectKey)?.title }
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (_: Throwable) {
        null
    }

    private data class MeegleInvocation(val command: String, val environment: Map<String, String>)
    private data class QueryResult(
        val itemIds: List<String> = emptyList(),
        val failure: RequirementLinkFailure? = null,
    )
    private data class WorkItemQuery(
        val project: MeegleProjectConfig,
        val sprint: String,
        val type: String,
        val path: String,
    )

    private fun itemIds(element: JsonElement): List<String> = mutableListOf<String>().also { collectItemIds(element, it) }
    private fun collectItemIds(element: JsonElement, results: MutableList<String>) { when (element) {
        is JsonObject -> {
            // Actual `workitem query --format json` responses encode each selected
            // column as { key: "work_item_id", value: { long_value: ... } }.
            // Read that pair before recursively handling simpler fixture shapes.
            val typedFieldKey = (element["key"] as? JsonPrimitive)?.content.orEmpty()
            if (typedFieldKey.normalizedFieldName() in itemIdFieldNames) {
                itemIdValue(element["value"] ?: return)?.let(results::add)
                return
            }
            element.forEach { (key, value) ->
                if (key.normalizedFieldName() in itemIdFieldNames) {
                    itemIdValue(value)?.let(results::add)
                } else collectItemIds(value, results)
            }
        }
        is kotlinx.serialization.json.JsonArray -> element.forEach { collectItemIds(it, results) }
        else -> Unit
    } }

    private fun String.normalizedFieldName(): String = replace("_", "").replace(" ", "").lowercase()

    /**
     * The Meegle CLI represents selected fields as typed values (for example
     * `value.long_value`), while lightweight fixtures may use a direct JSON
     * primitive.  Restricting this unwrapping to the known typed-value keys
     * keeps unrelated numeric response fields out of the result set.
     */
    private fun itemIdValue(value: JsonElement): String? = when (value) {
        is JsonPrimitive -> value.content.takeIf(String::isNotBlank)
        is JsonObject -> listOf("long_value", "string_value", "text_value")
            .firstNotNullOfOrNull { key -> value[key]?.let(::itemIdValue) }
        else -> null
    }
    private companion object {
        const val MAX_CONCURRENT_REQUESTS = 4
        val json = kotlinx.serialization.json.Json { ignoreUnknownKeys = true }
        val itemIdFieldNames = setOf("itemid", "workitemid")
        val types = linkedMapOf("User Story" to "userstory", "Tech Improvement" to "technical", "Bug" to "bug", "Task" to "othertask")
    }
}
