package com.snowball.silverwing.core

import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.util.Locale

enum class WorkspaceFileLanguage {
    JAVA,
    KOTLIN,
    GRADLE,
    PROPERTIES,
    HTML,
    CSS,
    JAVASCRIPT,
    TYPESCRIPT,
    JSON,
    YAML,
    SQL,
    PYTHON,
    SHELL,
    POWERSHELL,
    BATCH,
    MARKDOWN,
    PLAIN_TEXT,
    ;

    companion object {
        fun fromPath(path: String): WorkspaceFileLanguage {
            val normalized = path.lowercase(Locale.ROOT)
            return when {
                normalized.endsWith(".gradle") || normalized.endsWith(".gradle.kts") -> GRADLE
                normalized.endsWith(".java") -> JAVA
                normalized.endsWith(".kt") || normalized.endsWith(".kts") -> KOTLIN
                normalized.endsWith(".properties") || normalized.endsWith(".ini") || normalized.endsWith(".conf") -> PROPERTIES
                normalized.endsWith(".html") || normalized.endsWith(".htm") || normalized.endsWith(".xml") || normalized.endsWith(".svg") -> HTML
                normalized.endsWith(".css") || normalized.endsWith(".scss") || normalized.endsWith(".sass") || normalized.endsWith(".less") -> CSS
                normalized.endsWith(".js") || normalized.endsWith(".jsx") -> JAVASCRIPT
                normalized.endsWith(".ts") || normalized.endsWith(".tsx") -> TYPESCRIPT
                normalized.endsWith(".json") -> JSON
                normalized.endsWith(".yaml") || normalized.endsWith(".yml") -> YAML
                normalized.endsWith(".sql") -> SQL
                normalized.endsWith(".py") || normalized.endsWith(".pyw") || normalized.endsWith(".pyi") -> PYTHON
                normalized.endsWith(".ps1") || normalized.endsWith(".psm1") || normalized.endsWith(".psd1") -> POWERSHELL
                normalized.endsWith(".bat") || normalized.endsWith(".cmd") -> BATCH
                normalized.endsWith(".sh") || normalized.endsWith(".bash") || normalized.endsWith(".zsh") -> SHELL
                normalized.endsWith(".md") || normalized.endsWith(".markdown") -> MARKDOWN
                else -> PLAIN_TEXT
            }
        }
    }
}

enum class WorkspaceFilePreviewMode { COMPARISON, CONTENT }

enum class WorkspaceFileContentOrigin { WORKTREE, HEAD }

enum class WorkspaceFileComparisonLineKind { CONTEXT, DELETED, ADDED }

data class WorkspaceFilePreviewContent(
    val text: String,
    val truncated: Boolean,
    val origin: WorkspaceFileContentOrigin,
)

data class WorkspaceFileComparisonLine(
    val kind: WorkspaceFileComparisonLineKind,
    val lineNumber: Int,
    val text: String,
)

data class WorkspaceFileComparisonRow(
    val oldLine: WorkspaceFileComparisonLine? = null,
    val newLine: WorkspaceFileComparisonLine? = null,
) {
    val isChanged: Boolean
        get() = oldLine?.kind != WorkspaceFileComparisonLineKind.CONTEXT ||
            newLine?.kind != WorkspaceFileComparisonLineKind.CONTEXT
}

data class WorkspaceFileComparison(
    val rows: List<WorkspaceFileComparisonRow>,
    val oldContent: WorkspaceFilePreviewContent? = null,
    val newContent: WorkspaceFilePreviewContent? = null,
    val truncated: Boolean,
)

data class WorkspaceGitFilePreview(
    val language: WorkspaceFileLanguage,
    val content: WorkspaceFilePreviewContent? = null,
    val comparison: WorkspaceFileComparison? = null,
    val notice: String? = null,
    val unavailableReason: String? = null,
) {
    val defaultMode: WorkspaceFilePreviewMode
        get() = if (comparison?.rows?.isNotEmpty() == true) WorkspaceFilePreviewMode.COMPARISON else WorkspaceFilePreviewMode.CONTENT
}

/**
 * Reads only local Git and worktree state for the uncommitted-file dialog.
 * All diffs compare HEAD to the working tree so staged and unstaged changes are shown together.
 */
