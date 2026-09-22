package com.snowball.silverwing.core

import java.nio.charset.StandardCharsets
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

enum class CommandOutputStream {
    STDOUT,
    STDERR,
}

data class CommandOutputLine(
    val stream: CommandOutputStream,
    val text: String,
)

interface CommandRunner {
    fun run(
        command: List<String>,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
    ): CommandResult

    /**
     * 以显式 UTF-8 标准输入启动命令。
     *
     * 绝大多数既有命令不需要输入；默认抛错实现既保持轻量测试替身兼容，也要求传递
     * 敏感结构化输入的调用方明确选择支持标准输入的执行器。
     */
    fun runWithInput(
        command: List<String>,
        input: String,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
    ): CommandResult = throw UnsupportedOperationException("当前命令执行器不支持标准输入")
}

interface StreamingCommandRunner : CommandRunner {
    fun runStreaming(
        command: List<String>,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
        onOutput: (CommandOutputLine) -> Unit,
    ): CommandResult
}

class ProcessCommandRunner : StreamingCommandRunner {
    override fun run(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, null, null)

    override fun runWithInput(
        command: List<String>,
        input: String,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, null, input)

    override fun runStreaming(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: (CommandOutputLine) -> Unit,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, onOutput, null)

    private fun runInternal(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: ((CommandOutputLine) -> Unit)?,
        input: String?,
    ): CommandResult {
        require(command.isNotEmpty()) { "命令不能为空" }
        require(!timeout.isNegative && !timeout.isZero) { "timeout must be greater than zero" }
        val deadline = deadlineNanos(timeout)
        val process = ProcessBuilder(command)
            .directory(workingDirectory?.toFile())
            .apply {
                environment().putAll(environment)
                redirectInput(ProcessBuilder.Redirect.PIPE)
            }
            .start()
        if (input == null) {
            process.outputStream.close()
        } else {
            process.outputStream.bufferedWriter(StandardCharsets.UTF_8).use { writer -> writer.write(input) }
        }

        val executor = Executors.newFixedThreadPool(2)
        val observedProcesses = linkedSetOf<ProcessHandle>()
        var stdoutFuture: Future<String>? = null
        var stderrFuture: Future<String>? = null
        return try {
            stdoutFuture = executor.submit<String> {
                if (onOutput == null) {
                    process.inputStream.bufferedReader(StandardCharsets.UTF_8).use { it.readText() }
                } else {
                    readStreamingOutput(process.inputStream, CommandOutputStream.STDOUT, onOutput)
                }
            }
            stderrFuture = executor.submit<String> {
                if (onOutput == null) {
                    process.errorStream.bufferedReader(StandardCharsets.UTF_8).use { it.readText() }
                } else {
                    readStreamingOutput(process.errorStream, CommandOutputStream.STDERR, onOutput)
                }
            }
            val finished = waitForProcessTree(process, deadline, observedProcesses)
            if (!finished) {
                timeoutResult(process, stdoutFuture, stderrFuture, timeout, observedProcesses)
            } else {
                CommandResult(
                    exitCode = process.exitValue(),
                    stdout = stdoutFuture!!.get(remainingNanos(deadline), TimeUnit.NANOSECONDS),
                    stderr = stderrFuture!!.get(remainingNanos(deadline), TimeUnit.NANOSECONDS),
                )
            }
        } catch (_: TimeoutException) {
            timeoutResult(process, stdoutFuture, stderrFuture, timeout, observedProcesses)
        } catch (error: InterruptedException) {
            destroyProcessTree(process, observedProcesses)
            if (!process.waitFor(2, TimeUnit.SECONDS)) process.destroyForcibly()
            forceDestroyProcessTree(process, observedProcesses)
            closeProcessStreams(process)
            throw error
        } finally {
            executor.shutdownNow()
        }
    }

