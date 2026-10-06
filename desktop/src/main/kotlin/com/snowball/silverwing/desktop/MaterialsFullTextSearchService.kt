package com.snowball.silverwing.desktop

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runInterruptible
import org.apache.pdfbox.Loader
import org.apache.pdfbox.pdmodel.encryption.InvalidPasswordException
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.attribute.BasicFileAttributes
import java.util.Locale

/** All offsets use UTF-16, matching Compose text and the reader's PDF text index. */
internal data class MaterialsFullTextSearchHit(
    val relativePath: String,
    val query: String,
    val snippet: String,
    val snippetMatchRange: IntRange,
    val offset: Int,
    val line: Int?,
    val column: Int?,
    val pageIndex: Int?,
    val occurrenceIndex: Int,
    val pageOccurrenceIndex: Int? = null,
) {
    val fileName: String get() = relativePath.substringAfterLast('/')
    val locationLabel: String get() = pageIndex?.let { "第 ${it + 1} 页" } ?: "第 $line 行"
    val range: IntRange get() = offset until offset + query.length
}

internal data class MaterialsFullTextSearchFailure(val relativePath: String, val message: String)

internal data class MaterialsFullTextSearchProgress(
    val completedFiles: Int,
    val totalFiles: Int,
    val currentPath: String? = null,
)

internal data class MaterialsFullTextSearchResult(
    val query: String,
    val hits: List<MaterialsFullTextSearchHit>,
    val searchedFiles: Int,
    val totalFiles: Int,
    val skippedFiles: Int,
    val failures: List<MaterialsFullTextSearchFailure>,
    val truncated: Boolean,
    val failureCount: Int = failures.size,
    val unsupportedFiles: Int = 0,
    val readBytes: Long = 0,
)

internal data class MaterialsFullTextSearchLimits(
    val maxResults: Int = 500,
    val maxFiles: Int = 5_000,
    val maxVisitedEntries: Int = 20_000,
    val maxTextFileBytes: Int = 512 * 1024,
    val maxPdfFileBytes: Int = 64 * 1024 * 1024,
    val maxTotalBytes: Long = 256L * 1024 * 1024,
    val maxFailures: Int = 100,
) {
    init {
        require(maxResults > 0 && maxFiles > 0 && maxVisitedEntries > 0 && maxFailures > 0)
        require(maxTextFileBytes in 1 until Int.MAX_VALUE && maxPdfFileBytes in 1 until Int.MAX_VALUE)
        require(maxTotalBytes > 0)
    }
}

