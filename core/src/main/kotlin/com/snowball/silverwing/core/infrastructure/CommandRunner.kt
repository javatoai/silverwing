package com.snowball.silverwing.core

import java.nio.charset.StandardCharsets
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ExecutionException
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
     * Runs a command while explicitly removing selected inherited environment variables.
     *
     * The overload preserves lightweight existing [CommandRunner] test doubles: only
     * [ProcessCommandRunner] needs to act on removals, while old implementations keep
     * their previous behavior unless a caller opts into this controlled path.
     */
    fun run(
        command: List<String>,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
        environmentToRemove: Set<String>,
    ): CommandResult = run(command, workingDirectory, timeout, environment)

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

    /** Equivalent controlled-environment overload for commands that receive standard input. */
    fun runWithInput(
        command: List<String>,
        input: String,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
        environmentToRemove: Set<String>,
    ): CommandResult = runWithInput(command, input, workingDirectory, timeout, environment)
}

interface StreamingCommandRunner : CommandRunner {
    fun runStreaming(
        command: List<String>,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
        onOutput: (CommandOutputLine) -> Unit,
    ): CommandResult

    /** Controlled-environment overload for live process output. */
    fun runStreaming(
        command: List<String>,
        workingDirectory: Path? = null,
        timeout: Duration = Duration.ofMinutes(10),
        environment: Map<String, String> = emptyMap(),
        onOutput: (CommandOutputLine) -> Unit,
        environmentToRemove: Set<String>,
    ): CommandResult = runStreaming(command, workingDirectory, timeout, environment, onOutput)
}

