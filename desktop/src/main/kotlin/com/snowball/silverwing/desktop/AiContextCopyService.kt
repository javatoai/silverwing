package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import org.apache.pdfbox.Loader
import org.apache.pdfbox.pdmodel.encryption.InvalidPasswordException
import org.apache.pdfbox.text.PDFTextStripper
import org.apache.pdfbox.text.TextPosition
import java.io.Writer
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.attribute.BasicFileAttributes

internal const val AI_CONTEXT_SECTION_CHARACTERS = 32_000
internal const val AI_CONTEXT_COPY_CHARACTERS = 120_000
internal const val AI_CONTEXT_MATERIAL_FILES = 40
private const val AI_CONTEXT_PDF_BYTES = 16 * 1024 * 1024
private const val AI_CONTEXT_TEXT_BYTES = 2L * 1024 * 1024
private const val AI_CONTEXT_TRUNCATION = "\n\n[内容已截断]"

/** A captured task and material selection. Nothing is persisted or sent to a model. */
internal data class AiContextCopyRequest(
    val task: TaskManifest,
    val requirementTitle: String? = null,
    val requirementBody: String? = null,
    val materialsRoot: Path? = null,
    val selectedMaterialPaths: List<String> = emptyList(),
    /** Fresh Git status, keyed by worktreePath. Without it, the manifest branch is labelled as recorded. */
    val currentBranches: Map<String, String> = emptyMap(),
)

internal data class AiContextCopySection(
    val id: String,
    val title: String,
    val content: String,
    val selectedByDefault: Boolean = true,
)

internal data class AiContextCopyDocument(
    val sections: List<AiContextCopySection>,
    val notices: List<String> = emptyList(),
) {
    val defaultSelection: Set<String> get() = sections.filter { it.selectedByDefault }.mapTo(linkedSetOf()) { it.id }
}

internal data class AiContextCopyPreview(val text: String, val truncated: Boolean)

/** The preview and clipboard use the same bounded value, including an explicit truncation marker. */
internal fun buildAiContextCopyPreview(
    document: AiContextCopyDocument,
    selectedSectionIds: Set<String> = document.defaultSelection,
    maxCharacters: Int = AI_CONTEXT_COPY_CHARACTERS,
): AiContextCopyPreview {
    require(maxCharacters > AI_CONTEXT_TRUNCATION.length)
    val joined = document.sections.asSequence().filter { it.id in selectedSectionIds && it.content.isNotBlank() }
        .joinToString("\n\n") { "## ${it.title}\n\n${it.content}" }
    return AiContextCopyPreview(limitAiContextText(joined, maxCharacters), joined.length > maxCharacters)
}

internal fun limitAiContextText(text: String, maxCharacters: Int): String {
    require(maxCharacters > AI_CONTEXT_TRUNCATION.length)
    if (text.length <= maxCharacters) return text
    var end = maxCharacters - AI_CONTEXT_TRUNCATION.length
    if (end > 0 && text[end - 1].isHighSurrogate()) end--
    return text.take(end) + AI_CONTEXT_TRUNCATION
}

