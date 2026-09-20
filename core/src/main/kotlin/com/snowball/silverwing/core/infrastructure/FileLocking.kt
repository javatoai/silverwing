package com.snowball.silverwing.core

import java.nio.channels.FileChannel
import java.nio.channels.OverlappingFileLockException
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import java.security.MessageDigest
import java.util.Locale
import kotlin.io.path.createDirectories

internal object FileLocking {
    fun <T> withExclusiveLock(lockPath: Path, failureMessage: String, block: () -> T): T {
        lockPath.parent.createDirectories()
        FileChannel.open(
            lockPath,
            StandardOpenOption.CREATE,
            StandardOpenOption.WRITE,
        ).use { channel ->
            val lock = try {
                channel.tryLock()
            } catch (_: OverlappingFileLockException) {
                null
            } ?: throw IllegalStateException(failureMessage)
            lock.use { return block() }
        }
    }

    fun <T> withExclusiveLockWaiting(
        lockPath: Path,
        block: () -> T,
    ): T {
        lockPath.parent.createDirectories()
        var interrupted = false
        try {
            FileChannel.open(
                lockPath,
                StandardOpenOption.CREATE,
                StandardOpenOption.WRITE,
            ).use { channel ->
                while (true) {
                    val lock = try {
                        channel.tryLock()
                    } catch (_: OverlappingFileLockException) {
                        null
                    }
                    if (lock != null) lock.use { return block() }
                    try {
                        Thread.sleep(50)
                    } catch (_: InterruptedException) {
                        // An accepted Build click must still be reconciled after the active writer.
                        // Preserve cancellation for the caller without losing its build/audit record.
                        interrupted = true
                    }
                }
            }
        } finally {
            if (interrupted) Thread.currentThread().interrupt()
        }
    }

    fun stablePathHash(path: Path, length: Int = 16): String =
        stableTextHash(stablePathIdentity(path), length)

    /**
     * Lock names must identify the filesystem object rather than the spelling
     * used by the caller. This matters for macOS `/var` aliases, Windows
     * junctions, and task paths that are resolved through a symlink. The
     * lowercased identity intentionally keeps the historical conservative
     * behavior on filesystems whose case sensitivity is unknown.
     */
    private fun stablePathIdentity(path: Path): String {
        val normalized = path.toAbsolutePath().normalize()
        var existing = normalized
        while (!Files.exists(existing) && existing.parent != null) {
            existing = existing.parent
        }
        val realExisting = runCatching { existing.toRealPath() }.getOrNull()
        val identity = if (realExisting == null) {
            normalized.toString()
        } else if (existing == normalized) {
            realExisting.toString()
        } else {
            realExisting.resolve(existing.relativize(normalized)).normalize().toString()
        }
        return identity.lowercase(Locale.ROOT)
    }

    fun stableTextHash(value: String, length: Int = 16): String =
        MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(StandardCharsets.UTF_8))
            .take(length / 2)
            .joinToString("") { "%02x".format(Locale.ROOT, it) }
}
