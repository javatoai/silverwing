package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicReference
import kotlin.io.path.createDirectories
import kotlin.io.path.exists
import kotlin.io.path.readText
import kotlin.test.fail
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue
import org.junit.jupiter.api.io.TempDir

class CommandRunnerInterruptionTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `timeout also bounds inherited output pipe readers`() {
        val start = System.nanoTime()
        val childPidFile = temporary.resolve("child.pid")
        val result = ProcessCommandRunner().run(
            command = listOf(
                javaExecutable(),
                "-cp",
                System.getProperty("java.class.path"),
                ExitingParentProcess::class.java.name,
            ),
            timeout = Duration.ofSeconds(1),
            environment = mapOf("SILVERWING_TEST_CHILD_PID_OUT" to childPidFile.toString()),
        )
        val elapsed = Duration.ofNanos(System.nanoTime() - start).toMillis()
        val childPid = awaitChildPid(childPidFile)

        assertEquals(ProcessCommandRunner.TIMEOUT_EXIT_CODE, result.exitCode)
        assertTrue(elapsed < 2_500, "one-second timeout took $elapsed ms")
        assertTrue(awaitProcessExit(childPid), "inherited output child $childPid is still alive")
    }

    @Test
    fun `parent exit waits for a short lived descendant before declaring timeout`() {
        val start = System.nanoTime()

        val result = ProcessCommandRunner().run(
            command = listOf(
                javaExecutable(),
                "-cp",
                System.getProperty("java.class.path"),
                ExitingParentWithShortLivedChildProcess::class.java.name,
            ),
            timeout = Duration.ofSeconds(2),
        )
        val elapsed = Duration.ofNanos(System.nanoTime() - start).toMillis()

        assertEquals(0, result.exitCode)
        assertTrue(elapsed < 1_800, "short-lived descendant took $elapsed ms")
    }

    private fun awaitChildPid(path: Path): Long {
        val deadline = System.nanoTime() + Duration.ofSeconds(2).toNanos()
        while (System.nanoTime() < deadline) {
            if (path.exists()) return path.readText().trim().toLong()
            Thread.sleep(10)
        }
        fail("child PID was not recorded: $path")
    }

    private fun awaitProcessExit(pid: Long): Boolean {
        val deadline = System.nanoTime() + Duration.ofSeconds(2).toNanos()
        while (System.nanoTime() < deadline) {
            if (ProcessHandle.of(pid).map { !it.isAlive }.orElse(true)) return true
            Thread.sleep(10)
        }
        return false
    }

    @Test
    fun `interruption terminates the child and leaves compensation thread usable`() {
        val failure = AtomicReference<Throwable?>()
        val worker = Thread {
            runCatching {
                ProcessCommandRunner().run(
                    command = listOf(
                        Path.of(System.getProperty("java.home"), "bin", executable("java")).toString(),
                        "-cp",
                        System.getProperty("java.class.path"),
                        SleepingProcessMain::class.java.name,
                    ),
                    timeout = Duration.ofMinutes(1),
                )
            }.onFailure(failure::set)
        }

        worker.start()
        Thread.sleep(500)
        worker.interrupt()
        worker.join(5_000)

        assertFalse(worker.isAlive, "interrupted command worker should terminate promptly")
        assertIs<InterruptedException>(failure.get())
        assertFalse(worker.isInterrupted, "caller must be able to run compensation commands on the same thread")
    }

    private fun executable(name: String): String =
        if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) "$name.exe" else name
}

private fun javaExecutable(): String =
    Path.of(
        System.getProperty("java.home"),
        "bin",
        if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) "java.exe" else "java",
    ).toString()

object ExitingParentProcess {
    @JvmStatic
    fun main(args: Array<String>) {
        val child = ProcessBuilder(
            javaExecutable(),
            "-cp",
            System.getProperty("java.class.path"),
            HoldingOutputProcess::class.java.name,
        ).inheritIO().start()
        System.getenv("SILVERWING_TEST_CHILD_PID_OUT")?.let { path ->
            Path.of(path).parent?.createDirectories()
            java.nio.file.Files.writeString(Path.of(path), child.pid().toString())
        }
        Thread.sleep(150)
    }
}

object HoldingOutputProcess {
    @JvmStatic
    fun main(args: Array<String>) {
        Thread.sleep(4_000)
    }
}

object ExitingParentWithShortLivedChildProcess {
    @JvmStatic
    fun main(args: Array<String>) {
        ProcessBuilder(
            javaExecutable(),
            "-cp",
            System.getProperty("java.class.path"),
            ShortLivedChildProcess::class.java.name,
        ).inheritIO().start()
        Thread.sleep(200)
    }
}

object ShortLivedChildProcess {
    @JvmStatic
    fun main(args: Array<String>) {
        Thread.sleep(600)
    }
}

object SleepingProcessMain {
    @JvmStatic
    fun main(args: Array<String>) {
        Thread.sleep(60_000)
    }
}
