package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AutoDetectedCodexExecutable
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.io.TempDir
import kotlinx.serialization.json.*
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class CodexRpcIntegrationTest {
    @TempDir lateinit var root: Path
    private fun service(home: Path): CodexAiConfigurationService {
        val executable = AutoDetectedCodexExecutable()
        assumeTrue(runCatching { Files.isRegularFile(Path.of(executable.resolve())) }.getOrDefault(false), "Local Codex CLI required")
        return CodexAiConfigurationService(SystemCodexRpcFactory(executable, { mapOf("CODEX_HOME" to home.toString()) }), home)
    }
    @Test fun `real Codex preserves TOML comments and credentials and rejects stale global writes`() {
        val home = Files.createDirectory(root.resolve("codex")); val config = home.resolve("config.toml")
        Files.writeString(config, "# 保留注释\nmodel = \"gpt-6-luna\"\n[mcp_servers.fixture]\ncommand = \"echo\"\nenabled = false\n[mcp_servers.fixture.env]\nTOKEN = \"fixture-secret\"\n")
        val service=service(home);val snapshot=service.load(null);val entry=snapshot.servers.single()
        service.setEnabled(snapshot,entry,true)
        val saved=Files.readString(config);assertContains(saved,"# 保留注释");assertContains(saved,"fixture-secret");assertContains(saved,"model = \"gpt-6-luna\"")
        assertFailsWith<IllegalStateException>{service.setEnabled(snapshot,entry,false)}
        val fresh=service.load(null);service.save(fresh,config,"fixture",null)
        val deleted=Files.readString(config);assertFalse(deleted.contains("mcp_servers.fixture"));assertContains(deleted,"# 保留注释")
    }
    @Test fun `real Codex stages task writes separately and preserves task settings and revision checks`() {
        val home=Files.createDirectory(root.resolve("home"));val task=Files.createDirectory(root.resolve("中文 任务"));Files.createDirectory(task.resolve(".git"))
        Files.writeString(home.resolve("config.toml"),"model = \"gpt-6-luna\"\n[projects.${JsonPrimitive(task.toString())}]\ntrust_level = \"trusted\"\n")
        val service=service(home);var snapshot=service.load(task)
        val config=task.resolve(".codex/config.toml")
        service.save(snapshot,config,"task.fixture",buildJsonObject {put("command","echo");put("enabled",false)})
        assertContains(Files.readString(config),"task.fixture")
        val global=Files.readString(home.resolve("config.toml"));assertFalse(global.contains("mcp_servers"))
        snapshot=service.load(task);assertTrue(snapshot.servers.any {it.scope==McpScope.TASK&&it.name=="task.fixture"})
        Files.writeString(config,Files.readString(config)+"\n# 外部修改\n")
        assertFailsWith<IllegalArgumentException>{service.save(snapshot,config,"task.fixture",null)}
        val fresh=service.load(task);service.save(fresh,config,"task.fixture",null)
        assertContains(Files.readString(config),"# 外部修改");assertFalse(Files.readString(config).contains("task.fixture"))
        assertEquals(listOf("config.toml"),Files.list(config.parent).use {paths->paths.map {it.fileName.toString()}.toList()})
        val beforeRemoval=service.load(task);Files.delete(config)
        assertFailsWith<IllegalArgumentException>{service.save(beforeRemoval,config,"task.fixture",buildJsonObject {put("command","echo")})}
        assertFalse(Files.exists(config))
    }
    @Test fun `real MCP initialization checks inventory without calling a business tool`() {
        val home=Files.createDirectory(root.resolve("home"));val java=Path.of(System.getProperty("java.home"),"bin",if(System.getProperty("os.name").startsWith("Windows"))"java.exe" else "java")
        val fixture=root.resolve("McpFixture.java");val called=root.resolve("tool-called")
        Files.writeString(fixture,"""
            import java.io.*;import java.util.regex.*;import java.nio.file.*;
            class McpFixture {
                public static void main(String[] args)throws Exception {
                    BufferedReader in=new BufferedReader(new InputStreamReader(System.in));String s;
                    while((s=in.readLine())!=null){
                        Matcher id=Pattern.compile("\"id\"\\s*:\\s*([^,}]+)").matcher(s);if(!id.find())continue;
                        String result;
                        if(s.contains("tools/call")){Files.writeString(Path.of(args[0]),"called");result="{}";}
                        else if(s.contains("tools/list"))result="{\"tools\":[{\"name\":\"fixture_read\",\"description\":\"read only fixture\",\"inputSchema\":{\"type\":\"object\"}}]}";
                        else {Matcher version=Pattern.compile("\"protocolVersion\"\\s*:\\s*(\"[^\"]+\")").matcher(s);String v=version.find()?version.group(1):"\"2025-06-18\"";result="{\"protocolVersion\":"+v+",\"capabilities\":{\"tools\":{}},\"serverInfo\":{\"name\":\"silverwing-fixture\",\"version\":\"1\"}}";}
                        System.out.println("{\"jsonrpc\":\"2.0\",\"id\":"+id.group(1)+",\"result\":"+result+"}");System.out.flush();
                    }
                }
            }
        """.trimIndent())
        Files.writeString(home.resolve("config.toml"),"[mcp_servers.fixture]\ncommand = ${JsonPrimitive(java.toString())}\nargs = [${JsonPrimitive(fixture.toString())}, ${JsonPrimitive(called.toString())}]\nstartup_timeout_sec = 20\n")
        val results=service(home).connections(null);val connection=assertNotNull(results["fixture"])
        assertTrue(connection.success,connection.label);assertEquals(1,connection.tools);assertFalse(Files.exists(called))
    }
}
