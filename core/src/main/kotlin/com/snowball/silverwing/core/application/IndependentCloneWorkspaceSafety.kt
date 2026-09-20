package com.snowball.silverwing.core

import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.StandardCopyOption
import java.nio.file.attribute.BasicFileAttributes
import java.security.MessageDigest
import kotlin.io.path.exists

internal object IndependentCloneWorkspaceSafety {
    fun ownership(taskDirectory: Path, repositoryId: String, serviceId: String, moduleId: String): String {
        val identity = listOf(
            taskDirectory.toAbsolutePath().normalize().toString(), repositoryId, serviceId, moduleId,
        ).joinToString("\u0000")
        return MessageDigest.getInstance("SHA-256").digest(identity.toByteArray())
            .joinToString("") { "%02x".format(it) }
    }

    fun cloneIntoPlace(
        taskDirectory: Path,
        target: Path,
        ownership: String,
        clone: (Path) -> Unit,
    ) {
        val task = taskDirectory.toAbsolutePath().normalize()
        val normalizedTarget = target.toAbsolutePath().normalize()
        require(normalizedTarget.parent == task) { "独立克隆必须位于任务目录的直接子级" }
        Files.createDirectories(task)
        require(!normalizedTarget.exists()) { "目标目录已存在：$normalizedTarget" }
        val stagingRoot = Files.createTempDirectory(task, ".silverwing-clone-")
        val stagingOwner = stagingRoot.resolve(".silverwing-owner")
        Files.writeString(stagingOwner, ownership)
        val staging = stagingRoot.resolve("workspace")
        try {
            clone(staging)
            Files.writeString(marker(staging), ownership)
            try {
                Files.move(staging, normalizedTarget, StandardCopyOption.ATOMIC_MOVE)
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(staging, normalizedTarget)
            }
            Files.deleteIfExists(stagingOwner)
            Files.deleteIfExists(stagingRoot)
        } catch (error: Throwable) {
            runCatching { deleteOwned(task, normalizedTarget, ownership) }.onFailure(error::addSuppressed)
            runCatching { deleteStaging(task, stagingRoot, stagingOwner, ownership) }.onFailure(error::addSuppressed)
            throw error
        }
    }

    fun deleteOwned(taskDirectory: Path, target: Path, ownership: String) {
        val task = taskDirectory.toAbsolutePath().normalize()
        val normalized = target.toAbsolutePath().normalize()
        require(normalized.parent == task) { "拒绝删除任务目录之外的独立克隆：$normalized" }
        if (!normalized.exists()) return
        requireOwned(normalized, ownership)
        deleteRecursively(normalized)
    }

    fun requireOwned(target: Path, ownership: String) {
        val normalized = target.toAbsolutePath().normalize()
        val owner = marker(normalized)
        require(Files.isRegularFile(owner) && Files.readString(owner) == ownership) {
            "独立克隆所有权标记缺失或已变化：$normalized"
        }
    }

    /** Rebinds an already owned clone without granting ownership to an unmarked directory. */
    fun reassignOwnership(target: Path, expectedOwnership: String, ownership: String) {
        requireOwned(target, expectedOwnership)
        AtomicFileWriter.write(marker(target), ownership)
    }

    private fun marker(clone: Path): Path = clone.resolve(".git").resolve("silverwing-owner")

    private fun deleteStaging(task: Path, root: Path, marker: Path, ownership: String) {
        require(root.parent == task && root.fileName.toString().startsWith(".silverwing-clone-")) { "拒绝清理未知临时目录：$root" }
        if (!root.exists()) return
        require(Files.isRegularFile(marker) && Files.readString(marker) == ownership) { "临时克隆所有权已变化：$root" }
        deleteRecursively(root)
    }

    private fun deleteRecursively(path: Path) {
        Files.walkFileTree(path, object : SimpleFileVisitor<Path>() {
            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                deleteWritable(file)
                return FileVisitResult.CONTINUE
            }

            override fun postVisitDirectory(dir: Path, exc: java.io.IOException?): FileVisitResult {
                if (exc != null) throw exc
                deleteWritable(dir)
                return FileVisitResult.CONTINUE
            }
        })
    }

    private fun deleteWritable(path: Path) {
        try {
            Files.deleteIfExists(path)
        } catch (denied: java.nio.file.AccessDeniedException) {
            path.toFile().setWritable(true)
            try {
                Files.deleteIfExists(path)
            } catch (retry: Throwable) {
                retry.addSuppressed(denied)
                throw retry
            }
        }
    }
}
