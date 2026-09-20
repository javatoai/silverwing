package com.snowball.silverwing.core

import java.nio.file.Path

interface TaskOperationLock {
    fun <T> withLock(taskDirectory: Path, block: () -> T): T
}

class FileTaskOperationLock(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
) : TaskOperationLock {
    override fun <T> withLock(taskDirectory: Path, block: () -> T): T = FileLocking.withExclusiveLock(
        paths.locks.resolve("task-${FileLocking.stablePathHash(taskDirectory)}.lock"),
        "任务正在被另一个操作修改：$taskDirectory",
        block,
    )
}

object NoOpTaskOperationLock : TaskOperationLock {
    override fun <T> withLock(taskDirectory: Path, block: () -> T): T = block()
}
