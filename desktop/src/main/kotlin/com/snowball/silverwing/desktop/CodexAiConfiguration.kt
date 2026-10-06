package com.snowball.silverwing.desktop

import kotlinx.serialization.json.*
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.net.URI
import java.time.Duration
import java.util.UUID

internal enum class McpScope(val label: String) { GLOBAL("全局"), TASK("当前任务"), PLUGIN("插件提供"), MANAGED("受管理") }
internal data class CodexConfigLayer(val file: Path?, val type: String, val version: String?, val config: JsonObject, val disabledReason: String?, val fingerprint: String? = null)
internal data class McpConfiguration(
    val name: String, val scope: McpScope, val file: Path?, val version: String?,
    val raw: JsonObject, val active: Boolean = true, val pluginId: String? = null,
    val note: String? = null,
) {
    val enabled: Boolean get() = raw.bool("enabled", true)
    val key: String get() = "${file}|${pluginId}|$name"
    val editable: Boolean get() = scope in setOf(McpScope.GLOBAL, McpScope.TASK)
    val type: String get() = if (pluginId != null) "插件" else if (raw.text("url") != null) "HTTP" else "本地命令"
    val endpoint: String get() = raw.text("url")?.let(::safeMcpAddress) ?: raw.text("command") ?: "由插件启动"
}
internal data class AiSkill(val name: String, val description: String, val file: Path, val scope: String, val enabled: Boolean)
internal data class AiInstructionFile(val title: String, val file: Path, val source: String, val exists: Boolean)
internal data class CodexAiSnapshot(
    val cwd: Path?, val userFile: Path, val layers: List<CodexConfigLayer>, val servers: List<McpConfiguration>,
    val skills: List<AiSkill> = emptyList(), val files: List<AiInstructionFile> = emptyList(), val warnings: List<String> = emptyList(),
)
internal data class CodexThreadChoice(val id: String, val title: String, val cwd: Path)
internal data class McpConnectionResult(val label: String, val success: Boolean, val tools: Int = 0)

