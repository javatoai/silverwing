package com.snowball.silverwing.core

import java.nio.file.Path

fun interface RepositoryRemoteCatalog {
    fun list(repository: Path): List<String>
}

class GitRepositoryRemoteCatalog(
    private val git: GitClient = GitClient(),
) : RepositoryRemoteCatalog {
    override fun list(repository: Path): List<String> = git.run(repository, "remote").stdout
        .lineSequence()
        .map(String::trim)
        .filter(String::isNotBlank)
        .distinct()
        .sortedWith(String.CASE_INSENSITIVE_ORDER)
        .toList()
        .also { require(it.isNotEmpty()) { "仓库没有可用的 Git 远程" } }
}
