package com.snowball.silverwing.desktop

import kotlinx.serialization.json.*
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class CodexAiConfigurationTest {
    @TempDir lateinit var root: Path
    private fun obj(body: String) = Json.parseToJsonElement(body) as JsonObject
    private fun layer(file: Path, type: String, config: JsonObject, disabled: Boolean = false) = buildJsonObject {
        put("name", buildJsonObject { put("type", type); put(if (type == "project") "dotCodexFolder" else "file", if (type == "project") file.parent.toString() else file.toString()) })
        put("version", "sha256:fixture"); put("config", config); if (disabled) put("disabledReason", "untrusted")
    }
    private fun reply(user: Path, task: Path? = null, disabled: Boolean = false): JsonObject {
        val global = obj("""{"mcp_servers":{"docs":{"command":"global","enabled":true}}}""")
        val local = obj("""{"mcp_servers":{"docs":{"command":"task","enabled":true}}}""")
        val effective = if (task != null && !disabled) local else global
        return buildJsonObject {
            put("config", effective); put("layers", buildJsonArray { add(layer(user,"user",global)); task?.let { add(layer(it,"project",local,disabled)) } })
            put("origins", buildJsonObject { put("mcp_servers.docs.command", buildJsonObject { put("name", buildJsonObject {
                if (task != null && !disabled) { put("type","project"); put("dotCodexFolder",task.parent.toString()) } else { put("type","user");put("file",user.toString()) }
            }) }) })
        }
    }
    private fun factory(handler: (String, JsonObject) -> JsonObject) = CodexRpcFactory { object : CodexRpc {
        override fun request(method: String, params: JsonObject, timeout: Duration) = handler(method, params)
        override fun close() {}
    } }

    @Test fun `task overrides global while an untrusted layer remains visible but inactive`() {
        val user = root.resolve("home/config.toml"); val cwd = Files.createDirectory(root.resolve("任务")); val task = cwd.resolve(".codex/config.toml")
        for (disabled in listOf(false,true)) {
            val snapshot = CodexAiConfigurationService(factory { _,_ -> reply(user,task,disabled) }).load(cwd)
            val global = snapshot.servers.single { it.scope == McpScope.GLOBAL }; val local = snapshot.servers.single { it.scope == McpScope.TASK }
            assertEquals(disabled,global.active);assertEquals(!disabled,local.active)
            if (disabled) assertContains(local.note!!,"未生效") else assertContains(global.note!!,"覆盖")
        }
    }
    @Test fun `global rename and deletion use the versioned writer without touching other settings`() {
        val user = root.resolve("home/config.toml"); val calls = mutableListOf<Pair<String,JsonObject>>()
        val service = CodexAiConfigurationService(factory { method,params -> calls += method to params; if (method == "config/read") reply(user) else obj("{}") })
        val snapshot = service.load(null)
        service.save(snapshot,user,"new.name",obj("""{"command":"python","args":["中文 路径"],"env":{"SECRET":"value"},"enabled":false}"""),"docs")
        val write = calls.last().second
        assertEquals("sha256:fixture",write.text("expectedVersion"));assertEquals(user.toString(),write.text("filePath"))
        assertEquals(listOf("mcp_servers.\"docs\"","mcp_servers.\"new.name\""),write.array("edits").map { it.jsonObject.text("keyPath") })
        assertEquals(JsonNull,write.array("edits")[0].jsonObject["value"])
        service.save(snapshot,user,"docs",null)
        assertEquals(JsonNull,calls.last().second.array("edits").single().jsonObject["value"])
    }
    @Test fun `plugins expose source and per-server switch without overwriting transport`() {
        val home = Files.createDirectory(root.resolve("home")); val packageRoot = Files.createDirectories(home.resolve("plugins/cache/team/fixture/1"))
        Files.createDirectories(packageRoot.resolve(".codex-plugin"));Files.writeString(packageRoot.resolve(".codex-plugin/plugin.json"),"""{"name":"fixture","mcpServers":".mcp.json"}""")
        Files.writeString(packageRoot.resolve(".mcp.json"),"""{"mcpServers":{"files":{"command":"provider","env":{"SECRET":"hidden"}}}}""")
        var write: JsonObject? = null
        val response = reply(home.resolve("config.toml")); val config = JsonObject(response.obj("config") + ("plugins" to obj("""{"fixture@team":{"enabled":true,"mcp_servers":{"files":{"enabled":false}}}}""")))
        val service = CodexAiConfigurationService(factory { method,params -> if(method=="config/read") JsonObject(response+("config" to config)) else {write=params;obj("{}")} })
        val snapshot = service.load(null); val plugin = snapshot.servers.single {it.scope==McpScope.PLUGIN}
        assertEquals("fixture@team",plugin.pluginId);assertFalse(plugin.editable);assertFalse(plugin.enabled)
        service.setEnabled(snapshot,plugin,true)
        assertEquals("plugins.\"fixture@team\".mcp_servers.\"files\".enabled",write!!.array("edits").single().jsonObject.text("keyPath"))
        assertEquals(JsonPrimitive(true),write!!.array("edits").single().jsonObject["value"])
    }
    @Test fun `skills failures do not block MCP and guides prefer the local override`() {
        val home = Files.createDirectory(root.resolve("home")); val cwd = Files.createDirectory(root.resolve("任务"));Files.createDirectory(cwd.resolve(".git"))
        Files.writeString(home.resolve("AGENTS.md"),"全局");Files.writeString(cwd.resolve("AGENTS.md"),"普通");Files.writeString(cwd.resolve("AGENTS.override.md"),"覆盖")
        val service = CodexAiConfigurationService(factory { method,_ -> if(method=="skills/list") error("read failed") else reply(home.resolve("config.toml")) })
        val snapshot = service.load(cwd,true)
        assertEquals(1,snapshot.servers.size);assertTrue(snapshot.warnings.isNotEmpty())
        assertEquals(listOf("AGENTS.md","AGENTS.override.md"),snapshot.files.map {it.title});assertEquals("覆盖",service.readFile(snapshot.files.last().file))
    }
    @Test fun `missing task guide is visible and skills preserve disabled state`() {
        val cwd = Files.createDirectory(root.resolve("task"));val skill = Files.writeString(root.resolve("SKILL.md"),"skill")
        val service = CodexAiConfigurationService(factory { method,_ -> if(method=="skills/list") buildJsonObject {
            put("data",buildJsonArray {add(buildJsonObject {put("skills",buildJsonArray {add(buildJsonObject {put("name","fixture");put("description","test");put("path",skill.toString());put("scope","user");put("enabled",false)})})})})
        } else reply(root.resolve("home/config.toml")) })
        val snapshot=service.load(cwd,true);assertFalse(snapshot.skills.single().enabled)
        assertFalse(snapshot.files.single().exists);assertEquals(cwd.resolve("AGENTS.md"),snapshot.files.single().file)
    }
    @Test fun `connection check uses inventory only and authentication is not reported as success`() {
        val methods=mutableListOf<String>();val service=CodexAiConfigurationService(factory {method,_ -> methods+=method;obj("""{"data":[{"name":"ready","serverInfo":{"name":"fixture"},"tools":{}},{"name":"auth","authStatus":"notLoggedIn","tools":{}}],"nextCursor":null}""")})
        val results=service.connections(null);assertTrue(results.getValue("ready").success);assertFalse(results.getValue("auth").success)
        assertEquals("需要认证",results.getValue("auth").label);assertEquals(listOf("mcpServerStatus/list"),methods)
    }
    @Test fun `thread picker excludes other working directories and bindings survive reload`() {
        val cwd=Files.createDirectory(root.resolve("task"));val id="01a0fd65-1c49-7952-9725-369dfb7527b1"
        val service=CodexAiConfigurationService(factory {method,params ->assertEquals("thread/list",method);assertEquals(cwd.toString(),params.text("cwd"));buildJsonObject {put("data",buildJsonArray {
            add(buildJsonObject {put("id",id);put("name","支付研发");put("cwd",cwd.toString())});add(buildJsonObject {put("id",id);put("cwd",root.toString())})})} })
        assertEquals("支付研发",service.threads(cwd).single().title)
        val file=root.resolve("runtime/task-bindings.json");CodexTaskBindings(file).set(cwd,id);assertEquals(id,CodexTaskBindings(file).get(cwd))
        CodexTaskBindings(file).set(cwd,null);assertNull(CodexTaskBindings(file).get(cwd));assertFailsWith<IllegalArgumentException>{CodexTaskBindings(file).set(cwd,"not-a-uuid")}
    }
    @Test fun `invalid transports and timeouts are rejected and display addresses remove authentication`() {
        listOf("""{"url":"file:///tmp/x"}""","""{"url":"https://user:secret@example.com/mcp"}""","""{"command":""}""","""{"command":"node","tool_timeout_sec":0}""","""{"command":"node","startup_timeout_sec":-1}""","""{"command":"node","args":[1]}""").forEach {assertFailsWith<IllegalArgumentException>{validateMcpConfiguration(obj(it))}}
        validateMcpConfiguration(obj("""{"url":"https://example.com/mcp","tool_timeout_sec":10}"""))
        assertEquals("https://example.com/mcp",safeMcpAddress("https://user:secret@example.com/mcp?token=private#key"))
    }
    @Test fun `large or missing instruction files fail without reading binary data`() {
        val service=CodexAiConfigurationService(factory {_,_->obj("{}")});assertFailsWith<IllegalArgumentException>{service.readFile(root.resolve("missing"))}
        val huge=Files.writeString(root.resolve("huge.md"),"a".repeat(1_000_001));assertFailsWith<IllegalArgumentException>{service.readFile(huge)}
    }
}