internal class CodexAiConfigurationService(
    private val rpc: CodexRpcFactory,
    private val fallbackHome: Path = Path.of(System.getenv("CODEX_HOME") ?: Path.of(System.getProperty("user.home"), ".codex").toString()),
) {
    fun load(cwd: Path?, includeSkills: Boolean = false): CodexAiSnapshot = rpc.open(cwd).use { session ->
        val response = session.request("config/read", buildJsonObject { put("includeLayers", true); cwd?.let { put("cwd", it.toString()) } })
        val layers = response.array("layers").mapNotNull { value ->
            val layer = value as? JsonObject ?: return@mapNotNull null
            val name = layer.obj("name")
            val file = name.text("file") ?: name.text("dotCodexFolder")?.let { Path.of(it).resolve("config.toml").toString() }
            val path = file?.let(Path::of)
            val fingerprint = path?.takeIf { Files.isRegularFile(it, NOFOLLOW_LINKS) && Files.size(it) <= 4_000_000 }?.let { configRevision(Files.readAllBytes(it)) }
            CodexConfigLayer(path, name.text("type").orEmpty(), layer.text("version"), layer.obj("config"), layer.text("disabledReason"), fingerprint)
        }
        val userFile = layers.firstOrNull { it.type == "user" }?.file ?: fallbackHome.resolve("config.toml")
        val effective = response.obj("config").obj("mcp_servers")
        val warnings = mutableListOf<String>()
        val servers = mutableListOf<McpConfiguration>()
        layers.forEach { layer -> layer.config.obj("mcp_servers").forEach { (name, value) ->
            val raw = value as? JsonObject ?: return@forEach
            val scope = when (layer.type) { "user" -> McpScope.GLOBAL; "project" -> McpScope.TASK; else -> McpScope.MANAGED }
            val origin = response.obj("origins").entries.firstOrNull { (key, _) -> key == "mcp_servers.$name" || key.startsWith("mcp_servers.$name.") }?.value as? JsonObject
            val originName = origin?.obj("name")
            val originFile = originName?.text("file") ?: originName?.text("dotCodexFolder")?.let { Path.of(it).resolve("config.toml").toString() }
            val active = layer.disabledReason == null && effective[name] != null && (originFile == null || samePath(Path.of(originFile), layer.file))
            servers += McpConfiguration(name, scope, layer.file, layer.version, raw, active,
                note = layer.disabledReason?.let { "目录未受信任，该层配置未生效" } ?: if (!active) "被更高优先级配置覆盖" else null)
        } }
        // Some Codex versions provide origins only at the table root. Preserve effective-only managed entries.
        effective.forEach { (name, value) -> if (servers.none { it.name == name }) servers += McpConfiguration(
            name, McpScope.MANAGED, null, null, value as? JsonObject ?: JsonObject(emptyMap())) }
        val plugins = response.obj("config").obj("plugins")
        servers += pluginServers(userFile.parent, plugins, layers.firstOrNull { it.type == "user" }, warnings)
        var skills = emptyList<AiSkill>()
        if (includeSkills && cwd != null) runCatching {
            session.request("skills/list", buildJsonObject { put("cwds", buildJsonArray { add(cwd.toString()) }); put("forceReload", true) })
        }.onSuccess { result ->
            skills = result.array("data").flatMap { entry -> (entry as? JsonObject)?.array("skills").orEmpty() }.mapNotNull { entry ->
                val skill = entry as? JsonObject ?: return@mapNotNull null
                val path = skill.text("path") ?: return@mapNotNull null
                AiSkill(skill.text("name").orEmpty(), skill.text("description").orEmpty(), Path.of(path), skill.text("scope").orEmpty(), skill.bool("enabled", true))
            }
            if (result.array("data").any { (it as? JsonObject)?.array("errors")?.isNotEmpty() == true }) warnings += "部分 Skill 读取失败，请刷新或检查对应 SKILL.md"
        }.onFailure { warnings += "Skill 列表暂时无法读取，可刷新重试" }
        CodexAiSnapshot(cwd, userFile, layers, servers.sortedWith(compareBy({ it.scope.ordinal }, { it.name })), skills,
            cwd?.let { instructionFiles(it, userFile.parent, response.obj("config")) }.orEmpty(), warnings)
    }

    fun save(snapshot: CodexAiSnapshot, target: Path, name: String, raw: JsonObject?, oldName: String? = null) {
        require(name.matches(Regex("[A-Za-z0-9_.-]{1,100}"))) { "MCP 名称只能包含英文、数字、点、下划线和短横线" }
        raw?.let(::validateMcpConfiguration)
        val normalized = target.toAbsolutePath().normalize()
        val taskFile = snapshot.cwd?.resolve(".codex/config.toml")?.toAbsolutePath()?.normalize()
        val known = snapshot.layers.any { samePath(it.file, normalized) && it.type in setOf("user", "project") }
        require(samePath(normalized, snapshot.userFile) || normalized == taskFile || known) { "不能改写此配置文件" }
        require(!Files.isSymbolicLink(normalized) && !Files.isSymbolicLink(normalized.parent)) { "配置文件不能是符号链接" }
        if (oldName != null && oldName != name) require(snapshot.servers.none { samePath(it.file, normalized) && it.name == name }) { "该配置中已存在同名 MCP" }
        val edits = buildJsonArray {
            if (oldName != null && oldName != name) add(edit("mcp_servers.${quotedKey(oldName)}", JsonNull))
            add(edit("mcp_servers.${quotedKey(name)}", raw ?: JsonNull))
        }
        write(snapshot, normalized, edits)
    }

    fun setEnabled(snapshot: CodexAiSnapshot, server: McpConfiguration, enabled: Boolean) {
        if (server.pluginId == null) save(snapshot, requireNotNull(server.file), server.name,
            JsonObject(server.raw + ("enabled" to JsonPrimitive(enabled))))
        else write(snapshot, snapshot.userFile, buildJsonArray {
            add(edit("plugins.${quotedKey(server.pluginId)}.mcp_servers.${quotedKey(server.name)}.enabled", JsonPrimitive(enabled)))
        })
    }
    private fun write(snapshot: CodexAiSnapshot, target: Path, edits: JsonArray) {
        if (!samePath(target, snapshot.userFile)) { writeTaskConfig(snapshot, target, edits); return }
        rpc.open(snapshot.cwd).use { session -> session.request("config/batchWrite", buildJsonObject {
            put("edits", edits); put("filePath", target.toString()); put("reloadUserConfig", false)
            snapshot.layers.firstOrNull { samePath(it.file, target) }?.version?.let { put("expectedVersion", it) }
        }) }
    }
    /** Codex restricts its writer to the user layer. Stage task TOML through an isolated user layer,
     * then compare the task revision and atomically replace it. No auth or runtime state is put in the task. */
    private fun writeTaskConfig(snapshot: CodexAiSnapshot, target: Path, edits: JsonArray) {
        synchronized(taskWriteLock) {
            val exists = Files.exists(target, NOFOLLOW_LINKS)
            val before = if (exists) { require(Files.size(target) <= 4_000_000); Files.readAllBytes(target) } else byteArrayOf()
            val expected = snapshot.layers.firstOrNull { samePath(it.file, target) }?.fingerprint
            require(if (exists) expected == configRevision(before) else expected == null) { "任务配置已改变或尚未读取，请刷新后重试" }
            val isolated = Files.createTempDirectory("silverwing-codex-edit-")
            try {
                val config = isolated.resolve("config.toml"); Files.write(config, before)
                rpc.openIsolated(null, isolated).use { session ->
                    val read = session.request("config/read", buildJsonObject { put("includeLayers", true) })
                    val version = read.array("layers").mapNotNull { it as? JsonObject }.firstOrNull { it.obj("name").text("type") == "user" }?.text("version")
                    val sourceVersion = snapshot.layers.firstOrNull { samePath(it.file, target) }?.version
                    require(!exists || sourceVersion == version) { "任务配置已被其他程序修改，请刷新后重试" }
                    session.request("config/batchWrite", buildJsonObject {
                        put("edits", edits); put("filePath", config.toString()); version?.let { put("expectedVersion", it) }; put("reloadUserConfig", false)
                    })
                }
                require(Files.exists(target, NOFOLLOW_LINKS) == exists && (!exists || Files.readAllBytes(target).contentEquals(before))) { "任务配置已被其他程序修改，请刷新后重试" }
                require(!Files.isSymbolicLink(target) && !Files.isSymbolicLink(target.parent)) { "配置文件不能是符号链接" }
                Files.createDirectories(target.parent)
                val staged = Files.createTempFile(target.parent, "mcp-config-", ".tmp")
                try {
                    Files.write(staged, Files.readAllBytes(config))
                    try { Files.move(staged, target, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING) }
                    catch (_: java.nio.file.AtomicMoveNotSupportedException) { Files.move(staged, target, StandardCopyOption.REPLACE_EXISTING) }
                } finally { Files.deleteIfExists(staged) }
            } finally { Files.walk(isolated).use { paths -> paths.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists) } }
        }
    }
    fun connections(cwd: Path?): Map<String, McpConnectionResult> = rpc.open(cwd).use { session ->
        val records = mutableMapOf<String, McpConnectionResult>(); var cursor: String? = null
        do {
            val result = session.request("mcpServerStatus/list", buildJsonObject { put("limit", 100); cursor?.let { put("cursor", it) } }, Duration.ofSeconds(60))
            result.array("data").forEach { item -> val entry = item as? JsonObject ?: return@forEach
                val count = entry.obj("tools").size
                val ready = entry["serverInfo"] is JsonObject || count > 0
                records[entry.text("name").orEmpty()] = McpConnectionResult(
                    if (ready) "连接成功 · $count 个工具" else if (entry.text("authStatus") == "notLoggedIn") "需要认证" else "未就绪，请检查启动命令或认证", ready, count)
            }; cursor = result.text("nextCursor")
        } while (cursor != null)
        records
    }
    fun threads(cwd: Path): List<CodexThreadChoice> = rpc.open(cwd).use { session ->
        val result = session.request("thread/list", buildJsonObject { put("cwd", cwd.toString()); put("limit", 100); put("sortKey", "updated_at"); put("useStateDbOnly", true) })
        result.array("data").mapNotNull { item -> val entry = item as? JsonObject ?: return@mapNotNull null
            val path = entry.text("cwd")?.let(Path::of) ?: return@mapNotNull null
            val id = entry.text("id") ?: return@mapNotNull null
            if (!samePath(path, cwd)) return@mapNotNull null
            CodexThreadChoice(id, entry.text("name") ?: entry.text("preview")?.take(120) ?: id, path)
        }
    }
    fun readFile(file: Path): String {
        require(Files.isRegularFile(file, NOFOLLOW_LINKS)) { "文件不存在或是符号链接" }
        require(Files.size(file) <= 1_000_000) { "文件过大，请使用外部应用打开" }
        return Files.readString(file)
    }

    private fun pluginServers(home: Path, plugins: JsonObject, layer: CodexConfigLayer?, warnings: MutableList<String>): List<McpConfiguration> {
        val cache = home.resolve("plugins/cache")
        if (!Files.isDirectory(cache) || plugins.isEmpty()) return emptyList()
        val manifests = Files.walk(cache, 6).use { paths -> paths.filter { it.fileName.toString() == "plugin.json" && it.parent.fileName.toString() == ".codex-plugin" && Files.isRegularFile(it, NOFOLLOW_LINKS) }
            .limit(500).toList() }
        val result = mutableListOf<McpConfiguration>()
        plugins.forEach { (id, settingsValue) ->
            val settings = settingsValue as? JsonObject ?: return@forEach
            val name = id.substringBefore('@'); val source = id.substringAfter('@', "")
            val candidates = manifests.filter { path ->
                val relative = cache.relativize(path)
                source.isEmpty() || relative.getName(0).toString().equals(source, true)
            }.mapNotNull { path -> runCatching { Json.parseToJsonElement(Files.readString(path)) as JsonObject }.getOrNull()
                ?.takeIf { it.text("name") == name }?.let { path to it } }
            val selected = candidates.maxByOrNull { Files.getLastModifiedTime(it.first).toMillis() } ?: return@forEach
            val packageRoot = selected.first.parent.parent
            runCatching {
                val declaration = selected.second["mcpServers"]
                val mcpFile = packageRoot.resolve((declaration as? JsonPrimitive)?.contentOrNull ?: ".mcp.json").normalize()
                val config = (declaration as? JsonObject) ?: if (mcpFile.startsWith(packageRoot) && Files.isRegularFile(mcpFile, NOFOLLOW_LINKS))
                    Json.parseToJsonElement(Files.readString(mcpFile)) as? JsonObject else null
                val entries = config?.get("mcpServers") as? JsonObject ?: config ?: JsonObject(emptyMap())
                entries.forEach { (serverName, value) ->
                    val raw = value as? JsonObject ?: return@forEach
                    val override = settings.obj("mcp_servers").obj(serverName)
                    result += McpConfiguration(serverName, McpScope.PLUGIN, layer?.file, layer?.version,
                        JsonObject(raw + override), settings.bool("enabled", false), id,
                        if (!settings.bool("enabled", false)) "插件已停用" else "启动配置由插件提供")
                }
            }.onFailure { warnings += "插件 $name 的 MCP 清单读取失败" }
        }
        return result
    }
    companion object { private val taskWriteLock = Any() }
}

