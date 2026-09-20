package com.snowball.silverwing.core

import java.nio.file.Path

/** Reads persisted Tag operations without exposing JSON storage details to Desktop UI code. */
class TagHistoryQueryService(
    private val operations: TagOperationStore = TagOperationStore(),
) {
    fun list(config: AppConfig, tasks: List<TaskManifest>): List<TagOperation> {
        val taskRoot = config.taskRoot?.let(Path::of) ?: return emptyList()
        return tasks.flatMap { task ->
            runCatching { operations.list(taskRoot.resolve(task.taskDirectoryName)) }.getOrDefault(emptyList())
        }.sortedByDescending(TagOperation::updatedAt)
    }

    /**
     * Returns display-ready history items while keeping each child operation intact.
     * Operations created by one batch share a persisted batch ID and are rendered as
     * one card; records without a batch ID remain individual legacy/single items.
     */
    fun listItems(config: AppConfig, tasks: List<TaskManifest>): List<TagHistoryItem> {
        val all = list(config, tasks)
        return all
            .groupBy { it.batchId?.takeIf(String::isNotBlank) ?: it.operationId }
            .map { (groupId, entries) ->
                val children = entries.sortedWith(operationLatestFirst)
                TagHistoryItem(
                    groupId = groupId,
                    batchId = children.first().batchId?.takeIf(String::isNotBlank),
                    folderName = children.minWithOrNull(operationCreatedFirst)?.folderName.orEmpty(),
                    createdAt = children.minOf(TagOperation::createdAt),
                    updatedAt = children.maxOf(TagOperation::updatedAt),
                    operations = children,
                )
            }
            .sortedWith(historyItemLatestFirst)
    }

    /** Removes the oldest complete history groups until the configured global limit is met. */
    fun enforceRetention(config: AppConfig, tasks: List<TaskManifest>): Int {
        val items = listItems(config, tasks)
        if (items.size <= config.tagHistoryMaxGroups) return 0
        val selectedOperationIds = items
            .drop(config.tagHistoryMaxGroups)
            .flatMap(TagHistoryItem::operations)
            .map(TagOperation::operationId)
            .toSet()
        return deleteSelected(config, tasks, selectedOperationIds)
    }

    /** Clears only Tag build records for the currently known task directories. */
    fun clear(config: AppConfig, tasks: List<TaskManifest>): Int {
        val taskRoot = config.taskRoot?.let(Path::of) ?: return 0
        return tasks.sumOf { task -> operations.clear(taskRoot.resolve(task.taskDirectoryName)) }
    }

    /** Deletes selected Tag operation records from the currently known task directories. */
    fun deleteSelected(
        config: AppConfig,
        tasks: List<TaskManifest>,
        operationIds: Collection<String>,
    ): Int {
        if (operationIds.none(String::isNotBlank)) return 0
        val taskRoot = config.taskRoot?.let(Path::of) ?: return 0
        return tasks.fold(0) { deleted, task ->
            deleted + operations.deleteSelected(taskRoot.resolve(task.taskDirectoryName), operationIds)
        }
    }

    private companion object {
        val operationLatestFirst = compareByDescending<TagOperation> { it.updatedAt }
            .thenByDescending { it.operationId }
        val operationCreatedFirst = compareBy<TagOperation> { it.createdAt }
            .thenBy { it.operationId }
        val historyItemLatestFirst = compareByDescending<TagHistoryItem> { it.updatedAt }
            .thenBy { it.groupId }
    }
}