/** Read-only task search. Each request owns its parser/bytes and cancellation interrupts its worker. */
internal class MaterialsFullTextSearchService(
    private val limits: MaterialsFullTextSearchLimits = MaterialsFullTextSearchLimits(),
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
    suspend fun search(
        root: Path,
        query: String,
        onProgress: (MaterialsFullTextSearchProgress) -> Unit = {},
    ): MaterialsFullTextSearchResult = runInterruptible(ioDispatcher) {
        checkSearchInterrupted()
        require(query.length <= 4_096) { "搜索文字过长，请缩短到 4096 个字符以内。" }
        if (query.isEmpty()) return@runInterruptible MaterialsFullTextSearchResult(query, emptyList(), 0, 0, 0, emptyList(), false)
        searchBlocking(root, query, onProgress)
    }

    private fun searchBlocking(
        rootPath: Path,
        query: String,
        onProgress: (MaterialsFullTextSearchProgress) -> Unit,
    ): MaterialsFullTextSearchResult {
        val normalized = rootPath.toAbsolutePath().normalize()
        require(!Files.isSymbolicLink(normalized) && Files.isDirectory(normalized, LinkOption.NOFOLLOW_LINKS)) {
            "任务资料目录不存在或无法读取，请刷新目录后重试。"
        }
        val root = normalized.toRealPath()
        val candidates = mutableListOf<RequirementMaterialsMarkdownFile>()
        val failures = mutableListOf<MaterialsFullTextSearchFailure>()
        var failureCount = 0
        var unsupportedFiles = 0
        var entries = 0
        var truncated = false
        fun fail(path: String, message: String) {
            failureCount++
            if (failures.size < limits.maxFailures) failures += MaterialsFullTextSearchFailure(path, message)
        }
        fun relative(path: Path) = root.relativize(path).joinToString("/") { it.toString() }
        fun visitAllowed(): Boolean {
            checkSearchInterrupted()
            if (++entries <= limits.maxVisitedEntries) return true
            truncated = true
            return false
        }
        Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(directory: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (!visitAllowed()) return FileVisitResult.TERMINATE
                if (directory != root && (Files.isSymbolicLink(directory) ||
                        directory.fileName?.toString().equals(".git", ignoreCase = true) || isMaterialsStagingName(directory.fileName?.toString()))) return FileVisitResult.SKIP_SUBTREE
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (!visitAllowed()) return FileVisitResult.TERMINATE
                if (!attrs.isRegularFile || attrs.isSymbolicLink || Files.isSymbolicLink(file) || isMaterialsStagingName(file.fileName?.toString())) return FileVisitResult.CONTINUE
                val item = RequirementMaterialsMarkdownFile(relative(file), attrs.size())
                // Match the preview's actual formats; Office archives/images are never UTF-8 input.
                if (!item.textPreview && !item.pdf) {
                    unsupportedFiles++
                    return FileVisitResult.CONTINUE
                }
                if (candidates.size == limits.maxFiles) {
                    truncated = true
                    return FileVisitResult.TERMINATE
                }
                candidates += item
                return FileVisitResult.CONTINUE
            }

            override fun visitFileFailed(file: Path, exception: java.io.IOException): FileVisitResult {
                if (!visitAllowed()) return FileVisitResult.TERMINATE
                fail(relative(file), "无法读取此项：${exception.localizedMessage ?: "访问失败"}")
                return FileVisitResult.CONTINUE
            }

            override fun postVisitDirectory(directory: Path, exception: java.io.IOException?): FileVisitResult {
                checkSearchInterrupted()
                if (exception != null) fail(relative(directory), "无法读取目录：${exception.localizedMessage ?: "访问失败"}")
                return FileVisitResult.CONTINUE
            }
        })
        candidates.sortWith(compareBy<RequirementMaterialsMarkdownFile> { it.relativePath.lowercase(Locale.ROOT) }.thenBy { it.relativePath })
        val hits = mutableListOf<MaterialsFullTextSearchHit>()
        val safeFiles = RequirementMaterialsMarkdownService()
        var completedFiles = 0
        var searchedFiles = 0
        var skippedFiles = 0
        var readBytes = 0L
        onProgress(MaterialsFullTextSearchProgress(0, candidates.size))
        for (file in candidates) {
            checkSearchInterrupted()
            if (hits.size == limits.maxResults || readBytes >= limits.maxTotalBytes) {
                truncated = true
                break
            }
            onProgress(MaterialsFullTextSearchProgress(completedFiles, candidates.size, file.relativePath))
            checkSearchInterrupted()
            val fileLimit = if (file.pdf) limits.maxPdfFileBytes else limits.maxTextFileBytes
            val remaining = (limits.maxTotalBytes - readBytes).coerceAtMost(fileLimit.toLong()).toInt()
            if (file.sizeBytes > remaining) {
                skippedFiles++
                val message = if (file.sizeBytes > fileLimit) "文件超过搜索大小上限，已跳过。" else "本次读取总量达到上限，已跳过。"
                fail(file.relativePath, message)
                if (file.sizeBytes <= fileLimit) truncated = true
                completedFiles++
                continue
            }
            try {
                // Resolve again before opening; a stale tree entry cannot escape the task root.
                val path = safeFiles.resolveOpenFile(root, file.relativePath)
                val bytes = Files.newInputStream(path, LinkOption.NOFOLLOW_LINKS).use { stream -> stream.readNBytes(remaining + 1) }
                readBytes += bytes.size
                require(bytes.size <= remaining) { "文件在搜索期间增大，超过读取上限，已跳过。" }
                checkSearchInterrupted()
                if (file.pdf) {
                    val index = try {
                        Loader.loadPDF(bytes).use { document -> readPdfTextIndex(document) }
                    } catch (error: InvalidPasswordException) {
                        throw IllegalArgumentException("PDF 需要密码，请使用外部阅读器打开。", error)
                    } catch (error: java.io.IOException) {
                        throw IllegalArgumentException("PDF 无法读取，文件可能已损坏。", error)
                    }
                    var occurrence = 0
                    for ((page, text) in index.pages.withIndex()) {
                        checkSearchInterrupted()
                        val found = scanMaterialSearchText(file.relativePath, query, text.text, page, occurrence,
                            limits.maxResults - hits.size + 1)
                        occurrence += found.size
                        if (found.size + hits.size > limits.maxResults) truncated = true
                        hits += found.take(limits.maxResults - hits.size)
                        if (truncated && hits.size == limits.maxResults) break
                    }
                } else {
                    val text = try {
                        StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                            .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString().removePrefix("\uFEFF")
                    } catch (error: java.nio.charset.CharacterCodingException) {
                        throw IllegalArgumentException("文件不是有效的 UTF-8 文本，已跳过。", error)
                    }
                    val found = scanMaterialSearchText(file.relativePath, query, text, null, 0,
                        limits.maxResults - hits.size + 1)
                    if (found.size + hits.size > limits.maxResults) truncated = true
                    hits += found.take(limits.maxResults - hits.size)
                }
                searchedFiles++
            } catch (cancelled: java.util.concurrent.CancellationException) {
                throw cancelled
            } catch (interrupted: InterruptedException) {
                throw interrupted
            } catch (error: Exception) {
                checkSearchInterrupted()
                skippedFiles++
                fail(file.relativePath, error.message ?: "读取失败，已跳过。")
            }
            completedFiles++
        }
        checkSearchInterrupted()
        onProgress(MaterialsFullTextSearchProgress(completedFiles, candidates.size))
        return MaterialsFullTextSearchResult(query, hits, searchedFiles, candidates.size, skippedFiles, failures,
            truncated, failureCount, unsupportedFiles, readBytes)
    }
}

