package com.snowball.silverwing.core

import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.FileAlreadyExistsException
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

/** A single Markdown file that will be written for a task's Agent instructions. */
data class AgentDocumentPreviewFile(
    val relativePath: String,
    val content: String,
) {
    init {
        require(relativePath.isNotBlank()) { "Agent 预览文件路径不能为空" }
    }

    val fileName: String get() = relativePath.substringAfterLast('/')
}

/** Structured task Agent preview. Files stay independent instead of being rendered as one synthetic Markdown document. */
data class AgentDocumentPreview(
    val files: List<AgentDocumentPreviewFile>,
) {
    init {
        require(files.isNotEmpty()) { "Agent 预览至少需要一个文件" }
        require(files.map(AgentDocumentPreviewFile::relativePath).distinct().size == files.size) {
            "Agent 预览文件路径不能重复"
        }
    }

    val rootFile: AgentDocumentPreviewFile get() = files.first()
}

interface AgentDocuments {
    fun readGlobal(): String
    fun saveGlobal(content: String)
    fun readGroup(groupId: String): String
    fun saveGroup(groupId: String, content: String)
    fun writeTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String? = null,
    ): Path

    /** Creates the initial document set for a newly created task. */
    fun createTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String? = null,
    ): Path = writeTaskDocument(taskDirectory, manifest, repositories, taskNotes)

    /** Explicit repair entry point for a task whose generated route file is missing. */
    fun refreshTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
    ): Path = writeTaskDocument(taskDirectory, manifest, repositories)
}

/**
 * Owns the three-level AGENTS.md protocol. The disk files are authoritative;
 * Configuration shards intentionally store no duplicate instruction text.
 */
