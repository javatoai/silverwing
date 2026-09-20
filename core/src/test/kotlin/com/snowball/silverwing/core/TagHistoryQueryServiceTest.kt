package com.snowball.silverwing.core

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TagHistoryQueryServiceTest {
    @Test
    fun `returns operations from all tasks newest first`() {
        val root = Files.createTempDirectory("tag-history-query-")
        val store = TagOperationStore()
        val olderTask = task("older")
        val newerTask = task("newer")
        store.save(root.resolve("older"), operation("older-op", "2026-08-08 09:00:00"))
        store.save(root.resolve("newer"), operation("newer-op", "2026-08-08 10:00:00"))

        val result = TagHistoryQueryService(store).list(
            AppConfig(taskRoot = root.toString()),
            listOf(olderTask, newerTask),
        )

        assertEquals(listOf("newer-op", "older-op"), result.map(TagOperation::operationId))
    }

    @Test
    fun `loads early 0_7 operation fields and normalizes state`() {
        val taskDirectory = Files.createTempDirectory("tag-operation-legacy-")
        val directory = taskDirectory.resolve("tag-operations")
        Files.createDirectories(directory)
        Files.writeString(
            directory.resolve("legacy.json"),
            """{
              "operationId":"legacy","folderName":"TASK","serviceName":"service","repositoryId":"repo",
              "featureBranch":"feature/task","testBranch":"release/test","remote":"origin",
              "state":"TEST_BRANCH_PUSHED","createdAt":"2026-01-01","updatedAt":"2026-01-01"
            }""".trimIndent(),
        )

        val operation = TagOperationStore().load(taskDirectory, "legacy")

        assertEquals("feature/task", operation.sourceBranch)
        assertEquals("release/test", operation.targetBranch)
        assertEquals(TagOperationState.TARGET_BRANCH_PUSHED, operation.state)
    }

    @Test
    fun `groups operations with the same batch id and keeps legacy operations separate`() {
        val root = Files.createTempDirectory("tag-history-batches-")
        val store = TagOperationStore()
        val task = task("batch-task")
        val directory = root.resolve(task.taskDirectoryName)
        store.save(directory, operation("legacy-op", "2026-08-08 09:00:00"))
        store.save(directory, operation("batch-a-service-1", "2026-08-08 10:00:00", "batch-a"))
        store.save(directory, operation("batch-a-service-2", "2026-08-08 10:02:00", "batch-a"))

        val result = TagHistoryQueryService(store).listItems(
            AppConfig(taskRoot = root.toString()),
            listOf(task),
        )

        assertEquals(2, result.size)
        val batch = result[0]
        assertEquals("batch-a", batch.groupId)
        assertEquals("batch-a", batch.batchId)
        assertEquals("2026-08-08 10:00:00", batch.createdAt)
        assertEquals("2026-08-08 10:02:00", batch.updatedAt)
        assertEquals(
            listOf("batch-a-service-2", "batch-a-service-1"),
            batch.operations.map(TagOperation::operationId),
        )
        val legacy = result[1]
        assertEquals("legacy-op", legacy.groupId)
        assertEquals(null, legacy.batchId)
        assertEquals(listOf("legacy-op"), legacy.operations.map(TagOperation::operationId))
    }

    @Test
    fun `persists batch id on failed operation without changing the operation state`() {
        val directory = Files.createTempDirectory("tag-history-failed-batch-")
        val failed = operation("failed-op", "2026-08-08 10:00:00", "batch-failure").copy(
            state = TagOperationState.FAILED,
            message = "构建失败",
        )
        val store = TagOperationStore()

        store.save(directory, failed)
        val loaded = store.load(directory, failed.operationId)

        assertEquals("batch-failure", loaded.batchId)
        assertEquals(TagOperationState.FAILED, loaded.state)
        assertEquals("构建失败", loaded.message)
    }

    @Test
    fun `retention removes oldest complete groups and keeps newer groups`() {
        val root = Files.createTempDirectory("tag-history-retention-")
        val store = TagOperationStore()
        val task = task("task")
        val directory = root.resolve(task.taskDirectoryName)
        store.save(directory, operation("old-1", "2026-08-08 09:00:00", "old-batch"))
        store.save(directory, operation("old-2", "2026-08-08 09:01:00", "old-batch"))
        store.save(directory, operation("middle", "2026-08-08 10:00:00"))
        store.save(directory, operation("new", "2026-08-08 11:00:00"))
        val config = AppConfig(taskRoot = root.toString(), tagHistoryMaxGroups = 2)
        val service = TagHistoryQueryService(store)

        assertEquals(2, service.enforceRetention(config, listOf(task)))
        assertEquals(
            listOf("new", "middle"),
            service.listItems(config, listOf(task)).flatMap(TagHistoryItem::operations).map(TagOperation::operationId),
        )
        assertFalse(Files.exists(directory.resolve("tag-operations/old-1.json")))
        assertFalse(Files.exists(directory.resolve("tag-operations/old-2.json")))
        assertTrue(Files.exists(directory.resolve("tag-operations/middle.json")))
        assertTrue(Files.exists(directory.resolve("tag-operations/new.json")))
    }

    @Test
    fun `clear removes only Tag history files from every known task`() {
        val root = Files.createTempDirectory("tag-history-clear-")
        val store = TagOperationStore()
        val olderTask = task("older")
        val newerTask = task("newer")
        listOf(olderTask, newerTask).forEach { task ->
            val directory = root.resolve(task.taskDirectoryName)
            store.save(directory, operation("${task.folderName}-op", "2026-08-08 10:00:00"))
            store.appendHistory(directory, TagBuildHistoryEntry("${task.folderName}-op", "2026-08-08 10:00:00", task.folderName, "service", "feature/${task.folderName}", tag = "v1.0.0", state = TagOperationState.SUCCESS))
        }
        val unrelated = root.resolve("older").resolve("tag-operations").resolve("note.txt")
        Files.writeString(unrelated, "keep")
        val service = TagHistoryQueryService(store)

        assertEquals(4, service.clear(AppConfig(taskRoot = root.toString()), listOf(olderTask, newerTask)))
        assertEquals(emptyList(), service.list(AppConfig(taskRoot = root.toString()), listOf(olderTask, newerTask)))
        assertTrue(Files.exists(unrelated))
    }

    @Test
    fun `deleteSelected removes matching records and preserves unrelated or invalid legacy lines`() {
        val root = Files.createTempDirectory("tag-history-delete-")
        val store = TagOperationStore()
        val firstTask = task("first")
        val secondTask = task("second")
        val firstDirectory = root.resolve(firstTask.taskDirectoryName)
        val secondDirectory = root.resolve(secondTask.taskDirectoryName)

        store.save(firstDirectory, operation("delete-first", "2026-08-08 09:00:00"))
        store.save(firstDirectory, operation("keep-first", "2026-08-08 09:01:00"))
        store.save(secondDirectory, operation("delete-second", "2026-08-08 09:02:00"))
        store.appendHistory(
            firstDirectory,
            TagBuildHistoryEntry(
                operationId = "delete-first",
                timestamp = "2026-08-08 09:00:00",
                folderName = firstTask.folderName,
                serviceName = "service",
                sourceBranch = "feature/first",
                tag = "v1.0.0",
                state = TagOperationState.SUCCESS,
            ),
        )
        store.appendHistory(
            firstDirectory,
            TagBuildHistoryEntry(
                operationId = "keep-first",
                timestamp = "2026-08-08 09:01:00",
                folderName = firstTask.folderName,
                serviceName = "service",
                sourceBranch = "feature/first",
                tag = "v1.0.1",
                state = TagOperationState.SUCCESS,
            ),
        )
        val history = firstDirectory.resolve("tag-build-history.jsonl")
        Files.writeString(history, Files.readString(history) + "not-json\n")

        val deleted = TagHistoryQueryService(store).deleteSelected(
            AppConfig(taskRoot = root.toString()),
            listOf(firstTask, secondTask),
            setOf("delete-first", "delete-second"),
        )

        assertEquals(2, deleted)
        assertFalse(Files.exists(firstDirectory.resolve("tag-operations/delete-first.json")))
        assertFalse(Files.exists(secondDirectory.resolve("tag-operations/delete-second.json")))
        assertTrue(Files.exists(firstDirectory.resolve("tag-operations/keep-first.json")))
        val rewritten = Files.readString(history)
        assertFalse(rewritten.contains("delete-first"))
        assertTrue(rewritten.contains("keep-first"))
        assertTrue(rewritten.contains("not-json"))
    }

    @Test
    fun `deleteSelected with empty IDs is a no-op`() {
        val root = Files.createTempDirectory("tag-history-delete-empty-")
        val store = TagOperationStore()
        val task = task("task")
        val directory = root.resolve(task.taskDirectoryName)
        store.save(directory, operation("keep", "2026-08-08 09:00:00"))

        assertEquals(
            0,
            TagHistoryQueryService(store).deleteSelected(
                AppConfig(taskRoot = root.toString()),
                listOf(task),
                emptySet(),
            ),
        )
        assertTrue(Files.exists(directory.resolve("tag-operations/keep.json")))
    }

    private fun task(name: String) = TaskManifest(
        folderName = name,
        taskDirectoryName = name,
        featureBranch = "feature/$name",
        createdAt = "2026-08-08 08:00:00",
        updatedAt = "2026-08-08 08:00:00",
        lifecycleStatus = TaskLifecycleStatus.ACTIVE,
        services = emptyList(),
    )

    private fun operation(id: String, updatedAt: String, batchId: String? = null) = TagOperation(
        operationId = id,
        folderName = id,
        serviceName = "service",
        repositoryId = "repo",
        sourceBranch = "feature/$id",
        targetBranch = "release/test",
        remote = "origin",
        state = TagOperationState.SUCCESS,
        createdAt = updatedAt,
        updatedAt = updatedAt,
        tag = "v1.0.0",
        batchId = batchId,
    )
}
