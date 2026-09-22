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
    /** 清理当前 CLI profile 的本地登录凭据；不修改 SilverWing 保存的项目配置。 */
    fun logout()
    fun beginDeviceCodeLogin(host: String = "project.feishu.cn"): MeegleDeviceCodeChallenge
    fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge): MeegleDeviceCodeLoginResult
}

private const val MIN_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS = 2L
private const val MAX_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS = 60L

/** Meegle 设备码授权中可展示给用户的短期信息。 */
class MeegleDeviceCodeChallenge(
    val host: String,
    val verificationUri: String,
    val verificationUriComplete: String?,
    val userCode: String,
    val expiresInSeconds: Long,
    private val deviceCode: String,
    private val clientId: String,
    val pollingIntervalSeconds: Long = 5,
) {
    init {
        require(host.isNotBlank()) { "Meegle 登录站点不能为空" }
        require(verificationUri.isNotBlank()) { "Meegle 设备码登录缺少授权链接" }
        require(userCode.isNotBlank()) { "Meegle 设备码登录缺少授权码" }
        require(expiresInSeconds > 0) { "Meegle 设备码登录缺少有效期" }
        require(deviceCode.isNotBlank() && clientId.isNotBlank()) { "Meegle 设备码登录响应不完整" }
        require(pollingIntervalSeconds in MIN_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS..MAX_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS) {
            "Meegle 设备码轮询间隔必须在 $MIN_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS 到 $MAX_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS 秒之间"
        }
    }

    /** CLI 返回完整链接时优先使用它，让浏览器可以预填授权码。 */
    val authorizationUrl: String get() = verificationUriComplete?.takeIf(String::isNotBlank) ?: verificationUri

    /**
     * 不透明的设备码和 client id 仅用于轮询，保留在核心层，不能被 UI 渲染或复制。
     */
    internal fun pollingCommand(executable: String): List<String> = listOf(
        executable,
        "auth",
        "login",
        "--device-code",
        "--phase",
        "poll",
        "--device-code-value",
        deviceCode,
        "--client-id",
        clientId,
        "--host",
        host,
        "--once",
        "--format",
        "json",
    )

    /**
     * 设备码轮询的底层错误可能原样回显命令参数。错误展示前统一脱敏，避免短期凭据进入
     * UI、诊断日志或截图。
     */
    fun redactSecrets(message: String): String = message
        .replace(deviceCode, "[已隐藏]")
        .replace(clientId, "[已隐藏]")
}

enum class MeegleDeviceCodeLoginResult { AUTHORIZED, PENDING, SLOW_DOWN, EXPIRED }

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

    override fun logout() {
        val command = executable()
        val result = runner.run(
            listOf(command, "auth", "logout"),
            timeout = Duration.ofSeconds(10),
            environment = meegleExecutable.environment(),
        )
        check(result.succeeded) { "退出 Meegle 登录失败：${commandError(result)}" }
    }

    override fun beginDeviceCodeLogin(host: String): MeegleDeviceCodeChallenge {
        require(host.isNotBlank()) { "Meegle 登录站点不能为空" }
        val command = executable()
        val result = runner.run(
            listOf(command, "auth", "login", "--device-code", "--phase", "init", "--host", host, "--format", "json"),
            timeout = Duration.ofSeconds(30),
            environment = meegleExecutable.environment(),
        )
        check(result.succeeded) { "生成 Meegle 登录验证码失败：${commandError(result)}" }
        val response = runCatching { json.decodeFromString<DeviceCodeInitResponse>(result.stdout) }
            .getOrElse { error -> throw IllegalStateException("Meegle 登录验证码 JSON 解析失败：${error.message}", error) }
        return MeegleDeviceCodeChallenge(
            host = host,
            verificationUri = response.verificationUri,
            verificationUriComplete = response.verificationUriComplete,
            userCode = response.userCode,
            expiresInSeconds = response.expiresInSeconds,
            deviceCode = response.deviceCode,
            clientId = response.clientId,
            pollingIntervalSeconds = response.interval.coerceIn(
                MIN_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS,
                MAX_MEEGLE_DEVICE_CODE_POLL_INTERVAL_SECONDS,
            ),
        )
    }

    override fun completeDeviceCodeLogin(challenge: MeegleDeviceCodeChallenge): MeegleDeviceCodeLoginResult {
        val result = runner.run(
            challenge.pollingCommand(executable()),
            timeout = Duration.ofSeconds(20),
            environment = meegleExecutable.environment(),
        )
        check(result.succeeded) { "确认 Meegle 登录授权失败：${commandError(result)}" }
        val response = runCatching { json.decodeFromString<DeviceCodePollResponse>(result.stdout) }
            .getOrElse { error -> throw IllegalStateException("Meegle 登录授权状态 JSON 解析失败：${error.message}", error) }
        return when (response.status) {
            "ok" -> MeegleDeviceCodeLoginResult.AUTHORIZED
            "authorization_pending" -> MeegleDeviceCodeLoginResult.PENDING
            "slow_down" -> MeegleDeviceCodeLoginResult.SLOW_DOWN
            "expired_token" -> MeegleDeviceCodeLoginResult.EXPIRED
            else -> throw IllegalStateException("Meegle 登录授权状态异常：${response.status.ifBlank { "未知" }}")
        }
    }

    private fun executable(): String = meegleExecutable.resolve()

    @Serializable
    private data class AuthResponse(
        val authenticated: Boolean = false,
        val host: String? = null,
        @kotlinx.serialization.SerialName("expires_in_minutes") val expiresInMinutes: Long? = null,
    )

    @Serializable
    private data class DeviceCodeInitResponse(
        @kotlinx.serialization.SerialName("device_code") val deviceCode: String = "",
        @kotlinx.serialization.SerialName("user_code") val userCode: String = "",
        @kotlinx.serialization.SerialName("verification_uri") val verificationUri: String = "",
        @kotlinx.serialization.SerialName("verification_uri_complete") val verificationUriComplete: String? = null,
        @kotlinx.serialization.SerialName("expires_in") val expiresInSeconds: Long = 0,
        @kotlinx.serialization.SerialName("client_id") val clientId: String = "",
        val interval: Long = 5,
    )

    @Serializable
    private data class DeviceCodePollResponse(
        val status: String = "",
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
