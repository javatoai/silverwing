package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.CommandOutputLine
import com.snowball.silverwing.core.CommandOutputStream
import com.snowball.silverwing.core.WorkspaceCommandConfig
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertSame
import kotlin.test.assertTrue
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.BuildCircle
import androidx.compose.material.icons.outlined.CleaningServices
import androidx.compose.material.icons.outlined.MoveToInbox
import androidx.compose.material.icons.outlined.RocketLaunch

class WorkspaceCommandPresentationTest {
    @Test
    fun `command display quotes only tokens that need it and keeps each argument`() {
        val command = WorkspaceCommandConfig(
            id = "deploy",
            name = "Deploy",
            executable = "mvn",
            arguments = listOf("clean", "-Drevision=release candidate", "-Dpath=C:\\work\\repo"),
        )

        assertEquals(
            "mvn clean \"-Drevision=release candidate\" -Dpath=C:\\work\\repo",
            workspaceCommandDisplay(command),
        )
    }

    @Test
    fun `built in icon list has a safe fallback`() {
        assertTrue(
            workspaceCommandIconKeys.containsAll(
                listOf("build", "maven", "maven-clean", "maven-install", "maven-deploy", "terminal", "package", "upload"),
            ),
        )
        assertSame(Icons.Outlined.BuildCircle, workspaceCommandIcon("maven"))
        assertSame(Icons.Outlined.CleaningServices, workspaceCommandIcon("maven-clean"))
        assertSame(Icons.Outlined.MoveToInbox, workspaceCommandIcon("maven-install"))
        assertSame(Icons.Outlined.RocketLaunch, workspaceCommandIcon("maven-deploy"))
        assertSame(Icons.Outlined.Build, workspaceCommandIcon("unknown-icon"))
    }

    @Test
    fun `command output copy keeps interleaved text without stream labels`() {
        val output = workspaceCommandOutputCopyText(
            listOf(
                CommandOutputLine(CommandOutputStream.STDOUT, "[INFO] compiling"),
                CommandOutputLine(CommandOutputStream.STDERR, "warning: deprecated"),
                CommandOutputLine(CommandOutputStream.STDOUT, "BUILD SUCCESS"),
            ),
        )

        assertEquals("[INFO] compiling\nwarning: deprecated\nBUILD SUCCESS", output)
    }
}
