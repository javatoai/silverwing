package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.time.Duration
import java.util.Locale

@Serializable
data class MeegleProjectSummary(
    val name: String,
    @kotlinx.serialization.SerialName("project_key") val projectKey: String,
    @kotlinx.serialization.SerialName("simple_name") val simpleName: String,
)

fun interface MeegleProjectCatalog {
    fun list(): List<MeegleProjectSummary>
}

data class MeegleCliStatus(
    val installed: Boolean,
    val version: String? = null,
    val authenticated: Boolean = false,
    val host: String? = null,
    val expiresInMinutes: Long? = null,
    val authenticationError: String? = null,
)

interface MeegleCliService {
    fun status(): MeegleCliStatus
    fun login(host: String = "project.feishu.cn")
}

class ProcessMeegleCliService(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
) : MeegleCliService {
    override fun status(): MeegleCliStatus {
        val command = executable()
        val environment = meegleExecutable.environment()
        val versionResult = runCatching {
            runner.run(
                listOf(command, "--version"),
                timeout = Duration.ofSeconds(10),
                environment = environment,
            )
        }.getOrElse { return MeegleCliStatus(installed = false, version = null) }
        check(versionResult.succeeded) {
            "读取 Meegle CLI 版本失败：${commandError(versionResult)}"
        }
        val version = versionResult.stdout.trim().ifBlank { versionResult.stderr.trim() }.ifBlank { "未知" }
        val authResult = runner.run(
            listOf(command, "auth", "status", "--format", "json"),
            timeout = Duration.ofSeconds(15),
            environment = environment,
        )
        if (!authResult.succeeded) {
            return MeegleCliStatus(
                installed = true,
                version = version,
                authenticated = false,
                authenticationError = commandError(authResult),
            )
        }
        val auth = runCatching { json.decodeFromString<AuthResponse>(authResult.stdout) }
            .getOrElse { error -> throw IllegalStateException("Meegle 登录状态 JSON 解析失败：${error.message}", error) }
        return MeegleCliStatus(
            installed = true,
            version = version,
            authenticated = auth.authenticated,
            host = auth.host,
            expiresInMinutes = auth.expiresInMinutes,
        )
    }

    override fun login(host: String) {
        require(host.isNotBlank()) { "Meegle 登录站点不能为空" }
        val command = executable()
        val result = runner.run(
            listOf(command, "auth", "login", "--host", host, "--format", "json"),
            timeout = Duration.ofMinutes(10),
            environment = meegleExecutable.environment(),
        )
        check(result.succeeded) { "Meegle OAuth 登录失败：${commandError(result)}" }
    }

    private fun executable(): String = meegleExecutable.resolve()

    @Serializable
    private data class AuthResponse(
        val authenticated: Boolean = false,
        val host: String? = null,
        @kotlinx.serialization.SerialName("expires_in_minutes") val expiresInMinutes: Long? = null,
    )

    private fun commandError(result: CommandResult): String =
        result.stderr.ifBlank { result.stdout }.trim().ifBlank { "退出码 ${result.exitCode}" }

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
    }
}

class CliMeegleProjectCatalog(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
) : MeegleProjectCatalog {
    override fun list(): List<MeegleProjectSummary> {
        val command = meegleExecutable.resolve()
        val result = runner.run(
            listOf(
                command,
                "project",
                "search",
                "--auto-paginate",
                "--format",
                "json",
            ),
            timeout = Duration.ofSeconds(20),
            environment = meegleExecutable.environment(),
        )
        check(result.succeeded) {
            "读取 Meegle 项目失败：${result.stderr.ifBlank { result.stdout }.trim().ifBlank { "退出码 ${result.exitCode}" }}"
        }
        return runCatching { json.decodeFromString<ProjectResponse>(result.stdout).projects }
            .getOrElse { error -> throw IllegalStateException("Meegle 项目 JSON 解析失败：${error.message}", error) }
            .onEach { project ->
                require(project.name.isNotBlank() && project.projectKey.isNotBlank() && project.simpleName.isNotBlank()) {
                    "Meegle 项目缺少 name、project_key 或 simple_name"
                }
            }
    }

    @Serializable
    private data class ProjectResponse(val projects: List<MeegleProjectSummary> = emptyList())

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
    }
}
