package com.snowball.silverwing.core

import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.attribute.BasicFileAttributes
import java.util.Locale

/** A read-only view of the Skills installed in the current user's standard Codex Skill directory. */
data class LocalSkillCatalog(
    val root: String,
    val skills: List<LocalSkillCatalogItem>,
)

/** One direct child of ~/.agents/skills that contains a SKILL.md file. */
data class LocalSkillCatalogItem(
    /** Directory name, used as a bounded identifier for subsequent preview reads. */
    val directoryName: String,
    /** Frontmatter name when present; otherwise the directory name. */
    val name: String,
    val description: String,
)

/** A regular file found below one installed Skill directory. */
data class LocalSkillFileEntry(
    /** Slash-separated path relative to the Skill directory. */
    val relativePath: String,
    val sizeBytes: Long,
    val markdown: Boolean,
)

data class LocalSkillFileCatalog(
    val directoryName: String,
    val files: List<LocalSkillFileEntry>,
    val truncated: Boolean = false,
)

/**
 * Reads only the user's local ~/.agents/skills directory. It never runs a script,
 * follows no symbolic links, and does not modify installed Skills.
 */
class LocalSkillCatalogService(
    private val userHome: () -> Path = { Path.of(System.getProperty("user.home")) },
) {
    fun list(): LocalSkillCatalog {
        val root = root()
        if (!Files.exists(root, NOFOLLOW_LINKS)) return LocalSkillCatalog(root.toString(), emptyList())
        require(Files.isDirectory(root, NOFOLLOW_LINKS) && !Files.isSymbolicLink(root)) {
            "本机 Skill 目录不是普通目录：$root"
        }
        val skills = Files.list(root).use { entries ->
            entries
                .filter(::isSkillDirectory)
                .map(::toCatalogItem)
                .sorted(compareBy<LocalSkillCatalogItem> { it.name.lowercase(Locale.ROOT) }.thenBy(LocalSkillCatalogItem::directoryName))
                .toList()
        }
        return LocalSkillCatalog(root.toString(), skills)
    }

    /** Lists regular files under one Skill, including references and scripts, without following links. */
    fun files(directoryName: String): LocalSkillFileCatalog {
        val directory = skillDirectory(directoryName)
        val files = mutableListOf<LocalSkillFileEntry>()
        var truncated = false
        Files.walkFileTree(directory, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(current: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (current != directory && (Files.isSymbolicLink(current) || current.fileName.toString() == ".git")) {
                    return FileVisitResult.SKIP_SUBTREE
                }
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                if (!attrs.isRegularFile || Files.isSymbolicLink(file)) return FileVisitResult.CONTINUE
                if (files.size >= MAX_SKILL_FILES) {
                    truncated = true
                    return FileVisitResult.TERMINATE
                }
                val relativePath = directory.relativize(file).joinToString("/") { it.toString() }
                files += LocalSkillFileEntry(
                    relativePath = relativePath,
                    sizeBytes = attrs.size(),
                    markdown = relativePath.endsWith(".md", ignoreCase = true),
                )
                return FileVisitResult.CONTINUE
            }

            override fun visitFileFailed(file: Path, exception: java.io.IOException): FileVisitResult = FileVisitResult.CONTINUE
        })
        return LocalSkillFileCatalog(
            directoryName = directoryName,
            files = files.sortedWith(
                compareBy<LocalSkillFileEntry> { it.relativePath != SKILL_FILE }
                    .thenBy { it.relativePath.lowercase(Locale.ROOT) },
            ),
            truncated = truncated,
        )
    }

    /** Reads one user-selected, text-based file under an already discovered local Skill. */
    fun preview(directoryName: String, relativePath: String): String {
        val file = skillFile(directoryName, relativePath)
        require(Files.size(file) <= MAX_SKILL_FILE_BYTES) { "文件过大，无法安全预览：$relativePath" }
        require(isTextFile(file)) { "该文件不是可安全预览的文本文件：$relativePath" }
        return Files.readString(file)
    }

    private fun root(): Path = userHome().resolve(".agents").resolve("skills").toAbsolutePath().normalize()

    private fun isSkillDirectory(directory: Path): Boolean =
        Files.isDirectory(directory, NOFOLLOW_LINKS) &&
            !Files.isSymbolicLink(directory) &&
            Files.isRegularFile(directory.resolve(SKILL_FILE), NOFOLLOW_LINKS)

    private fun toCatalogItem(directory: Path): LocalSkillCatalogItem {
        val directoryName = directory.fileName.toString()
        val skillFile = directory.resolve(SKILL_FILE)
        return runCatching {
            if (Files.size(skillFile) > MAX_SKILL_FILE_BYTES) {
                return@runCatching LocalSkillCatalogItem(directoryName, directoryName, "SKILL.md 超过 512 KB，无法预览")
            }
            val content = Files.readString(skillFile)
            LocalSkillCatalogItem(
                directoryName = directoryName,
                name = frontMatterValue(content, "name") ?: directoryName,
                description = frontMatterValue(content, "description") ?: "未提供说明",
            )
        }.getOrElse { error ->
            LocalSkillCatalogItem(directoryName, directoryName, "无法读取 SKILL.md：${error.message ?: "未知错误"}")
        }
    }

    private fun skillDirectory(directoryName: String): Path {
        val name = directoryName.trim()
        require(name.isNotBlank() && name != "." && name != ".." && '/' !in name && '\\' !in name) {
            "Skill 目录名不安全"
        }
        val root = root()
        val directory = root.resolve(name).normalize()
        require(directory.parent == root) { "Skill 路径不安全" }
        require(Files.isDirectory(directory, NOFOLLOW_LINKS) && !Files.isSymbolicLink(directory)) {
            "Skill 目录不存在或不是普通目录：$directory"
        }
        return directory
    }

    private fun skillFile(directoryName: String, relativePath: String): Path {
        val directory = skillDirectory(directoryName)
        val rawPath = relativePath.trim().replace('\\', '/')
        val segments = rawPath.split('/')
        require(rawPath.isNotBlank() && segments.all { it.isNotBlank() && it != "." && it != ".." }) {
            "Skill 文件路径不安全"
        }
        var file = directory
        segments.forEach { segment ->
            file = file.resolve(segment)
            require(!Files.isSymbolicLink(file)) { "Skill 文件路径不能包含符号链接：$relativePath" }
        }
        require(file.startsWith(directory) && Files.isRegularFile(file, NOFOLLOW_LINKS)) {
            "Skill 文件不存在或不是普通文件：$relativePath"
        }
        return file
    }

    private fun isTextFile(file: Path): Boolean {
        val sample = ByteArray(TEXT_SAMPLE_BYTES)
        val read = Files.newInputStream(file).use { input -> input.read(sample) }
        return sample.take(read.coerceAtLeast(0)).none { it == 0.toByte() }
    }

    private fun frontMatterValue(content: String, key: String): String? {
        val frontMatter = FRONT_MATTER.find(content)?.groupValues?.get(1) ?: return null
        return frontMatter.lineSequence()
            .firstOrNull { line -> line.substringBefore(':').trim() == key }
            ?.substringAfter(':', missingDelimiterValue = "")
            ?.trim()
            ?.removeSurrounding("\"")
            ?.removeSurrounding("'")
            ?.takeIf(String::isNotBlank)
    }

    private companion object {
        const val SKILL_FILE = "SKILL.md"
        const val MAX_SKILL_FILE_BYTES = 512 * 1024
        const val MAX_SKILL_FILES = 2_000
        const val TEXT_SAMPLE_BYTES = 8 * 1024
        val FRONT_MATTER = Regex("\\A---\\s*\\r?\\n(.*?)\\r?\\n---(?:\\r?\\n|\\z)", RegexOption.DOT_MATCHES_ALL)
    }
}
