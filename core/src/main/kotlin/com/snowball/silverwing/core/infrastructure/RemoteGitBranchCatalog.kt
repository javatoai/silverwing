package com.snowball.silverwing.core

import java.time.Duration

/**
 * 只读读取远程仓库的普通分支。这里绝不 clone、fetch 或执行仓库内容，
 * 因此可安全地用于“添加来源”表单的即时分支选择。
 */
class RemoteGitBranchCatalog(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val gitExecutable: () -> String = { "git" },
) {
    fun load(repositoryUrl: String): List<String> {
        validateRemoteGitUrl(repositoryUrl)
        val result = runner.run(
            listOf(gitExecutable(), "ls-remote", "--heads", repositoryUrl),
            timeout = TIMEOUT,
        )
        if (!result.succeeded) {
            throw CodexExtensionCommandException("Git 分支查询失败：ls-remote --heads $repositoryUrl", result)
        }
        return parseRemoteGitBranches(result.stdout)
    }

    private companion object {
        val TIMEOUT: Duration = Duration.ofMinutes(1)
    }
}

/** 解析 `git ls-remote --heads` 输出；只保留合法的 refs/heads 名称。 */
fun parseRemoteGitBranches(output: String): List<String> = output.lineSequence()
    .mapNotNull { line ->
        val ref = line.substringAfter('\t', missingDelimiterValue = "")
        if (ref.startsWith("refs/heads/")) ref.removePrefix("refs/heads/") else null
    }
    .filter { branch -> runCatching { validateGitRef(branch) }.isSuccess }
    .distinct()
    .sorted()
    .toList()

/** 按产品约定选取默认分支；没有 master/main 时保留空选择。 */
fun preferredRemoteGitBranch(branches: List<String>): String? = when {
    "master" in branches -> "master"
    "main" in branches -> "main"
    else -> null
}

/** 从 HTTPS 或 SSH Git URL 推导可编辑的默认显示名称。 */
fun defaultExtensionSourceName(repositoryUrl: String): String = repositoryUrl
    .trim()
    .substringAfterLast('/')
    .substringAfterLast(':')
    .removeSuffix(".git")
    .ifBlank { "" }
