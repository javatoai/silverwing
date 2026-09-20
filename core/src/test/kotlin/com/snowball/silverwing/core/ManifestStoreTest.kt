package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path

class ManifestStoreTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `schema 0 5 0 round trip preserves workspace tool launch results`() {
        val directory = temporary.resolve("tool-launch")
        val store = ManifestStore()
        val expected = TaskManifest(
            folderName = "tool-launch",
            taskDirectoryName = "tool-launch",
            featureBranch = "feature/tool-launch",
            createdAt = "2026-08-08 12:00:00",
            updatedAt = "2026-08-08 12:00:01",
            lifecycleStatus = TaskLifecycleStatus.ACTIVE,
            services = emptyList(),
            workspaceToolLaunches = listOf(
                WorkspaceToolLaunch(
                    toolId = "codex",
                    status = WorkspaceToolLaunchStatus.OPENED,
                    updatedAt = "2026-08-08 12:00:01",
                ),
                WorkspaceToolLaunch(
                    toolId = "cursor",
                    status = WorkspaceToolLaunchStatus.FAILED,
                    updatedAt = "2026-08-08 12:00:01",
                    message = "not installed",
                ),
            ),
        )

        store.save(directory, expected)

        assertEquals(CURRENT_TASK_MANIFEST_SCHEMA_VERSION, store.load(directory).schemaVersion)
        assertEquals(expected, store.load(directory))
    }

    @Test
    fun `legacy agent workspace manifest is ignored without compatibility read`() {
        val directory = temporary.resolve("legacy-name")
        Files.createDirectories(directory)
        val legacy = """{"schemaVersion":"1.0.9"}"""
        val legacyFile = directory.resolve("agent-workspace.json")
        Files.writeString(legacyFile, legacy)
        val store = ManifestStore()

        assertThrows(java.nio.file.NoSuchFileException::class.java) { store.load(directory) }
        val scan = store.scan(temporary)
        kotlin.test.assertTrue(scan.current.isEmpty())
        kotlin.test.assertTrue(scan.unsupportedDirectories.isEmpty())
        kotlin.test.assertTrue(scan.failures.isEmpty())
        assertEquals(legacy, Files.readString(legacyFile))
    }

    @Test
    fun `unsupported manifests are preserved and reported but not imported`() {
        val taskDirectory = temporary.resolve("legacy-task")
        Files.createDirectories(taskDirectory)
        Files.writeString(
            taskDirectory.resolve(ManifestStore.FILE_NAME),
            """{"schemaVersion":4}""",
        )
        val store = ManifestStore()

        assertThrows(IllegalArgumentException::class.java) { store.load(taskDirectory) }
        val scan = store.scan(temporary)
        kotlin.test.assertTrue(scan.current.isEmpty())
        kotlin.test.assertEquals(listOf(taskDirectory), scan.unsupportedDirectories)
        kotlin.test.assertEquals(
            "任务 JSON 版本不受支持：4，当前版本为 $CURRENT_TASK_MANIFEST_SCHEMA_VERSION",
            scan.unsupportedReasons[taskDirectory],
        )
        kotlin.test.assertEquals("""{"schemaVersion":4}""", Files.readString(taskDirectory.resolve(ManifestStore.FILE_NAME)))
    }

    @Test
    fun `0 4 manifest is rejected byte for byte without migration`() {
        val taskDirectory = temporary.resolve("legacy-0-4")
        Files.createDirectories(taskDirectory)
        val legacy = """{"schemaVersion":"0.4.2","folderName":"legacy","status":"READY"}"""
        val target = taskDirectory.resolve(ManifestStore.FILE_NAME)
        Files.writeString(target, legacy)

        val store = ManifestStore()
        assertThrows(IllegalArgumentException::class.java) { store.load(taskDirectory) }
        assertEquals(legacy, Files.readString(target))
        assertEquals(listOf(taskDirectory), store.scan(temporary).unsupportedDirectories)
    }

    @Test
    fun `manifest from another patch release is compatible`() {
        val taskDirectory = temporary.resolve("compatible-patch")
        Files.createDirectories(taskDirectory)
        Files.writeString(
            taskDirectory.resolve(ManifestStore.FILE_NAME),
            """{"schemaVersion":"2.0.7","folderName":"compatible","taskDirectoryName":"compatible","featureBranch":"feature/compatible","createdAt":"2026-08-09 00:00:00","updatedAt":"2026-08-09 00:00:00","lifecycleStatus":"ACTIVE","services":[]}""",
        )

        val store = ManifestStore()
        val manifest = store.load(taskDirectory)
        assertEquals("2.0.7", manifest.schemaVersion)
        store.save(taskDirectory, manifest)
        assertEquals(CURRENT_TASK_MANIFEST_SCHEMA_VERSION, store.load(taskDirectory).schemaVersion)
    }

    @Test
    fun `0 12 manifest is rejected without rewriting`() {
        val taskDirectory = temporary.resolve("legacy-0-12")
        Files.createDirectories(taskDirectory)
        val legacy = """{"schemaVersion":"0.12.7","folderName":"legacy","taskDirectoryName":"legacy","featureBranch":"feature/legacy","createdAt":"2026-08-09 00:00:00","updatedAt":"2026-08-09 00:00:00","lifecycleStatus":"ACTIVE","services":[]}"""
        val target = taskDirectory.resolve(ManifestStore.FILE_NAME)
        Files.writeString(target, legacy)

        val store = ManifestStore()
        assertThrows(IllegalArgumentException::class.java) { store.load(taskDirectory) }
        assertEquals(legacy, Files.readString(target))
    }

    @Test
    fun `0 9 manifest is rejected byte for byte without migration`() {
        val taskDirectory = temporary.resolve("legacy-0-9")
        Files.createDirectories(taskDirectory)
        val legacy = """{"schemaVersion":"0.9.11","folderName":"legacy","taskDirectoryName":"legacy","featureBranch":"feature/legacy","createdAt":"2026-08-09 00:00:00","updatedAt":"2026-08-09 00:00:00","services":[]}"""
        val target = taskDirectory.resolve(ManifestStore.FILE_NAME)
        Files.writeString(target, legacy)

        val store = ManifestStore()
        assertThrows(IllegalArgumentException::class.java) { store.load(taskDirectory) }
        assertEquals(legacy, Files.readString(target))
        assertEquals(listOf(taskDirectory), store.scan(temporary).unsupportedDirectories)
    }

    @Test
    fun `rejects manifests without a schema version`() {
        val taskDirectory = temporary.resolve("missing-version")
        Files.createDirectories(taskDirectory)
        Files.writeString(taskDirectory.resolve(ManifestStore.FILE_NAME), "{}")

        assertThrows(IllegalArgumentException::class.java) { ManifestStore().load(taskDirectory) }
    }

    @Test
    fun `corrupt manifest is reported without hiding valid tasks`() {
        val store = ManifestStore()
        val validDirectory = temporary.resolve("valid")
        store.save(
            validDirectory,
            TaskManifest(
                folderName = "valid",
                taskDirectoryName = "valid",
                featureBranch = "feature/valid",
                createdAt = "2026-08-08T00:00:00Z",
                updatedAt = "2026-08-08T00:00:00Z",
                lifecycleStatus = TaskLifecycleStatus.ACTIVE,
                services = emptyList(),
            ),
        )
        val brokenDirectory = temporary.resolve("broken")
        Files.createDirectories(brokenDirectory)
        Files.writeString(brokenDirectory.resolve(ManifestStore.FILE_NAME), "{not-json")

        val scan = store.scan(temporary)

        kotlin.test.assertEquals(listOf("valid"), scan.current.map { it.second.folderName })
        kotlin.test.assertEquals(setOf(brokenDirectory), scan.failures.keys)
    }

    @Test
    fun `removed source repository task is preserved and reported as a read failure`() {
        val taskDirectory = temporary.resolve("source-task")
        Files.createDirectories(taskDirectory)
        val original = """{"schemaVersion":"0.7.0","folderName":"source","taskDirectoryName":"source","featureBranch":"feature/source","createdAt":"2026-08-12 00:00:00","updatedAt":"2026-08-12 00:00:00","services":[{"repositoryId":"repo","serviceName":"source","repositoryPath":"D:/repo","worktreePath":"D:/repo","developmentTool":"INTELLIJ_IDEA","branch":"feature/source","strategy":"SOURCE_REPOSITORY"}]}"""
        val target = taskDirectory.resolve(ManifestStore.FILE_NAME)
        Files.writeString(target, original)

        val store = ManifestStore()
        val error = assertThrows(IllegalArgumentException::class.java) { store.load(taskDirectory) }
        val scan = store.scan(temporary)

        kotlin.test.assertTrue(error.message.orEmpty().contains("版本不受支持"))
        assertEquals(listOf(taskDirectory), scan.unsupportedDirectories)
        kotlin.test.assertTrue(scan.current.isEmpty())
        assertEquals(original, Files.readString(target))
    }

    @Test
    fun `ordinary early 0_7 task is rejected without migration`() {
        val taskDirectory = temporary.resolve("ordinary-with-source-metadata")
        Files.createDirectories(taskDirectory)
        Files.writeString(
            taskDirectory.resolve(ManifestStore.FILE_NAME),
            """{"schemaVersion":"0.7.0","folderName":"ordinary","taskDirectoryName":"ordinary","featureBranch":"feature/ordinary","createdAt":"2026-08-12 00:00:00","updatedAt":"2026-08-12 00:00:00","services":[{"repositoryId":"repo","serviceName":"ordinary","repositoryPath":"D:/repo","worktreePath":"D:/task/ordinary","developmentTool":"INTELLIJ_IDEA","branch":"feature/ordinary","strategy":"STANDARD_WORKTREE","sourcePreviousBranch":null}]}""",
        )

        val store = ManifestStore()
        assertThrows(IllegalArgumentException::class.java) { store.load(taskDirectory) }
        kotlin.test.assertTrue(Files.readString(taskDirectory.resolve(ManifestStore.FILE_NAME)).contains("sourcePreviousBranch"))
    }
}
