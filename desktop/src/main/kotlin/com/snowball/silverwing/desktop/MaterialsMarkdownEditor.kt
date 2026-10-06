package com.snowball.silverwing.desktop

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean

internal const val MAX_MARKDOWN_EDITOR_BYTES = 512L * 1024

internal data class MarkdownDraftIdentity(
    val taskPath: String,
    val materialsRoot: String,
    val relativePath: String,
) {
    val stableKey: String get() = listOf(taskPath, materialsRoot, relativePath).joinToString("\u0000")
}

internal data class MarkdownDraft(
    val content: String,
    val baselineDigest: String,
    val bom: Boolean,
    val newline: String,
)

internal data class MarkdownEditorConflict(
    val external: MarkdownFileSnapshot,
    val externalContent: String,
)

internal data class MarkdownEditorSession(
    val initialized: Boolean = false,
    val content: String = "",
    val baseline: MarkdownFileSnapshot? = null,
    val draftBaselineDigest: String? = null,
    val dirty: Boolean = false,
    val busy: Boolean = false,
    val revision: Long = 0,
    val saveRevision: Long? = null,
    val conflict: MarkdownEditorConflict? = null,
    val error: String? = null,
    val cacheError: String? = null,
)

internal data class MarkdownSaveToken(
    val identity: MarkdownDraftIdentity,
    val revision: Long,
    val content: String,
    val baselineDigest: String,
)

@Serializable
private data class MarkdownDraftRecord(
    val key: String,
    val content: String,
    val baselineDigest: String,
    val bom: Boolean = false,
    val newline: String = "\n",
)

@Serializable
private data class MarkdownDraftFile(
    val schema: Int = 1,
    val records: List<MarkdownDraftRecord> = emptyList(),
)

