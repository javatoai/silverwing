package com.snowball.silverwing.core

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.time.Duration
import java.util.Locale

/** A Sprint linked to a requirement, resolved from the locally authenticated Meegle CLI. */
data class RequirementSprint(
    val id: String,
    val label: String,
    val status: String?,
)

fun interface RequirementIterationProvider {
    /**
     * Resolves every Sprint associated with the supplied requirement. Callers,
     * rather than this transport adapter, own the Sprint selection rule.
     */
    fun resolve(requirementLink: String, projectKey: String): List<RequirementSprint>
}

/**
 * Uses MQL instead of guessing a Sprint label from a requirement title. The
 * response reader intentionally supports Meegle's typed `moql_field_list`
 * shape and lightweight JSON fixtures used by local adapters.
 */
class MeegleRequirementIterationProvider(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
) : RequirementIterationProvider {
    override fun resolve(requirementLink: String, projectKey: String): List<RequirementSprint> {
        val workItem = FeishuWorkItemLink.parse(requirementLink)
            ?: throw IllegalArgumentException("需求链接不是支持的飞书项目工作项链接")
        require(projectKey.isNotBlank()) { "需求链接所在空间没有配置 Meegle project key" }
        val workItemType = workItemTypes[workItem.kind]
            ?: throw IllegalArgumentException("不支持查询 Sprint 的工作项类型：${workItem.kind}")
        val related = query(
            projectKey,
            "SELECT `Sprint` FROM `${projectKey}`.`$workItemType` " +
                "WHERE `Item Id` = '${workItem.workItemId}' LIMIT 1",
        )
        val idsAndLabels = related.flatMap(::sprintsFromRow)
            .distinctBy(RequirementSprint::id)
        return idsAndLabels.map { sprint ->
            val statusRow = query(
                projectKey,
                "SELECT `Status` FROM `${projectKey}`.`Sprint` " +
                    "WHERE `Item Id` = '${sprint.id}' LIMIT 1",
            ).firstOrNull()
            sprint.copy(status = statusRow?.let(::statusFromRow))
        }
    }

    private fun query(projectKey: String, mql: String): List<JsonObject> {
        val result = runner.run(
            command = listOf(
                meegleExecutable.resolve(),
                "workitem",
                "query",
                "--project-key",
                projectKey,
                "--mql",
                mql,
                "--auto-paginate",
                "--format",
                "json",
            ),
            timeout = Duration.ofSeconds(20),
            environment = meegleExecutable.environment(),
        )
        require(result.succeeded) {
            "查询需求关联 Sprint 失败：${result.stderr.trim().ifBlank { "Meegle 命令返回 ${result.exitCode}" }.take(300)}"
        }
        val root = runCatching { json.parseToJsonElement(result.stdout) }
            .getOrElse { throw IllegalStateException("解析 Meegle Sprint 响应失败", it) }
        return rows(root)
    }

    private fun rows(element: JsonElement): List<JsonObject> = buildList {
        fun visit(value: JsonElement) {
            when (value) {
                is JsonObject -> {
                    if (value.containsKey("moql_field_list")) add(value)
                    else value.values.forEach(::visit)
                }
                is JsonArray -> value.forEach(::visit)
                else -> Unit
            }
        }
        visit(element)
    }

    private fun sprintsFromRow(row: JsonObject): List<RequirementSprint> = selectedField(row, "Sprint")
        ?.let(::keyLabelValues)
        .orEmpty()
        .mapNotNull { value ->
            val id = value["key"]?.jsonPrimitive?.content?.trim().orEmpty()
            val label = value["label"]?.jsonPrimitive?.content?.trim().orEmpty()
            if (id.isBlank() || label.isBlank()) null else RequirementSprint(id, label, null)
        }

    private fun statusFromRow(row: JsonObject): String? = selectedField(row, "Status")
        ?.let(::keyLabelValues)
        ?.firstOrNull()
        ?.get("label")
        ?.jsonPrimitive
        ?.content
        ?.trim()
        ?.ifBlank { null }

    private fun selectedField(row: JsonObject, wantedName: String): JsonElement? {
        val fields = row["moql_field_list"]?.jsonArray.orEmpty()
        return fields.firstOrNull { field ->
            field.jsonObject["name"]?.jsonPrimitive?.content?.equals(wantedName, ignoreCase = true) == true ||
                field.jsonObject["key"]?.jsonPrimitive?.content?.replace("_", "")
                    ?.equals(wantedName.replace(" ", ""), ignoreCase = true) == true
        }?.jsonObject?.get("value")
    }

    private fun keyLabelValues(value: JsonElement): List<JsonObject> = when (value) {
        is JsonObject -> value["key_label_value_list"]?.jsonArray
            ?.mapNotNull { it as? JsonObject }
            .orEmpty()
        else -> emptyList()
    }

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
        val workItemTypes = mapOf(
            "userstory" to "User Story",
            "technical" to "Tech Improvement",
            "bug" to "Bug",
            "othertask" to "Task",
        )
    }
}
