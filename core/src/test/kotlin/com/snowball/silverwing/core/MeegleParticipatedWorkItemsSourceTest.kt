package com.snowball.silverwing.core

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import org.junit.jupiter.api.Test
import java.math.BigDecimal
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class MeegleParticipatedWorkItemsSourceTest {
    @Test
    fun `queries selectable sprints then defaults to the unique in-progress sprint`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val runner = runner { command ->
            commands += command
            val mql = command.valueAfter("--mql")!!
            when (val type = mql.queriedType()) {
                "Sprint" -> CommandResult(0, sprintResponse("777"), "")
                else -> {
                    val id = when (type) {
                        "User Story" -> "101"
                        "Tech Improvement" -> "102"
                        "Bug" -> "103"
                        else -> "104"
                    }
                    CommandResult(0, response(id, "标题 $type"), "")
                }
            }
        }
        val source = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(5, commands.size)
        assertEquals(4, result.completedQueries)
        assertEquals(4, result.items.size)
        assertEquals(listOf("userstory", "technical", "bug", "othertask"), result.items.map { it.type })
        assertEquals("https://project.feishu.cn/obt/bug/detail/103", result.items[2].url)
        val sprintMql = commands.first().valueAfter("--mql")!!
        assertTrue(sprintMql.contains(".`Sprint`"))
        assertEquals(
            "SELECT `work_item_id`, `name`, `work_item_status` FROM `project-key`.`Sprint` " +
                "WHERE (`Status` = '进行中' OR `Status` = '未开始')",
            sprintMql,
        )
        assertFalse(sprintMql.contains("LIMIT"))
        assertEquals("project-key:777", result.selectedSprintKey)
        assertEquals(listOf(ParticipatedSprint("project-key", "obt", "777", "Sprint 777", "进行中")), result.sprints)
        assertFalse(sprintMql.contains("all_participate_persons()"))
        val workItemMqls = commands.drop(1).map { it.valueAfter("--mql")!! }
        workItemMqls.forEach { assertParticipationWhere(it) }
        assertTrue(workItemMqls.none { it.contains("LIMIT") })
        assertTrue(commands.all { "--auto-paginate" in it })
    }

    @Test
    fun `retains all auto-paginated rows and deduplicates repeated work items`() = runBlocking {
        val runner = runner { command ->
            val mql = command.valueAfter("--mql")!!
            when {
                mql.queriedType() == "Sprint" -> CommandResult(0, sprintResponse("777"), "")
                mql.contains("User Story") -> {
                    val rows = (1..61).joinToString(",") { row(it.toString(), "需求 $it") }
                    CommandResult(0, """{"data":{"1":[$rows,${row("61", "需求 61")}]}}""", "")
                }
                else -> CommandResult(0, """{"data":{"1":[]}}""", "")
            }
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
            val mql = command.valueAfter("--mql")!!
            when {
                mql.queriedType() == "Sprint" -> CommandResult(0, sprintResponse("777"), "")
                mql.contains("Bug") -> CommandResult(1, "", "permission denied")
                else -> CommandResult(0, response("123", "可见需求"), "")
            }
        }
        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(3, result.completedQueries)
        assertEquals(3, result.items.size)
        assertEquals(1, result.failures.size)
        assertTrue(result.failures.single().contains("permission denied"))
        assertFalse(result.failures.single().contains("http://"))
    }

    @Test
    fun `reports malformed sprint responses instead of silently returning an empty list`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val source = MeegleParticipatedWorkItemsSource(
            runner { command -> commands += command; CommandResult(0, "{}", "") },
            isWindows = false,
            loginStatus = ::authenticated,
        )

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(1, commands.size)
        assertEquals(0, result.completedQueries)
        assertEquals(1, result.failures.size)
        assertTrue(result.failures.single().contains("Sprint"))
    }

    @Test
    fun `reports malformed type responses after the sprint query succeeds`() = runBlocking {
        val source = MeegleParticipatedWorkItemsSource(
            runner { command ->
                if (command.valueAfter("--mql")!!.queriedType() == "Sprint") CommandResult(0, sprintResponse("777"), "")
                else CommandResult(0, "{}", "")
            },
            isWindows = false,
            loginStatus = ::authenticated,
        )

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(0, result.completedQueries)
        assertEquals(0, result.items.size)
        assertEquals(4, result.failures.size)
    }

    @Test
    fun `returns a successful empty result when a space has no in-progress sprint`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val source = MeegleParticipatedWorkItemsSource(
            runner { command -> commands += command; CommandResult(0, """{"data":{"1":[]}}""", "") },
            isWindows = false,
            loginStatus = ::authenticated,
        )

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(1, commands.size)
        assertEquals(0, result.completedQueries)
        assertEquals(0, result.items.size)
        assertTrue(result.failures.isEmpty())
    }

    @Test
    fun `explicit selection queries only one of multiple in-progress sprints`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val runner = runner { command ->
            commands += command
            val mql = command.valueAfter("--mql")!!
            when {
                mql.queriedType() == "Sprint" -> CommandResult(0, sprintResponse("777", "888"), "")
                mql.contains("User Story") && mql.contains("<id:888>") ->
                    CommandResult(0, """{"data":{"1":[${row("101", "需求 A")},${row("102", "需求 B")}]}}""", "")
                mql.contains("User Story") -> CommandResult(0, response("101", "需求 A"), "")
                else -> CommandResult(0, """{"data":{"1":[]}}""", "")
            }
        }

        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")), "project-key:888")

        assertEquals(4, result.completedQueries)
        assertEquals(2, result.items.size)
        assertEquals(listOf("101", "102"), result.items.map { it.id })
        assertTrue(result.failures.isEmpty())
        assertEquals(listOf("777", "888"), result.sprints!!.map { it.id })
        assertEquals("project-key:888", result.selectedSprintKey)
        val userStoryMqls = commands.map { it.valueAfter("--mql")!! }.filter { it.contains("User Story") }
        assertEquals(1, userStoryMqls.size)
        assertTrue(userStoryMqls.single().contains("<id:888>"))
    }

    @Test
    fun `skips work item queries for a space whose sprint query failed`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val runner = runner { command ->
            commands += command
            val mql = command.valueAfter("--mql")!!
            val projectKey = command.valueAfter("--project-key")!!
            when {
                mql.queriedType() == "Sprint" && projectKey == "project-key-a" -> CommandResult(1, "", "permission denied")
                mql.queriedType() == "Sprint" -> CommandResult(0, sprintResponse("999"), "")
                else -> CommandResult(0, response("201", "B 需求"), "")
            }
        }

        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key-a", "space-a"), MeegleProjectConfig("project-key-b", "space-b")), "project-key-b:999")

        assertEquals(4, result.completedQueries)
        assertEquals(listOf("space-b"), result.items.map { it.projectName }.distinct())
        assertEquals(1, result.failures.size)
        assertTrue(result.failures.single().contains("space-a"))
        assertTrue(result.failures.single().contains("Sprint"))
        assertFalse(result.failures.single().contains("http://"))
        assertEquals(1, commands.count { it.valueAfter("--project-key") == "project-key-a" })
    }

    @Test
    fun `ignores duplicate sprint ids returned by the sprint query`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val runner = runner { command ->
            commands += command
            val mql = command.valueAfter("--mql")!!
            if (mql.queriedType() == "Sprint") CommandResult(0, sprintResponse("777", "777"), "")
            else CommandResult(0, response("101", "需求"), "")
        }

        MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(5, commands.size)
    }

    @Test
    fun `records a failure for sprint rows without a numeric id and keeps valid ones`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val runner = runner { command ->
            commands += command
            val mql = command.valueAfter("--mql")!!
            if (mql.queriedType() == "Sprint") {
                CommandResult(0, """{"data":{"1":[${sprintRow("777")},${sprintRow("abc")}]}}""", "")
            } else CommandResult(0, response("101", "需求"), "")
        }

        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")), "project-key:777")

        assertEquals(4, result.items.size)
        assertEquals(1, result.failures.size)
        assertTrue(result.failures.single().contains("Sprint"))
        assertEquals(listOf("777"), result.sprints!!.map { it.id })
        assertEquals("project-key:777", result.selectedSprintKey)
        result.items.forEach { assertDays("0", it.mySprintEstimateDays) }
        assertEquals(5, commands.size)
    }

    @Test
    fun `accepts lightweight sprint rows keyed by item_id`() = runBlocking {
        val runner = runner { command ->
            val mql = command.valueAfter("--mql")!!
            if (mql.queriedType() == "Sprint") CommandResult(0, """{"data":{"1":[{"item_id":"777","name":"Current","work_item_status":"进行中"}]}}""", "")
            else CommandResult(0, response("101", "需求"), "")
        }

        val result = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
            .load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertEquals(4, result.items.size)
        assertTrue(result.failures.isEmpty())
    }

    @Test
    fun `unauthenticated CLI produces a direct login hint without querying work items`() = runBlocking {
        val commands = CopyOnWriteArrayList<List<String>>()
        val source = MeegleParticipatedWorkItemsSource(
            runner = rawRunner { command -> commands += command; CommandResult(0, "{}", "") },
            isWindows = false,
            loginStatus = { MeegleCliStatus(installed = true, authenticated = false) },
        )

        val result = source.load(listOf(MeegleProjectConfig("project-key", "obt")))

        assertTrue(commands.isEmpty())
        assertEquals(0, result.completedQueries)
        assertTrue(result.failures.single().contains("未登录"))
    }

    @Test
    fun `new item metadata has backward compatible defaults and empty projects make no calls`() = runBlocking {
        val item = ParticipatedWorkItem("project-key", "obt", "userstory", "User Story", "101", "Example", "https://example.invalid/101")
        assertTrue(item.developers.isEmpty())
        assertTrue(item.qcOwners.isEmpty())
        assertTrue(item.productManagers.isEmpty())
        assertNull(item.mySprintEstimateDays)
        assertTrue(item.metadataWarnings.isEmpty())
        val result = load(rawRunner { error("Empty projects must not run a command: $it") }, emptyList())
        assertTrue(result.items.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertEquals(0, result.completedQueries)
    }

    @Test
    fun `all types project their own status labels without using sprint or internal status keys`() = runBlocking {
        val labels = mapOf("User Story" to "开发中", "Tech Improvement" to "待排期", "Task" to "已完成", "Bug" to "待验证")
        val queries = CopyOnWriteArrayList<String>()
        val result = load(runner { command ->
            val mql = command.valueAfter("--mql")!!
            val type = mql.queriedType()
            if (type == "Sprint") return@runner ok(sprintResponse("777"))
            queries += mql
            ok(rows(row("101", type,
                """{"key":"work_item_status","value":{"key_label_value_list":[{"key":"internal-status-id","label":"${labels.getValue(type)}"}]}}""",
            )))
        })
        assertEquals(labels.values.toSet(), result.items.map { it.status }.toSet())
        assertTrue(queries.all { "`work_item_status`" in it.substringBefore(" FROM ") })
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
    }

    @Test
    fun `missing or malformed status stays unknown while multiple actual labels are preserved`() = runBlocking {
        val values = listOf(
            null, "null", "{}", """{"key_label_value_list":[]}""",
            """{"key_label_value_list":[{"key":"not-a-label"}]}""",
            """{"key_label_value_list":[{"label":123}]}""",
            """{"key_label_value_list":[{"label":" "}]}""",
            """{"key_label_value_list":[{"label":"自定义阶段"},{"label":"待验证"},{"label":"自定义阶段"}]}""",
        )
        val result = load(runner { command ->
            when (command.valueAfter("--mql")!!.queriedType()) {
                "Sprint" -> ok(sprintResponse("777"))
                "User Story" -> ok(rows(*values.mapIndexed { index, value ->
                    val fields = value?.let { listOf("""{"key":"work_item_status","value":$it}""") }.orEmpty()
                    row((index + 1).toString(), "Item $index", *fields.toTypedArray())
                }.toTypedArray()))
                else -> ok(rows())
            }
        })
        assertEquals(values.size, result.items.size)
        assertTrue(result.items.dropLast(1).all { it.status == null })
        assertEquals("自定义阶段、待验证", result.items.last().status)
        assertTrue(result.failures.isEmpty())
        assertEquals(4, result.completedQueries)
    }

    @Test
    fun `role fields use dynamic names with ordered display fallbacks and per role deduplication`() = runBlocking {
        val developers = """{"user_value_list":[
            {"user_key":"dev-a","name_cn":"开发甲","name_en":"Developer A","email":"a@example.invalid"},
            {"user_key":"dev-a","name_cn":"开发甲","name_en":"Developer A","email":"a@example.invalid"},
            {"user_key":"dev-b","name_cn":"","name_en":"Developer B"},
            {"email":"c@example.invalid"},{"email":"c@example.invalid"},{"user_key":"dev-d"}
        ]}"""
        val result = load(runner { command ->
            when (command.valueAfter("--mql")!!.queriedType()) {
                "Sprint" -> ok(sprintResponse("777"))
                "User Story" -> ok(rows(row("101", "Roles",
                    roleField("__Dev Owner", developers, "custom_17"),
                    roleField("__QC Owner", """{"user_value_list":[{"user_key":"qc","name_en":"Tester"}]}"""),
                    roleField("__产品经理", """{"user_value_list":[{"user_key":"pm","name_en":"Product"}]}"""),
                )))
                else -> ok(rows())
            }
        })
        val item = result.items.single()
        assertEquals(listOf("开发甲", "Developer B", "c@example.invalid", "dev-d"), item.developers.map { it.name })
        assertEquals("a@example.invalid", item.developers.first().email)
        assertEquals(listOf("Tester"), item.qcOwners.map { it.name })
        assertEquals(listOf("Product"), item.productManagers.map { it.name })
        assertTrue(item.metadataWarnings.isEmpty())
    }

    @Test
    fun `each type projects only its supported roles and Task uses engineering fields not owner or creator`() = runBlocking {
        val engineering = listOf("__iOS工程师", "__Android工程师", "__服务端工程师", "__Web端工程师", "__嵌入式工程师", "__SE工程师")
        val queries = CopyOnWriteArrayList<String>()
        val result = load(runner { command ->
            val mql = command.valueAfter("--mql")!!
            if (mql.queriedType() == "Sprint") return@runner ok(sprintResponse("777"))
            queries += mql
            val fields = when (mql.queriedType()) {
                "Task" -> engineering.mapIndexed { index, name ->
                    roleField(name, """{"user_value_list":[{"user_key":"engineer-$index"}]}""")
                } + listOf(
                    roleField("__测试工程师", """{"user_value_list":[{"user_key":"tester"}]}"""),
                    roleField("Task Owner", """{"user_value_list":[{"user_key":"not-a-developer"}]}"""),
                    roleField("Creator", """{"user_value_list":[{"user_key":"not-a-tester"}]}"""),
                )
                else -> listOf(roleField("__Dev Owner", "null"), roleField("__QC Owner", """{"user_value_list":null}"""))
            }
            ok(rows(row("101", "Typed roles", *fields.toTypedArray())))
        })
        for (mql in queries) {
            val projection = mql.substringBefore(" FROM ")
            val expected = when (mql.queriedType()) {
                "User Story" -> listOf("__Dev Owner", "__QC Owner", "__产品经理")
                "Task" -> engineering + "__测试工程师"
                else -> listOf("__Dev Owner", "__QC Owner")
            }
            expected.forEach { assertTrue(projection.contains("`$it`"), mql) }
            (engineering + listOf("__测试工程师", "__Dev Owner", "__QC Owner", "__产品经理", "Task Owner", "Creator"))
                .filterNot { it in expected }.forEach { assertFalse(projection.contains("`$it`"), mql) }
        }
        val task = result.items.single { it.type == "othertask" }
        assertEquals((0..5).map { "engineer-$it" }, task.developers.map { it.name })
        assertEquals(listOf("tester"), task.qcOwners.map { it.name })
        assertTrue(task.productManagers.isEmpty())
        result.items.filterNot { it.type == "othertask" }.forEach {
            assertTrue(it.developers.isEmpty())
            assertTrue(it.qcOwners.isEmpty())
            assertTrue(it.productManagers.isEmpty())
            assertTrue(it.metadataWarnings.isEmpty(), it.metadataWarnings.toString())
        }
    }

    @Test
    fun `every type matches all discovered People roles independently of projection and zero null or failed estimates`() = runBlocking {
        val types = listOf("User Story", "Tech Improvement", "Bug", "Task")
        val rolesByType = types.associateWith { listOf("产品经理", "质量 QA", "自定义 $it 角色", "Task Owner") }
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(runner(intercept = { command ->
            calls += command
            when {
                command.isAction("workitem", "meta-roles") -> {
                    val type = command.valueAfter("--work-item-type")!!
                    assertEquals("project-b", command.valueAfter("--project-key"))
                    assertEquals("1", command.valueAfter("--page-num"))
                    assertEquals("json", command.valueAfter("--format"))
                    ok(page(*rolesByType.getValue(type).mapIndexed { index, name ->
                        roleMetadata("role_fixture_${types.indexOf(type)}_$index", name)
                    }.toTypedArray()))
                }
                command.isAction("workflow", "get-node") -> when (command.valueAfter("--work-item-id")) {
                    "101" -> ok(page(node(people = listOf(personalRow(points = "0"))).toString()))
                    "102" -> ok(page(node(people = listOf(personalRow(points = "null"))).toString()))
                    else -> CommandResult(1, "", "estimate unavailable")
                }
                else -> null
            }
        }) { command ->
            val mql = command.valueAfter("--mql")!!
            val type = mql.queriedType()
            if (type == "Sprint") return@runner ok(sprintResponse("777"))
            assertParticipationWhere(mql, roles = rolesByType.getValue(type))
            assertFalse("自定义 $type 角色" in mql.substringBefore(" FROM "))
            // The server matched an unprojected role; displayed developers need not include me.
            ok(rows(*(101..103).map { id -> row(id.toString(), "Matched item $id",
                roleField("__Dev Owner", """{"user_value_list":[{"user_key":"fixture-other-user"}]}"""),
            ) }.toTypedArray()))
        }, listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b")), "project-b:777")
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
        assertEquals(12, result.items.size)
        assertEquals(listOf("userstory", "technical", "bug", "othertask"), result.items.map { it.type }.distinct())
        result.items.groupBy { it.type }.values.forEach { items ->
            assertEquals(listOf("101", "102", "103"), items.map { it.id })
            items.take(2).forEach { assertDays("0", it.mySprintEstimateDays); assertTrue(it.metadataWarnings.isEmpty()) }
            assertNull(items.last().mySprintEstimateDays)
            assertTrue(items.last().metadataWarnings.single().contains("estimate unavailable"))
        }
        val metadata = calls.filter { it.isAction("workitem", "meta-roles") }
        assertEquals(types.toSet(), metadata.map { it.valueAfter("--work-item-type") }.toSet())
        assertEquals(4, metadata.size)
        assertEquals(4, calls.count { it.valueAfter("--mql")?.queriedType()?.let { type -> type != "Sprint" } == true })
        assertEquals(12, calls.count { it.isAction("workflow", "get-node") })
    }

    @Test
    fun `role metadata reads all pages beyond fifty before MQL and deduplicates identical role identities`() = runBlocking {
        val names = (1..121).map { "自定义角色 $it" }
        val entries = names.mapIndexed { index, name -> roleMetadata("role_fixture_$index", name) }
        val pagedEntries = entries + entries.first()
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(runner(intercept = { command ->
            calls += command
            if (!command.isAction("workitem", "meta-roles")) return@runner null
            val number = command.valueAfter("--page-num")!!.toInt()
            check(number in 1..3)
            val pageEntries = pagedEntries.drop((number - 1) * 50).take(50)
            ok("""{"list":[${pageEntries.joinToString(",")}],"pagination":{"has_more":${number < 3},"page_num":$number,"page_size":50,"total":122}}""")
        }) { command ->
            val mql = command.valueAfter("--mql")!!
            val type = mql.queriedType()
            if (type == "Sprint") return@runner ok(sprintResponse("777"))
            assertEquals(listOf("1", "2", "3"), calls.filter {
                it.isAction("workitem", "meta-roles") && it.valueAfter("--work-item-type") == type
            }.map { it.valueAfter("--page-num") })
            assertParticipationWhere(mql, roles = names)
            ok(rows())
        })
        assertEquals(4, result.completedQueries)
        assertTrue(result.items.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertEquals(12, calls.count { it.isAction("workitem", "meta-roles") })
        assertEquals(5, calls.count { it.isAction("workitem", "query") })
    }

    @Test
    fun `an empty role catalog completes that type without a business query or unconditional fallback`() = runBlocking {
        for (allEmpty in listOf(true, false)) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner(intercept = { command ->
                calls += command
                if (command.isAction("workitem", "meta-roles") && (allEmpty || command.valueAfter("--work-item-type") == "User Story")) {
                    ok(page())
                } else null
            }) { command ->
                if (command.valueAfter("--mql")!!.queriedType() == "Sprint") ok(sprintResponse("777"))
                else ok(response("101", "Visible item"))
            })
            assertEquals("project-key:777", result.selectedSprintKey)
            assertEquals(4, result.completedQueries)
            assertTrue(result.failures.isEmpty())
            assertEquals(if (allEmpty) 0 else 3, result.items.size)
            assertTrue(result.items.none { it.type == "userstory" })
            assertEquals(4, calls.count { it.isAction("workitem", "meta-roles") })
            assertEquals(if (allEmpty) 1 else 4, calls.count { it.isAction("workitem", "query") })
            assertTrue(calls.none { it.valueAfter("--mql")?.queriedType() == "User Story" })
            assertEquals(if (allEmpty) 0 else 3, calls.count { it.isAction("workflow", "get-node") })
        }
    }

    @Test
    fun `role metadata failure or malformed shape fails only its type without querying or falling back`() = runBlocking {
        val invalid = listOf(
            CommandResult(1, "", "role metadata unavailable"),
            CommandResult(1, """{"code":3003,"msg":"role metadata unavailable"}""", ""),
            ok("not-json"), ok("[]"), ok("{}"), ok("""{"list":null,"pagination":{"has_more":false,"page_num":1}}"""),
            ok("""{"list":{},"pagination":{"has_more":false,"page_num":1}}"""),
            ok("""{"list":[]}"""), ok(page("null")), ok(page("123")),
            ok("""{"list":[],"pagination":{"has_more":"false","page_num":1}}"""),
            ok(page(number = 2)), ok(page(more = true)),
        )
        for (failure in invalid) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner(intercept = { command ->
                calls += command
                if (command.isAction("workitem", "meta-roles") && command.valueAfter("--work-item-type") == "User Story") failure else null
            }) { command ->
                if (command.valueAfter("--mql")!!.queriedType() == "Sprint") ok(sprintResponse("777"))
                else ok(response("101", "Unaffected item"))
            })
            assertEquals(3, result.completedQueries, failure.toString())
            assertEquals(3, result.items.size, failure.toString())
            assertTrue(result.items.none { it.type == "userstory" })
            result.items.forEach { assertDays("0", it.mySprintEstimateDays) }
            assertTrue(result.failures.single().contains("需求"), failure.toString())
            assertTrue(calls.none { it.valueAfter("--mql")?.queriedType() == "User Story" })
            assertEquals(1, calls.count { it.isAction("workitem", "meta-roles") && it.valueAfter("--work-item-type") == "User Story" })
        }
    }

    @Test
    fun `invalid role identities missing names and unsafe identifiers reject the entire type instead of dropping a role`() = runBlocking {
        val invalid = listOf(
            """{"role_name":"产品经理"}""", """{"role_id":null,"role_name":"产品经理"}""",
            """{"role_id":123,"role_name":"产品经理"}""", roleMetadata(" ", "产品经理"),
            roleMetadata("role bad", "产品经理"), roleMetadata("role_\u0000", "产品经理"),
            """{"role_id":"role_fixture_bad"}""", """{"role_id":"role_fixture_bad","role_name":null}""",
            """{"role_id":"role_fixture_bad","role_name":123}""", roleMetadata("role_fixture_bad", ""),
            roleMetadata("role_fixture_bad", " "),
        ) + listOf("bad`name", "bad\\name", "bad\nname", "bad\rname", "bad\tname", "bad\u0000name", "bad\u007fname", "bad\u2028name", "bad\u2029name")
            .map { roleMetadata("role_fixture_bad", it) }
        for (entry in invalid) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner(intercept = { command ->
                calls += command
                if (command.isAction("workitem", "meta-roles") && command.valueAfter("--work-item-type") == "User Story") {
                    ok(page(roleMetadata("role_fixture_good", "有效角色"), entry))
                } else null
            }, block = ::singleItemMql))
            assertEquals(3, result.completedQueries, entry)
            assertTrue(result.items.isEmpty(), entry)
            assertTrue(result.failures.single().contains("需求"), entry)
            assertTrue(calls.none { it.valueAfter("--mql")?.queriedType() == "User Story" }, entry)
            assertTrue(result.failures.single().let { "role_id" in it || "role_name" in it || "MQL 标识字符" in it }, entry)
        }
    }

    @Test
    fun `later role page failure malformed entries repeated pages or conflicting names never issue a partial role query`() = runBlocking {
        val first = roleMetadata("role_fixture_a", "产品经理")
        val invalidPages = listOf(
            CommandResult(1, "", "later role page unavailable"),
            CommandResult(1, """{"code":3003,"msg":"later role page unavailable"}""", ""),
            ok("{}"), ok(page(first, number = 1)), ok(page(first, number = 2, more = true)),
            ok(page("null", number = 2)), ok(page(roleMetadata("role_fixture_b", ""), number = 2)),
            ok(page(roleMetadata("role_fixture_b", "产品经理"), number = 2)),
        )
        for (second in invalidPages) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner(intercept = { command ->
                calls += command
                if (!command.isAction("workitem", "meta-roles") || command.valueAfter("--work-item-type") != "User Story") return@runner null
                when (command.valueAfter("--page-num")) {
                    "1" -> ok(page(first, more = true))
                    "2" -> second
                    else -> error("Must stop at the invalid role page")
                }
            }, block = ::singleItemMql))
            assertEquals(3, result.completedQueries, second.toString())
            assertTrue(result.items.isEmpty())
            assertEquals(1, result.failures.size)
            assertEquals(listOf("1", "2"), calls.filter {
                it.isAction("workitem", "meta-roles") && it.valueAfter("--work-item-type") == "User Story"
            }.map { it.valueAfter("--page-num") })
            assertTrue(calls.none { it.valueAfter("--mql")?.queriedType() == "User Story" })
            if (second.stdout.contains("role_fixture_b") && second.stdout.contains("产品经理")) {
                assertTrue(result.failures.single().contains("多个角色标识"))
            }
        }
        val result = load(runner(intercept = { command ->
            if (command.isAction("workitem", "meta-roles")) {
                ok(page(first, roleMetadata("role_fixture_b", "产品经理")))
            } else null
        }, block = ::singleItemMql))
        assertEquals(0, result.completedQueries)
        assertEquals(4, result.failures.size)
        assertTrue(result.failures.all { "多个角色标识" in it })
    }

    @Test
    fun `unknown optional role error 3003 retries base fields once but other errors never retry`() = runBlocking {
        val queries = CopyOnWriteArrayList<String>()
        val result = load(runner { command ->
            val mql = command.valueAfter("--mql")!!
            queries += mql
            when {
                mql.queriedType() == "Sprint" -> ok(sprintResponse("777"))
                mql.queriedType() == "Bug" -> CommandResult(1, "", "permission denied")
                mql.queriedType() == "User Story" && mql.substringBefore(" FROM ").contains("__Dev Owner") ->
                    CommandResult(1, """{"code":3003,"msg":"Unknown field __Dev Owner"}""", "")
                mql.queriedType() == "User Story" -> ok(rows(row("101", "Still visible",
                    """{"key":"work_item_status","value":{"key_label_value_list":[{"key":"dev","label":"开发中"}]}}""",
                )))
                else -> ok(rows())
            }
        })
        val retries = queries.filter { it.queriedType() == "User Story" }
        assertEquals(2, retries.size)
        assertEquals("SELECT `work_item_id`, `name`, `work_item_status`", retries.last().substringBefore(" FROM "))
        assertEquals(retries.first().substringAfter(" FROM "), retries.last().substringAfter(" FROM "))
        retries.forEach { assertParticipationWhere(it) }
        assertEquals(1, queries.count { it.queriedType() == "Bug" })
        assertEquals(3, result.completedQueries)
        assertEquals(1, result.failures.size)
        val item = result.items.single()
        assertEquals("Still visible", item.title)
        assertEquals("开发中", item.status)
        assertTrue(item.metadataWarnings.isNotEmpty())
        assertTrue(item.developers.isEmpty())
        assertDays("0", item.mySprintEstimateDays)
    }

    @Test
    fun `identity failure leaves base items and query counts intact with unknown estimates`() = runBlocking {
        for (identity in listOf(CommandResult(1, "", "identity unavailable"), ok("{}"))) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner(intercept = { command ->
                calls += command
                if (command.isAction("user", "me")) identity else null
            }, block = ::singleItemMql))
            assertEquals(4, result.completedQueries)
            assertTrue(result.failures.isEmpty())
            val item = result.items.single()
            assertNull(item.mySprintEstimateDays)
            assertTrue(item.metadataWarnings.isNotEmpty())
            assertEquals(1, calls.count { it.isAction("user", "me") })
        }
    }

    @Test
    fun `discovers Sprint date key across metadata pages once per project without guessing field names`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(runner(intercept = { command ->
            calls += command
            when {
                command.isAction("workitem", "meta-fields") -> {
                    assertEquals("Sprint", command.valueAfter("--work-item-type"))
                    when (command.valueAfter("--page-num")) {
                        "1" -> ok(page("""{"field_name":"Other date","field_type":"date","field_key":"not_duration"}""", more = true))
                        "2" -> ok(page(durationField("discovered_schedule_42"), number = 2))
                        else -> error("Unexpected metadata page: $command")
                    }
                }
                command.isAction("workitem", "get") -> {
                    assertEquals("discovered_schedule_42", command.valueAfter("--fields"))
                    ok(sprintDates("discovered_schedule_42"))
                }
                else -> null
            }
        }) { command ->
            when {
                command.valueAfter("--project-key") == "empty-project" -> ok(rows())
                command.valueAfter("--mql")!!.queriedType() == "Sprint" -> ok(sprintResponse("777", "888"))
                command.valueAfter("--mql")!!.queriedType() == "User Story" -> ok(response("101", "Example"))
                else -> ok(rows())
            }
        }, listOf(MeegleProjectConfig("project-key", "obt"), MeegleProjectConfig("empty-project", "empty")), "project-key:777")
        assertDays("0", result.items.single().mySprintEstimateDays)
        assertEquals(4, result.completedQueries)
        assertEquals(1, calls.count { it.isAction("user", "me") })
        assertEquals(listOf("1", "2"), calls.filter { it.isAction("workitem", "meta-fields") }.map { it.valueAfter("--page-num") })
        assertEquals(listOf("777"), calls.filter { it.isAction("workitem", "get") }.map { it.valueAfter("--work-item-id") })
        assertTrue(calls.filter { it.isAction("workitem", "meta-fields") || it.isAction("workitem", "get") }
            .all { it.valueAfter("--project-key") == "project-key" })
        assertEquals(1, calls.count { it.isAction("workflow", "get-node") })
    }

    @Test
    fun `unknown duration metadata or missing dates warns without failing the base list`() = runBlocking {
        val overrides = listOf<Pair<String, CommandResult>>(
            "meta-fields" to ok(page()),
            "meta-fields" to ok(page("""{"field_name":"Duration","field_type":"schedule"}""")),
            "meta-fields" to CommandResult(1, "", "metadata unavailable"),
            "get" to ok("""{"work_item_fields":[]}"""),
            "get" to ok("""{"work_item_fields":[{"key":"duration_key","value":{"start_time":{"timestamp":100},"end_time":null}}]}"""),
        )
        for ((action, response) in overrides) {
            val result = load(runner(intercept = { command ->
                if (command.isAction("workitem", action)) response else null
            }, block = ::singleItemMql))
            assertEquals(4, result.completedQueries)
            assertTrue(result.failures.isEmpty())
            assertEquals("101", result.items.single().id)
            assertNull(result.items.single().mySprintEstimateDays)
            assertTrue(result.items.single().metadataWarnings.isNotEmpty())
        }
    }

    @Test
    fun `current sprint counts one day from children instead of four and a half day rollup`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val development = node(people = listOf(
            personalRow(points = "4.5", start = "1784476800000", end = "1790351999999"),
            personalRow(user = "other", points = "1.5"),
        ), subTasks = listOf(
            subTask("older", points = "2.5", start = "2026-08-17T00:00:00+08:00", end = "2026-08-28T23:59:59+08:00"),
            subTask("previous", points = "1", start = "2026-08-31T00:00:00+08:00", end = "2026-09-11T23:59:59+08:00"),
            subTask("current", points = "1", start = "2026-09-14T00:00:00+08:00", end = "2026-09-25T23:59:59+08:00"),
            subTask("other-person", owners = listOf("other"), points = "1.5", start = "2026-09-14T00:00:00+08:00", end = "2026-09-25T23:59:59+08:00"),
        ))
        val result = load(runner(intercept = { command ->
            calls += command
            when {
                command.isAction("workitem", "get") -> ok(sprintDates(start = 1789315200000, end = 1790351999999))
                command.isAction("workflow", "get-node") -> ok(page(development.toString()))
                else -> null
            }
        }, block = ::singleItemMql))
        assertDays("1", result.items.single().mySprintEstimateDays)
        assertTrue(result.items.single().metadataWarnings.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertDays("1", personalNodeEstimate(development, "me", listOf(SprintDateRange(1789315200000, 1790351999999))))
        assertEquals("true", calls.single { it.isAction("workflow", "get-node") }.valueAfter("--need-sub-task"))
    }

    @Test
    fun `incomplete child coverage never publishes a partial personal estimate`() = runBlocking {
        val candidates = listOf(
            node(people = listOf(personalRow(points = "3")), subTasks = listOf(subTask())),
            node(people = listOf(personalRow(points = "1")), subTasks = listOf(subTask())),
            node(people = listOf(personalRow(points = "null")), subTasks = listOf(subTask())),
            node(people = emptyList(), subTasks = listOf(subTask())),
            node(subTasks = listOf(subTask(owners = listOf("other")))),
        )
        candidates.forEach { candidate ->
            val result = load(runner(intercept = { command ->
                if (command.isAction("workflow", "get-node")) ok(page(candidate.toString())) else null
            }, block = ::singleItemMql))
            assertNull(result.items.single().mySprintEstimateDays, candidate.toString())
            assertTrue(result.items.single().metadataWarnings.isNotEmpty())
            assertTrue(result.failures.isEmpty())
            assertEquals(4, result.completedQueries)
        }
    }

    @Test
    fun `shared child points cannot be attributed to me even outside the sprint`() = runBlocking {
        for (start in listOf("1970-01-01T00:00:00.100Z", "1970-01-01T00:00:00.050Z")) {
            val shared = node(subTasks = listOf(subTask(owners = listOf("me", "other"), start = start, end = start)))
            val result = load(runner(intercept = { command ->
                if (command.isAction("workflow", "get-node")) ok(page(shared.toString())) else null
            }, block = ::singleItemMql))
            assertNull(result.items.single().mySprintEstimateDays)
            assertTrue(result.items.single().metadataWarnings.isNotEmpty())
            assertTrue(result.failures.isEmpty())
        }
    }

    @Test
    fun `child estimates of two and one and a half are not added to parent rollups`() = runBlocking {
        val ranges = listOf(SprintDateRange(100, 200))
        for (points in listOf("2", "1.5")) {
            val nodes = listOf(
                node("owned", listOf(personalRow(points = points)), subTasks = listOf(
                    subTask("owned-child", owners = listOf("me", "me"), points = points),
                )),
                node("others", listOf(personalRow(user = "other-a", points = "3")), subTasks = listOf(
                    subTask("other-child", owners = listOf("other-a", "other-b"), points = "3"),
                )),
                node("unassigned", people = emptyList(), subTasks = listOf(
                    subTask("empty-child", owners = emptyList(), points = "0"),
                )),
                node("shared-zero", people = emptyList(), subTasks = listOf(
                    subTask("shared-empty-child", owners = listOf("me", "other-a"), points = "0"),
                )),
            )
            nodes.zip(listOf(points, "0", "0", "0")).forEach { (candidate, expected) ->
                assertDays(expected, personalNodeEstimate(candidate, "me", ranges), candidate.toString())
            }
            val result = load(runner(intercept = { command ->
                if (command.isAction("workflow", "get-node")) ok(page(*nodes.map { it.toString() }.toTypedArray())) else null
            }, block = ::singleItemMql))
            assertDays(points, result.items.single().mySprintEstimateDays)
            assertTrue(result.items.single().metadataWarnings.isEmpty())
            assertTrue(result.failures.isEmpty())
            assertEquals(4, result.completedQueries)
        }
    }

    @Test
    fun `four child source nodes contribute independently using child dates not parent envelopes`() {
        val nodes = listOf(
            node("a", listOf(personalRow(points = "2", start = "900", end = "1000")),
                name = "Planning", status = "finished", subTasks = listOf(subTask("plan-child"))),
            node("b", listOf(personalRow(points = "1.5", start = "0", end = "50")),
                name = "Review", status = "not_started", subTasks = listOf(subTask("review-child", points = "1.5"))),
            node("c", listOf(personalRow(user = "other", points = "3")),
                name = "Development", status = "in_progress", subTasks = listOf(subTask("other-child", owners = listOf("other"), points = "3"))),
            node("d", listOf(personalRow(points = "4")),
                name = "Testing", status = "finished", subTasks = listOf(subTask("later-child", points = "4",
                    start = "1970-01-01T00:00:00.201Z", end = "1970-01-01T00:00:00.300Z"))),
        )
        val ranges = listOf(SprintDateRange(100, 200))
        nodes.zip(listOf("2", "1.5", "0", "0")).forEach { (candidate, expected) ->
            assertDays(expected, personalNodeEstimate(candidate, "me", ranges), candidate.toString())
        }
        assertDays("3.5", personalWorkItemEstimate(nodes, "me", ranges))
    }

    @Test
    fun `child overlap includes boundaries and full points only once across matching sprints`() {
        val ranges = listOf(SprintDateRange(100, 200), SprintDateRange(150, 250))
        for ((start, end, expected) in listOf(
            Triple("050", "175", "7"), Triple("050", "100", "7"),
            Triple("250", "300", "7"), Triple("175", "175", "7"),
            Triple("100", "200", "7"), Triple("050", "300", "7"),
            Triple("050", "099", "0"), Triple("251", "300", "0"),
        )) {
            val candidate = node(people = listOf(personalRow(points = "7", start = "900", end = "1000")), subTasks = listOf(
                subTask(points = "7", start = "1970-01-01T00:00:00.${start}Z", end = "1970-01-01T00:00:00.${end}Z"),
            ))
            assertDays(expected, personalNodeEstimate(candidate, "me", ranges), "$start..$end")
        }
        val offsetDates = node(subTasks = listOf(subTask(
            start = "1970-01-01T08:00:00.150+08:00", end = "1969-12-31T20:00:00.200-04:00",
        )))
        assertDays("2", personalNodeEstimate(offsetDates, "me", ranges))
        assertDays("0", personalNodeEstimate(offsetDates, "me", emptyList()))
    }

    @Test
    fun `undated children still cover the rollup while null or absent points contribute zero`() = runBlocking {
        val ranges = listOf(SprintDateRange(100, 200))
        val undated = subTask("undated")
        val incompleteDates = listOf(
            JsonObject(undated - "estimate_start_date"),
            JsonObject(undated - "estimate_end_date"),
            JsonObject(undated + ("estimate_start_date" to JsonNull)),
            JsonObject(undated + ("estimate_end_date" to JsonNull)),
        )
        for (child in incompleteDates) {
            val children = listOf(subTask("dated", points = "1"), child)
            val covered = node(people = listOf(personalRow(points = "3")), subTasks = children)
            assertDays("1", personalNodeEstimate(covered, "me", ranges), child.toString())
            // Undated points count toward coverage even though they contribute no Sprint points.
            val mismatch = node(people = listOf(personalRow(points = "1")), subTasks = children)
            val result = load(runner(intercept = { command ->
                if (command.isAction("workflow", "get-node")) ok(page(mismatch.toString())) else null
            }, block = ::singleItemMql))
            assertNull(result.items.single().mySprintEstimateDays, child.toString())
            assertTrue(result.items.single().metadataWarnings.isNotEmpty(), child.toString())
            assertTrue(result.failures.isEmpty())
            assertEquals(4, result.completedQueries)
        }
        for (child in listOf(subTask(points = "null"), JsonObject(subTask() - "points"))) {
            for (people in listOf(emptyList(), listOf(personalRow(points = "null")))) {
                assertDays("0", personalNodeEstimate(node(people = people, subTasks = listOf(child)), "me", ranges), child.toString())
            }
        }
    }

    @Test
    fun `child ids control deduplication and decimal child totals remain exact`() {
        val tenth = subTask("child-a", points = "0.1")
        val twoTenths = subTask("child-b", points = "0.2")
        val first = node("a", listOf(personalRow(points = "0.30")), subTasks = listOf(tenth, tenth, twoTenths))
        val differentId = JsonObject(tenth + ("sub_task_id" to JsonPrimitive("child-c")))
        val second = node("b", listOf(personalRow(points = "0.4")), subTasks = listOf(tenth, twoTenths, differentId))
        val ranges = listOf(SprintDateRange(100, 200))
        assertDays("0.3", personalNodeEstimate(first, "me", ranges))
        assertDays("0.4", personalNodeEstimate(second, "me", ranges))
        assertDays("0.7", personalWorkItemEstimate(listOf(first, first, second), "me", ranges))
    }

    @Test
    fun `duplicate child source nodes normalize child order owner sets decimals and offsets across pages`() = runBlocking {
        val first = node(people = listOf(personalRow(points = "2.00")), subTasks = listOf(
            subTask("mine", owners = listOf("me", "me"), points = "2.00"),
            subTask("others", owners = listOf("other-a", "other-b", "other-a"), points = "3.00"),
        ))
        val offsetStart = "1970-01-01T08:00:00.100+08:00"
        val offsetEnd = "1970-01-01T08:00:00.200+08:00"
        val duplicate = node(people = listOf(personalRow(points = "2.0")), subTasks = listOf(
            subTask("others", owners = listOf("other-b", "other-a"), points = "3.0", start = offsetStart, end = offsetEnd),
            subTask("mine", points = "2", start = offsetStart, end = offsetEnd),
        ))
        val pages = CopyOnWriteArrayList<String?>()
        val result = load(runner(intercept = { command ->
            if (!command.isAction("workflow", "get-node")) return@runner null
            pages += command.valueAfter("--page-num")
            when (command.valueAfter("--page-num")) {
                "1" -> ok(page(first.toString(), more = true))
                "2" -> ok(page(duplicate.toString(), number = 2))
                else -> error("Unexpected node page: $command")
            }
        }, block = ::singleItemMql))
        assertEquals(listOf("1", "2"), pages.toList())
        assertDays("2", result.items.single().mySprintEstimateDays)
        assertTrue(result.items.single().metadataWarnings.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertEquals(4, result.completedQueries)
    }

    @Test
    fun `same rollup with conflicting child input on page two makes the estimate unknown`() = runBlocking {
        val mine = subTask("mine")
        val other = subTask("other", owners = listOf("other-a"), points = "3")
        val first = node(subTasks = listOf(mine, other))
        val conflicts = listOf(
            "dates" to listOf(subTask("mine", start = "1970-01-01T00:00:00.101Z"), other),
            "owners" to listOf(mine, subTask("other", owners = listOf("other-b"), points = "3")),
            "points" to listOf(mine, subTask("other", owners = listOf("other-a"), points = "4")),
            "ids" to listOf(subTask("different-id"), other),
        )
        val ranges = listOf(SprintDateRange(100, 200))
        assertDays("2", personalNodeEstimate(first, "me", ranges))
        for ((label, children) in conflicts) {
            val conflict = node(subTasks = children)
            // Both pages are valid alone: rejection must compare the full child input, not just the rollup.
            assertDays("2", personalNodeEstimate(conflict, "me", ranges), label)
            val pages = CopyOnWriteArrayList<String?>()
            val result = load(runner(intercept = { command ->
                if (!command.isAction("workflow", "get-node")) return@runner null
                pages += command.valueAfter("--page-num")
                when (command.valueAfter("--page-num")) {
                    "1" -> ok(page(first.toString(), more = true))
                    "2" -> ok(page(conflict.toString(), number = 2))
                    else -> error("Unexpected node page: $command")
                }
            }, block = ::singleItemMql))
            assertEquals(listOf("1", "2"), pages.toList(), label)
            assertNull(result.items.single().mySprintEstimateDays, label)
            assertTrue(result.items.single().metadataWarnings.isNotEmpty(), label)
            assertTrue(result.failures.isEmpty(), label)
            assertEquals(4, result.completedQueries, label)
        }
    }

    @Test
    fun `invalid child fields make source estimates unknown without failing base queries`() = runBlocking {
        val child = subTask()
        val valid = node(subTasks = listOf(child))
        val invalidChildren = listOf(
            "malformed owner JSON" to JsonObject(child + ("owner" to JsonPrimitive("not-json"))),
            "encoded owner object" to JsonObject(child + ("owner" to JsonPrimitive("{}"))),
            "unencoded owner array" to JsonObject(child + ("owner" to JsonArray(emptyList()))),
            "owner string entry" to JsonObject(child + ("owner" to JsonPrimitive("""["me"]"""))),
            "numeric username" to JsonObject(child + ("owner" to JsonPrimitive("""[{"username":123}]"""))),
            "negative points" to subTask(points = "-1"),
            "quoted numeric points" to subTask(points = "\"2\""),
            "nonnumeric text points" to subTask(points = "\"not-a-number\""),
            "boolean points" to subTask(points = "true"),
            "malformed date" to subTask(start = "not-a-date"),
            "date without offset" to subTask(end = "1970-01-01T00:00:00.200"),
            "reversed dates" to subTask(start = "1970-01-01T00:00:00.201Z"),
        )
        val candidates = listOf(
            "missing children" to JsonObject(valid - "sub_tasks"),
            "null children" to JsonObject(valid + ("sub_tasks" to JsonNull)),
            "non-array children" to JsonObject(valid + ("sub_tasks" to JsonObject(emptyMap()))),
        ) + invalidChildren.map { (label, malformed) -> label to node(subTasks = listOf(malformed)) }
        for ((label, candidate) in candidates) {
            val result = load(runner(intercept = { command ->
                if (command.isAction("workflow", "get-node")) ok(page(candidate.toString())) else null
            }, block = ::singleItemMql))
            val item = result.items.single()
            assertEquals("101", item.id, label)
            assertNull(item.mySprintEstimateDays, label)
            assertTrue(item.metadataWarnings.isNotEmpty(), label)
            assertTrue(result.failures.isEmpty(), label)
            assertEquals(4, result.completedQueries, label)
        }
    }

    @Test
    fun `all four workflow nodes contribute independently without name or status filtering`() {
        val nodes = listOf(
            node("a", listOf(personalRow(points = "2")), name = "Planning", status = "finished"),
            node("b", listOf(personalRow(points = "1.5")), name = "Planning", status = "not_started"),
            node("c", listOf(personalRow(user = "someone-else", points = "3")), name = "Development"),
            node("d", listOf(personalRow(points = "4", start = "201", end = "300")), name = "Testing"),
        )
        val ranges = listOf(SprintDateRange(100, 200))
        nodes.zip(listOf("2", "1.5", "0", "0")).forEach { (node, expected) ->
            assertDays(expected, personalNodeEstimate(node, "me", ranges))
        }
        assertDays("3.5", personalWorkItemEstimate(nodes, "me", ranges))
    }

    @Test
    fun `personal overlap is inclusive full points and matching several sprints counts once`() {
        val ranges = listOf(SprintDateRange(100, 200), SprintDateRange(150, 250))
        for ((start, end, expected) in listOf(
            Triple("50", "100", "7"), Triple("250", "300", "7"),
            Triple("100", "100", "7"), Triple("50", "300", "7"),
            Triple("50", "99", "0"), Triple("251", "300", "0"),
        )) {
            assertDays(expected, personalNodeEstimate(node(people = listOf(personalRow(points = "7", start = start, end = end))), "me", ranges))
        }
        assertDays("0", personalNodeEstimate(node(), "me", emptyList()))
    }

    @Test
    fun `decimal personal estimates sum exactly without floating point rounding`() {
        val nodes = listOf(node("a", listOf(personalRow(points = "0.1"))), node("b", listOf(personalRow(points = "0.2"))))
        assertDays("0.3", personalWorkItemEstimate(nodes, "me", listOf(SprintDateRange(100, 200))))
    }

    @Test
    fun `absent current user and null personal fields contribute zero without aggregate fallback`() {
        val ranges = listOf(SprintDateRange(100, 200))
        val nodes = listOf(
            node(people = emptyList()),
            node(people = listOf(personalRow(user = "other"))),
            node(people = listOf(personalRow(points = "null"))),
            node(people = listOf(personalRow(start = "null"))),
            node(people = listOf(personalRow(end = "null"))),
            Json.parseToJsonElement("""{"basic":{"node_key":"null-list"},"schedule":{"points":999},"assignee_schedule_list":null,"sub_tasks":[]}""").jsonObject,
        )
        val overlappingAggregate = Json.parseToJsonElement(
            """{"points":999,"estimate_start_time":100,"estimate_finish_time":200}""",
        )
        nodes.forEach {
            assertDays("0", personalNodeEstimate(JsonObject(it + ("schedule" to overlappingAggregate)), "me", ranges))
        }
    }

    @Test
    fun `malformed nodes and invalid personal numbers or intervals fail explicitly`() {
        val malformed = listOf(
            """{"basic":{"node_key":"missing-list"},"schedule":{"points":999},"sub_tasks":[]}""",
            """{"basic":{},"assignee_schedule_list":[],"sub_tasks":[]}""",
            """{"basic":{"node_key":null},"assignee_schedule_list":[],"sub_tasks":[]}""",
            """{"basic":{"node_key":{}},"assignee_schedule_list":[],"sub_tasks":[]}""",
            """{"basic":{"node_key":"a"},"assignee_schedule_list":{},"sub_tasks":[]}""",
        ).map { Json.parseToJsonElement(it).jsonObject } + listOf(
            node(people = listOf(personalRow(points = "-1"))),
            node(people = listOf(personalRow(points = "\"not-a-number\""))),
            node(people = listOf(personalRow(start = "\"invalid\""))),
            node(people = listOf(personalRow(start = "100.5"))),
            node(people = listOf(personalRow(start = "200", end = "100"))),
        )
        malformed.forEach { candidate ->
            assertFailsWith<IllegalArgumentException>(candidate.toString()) {
                personalNodeEstimate(candidate, "me", listOf(SprintDateRange(100, 200)))
            }
        }
    }

    @Test
    fun `identical personal records and workflow nodes collapse but distinct node keys do not`() {
        val person = personalRow(points = "1.5")
        val first = node("a", listOf(person, person), name = "Same name")
        val second = node("b", listOf(person), name = "Same name")
        val ranges = listOf(SprintDateRange(100, 200), SprintDateRange(100, 200))
        assertDays("1.5", personalNodeEstimate(first, "me", ranges))
        assertDays("3", personalWorkItemEstimate(listOf(first, first, second), "me", ranges))
    }

    @Test
    fun `conflicting current user records fail within a node and across duplicate node keys`() {
        val ranges = listOf(SprintDateRange(100, 200))
        val original = personalRow(points = "2")
        for (conflict in listOf(personalRow(points = "3"), personalRow(points = "2", start = "101"))) {
            assertFailsWith<IllegalArgumentException> {
                personalNodeEstimate(node(people = listOf(original, conflict)), "me", ranges)
            }
            assertFailsWith<IllegalArgumentException> {
                personalWorkItemEstimate(listOf(node("a", listOf(original)), node("a", listOf(conflict))), "me", ranges)
            }
        }
    }

    @Test
    fun `source sums all personal nodes using personal dates and ignores aggregate and other users`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val nodes = listOf(
            node("a", listOf(personalRow(), personalRow(user = "other", points = "100")), name = "Review"),
            node("b", listOf(personalRow(points = "1.5")), name = "Review", status = "not_started"),
            node("c", listOf(personalRow(user = "other", points = "3"))),
            node("d", listOf(personalRow(points = "4", start = "201", end = "300"))),
        )
        val result = load(runner(intercept = { command ->
            calls += command
            if (command.isAction("workflow", "get-node")) ok(page(*nodes.map { it.toString() }.toTypedArray())) else null
        }, block = ::singleItemMql))
        assertDays("3.5", result.items.single().mySprintEstimateDays)
        assertTrue(result.items.single().metadataWarnings.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertEquals(4, result.completedQueries)
        assertEquals(5, calls.count { "--mql" in it })
        assertEquals(1, calls.count { it.isAction("user", "me") })
        assertEquals(1, calls.count { it.isAction("workitem", "meta-fields") })
        assertEquals(1, calls.count { it.isAction("workitem", "get") })
        assertEquals(1, calls.count { it.isAction("workflow", "get-node") })
        val nodeCall = calls.single { it.isAction("workflow", "get-node") }
        assertEquals("101", nodeCall.valueAfter("--work-item-id"))
        assertEquals("1", nodeCall.valueAfter("--page-num"))
        assertEquals("project-key", nodeCall.valueAfter("--project-key"))
    }

    @Test
    fun `switching to a future sprint queries only its associations and dates with different estimates`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val runner = runner(intercept = { command ->
            calls += command
            when {
                command.isAction("workitem", "get") && command.valueAfter("--work-item-id") == "888" ->
                    ok(sprintDates(start = 300, end = 400))
                command.isAction("workflow", "get-node") -> when (command.valueAfter("--work-item-id")) {
                    "101" -> ok(page(node(people = listOf(personalRow(points = "3")), subTasks = listOf(
                        subTask("later", points = "2", start = "1970-01-01T00:00:00.300Z", end = "1970-01-01T00:00:00.350Z"),
                        subTask("shared", points = "1", start = "1970-01-01T00:00:00.150Z", end = "1970-01-01T00:00:00.350Z"),
                    )).toString()))
                    "102" -> ok(page(node(people = listOf(personalRow(points = "5", start = "300", end = "350"))).toString()))
                    "103" -> ok(page(node(people = listOf(personalRow(points = "1.5", start = "300", end = "350"))).toString()))
                    else -> error("Unexpected item: $command")
                }
                else -> null
            }
        }) { command ->
            val mql = command.valueAfter("--mql")!!
            when {
                mql.queriedType() == "Sprint" -> ok(rows(sprintRow("777"), sprintRow("888", "Future", "未开始")))
                mql.queriedType() != "User Story" -> ok(rows())
                mql.contains("<id:777>") -> ok(rows(row("101", "Shared"), row("102", "First Sprint only")))
                else -> ok(rows(row("101", "Shared"), row("101", "Shared"), row("103", "Future Sprint only")))
            }
        }
        for ((id, sharedDays) in listOf("777" to "1", "888" to "3")) {
            calls.clear()
            val result = load(runner, sprintKey = "project-key:$id")
            val items = result.items.associateBy { it.id }
            val otherId = if (id == "777") "102" else "103"
            assertEquals(setOf("101", otherId), items.keys)
            assertEquals(4, result.completedQueries)
            assertEquals("project-key:$id", result.selectedSprintKey)
            assertEquals(listOf("777", "888"), result.sprints!!.map { it.id })
            assertDays(sharedDays, items.getValue("101").mySprintEstimateDays)
            assertDays(if (id == "777") "0" else "1.5", items.getValue(otherId).mySprintEstimateDays)
            assertTrue(result.failures.isEmpty())
            assertTrue(items.values.all { it.metadataWarnings.isEmpty() })
            assertEquals(listOf(id), calls.filter { it.isAction("workitem", "get") }.map { it.valueAfter("--work-item-id") })
            val itemQueries = calls.mapNotNull { it.valueAfter("--mql") }.filter { it.queriedType() != "Sprint" }
            assertEquals(4, itemQueries.size)
            assertTrue(itemQueries.all { "array_contains(`Sprint`, '<id:$id>')" in it })
            val traversals = calls.filter { it.isAction("workflow", "get-node") }
            assertEquals(2, traversals.size)
            assertEquals(items.keys, traversals.map { it.valueAfter("--work-item-id") }.toSet())
        }
    }

    @Test
    fun `missing dates on an unselected linked sprint do not affect estimates`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(runner(intercept = { command ->
            calls += command
            when {
                command.isAction("workitem", "get") && command.valueAfter("--work-item-id") != "777" ->
                    ok("""{"work_item_fields":[]}""")
                command.isAction("workflow", "get-node") -> ok(page(node().toString()))
                else -> null
            }
        }) { command ->
            val mql = command.valueAfter("--mql")!!
            when {
                mql.queriedType() == "Sprint" -> ok(sprintResponse("777", "888", "999"))
                mql.queriedType() != "User Story" -> ok(rows())
                mql.contains("<id:777>") -> ok(rows(row("101", "Shared"), row("102", "Unaffected")))
                else -> ok(response("101", "Shared"))
            }
        }, sprintKey = "project-key:777")
        assertEquals(2, result.items.size)
        result.items.forEach {
            assertDays("2", it.mySprintEstimateDays)
            assertTrue(it.metadataWarnings.isEmpty())
        }
        assertEquals(listOf("777"), calls.filter { it.isAction("workitem", "get") }.map { it.valueAfter("--work-item-id") })
        assertTrue(result.failures.isEmpty())
        assertEquals(4, result.completedQueries)
    }

    @Test
    fun `incomplete selected association rows poison estimates only for that type`() = runBlocking {
        val result = load(runner(intercept = { command ->
            if (command.isAction("workflow", "get-node")) ok(page(node().toString())) else null
        }) { command ->
            val mql = command.valueAfter("--mql")!!
            when {
                mql.queriedType() == "Sprint" -> ok(sprintResponse("777", "888"))
                mql.queriedType() == "User Story" -> ok(rows(row("101", "Story"), """{"work_item_id":"invalid","name":"Bad row"}"""))
                mql.queriedType() == "Bug" -> ok(response("201", "Bug"))
                else -> ok(rows())
            }
        }, listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b")), "project-a:777")
        assertEquals(4, result.completedQueries)
        assertEquals(1, result.failures.size)
        assertEquals(2, result.items.size)
        assertTrue(result.items.all { it.projectKey == "project-a" })
        val affected = result.items.single { it.type == "userstory" }
        assertNull(affected.mySprintEstimateDays)
        assertTrue(affected.metadataWarnings.isNotEmpty())
        val unaffected = result.items.single { it.type == "bug" }
        assertDays("2", unaffected.mySprintEstimateDays)
        assertTrue(unaffected.metadataWarnings.isEmpty())
        assertEquals("project-a:777", result.selectedSprintKey)
        assertEquals(4, result.sprints!!.size)
    }

    @Test
    fun `workflow pages advance sequentially and exact duplicate nodes count once across pages`() = runBlocking {
        val pages = CopyOnWriteArrayList<String?>()
        val first = node("a").toString()
        val second = node("b", listOf(personalRow(points = "1.5"))).toString()
        val result = load(runner(intercept = { command ->
            if (!command.isAction("workflow", "get-node")) return@runner null
            pages += command.valueAfter("--page-num")
            when (command.valueAfter("--page-num")) {
                "1" -> ok(page(first, more = true))
                "2" -> ok(page(first, second, number = 2))
                else -> error("Unexpected node page: $command")
            }
        }, block = ::singleItemMql))
        assertEquals(listOf("1", "2"), pages.toList())
        assertDays("3.5", result.items.single().mySprintEstimateDays)
        assertTrue(result.items.single().metadataWarnings.isEmpty())
        assertEquals(4, result.completedQueries)
    }

    @Test
    fun `conflicting workflow records across pages warn instead of publishing a partial estimate`() = runBlocking {
        val result = load(runner(intercept = { command ->
            if (!command.isAction("workflow", "get-node")) return@runner null
            when (command.valueAfter("--page-num")) {
                "1" -> ok(page(node("a").toString(), more = true))
                "2" -> ok(page(node("a", listOf(personalRow(points = "3"))).toString(), number = 2))
                else -> error("Unexpected node page: $command")
            }
        }, block = ::singleItemMql))
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
        assertNull(result.items.single().mySprintEstimateDays)
        assertTrue(result.items.single().metadataWarnings.isNotEmpty())
    }

    @Test
    fun `failed or malformed later workflow pages invalidate the entire estimate and stop traversal`() = runBlocking {
        val secondPages = listOf(
            CommandResult(1, "", "page unavailable"),
            ok(page(number = 1)),
            ok("""{"list":[]}"""),
            ok("""{"list":[],"pagination":{"has_more":"false","page_num":2}}"""),
        )
        for (second in secondPages) {
            val pages = CopyOnWriteArrayList<String?>()
            val result = load(runner(intercept = { command ->
                if (!command.isAction("workflow", "get-node")) return@runner null
                pages += command.valueAfter("--page-num")
                when (command.valueAfter("--page-num")) {
                    "1" -> ok(page(node().toString(), more = true))
                    "2" -> second
                    else -> error("Traversal must stop after an invalid page")
                }
            }, block = ::singleItemMql))
            assertEquals(listOf("1", "2"), pages.toList())
            assertNull(result.items.single().mySprintEstimateDays)
            assertTrue(result.items.single().metadataWarnings.isNotEmpty())
            assertTrue(result.failures.isEmpty())
            assertEquals(4, result.completedQueries)
        }
    }

    @Test
    fun `node transport or malformed payload failure preserves base items and other item estimates`() = runBlocking {
        for (failure in listOf(
            CommandResult(1, "", "nodes unavailable"),
            ok(page("""{"basic":{"node_key":"a"},"schedule":{"points":999},"sub_tasks":[]}""")),
        )) {
            val result = load(runner(intercept = { command ->
                if (!command.isAction("workflow", "get-node")) null
                else if (command.valueAfter("--work-item-id") == "101") failure
                else ok(page(node().toString()))
            }) { command ->
                when (command.valueAfter("--mql")!!.queriedType()) {
                    "Sprint" -> ok(sprintResponse("777"))
                    "User Story" -> ok(rows(row("101", "Unavailable estimate"), row("102", "Available estimate")))
                    else -> ok(rows())
                }
            })
            assertEquals(2, result.items.size)
            assertEquals(4, result.completedQueries)
            assertTrue(result.failures.isEmpty())
            val unavailable = result.items.single { it.id == "101" }
            assertNull(unavailable.mySprintEstimateDays)
            assertTrue(unavailable.metadataWarnings.isNotEmpty())
            assertDays("2", result.items.single { it.id == "102" }.mySprintEstimateDays)
        }
    }

    @Test
    fun `supplementary commands share the four request concurrency bound`() = runBlocking {
        val active = AtomicInteger()
        val maximum = AtomicInteger()
        val nodeCalls = AtomicInteger()
        val roleCalls = AtomicInteger()
        val result = load(rawRunner { command ->
            val current = active.incrementAndGet()
            maximum.updateAndGet { maxOf(it, current) }
            try {
                // Keep calls briefly in flight so an unbounded fan-out is observable.
                Thread.sleep(20)
                if (command.isAction("workflow", "get-node")) nodeCalls.incrementAndGet()
                if (command.isAction("workitem", "meta-roles")) roleCalls.incrementAndGet()
                defaultSupplementary(command) ?: when (command.valueAfter("--mql")!!.queriedType()) {
                    "Sprint" -> ok(sprintResponse("777"))
                    "User Story" -> ok(rows(*(1..12).map { row(it.toString(), "Item $it") }.toTypedArray()))
                    else -> ok(rows())
                }
            } finally {
                active.decrementAndGet()
            }
        })
        assertEquals(12, result.items.size)
        assertEquals(12, nodeCalls.get())
        assertEquals(4, roleCalls.get())
        assertTrue(maximum.get() in 1..4, "Observed ${maximum.get()} concurrent commands")
        assertEquals(0, active.get())
        assertEquals(4, result.completedQueries)
    }

    @Test
    fun `workflow cancellation propagates instead of becoming a metadata warning`() {
        assertFailsWith<CancellationException> {
            runBlocking {
                load(runner(intercept = { command ->
                    if (command.isAction("workflow", "get-node")) throw CancellationException("cancel node fetch")
                    null
                }, block = ::singleItemMql))
            }
        }
    }

    @Test
    fun `catalog filters states and only excludes exact trimmed case insensitive future Backlog`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(rawRunner { command ->
            calls += command
            ok(rows(
                sprintRow("801", "Future", "未开始"),
                sprintRow("802", "Backlog", "进行中"),
                sprintRow("803", "  bAcKlOg  ", "未开始"),
                sprintRow("804", "Finished", "已完成"),
                sprintRow("805", "Backlog planning", "未开始"),
                sprintRow("806", "Cancelled", "已终止"),
                sprintRow("807", "Current", "进行中"),
                sprintRow("801", "Future", "未开始"),
            ))
        })
        assertEquals(listOf("802", "807", "801", "805"), result.sprints!!.map { it.id })
        assertEquals(listOf("Backlog", "Current", "Future", "Backlog planning"), result.sprints.map { it.title })
        assertNull(result.selectedSprintKey)
        assertTrue(result.items.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertEquals(0, result.completedQueries)
        assertEquals(1, calls.size)
    }

    @Test
    fun `one ongoing sprint is selected by default even with future candidates`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(runner(intercept = { calls += it; null }) { command ->
            if (command.valueAfter("--mql")!!.queriedType() == "Sprint") {
                ok(rows(sprintRow("888", "Future", "未开始"), sprintRow("777"), sprintRow("999", "Later", "未开始")))
            } else ok(rows())
        })
        assertEquals("project-key:777", result.selectedSprintKey)
        assertEquals(listOf("777", "888", "999"), result.sprints!!.map { it.id })
        assertEquals(4, result.completedQueries)
        assertEquals(listOf("777"), calls.filter { it.isAction("workitem", "get") }.map { it.valueAfter("--work-item-id") })
        assertTrue(calls.mapNotNull { it.valueAfter("--mql") }.filter { it.queriedType() != "Sprint" }
            .all { "<id:777>" in it })
    }

    @Test
    fun `default project selection matrix preserves the full catalog and selects only an unambiguous ongoing sprint`() = runBlocking {
        val projects = listOf(
            MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b"),
            MeegleProjectConfig("project-c", "space-c"),
        )
        data class Case(val a: Int, val b: Int, val default: String?, val selected: String?)
        val cases = listOf(
            Case(1, 0, null, "project-a:701"), Case(1, 0, "", "project-a:701"),
            Case(1, 0, "missing-project", "project-a:701"), Case(1, 0, "project-b", "project-a:701"),
            Case(1, 0, "project-c", "project-a:701"), Case(0, 1, "project-a", "project-b:801"),
            Case(1, 1, "project-a", "project-a:701"), Case(1, 1, "project-b", "project-b:801"),
            Case(2, 1, "project-b", "project-b:801"), Case(1, 2, "project-a", "project-a:701"),
            Case(1, 1, null, null), Case(1, 1, "", null), Case(1, 1, " ", null),
            Case(1, 1, "missing-project", null), Case(1, 1, "space-a", null), Case(1, 1, "project-c", null),
            Case(2, 1, "project-a", null), Case(2, 0, "project-b", null), Case(2, 2, "project-b", null),
            Case(0, 0, "project-a", null),
        )
        for (case in cases) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val aIds = (1..case.a).map { (700 + it).toString() }
            val bIds = (1..case.b).map { (800 + it).toString() }
            val result = load(runner(intercept = { calls += it; null }) { command ->
                if (command.valueAfter("--mql")!!.queriedType() != "Sprint") return@runner ok(response("101", "Visible item"))
                val project = command.valueAfter("--project-key")!!
                val ids = when (project) { "project-a" -> aIds; "project-b" -> bIds; else -> emptyList() }
                ok(rows(*(
                    listOf(sprintRow("900", "Future", "未开始"), sprintRow("901", " Backlog ", "未开始")) +
                        ids.map { sprintRow(it, if (it == "701") "Backlog" else "Current $it") }
                    ).toTypedArray()))
            }, projects + projects.first(), defaultSprintProjectKey = case.default)
            val label = case.toString()
            assertEquals(case.selected, result.selectedSprintKey, label)
            assertEquals(aIds.map { "project-a:$it" } + bIds.map { "project-b:$it" } + projects.map { "${it.projectKey}:900" },
                result.sprints!!.map { it.key }, label)
            assertTrue(result.failures.isEmpty(), label)
            assertEquals(3, calls.count { it.valueAfter("--mql")?.queriedType() == "Sprint" }, label)
            if (case.selected == null) {
                assertEquals(3, calls.size, label)
                assertEquals(0, result.completedQueries, label)
                assertTrue(result.items.isEmpty(), label)
            } else {
                val project = case.selected.substringBefore(':')
                val sprintId = case.selected.substringAfter(':')
                assertEquals(4, result.completedQueries, label)
                assertEquals(4, result.items.size, label)
                assertTrue(result.items.all { it.projectKey == project }, label)
                assertEquals(4, calls.count { it.isAction("workitem", "meta-roles") }, label)
                assertTrue(calls.filter { it.valueAfter("--project-key") != null && it.valueAfter("--mql")?.queriedType() != "Sprint" }
                    .all { it.valueAfter("--project-key") == project }, label)
                calls.mapNotNull { it.valueAfter("--mql") }.filter { it.queriedType() != "Sprint" }
                    .forEach { assertParticipationWhere(it, sprintId) }
            }
        }
    }

    @Test
    fun `explicit future selection overrides a default project and an invalid explicit key never falls back`() = runBlocking {
        val projects = listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b"))
        for (key in listOf("project-b:888", "project-b:missing", "")) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner(intercept = { calls += it; null }) { command ->
                if (command.valueAfter("--mql")!!.queriedType() == "Sprint") {
                    ok(rows(sprintRow("777"), sprintRow("888", "Future", "未开始")))
                } else ok(response("101", "Future item"))
            }, projects, sprintKey = key, defaultSprintProjectKey = "project-a")
            assertEquals(listOf("project-a:777", "project-b:777", "project-a:888", "project-b:888"), result.sprints!!.map { it.key })
            if (key == "project-b:888") {
                assertEquals(key, result.selectedSprintKey)
                assertEquals(4, result.completedQueries)
                assertTrue(result.items.all { it.projectKey == "project-b" })
                assertTrue(result.failures.isEmpty())
                calls.mapNotNull { it.valueAfter("--mql") }.filter { it.queriedType() != "Sprint" }
                    .forEach { assertParticipationWhere(it, sprintId = "888") }
            } else {
                assertNull(result.selectedSprintKey)
                assertTrue(result.items.isEmpty())
                assertEquals(0, result.completedQueries)
                assertTrue(result.failures.single().contains("所选 Sprint"))
                assertEquals(2, calls.size)
            }
        }
    }

    @Test
    fun `default project cannot select from a partially failed catalog even when its own ongoing sprint is unique`() = runBlocking {
        val projects = listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b"))
        val incomplete = listOf(
            CommandResult(1, "", "catalog unavailable"),
            ok(rows(sprintRow("801"), """{"item_id":"802"}""")),
            ok(mqlPage("incomplete-catalog", 51, (1..50).map { sprintRow(it.toString()) })),
        )
        for (failure in incomplete) {
            for (default in listOf("project-a", "project-b")) {
                val calls = CopyOnWriteArrayList<List<String>>()
                val result = load(rawRunner { command ->
                    calls += command
                    when {
                        "--session-id" in command -> CommandResult(1, "", "catalog page unavailable")
                        command.valueAfter("--project-key") == "project-b" -> failure
                        else -> ok(sprintResponse("777"))
                    }
                }, projects, defaultSprintProjectKey = default)
                assertNotNull(result.sprints)
                assertTrue(result.sprints.any { it.key == "project-a:777" })
                assertNull(result.selectedSprintKey)
                assertTrue(result.items.isEmpty())
                assertEquals(0, result.completedQueries)
                assertEquals(1, result.failures.size)
                assertTrue(calls.all { it.isAction("workitem", "query") })
                assertTrue(calls.mapNotNull { it.valueAfter("--mql") }.all { it.queriedType() == "Sprint" })
            }
        }
    }

    @Test
    fun `multiple or no ongoing sprints wait for selection without supplementary calls`() = runBlocking {
        for (catalog in listOf(
            sprintResponse("777", "888"),
            rows(sprintRow("777", "Only future", "未开始")),
            rows(sprintRow("777", "Future", "未开始"), sprintRow("888", "Later", "未开始")),
            rows(),
        )) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(rawRunner { command -> calls += command; ok(catalog) })
            assertNotNull(result.sprints)
            assertNull(result.selectedSprintKey)
            assertTrue(result.items.isEmpty())
            assertTrue(result.failures.isEmpty())
            assertEquals(0, result.completedQueries)
            assertEquals(1, calls.size)
            assertEquals("Sprint", calls.single().valueAfter("--mql")!!.queriedType())
        }
    }

    @Test
    fun `same sprint id and title in different projects remain distinct and select by full key`() = runBlocking {
        val projects = listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b"))
        val calls = CopyOnWriteArrayList<List<String>>()
        val runner = runner(intercept = { calls += it; null }) { command ->
            singleItemMql(command)
        }
        val unselected = load(runner, projects + projects.first())
        assertEquals(listOf("project-a:777", "project-b:777"), unselected.sprints!!.map { it.key })
        assertEquals(listOf("space-a", "space-b"), unselected.sprints.map { it.projectName })
        assertNull(unselected.selectedSprintKey)
        assertEquals(2, calls.size)
        calls.clear()
        val selected = load(runner, projects, "project-b:777")
        assertEquals(unselected.sprints, selected.sprints)
        assertEquals("project-b:777", selected.selectedSprintKey)
        assertEquals(4, selected.completedQueries)
        assertEquals("project-b", selected.items.single().projectKey)
        assertTrue(calls.filter { command ->
            command.valueAfter("--project-key") != null && command.valueAfter("--mql")?.queriedType() != "Sprint"
        }.all { it.valueAfter("--project-key") == "project-b" })
        assertEquals(1, calls.count { it.isAction("workitem", "get") })
    }

    @Test
    fun `unavailable explicit selection returns a trusted catalog but never falls back`() = runBlocking {
        for (catalog in listOf(
            rows(),
            rows(sprintRow("777", "Completed", "已完成"), sprintRow("888")),
            rows(sprintRow("777", " Backlog ", "未开始"), sprintRow("888")),
            sprintResponse("888"),
        )) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(rawRunner { command -> calls += command; ok(catalog) }, sprintKey = "project-key:777")
            assertNotNull(result.sprints)
            assertNull(result.selectedSprintKey)
            assertTrue(result.items.isEmpty())
            assertEquals(0, result.completedQueries)
            assertTrue(result.failures.single().contains("所选 Sprint"))
            assertEquals(1, calls.size)
        }
    }

    @Test
    fun `auto paginated catalog retains more than one hundred sprints and can select the last`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val first = (1..75).joinToString(",") { sprintRow(it.toString(), "Sprint $it", "未开始") }
        val second = (76..151).joinToString(",") { sprintRow(it.toString(), "Sprint $it", "未开始") }
        val result = load(runner { command ->
            calls += command
            if (command.valueAfter("--mql")!!.queriedType() == "Sprint") {
                ok("""{"data":{"1":[$first],"2":[$second]}}""")
            } else ok(rows())
        }, sprintKey = "project-key:151")
        assertEquals((1..151).map { it.toString() }, result.sprints!!.map { it.id })
        assertEquals("project-key:151", result.selectedSprintKey)
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
        assertEquals(5, calls.size)
        assertTrue(calls.all { "--auto-paginate" in it && "LIMIT" !in it.valueAfter("--mql")!! })
        assertTrue(calls.drop(1).all { "<id:151>" in it.valueAfter("--mql")!! })
    }

    @Test
    fun `missing or malformed catalog metadata is never guessed or treated as a successful empty catalog`() = runBlocking {
        val malformedRows = listOf(
            "{}",
            """{"item_id":"777"}""",
            """{"work_item_id":"777","name":"Current"}""",
            """{"work_item_id":"777","name":" ","status":"进行中"}""",
            """{"work_item_id":"777","name":123,"status":"进行中"}""",
            """{"work_item_id":"777","name":"Current","status":true}""",
            """{"name":"Current","status":"进行中"}""",
            row("777", "Current"),
            row("777", "Current", """{"key":"work_item_status","value":{"key_label_value_list":[{"key":"in_progress"}]}}"""),
            row("777", "Current", """{"key":"work_item_status","value":{"key_label_value_list":[{"label":123}]}}"""),
            row("777", "Current", """{"key":"work_item_status","value":"进行中"}"""),
        )
        for (payload in malformedRows.map { rows(it) } + listOf("{}", """{"data":null}""", """{"data":[true]}""")) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(rawRunner { command -> calls += command; ok(payload) }, sprintKey = "project-key:777")
            assertNull(result.sprints, payload)
            assertNull(result.selectedSprintKey, payload)
            assertTrue(result.items.isEmpty(), payload)
            assertTrue(result.failures.isNotEmpty(), payload)
            assertEquals(0, result.completedQueries, payload)
            assertEquals(1, calls.size, payload)
        }
    }

    @Test
    fun `partial catalog failure suppresses default selection without poisoning explicitly selected estimates`() = runBlocking {
        for (failure in listOf(CommandResult(1, "", "catalog unavailable"), ok(rows("""{"item_id":"888"}""")))) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val projects = listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b"))
            val runner = runner(intercept = { command ->
                calls += command
                if (command.isAction("workflow", "get-node")) ok(page(node().toString())) else null
            }) { command ->
                if (command.valueAfter("--project-key") == "project-a") failure else singleItemMql(command)
            }
            val unselected = load(runner, projects)
            assertEquals(listOf("project-b:777"), unselected.sprints!!.map { it.key })
            assertNull(unselected.selectedSprintKey)
            assertEquals(1, unselected.failures.size)
            assertEquals(2, calls.size)
            val selected = load(runner, projects, "project-b:777")
            assertEquals(unselected.sprints, selected.sprints)
            assertEquals("project-b:777", selected.selectedSprintKey)
            assertEquals(1, selected.failures.size)
            assertEquals(4, selected.completedQueries)
            assertDays("2", selected.items.single().mySprintEstimateDays)
            assertTrue(selected.items.single().metadataWarnings.isEmpty())
        }
    }

    @Test
    fun `all catalog failures return null while one successful empty catalog remains nonnull`() = runBlocking {
        val projects = listOf(MeegleProjectConfig("project-a", "space-a"), MeegleProjectConfig("project-b", "space-b"))
        for (successfulEmpty in listOf(false, true)) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(rawRunner { command ->
                calls += command
                if (successfulEmpty && command.valueAfter("--project-key") == "project-b") ok(rows())
                else CommandResult(1, "", "catalog unavailable")
            }, projects, "project-a:777")
            if (successfulEmpty) assertEquals(emptyList(), result.sprints) else assertNull(result.sprints)
            assertNull(result.selectedSprintKey)
            assertTrue(result.items.isEmpty())
            assertTrue(result.failures.isNotEmpty())
            assertEquals(0, result.completedQueries)
            assertEquals(2, calls.size)
        }
    }

    @Test
    fun `cancellation in catalog identity date role metadata or association queries propagates`() {
        for (stage in listOf("catalog", "identity", "metadata", "date", "roles", "association")) {
            assertFailsWith<CancellationException>(stage) {
                runBlocking {
                    load(runner(intercept = { command ->
                        val cancel = when (stage) {
                            "catalog" -> command.valueAfter("--mql")?.queriedType() == "Sprint"
                            "identity" -> command.isAction("user", "me")
                            "metadata" -> command.isAction("workitem", "meta-fields")
                            "date" -> command.isAction("workitem", "get")
                            "roles" -> command.isAction("workitem", "meta-roles")
                            else -> command.valueAfter("--mql")?.queriedType() == "User Story"
                        }
                        if (cancel) throw CancellationException("cancel $stage")
                        null
                    }, block = ::singleItemMql))
                }
            }
        }
    }

    @Test
    fun `later role metadata cancellation propagates without issuing an incomplete role predicate`() {
        val pages = CopyOnWriteArrayList<String?>()
        val queries = CopyOnWriteArrayList<String>()
        assertFailsWith<CancellationException> {
            runBlocking {
                load(runner(intercept = { command ->
                    command.valueAfter("--mql")?.let { queries += it }
                    if (!command.isAction("workitem", "meta-roles") || command.valueAfter("--work-item-type") != "User Story") return@runner null
                    pages += command.valueAfter("--page-num")
                    when (command.valueAfter("--page-num")) {
                        "1" -> ok(page(roleMetadata("role_fixture_a", "产品经理"), more = true))
                        "2" -> throw CancellationException("cancel later roles")
                        else -> error("No more role pages expected")
                    }
                }, block = ::singleItemMql))
            }
        }
        assertEquals(listOf("1", "2"), pages.toList())
        assertTrue(queries.none { it.queriedType() == "User Story" })
    }

    @Test
    fun `cancelling blocked first or later role metadata interrupts commands without publishing a result`() = runBlocking {
        for (blockedPage in listOf("1", "2")) {
            val started = CompletableDeferred<Unit>()
            val interrupted = AtomicInteger()
            val published = AtomicInteger()
            val calls = CopyOnWriteArrayList<List<String>>()
            val job = launch {
                load(runner(intercept = { command ->
                    calls += command
                    if (!command.isAction("workitem", "meta-roles") || command.valueAfter("--work-item-type") != "User Story") return@runner null
                    if (command.valueAfter("--page-num") != blockedPage) {
                        ok(page(roleMetadata("role_fixture_a", "产品经理"), more = true))
                    } else {
                        started.complete(Unit)
                        try {
                            CountDownLatch(1).await()
                            error("Blocked metadata command should be interrupted")
                        } catch (error: InterruptedException) {
                            interrupted.incrementAndGet()
                            throw error
                        }
                    }
                }, block = ::singleItemMql))
                published.incrementAndGet()
            }
            try {
                withTimeout(5_000) { started.await() }
                withTimeout(5_000) { job.cancelAndJoin() }
                assertTrue(job.isCancelled)
                assertEquals(1, interrupted.get())
                assertEquals(0, published.get())
                assertEquals((1..blockedPage.toInt()).map { it.toString() }, calls.filter {
                    it.isAction("workitem", "meta-roles") && it.valueAfter("--work-item-type") == "User Story"
                }.map { it.valueAfter("--page-num") })
                assertTrue(calls.none { it.valueAfter("--mql")?.queriedType() == "User Story" })
                assertTrue(calls.none { it.isAction("workflow", "get-node") })
            } finally {
                job.cancelAndJoin()
            }
        }
    }

    @Test
    fun `cancelling a blocked catalog interrupts the command and never queries items`() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val calls = CopyOnWriteArrayList<List<String>>()
        val interrupted = AtomicInteger()
        val job = launch {
            load(rawRunner { command ->
                calls += command
                started.complete(Unit)
                try {
                    CountDownLatch(1).await()
                    error("Blocked command should be interrupted")
                } catch (error: InterruptedException) {
                    interrupted.incrementAndGet()
                    throw error
                }
            })
        }
        try {
            withTimeout(5_000) { started.await() }
            withTimeout(5_000) { job.cancelAndJoin() }
            assertTrue(job.isCancelled)
            assertEquals(1, interrupted.get())
            assertEquals(1, calls.size)
            assertEquals("Sprint", calls.single().valueAfter("--mql")!!.queriedType())
        } finally {
            job.cancelAndJoin()
        }
    }

    @Test
    fun `session catalog fetches every page before selecting the unique ongoing sprint on the last page`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val entries = (1..121).map { id ->
            sprintRow(id.toString(), if (id == 1) " Backlog " else "Sprint $id", if (id == 121) "进行中" else "未开始")
        }
        val result = load(runner { command ->
            calls += command
            when {
                "--session-id" in command -> {
                    val number = assertSessionPage(command, "catalog-session")
                    ok(mqlPage("catalog-session", 121, entries.drop((number - 1) * 50).take(50)))
                }
                command.valueAfter("--mql")!!.queriedType() == "Sprint" ->
                    ok(mqlPage("catalog-session", 121, entries.take(50)))
                else -> ok(rows())
            }
        })
        assertEquals(listOf("121") + (2..120).map { it.toString() }, result.sprints!!.map { it.id })
        assertEquals("project-key:121", result.selectedSprintKey)
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
        assertEquals(listOf(2, 3), calls.filter { "--session-id" in it }.map { assertSessionPage(it, "catalog-session") })
        val itemQueries = calls.mapNotNull { it.valueAfter("--mql") }.filter { it.queriedType() != "Sprint" }
        assertEquals(4, itemQueries.size)
        itemQueries.forEach { assertParticipationWhere(it, sprintId = "121") }
    }

    @Test
    fun `all work item types retain session pages over one hundred rows and estimate last page items`() = runBlocking {
        val calls = CopyOnWriteArrayList<List<String>>()
        val result = load(runner(intercept = { command ->
            if (command.isAction("workflow", "get-node")) ok(page(node().toString())) else null
        }) { command ->
            calls += command
            val session = command.valueAfter("--session-id")
            if (session != null) {
                val number = assertSessionPage(command, session)
                val start = (number - 1) * 50 + 1
                ok(mqlPage(session, 121, (start..minOf(number * 50, 121)).map { row(it.toString(), "$session $it") }))
            } else {
                val type = command.valueAfter("--mql")!!.queriedType()
                if (type == "Sprint") ok(sprintResponse("777"))
                else ok(mqlPage(type, 121, (1..50).map { row(it.toString(), "$type $it") }))
            }
        })
        assertEquals(484, result.items.size)
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
        assertEquals("project-key:777", result.selectedSprintKey)
        result.items.groupBy { it.type }.values.forEach { items ->
            assertEquals((1..121).map { it.toString() }, items.map { it.id })
            assertDays("2", items.last().mySprintEstimateDays)
            assertTrue(items.all { it.metadataWarnings.isEmpty() })
        }
        for (type in listOf("User Story", "Tech Improvement", "Bug", "Task")) {
            assertEquals(listOf(2, 3), calls.filter { it.valueAfter("--session-id") == type }.map { assertSessionPage(it, type) })
        }
    }

    @Test
    fun `session count controls exact final page and later pages can omit metadata`() = runBlocking {
        for (count in listOf(0, 1, 49, 50, 51, 100, 101)) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val entries = (1..count).map { sprintRow(it.toString(), "Future $it", "未开始") }
            val result = load(rawRunner { command ->
                calls += command
                if ("--session-id" in command) {
                    val number = assertSessionPage(command, "original-session")
                    ok(rows(*entries.drop((number - 1) * 50).take(50).toTypedArray()))
                } else ok(mqlPage("original-session", count, entries.take(50)))
            })
            assertEquals(count, result.sprints!!.size, "count=$count")
            assertNull(result.selectedSprintKey)
            assertTrue(result.failures.isEmpty())
            assertEquals(maxOf(1, (count + 49) / 50), calls.size, "count=$count")
        }
    }

    @Test
    fun `native empty session response is complete for both catalog and work item queries`() = runBlocking {
        val empty = """{"data":{},"extra_info":null,"list":null,"search_status_info":null,"session_id":"empty-session"}"""
        for (catalog in listOf(true, false)) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(runner { command ->
                calls += command
                check("--session-id" !in command)
                if (!catalog && command.valueAfter("--mql")!!.queriedType() == "Sprint") ok(sprintResponse("777"))
                else ok(empty)
            })
            assertTrue(result.failures.isEmpty(), result.failures.toString())
            assertTrue(result.items.isEmpty())
            assertNotNull(result.sprints)
            assertEquals(if (catalog) 0 else 1, result.sprints.size)
            assertEquals(if (catalog) null else "project-key:777", result.selectedSprintKey)
            assertEquals(if (catalog) 0 else 4, result.completedQueries)
            assertEquals(if (catalog) 1 else 5, calls.size)
        }
    }

    @Test
    fun `session raw entry counts precede cross page deduplication for catalog and items`() = runBlocking {
        for (catalog in listOf(true, false)) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val entries = ((1..50).toList() + (50..99).toList() + listOf(99, 100)).map {
                if (catalog) sprintRow(it.toString(), "Future $it", "未开始") else row(it.toString(), "Item $it")
            }
            val result = load(runner { command ->
                calls += command
                if ("--session-id" in command) {
                    val number = assertSessionPage(command, "dedup-session")
                    ok(mqlPage("dedup-session", entries.size, entries.drop((number - 1) * 50).take(50)))
                } else when (command.valueAfter("--mql")!!.queriedType()) {
                    "Sprint" -> if (catalog) ok(mqlPage("dedup-session", entries.size, entries.take(50))) else ok(sprintResponse("777"))
                    "User Story" -> if (catalog) ok(rows()) else ok(mqlPage("dedup-session", entries.size, entries.take(50)))
                    else -> ok(rows())
                }
            }, sprintKey = if (catalog) "project-key:100" else "project-key:777")
            val ids = if (catalog) result.sprints!!.map { it.id } else result.items.map { it.id }
            assertEquals((1..100).map { it.toString() }, ids)
            assertEquals(4, result.completedQueries)
            assertTrue(result.failures.isEmpty())
            assertEquals(2, calls.count { "--session-id" in it })
            result.items.forEach { assertDays("0", it.mySprintEstimateDays) }
        }
    }

    @Test
    fun `first session response rejects missing or invalid metadata counts and row coverage without guessing`() = runBlocking {
        val entries = (1..50).map { sprintRow(it.toString()) }
        val first = Json.parseToJsonElement(mqlPage("session", 51, entries)).jsonObject
        val malformed = listOf(
            JsonObject(first - "session_id").toString(),
            JsonObject(first - "list").toString(),
            JsonObject(first + ("list" to JsonNull)).toString(),
            JsonObject(first + ("session_id" to JsonNull)).toString(),
            JsonObject(first + ("session_id" to JsonPrimitive(" "))).toString(),
            JsonObject(first + ("session_id" to JsonPrimitive(123))).toString(),
            """{"session_id":"session","list":[],"data":{"1":[]}}""",
            """{"session_id":"session","list":[{}],"data":{"1":[]}}""",
            """{"session_id":"session","list":[{"count":1}],"data":{"2":[${entries.first()}]}}""",
            mqlPage("session", 51, entries.dropLast(1)),
            mqlPage("session", 49, entries),
            mqlPage("session", 0, entries),
            mqlPage("session", 1, listOf("{}")),
            mqlPage("session", 1, listOf("null")),
            mqlPage("session", 1, listOf(sprintRow("invalid"))),
        ) + listOf("null", "-1", "1.5", "\"51\"", "true", "9223372036854775808").map { count ->
            """{"session_id":"session","list":[{"count":$count}],"data":{"1":[]}}"""
        }
        for (payload in malformed) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val result = load(rawRunner { command -> calls += command; ok(payload) })
            assertNull(result.sprints, payload)
            assertNull(result.selectedSprintKey, payload)
            assertTrue(result.items.isEmpty(), payload)
            assertEquals(0, result.completedQueries, payload)
            assertEquals(1, result.failures.size, payload)
            assertEquals(1, calls.size, payload)
        }
    }

    @Test
    fun `invalid second or final session pages discard catalogs and affected types without partial estimates or retries`() = runBlocking {
        for (catalog in listOf(true, false)) {
            val entries = (1..103).map {
                if (catalog) sprintRow(it.toString(), "Sprint $it", if (it == 1) "进行中" else "未开始")
                else row(it.toString(), "Item $it")
            }
            for (failedPage in listOf(2, 3)) {
                val expected = entries.drop((failedPage - 1) * 50).take(50)
                val failures = listOf(
                    CommandResult(1, "", "page unavailable"),
                    CommandResult(1, """{"code":3003,"msg":"page unavailable"}""", ""),
                    ok("not-json"),
                    ok("{}"),
                    ok("""{"data":{"2":[${expected.joinToString(",")}]}}"""),
                    ok(mqlPage("session", 103, emptyList())),
                    ok(mqlPage("session", 103, entries.take(expected.size).reversed())),
                    ok(mqlPage("session", 103, expected.dropLast(1))),
                    ok(mqlPage("session", 103, expected + entries.first())),
                    ok(mqlPage("changed-session", 103, expected)),
                    ok(mqlPage("session", 104, expected)),
                    ok(mqlPage("session", 103, expected.dropLast(1) + "null")),
                    ok(mqlPage("session", 103, expected.dropLast(1) + "{}")),
                )
                for (failure in failures) {
                    val calls = CopyOnWriteArrayList<List<String>>()
                    val result = load(runner(intercept = { command ->
                        calls += command
                        if (command.isAction("workflow", "get-node")) ok(page(node().toString())) else null
                    }) { command ->
                        if ("--session-id" in command) {
                            val number = assertSessionPage(command, "session")
                            check(number <= failedPage) { "Must stop at failed page" }
                            if (number == failedPage) failure
                            else ok(mqlPage("session", 103, entries.drop((number - 1) * 50).take(50)))
                        } else when (command.valueAfter("--mql")!!.queriedType()) {
                            "Sprint" -> if (catalog) ok(mqlPage("session", 103, entries.take(50))) else ok(sprintResponse("777"))
                            "User Story" -> if (catalog) error("Incomplete catalog must not query items")
                                else ok(mqlPage("session", 103, entries.take(50)))
                            "Bug" -> ok(response("999", "Unaffected bug"))
                            else -> ok(rows())
                        }
                    }, sprintKey = if (catalog && failedPage == 3) "project-key:1" else null)
                    assertEquals(1, result.failures.size, failure.toString())
                    assertEquals((2..failedPage).toList(), calls.filter { "--session-id" in it }.map { assertSessionPage(it, "session") })
                    val type = if (catalog) "Sprint" else "User Story"
                    assertEquals(1, calls.count { it.valueAfter("--mql")?.queriedType() == type })
                    if (catalog) {
                        assertNull(result.sprints)
                        assertNull(result.selectedSprintKey)
                        assertTrue(result.items.isEmpty())
                        assertEquals(0, result.completedQueries)
                        assertTrue(calls.all { it.isAction("workitem", "query") })
                    } else {
                        assertEquals("project-key:777", result.selectedSprintKey)
                        assertEquals(3, result.completedQueries)
                        assertEquals("bug", result.items.single().type)
                        assertDays("2", result.items.single().mySprintEstimateDays)
                        assertEquals(listOf("999"), calls.filter { it.isAction("workflow", "get-node") }.map { it.valueAfter("--work-item-id") })
                    }
                }
            }
        }
    }

    @Test
    fun `role fallback paginates its own base session once and never retries base or later page failures`() = runBlocking {
        for (outcome in listOf("complete", "base failure", "later failure")) {
            val calls = CopyOnWriteArrayList<List<String>>()
            val entries = (1..51).map { row(it.toString(), "Item $it") }
            val roleFailure = CommandResult(1, """{"code":3003,"msg":"Unknown role field"}""", "")
            val result = load(runner(intercept = { command ->
                calls += command
                if (command.isAction("workflow", "get-node")) ok(page(node().toString())) else null
            }) { command ->
                if ("--session-id" in command) {
                    assertEquals(2, assertSessionPage(command, "base-session"))
                    if (outcome == "later failure") roleFailure else ok(mqlPage("base-session", 51, entries.drop(50)))
                } else {
                    val mql = command.valueAfter("--mql")!!
                    when {
                        mql.queriedType() == "Sprint" -> ok(sprintResponse("777"))
                        mql.queriedType() != "User Story" -> ok(rows())
                        "__Dev Owner" in mql.substringBefore(" FROM ") -> roleFailure
                        outcome == "base failure" -> roleFailure
                        else -> ok(mqlPage("base-session", 51, entries.take(50)))
                    }
                }
            })
            val queries = calls.mapNotNull { it.valueAfter("--mql") }.filter { it.queriedType() == "User Story" }
            assertEquals(2, queries.size, outcome)
            assertEquals("SELECT `work_item_id`, `name`, `work_item_status`", queries.last().substringBefore(" FROM "))
            assertEquals(queries.first().substringAfter(" FROM "), queries.last().substringAfter(" FROM "))
            queries.forEach { assertParticipationWhere(it) }
            assertEquals(1, calls.count { it.isAction("workitem", "meta-roles") && it.valueAfter("--work-item-type") == "User Story" })
            assertEquals(if (outcome == "base failure") 0 else 1, calls.count { "--session-id" in it })
            if (outcome == "complete") {
                assertEquals(4, result.completedQueries)
                assertEquals(51, result.items.size)
                assertEquals("51", result.items.last().id)
                assertTrue(result.failures.isEmpty())
                result.items.forEach {
                    assertDays("2", it.mySprintEstimateDays)
                    assertTrue(it.metadataWarnings.single().contains("人员字段不可用"))
                }
            } else {
                assertEquals(3, result.completedQueries)
                assertTrue(result.items.isEmpty())
                assertEquals(1, result.failures.size)
                assertTrue(calls.none { it.isAction("workflow", "get-node") })
            }
        }
    }

    @Test
    fun `last session page cancellation propagates from catalog and work item queries`() {
        for (catalog in listOf(true, false)) {
            val pages = CopyOnWriteArrayList<Int>()
            val entries = (1..101).map { if (catalog) sprintRow(it.toString()) else row(it.toString(), "Item $it") }
            assertFailsWith<CancellationException> {
                runBlocking {
                    load(runner { command ->
                        if ("--session-id" in command) {
                            val number = assertSessionPage(command, "cancel-session")
                            pages += number
                            if (number == 3) throw CancellationException("cancel last page")
                            ok(mqlPage("cancel-session", 101, entries.drop(50).take(50)))
                        } else when (command.valueAfter("--mql")!!.queriedType()) {
                            "Sprint" -> if (catalog) ok(mqlPage("cancel-session", 101, entries.take(50))) else ok(sprintResponse("777"))
                            "User Story" -> ok(mqlPage("cancel-session", 101, entries.take(50)))
                            else -> ok(rows())
                        }
                    })
                }
            }
            assertEquals(listOf(2, 3), pages.toList())
        }
    }

    @Test
    fun `cancelling blocked later session pages interrupts catalog and item commands without publishing`() = runBlocking {
        for (catalog in listOf(true, false)) {
            val started = CompletableDeferred<Unit>()
            val interrupted = AtomicInteger()
            val published = AtomicInteger()
            val calls = CopyOnWriteArrayList<List<String>>()
            val entries = (1..50).map { if (catalog) sprintRow(it.toString()) else row(it.toString(), "Item $it") }
            val job = launch {
                load(runner(intercept = { calls += it; null }) { command ->
                    if ("--session-id" in command) {
                        assertEquals(2, assertSessionPage(command, "blocked-session"))
                        started.complete(Unit)
                        try {
                            CountDownLatch(1).await()
                            error("Blocked page should be interrupted")
                        } catch (error: InterruptedException) {
                            interrupted.incrementAndGet()
                            throw error
                        }
                    } else when (command.valueAfter("--mql")!!.queriedType()) {
                        "Sprint" -> if (catalog) ok(mqlPage("blocked-session", 51, entries)) else ok(sprintResponse("777"))
                        "User Story" -> ok(mqlPage("blocked-session", 51, entries))
                        else -> ok(rows())
                    }
                })
                published.incrementAndGet()
            }
            try {
                withTimeout(5_000) { started.await() }
                withTimeout(5_000) { job.cancelAndJoin() }
                assertTrue(job.isCancelled)
                assertEquals(1, interrupted.get())
                assertEquals(0, published.get())
                assertEquals(1, calls.count { "--session-id" in it })
                assertTrue(calls.none { it.isAction("workflow", "get-node") })
            } finally {
                job.cancelAndJoin()
            }
        }
    }

    @Test
    fun `all session followups share the four request concurrency limit`() = runBlocking {
        val active = AtomicInteger()
        val maximum = AtomicInteger()
        val followups = CountDownLatch(4)
        val result = load(rawRunner { command ->
            val current = active.incrementAndGet()
            maximum.updateAndGet { maxOf(it, current) }
            try {
                defaultSupplementary(command) ?: if ("--session-id" in command) {
                    val session = command.valueAfter("--session-id")!!
                    assertEquals(2, assertSessionPage(command, session))
                    followups.countDown()
                    assertTrue(followups.await(5, java.util.concurrent.TimeUnit.SECONDS), "All four type pages should run concurrently")
                    ok(mqlPage(session, 51, listOf(row("51", "Last item"))))
                } else {
                    val type = command.valueAfter("--mql")!!.queriedType()
                    if (type == "Sprint") ok(sprintResponse("777"))
                    else ok(mqlPage(type, 51, (1..50).map { row(it.toString(), "Item $it") }))
                }
            } finally {
                active.decrementAndGet()
            }
        })
        assertEquals(4, maximum.get())
        assertEquals(0, active.get())
        assertEquals(204, result.items.size)
        assertEquals(4, result.completedQueries)
        assertTrue(result.failures.isEmpty())
    }

    private fun mqlPage(session: String, count: Int, entries: List<String>): String =
        """{"session_id":${JsonPrimitive(session)},"list":[{"group_id":"1","count":$count}],"data":{"1":[${entries.joinToString(",")}]}}"""

    private fun assertSessionPage(command: List<String>, session: String): Int {
        assertEquals("project-key", command.valueAfter("--project-key"))
        assertEquals(session, command.valueAfter("--session-id"))
        assertEquals("json", command.valueAfter("--format"))
        assertTrue(command.isAction("workitem", "query"))
        assertFalse("--mql" in command)
        assertFalse("--auto-paginate" in command)
        assertFalse("--page-num" in command)
        val params = Json.parseToJsonElement(command.valueAfter("--params")!!).jsonObject
        val groups = params.getValue("group_pagination_list") as JsonArray
        val number = (groups.single().jsonObject.getValue("page_num") as JsonPrimitive)
        assertFalse(number.isString)
        val page = number.content.toInt()
        assertTrue(page >= 2)
        assertEquals(Json.parseToJsonElement("""{"group_pagination_list":[{"group_id":"1","page_num":$page}]}"""), params)
        return page
    }

    private fun authenticated() = MeegleCliStatus(installed = true, authenticated = true)

    // Existing tests see only MQL commands; specialized tests can intercept any command first.
    private fun runner(
        intercept: (List<String>) -> CommandResult? = { null },
        block: (List<String>) -> CommandResult,
    ): CommandRunner = rawRunner { command ->
        intercept(command) ?: defaultSupplementary(command) ?: block(command)
    }

    private fun rawRunner(block: (List<String>) -> CommandResult): CommandRunner = object : CommandRunner {
        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult = block(command)
    }

    private fun defaultSupplementary(command: List<String>): CommandResult? = when {
        command.isAction("user", "me") -> ok("""{"user_key":"me"}""")
        command.isAction("workitem", "meta-roles") -> ok(page(*defaultRoleNames.mapIndexed { index, name ->
            roleMetadata("role_fixture_$index", name)
        }.toTypedArray()))
        command.isAction("workitem", "meta-fields") -> ok(page(durationField()))
        command.isAction("workitem", "get") -> ok(sprintDates())
        command.isAction("workflow", "get-node") -> ok(page())
        else -> null
    }

    private val defaultRoleNames = listOf("Dev Owner", "产品经理", "测试工程师", "自定义 People 角色")

    private fun roleMetadata(id: String, name: String) =
        """{"role_id":${JsonPrimitive(id)},"role_name":${JsonPrimitive(name)}}"""

    private fun assertParticipationWhere(mql: String, sprintId: String = "777", roles: List<String> = defaultRoleNames) {
        val predicates = roles.joinToString(" OR ") { "array_contains(`__$it`, current_login_user())" }
        assertEquals("array_contains(`Sprint`, '<id:$sprintId>') AND ($predicates)", mql.substringAfter(" WHERE "))
        assertFalse("all_participate_persons" in mql)
    }

    private suspend fun load(
        runner: CommandRunner,
        projects: List<MeegleProjectConfig> = listOf(MeegleProjectConfig("project-key", "obt")),
        sprintKey: String? = null,
        defaultSprintProjectKey: String? = null,
    ) = MeegleParticipatedWorkItemsSource(runner, isWindows = false, loginStatus = ::authenticated)
        .load(projects, sprintKey, defaultSprintProjectKey)

    private fun singleItemMql(command: List<String>): CommandResult = when (command.valueAfter("--mql")!!.queriedType()) {
        "Sprint" -> ok(sprintResponse("777"))
        "User Story" -> ok(response("101", "Example item"))
        else -> ok(rows())
    }

    private fun ok(json: String) = CommandResult(0, json, "")

    private fun page(vararg entries: String, number: Int = 1, more: Boolean = false) =
        """{"list":[${entries.joinToString(",")}],"pagination":{"has_more":$more,"page_num":$number}}"""

    private fun durationField(key: String = "duration_key") =
        """{"field_name":"Duration","field_type":"schedule","field_key":"$key"}"""

    private fun sprintDates(key: String = "duration_key", start: Long = 100, end: Long = 200) =
        """{"work_item_fields":[{"key":"$key","value":{"start_time":{"timestamp":$start},"end_time":{"timestamp":$end}}}]}"""

    private fun personalRow(
        user: String = "me",
        points: String = "2",
        start: String = "100",
        end: String = "200",
    ) = """{"assignee":{"user_key":"$user"},"schedule_info":{"points":$points,"estimate_start_time":$start,"estimate_finish_time":$end}}"""

    private fun node(
        key: String = "node-a",
        people: List<String> = listOf(personalRow()),
        name: String = "Any workflow step",
        status: String = "finished",
        subTasks: List<JsonObject> = emptyList(),
    ): JsonObject = Json.parseToJsonElement(
        """{"basic":{"node_key":"$key","name":"$name","status":"$status"},"schedule":{"points":999,"estimate_start_time":900,"estimate_finish_time":1000},"assignee_schedule_list":[${people.joinToString(",")}],"sub_tasks":[${subTasks.joinToString(",")}] }""",
    ).jsonObject

    private fun subTask(
        key: String = "child-a",
        owners: List<String> = listOf("me"),
        points: String = "2",
        start: String = "1970-01-01T00:00:00.100Z",
        end: String = "1970-01-01T00:00:00.200Z",
    ): JsonObject = JsonObject(mapOf(
        "sub_task_id" to JsonPrimitive(key),
        "owner" to JsonPrimitive(JsonArray(owners.map { JsonObject(mapOf("username" to JsonPrimitive(it))) }).toString()),
        "points" to Json.parseToJsonElement(points),
        "estimate_start_date" to JsonPrimitive(start),
        "estimate_end_date" to JsonPrimitive(end),
    ))

    private fun assertDays(expected: String, actual: BigDecimal?, message: String? = null) {
        assertNotNull(actual, message)
        assertEquals(0, BigDecimal(expected).compareTo(actual), message ?: "Expected $expected days, got $actual")
    }

    private fun rows(vararg rows: String) = """{"data":{"1":[${rows.joinToString(",")}]}}"""

    private fun roleField(name: String, users: String, key: String = "dynamic_${name.hashCode()}") =
        """{"key":"$key","name":"$name","value":$users}"""

    private fun response(id: String, title: String) = rows(row(id, title))

    private fun row(id: String, title: String, vararg extraFields: String) =
        """{"moql_field_list":[{"key":"work_item_id","value":{"long_value":$id}},{"key":"name","value":{"string_value":"$title"}}${extraFields.joinToString("") { ",$it" }}]}"""

    private fun sprintRow(id: String, title: String = "Sprint $id", status: String = "进行中") =
        """{"moql_field_list":[{"key":"work_item_id","name":"Item Id","value":{"long_value":"$id"}},{"key":"name","name":"Name","value":{"string_value":${JsonPrimitive(title)}}},{"key":"work_item_status","name":"Status","value":{"key_label_value_list":[{"key":"internal-status-id","label":${JsonPrimitive(status)}}]}}]}"""

    private fun sprintResponse(vararg ids: String) =
        """{"data":{"1":[${ids.joinToString(",") { sprintRow(it) }}]}}"""

    private fun String.queriedType(): String = substringAfter(".`").substringBefore('`')

    private fun List<String>.isAction(group: String, action: String): Boolean =
        windowed(2).any { it == listOf(group, action) }

    private fun List<String>.valueAfter(flag: String): String? =
        indexOf(flag).takeIf { it >= 0 }?.let { getOrNull(it + 1) }
}
