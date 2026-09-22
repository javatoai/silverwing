package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.time.Duration
import java.util.Locale

/**
 * Business domains exposed by `lark-cli auth login --domain`.
 * `all` is intentionally excluded: an empty or implicit selection must never
 * silently request every permission from the user's Lark account.
 */
val LARK_BUSINESS_DOMAINS: List<String> = listOf(
    "application",
    "approval",
    "apps",
    "attendance",
    "base",
    "calendar",
    "contact",
    "docs",
    "drive",
    "event",
    "im",
    "mail",
    "markdown",
    "mindnotes",
    "minutes",
    "note",
    "okr",
    "sheets",
    "slides",
    "task",
    "vc",
    "wiki",
)

data class LarkCliStatus(
    val installed: Boolean,
    val version: String? = null,
    val authenticated: Boolean = false,
    val brand: String? = null,
    val tokenStatus: String? = null,
    val expiresAt: String? = null,
    val authenticationError: String? = null,
)

interface LarkCliService {
    fun status(): LarkCliStatus
    fun logout()
    fun beginDeviceCodeLogin(domains: List<String>): LarkDeviceCodeChallenge
    fun completeDeviceCodeLogin(challenge: LarkDeviceCodeChallenge)
}

/**
 * Device-code data returned by the CLI.  The opaque device code is deliberately
 * private: it can only be used by the core service to continue the login flow.
 */
class LarkDeviceCodeChallenge(
    val verificationUrl: String,
    val expiresInSeconds: Long,
    private val deviceCode: String,
) {
    init {
        require(verificationUrl.isNotBlank()) { "Lark 登录缺少授权链接" }
        require(expiresInSeconds > 0) { "Lark 登录缺少有效期" }
        require(deviceCode.isNotBlank()) { "Lark 登录缺少设备码" }
    }

    internal fun pollingCommand(executable: String): List<String> = listOf(
        executable,
        "auth",
        "login",
        "--device-code",
        deviceCode,
        "--json",
    )

    fun redactSecrets(message: String): String = message
        .replace(deviceCode, "[已隐藏]")
        .replace(verificationUrl, "[授权链接已隐藏]")
}

