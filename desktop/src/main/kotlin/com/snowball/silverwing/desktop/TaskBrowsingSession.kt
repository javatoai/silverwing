package com.snowball.silverwing.desktop

import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.runtime.*

internal enum class TaskContentView(val label: String) {
    DETAIL("任务详情"), MATERIALS("任务资料"), REQUIREMENT("需求详情"), NOTES("需求说明")
}

/** 只保存浏览位置，不保存文件内容、PDF 页面或文件句柄；离开页面后仍能继续阅读。 */
internal class TaskBrowsingSession(
    preferredWidth: Float = WindowPreferences.load().taskListPaneWidth?.toFloat() ?: DEFAULT_TASK_LIST_PANE_WIDTH_DP,
) {
    var view by mutableStateOf(TaskContentView.DETAIL)
    private var preferredWidthState by mutableFloatStateOf(validPreferredWidth(preferredWidth))
    var preferredWidth: Float
        get() = preferredWidthState
        set(value) { preferredWidthState = validPreferredWidth(value) }
    private val materials = mutableStateMapOf<Pair<String, String?>, MaterialsBrowserState>()
    private val requirements = mutableStateMapOf<Pair<String, String>, MaterialsReadingState>()
    private val materialsRecency = linkedSetOf<Pair<String, String?>>()
    private val requirementsRecency = linkedSetOf<Pair<String, String>>()
    private val taskIndexes = mutableMapOf<Boolean, TaskIndexBrowsingState>()

    private fun validPreferredWidth(value: Float): Float =
        (value.takeIf(Float::isFinite) ?: DEFAULT_TASK_LIST_PANE_WIDTH_DP).coerceAtLeast(MIN_TASK_LIST_PANE_WIDTH_DP)

    fun taskIndexFor(archived: Boolean): TaskIndexBrowsingState =
        taskIndexes.getOrPut(archived) { TaskIndexBrowsingState() }

    fun materialsFor(taskPath: String, root: String?): MaterialsBrowserState {
        val taskKey = normalizedReadingPath(taskPath)
        val rootKey = root?.takeIf(String::isNotBlank)?.let(::normalizedReadingPath)
        // 资料关联改变后，旧根目录的选中路径和阅读位置不能带入新目录。
        materials.keys.removeAll { it.first == taskKey && it.second != rootKey }
        materialsRecency.retainAll(materials.keys)
        val key = taskKey to rootKey
        materialsRecency.remove(key); materialsRecency.add(key)
        val state = materials.getOrPut(key) { MaterialsBrowserState() }
        while (materials.size > MAX_READING_TASKS) { val oldest = materialsRecency.first(); materialsRecency.remove(oldest); materials.remove(oldest) }
        return state
    }

    fun requirementFor(taskPath: String, link: String): MaterialsReadingState {
        val taskKey = normalizedReadingPath(taskPath)
        val linkKey = link.trim()
        requirements.keys.removeAll { it.first == taskKey && it.second != linkKey }
        requirementsRecency.retainAll(requirements.keys)
        val key = taskKey to linkKey
        requirementsRecency.remove(key); requirementsRecency.add(key)
        val state = requirements.getOrPut(key) { MaterialsReadingState().apply { mode.value = MarkdownPreviewMode.RENDERED } }
        while (requirements.size > MAX_READING_TASKS) { val oldest = requirementsRecency.first(); requirementsRecency.remove(oldest); requirements.remove(oldest) }
        return state
    }

    fun snapshot(lastTaskPath: String? = null): ReadingSnapshot = ReadingSnapshot(
        lastTaskPath = lastTaskPath, view = view.name,
        materials = materialsRecency.mapNotNull { key -> materials[key]?.let { state -> MaterialsSnapshot(
            key.first, key.second, state.selectedPath.value, state.expandedDirectories.value.take(500),
            state.expansionInitialized.value, state.directoryCollapsed.value, state.compactShowingPreview.value,
            state.directoryPosition.snapshot(), state.fileSnapshots(), state.documentTabs.snapshot(), state.sortOrder.value.name) } },
        requirements = requirementsRecency.mapNotNull { key -> requirements[key]?.let { state -> RequirementReadingSnapshot(key.first, key.second, state.snapshot()) } },
    ).bounded()
    fun restore(saved: ReadingSnapshot) {
        val bounded = saved.bounded()
        materials.clear(); requirements.clear(); materialsRecency.clear(); requirementsRecency.clear()
        view = TaskContentView.entries.firstOrNull { it.name == bounded.view } ?: TaskContentView.DETAIL
        bounded.materials.forEach { record ->
            materialsFor(record.taskPath, record.root).apply {
                selectedPath.value = record.selectedPath
                expandedDirectories.value = record.expandedDirectories.take(500).toSet()
                expansionInitialized.value = record.expansionInitialized
                directoryCollapsed.value = record.directoryCollapsed
                sortOrder.value = MaterialsSortOrder.fromName(record.sortOrder)
                compactShowingPreview.value = record.compactShowingPreview
                directoryPosition.restore(record.directoryPosition)
                documentTabs.restore(record.documentTabs)
                record.files.entries.takeLastBounded(100).forEach { (path, value) -> readingFor(path).restore(value) }
            }
        }
        bounded.requirements.forEach { requirementFor(it.taskPath, it.link).restore(it.reading) }
    }
}

