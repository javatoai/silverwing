package com.snowball.silverwing.core

/**
 * A fully qualified remote branch reference persisted as `<remote>/<branch>`.
 * The first slash is the boundary because Git branch names may contain slashes,
 * while remote names used by silverwing are deliberately restricted to one segment.
 */
data class RemoteBranchRef(val remote: String, val branch: String) {
    init {
        require(remote.isNotBlank() && '/' !in remote && remote != "." && remote != "..") {
            "远程名称不合法：$remote"
        }
        require(branch.isNotBlank() && !branch.startsWith('/') && !branch.endsWith('/')) {
            "远程分支不能为空"
        }
        require(branch != "@" && ".." !in branch && "@{" !in branch) { "远程分支格式不合法：$branch" }
        require(branch.none { it.isWhitespace() || it.code < 32 || it in "~^:?*[\\" }) {
            "远程分支包含 Git 不允许的字符：$branch"
        }
        require(branch.split('/').all { segment ->
            segment.isNotEmpty() && !segment.startsWith('.') && !segment.endsWith('.') &&
                !segment.endsWith(".lock", ignoreCase = true)
        }) { "远程分支路径片段不合法：$branch" }
    }

    override fun toString(): String = "$remote/$branch"

    companion object {
        fun parse(value: String): RemoteBranchRef {
            val normalized = value.trim()
            val separator = normalized.indexOf('/')
            require(separator > 0 && separator < normalized.lastIndex) {
                "远程分支必须使用 <remote>/<branch> 格式，例如 origin/release/test"
            }
            return RemoteBranchRef(normalized.substring(0, separator), normalized.substring(separator + 1))
        }
    }
}

/** Pure policy shared by the sidebar and controller navigation fallback. */
object TagNavigationPolicy {
    fun isVisible(config: AppConfig): Boolean = config.tagEnabled && config.groups.any(GroupConfig::tagEnabled)
}

/** Keeps a user's branch edit while allowing untouched drafts to follow the selected group. */
object GroupBranchPrefixPolicy {
    fun onGroupChanged(
        currentBranch: String,
        previousPrefix: String,
        manuallyEdited: Boolean,
        nextPrefix: String,
    ): String = if (!manuallyEdited || currentBranch == previousPrefix) nextPrefix else currentBranch
}

/** Resolves an optional label without coupling display text to branch naming. */
object ModuleDisplayNaming {
    fun resolve(configuredName: String, serviceName: String, baseRef: String, moduleCount: Int): String {
        val configured = configuredName.trim()
        if (configured.isBlank() || moduleCount == 1 && configured == "default") return if (moduleCount == 1) {
            serviceName
        } else {
            baseRef.trimEnd('/').substringAfterLast('/')
        }
        return configured
    }
}
