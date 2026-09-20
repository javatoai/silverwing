package com.snowball.silverwing.core

/** Delegates branch grammar to Git itself so UI and provisioning share Git's exact rules. */
class GitBranchReferenceValidator(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val gitExecutable: GitExecutable = GitExecutable.pathFallback(),
) : BranchReferenceValidator {
    override fun isValid(branch: String): Boolean = runCatching {
        runner.run(listOf(gitExecutable.resolve(), "check-ref-format", "--branch", branch)).succeeded
    }.getOrDefault(false)
}
