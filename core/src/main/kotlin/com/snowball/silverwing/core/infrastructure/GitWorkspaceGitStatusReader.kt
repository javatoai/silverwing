package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.CancellationException

/** Reads only local Git metadata; it deliberately never fetches or contacts a remote. */
class GitWorkspaceGitStatusReader(
    private val git: GitClient = GitClient(),
) : WorkspaceGitStatusReader {
    override fun read(workspace: ServiceWorkspace): WorkspaceGitHealth {
        val worktreePath = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
        if (!Files.isDirectory(worktreePath)) return WorkspaceGitHealth(
            state = WorkspaceGitHealthState.MISSING,
            issue = WorkspaceGitIssue.MISSING,
            expectedBranch = workspace.branch,
            message = "工作区不存在：$worktreePath",
        )
        return runCatching {
            val repositoryProbe = runCatching {
                git.readOnly(
                    worktreePath,
                    "rev-parse",
                    "--show-toplevel",
                    "--absolute-git-dir",
                    "--path-format=absolute",
                    "--git-common-dir",
                )
            }.getOrElse { error ->
                rethrowCancellation(error)
                return WorkspaceGitHealth(
                    state = WorkspaceGitHealthState.FAILED,
                    issue = WorkspaceGitIssue.NOT_GIT,
                    expectedBranch = workspace.branch,
                    message = "不是有效的 Git 工作区：${error.message ?: worktreePath}",
                )
            }
            val repositoryLines = repositoryProbe.stdout.lineSequence().filter(String::isNotBlank).toList()
            require(repositoryLines.size >= 3) { "Git repository probe returned incomplete output: $worktreePath" }
            val topLevel = Path.of(repositoryLines[0]).toAbsolutePath().normalize()
            val gitDirectory = Path.of(repositoryLines[1]).toAbsolutePath().normalize()
            val commonDirectory = Path.of(repositoryLines[2]).toAbsolutePath().normalize()
            if (topLevel.canonicalOrNormalized() != worktreePath.canonicalOrNormalized()) return WorkspaceGitHealth(
                state = WorkspaceGitHealthState.FAILED,
                issue = WorkspaceGitIssue.NOT_GIT,
                expectedBranch = workspace.branch,
                message = "目标目录不是 Git 顶层目录：$worktreePath",
            )
            val identityMatches = when (workspace.strategy) {
                WorkspaceStrategy.STANDARD_WORKTREE -> {
                    val repository = Path.of(workspace.repositoryPath).toAbsolutePath().normalize()
                    val expectedCommonDirectory = git.readOnly(
                        repository,
                        "rev-parse",
                        "--path-format=absolute",
                        "--git-common-dir",
                    ).stdout.trim().let(Path::of).toAbsolutePath().normalize()
                    commonDirectory.canonicalOrNormalized() == expectedCommonDirectory.canonicalOrNormalized()
                }
                WorkspaceStrategy.INDEPENDENT_CLONE ->
                    git.readOnly(
                        worktreePath,
                        "config",
                        "--get",
                        "remote.${workspace.pushRemote}.url",
                        check = false,
                    ).takeIf { it.succeeded }?.stdout?.trim() == workspace.originUrl?.trim()
            }
            if (!identityMatches) return WorkspaceGitHealth(
                state = WorkspaceGitHealthState.FAILED,
                issue = WorkspaceGitIssue.IDENTITY_MISMATCH,
                expectedBranch = workspace.branch,
                message = "Git 仓库身份与任务记录不匹配：$worktreePath",
            )
            val operationInProgress = operationInProgress(gitDirectory)
            if (operationInProgress != null) return WorkspaceGitHealth(
                state = WorkspaceGitHealthState.FAILED,
                issue = WorkspaceGitIssue.OPERATION_IN_PROGRESS,
                expectedBranch = workspace.branch,
                message = "存在进行中的 Git 操作：$operationInProgress",
            )
            val status = git.readOnly(worktreePath, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
            val parsedStatus = PorcelainV2Parser.parse(status.stdout)
            val actualBranch = parsedStatus.branch
            if (actualBranch == null || actualBranch == "(detached)") return WorkspaceGitHealth(
                state = WorkspaceGitHealthState.FAILED,
                issue = WorkspaceGitIssue.DETACHED_HEAD,
                expectedBranch = workspace.branch,
                message = "工作区处于 Detached HEAD，期望分支 ${workspace.branch}",
            )
            if (actualBranch != workspace.branch) return WorkspaceGitHealth(
                state = WorkspaceGitHealthState.FAILED,
                issue = WorkspaceGitIssue.BRANCH_MISMATCH,
                actualBranch = actualBranch,
                expectedBranch = workspace.branch,
                message = "分支不一致：当前 $actualBranch，期望 ${workspace.branch}",
            )
            val dirtyFiles = parsedStatus.changedFiles
            val dirty = dirtyFiles.size
            if (parsedStatus.upstream == null) {
                WorkspaceGitHealth(
                    state = WorkspaceGitHealthState.READY,
                    dirtyFileCount = dirty,
                    pushState = LocalPushState.NO_UPSTREAM,
                    actualBranch = actualBranch,
                    expectedBranch = workspace.branch,
                    dirtyFiles = dirtyFiles,
                )
            } else if (parsedStatus.ahead == null) {
                WorkspaceGitHealth(
                    state = WorkspaceGitHealthState.READY,
                    dirtyFileCount = dirty,
                    pushState = LocalPushState.REMOTE_BRANCH_MISSING,
                    actualBranch = actualBranch,
                    expectedBranch = workspace.branch,
                    dirtyFiles = dirtyFiles,
                )
            } else {
                val ahead = parsedStatus.ahead
                WorkspaceGitHealth(
                    state = WorkspaceGitHealthState.READY,
                    dirtyFileCount = dirty,
                    pushState = if (ahead == 0) LocalPushState.PUSHED else LocalPushState.AHEAD,
                    unpushedCommitCount = ahead,
                    actualBranch = actualBranch,
                    expectedBranch = workspace.branch,
                    dirtyFiles = dirtyFiles,
                )
            }
        }.getOrElse { error ->
            rethrowCancellation(error)
            WorkspaceGitHealth(
                WorkspaceGitHealthState.FAILED,
                pushState = LocalPushState.FAILED,
                message = error.message ?: "Git 状态检查失败",
                issue = WorkspaceGitIssue.INSPECTION_FAILED,
                expectedBranch = workspace.branch,
            )
        }
    }

    private fun rethrowCancellation(error: Throwable) {
        when (error) {
            is CancellationException -> throw error
            is InterruptedException -> throw error
        }
    }

    private fun operationInProgress(gitDirectory: Path): String? = when {
        Files.exists(gitDirectory.resolve("MERGE_HEAD")) -> "merge"
        Files.exists(gitDirectory.resolve("rebase-merge")) -> "rebase"
        Files.exists(gitDirectory.resolve("rebase-apply")) -> "rebase"
        Files.exists(gitDirectory.resolve("CHERRY_PICK_HEAD")) -> "cherry-pick"
        Files.exists(gitDirectory.resolve("REVERT_HEAD")) -> "revert"
        Files.exists(gitDirectory.resolve("BISECT_LOG")) -> "bisect"
        Files.exists(gitDirectory.resolve("BISECT_START")) -> "bisect"
        else -> null
    }
}

/** Parses NUL-delimited porcelain-v2 output and reports one change per destination path. */
object PorcelainV2Parser {
    data class Status(
        val branch: String?,
        val upstream: String?,
        val ahead: Int?,
        val behind: Int?,
        val changedFiles: List<WorkspaceGitFileChange>,
    ) {
        val changedPaths: Set<String>
            get() = changedFiles.mapTo(linkedSetOf(), WorkspaceGitFileChange::path)
    }

    fun changedPaths(output: String): Set<String> = parse(output).changedPaths

    fun changedFiles(output: String): List<WorkspaceGitFileChange> = parse(output).changedFiles

    fun parse(output: String): Status {
        val fields = output.split('\u0000')
        val files = linkedMapOf<String, WorkspaceGitFileChange>()
        var branch: String? = null
        var upstream: String? = null
        var ahead: Int? = null
        var behind: Int? = null
        var index = 0
        while (index < fields.size) {
            val record = fields[index]
            when {
                record.startsWith("# branch.head ") -> branch = record.removePrefix("# branch.head ").ifBlank { null }
                record.startsWith("# branch.upstream ") -> upstream = record.removePrefix("# branch.upstream ").ifBlank { null }
                record.startsWith("# branch.ab ") -> {
                    val match = Regex("\\+(\\d+) -(\\d+)").matchEntire(record.removePrefix("# branch.ab "))
                    ahead = match?.groupValues?.get(1)?.toIntOrNull()
                    behind = match?.groupValues?.get(2)?.toIntOrNull()
                }
                record.startsWith("1 ") -> changedFile(record, '1', 8)?.let { files[it.path] = it }
                record.startsWith("2 ") -> {
                    changedFile(record, '2', 9)?.let { files[it.path] = it }
                    index++
                }
                record.startsWith("u ") -> changedFile(record, 'u', 10)?.let { files[it.path] = it }
                record.startsWith("? ") -> record.removePrefix("? ").takeIf(String::isNotEmpty)?.let {
                    files[it] = WorkspaceGitFileChange(it, WorkspaceGitFileChangeKind.UNTRACKED)
                }
            }
            index++
        }
        return Status(branch, upstream, ahead, behind, files.values.toList())
    }

    private fun changedFile(record: String, recordType: Char, fieldCount: Int): WorkspaceGitFileChange? {
        val path = pathAfterFields(record, fieldCount) ?: return null
        val xy = record.split(' ', limit = 3).getOrNull(1) ?: return null
        return WorkspaceGitFileChange(path, changeKind(recordType, xy))
    }

    private fun changeKind(recordType: Char, xy: String): WorkspaceGitFileChangeKind {
        if (recordType == 'u' || xy.any { it == 'U' }) return WorkspaceGitFileChangeKind.CONFLICTED
        if (recordType == '2') {
            return when {
                xy.any { it == 'C' } -> WorkspaceGitFileChangeKind.COPIED
                xy.any { it == 'R' } -> WorkspaceGitFileChangeKind.RENAMED
                else -> WorkspaceGitFileChangeKind.MODIFIED
            }
        }
        return when {
            xy.any { it == 'D' } -> WorkspaceGitFileChangeKind.DELETED
            xy.any { it == 'A' } -> WorkspaceGitFileChangeKind.ADDED
            xy.any { it == 'T' } -> WorkspaceGitFileChangeKind.TYPE_CHANGED
            xy.any { it == 'R' } -> WorkspaceGitFileChangeKind.RENAMED
            xy.any { it == 'C' } -> WorkspaceGitFileChangeKind.COPIED
            else -> WorkspaceGitFileChangeKind.MODIFIED
        }
    }

    private fun pathAfterFields(record: String, fieldCount: Int): String? =
        record.split(' ', limit = fieldCount + 1).getOrNull(fieldCount)?.takeIf(String::isNotEmpty)
}
