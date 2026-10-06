package com.snowball.silverwing.core

import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.security.MessageDigest
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

class ProcessCommandRunnerInputTest {
    @TempDir lateinit var root: Path
    private val largeInput = "支付😀\n".repeat(300_000)

    @Test fun `large stdout and stderr before reading large UTF8 stdin do not deadlock`() {
        val pidFile = root.resolve("exchange.pid")
        try {
            val result = boundedRun {
                ProcessCommandRunner().runWithInput(fixtureCommand("exchange", pidFile), largeInput, timeout = Duration.ofSeconds(8))
            }
            val bytes = largeInput.toByteArray(StandardCharsets.UTF_8)
            assertEquals(0, result.exitCode, result.stderr.take(300))
            assertEquals("E".repeat(INPUT_FIXTURE_OUTPUT_SIZE), result.stderr)
            assertEquals("O".repeat(INPUT_FIXTURE_OUTPUT_SIZE) + "\nINPUT_BYTES=${bytes.size}\nINPUT_SHA256=${inputHash(bytes)}\n", result.stdout)
            assertTrue(awaitExit(readPid(pidFile)))
        } finally { cleanup(pidFile) }
    }

    @Test fun `process that never reads stdin still obeys the command deadline`() {
        val pidFile = root.resolve("blocked-writer.pid")
        val start = System.nanoTime()
        try {
            val result = boundedRun {
                ProcessCommandRunner().runWithInput(fixtureCommand("never-read", pidFile), largeInput, timeout = Duration.ofSeconds(2))
            }
            val elapsed = Duration.ofNanos(System.nanoTime() - start).toMillis()
            assertEquals(ProcessCommandRunner.TIMEOUT_EXIT_CODE, result.exitCode)
            assertTrue(elapsed < 4_000, "two-second timeout took $elapsed ms")
            assertTrue(awaitExit(readPid(pidFile)), "timed-out process remains alive")
        } finally { cleanup(pidFile) }
    }

    @Test fun `interruption during blocked stdin write terminates the parent and child`() {
        val parentPidFile = root.resolve("parent.pid")
        val childPidFile = root.resolve("child.pid")
        val failure = AtomicReference<Throwable?>()
        val interruptedAfterRun = AtomicBoolean(true)
        val worker = Thread {
            runCatching {
                ProcessCommandRunner().runWithInput(fixtureCommand("tree", parentPidFile, childPidFile),
                    largeInput, timeout = Duration.ofMinutes(1))
            }.onFailure(failure::set)
            interruptedAfterRun.set(Thread.currentThread().isInterrupted)
        }.apply { isDaemon = true }
        try {
            worker.start()
            val parent = awaitPid(parentPidFile)
            val child = awaitPid(childPidFile)
            assertTrue(ProcessHandle.of(parent).orElseThrow().isAlive)
            assertTrue(ProcessHandle.of(child).orElseThrow().isAlive)
            worker.interrupt(); worker.join(5_000)
            assertFalse(worker.isAlive, "blocked input writer prevented command cancellation")
            assertIs<InterruptedException>(failure.get())
            assertFalse(interruptedAfterRun.get(), "compensation commands must remain possible on the caller thread")
            assertTrue(awaitExit(parent), "cancelled parent remains alive")
            assertTrue(awaitExit(child), "cancelled child remains alive")
        } finally {
            cleanup(childPidFile, parentPidFile)
            worker.interrupt(); worker.join(2_000)
        }
    }

    /** A regression cannot hang the test suite; fixture PIDs are killed by each test's finally. */
    private fun boundedRun(block: () -> CommandResult): CommandResult {
        val executor = Executors.newSingleThreadExecutor { task -> Thread(task, "command-input-test").apply { isDaemon = true } }
        return try { executor.submit<CommandResult>(block).get(10, TimeUnit.SECONDS) }
        finally { executor.shutdownNow() }
    }

    private fun readPid(path: Path): Long = Files.readString(path).trim().toLong()
    private fun awaitPid(path: Path): Long {
        val deadline = System.nanoTime() + Duration.ofSeconds(5).toNanos()
        while (System.nanoTime() < deadline) {
            runCatching { readPid(path) }.getOrNull()?.let { return it }
            Thread.sleep(10)
        }
        fail("fixture PID was not recorded: $path")
    }
    private fun awaitExit(pid: Long): Boolean {
        val deadline = System.nanoTime() + Duration.ofSeconds(2).toNanos()
        while (System.nanoTime() < deadline) {
            if (ProcessHandle.of(pid).map { !it.isAlive }.orElse(true)) return true
            Thread.sleep(10)
        }
        return false
    }
    private fun cleanup(vararg pidFiles: Path) {
        pidFiles.forEach { file -> runCatching {
            ProcessHandle.of(readPid(file)).ifPresent { process ->
                process.descendants().toList().asReversed().forEach { it.destroyForcibly() }
                process.destroyForcibly()
            }
        } }
    }
}

private const val INPUT_FIXTURE_OUTPUT_SIZE = 1_048_576

private fun fixtureJava(): String = Path.of(System.getProperty("java.home"), "bin",
    if (System.getProperty("os.name").startsWith("Windows", true)) "java.exe" else "java").toString()

private fun fixtureCommand(mode: String, pidFile: Path, childPidFile: Path? = null): List<String> = buildList {
    addAll(listOf(fixtureJava(), "-cp", System.getProperty("java.class.path"), CommandInputFixtureMain::class.java.name, mode, pidFile.toString()))
    childPidFile?.let { add(it.toString()) }
}

private fun inputHash(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(bytes)
    .joinToString("") { "%02x".format(it.toInt() and 0xff) }

/** Child-JVM fixture deliberately uses real OS pipes and never invokes a network service. */
object CommandInputFixtureMain {
    @JvmStatic fun main(args: Array<String>) {
        Files.writeString(Path.of(args[1]), ProcessHandle.current().pid().toString())
        when (args[0]) {
            "exchange" -> {
                System.out.write(ByteArray(INPUT_FIXTURE_OUTPUT_SIZE) { 'O'.code.toByte() }); System.out.flush()
                System.err.write(ByteArray(INPUT_FIXTURE_OUTPUT_SIZE) { 'E'.code.toByte() }); System.err.flush()
                val bytes = System.`in`.readBytes()
                print("\nINPUT_BYTES=${bytes.size}\nINPUT_SHA256=${inputHash(bytes)}\n")
            }
            "tree" -> {
                ProcessBuilder(fixtureCommand("never-read", Path.of(args[2]))).inheritIO().start()
                Thread.sleep(60_000)
            }
            "never-read" -> Thread.sleep(60_000)
            else -> error("Unknown fixture mode")
        }
    }
}
