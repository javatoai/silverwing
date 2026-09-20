package com.snowball.silverwing.core

import kotlinx.serialization.json.Json
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.nio.channels.OverlappingFileLockException
import java.nio.charset.StandardCharsets
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.NoSuchFileException
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

/** Result of an optimistic operation update. [changed] is false when the CAS did not match. */
internal data class TagOperationUpdateResult(
    val operation: TagOperation,
    val changed: Boolean,
)

class TagOperationStore(
    private val json: Json = Json {
        prettyPrint = true
        ignoreUnknownKeys = true
        encodeDefaults = true
    },
) {
    fun save(taskDirectory: Path, operation: TagOperation) {
        val directory = taskDirectory.resolve("tag-operations")
        directory.createDirectories()
        withOperationLock(taskDirectory, operation.operationId) {
            saveUnlocked(directory, operation)
        }
    }

    /**
     * Applies a status-only update if the stored operation still matches
     * [expected]. The record lock spans the reload and atomic replacement, so a
     * late Genbu response cannot overwrite a newer build/tag/delete operation.
     * A missing record returns null and is never recreated.
     */
    internal fun updateIfUnchanged(
        taskDirectory: Path,
        expected: TagOperation,
        update: (TagOperation) -> TagOperation,
    ): TagOperationUpdateResult? {
        val directory = taskDirectory.resolve("tag-operations")
        val target = directory.resolve("${expected.operationId}.json")
        // Do this check before opening the lock. The lock helper used for writes
        // creates its parent directory; a late probe must not recreate a task
        // whose operation directory was already deleted.
        if (!Files.isRegularFile(target)) return null
        return withExistingOperationLock<TagOperationUpdateResult?>(taskDirectory, expected.operationId) {
            val current = runCatching { loadUnlocked(target) }.getOrNull()
            when {
                current == null -> null
                current != expected -> TagOperationUpdateResult(current, changed = false)
                else -> {
                    val updated = update(current)
                    if (updated != current) saveUnlocked(directory, updated)
                    TagOperationUpdateResult(updated, changed = updated != current)
                }
            }
        }
    }

    private fun saveUnlocked(directory: Path, operation: TagOperation) {
        val target = directory.resolve("${operation.operationId}.json")
        val temporary = Files.createTempFile(directory, ".${operation.operationId}-", ".json.tmp")
        Files.writeString(temporary, json.encodeToString(operation))
        try {
            Files.move(
                temporary,
                target,
                StandardCopyOption.REPLACE_EXISTING,
                StandardCopyOption.ATOMIC_MOVE,
            )
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
        }
    }

    fun load(taskDirectory: Path, operationId: String): TagOperation =
        loadUnlocked(taskDirectory.resolve("tag-operations").resolve("$operationId.json"))

    private fun loadUnlocked(target: Path): TagOperation =
        json.decodeFromString<TagOperation>(Files.readString(target)).normalizeLegacyState()

    fun list(taskDirectory: Path): List<TagOperation> {
        val directory = taskDirectory.resolve("tag-operations")
        if (!directory.exists()) return emptyList()
        return Files.list(directory).use { files ->
            files
                .filter { it.fileName.toString().endsWith(".json") }
                .map { json.decodeFromString<TagOperation>(Files.readString(it)).normalizeLegacyState() }
                .sorted(compareByDescending { it.updatedAt })
                .toList()
        }
    }

    /** Deletes only persisted Tag-operation records and the legacy summary for one task. */
    fun clear(taskDirectory: Path): Int {
        var deleted = 0
        val directory = taskDirectory.resolve("tag-operations")
        if (directory.exists()) {
            Files.list(directory).use { files ->
                val operationIds = files
                    .filter { it.fileName.toString().endsWith(".json") }
                    .map { it.fileName.toString().removeSuffix(".json") }
                    .toList()
                operationIds.forEach { operationId ->
                    withOperationLock(taskDirectory, operationId) {
                        if (Files.deleteIfExists(directory.resolve("$operationId.json"))) deleted++
                    }
                }
            }
        }
        if (Files.deleteIfExists(taskDirectory.resolve("tag-build-history.jsonl"))) deleted++
        return deleted
    }

    /**
     * Deletes the selected operation records for one task.
     *
     * Operation IDs are compared with the file name rather than interpolated into a
     * path. This keeps the operation selection bounded to this task's direct
     * JSON files directly under the task's `tag-operations` directory, even if a
     * caller supplies an unexpected ID.
     * The legacy JSONL file is rewritten atomically and only valid entries whose
     * operation ID is selected are removed; malformed or unrelated lines are kept.
     */
    fun deleteSelected(taskDirectory: Path, operationIds: Collection<String>): Int {
        val selected = operationIds.filter(String::isNotBlank).toSet()
        if (selected.isEmpty()) return 0

        var deleted = 0
        val directory = taskDirectory.resolve("tag-operations")
        if (directory.exists()) {
            Files.list(directory).use { files ->
                val operationIds = files
                    .filter { it.fileName.toString().endsWith(".json") }
                    .map { it.fileName.toString().removeSuffix(".json") }
                    .filter { it in selected }
                    .toList()
                operationIds.forEach { operationId ->
                    withOperationLock(taskDirectory, operationId) {
                        if (Files.deleteIfExists(directory.resolve("$operationId.json"))) {
                            deleted++
                        }
                    }
                }
            }
        }

        rewriteLegacyHistory(taskDirectory.resolve("tag-build-history.jsonl"), selected)
        return deleted
    }

    fun appendHistory(taskDirectory: Path, entry: TagBuildHistoryEntry) {
        taskDirectory.createDirectories()
        val bytes = (Json.encodeToString(entry) + System.lineSeparator())
            .toByteArray(StandardCharsets.UTF_8)
        FileChannel.open(
            taskDirectory.resolve("tag-build-history.jsonl"),
            java.nio.file.StandardOpenOption.CREATE,
            java.nio.file.StandardOpenOption.WRITE,
            java.nio.file.StandardOpenOption.APPEND,
        ).use { channel ->
            channel.lock().use {
                val buffer = ByteBuffer.wrap(bytes)
                while (buffer.hasRemaining()) channel.write(buffer)
                channel.force(false)
            }
        }
    }

    private fun rewriteLegacyHistory(historyFile: Path, selected: Set<String>) {
        if (!historyFile.exists()) return
        val original = Files.readString(historyFile, StandardCharsets.UTF_8)
        // Kotlin's default limit (0) retains the trailing empty element, so the
        // original final newline is preserved when lines are joined below.
        val retained = original.split('\n').filter { line ->
            val entry = runCatching {
                json.decodeFromString<TagBuildHistoryEntry>(line.trimEnd('\r'))
            }.getOrNull()
            entry == null || entry.operationId !in selected
        }
        val rewritten = retained.joinToString("\n")
        if (rewritten == original) return

        val parent = historyFile.parent ?: return
        val temporary = Files.createTempFile(parent, ".${historyFile.fileName}-", ".tmp")
        try {
            Files.writeString(temporary, rewritten, StandardCharsets.UTF_8)
            try {
                Files.move(
                    temporary,
                    historyFile,
                    StandardCopyOption.REPLACE_EXISTING,
                    StandardCopyOption.ATOMIC_MOVE,
                )
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(temporary, historyFile, StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun <T> withOperationLock(taskDirectory: Path, operationId: String, block: () -> T): T {
        val lockId = FileLocking.stablePathHash(taskDirectory.resolve(operationId))
        return FileLocking.withExclusiveLockWaiting(
            taskDirectory.resolve("tag-operations").resolve(".$lockId.lock"),
            block,
        )
    }

    /**
     * Acquires a record lock without creating its parent. This is deliberately
     * used only by the late-probe CAS path: if the task or operation directory
     * disappeared after the initial existence check, the update is skipped.
     */
    private fun <T> withExistingOperationLock(
        taskDirectory: Path,
        operationId: String,
        block: () -> T,
    ): T? {
        val parent = taskDirectory.resolve("tag-operations")
        if (!Files.isDirectory(parent)) return null
        val lockId = FileLocking.stablePathHash(taskDirectory.resolve(operationId))
        val lockPath = parent.resolve(".$lockId.lock")
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
                    if (lock != null) {
                        return lock.use { block() }
                    }
                    try {
                        Thread.sleep(50)
                    } catch (_: InterruptedException) {
                        interrupted = true
                    }
                }
            }
        } catch (_: NoSuchFileException) {
            return null
        } finally {
            if (interrupted) Thread.currentThread().interrupt()
        }
    }
}

@Suppress("DEPRECATION")
private fun TagOperation.normalizeLegacyState(): TagOperation = copy(
    state = when (state) {
        TagOperationState.FEATURE_PUSHED -> TagOperationState.SOURCE_BRANCH_PUSHED
        TagOperationState.TEST_BRANCH_PUSHED -> TagOperationState.TARGET_BRANCH_PUSHED
        else -> state
    },
)
