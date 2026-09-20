package com.snowball.silverwing.core

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class AgentDocumentPropagationServiceTest {
    @Test
    fun `group change regenerates only matching legacy tasks`() {
        val root = Files.createTempDirectory("agent-propagation-")
        val manifests = ManifestStore()
        fun save(name: String, group: String, layout: AgentDocumentLayout = AgentDocumentLayout.INLINE_V1) {
            val directory = root.resolve(name)
            manifests.save(
                directory,
                TaskManifest(
                    folderName = name,
                    taskDirectoryName = name,
                    featureBranch = "feature/$name",
                    createdAt = "2026-08-08T00:00:00Z",
                    updatedAt = "2026-08-08T00:00:00Z",
                    lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                    services = emptyList(),
                    groupId = group,
                    agentDocumentLayout = layout,
                ),
            )
        }
        save("a", "alpha")
        save("b", "beta")
        save("referenced", "alpha", AgentDocumentLayout.REFERENCED_V2)
        val documents = CountingDocuments()
        val result = AgentDocumentPropagationService(manifests, documents, NoOpTaskOperationLock).propagate(
            AppConfig(taskRoot = root.toString()),
            AgentInstructionScope.Group("alpha"),
        )

        assertEquals(listOf("a"), result.updatedTaskDirectories.map { it.fileName.toString() })
        assertEquals(listOf("a"), documents.tasks)
    }

    @Test
    fun `task deleted after scan is not recreated by propagation`() {
        val root = Files.createTempDirectory("agent-propagation-delete-")
        val directory = root.resolve("task")
        val manifests = ManifestStore()
        manifests.save(
            directory,
            TaskManifest(
                folderName = "task",
                taskDirectoryName = "task",
                featureBranch = "feature/task",
                createdAt = "2026-08-08T00:00:00Z",
                updatedAt = "2026-08-08T00:00:00Z",
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = emptyList(),
            ),
        )
        val documents = CountingDocuments()
        val deletingLock = object : TaskOperationLock {
            override fun <T> withLock(taskDirectory: java.nio.file.Path, block: () -> T): T {
                Files.walk(taskDirectory).use { paths ->
                    paths.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists)
                }
                return block()
            }
        }

        val result = AgentDocumentPropagationService(manifests, documents, deletingLock).propagate(
            AppConfig(taskRoot = root.toString()),
            AgentInstructionScope.Global,
        )

        assertFalse(Files.exists(directory))
        assertEquals(0, documents.tasks.size)
        assertEquals(1, result.failures.size)
    }
}

private class CountingDocuments : AgentDocuments {
    val tasks = mutableListOf<String>()
    override fun readGlobal() = ""
    override fun saveGlobal(content: String) = Unit
    override fun readGroup(groupId: String) = ""
    override fun saveGroup(groupId: String, content: String) = Unit
    override fun writeTaskDocument(
        taskDirectory: java.nio.file.Path,
        manifest: TaskManifest,
        repositories: List<RepositoryInfo>,
        taskNotes: String?,
    ): java.nio.file.Path {
        tasks += manifest.folderName
        return taskDirectory.resolve("AGENTS.md")
    }
}