class ProcessCommandRunner internal constructor(
    private val processStarter: (ProcessBuilder) -> Process,
    private val isWindows: Boolean = System.getProperty("os.name").startsWith("Windows", ignoreCase = true),
) : StreamingCommandRunner {
    constructor() : this({ it.start() })
    override fun run(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, emptySet(), null, null)

    override fun run(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        environmentToRemove: Set<String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, environmentToRemove, null, null)

    override fun runWithInput(
        command: List<String>,
        input: String,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, emptySet(), null, input)

    override fun runWithInput(
        command: List<String>,
        input: String,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        environmentToRemove: Set<String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, environmentToRemove, null, input)

    override fun runStreaming(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: (CommandOutputLine) -> Unit,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, emptySet(), onOutput, null)

    override fun runStreaming(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: (CommandOutputLine) -> Unit,
        environmentToRemove: Set<String>,
    ): CommandResult = runInternal(command, workingDirectory, timeout, environment, environmentToRemove, onOutput, null)

    private fun runInternal(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        environmentToRemove: Set<String>,
        onOutput: ((CommandOutputLine) -> Unit)?,
        input: String?,
    ): CommandResult {
        require(command.isNotEmpty()) { "命令不能为空" }
        require(!timeout.isNegative && !timeout.isZero) { "timeout must be greater than zero" }
        val startedAt = System.nanoTime()
        val deadline = deadlineNanos(timeout)
        val process = processStarter(ProcessBuilder(command)
            .directory(workingDirectory?.toFile())
            .apply {
                val processEnvironment = environment()
                applyCommandEnvironment(processEnvironment, environment, environmentToRemove)
                redirectInput(ProcessBuilder.Redirect.PIPE)
            }
        )
        val startupMillis = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startedAt)
        // Drain both output pipes before writing input: a process may fill stdout/stderr
        // before it starts reading stdin. All three pipes must progress independently.
        val executor = Executors.newFixedThreadPool(3)
        val observedProcesses = linkedSetOf<ProcessHandle>()
        var stdinFuture: Future<*>? = null
        var stdoutFuture: Future<String>? = null
        var stderrFuture: Future<String>? = null
        var timeoutPhase = TimeoutPhase.PROCESS
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
            stdinFuture = executor.submit {
                if (input == null) process.outputStream.close()
                else process.outputStream.bufferedWriter(StandardCharsets.UTF_8).use { writer -> writer.write(input) }
            }
            val finished = waitForProcessTree(process, deadline, observedProcesses)
            if (!finished) {
                timeoutResult(process, stdoutFuture, stderrFuture, timeout, observedProcesses,
                    stdinFuture, timeoutPhase, startupMillis)
            } else {
                // Input delivery and output collection share the process deadline; neither
                // a blocked writer nor an inherited pipe can extend the command timeout.
                timeoutPhase = TimeoutPhase.INPUT
                stdinFuture!!.get(remainingNanos(deadline), TimeUnit.NANOSECONDS)
                val exitCode = process.exitValue()
                timeoutPhase = TimeoutPhase.STDOUT
                val stdout = stdoutFuture!!.get(remainingNanos(deadline), TimeUnit.NANOSECONDS)
                timeoutPhase = TimeoutPhase.STDERR
                val stderr = stderrFuture!!.get(remainingNanos(deadline), TimeUnit.NANOSECONDS)
                CommandResult(
                    exitCode = exitCode,
                    stdout = stdout,
                    stderr = stderr,
                )
            }
        } catch (_: TimeoutException) {
            timeoutResult(process, stdoutFuture, stderrFuture, timeout, observedProcesses,
                stdinFuture, timeoutPhase, startupMillis)
        } catch (error: Throwable) {
            forceDestroyProcessTree(process, observedProcesses)
            closeProcessStreams(process)
            // Keep the original input/output exception, rather than exposing Future's wrapper.
            throw if (error is ExecutionException) error.cause ?: error else error
        } finally {
            stdinFuture?.cancel(true)
            stdoutFuture?.cancel(true)
            stderrFuture?.cancel(true)
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
        stdinFuture: Future<*>?,
        phase: TimeoutPhase,
        startupMillis: Long,
    ): CommandResult {
        // Capture before teardown changes process and pipe state. Never query command,
        // arguments, environment, executable paths, or output for this diagnostic.
        val parentAlive = runCatching { process.isAlive }.getOrNull()
        val liveDescendants = runCatching {
            observedProcesses.filter { it.isAlive }.map { it.pid() }
        }.getOrDefault(emptyList())
        val diagnostic = "超时诊断：阶段=${phase.label}；启动耗时=${startupMillis}ms；" +
            "父进程存活=$parentAlive；存活后代PID=${liveDescendants.take(16).joinToString(",").ifEmpty { "无" }}；" +
            "stdin/stdout/stderr完成=${stdinFuture?.isDone == true}/${stdoutFuture?.isDone == true}/${stderrFuture?.isDone == true}\n"
        forceDestroyProcessTree(process, observedProcesses)
        closeProcessStreams(process)
        return CommandResult(
            exitCode = TIMEOUT_EXIT_CODE,
            stdout = completedOutput(stdoutFuture),
            stderr = "命令执行超时（${timeout.seconds} 秒）\n" + diagnostic + completedOutput(stderrFuture),
        )
    }

    private enum class TimeoutPhase(val label: String) {
        PROCESS("进程等待"), INPUT("标准输入"), STDOUT("标准输出"), STDERR("错误输出"),
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
        // Windows can expose a live child's PID before its executable name is available.
        // Revisit tracked children even after their parent exits.
        observedProcesses.removeAll { it.isAlive && !isCommandDescendant(it) }
        val parents = observedProcesses.toMutableList()
        if (process.isAlive) parents.add(0, process.toHandle())
        observedProcesses += liveCommandDescendants(parents)
    }

    private fun liveCommandDescendants(parents: Collection<ProcessHandle>): Set<ProcessHandle> {
        val pending = ArrayDeque<ProcessHandle>().apply { addAll(parents) }
        val visited = mutableSetOf<ProcessHandle>()
        val descendants = linkedSetOf<ProcessHandle>()
        while (pending.isNotEmpty()) {
            val parent = pending.removeFirst()
            if (!visited.add(parent) || !parent.isAlive) continue
            // ProcessHandle.descendants() re-resolves the root PID in a system snapshot.
            // When that root has exited, old parent-PID links can attach unrelated trees.
            // children() filters against this handle's original start time instead.
            parent.children().use { children ->
                children.filter(::isCommandDescendant).forEach { child ->
                    if (child !in visited) {
                        descendants += child
                        pending.addLast(child)
                    }
                }
            }
        }
        return descendants
    }

    private fun forceDestroyProcessTree(process: Process, observedProcesses: Set<ProcessHandle>) {
        // Snapshot and forcibly stop descendants before closing any Java pipe. A graceful
        // Process.destroy() can itself close stdin and wait for the blocked writer's lock
        // while a descendant still owns the other end of that pipe.
        val root = process.toHandle()
        val commandProcesses = observedProcesses.filter(::isCommandDescendant)
        val handles = linkedSetOf<ProcessHandle>().apply {
            add(root)
            addAll(commandProcesses)
            addAll(liveCommandDescendants(listOf(root) + commandProcesses))
        }
        handles.toList().asReversed().forEach { handle ->
            // Classification can change between snapshot and teardown, too. Never stop the
            // console host on the strength of an earlier unknown-name observation.
            if (handle.isAlive && (handle == root || isCommandDescendant(handle))) handle.destroyForcibly()
        }
    }

    private fun isCommandDescendant(handle: ProcessHandle): Boolean {
        if (!isWindows) return true
        // Windows may keep a console host alive after the launched command exits.
        // It is OS infrastructure, not part of the command whose completion we await or kill.
        val executable = handle.info().command().orElse("").replace('\\', '/').substringAfterLast('/')
        return !executable.equals("conhost.exe", ignoreCase = true)
    }

    companion object {
        const val TIMEOUT_EXIT_CODE = 124
        // Short-lived launchers can create a child and exit before a coarse poll sees it,
        // especially on loaded Windows CI workers. Preserve observed descendants so a later
        // timeout can reliably tear down the process tree.
        private val PROCESS_WAIT_POLL_NANOS = TimeUnit.MILLISECONDS.toNanos(10)
    }
}

/**
 * Applies a controlled environment policy without mutating the parent JVM.
 *
 * Matching removals case-insensitively is necessary on Windows, whose process
 * environment is case-insensitive even though [MutableMap] itself is not.
 */
internal fun applyCommandEnvironment(
    processEnvironment: MutableMap<String, String>,
    additions: Map<String, String>,
    removals: Set<String>,
) {
    removals.forEach { requestedName ->
        processEnvironment.keys
            .filter { actualName -> actualName.equals(requestedName, ignoreCase = true) }
            .toList()
            .forEach(processEnvironment::remove)
    }
    processEnvironment.putAll(additions)
}
