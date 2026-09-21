package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.AgentDocumentService
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.GroupConfig
import com.snowball.silverwing.core.TaskManifest
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals

class AgentInstructionsAsyncReadTest {
    @Test
    fun `async agent reads return global group and independent task rule files`() {
        val root = Files.createTempDirectory("agent-async-read")
        val paths = ApplicationPaths(root.resolve("home"))
        val taskRoot = root.resolve("tasks")
        val store = ConfigStore(paths)
        store.save(
            AppConfig(
                taskRoot = taskRoot.toString(),
                groups = listOf(GroupConfig("group", "Group")),
            ),
        )
        Files.createDirectories(paths.globalAgents.parent)
        Files.createDirectories(paths.groupAgents("group").parent)
        Files.writeString(paths.globalAgents, "global instructions")
        Files.writeString(paths.groupAgents("group"), "group instructions")

        val task = task("task")
        val taskRules = taskRoot.resolve(task.taskDirectoryName)
            .resolve(".workspace")
            .resolve("agent")
            .resolve(AgentDocumentService.TASK_RULES_FILE_NAME)
        Files.createDirectories(taskRules.parent)
        Files.writeString(taskRules, "task rules")

        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            kotlinx.coroutines.runBlocking {
                assertEquals("global instructions", controller.readGlobalAgentsAsync())
                assertEquals("group instructions", controller.readGroupAgentsAsync("group"))
                assertEquals("task rules", controller.readTaskNotesAsync(task))
            }
        } finally {
            controller.close()
            root.toFile().deleteRecursively()
        }
    }

    private fun task(name: String) = TaskManifest(
        folderName = name,
        taskDirectoryName = name,
        featureBranch = "feature/$name",
        createdAt = "2026-09-08 00:00:00",
        updatedAt = "2026-09-08 00:00:00",
        services = emptyList(),
    )
}
