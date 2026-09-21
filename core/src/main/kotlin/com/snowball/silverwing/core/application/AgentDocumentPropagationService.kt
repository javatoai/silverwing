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

/**
 * Referenced task routers point to shared rule files directly, so saving a
 * global or group document never rewrites task-local AGENTS.md files.
 */
class AgentDocumentPropagationService(
    @Suppress("UNUSED_PARAMETER") manifests: TaskManifestRepository = ManifestStore(),
    @Suppress("UNUSED_PARAMETER") documents: AgentDocuments = AgentDocumentService(),
    @Suppress("UNUSED_PARAMETER") operationLock: TaskOperationLock = FileTaskOperationLock(),
) {
    fun propagate(config: AppConfig, scope: AgentInstructionScope): AgentPropagationResult =
        AgentPropagationResult(emptyList(), emptyMap())
}
