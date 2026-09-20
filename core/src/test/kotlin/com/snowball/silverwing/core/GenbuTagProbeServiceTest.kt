package com.snowball.silverwing.core

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class GenbuTagProbeServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `disabled service never queries Genbu`() {
        val fixture = fixture(enabled = false)
        fixture.store.save(fixture.taskDirectory, operation("1.0.0.1", "2026-08-25 10:00:00"))

        assertFalse(fixture.probes.probe(fixture.config, listOf(fixture.task)))
        assertEquals(emptyList(), fixture.calls)
    }

    @Test
    fun `built tag remains eligible until released`() {
        val fixture = fixture(resultByTag = mapOf("1.0.0.1" to buildingResult()))
        fixture.store.save(fixture.taskDirectory, operation("1.0.0.1", "2026-08-25 10:00:00"))

        assertTrue(fixture.probes.probe(fixture.config, listOf(fixture.task)))
        fixture.probes.probe(fixture.config, listOf(fixture.task))

        assertEquals(listOf("1.0.0.1", "1.0.0.1"), fixture.calls)
        val status = fixture.store.list(fixture.taskDirectory).single().genbuStatus
        assertEquals(GenbuStageStatus.SUCCESS, status.build)
        assertEquals(GenbuStageStatus.INITIAL, status.uat)
        assertFalse(status.stoppedByNewerRelease)
    }

    @Test
    fun `failed build tag stops automatic polling until re-tagged`() {
        val fixture = fixture(
            resultByTag = mapOf(
                "1.0.0.1" to GenbuTagQueryResult(
                    build = GenbuStageStatus.FAILED,
                    uat = GenbuStageStatus.INITIAL,
                    production = GenbuStageStatus.INITIAL,
                ),
            ),
        )
        fixture.store.save(fixture.taskDirectory, operation("1.0.0.1", "2026-08-25 10:00:00"))

        assertTrue(fixture.probes.probe(fixture.config, listOf(fixture.task)))
        assertFalse(fixture.probes.probe(fixture.config, listOf(fixture.task)))

        assertEquals(listOf("1.0.0.1"), fixture.calls)
        val status = fixture.store.list(fixture.taskDirectory).single().genbuStatus
        assertEquals(GenbuStageStatus.FAILED, status.build)
    }

    @Test
    fun `every tagged operation is probed even when its Git operation failed`() {
        val fixture = fixture()
        fixture.store.save(
            fixture.taskDirectory,
            operation("1.0.0.1", "2026-08-25 10:00:00").copy(state = TagOperationState.FAILED),
        )

        assertTrue(fixture.probes.probe(fixture.config, listOf(fixture.task)))

        assertEquals(listOf("1.0.0.1"), fixture.calls)
    }

    @Test
    fun `newer released tag stops older tag polling`() {
        val fixture = fixture(resultByTag = mapOf("1.0.0.2" to releasedResult()))
        fixture.store.save(fixture.taskDirectory, operation("1.0.0.1", "2026-08-25 10:00:00"))
        fixture.store.save(fixture.taskDirectory, operation("1.0.0.2", "2026-08-25 11:00:00"))

        assertTrue(fixture.probes.probe(fixture.config, listOf(fixture.task)))

        assertEquals(listOf("1.0.0.2"), fixture.calls)
        val byTag = fixture.store.list(fixture.taskDirectory).associateBy { it.tag }
        assertEquals(GenbuStageStatus.SUCCESS, requireNotNull(byTag["1.0.0.2"]).genbuStatus.uat)
        assertTrue(requireNotNull(byTag["1.0.0.1"]).genbuStatus.stoppedByNewerRelease)
    }

    @Test
    fun `saved release status still stops older tags without querying again`() {
        val fixture = fixture()
        fixture.store.save(fixture.taskDirectory, operation("1.0.0.1", "2026-08-25 10:00:00"))
        fixture.store.save(
            fixture.taskDirectory,
            operation("1.0.0.2", "2026-08-25 11:00:00").copy(
                genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.SUCCESS, uat = GenbuStageStatus.SUCCESS),
            ),
        )

        assertTrue(fixture.probes.probe(fixture.config, listOf(fixture.task)))

        assertEquals(emptyList(), fixture.calls)
        val byTag = fixture.store.list(fixture.taskDirectory).associateBy { it.tag }
        assertTrue(requireNotNull(byTag["1.0.0.1"]).genbuStatus.stoppedByNewerRelease)
    }

    @Test
    fun `forced refresh queries every tagged record including released and superseded tags`() {
        val fixture = fixture()
        fixture.store.save(
            fixture.taskDirectory,
            operation("1.0.0.1", "2026-08-25 10:00:00").copy(
                genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.SUCCESS, uat = GenbuStageStatus.SUCCESS),
            ),
        )
        fixture.store.save(
            fixture.taskDirectory,
            operation("1.0.0.2", "2026-08-25 11:00:00").copy(genbuStatus = GenbuTagProbeStatus(notFound = true, stoppedByNewerRelease = true)),
        )

        fixture.probes.probe(fixture.config, listOf(fixture.task), force = true)

        assertEquals(listOf("1.0.0.2", "1.0.0.1"), fixture.calls)
    }

    @Test
    fun `legacy service config defaults the Genbu name to its display name`() {
        val taskRoot = temporary.resolve("legacy-tasks")
        val task = TaskManifest(
            folderName = "task",
            taskDirectoryName = "task",
            featureBranch = "feature/task",
            createdAt = "2026-08-25 09:00:00",
            updatedAt = "2026-08-25 09:00:00",
            services = emptyList<ServiceWorkspace>(),
            groupId = "group",
        )
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(RepositoryConfig("repo", "仓库", temporary.resolve("repo").toString(), temporary.resolve("repo/.git").toString(), "https://example.test/repo.git")),
            groups = listOf(GroupConfig("group", "测试", services = listOf(GroupServiceConfig("service-random", "repo", "operation-center", genbuProbeEnabled = true)))),
        )
        val services = mutableListOf<String>()
        val store = TagOperationStore()
        store.save(taskRoot.resolve("task"), operation("1.0.0.1", "2026-08-25 10:00:00").copy(groupServiceId = "service-random", folderName = "task"))
        val probes = GenbuTagProbeService(store, GenbuTagStatusProvider { service, _ ->
            services += service
            buildingResult()
        })

        probes.probe(config, listOf(task))

        assertEquals(listOf("operation-center"), services)
    }

    @Test
    fun `late response cannot overwrite a newer operation and deleted record stays deleted`() {
        val fixture = fixture()
        val old = operation("1.0.0.1", "2026-08-25 10:00:00")
        val newer = old.copy(
            tag = "1.0.0.2",
            updatedAt = "2026-08-25 10:00:01",
            message = "newer build",
        )
        fixture.store.save(fixture.taskDirectory, old)
        val directory = fixture.taskDirectory.resolve("tag-operations")
        val probes = GenbuTagProbeService(fixture.store, GenbuTagStatusProvider { _, _ ->
            fixture.store.save(fixture.taskDirectory, newer)
            GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
            )
        })

        probes.probeOperation(fixture.config, fixture.task, old.operationId)

        val after = fixture.store.load(fixture.taskDirectory, old.operationId)
        assertEquals(newer.tag, after.tag)
        assertEquals(newer.message, after.message)

        val deleteProbe = GenbuTagProbeService(fixture.store, GenbuTagStatusProvider { _, _ ->
            Files.deleteIfExists(directory.resolve("${old.operationId}.json"))
            GenbuTagQueryResult(
                build = GenbuStageStatus.SUCCESS,
                uat = GenbuStageStatus.INITIAL,
                production = GenbuStageStatus.INITIAL,
            )
        })
        assertEquals(null, deleteProbe.probeOperation(fixture.config, fixture.task, old.operationId))
        assertFalse(Files.exists(directory.resolve("${old.operationId}.json")))

        fixture.store.save(fixture.taskDirectory, newer)
        val removedTask = temporary.resolve("removed-task-backup")
        val removedTaskProbe = GenbuTagProbeService(fixture.store, GenbuTagStatusProvider { _, _ ->
            Files.move(fixture.taskDirectory, removedTask)
            buildingResult()
        })
        assertEquals(null, removedTaskProbe.probeOperation(fixture.config, fixture.task, old.operationId))
        assertFalse(Files.exists(fixture.taskDirectory), "a late probe must not recreate the removed task directory")
        assertTrue(Files.exists(removedTask.resolve("tag-operations/${old.operationId}.json")))
    }

    private fun fixture(
        enabled: Boolean = true,
        resultByTag: Map<String, GenbuTagQueryResult> = emptyMap(),
    ): Fixture {
        val taskRoot = temporary.resolve("tasks")
        val task = TaskManifest(
            folderName = "task-42",
            taskDirectoryName = "task-42",
            featureBranch = "feature/task-42",
            createdAt = "2026-08-25 09:00:00",
            updatedAt = "2026-08-25 09:00:00",
            services = emptyList(),
            groupId = "group",
        )
        val config = AppConfig(
            taskRoot = taskRoot.toString(),
            repositories = listOf(RepositoryConfig("repo", "仓库", temporary.resolve("repo").toString(), temporary.resolve("repo/.git").toString(), "https://example.test/repo.git")),
            groups = listOf(GroupConfig("group", "测试", services = listOf(
                GroupServiceConfig("service", "repo", "服务", genbuProbeEnabled = enabled, genbuServiceName = "uat-service"),
            ))),
        )
        val calls = mutableListOf<String>()
        val probes = GenbuTagProbeService(
            operations = TagOperationStore(),
            genbu = GenbuTagStatusProvider { _, tag ->
                calls += tag
                resultByTag[tag] ?: buildingResult()
            },
            clock = Clock.fixed(Instant.parse("2026-08-25T04:00:00Z"), ZoneOffset.UTC),
        )
        return Fixture(config, task, taskRoot.resolve(task.taskDirectoryName), TagOperationStore(), probes, calls)
    }

    private fun buildingResult() = GenbuTagQueryResult(
        build = GenbuStageStatus.SUCCESS,
        uat = GenbuStageStatus.INITIAL,
        production = GenbuStageStatus.INITIAL,
    )

    private fun releasedResult() = GenbuTagQueryResult(
        build = GenbuStageStatus.SUCCESS,
        uat = GenbuStageStatus.SUCCESS,
        production = GenbuStageStatus.INITIAL,
    )

    private fun operation(tag: String, createdAt: String) = TagOperation(
        operationId = "operation-$tag",
        folderName = "task-42",
        serviceName = "服务",
        repositoryId = "repo",
        sourceBranch = "feature/task-42",
        remote = "origin",
        state = TagOperationState.SUCCESS,
        createdAt = createdAt,
        updatedAt = createdAt,
        tag = tag,
        groupServiceId = "service",
    )

    private data class Fixture(
        val config: AppConfig,
        val task: TaskManifest,
        val taskDirectory: Path,
        val store: TagOperationStore,
        val probes: GenbuTagProbeService,
        val calls: MutableList<String>,
    )
}
