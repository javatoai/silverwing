package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Duration
import kotlin.io.path.Path
import kotlin.io.path.createDirectories

class GitException(
    message: String,
    val result: CommandResult,
) : RuntimeException("$message\n${result.stderr.ifBlank { result.stdout }}")

data class WorktreeRecord(
    val path: Path,
    val head: String?,
    val branch: String?,
    val bare: Boolean = false,
    val detached: Boolean = false,
    val locked: Boolean = false,
)

data class RepositoryStatus(
    val staged: Boolean,
    val unstaged: Boolean,
    val untracked: Boolean,
    val unpushedCommits: Int,
    val operationInProgress: String?,
) {
    val safeToArchive: Boolean
        get() = !staged && !unstaged && !untracked && unpushedCommits == 0 && operationInProgress == null

    /** Uncommitted work or in-progress git operation; unpushed commits alone do not count. */
    val hasUncommittedChanges: Boolean
        get() = staged || unstaged || untracked || operationInProgress != null
}

class GitClient(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val executable: GitExecutable = GitExecutable.pathFallback(),
) {
    fun run(
        repository: Path,
        vararg arguments: String,
        timeout: Duration = Duration.ofMinutes(10),
        check: Boolean = true,
    ): CommandResult {
        val result = runner.run(
            listOf(executable.resolve(), "-c", "core.longpaths=true", "-C", repository.toString()) + arguments,
            timeout = timeout,
        )
        if (check && !result.succeeded) {
            throw GitException("Git 命令失败：git ${arguments.joinToString(" ")}", result)
        }
        return result
    }

    /** Runs a local, read-only Git probe without taking optional repository locks. */
    fun readOnly(
        repository: Path,
        vararg arguments: String,
        timeout: Duration = Duration.ofMinutes(2),
        check: Boolean = true,
    ): CommandResult {
        val result = runner.run(
            listOf(executable.resolve(), "--no-optional-locks", "-c", "core.longpaths=true", "-C", repository.toString()) + arguments,
            timeout = timeout,
        )
        if (check && !result.succeeded) {
            throw GitException("Git read-only command failed: git ${arguments.joinToString(" ")}", result)
        }
        return result
    }

    fun commonDirectory(repository: Path): Path {
        val output = run(repository, "rev-parse", "--path-format=absolute", "--git-common-dir").stdout.trim()
        return Path(output).toAbsolutePath().normalize()
    }

    fun topLevel(repository: Path): Path =
        Path(run(repository, "rev-parse", "--show-toplevel").stdout.trim()).toAbsolutePath().normalize()

    fun currentBranch(repository: Path): String? =
        run(repository, "branch", "--show-current").stdout.trim().ifBlank { null }

    fun remoteUrl(repository: Path, remote: String = "origin"): String? =
        run(repository, "remote", "get-url", remote, check = false)
            .takeIf { it.succeeded }
            ?.stdout
            ?.trim()
            ?.ifBlank { null }

    fun remoteNames(repository: Path): List<String> =
        run(repository, "remote").stdout.lineSequence().map { it.trim() }.filter { it.isNotEmpty() }.toList()

    fun addRemote(repository: Path, name: String, url: String) {
        require(name.isNotBlank() && '/' !in name && name != "." && name != "..") { "远程名称不合法：$name" }
        require(url.isNotBlank()) { "远程 URL 不能为空" }
        run(repository, "remote", "add", name, url)
    }

    fun remoteDefaultBranch(repository: Path, remote: String = "origin"): String? {
        val symbolic = run(
            repository,
            "symbolic-ref",
            "--quiet",
            "--short",
            "refs/remotes/$remote/HEAD",
            check = false,
        )
        return symbolic.takeIf { it.succeeded }
            ?.stdout
            ?.trim()
            ?.removePrefix("$remote/")
            ?.ifBlank { null }
    }

    /** Performs a full clone and checks out the requested remote branch. */
    fun cloneRepository(originUrl: String, target: Path, branch: String) {
        target.parent?.createDirectories()
        val result = runner.run(
            listOf(
                executable.resolve(),
                "-c",
                "core.longpaths=true",
                "clone",
                "--branch",
                branch,
                originUrl,
                target.toString(),
            ),
            timeout = Duration.ofMinutes(10),
        )
        if (!result.succeeded) {
            throw GitException("Git 克隆失败：$originUrl#$branch", result)
        }
    }

    fun version(timeout: Duration = Duration.ofSeconds(10)): CommandResult =
        runner.run(listOf(executable.resolve(), "--version"), timeout = timeout)

    fun worktrees(repository: Path): List<WorktreeRecord> {
        val records = mutableListOf<WorktreeRecord>()
        var path: Path? = null
        var head: String? = null
        var branch: String? = null
        var bare = false
        var detached = false
        var locked = false

        fun flush() {
            val value = path ?: return
            records += WorktreeRecord(value, head, branch, bare, detached, locked)
            path = null
            head = null
            branch = null
            bare = false
            detached = false
            locked = false
        }

        run(repository, "worktree", "list", "--porcelain").stdout.lineSequence().forEach { line ->
            when {
                line.isBlank() -> flush()
                line.startsWith("worktree ") -> {
                    flush()
                    path = Path(line.removePrefix("worktree ")).toAbsolutePath().normalize()
                }
                line.startsWith("HEAD ") -> head = line.removePrefix("HEAD ")
                line.startsWith("branch ") -> branch = line.removePrefix("branch ").removePrefix("refs/heads/")
                line == "bare" -> bare = true
                line == "detached" -> detached = true
                line == "locked" || line.startsWith("locked ") -> locked = true
            }
        }
        flush()
        return records
    }

    /** Removes only Git's stale linked-worktree administrative entries. */
    fun pruneWorktrees(repository: Path) {
        run(repository, "worktree", "prune")
    }

    fun refExists(repository: Path, ref: String): Boolean =
        run(repository, "show-ref", "--verify", "--quiet", ref, check = false).succeeded

    fun resolve(repository: Path, ref: String): String =
        run(repository, "rev-parse", "--verify", "$ref^{commit}").stdout.trim()

    fun isAncestor(repository: Path, ancestor: String, descendant: String): Boolean =
        run(repository, "merge-base", "--is-ancestor", ancestor, descendant, check = false).succeeded

    fun fetch(repository: Path, remote: String = "origin") {
        // Branch refresh must not fetch tags. A conflicting local tag is unrelated
        // to task provisioning and must never make ordinary workspace creation fail.
        run(repository, "fetch", "--prune", "--no-tags", remote, timeout = Duration.ofMinutes(5))
    }

    /**
     * Synchronizes local Tag refs from the remote as the authoritative source.
     *
     * This may overwrite conflicting local Tag refs and remove local-only tags,
     * but it never changes a remote ref or any branch/worktree state.
     */
    fun fetchTags(repository: Path, remote: String = "origin") {
        run(
            repository,
            "fetch",
            "--tags",
            "--prune",
            "--prune-tags",
            "--force",
            remote,
            timeout = Duration.ofMinutes(5),
        )
    }

    fun addWorktree(
        repository: Path,
        target: Path,
        branch: String,
        baseRef: String,
        trackingRemote: String = "origin",
    ) {
        var createdByThisCall = false
        try {
            run(
                repository,
                "-c",
                "core.symlinks=false",
                "worktree",
                "add",
                "-b",
                branch,
                "--no-track",
                target.toString(),
                baseRef,
                timeout = Duration.ofMinutes(5),
            )
            createdByThisCall = true
            run(repository, "config", "branch.$branch.remote", trackingRemote)
            run(repository, "config", "branch.$branch.merge", "refs/heads/$branch")
        } catch (error: Throwable) {
            // A failure after `worktree add` must not leave an invisible partial
            // checkout that prevents the same task from being retried.
            if (createdByThisCall) {
                runCatching { removeWorktree(repository, target, force = true) }
                run(repository, "branch", "-D", branch, check = false)
                run(repository, "worktree", "prune", check = false)
            }
            throw error
        }
    }

    fun addExistingWorktree(repository: Path, target: Path, branch: String, force: Boolean = false) {
        val args = buildList {
            add("-c")
            add("core.symlinks=false")
            add("worktree")
            add("add")
            if (force) add("--force")
            add(target.toString())
            add(branch)
        }
        run(repository, *args.toTypedArray(), timeout = Duration.ofMinutes(5))
    }

    /** Creates the missing local branch from an already fetched remote feature branch. */
    fun addTrackedRemoteWorktree(repository: Path, target: Path, branch: String, remote: String) {
        var createdByThisCall = false
        try {
            run(
                repository,
                "-c",
                "core.symlinks=false",
                "worktree",
                "add",
                "-b",
                branch,
                "--track",
                target.toString(),
                "$remote/$branch",
                timeout = Duration.ofMinutes(5),
            )
            createdByThisCall = true
        } catch (error: Throwable) {
            if (createdByThisCall) {
                runCatching { removeWorktree(repository, target, force = true) }
                run(repository, "branch", "-D", branch, check = false)
                run(repository, "worktree", "prune", check = false)
            }
            throw error
        }
    }

    fun setBranchUpstream(repository: Path, branch: String, remote: String) {
        run(repository, "config", "branch.$branch.remote", remote)
        run(repository, "config", "branch.$branch.merge", "refs/heads/$branch")
    }

    fun addDetachedWorktree(repository: Path, target: Path, ref: String) {
        run(
            repository,
            "-c",
            "core.symlinks=false",
            "worktree",
            "add",
            "--detach",
            target.toString(),
            ref,
            timeout = Duration.ofMinutes(5),
        )
    }

    fun removeWorktree(repository: Path, target: Path, force: Boolean = false) {
        val args = buildList {
            add("worktree")
            add("remove")
            if (force) add("--force")
            add(target.toString())
        }
        run(repository, *args.toTypedArray(), timeout = Duration.ofMinutes(5))
    }

    fun moveWorktree(repository: Path, source: Path, target: Path) {
        run(repository, "worktree", "move", source.toString(), target.toString(), timeout = Duration.ofMinutes(5))
    }

    fun repairWorktree(repository: Path, target: Path) {
        run(repository, "worktree", "repair", target.toString(), timeout = Duration.ofMinutes(5))
    }

    fun status(repository: Path): RepositoryStatus {
        val lines = run(repository, "status", "--porcelain=v1", "--untracked-files=all").stdout.lines()
        val staged = lines.any { it.length >= 2 && it[0] != ' ' && it[0] != '?' }
        val unstaged = lines.any { it.length >= 2 && it[1] != ' ' && it[1] != '?' }
        val untracked = lines.any { it.startsWith("??") }
        val upstream = run(
            repository,
            "rev-parse",
            "--abbrev-ref",
            "--symbolic-full-name",
            "@{upstream}",
            check = false,
        )
        val unpushed = if (upstream.succeeded) {
            run(repository, "rev-list", "--count", "@{upstream}..HEAD").stdout.trim().toIntOrNull() ?: 0
        } else {
            run(repository, "rev-list", "--count", "HEAD", "--not", "--remotes").stdout
                .trim()
                .toIntOrNull() ?: 0
        }
        return RepositoryStatus(
            staged = staged,
            unstaged = unstaged,
            untracked = untracked,
            unpushedCommits = unpushed,
            operationInProgress = operationInProgress(repository),
        )
    }

    /** Counts commits on any local branch that are not reachable from any remote ref. */
    fun localOnlyCommitCount(repository: Path): Int = run(
        repository,
        "rev-list",
        "--count",
        "HEAD",
        "--all",
        "--not",
        "--remotes",
    ).stdout.trim().toIntOrNull()
        ?: error("无法解析本地未推送提交数量：$repository")

    private fun operationInProgress(repository: Path): String? {
        val gitDirectory = Path(run(repository, "rev-parse", "--absolute-git-dir").stdout.trim())
        return when {
            gitDirectory.resolve("MERGE_HEAD").toFile().exists() -> "merge"
            gitDirectory.resolve("rebase-merge").toFile().exists() -> "rebase"
            gitDirectory.resolve("rebase-apply").toFile().exists() -> "rebase"
            gitDirectory.resolve("CHERRY_PICK_HEAD").toFile().exists() -> "cherry-pick"
            gitDirectory.resolve("REVERT_HEAD").toFile().exists() -> "revert"
            gitDirectory.resolve("BISECT_LOG").toFile().exists() -> "bisect"
            gitDirectory.resolve("BISECT_START").toFile().exists() -> "bisect"
            else -> null
        }
    }
}