/** Local, bounded material reads; the optional body callback reuses the app's existing requirement repository. */
internal class AiContextCopyService(
    private val files: RequirementMaterialsMarkdownService = RequirementMaterialsMarkdownService(AI_CONTEXT_TEXT_BYTES),
    private val sectionCharacters: Int = AI_CONTEXT_SECTION_CHARACTERS,
    private val materialFiles: Int = AI_CONTEXT_MATERIAL_FILES,
) {
    init { require(sectionCharacters > AI_CONTEXT_TRUNCATION.length && materialFiles > 0) }

    suspend fun load(
        request: AiContextCopyRequest,
        loadRequirementBody: (suspend () -> String?)? = null,
        ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    ): AiContextCopyDocument {
        val sections = mutableListOf<AiContextCopySection>()
        val notices = mutableListOf<String>()
        fun section(id: String, title: String, content: String, selected: Boolean = true) {
            if (content.length > sectionCharacters) notices += "$title 超过 $sectionCharacters 字符，已截断。"
            sections += AiContextCopySection(id, title, limitAiContextText(content, sectionCharacters), selected)
        }
        section("task", "当前任务", "任务：${request.task.folderName}\n任务分支：${request.task.featureBranch}")
        if (request.task.requirementLink.isNotBlank()) {
            section("requirement", "关联需求", buildString {
                request.requirementTitle?.takeIf(String::isNotBlank)?.let { appendLine("标题：$it") }
                request.task.requirementId?.takeIf(String::isNotBlank)?.let { appendLine("需求 ID：$it") }
                append("链接：${request.task.requirementLink.trim()}")
            })
            try {
                val body = request.requirementBody ?: loadRequirementBody?.invoke()
                currentCoroutineContext().ensureActive()
                if (!body.isNullOrBlank()) section("body", "需求正文", body)
                else notices += if (loadRequirementBody == null && request.requirementBody == null)
                    "需求正文尚未加载，可关闭后重试；任务和资料仍可复制。" else "关联需求没有可读取的正文；任务和资料仍可复制。"
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                currentCoroutineContext().ensureActive()
                notices += "需求正文读取失败：${error.message.orEmpty().ifBlank { "请稍后重试" }}。其他内容仍可复制。"
            }
        } else notices += "该任务没有关联需求链接，仍可复制任务、服务和所选资料。"
        if (request.task.services.isNotEmpty()) section("services", "服务与分支", request.task.services.joinToString("\n") { workspace ->
            val current = request.currentBranches[workspace.worktreePath]
            "- ${workspace.serviceName} / ${workspace.moduleName}：${current ?: workspace.branch}" +
                if (current == null) "（任务记录）" else "（当前分支）"
        })
        if (request.selectedMaterialPaths.isNotEmpty()) {
            val root = request.materialsRoot
            if (root == null) notices += "任务资料目录未就绪，无法读取所选资料。"
            else {
                val selected = runInterruptible(ioDispatcher) { selectedFiles(root, request.selectedMaterialPaths, notices) }
                for ((index, path) in selected.withIndex()) {
                    currentCoroutineContext().ensureActive()
                    try {
                        val content = runInterruptible(ioDispatcher) { readMaterial(root, path) }
                        val body = if (content.text.isNotBlank()) content.text
                        else if (RequirementMaterialsMarkdownFile(path, 0).pdf) "（没有可提取的文字；PDF 扫描件需要先进行 OCR）"
                        else "（文件内容为空）"
                        section("material:$index:$path", "资料：$path", "文件：$path\n\n$body")
                        if (content.truncated) notices += "$path 的文字超过 $sectionCharacters 字符，已截断。"
                    } catch (cancelled: CancellationException) {
                        throw cancelled
                    } catch (error: Exception) {
                        currentCoroutineContext().ensureActive()
                        val message = error.message.orEmpty().ifBlank { "读取失败，请检查文件" }
                        notices += "$path：$message。"
                        section("material:$index:$path", "资料：$path（仅路径）", "文件：$path\n未读取正文：$message", selected = false)
                    }
                }
            }
        } else notices += "未选择资料；可在资料列表选择文件后再次打开此窗口。"
        currentCoroutineContext().ensureActive()
        return AiContextCopyDocument(sections, notices)
    }

    private fun selectedFiles(root: Path, paths: List<String>, notices: MutableList<String>): List<String> {
        val result = linkedSetOf<String>()
        val portablePaths = paths.asSequence().take(2_001).map { it.replace('\\', '/') }.distinct().toList()
        var inspected = 0
        var limited = paths.size > 2_000
        for (relativePath in portablePaths.take(2_000)) {
            checkPdfInterrupted()
            if (result.size >= materialFiles || inspected >= 2_000) { limited = true; break }
            try {
                val target = files.resolveCopyItem(root, relativePath)
                if (Files.isDirectory(target, LinkOption.NOFOLLOW_LINKS)) {
                    Files.walkFileTree(target, object : SimpleFileVisitor<Path>() {
                        override fun preVisitDirectory(dir: Path, attrs: BasicFileAttributes): FileVisitResult {
                            checkPdfInterrupted()
                            if (++inspected >= 2_000 || result.size >= materialFiles) { limited = true; return FileVisitResult.TERMINATE }
                            return if (Files.isSymbolicLink(dir) || dir.fileName?.toString().equals(".git", true) || isMaterialsStagingName(dir.fileName?.toString())) FileVisitResult.SKIP_SUBTREE
                            else FileVisitResult.CONTINUE
                        }
                        override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                            checkPdfInterrupted()
                            if (++inspected >= 2_000 || result.size >= materialFiles) { limited = true; return FileVisitResult.TERMINATE }
                            if (attrs.isRegularFile && !attrs.isSymbolicLink && !isMaterialsStagingName(file.fileName?.toString())) result += (relativePath + "/" + target.relativize(file).joinToString("/") { it.toString() })
                            return FileVisitResult.CONTINUE
                        }
                        override fun visitFileFailed(file: Path, exc: java.io.IOException): FileVisitResult {
                            checkPdfInterrupted()
                            if (++inspected >= 2_000) { limited = true; return FileVisitResult.TERMINATE }
                            notices += "${file.fileName}：无法读取，${exc.message.orEmpty()}。"
                            return FileVisitResult.CONTINUE
                        }
                    })
                } else result += relativePath
            } catch (error: InterruptedException) { throw error }
            catch (error: Exception) { notices += "$relativePath：${error.message.orEmpty().ifBlank { "路径无法读取" }}。" }
        }
        if (limited) notices += "所选资料较多，本次最多读取 $materialFiles 个文件；请缩小选择后重试。"
        return result.toList()
    }

    private data class MaterialText(val text: String, val truncated: Boolean = false)

    private fun readMaterial(root: Path, path: String): MaterialText {
        checkPdfInterrupted()
        if (RequirementMaterialsMarkdownFile(path, 0).pdf) return readPdf(root, path)
        require(RequirementMaterialsMarkdownFile(path, 0).textPreview) { "该文件类型不支持文字提取，可仅复制文件路径" }
        val text = files.read(root, path)
        require('\u0000' !in text && text.count { it.isISOControl() && it != '\n' && it != '\r' && it != '\t' } <= text.length / 100) {
            "文件包含二进制或控制字符，无法作为文本复制"
        }
        return MaterialText(text)
    }

    /** Uses the existing safe PDF byte reader, stopping extraction before a huge text/glyph buffer is retained. */
    private fun readPdf(root: Path, path: String): MaterialText {
        val bytes = files.readPdf(root, path, AI_CONTEXT_PDF_BYTES)
        val output = StringBuilder()
        var truncated = false
        try { Loader.loadPDF(bytes).use { document ->
            require(!document.isEncrypted || document.currentAccessPermission.canExtractContent()) { "PDF 不允许提取文字" }
            require(document.numberOfPages > 0) { "PDF 没有可读取页面" }
            var positions = 0
            var pagePositions = 0
            val stripper = object : PDFTextStripper() {
                override fun startPage(page: org.apache.pdfbox.pdmodel.PDPage) {
                    checkPdfInterrupted(); pagePositions = 0; super.startPage(page)
                }
                override fun processTextPosition(position: TextPosition) {
                    checkPdfInterrupted()
                    require(++positions <= 500_000 && ++pagePositions <= 100_000) { "PDF 单页文字较多，请缩小资料范围" }
                    super.processTextPosition(position)
                }
            }.apply { sortByPosition = true; endPage = minOf(document.numberOfPages, 200) }
            val writer = object : Writer() {
                override fun write(chars: CharArray, offset: Int, length: Int) {
                    checkPdfInterrupted()
                    val available = sectionCharacters + 1 - output.length
                    output.append(chars, offset, minOf(length, available).coerceAtLeast(0))
                    if (length > available || output.length > sectionCharacters) throw PdfContextLimitReached()
                }
                override fun flush() = Unit
                override fun close() = Unit
            }
            try { stripper.writeText(document, writer) }
            catch (_: PdfContextLimitReached) { truncated = true }
            if (document.numberOfPages > 200) truncated = true
        } } catch (error: InvalidPasswordException) {
            throw IllegalArgumentException("PDF 需要密码，请使用外部阅读器打开", error)
        } catch (error: java.io.IOException) {
            throw IllegalArgumentException("PDF 无法读取，文件可能已损坏或格式不受支持", error)
        }
        return MaterialText(if (truncated) limitAiContextText(output.toString() + AI_CONTEXT_TRUNCATION, sectionCharacters) else output.toString(), truncated)
    }
    private class PdfContextLimitReached : java.io.IOException()
}

