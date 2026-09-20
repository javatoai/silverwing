package com.snowball.silverwing.core

import kotlinx.serialization.json.Json
import java.nio.file.Files
import kotlin.io.path.exists

/**
 * Persists the user's task-notes template library as a single JSON file.
 * The file is user-editable data, not part of the strict AppConfig schema.
 */
class AgentTaskTemplateStore(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val json: Json = Json {
        prettyPrint = true
        ignoreUnknownKeys = true
        encodeDefaults = true
    },
) {
    fun list(): List<AgentTaskTemplate> {
        val file = paths.agentTaskTemplates
        if (!file.exists()) return emptyList()
        val content = Files.readString(file)
        if (content.isBlank()) return emptyList()
        return json.decodeFromString<List<AgentTaskTemplate>>(content).sortedBy { it.name }
    }

    fun saveAll(templates: List<AgentTaskTemplate>) = withMutationLock {
        saveAllUnlocked(templates)
    }

    /** Serializes the complete read-modify-write operation across silverwing instances. */
    fun update(transform: (List<AgentTaskTemplate>) -> List<AgentTaskTemplate>): List<AgentTaskTemplate> =
        withMutationLock {
            val updated = transform(list())
            saveAllUnlocked(updated)
            list()
        }

    private fun saveAllUnlocked(templates: List<AgentTaskTemplate>) {
        validate(templates)
        AtomicFileWriter.write(paths.agentTaskTemplates, json.encodeToString(templates))
    }

    private fun <T> withMutationLock(block: () -> T): T =
        FileLocking.withExclusiveLockWaiting(paths.locks.resolve("agent-task-templates.lock"), block)

    private fun validate(templates: List<AgentTaskTemplate>) {
        val names = templates.map { it.name.trim() }
        require(names.distinct().size == names.size) { "模板名称不能重复" }
        val reserved = listOf(
            AgentDocumentService.GENERATED_BEGIN,
            AgentDocumentService.GENERATED_END,
            AgentDocumentService.TASK_NOTES_BEGIN,
            AgentDocumentService.TASK_NOTES_END,
        )
        templates.forEach { template ->
            val marker = reserved.firstOrNull(template.content::contains)
            require(marker == null) { "模板「${template.name}」包含 silverwing 保留标记：$marker" }
        }
    }
}
