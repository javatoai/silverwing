package com.snowball.silverwing.core

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.io.OutputStream
import java.lang.reflect.Proxy
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.stream.Stream
import kotlin.test.*

class CommandRunnerTimeoutDiagnosticsTest {
    @Test fun `timeout identifies each blocked phase without exposing command or input`() {
        for (phase in listOf("进程等待", "标准输入", "标准输出", "错误输出")) {
            val inputGate = if (phase == "标准输入") CountDownLatch(1) else null
            val stdoutGate = if (phase == "标准输出") CountDownLatch(1) else null
            val stderrGate = if (phase == "错误输出") CountDownLatch(1) else null
            val process = ControlledProcess(
                alive = phase == "进程等待",
                stdin = inputGate?.let { gate -> object : OutputStream() {
                    override fun write(value: Int) { gate.await() }
                    override fun close() { gate.countDown() }
                } } ?: ByteArrayOutputStream(),
                stdout = gatedInput(stdoutGate), stderr = gatedInput(stderrGate),
            )
            val result = ProcessCommandRunner({ process }, isWindows = true).runWithInput(
                listOf("fake-git", "https://user:secret@example.invalid/repo", "--private-argument"),
                "private-input", timeout = Duration.ofMillis(300),
                environment = mapOf("PRIVATE_VALUE" to "private-environment"),
            )
            assertEquals(ProcessCommandRunner.TIMEOUT_EXIT_CODE, result.exitCode)
            val diagnostic = result.stderr.lineSequence().single { it.startsWith("超时诊断：") }
            assertTrue(diagnostic.startsWith("超时诊断：阶段=$phase；"), diagnostic)
            assertTrue(diagnostic.contains("启动耗时="), diagnostic)
            assertTrue(diagnostic.contains("父进程存活=${phase == "进程等待"}"), diagnostic)
            assertTrue(diagnostic.contains("存活后代PID=无"), diagnostic)
            for (secret in listOf("fake-git", "secret", "example.invalid", "private-argument", "private-input", "private-environment")) {
                assertFalse(diagnostic.contains(secret), diagnostic)
            }
            assertFalse(process.isAlive)
            for (gate in listOfNotNull(inputGate, stdoutGate, stderrGate)) assertEquals(0L, gate.count)
        }
    }

    private fun gatedInput(gate: CountDownLatch?): InputStream =
        gate?.let { object : InputStream() {
            override fun read(): Int { it.await(); return -1 }
            override fun close() { it.countDown() }
        } } ?: ByteArrayInputStream(byteArrayOf())

    /** No OS process is launched or killed; the proxy supports only runner-used operations. */
    private class ControlledProcess(
        var alive: Boolean,
        private val stdin: OutputStream,
        private val stdout: InputStream,
        private val stderr: InputStream,
    ) : Process() {
        private val handle = Proxy.newProxyInstance(ProcessHandle::class.java.classLoader,
            arrayOf(ProcessHandle::class.java)) { proxy, method, args ->
            when (method.name) {
                "children" -> Stream.empty<ProcessHandle>()
                "pid" -> 101L
                "isAlive" -> alive
                "destroyForcibly" -> { alive = false; true }
                "equals" -> proxy === args?.firstOrNull()
                "hashCode" -> System.identityHashCode(proxy)
                else -> error("Unexpected process-handle operation: ${method.name}")
            }
        } as ProcessHandle
        override fun getInputStream() = stdout
        override fun getErrorStream() = stderr
        override fun getOutputStream() = stdin
        override fun toHandle() = handle
        override fun isAlive() = alive
        override fun exitValue(): Int { check(!alive); return 0 }
        override fun waitFor(): Int = error("Unbounded waiting is not supported by this fixture")
        override fun waitFor(timeout: Long, unit: TimeUnit): Boolean {
            if (alive) unit.sleep(timeout)
            return !alive
        }
        override fun destroy() { alive = false }
        override fun destroyForcibly(): Process { alive = false; return this }
    }
}
