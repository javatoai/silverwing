package com.snowball.silverwing.core

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.time.Duration
import java.time.Instant
import java.util.Optional
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit
import java.util.stream.Stream
import kotlin.test.*

class CommandRunnerDescendantLifecycleTest {
    @Test fun `child observed without a name is excluded after parent exits and it becomes conhost`() {
        val console = FakeHandle(200, null)
        val process = FakeProcess(listOf(console))
        process.onWait = {
            assertTrue(console.infoReads > 0, "unknown live child must have been observed before the transition")
            assertTrue(console.alive)
            process.alive = false
            console.command = "C:\\Windows\\System32\\conhost.exe"
            true
        }
        val result = ProcessCommandRunner({ process }, isWindows = true)
            .run(listOf("fake-git"), timeout = Duration.ofSeconds(1))
        assertEquals(0, result.exitCode)
        assertEquals(1, process.waitCalls, "once identified, console host must not prolong command completion")
        assertTrue(console.alive)
        assertEquals(0, console.destroyCalls)
        assertEquals(0, console.forceDestroyCalls)
        assertTrue(console.infoReads >= 2)
    }

    @Test fun `interruption reclassifies previously unknown child before destruction`() {
        val console = FakeHandle(200, null)
        val actual = FakeHandle(201, "helper.exe")
        val unknown = FakeHandle(202, null)
        val process = FakeProcess(listOf(console, actual, unknown))
        process.onWait = {
            assertTrue(console.infoReads > 0)
            assertTrue(actual.infoReads > 0)
            assertTrue(unknown.infoReads > 0)
            // No subsequent wait-loop iteration runs: teardown itself must reclassify it.
            process.alive = false
            console.command = "C:\\Windows\\System32\\CONHOST.EXE"
            throw InterruptedException("controlled cancellation")
        }
        assertFailsWith<InterruptedException> {
            ProcessCommandRunner({ process }, isWindows = true)
                .run(listOf("fake-git"), timeout = Duration.ofSeconds(1))
        }
        assertTrue(console.alive)
        assertEquals(0, console.destroyCalls)
        assertEquals(0, console.forceDestroyCalls)
        assertFalse(actual.alive)
        assertFalse(unknown.alive)
        assertEquals(1, actual.forceDestroyCalls)
        assertEquals(1, unknown.forceDestroyCalls)
    }

    @Test fun `known and still unknown live descendants remain tracked after parent exit and are killed on timeout`() {
        val actual = FakeHandle(201, "helper.exe")
        val unknown = FakeHandle(202, null)
        val process = FakeProcess(listOf(actual, unknown))
        process.onWait = {
            assertTrue(actual.infoReads > 0)
            assertTrue(unknown.infoReads > 0)
            process.alive = false
            true
        }
        val result = ProcessCommandRunner({ process }, isWindows = true)
            .run(listOf("fake-git"), timeout = Duration.ofMillis(100))
        assertEquals(ProcessCommandRunner.TIMEOUT_EXIT_CODE, result.exitCode)
        assertFalse(actual.alive)
        assertFalse(unknown.alive)
        assertEquals(1, actual.forceDestroyCalls)
        assertEquals(1, unknown.forceDestroyCalls)
    }

    @Test fun `an exited root cannot attach an unrelated tree through stale descendant enumeration`() {
        val unrelated = FakeHandle(300, "unrelated-application.exe")
        val process = FakeProcess(emptyList(), listOf(unrelated)).apply { alive = false }
        val result = ProcessCommandRunner({ process }, isWindows = true)
            .run(listOf("fake-git"), timeout = Duration.ofMillis(300))
        assertEquals(0, result.exitCode)
        assertEquals(0, process.rootHandle.descendantReads)
        assertEquals(0, process.rootHandle.childReads)
        assertTrue(unrelated.alive)
        assertEquals(0, unrelated.forceDestroyCalls)
    }

    @Test fun `timeout tears down confirmed children without querying a stale unrelated descendant tree`() {
        val actual = FakeHandle(201, "helper.exe")
        val unrelated = FakeHandle(300, "unrelated-application.exe")
        val process = FakeProcess(listOf(actual), listOf(unrelated))
        process.onWait = { process.alive = false; true }
        val result = ProcessCommandRunner({ process }, isWindows = true)
            .run(listOf("fake-git"), timeout = Duration.ofMillis(100))
        assertEquals(ProcessCommandRunner.TIMEOUT_EXIT_CODE, result.exitCode)
        assertFalse(actual.alive)
        assertEquals(0, process.rootHandle.descendantReads)
        assertTrue(unrelated.alive)
        assertEquals(0, unrelated.forceDestroyCalls)
    }