private fun configRevision(bytes: ByteArray): String = "sha256:" + java.security.MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

internal fun JsonObject.text(key: String): String? = (get(key) as? JsonPrimitive)?.contentOrNull
internal fun JsonObject.bool(key: String, fallback: Boolean): Boolean = (get(key) as? JsonPrimitive)?.booleanOrNull ?: fallback
internal fun JsonObject.obj(key: String): JsonObject = get(key) as? JsonObject ?: JsonObject(emptyMap())
internal fun JsonObject.array(key: String): JsonArray = get(key) as? JsonArray ?: JsonArray(emptyList())
internal fun samePath(a: Path?, b: Path?): Boolean = a != null && b != null && a.toAbsolutePath().normalize().toString().equals(b.toAbsolutePath().normalize().toString(), System.getProperty("os.name").startsWith("Windows", true))
private fun quotedKey(key: String): String = JsonPrimitive(key).toString()
private fun edit(key: String, value: JsonElement): JsonObject = buildJsonObject { put("keyPath", key); put("value", value); put("mergeStrategy", "replace") }
internal fun safeMcpAddress(address: String): String = runCatching { val uri = URI(address); URI(uri.scheme, null, uri.host, uri.port, uri.path, null, null).toString() }.getOrDefault("HTTP 服务")
internal fun validateMcpConfiguration(config: JsonObject) {
    val url = config.text("url")
    if (url != null) { val uri = runCatching { URI(url) }.getOrNull(); require(uri != null && uri.scheme in setOf("http", "https") && !uri.host.isNullOrBlank() && uri.userInfo == null) { "请输入有效的 HTTP/HTTPS 地址；认证请使用环境变量或认证入口" } }
    else require(!config.text("command").isNullOrBlank()) { "请填写启动命令" }
    require(url == null || config.text("command") == null) { "启动命令和 HTTP 地址只能选择一种" }
    listOf("startup_timeout_sec", "tool_timeout_sec").forEach { field -> config[field]?.let {
        require((it as? JsonPrimitive)?.doubleOrNull?.let { n -> n.isFinite() && n > 0 && n <= 3600 } == true) { "超时必须为 0 到 3600 之间的正数" }
    } }
    require(config["args"] == null || config["args"] is JsonArray && config.array("args").all { it is JsonPrimitive && it.isString }) { "命令参数必须是字符串数组" }
    listOf("env", "http_headers").forEach { field -> config[field]?.let { require(it is JsonObject && it.values.all { v -> v is JsonPrimitive && v.isString }) { "环境变量和请求头必须是文本键值" } } }
}
private fun instructionFiles(cwd: Path, home: Path, config: JsonObject): List<AiInstructionFile> {
    val ancestors = generateSequence(cwd.toAbsolutePath().normalize()) { it.parent }.toList()
    val markers = config.array("project_root_markers").mapNotNull { (it as? JsonPrimitive)?.contentOrNull }.ifEmpty { listOf(".git", ".hg") }
    val root = ancestors.indexOfFirst { dir -> markers.any { Files.exists(dir.resolve(it), NOFOLLOW_LINKS) } }.takeIf { it >= 0 } ?: ancestors.lastIndex
    val result = mutableListOf<AiInstructionFile>()
    val guideNames = listOf("AGENTS.override.md", "AGENTS.md") + config.array("project_doc_fallback_filenames").mapNotNull { (it as? JsonPrimitive)?.contentOrNull }
    fun addGuide(dir: Path, source: String, required: Boolean = false) {
        val path = guideNames.map(dir::resolve).firstOrNull { Files.isRegularFile(it, NOFOLLOW_LINKS) } ?: dir.resolve("AGENTS.md")
        if (required || Files.isRegularFile(path, NOFOLLOW_LINKS)) result += AiInstructionFile(path.fileName.toString(), path, source, Files.isRegularFile(path, NOFOLLOW_LINKS))
    }
    addGuide(home, "Codex 全局")
    ancestors.take(root + 1).asReversed().forEach { addGuide(it, if (samePath(it, cwd)) "当前任务" else "上级目录", samePath(it, cwd)) }
    config.text("model_instructions_file")?.let { raw -> val path = Path.of(raw); val absolute = if (path.isAbsolute) path else cwd.resolve(path)
        result += AiInstructionFile(absolute.fileName.toString(), absolute, "Codex 模型说明", Files.isRegularFile(absolute, NOFOLLOW_LINKS)) }
    return result.distinctBy { it.file.toAbsolutePath().normalize() }
}