internal class TaskIndexBrowsingState {
    val expandedGroups = mutableStateMapOf<String, Boolean>()
    val position = MaterialsReadingState()
}

internal class MaterialsBrowserState {
    val sortOrder = mutableStateOf(MaterialsSortOrder.NAME)
    val selectedPath = mutableStateOf<String?>(null)
    val expandedDirectories = mutableStateOf<Set<String>>(emptySet())
    val expansionInitialized = mutableStateOf(false)
    val compactShowingPreview = mutableStateOf(false)
    val directoryCollapsed = mutableStateOf(false)
    val directoryPosition = MaterialsReadingState()
    val documentTabs = MaterialsDocumentTabsState()
    internal val files = mutableStateMapOf<String, MaterialsReadingState>()
    private val fileRecency = linkedSetOf<String>()

    fun readingFor(path: String): MaterialsReadingState {
        fileRecency.remove(path); fileRecency.add(path)
        val state = files.getOrPut(path) { MaterialsReadingState() }
        while (files.size > MAX_READING_FILES_PER_TASK) {
            val oldest = fileRecency.first { it != selectedPath.value && it != path }
            fileRecency.remove(oldest); files.remove(oldest)
        }
        return state
    }

    fun fileSnapshots(): Map<String, ReadingPositionSnapshot> = fileRecency.mapNotNull { path -> files[path]?.let { path to it.snapshot() } }.toMap()

    fun retainFiles(paths: Set<String>) {
        files.keys.retainAll(paths)
        fileRecency.retainAll(paths)
        val previous = selectedPath.value
        val nextTab = documentTabs.reconcile(paths)
        selectedPath.value = when {
            previous != null && previous in paths -> previous
            nextTab != null -> nextTab
            previous == null && documentTabs.isInitialized -> null
            else -> paths.firstOrNull()
        }
        selectedPath.value?.let { documentTabs.initializeFromSelected(it) }
        if (previous != null && previous !in paths && nextTab == null) {
            selectedPath.value?.let { documentTabs.openPreview(it) }
        }
    }

    fun renamePath(previous: String, replacement: String) {
        fun remap(path: String): String = when {
            path == previous -> replacement
            path.startsWith("$previous/") -> replacement + path.removePrefix(previous)
            else -> path
        }
        val moved = files.entries.associate { remap(it.key) to it.value }
        files.clear(); files.putAll(moved)
        val recent = fileRecency.map(::remap)
        fileRecency.clear(); fileRecency.addAll(recent)
        selectedPath.value = selectedPath.value?.let(::remap)
        expandedDirectories.value = expandedDirectories.value.map(::remap).toSet()
        documentTabs.rename(previous, replacement)
    }
}

internal data class MaterialsListPosition(val index: Int = 0, val offset: Int = 0)