    @Test fun `confirmed child can create a grandchild after the root exits and both are cleaned up`() {
        val grandchild = FakeHandle(202, "late-helper.exe")
        var grandchildren = emptyList<FakeHandle>()
        val child = FakeHandle(201, "helper.exe", childSupplier = { grandchildren.stream().map { it as ProcessHandle } })
        val process = FakeProcess(listOf(child))
        process.onWait = {
            process.alive = false
            grandchildren = listOf(grandchild)
            true
        }
        val result = ProcessCommandRunner({ process }, isWindows = true)
            .run(listOf("fake-git"), timeout = Duration.ofMillis(100))
        assertEquals(ProcessCommandRunner.TIMEOUT_EXIT_CODE, result.exitCode)
        assertFalse(child.alive)
        assertFalse(grandchild.alive)
        assertEquals(1, child.forceDestroyCalls)
        assertEquals(1, grandchild.forceDestroyCalls)
        assertEquals(0, child.descendantReads)
    }

    private class FakeProcess(private val children: List<FakeHandle>, private val staleDescendants: List<FakeHandle> = emptyList()) : Process() {
        var alive = true
        var waitCalls = 0
        var onWait: () -> Boolean = { false }
        private val stdin = ByteArrayOutputStream()
        private val stdout = ByteArrayInputStream("ready".toByteArray())
        private val stderr = ByteArrayInputStream(byteArrayOf())
        val rootHandle = FakeHandle(100, "git.exe", aliveState = { alive }, stop = { alive = false },
            childSupplier = { if (alive) children.stream().map { it as ProcessHandle } else Stream.empty() },
            descendantSupplier = { staleDescendants.stream().map { it as ProcessHandle } })
        override fun getOutputStream() = stdin
        override fun getInputStream() = stdout
        override fun getErrorStream() = stderr
        override fun waitFor(): Int { onWait(); return exitValue() }
        override fun waitFor(timeout: Long, unit: TimeUnit): Boolean { waitCalls++; return onWait() }
        override fun exitValue(): Int { check(!alive); return 0 }
        override fun isAlive() = alive
        override fun destroy() { alive = false }
        override fun destroyForcibly(): Process { alive = false; return this }
        override fun toHandle(): ProcessHandle = rootHandle
        override fun pid() = rootHandle.pid()
    }

    private class FakeHandle(
        private val id: Long,
        var command: String?,
        private val aliveState: (() -> Boolean)? = null,
        private val stop: () -> Unit = {},
        private val childSupplier: () -> Stream<ProcessHandle> = { Stream.empty() },
        private val descendantSupplier: () -> Stream<ProcessHandle> = { Stream.empty() },
    ) : ProcessHandle {
        var alive = true
        var infoReads = 0
        var destroyCalls = 0
        var forceDestroyCalls = 0
        var childReads = 0
        var descendantReads = 0
        override fun pid() = id
        override fun parent(): Optional<ProcessHandle> = Optional.empty()
        override fun children(): Stream<ProcessHandle> { childReads++; return childSupplier() }
        override fun descendants(): Stream<ProcessHandle> { descendantReads++; return descendantSupplier() }
        override fun isAlive() = aliveState?.invoke() ?: alive
        override fun supportsNormalTermination() = true
        override fun destroy(): Boolean { destroyCalls++; alive = false; stop(); return true }
        override fun destroyForcibly(): Boolean { forceDestroyCalls++; alive = false; stop(); return true }
        override fun onExit(): CompletableFuture<ProcessHandle> = CompletableFuture.completedFuture(this)
        override fun compareTo(other: ProcessHandle) = id.compareTo(other.pid())
        override fun info(): ProcessHandle.Info {
            infoReads++
            val currentCommand = command
            return object : ProcessHandle.Info {
                override fun command(): Optional<String> = Optional.ofNullable(currentCommand)
                override fun commandLine(): Optional<String> = Optional.empty()
                override fun arguments(): Optional<Array<String>> = Optional.empty()
                override fun startInstant(): Optional<Instant> = Optional.empty()
                override fun totalCpuDuration(): Optional<Duration> = Optional.empty()
                override fun user(): Optional<String> = Optional.empty()
            }
        }
    }
}
