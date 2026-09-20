package com.snowball.silverwing.core

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import org.junit.jupiter.api.io.TempDir

class RequirementDocumentationServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `creates deterministic iteration and requirement documents from the unique active Sprint`() {
        val root = Files.createDirectories(temporary.resolve("docs"))
        val service = RequirementDocumentationService(iterations = FixedIterations(singleActive))
        val config = materialsConfig(root)

        val plan = service.plan(config, link, "登录优化", directoryFolderName = "任务目录")
        val materialized = service.materialize(config, plan)

        assertEquals("OBT-20260817--20260828", plan.sprint.label)
        assertEquals(false, plan.reusedHistoricalDirectory)
        assertTrue(Files.isRegularFile(Path.of(plan.documentationDirectory).resolve(".silverwing-requirement.json")))
        assertTrue(Files.isRegularFile(root.resolve(".silverwing-requirement-index.jsonl")))
        assertTrue(Files.readString(Path.of(plan.iterationDirectory).resolve("00-迭代任务总览.md")).contains("7064764629-登录优化"))
        assertEquals(plan.documentationDirectory, materialized.agentContext.documentationDirectory)
    }

    @Test
    fun `reuses exactly one historical manifest without querying Meegle again`() {
        val root = Files.createDirectories(temporary.resolve("docs-history"))
        val config = materialsConfig(root)
        val initial = RequirementDocumentationService(iterations = FixedIterations(singleActive))
        val created = initial.plan(config, link, "登录优化", directoryFolderName = "任务目录")
        initial.materialize(config, created)
        val offline = RequirementDocumentationService(iterations = RequirementIterationProvider { _, _ ->
            error("historical reuse must not call Meegle")
        })

        val reused = offline.plan(config, link, "不同的标题不会改变历史目录", directoryFolderName = "其他任务目录")

        assertEquals(true, reused.reusedHistoricalDirectory)
        assertEquals(created.documentationDirectory, reused.documentationDirectory)
        assertEquals("登录优化", reused.requirementTitle)
    }

    @Test
    fun `adds Agent process documents to an existing desktop materials write root`() {
        val root = Files.createDirectories(temporary.resolve("shared-materials"))
        val existingWriteRoot = Files.createDirectories(
            root.resolve("OBT-20260817--20260828").resolve("7064764629-桌面任务").resolve("研发"),
        )
        val config = materialsConfig(root)
        val service = RequirementDocumentationService(iterations = FixedIterations(singleActive))

        val plan = service.plan(config, link, "Agent 标题", directoryFolderName = "另一个任务名")
        val materialized = service.materialize(config, plan)

        assertEquals(existingWriteRoot.toString(), plan.documentationDirectory)
        assertEquals(existingWriteRoot.toString(), materialized.agentContext.documentationDirectory)
        assertTrue(Files.isRegularFile(existingWriteRoot.resolve(".silverwing-requirement.json")))
        assertTrue(Files.isRegularFile(existingWriteRoot.resolve("00-需求总览.md")))
    }

    @Test
    fun `preserves a user overview when adding Agent documents to a desktop materials directory`() {
        val root = Files.createDirectories(temporary.resolve("shared-user-overview"))
        val existingWriteRoot = Files.createDirectories(
            root.resolve("OBT-20260817--20260828").resolve("7064764629-桌面任务").resolve("研发"),
        )
        val overview = existingWriteRoot.resolve("00-需求总览.md")
        val userContent = "# 用户需求资料\r\n\r\n保留已有结论与 SQL 说明。\r\n"
        Files.writeString(overview, userContent)
        val config = materialsConfig(root)
        val service = RequirementDocumentationService(iterations = FixedIterations(singleActive))

        val plan = service.plan(config, link, "Agent 标题", directoryFolderName = "另一个任务名")
        service.materialize(config, plan)

        assertEquals(userContent, Files.readString(overview))
        assertTrue(Files.isRegularFile(existingWriteRoot.resolve(".silverwing-requirement.json")))
        assertTrue(Files.readString(root.resolve(".silverwing-requirement-index.jsonl")).contains("7064764629"))
    }

    @Test
    fun `enriches a desktop Sprint marker with a real ID and preserves known identity`() {
        val root = Files.createDirectories(temporary.resolve("shared-sprint-identity"))
        val iterationDirectory = root.resolve("OBT-20260817--20260828")
        Files.createDirectories(iterationDirectory.resolve("7064764629-桌面任务").resolve("研发"))
        val config = materialsConfig(root)
        val service = RequirementDocumentationService(iterations = FixedIterations(singleActive))
        val files = RequirementDocumentationFileStore()
        val iterationManifest = iterationDirectory.resolve(".silverwing-iteration.json")
        service.materialize(config, service.plan(config, link, "桌面任务"))
        val original = files.readIteration(iterationManifest)
        assertEquals("", original.sprint.id)

        val secondPlan = service.plan(
            config, link.replace("7064764629", "7064764630"), "新需求", directoryFolderName = "新任务",
        )
        val second = service.materialize(config, secondPlan)

        assertEquals("7070412889", files.readIteration(iterationManifest).sprint.id)
        assertEquals(original.createdAt, files.readIteration(iterationManifest).createdAt)
        assertEquals(secondPlan.sprint, second.plan.sprint)
        assertEquals(
            secondPlan.sprint,
            files.readRequirement(Path.of(secondPlan.documentationDirectory).resolve(".silverwing-requirement.json")).sprint,
        )

        val unknownIdPlan = service.plan(
            config, link.replace("7064764629", "7064764631"), "后续需求", directoryFolderName = "后续任务",
        ).let { it.copy(sprint = it.sprint.copy(id = "")) }
        val later = service.materialize(config, unknownIdPlan)
        assertEquals(secondPlan.sprint, files.readIteration(iterationManifest).sprint)
        assertEquals(secondPlan.sprint, later.plan.sprint)

        val conflictingPlan = service.plan(
            config, link.replace("7064764629", "7064764632"), "冲突需求", directoryFolderName = "冲突任务",
        ).let { it.copy(sprint = it.sprint.copy(id = "9999999999")) }
        val error = assertFailsWith<IllegalArgumentException> { service.materialize(config, conflictingPlan) }
        assertTrue(error.message.orEmpty().contains("Sprint 不匹配"))
        assertEquals(secondPlan.sprint, files.readIteration(iterationManifest).sprint)
        assertTrue(!Files.exists(Path.of(conflictingPlan.documentationDirectory)))
    }

    @Test
    fun `keeps a legacy requirement-root manifest as an identity marker and writes Agent docs below subdirectory`() {
        val root = Files.createDirectories(temporary.resolve("legacy-root-manifest"))
        val requirementDirectory = Files.createDirectories(root.resolve("已结束 Sprint").resolve("7064764629-桌面任务"))
        Files.writeString(
            requirementDirectory.resolve(".silverwing-requirement.json"),
            Json.encodeToString(
                RequirementDocumentationManifest(
                    identity = RequirementIdentity("obt", "userstory", "7064764629"),
                    requirementTitle = "桌面任务",
                    sprint = RequirementSprintSnapshot("", "已结束 Sprint"),
                    directoryName = "7064764629-桌面任务",
                    createdAt = "2026-08-23T00:00:00Z",
                    updatedAt = "2026-08-23T00:00:00Z",
                ),
            ),
        )
        val service = RequirementDocumentationService(
            iterations = RequirementIterationProvider { _, _ -> error("reuse must not query Sprint") },
            metadata = RequirementMetadataProvider { error("reuse must not query metadata") },
        )

        val plan = service.plan(materialsConfig(root), link, requestedTitle = "Agent 标题", directoryFolderName = "其他任务")
        val materialized = service.materialize(materialsConfig(root), plan)

        assertEquals(requirementDirectory.resolve("研发").toString(), plan.documentationDirectory)
        assertEquals(plan.documentationDirectory, materialized.agentContext.documentationDirectory)
        assertTrue(Files.isRegularFile(requirementDirectory.resolve(".silverwing-requirement.json")))
        assertTrue(Files.isRegularFile(requirementDirectory.resolve("研发/.silverwing-requirement.json")))
    }

    @Test
    fun `blocks a legacy requirement-root manifest with a different identity`() {
        val root = Files.createDirectories(temporary.resolve("legacy-root-mismatch"))
        val requirementDirectory = Files.createDirectories(root.resolve("已结束 Sprint").resolve("7064764629-桌面任务"))
        Files.writeString(
            requirementDirectory.resolve(".silverwing-requirement.json"),
            """{"identity":{"space":"obt","kind":"userstory","workItemId":"999"}}""",
        )

        val error = assertFailsWith<IllegalArgumentException> {
            RequirementDocumentationService(
                iterations = RequirementIterationProvider { _, _ -> error("mismatch must not query Sprint") },
            ).plan(materialsConfig(root), link, "Agent 标题", directoryFolderName = "任务目录")
        }

        assertTrue(error.message.orEmpty().contains("需求编号不一致"))
    }

    @Test
    fun `reuses a desktop materials directory without requiring Meegle or an active Sprint`() {
        val root = Files.createDirectories(temporary.resolve("shared-offline"))
        val existingWriteRoot = Files.createDirectories(
            root.resolve("已结束 Sprint").resolve("7064764629-桌面任务").resolve("研发"),
        )
        val service = RequirementDocumentationService(
            iterations = RequirementIterationProvider { _, _ -> error("reuse must not query Sprint") },
            metadata = RequirementMetadataProvider { error("reuse must not query metadata") },
        )

        val plan = service.plan(materialsConfig(root), link, requestedTitle = null, directoryFolderName = "Agent 任务")

        assertTrue(plan.reusedHistoricalDirectory)
        assertEquals(existingWriteRoot.toString(), plan.documentationDirectory)
        assertEquals("已结束 Sprint", plan.sprint.label)
    }

    @Test
    fun `materialize reuses a directory created after plan instead of creating a second one`() {
        val root = Files.createDirectories(temporary.resolve("plan-apply-race"))
        val config = materialsConfig(root)
        val service = RequirementDocumentationService(iterations = FixedIterations(singleActive))
        val plan = service.plan(config, link, "登录优化", directoryFolderName = "任务目录")
        val concurrentRequirementDirectory = Files.createDirectories(
            root.resolve("已结束 Sprint").resolve("7064764629-并发任务"),
        )

        val materialized = service.materialize(config, plan)

        assertEquals(
            concurrentRequirementDirectory.resolve("研发").toString(),
            materialized.agentContext.documentationDirectory,
        )
        assertTrue(Files.isRegularFile(concurrentRequirementDirectory.resolve("研发/.silverwing-requirement.json")))
        assertTrue(!Files.exists(Path.of(plan.documentationDirectory)))
    }

    @Test
    fun `blocks an ambiguous active Sprint selection`() {
        val root = Files.createDirectories(temporary.resolve("docs-ambiguous"))
        val service = RequirementDocumentationService(
            iterations = FixedIterations(
                listOf(
                    singleActive.single(),
                    RequirementSprint("2", "OBT-20260901--20260914", "进行中"),
                ),
            ),
        )

        val error = assertFailsWith<IllegalArgumentException> {
            service.plan(materialsConfig(root), link, "登录优化", directoryFolderName = "任务目录")
        }
        assertTrue(error.message.orEmpty().contains("多个进行中 Sprint"))
    }

    @Test
    fun `uses the only linked Sprint even when it is not in progress`() {
        val root = Files.createDirectories(temporary.resolve("docs-single-inactive"))
        val service = RequirementDocumentationService(
            iterations = FixedIterations(listOf(RequirementSprint("9", "OBT-20260901--20260914", "未开始"))),
        )

        val plan = service.plan(materialsConfig(root), link, "登录优化", directoryFolderName = "任务目录")

        assertEquals("OBT-20260901--20260914", plan.sprint.label)
        assertEquals(false, plan.reusedHistoricalDirectory)
    }

    @Test
    fun `picks the in-progress Sprint when several are linked`() {
        val root = Files.createDirectories(temporary.resolve("docs-multi-mixed"))
        val service = RequirementDocumentationService(
            iterations = FixedIterations(
                listOf(
                    RequirementSprint("9", "OBT-20260901--20260914", "未开始"),
                    singleActive.single(),
                ),
            ),
        )

        val plan = service.plan(materialsConfig(root), link, "登录优化", directoryFolderName = "任务目录")

        assertEquals("OBT-20260817--20260828", plan.sprint.label)
    }

    @Test
    fun `blocks several linked Sprints when none is in progress`() {
        val root = Files.createDirectories(temporary.resolve("docs-multi-inactive"))
        val service = RequirementDocumentationService(
            iterations = FixedIterations(
                listOf(
                    RequirementSprint("8", "OBT-20260817--20260828", "已结束"),
                    RequirementSprint("9", "OBT-20260901--20260914", "未开始"),
                ),
            ),
        )

        val error = assertFailsWith<IllegalArgumentException> {
            service.plan(materialsConfig(root), link, "登录优化", directoryFolderName = "任务目录")
        }
        assertTrue(error.message.orEmpty().contains("均不在进行中"))
    }

    @Test
    fun `does not let an index hide a second historical manifest`() {
        val root = Files.createDirectories(temporary.resolve("docs-duplicate"))
        val config = materialsConfig(root)
        val service = RequirementDocumentationService(iterations = FixedIterations(singleActive))
        val first = service.plan(config, link, "登录优化", directoryFolderName = "任务目录")
        service.materialize(config, first)
        val duplicate = Path.of(first.iterationDirectory).resolve("7064764629-重复目录")
        Files.createDirectory(duplicate)
        Files.createDirectory(duplicate.resolve("研发"))
        Files.copy(Path.of(first.documentationDirectory).resolve(".silverwing-requirement.json"), duplicate.resolve("研发/.silverwing-requirement.json"))

        val error = assertFailsWith<IllegalArgumentException> {
            service.plan(config, link, "登录优化", directoryFolderName = "任务目录")
        }

        assertTrue(error.message.orEmpty().contains("多个历史目录"))
    }

    @Test
    fun `does not reuse a manifest outside the canonical requirement write root`() {
        val root = Files.createDirectories(temporary.resolve("docs-invalid-layout"))
        val invalidManifest = root.resolve("OBT-20260817--20260828")
            .resolve("7064764629-任务目录")
            .resolve("研发")
            .resolve("nested")
            .resolve(".silverwing-requirement.json")
        Files.createDirectories(invalidManifest.parent)
        Files.writeString(
            invalidManifest,
            Json.encodeToString(
                RequirementDocumentationManifest(
                    identity = RequirementIdentity("obt", "userstory", "7064764629"),
                    requirementTitle = "登录优化",
                    sprint = RequirementSprintSnapshot("7070412889", "OBT-20260817--20260828"),
                    directoryName = "7064764629-任务目录",
                    createdAt = "2026-08-23T00:00:00Z",
                    updatedAt = "2026-08-23T00:00:00Z",
                ),
            ),
        )

        val error = assertFailsWith<IllegalArgumentException> {
            RequirementDocumentationService(iterations = FixedIterations(singleActive))
                .plan(materialsConfig(root), link, "登录优化", directoryFolderName = "任务目录")
        }

        assertTrue(error.message.orEmpty().contains("必须位于需求资料写入目录"))
    }

    private class FixedIterations(private val values: List<RequirementSprint>) : RequirementIterationProvider {
        override fun resolve(requirementLink: String, projectKey: String): List<RequirementSprint> = values
    }

    private fun materialsConfig(root: Path) = AppConfig(
        requirementMaterialsRoot = root.toString(),
        requirementMaterialsSubdirectory = "研发",
    )

    private companion object {
        const val link = "https://project.feishu.cn/obt/userstory/detail/7064764629"
        val singleActive = listOf(RequirementSprint("7070412889", "OBT-20260817--20260828", "进行中"))
    }
}
