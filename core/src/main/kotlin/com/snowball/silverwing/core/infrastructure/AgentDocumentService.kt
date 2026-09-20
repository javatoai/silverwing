package com.snowball.silverwing.core

import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.FileAlreadyExistsException
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

class AgentDocumentFormatException(message: String) : IllegalStateException(message)

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
        validateUserContent(content, "全局 Agent 说明")
        writeAtomically(paths.globalAgents, content)
    }

    override fun readGroup(groupId: String): String = readOrEmpty(paths.groupAgents(groupId))

    override fun saveGroup(groupId: String, content: String) {
        validateUserContent(content, "组 Agent 说明")
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
    ): Path = when (manifest.agentDocumentLayout) {
        AgentDocumentLayout.INLINE_V1 -> writeInlineTaskDocument(taskDirectory, manifest, repositories, taskNotes)
        AgentDocumentLayout.REFERENCED_V2 -> writeReferencedTaskDocuments(
            taskDirectory,
            manifest,
            repositories,
            taskNotes,
            restoreRootRouter,
        )
    }

    /** Returns the only user-editable task rules file for the selected layout. */
    fun taskNotesFile(taskDirectory: Path, manifest: TaskManifest): Path = when (manifest.agentDocumentLayout) {
        AgentDocumentLayout.INLINE_V1 -> taskDirectory.resolve(AgentsMdWriter.FILE_NAME)
        AgentDocumentLayout.REFERENCED_V2 -> taskAgentDirectory(taskDirectory).resolve(TASK_RULES_FILE_NAME)
    }

    fun taskAgentDirectory(taskDirectory: Path): Path =
        taskDirectory.resolve(HandoffDocumentWriter.DIRECTORY_NAME).resolve(TASK_AGENT_DIRECTORY_NAME)

    fun taskNotesFromDocument(manifest: TaskManifest, document: String): String = when (manifest.agentDocumentLayout) {
        AgentDocumentLayout.INLINE_V1 -> extractTaskNotes(document)
        AgentDocumentLayout.REFERENCED_V2 -> document.trimEnd('\r', '\n')
    }

    fun replaceTaskNotesDocument(manifest: TaskManifest, document: String, notes: String): String = when (manifest.agentDocumentLayout) {
        AgentDocumentLayout.INLINE_V1 -> replaceInlineTaskNotes(document, notes)
        AgentDocumentLayout.REFERENCED_V2 -> notes.trimEnd()
    }

    private fun writeInlineTaskDocument(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): Path {
        taskDirectory.createDirectories()
        val target = taskDirectory.resolve(AgentsMdWriter.FILE_NAME)
        val preservedNotes = when {
            taskNotes != null -> taskNotes
            target.exists() -> extractTaskNotes(Files.readString(target))
            else -> ""
        }
        validateUserContent(preservedNotes, "任务人工说明")
        val generated = buildGeneratedContent(taskDirectory, manifest, repositories)
        val document = buildString {
            appendLine(GENERATED_BEGIN)
            append(generated.trimEnd())
            appendLine()
            appendLine(GENERATED_END)
            appendLine()
            appendLine(TASK_NOTES_BEGIN)
            if (preservedNotes.isNotBlank()) {
                appendLine(preservedNotes.trimEnd())
            }
            appendLine(TASK_NOTES_END)
        }
        writeAtomically(target, document)
        return target
    }

    fun renderPreview(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String,
    ): String = when (manifest.agentDocumentLayout) {
        AgentDocumentLayout.INLINE_V1 -> renderInlinePreview(taskDirectory, manifest, repositories, taskNotes)
        AgentDocumentLayout.REFERENCED_V2 -> renderReferencedPreview(taskDirectory, manifest, repositories, taskNotes)
    }

    private fun renderInlinePreview(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String,
    ): String {
        val generated = buildGeneratedContent(taskDirectory, manifest, repositories)
        return buildString {
            appendLine(GENERATED_BEGIN)
            append(generated.trimEnd())
            appendLine()
            appendLine(GENERATED_END)
            appendLine()
            appendLine(TASK_NOTES_BEGIN)
            if (taskNotes.isNotBlank()) appendLine(taskNotes.trimEnd())
            appendLine(TASK_NOTES_END)
        }
    }

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
                validateUserContent(taskNotes, "任务人工说明")
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
    ): String = buildString {
        appendLine("# Agent 文件预览")
        appendPreviewFile(AgentsMdWriter.FILE_NAME, renderReferencedRootRouter())
        appendPreviewFile(relativeTaskAgentPath(TASK_CONTEXT_FILE_NAME), renderTaskContext(manifest))
        appendPreviewFile(relativeTaskAgentPath(WORKTREE_SCOPE_FILE_NAME), renderWorktreeScope(manifest, repositories))
        appendPreviewFile(relativeTaskAgentPath(RULE_SOURCES_FILE_NAME), renderRuleSources(manifest))
        appendPreviewFile(relativeTaskAgentPath(TASK_RULES_FILE_NAME), taskNotes.trimEnd())
    }

    private fun StringBuilder.appendPreviewFile(relativePath: String, content: String) {
        appendLine("## `$relativePath`")
        appendLine()
        if (content.isBlank()) appendLine("（空）") else appendLine(content.trimEnd())
        appendLine()
    }

    private fun renderReferencedRootRouter(): String = """
        <!-- SILVERWING:AGENT-ROUTER:V2 -->
        # silverwing 任务说明

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
        appendLine("> 本文件由 silverwing 生成，会随需求和任务上下文更新。")
        appendLine()
        append(AgentsMdWriter.renderTaskContext(manifest).trimEnd())
        appendLine()
        appendLine()
        appendLine("## 系统元数据")
        appendLine()
        appendLine("- `silverwing.json` 是任务元数据，仅供读取；不要手动改写。")
        appendLine("- `${HandoffDocumentWriter.DIRECTORY_NAME}/${HandoffDocumentWriter.FILE_NAME}` 存在时，是 Agent CLI 任务的交接记录。")
        appendLine()
    }

    private fun renderWorktreeScope(
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
    ): String = buildString {
        appendLine("# Worktree 范围")
        appendLine()
        appendLine("> 本文件由 silverwing 生成，会随服务和 Worktree 变动更新。")
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

    fun extractTaskNotes(document: String): String {
        val generatedBegin = uniqueMarkerIndex(document, GENERATED_BEGIN)
        val generatedEnd = uniqueMarkerIndex(document, GENERATED_END)
        val begin = document.indexOf(TASK_NOTES_BEGIN)
        val end = document.indexOf(TASK_NOTES_END)
        if (generatedBegin < 0 || generatedEnd < 0 || begin < 0 || end < 0 ||
            !(generatedBegin < generatedEnd && generatedEnd < begin && begin < end) ||
            document.indexOf(TASK_NOTES_BEGIN, begin + TASK_NOTES_BEGIN.length) >= 0 ||
            document.indexOf(TASK_NOTES_END, end + TASK_NOTES_END.length) >= 0
        ) {
            throw AgentDocumentFormatException(
                "AGENTS.md 的 SILVERWING:TASK-NOTES 标记缺失或损坏；为避免覆盖人工内容，已停止生成。",
            )
        }
        return document.substring(begin + TASK_NOTES_BEGIN.length, end).trim('\r', '\n')
    }

    private fun replaceInlineTaskNotes(document: String, notes: String): String {
        extractTaskNotes(document)
        val begin = document.indexOf(TASK_NOTES_BEGIN) + TASK_NOTES_BEGIN.length
        val end = document.indexOf(TASK_NOTES_END)
        return buildString {
            append(document.substring(0, begin)); appendLine()
            if (notes.isNotBlank()) appendLine(notes.trimEnd())
            append(document.substring(end))
        }
    }

    private fun uniqueMarkerIndex(document: String, marker: String): Int {
        val first = document.indexOf(marker)
        if (first < 0 || document.indexOf(marker, first + marker.length) >= 0) return -1
        return first
    }

    private fun buildGeneratedContent(
        taskDirectory: Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
    ): String {
        val global = readGlobal()
        val group = readGroup(manifest.groupId)
        validateUserContent(global, "全局 Agent 说明")
        validateUserContent(group, "组 Agent 说明")
        return buildString {
            append(AgentsMdWriter.render(taskDirectory, manifest, repositories, ""))
            appendInstructionSection("全局 Agent 说明", global)
            appendInstructionSection("组 Agent 说明", group)
            appendLine()
            appendLine("## 说明优先级")
            appendLine()
            appendLine("发生冲突时按：任务人工说明 > 组说明 > 全局说明。")
        }
    }

    private fun StringBuilder.appendInstructionSection(title: String, content: String) {
        if (content.isBlank()) return
        appendLine()
        appendLine("## $title")
        appendLine()
        appendLine(content.trim())
    }

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

    private fun validateUserContent(content: String, label: String) {
        val reserved = listOf(GENERATED_BEGIN, GENERATED_END, TASK_NOTES_BEGIN, TASK_NOTES_END)
        require(reserved.none(content::contains)) { "$label 包含 silverwing 保留标记，已拒绝保存或生成" }
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
        const val GENERATED_BEGIN = "<!-- SILVERWING:GENERATED:BEGIN -->"
        const val GENERATED_END = "<!-- SILVERWING:GENERATED:END -->"
        const val TASK_NOTES_BEGIN = "<!-- SILVERWING:TASK-NOTES:BEGIN -->"
        const val TASK_NOTES_END = "<!-- SILVERWING:TASK-NOTES:END -->"
        const val TASK_AGENT_DIRECTORY_NAME = "agent"
        const val TASK_CONTEXT_FILE_NAME = "TASK-CONTEXT.md"
        const val WORKTREE_SCOPE_FILE_NAME = "WORKTREE-SCOPE.md"
        const val RULE_SOURCES_FILE_NAME = "RULE-SOURCES.md"
        const val TASK_RULES_FILE_NAME = "TASK-RULES.md"
    }
}
