package com.snowball.silverwing.core

import java.nio.file.Path

sealed interface AgentInstructionScope {
    data object Global : AgentInstructionScope
    data class Group(val groupId: String) : AgentInstructionScope
}

data class AgentPropagationResult(
    val updatedTaskDirectories: List<Path>,
    val failures: Map<Path, String>,
)

/** Regenerates only legacy inline task documents affected by a global or group file change. */
class AgentDocumentPropagationService(
    private val manifests: TaskManifestRepository = ManifestStore(),
    private val documents: AgentDocuments = AgentDocumentService(),
    private val operationLock: TaskOperationLock = FileTaskOperationLock(),
) {
    fun propagate(config: AppConfig, scope: AgentInstructionScope): AgentPropagationResult {
        val root = config.taskRoot?.let(Path::of)
            ?: return AgentPropagationResult(emptyList(), emptyMap())
        val updated = mutableListOf<Path>()
        val failures = linkedMapOf<Path, String>()
        manifests.scan(root).current
            .filter { (_, manifest) ->
                manifest.agentDocumentLayout == AgentDocumentLayout.INLINE_V1 && when (scope) {
                    AgentInstructionScope.Global -> true
                    is AgentInstructionScope.Group -> manifest.groupId == scope.groupId
                }
            }
            .forEach { (directory, _) ->
                runCatching {
                    operationLock.withLock(directory) {
                        // The scan is only a candidate list. Reload after taking
                        // the task lock so a concurrent delete cannot be recreated
                        // and archive status cannot be overwritten with stale data.
                        val current = manifests.load(directory)
                        documents.writeTaskDocument(directory, current, config.repositories.map(RepositoryConfig::toInfo))
                    }
                }.onSuccess {
                    updated.add(directory)
                }.onFailure { error ->
                    failures[directory] = error.message ?: error::class.simpleName.orEmpty()
                }
            }
        return AgentPropagationResult(updated, failures)
    }
}