class WorkspaceGitFilePreviewService(
    private val git: GitClient = GitClient(),
) {
    fun preview(worktreePath: String, change: WorkspaceGitFileChange): WorkspaceGitFilePreview {
        val worktree = resolveWorktree(worktreePath)
        val safeChange = change.copy(path = normalizedRelativePath(worktree, change.path))
        return when (safeChange.kind) {
            WorkspaceGitFileChangeKind.MODIFIED -> modifiedPreview(worktree, safeChange)
            WorkspaceGitFileChangeKind.ADDED -> addedPreview(worktree, safeChange)
            WorkspaceGitFileChangeKind.UNTRACKED -> untrackedPreview(worktree, safeChange)
            WorkspaceGitFileChangeKind.DELETED -> deletedPreview(worktree, safeChange)
            WorkspaceGitFileChangeKind.RENAMED -> contentPreview(worktree, safeChange, "文件已重命名，显示当前文件内容。")
            WorkspaceGitFileChangeKind.COPIED -> contentPreview(worktree, safeChange, "文件已复制，显示当前文件内容。")
            WorkspaceGitFileChangeKind.TYPE_CHANGED -> contentPreview(worktree, safeChange, "文件类型已变更，显示当前文件内容。")
            WorkspaceGitFileChangeKind.CONFLICTED -> contentPreview(worktree, safeChange, "文件存在冲突，显示当前文件内容。")
        }
    }

    private fun modifiedPreview(worktree: Worktree, change: WorkspaceGitFileChange): WorkspaceGitFilePreview {
        val oldContent = readHeadContent(worktree.path, change.path)
        val newContent = readWorktreeContent(worktree, change.path)
        val diff = readComparisonDiff(worktree.path, change.path)
        val comparison = buildComparison(oldContent.content, newContent.content, diff.hunks)
        return WorkspaceGitFilePreview(
            language = WorkspaceFileLanguage.fromPath(change.path),
            content = newContent.content,
            comparison = comparison,
            notice = comparisonFallbackNotice(comparison, oldContent, newContent, diff),
            unavailableReason = newContent.reason ?: oldContent.reason ?: diff.reason,
        )
    }

    private fun addedPreview(worktree: Worktree, change: WorkspaceGitFileChange): WorkspaceGitFilePreview {
        val content = readWorktreeContent(worktree, change.path)
        val comparison = singleSideComparison(content.content, WorkspaceFileComparisonLineKind.ADDED)
        return WorkspaceGitFilePreview(
            language = WorkspaceFileLanguage.fromPath(change.path),
            content = content.content,
            comparison = comparison,
            notice = comparisonFallbackNotice(comparison, newContent = content),
            unavailableReason = content.reason,
        )
    }

    private fun untrackedPreview(worktree: Worktree, change: WorkspaceGitFileChange): WorkspaceGitFilePreview {
        val content = readWorktreeContent(worktree, change.path)
        val comparison = singleSideComparison(content.content, WorkspaceFileComparisonLineKind.ADDED)
        return WorkspaceGitFilePreview(
            language = WorkspaceFileLanguage.fromPath(change.path),
            content = content.content,
            comparison = comparison,
            notice = comparisonFallbackNotice(comparison, newContent = content),
            unavailableReason = content.reason,
        )
    }

    private fun deletedPreview(worktree: Worktree, change: WorkspaceGitFileChange): WorkspaceGitFilePreview {
        val content = readHeadContent(worktree.path, change.path)
        val comparison = singleSideComparison(content.content, WorkspaceFileComparisonLineKind.DELETED)
        return WorkspaceGitFilePreview(
            language = WorkspaceFileLanguage.fromPath(change.path),
            content = content.content,
            comparison = comparison,
            notice = comparisonFallbackNotice(comparison, oldContent = content),
            unavailableReason = content.reason,
        )
    }

    private fun contentPreview(
        worktree: Worktree,
        change: WorkspaceGitFileChange,
        notice: String,
    ): WorkspaceGitFilePreview {
        val content = readWorktreeContent(worktree, change.path)
        return WorkspaceGitFilePreview(
            language = WorkspaceFileLanguage.fromPath(change.path),
            content = content.content,
            notice = notice,
            unavailableReason = content.reason,
        )
    }

    private fun resolveWorktree(worktreePath: String): Worktree {
        val path = Path.of(worktreePath).toAbsolutePath().normalize()
        require(Files.isDirectory(path)) { "工作区不存在，无法预览文件" }
        val realPath = runCatching { path.toRealPath() }.getOrElse {
            throw IllegalArgumentException("工作区不存在，无法预览文件")
        }
        return Worktree(path, realPath)
    }

    private fun normalizedRelativePath(worktree: Worktree, relativePath: String): String {
        val target = worktree.path.resolve(relativePath).normalize()
        require(target.startsWith(worktree.path)) { "文件路径不在当前工作区内，无法预览" }
        return worktree.path.relativize(target).toString().replace('\\', '/')
    }

    private fun readWorktreeContent(worktree: Worktree, relativePath: String): PreviewReadResult {
        val target = worktree.path.resolve(relativePath).normalize()
        if (!Files.isRegularFile(target)) return PreviewReadResult.Unavailable("文件不存在或已删除，无法预览")
        val realFile = runCatching { target.toRealPath() }.getOrElse {
            return PreviewReadResult.Unavailable("文件不存在或已删除，无法预览")
        }
        if (!realFile.startsWith(worktree.realPath)) return PreviewReadResult.Unavailable("文件不在当前工作区内，无法预览")
        return runCatching {
            Files.newInputStream(realFile).use { input ->
                decodeFileBytes(input.readNBytes(MAX_WORKSPACE_FILE_PREVIEW_BYTES + 1), WorkspaceFileContentOrigin.WORKTREE)
            }
        }.getOrElse { error ->
            PreviewReadResult.Unavailable(error.message ?: "读取文件失败")
        }
    }

    private fun readHeadContent(worktree: Path, relativePath: String): PreviewReadResult {
        val result = git.readOnly(worktree, "cat-file", "blob", "HEAD:$relativePath", check = false)
        if (!result.succeeded) return PreviewReadResult.Unavailable("无法从 HEAD 读取文件")
        if (result.stdout.indexOf('\u0000') >= 0 || result.stdout.indexOf('\uFFFD') >= 0) {
            return PreviewReadResult.Unavailable("疑似二进制文件，暂不支持文本预览")
        }
        val (text, truncated) = truncateText(result.stdout)
        return PreviewReadResult.Available(WorkspaceFilePreviewContent(text, truncated, WorkspaceFileContentOrigin.HEAD))
    }

    private fun decodeFileBytes(bytes: ByteArray, origin: WorkspaceFileContentOrigin): PreviewReadResult {
        val truncated = bytes.size > MAX_WORKSPACE_FILE_PREVIEW_BYTES
        val previewBytes = if (truncated) bytes.copyOf(MAX_WORKSPACE_FILE_PREVIEW_BYTES) else bytes
        if (previewBytes.any { it == 0.toByte() }) return PreviewReadResult.Unavailable("疑似二进制文件，暂不支持文本预览")
        val text = decodeUtf8(previewBytes) ?: if (truncated) {
            (1..3).firstNotNullOfOrNull { removedBytes ->
                previewBytes.takeIf { it.size > removedBytes }
                    ?.copyOf(previewBytes.size - removedBytes)
                    ?.let(::decodeUtf8)
            }
        } else {
            null
        } ?: return PreviewReadResult.Unavailable("文件不是 UTF-8 文本，暂不支持预览")
        return PreviewReadResult.Available(WorkspaceFilePreviewContent(text, truncated, origin))
    }

    private fun decodeUtf8(bytes: ByteArray): String? = try {
        StandardCharsets.UTF_8.newDecoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
            .decode(ByteBuffer.wrap(bytes))
            .toString()
    } catch (_: CharacterCodingException) {
        null
    }

    private fun readComparisonDiff(worktree: Path, relativePath: String): ComparisonDiffReadResult {
        val result = git.readOnly(
            worktree,
            "diff",
            "--no-ext-diff",
            "--unified=0",
            "HEAD",
            "--",
            relativePath,
            check = false,
        )
        if (!result.succeeded) return ComparisonDiffReadResult.Unavailable("无法读取 Git 差异")
        if (result.stdout.isBlank()) return ComparisonDiffReadResult.Empty
        if (result.stdout.contains("GIT binary patch") || result.stdout.contains("Binary files ")) {
            return ComparisonDiffReadResult.Unavailable("疑似二进制文件，无法显示左右对比")
        }
        val (text, truncated) = truncateText(result.stdout)
        if (truncated) return ComparisonDiffReadResult.Unavailable("变更内容较大，无法生成完整左右对比")
        return ComparisonDiffReadResult.Available(parseComparisonHunks(text))
    }

    private fun buildComparison(
        oldContent: WorkspaceFilePreviewContent?,
        newContent: WorkspaceFilePreviewContent?,
        hunks: List<ComparisonHunk>?,
    ): WorkspaceFileComparison? {
        if (oldContent == null || newContent == null || hunks == null || oldContent.truncated || newContent.truncated) return null
        val oldLines = previewLines(oldContent.text)
        val newLines = previewLines(newContent.text)
        val rows = mutableListOf<WorkspaceFileComparisonRow>()
        var oldIndex = 0
        var newIndex = 0

        fun appendContextUntil(oldTarget: Int, newTarget: Int): Boolean {
            if (oldTarget !in oldIndex..oldLines.size || newTarget !in newIndex..newLines.size) return false
            while (oldIndex < oldTarget || newIndex < newTarget) {
                val oldLine = oldLines.getOrNull(oldIndex)?.let {
                    WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.CONTEXT, oldIndex + 1, it)
                }
                val newLine = newLines.getOrNull(newIndex)?.let {
                    WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.CONTEXT, newIndex + 1, it)
                }
                if (oldLine == null && newLine == null) return false
                rows += WorkspaceFileComparisonRow(oldLine, newLine)
                if (oldLine != null) oldIndex++
                if (newLine != null) newIndex++
            }
            return true
        }

        hunks.forEach { hunk ->
            val oldTarget = if (hunk.oldStart == 0) 0 else hunk.oldStart - 1
            val newTarget = if (hunk.newStart == 0) 0 else hunk.newStart - 1
            if (!appendContextUntil(oldTarget, newTarget)) return null

            val deleted = mutableListOf<WorkspaceFileComparisonLine>()
            val added = mutableListOf<WorkspaceFileComparisonLine>()
            hunk.operations.forEach { operation ->
                when (operation) {
                    ComparisonDiffOperation.DELETED -> {
                        val text = oldLines.getOrNull(oldIndex) ?: return null
                        deleted += WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.DELETED, oldIndex + 1, text)
                        oldIndex++
                    }

                    ComparisonDiffOperation.ADDED -> {
                        val text = newLines.getOrNull(newIndex) ?: return null
                        added += WorkspaceFileComparisonLine(WorkspaceFileComparisonLineKind.ADDED, newIndex + 1, text)
                        newIndex++
                    }
                }
            }
            repeat(maxOf(deleted.size, added.size)) { index ->
                rows += WorkspaceFileComparisonRow(deleted.getOrNull(index), added.getOrNull(index))
            }
        }
        if (!appendContextUntil(oldLines.size, newLines.size)) return null
        return WorkspaceFileComparison(
            rows = rows,
            oldContent = oldContent,
            newContent = newContent,
            truncated = false,
        )
    }

    private fun singleSideComparison(
        content: WorkspaceFilePreviewContent?,
        kind: WorkspaceFileComparisonLineKind,
    ): WorkspaceFileComparison? {
        if (content == null || content.truncated) return null
        val rows = previewLines(content.text).mapIndexed { index, text ->
            val line = WorkspaceFileComparisonLine(kind, index + 1, text)
            if (kind == WorkspaceFileComparisonLineKind.DELETED) WorkspaceFileComparisonRow(oldLine = line)
            else WorkspaceFileComparisonRow(newLine = line)
        }
        return WorkspaceFileComparison(
            rows = rows,
            oldContent = content.takeIf { kind == WorkspaceFileComparisonLineKind.DELETED },
            newContent = content.takeIf { kind == WorkspaceFileComparisonLineKind.ADDED },
            truncated = false,
        )
    }

    private fun comparisonFallbackNotice(
        comparison: WorkspaceFileComparison?,
        oldContent: PreviewReadResult? = null,
        newContent: PreviewReadResult? = null,
        diff: ComparisonDiffReadResult? = null,
    ): String? {
        if (comparison != null) return null
        if (oldContent?.content?.truncated == true || newContent?.content?.truncated == true) {
            return "文件较大，无法生成完整左右对比，已显示可读取的文件内容。"
        }
        if (diff?.reason != null) return "无法读取 Git 差异，已显示可读取的文件内容。"
        return if (oldContent?.content != null || newContent?.content != null) "无法生成完整左右对比，已显示可读取的文件内容。" else null
    }

    private fun parseComparisonHunks(output: String): List<ComparisonHunk> {
        val hunks = mutableListOf<ComparisonHunk>()
        var current: ComparisonHunkBuilder? = null
        output.lineSequence().forEach { raw ->
            val line = raw.removeSuffix("\r")
            val hunk = hunkHeader.matchEntire(line)
            if (hunk != null) {
                current?.let { hunks += it.build() }
                current = ComparisonHunkBuilder(
                    oldStart = hunk.groupValues[1].toInt(),
                    newStart = hunk.groupValues[2].toInt(),
                )
                return@forEach
            }
            val active = current ?: return@forEach
            if (line == "\\ No newline at end of file") return@forEach
            when {
                line.startsWith("-") -> active.operations += ComparisonDiffOperation.DELETED
                line.startsWith("+") -> active.operations += ComparisonDiffOperation.ADDED
            }
        }
        current?.let { hunks += it.build() }
        return hunks
    }

    private fun truncateText(text: String): Pair<String, Boolean> {
        if (text.length <= MAX_WORKSPACE_FILE_PREVIEW_BYTES) return text to false
        val lastCompleteLine = text.lastIndexOf('\n', MAX_WORKSPACE_FILE_PREVIEW_BYTES).takeIf { it > 0 }
            ?: MAX_WORKSPACE_FILE_PREVIEW_BYTES
        return text.substring(0, lastCompleteLine) to true
    }

    private fun previewLines(text: String): List<String> {
        if (text.isEmpty()) return emptyList()
        val lines = text.lineSequence().toList()
        return if (text.endsWith('\n') && lines.lastOrNull()?.isEmpty() == true) lines.dropLast(1) else lines
    }

    private data class Worktree(val path: Path, val realPath: Path)

    private sealed interface PreviewReadResult {
        data class Available(val value: WorkspaceFilePreviewContent) : PreviewReadResult
        data class Unavailable(val message: String) : PreviewReadResult

        val content: WorkspaceFilePreviewContent?
            get() = (this as? Available)?.value
        val reason: String?
            get() = (this as? Unavailable)?.message
    }

    private enum class ComparisonDiffOperation { DELETED, ADDED }

    private data class ComparisonHunk(
        val oldStart: Int,
        val newStart: Int,
        val operations: List<ComparisonDiffOperation>,
    )

    private data class ComparisonHunkBuilder(
        val oldStart: Int,
        val newStart: Int,
        val operations: MutableList<ComparisonDiffOperation> = mutableListOf(),
    ) {
        fun build(): ComparisonHunk = ComparisonHunk(oldStart, newStart, operations.toList())
    }

    private sealed interface ComparisonDiffReadResult {
        data class Available(val value: List<ComparisonHunk>) : ComparisonDiffReadResult
        data class Unavailable(val message: String) : ComparisonDiffReadResult
        data object Empty : ComparisonDiffReadResult

        val hunks: List<ComparisonHunk>?
            get() = when (this) {
                is Available -> value
                Empty -> emptyList()
                is Unavailable -> null
            }
        val reason: String?
            get() = (this as? Unavailable)?.message
    }

    private companion object {
        const val MAX_WORKSPACE_FILE_PREVIEW_BYTES = 512 * 1024
        val hunkHeader = Regex("^@@ -(\\d+)(?:,\\d+)? \\+(\\d+)(?:,\\d+)? @@.*$")
    }
}
