package com.snowball.silverwing.core

import java.net.URI
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.security.MessageDigest
import java.util.Locale

fun interface RepositoryInspector {
    fun inspect(selectedDirectory: Path): RepositoryConfig
}

/** Validates one user-selected main checkout; it never walks surrounding directories. */
class GitRepositoryInspector(
    private val git: GitClient = GitClient(),
) : RepositoryInspector {
    override fun inspect(selectedDirectory: Path): RepositoryConfig {
        require(Files.isDirectory(selectedDirectory)) { "目录不存在：$selectedDirectory" }
        val root = runCatching { git.topLevel(selectedDirectory).canonicalOrNormalized() }
            .getOrElse { throw IllegalArgumentException("所选目录不是 Git 仓库：$selectedDirectory", it) }

        // Linked worktrees and submodules use a .git pointer file. They are valid
        // development checkouts but unstable catalog roots because they may be removed.
        require(Files.isDirectory(root.resolve(".git"))) {
            "请选择主 Git 仓库目录，不能添加 Linked Worktree 或子模块：$root"
        }
        val common = git.commonDirectory(root).canonicalOrNormalized()
        val origin = git.remoteUrl(root)?.also(::requireCredentialFreeUrl)
        return RepositoryConfig(
            id = repositoryId(common),
            name = root.fileName.toString(),
            rootPath = root.toString(),
            gitCommonDirectory = common.toString(),
            originUrl = origin,
            currentBranch = git.currentBranch(root),
            defaultRemoteBranch = origin?.let { git.remoteDefaultBranch(root) },
        )
    }

    private fun requireCredentialFreeUrl(value: String) {
        val uri = runCatching { URI(value) }.getOrNull() ?: return
        require(uri.scheme?.lowercase(Locale.ROOT) !in setOf("http", "https") || uri.userInfo.isNullOrBlank()) {
            "origin URL 包含内嵌凭证，请改用 Git Credential Manager 或 SSH 后再添加"
        }
    }

    private fun repositoryId(commonDirectory: Path): String {
        val digest = MessageDigest.getInstance("SHA-256")
            .digest(commonDirectory.toString().lowercase(Locale.ROOT).toByteArray(StandardCharsets.UTF_8))
            .take(8)
            .joinToString("") { "%02x".format(Locale.ROOT, it) }
        return "repo-$digest"
    }
}