/** Runtime-only Markdown drafts. A draft is never written into a task or config file. */
internal class MarkdownDraftStore(
    private val path: Path,
    private val maxEntries: Int = 100,
) {
    private companion object { const val MAX_BYTES = 8 * 1024 * 1024; const val DEBOUNCE_MILLIS = 250L }
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }
    private val lock = Any()
    private val records = linkedMapOf<String, MarkdownDraft>()
    private val sessions = ConcurrentHashMap<String, MutableStateFlow<MarkdownEditorSession>>()
    private val busyState = MutableStateFlow(false)
    // Conservative estimate so typing does not repeatedly serialize the whole
    // cache on the UI thread. The exact JSON payload is checked by the writer.
    private var estimatedBytes: Long = 2L
    private var loaded = false
    private var pending: ScheduledFuture<*>? = null
    private val writer = Executors.newSingleThreadScheduledExecutor { runnable ->
        Thread(runnable, "silverwing-markdown-draft-cache").apply { isDaemon = true }
    }

    fun get(identity: MarkdownDraftIdentity): MarkdownDraft? = synchronized(lock) { records[identity.stableKey] }

    fun session(identity: MarkdownDraftIdentity): StateFlow<MarkdownEditorSession> =
        sessions.computeIfAbsent(identity.stableKey) { MutableStateFlow(MarkdownEditorSession()) }

    fun initialize(identity: MarkdownDraftIdentity, snapshot: MarkdownFileSnapshot, originalContent: String) {
        val flow = sessions.computeIfAbsent(identity.stableKey) { MutableStateFlow(MarkdownEditorSession()) }
        synchronized(lock) {
            val draft = records[identity.stableKey]
            flow.update { current ->
                if (current.initialized && current.busy) current
                else {
                    val content = draft?.content ?: originalContent
                    val expected = draft?.baselineDigest ?: snapshot.digest
                    val conflict = draft?.takeIf { it.baselineDigest != snapshot.digest }?.let {
                        MarkdownEditorConflict(snapshot, snapshot.utf8Content())
                    }
                    current.copy(
                        initialized = true,
                        content = content,
                        baseline = snapshot,
                        draftBaselineDigest = expected,
                        dirty = runCatching { MaterialsMarkdownEditorService().encode(content, snapshot.bom, snapshot.newline).contentEquals(snapshot.bytes).not() }
                            .getOrDefault(content != originalContent),
                        conflict = conflict,
                        error = null,
                    )
                }
            }
        }
    }

    fun updateContent(identity: MarkdownDraftIdentity, value: String) {
        val flow = sessions.computeIfAbsent(identity.stableKey) { MutableStateFlow(MarkdownEditorSession()) }
        if (value.toByteArray(StandardCharsets.UTF_8).size > MAX_MARKDOWN_EDITOR_BYTES) {
            flow.update { it.copy(error = "Markdown 内容超过 512 KB，无法继续输入。") }
            return
        }
        val current = flow.value
        if (current.busy) return
        val baseline = current.baseline ?: return
        val draft = MarkdownDraft(value, current.draftBaselineDigest ?: baseline.digest, baseline.bom, baseline.newline)
        try {
            put(identity, draft)
            flow.update { it.copy(content = value, dirty = !MaterialsMarkdownEditorService().encode(value, baseline.bom, baseline.newline).contentEquals(baseline.bytes), error = null, revision = it.revision + 1) }
        } catch (error: Throwable) {
            flow.update {
                it.copy(content = value, dirty = true, error = null,
                    cacheError = error.message ?: "无法保存草稿缓存。", revision = it.revision + 1)
            }
        }
    }

    fun beginSave(identity: MarkdownDraftIdentity, force: Boolean = false): MarkdownSaveToken? {
        val flow = sessions[identity.stableKey] ?: return null
        var token: MarkdownSaveToken? = null
        flow.update { current ->
            val baseline = current.baseline ?: return@update current
            if (!current.initialized || current.busy || !current.dirty) return@update current
            val revision = current.revision + 1
            val digest = if (force) current.conflict?.external?.digest ?: baseline.digest else current.draftBaselineDigest ?: baseline.digest
            token = MarkdownSaveToken(identity, revision, current.content, digest)
            current.copy(busy = true, saveRevision = revision, revision = revision, error = null)
        }
        refreshBusyState()
        return token
    }

    fun completeSaved(token: MarkdownSaveToken, snapshot: MarkdownFileSnapshot): Boolean {
        val flow = sessions[token.identity.stableKey] ?: return false
        var accepted = false
        flow.update { current ->
            if (current.saveRevision != token.revision) return@update current
            accepted = true
            current.copy(baseline = snapshot, draftBaselineDigest = snapshot.digest, dirty = false, busy = false,
                saveRevision = null, conflict = null, error = null)
        }
        if (accepted) removeSavedDraftIfUnchanged(token)
        refreshBusyState()
        return accepted
    }

    fun completeConflict(token: MarkdownSaveToken, currentSnapshot: MarkdownFileSnapshot) {
        sessions[token.identity.stableKey]?.update { current ->
            if (current.saveRevision != token.revision) current else current.copy(
                baseline = currentSnapshot,
                busy = false,
                saveRevision = null,
                conflict = MarkdownEditorConflict(currentSnapshot, currentSnapshot.utf8Content()),
            )
        }
        refreshBusyState()
    }

    fun completeFailure(token: MarkdownSaveToken, message: String): Boolean {
        var accepted = false
        sessions[token.identity.stableKey]?.update { current ->
            if (current.saveRevision != token.revision) current else {
                accepted = true
                current.copy(busy = false, saveRevision = null, error = message)
            }
        }
        refreshBusyState()
        return accepted
    }

    fun useExternal(identity: MarkdownDraftIdentity) {
        val flow = sessions[identity.stableKey] ?: return
        val conflict = flow.value.conflict ?: return
        remove(identity)
        flow.update { it.copy(content = conflict.externalContent, baseline = conflict.external, draftBaselineDigest = conflict.external.digest,
            dirty = false, conflict = null, error = null) }
    }

    fun discard(identity: MarkdownDraftIdentity) {
        val flow = sessions[identity.stableKey] ?: return
        val baseline = flow.value.baseline ?: return
        remove(identity)
        flow.update { it.copy(content = baseline.utf8Content(), draftBaselineDigest = baseline.digest, dirty = false, conflict = null, error = null) }
    }

    fun retryCache(identity: MarkdownDraftIdentity) {
        val flow = sessions[identity.stableKey]
        val state = flow?.value
        val baseline = state?.baseline
        if (state?.dirty == true && baseline != null) {
            runCatching {
                put(identity, MarkdownDraft(state.content, state.draftBaselineDigest ?: baseline.digest, baseline.bom, baseline.newline))
            }.onSuccess {
                flow.update { it.copy(cacheError = null) }
            }.onFailure { error ->
                flow.update { it.copy(cacheError = error.message ?: "无法保存草稿缓存。") }
            }
        } else {
            synchronized(lock) { scheduleWriteLocked() }
            flow?.update { it.copy(cacheError = null) }
        }
    }

    /** Migrate all drafts and live sessions for a file or renamed directory. */
    fun rename(taskPath: String, materialsRoot: String, previous: String, replacement: String) {
        val prefix = "$taskPath\u0000$materialsRoot\u0000"
        fun mapped(key: String): String? {
            if (!key.startsWith(prefix)) return null
            val path = key.removePrefix(prefix)
            return when {
                path == previous -> prefix + replacement
                path.startsWith("$previous/") -> prefix + replacement + path.removePrefix(previous)
                else -> null
            }
        }
        synchronized(lock) {
            records.entries.toList().forEach { (key, value) -> mapped(key)?.let { next -> records.remove(key); records[next] = value } }
            estimatedBytes = records.entries.sumOf { (key, draft) -> estimateRecordBytes(key, draft) }.coerceAtLeast(2L)
            scheduleWriteLocked()
        }
        sessions.entries.toList().forEach { (key, flow) -> mapped(key)?.let { next -> sessions.remove(key); sessions[next] = flow } }
    }

    /** Called from the app IO scope and by the editor before first use. */
    fun loadFromDisk() {
        synchronized(lock) {
        if (loaded) return
        loaded = true
        runCatching {
            if (!Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS) || Files.size(path) > MAX_BYTES) return@runCatching
            val bytes = Files.newInputStream(path, LinkOption.NOFOLLOW_LINKS).use { it.readNBytes(MAX_BYTES + 1) }
            if (bytes.size > MAX_BYTES) return@runCatching
            val file = json.decodeFromString<MarkdownDraftFile>(bytes.toString(StandardCharsets.UTF_8))
            if (file.schema != 1) return@runCatching
            var used = 0L
            file.records.forEach { record ->
                val draft = MarkdownDraft(record.content, record.baselineDigest, record.bom, if (record.newline == "\r\n") "\r\n" else "\n")
                val bytesUsed = draft.content.toByteArray(StandardCharsets.UTF_8).size
                val estimate = estimateRecordBytes(record.key, draft)
                if (record.key.isNotBlank() && bytesUsed <= MAX_MARKDOWN_EDITOR_BYTES && used + estimate <= MAX_BYTES.toLong() && records.size < maxEntries) {
                    records[record.key] = draft; used += estimate
                }
            }
            estimatedBytes = used.coerceAtLeast(2L)
        }
        }
    }

    /** Updates memory synchronously so a task/page switch cannot lose a just typed draft. */
    fun put(identity: MarkdownDraftIdentity, draft: MarkdownDraft) {
        require(draft.content.toByteArray(StandardCharsets.UTF_8).size <= MAX_MARKDOWN_EDITOR_BYTES) { "Markdown 草稿超过 512 KB" }
        synchronized(lock) {
            require(records.containsKey(identity.stableKey) || records.size < maxEntries) { "Markdown 草稿缓存已达到条数上限" }
            val previous = records[identity.stableKey]
            val nextEstimate = estimatedBytes - (previous?.let { estimateRecordBytes(identity.stableKey, it) } ?: 0L) +
                estimateRecordBytes(identity.stableKey, draft)
            require(nextEstimate <= MAX_BYTES) { "Markdown 草稿缓存已达到空间上限" }
            records[identity.stableKey] = draft
            estimatedBytes = nextEstimate
            scheduleWriteLocked()
        }
    }

    fun remove(identity: MarkdownDraftIdentity) {
        synchronized(lock) {
            records.remove(identity.stableKey)?.let {
                estimatedBytes = (estimatedBytes - estimateRecordBytes(identity.stableKey, it)).coerceAtLeast(2L)
                scheduleWriteLocked()
            }
        }
    }

    private fun removeSavedDraftIfUnchanged(token: MarkdownSaveToken) {
        val flow = sessions[token.identity.stableKey]
        synchronized(lock) {
            val state = flow?.value
            if (state == null || state.revision != token.revision || state.dirty || state.busy) return
            records.remove(token.identity.stableKey)?.let {
                estimatedBytes = (estimatedBytes - estimateRecordBytes(token.identity.stableKey, it)).coerceAtLeast(2L)
                scheduleWriteLocked()
            }
        }
    }

    fun clear() {
        synchronized(lock) { records.clear(); estimatedBytes = 2L; scheduleWriteLocked() }
    }

    fun flush() {
        val snapshot = synchronized(lock) {
            pending?.cancel(false)
            pending = null
            records.toMap()
        }
        submitAndWait(snapshot)
    }

    fun close() {
        flush()
        writer.shutdown()
        runCatching { writer.awaitTermination(2, TimeUnit.SECONDS) }
    }

    internal fun snapshot(): Map<String, MarkdownDraft> = synchronized(lock) { records.toMap() }

    /** Used by file operations to avoid renaming/deleting an identity mid-save. */
    fun anyBusy(): Boolean = busyState.value

    val busy: StateFlow<Boolean> get() = busyState

    private fun refreshBusyState() {
        busyState.value = sessions.values.any { it.value.busy }
    }

    private fun scheduleWriteLocked() {
        pending?.cancel(false)
        pending = writer.schedule({
            val snapshot = synchronized(lock) {
                pending = null
                records.toMap()
            }
            persistSnapshot(snapshot)
        }, DEBOUNCE_MILLIS, TimeUnit.MILLISECONDS)
    }

    private fun submitAndWait(snapshot: Map<String, MarkdownDraft>) {
        try {
            writer.submit { persistSnapshot(snapshot) }.get()
        } catch (error: java.util.concurrent.ExecutionException) {
            if (error.cause is InterruptedException) Thread.currentThread().interrupt()
        } catch (_: java.util.concurrent.RejectedExecutionException) {
            // close() may race the final flush. The draft remains in memory.
        }
    }

    private fun persistSnapshot(snapshot: Map<String, MarkdownDraft>) {
        try {
            val destination = path.toAbsolutePath().normalize()
            Files.createDirectories(destination.parent)
            val payload = serialize(snapshot)
            require(payload.size <= MAX_BYTES) { "Markdown 草稿缓存已达到空间上限" }
            val temporary = Files.createTempFile(destination.parent, ".markdown-drafts-", ".tmp")
            try {
                Files.write(temporary, payload)
                try { Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE) }
                catch (_: java.nio.file.AtomicMoveNotSupportedException) { Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING) }
            } finally { Files.deleteIfExists(temporary) }
            sessions.values.forEach { it.update { state -> state.copy(cacheError = null) } }
        } catch (error: Throwable) {
            sessions.values.forEach { it.update { state -> state.copy(cacheError = error.message ?: "无法写入 Markdown 草稿缓存") } }
        }
    }

    private fun estimateRecordBytes(key: String, draft: MarkdownDraft): Long {
        val keyBytes = key.toByteArray(StandardCharsets.UTF_8).size.toLong()
        val contentBytes = draft.content.toByteArray(StandardCharsets.UTF_8).size.toLong()
        // JSON escaping can add one byte per character for quotes/backslashes.
        return keyBytes + contentBytes + draft.content.length + draft.baselineDigest.length + draft.newline.length + 256L
    }

    private fun serialize(source: Map<String, MarkdownDraft>): ByteArray = MarkdownDraftFile(records = source.entries.map { (key, draft) ->
        MarkdownDraftRecord(key, draft.content, draft.baselineDigest, draft.bom, draft.newline)
    }).let { json.encodeToString(MarkdownDraftFile.serializer(), it) }.toByteArray(StandardCharsets.UTF_8)
}

