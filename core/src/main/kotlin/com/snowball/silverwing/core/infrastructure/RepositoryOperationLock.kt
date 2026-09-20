package com.snowball.silverwing.core

import java.nio.file.Path

class RepositoryOperationLock(private val paths: ApplicationPaths = ApplicationPaths.systemDefault()) {
    fun <T> withLock(repository: Path, block: () -> T): T = FileLocking.withExclusiveLock(
        paths.locks.resolve("repository-${FileLocking.stablePathHash(repository)}.lock"),
        "仓库正在被另一个工作区操作占用：$repository",
        block,
    )
}
