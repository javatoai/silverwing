package com.snowball.silverwing.core

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFileAttributeView
import java.nio.file.attribute.PosixFilePermission.GROUP_EXECUTE
import java.nio.file.attribute.PosixFilePermission.GROUP_READ
import java.nio.file.attribute.PosixFilePermission.OWNER_EXECUTE
import java.nio.file.attribute.PosixFilePermission.OWNER_READ
import java.nio.file.attribute.PosixFilePermission.OWNER_WRITE
import java.nio.file.attribute.PosixFilePermission.OTHERS_EXECUTE
import java.nio.file.attribute.PosixFilePermission.OTHERS_READ
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class GenbuExecutableTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `non executable candidates do not hide a runnable Genbu on POSIX`() {
        org.junit.jupiter.api.Assumptions.assumeTrue(
            Files.getFileAttributeView(temporary, PosixFileAttributeView::class.java) != null,
            "requires POSIX execute permissions",
        )
        val bundledDirectory = Files.createDirectories(temporary.resolve("bundled"))
        val unusable = Files.createFile(bundledDirectory.resolve("genbu"))
        Files.setPosixFilePermissions(unusable, setOf(OWNER_READ, OWNER_WRITE))
        val executable = createExecutable(temporary.resolve("runnable-genbu"))
        val runner = RecordingRunner(CommandResult(0, "$unusable\n$executable\n", ""))
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { null },
            runner = runner,
            osName = "Mac OS X",
            bundledDirectories = { listOf(bundledDirectory) },
        )

        assertEquals(executable.toString(), genbu.probe())
        assertEquals(1, runner.calls)
    }

    @Test
    fun `configured absolute executable wins without probing`() {
        val executable = createExecutable(temporary.resolve("configured-genbu.exe"))
        val runner = RecordingRunner(CommandResult(1, "", "must not run"))
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { executable.toString() },
            runner = runner,
            osName = "Windows 11",
            bundledDirectories = { emptyList() },
        )

        assertEquals(executable.toString(), genbu.resolve())
        assertEquals(GenbuCommandSource.CONFIGURED, genbu.source())
        assertEquals(0, runner.calls)
    }

    @Test
    fun `portable executable in a candidate directory is automatically detected`() {
        val directory = Files.createDirectories(temporary.resolve("Downloads"))
        val executable = createExecutable(directory.resolve("genbu.exe"))
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { null },
            runner = RecordingRunner(CommandResult(1, "", "not on PATH")),
            osName = "Windows 11",
            bundledDirectories = { listOf(directory) },
        )

        assertEquals(executable.toAbsolutePath().normalize().toString(), genbu.probe())
        assertEquals(GenbuCommandSource.PROBED, genbu.source())
    }

    @Test
    fun `PATH probing accepts only an existing absolute executable`() {
        val executable = createExecutable(temporary.resolve("genbu.exe"))
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { null },
            runner = RecordingRunner(CommandResult(0, "C:\\missing\\genbu.exe\n$executable\n", "")),
            osName = "Windows 11",
            bundledDirectories = { emptyList() },
        )

        assertEquals(executable.toString(), genbu.probe())
        assertEquals(GenbuCommandSource.PROBED, genbu.source())
    }

    @Test
    fun `failed automatic detection retains the bare command fallback`() {
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { null },
            runner = RecordingRunner(CommandResult(1, "", "not found")),
            osName = "Windows 11",
            bundledDirectories = { emptyList() },
        )

        assertEquals("genbu.exe", genbu.probe())
        assertEquals(GenbuCommandSource.PATH_FALLBACK, genbu.source())
    }

    @Test
    fun `detect rescans locations and ignores a still-valid configured path`() {
        val configured = createExecutable(temporary.resolve("configured-genbu.exe"))
        val directory = Files.createDirectories(temporary.resolve("Downloads"))
        val moved = createExecutable(directory.resolve("genbu.exe"))
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { configured.toString() },
            runner = RecordingRunner(CommandResult(1, "", "not found")),
            osName = "Windows 11",
            bundledDirectories = { listOf(directory) },
        )

        assertEquals(moved.toAbsolutePath().normalize().toString(), genbu.detect())
        assertEquals(configured.toString(), genbu.resolve())
        assertEquals(GenbuCommandSource.CONFIGURED, genbu.source())
    }

    @Test
    fun `macOS probing uses a zsh login shell`() {
        val commands = mutableListOf<List<String>>()
        val genbu = ConfiguredGenbuExecutable(
            configuredPath = { null },
            runner = object : CommandRunner {
                override fun run(
                    command: List<String>,
                    workingDirectory: Path?,
                    timeout: Duration,
                    environment: Map<String, String>,
                ): CommandResult {
                    commands += command
                    return CommandResult(1, "", "not found")
                }
            },
            osName = "Mac OS X",
            bundledDirectories = { emptyList() },
        )

        genbu.probe()

        assertEquals(listOf(listOf("/bin/zsh", "-lc", "command -v genbu")), commands)
    }

    @Test
    fun `manual path normalization rejects relative and missing files`() {
        assertFailsWith<IllegalArgumentException> { normalizeGenbuExecutablePath("genbu.exe") }
        assertFailsWith<IllegalArgumentException> { normalizeGenbuExecutablePath(temporary.resolve("missing.exe").toString()) }
        assertEquals(null, normalizeGenbuExecutablePath(" "))
    }

    private fun createExecutable(path: Path): Path {
        Files.createFile(path)
        if (Files.getFileAttributeView(path, PosixFileAttributeView::class.java) != null) {
            Files.setPosixFilePermissions(
                path,
                setOf(
                    OWNER_READ,
                    OWNER_WRITE,
                    OWNER_EXECUTE,
                    GROUP_READ,
                    GROUP_EXECUTE,
                    OTHERS_READ,
                    OTHERS_EXECUTE,
                ),
            )
        } else {
            // Windows has no POSIX permission view; .exe files are executable by type.
            path.toFile().setExecutable(true, false)
        }
        check(Files.isExecutable(path)) { "Test fixture was not executable: $path" }
        return path
    }

    private class RecordingRunner(private val result: CommandResult) : CommandRunner {
        var calls = 0

        override fun run(
            command: List<String>,
            workingDirectory: Path?,
            timeout: Duration,
            environment: Map<String, String>,
        ): CommandResult {
            calls += 1
            return result
        }
    }
}
