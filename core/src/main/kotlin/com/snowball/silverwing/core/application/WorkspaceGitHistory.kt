package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Instant
import java.time.OffsetDateTime

fun interface WorkspaceGitHistoryReader {
    fun read(worktreePath: Path): List<WorkspaceGitCommit>
}

class WorkspaceGitHistoryService(
    private val reader: WorkspaceGitHistoryReader = GitWorkspaceGitHistoryReader(),
) {
    fun read(worktreePath: Path): List<WorkspaceGitCommit> = reader.read(worktreePath)
}

class GitWorkspaceGitHistoryReader(
    private val git: GitClient = GitClient(),
) : WorkspaceGitHistoryReader {
    override fun read(worktreePath: Path): List<WorkspaceGitCommit> {
        val result = git.readOnly(
            worktreePath,
            "log",
            "HEAD",
            "--abbrev=7",
            "--format=%h%x00%cI%x00%an%x00%B%x00",
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
        while (index + 3 < fields.size) {
            val shortHash = fields[index].trim('\r', '\n')
            if (shortHash.isBlank()) {
                index++
                continue
            }
            val timestamp = fields[index + 1].trim('\r', '\n')
            require(timestamp.isNotBlank()) { "Git 提交历史缺少提交时间：$shortHash" }
            val authorName = fields[index + 2].trim('\r', '\n')
            val message = fields[index + 3].trimEnd('\r', '\n')
            commits += WorkspaceGitCommit(
                shortHash = shortHash.take(7),
                committedAt = parseTimestamp(timestamp, shortHash),
                message = message,
                authorName = authorName,
            )
            index += 4
        }
        return commits
    }

    private fun parseTimestamp(timestamp: String, shortHash: String): Instant = runCatching {
        OffsetDateTime.parse(timestamp).toInstant()
    }.getOrElse { error ->
        throw IllegalArgumentException("无法解析提交时间：$shortHash $timestamp", error)
    }
}
