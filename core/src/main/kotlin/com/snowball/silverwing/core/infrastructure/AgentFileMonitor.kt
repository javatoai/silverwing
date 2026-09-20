package com.snowball.silverwing.core

import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.ClosedWatchServiceException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardWatchEventKinds
import java.nio.file.WatchService
import java.security.MessageDigest
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

enum class AgentConflictResolution {
    USE_DISK,
    USE_LOCAL,
}

class AgentDocumentConflictException(val change: AgentFileChange.Conflict) :
    IllegalStateException("Agent 文件已被外部修改，请先选择磁盘版本或本地版本")

data class AgentEditorSnapshot(
    val path: Path,
    val content: String,
    val dirty: Boolean,
    val diskHash: String,
)

sealed interface AgentFileChange {
    val path: Path

    data class Reloaded(
        override val path: Path,
        val content: String,
    ) : AgentFileChange

    data class Conflict(
        override val path: Path,
        val diskContent: String,
        val localContent: String,
    ) : AgentFileChange
}

/**
 * Watches authoritative Agent files while retaining a hash-based focus fallback.
 * Hashes are compared even when mtimes match because editors and network drives can
 * coalesce timestamps. Dirty buffers are never overwritten without a resolution.
 */
class AgentFileMonitor(
    private val onChange: (AgentFileChange) -> Unit,
    private val debounceMillis: Long = 250,
    startWatchThread: Boolean = true,
) : AutoCloseable {
    private data class State(
        var content: String,
        var dirty: Boolean,
        var diskHash: String,
        var pendingDiskContent: String? = null,
        var pendingDiskHash: String? = null,
    )

    private val watchService: WatchService = Path.of(".").fileSystem.newWatchService()
    private val tracked = linkedMapOf<Path, State>()
    private val registeredDirectories = mutableSetOf<Path>()
    private val scheduler = Executors.newSingleThreadScheduledExecutor { task ->
        Thread(task, "silverwing-agent-debounce").apply { isDaemon = true }
    }
    private val watcher = Executors.newSingleThreadExecutor { task ->
        Thread(task, "silverwing-agent-watch").apply { isDaemon = true }
    }
    private var pendingCheck: ScheduledFuture<*>? = null

    init {
        if (startWatchThread) watcher.submit(::watchLoop)
    }

    @Synchronized
    fun track(path: Path): AgentEditorSnapshot {
        val normalized = path.toAbsolutePath().normalize()
        // Tracking is a read/observe operation. In particular, a task can be
        // deleted while its detail view is still receiving a file-change event;
        // recreating the parent here would leave an empty task directory behind.
        // Writers create their parent explicitly in writeAtomically().
        normalized.parent?.takeIf(Files::isDirectory)?.let(::registerDirectory)
        val disk = readOrEmpty(normalized)
        val state = tracked.getOrPut(normalized) { State(disk, dirty = false, diskHash = hash(disk)) }
        return state.snapshot(normalized)
    }

    @Synchronized
    fun untrack(path: Path) {
        tracked.remove(path.toAbsolutePath().normalize())
    }

    @Synchronized
    fun markLocalEdit(path: Path, content: String) {
        val normalized = path.toAbsolutePath().normalize()
        val state = tracked[normalized] ?: run {
            track(normalized)
            tracked.getValue(normalized)
        }
        state.content = content
        state.dirty = true
    }

    @Synchronized
    fun save(path: Path, content: String) {
        val normalized = path.toAbsolutePath().normalize()
        val state = tracked[normalized] ?: run {
            track(normalized)
            tracked.getValue(normalized)
        }
        val disk = readOrEmpty(normalized)
        val diskHash = hash(disk)
        if (diskHash != state.diskHash || state.pendingDiskHash != null) {
            val change = AgentFileChange.Conflict(normalized, disk, content)
            state.content = content
            state.dirty = true
            state.pendingDiskContent = disk
            state.pendingDiskHash = diskHash
            onChange(change)
            throw AgentDocumentConflictException(change)
        }
        writeAtomically(normalized, content)
        tracked[normalized] = State(content, dirty = false, diskHash = hash(content))
    }

    @Synchronized
    fun resolve(
        path: Path,
        resolution: AgentConflictResolution,
        expectedDiskContent: String? = null,
        writeLocal: ((String) -> Unit)? = null,
    ): AgentEditorSnapshot {
        val normalized = path.toAbsolutePath().normalize()
        val state = tracked[normalized] ?: error("文件未被监控：$normalized")
        when (resolution) {
            AgentConflictResolution.USE_DISK -> {
                val disk = readOrEmpty(normalized)
                state.content = disk
                state.diskHash = hash(disk)
                state.dirty = false
                state.pendingDiskContent = null
                state.pendingDiskHash = null
                onChange(AgentFileChange.Reloaded(normalized, disk))
            }
            AgentConflictResolution.USE_LOCAL -> {
                val latestDisk = readOrEmpty(normalized)
                val latestHash = hash(latestDisk)
                val expectedHash = expectedDiskContent?.let(::hash) ?: state.pendingDiskHash
                if (expectedHash != null && latestHash != expectedHash) {
                    val change = AgentFileChange.Conflict(normalized, latestDisk, state.content)
                    state.pendingDiskContent = latestDisk
                    state.pendingDiskHash = latestHash
                    onChange(change)
                    throw AgentDocumentConflictException(change)
                }
                if (writeLocal == null) {
                    writeAtomically(normalized, state.content)
                } else {
                    // Task documents regenerate their system section under the task
                    // lock, but must pass the same conflict check as other files.
                    writeLocal(state.content)
                    state.content = readOrEmpty(normalized)
                }
                state.diskHash = hash(state.content)
                state.dirty = false
                state.pendingDiskContent = null
                state.pendingDiskHash = null
            }
        }
        return state.snapshot(normalized)
    }

    @Synchronized
    fun snapshot(path: Path): AgentEditorSnapshot? {
        val normalized = path.toAbsolutePath().normalize()
        return tracked[normalized]?.snapshot(normalized)
    }

    /** Call on window focus as a fallback for missed/coalesced WatchService events. */
    @Synchronized
    fun checkNow() {
        tracked.forEach { (path, state) ->
            val disk = readOrEmpty(path)
            val newHash = hash(disk)
            if (newHash == state.diskHash || newHash == state.pendingDiskHash) return@forEach
            if (state.dirty) {
                state.pendingDiskContent = disk
                state.pendingDiskHash = newHash
                onChange(AgentFileChange.Conflict(path, disk, state.content))
            } else {
                state.content = disk
                state.diskHash = newHash
                onChange(AgentFileChange.Reloaded(path, disk))
            }
        }
    }

    private fun watchLoop() {
        try {
            while (!Thread.currentThread().isInterrupted) {
                val key = watchService.take()
                val relevant = key.pollEvents().any { event ->
                    event.kind() != StandardWatchEventKinds.OVERFLOW
                }
                key.reset()
                if (relevant) scheduleCheck()
            }
        } catch (_: ClosedWatchServiceException) {
            // Normal shutdown.
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
    }

    @Synchronized
    private fun scheduleCheck() {
        pendingCheck?.cancel(false)
        pendingCheck = scheduler.schedule(::checkNow, debounceMillis, TimeUnit.MILLISECONDS)
    }

    private fun registerDirectory(directory: Path) {
        if (!registeredDirectories.add(directory)) return
        directory.register(
            watchService,
            StandardWatchEventKinds.ENTRY_CREATE,
            StandardWatchEventKinds.ENTRY_MODIFY,
            StandardWatchEventKinds.ENTRY_DELETE,
        )
    }

    override fun close() {
        watchService.close()
        watcher.shutdownNow()
        scheduler.shutdownNow()
    }

    private fun State.snapshot(path: Path) = AgentEditorSnapshot(path, content, dirty, diskHash)

    private fun readOrEmpty(path: Path): String = if (path.exists()) Files.readString(path) else ""

    private fun hash(content: String): String = MessageDigest.getInstance("SHA-256")
        .digest(content.toByteArray(Charsets.UTF_8))
        .joinToString("") { "%02x".format(it) }

    private fun writeAtomically(target: Path, content: String) {
        target.parent.createDirectories()
        val temporary = Files.createTempFile(target.parent, ".${target.fileName}-", ".tmp")
        Files.writeString(temporary, content)
        try {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
        }
    }
}
