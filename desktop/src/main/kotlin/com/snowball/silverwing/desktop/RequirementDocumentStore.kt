package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ParticipatedWorkItem
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import org.intellij.markdown.MarkdownElementTypes
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.flavours.gfm.GFMFlavourDescriptor
import org.intellij.markdown.parser.MarkdownParser
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardCopyOption.REPLACE_EXISTING
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.nio.file.attribute.BasicFileAttributes
import java.nio.file.attribute.FileTime

internal data class CachedRequirementDocument(
    val key: String,
    val markdownPath: Path,
    val assetsDirectory: Path,
    val content: String,
    val fetchedAt: Long,
    val generation: String,
    val images: Map<String, Path>,
    val imageUrls: List<String>,
    val imageFailures: Map<String, String>,
    val refreshedImages: Set<String> = emptySet(),
) {
    val complete: Boolean get() = imageUrls.all { it in images }
    fun copyPaths(): List<Path> = listOf(markdownPath) + assetsDirectory.takeIf { imageUrls.isNotEmpty() }.let(::listOfNotNull)
}

internal data class RequirementBodyReadResult(
    val content: String,
    val document: CachedRequirementDocument? = null,
    val cacheError: String? = null,
    val retryRefresh: Boolean = false,
)

/** Markdown is the only stored body. The small index contains identity and reversible image mappings. */
internal class RequirementDocumentStore(directory: Path) {
    private val root = directory.toAbsolutePath().normalize()
    private val guard = guards.computeIfAbsent(root) { Any() }
    private val json = Json { encodeDefaults = true; ignoreUnknownKeys = false }
    private data class MarkdownMemory(val modified: FileTime, val size: Long, val value: String)
    private val markdownMemory = LinkedHashMap<String, MarkdownMemory>(16, 0.75f, true)
    private var memoryCharacters = 0L

    @Serializable private data class ImageEntry(val relativePath: String? = null, val error: String? = null, val generation: String? = null)
    @Serializable private data class Index(
        val version: Int = 1,
        val key: String,
        val project: String,
        val type: String,
        val id: String,
        val title: String,
        val url: String,
        val fetchedAt: Long,
        val generation: String,
        val bodyOffset: Int,
        val markdownHash: String,
        val images: Map<String, ImageEntry>,
    )

    fun read(key: String): CachedRequirementDocument? = synchronized(guard) {
        val directory = directory(key)
        if (!Files.exists(directory, NOFOLLOW_LINKS)) return@synchronized null
        safe(directory)
        val indexPath = directory.resolve("index.json")
        if (!Files.isRegularFile(indexPath, NOFOLLOW_LINKS)) return@synchronized null
        safe(indexPath)
        val index = json.decodeFromString<Index>(Files.readString(indexPath))
        require(index.version == 1 && index.key == requirementDocumentDigest(key)) { "需求文档索引不匹配" }
        load(key, directory, index)
    }

    fun write(key: String, item: ParticipatedWorkItem, content: String, fetchedAt: Long): CachedRequirementDocument = synchronized(guard) {
        require(item.id.matches(Regex("[A-Za-z0-9_-]+"))) { "需求编号不能用于文件路径" }
        val directory = directory(key)
        ensureDirectory(directory)
        val previous = runCatching { read(key) }.getOrNull()
        val urls = requirementDocumentImageUrls(content)
        val images = urls.associateWith { url -> ImageEntry(previous?.images?.get(url)?.let { directory.relativize(it).toString().replace('\\', '/') }) }
        val header = "# ${item.title.replace(Regex("[\\r\\n]+"), " ")}\n\n来源：${item.url}\n\n---\n\n"
        val markdown = header + requirementReplaceImageDestinations(content, images.mapNotNull { (url, entry) -> entry.relativePath?.let { url to it } }.toMap())
        val index = Index(key = requirementDocumentDigest(key), project = item.projectKey, type = item.type, id = item.id,
            title = item.title, url = item.url, fetchedAt = fetchedAt, generation = UUID.randomUUID().toString(),
            bodyOffset = header.length, markdownHash = requirementDocumentDigest(markdown), images = images)
        commit(directory, index, markdown)
        load(key, directory, index)
    }

    fun saveImage(key: String, generation: String, url: String, downloaded: Path): CachedRequirementDocument? = synchronized(guard) {
        val directory = directory(key)
        val current = read(key) ?: return@synchronized null
        if (current.generation != generation || url !in current.imageUrls) return@synchronized null
        require(Files.isRegularFile(downloaded, NOFOLLOW_LINKS) && !Files.isSymbolicLink(downloaded)) { "下载的图片不存在" }
        val bytes = Files.readAllBytes(downloaded)
        val extension = requirementImageExtension(bytes)
        val assets = current.assetsDirectory
        ensureDirectory(assets)
        // The URL suffix keeps reverse mappings unambiguous when two URLs serve identical bytes.
        val destination = assets.resolve("${requirementDocumentDigest(bytes)}-${requirementDocumentDigest(url).take(12)}.$extension")
        atomicWrite(destination, bytes)
        val index = readIndex(directory)
        val updated = index.copy(images = index.images + (url to ImageEntry(directory.relativize(destination).toString().replace('\\', '/'), generation = generation)))
        val markdown = Files.readString(current.markdownPath).take(index.bodyOffset) + requirementReplaceImageDestinations(current.content,
            updated.images.mapNotNull { (imageUrl, entry) -> entry.relativePath?.let { imageUrl to it } }.toMap())
        val completed = updated.copy(markdownHash = requirementDocumentDigest(markdown))
        commit(directory, completed, markdown)
        load(key, directory, completed)
    }

