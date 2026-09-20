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
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, null)

    override fun runStreaming(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: (CommandOutputLine) -> Unit,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, onOutput)

    private fun runInternal(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: ((CommandOutputLine) -> Unit)?,
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
        process.outputStream.close()

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
            val finished = waitForProcess(process, deadline, observedProcesses)
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

    private fun waitForProcess(
        process: Process,
        deadline: Long,
        observedProcesses: MutableSet<ProcessHandle>,
    ): Boolean {
        while (true) {
            observeDescendants(process, observedProcesses)
            val remaining = remainingNanos(deadline)
            if (remaining <= 0) return !process.isAlive
            if (process.waitFor(minOf(remaining, PROCESS_WAIT_POLL_NANOS), TimeUnit.NANOSECONDS)) {
                observeDescendants(process, observedProcesses)
                return true
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
        private val PROCESS_WAIT_POLL_NANOS = TimeUnit.MILLISECONDS.toNanos(50)
    }
}
