package com.snowball.silverwing.core

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.supervisorScope
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import java.time.Duration
import java.util.Locale

/** A read-only Meegle work item from one explicitly configured project. */
data class ParticipatedWorkItem(
    val projectKey: String,
    val projectName: String,
    val type: String,
    val typeLabel: String,
    val id: String,
    val title: String,
    val url: String,
) {
    val key: String get() = "$projectKey:$type:$id"
}

data class ParticipatedWorkItemsResult(
    val items: List<ParticipatedWorkItem>,
    val failures: List<String> = emptyList(),
    val completedQueries: Int = 0,
)

interface ParticipatedWorkItemsSource {
    suspend fun load(projects: List<MeegleProjectConfig>): ParticipatedWorkItemsResult
    fun loadBody(item: ParticipatedWorkItem): String?
}

/** Separate from the active-Sprint picker: this catalog includes every status and archived item. */
class MeegleParticipatedWorkItemsSource(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
    private val bodyReader: MeegleRequirementAiContextProvider = MeegleRequirementAiContextProvider(
        runner = runner,
        isWindows = isWindows,
        meegleExecutable = meegleExecutable,
    ),
    private val loginStatus: () -> MeegleCliStatus = {
        ProcessMeegleCliService(runner = runner, isWindows = isWindows, meegleExecutable = meegleExecutable).status()
    },
) : ParticipatedWorkItemsSource {
    private val dispatcher = Dispatchers.IO.limitedParallelism(MAX_CONCURRENT_REQUESTS)

    override suspend fun load(projects: List<MeegleProjectConfig>): ParticipatedWorkItemsResult = supervisorScope {
        if (projects.isEmpty()) return@supervisorScope ParticipatedWorkItemsResult(emptyList())
        val status = try {
            runInterruptible(dispatcher) { loginStatus() }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            return@supervisorScope ParticipatedWorkItemsResult(
                emptyList(), listOf("Meegle CLI 登录状态检查失败：${error.message.orEmpty().take(300)}"),
            )
        }
        if (!status.installed) return@supervisorScope ParticipatedWorkItemsResult(
            emptyList(), listOf("Meegle CLI 未安装或无法启动，请在设置中检查命令路径"),
        )
        if (!status.authenticated) return@supervisorScope ParticipatedWorkItemsResult(
            emptyList(), listOf(status.authenticationError?.let { "Meegle CLI 登录状态检查失败：${it.take(300)}" }
                ?: "Meegle CLI 未登录，请先在设置中登录"),
        )
        val command = meegleExecutable.resolve()
        val environment = meegleExecutable.environment()
        val requests = projects.distinctBy(MeegleProjectConfig::projectKey).flatMap { project ->
            TYPES.map { type -> project to type }
        }
        val results = requests.map { (project, type) ->
            async { query(project, type, command, environment) }
        }.awaitAll()
        ParticipatedWorkItemsResult(
            items = results.flatMap(QueryResult::items).distinctBy(ParticipatedWorkItem::key),
            failures = results.flatMap(QueryResult::failures),
            completedQueries = results.count { it.completed },
        )
    }

    override fun loadBody(item: ParticipatedWorkItem): String? =
        bodyReader.fetchFullBody(item.url, item.projectKey)

    private suspend fun query(
        project: MeegleProjectConfig,
        type: WorkItemType,
        command: String,
        environment: Map<String, String>,
    ): QueryResult {
        val mql = "SELECT `work_item_id`, `name` FROM `${project.projectKey}`.`${type.mqlName}` " +
            "WHERE array_contains(all_participate_persons(), current_login_user())"
        val context = "${project.simpleName} · ${type.label}"
        return try {
            val result = runInterruptible(dispatcher) {
                runner.run(
                    listOf(command, "workitem", "query", "--project-key", project.projectKey,
                        "--mql", mql, "--auto-paginate", "--format", "json"),
                    timeout = Duration.ofSeconds(60),
                    environment = environment,
                )
            }
            if (!result.succeeded) {
                QueryResult(failures = listOf("$context：${commandError(result)}"))
            } else {
                val parsed = parseRows(json.parseToJsonElement(result.stdout), project, type)
                QueryResult(parsed.items, parsed.failures.map { "$context：$it" }, completed = true)
            }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            QueryResult(failures = listOf("$context：${error.message.orEmpty().take(300).ifBlank { "读取失败" }}"))
        }
    }

    private fun parseRows(root: JsonElement, project: MeegleProjectConfig, type: WorkItemType): QueryResult {
        require(root is JsonObject && (root.containsKey("data") || root.containsKey("list"))) {
            "Meegle 未返回可识别的工作项列表"
        }
        val rows = mutableListOf<JsonObject>()
        fun collect(value: JsonElement) {
            when (value) {
                is JsonObject -> if (value.containsKey("moql_field_list") || value.containsKey("work_item_id")) {
                    rows += value
                } else value.values.forEach(::collect)
                is JsonArray -> value.forEach(::collect)
                else -> Unit
            }
        }
        collect(root)
        val failures = mutableListOf<String>()
        val items = rows.mapNotNull { row ->
            val id = selectedField(row, "work_item_id")?.let(::fieldText)
            if (id == null || !id.matches(Regex("[0-9]+"))) {
                failures += "返回的工作项缺少有效 ID"
                return@mapNotNull null
            }
            val title = selectedField(row, "name")?.let(::fieldText)
            if (title.isNullOrBlank()) failures += "工作项 $id 缺少标题"
            ParticipatedWorkItem(
                projectKey = project.projectKey,
                projectName = project.simpleName,
                type = type.path,
                typeLabel = type.label,
                id = id,
                title = title?.takeIf(String::isNotBlank) ?: "未命名工作项 #$id",
                url = "https://project.feishu.cn/${project.simpleName}/${type.path}/detail/$id",
            )
        }
        return QueryResult(items, failures, completed = true)
    }

    private fun selectedField(row: JsonObject, key: String): JsonElement? {
        row[key]?.let { return it }
        return (row["moql_field_list"] as? JsonArray)
            ?.mapNotNull { it as? JsonObject }
            ?.firstOrNull { field ->
                (field["key"] as? JsonPrimitive)?.content?.replace("_", "")?.replace(" ", "")
                    ?.equals(key.replace("_", ""), ignoreCase = true) == true
            }
            ?.get("value")
    }

    private fun fieldText(value: JsonElement): String? = when (value) {
        is JsonPrimitive -> value.contentOrNull?.trim()?.ifBlank { null }
        is JsonObject -> listOf("string_value", "text_value", "long_value", "value")
            .firstNotNullOfOrNull { value[it]?.let(::fieldText) }
        else -> null
    }

    private fun commandError(result: CommandResult): String = result.stderr.ifBlank { result.stdout }
        .lineSequence().firstOrNull()?.trim()?.take(300).orEmpty()
        .ifBlank { "退出码 ${result.exitCode}" }

    private data class WorkItemType(val mqlName: String, val path: String, val label: String)
    private data class QueryResult(
        val items: List<ParticipatedWorkItem> = emptyList(),
        val failures: List<String> = emptyList(),
        val completed: Boolean = false,
    )

    private companion object {
        const val MAX_CONCURRENT_REQUESTS = 4
        val json = Json { ignoreUnknownKeys = true }
        val TYPES = listOf(
            WorkItemType("User Story", "userstory", "需求"),
            WorkItemType("Tech Improvement", "technical", "技术改进"),
            WorkItemType("Bug", "bug", "缺陷"),
            WorkItemType("Task", "othertask", "任务"),
        )
    }
}
