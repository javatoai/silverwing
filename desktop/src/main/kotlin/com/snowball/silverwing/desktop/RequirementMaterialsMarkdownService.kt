package com.snowball.silverwing.desktop

import java.nio.charset.StandardCharsets
import java.nio.charset.CodingErrorAction
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.attribute.BasicFileAttributes
import java.util.Locale
import com.snowball.silverwing.core.WorkspaceFileLanguage

internal data class RequirementMaterialsMarkdownFile(
    val relativePath: String,
    val sizeBytes: Long,
    val modifiedAtMillis: Long = 0,
) {
    val fileName: String get() = relativePath.substringAfterLast('/')
    val extension: String get() = fileName.substringAfterLast('.', "").lowercase(Locale.ROOT)
    val language: WorkspaceFileLanguage get() = WorkspaceFileLanguage.fromPath(fileName)
    val markdown: Boolean get() = language == WorkspaceFileLanguage.MARKDOWN
    val pdf: Boolean get() = extension == "pdf"
    val image: Boolean get() = extension in MATERIALS_IMAGE_EXTENSIONS
    val textPreview: Boolean get() = extension in setOf("csv", "txt") || language != WorkspaceFileLanguage.PLAIN_TEXT
}

internal data class RequirementMaterialsMarkdownCatalog(
    val root: Path,
    val files: List<RequirementMaterialsMarkdownFile>,
    val directories: List<String> = emptyList(),
    val directoryModifiedAtMillis: Map<String, Long> = emptyMap(),
)

