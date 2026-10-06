package com.snowball.silverwing.core

val ServiceWorkspace.selectionKey: String
    get() = "$groupServiceId:$moduleId"

data class EffectiveTagTarget(
    val workspace: ServiceWorkspace,
    val sourceBranch: String,
    val remote: String,
    val targetBranch: String?,
    val tagMessagePrefix: String,
    val mode: TagBuildMode,
)

/** Resolves Tag permissions separately from the target captured by each operation. */
object TagPolicy {
    fun requireEnabled(config: AppConfig) {
        check(config.tagEnabled) { "全局测试Tag已关闭" }
    }

    fun resolve(config: AppConfig, manifest: TaskManifest, selection: String): EffectiveTagTarget {
        val workspace = requireWorkspace(config, manifest, selection)
        val target = if (workspace.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
            RemoteBranchRef.parse(requireNotNull(workspace.tagTargetRef) { "模块缺少测试目标分支" })
        } else null
        return EffectiveTagTarget(
            workspace,
            workspace.branch,
            target?.remote ?: workspace.pushRemote,
            target?.branch,
            workspace.tagMessagePrefix,
            workspace.tagMode,
        )
    }

    /** A retry always uses its saved destination, even after the task target changes. */
    fun resolveHistorical(config: AppConfig, manifest: TaskManifest, operation: TagOperation): EffectiveTagTarget {
        val workspace = requireWorkspace(config, manifest, "${operation.groupServiceId}:${operation.moduleId}")
        require(operation.folderName == manifest.folderName && operation.repositoryId == workspace.repositoryId) {
            "历史测试Tag与当前任务模块不匹配，无法恢复"
        }
        require(operation.sourceBranch == workspace.branch) { "历史测试Tag的研发分支与当前工作区不一致，无法恢复" }
        require(operation.remote.isNotBlank()) { "历史测试Tag缺少原远程，无法恢复；请新建测试Tag" }
        require(!operation.remote.startsWith('-')) { "历史测试Tag的原远程名称不合法，无法恢复" }
        val branch = if (operation.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) {
            requireNotNull(operation.targetBranch?.takeIf(String::isNotBlank)) {
                "历史测试Tag缺少原目标分支，无法恢复；请新建测试Tag"
            }
        } else operation.sourceBranch
        RemoteBranchRef(operation.remote, branch)
        return EffectiveTagTarget(
            workspace = workspace,
            sourceBranch = operation.sourceBranch,
            remote = operation.remote,
            targetBranch = if (operation.tagMode == TagBuildMode.MERGE_TO_TARGET_BRANCH) branch else null,
            tagMessagePrefix = workspace.tagMessagePrefix,
            mode = operation.tagMode,
        )
    }

    internal fun requireWorkspace(config: AppConfig, manifest: TaskManifest, selection: String): ServiceWorkspace {
        requireEnabled(config)
        val candidates = manifest.services.filter {
            it.selectionKey == selection || it.repositoryId == selection
        }
        require(candidates.isNotEmpty()) { "任务中不存在测试Tag目标：$selection" }
        require(candidates.size == 1) { "测试Tag目标不唯一，请选择具体模块：$selection" }
        val workspace = candidates.single()
        val group = config.group(manifest.groupId)
        check(group.tagEnabled) { "组 ${group.name} 已关闭测试Tag" }
        check(workspace.tagEnabled) { "模块 ${workspace.moduleName} 已关闭测试Tag" }
        return workspace
    }
}
