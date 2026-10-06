package com.snowball.silverwing.desktop

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.Locale
import java.util.concurrent.atomic.AtomicLong

internal const val MAX_READING_TASKS = 100
internal const val MAX_READING_FILES_PER_TASK = 100
internal const val MAX_READING_POSITIONS = 30
internal const val MAX_READING_CACHE_BYTES = 4_000_000
internal const val READING_CACHE_MAX_AGE_MILLIS = 90L * 24 * 60 * 60 * 1000

@Serializable internal data class ReadingPositionSnapshot(val mode: String = "RENDERED", val zoom: Int = 100,
    val scrolls: Map<String, Int> = emptyMap(), val lists: Map<String, List<Int>> = emptyMap())
@Serializable internal data class MaterialsSnapshot(val taskPath: String, val root: String?, val selectedPath: String? = null,
    val expandedDirectories: List<String> = emptyList(), val expansionInitialized: Boolean = false,
    val directoryCollapsed: Boolean = false, val compactShowingPreview: Boolean = false,
    val directoryPosition: ReadingPositionSnapshot = ReadingPositionSnapshot(),
    val files: Map<String, ReadingPositionSnapshot> = emptyMap(),
    val documentTabs: MaterialsDocumentTabsSnapshot = MaterialsDocumentTabsSnapshot(),
    val sortOrder: String = "NAME")
@Serializable internal data class RequirementReadingSnapshot(val taskPath: String, val link: String, val reading: ReadingPositionSnapshot)
@Serializable internal data class ReadingSnapshot(val schema: Int = 1, val lastTaskPath: String? = null, val view: String = "DETAIL",
    val materials: List<MaterialsSnapshot> = emptyList(), val requirements: List<RequirementReadingSnapshot> = emptyList(),
    val savedAtEpochMillis: Long = 0)

/** 独立的运行时缓存；损坏或未知版本不会影响配置和启动，写入始终替换完整文件。 */
internal class ReadingStateStore(private val path: Path, private val clock: () -> Long = System::currentTimeMillis) {
    private val json = Json { ignoreUnknownKeys = true }
    private val revisions = AtomicLong()
    fun load(): ReadingSnapshot = runCatching {
        if (!Files.isRegularFile(path) || Files.size(path) > MAX_READING_CACHE_BYTES) return ReadingSnapshot()
        // 检查后文件仍可能增长，因此读取本身也必须有上界。
        val bytes = Files.newInputStream(path).use { it.readNBytes(MAX_READING_CACHE_BYTES + 1) }
        if (bytes.size > MAX_READING_CACHE_BYTES) return ReadingSnapshot()
        val saved = json.decodeFromString<ReadingSnapshot>(bytes.toString(Charsets.UTF_8))
        if (saved.schema != 1) return ReadingSnapshot()
        val writtenAt = if (saved.savedAtEpochMillis == 0L) Files.getLastModifiedTime(path).toMillis() else saved.savedAtEpochMillis
        val now = clock()
        if (writtenAt < 0 || writtenAt < now - READING_CACHE_MAX_AGE_MILLIS || writtenAt > now + 86_400_000L) return ReadingSnapshot()
        saved.bounded()
    }.getOrDefault(ReadingSnapshot())
    /** 捕获快照时预订序号，较旧的延迟任务不能覆盖新状态。 */
    fun reserveSaveRevision(): Long = revisions.incrementAndGet()

    @Synchronized fun save(value: ReadingSnapshot, revision: Long = reserveSaveRevision()): Boolean {
        if (revision < revisions.get()) return false
        revisions.accumulateAndGet(revision) { current, requested -> maxOf(current, requested) }
        val destination = path.toAbsolutePath().normalize()
        var bounded = value.bounded().copy(savedAtEpochMillis = clock())
        var bytes = json.encodeToString(ReadingSnapshot.serializer(), bounded).toByteArray(Charsets.UTF_8)
        // 超限应淘汰旧位置并继续保存，不能让缓存永久停在之前的快照。
        while (bytes.size > MAX_READING_CACHE_BYTES) {
            bounded = bounded.trimOldestReading()
            bytes = json.encodeToString(ReadingSnapshot.serializer(), bounded).toByteArray(Charsets.UTF_8)
        }
        Files.createDirectories(destination.parent)
        val temporary = Files.createTempFile(destination.parent, ".reading-", ".tmp")
        try {
            Files.write(temporary, bytes)
            if (revision < revisions.get()) return false
            try { Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE) }
            catch (_: java.nio.file.AtomicMoveNotSupportedException) { Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING) }
        } finally { Files.deleteIfExists(temporary) }
        return true
    }
}

internal fun normalizedReadingPath(value: String): String =
    runCatching { Path.of(value).toAbsolutePath().normalize().toString() }.getOrDefault(value).let {
        if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) it.lowercase(Locale.ROOT) else it
    }

private fun validReadingPath(value: String): Boolean = value.isNotBlank() && value.length <= 4096 &&
    runCatching { Path.of(value); true }.getOrDefault(false)

