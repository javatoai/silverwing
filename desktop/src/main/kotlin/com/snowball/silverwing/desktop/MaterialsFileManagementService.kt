package com.snowball.silverwing.desktop

import java.awt.Image
import java.awt.Toolkit
import java.awt.datatransfer.DataFlavor
import java.awt.image.BufferedImage
import java.io.ByteArrayOutputStream
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.NoSuchFileException
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.BasicFileAttributes
import java.time.Clock
import java.time.LocalDateTime
import java.time.format.DateTimeFormatter
import java.util.UUID
import javax.imageio.ImageIO
import javax.swing.ImageIcon

/** All filesystem effects are replaceable; tests never need user files or the system clipboard. */
internal interface MaterialsFileSystem {
    fun attributes(path: Path): BasicFileAttributes?
    fun realPath(path: Path): Path
    fun children(path: Path): List<Path>
    fun createDirectory(path: Path)
    fun writeNew(path: Path, bytes: ByteArray)
    fun copyNew(source: Path, destination: Path)
    fun move(source: Path, destination: Path, replace: Boolean = false)
    fun delete(path: Path)
}

internal object NioMaterialsFileSystem : MaterialsFileSystem {
    override fun attributes(path: Path): BasicFileAttributes? = try {
        Files.readAttributes(path, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    } catch (_: NoSuchFileException) { null }
    override fun realPath(path: Path): Path = path.toRealPath()
    override fun children(path: Path): List<Path> = Files.newDirectoryStream(path).use { it.toList() }
    override fun createDirectory(path: Path) { Files.createDirectory(path) }
    override fun writeNew(path: Path, bytes: ByteArray) {
        writeNewOutput(path) { it.write(bytes) }
    }
    override fun copyNew(source: Path, destination: Path) {
        Files.newInputStream(source, LinkOption.NOFOLLOW_LINKS).use { input ->
            writeNewOutput(destination) { output -> input.copyTo(output) }
        }
    }
    private fun writeNewOutput(path: Path, write: (java.io.OutputStream) -> Unit) {
        // Opening an existing file, including a final symlink, always fails instead of truncating it.
        val output = Files.newOutputStream(path, StandardOpenOption.CREATE_NEW, StandardOpenOption.WRITE,
            LinkOption.NOFOLLOW_LINKS)
        val createdKey = runCatching {
            Files.readAttributes(path, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS).fileKey()
        }.getOrNull()
        try { output.use(write) } catch (error: Throwable) {
            // A concurrent writer may have replaced the name while this stream was open. Only
            // remove the inode we created; retaining an incomplete file is safer if identity is unavailable.
            runCatching {
                val remaining = Files.readAttributes(path, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
                if (createdKey != null && remaining.isRegularFile && !remaining.isSymbolicLink && remaining.fileKey() == createdKey) {
                    Files.deleteIfExists(path)
                }
            }
            throw error
        }
    }
    override fun move(source: Path, destination: Path, replace: Boolean) {
        if (replace) Files.move(source, destination, StandardCopyOption.REPLACE_EXISTING)
        else Files.move(source, destination)
    }
    override fun delete(path: Path) { Files.deleteIfExists(path) }
}

internal fun interface MaterialsImageClipboard {
    /** Called only in response to the user's paste command. */
    fun readImage(): BufferedImage?
}

internal object SystemMaterialsImageClipboard : MaterialsImageClipboard {
    override fun readImage(): BufferedImage? {
        val content = Toolkit.getDefaultToolkit().systemClipboard.getContents(null) ?: return null
        if (!content.isDataFlavorSupported(DataFlavor.imageFlavor)) return null
        val image = content.getTransferData(DataFlavor.imageFlavor) as? Image ?: return null
        val loaded = ImageIcon(image).image
        val width = loaded.getWidth(null)
        val height = loaded.getHeight(null)
        validateMaterialsImageDimensions(width, height)
        return BufferedImage(width, height, BufferedImage.TYPE_INT_ARGB).also { copy ->
            val graphics = copy.createGraphics()
            try { check(graphics.drawImage(loaded, 0, 0, null)) { "剪贴板图片尚未加载完成，请重新复制后粘贴。" } }
            finally { graphics.dispose() }
        }
    }
}

private fun validateMaterialsImageDimensions(width: Int, height: Int) {
    require(width > 0 && height > 0 && width.toLong() * height <= 32_000_000) {
        "截图尺寸无效或超过 3200 万像素，请缩小图片后粘贴。"
    }
}

internal sealed interface MaterialsFileOperation {
    data object NewDirectory : MaterialsFileOperation
    data class NewFile(val bytes: ByteArray) : MaterialsFileOperation
    data class ImportFile(val source: Path) : MaterialsFileOperation
    data class Rename(val relativePath: String) : MaterialsFileOperation
}

internal data class MaterialsFileRequest(
    val root: Path,
    val directory: String,
    val name: String,
    val operation: MaterialsFileOperation,
)

internal data class MaterialsFileIdentity(val key: String?, val size: Long, val modifiedMillis: Long)

internal data class MaterialsFileConflict(
    val request: MaterialsFileRequest,
    val suggestedName: String,
    val canReplace: Boolean,
    val existingIdentity: MaterialsFileIdentity?,
)

internal enum class MaterialsConflictChoice { CANCEL, SAVE_AS, REPLACE }

internal sealed interface MaterialsFileOperationResult {
    data class Completed(val relativePath: String, val previousRelativePath: String? = null,
        val isDirectory: Boolean = false) : MaterialsFileOperationResult
    data class Conflict(val conflict: MaterialsFileConflict) : MaterialsFileOperationResult
    data object Cancelled : MaterialsFileOperationResult
}

/** Creates and imports only under the task's root; a conflict is an explicit, resumable decision. */
internal class MaterialsFileManagementService(
    private val fileSystem: MaterialsFileSystem = NioMaterialsFileSystem,
    private val clock: Clock = Clock.systemDefaultZone(),
) {
    fun createDirectory(root: Path, directory: String, name: String): MaterialsFileOperationResult =
        execute(MaterialsFileRequest(root, directory, name, MaterialsFileOperation.NewDirectory))

    fun createMarkdown(root: Path, directory: String, name: String): MaterialsFileOperationResult =
        execute(MaterialsFileRequest(root, directory, markdownFileName(name), MaterialsFileOperation.NewFile(byteArrayOf())))

    fun rename(root: Path, relativePath: String, name: String): MaterialsFileOperationResult =
        execute(MaterialsFileRequest(root, relativePath.replace('\\', '/').substringBeforeLast('/', ""), name,
            MaterialsFileOperation.Rename(relativePath)))

    fun importFile(root: Path, directory: String, source: Path): MaterialsFileOperationResult =
        execute(MaterialsFileRequest(root, directory, source.fileName?.toString() ?: error("导入文件名无效。"),
            MaterialsFileOperation.ImportFile(source)))

    fun pasteImage(root: Path, directory: String, image: BufferedImage): MaterialsFileOperationResult {
        validateMaterialsImageDimensions(image.width, image.height)
        val png = ByteArrayOutputStream().use { output ->
            check(ImageIO.write(image, "png", output)) { "无法把截图保存为 PNG。" }
            output.toByteArray()
        }
        val name = "截图-${LocalDateTime.now(clock).format(DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss"))}.png"
        return execute(MaterialsFileRequest(root, directory, name, MaterialsFileOperation.NewFile(png)))
    }

    fun resolveConflict(conflict: MaterialsFileConflict, choice: MaterialsConflictChoice,
        alternateName: String = conflict.suggestedName): MaterialsFileOperationResult = when (choice) {
        MaterialsConflictChoice.CANCEL -> MaterialsFileOperationResult.Cancelled
        MaterialsConflictChoice.SAVE_AS -> execute(conflict.request.copy(name = alternateName))
        MaterialsConflictChoice.REPLACE -> {
            require(conflict.canReplace) { "文件夹和重命名操作不能替换已有项，请另存或取消。" }
            execute(conflict.request, conflict)
        }
    }

    fun execute(request: MaterialsFileRequest): MaterialsFileOperationResult = execute(request, null)

    fun resolveCurrentDirectory(root: Path, directory: String): Path = resolveDirectory(validatedRoot(root), directory)

    private fun execute(request: MaterialsFileRequest, replaceConflict: MaterialsFileConflict?): MaterialsFileOperationResult {
        validateMaterialsEntryName(request.name)
        val root = validatedRoot(request.root)
        val parent = resolveDirectory(root, request.directory)
        val destination = parent.resolve(request.name)
        val operation = request.operation
        val source = when (operation) {
            is MaterialsFileOperation.Rename -> resolveExisting(root, operation.relativePath).also { path ->
                if (fileSystem.attributes(path)?.isDirectory == true) validateTree(root, path)
            }
            is MaterialsFileOperation.ImportFile -> validatedImportSource(operation.source)
            else -> null
        }
        if (operation is MaterialsFileOperation.Rename && source == destination) {
            if (source!!.fileName.toString() != request.name) {
                require(validatedRoot(request.root) == root && resolveDirectory(root, request.directory) == parent) {
                    "任务资料目录发生了变化，请刷新后重试。"
                }
                renameChangingCase(root, parent, source, destination)
            }
            return completed(root, destination, operation, fileSystem.attributes(destination)?.isDirectory == true)
        }
        require(operation !is MaterialsFileOperation.ImportFile || source != destination) { "源文件已在当前目录中，无需重复导入。" }
        val existing = fileSystem.attributes(destination)
        if (existing != null) {
            validateExisting(root, destination, existing)
            val canReplace = existing.isRegularFile && (operation is MaterialsFileOperation.NewFile || operation is MaterialsFileOperation.ImportFile)
            val conflict = MaterialsFileConflict(request, availableName(parent, request.name), canReplace,
                existing.takeIf { it.isRegularFile }?.identity())
            if (replaceConflict == null) return MaterialsFileOperationResult.Conflict(conflict)
            require(canReplace && existing.identity() == replaceConflict.existingIdentity) {
                "同名文件在确认期间发生了变化，请重新导入并选择处理方式。"
            }
        } else require(replaceConflict == null) { "同名文件在确认期间已被移走，请重新导入。" }

        // Re-check the root and parents directly before mutation, rather than relying on a UI catalog.
        require(validatedRoot(request.root) == root && resolveDirectory(root, request.directory) == parent) {
            "任务资料目录发生了变化，请刷新后重试。"
        }
        val isDirectory = operation is MaterialsFileOperation.NewDirectory ||
            (operation is MaterialsFileOperation.Rename && fileSystem.attributes(source!!)?.isDirectory == true)
        when (operation) {
            MaterialsFileOperation.NewDirectory -> fileSystem.createDirectory(destination)
            is MaterialsFileOperation.Rename -> fileSystem.move(source!!, destination)
            else -> if (replaceConflict == null) writeNew(operation, source, destination)
                else replaceFile(root, parent, destination, operation, source, replaceConflict.existingIdentity!!)
        }
        return completed(root, destination, operation, isDirectory)
    }

    private fun writeNew(operation: MaterialsFileOperation, source: Path?, destination: Path) = when (operation) {
        is MaterialsFileOperation.NewFile -> fileSystem.writeNew(destination, operation.bytes)
        is MaterialsFileOperation.ImportFile -> fileSystem.copyNew(source!!, destination)
        else -> error("不支持的文件写入操作。")
    }

    private fun renameChangingCase(root: Path, parent: Path, source: Path, destination: Path) {
        // Windows Path equality ignores case; a two-step move makes the new spelling observable.
        val staging = parent.resolve(".silverwing-rename-${UUID.randomUUID()}")
        fileSystem.move(source, staging)
        try {
            resolveDirectory(root, relative(root, parent))
            fileSystem.move(staging, destination)
        } catch (error: Throwable) {
            runCatching {
                resolveDirectory(root, relative(root, parent))
                fileSystem.move(staging, source)
            }.exceptionOrNull()?.let { rollbackError -> error.addSuppressed(rollbackError) }
            throw error
        }
    }

    private fun replaceFile(root: Path, parent: Path, destination: Path, operation: MaterialsFileOperation,
        source: Path?, expected: MaterialsFileIdentity) {
        val staging = parent.resolve(".silverwing-import-${UUID.randomUUID()}.tmp")
        var created = false
        try {
            writeNew(operation, source, staging)
            created = true
            resolveDirectory(root, relative(root, parent))
            val current = fileSystem.attributes(destination) ?: error("待替换文件已被移走，请重试。")
            validateExisting(root, destination, current)
            require(current.isRegularFile && current.identity() == expected) { "待替换文件发生了变化，请重新导入。" }
            fileSystem.move(staging, destination, replace = true)
            created = false
        } finally {
            if (created) runCatching {
                resolveDirectory(root, relative(root, parent))
                fileSystem.delete(staging)
            }
        }
    }

    private fun completed(root: Path, destination: Path, operation: MaterialsFileOperation, directory: Boolean) =
        MaterialsFileOperationResult.Completed(relative(root, destination),
            (operation as? MaterialsFileOperation.Rename)?.relativePath?.replace('\\', '/'), directory)

    private fun validatedRoot(rootPath: Path): Path {
        val normalized = rootPath.toAbsolutePath().normalize()
        rejectLinkAncestors(normalized)
        val attributes = fileSystem.attributes(normalized)
        require(attributes?.isDirectory == true && !attributes.isSymbolicLink) { "任务资料根目录不存在或不是普通目录。" }
        require(normalized.none { it.toString().equals(".git", true) }) { "不能在 Git 内部目录管理任务资料。" }
        return fileSystem.realPath(normalized)
    }

    private fun resolveDirectory(root: Path, directory: String): Path =
        if (directory.isEmpty()) root.also { validateExisting(root, it, fileSystem.attributes(it) ?: error("任务资料目录已被移走。")) }
        else resolveExisting(root, directory).also {
            require(fileSystem.attributes(it)?.isDirectory == true) { "目标不是文件夹：$directory" }
        }

    private fun resolveExisting(root: Path, relativePath: String): Path {
        val segments = relativePath.replace('\\', '/').split('/')
        require(segments.isNotEmpty() && segments.all { it.isNotEmpty() && it != "." && it != ".." && ':' !in it }) {
            "任务资料路径不安全：$relativePath"
        }
        var target = root
        for (segment in segments) {
            require(!segment.equals(".git", true)) { "不能操作 Git 内部目录。" }
            target = target.resolve(segment)
            validateExisting(root, target, fileSystem.attributes(target) ?: error("文件或文件夹不存在：$relativePath"))
        }
        require(target != root && target.normalize().startsWith(root)) { "任务资料路径越界：$relativePath" }
        return target
    }

    private fun validateExisting(root: Path, path: Path, attributes: BasicFileAttributes) {
        require(!attributes.isSymbolicLink && (attributes.isRegularFile || attributes.isDirectory)) {
            "任务资料路径不能包含符号链接或特殊文件：${path.fileName}"
        }
        val real = fileSystem.realPath(path)
        require(path.normalize().startsWith(root) && real.startsWith(root)) {
            "任务资料路径越界：${path.fileName}"
        }
        require(real == path.normalize()) { "任务资料路径不能包含目录链接或交接点：${path.fileName}" }
    }

    private fun validateTree(root: Path, directory: Path) {
        val pending = ArrayDeque<Path>().apply { add(directory) }
        while (pending.isNotEmpty()) for (child in fileSystem.children(pending.removeFirst())) {
            require(!child.fileName.toString().equals(".git", true)) { "文件夹包含 Git 内部目录，不能重命名。" }
            val attributes = fileSystem.attributes(child) ?: error("文件夹内容发生了变化，请重试。")
            validateExisting(root, child, attributes)
            if (attributes.isDirectory) pending.add(child)
        }
    }

    private fun validatedImportSource(path: Path): Path {
        val normalized = path.toAbsolutePath().normalize()
        rejectLinkAncestors(normalized)
        require(normalized.none { it.toString().equals(".git", true) }) { "不能导入 Git 内部文件。" }
        val attributes = fileSystem.attributes(normalized)
        require(attributes?.isRegularFile == true && !attributes.isSymbolicLink) { "只能拖入普通文件：${path.fileName}" }
        return fileSystem.realPath(normalized)
    }

    private fun rejectLinkAncestors(path: Path) {
        var current = path.root ?: error("文件路径缺少根目录。")
        for (segment in path) {
            current = current.resolve(segment)
            val attributes = fileSystem.attributes(current)
            require(attributes?.isSymbolicLink != true) { "文件路径不能包含符号链接：${current.fileName}" }
            if (attributes != null) require(fileSystem.realPath(current) == current) {
                // Windows directory junctions can report isDirectory without isSymbolicLink.
                "文件路径不能包含目录链接或交接点：${current.fileName}"
            }
        }
    }

    private fun availableName(parent: Path, name: String): String {
        val dot = name.lastIndexOf('.').takeIf { it > 0 } ?: name.length
        val stem = name.substring(0, dot)
        val suffix = name.substring(dot)
        for (index in 1..10_000) {
            val candidate = "$stem ($index)$suffix"
            if (fileSystem.attributes(parent.resolve(candidate)) == null) return candidate
        }
        error("同名文件过多，请手动输入其他名称。")
    }

    private fun relative(root: Path, path: Path) = root.relativize(path).joinToString("/") { it.toString() }
    private fun BasicFileAttributes.identity() = MaterialsFileIdentity(fileKey()?.toString(), size(), lastModifiedTime().toMillis())
}

internal fun markdownFileName(name: String): String {
    validateMaterialsEntryName(name)
    return if (name.endsWith(".md", true) || name.endsWith(".markdown", true)) name else "$name.md"
}

internal fun validateMaterialsEntryName(name: String) {
    require(name.isNotBlank() && name.length <= 255 && name != "." && name != ".." && !name.equals(".git", true)) {
        "请输入有效名称，不能使用 .、.. 或 .git。"
    }
    require(name.none { it.code < 32 || it in "<>:\"/\\|?*" } && !name.endsWith('.') && !name.endsWith(' ')) {
        "名称不能含路径分隔符或 <>:\"|?*，也不能以空格或句点结尾。"
    }
    require(!Regex("(?i)(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\\..*)?").matches(name)) {
        "该名称是系统保留名称，请换一个名称。"
    }
}
