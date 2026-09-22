package com.snowball.silverwing.core

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import java.time.Duration
import java.util.Locale

/**
 * 经本机配置的 Meegle CLI 读取标题和一个面向用户的描述字段；不会向 Codex 命名层返回
 * 标识符、链接、人员、服务或原始响应 JSON。
 */
class MeegleRequirementAiContextProvider(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
) : RequirementAiContextProvider {
    override fun fetch(requirementLink: String, projectKey: String?): RequirementAiContext {
        val workItem = FeishuWorkItemLink.parse(requirementLink)
            ?: throw IllegalArgumentException("仅支持飞书需求链接")
        val resolvedProjectKey = projectKey ?: workItem.projectKey
            ?: throw IllegalStateException("需求链接缺少可用的 Meegle 项目")
        val summary = workItemGet(resolvedProjectKey, workItem.workItemId)
        val title = title(summary) ?: throw IllegalStateException("需求缺少标题")
        val body = standardFieldBody(summary) ?: directBody(summary) ?: customFieldBody(summary, resolvedProjectKey, workItem)
            ?: throw IllegalStateException("需求缺少可读取的正文")
        return RequirementAiContext(
            title = title,
            body = RequirementAiContext.truncateBody(normalizeBody(body)),
        )
    }

    private fun workItemGet(projectKey: String, workItemId: String, fieldKey: String? = null): JsonElement {
        val command = buildList {
            add(meegleExecutable.resolve())
            add("workitem")
            add("get")
            add("--project-key")
            add(projectKey)
            add("--work-item-id")
            add(workItemId)
            if (fieldKey != null) {
                add("--fields")
                add(fieldKey)
            }
            add("--format")
            add("json")
        }
        return execute(command, "读取需求")
    }

    private fun customFieldBody(summary: JsonElement, projectKey: String, workItem: FeishuWorkItemLink): String? {
        val type = textAt(summary, "work_item_attribute", "work_item_type", "key")
            ?: textAt(summary, "work_item_attribute", "work_item_type", "name")
            ?: workItem.kind
        for (query in CONTENT_QUERIES) {
            val field = execute(
                listOf(
                    meegleExecutable.resolve(), "workitem", "meta-fields",
                    "--project-key", projectKey,
                    "--work-item-type", type,
                    "--page-num", "1",
                    "--field-query", query,
                    "--format", "json",
                ),
                "查询需求正文配置",
            ).contentField() ?: continue
            val fieldKey = textAt(field, "field_key") ?: continue
            val fieldValue = workItemGet(projectKey, workItem.workItemId, fieldKey)
            fieldValues(fieldValue, fieldKey)
                .asSequence()
                .mapNotNull(::textFrom)
                .map(::normalizeBody)
                .firstOrNull(String::isNotBlank)
                ?.let { return it }
        }
        return null
    }

    private fun JsonElement.contentField(): JsonElement? = arrayAt(this, "list")
        .firstOrNull { element ->
            textAt(element, "field_name")?.let { name -> CONTENT_FIELD_NAMES.any { it.equals(name, ignoreCase = true) } } == true
        }

    private fun directBody(root: JsonElement): String? = DIRECT_BODY_PATHS.asSequence()
        .mapNotNull { path -> textFrom(elementAt(root, path)) }
        .map(::normalizeBody)
        .firstOrNull(String::isNotBlank)

    /**
     * Meegle 的标准 Description 不在顶层，而是位于 work_item_fields。
     * 这是首选路径，命中后不必再查询字段元数据，既避免误报缺正文也减少一次 CLI 调用。
     */
    private fun standardFieldBody(root: JsonElement): String? = arrayAt(root, "work_item_fields")
        .asSequence()
        .filter { field ->
            val key = textAt(field, "key")
            val name = textAt(field, "name")
            STANDARD_CONTENT_FIELD_KEYS.any { candidate -> candidate.equals(key, ignoreCase = true) } ||
                CONTENT_FIELD_NAMES.any { candidate -> candidate.equals(name, ignoreCase = true) }
        }
        .mapNotNull { field -> (field as? JsonObject)?.get("value") }
        .mapNotNull(::textFrom)
        .map(::normalizeBody)
        .firstOrNull(String::isNotBlank)

    private fun title(root: JsonElement): String? = sequenceOf(
        elementAt(root, listOf("name")),
        elementAt(root, listOf("work_item_name")),
        elementAt(root, listOf("work_item_attribute", "name")),
        elementAt(root, listOf("work_item_attribute", "work_item_name")),
    ).mapNotNull(::textFrom).map(String::trim).firstOrNull(String::isNotBlank)

    private fun fieldValues(root: JsonElement, fieldKey: String): List<JsonElement> = arrayAt(root, "work_item_fields")
        .filter { textAt(it, "key") == fieldKey }
        .flatMap { field ->
            when (val value = (field as? JsonObject)?.get("value")) {
                is JsonArray -> value.toList()
                null -> emptyList()
                else -> listOf(value)
            }
        }

    /** 从常见 Meegle 富文本值结构中提取用户可读文字。 */
    private fun textFrom(element: JsonElement?): String? {
        if (element == null) return null
        val parts = mutableListOf<String>()
        fun collect(current: JsonElement) {
            when (current) {
                is JsonPrimitive -> current.contentOrNull?.takeIf(String::isNotBlank)?.let(parts::add)
                is JsonArray -> current.forEach(::collect)
                is JsonObject -> {
                    val preferred = listOf("text", "text_value", "string_value", "content", "value", "label")
                        .firstNotNullOfOrNull { key -> current[key] }
                    if (preferred != null) {
                        collect(preferred)
                        current["children"]?.let(::collect)
                    } else {
                        current.values.forEach(::collect)
                    }
                }
            }
        }
        collect(element)
        return parts.joinToString("\n").trim().takeIf(String::isNotBlank)
    }

    private fun normalizeBody(value: String): String = value
        .lineSequence()
        .map(String::trim)
        .filter(String::isNotBlank)
        .joinToString("\n")
        .trim()

    private fun execute(command: List<String>, action: String): JsonElement {
        val result = runner.run(
            command = command,
            timeout = Duration.ofSeconds(12),
            environment = meegleExecutable.environment(),
        )
        check(result.succeeded) { "${action}失败：${commandError(result)}" }
        return runCatching { json.parseToJsonElement(result.stdout) }
            .getOrElse { error -> throw IllegalStateException("${action}返回的 JSON 无法解析", error) }
    }

    private fun commandError(result: CommandResult): String = result.stderr
        .ifBlank { result.stdout }
        .lineSequence()
        .firstOrNull()
        ?.trim()
        ?.take(300)
        .orEmpty()
        .ifBlank { "退出码 ${result.exitCode}" }

    private fun elementAt(element: JsonElement, path: List<String>): JsonElement? {
        var current: JsonElement = element
        path.forEach { key -> current = (current as? JsonObject)?.get(key) ?: return null }
        return current
    }

    private fun textAt(element: JsonElement, vararg path: String): String? =
        (elementAt(element, path.toList()) as? JsonPrimitive)?.contentOrNull?.trim()?.ifBlank { null }

    private fun arrayAt(element: JsonElement, key: String): List<JsonElement> =
        ((element as? JsonObject)?.get(key) as? JsonArray)?.toList().orEmpty()

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
        val STANDARD_CONTENT_FIELD_KEYS = listOf("description", "content", "detail")
        val CONTENT_FIELD_NAMES = listOf("需求描述", "描述", "需求内容", "内容", "详情", "Description", "Content", "Detail")
        val CONTENT_QUERIES = listOf("需求描述", "描述", "内容", "详情")
        val DIRECT_BODY_PATHS = listOf(
            listOf("description"),
            listOf("content"),
            listOf("detail"),
            listOf("work_item_description"),
            listOf("work_item_content"),
            listOf("work_item_attribute", "description"),
            listOf("work_item_attribute", "content"),
        )
    }
}
