package com.snowball.silverwing.core

import java.net.URI
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CancellationException
import java.util.concurrent.TimeoutException

data class RepositoryRemoteAddress(
    val remote: String,
    val gitUrl: String,
    val fetch: Boolean,
    val push: Boolean,
)

fun interface RepositoryRemoteAddressCatalog {
    fun list(repository: Path): List<RepositoryRemoteAddress>
}

enum class RepositoryRemoteAddressReadStage { REMOTE_LIST, FETCH_ADDRESSES, PUSH_ADDRESSES }
enum class RepositoryRemoteAddressFailureKind { TIMEOUT, OTHER }

/** Only fixed categories cross the Git boundary; no diagnostic output or repository identity is retained. */
data class RepositoryRemoteAddressFailure(
    val stage: RepositoryRemoteAddressReadStage,
    val kind: RepositoryRemoteAddressFailureKind,
) {
    val message: String get() {
        val action = when (stage) {
            RepositoryRemoteAddressReadStage.REMOTE_LIST -> "远程列表"
            RepositoryRemoteAddressReadStage.FETCH_ADDRESSES -> "拉取地址"
            RepositoryRemoteAddressReadStage.PUSH_ADDRESSES -> "推送地址"
        }
        return when (kind) {
            RepositoryRemoteAddressFailureKind.TIMEOUT -> "${action}读取超时，请重试"
            RepositoryRemoteAddressFailureKind.OTHER -> "${action}读取失败，请检查本地路径和 Git 配置"
        }
    }
}

class RepositoryRemoteAddressReadException(val failure: RepositoryRemoteAddressFailure) : IllegalStateException(failure.message)

/** 只读取本地 Git 配置；原始凭据在离开读取层前移除，错误也不携带 Git 输出。 */
class GitRepositoryRemoteAddressCatalog(private val git: GitClient = GitClient()) : RepositoryRemoteAddressCatalog {
    override fun list(repository: Path): List<RepositoryRemoteAddress> {
        val names = read(repository, RepositoryRemoteAddressReadStage.REMOTE_LIST, "remote").lineSequence().map(String::trim)
            .filter(String::isNotEmpty).distinct()
            .sortedWith(compareBy<String> { it != "origin" }.thenBy { it })
            .toList()
        return names.flatMap { remote ->
            fun urls(push: Boolean): List<String> {
                val args = if (push) arrayOf("remote", "get-url", "--push", "--all", remote)
                    else arrayOf("remote", "get-url", "--all", remote)
                val stage = if (push) RepositoryRemoteAddressReadStage.PUSH_ADDRESSES else RepositoryRemoteAddressReadStage.FETCH_ADDRESSES
                return read(repository, stage, *args).lineSequence().map(String::trim)
                    .filter(String::isNotEmpty).toList()
            }
            val fetch = urls(false)
            val push = urls(true)
            val addresses = linkedMapOf<String, RepositoryRemoteAddress>()
            (fetch + push).forEach { url ->
                val safe = sanitizeGitRemoteUrl(url)
                val previous = addresses[safe]
                addresses[safe] = RepositoryRemoteAddress(remote, safe,
                    previous?.fetch == true || url in fetch, previous?.push == true || url in push)
            }
            addresses.values.toList()
        }
    }

    private fun read(repository: Path, stage: RepositoryRemoteAddressReadStage, vararg arguments: String): String = try {
        git.readOnly(repository, *arguments, timeout = Duration.ofSeconds(15)).stdout
    } catch (error: Exception) {
        if (error is InterruptedException || error is CancellationException) throw error
        val kind = if (error is TimeoutException || error is GitException && error.result.exitCode == 124)
            RepositoryRemoteAddressFailureKind.TIMEOUT else RepositoryRemoteAddressFailureKind.OTHER
        throw RepositoryRemoteAddressReadException(RepositoryRemoteAddressFailure(stage, kind))
    }
}

/** 网络 URL 移除认证信息；本地路径与 scp 路径中的特殊字符属于仓库名称。 */
fun sanitizeGitRemoteUrl(raw: String): String {
    val value = raw.trim()
    if (value.startsWith('/') || value.startsWith('\\') || value.startsWith('.')) return value
    if (value.contains("://")) {
        val prefix = value.substringBefore("://") + "://"
        // Parse valid authorities before stripping parameters: an @ in a query is
        // not user information, including URLs which have no repository path.
        val parsed = runCatching { URI(value) }.getOrNull()
        if (!prefix.equals("file://", ignoreCase = true) && parsed?.host != null) {
            return prefix + parsed.rawAuthority.substringAfterLast('@') + parsed.rawPath.orEmpty()
        }
        val rest = value.substringAfter("://")
        val authority = rest.substringBefore('/')
        val safeRest = authority.substringAfterLast('@') + rest.removePrefix(authority)
        return prefix + if (prefix.equals("file://", ignoreCase = true)) safeRest else safeRest.substringBefore('?').substringBefore('#')
    }
    // 仅去掉主机前的用户名；路径内的 @、#、? 不能被当成认证或 URL 参数。
    val colon = value.indexOf(':')
    val host = value.take(colon.coerceAtLeast(0))
    return if (colon > 0 && '@' in host && '/' !in host && '\\' !in host)
        host.substringAfterLast('@') + value.substring(colon) else value
}

fun gitRemoteBrowserUrl(raw: String): String? {
    val safe = sanitizeGitRemoteUrl(raw)
    fun webUrl(scheme: String, host: String, port: Int, path: String, encoded: Boolean = false): String? {
        if (host.isBlank() || path.isBlank() || path == "/") return null
        val trimmed = path.trimEnd('/')
        val repoPath = if (encoded) trimmed.replace(Regex("(?:\\.|%2[eE])(?:g|%67)(?:i|%69)(?:t|%74)$"), "")
            else trimmed.removeSuffix(".git")
        return runCatching {
            if (encoded) {
                val authority = URI(scheme, null, host, port, null, null, null).toASCIIString()
                URI(authority + repoPath).toASCIIString()
            } else URI(scheme, null, host, port, repoPath, null, null).toASCIIString()
        }.getOrNull()
    }
    if (safe.contains("://")) {
        val uri = runCatching { URI(safe) }.getOrNull() ?: return null
        val host = uri.host ?: return null
        return when (uri.scheme?.lowercase()) {
            "http", "https" -> webUrl(uri.scheme.lowercase(), host, uri.port, uri.rawPath.orEmpty(), encoded = true)
            "ssh" -> webUrl("https", host, -1, uri.rawPath.orEmpty(), encoded = true)
            else -> null
        }
    }
    if (Regex("^[A-Za-z]:.*").matches(safe) || safe.startsWith('/') || safe.startsWith('.')) return null
    val match = Regex("^([A-Za-z0-9.-]+):([^:].*)$").matchEntire(safe) ?: return null
    return webUrl("https", match.groupValues[1], -1, "/" + match.groupValues[2].trimStart('/'))
}
