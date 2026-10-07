package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.jupiter.api.io.TempDir

class AgentDocumentServiceTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `new global file is empty and existing content is never overwritten`() {
        val paths = ApplicationPaths(temporary.resolve("defaults-home"))
        val service = AgentDocumentService(paths)
        service.ensureGlobalFile()
        assertEquals("", Files.readString(paths.globalAgents))

        Files.writeString(paths.globalAgents, "用户自己的规则")
        service.ensureGlobalFile()
        assertEquals("用户自己的规则", Files.readString(paths.globalAgents))
    }

    @Test
    fun `historic layout metadata still writes the neutral referenced document set`() {
        val service = AgentDocumentService(ApplicationPaths(temporary.resolve("home")))
        val taskDirectory = temporary.resolve("tasks").resolve("OBT-123")
        val manifest = manifest().copy(agentDocumentLayout = AgentDocumentLayout.INLINE_V1)

        service.createTaskDocument(taskDirectory, manifest, emptyList(), "任务第一次说明")

        val root = Files.readString(taskDirectory.resolve("AGENTS.md"))
        val rules = service.taskNotesFile(taskDirectory, manifest)
        assertTrue(root.startsWith("# 任务说明"))
        assertFalse(root.contains("<!--"))
        assertFalse(root.contains("RULE-SOURCES.md"))
        assertFalse(root.contains("WORKTREE-SCOPE.md"))
        assertEquals("任务第一次说明", Files.readString(rules))
        assertFalse(Files.exists(taskDirectory.resolve(".silverwing")))
    }

    @Test
    fun `referenced task documents keep the root stable while dynamic files change`() {
        val paths = ApplicationPaths(temporary.resolve("home-referenced"))
        val service = AgentDocumentService(paths)
        service.saveGlobal("全局约定")
        service.saveGroup("growth", "增长组约定")
        val taskDirectory = temporary.resolve("tasks").resolve("OBT-456")
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "订单服务",
            repositoryPath = "C:/source/orders",
            worktreePath = "C:/tasks/OBT-456/orders",
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/orders",
            baseRef = "origin/master",
        )
        val initial = manifest(groupId = "growth").copy(
            taskDirectoryName = "OBT-456",
            agentDocumentLayout = AgentDocumentLayout.REFERENCED_V2,
            services = listOf(workspace),
            agentContext = AgentTaskContext(
                documentationDirectory = "D:/requirements/OBT-456/研发资料",
                iterationLabel = "Sprint-456",
            ),
        )

        service.createTaskDocument(taskDirectory, initial, emptyList(), "第一版任务规则")

        val root = taskDirectory.resolve("AGENTS.md")
        val stableRoot = Files.readString(root)
        val agentDirectory = service.taskAgentDirectory(taskDirectory)
        assertTrue(stableRoot.startsWith("# 任务说明"))
        assertFalse(stableRoot.contains("<!--"))
        assertTrue(stableRoot.contains(paths.globalAgents.toAbsolutePath().normalize().toString()))
        assertTrue(stableRoot.contains(paths.groupAgents("growth").toAbsolutePath().normalize().toString()))
        assertFalse(stableRoot.contains("全局约定"))
        assertFalse(stableRoot.contains("增长组约定"))
        assertTrue(stableRoot.contains("TASK-CONTEXT.md"))
        assertFalse(stableRoot.contains(initial.requirementLink))
        assertFalse(stableRoot.contains("第一版任务规则"))
        assertTrue(Files.readString(agentDirectory.resolve(AgentDocumentService.TASK_CONTEXT_FILE_NAME)).contains(initial.requirementLink))
        val context = Files.readString(agentDirectory.resolve(AgentDocumentService.TASK_CONTEXT_FILE_NAME))
        assertTrue(context.contains("HANDOFF.md"))
        assertFalse(context.contains("silverwing", ignoreCase = true))
        assertTrue(context.contains("## Worktree 范围"))
        assertTrue(context.contains("### 本任务可改动的 Worktree"))
        assertTrue(context.contains(workspace.worktreePath))
        assertTrue(context.contains("禁止直接修改或提交"))
        assertFalse(Files.exists(agentDirectory.resolve("WORKTREE-SCOPE.md")))
        assertFalse(Files.exists(agentDirectory.resolve("RULE-SOURCES.md")))
        assertEquals("第一版任务规则", Files.readString(agentDirectory.resolve(AgentDocumentService.TASK_RULES_FILE_NAME)))

        service.saveGlobal("更新后的全局约定")
        val updated = initial.copy(requirementLink = "https://project.feishu.cn/obt/userstory/detail/456", services = emptyList())
        service.writeTaskDocument(taskDirectory, updated, emptyList())

        assertEquals(stableRoot, Files.readString(root))
        assertTrue(Files.readString(agentDirectory.resolve(AgentDocumentService.TASK_CONTEXT_FILE_NAME)).contains(updated.requirementLink))
        assertEquals("第一版任务规则", Files.readString(agentDirectory.resolve(AgentDocumentService.TASK_RULES_FILE_NAME)))

        service.writeTaskDocument(taskDirectory, updated, emptyList(), "第二版任务规则")
        assertEquals(stableRoot, Files.readString(root))
        assertEquals("第二版任务规则", Files.readString(agentDirectory.resolve(AgentDocumentService.TASK_RULES_FILE_NAME)))

        Files.delete(root)
        service.writeTaskDocument(taskDirectory, updated, emptyList())
        assertFalse(Files.exists(root))
        service.refreshTaskDocument(taskDirectory, updated, emptyList())
        assertEquals(stableRoot, Files.readString(root))
    }

    @Test
    fun `referenced preview keeps every agent file independent with root first`() {
        val service = AgentDocumentService(ApplicationPaths(temporary.resolve("home-preview")))
        val preview = service.renderPreview(
            temporary.resolve("tasks").resolve("OBT-789"),
            manifest().copy(agentDocumentLayout = AgentDocumentLayout.REFERENCED_V2),
            emptyList(),
            "任务规则",
        )

        val agentPath = "${HandoffDocumentWriter.DIRECTORY_NAME}/${AgentDocumentService.TASK_AGENT_DIRECTORY_NAME}"
        assertEquals(
            listOf(
                "AGENTS.md",
                "$agentPath/${AgentDocumentService.TASK_CONTEXT_FILE_NAME}",
                "$agentPath/${AgentDocumentService.TASK_RULES_FILE_NAME}",
            ),
            preview.files.map(AgentDocumentPreviewFile::relativePath),
        )
        assertEquals("AGENTS.md", preview.rootFile.relativePath)
        assertTrue(preview.rootFile.content.startsWith("# 任务说明"))
        assertFalse(preview.rootFile.content.contains("<!--"))
        assertTrue(preview.rootFile.content.contains("## 共享规则来源"))
        assertTrue(preview.rootFile.content.contains("任务专属规则 > 组规则 > 全局规则"))
        assertFalse(preview.rootFile.content.contains("# 任务上下文"))
        assertTrue(preview.files[1].content.contains("# 任务上下文"))
        assertTrue(preview.files[1].content.contains("## Worktree 范围"))
        assertTrue(preview.files.last().content.contains("任务规则"))
    }

    @Test
    fun `preview remains split even when manifest has historic layout metadata`() {
        val service = AgentDocumentService(ApplicationPaths(temporary.resolve("home-inline-preview")))
        val preview = service.renderPreview(
            temporary.resolve("tasks").resolve("OBT-790"),
            manifest(),
            emptyList(),
            "任务规则",
        )

        assertEquals(3, preview.files.size)
        assertEquals("AGENTS.md", preview.rootFile.relativePath)
        assertEquals("任务规则", preview.files.last().content)
    }

    @Test
    fun `three document preview matches generated files and shared rules are referenced without copying their text`() {
        val paths = ApplicationPaths(temporary.resolve("three-file-home"))
        val service = AgentDocumentService(paths)
        service.saveGlobal("global private instructions")
        service.saveGroup(DEFAULT_GROUP_ID, "group private instructions")
        val taskDirectory = temporary.resolve("three-file-task")
        val task = manifest()
        val notes = "# 手工说明\n\n保留内容。"
        service.createTaskDocument(taskDirectory, task, emptyList(), notes)
        val preview = service.renderPreview(taskDirectory, task, emptyList(), notes)

        assertEquals(3, preview.files.size)
        preview.files.forEach { file -> assertEquals(file.content, Files.readString(taskDirectory.resolve(file.relativePath))) }
        Files.list(service.taskAgentDirectory(taskDirectory)).use { files ->
            assertEquals(setOf("TASK-CONTEXT.md", "TASK-RULES.md"), files.map { it.fileName.toString() }.toList().toSet())
        }
        assertTrue(paths.globalAgents.toAbsolutePath().normalize().toString() in preview.rootFile.content)
        assertTrue(paths.groupAgents(DEFAULT_GROUP_ID).toAbsolutePath().normalize().toString() in preview.rootFile.content)
        assertFalse("global private instructions" in preview.rootFile.content)
        assertFalse("group private instructions" in preview.rootFile.content)
        assertFalse("WORKTREE-SCOPE.md" in preview.rootFile.content)
        assertFalse("RULE-SOURCES.md" in preview.rootFile.content)
        service.refreshTaskDocument(taskDirectory, task, emptyList())
        assertEquals(notes, Files.readString(service.taskNotesFile(taskDirectory, task)))
    }

    @Test
    fun `ensuring editable instruction files creates the real disk targets`() {
        val paths = ApplicationPaths(temporary.resolve("home-open"))
        val service = AgentDocumentService(paths)

        assertEquals(paths.globalAgents, service.ensureGlobalFile())
        assertEquals(paths.groupAgents("growth"), service.ensureGroupFile("growth"))
        assertTrue(Files.isRegularFile(paths.globalAgents))
        assertTrue(Files.isRegularFile(paths.groupAgents("growth")))
    }

    @Test
    fun `generated context and workspace scope keep the edit boundary without redundant task facts`() {
        val workspace = ServiceWorkspace(
            repositoryId = "repo",
            serviceName = "订单服务",
            repositoryPath = "C:/source/orders",
            worktreePath = "C:/tasks/table/orders",
            developmentTool = DevelopmentToolType.INTELLIJ_IDEA,
            branch = "feature/orders",
            health = WorkspaceHealth.READY,
            baseRef = "origin/master",
        )
        val manifest = manifest().copy(
            services = listOf(workspace),
            requirementMaterials = RequirementMaterialsDirectory(
                status = RequirementMaterialsStatus.READY,
                writeRoot = "D:/requirements/Sprint/OBT-123/研发资料",
            ),
        )
        val rendered = AgentsMdWriter.renderTaskContext(manifest) + AgentsMdWriter.renderWorktreeScope(manifest, emptyList())

        assertTrue("需求链接" in rendered)
        assertTrue("任务资料目录" in rendered)
        assertTrue("D:/requirements/Sprint/OBT-123/研发资料" in rendered)
        assertTrue("需求辅助 Markdown、SQL 和脚本" in rendered)
        assertTrue("origin/master" in rendered)
        assertTrue("STANDARD_WORKTREE" in rendered)
        assertTrue("C:/tasks/table/orders" in rendered)
        assertFalse("C:/source/orders" in rendered)
        assertFalse("## 基本信息" in rendered)
        assertFalse("feature/orders" in rendered)
        assertFalse("READY" in rendered)
        assertFalse("silverwing" in rendered.lowercase())
    }

    @Test
    fun `generated task context omits materials section when materials are not requested`() {
        val rendered = AgentsMdWriter.renderTaskContext(
            manifest().copy(requirementMaterials = RequirementMaterialsDirectory()),
        )

        assertFalse("## 任务资料目录" in rendered)
    }

    private fun manifest(groupId: String = DEFAULT_GROUP_ID) = TaskManifest(
        folderName = "OBT-123",
        taskDirectoryName = "OBT-123",
        featureBranch = "feature/OBT-123",
        requirementLink = "https://project.feishu.cn/obt/userstory/detail/123",
        createdAt = Instant.EPOCH.toString(),
        updatedAt = Instant.EPOCH.toString(),
        lifecycleStatus = TaskLifecycleStatus.ACTIVE,
        services = emptyList(),
        groupId = groupId,
    )
}
