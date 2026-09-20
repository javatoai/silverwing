package com.snowball.silverwing.cli

import com.snowball.silverwing.core.AgentCreateTaskRequest
import com.snowball.silverwing.core.AgentOperationService
import com.snowball.silverwing.core.HandoffDocumentWriter
import com.snowball.silverwing.core.TagOperationCliFacade
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.PrintStream
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import kotlin.system.exitProcess

private val json = Json { prettyPrint = true; encodeDefaults = true; ignoreUnknownKeys = false }

@Serializable
private data class Failure(val ok: Boolean = false, val error: String)

/**
 * Intentionally small, machine-readable CLI surface. All mutations go through
 * AgentOperationService; this executable never writes task files on its own.
 */
fun main(arguments: Array<String>) {
    // Windows' default console code page can otherwise corrupt JSON string values.
    System.setOut(PrintStream(System.out, true, StandardCharsets.UTF_8))
    System.setErr(PrintStream(System.err, true, StandardCharsets.UTF_8))
    val args = arguments.toList()
    try {
        val result = execute(args)
        println("{\"ok\":true,\"result\":$result}")
    } catch (error: Throwable) {
        System.err.println(json.encodeToString(Failure(error = error.message ?: error::class.simpleName ?: "执行失败")))
        exitProcess(1)
    }
}

private fun execute(args: List<String>): String {
    if (args.isEmpty() || args.first() in setOf("--help", "-h", "help")) return json.encodeToString(help())
    return when (args.first()) {
        "agent" -> executeAgent(args)
        "tag" -> executeTag(args)
        else -> throw IllegalArgumentException("仅支持 `silverwing agent ...` 与 `silverwing tag ...`；运行 `silverwing --help` 查看受支持命令")
    }
}

private fun executeAgent(args: List<String>): String {
    val service = AgentOperationService()
    return when (args.getOrNull(1)) {
        "inspect" -> json.encodeToString(service.inspect())
        "plan" -> json.encodeToString(service.plan(readRequest(args.valueAfter("--request"))))
        "apply" -> json.encodeToString(service.apply(args.valueAfter("--operation"), args.valueAfter("--nonce")))
        "status" -> json.encodeToString(service.status(args.valueAfter("--operation")))
        "handoff-template" -> json.encodeToString(HandoffTemplate(HandoffDocumentWriter.template()))
        else -> throw IllegalArgumentException("不支持的 agent 子命令；支持 inspect、plan、apply、status、handoff-template")
    }
}

private fun executeTag(args: List<String>): String {
    val facade = TagOperationCliFacade()
    return when (args.getOrNull(1)) {
        "build" -> json.encodeToString(
            facade.build(
                taskFolder = args.valueAfter("--task"),
                selectionKeys = args.valuesAfter("--service"),
                allServices = args.contains("--all-services"),
            ),
        )
        "status" -> json.encodeToString(facade.status(args.valueAfter("--task"), args.valueAfter("--operation")))
        "history" -> json.encodeToString(facade.history(args.valueAfter("--task")))
        "retry" -> json.encodeToString(facade.retry(args.valueAfter("--task"), args.valueAfter("--operation")))
        "workspace-check" -> json.encodeToString(facade.workspaceCheck(args.valueAfter("--task"), args.valueAfter("--operation")))
        else -> throw IllegalArgumentException("不支持的 tag 子命令；支持 build、status、history、retry、workspace-check")
    }
}

@Serializable
private data class HandoffTemplate(val markdown: String)

@Serializable
private data class Help(val usage: List<String>)

private fun help(): Help = Help(
    listOf(
        "silverwing agent inspect --json",
        "silverwing agent handoff-template --json",
        "silverwing agent plan --request <request.json> --json",
        "silverwing agent apply --operation <operation-id> --nonce <nonce> --json",
        "silverwing agent status --operation <operation-id> --json",
        "silverwing tag build --task <任务文件夹名> [--service <服务ID:模块ID>]... --json",
        "silverwing tag build --task <任务文件夹名> --all-services --json",
        "silverwing tag status --task <任务文件夹名> --operation <operation-id> --json",
        "silverwing tag history --task <任务文件夹名> --json",
        "silverwing tag retry --task <任务文件夹名> --operation <operation-id> --json",
        "silverwing tag workspace-check --task <任务文件夹名> --operation <operation-id> --json",
    ),
)

private fun readRequest(value: String): AgentCreateTaskRequest {
    val path = Path.of(value).toAbsolutePath().normalize()
    require(Files.isRegularFile(path)) { "请求 JSON 文件不存在：$path" }
    return json.decodeFromString(Files.readString(path))
}

private fun List<String>.valueAfter(flag: String): String {
    val index = indexOf(flag)
    require(index >= 0) { "缺少参数：$flag" }
    return valueAt(index, flag)
}

private fun List<String>.valuesAfter(flag: String): List<String> =
    indices.filter { this[it] == flag }.map { valueAt(it, flag) }

/** A following token that looks like another flag means the value was omitted. */
private fun List<String>.valueAt(flagIndex: Int, flag: String): String {
    val value = getOrNull(flagIndex + 1)
    require(value != null && !value.startsWith("--")) { "缺少参数值：$flag" }
    return value
}
