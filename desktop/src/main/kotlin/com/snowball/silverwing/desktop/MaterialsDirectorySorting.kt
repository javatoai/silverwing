package com.snowball.silverwing.desktop

import java.util.Locale

internal enum class MaterialsSortOrder(val label: String) {
    NAME("按名称"), MODIFIED_DESC("最近修改");

    companion object {
        fun fromName(value: String): MaterialsSortOrder = entries.firstOrNull { it.name == value } ?: NAME
    }
}

/** Sort siblings only: keep folders first and preserve the existing directory hierarchy. */
internal fun sortMaterialsFileTree(
    tree: LocalSkillFileTreeNode.Directory,
    order: MaterialsSortOrder,
    catalog: RequirementMaterialsMarkdownCatalog?,
): LocalSkillFileTreeNode.Directory {
    val modifiedTimes = catalog?.files.orEmpty().associate { it.relativePath to it.modifiedAtMillis } +
        catalog?.directoryModifiedAtMillis.orEmpty()
    fun LocalSkillFileTreeNode.path(): String = when (this) {
        is LocalSkillFileTreeNode.Directory -> relativePath
        is LocalSkillFileTreeNode.File -> entry.relativePath
    }
    val comparator = compareBy<LocalSkillFileTreeNode> { it is LocalSkillFileTreeNode.File }
        .thenByDescending { if (order == MaterialsSortOrder.MODIFIED_DESC) modifiedTimes[it.path()] ?: 0L else 0L }
        .thenBy { it.path().substringAfterLast('/').lowercase(Locale.ROOT) }
        .thenBy { it.path() }
    fun sorted(directory: LocalSkillFileTreeNode.Directory): LocalSkillFileTreeNode.Directory = directory.copy(
        children = directory.children.map { if (it is LocalSkillFileTreeNode.Directory) sorted(it) else it }
            .sortedWith(comparator),
    )
    return sorted(tree)
}
