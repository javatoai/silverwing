package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AgentDocumentFormatException
import com.snowball.silverwing.core.AgentDocumentLayout
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.GroupConfig
import com.snowball.silverwing.core.TaskManifest
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class AgentInstructionsAsyncReadTest {
    @Test
    fun `async agent reads return content and preserve format failures`() {
        val root = Files.createTempDirectory("SILVERWING-agent-async-read")
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
        val taskDirectory = taskRoot.resolve(task.taskDirectoryName)
        Files.createDirectories(taskDirectory)
        Files.writeString(
            taskDirectory.resolve("AGENTS.md"),
            """<!-- SILVERWING:GENERATED:BEGIN -->
generated
<!-- SILVERWING:GENERATED:END -->

<!-- SILVERWING:TASK-NOTES:BEGIN -->
task notes
<!-- SILVERWING:TASK-NOTES:END -->
""".trimIndent(),
        )

        val invalidTask = task("invalid")
        val invalidDirectory = taskRoot.resolve(invalidTask.taskDirectoryName)
        Files.createDirectories(invalidDirectory)
        Files.writeString(invalidDirectory.resolve("AGENTS.md"), "missing SILVERWING markers")

        val referencedTask = task("referenced").copy(agentDocumentLayout = AgentDocumentLayout.REFERENCED_V2)
        val referencedRules = taskRoot.resolve(referencedTask.taskDirectoryName).resolve(".silverwing").resolve("agent").resolve("TASK-RULES.md")
        Files.createDirectories(referencedRules.parent)
        Files.writeString(referencedRules, "referenced task rules")

        val controller = DesktopApplication(paths = paths, configStore = store)
        try {
            kotlinx.coroutines.runBlocking {
                assertEquals("global instructions", controller.readGlobalAgentsAsync())
                assertEquals("group instructions", controller.readGroupAgentsAsync("group"))
                assertEquals("task notes", controller.readTaskNotesAsync(task))
                assertEquals("referenced task rules", controller.readTaskNotesAsync(referencedTask))
                assertFailsWith<AgentDocumentFormatException> {
                    controller.readTaskNotesAsync(invalidTask)
                }
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