class ProcessLarkCliService(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name").lowercase(Locale.ROOT).contains("win"),
    private val larkExecutable: LarkExecutable = LarkExecutable.pathFallback(isWindows),
) : LarkCliService {
    override fun status(): LarkCliStatus {
        val command = executable()
        val environment = larkExecutable.environment()
        val versionResult = runCatching {
            runner.run(listOf(command, "--version"), timeout = Duration.ofSeconds(10), environment = environment)
        }.getOrElse { return LarkCliStatus(installed = false) }
        if (!versionResult.succeeded) {
            return LarkCliStatus(installed = false, authenticationError = safeError(commandError(versionResult)))
        }
        val version = normalizeVersion(versionResult.stdout.trim().ifBlank { versionResult.stderr.trim() }.ifBlank { "未知" })
        val authResult = runCatching {
            runner.run(
                listOf(command, "auth", "status", "--json"),
                timeout = Duration.ofSeconds(15),
                environment = environment,
            )
        }.getOrElse { error ->
            return LarkCliStatus(
                installed = true,
                version = version,
                authenticationError = safeError(error.message.orEmpty()),
            )
        }
        if (!authResult.succeeded) {
            return LarkCliStatus(
                installed = true,
                version = version,
                authenticationError = safeError(commandError(authResult)),
            )
        }
        val auth = runCatching { json.decodeFromString<AuthStatusResponse>(authResult.stdout) }
            .getOrElse { error ->
                return LarkCliStatus(
                    installed = true,
                    version = version,
                    authenticationError = safeError("Lark 登录状态 JSON 解析失败：${error.message}"),
                )
            }
        val user = auth.identities?.user
        val authenticated = user?.available == true &&
            user.status.equals("ready", ignoreCase = true) &&
            user.tokenStatus.equals("valid", ignoreCase = true)
        return LarkCliStatus(
            installed = true,
            version = version,
            authenticated = authenticated,
            brand = auth.brand,
            tokenStatus = user?.tokenStatus,
            expiresAt = user?.expiresAt,
            authenticationError = user?.message?.let(::safeError),
        )
    }

    override fun logout() {
        val command = executable()
        val result = runCatching {
            runner.run(
                listOf(command, "auth", "logout", "--json"),
                timeout = Duration.ofSeconds(15),
                environment = larkExecutable.environment(),
            )
        }.getOrElse { error ->
            throw IllegalStateException("退出 Lark 登录失败：${safeError(error.message.orEmpty())}", error)
        }
        check(result.succeeded) { "退出 Lark 登录失败：${safeError(commandError(result))}" }
    }

    override fun beginDeviceCodeLogin(domains: List<String>): LarkDeviceCodeChallenge {
        val selected = domains.map(String::trim).filter(String::isNotBlank).distinct()
        require(selected.isNotEmpty()) { "至少选择一个 Lark 业务域" }
        require(selected.all { it in LARK_BUSINESS_DOMAINS }) { "包含不支持的 Lark 业务域" }
        val command = executable()
        val args = buildList {
            add(command)
            add("auth")
            add("login")
            selected.forEach {
                add("--domain")
                add(it)
            }
            add("--no-wait")
            add("--json")
        }
        val result = runCatching {
            runner.run(args, timeout = Duration.ofSeconds(30), environment = larkExecutable.environment())
        }.getOrElse { error ->
            throw IllegalStateException("生成 Lark 登录授权链接失败：${safeError(error.message.orEmpty())}", error)
        }
        check(result.succeeded) { "生成 Lark 登录授权链接失败：${safeError(commandError(result))}" }
        val response = runCatching { json.decodeFromString<DeviceCodeInitResponse>(result.stdout) }
            .getOrElse { error -> throw IllegalStateException("Lark 登录授权 JSON 解析失败：${error.message}", error) }
        return LarkDeviceCodeChallenge(
            verificationUrl = response.verificationUrl,
            expiresInSeconds = response.expiresInSeconds,
            deviceCode = response.deviceCode,
        )
    }

    override fun completeDeviceCodeLogin(challenge: LarkDeviceCodeChallenge) {
        val timeoutSeconds = challenge.expiresInSeconds.coerceIn(1L, MAX_LOGIN_WAIT_SECONDS)
        val result = runCatching {
            runner.run(
                challenge.pollingCommand(executable()),
                timeout = Duration.ofSeconds(timeoutSeconds),
                environment = larkExecutable.environment(),
            )
        }.getOrElse { error ->
            throw IllegalStateException(
                "确认 Lark 登录授权失败：${challenge.redactSecrets(safeError(error.message.orEmpty()))}",
                error,
            )
        }
        check(result.succeeded) { "确认 Lark 登录授权失败：${challenge.redactSecrets(safeError(commandError(result)))}" }
    }

    private fun executable(): String = larkExecutable.resolve()

    @Serializable
    private data class AuthStatusResponse(
        val brand: String? = null,
        val identities: IdentitySet? = null,
    )

    @Serializable
    private data class IdentitySet(val user: UserIdentity? = null)

    @Serializable
    private data class UserIdentity(
        val status: String? = null,
        val available: Boolean = false,
        val message: String? = null,
        val tokenStatus: String? = null,
        val expiresAt: String? = null,
    )

    @Serializable
    private data class DeviceCodeInitResponse(
        @kotlinx.serialization.SerialName("verification_url") val verificationUrl: String = "",
        @kotlinx.serialization.SerialName("device_code") val deviceCode: String = "",
        @kotlinx.serialization.SerialName("expires_in") val expiresInSeconds: Long = 0,
        val hint: String? = null,
    )

    private fun commandError(result: CommandResult): String =
        result.stderr.ifBlank { result.stdout }.trim().ifBlank { "退出码 ${result.exitCode}" }

    private fun normalizeVersion(output: String): String =
        Regex("""\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?""").find(output)?.value ?: output

    private fun safeError(message: String): String = message
        .replace(Regex("(?i)(device[_-]?code|access[_-]?token|refresh[_-]?token)\\s*[:=]\\s*\\S+"), "$1=[已隐藏]")
        .replace(Regex("https?://\\S+"), "[链接已隐藏]")
        .lineSequence()
        .firstOrNull()
        ?.trim()
        ?.take(MAX_ERROR_LENGTH)
        .orEmpty()
        .ifBlank { "未知错误" }

    private companion object {
        const val MAX_LOGIN_WAIT_SECONDS = 10 * 60L
        const val MAX_ERROR_LENGTH = 240
        val json = Json { ignoreUnknownKeys = true }
    }
}