private fun checkSearchInterrupted() {
    if (Thread.currentThread().isInterrupted) throw InterruptedException("全文搜索已取消")
}

/** Literal, case-insensitive and non-overlapping, like DocumentFindState. */
internal fun scanMaterialSearchText(
    relativePath: String,
    query: String,
    text: String,
    pageIndex: Int? = null,
    occurrenceBase: Int = 0,
    limit: Int = 500,
): List<MaterialsFullTextSearchHit> {
    if (query.isEmpty() || limit <= 0) return emptyList()
    val hits = mutableListOf<MaterialsFullTextSearchHit>()
    var offset = 0
    var lineCursor = 0
    var line = 1
    var lineStart = 0
    while (offset <= text.length - query.length && hits.size < limit) {
        checkSearchInterrupted()
        val start = text.indexOf(query, offset, ignoreCase = true)
        if (start < 0) break
        if (pageIndex == null) {
            while (lineCursor < start) {
                if (text[lineCursor] == '\n' || (text[lineCursor] == '\r' && text.getOrNull(lineCursor + 1) != '\n')) {
                    line++
                    lineStart = lineCursor + 1
                }
                lineCursor++
            }
        }
        val (snippet, range) = materialSearchSnippet(text, start, query.length)
        hits += MaterialsFullTextSearchHit(relativePath, query, snippet, range, start,
            if (pageIndex == null) line else null, if (pageIndex == null) start - lineStart + 1 else null,
            pageIndex, occurrenceBase + hits.size, if (pageIndex != null) hits.size else null)
        offset = start + query.length
    }
    return hits
}

private fun materialSearchSnippet(text: String, match: Int, length: Int): Pair<String, IntRange> {
    var start = (match - 64).coerceAtLeast(0)
    val previousLine = text.lastIndexOf('\n', (match - 1).coerceAtLeast(0)) + 1
    start = maxOf(start, previousLine.coerceAtMost(match))
    if (start > 0 && Character.isLowSurrogate(text[start]) && Character.isHighSurrogate(text[start - 1])) start++
    var end = (maxOf(match + length.coerceAtMost(176), match + 96)).coerceAtMost(text.length).coerceAtMost(start + 240)
    val nextLine = text.indexOf('\n', match + length.coerceAtMost(text.length - match))
    if (nextLine >= 0) end = minOf(end, nextLine)
    if (end < text.length && end > match && Character.isHighSurrogate(text[end - 1]) && Character.isLowSurrogate(text[end])) end--
    val prefix = if (start > previousLine) "…" else ""
    val suffix = if (end < text.length && text[end] != '\n') "…" else ""
    val snippet = prefix + text.substring(start, end).map { if (it == '\n' || it == '\r' || it == '\t') ' ' else it }.joinToString("") + suffix
    val from = prefix.length + match - start
    val to = prefix.length + minOf(match + length, end) - start
    return snippet to (from until to)
}