internal class MaterialsReadingState {
    val mode = mutableStateOf(initialMarkdownPreviewMode())
    /** Editing is session UI state; Markdown content itself lives in MarkdownDraftStore. */
    val editing = mutableStateOf(false)
    val editorDirty = mutableStateOf(false)
    val editorBusy = mutableStateOf(false)
    val editorSaveRequest = mutableIntStateOf(0)
    val zoomPercent = mutableIntStateOf(100)
    val scrollPositions = mutableStateMapOf<String, Int>()
    val listPositions = mutableStateMapOf<String, MaterialsListPosition>()
}

internal val LocalMaterialsReadingState = staticCompositionLocalOf<MaterialsReadingState?> { null }

/** 使用轻量数值记录位置，隐藏的资料页不会因此保留整棵界面或渲染资源。 */
@Composable
internal fun rememberMaterialsScrollState(id: String, ready: Boolean = true): ScrollState {
    val saved = LocalMaterialsReadingState.current ?: return rememberScrollState()
    val scroll = remember(saved, id) { ScrollState(saved.scrollPositions[id] ?: 0) }
    val canRecord = remember(scroll) { mutableStateOf(false) }
    val currentReady by rememberUpdatedState(ready)
    LaunchedEffect(scroll, ready) {
        canRecord.value = false
        if (!ready) return@LaunchedEffect
        // 异步预览的空布局可能已把状态夹到 0，真正内容测量后再应用缓存。
        withFrameNanos { }
        scroll.scrollTo((saved.scrollPositions[id] ?: 0).coerceAtLeast(0))
        canRecord.value = true
        snapshotFlow { scroll.value }.collect { saved.scrollPositions[id] = it }
    }
    DisposableEffect(scroll) { onDispose { if (currentReady && canRecord.value) saved.scrollPositions[id] = scroll.value } }
    return scroll
}

@Composable
internal fun rememberMaterialsLazyListState(id: String, ready: Boolean = true): LazyListState {
    val saved = LocalMaterialsReadingState.current ?: return rememberLazyListState()
    val list = remember(saved, id) {
        val position = saved.listPositions[id] ?: MaterialsListPosition()
        LazyListState(position.index, position.offset)
    }
    val canRecord = remember(list) { mutableStateOf(false) }
    val currentReady by rememberUpdatedState(ready)
    LaunchedEffect(list, ready) {
        canRecord.value = false
        if (!ready) return@LaunchedEffect
        withFrameNanos { }
        val position = saved.listPositions[id] ?: MaterialsListPosition()
        val lastIndex = (list.layoutInfo.totalItemsCount - 1).coerceAtLeast(0)
        list.scrollToItem(position.index.coerceIn(0, lastIndex), position.offset.coerceAtLeast(0))
        canRecord.value = true
        snapshotFlow { MaterialsListPosition(list.firstVisibleItemIndex, list.firstVisibleItemScrollOffset) }
            .collect { saved.listPositions[id] = it }
    }
    DisposableEffect(list) {
        onDispose { if (currentReady && canRecord.value) saved.listPositions[id] = MaterialsListPosition(list.firstVisibleItemIndex, list.firstVisibleItemScrollOffset) }
    }
    return list
}

private fun <T> Collection<T>.takeLastBounded(count: Int): List<T> = toList().takeLast(count)
internal fun MaterialsReadingState.snapshot() = ReadingPositionSnapshot(mode.value.name, zoomPercent.intValue,
    scrollPositions.toMap(), listPositions.mapValues { (_, value) -> listOf(value.index, value.offset) }).bounded()
internal fun MaterialsReadingState.restore(saved: ReadingPositionSnapshot) {
    val bounded = saved.bounded()
    mode.value = MarkdownPreviewMode.entries.first { it.name == bounded.mode }
    zoomPercent.intValue = bounded.zoom
    scrollPositions.clear(); listPositions.clear()
    scrollPositions.putAll(bounded.scrolls)
    listPositions.putAll(bounded.lists.mapValues { (_, value) -> MaterialsListPosition(value[0], value[1]) })
}
