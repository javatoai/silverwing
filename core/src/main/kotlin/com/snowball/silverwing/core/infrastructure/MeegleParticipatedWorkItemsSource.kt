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
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.longOrNull
import java.math.BigDecimal
import java.time.Duration
import java.time.OffsetDateTime
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
    val developers: List<RequirementPerson> = emptyList(),
    val qcOwners: List<RequirementPerson> = emptyList(),
    val productManagers: List<RequirementPerson> = emptyList(),
    val mySprintEstimateDays: BigDecimal? = null,
    val metadataWarnings: List<String> = emptyList(),
    val status: String? = null,
) {
    val key: String get() = "$projectKey:$type:$id"
}

internal data class SprintDateRange(val start: Long, val end: Long) {
    init {
        require(start <= end) { "排期开始时间晚于结束时间" }
    }

    fun overlaps(other: SprintDateRange): Boolean = start <= other.end && end >= other.start
}

internal fun personalNodeEstimate(
    node: JsonObject,
    currentUserKey: String,
    sprintRanges: List<SprintDateRange>,
): BigDecimal {
    nodeKey(node)
    return nodeEstimates(node, currentUserKey).estimate(currentUserKey, sprintRanges)
}

internal fun personalWorkItemEstimate(
    nodes: List<JsonObject>,
    currentUserKey: String,
    sprintRanges: List<SprintDateRange>,
): BigDecimal {
    val uniqueNodes = linkedMapOf<String, NodeEstimates>()
    nodes.forEach { node ->
        val key = nodeKey(node)
        val estimates = nodeEstimates(node, currentUserKey)
        val prior = uniqueNodes.putIfAbsent(key, estimates)
        require(prior == null || prior == estimates) { "节点 $key 的个人估分明细重复且不一致" }
    }
    return uniqueNodes.values.fold(BigDecimal.ZERO) { total, node ->
        total + node.estimate(currentUserKey, sprintRanges)
    }
}

private data class PersonalNodeSchedule(val points: BigDecimal?, val start: Long?, val end: Long?)
private data class SubTaskEstimate(val owners: Set<String>, val schedule: PersonalNodeSchedule)
private data class NodeEstimates(
    val personalSchedules: List<PersonalNodeSchedule>,
    val subTasks: Map<String, SubTaskEstimate>,
) {
    fun estimate(currentUserKey: String, sprintRanges: List<SprintDateRange>): BigDecimal {
        val schedules = if (subTasks.isEmpty()) personalSchedules else {
            val mine = subTasks.values.filter { currentUserKey in it.owners }.map { child ->
                require(child.owners.size == 1 || child.schedule.points == null || child.schedule.points.signum() == 0) {
                    "多人子项缺少个人估分分配，无法计算当前用户估分"
                }
                child.schedule
            }
            val childTotal = mine.fold(BigDecimal.ZERO) { total, schedule -> total + (schedule.points ?: BigDecimal.ZERO) }
            val rollup = personalSchedules.singleOrNull()?.points ?: BigDecimal.ZERO
            require(childTotal.compareTo(rollup) == 0) { "子项个人估分与节点个人汇总不一致，无法确认明细完整性" }
            mine
        }
        return schedules.fold(BigDecimal.ZERO) { total, schedule ->
            val points = schedule.points ?: return@fold total
            val start = schedule.start ?: return@fold total
            val end = schedule.end ?: return@fold total
            val range = SprintDateRange(start, end)
            if (sprintRanges.any(range::overlaps)) total + points else total
        }
    }
}

private fun nodeEstimates(node: JsonObject, currentUserKey: String): NodeEstimates {
    val children = node["sub_tasks"] as? JsonArray ?: error("节点未返回完整子项估分明细")
    val subTasks = linkedMapOf<String, SubTaskEstimate>()
    children.forEach { element ->
        val child = requireNotNull(element as? JsonObject) { "子项估分明细格式无效" }
        val id = child.requiredString("sub_task_id")
        val ownerJson = child.requiredString("owner")
        val owners = Json.parseToJsonElement(ownerJson) as? JsonArray ?: error("子项 $id 人员列表格式无效")
        val userKeys = owners.map { owner ->
            requireNotNull(owner as? JsonObject) { "子项 $id 人员格式无效" }.requiredString("username")
        }.toSet()
        val start = child.optionalOffsetMillis("estimate_start_date")
        val end = child.optionalOffsetMillis("estimate_end_date")
        if (start != null && end != null) require(start <= end) { "子项 $id 排期开始时间晚于结束时间" }
        val estimate = SubTaskEstimate(userKeys, PersonalNodeSchedule(child.optionalPoints(), start, end))
        val prior = subTasks.putIfAbsent(id, estimate)
        require(prior == null || prior == estimate) { "子项 $id 的估分明细重复且不一致" }
    }
    return NodeEstimates(personalSchedules(node, currentUserKey), subTasks)
}

