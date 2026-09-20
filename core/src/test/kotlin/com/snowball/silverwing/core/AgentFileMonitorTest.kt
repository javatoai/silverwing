package com.snowball.silverwing.core

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class AgentFileMonitorTest {
    @Test
    fun `external change reloads clean editor but conflicts with unsaved edit`() {
        val root = Files.createTempDirectory("agent-monitor-")
        val file = root.resolve("AGENTS.md")
        Files.writeString(file, "v1")
        val changes = mutableListOf<AgentFileChange>()
        AgentFileMonitor(changes::add, startWatchThread = false).use { monitor ->
            monitor.track(file)
            Files.writeString(file, "v2")
            monitor.checkNow()
            assertEquals("v2", assertIs<AgentFileChange.Reloaded>(changes.removeLast()).content)

            monitor.markLocalEdit(file, "local-v3")
            Files.writeString(file, "disk-v3")
            monitor.checkNow()
            val conflict = assertIs<AgentFileChange.Conflict>(changes.removeLast())
            assertEquals("local-v3", conflict.localContent)
            assertEquals("disk-v3", conflict.diskContent)

            monitor.resolve(file, AgentConflictResolution.USE_LOCAL)
            assertEquals("local-v3", Files.readString(file))
            assertTrue(monitor.snapshot(file)?.dirty == false)
        }
    }

    @Test
    fun `focus fallback compares hashes even when timestamp is unchanged`() {
        val root = Files.createTempDirectory("agent-hash-")
        val file = root.resolve("AGENTS.md")
        Files.writeString(file, "one")
        val originalTime = Files.getLastModifiedTime(file)
        val changes = mutableListOf<AgentFileChange>()
        AgentFileMonitor(changes::add, startWatchThread = false).use { monitor ->
            monitor.track(file)
            Files.writeString(file, "two")
            Files.setLastModifiedTime(file, originalTime)

            monitor.checkNow()

            assertEquals("two", assertIs<AgentFileChange.Reloaded>(changes.single()).content)
        }
    }

    @Test
    fun `save rechecks disk hash and cannot race past a pending watcher event`() {
        val root = Files.createTempDirectory("agent-save-race-")
        val file = root.resolve("AGENTS.md")
        Files.writeString(file, "v1")
        val changes = mutableListOf<AgentFileChange>()
        AgentFileMonitor(changes::add, startWatchThread = false).use { monitor ->
            monitor.track(file)
            monitor.markLocalEdit(file, "local")
            Files.writeString(file, "external")

            assertFailsWith<AgentDocumentConflictException> { monitor.save(file, "local") }
            assertEquals("external", Files.readString(file))
            assertIs<AgentFileChange.Conflict>(changes.single())
        }
    }

    @Test
    fun `tracking a deleted file does not recreate its parent directory`() {
        val root = Files.createTempDirectory("agent-monitor-delete-")
        val taskDirectory = root.resolve("task")
        val file = taskDirectory.resolve("AGENTS.md")
        Files.createDirectories(taskDirectory)
        Files.writeString(file, "task")

        AgentFileMonitor({}, startWatchThread = false).use { monitor ->
            monitor.track(file)
            Files.delete(file)
            Files.delete(taskDirectory)

            monitor.track(file)

            assertFalse(Files.exists(taskDirectory))
        }
    }

    @Test
    fun `local resolution validates the displayed disk version before a custom writer runs`() {
        val root = Files.createTempDirectory("agent-resolve-race-")
        val file = root.resolve("AGENTS.md")
        Files.writeString(file, "initial")
        val changes = mutableListOf<AgentFileChange>()
        AgentFileMonitor(changes::add, startWatchThread = false).use { monitor ->
            monitor.track(file)
            monitor.markLocalEdit(file, "local")
            Files.writeString(file, "first external")
            monitor.checkNow()
            val displayed = assertIs<AgentFileChange.Conflict>(changes.last())

            Files.writeString(file, "second external")
            // A watcher may update its pending hash before the UI handles that event.
            monitor.checkNow()
            var writerCalled = false
            assertFailsWith<AgentDocumentConflictException> {
                monitor.resolve(file, AgentConflictResolution.USE_LOCAL, displayed.diskContent) {
                    writerCalled = true
                    Files.writeString(file, it)
                }
            }

            assertFalse(writerCalled)
            assertEquals("second external", Files.readString(file))
            assertEquals("local", monitor.snapshot(file)?.content)
            assertTrue(monitor.snapshot(file)?.dirty == true)
        }
    }

    @Test
    fun `custom local resolution tracks the regenerated document`() {
        val root = Files.createTempDirectory("agent-resolve-writer-")
        val file = root.resolve("AGENTS.md")
        Files.writeString(file, "initial")
        AgentFileMonitor({}, startWatchThread = false).use { monitor ->
            monitor.track(file)
            monitor.markLocalEdit(file, "local notes")
            Files.writeString(file, "external")
            monitor.checkNow()

            val resolved = monitor.resolve(file, AgentConflictResolution.USE_LOCAL, "external") { local ->
                Files.writeString(file, "regenerated: $local")
            }

            assertEquals("regenerated: local notes", resolved.content)
            assertFalse(resolved.dirty)
            // The next save must compare against the generated content's hash.
            monitor.save(file, "next edit")
            assertEquals("next edit", Files.readString(file))
        }
    }
}