internal sealed interface AiContextCopyLoadState {
    data object Idle : AiContextCopyLoadState
    data class Loading(val request: AiContextCopyRequest) : AiContextCopyLoadState
    data class Ready(val request: AiContextCopyRequest, val document: AiContextCopyDocument) : AiContextCopyLoadState
    data class Failed(val request: AiContextCopyRequest, val message: String) : AiContextCopyLoadState
}

/** Closing or changing task/root/selection cancels the read and rejects non-cooperative old results. */
internal class AiContextCopyController(
    private val scope: CoroutineScope,
    private val loader: suspend (AiContextCopyRequest) -> AiContextCopyDocument,
) {
    var state by mutableStateOf<AiContextCopyLoadState>(AiContextCopyLoadState.Idle)
        private set
    private var generation = 0L
    private var activeRequest: AiContextCopyRequest? = null
    private var job: Job? = null
    fun clear() {
        generation++; job?.cancel(); job = null; activeRequest = null; state = AiContextCopyLoadState.Idle
    }
    fun load(request: AiContextCopyRequest) {
        clear()
        activeRequest = request
        val current = generation
        state = AiContextCopyLoadState.Loading(request)
        job = scope.launch {
            try {
                val document = loader(request)
                currentCoroutineContext().ensureActive()
                if (generation == current && activeRequest == request) state = AiContextCopyLoadState.Ready(request, document)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) {
                if (generation == current && activeRequest == request) state = AiContextCopyLoadState.Failed(request,
                    error.message.orEmpty().ifBlank { "上下文读取失败，请重试" })
            }
        }
    }
}
