package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.MeegleRequirementIdentityResult
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow
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
    internal val documentDirectory: Path get() = directory.resolve("requirement-documents")
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

    /** Only a caller with the exact identity key can retire a migrated legacy body. */
    fun remove(key: String) = synchronized(guard) { Files.deleteIfExists(file(key)); Unit }

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

internal data class RequirementIdentitySnapshot(val identity: String?, val revision: Long, val confirmed: Boolean)

/** A confirmed session identity is enough for local reads; remote writes require a fresh check. */
internal class RequirementCacheAccess(
    val cache: RequirementReadCache,
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob()),
    private val probe: (() -> MeegleRequirementIdentityResult)? = null,
    private val contextStamp: (() -> Any?)? = null,
    private val readIdentity: () -> String?,
) {
    private val identityGuard = Any()
    private var session = RequirementIdentitySnapshot(null, 0L, false)
    private var stamp: Any? = contextStamp?.invoke()
    private data class Probe(val revision: Long, val result: Deferred<MeegleRequirementIdentityResult>)
    private var running: Probe? = null
    private val identityChanges = MutableSharedFlow<RequirementIdentitySnapshot>(extraBufferCapacity = 32)
    val changes = identityChanges.asSharedFlow()
    val snapshot: RequirementIdentitySnapshot get() = synchronized(identityGuard) { session }

    /** Explicit logout/configuration changes invalidate even a verification still in flight. */
    fun invalidate(loggedOut: Boolean = false) = synchronized(identityGuard) {
        session = RequirementIdentitySnapshot(null, session.revision + 1, loggedOut)
        running?.result?.cancel()
        running = null
        identityChanges.tryEmit(session)
    }

    private fun checkContext() {
        val next = contextStamp?.invoke() ?: return
        synchronized(identityGuard) {
            if (next != stamp) { stamp = next; invalidate() }
        }
    }

    suspend fun identity(dispatcher: CoroutineDispatcher): String? {
        checkContext()
        snapshot.takeIf { it.confirmed }?.let { return it.identity }
        recheck(dispatcher)
        return snapshot.identity
    }

    /** All simultaneous consumers share a probe owned by the application, not its first reader. */
    suspend fun recheck(dispatcher: CoroutineDispatcher, strict: Boolean = false): String? {
        checkContext()
        val pending = synchronized(identityGuard) {
            running?.takeIf { it.revision == session.revision && !it.result.isCompleted } ?: run {
                val revision = session.revision
                val result = scope.async(dispatcher, start = CoroutineStart.LAZY) {
                    val checked = try { runInterruptible(dispatcher) {
                        probe?.invoke() ?: readIdentity()?.let(MeegleRequirementIdentityResult::Authenticated)
                            ?: MeegleRequirementIdentityResult.Unavailable
                    } } catch (cancelled: CancellationException) { throw cancelled }
                    catch (_: Exception) { MeegleRequirementIdentityResult.Unavailable }
                    synchronized(identityGuard) {
                        if (session.revision == revision && checked != MeegleRequirementIdentityResult.Unavailable) {
                            val identity = (checked as? MeegleRequirementIdentityResult.Authenticated)?.identity
                            val changed = session.confirmed && session.identity != identity
                            session = RequirementIdentitySnapshot(identity, session.revision + if (changed) 1 else 0, true)
                            if (changed) identityChanges.tryEmit(session)
                        }
                    }
                    checked
                }
                Probe(revision, result).also { running = it }
            }
        }
        pending.result.start()
        val checked = pending.result.await()
        synchronized(identityGuard) { if (running === pending) running = null }
        if (strict && checked == MeegleRequirementIdentityResult.Unavailable)
            throw IllegalStateException("无法确认当前 Meegle 账户，请重试；本地需求已保留")
        return snapshot.identity
    }

    fun verifySession(expectedIdentity: String?, expectedRevision: Long? = null) {
        checkContext()
        val current = snapshot
        if (expectedIdentity != current.identity || (expectedRevision != null && expectedRevision != current.revision))
            throw RequirementIdentityChangedException()
    }

    fun ensureNotLoggedOut() {
        if (snapshot.confirmed && snapshot.identity == null)
            throw IllegalStateException("Meegle 已退出登录，请重新登录后读取需求")
    }

    suspend fun verify(expectedIdentity: String?, dispatcher: CoroutineDispatcher) {
        if (expectedIdentity != null && recheck(dispatcher, strict = true) != expectedIdentity)
            throw RequirementIdentityChangedException()
    }
}

internal class RequirementIdentityChangedException : IllegalStateException("Meegle 登录身份已变化，请重新读取需求")

internal fun requirementReadKey(kind: String, identity: String, vararg context: String?): String = buildJsonArray {
    add(kind); add(identity); context.forEach { add(it?.let(::JsonPrimitive) ?: JsonNull) }
}.toString()

internal fun requirementBodyIdentity(item: com.snowball.silverwing.core.ParticipatedWorkItem): String =
    requirementReadKey("item", item.projectKey, item.type, item.id,
        com.snowball.silverwing.core.FeishuWorkItemLink.parse(item.url)?.let { "project.feishu.cn/${it.space}/${it.kind}/${it.workItemId}" } ?: item.url.trim())