/** Runtime bindings do not alter task manifests or exported product configuration. */
internal class CodexTaskBindings(private val file: Path) {
    @Synchronized fun get(cwd: Path): String? = load().text(cwd.toAbsolutePath().normalize().toString())
    @Synchronized fun set(cwd: Path, threadId: String?) {
        threadId?.let { UUID.fromString(it) }
        val key = cwd.toAbsolutePath().normalize().toString(); val values = load().toMutableMap()
        if (threadId == null) values.remove(key) else values[key] = JsonPrimitive(threadId)
        Files.createDirectories(file.parent)
        val temp = Files.createTempFile(file.parent, "codex-bindings-", ".tmp")
        try { Files.writeString(temp, JsonObject(values).toString())
            try { Files.move(temp, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING) }
            catch (_: java.nio.file.AtomicMoveNotSupportedException) { Files.move(temp, file, StandardCopyOption.REPLACE_EXISTING) }
        } finally { Files.deleteIfExists(temp) }
    }
    private fun load(): JsonObject = if (!Files.exists(file)) JsonObject(emptyMap()) else {
        require(Files.size(file) <= 1_000_000) { "Codex 关联文件过大" }
        Json.parseToJsonElement(Files.readString(file)) as? JsonObject ?: error("Codex 关联文件格式不正确")
    }
}
