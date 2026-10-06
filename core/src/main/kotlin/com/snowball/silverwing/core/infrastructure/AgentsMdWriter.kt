package com.snowball.silverwing.core

import java.util.Locale

/** Renders dynamic fragments consumed by the task-local Agent document set. */
object AgentsMdWriter {
    const val FILE_NAME = "AGENTS.md"

    /** Dynamic requirement and handoff facts. */
    fun renderTaskContext(manifest: TaskManifest): String = buildString {
        appendLine("## 需求链接")
        appendLine()
        appendLine(manifest.requirementLink.ifBlank { "（未填写）" })
        appendLine()
        manifest.agentContext?.let { context ->
            appendLine("## 任务交接（仅 Agent CLI 创建）")
            appendLine()
            appendLine("在阅读、修改或执行任何任务操作前，先阅读 `${context.handoffRelativePath}`。")
            appendLine("将其作为本任务的目标、范围、已验证事实、风险与下一步的权威交接记录。")
            appendLine("若该文件缺失、损坏、过期或与当前工作区状态矛盾，停止执行会产生副作用的操作，先向用户报告差异。")
            appendLine()
        }
        if (manifest.requirementMaterials.status != RequirementMaterialsStatus.NOT_REQUESTED) {
            appendLine("## 任务资料目录")
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
                    appendLine("可在任务详情中重试。")
                }
            }
            appendLine()
        }
    }

    /** Dynamic writable/read-only repository boundary. */
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
            appendLine("- 需要额外服务时，请先让用户在任务中添加服务。")
            appendLine()
        }
    }

    private fun escapeCell(value: String): String = value.replace("|", "\\|")
}
