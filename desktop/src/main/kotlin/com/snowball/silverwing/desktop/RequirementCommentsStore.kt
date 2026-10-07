package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.RequirementComments
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.nio.file.*
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.StandardCopyOption.*
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap

internal data class CachedRequirementComments(val key: String, val data: RequirementComments, val fetchedAt: Long, val path: Path?)

/** One comments file per account and requirement; independent of the Markdown body and its expiry. */
internal class RequirementCommentsStore(directory: Path) {
    private val directory = directory.toAbsolutePath().normalize()
    private val guard = guards.computeIfAbsent(this.directory) { Any() }
    fun read(key: String): CachedRequirementComments? = synchronized(guard) {
        val file = file(key)
        if (!Files.exists(file, NOFOLLOW_LINKS)) return@synchronized null
        require(Files.isRegularFile(file, NOFOLLOW_LINKS) && !Files.isSymbolicLink(file)) { "评论缓存不是普通文件" }
        val bytes = Files.newInputStream(file).use { it.readNBytes(MAX_BYTES + 1) }
        require(bytes.size <= MAX_BYTES) { "评论缓存超过大小限制" }
        val saved = json.decodeFromString(Envelope.serializer(), bytes.toString(Charsets.UTF_8))
        require(saved.version == 1 && saved.key == digest(key) && saved.fetchedAt >= 0) { "评论缓存格式无效" }
        CachedRequirementComments(key, saved.data, saved.fetchedAt, file)
    }
    fun write(key: String, data: RequirementComments, fetchedAt: Long): CachedRequirementComments = synchronized(guard) {
        val bytes = json.encodeToString(Envelope.serializer(), Envelope(key = digest(key), fetchedAt = fetchedAt, data = data)).toByteArray()
        require(bytes.size <= MAX_BYTES) { "评论缓存超过大小限制" }
        Files.createDirectories(directory)
        val temporary = Files.createTempFile(directory, ".comments-", ".tmp")
        try {
            Files.write(temporary, bytes)
            val verified = json.decodeFromString(Envelope.serializer(), Files.readString(temporary))
            check(verified.data == data) { "评论缓存写入校验失败" }
            try { Files.move(temporary, file(key), ATOMIC_MOVE, REPLACE_EXISTING) }
            catch (_: AtomicMoveNotSupportedException) { Files.move(temporary, file(key), REPLACE_EXISTING) }
        } finally { Files.deleteIfExists(temporary) }
        CachedRequirementComments(key, data, fetchedAt, file(key))
    }
    private fun file(key: String) = directory.resolve("${digest(key)}.json")
    @Serializable private data class Envelope(val version: Int = 1, val key: String, val fetchedAt: Long, val data: RequirementComments)
    companion object {
        private const val MAX_BYTES = 16 * 1024 * 1024
        private val guards = ConcurrentHashMap<Path, Any>()
        private val json = Json { encodeDefaults = true }
        private fun digest(value: String) = MessageDigest.getInstance("SHA-256").digest(value.toByteArray())
            .joinToString("") { "%02x".format(it) }
    }
}
