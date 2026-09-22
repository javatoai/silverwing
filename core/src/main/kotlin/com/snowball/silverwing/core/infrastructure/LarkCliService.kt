package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
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

enum class LarkAuthenticationState { AUTHENTICATED, LOGIN_REQUIRED, CHECK_FAILED }

data class LarkCliStatus(
    val installed: Boolean,
    val version: String? = null,
    val authenticated: Boolean = false,
    val authenticationState: LarkAuthenticationState = if (authenticated) {
        LarkAuthenticationState.AUTHENTICATED
    } else {
        LarkAuthenticationState.LOGIN_REQUIRED
    },
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
            runJsonCommandWithLegacyFallback(
                listOf(command, "auth", "status"),
                timeout = Duration.ofSeconds(15),
                environment = environment,
            )
        }.getOrElse { error ->
            return LarkCliStatus(
                installed = true,
                version = version,
                authenticationState = LarkAuthenticationState.CHECK_FAILED,
                authenticationError = safeError(error.message.orEmpty()),
            )
        }
        if (!authResult.succeeded) {
            return LarkCliStatus(
                installed = true,
                version = version,
                authenticationState = LarkAuthenticationState.CHECK_FAILED,
                authenticationError = safeError(commandError(authResult)),
            )
        }
        val auth = runCatching { parseAuthStatus(authResult.stdout) }
            .getOrElse { error ->
                return LarkCliStatus(
                    installed = true,
                    version = version,
                    authenticationState = LarkAuthenticationState.CHECK_FAILED,
                    authenticationError = safeError(error.message.orEmpty()).ifBlank {
                        "Lark 登录状态返回格式无法识别"
                    },
                )
            }
        val user = auth.response.userIdentity(auth.declaredIdentity)
        val authenticated = user?.available == true
        return LarkCliStatus(
            installed = true,
            version = version,
            authenticated = authenticated,
            authenticationState = if (authenticated) {
                LarkAuthenticationState.AUTHENTICATED
            } else {
                LarkAuthenticationState.LOGIN_REQUIRED
            },
            brand = auth.response.brand,
            tokenStatus = user?.tokenStatus,
            expiresAt = user?.expiresAt,
        )
    }

    override fun logout() {
        val command = executable()
        val result = runCatching {
            runJsonCommandWithLegacyFallback(
                listOf(command, "auth", "logout"),
                timeout = Duration.ofSeconds(15),
                environment = larkExecutable.environment(),
            )
        }.getOrElse { error ->
            throw IllegalStateException("退出 Lark 登录失败：${safeError(error.message.orEmpty())}", error)
        }
        check(result.succeeded) { "退出 Lark 登录失败：${safeError(commandError(result))}" }
        ensureNoCliErrorEnvelope(result.stdout, "退出 Lark 登录")
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
        }
        val result = runCatching {
            runJsonCommandWithLegacyFallback(args, timeout = Duration.ofSeconds(30), environment = larkExecutable.environment())
        }.getOrElse { error ->
            throw IllegalStateException("生成 Lark 登录授权链接失败：${safeError(error.message.orEmpty())}", error)
        }
        check(result.succeeded) { "生成 Lark 登录授权链接失败：${safeError(commandError(result))}" }
        val response = runCatching { parseDeviceCodeInitResponse(result.stdout) }
            .getOrElse { error -> throw IllegalStateException("Lark 登录授权返回格式无法识别", error) }
        return LarkDeviceCodeChallenge(
            verificationUrl = response.verificationUrl,
            expiresInSeconds = response.expiresInSeconds,
            deviceCode = response.deviceCode,
        )
    }

    override fun completeDeviceCodeLogin(challenge: LarkDeviceCodeChallenge) {
        val timeoutSeconds = challenge.expiresInSeconds.coerceIn(1L, MAX_LOGIN_WAIT_SECONDS)
        val result = runCatching {
            runJsonCommandWithLegacyFallback(
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
        runCatching { ensureNoCliErrorEnvelope(result.stdout, "确认 Lark 登录授权") }
            .getOrElse { error ->
                throw IllegalStateException(
                    "确认 Lark 登录授权失败：${challenge.redactSecrets(safeError(error.message.orEmpty()))}",
                    error,
                )
            }
    }

    private fun executable(): String = larkExecutable.resolve()

    @Serializable
    private data class AuthStatusResponse(
        val brand: String? = null,
        val identities: IdentitySet? = null,
        val identity: String? = null,
        val user: UserIdentity? = null,
        val status: String? = null,
        val available: Boolean? = null,
        val message: String? = null,
        val tokenStatus: String? = null,
        val expiresAt: String? = null,
    )

    @Serializable
    private data class IdentitySet(val user: UserIdentity? = null)

    @Serializable
    private data class UserIdentity(
        val status: String? = null,
        val available: Boolean? = null,
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

    private data class ParsedAuthStatus(
        val response: AuthStatusResponse,
        val declaredIdentity: String?,
    )

    private data class CliPayload(
        val data: JsonElement? = null,
        val declaredIdentity: String? = null,
    )

    private fun parseAuthStatus(output: String): ParsedAuthStatus {
        val payload = parseSuccessfulCliPayload(output, "Lark 登录状态")
        return ParsedAuthStatus(
            response = json.decodeFromJsonElement<AuthStatusResponse>(
                payload.data?.jsonObject ?: throw IllegalStateException("Lark 登录状态成功响应缺少 data 对象"),
            ),
            declaredIdentity = payload.declaredIdentity,
        )
    }

    private fun parseDeviceCodeInitResponse(output: String): DeviceCodeInitResponse =
        json.decodeFromJsonElement(
            parseSuccessfulCliPayload(output, "Lark 登录授权").data?.jsonObject
                ?: throw IllegalStateException("Lark 登录授权成功响应缺少 data 对象"),
        )

    private fun AuthStatusResponse.userIdentity(declaredIdentity: String?): UserIdentity? =
        identities?.user ?: user ?: run {
            val isUser = identity.equals("user", ignoreCase = true) ||
                declaredIdentity.equals("user", ignoreCase = true)
            if (!isUser && available == null) return@run null
            UserIdentity(
                status = status,
                available = available,
                message = message,
                tokenStatus = tokenStatus,
                expiresAt = expiresAt,
            )
        }

    private fun runJsonCommandWithLegacyFallback(
        command: List<String>,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult {
        val jsonResult = runner.run(command + "--json", timeout = timeout, environment = environment)
        if (jsonResult.succeeded || !jsonFlagUnsupported(jsonResult)) return jsonResult
        return runner.run(command, timeout = timeout, environment = environment)
    }

    private fun jsonFlagUnsupported(result: CommandResult): Boolean {
        val output = commandError(result)
        return output.contains("--json", ignoreCase = true) &&
            (output.contains("unknown flag", ignoreCase = true) || output.contains("unknown option", ignoreCase = true))
    }

    private fun ensureNoCliErrorEnvelope(output: String, operation: String) {
        if (!output.trimStart().startsWith("{")) return
        parseSuccessfulCliPayload(output, operation)
    }

    private fun parseSuccessfulCliPayload(output: String, operation: String): CliPayload {
        val root = json.parseToJsonElement(output.trim()).jsonObject
        return when (root["ok"]?.jsonPrimitive?.booleanOrNull) {
            false -> throw IllegalStateException("$operation 失败：${cliEnvelopeError(root)}")
            true -> CliPayload(
                data = root["data"],
                declaredIdentity = root["identity"]?.jsonPrimitive?.contentOrNull,
            )
            null -> CliPayload(data = root, declaredIdentity = root["identity"]?.jsonPrimitive?.contentOrNull)
        }
    }

    private fun cliEnvelopeError(root: JsonObject): String {
        val error = root["error"]
        val detail = when (error) {
            is JsonObject -> listOf("message", "hint", "type")
                .mapNotNull { key -> error[key]?.jsonPrimitive?.contentOrNull }
                .joinToString("：")
            is JsonPrimitive -> error.contentOrNull
            else -> null
        }
        return detail ?: root["message"]?.jsonPrimitive?.contentOrNull ?: "未知错误"
    }

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
