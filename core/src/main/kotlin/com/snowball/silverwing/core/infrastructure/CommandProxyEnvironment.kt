package com.snowball.silverwing.core

import java.net.URI
import java.net.URLEncoder
import java.nio.file.Path
import java.time.Duration
import java.nio.charset.StandardCharsets

/**
 * Independently selectable external-command groups that may use SilverWing's
 * manually configured network proxy.
 */
@kotlinx.serialization.Serializable
enum class CommandProxyTarget {
    CODEX,
    GIT,
    WORKSPACE_COMMAND,
    MEEGLE,
    LARK,
    GENBU,
}

/** A resolved, process-local environment policy. It never mutates the parent JVM environment. */
data class CommandProxyEnvironment(
    val additions: Map<String, String>,
    val removals: Set<String>,
    val enabled: Boolean,
)

enum class CommandProxyProtocol(
    val scheme: String,
    val displayName: String,
    val defaultPort: Int,
) {
    HTTP("http", "HTTP", 80),
    HTTPS("https", "HTTPS", 443),
    SOCKS5("socks5", "SOCKS5", 1080),
    ;

    companion object {
        fun fromScheme(value: String): CommandProxyProtocol = when (value) {
            "socks", "socks5", "socks5h" -> SOCKS5
            else -> entries.firstOrNull { it.scheme == value }
        } ?: throw IllegalArgumentException("命令代理仅支持 HTTP、HTTPS 或 SOCKS5")
    }
}

/** Persisted proxy endpoint details used by the host-and-port settings form. */
data class CommandProxyEndpoint(
    val protocol: CommandProxyProtocol,
    val host: String,
    val port: Int,
)

/** Validated credentials supplied only to selected child-process proxy environments. */
data class CommandProxyAuthentication(
    val username: String,
    val password: String,
) {
    init {
        require(username.isNotBlank() && username == username.trim()) { "代理账号不能为空" }
        require(username.none { it == '\r' || it == '\n' }) { "代理账号不能包含换行" }
        require(password.isNotEmpty()) { "代理密码不能为空" }
        require(password.none { it == '\r' || it == '\n' }) { "代理密码不能包含换行" }
    }
}

/**
 * Strictly normalizes a manually entered, unauthenticated HTTP(S)/SOCKS5 proxy endpoint.
 *
 * A command proxy is deliberately a host-and-port endpoint instead of a general URL:
 * paths, query strings, fragments and embedded credentials are rejected. SOCKS is
 * deliberately constrained to SOCKS5, which supports the optional authentication pair.
 */
fun normalizeCommandProxyUrl(raw: String?): String? {
    val value = raw?.trim().orEmpty()
    if (value.isEmpty()) return null
    val uri = runCatching { URI(value) }
        .getOrElse { throw IllegalArgumentException("命令代理地址格式不合法") }
    val scheme = uri.scheme?.lowercase()
    val protocol = CommandProxyProtocol.fromScheme(scheme.orEmpty())
    require(!uri.isOpaque && !uri.host.isNullOrBlank()) { "命令代理地址必须包含主机" }
    require(uri.port in 1..65535) { "命令代理地址必须包含有效端口" }
    require(uri.rawUserInfo.isNullOrBlank()) { "代理账号和密码请填写在独立认证项中" }
    require(uri.rawPath.isNullOrEmpty() || uri.rawPath == "/") { "命令代理地址不能包含路径" }
    require(uri.rawQuery == null && uri.rawFragment == null) { "命令代理地址不能包含查询参数或片段" }
    val authority = requireNotNull(uri.rawAuthority) { "命令代理地址必须包含主机和端口" }
    return "${protocol.scheme}://$authority"
}

/** Parses a validated persisted endpoint for the host/port-based settings form. */
fun commandProxyEndpoint(raw: String?): CommandProxyEndpoint? {
    val normalized = normalizeCommandProxyUrl(raw) ?: return null
    val uri = URI(normalized)
    return CommandProxyEndpoint(
        protocol = CommandProxyProtocol.fromScheme(requireNotNull(uri.scheme).lowercase()),
        host = requireNotNull(uri.host),
        port = uri.port,
    )
}

/** Builds a canonical persisted proxy endpoint from the form's protocol, host and port controls. */
fun buildCommandProxyUrl(
    protocol: CommandProxyProtocol,
    rawHost: String,
    rawPort: String,
): String? {
    val host = rawHost.trim()
    if (host.isEmpty()) return null
    val port = rawPort.trim().toIntOrNull() ?: throw IllegalArgumentException("命令代理端口必须是数字")
    require(port in 1..65535) { "命令代理端口必须在 1 到 65535 之间" }
    val unbracketedHost = host.removePrefix("[").removeSuffix("]")
    val renderedHost = if (unbracketedHost.contains(':')) "[$unbracketedHost]" else unbracketedHost
    return requireNotNull(normalizeCommandProxyUrl("${protocol.scheme}://$renderedHost:$port"))
}

/** Normalizes the comma-separated standard NO_PROXY patterns without accepting URL fragments. */
fun normalizeCommandProxyNoProxy(raw: String?): String? {
    val value = raw?.trim().orEmpty()
    if (value.isEmpty()) return null
    require(value.none { it == '\r' || it == '\n' }) { "不代理地址不能包含换行" }
    val entries = value.split(',').map(String::trim).filter(String::isNotEmpty)
    require(entries.isNotEmpty()) { "不代理地址不能为空" }
    entries.forEach { entry ->
        require(!entry.contains("://") && !entry.contains('@')) { "不代理地址只能填写主机、IP 或通配符" }
        require(entry.none(Char::isWhitespace)) { "不代理地址不能包含空格" }
    }
    return entries.distinct().joinToString(",")
}