private fun JsonObject.requiredString(key: String): String {
    val value = get(key)
    require(value is JsonPrimitive && value.isString && value.content.isNotBlank()) { "$key 缺少有效文本" }
    return value.content
}

private fun JsonObject.optionalOffsetMillis(key: String): Long? {
    val value = get(key)
    if (value == null || value == JsonNull) return null
    require(value is JsonPrimitive && value.isString) { "子项排期 $key 不是带时区时间" }
    if (value.content.isBlank()) return null
    return OffsetDateTime.parse(value.content).toInstant().toEpochMilli()
}

private fun JsonObject.optionalPoints(): BigDecimal? {
    val value = get("points")
    if (value == null || value == JsonNull) return null
    require(value is JsonPrimitive && !value.isString) { "个人估分不是数字" }
    val points = requireNotNull(value.content.toBigDecimalOrNull()) { "个人估分不是有限数字" }
    require(points.signum() >= 0) { "个人估分不能为负数" }
    return points.stripTrailingZeros()
}

private fun personalSchedules(node: JsonObject, currentUserKey: String): List<PersonalNodeSchedule> {
    val raw = node["assignee_schedule_list"]
    require(raw is JsonArray || raw == JsonNull) { "节点缺少个人排期列表" }
    val schedules = (raw as? JsonArray).orEmpty().mapNotNull { element ->
        val record = requireNotNull(element as? JsonObject) { "个人排期格式无效" }
        val userKey = requireNotNull((record["assignee"] as? JsonObject)?.nonBlankText("user_key")) {
            "个人排期缺少人员标识"
        }
        if (userKey != currentUserKey) return@mapNotNull null
        val info = requireNotNull(record["schedule_info"] as? JsonObject) { "个人排期明细格式无效" }
        val points = info.optionalPoints()
        val start = info.optionalMillis("estimate_start_time")
        val end = info.optionalMillis("estimate_finish_time")
        if (start != null && end != null) require(start <= end) { "个人排期开始时间晚于结束时间" }
        PersonalNodeSchedule(points, start, end)
    }.distinct()
    require(schedules.size <= 1) { "同一节点的个人排期重复且不一致" }
    return schedules
}

private fun nodeKey(node: JsonObject): String =
    requireNotNull((node["basic"] as? JsonObject)?.nonBlankText("node_key")) { "节点缺少有效标识" }

private fun JsonObject.nonBlankText(key: String): String? =
    (get(key) as? JsonPrimitive)?.contentOrNull?.trim()?.takeIf(String::isNotEmpty)

private fun JsonObject.optionalMillis(key: String): Long? {
    val value = get(key)
    if (value == null || value == JsonNull) return null
    require(value is JsonPrimitive && !value.isString) { "排期 $key 不是毫秒时间戳" }
    return requireNotNull(value.longOrNull) { "排期 $key 不是有效毫秒时间戳" }
}

data class ParticipatedSprint(
    val projectKey: String,
    val projectName: String,
    val id: String,
    val title: String,
    val status: String,
) {
    val key: String get() = "$projectKey:$id"
}

data class ParticipatedWorkItemsResult(
    val items: List<ParticipatedWorkItem>,
    val failures: List<String> = emptyList(),
    val completedQueries: Int = 0,
    val sprints: List<ParticipatedSprint>? = null,
    val selectedSprintKey: String? = null,
)

interface ParticipatedWorkItemsSource {
    suspend fun load(
        projects: List<MeegleProjectConfig>,
        sprintKey: String? = null,
        defaultSprintProjectKey: String? = null,
    ): ParticipatedWorkItemsResult
    fun loadBody(item: ParticipatedWorkItem): String?
}

