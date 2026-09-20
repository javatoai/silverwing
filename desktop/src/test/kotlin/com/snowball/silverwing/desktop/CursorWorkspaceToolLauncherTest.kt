package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.TaskWorkspaceContext
import com.snowball.silverwing.core.TaskWorkspaceToolAvailability
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

class CursorWorkspaceToolLauncherTest {
    @Test
    fun `opens the task directory in a new window without adding a prompt`() {
        val commands = mutableListOf<List<String>>()
        val launcher = CursorWorkspaceToolLauncher(
            locator = CursorCommandLocator { listOf("C:\\Cursor\\Cursor.exe") },
            processLauncher = DetachedProcessLauncher(commands::add),
        )

        launcher.open(context())

        assertEquals(
            listOf(
                "C:\\Cursor\\Cursor.exe",
                "--new-window",
                Path.of("C:\\tasks\\PAY 1024").toAbsolutePath().normalize().toString(),
            ),
            commands.single(),
        )
    }

    @Test
    fun `reports unavailable when Cursor cannot be located`() {
        val launcher = CursorWorkspaceToolLauncher(CursorCommandLocator { null })

        assertIs<TaskWorkspaceToolAvailability.Unavailable>(launcher.availability())
    }

    private fun context() = TaskWorkspaceContext(
        taskName = "PAY 1024",
        taskDirectory = Path.of("C:\\tasks\\PAY 1024"),
        agentsFile = Path.of("C:\\tasks\\PAY 1024\\AGENTS.md"),
        workspaces = emptyList(),
    )
}
