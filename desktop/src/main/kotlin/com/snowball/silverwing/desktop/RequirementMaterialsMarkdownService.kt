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

internal data class RequirementMaterialsMarkdownFile(
    val relativePath: String,
    val sizeBytes: Long,
) {
    val fileName: String get() = relativePath.substringAfterLast('/')
    val extension: String get() = fileName.substringAfterLast('.', "").lowercase(Locale.ROOT)
    val markdown: Boolean get() = extension == "md"
}

internal data class RequirementMaterialsMarkdownCatalog(
    val root: Path,
    val files: List<RequirementMaterialsMarkdownFile>,
    val directories: List<String> = emptyList(),
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
        Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(directory: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (directory != root &&
                    (Files.isSymbolicLink(directory) || directory.fileName?.toString().equals(".git", ignoreCase = true))
                ) {
                    return FileVisitResult.SKIP_SUBTREE
                }
                if (directory != root) directories += root.relativize(directory).joinToString("/") { it.toString() }
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (attrs.isRegularFile && !attrs.isSymbolicLink && !Files.isSymbolicLink(file) && file.isPreviewable()) {
                    val relativePath = root.relativize(file).joinToString("/") { it.toString() }
                    files += RequirementMaterialsMarkdownFile(relativePath, attrs.size())
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

    /** Resolves a selected file or folder for native clipboard copy without following symlinks. */
    fun resolveCopyItem(rootPath: Path, relativePath: String): Path {
        val root = validatedRoot(rootPath)
        val path = resolveSafePath(root, relativePath)
        require(Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS) || Files.isDirectory(path, LinkOption.NOFOLLOW_LINKS)) {
            "文件或文件夹不存在：$relativePath"
        }
        return path
    }

    private fun validatedRoot(path: Path): Path {
        val normalized = path.toAbsolutePath().normalize()
        require(!Files.isSymbolicLink(normalized) && Files.isDirectory(normalized, LinkOption.NOFOLLOW_LINKS)) {
            "需求资料目录不存在或不是普通目录：$normalized"
        }
        return normalized.toRealPath().also { realRoot ->
            require(Files.isDirectory(realRoot, LinkOption.NOFOLLOW_LINKS) && !Files.isSymbolicLink(realRoot)) {
                "需求资料目录不是普通目录：$realRoot"
            }
        }
    }

    private fun resolvePreviewFile(root: Path, relativePath: String): Path {
        require(relativePath.substringAfterLast('.', "").lowercase(Locale.ROOT) in setOf("md", "json", "csv", "txt")) {
            "只允许预览 .md、.json、.csv、.txt 文件：$relativePath"
        }
        val path = resolveSafePath(root, relativePath)
        return path
    }

    private fun resolveSafePath(root: Path, relativePath: String): Path {
        val portablePath = relativePath.trim().replace('\\', '/')
        val segments = portablePath.split('/')
        require(
            portablePath.isNotBlank() &&
                !portablePath.startsWith('/') &&
                segments.all { it.isNotBlank() && it != "." && it != ".." && ':' !in it },
        ) {
            "需求资料路径不安全：$relativePath"
        }
        var file = root
        segments.forEach { segment ->
            file = file.resolve(segment)
            require(!Files.isSymbolicLink(file)) { "需求资料路径不能包含符号链接：$relativePath" }
        }
        val normalized = file.normalize()
        require(normalized.startsWith(root) && normalized != root) { "需求资料路径越界：$relativePath" }
        val realFile = normalized.toRealPath()
        require(realFile.startsWith(root) && realFile != root) { "需求资料路径越界：$relativePath" }
        return normalized
    }

    private fun Path.isPreviewable(): Boolean = fileName?.toString()?.substringAfterLast('.', "")
        ?.lowercase(Locale.ROOT) in setOf("md", "json", "csv", "txt")

    private companion object {
        const val MAX_MARKDOWN_FILE_BYTES = 512L * 1024
    }
}
