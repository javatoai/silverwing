package com.snowball.silverwing.core

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class TagOperationCliFacadeTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `retry kind covers conflict interrupted failed partial retag and terminal states`() {
        assertEquals(TagRetryKind.RESOLVE_CONFLICT, tagRetryKind(operation(TagOperationState.CONFLICT)))
        assertEquals(TagRetryKind.RETRY_INTERRUPTED, tagRetryKind(operation(TagOperationState.CREATED)))
        assertEquals(TagRetryKind.RETRY_INTERRUPTED, tagRetryKind(operation(TagOperationState.SOURCE_BRANCH_PUSHED)))
        assertEquals(TagRetryKind.RETRY_BUILD, tagRetryKind(operation(TagOperationState.FAILED)))
        assertEquals(TagRetryKind.RESUME_PARTIAL, tagRetryKind(operation(TagOperationState.PARTIAL)))
        assertEquals(
            TagRetryKind.RETAG,
            tagRetryKind(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
                ),
            ),
        )
        assertEquals(
            TagRetryKind.NONE,
            tagRetryKind(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(
                        build = GenbuStageStatus.FAILED,
                        failureReason = "live query unavailable",
                    ),
                ),
            ),
        )
        assertEquals(TagRetryKind.NONE, tagRetryKind(operation(TagOperationState.SUCCESS)))
        assertEquals(TagRetryKind.NONE, tagRetryKind(operation(TagOperationState.TAG_PUSHED)))
    }

    @Test
    fun `conflict guidance names both branches and the retry command`() {
        val conflict = operation(TagOperationState.CONFLICT).copy(
            sourceBranch = "feature/task-42",
            targetBranch = "release/test",
            remote = "origin",
        )

        val guidance = conflict.toReport("task-42").guidance.orEmpty()

        assertTrue(guidance.contains("我在将服务 服务 的 feature/task-42 分支合并到 origin/release/test 时遇到了冲突，请你解决。"))
        assertTrue(guidance.contains("silverwing tag retry --task task-42 --operation operation-42"))
    }

    @Test
    fun `genbu build failure guidance explains the re-Tag version bump`() {
        val failed = operation(TagOperationState.SUCCESS).copy(
            tag = "1.6.89.beta-9",
            genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
        )

        val report = failed.toReport("task-42")

        assertEquals(TagRetryKind.RETAG, report.retryKind)
        assertTrue(report.guidance.orEmpty().contains("1.6.89.beta-9"))
        assertTrue(report.guidance.orEmpty().contains("+1"))
    }

    @Test
    fun `healthy successful operation carries no guidance`() {
        val report = operation(TagOperationState.SUCCESS).toReport("task-42")

        assertEquals(TagRetryKind.NONE, report.retryKind)
        assertNull(report.guidance)
    }

    @Test
    fun `guidance addresses the task directory instead of the record display name`() {
        val fixture = fixture(probeEnabled = false)
        // A record keeps the manifest's display folderName, which may differ from
        // the directory name that `--task` resolves.
        val saved = operation(TagOperationState.CONFLICT).copy(folderName = "显示名称", tag = null)
        fixture.operations.save(fixture.taskDirectory, saved)

        val guidance = fixture.facade.history("task-42").single().guidance.orEmpty()

        assertTrue(guidance.contains("--task task-42"))
        assertFalse(guidance.contains("显示名称"))
    }

    @Test
    fun `status refreshes Genbu stages live and persists them`() {
        val fixture = fixture(
            probeEnabled = true,
            genbuResult = GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
                builtCompletedAt = "2026-09-07 10:00:00",
            ),
        )
        val saved = operation(TagOperationState.SUCCESS).copy(tag = "1.0.0.1")
        fixture.operations.save(fixture.taskDirectory, saved)

        val report = fixture.facade.status("task-42", saved.operationId)

        assertEquals(GenbuStageStatus.SUCCESS, report.operation.genbuStatus.build)
        assertEquals("2026-09-07 10:00:00", report.operation.genbuStatus.builtCompletedAt)
        val persisted = fixture.operations.load(fixture.taskDirectory, saved.operationId)
        assertEquals(GenbuStageStatus.SUCCESS, persisted.genbuStatus.build)
    }

    @Test
    fun `status keeps the stored record when the service probe is disabled`() {
        val fixture = fixture(probeEnabled = false)
        val saved = operation(TagOperationState.SUCCESS).copy(tag = "1.0.0.1")
        fixture.operations.save(fixture.taskDirectory, saved)

        val report = fixture.facade.status("task-42", saved.operationId)

        assertEquals(GenbuTagProbeStatus(), report.operation.genbuStatus)
        assertEquals(emptyList(), fixture.genbuCalls)
    }

    @Test
    fun `status reports an unknown operation id`() {
        val fixture = fixture(probeEnabled = true)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.status("task-42", "missing")
        }

        assertTrue(error.message.orEmpty().contains("找不到Tag构建记录"))
    }

    @Test
    fun `history returns the newest records first`() {
        val fixture = fixture(probeEnabled = false)
        fixture.operations.save(fixture.taskDirectory, operation(TagOperationState.SUCCESS).copy(
            operationId = "older",
            tag = "1.0.0.1",
            updatedAt = "2026-09-07 09:00:00",
        ))
        fixture.operations.save(fixture.taskDirectory, operation(TagOperationState.SUCCESS).copy(
            operationId = "newer",
            tag = "1.0.0.2",
            updatedAt = "2026-09-07 10:00:00",
        ))

        val history = fixture.facade.history("task-42")

        assertEquals(listOf("newer", "older"), history.map { it.operation.operationId })
    }

    @Test
    fun `retry rejects an operation that has nothing to retry`() {
        val fixture = fixture(probeEnabled = false)
        val saved = operation(TagOperationState.SUCCESS).copy(tag = "1.0.0.1")
        fixture.operations.save(fixture.taskDirectory, saved)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.retry("task-42", saved.operationId)
        }

        assertTrue(error.message.orEmpty().contains("无需重试"))
    }

    @Test
    fun `retry refreshes Genbu before deciding so a stale build failure never re-Tags`() {
        val fixture = fixture(
            probeEnabled = true,
            genbuResult = GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
            ),
        )
        val stale = operation(TagOperationState.SUCCESS).copy(
            tag = "1.0.0.1",
            genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
        )
        fixture.operations.save(fixture.taskDirectory, stale)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.retry("task-42", stale.operationId)
        }

        assertTrue(error.message.orEmpty().contains("无需重试"))
        val persisted = fixture.operations.load(fixture.taskDirectory, stale.operationId)
        assertEquals(GenbuStageStatus.SUCCESS, persisted.genbuStatus.build)
    }

    @Test
    fun `retry blocks retag when the live Genbu query fails`() {
        val fixture = fixture(probeEnabled = true, genbuFailure = "live query unavailable")
        val stale = operation(TagOperationState.SUCCESS).copy(
            tag = "1.0.0.1",
            genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
        )
        fixture.operations.save(fixture.taskDirectory, stale)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.retry("task-42", stale.operationId)
        }

        assertTrue(error.message.orEmpty().contains("实时状态查询失败"))
        val persisted = fixture.operations.load(fixture.taskDirectory, stale.operationId)
        assertEquals(TagOperationState.SUCCESS, persisted.state)
        assertEquals(stale.tag, persisted.tag)
        assertEquals("live query unavailable", persisted.genbuStatus.failureReason)
    }

    @Test
    fun `build rejects service selection combined with all services`() {
        val fixture = fixture(probeEnabled = false)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.build("task-42", listOf("service:default"), allServices = true)
        }

        assertTrue(error.message.orEmpty().contains("不能同时使用"))
    }

    @Test
    fun `build requires a service selection`() {
        val fixture = fixture(probeEnabled = false)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.build("task-42", emptyList(), allServices = false)
        }

        assertTrue(error.message.orEmpty().contains("请指定 --service"))
    }

    @Test
    fun `build rejects an explicit service whose module Tag switch is off`() {
        val fixture = fixture(probeEnabled = false, services = listOf(workspace(tagEnabled = false)))

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.build("task-42", listOf("service:default"), allServices = false)
        }

        assertTrue(error.message.orEmpty().contains("已关闭测试Tag"))
        assertEquals(emptyList(), fixture.facade.history("task-42").map { it.operation.operationId })
    }

    @Test
    fun `build rejects an explicit service when the group Tag switch is off`() {
        val fixture = fixture(
            probeEnabled = false,
            groupTagEnabled = false,
            services = listOf(workspace(tagEnabled = true)),
        )

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.build("task-42", listOf("service:default"), allServices = false)
        }

        assertTrue(error.message.orEmpty().contains("组 测试 已关闭测试Tag"))
    }

    @Test
    fun `build rejects every Tag operation when the global switch is off`() {
        val fixture = fixture(probeEnabled = false, globalTagEnabled = false)

        val error = assertFailsWith<IllegalStateException> {
            fixture.facade.build("task-42", listOf("service:default"), allServices = false)
        }

        assertTrue(error.message.orEmpty().contains("全局测试Tag已关闭"))
    }

    @Test
    fun `build collapses repeated and aliased service keys into one operation`() {
        val fixture = fixture(probeEnabled = false, services = listOf(workspace(tagEnabled = true)))

        // The same module addressed three ways: twice by selection key and once by
        // repository id. Building it more than once would push a second Tag.
        val reports = fixture.facade.build(
            "task-42",
            listOf("service:default", "service:default", "repo"),
            allServices = false,
        )

        assertEquals(1, reports.size)
        assertEquals(1, fixture.facade.history("task-42").size)
    }

    @Test
    fun `commands reject a task directory renamed away from its manifest`() {
        val fixture = fixture(probeEnabled = false)
        val renamed = fixture.taskDirectory.resolveSibling("task-42-renamed")
        Files.move(fixture.taskDirectory, renamed)

        val error = assertFailsWith<IllegalArgumentException> {
            fixture.facade.history("task-42-renamed")
        }

        assertTrue(error.message.orEmpty().contains("与任务清单名称不一致"))
    }

    private fun operation(state: TagOperationState) = TagOperation(
        operationId = "operation-42",
        folderName = "task-42",
        serviceName = "服务",
        repositoryId = "repo",
        sourceBranch = "feature/task-42",
        targetBranch = "release/test",
        remote = "origin",
        state = state,
        createdAt = "2026-09-07 09:00:00",
        updatedAt = "2026-09-07 09:00:00",
        groupServiceId = "service",
    )

    private fun workspace(tagEnabled: Boolean) = ServiceWorkspace(
        repositoryId = "repo",
        serviceName = "服务",
        repositoryPath = temporary.resolve("repo").toString(),
        worktreePath = temporary.resolve("worktree").toString(),
        developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
        branch = "feature/task-42",
        groupServiceId = "service",
        tagEnabled = tagEnabled,
    )

    private fun fixture(
        probeEnabled: Boolean,
        genbuResult: GenbuTagQueryResult = GenbuTagQueryResult(
            build = GenbuStageStatus.BUILDING,
            uat = GenbuStageStatus.INITIAL,
            production = GenbuStageStatus.INITIAL,
        ),
        genbuFailure: String? = null,
        globalTagEnabled: Boolean = true,
        groupTagEnabled: Boolean = true,
        services: List<ServiceWorkspace> = emptyList(),
    ): Fixture {
        val taskRoot = temporary.resolve("tasks")
        val taskDirectory = taskRoot.resolve("task-42")
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(
                RepositoryConfig(
                    "repo",
                    "仓库",
                    temporary.resolve("repo").toString(),
                    temporary.resolve("repo/.git").toString(),
                    "https://example.test/repo.git",
                ),
            ),
            tagEnabled = globalTagEnabled,
            groups = listOf(GroupConfig("group", "测试", tagEnabled = groupTagEnabled, services = listOf(
                GroupServiceConfig(
                    "service",
                    "repo",
                    "服务",
                    genbuProbeEnabled = probeEnabled,
                    genbuServiceName = "svc",
                ),
            ))),
        )
        val manifest = TaskManifest(
            folderName = "task-42",
            taskDirectoryName = "task-42",
            featureBranch = "feature/task-42",
            createdAt = "2026-09-07 09:00:00",
            updatedAt = "2026-09-07 09:00:00",
            services = services,
            groupId = "group",
        )
        ManifestStore().save(taskDirectory, manifest)
        val genbuCalls = mutableListOf<String>()
        val operations = TagOperationStore()
        val facade = TagOperationCliFacade(
            configurations = object : ConfigurationRepository {
                override fun load(): AppConfig = config
                override fun save(config: AppConfig) = Unit
            },
            builds = TagBuildService(paths = ApplicationPaths(temporary.resolve("app-home"))),
            operations = operations,
            probes = GenbuTagProbeService(
                operations = operations,
                genbu = GenbuTagStatusProvider { _, tag ->
                    genbuCalls += tag
                    genbuFailure?.let { error(it) }
                    genbuResult
                },
                clock = Clock.fixed(Instant.parse("2026-09-07T02:00:00Z"), ZoneOffset.UTC),
            ),
        )
        return Fixture(facade, operations, taskDirectory, genbuCalls)
    }

    private data class Fixture(
        val facade: TagOperationCliFacade,
        val operations: TagOperationStore,
        val taskDirectory: Path,
        val genbuCalls: MutableList<String>,
    )
}
