package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Instant
import java.time.OffsetDateTime

interface WorkspaceGitHistoryReader {
    fun read(worktreePath: Path, baselineRef: String?): List<WorkspaceGitCommit>
    fun read(worktreePath: Path): List<WorkspaceGitCommit> = read(worktreePath, null)
}

class WorkspaceGitHistoryService(
    private val reader: WorkspaceGitHistoryReader = GitWorkspaceGitHistoryReader(),
) {
    fun read(worktreePath: Path, baselineRef: String? = null): List<WorkspaceGitCommit> = reader.read(worktreePath, baselineRef)
}

class GitWorkspaceGitHistoryReader(
    private val git: GitClient = GitClient(),
) : WorkspaceGitHistoryReader {
    override fun read(worktreePath: Path, baselineRef: String?): List<WorkspaceGitCommit> {
        val revision = baselineRef?.let { configured ->
            val ref = RemoteBranchRef.parse(configured)
            val qualified = "refs/remotes/$ref"
            val found = git.readOnly(worktreePath, "rev-parse", "--verify", "--quiet", "$qualified^{commit}", check = false)
            require(found.succeeded) { "本地不存在主分支 $ref；请检查任务快照或更新仓库的远端跟踪分支" }
            "$qualified..HEAD"
        } ?: "HEAD"
        val result = git.readOnly(
            worktreePath,
            "log",
            revision,
            "--format=%H%x00%cI%x00%cn%x00%ce%x00%an%x00%ae%x00%B%x00",
            check = false,
        )
        if (result.succeeded) return WorkspaceGitCommitLogParser.parse(result.stdout)
        if (result.isEmptyHistoryFailure()) return emptyList()
        throw GitException("Git 提交历史读取失败", result)
    }

    private fun CommandResult.isEmptyHistoryFailure(): Boolean {
        val diagnostic = "$stderr\n$stdout".lowercase()
        return "does not have any commits yet" in diagnostic ||
            ("ambiguous argument 'head'" in diagnostic &&
                "unknown revision" in diagnostic &&
                "or path not in the working tree" in diagnostic)
    }
}

object WorkspaceGitCommitLogParser {
    private const val FIELD_SEPARATOR = '\u0000'

    fun parse(output: String): List<WorkspaceGitCommit> {
        if (output.isBlank()) return emptyList()
        val fields = output.split(FIELD_SEPARATOR)
        val commits = mutableListOf<WorkspaceGitCommit>()
        var index = 0
        while (index + 6 < fields.size) {
            val fullHash = fields[index].trim('\r', '\n')
            if (fullHash.isBlank()) {
                index++
                continue
            }
            val timestamp = fields[index + 1].trim('\r', '\n')
            require(timestamp.isNotBlank()) { "Git 提交历史缺少提交时间：$fullHash" }
            val committerName = fields[index + 2].trim('\r', '\n')
            val committerEmail = fields[index + 3].trim('\r', '\n')
            val authorName = fields[index + 4].trim('\r', '\n')
            val authorEmail = fields[index + 5].trim('\r', '\n')
            val message = fields[index + 6].trimEnd('\r', '\n')
            commits += WorkspaceGitCommit(
                shortHash = fullHash.take(7),
                fullHash = fullHash,
                committedAt = parseTimestamp(timestamp, fullHash),
                message = message,
                authorName = authorName,
                authorEmail = authorEmail,
                committerName = committerName,
                committerEmail = committerEmail,
            )
            index += 7
        }
        return commits
    }

    private fun parseTimestamp(timestamp: String, shortHash: String): Instant = runCatching {
        OffsetDateTime.parse(timestamp).toInstant()
    }.getOrElse { error ->
        throw IllegalArgumentException("无法解析提交时间：$shortHash $timestamp", error)
    }
}
