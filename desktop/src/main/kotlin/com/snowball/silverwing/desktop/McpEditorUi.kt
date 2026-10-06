@file:OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import kotlinx.serialization.json.*

@Composable internal fun McpEditorDialog(controller: DesktopApplication, snapshot: CodexAiSnapshot, server: McpConfiguration?, onDismiss: () -> Unit, onSaved: () -> Unit) {
    val ai = controller.codexAiController; val original = remember(server) { server?.raw ?: JsonObject(emptyMap()) }
    var name by remember { mutableStateOf(server?.name.orEmpty()) }
    var global by remember { mutableStateOf(server?.scope == McpScope.GLOBAL || server == null && snapshot.cwd == null) }
    var http by remember { mutableStateOf(original.text("url") != null) }
    var endpoint by remember { mutableStateOf(original.text("url") ?: original.text("command").orEmpty()) }
    val initialArgs = remember { original.array("args").joinToString("\n") { (it as? JsonPrimitive)?.content.orEmpty() } }
    val initialEnv = remember { original.obj("env").entries.joinToString("\n") { "${it.key}=${(it.value as? JsonPrimitive)?.content.orEmpty()}" } }
    val initialHeaders = remember { original.obj("http_headers").entries.joinToString("\n") { "${it.key}=${(it.value as? JsonPrimitive)?.content.orEmpty()}" } }
    var arguments by remember { mutableStateOf(initialArgs) }; var environment by remember { mutableStateOf(initialEnv) }; var headers by remember { mutableStateOf(initialHeaders) }
    var tokenVariable by remember { mutableStateOf(original.text("bearer_token_env_var").orEmpty()) }
    var startup by remember { mutableStateOf(original.text("startup_timeout_sec") ?: "10") }; var timeout by remember { mutableStateOf(original.text("tool_timeout_sec") ?: "60") }
    var showSecrets by remember { mutableStateOf(false) }; var error by remember { mutableStateOf<String?>(null) }
    fun composeConfig(): JsonObject {
        fun pairs(text: String): JsonObject = buildJsonObject { val names = mutableSetOf<String>(); text.lineSequence().filter(String::isNotBlank).forEach { line ->
            val i = line.indexOf('='); require(i > 0) { "环境变量和请求头请按每行 NAME=VALUE 填写" }; val key = line.take(i).trim()
            require(key.isNotBlank()) { "环境变量和请求头名称不能为空" }; require(names.add(key)) { "环境变量或请求头名称重复" }; put(key, line.drop(i + 1))
        } }
        val values = original.toMutableMap(); listOf("command", "url", "args", "env", "http_headers", "bearer_token_env_var").forEach(values::remove)
        values[if (http) "url" else "command"] = JsonPrimitive(endpoint.trim())
        if (!http) {
            values["args"] = if (arguments == initialArgs && original["args"] != null) original.getValue("args") else JsonArray(arguments.lineSequence().filter(String::isNotEmpty).map(::JsonPrimitive).toList())
            values["env"] = if (environment == initialEnv) original.obj("env") else pairs(environment)
        } else {
            values["http_headers"] = if (headers == initialHeaders) original.obj("http_headers") else pairs(headers)
            tokenVariable.trim().takeIf(String::isNotEmpty)?.let { values["bearer_token_env_var"] = JsonPrimitive(it) }
        }
        values["startup_timeout_sec"] = if (startup == original.text("startup_timeout_sec")) original.getValue("startup_timeout_sec") else JsonPrimitive(startup.toLongOrNull() ?: throw IllegalArgumentException("启动超时必须是数字"))
        values["tool_timeout_sec"] = if (timeout == original.text("tool_timeout_sec")) original.getValue("tool_timeout_sec") else JsonPrimitive(timeout.toLongOrNull() ?: throw IllegalArgumentException("工具超时必须是数字"))
        return JsonObject(values).also(::validateMcpConfiguration)
    }
    AiBoundedDialog(if (server == null) "添加 MCP" else "编辑 MCP", onDismiss, ai.busy, footer = {
        TextButton(onDismiss, enabled = !ai.busy) { Text("取消") }
        Button({ try {
            val raw = composeConfig(); require(name.matches(Regex("[A-Za-z0-9_.-]{1,100}"))) { "名称只能包含英文、数字、点、下划线和短横线" }
            val target = server?.file ?: if (global) snapshot.userFile else requireNotNull(snapshot.cwd).resolve(".codex/config.toml")
            require(server != null || snapshot.servers.none { samePath(it.file, target) && it.name == name }) { "该配置中已存在同名 MCP" }
            error = null; ai.save(snapshot, target, name, raw, server?.name, onSaved)
        } catch (failure: Exception) { error = failure.message } }, enabled = !ai.busy) { Text(if (ai.busy) "正在保存…" else "保存") }
    }) {
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            AiOperationStatus(ai, snapshot.cwd)
            Column(Modifier.weight(1f).fillMaxWidth().verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            if (server == null && snapshot.cwd != null) FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilterChip(global, { global = true }, label = { Text("全局配置") }, enabled = !ai.busy); FilterChip(!global, { global = false }, label = { Text("当前任务") }, enabled = !ai.busy)
            }
            OutlinedTextField(name, { name = it }, Modifier.fillMaxWidth(), label = { Text("名称") }, singleLine = true, enabled = !ai.busy)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilterChip(!http, { if (http) { http = false; endpoint = "" } }, label = { Text("本地命令") }, enabled = !ai.busy)
                FilterChip(http, { if (!http) { http = true; endpoint = "" } }, label = { Text("HTTP 服务") }, enabled = !ai.busy)
            }
            OutlinedTextField(endpoint, { endpoint = it }, Modifier.fillMaxWidth(), label = { Text(if (http) "服务地址" else "启动程序") }, singleLine = true, enabled = !ai.busy)
            if (!http) {
                OutlinedTextField(arguments, { arguments = it }, Modifier.fillMaxWidth(), label = { Text("命令参数 · 每行一个") }, minLines = 2, enabled = !ai.busy)
                OutlinedTextField(environment, { environment = it }, Modifier.fillMaxWidth(), label = { Text("环境变量 · 每行 NAME=VALUE") }, minLines = 2, enabled = !ai.busy,
                    visualTransformation = if (showSecrets) VisualTransformation.None else PasswordVisualTransformation())
            } else {
                OutlinedTextField(tokenVariable, { tokenVariable = it }, Modifier.fillMaxWidth(), label = { Text("Bearer Token 的环境变量名（可选）") }, singleLine = true, enabled = !ai.busy)
                OutlinedTextField(headers, { headers = it }, Modifier.fillMaxWidth(), label = { Text("请求头 · 每行 NAME=VALUE（可选）") }, minLines = 2, enabled = !ai.busy,
                    visualTransformation = if (showSecrets) VisualTransformation.None else PasswordVisualTransformation())
            }
            Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(showSecrets, { showSecrets = it }, enabled = !ai.busy); Text("显示环境变量／请求头内容", style = MaterialTheme.typography.bodySmall) }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(startup, { startup = it }, Modifier.widthIn(max = 250.dp), label = { Text("启动超时（秒）") }, singleLine = true, enabled = !ai.busy)
                OutlinedTextField(timeout, { timeout = it }, Modifier.widthIn(max = 250.dp), label = { Text("工具超时（秒）") }, singleLine = true, enabled = !ai.busy)
            }
            }
        }
    }
}
