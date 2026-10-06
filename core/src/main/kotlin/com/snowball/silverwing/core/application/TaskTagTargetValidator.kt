package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Duration

/** Checks the selected remote without fetching or changing any local Git state. */
fun interface TaskTagTargetValidator {
    fun validate(repository: Path, target: RemoteBranchRef)
}

class GitTaskTagTargetValidator(
    private val git: GitClient = GitClient(),
    private val branchValidator: BranchReferenceValidator = GitBranchReferenceValidator(),
) : TaskTagTargetValidator {
    override fun validate(repository: Path, target: RemoteBranchRef) {
        require(!target.remote.startsWith('-')) { "测试目标远程名称不能以 - 开头：${target.remote}" }
        require(branchValidator.isValid(target.branch)) { "测试目标分支格式不合法：${target.branch}" }
        require(target.remote in git.remoteNames(repository)) { "仓库中不存在远程：${target.remote}" }
        val ref = "refs/heads/${target.branch}"
        val result = git.readOnly(
            repository, "ls-remote", "--heads", target.remote, ref,
            timeout = Duration.ofSeconds(20),
        )
        require(result.stdout.lineSequence().any { it.substringAfter('\t', "").trim() == ref }) {
            "远程分支不存在：$target"
        }
    }
}
