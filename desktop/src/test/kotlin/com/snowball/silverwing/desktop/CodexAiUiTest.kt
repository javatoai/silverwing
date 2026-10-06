@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class, kotlinx.coroutines.ExperimentalCoroutinesApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.*
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import com.snowball.silverwing.core.*
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import kotlinx.serialization.json.*
import org.junit.jupiter.api.io.TempDir
import java.nio.file.*
import java.time.Duration
import kotlin.test.*

class CodexAiUiTest {
    @TempDir lateinit var root: Path
    private val io=StandardTestDispatcher()
    @org.junit.jupiter.api.BeforeEach fun setup(){Dispatchers.setMain(io)}
    @org.junit.jupiter.api.AfterEach fun restore(){Dispatchers.resetMain()}
    private var failing=false;private var enabled=true;private val writes=mutableListOf<JsonObject>()
    private fun app(onThread: (String) -> Unit = {}): DesktopApplication {
        val taskRoot=Files.createDirectories(root.resolve("tasks"))
        val paths=ApplicationPaths(Files.createTempDirectory(root,"app-"));val config=ConfigStore(paths).apply {save(AppConfig(taskRoot=taskRoot.toString(),aiRequirementNamingEnabled=false))}
        val factory=CodexRpcFactory { object:CodexRpc {
            override fun close(){}
            override fun request(method:String,params:JsonObject,timeout:Duration):JsonObject {
                if(failing)error("读取失败，请重试")
                return when(method){
                    "config/read"->buildJsonObject {
                        val local=buildJsonObject {put("mcp_servers",buildJsonObject {put("fixture-docs",buildJsonObject {put("command","fixture");put("enabled",enabled)})})}
                        put("config",local);put("layers",buildJsonArray {add(buildJsonObject {put("name",buildJsonObject {put("type","user");put("file",paths.home.resolve("codex/config.toml").toString())});put("version","fixture");put("config",local)})})
                    }
                    "config/batchWrite"->{writes+=params;val raw=params.array("edits").last().jsonObject["value"] as? JsonObject;enabled=raw?.bool("enabled",false)?:false;JsonObject(emptyMap())}
                    "mcpServerStatus/list"->Json.parseToJsonElement("""{"data":[{"name":"fixture-docs","serverInfo":{"name":"fixture"},"tools":{}}]}""").jsonObject
                    "skills/list"->buildJsonObject { put("data",buildJsonArray { add(buildJsonObject { put("skills",buildJsonArray { add(buildJsonObject {
                        val skill=Files.writeString(root.resolve("SKILL.md"),"# 团队 Skill\n\n研发说明")
                        put("name","团队 Skill");put("description","研发说明");put("path",skill.toString());put("scope","user");put("enabled",true)
                    }) }) }) }) }
                    "thread/list"->buildJsonObject {put("data",buildJsonArray {add(buildJsonObject {put("id","01a0fd65-1c49-7952-9725-369dfb7527b1");put("name","支付研发会话");put("cwd",params.text("cwd").orEmpty())})})}
                    else->JsonObject(emptyMap())
                }
            }
        } }
        return DesktopApplication(paths=paths,configStore=config,codexAiRpcFactory=factory,codexThreadOpener=onThread,ioDispatcher=io,
            developmentToolStartupDetection=DevelopmentToolStartupDetection {DevelopmentToolAutoDetectionResult(it,emptySet())})
    }
    @Test fun `MCP panel wraps actions on narrow windows and switch writes immediately in both themes`() {
        for(dark in listOf(false,true))for(width in listOf(420,1050)) {
            enabled=true;writes.clear()
            app().use {app->ImageComposeScene(width,700,coroutineContext=Dispatchers.Unconfined) {
                SilverWingTheme(if(dark)ThemePreference.DARK else ThemePreference.LIGHT){Surface {Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {CodexMcpManagementSection(app)}}}
            }.use {scene->
                scene.await {scene.text("fixture-docs")!=null}
                listOf("刷新","检测连接","添加 MCP").forEach {title->val node=assertNotNull(scene.text(title));assertTrue(node.boundsInRoot.left>=0 && node.boundsInRoot.right<=width)}
                scene.click(scene.nodes().single {it.config.getOrNull(SemanticsProperties.Role)==Role.Switch})
                scene.await {!enabled && writes.size==1 && app.codexAiController.snapshots["global"]?.servers?.single()?.enabled==false}
                assertNotNull(scene.text("已停用"));assertEquals("fixture",writes.single().text("expectedVersion"))
                scene.click(scene.text("检测连接")!!);scene.await {app.codexAiController.connections.containsKey("global")}
                scene.screenshot("${if(dark)"dark" else "light"}-mcp-$width")
            } }
        }
    }
    @Test fun `MCP read failure is inline and refresh recovers without a duplicate error`() {
        failing=true
        app().use {app->ImageComposeScene(800,600,coroutineContext=Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT){Surface {CodexMcpManagementSection(app)}}
        }.use {scene->
            scene.await {scene.text("读取失败，请重试")!=null};assertEquals(1,scene.nodes().count {it.config.getOrNull(SemanticsProperties.Text)?.any {text->text.text=="读取失败，请重试"}==true})
            failing=false;scene.click(scene.text("刷新")!!);scene.await {scene.text("fixture-docs")!=null};assertNull(scene.text("读取失败，请重试"))
        } }
    }
    @Test fun `editor retains illegal timeout and keeps save failure draft open`() {
        app().use {app->
            app.codexAiController.refresh(null);io.scheduler.runCurrent()
            val snapshot=app.codexAiController.snapshots.getValue("global")
            ImageComposeScene(1000,800,coroutineContext=Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT){Surface {McpEditorDialog(app,snapshot,snapshot.servers.single(),{}, {})}}
            }.use {scene->
                scene.await {scene.editable("60")!=null}
                scene.editable("60")!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("invalid"))
                scene.click(scene.text("保存")!!);scene.await {scene.text("工具超时必须是数字")!=null};assertNotNull(scene.editable("invalid"));assertTrue(writes.isEmpty())
                scene.editable("invalid")!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("60"));failing=true
                scene.click(scene.text("保存")!!);scene.await {scene.text("读取失败，请重试")!=null};assertNotNull(scene.editable("fixture-docs"));failing=false
                assertTrue(scene.text("读取失败，请重试")!!.boundsInRoot.top in 0f..800f)
                scene.screenshot("light-editor-error")
            }
        }
    }
    @Test fun `task more menu exposes Codex and AI files can be read on both themes and widths`() {
        val task=TaskManifest(folderName="支付渠道优化",taskDirectoryName="支付渠道优化",featureBranch="payment",createdAt="now",updatedAt="now",services=emptyList())
        val taskDirectory=Files.createDirectories(root.resolve("tasks/支付渠道优化"));Files.createDirectory(taskDirectory.resolve(".git"));Files.writeString(taskDirectory.resolve("AGENTS.md"),"# 当前任务说明\n\n支付渠道优化")
        for(dark in listOf(false,true))for(width in listOf(600,1100)) app().use {app->
            app.selectTask(task)
            ImageComposeScene(width,850,coroutineContext=Dispatchers.Unconfined) {
                SilverWingTheme(if(dark)ThemePreference.DARK else ThemePreference.LIGHT){Surface {TaskDetail(app,task,Modifier.fillMaxSize())}}
            }.use {scene->
                scene.await {scene.nodes().any {it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("更多操作")==true}}
                scene.click(scene.nodes().first {it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("更多操作")==true})
                scene.await {scene.text("AI 配置")!=null}
                assertNotNull(scene.text("在 Codex 中打开"));assertNotNull(scene.text("关联 Codex 会话"));scene.click(scene.text("AI 配置")!!)
                scene.await {scene.text("fixture-docs")!=null};scene.screenshot("${if(dark)"dark" else "light"}-task-ai-$width")
                scene.click(scene.text("Skills")!!);scene.await {scene.text("团队 Skill")!=null};scene.screenshot("${if(dark)"dark" else "light"}-task-skills-$width")
                scene.click(scene.text("说明文件")!!);scene.await {scene.text("AGENTS.md")!=null && scene.text("当前任务")!=null}
                scene.screenshot("${if(dark)"dark" else "light"}-task-guides-$width")
                scene.click(scene.text("阅读")!!);scene.await {scene.text("Agent 文件预览")!=null || scene.text("当前任务说明")!=null || scene.text("预览")!=null}
            }
        }
    }
    @Test fun `association picker persists binding and reopening uses existing thread`() {
        val task=TaskManifest(folderName="支付",taskDirectoryName="支付",featureBranch="payment",createdAt="now",updatedAt="now",services=emptyList())
        Files.createDirectories(root.resolve("tasks/支付"));val opened=mutableListOf<String>()
        app(opened::add).use {app->app.selectTask(task)
            ImageComposeScene(1000,800,coroutineContext=Dispatchers.Unconfined) {
                SilverWingTheme(ThemePreference.LIGHT){Surface {CodexTaskAssociationDialog(app,task,{})}}
            }.use {scene->
                scene.await {scene.text("支付研发会话")!=null}
                scene.click(scene.nodes().single {it.config.getOrNull(SemanticsProperties.Role)==Role.RadioButton})
                scene.await {scene.nodes().single {it.config.getOrNull(SemanticsProperties.Role)==Role.RadioButton}.config.getOrNull(SemanticsProperties.Selected)==true}
                scene.click(scene.text("保存关联")!!)
                scene.await {app.codexAiController.boundThreads.values.isNotEmpty() && !app.codexAiController.busy}
                app.openTaskInCodex(task);scene.await {opened.isNotEmpty()};assertEquals(listOf("01a0fd65-1c49-7952-9725-369dfb7527b1"),opened)
                scene.screenshot("light-task-association")
            }
        }
    }
    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(n: SemanticsNode): List<SemanticsNode> = listOf(n) + n.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }
    private fun ImageComposeScene.text(value:String)=nodes().firstOrNull {it.config.getOrNull(SemanticsProperties.Text)?.any {text->text.text==value}==true}
    private fun ImageComposeScene.editable(value:String)=nodes().firstOrNull {it.config.getOrNull(SemanticsProperties.EditableText)?.text==value}
    private fun ImageComposeScene.await(condition:()->Boolean){val end=System.nanoTime()+8_000_000_000;do{io.scheduler.runCurrent();Snapshot.sendApplyNotifications();repeat(3){render(System.nanoTime()).close()};if(condition())return;Thread.sleep(10)}while(System.nanoTime()<end);assertTrue(condition(),nodes().mapNotNull {it.config.getOrNull(SemanticsProperties.Text)?.joinToString {it.text}}.toString())}
    private fun ImageComposeScene.click(node:SemanticsNode){sendPointerEvent(PointerEventType.Press,node.boundsInRoot.center,buttons=PointerButtons(isPrimaryPressed=true),button=PointerButton.Primary);sendPointerEvent(PointerEventType.Release,node.boundsInRoot.center,buttons=PointerButtons(),button=PointerButton.Primary)}
    private fun ImageComposeScene.screenshot(name:String){repeat(20){render(System.nanoTime()).close()};val file=Path.of("build/reports/codex-ai/$name.png");Files.createDirectories(file.parent);render(System.nanoTime()).use {image->image.encodeToData()!!.use {Files.write(file,it.bytes)}}}
}
