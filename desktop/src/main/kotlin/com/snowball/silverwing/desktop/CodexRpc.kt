package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.CodexExecutable
import com.snowball.silverwing.core.CURRENT_PRODUCT_VERSION
import kotlinx.serialization.json.*
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

interface CodexRpc : AutoCloseable {
    fun request(method: String, params: JsonObject, timeout: Duration = Duration.ofSeconds(30)): JsonObject
}

fun interface CodexRpcFactory {
    fun open(cwd: Path?): CodexRpc
    fun openIsolated(cwd: Path?, home: Path): CodexRpc = open(cwd)
}

/** Uses Codex's parser and versioned config writer; no credentials are placed in command arguments or logs. */
internal class SystemCodexRpcFactory(
    private val executable: CodexExecutable,
    private val environment: () -> Map<String, String> = { emptyMap() },
    private val removals: () -> Set<String> = { emptySet() },
) : CodexRpcFactory {
    override fun open(cwd: Path?): CodexRpc = start(cwd, null)
    override fun openIsolated(cwd: Path?, home: Path): CodexRpc = start(cwd, home)
    private fun start(cwd: Path?, home: Path?): CodexRpc {
        val command = executable.resolve()
        val args = when {
            command.endsWith(".ps1", true) -> listOf("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", command)
            command.endsWith(".cmd", true) || command.endsWith(".bat", true) -> listOf("cmd.exe", "/d", "/c", command)
            else -> listOf(command)
        } + listOf("app-server", "--stdio")
        val builder = ProcessBuilder(args).redirectError(ProcessBuilder.Redirect.DISCARD)
        cwd?.let { builder.directory(it.toFile()) }
        val env = builder.environment()
        removals().forEach { key -> env.keys.filter { it.equals(key, true) }.toList().forEach(env::remove) }
        env.putAll(executable.environment()); env.putAll(environment())
        home?.let { env["CODEX_HOME"] = it.toString() }
        return ProcessCodexRpc(builder.start()).also {
            try {
                it.request("initialize", buildJsonObject {
                    put("clientInfo", buildJsonObject { put("name", "silverwing"); put("version", CURRENT_PRODUCT_VERSION) })
                    put("capabilities", buildJsonObject { put("experimentalApi", true) })
                })
                it.notify("initialized")
            } catch (failure: Throwable) { it.close(); throw failure }
        }
    }
}

internal class ProcessCodexRpc(private val process: Process) : CodexRpc {
    private val json = Json { ignoreUnknownKeys = true }
    private val writer = process.outputStream.bufferedWriter(Charsets.UTF_8)
    private val messages = LinkedBlockingQueue<JsonObject>(256)
    @Volatile private var ended = false
    private var serial = 0
    private val reader = Thread({
        try {
            process.inputStream.bufferedReader(Charsets.UTF_8).use { input ->
                while (true) {
                    val line = input.readLine() ?: break
                    if (line.length > 4_000_000) break
                    val message = runCatching { json.parseToJsonElement(line) as? JsonObject }.getOrNull() ?: continue
                    // Inventory/config methods may request user input from a server. Do not consent implicitly.
                    if (message["id"] != null && message["method"] != null) {
                        send(buildJsonObject { put("id", message.getValue("id")); put("error", buildJsonObject {
                            put("code", -32601); put("message", "Interactive requests are not supported in this view")
                        }) })
                    } else if (message["id"] != null) messages.put(message)
                }
            }
        } catch (_: Exception) { } finally { ended = true }
    }, "silverwing-codex-config").apply { isDaemon = true; start() }

    @Synchronized private fun send(message: JsonObject) { writer.write(message.toString()); writer.newLine(); writer.flush() }
    fun notify(method: String) = send(buildJsonObject { put("method", method) })
    override fun request(method: String, params: JsonObject, timeout: Duration): JsonObject {
        val id = ++serial
        send(buildJsonObject { put("id", id); put("method", method); put("params", params) })
        val deadline = System.nanoTime() + timeout.toNanos()
        while (System.nanoTime() < deadline) {
            val response = messages.poll(100, TimeUnit.MILLISECONDS)
            if (response?.get("id")?.jsonPrimitive?.intOrNull == id) {
                if (response["error"] != null) {
                    val error = response["error"] as? JsonObject
                    val reason = error?.get("message")?.jsonPrimitive?.content.orEmpty().lowercase()
                    val message = when {
                        "version" in reason || "modified" in reason || "conflict" in reason -> "配置已被其他程序修改，请刷新后重试"
                        "trust" in reason -> "该任务目录尚未受 Codex 信任，请先在 Codex 中打开并确认目录"
                        "method" in reason -> "当前 Codex 不支持此接口，请更新 Codex 后重试"
                        else -> "Codex 未完成此操作（${error?.get("code") ?: "未知错误"}），请检查配置后重试"
                    }
                    throw IllegalStateException(message)
                }
                return response["result"] as? JsonObject ?: JsonObject(emptyMap())
            }
            check(!ended || !messages.isEmpty()) { "Codex 配置服务已退出，请检查 CLI 路径或配置后重试" }
        }
        throw IllegalStateException("Codex 操作超时，请重试或取消")
    }
    override fun close() {
        val children = process.toHandle().descendants().use { it.toList() }
        runCatching { writer.close() }
        children.asReversed().forEach { runCatching { it.destroy() } }
        process.destroy()
        runCatching { if (!process.waitFor(300, TimeUnit.MILLISECONDS)) process.destroyForcibly() }
        children.filter { it.isAlive }.forEach { runCatching { it.destroyForcibly() } }
        reader.interrupt()
    }
}