/** Discovers selectable sprints and reads participated items only for the selected sprint. */
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

    override suspend fun load(
        projects: List<MeegleProjectConfig>,
        sprintKey: String?,
        defaultSprintProjectKey: String?,
    ): ParticipatedWorkItemsResult = supervisorScope {
        if (projects.isEmpty()) return@supervisorScope ParticipatedWorkItemsResult(
            emptyList(),
            failures = if (sprintKey == null) emptyList() else listOf("所选 Sprint 已不可选，请重新选择"),
            sprints = emptyList(),
        )
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
        val configuredProjects = projects.distinctBy(MeegleProjectConfig::projectKey)
        val sprintResults = configuredProjects.map { project ->
            async { querySprints(project, command, environment) }
        }.awaitAll()
        val catalogFailures = sprintResults.flatMap(SprintQueryResult::failures)
        val candidates = sprintResults.flatMap(SprintQueryResult::sprints)
            .distinctBy(ParticipatedSprint::key).sortedBy { it.status != "进行中" }
        val catalog = candidates.takeUnless { it.isEmpty() && sprintResults.none { result -> result.completed } }
        val selected = when {
            sprintKey != null -> candidates.singleOrNull { it.key == sprintKey }
            sprintResults.all { it.completed } -> {
                val ongoing = candidates.filter { it.status == "进行中" }
                ongoing.singleOrNull() ?: defaultSprintProjectKey?.takeIf { it.isNotBlank() }?.let { key ->
                    ongoing.singleOrNull { it.projectKey == key }
                }
            }
            else -> null
        }
        if (selected == null) return@supervisorScope ParticipatedWorkItemsResult(
            items = emptyList(),
            failures = catalogFailures + if (sprintKey != null && catalog != null) {
                listOf("所选 Sprint 已不可选，请重新选择")
            } else emptyList(),
            sprints = catalog,
        )
        val project = configuredProjects.single { it.projectKey == selected.projectKey }
        val currentUser = async {
            captureMetadata {
                readJson(command, listOf("user", "me"), environment).nonBlankText("user_key")
                    ?: error("当前登录人缺少 user_key")
            }
        }
        val dateQuery = async { querySprintDate(project, selected.id, command, environment) }
        val requests = TYPES.map { type -> WorkItemRequest(project, type, selected.id) }
        val results = requests.map { request -> async { query(request, command, environment) } }.awaitAll()
        val date = dateQuery.await()
        val user = currentUser.await()
        val incompleteTypes = requests.zip(results).filter { !it.second.completed || it.second.failures.isNotEmpty() }
            .map { it.first.project.projectKey to it.first.type.path }.toSet()
        val occurrences = requests.zip(results).flatMap { (request, result) -> result.items.map { request to it } }
            .groupBy { it.second.key }
        val items = occurrences.values.map { entries ->
            async {
                val item = entries.first().second
                val estimate = captureMetadata {
                    val userKey = user.getOrThrow()
                    require((item.projectKey to item.type) !in incompleteTypes) {
                        "工作项的 Sprint 归属查询不完整"
                    }
                    val ranges = listOf(date.getOrThrow())
                    val nodes = readPages(
                        command,
                        listOf("workflow", "get-node", "--project-key", item.projectKey, "--work-item-id", item.id,
                            "--node-id-list", "_all", "--field-key-list", "_all", "--need-sub-task", "true"),
                        environment,
                    )
                    personalWorkItemEstimate(nodes, userKey, ranges)
                }
                item.copy(
                    mySprintEstimateDays = estimate.getOrNull(),
                    metadataWarnings = (entries.flatMap { it.second.metadataWarnings } +
                        listOfNotNull(estimate.exceptionOrNull()?.let {
                            "${item.projectName} · #${item.id} 个人估分：${metadataError(it)}"
                        })).distinct(),
                )
            }
        }.awaitAll()
        ParticipatedWorkItemsResult(
            items = items,
            failures = catalogFailures + results.flatMap(QueryResult::failures),
            completedQueries = results.count { it.completed },
            sprints = catalog,
            selectedSprintKey = selected.key,
        )
    }

    override fun loadBody(item: ParticipatedWorkItem): String? =
        bodyReader.fetchFullBody(item.url, item.projectKey)

    private suspend fun querySprintDate(
        project: MeegleProjectConfig,
        id: String,
        command: String,
        environment: Map<String, String>,
    ): Result<SprintDateRange> = captureMetadata {
        val metadata = readPages(
            command,
            listOf("workitem", "meta-fields", "--project-key", project.projectKey, "--work-item-type", "Sprint"),
            environment,
        )
        val fieldKey = metadata.filter {
            it.nonBlankText("field_name") == "Duration" && it.nonBlankText("field_type") == "schedule"
        }.singleOrNull()?.nonBlankText("field_key") ?: error("Sprint 的 Duration 排期字段缺失或不唯一")
        val root = readJson(
            command,
            listOf("workitem", "get", "--project-key", project.projectKey, "--work-item-id", id,
                "--fields", fieldKey),
            environment,
        )
        val fields = root["work_item_fields"] as? JsonArray ?: error("Sprint $id 未返回排期字段")
        val field = fields.mapNotNull { it as? JsonObject }.singleOrNull { it.nonBlankText("key") == fieldKey }
        val value = field?.get("value") as? JsonObject ?: error("Sprint $id 未设置排期")
        val start = (value["start_time"] as? JsonObject)?.optionalMillis("timestamp")
            ?: error("Sprint $id 缺少排期开始时间")
        val end = (value["end_time"] as? JsonObject)?.optionalMillis("timestamp")
            ?: error("Sprint $id 缺少排期结束时间")
        SprintDateRange(start, end)
    }

    private suspend fun readJson(
        command: String,
        arguments: List<String>,
        environment: Map<String, String>,
    ): JsonObject {
        val result = runInterruptible(dispatcher) {
            runner.run(listOf(command) + arguments + listOf("--format", "json"),
                timeout = Duration.ofSeconds(20), environment = environment)
        }
        check(result.succeeded) { commandError(result) }
        return json.parseToJsonElement(result.stdout) as? JsonObject ?: error("Meegle 返回不是 JSON 对象")
    }

    private suspend fun readPages(
        command: String,
        arguments: List<String>,
        environment: Map<String, String>,
    ): List<JsonObject> {
        val rows = mutableListOf<JsonObject>()
        val seenPages = mutableSetOf<JsonArray>()
        var page = 1
        while (true) {
            val root = readJson(command, arguments + listOf("--page-num", page.toString()), environment)
            val list = root["list"] as? JsonArray ?: error("Meegle 未返回完整列表")
            val pagination = root["pagination"] as? JsonObject ?: error("Meegle 缺少分页信息")
            val pageNumber = (pagination["page_num"] as? JsonPrimitive)?.intOrNull
            require(pageNumber == page) { "Meegle 返回的分页页码未正确前进" }
            val hasMore = (pagination["has_more"] as? JsonPrimitive)?.takeUnless { it.isString }?.booleanOrNull
                ?: error("Meegle 分页状态无效")
            rows += list.map { it as? JsonObject ?: error("Meegle 列表条目格式无效") }
            if (!hasMore) return rows
            require(list.isNotEmpty() && seenPages.add(list)) { "Meegle 分页内容未前进" }
            page++
        }
    }

    private suspend fun <T> captureMetadata(block: suspend () -> T): Result<T> = try {
        Result.success(block())
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (error: Exception) {
        Result.failure(error)
    }

    private fun metadataError(error: Throwable): String = error.message.orEmpty().lineSequence().firstOrNull()
        .orEmpty().take(300).ifBlank { "读取失败" }

    private class MqlInitialQueryFailure(val result: CommandResult, message: String) : IllegalStateException(message)

    private suspend fun executeMql(
        command: String,
        projectKey: String,
        mql: String,
        environment: Map<String, String>,
        timeout: Duration,
    ): JsonObject {
        suspend fun execute(arguments: List<String>): CommandResult = runInterruptible(dispatcher) {
            runner.run(
                listOf(command, "workitem", "query", "--project-key", projectKey) + arguments + listOf("--format", "json"),
                timeout = timeout,
                environment = environment,
            )
        }
        fun parse(result: CommandResult): JsonObject =
            json.parseToJsonElement(result.stdout) as? JsonObject ?: error("Meegle MQL 返回不是 JSON 对象")
        fun descriptor(root: JsonObject): JsonObject? =
            (root["list"] as? JsonArray)?.firstOrNull() as? JsonObject
        fun totalCount(root: JsonObject): Long =
            (descriptor(root)?.get("count") as? JsonPrimitive)?.takeUnless { it.isString }?.longOrNull
                ?.takeIf { it >= 0 } ?: error("Meegle MQL 缺少有效总条目数")

        val first = execute(listOf("--mql", mql, "--auto-paginate"))
        if (!first.succeeded) throw MqlInitialQueryFailure(first, commandError(first))
        var root = parse(first)
        // Lightweight responses without either pagination marker are already complete.
        if (!root.containsKey("session_id") && descriptor(root)?.containsKey("count") != true) return root
        val session = root.requiredString("session_id")
        // A successful empty first query has no group descriptor or count.
        if (root["list"] == JsonNull && (root["data"] as? JsonObject)?.isEmpty() == true) return root
        val total = totalCount(root)
        val rows = mutableListOf<JsonObject>()
        val seenIds = mutableSetOf<String>()
        var page = 1L
        while (true) {
            // Later responses may omit metadata, but must never contradict the original session/count.
            if (root.containsKey("session_id")) require(root.requiredString("session_id") == session) {
                "Meegle MQL 分页会话发生变化"
            }
            if (root.containsKey("list")) require(totalCount(root) == total) { "Meegle MQL 分页总条目数发生变化" }
            val pageRows = ((root["data"] as? JsonObject)?.get("1") as? JsonArray)
                ?.map { it as? JsonObject ?: error("Meegle MQL 列表条目格式无效") }
                ?: error("Meegle MQL 未返回分组 1 的完整列表")
            val expected = minOf(MQL_PAGE_SIZE.toLong(), total - rows.size)
            require(pageRows.size.toLong() == expected) { "Meegle MQL 第 $page 页条目数不完整或超出总数" }
            val ids = pageRows.map { row ->
                SPRINT_ID_KEYS.firstNotNullOfOrNull { key -> selectedField(row, key)?.let(::fieldText) }
                    ?.takeIf { it.matches(Regex("[0-9]+")) } ?: error("Meegle MQL 条目缺少有效 ID")
            }
            require(pageRows.isEmpty() || seenIds.addAll(ids)) { "Meegle MQL 第 $page 页重复，分页内容未前进" }
            // Count raw MQL entries; downstream parsers still deduplicate work item identities.
            rows += pageRows
            if (rows.size.toLong() == total) return JsonObject(mapOf("data" to JsonObject(mapOf("1" to JsonArray(rows)))))
            page++
            // CLI 1.0.19 cannot paginate MQL sessions automatically. page_num must be a JSON number.
            val result = execute(listOf("--session-id", session, "--params",
                """{"group_pagination_list":[{"group_id":"1","page_num":$page}]}"""))
            check(result.succeeded) { "Meegle MQL 第 $page 页：${commandError(result)}" }
            root = parse(result)
        }
    }

    private suspend fun querySprints(
        project: MeegleProjectConfig,
        command: String,
        environment: Map<String, String>,
    ): SprintQueryResult {
        val mql = "SELECT `work_item_id`, `name`, `work_item_status` FROM `${project.projectKey}`.`Sprint` " +
            "WHERE (`Status` = '进行中' OR `Status` = '未开始')"
        val context = "${project.simpleName} · Sprint"
        return try {
            val root = executeMql(command, project.projectKey, mql, environment, Duration.ofSeconds(20))
            parseSprints(root, project, context)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            SprintQueryResult(failures = listOf("$context：${error.message.orEmpty().take(300).ifBlank { "读取失败" }}"))
        }
    }

    private fun parseSprints(root: JsonElement, project: MeegleProjectConfig, context: String): SprintQueryResult {
        require(root is JsonObject && (root.containsKey("data") || root.containsKey("list"))) {
            "Meegle 未返回可识别的 Sprint 列表"
        }
        val rows = mutableListOf<JsonObject>()
        val failures = mutableListOf<String>()
        fun collect(value: JsonElement, isRow: Boolean = false) {
            when {
                value is JsonObject && (isRow || SPRINT_ROW_MARKERS.any(value::containsKey)) -> rows += value
                value is JsonObject -> value.values.forEach { collect(it) }
                value is JsonArray -> value.forEach { collect(it, isRow = true) }
                else -> failures += "$context：Sprint 列表条目格式无效"
            }
        }
        collect(root["data"] ?: root.getValue("list"))
        val sprints = rows.mapNotNull { row ->
            val id = SPRINT_ID_KEYS.firstNotNullOfOrNull { key -> selectedField(row, key)?.let(::fieldText) }
            if (id == null || !id.matches(Regex("[0-9]+"))) {
                failures += "$context：返回的 Sprint 缺少有效 ID"
                return@mapNotNull null
            }
            val title = selectedField(row, "name")?.let(::sprintTitle)
            val status = workItemStatus(row) ?: if (row.containsKey("moql_field_list")) null else {
                ((row["work_item_status"] ?: row["status"]) as? JsonPrimitive)
                    ?.takeIf { it.isString }?.content?.trim()?.takeIf(String::isNotEmpty)
            }
            if (title == null || status == null) {
                failures += "$context：Sprint $id 缺少有效名称或状态"
                return@mapNotNull null
            }
            if (status != "进行中" && status != "未开始") return@mapNotNull null
            if (status == "未开始" && title.equals("Backlog", ignoreCase = true)) return@mapNotNull null
            ParticipatedSprint(project.projectKey, project.simpleName, id, title, status)
        }
        return SprintQueryResult(sprints.distinctBy(ParticipatedSprint::key), failures, completed = failures.isEmpty())
    }

    private fun sprintTitle(value: JsonElement): String? = when (value) {
        is JsonPrimitive -> value.takeIf { it.isString }?.content?.trim()?.takeIf(String::isNotEmpty)
        is JsonObject -> listOf("string_value", "text_value", "value").firstNotNullOfOrNull { value[it]?.let(::sprintTitle) }
        else -> null
    }

    private suspend fun query(
        request: WorkItemRequest,
        command: String,
        environment: Map<String, String>,
    ): QueryResult {
        val (project, type, sprintId) = request
        val context = "${project.simpleName} · ${type.label}"
        return try {
            val roles = readPages(
                command,
                listOf("workitem", "meta-roles", "--project-key", project.projectKey, "--work-item-type", type.mqlName),
                environment,
            )
            val roleIdsByName = linkedMapOf<String, String>()
            roles.forEach { role ->
                val id = role.requiredString("role_id")
                require(id.none { it.isWhitespace() || it.isISOControl() }) { "role_id 不是有效角色标识" }
                val name = role.requiredString("role_name")
                require(name.none { it == '`' || it == '\\' || it.isISOControl() || it == '\u2028' || it == '\u2029' }) {
                    "角色名称包含不支持的 MQL 标识字符"
                }
                val prior = roleIdsByName.putIfAbsent(name, id)
                require(prior == null || prior == id) { "角色名称 $name 对应多个角色标识，无法无歧义查询" }
            }
            if (roleIdsByName.isEmpty()) return QueryResult(completed = true)
            // Matching uses every People role, independently of the optional display projection.
            val participation = roleIdsByName.keys.joinToString(" OR ") {
                "array_contains(`__$it`, current_login_user())"
            }
            val fromAndWhere = "FROM `${project.projectKey}`.`${type.mqlName}` " +
                "WHERE array_contains(`Sprint`, '<id:$sprintId>') AND ($participation)"
            val baseColumns = listOf("work_item_id", "name", "work_item_status")
            suspend fun execute(columns: List<String>): JsonObject {
                val mql = "SELECT ${columns.joinToString(", ") { "`$it`" }} $fromAndWhere"
                return executeMql(command, project.projectKey, mql, environment, Duration.ofSeconds(60))
            }
            var roleWarning: String? = null
            val root = try {
                execute(baseColumns + type.roleNames.map { "__$it" })
            } catch (error: MqlInitialQueryFailure) {
                val result = error.result
                if (!Regex("\\b3003\\b").containsMatchIn(result.stdout + result.stderr)) throw error
                roleWarning = "$context 人员字段不可用：${commandError(result)}"
                execute(baseColumns)
            }
            val parsed = parseRows(root, project, type)
            val items = if (roleWarning == null) parsed.items else parsed.items.map {
                it.copy(metadataWarnings = it.metadataWarnings + roleWarning)
            }
            QueryResult(items, parsed.failures.map { "$context：$it" }, completed = true)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            QueryResult(failures = listOf("$context：${error.message.orEmpty().take(300).ifBlank { "读取失败" }}"))
        }
    }

    private fun responseRows(root: JsonElement, markers: Set<String>, subject: String): List<JsonObject> {
        require(root is JsonObject && (root.containsKey("data") || root.containsKey("list"))) { subject }
        val rows = mutableListOf<JsonObject>()
        fun collect(value: JsonElement) {
            when (value) {
                is JsonObject -> if (markers.any(value::containsKey)) {
                    rows += value
                } else value.values.forEach(::collect)
                is JsonArray -> value.forEach(::collect)
                else -> Unit
            }
        }
        collect(root)
        return rows
    }

    private fun parseRows(root: JsonElement, project: MeegleProjectConfig, type: WorkItemType): QueryResult {
        val rows = responseRows(root, WORK_ITEM_ROW_MARKERS, "Meegle 未返回可识别的工作项列表")
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
                status = workItemStatus(row),
            ).let { item ->
                try {
                    item.copy(
                        developers = roleMembers(row, type.developerRoles),
                        qcOwners = roleMembers(row, type.qcRoles),
                        productManagers = roleMembers(row, type.productRoles),
                    )
                } catch (error: Exception) {
                    item.copy(metadataWarnings = listOf("${project.simpleName} · #$id 人员：${metadataError(error)}"))
                }
            }
        }
        return QueryResult(items, failures, completed = true)
    }

    private fun workItemStatus(row: JsonObject): String? {
        val value = selectedField(row, "work_item_status") as? JsonObject ?: return null
        val entries = value["key_label_value_list"] as? JsonArray ?: return null
        return entries.map { entry ->
            val label = (entry as? JsonObject)?.get("label") as? JsonPrimitive ?: return null
            if (!label.isString) return null
            label.content.trim().takeIf(String::isNotEmpty) ?: return null
        }.distinct().joinToString("、").ifBlank { null }
    }

    private fun roleMembers(row: JsonObject, names: List<String>): List<RequirementPerson> {
        val fields = (row["moql_field_list"] as? JsonArray).orEmpty().mapNotNull { it as? JsonObject }
        return names.flatMap { name ->
            val field = fields.firstOrNull { it.nonBlankText("name") == "__$name" }
            val value = field?.get("value")
            if (value == null || value == JsonNull) emptyList() else {
                require(value is JsonObject) { "角色 $name 返回格式无效" }
                val users = value["user_value_list"]
                require(users is JsonArray || users == JsonNull) { "角色 $name 未返回人员列表" }
                (users as? JsonArray).orEmpty().map { user -> user as? JsonObject ?: error("角色 $name 人员格式无效") }
            }
        }.map { user ->
            val name = listOf("name_cn", "name_en", "email", "user_key").firstNotNullOfOrNull(user::nonBlankText)
                ?: error("角色成员缺少可展示身份")
            val email = user.nonBlankText("email")
            (user.nonBlankText("user_key") ?: email ?: name) to RequirementPerson(name, email)
        }.distinctBy { it.first }.map { it.second }
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

    private data class WorkItemType(
        val mqlName: String,
        val path: String,
        val label: String,
        val developerRoles: List<String> = listOf("Dev Owner"),
        val qcRoles: List<String> = listOf("QC Owner"),
        val productRoles: List<String> = emptyList(),
    ) {
        val roleNames: List<String> get() = (developerRoles + qcRoles + productRoles).distinct()
    }
    private data class WorkItemRequest(
        val project: MeegleProjectConfig,
        val type: WorkItemType,
        val sprintId: String,
    )
    private data class SprintQueryResult(
        val sprints: List<ParticipatedSprint> = emptyList(),
        val failures: List<String> = emptyList(),
        val completed: Boolean = false,
    )
    private data class QueryResult(
        val items: List<ParticipatedWorkItem> = emptyList(),
        val failures: List<String> = emptyList(),
        val completed: Boolean = false,
    )

    private companion object {
        const val MAX_CONCURRENT_REQUESTS = 4
        const val MQL_PAGE_SIZE = 50
        val json = Json { ignoreUnknownKeys = true }
        // selectedField strips spaces and underscores from response keys but only
        // underscores from the requested key, so these stay space-free to match "Item Id".
        val SPRINT_ID_KEYS = listOf("work_item_id", "item_id")
        val SPRINT_ROW_MARKERS = setOf("moql_field_list", "item_id", "work_item_id", "name", "work_item_status", "status")
        val WORK_ITEM_ROW_MARKERS = setOf("moql_field_list", "work_item_id")
        val TYPES = listOf(
            WorkItemType("User Story", "userstory", "需求", productRoles = listOf("产品经理")),
            WorkItemType("Tech Improvement", "technical", "技术改进"),
            WorkItemType("Bug", "bug", "缺陷"),
            WorkItemType("Task", "othertask", "任务",
                developerRoles = listOf("iOS工程师", "Android工程师", "服务端工程师", "Web端工程师", "嵌入式工程师", "SE工程师"),
                qcRoles = listOf("测试工程师")),
        )
    }
}
