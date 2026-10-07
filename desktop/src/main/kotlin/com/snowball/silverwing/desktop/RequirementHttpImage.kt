package com.snowball.silverwing.desktop

import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.TimeUnit

private val requirementImageHttpClient: HttpClient by lazy {
    HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(20)).followRedirects(HttpClient.Redirect.NORMAL).build()
}

/** Public images use HTTP; authenticated Meegle attachments retain their existing CLI transport. */
internal fun downloadRequirementHttpImage(url: String): Path {
    val uri = URI(url)
    require(uri.scheme.lowercase() in setOf("http", "https") && uri.host != null && uri.rawUserInfo == null) { "图片地址不受支持" }
    val request = HttpRequest.newBuilder(uri).timeout(Duration.ofSeconds(60)).GET().build()
    val pending = requirementImageHttpClient.sendAsync(request, HttpResponse.BodyHandlers.ofByteArray())
    try {
        val response = pending.get(60, TimeUnit.SECONDS)
        check(response.statusCode() in 200..299) { "图片读取失败（HTTP ${response.statusCode()}）" }
        val bytes = response.body()
        require(bytes.size <= 32 * 1024 * 1024) { "图片超过 32 MiB，无法缓存" }
        requirementImageExtension(bytes)
        val temporary = Files.createTempFile("silverwing-requirement-image-", ".tmp")
        try { Files.write(temporary, bytes); return temporary }
        catch (error: Exception) { Files.deleteIfExists(temporary); throw error }
    } finally { pending.cancel(true) }
}