/** Read-only, bounded Markdown access for a task's persisted requirement-materials directory. */
internal class RequirementMaterialsMarkdownService(
    private val maxFileBytes: Long = MAX_MARKDOWN_FILE_BYTES,
) {
    init {
        require(maxFileBytes in 1..(Int.MAX_VALUE - 1L)) { "Markdown 文件大小上限无效" }
    }

    fun list(rootPath: Path): RequirementMaterialsMarkdownCatalog {
        val root = validatedRoot(rootPath)
        val files = mutableListOf<RequirementMaterialsMarkdownFile>()
        val directories = mutableListOf<String>()
        val directoryModifiedAtMillis = mutableMapOf<String, Long>()
        Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(directory: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (directory != root &&
                    (Files.isSymbolicLink(directory) || directory.fileName?.toString().equals(".git", ignoreCase = true) || isMaterialsStagingName(directory.fileName?.toString()))
                ) {
                    return FileVisitResult.SKIP_SUBTREE
                }
                if (directory != root) {
                    val relativePath = root.relativize(directory).joinToString("/") { it.toString() }
                    directories += relativePath
                    directoryModifiedAtMillis[relativePath] = attrs.lastModifiedTime().toMillis()
                }
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (attrs.isRegularFile && !attrs.isSymbolicLink && !Files.isSymbolicLink(file) && !isMaterialsStagingName(file.fileName?.toString())) {
                    val relativePath = root.relativize(file).joinToString("/") { it.toString() }
                    files += RequirementMaterialsMarkdownFile(relativePath, attrs.size(), attrs.lastModifiedTime().toMillis())
                }
                return FileVisitResult.CONTINUE
            }

            override fun visitFileFailed(file: Path, exception: java.io.IOException): FileVisitResult {
                throw exception
            }
        })
        return RequirementMaterialsMarkdownCatalog(
            root = root,
            files = files.sortedWith(
                compareBy<RequirementMaterialsMarkdownFile> { it.relativePath.lowercase(Locale.ROOT) }
                    .thenBy(RequirementMaterialsMarkdownFile::relativePath),
            ),
            directories = directories.sortedWith(String.CASE_INSENSITIVE_ORDER),
            directoryModifiedAtMillis = directoryModifiedAtMillis,
        )
    }

    /** Reads a supported text file rooted under the persisted write directory. */
    fun read(rootPath: Path, relativePath: String): String {
        val root = validatedRoot(rootPath)
        val file = resolvePreviewFile(root, relativePath)
        val attrs = Files.readAttributes(file, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
        require(attrs.isRegularFile && !attrs.isSymbolicLink && !Files.isSymbolicLink(file)) {
            "预览文件必须是普通文件：$relativePath"
        }
        require(attrs.size() <= maxFileBytes) {
            "文件超过 ${maxFileBytes / 1024} KB，无法预览：$relativePath"
        }

        // NOFOLLOW_LINKS also protects the final open if the selected path is replaced by a symlink
        // after the component checks. readNBytes bounds memory if the file grows during the read.
        val bytes = Files.newInputStream(file, LinkOption.NOFOLLOW_LINKS).use { it.readNBytes(maxFileBytes.toInt() + 1) }
        require(bytes.size <= maxFileBytes) {
            "文件超过 ${maxFileBytes / 1024} KB，无法预览：$relativePath"
        }
        return runCatching {
            StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(java.nio.ByteBuffer.wrap(bytes)).toString().removePrefix("\uFEFF")
        }.getOrElse { throw IllegalArgumentException("文件不是有效的 UTF-8 文本：$relativePath", it) }
    }

    /** PDF 使用独立的二进制入口，不能经过文本解码或取消文本文件的大小限制。 */
    fun readPdf(rootPath: Path, relativePath: String, maxBytes: Int = MAX_PDF_FILE_BYTES): ByteArray {
        require(maxBytes in 1 until Int.MAX_VALUE) { "PDF 文件大小上限无效" }
        require(relativePath.substringAfterLast('.', "").equals("pdf", ignoreCase = true)) {
            "只允许预览 PDF 文件：$relativePath"
        }
        val file = resolveSafePath(validatedRoot(rootPath), relativePath)
        val attrs = Files.readAttributes(file, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
        require(attrs.isRegularFile && !attrs.isSymbolicLink) { "PDF 必须是普通文件：$relativePath" }
        val limitMessage = "PDF 超过 ${maxBytes / (1024 * 1024)} MB，请使用外部阅读器打开。"
        require(attrs.size() <= maxBytes) { limitMessage }
        // 有界读取并拒绝最终文件的符号链接；文件在读取期间增长也不能突破内存上限。
        return Files.newInputStream(file, LinkOption.NOFOLLOW_LINKS).use { it.readNBytes(maxBytes + 1) }
            .also { require(it.size <= maxBytes) { limitMessage } }
    }

    /** Resolves a selected file or folder for native clipboard copy without following symlinks. */
    fun resolveCopyItem(rootPath: Path, relativePath: String): Path {
        val root = validatedRoot(rootPath)
        val path = resolveSafePath(root, relativePath)
        require(Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS) || Files.isDirectory(path, LinkOption.NOFOLLOW_LINKS)) {
            "文件或文件夹不存在：$relativePath"
        }
        return path
    }

    /** 删除操作重新检查每一项，目录交接点或符号链接不能把操作带出资料根目录。 */
    fun resolveDeleteItems(rootPath: Path, relativePaths: List<String>): List<Path> {
        val root = validatedRoot(rootPath)
        return distinctMaterialTargets(relativePaths.map { resolveCopyItem(root, it) }).onEach { path ->
            if (Files.isDirectory(path, LinkOption.NOFOLLOW_LINKS)) Files.walk(path).use { entries ->
                entries.forEach { entry ->
                    require(!Files.isSymbolicLink(entry) && entry.toRealPath().startsWith(root)) { "删除目录包含符号链接或越界路径：${entry.fileName}" }
                }
            }
        }
    }

    /** 外部打开也复用目录边界检查，不依赖界面上曾经验证过的路径。 */
    fun resolveOpenFile(rootPath: Path, relativePath: String): Path =
        resolveSafePath(validatedRoot(rootPath), relativePath).also {
            require(Files.isRegularFile(it, LinkOption.NOFOLLOW_LINKS)) { "文件不存在或不是普通文件：$relativePath" }
        }

    private fun validatedRoot(path: Path): Path {
        val normalized = path.toAbsolutePath().normalize()
        require(!Files.isSymbolicLink(normalized) && Files.isDirectory(normalized, LinkOption.NOFOLLOW_LINKS)) {
            "任务资料目录不存在或不是普通目录：$normalized"
        }
        return normalized.toRealPath().also { realRoot ->
            require(Files.isDirectory(realRoot, LinkOption.NOFOLLOW_LINKS) && !Files.isSymbolicLink(realRoot)) {
                "任务资料目录不是普通目录：$realRoot"
            }
        }
    }

    private fun resolvePreviewFile(root: Path, relativePath: String): Path {
        require(RequirementMaterialsMarkdownFile(relativePath, 0).textPreview) {
            "该文件类型不支持文本预览：$relativePath"
        }
        val path = resolveSafePath(root, relativePath)
        return path
    }

    private fun resolveSafePath(root: Path, relativePath: String): Path {
        // 文件名的空格属于路径；裁剪会把合法文件映射到另一份同名文件。
        val portablePath = relativePath.replace('\\', '/')
        val segments = portablePath.split('/')
        require(
            portablePath.isNotEmpty() &&
                !portablePath.startsWith('/') &&
                segments.all { it.isNotEmpty() && it != "." && it != ".." && ':' !in it },
        ) {
            "任务资料路径不安全：$relativePath"
        }
        var file = root
        segments.forEach { segment ->
            file = file.resolve(segment)
            require(!Files.isSymbolicLink(file)) { "任务资料路径不能包含符号链接：$relativePath" }
        }
        val normalized = file.normalize()
        require(normalized.startsWith(root) && normalized != root) { "任务资料路径越界：$relativePath" }
        val realFile = normalized.toRealPath()
        require(realFile.startsWith(root) && realFile != root) { "任务资料路径越界：$relativePath" }
        return normalized
    }

    private companion object {
        const val MAX_MARKDOWN_FILE_BYTES = 512L * 1024
        const val MAX_PDF_FILE_BYTES = 64 * 1024 * 1024
    }
}