/** Validates the optional proxy authentication pair without exposing either value in diagnostics. */
fun commandProxyAuthentication(
    rawUsername: String?,
    rawPassword: String?,
): CommandProxyAuthentication? {
    val username = rawUsername?.trim().orEmpty().ifBlank { null }
    val password = rawPassword?.takeIf(String::isNotEmpty)
    require((username == null) == (password == null)) { "代理账号和密码必须同时填写" }
    return if (username == null) null else CommandProxyAuthentication(username, requireNotNull(password))
}

/** Adds validated proxy credentials to an already validated persisted endpoint. */
fun authenticatedCommandProxyUrl(endpoint: String, authentication: CommandProxyAuthentication?): String {
    val normalized = requireNotNull(normalizeCommandProxyUrl(endpoint))
    if (authentication == null) return normalized
    val uri = URI(normalized)
    val userInfo = "${encodeProxyCredential(authentication.username)}:${encodeProxyCredential(authentication.password)}"
    return "${uri.scheme}://$userInfo@${requireNotNull(uri.rawAuthority)}"
}

/**
 * Produces a short-lived process environment for one command family. Even an
 * unchecked family has inherited proxy variables removed, so a terminal-launched
 * desktop app cannot accidentally route a command through an unrelated proxy.
 */
class CommandProxyEnvironmentProvider(
    private val proxyUrl: () -> String?,
    private val enabledTargets: () -> Set<CommandProxyTarget>,
    private val noProxy: () -> String? = { null },
    private val authentication: () -> CommandProxyAuthentication? = { null },
) {
    fun forTarget(target: CommandProxyTarget): CommandProxyEnvironment {
        val endpoint = configuredEndpoint()
        val enabled = endpoint != null && target in enabledTargets()
        val additions = if (enabled) proxyEnvironmentValues(requireNotNull(endpoint), normalizeCommandProxyNoProxy(noProxy())) else emptyMap()
        return CommandProxyEnvironment(
            additions = additions,
            removals = PROXY_ENVIRONMENT_VARIABLES,
            enabled = enabled,
        )
    }

    /** Removes the configured endpoint from user-visible command diagnostics. */
    fun redactConfiguredEndpoint(message: String): String {
        val endpoint = runCatching { configuredEndpoint() }.getOrNull() ?: return message
        val persistedEndpoint = runCatching { normalizeCommandProxyUrl(proxyUrl()) }.getOrNull()
        return listOf(endpoint, persistedEndpoint)
            .filterNotNull()
            .distinct()
            .sortedByDescending(String::length)
            .fold(message) { result, value -> result.replace(value, "[代理地址已隐藏]", ignoreCase = true) }
    }

    private fun configuredEndpoint(): String? = normalizeCommandProxyUrl(proxyUrl())
        ?.let { authenticatedCommandProxyUrl(it, authentication()) }

    private fun proxyEnvironmentValues(endpoint: String, noProxy: String?): Map<String, String> = linkedMapOf(
        "HTTP_PROXY" to endpoint,
        "HTTPS_PROXY" to endpoint,
        "ALL_PROXY" to endpoint,
        "http_proxy" to endpoint,
        "https_proxy" to endpoint,
        "all_proxy" to endpoint,
    ).apply {
        noProxy?.let { value ->
            put("NO_PROXY", value)
            put("no_proxy", value)
        }
    }
}

private fun encodeProxyCredential(value: String): String =
    URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20")

/**
 * A target-bound runner that layers proxy policy over normal executable-specific
 * environment additions (for example a macOS login-shell PATH).
 */
class ProxyTargetCommandRunner(
    private val delegate: StreamingCommandRunner,
    private val target: CommandProxyTarget,
    private val proxyEnvironment: CommandProxyEnvironmentProvider,
) : StreamingCommandRunner {
    override fun run(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult {
        val policy = policy()
        return delegate.run(
            command = command,
            workingDirectory = workingDirectory,
            timeout = timeout,
            environment = mergedEnvironment(environment, policy),
            environmentToRemove = policy.removals,
        )
    }

    override fun runWithInput(
        command: List<String>,
        input: String,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
    ): CommandResult {
        val policy = policy()
        return delegate.runWithInput(
            command = command,
            input = input,
            workingDirectory = workingDirectory,
            timeout = timeout,
            environment = mergedEnvironment(environment, policy),
            environmentToRemove = policy.removals,
        )
    }

    override fun runStreaming(
        command: List<String>,
        workingDirectory: Path?,
        timeout: Duration,
        environment: Map<String, String>,
        onOutput: (CommandOutputLine) -> Unit,
    ): CommandResult {
        val policy = policy()
        return delegate.runStreaming(
            command = command,
            workingDirectory = workingDirectory,
            timeout = timeout,
            environment = mergedEnvironment(environment, policy),
            onOutput = onOutput,
            environmentToRemove = policy.removals,
        )
    }

    /** Exposed for UI diagnostics and focused tests without exposing the proxy URL. */
    fun proxyEnabled(): Boolean = policy().enabled

    private fun mergedEnvironment(
        base: Map<String, String>,
        policy: CommandProxyEnvironment,
    ): Map<String, String> =
        base.filterKeys { candidate -> PROXY_ENVIRONMENT_VARIABLES.none { it.equals(candidate, ignoreCase = true) } } +
            policy.additions

    private fun policy(): CommandProxyEnvironment = proxyEnvironment.forTarget(target)
}

/** Variables deliberately removed for every target before optional proxy values are set. */
internal val PROXY_ENVIRONMENT_VARIABLES: Set<String> = linkedSetOf(
    "HTTP_PROXY",
    "HTTPS_PROXY",
    "ALL_PROXY",
    "NO_PROXY",
    "http_proxy",
    "https_proxy",
    "all_proxy",
    "no_proxy",
)