internal data class MarkdownFileSnapshot(
    val bytes: ByteArray,
    val digest: String,
    val bom: Boolean,
    val newline: String,
)

internal fun MarkdownFileSnapshot.utf8Content(): String {
    val body = if (bom) bytes.copyOfRange(3, bytes.size) else bytes
    return StandardCharsets.UTF_8.newDecoder()
        .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
        .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
        .decode(java.nio.ByteBuffer.wrap(body)).toString()
}

internal sealed interface MarkdownSaveResult {
    data class Saved(val snapshot: MarkdownFileSnapshot) : MarkdownSaveResult
    data class Conflict(val current: MarkdownFileSnapshot) : MarkdownSaveResult
}

internal class MaterialsMarkdownEditorService(
    private val maxBytes: Long = MAX_MARKDOWN_EDITOR_BYTES,
) {
    fun snapshot(root: Path, relativePath: String): MarkdownFileSnapshot {
        val file = safeFile(root, relativePath)
        val attrs = Files.readAttributes(file, java.nio.file.attribute.BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
        require(attrs.isRegularFile && !attrs.isSymbolicLink && !Files.isSymbolicLink(file)) { "Markdown 文件不存在或不是普通文件：$relativePath" }
        require(attrs.size() <= maxBytes) { "文件超过 ${maxBytes / 1024} KB，无法编辑：$relativePath" }
        val bytes = Files.newInputStream(file, LinkOption.NOFOLLOW_LINKS).use { it.readNBytes(maxBytes.toInt() + 1) }
        require(bytes.size <= maxBytes) { "文件超过 ${maxBytes / 1024} KB，无法编辑：$relativePath" }
        return snapshotBytes(bytes)
    }

    fun save(root: Path, relativePath: String, content: String, baselineDigest: String): MarkdownSaveResult {
        require(content.toByteArray(StandardCharsets.UTF_8).size <= maxBytes) { "Markdown 内容超过 ${maxBytes / 1024} KB" }
        val current = snapshot(root, relativePath)
        if (current.digest != baselineDigest) return MarkdownSaveResult.Conflict(current)
        val bytes = encode(content, current.bom, current.newline)
        require(bytes.size <= maxBytes) { "Markdown 内容超过 ${maxBytes / 1024} KB" }
        val file = safeFile(root, relativePath)
        val parent = file.parent ?: error("Markdown 文件目录无效")
        val temporary = Files.createTempFile(parent, ".silverwing-markdown-", ".tmp")
        try {
            Files.write(temporary, bytes)
            val beforeReplace = snapshot(root, relativePath)
            if (beforeReplace.digest != baselineDigest) return MarkdownSaveResult.Conflict(beforeReplace)
            try { Files.move(temporary, file, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE) }
            catch (_: java.nio.file.AtomicMoveNotSupportedException) { Files.move(temporary, file, StandardCopyOption.REPLACE_EXISTING) }
        } finally { Files.deleteIfExists(temporary) }
        // The bytes just written are the saved baseline. Reading the path again here could
        // mistake a watcher/editor race for our own write.
        return MarkdownSaveResult.Saved(snapshotBytes(bytes))
    }

    fun snapshotBytes(bytes: ByteArray): MarkdownFileSnapshot {
        val bom = bytes.size >= 3 && bytes[0] == 0xEF.toByte() && bytes[1] == 0xBB.toByte() && bytes[2] == 0xBF.toByte()
        val textBytes = if (bom) bytes.copyOfRange(3, bytes.size) else bytes
        val text = StandardCharsets.UTF_8.newDecoder()
            .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
            .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
            .decode(java.nio.ByteBuffer.wrap(textBytes)).toString()
        val newline = if (text.contains("\r\n")) "\r\n" else "\n"
        return MarkdownFileSnapshot(bytes.copyOf(), digest(bytes), bom, newline)
    }

    fun encode(content: String, bom: Boolean, newline: String): ByteArray {
        val normalized = content.replace("\r\n", "\n").replace('\r', '\n')
        val converted = if (newline == "\r\n") normalized.replace("\n", "\r\n") else normalized
        val payload = converted.toByteArray(StandardCharsets.UTF_8)
        return if (bom) byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + payload else payload
    }

    fun digest(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    private fun safeFile(root: Path, relativePath: String): Path {
        val normalizedRoot = root.toAbsolutePath().normalize()
        require(!normalizedRoot.any { it.toString().equals(".git", ignoreCase = true) }) { "Markdown 路径不能位于 .git 目录" }
        var component = normalizedRoot.root ?: error("Markdown 根路径无效")
        normalizedRoot.forEach { segment ->
            component = component.resolve(segment)
            require(!Files.isSymbolicLink(component) && runCatching { component.toRealPath() == component.normalize() }.getOrDefault(false)) {
                "Markdown 根路径不能包含符号链接或 junction"
            }
        }
        val portable = relativePath.replace('\\', '/')
        require(portable.split('/').none { it.equals(".git", ignoreCase = true) }) { "Markdown 路径不能位于 .git 目录" }
        val file = RequirementMaterialsMarkdownService().resolveOpenFile(normalizedRoot, relativePath)
        require(RequirementMaterialsMarkdownFile(relativePath, 0).markdown) { "只有 Markdown 文件支持编辑：$relativePath" }
        // Windows junctions can report as ordinary directories. Requiring the resolved
        // target to equal the normalized path prevents an edit from crossing a junction.
        require(runCatching { file.toRealPath() == file.toAbsolutePath().normalize() }.getOrDefault(false)) {
            "Markdown 路径不能包含符号链接或 junction：$relativePath"
        }
        return file
    }
}


/** Shared, application-owned editor surface used by every task/document instance. */
@Composable
internal fun MaterialsMarkdownEditor(
    store: MarkdownDraftStore,
    service: MaterialsMarkdownEditorService,
    identity: MarkdownDraftIdentity,
    root: Path,
    relativePath: String,
    originalContent: String,
    refreshKey: Any,
    onSaved: () -> Unit,
    onUseExternal: () -> Unit,
    onError: (Throwable) -> Unit,
    appScope: CoroutineScope,
    saveRequest: Int = 0,
    onDirtyChanged: (Boolean) -> Unit = {},
    onBusyChanged: (Boolean) -> Unit = {},
    modifier: Modifier = Modifier,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
    val session by store.session(identity).collectAsState()
    val scroll = rememberScrollState()
    val focusRequester = remember(identity) { FocusRequester() }
    val editorActive = remember(identity, root, relativePath) { AtomicBoolean(true) }
    var showingExternal by remember(identity) { mutableStateOf(false) }
    var confirmingExternal by remember(identity) { mutableStateOf(false) }
    var confirmingDiscard by remember(identity) { mutableStateOf(false) }
    androidx.compose.runtime.DisposableEffect(identity, root, relativePath) {
        editorActive.set(true)
        onDispose { editorActive.set(false) }
    }

    LaunchedEffect(identity, root, relativePath, refreshKey) {
        try {
            runInterruptible(ioDispatcher) { store.loadFromDisk() }
            val snapshot = runInterruptible(ioDispatcher) { service.snapshot(root, relativePath) }
            store.initialize(identity, snapshot, originalContent)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            onError(error)
        }
    }

    LaunchedEffect(session.dirty) { onDirtyChanged(session.dirty) }
    LaunchedEffect(session.busy) { onBusyChanged(session.busy) }

    fun save(force: Boolean = false) {
        val token = store.beginSave(identity, force) ?: return
        appScope.launch {
            try {
                when (val result = runInterruptible(ioDispatcher) {
                    service.save(root, relativePath, token.content, token.baselineDigest)
                }) {
                    is MarkdownSaveResult.Saved -> if (store.completeSaved(token, result.snapshot) && editorActive.get()) onSaved()
                    is MarkdownSaveResult.Conflict -> store.completeConflict(token, result.current)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                if (store.completeFailure(token, error.message ?: "保存 Markdown 失败。") && editorActive.get()) onError(error)
            }
        }
    }

    LaunchedEffect(saveRequest) { if (saveRequest > 0) save() }

    Column(modifier.fillMaxSize().border(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        session.conflict?.let { conflict ->
            androidx.compose.foundation.layout.BoxWithConstraints(Modifier.fillMaxWidth()) {
                if (maxWidth < 620.dp) {
                    Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.errorContainer).padding(10.dp),
                        verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Text("文件已被外部修改，当前草稿尚未覆盖外部内容。", color = MaterialTheme.colorScheme.onErrorContainer,
                            style = MaterialTheme.typography.bodySmall)
                        androidx.compose.foundation.layout.FlowRow(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(6.dp),
                            verticalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            TextButton(onClick = { showingExternal = true }) { Text("查看外部内容") }
                            TextButton(onClick = { confirmingExternal = true }) { Text("使用外部内容") }
                            TextButton(enabled = !session.busy, onClick = { save(force = true) }) { Text("保留草稿并覆盖") }
                        }
                    }
                } else Row(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.errorContainer).padding(horizontal = 12.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Icon(Icons.Outlined.ErrorOutline, null, tint = MaterialTheme.colorScheme.onErrorContainer)
                    Text("文件已被外部修改，当前草稿尚未覆盖外部内容。", Modifier.weight(1f), color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall)
                    TextButton(onClick = { showingExternal = true }) { Text("查看外部内容") }
                    TextButton(onClick = { confirmingExternal = true }) { Text("使用外部内容") }
                    TextButton(enabled = !session.busy, onClick = { save(force = true) }) { Text("保留草稿并覆盖") }
                }
            }
        }
        session.error?.let { message ->
            Text("保存失败：$message", Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp), color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall)
        }
        session.cacheError?.let { message ->
            Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("草稿缓存失败：$message", Modifier.weight(1f), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                TextButton(onClick = { store.retryCache(identity) }) { Text("重试缓存") }
            }
        }
        if (!session.initialized) Text("正在准备编辑器…", Modifier.padding(12.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
        HorizontalDivider()
        BasicTextField(
            value = session.content,
            onValueChange = { store.updateContent(identity, it) },
            enabled = session.initialized && !session.busy,
            modifier = Modifier.weight(1f).fillMaxWidth().focusRequester(focusRequester).testTag("materials-markdown-editor")
                .verticalScroll(scroll).padding(14.dp)
                .onPreviewKeyEvent { event ->
                    if (event.type == KeyEventType.KeyDown && event.isCtrlPressed && event.key == Key.S) { save(); true } else false
                },
            textStyle = TextStyle(fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurface),
            cursorBrush = androidx.compose.ui.graphics.SolidColor(MaterialTheme.colorScheme.primary),
            decorationBox = { inner ->
                if (session.content.isEmpty()) Text("在这里编辑 Markdown…", color = MaterialTheme.colorScheme.onSurfaceVariant, fontFamily = FontFamily.Monospace)
                inner()
            },
        )
        HorizontalDivider()
        Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(if (session.dirty) "有未保存修改" else "已保存", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall,
                color = if (session.dirty) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
            Text("${session.content.toByteArray(StandardCharsets.UTF_8).size / 1024} KB", style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            TextButton(enabled = session.dirty && !session.busy, onClick = { save() }) {
                Icon(Icons.Outlined.Save, null, Modifier.size(16.dp)); Text("保存")
            }
            TextButton(enabled = session.dirty && !session.busy, onClick = { confirmingDiscard = true }) { Text("撤销修改") }
        }
    }
    if (showingExternal) AlertDialog(
        onDismissRequest = { showingExternal = false },
        title = { Text("外部文件内容") },
        text = {
            SelectionContainer {
                Text(
                    session.conflict?.externalContent.orEmpty(),
                    modifier = Modifier.fillMaxWidth().heightIn(max = 400.dp).verticalScroll(rememberScrollState()),
                    fontFamily = FontFamily.Monospace,
                )
            }
        },
        confirmButton = { TextButton(onClick = { showingExternal = false }) { Text("关闭") } },
    )
    if (confirmingExternal) AlertDialog(
        onDismissRequest = { confirmingExternal = false },
        title = { Text("使用外部内容？") },
        text = { Text("当前本地草稿将被丢弃，外部文件内容会替换编辑区。") },
        confirmButton = { Button(onClick = { confirmingExternal = false; store.useExternal(identity); onUseExternal() }) { Text("确认替换") } },
        dismissButton = { TextButton(onClick = { confirmingExternal = false }) { Text("取消") } },
    )
    if (confirmingDiscard) AlertDialog(
        onDismissRequest = { confirmingDiscard = false },
        title = { Text("撤销本地修改？") },
        text = { Text("未保存的 Markdown 草稿将被丢弃。") },
        confirmButton = { Button(onClick = { confirmingDiscard = false; store.discard(identity) }) { Text("确认撤销") } },
        dismissButton = { TextButton(onClick = { confirmingDiscard = false }) { Text("取消") } },
    )
}