    fun failImage(key: String, generation: String, url: String, message: String): CachedRequirementDocument? = synchronized(guard) {
        val current = read(key) ?: return@synchronized null
        if (current.generation != generation || url !in current.imageUrls) return@synchronized null
        val directory = directory(key)
        val index = readIndex(directory)
        val entry = index.images.getValue(url).copy(error = message.take(500))
        val updated = index.copy(images = index.images + (url to entry))
        atomicWrite(directory.resolve("index.json"), json.encodeToString(Index.serializer(), updated).toByteArray(Charsets.UTF_8))
        load(key, directory, updated)
    }

    private fun load(key: String, directory: Path, index: Index): CachedRequirementDocument {
        require(index.id.matches(Regex("[A-Za-z0-9_-]+"))) { "需求编号不能用于文件路径" }
        val markdownPath = directory.resolve("需求-${index.id}.md")
        safe(markdownPath)
        val markdown = readMarkdown(markdownPath, index.markdownHash)
        require(requirementDocumentDigest(markdown) == index.markdownHash && index.bodyOffset in 0..markdown.length) { "本地需求 Markdown 不完整，请刷新" }
        val reverse = index.images.mapNotNull { (url, entry) -> entry.relativePath?.let { it to url } }.toMap()
        val content = requirementReplaceImageDestinations(markdown.substring(index.bodyOffset), reverse)
        val images = index.images.mapNotNull { (url, entry) -> entry.relativePath?.let { relative ->
            val path = directory.resolve(relative).normalize()
            require(path.startsWith(directory.resolve("需求-${index.id}.assets"))) { "图片路径越界" }
            safe(path)
            path.takeIf { Files.isRegularFile(it, NOFOLLOW_LINKS) }?.let { url to it }
        } }.toMap()
        return CachedRequirementDocument(key, markdownPath, directory.resolve("需求-${index.id}.assets"), content,
            index.fetchedAt, index.generation, images, index.images.keys.toList(),
            index.images.mapNotNull { (url, entry) -> entry.error?.let { url to it } }.toMap(),
            index.images.filterValues { it.generation == index.generation && it.error == null }.keys)
    }

    private fun readIndex(directory: Path) = json.decodeFromString<Index>(Files.readString(directory.resolve("index.json")))
    private fun directory(key: String) = root.resolve(requirementDocumentDigest(key))

    private fun readMarkdown(path: Path, hash: String): String {
        val attributes = Files.readAttributes(path, BasicFileAttributes::class.java, NOFOLLOW_LINKS)
        val key = "$path:$hash"
        markdownMemory[key]?.takeIf { it.modified == attributes.lastModifiedTime() && it.size == attributes.size() }?.let { return it.value }
        markdownMemory.remove(key)?.let { memoryCharacters -= it.value.length }
        val value = Files.readString(path)
        if (value.length <= 4_000_000) {
            markdownMemory[key] = MarkdownMemory(attributes.lastModifiedTime(), attributes.size(), value)
            memoryCharacters += value.length
            val entries = markdownMemory.entries.iterator()
            while (markdownMemory.size > 64 || memoryCharacters > 4_000_000) { memoryCharacters -= entries.next().value.value.length; entries.remove() }
        }
        return value
    }

    private fun commit(directory: Path, index: Index, markdown: String) {
        val markdownPath = directory.resolve("需求-${index.id}.md")
        val old = markdownPath.takeIf { Files.isRegularFile(it, NOFOLLOW_LINKS) }?.let(Files::readAllBytes)
        atomicWrite(markdownPath, markdown.toByteArray(Charsets.UTF_8))
        try { atomicWrite(directory.resolve("index.json"), json.encodeToString(Index.serializer(), index).toByteArray(Charsets.UTF_8)) }
        catch (failure: Exception) {
            runCatching { if (old != null) atomicWrite(markdownPath, old) else Files.deleteIfExists(markdownPath) }
                .onFailure(failure::addSuppressed)
            throw failure
        }
    }

