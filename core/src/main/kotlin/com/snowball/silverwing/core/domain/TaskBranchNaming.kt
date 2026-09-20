package com.snowball.silverwing.core

/** Naming rules shared by standard Worktree configuration, preview, preflight and provisioning. */
object StandardWorktreeModuleNaming {
    const val DEFAULT_NAME: String = "default"

    private val validName = Regex("[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*")

    fun effectiveName(module: ServiceModuleConfig): String = module.name.trim().ifBlank { DEFAULT_NAME }

    fun requireValid(modules: List<ServiceModuleConfig>): Map<String, String> {
        require(modules.isNotEmpty()) { "至少需要一个模块" }
        val names = modules.associate { module -> module.id to effectiveName(module) }
        names.forEach { (moduleId, name) ->
            require(validName.matches(name)) {
                "模块 $moduleId 名称不合法：$name；只允许英文字母、数字、-、_、/，且 / 不能连续或位于首尾"
            }
        }
        if (modules.size > 1) {
            val duplicates = names.values.groupBy(String::lowercase).filterValues { it.size > 1 }.values.flatten().distinct()
            require(duplicates.isEmpty()) { "多模块名称不能重复（忽略大小写）：${duplicates.joinToString()}" }
            val directoryDuplicates = names.values
                .groupBy { directorySegment(it).lowercase() }
                .filterValues { it.size > 1 }
                .values
                .flatten()
                .distinct()
            require(directoryDuplicates.isEmpty()) {
                "模块名称转换为目录后不能重复：${directoryDuplicates.joinToString()}"
            }
        }
        return names
    }

    fun directorySegment(moduleName: String): String = moduleName.replace('/', '-')
}

/** Derives one deterministic writable task branch per standard Worktree module. */
object TaskBranchNaming {
    fun derive(requestedBranch: String, modules: List<ServiceModuleConfig>): Map<String, String> {
        require(requestedBranch.isNotBlank()) { "任务分支不能为空" }
        require(modules.isNotEmpty()) { "至少需要一个模块" }
        require(modules.map { it.id }.distinct().size == modules.size) { "模块 ID 不能重复" }
        val names = StandardWorktreeModuleNaming.requireValid(modules)
        if (modules.size == 1) return mapOf(modules.single().id to requestedBranch)
        return modules.associate { module -> module.id to "$requestedBranch-${names.getValue(module.id)}" }
    }

    fun resolve(
        requestedBranch: String,
        modules: List<ServiceModuleConfig>,
        explicitBranches: Map<String, String> = emptyMap(),
    ): Map<String, String> {
        require(explicitBranches.keys.all { explicitId -> modules.any { it.id == explicitId } }) {
            "任务分支覆盖引用了不存在的模块"
        }
        val defaults = derive(requestedBranch, modules)
        val resolved = defaults.mapValues { (moduleId, defaultBranch) ->
            explicitBranches[moduleId]?.trim()?.also { require(it.isNotBlank()) { "模块任务分支不能为空" } }
                ?: defaultBranch
        }
        requireNoConflicts(resolved.values)
        return resolved
    }

    /** Validates final targets in one repository; Git cannot store both a ref and its descendants. */
    fun requireNoConflicts(branches: Collection<String>) {
        val grouped = branches.groupBy(String::lowercase)
        val duplicates = grouped.filterValues { it.size > 1 }.keys
        require(duplicates.isEmpty()) { "同一仓库的模块目标分支不能重复：${duplicates.joinToString()}" }
        grouped.forEach { (branch, originals) ->
            val parent = branch.indices
                .filter { branch[it] == '/' }
                .map { branch.substring(0, it) }
                .firstOrNull(grouped::containsKey)
                ?.let { grouped.getValue(it).single() }
            require(parent == null) {
                "同一仓库的模块目标分支不能互为父子路径：$parent 与 ${originals.single()}"
            }
        }
    }

    /** Canonical identity used to decide whether modules may share one physical Worktree. */
    fun baseIdentity(module: ServiceModuleConfig): String = "${module.baseRemote}|${normalizeBaseRef(module)}"

    fun normalizeBaseRef(module: ServiceModuleConfig): String {
        val value = module.baseRef.trim()
        return value
            .removePrefix("refs/remotes/${module.baseRemote}/")
            .removePrefix("refs/heads/")
            .removePrefix("${module.baseRemote}/")
    }
}