class AgentDocumentService(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
) : AgentDocuments {
    fun ensureGlobalFile(): Path = ensureFile(paths.globalAgents)

    fun ensureGroupFile(groupId: String): Path = ensureFile(paths.groupAgents(groupId))

    override fun readGlobal(): String = readOrEmpty(paths.globalAgents)

    override fun saveGlobal(content: String) {
        writeAtomically(paths.globalAgents, content)
    }

    override fun readGroup(groupId: String): String = readOrEmpty(paths.groupAgents(groupId))

    override fun saveGroup(groupId: String, content: String) {
        writeAtomically(paths.groupAgents(groupId), content)
    }

    override fun writeTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path = writeTaskDocuments(taskDirectory, manifest, repositories, taskNotes, restoreRootRouter = false)

    override fun createTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path = writeTaskDocuments(taskDirectory, manifest, repositories, taskNotes, restoreRootRouter = true)

    override fun refreshTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
    ): Path = writeTaskDocuments(taskDirectory, manifest, repositories, taskNotes = null, restoreRootRouter = true)

    private fun writeTaskDocuments(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
        restoreRootRouter: Boolean,
    ): Path = writeReferencedTaskDocuments(
        taskDirectory,
        manifest,
        repositories,
        taskNotes,
        restoreRootRouter,
    )

    /** Returns the only user-editable task rules file. */
    fun taskNotesFile(taskDirectory: Path, manifest: TaskManifest): Path =
        taskAgentDirectory(taskDirectory).resolve(TASK_RULES_FILE_NAME)

    fun taskAgentDirectory(taskDirectory: Path): Path =
        taskDirectory.resolve(HandoffDocumentWriter.DIRECTORY_NAME).resolve(TASK_AGENT_DIRECTORY_NAME)

    fun taskNotesFromDocument(manifest: TaskManifest, document: String): String = document.trimEnd('\r', '\n')

    fun replaceTaskNotesDocument(manifest: TaskManifest, document: String, notes: String): String = notes.trimEnd()

    fun renderPreview(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String,
    ): AgentDocumentPreview = renderReferencedPreview(taskDirectory, manifest, repositories, taskNotes)

    private fun writeReferencedTaskDocuments(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
        restoreRootRouter: Boolean,
    ): Path {
        taskDirectory.createDirectories()
        val root = taskDirectory.resolve(AgentsMdWriter.FILE_NAME)
        if (restoreRootRouter && !root.exists()) writeAtomically(root, renderReferencedRootRouter())

        ensureGlobalFile()
        ensureGroupFile(manifest.groupId)
        val agentDirectory = taskAgentDirectory(taskDirectory)
        writeIfChanged(agentDirectory.resolve(TASK_CONTEXT_FILE_NAME), renderTaskContext(manifest))
        writeIfChanged(agentDirectory.resolve(WORKTREE_SCOPE_FILE_NAME), renderWorktreeScope(manifest, repositories))
        writeIfChanged(agentDirectory.resolve(RULE_SOURCES_FILE_NAME), renderRuleSources(manifest))

        val rules = agentDirectory.resolve(TASK_RULES_FILE_NAME)
        when {
            taskNotes != null -> {
                writeIfChanged(rules, taskNotes.trimEnd())
            }
            !rules.exists() -> writeAtomically(rules, "")
        }
        return root
    }

    private fun renderReferencedPreview(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String,
    ): AgentDocumentPreview = AgentDocumentPreview(
        listOf(
            AgentDocumentPreviewFile(AgentsMdWriter.FILE_NAME, renderReferencedRootRouter()),
            AgentDocumentPreviewFile(relativeTaskAgentPath(TASK_CONTEXT_FILE_NAME), renderTaskContext(manifest)),
            AgentDocumentPreviewFile(relativeTaskAgentPath(WORKTREE_SCOPE_FILE_NAME), renderWorktreeScope(manifest, repositories)),
            AgentDocumentPreviewFile(relativeTaskAgentPath(RULE_SOURCES_FILE_NAME), renderRuleSources(manifest)),
            AgentDocumentPreviewFile(relativeTaskAgentPath(TASK_RULES_FILE_NAME), taskNotes.trimEnd()),
        ),
    )

    private fun renderReferencedRootRouter(): String = """
        # 任务说明

        > 本文件是稳定的系统路由。不要在这里写任务专属要求；请编辑 `${relativeTaskAgentPath(TASK_RULES_FILE_NAME)}`。

        在分析、修改代码或执行 Git 写操作前，必须阅读以下文件：

        1. `${relativeTaskAgentPath(TASK_CONTEXT_FILE_NAME)}`：需求链接、资料目录、任务交接与系统元数据。
        2. `${relativeTaskAgentPath(WORKTREE_SCOPE_FILE_NAME)}`：允许修改的 Worktree 与仅可阅读的本地仓库。
        3. `${relativeTaskAgentPath(RULE_SOURCES_FILE_NAME)}`：全局和组规则的实际位置；继续阅读其中列出的规则文件。
        4. `${relativeTaskAgentPath(TASK_RULES_FILE_NAME)}`：本任务专属规则。

        只允许修改 `WORKTREE-SCOPE.md` 标明可改动的 Worktree。规则冲突时按：任务专属规则 > 组规则 > 全局规则。
    """.trimIndent() + "\n"

    private fun renderTaskContext(manifest: TaskManifest): String = buildString {
        appendLine("# 任务上下文")
        appendLine()
        appendLine("> 本文件由系统生成，会随需求和任务上下文更新。")
        appendLine()
        append(AgentsMdWriter.renderTaskContext(manifest).trimEnd())
        appendLine()
        appendLine()
        appendLine("## 系统元数据")
        appendLine()
        appendLine("- 任务清单是只读元数据；不要手动改写。")
        appendLine("- `${HandoffDocumentWriter.DIRECTORY_NAME}/${HandoffDocumentWriter.FILE_NAME}` 存在时，是 Agent CLI 任务的交接记录。")
        appendLine()
    }

    private fun renderWorktreeScope(
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
    ): String = buildString {
        appendLine("# Worktree 范围")
        appendLine()
        appendLine("> 本文件由系统生成，会随服务和 Worktree 变动更新。")
        appendLine()
        append(AgentsMdWriter.renderWorktreeScope(manifest, repositories).trimEnd())
        appendLine()
    }

    private fun renderRuleSources(manifest: TaskManifest): String = buildString {
        appendLine("# 共享规则来源")
        appendLine()
        appendLine("> 先阅读以下共享规则文件，再结合任务专属规则执行。")
        appendLine()
        appendLine("## 全局规则")
        appendLine()
        appendLine("`${paths.globalAgents.toAbsolutePath().normalize()}`")
        appendLine()
        appendLine("## 组规则")
        appendLine()
        appendLine("`${paths.groupAgents(manifest.groupId).toAbsolutePath().normalize()}`")
        appendLine()
        appendLine("## 规则优先级")
        appendLine()
        appendLine("发生冲突时按：任务专属规则 > 组规则 > 全局规则。")
        appendLine()
    }

    private fun relativeTaskAgentPath(fileName: String): String =
        "${HandoffDocumentWriter.DIRECTORY_NAME}/$TASK_AGENT_DIRECTORY_NAME/$fileName"

    private fun readOrEmpty(path: Path): String =
        if (path.exists()) Files.readString(path) else ""

    private fun ensureFile(path: Path, initialContent: String = ""): Path {
        path.parent.createDirectories()
        if (!path.exists()) {
            try {
                Files.writeString(path, initialContent, StandardOpenOption.CREATE_NEW)
            } catch (_: FileAlreadyExistsException) {
                // A watcher or another window may have created it concurrently.
            }
        }
        return path
    }

    private fun writeAtomically(target: Path, content: String) {
        target.parent.createDirectories()
        val temporary = Files.createTempFile(target.parent, ".${target.fileName}-", ".tmp")
        Files.writeString(temporary, content)
        try {
            Files.move(
                temporary,
                target,
                StandardCopyOption.REPLACE_EXISTING,
                StandardCopyOption.ATOMIC_MOVE,
            )
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
        }
    }

    private fun writeIfChanged(target: Path, content: String) {
        if (target.exists() && Files.readString(target) == content) return
        writeAtomically(target, content)
    }

    companion object {
        const val TASK_AGENT_DIRECTORY_NAME = "agent"
        const val TASK_CONTEXT_FILE_NAME = "TASK-CONTEXT.md"
        const val WORKTREE_SCOPE_FILE_NAME = "WORKTREE-SCOPE.md"
        const val RULE_SOURCES_FILE_NAME = "RULE-SOURCES.md"
        const val TASK_RULES_FILE_NAME = "TASK-RULES.md"
    }
}