    private fun ensureDirectory(path: Path) { safe(path); Files.createDirectories(path); safe(path) }
    private fun safe(path: Path) {
        val normalized = path.toAbsolutePath().normalize()
        require(normalized.startsWith(root)) { "需求缓存路径越界" }
        var cursor: Path? = normalized
        while (cursor != null) {
            require(!Files.isSymbolicLink(cursor)) { "需求缓存路径不能包含符号链接" }
            cursor = cursor.parent
        }
    }
    private fun atomicWrite(path: Path, bytes: ByteArray) {
        safe(path)
        val temporary = Files.createTempFile(path.parent, ".requirement-", ".tmp")
        try {
            Files.write(temporary, bytes)
            try { Files.move(temporary, path, ATOMIC_MOVE, REPLACE_EXISTING) }
            catch (_: AtomicMoveNotSupportedException) { Files.move(temporary, path, REPLACE_EXISTING) }
        } finally { Files.deleteIfExists(temporary) }
    }

    companion object { private val guards = ConcurrentHashMap<Path, Any>() }
}

internal fun requirementDocumentDigest(value: String) = requirementDocumentDigest(value.toByteArray(Charsets.UTF_8))
internal fun requirementDocumentDigest(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256")
    .digest(bytes).joinToString("") { "%02x".format(it) }

private data class RequirementImageDestination(val start: Int, val end: Int, val value: String, val angled: Boolean)

/** Uses the same parser as the reader, excluding code and including reference-style images. */
private fun requirementImageDestinations(content: String): List<RequirementImageDestination> {
    val tree = MarkdownParser(GFMFlavourDescriptor()).buildMarkdownTreeFromString(content)
    fun descendants(node: ASTNode): Sequence<ASTNode> = sequence { yield(node); node.children.forEach { yieldAll(descendants(it)) } }
    fun label(node: ASTNode) = content.substring(node.startOffset, node.endOffset).trim('[', ']').trim().lowercase().replace(Regex("\\s+"), " ")
    val nodes = descendants(tree).toList()
    fun destination(node: ASTNode) = node.children.firstOrNull {
        it.type == MarkdownElementTypes.LINK_DESTINATION || it.type == MarkdownElementTypes.AUTOLINK
    }
    val definitions = nodes.filter { it.type == MarkdownElementTypes.LINK_DEFINITION }.mapNotNull { definition ->
        val children = descendants(definition).toList()
        val identifier = children.firstOrNull { it.type == MarkdownElementTypes.LINK_LABEL } ?: return@mapNotNull null
        val target = destination(definition) ?: return@mapNotNull null
        label(identifier) to target
    }.toMap()
    return nodes.filter { it.type == MarkdownElementTypes.IMAGE }.mapNotNull { image ->
        val children = descendants(image).toList()
        val destination = children.firstOrNull { it.type == MarkdownElementTypes.INLINE_LINK }?.let(::destination)
            ?: children.lastOrNull { it.type == MarkdownElementTypes.LINK_LABEL }?.let { definitions[label(it)] }
            ?: children.firstOrNull { it.type == MarkdownElementTypes.LINK_TEXT }?.let { definitions[label(it)] }
            ?: return@mapNotNull null
        val raw = content.substring(destination.startOffset, destination.endOffset)
        RequirementImageDestination(destination.startOffset, destination.endOffset, raw.removeSurrounding("<", ">"), raw.startsWith('<'))
    }.distinctBy { it.start }
}

internal fun requirementDocumentImageUrls(content: String): List<String> = requirementImageDestinations(content)
    .map { it.value }.filter { runCatching { java.net.URI(it).scheme?.lowercase() in setOf("http", "https") }.getOrDefault(false) }.distinct()

internal fun requirementReplaceImageDestinations(content: String, replacements: Map<String, String>): String {
    if (replacements.isEmpty()) return content
    val result = StringBuilder(content)
    requirementImageDestinations(content).sortedByDescending { it.start }.forEach { destination ->
        replacements[destination.value]?.let { replacement ->
            result.replace(destination.start, destination.end, if (destination.angled) "<$replacement>" else replacement)
        }
    }
    return result.toString()
}

internal fun requirementImageExtension(bytes: ByteArray): String {
    require(bytes.isNotEmpty()) { "图片内容为空" }
    fun starts(vararg signature: Int) = bytes.size >= signature.size && signature.indices.all { bytes[it].toInt() and 255 == signature[it] }
    val prefix = bytes.take(1024).toByteArray().toString(Charsets.UTF_8).trimStart()
    return when {
        starts(137, 80, 78, 71, 13, 10, 26, 10) -> "png"
        starts(255, 216, 255) -> "jpg"
        prefix.startsWith("GIF87a") || prefix.startsWith("GIF89a") -> "gif"
        bytes.size >= 12 && bytes.copyOfRange(0, 4).toString(Charsets.US_ASCII) == "RIFF" && bytes.copyOfRange(8, 12).toString(Charsets.US_ASCII) == "WEBP" -> "webp"
        starts(66, 77) -> "bmp"
        prefix.startsWith("<svg") || (prefix.startsWith("<?xml") && prefix.contains("<svg")) -> "svg"
        bytes.size >= 12 && bytes.copyOfRange(4, 12).toString(Charsets.US_ASCII) in setOf("ftypavif", "ftypavis") -> "avif"
        else -> error("下载结果不是受支持的图片")
    }
}
