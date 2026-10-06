package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.serialization.Serializable

@Serializable
internal data class MaterialsDocumentTab(val path: String, val isPinned: Boolean = false)

/** Only relative paths and tab choices are persisted; document content stays in the active preview. */
@Serializable
internal data class MaterialsDocumentTabsSnapshot(
    val tabs: List<MaterialsDocumentTab> = emptyList(),
    val activePath: String? = null,
    val initialized: Boolean = false,
)

/** One replaceable preview tab, plus documents explicitly kept open for comparison. */
internal class MaterialsDocumentTabsState {
    var tabs by mutableStateOf<List<MaterialsDocumentTab>>(emptyList())
        private set
    var activePath by mutableStateOf<String?>(null)
        private set
    var isInitialized by mutableStateOf(false)
        private set

    val temporaryPath: String? get() = tabs.firstOrNull { !it.isPinned }?.path
    val pinnedPaths: List<String> get() = tabs.filter { it.isPinned }.map { it.path }

    /** A legacy selection seeds the first tab once; closing every tab remains an explicit empty choice. */
    fun initializeFromSelected(path: String?): String? {
        if (!isInitialized && path != null) openPreview(path)
        return activePath
    }

    fun openPreview(path: String): String? {
        val validPath = readingRelativePath(path) ?: return activePath
        isInitialized = true
        if (tabs.none { it.path == validPath }) {
            val previewIndex = tabs.indexOfFirst { !it.isPinned }
            tabs = if (previewIndex < 0) tabs + MaterialsDocumentTab(validPath) else {
                tabs.toMutableList().apply { this[previewIndex] = MaterialsDocumentTab(validPath) }
            }
        }
        activePath = validPath
        return activePath
    }

    fun pin(path: String? = activePath): String? {
        val validPath = path?.let(::readingRelativePath) ?: return activePath
        if (tabs.none { it.path == validPath }) openPreview(validPath)
        tabs = tabs.map { if (it.path == validPath) it.copy(isPinned = true) else it }
        return activePath
    }

    fun select(path: String): Boolean {
        val validPath = readingRelativePath(path) ?: return false
        if (tabs.none { it.path == validPath }) return false
        activePath = validPath
        return true
    }

    /** Prefer the next tab, then the previous tab, when the active document is closed. */
    fun close(path: String): String? {
        val validPath = readingRelativePath(path) ?: return activePath
        val previous = tabs
        tabs = previous.filterNot { it.path == validPath }
        repairActive(previous)
        return activePath
    }

    /** A catalog refresh removes deleted files without opening documents the user has already closed. */
    fun reconcile(existingPaths: Set<String>): String? {
        val validPaths = existingPaths.mapNotNull(::readingRelativePath).toSet()
        val previous = tabs
        tabs = previous.filter { it.path in validPaths }
        repairActive(previous)
        return activePath
    }

    /** Also handles a directory rename; collision repair retains a pinned choice for the destination. */
    fun rename(oldPath: String, newPath: String): String? {
        val old = readingRelativePath(oldPath) ?: return activePath
        val new = readingRelativePath(newPath) ?: return activePath
        if (old == new) return activePath
        fun renamed(path: String): String = when {
            path == old -> new
            path.startsWith("$old/") -> new + path.removePrefix(old)
            else -> path
        }
        val merged = linkedMapOf<String, MaterialsDocumentTab>()
        tabs.forEach { tab ->
            val path = renamed(tab.path)
            merged[path] = MaterialsDocumentTab(path, tab.isPinned || merged[path]?.isPinned == true)
        }
        tabs = merged.values.toList()
        activePath = activePath?.let(::renamed)?.takeIf { path -> tabs.any { it.path == path } }
            ?: tabs.lastOrNull()?.path
        return activePath
    }

    fun adjacentPath(path: String, offset: Int): String? {
        val index = tabs.indexOfFirst { it.path == path }
        if (index < 0 || tabs.isEmpty()) return null
        return tabs[(index + offset).coerceIn(0, tabs.lastIndex)].path
    }

    fun snapshot() = MaterialsDocumentTabsSnapshot(tabs, activePath, isInitialized).bounded()

    fun restore(saved: MaterialsDocumentTabsSnapshot) {
        val bounded = saved.bounded()
        tabs = bounded.tabs
        activePath = bounded.activePath
        isInitialized = bounded.initialized
    }

    fun reset() = restore(MaterialsDocumentTabsSnapshot())

    private fun repairActive(previous: List<MaterialsDocumentTab>) {
        if (tabs.any { it.path == activePath }) return
        val index = previous.indexOfFirst { it.path == activePath }
        activePath = previous.drop((index + 1).coerceAtLeast(0)).firstOrNull { old -> tabs.any { it.path == old.path } }?.path
            ?: previous.take(index.coerceAtLeast(0)).lastOrNull { old -> tabs.any { it.path == old.path } }?.path
            ?: tabs.firstOrNull()?.path
    }
}

/** Cache input is untrusted: normalize paths, deduplicate, bound size, and allow just one preview. */
internal fun MaterialsDocumentTabsSnapshot.bounded(): MaterialsDocumentTabsSnapshot {
    val merged = linkedMapOf<String, MaterialsDocumentTab>()
    tabs.forEach { tab -> readingRelativePath(tab.path)?.let { path ->
        merged[path] = MaterialsDocumentTab(path, tab.isPinned || merged[path]?.isPinned == true)
    } }
    val preview = merged.values.lastOrNull { !it.isPinned }?.path
    val valid = merged.values.filter { it.isPinned || it.path == preview }
    val requestedActive = activePath?.let(::readingRelativePath)?.takeIf { path -> valid.any { it.path == path } }
    val limited = valid.takeLast(MAX_READING_FILES_PER_TASK).toMutableList()
    requestedActive?.let { path ->
        if (limited.none { it.path == path }) {
            if (limited.size >= MAX_READING_FILES_PER_TASK) limited.removeAt(0)
            limited.add(0, valid.first { it.path == path })
        }
    }
    return copy(tabs = limited, activePath = requestedActive ?: limited.lastOrNull()?.path,
        initialized = initialized || limited.isNotEmpty())
}
