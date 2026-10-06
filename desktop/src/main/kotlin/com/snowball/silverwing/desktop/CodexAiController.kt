package com.snowball.silverwing.desktop

import androidx.compose.runtime.*
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import java.nio.file.Path

internal class CodexAiController(
    private val service: CodexAiConfigurationService,
    private val bindings: CodexTaskBindings,
    private val scope: CoroutineScope,
    private val io: CoroutineDispatcher,
    private val authenticate: (Path?, String, Boolean) -> Unit,
    private val onStatus: (String) -> Unit,
) {
    val snapshots = mutableStateMapOf<String, CodexAiSnapshot>()
    val loading = mutableStateMapOf<String, Boolean>()
    val errors = mutableStateMapOf<String, String>()
    val connections = mutableStateMapOf<String, Map<String, McpConnectionResult>>()
    val threads = mutableStateMapOf<String, List<CodexThreadChoice>>()
    val threadLoading = mutableStateMapOf<String, Boolean>()
    val threadErrors = mutableStateMapOf<String, String>()
    val boundThreads = mutableStateMapOf<String, String>()
    var busy by mutableStateOf(false); private set
    var busyLabel by mutableStateOf(""); private set
    private val readJobs = mutableMapOf<String, Job>()
    private val threadJobs = mutableMapOf<String, Job>()
    private var operation: Job? = null
    fun key(cwd: Path?): String = cwd?.toAbsolutePath()?.normalize()?.toString() ?: "global"
    fun refresh(cwd: Path?, includeSkills: Boolean = false) {
        val key = key(cwd); readJobs.remove(key)?.cancel(); loading[key] = true; errors.remove(key)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                val snapshot = runInterruptible(io) { service.load(cwd, includeSkills) }
                ensureActive(); snapshots[key] = snapshot
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { errors[key] = error.message ?: "读取失败，请重试" }
            finally { if (readJobs[key] === coroutineContext[Job]) { loading[key] = false; readJobs.remove(key) } }
        }; readJobs[key] = job; job.start()
    }
    fun cancelRead(cwd: Path?) { readJobs.remove(key(cwd))?.cancel(); loading[key(cwd)] = false }
    fun save(snapshot: CodexAiSnapshot, file: Path, name: String, raw: JsonObject?, oldName: String? = null, onSaved: () -> Unit = {}) =
        operate(snapshot.cwd, "正在保存 MCP…", "MCP 已保存，新建或重启 Codex 会话后生效", {
            service.save(snapshot, file, name, raw, oldName)
        }) { refresh(snapshot.cwd, snapshot.cwd != null); connections.remove(key(snapshot.cwd)); onSaved() }
    fun enable(snapshot: CodexAiSnapshot, server: McpConfiguration, enabled: Boolean) =
        operate(snapshot.cwd, "正在更新 MCP…", "MCP 开关已保存，新建或重启 Codex 会话后生效", { service.setEnabled(snapshot, server, enabled) }) {
            connections.remove(key(snapshot.cwd)); refresh(snapshot.cwd, snapshot.cwd != null)
        }
    fun detect(cwd: Path?) = operate(cwd, "正在检测 MCP 连接…", "MCP 连接检测完成", { service.connections(cwd) }) { connections[key(cwd)] = it }
    fun auth(cwd: Path?, server: McpConfiguration, login: Boolean) = operate(cwd,
        if (login) "正在等待 MCP 认证…" else "正在退出 MCP 认证…", if (login) "MCP 认证已完成" else "MCP 认证已退出",
        { authenticate(cwd, server.name, login) }) { connections.remove(key(cwd)) }
    private fun <T> operate(cwd: Path?, label: String, success: String, block: () -> T, done: (T) -> Unit) {
        if (busy) return
        busy = true; busyLabel = label; errors.remove(key(cwd))
        operation = scope.launch {
            try { val result = runInterruptible(io, block); ensureActive(); done(result); onStatus(success) }
            catch (cancelled: CancellationException) { errors[key(cwd)] = "操作已取消，可刷新确认当前状态后重试"; throw cancelled }
            catch (error: Exception) { errors[key(cwd)] = error.message ?: "操作失败，请重试" }
            finally { busy = false; busyLabel = ""; operation = null }
        }
    }
    fun cancelOperation() { operation?.cancel() }
    fun refreshThreads(cwd: Path) {
        val key = key(cwd); threadJobs.remove(key)?.cancel(); threadLoading[key] = true; threadErrors.remove(key)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                val result = runInterruptible(io) { service.threads(cwd) to bindings.get(cwd) }
                ensureActive(); threads[key] = result.first
                if (result.second == null) boundThreads.remove(key) else boundThreads[key] = result.second!!
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { threadErrors[key] = error.message ?: "读取会话失败，请重试" }
            finally { if (threadJobs[key] === coroutineContext[Job]) { threadLoading[key] = false; threadJobs.remove(key) } }
        }; threadJobs[key] = job; job.start()
    }
    fun cancelThreadRead(cwd: Path) { threadJobs.remove(key(cwd))?.cancel(); threadLoading[key(cwd)] = false }
    fun bind(cwd: Path, id: String?, onSaved: () -> Unit = {}) {
        require(id == null || threads[key(cwd)].orEmpty().any { it.id == id && samePath(it.cwd, cwd) }) { "只能关联当前任务目录的会话" }
        operate(cwd, "正在保存 Codex 关联…", if (id == null) "Codex 会话关联已解除" else "Codex 会话已关联", { bindings.set(cwd, id) }) {
            if (id == null) boundThreads.remove(key(cwd)) else boundThreads[key(cwd)] = id
            onSaved()
        }
    }
    suspend fun fileContent(file: Path): String = runInterruptible(io) { service.readFile(file) }
    suspend fun boundThread(cwd: Path): String? = runInterruptible(io) { bindings.get(cwd) }
}
