package com.snowball.silverwing.desktop

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Required
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.*
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardCopyOption.REPLACE_EXISTING
import java.security.MessageDigest
import java.time.Clock
import java.time.Duration
import java.util.concurrent.ConcurrentHashMap

internal val REQUIREMENT_CACHE_TTL: Duration = Duration.ofDays(14)
internal data class RequirementCachedValue<T>(val value: T, val fetchedAt: Long)

/** Rebuildable files under ApplicationPaths.cache, separate from the strict configuration schema. */
internal class RequirementReadCache(directory: Path, private val clock: Clock = Clock.systemUTC()) {
    private val directory = directory.toAbsolutePath().normalize()
    private val guard = guards.computeIfAbsent(this.directory) { Any() }
    private var lastCleanup: Long? = null

    fun <T> read(key: String, serializer: KSerializer<T>): RequirementCachedValue<T>? = synchronized(guard) {
        cleanupIfDue()
        val file = file(key)
        val envelope = readEnvelope(file) ?: return@synchronized null
        if (envelope.key != digest(key)) { discard(file); return@synchronized null }
        try { RequirementCachedValue(json.decodeFromJsonElement(serializer, envelope.value), envelope.fetchedAt) }
        catch (_: Exception) { discard(file); null }
    }

    fun <T> write(key: String, serializer: KSerializer<T>, value: T, fetchedAt: Long = clock.millis()) = synchronized(guard) {
        cleanupIfDue()
        // A failed write must neither erase the previous file nor fail a successful source read.
        runCatching {
            val envelope = Envelope(key = digest(key), fetchedAt = fetchedAt, value = json.encodeToJsonElement(serializer, value))
            val bytes = json.encodeToString(Envelope.serializer(), envelope).toByteArray(Charsets.UTF_8)
            if (bytes.size > MAX_BYTES) return@runCatching
            Files.createDirectories(directory)
            val temporary = Files.createTempFile(directory, ".requirement-", ".tmp")
            try {
                Files.write(temporary, bytes)
                try { Files.move(temporary, file(key), ATOMIC_MOVE, REPLACE_EXISTING) }
                catch (_: AtomicMoveNotSupportedException) { Files.move(temporary, file(key), REPLACE_EXISTING) }
            } finally { Files.deleteIfExists(temporary) }
        }
        Unit
    }

    /** First use and at most hourly thereafter; every individual read still checks its exact expiry. */
    fun cleanup() = synchronized(guard) { lastCleanup = null; cleanupIfDue() }

    private fun cleanupIfDue() {
        val now = clock.millis()
        if (lastCleanup?.let { now >= it && now - it < Duration.ofHours(1).toMillis() } == true) return
        lastCleanup = now
        runCatching {
            if (!Files.isDirectory(directory, NOFOLLOW_LINKS)) return@runCatching
            Files.newDirectoryStream(directory).use { files ->
                files.forEach { file ->
                    if (file.fileName.toString().matches(Regex("[a-f0-9]{64}\\.json"))) readEnvelope(file)
                    else if (file.fileName.toString().startsWith(".requirement-") && file.toString().endsWith(".tmp") &&
                        Files.isRegularFile(file, NOFOLLOW_LINKS) && !fresh(Files.getLastModifiedTime(file).toMillis(), now)) discard(file)
                }
            }
        }
    }

    private fun readEnvelope(file: Path): Envelope? = try {
        if (!Files.isRegularFile(file, NOFOLLOW_LINKS)) null else {
            val bytes = Files.newInputStream(file).use { it.readNBytes(MAX_BYTES + 1) }
            require(bytes.size <= MAX_BYTES)
            val saved = json.decodeFromString(Envelope.serializer(), bytes.toString(Charsets.UTF_8))
            require(saved.version == 1 && fresh(saved.fetchedAt, clock.millis()))
            saved
        }
    } catch (_: Exception) { discard(file); null }

    private fun discard(file: Path) { runCatching { Files.deleteIfExists(file) } }
    private fun file(key: String): Path = directory.resolve("${digest(key)}.json")

    @Serializable private data class Envelope(@Required val version: Int = 1, val key: String, val fetchedAt: Long, val value: JsonElement)

    companion object {
        private const val MAX_BYTES = 16 * 1024 * 1024
        private val guards = ConcurrentHashMap<Path, Any>()
        private val json = Json { encodeDefaults = true }
        fun fresh(fetchedAt: Long, now: Long): Boolean = fetchedAt >= 0 && fetchedAt <= now && now - fetchedAt < REQUIREMENT_CACHE_TTL.toMillis()
        private fun digest(value: String): String = MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }
    }
}

/** Identity checks run off the UI thread. If identity cannot be proven, read live without a cache. */
internal class RequirementCacheAccess(val cache: RequirementReadCache, private val readIdentity: () -> String?) {
    private val identityMutex = Mutex()
    private val identityGuard = Any()
    private var identityRevision = 0L
    private var latestIdentity: String? = null

    suspend fun identity(dispatcher: CoroutineDispatcher): String? {
        val startedAtRevision = synchronized(identityGuard) { identityRevision }
        return identityMutex.withLock {
            // Concurrent readers may share the check already running when they started, but a later
            // read always checks again. No time window can hide an external CLI account switch.
            synchronized(identityGuard) {
                if (startedAtRevision != identityRevision) return@withLock latestIdentity
            }
            val identity = try { runInterruptible(dispatcher) { readIdentity() } }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { null }
            synchronized(identityGuard) { latestIdentity = identity; identityRevision++ }
            identity
        }
    }

    suspend fun verify(expectedIdentity: String?, dispatcher: CoroutineDispatcher) {
        if (expectedIdentity != null && identity(dispatcher) != expectedIdentity) throw RequirementIdentityChangedException()
    }
}

internal class RequirementIdentityChangedException : IllegalStateException("Meegle 登录身份已变化，请重新读取需求")

internal fun requirementReadKey(kind: String, identity: String, vararg context: String?): String = buildJsonArray {
    add(kind); add(identity); context.forEach { add(it?.let(::JsonPrimitive) ?: JsonNull) }
}.toString()

internal fun requirementBodyIdentity(item: com.snowball.silverwing.core.ParticipatedWorkItem): String =
    requirementReadKey("item", item.projectKey, item.type, item.id,
        com.snowball.silverwing.core.FeishuWorkItemLink.parse(item.url)?.let { "project.feishu.cn/${it.space}/${it.kind}/${it.workItemId}" } ?: item.url.trim())
