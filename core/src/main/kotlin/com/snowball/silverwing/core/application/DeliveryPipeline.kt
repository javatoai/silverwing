package com.snowball.silverwing.core

import java.nio.file.Path

data class DeliveryPipelineDescriptor(
    val id: String,
    val displayName: String,
    val historyDisplayName: String,
)

data class DeliveryTarget(
    val config: AppConfig,
    val taskDirectory: Path,
    val selectionKey: String,
)

data class DeliveryExecution(
    val pipelineId: String,
    val executionId: String,
    val state: String,
    val message: String? = null,
)

data class DeliveryHistoryRecord(
    val pipelineId: String,
    val executionId: String,
    val updatedAt: String,
    val taskName: String,
    val targetName: String,
    val state: String,
    val artifact: String? = null,
    val message: String? = null,
)

/** Extension point for task delivery without leaking adapter-specific configuration into the UI. */
interface DeliveryPipelineAdapter {
    val descriptor: DeliveryPipelineDescriptor
    fun execute(target: DeliveryTarget): DeliveryExecution
    fun history(config: AppConfig, tasks: List<TaskManifest>): List<DeliveryHistoryRecord>
}

class DeliveryPipelineRegistry(adapters: List<DeliveryPipelineAdapter>) {
    private val byId = adapters.associateBy { it.descriptor.id }

    init {
        require(byId.size == adapters.size) { "交付流水线 ID 不能重复" }
    }

    fun descriptors(): List<DeliveryPipelineDescriptor> = byId.values.map(DeliveryPipelineAdapter::descriptor)

    fun adapter(id: String): DeliveryPipelineAdapter? = byId[id]
}

/** Strongly typed Git Tag implementation of the generic delivery boundary. */
class GitTagDeliveryAdapter(
    private val tags: TagBuildService = TagBuildService(),
    private val historyQuery: TagHistoryQueryService = TagHistoryQueryService(),
) : DeliveryPipelineAdapter {
    override val descriptor = DeliveryPipelineDescriptor(
        id = ID,
        displayName = "测试Tag",
        historyDisplayName = "Tag构建历史",
    )

    override fun execute(target: DeliveryTarget): DeliveryExecution {
        val operation = executeTag(target)
        return operation.toExecution()
    }

    fun executeTag(target: DeliveryTarget): TagOperation =
        tags.build(target.config, target.taskDirectory, target.selectionKey)

    fun inspectWorkspace(target: DeliveryTarget): TagWorkspaceCheck =
        tags.inspectFeatureWorkspace(target.config, target.taskDirectory, target.selectionKey)

    fun resumeConflict(target: DeliveryTarget, operationId: String): TagOperation =
        tags.resumeConflict(target.config, target.taskDirectory, operationId)

    fun resumeInterrupted(target: DeliveryTarget, operationId: String): TagOperation =
        tags.resumeInterrupted(target.config, target.taskDirectory, operationId)

    fun resumeFailed(target: DeliveryTarget, operationId: String): TagOperation =
        tags.resumeFailed(target.config, target.taskDirectory, operationId)

    fun resumePartial(target: DeliveryTarget, operationId: String): TagOperation =
        tags.resumePartial(target.config, target.taskDirectory, operationId)

    fun retag(target: DeliveryTarget, operationId: String): TagOperation =
        tags.retag(target.config, target.taskDirectory, operationId)

    fun executeBatch(config: AppConfig, taskDirectory: Path, selectionKeys: List<String>): List<TagOperation> =
        tags.buildBatch(config, taskDirectory, selectionKeys)

    fun historyOperations(config: AppConfig, tasks: List<TaskManifest>): List<TagOperation> =
        historyQuery.list(config, tasks)

    fun historyItems(config: AppConfig, tasks: List<TaskManifest>): List<TagHistoryItem> =
        historyQuery.listItems(config, tasks)

    fun enforceHistoryRetention(config: AppConfig, tasks: List<TaskManifest>): Int =
        historyQuery.enforceRetention(config, tasks)

    fun clearHistory(config: AppConfig, tasks: List<TaskManifest>): Int =
        historyQuery.clear(config, tasks)

    fun deleteHistory(config: AppConfig, tasks: List<TaskManifest>, operationIds: Collection<String>): Int =
        historyQuery.deleteSelected(config, tasks, operationIds)

    override fun history(config: AppConfig, tasks: List<TaskManifest>): List<DeliveryHistoryRecord> =
        historyQuery.list(config, tasks).map { operation ->
            DeliveryHistoryRecord(
                pipelineId = ID,
                executionId = operation.operationId,
                updatedAt = operation.updatedAt,
                taskName = operation.folderName,
                targetName = operation.serviceName,
                state = operation.state.name,
                artifact = operation.tag,
                message = operation.message,
            )
        }

    private fun TagOperation.toExecution() = DeliveryExecution(
        pipelineId = ID,
        executionId = operationId,
        state = state.name,
        message = message,
    )

    companion object {
        const val ID = "git-tag"
    }
}
