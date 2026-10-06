package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LocalSkillFileEntry
import java.util.Locale

/**
 * A presentation-only tree derived from the already validated, slash-separated Skill file paths.
 *
 * The catalog service remains responsible for filesystem traversal and path safety. Keeping this
 * conversion in the desktop layer lets the UI show folders without widening that read boundary.
 */
internal sealed interface LocalSkillFileTreeNode {
    data class Directory(
        val name: String,
        /** Empty only for the selected Skill's root directory. */
        val relativePath: String,
        val children: List<LocalSkillFileTreeNode>,
    ) : LocalSkillFileTreeNode

    data class File(val entry: LocalSkillFileEntry) : LocalSkillFileTreeNode
}

internal data class LocalSkillFileTreeRow(
    val node: LocalSkillFileTreeNode,
    val depth: Int,
    val expanded: Boolean = false,
) {
    val key: String = when (node) {
        is LocalSkillFileTreeNode.Directory -> "directory:${node.relativePath}"
        is LocalSkillFileTreeNode.File -> "file:${node.entry.relativePath}"
    }
}

/** Builds a deterministic directory tree for one installed Skill. */
internal fun buildLocalSkillFileTree(
    skillDirectoryName: String,
    files: List<LocalSkillFileEntry>,
    directoryPaths: List<String> = emptyList(),
): LocalSkillFileTreeNode.Directory {
    require(skillDirectoryName.isNotBlank()) { "Skill 目录名不能为空" }
    val root = MutableDirectory(skillDirectoryName, "")
    directoryPaths.forEach { relativePath ->
        val segments = relativePath.split('/')
        require(relativePath.isNotEmpty() && segments.all { it.isNotEmpty() && it != "." && it != ".." }) {
            "目录路径不安全：$relativePath"
        }
        var directory = root
        segments.forEach { segment ->
            val childPath = listOf(directory.relativePath, segment).filter(String::isNotEmpty).joinToString("/")
            directory = directory.directories.getOrPut(segment) { MutableDirectory(segment, childPath) }
        }
    }
    files.forEach { file ->
        val segments = file.relativePath.split('/')
        require(file.relativePath.isNotEmpty() && segments.all { it.isNotEmpty() && it != "." && it != ".." }) {
            "Skill 文件路径不安全：${file.relativePath}"
        }
        var directory = root
        segments.dropLast(1).forEach { segment ->
            val relativePath = listOf(directory.relativePath, segment)
                .filter(String::isNotEmpty)
                .joinToString("/")
            directory = directory.directories.getOrPut(segment) {
                MutableDirectory(segment, relativePath)
            }
        }
        check(directory.files.none { it.relativePath == file.relativePath }) {
            "Skill 文件路径重复：${file.relativePath}"
        }
        directory.files += file
    }
    return root.freeze(isRoot = true)
}

/**
 * Flattens only the visible portion of the tree for a LazyColumn.
 * The root is intentionally always expanded; all other folders are opt-in through [expandedDirectoryPaths].
 */
internal fun visibleLocalSkillFileTreeRows(
    root: LocalSkillFileTreeNode.Directory,
    expandedDirectoryPaths: Set<String>,
): List<LocalSkillFileTreeRow> = buildList {
    fun visit(directory: LocalSkillFileTreeNode.Directory, depth: Int) {
        val expanded = directory.relativePath.isEmpty() || directory.relativePath in expandedDirectoryPaths
        add(LocalSkillFileTreeRow(directory, depth, expanded))
        if (!expanded) return
        directory.children.forEach { child ->
            when (child) {
                is LocalSkillFileTreeNode.Directory -> visit(child, depth + 1)
                is LocalSkillFileTreeNode.File -> add(LocalSkillFileTreeRow(child, depth + 1))
            }
        }
    }

    visit(root, 0)
}

private class MutableDirectory(
    val name: String,
    val relativePath: String,
) {
    val directories = linkedMapOf<String, MutableDirectory>()
    val files = mutableListOf<LocalSkillFileEntry>()

    fun freeze(isRoot: Boolean): LocalSkillFileTreeNode.Directory {
        val sortedDirectories = directories.values
            .sortedBy { it.name.lowercase(Locale.ROOT) }
            .map { it.freeze(isRoot = false) }
        val sortedFiles = files.sortedWith(
            compareBy<LocalSkillFileEntry> { it.relativePath.substringAfterLast('/').lowercase(Locale.ROOT) }
                .thenBy(LocalSkillFileEntry::relativePath),
        )
        val rootSkillFile = sortedFiles.firstOrNull { isRoot && it.relativePath == "SKILL.md" }
        val otherFiles = if (rootSkillFile == null) sortedFiles else sortedFiles.filterNot { it == rootSkillFile }
        return LocalSkillFileTreeNode.Directory(
            name = name,
            relativePath = relativePath,
            children = buildList {
                rootSkillFile?.let { add(LocalSkillFileTreeNode.File(it)) }
                addAll(sortedDirectories)
                otherFiles.forEach { add(LocalSkillFileTreeNode.File(it)) }
            },
        )
    }
}
