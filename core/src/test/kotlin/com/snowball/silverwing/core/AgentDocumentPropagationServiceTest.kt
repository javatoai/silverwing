package com.snowball.silverwing.core

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class AgentDocumentPropagationServiceTest {
    @Test
    fun `shared rule changes never rewrite task local routers`() {
        val root = Files.createTempDirectory("agent-propagation-")
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

        val result = AgentDocumentPropagationService(manifests, documents, NoOpTaskOperationLock).propagate(
            AppConfig(taskRoot = root.toString()),
            AgentInstructionScope.Global,
        )

        assertTrue(result.updatedTaskDirectories.isEmpty())
        assertTrue(result.failures.isEmpty())
        assertTrue(documents.tasks.isEmpty())
        assertEquals(true, Files.isDirectory(directory))
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
