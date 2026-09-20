package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.util.Locale

/** Writes the generated system section of a task-level AGENTS.md document. */
object AgentsMdWriter {
    const val FILE_NAME = "AGENTS.md"

    fun write(
        taskDirectory: Path,
        manifest: TaskManifest,
        allRepositories: List<RepositoryInfo>,
        appendix: String,
    ) {
        Files.writeString(taskDirectory.resolve(FILE_NAME), render(taskDirectory, manifest, allRepositories, appendix))
    }

    /**
     * Renders only task context that an agent needs to safely change code.
     *
     * Task names, branches and lifecycle state are intentionally omitted: they duplicate the UI
     * and become stale quickly, whereas the worktree baseline and path define the edit boundary.
     */
    fun render(
        taskDirectory: Path,
        manifest: TaskManifest,
        allRepositories: List<RepositoryInfo>,
        appendix: String,
    ): String {
        val notes = appendix.trim()

        return buildString {
            appendLine("**任务工作区说明（AGENTS）**")
            appendLine()
            appendLine("> 本文件由 silverwing 生成。系统区会随任务说明保存而更新；人工说明区会被保留。")
            appendLine()
            append(renderTaskContext(manifest))
            append(renderWorktreeScope(manifest, allRepositories))
            if (notes.isNotEmpty()) {
                appendLine()
                appendLine("## 任务人工说明")
                appendLine()
                appendLine(notes)
            }
            appendLine()
        }
    }

    /** Dynamic requirement and handoff facts shared by the legacy and referenced layouts. */
    fun renderTaskContext(manifest: TaskManifest): String = buildString {
            appendLine("## 需求链接")
            appendLine()
            appendLine(manifest.requirementLink.ifBlank { "（未填写）" })
            appendLine()
            manifest.agentContext?.let { context ->
                appendLine("## silverwing 任务交接（仅 Agent CLI 创建）")
                appendLine()
                appendLine("在阅读、修改或执行任何任务操作前，先阅读 `${context.handoffRelativePath}`。")
                appendLine("将其作为本任务的目标、范围、已验证事实、风险与下一步的权威交接记录。")
                appendLine("若该文件缺失、损坏、过期或与当前工作区状态矛盾，停止执行会产生副作用的操作，先向用户报告差异。")
                appendLine()
            }
            if (manifest.requirementMaterials.status != RequirementMaterialsStatus.NOT_REQUESTED) {
                appendLine("## 需求资料目录")
                appendLine()
                when (manifest.requirementMaterials.status) {
                    RequirementMaterialsStatus.NOT_REQUESTED -> Unit
                    RequirementMaterialsStatus.READY -> {
                        val writeRoot = manifest.requirementMaterials.writeRoot
                        appendLine("`${writeRoot}`")
                        appendLine()
                        if (manifest.agentContext != null) {
                            appendLine("Agent 过程文档与需求分析、方案、验收、风险、SQL 和脚本等研发辅助资料统一写入此目录。")
                            appendLine("迭代：`${manifest.agentContext.iterationLabel}`；`${HandoffDocumentWriter.DIRECTORY_NAME}/${HandoffDocumentWriter.FILE_NAME}` 仍位于任务目录，用于记录本任务交接上下文。")
                        } else {
                            appendLine("需求辅助 Markdown、SQL 和脚本写入此目录；产品源代码仍写入本任务 Worktree。")
                        }
                        appendLine("产品源代码仍写入本任务 Worktree；服务仓库自身的 README、ADR、API 文档等仍留在相应 Worktree。")
                    }
                    RequirementMaterialsStatus.FAILED -> {
                        appendLine("（暂不可用）")
                        manifest.requirementMaterials.failureReason?.let { appendLine("原因：$it") }
                        appendLine("可在 silverwing 的任务详情中重试。")
                    }
                }
                appendLine()
            }
        }

    /** Dynamic writable/read-only repository boundary shared by both document layouts. */
    fun renderWorktreeScope(
        manifest: TaskManifest,
        allRepositories: List<RepositoryInfo>,
    ): String {
        val taskRepositoryIds = manifest.services.map { it.repositoryId }.toSet()
        val otherRepositories = allRepositories
            .filter { it.id !in taskRepositoryIds }
            .sortedBy { it.name.lowercase(Locale.ROOT) }

        return buildString {
            appendLine("## 本任务可改动的 Worktree")
            appendLine()
            appendLine("| 服务名 | 创建基线 | 策略 | Worktree 路径 |")
            appendLine("|--------|----------|------|---------------|")
            val workspaces = manifest.services.distinctBy { workspace ->
                listOf(
                    workspace.serviceName,
                    workspace.baseRef.orEmpty(),
                    workspace.strategy.name,
                    workspace.worktreePath,
                )
            }
            if (workspaces.isEmpty()) {
                appendLine("| （无） |  |  |  |")
            } else {
                workspaces.forEach { workspace ->
                    appendLine(
                        "| ${escapeCell(workspace.serviceName)} | `${workspace.baseRef ?: workspace.branch}` | " +
                            "${workspace.strategy.name} | `${workspace.worktreePath}` |",
                    )
                }
            }
            appendLine()
            appendLine("## 其他本地服务（只读上下文）")
            appendLine()
            appendLine("下列仓库不在本任务 Worktree 中。它们仅用于阅读代码和梳理调用链，禁止直接修改或提交。")
            appendLine()
            appendLine("| 服务名 | 本地仓库路径 |")
            appendLine("|--------|--------------|")
            if (otherRepositories.isEmpty()) {
                appendLine("| （无） |  |")
            } else {
                otherRepositories.forEach { repository ->
                    appendLine("| ${escapeCell(repository.name)} | `${repository.rootPath}` |")
                }
            }
            appendLine()
            appendLine("## Agent 使用提示")
            appendLine()
            appendLine("- 只允许修改上方“本任务可改动的 Worktree”中的路径；不要修改主 checkout 或其他本地服务。")
            appendLine("- 需要额外服务时，请先让用户在 silverwing 中为本任务添加服务。")
            appendLine()
        }
    }

    private fun escapeCell(value: String): String = value.replace("|", "\\|")
}