internal fun readingRelativePath(value: String): String? {
    if (value.isEmpty() || value.length > 4096 || '\u0000' in value) return null
    val path = value.replace('\\', '/')
    if (path.startsWith('/') || Regex("^[A-Za-z]:").containsMatchIn(path) || path.split('/').any { it.isEmpty() || it == "." || it == ".." }) return null
    return path
}

internal fun ReadingPositionSnapshot.bounded(): ReadingPositionSnapshot = copy(
    mode = MarkdownPreviewMode.entries.firstOrNull { it.name == mode }?.name ?: "RENDERED", zoom = zoom.coerceIn(25, 200),
    scrolls = scrolls.entries.filter { it.key.isNotBlank() && it.key.length <= 64 }.takeLast(MAX_READING_POSITIONS)
        .associate { it.key to it.value.coerceIn(0, 100_000_000) },
    lists = lists.entries.filter { it.key.isNotBlank() && it.key.length <= 64 }.takeLast(MAX_READING_POSITIONS)
        .associate { (key, value) -> key to listOf(value.getOrElse(0) { 0 }.coerceIn(0, 1_000_000), value.getOrElse(1) { 0 }.coerceIn(0, 100_000_000)) },
)

internal fun ReadingSnapshot.bounded(): ReadingSnapshot {
    if (schema != 1) return ReadingSnapshot()
    val lastTask = lastTaskPath?.takeIf(::validReadingPath)
    val activeKey = lastTask?.let(::normalizedReadingPath)
    val materialRecords = linkedMapOf<String, MaterialsSnapshot>()
    materials.prioritizeTask(activeKey) { it.taskPath }.takeLast(MAX_READING_TASKS).forEach { record ->
        if (!validReadingPath(record.taskPath) || record.root?.let { it.isNotBlank() && !validReadingPath(it) } == true) return@forEach
        val task = normalizedReadingPath(record.taskPath)
        val selected = record.selectedPath?.let(::readingRelativePath)
        val normalizedFiles = record.files.entries.mapNotNull { (path, reading) -> readingRelativePath(path)?.let { it to reading.bounded() } }.toMap()
        val files = normalizedFiles.entries.toList().takeLast(MAX_READING_FILES_PER_TASK).associate { it.toPair() }.toMutableMap()
        selected?.let { key -> normalizedFiles[key]?.let { reading ->
            if (key !in files && files.size >= MAX_READING_FILES_PER_TASK) files.remove(files.keys.first())
            files[key] = reading
        } }
        materialRecords.remove(task)
        materialRecords[task] = record.copy(taskPath = task, root = record.root?.takeIf(String::isNotBlank)?.let(::normalizedReadingPath),
            selectedPath = selected, expandedDirectories = record.expandedDirectories.mapNotNull(::readingRelativePath).distinct().take(500),
            directoryPosition = record.directoryPosition.bounded(), files = files, documentTabs = record.documentTabs.bounded(),
            sortOrder = MaterialsSortOrder.fromName(record.sortOrder).name)
    }
    val requirementRecords = linkedMapOf<String, RequirementReadingSnapshot>()
    requirements.prioritizeTask(activeKey) { it.taskPath }.takeLast(MAX_READING_TASKS).forEach { record ->
        if (!validReadingPath(record.taskPath) || record.link.isBlank() || record.link.length > 8192) return@forEach
        val task = normalizedReadingPath(record.taskPath)
        requirementRecords.remove(task)
        requirementRecords[task] = record.copy(taskPath = task, link = record.link.trim(), reading = record.reading.bounded())
    }
    return copy(schema = 1, lastTaskPath = lastTask, view = TaskContentView.entries.firstOrNull { it.name == view }?.name ?: "DETAIL",
        materials = materialRecords.values.toList(), requirements = requirementRecords.values.toList())
}

private fun <T> List<T>.prioritizeTask(activeKey: String?, path: (T) -> String): List<T> {
    if (activeKey == null) return this
    val (active, others) = partition { normalizedReadingPath(path(it)) == activeKey }
    return others + active
}

private fun ReadingSnapshot.trimOldestReading(): ReadingSnapshot = when {
    materials.size > 1 -> copy(materials = materials.takeLast(maxOf(1, materials.size / 2)))
    requirements.size > 1 -> copy(requirements = requirements.takeLast(maxOf(1, requirements.size / 2)))
    materials.firstOrNull()?.files?.size?.let { it > 1 } == true -> {
        val record = materials.first()
        val keep = record.files.entries.filter { it.key != record.selectedPath }.takeLast(record.files.size / 2).associate { it.toPair() }.toMutableMap()
        record.selectedPath?.let { key -> record.files[key]?.let { keep[key] = it } }
        copy(materials = listOf(record.copy(files = keep)))
    }
    materials.firstOrNull()?.expandedDirectories?.size?.let { it > 1 } == true -> {
        val record = materials.first()
        copy(materials = listOf(record.copy(expandedDirectories = record.expandedDirectories.takeLast(record.expandedDirectories.size / 2))))
    }
    materials.isNotEmpty() -> copy(materials = emptyList())
    requirements.isNotEmpty() -> copy(requirements = emptyList())
    else -> ReadingSnapshot(lastTaskPath = lastTaskPath, view = view, savedAtEpochMillis = savedAtEpochMillis)
}