    private fun readStreamingOutput(
        stream: java.io.InputStream,
        outputStream: CommandOutputStream,
        onOutput: (CommandOutputLine) -> Unit,
    ): String = buildString {
        stream.bufferedReader(StandardCharsets.UTF_8).useLines { lines ->
            lines.forEach { line ->
                onOutput(CommandOutputLine(outputStream, line))
                append(line).append('\n')
            }
        }
    }

    private fun timeoutResult(
        process: Process,
        stdoutFuture: Future<String>?,
        stderrFuture: Future<String>?,
        timeout: Duration,
        observedProcesses: Set<ProcessHandle>,
    ): CommandResult {
        forceDestroyProcessTree(process, observedProcesses)
        closeProcessStreams(process)
        return CommandResult(
            exitCode = TIMEOUT_EXIT_CODE,
            stdout = completedOutput(stdoutFuture),
            stderr = "命令执行超时（${timeout.seconds} 秒）\n" + completedOutput(stderrFuture),
        )
    }

    private fun completedOutput(future: Future<String>?): String {
        if (future == null || !future.isDone) {
            future?.cancel(true)
            return ""
        }
        return runCatching { future.get() }.getOrDefault("")
    }

    private fun closeProcessStreams(process: Process) {
        runCatching { process.inputStream.close() }
        runCatching { process.errorStream.close() }
        runCatching { process.outputStream.close() }
    }

    private fun deadlineNanos(timeout: Duration): Long {
        val now = System.nanoTime()
        val timeoutNanos = timeout.toNanos()
        return if (timeoutNanos > 0 && now > Long.MAX_VALUE - timeoutNanos) Long.MAX_VALUE else now + timeoutNanos
    }

    private fun remainingNanos(deadline: Long): Long =
        if (deadline == Long.MAX_VALUE) Long.MAX_VALUE else (deadline - System.nanoTime()).coerceAtLeast(0)

    private fun waitForProcessTree(
        process: Process,
        deadline: Long,
        observedProcesses: MutableSet<ProcessHandle>,
    ): Boolean {
        while (true) {
            observeDescendants(process, observedProcesses)
            if (!process.isAlive && observedProcesses.none { it.isAlive }) return true

            val remaining = remainingNanos(deadline)
            if (remaining <= 0) return false

            val pollNanos = minOf(remaining, PROCESS_WAIT_POLL_NANOS)
            if (process.isAlive) {
                process.waitFor(pollNanos, TimeUnit.NANOSECONDS)
            } else {
                TimeUnit.NANOSECONDS.sleep(pollNanos)
            }
        }
    }

    private fun observeDescendants(process: Process, observedProcesses: MutableSet<ProcessHandle>) {
        observedProcesses += process.toHandle().descendants().toList()
    }

    private fun destroyProcessTree(process: Process, observedProcesses: Set<ProcessHandle>) {
        process.toHandle().descendants().toList().asReversed().forEach { descendant ->
            descendant.destroy()
        }
        observedProcesses.toList().asReversed().forEach { descendant ->
            descendant.destroy()
        }
        process.destroy()
    }

    private fun forceDestroyProcessTree(process: Process, observedProcesses: Set<ProcessHandle>) {
        destroyProcessTree(process, observedProcesses)
        val handles = linkedSetOf<ProcessHandle>().apply {
            add(process.toHandle())
            addAll(observedProcesses)
            addAll(process.toHandle().descendants().toList())
            observedProcesses.forEach { addAll(it.descendants().toList()) }
        }
        handles.toList().asReversed().forEach { handle ->
            if (handle.isAlive) handle.destroyForcibly()
        }
    }

    companion object {
        const val TIMEOUT_EXIT_CODE = 124
        // Short-lived launchers can create a child and exit before a coarse poll sees it,
        // especially on loaded Windows CI workers. Preserve observed descendants so a later
        // timeout can reliably tear down the process tree.
        private val PROCESS_WAIT_POLL_NANOS = TimeUnit.MILLISECONDS.toNanos(10)
    }
}
