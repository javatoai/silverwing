package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.ChevronLeft
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.automirrored.outlined.Sort
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.ManageSearch
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.snapshotFlow
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenuItem
import kotlinx.coroutines.launch
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.first
import androidx.compose.ui.input.pointer.isSecondaryPressed
import androidx.compose.ui.input.pointer.isPrimaryPressed
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.onPointerEvent
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.isCtrlPressed as isPointerCtrlPressed
import androidx.compose.ui.input.pointer.isShiftPressed as isPointerShiftPressed
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalViewConfiguration
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.LocalSkillFileEntry
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.runInterruptible
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.awt.Cursor
import kotlin.math.roundToInt

private sealed interface RequirementMaterialsFilesState {
    data object Loading : RequirementMaterialsFilesState
    data class Loaded(val catalog: RequirementMaterialsMarkdownCatalog) : RequirementMaterialsFilesState
    data class Failed(val message: String) : RequirementMaterialsFilesState
}

private data class MaterialsDeleteRequest(
    val root: Path,
    val relativePaths: List<String>,
    val context: Pair<String, String?>,
)

internal sealed interface RequirementMaterialsDocumentState {
    data object Empty : RequirementMaterialsDocumentState
    data object Loading : RequirementMaterialsDocumentState
    data class Loaded(val relativePath: String, val content: String) : RequirementMaterialsDocumentState
    data class Pdf(val relativePath: String, val refreshKey: Pair<Int, Int>) : RequirementMaterialsDocumentState
    data class External(val relativePath: String) : RequirementMaterialsDocumentState
    data class Failed(val relativePath: String, val message: String) : RequirementMaterialsDocumentState
}

private const val DEFAULT_MATERIALS_DIRECTORY_WIDTH_DP = 240f
private const val MIN_MATERIALS_DIRECTORY_WIDTH_DP = 180f
private const val MAX_MATERIALS_DIRECTORY_WIDTH_DP = 420f
private const val MIN_MATERIALS_PREVIEW_WIDTH_DP = 360f
private const val MATERIALS_RESIZE_HANDLE_WIDTH_DP = 8f
private const val MIN_MATERIALS_SPLIT_WIDTH_DP = MIN_MATERIALS_DIRECTORY_WIDTH_DP +
    MATERIALS_RESIZE_HANDLE_WIDTH_DP + MIN_MATERIALS_PREVIEW_WIDTH_DP

/** Clamp only the displayed width; a smaller window must not replace the saved preference. */
internal fun resolveMaterialsDirectoryWidth(preferredWidthDp: Float, availableWidthDp: Float): Float =
    preferredWidthDp.coerceIn(
        MIN_MATERIALS_DIRECTORY_WIDTH_DP,
        (availableWidthDp - MATERIALS_RESIZE_HANDLE_WIDTH_DP - MIN_MATERIALS_PREVIEW_WIDTH_DP)
            .coerceIn(MIN_MATERIALS_DIRECTORY_WIDTH_DP, MAX_MATERIALS_DIRECTORY_WIDTH_DP),
    )

internal fun selectMaterialEntryKeys(
    selected: Set<String>,
    visibleKeys: List<String>,
    anchor: String?,
    key: String,
    ctrl: Boolean,
    shift: Boolean,
): Set<String> = when {
    shift && anchor in visibleKeys -> {
        val from = visibleKeys.indexOf(anchor)
        val to = visibleKeys.indexOf(key)
        val range = visibleKeys.subList(minOf(from, to), maxOf(from, to) + 1).toSet()
        if (ctrl) selected + range else range
    }
    ctrl -> if (key in selected) selected - key else selected + key
    else -> setOf(key)
}

internal fun initialExpandedMaterialsDirectories(directories: List<String>, enabled: Boolean): Set<String> =
    if (enabled) directories.filter { path -> path.count { it == '/' } < 2 }.toSet() else emptySet()

/** Local reading and file actions stay within the task's persisted requirement-materials root. */
@Composable
internal fun RequirementMaterialsBrowser(
    controller: DesktopApplication,
    task: TaskManifest,
    modifier: Modifier = Modifier,
    browserState: MaterialsBrowserState = remember(task.folderName, task.requirementMaterials.writeRoot) { MaterialsBrowserState() },
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    onCopyFiles: (List<Path>) -> Unit = { controller.copyFiles(it) },
    onCopyPaths: (List<Path>) -> Unit = { controller.copyText(it.joinToString("\n"), "路径已复制") },
    imageClipboard: MaterialsImageClipboard = SystemMaterialsImageClipboard,
    importPicker: MaterialsImportFilePicker = FileKitMaterialsImportFilePicker,
) {
    if (LocalDocumentFind.current == null) {
        val find = remember(browserState, task.folderName, task.requirementMaterials.writeRoot, browserState.selectedPath.value) { DocumentFindState() }
        DocumentFindScope(find, modifier) { RequirementMaterialsBrowser(controller, task, Modifier.fillMaxSize(), browserState, ioDispatcher, onCopyFiles, onCopyPaths,
            imageClipboard, importPicker) }
        return
    }
    val rootValue = task.requirementMaterials.writeRoot
    val browserContext = task.folderName to rootValue
    // Some task fixtures (and older imported tasks) do not have a configured
    // workspace path.  The task folder still gives drafts a stable identity;
    // a configured path is used whenever it is available.
    val taskIdentityPath = remember(task.folderName, rootValue) {
        runCatching { controller.taskPath(task) }.getOrElse { task.folderName }
    }
    val rootPath = remember(browserState, rootValue) {
        rootValue?.takeIf(String::isNotBlank)?.let { runCatching { Path.of(it) }.getOrNull() }
    }
    val service = remember { RequirementMaterialsMarkdownService() }
    var refreshAttempt by remember(browserState, browserContext) { mutableStateOf(0) }
    val selectionActionIdentity = remember(browserState, browserContext, refreshAttempt) { Any() }
    val currentSelectionActionIdentity by rememberUpdatedState(selectionActionIdentity)
    var resolvingSelection by remember(selectionActionIdentity) { mutableStateOf(false) }
    var selectionActionJob by remember(selectionActionIdentity) { mutableStateOf<Job?>(null) }
    androidx.compose.runtime.DisposableEffect(selectionActionIdentity) {
        onDispose { selectionActionJob?.cancel() }
    }
    var previewAttempt by remember(browserState, browserContext) { mutableStateOf(0) }
    var filesState by remember(browserState, browserContext) { mutableStateOf<RequirementMaterialsFilesState>(RequirementMaterialsFilesState.Loading) }
    var documentState by remember(browserState, browserContext) { mutableStateOf<RequirementMaterialsDocumentState>(RequirementMaterialsDocumentState.Empty) }
    var selectedRelativePath by browserState.selectedPath
    val documentTabs = browserState.documentTabs
    MaterialsLiveRefreshEffect(rootPath, selectedRelativePath, ioDispatcher) { change ->
        if (change.catalogChanged) refreshAttempt += 1
        else if (change.selectedFileChanged) previewAttempt += 1
    }
    var pendingAnchor by remember(browserState, browserContext) { mutableStateOf<String?>(null) }
    var selectedEntryKeys by remember(browserState, browserContext) { mutableStateOf<Set<String>>(emptySet()) }
    var selectionAnchor by remember(browserState, browserContext) { mutableStateOf<String?>(null) }
    var selectionInitialized by remember(browserState, browserContext) { mutableStateOf(false) }
    var expandedDirectories by browserState.expandedDirectories
    var expansionInitialized by browserState.expansionInitialized
    var compactShowingPreview by browserState.compactShowingPreview
    var directoryCollapsed by browserState.directoryCollapsed
    var sortOrder by browserState.sortOrder
    val actionScope = rememberCoroutineScope()
    val markdownStoreBusy by controller.markdownDraftStore.busy.collectAsState()
    var deleting by remember(browserState, browserContext) { mutableStateOf(false) }
    var deleteSelection by remember(browserState, browserContext) { mutableStateOf<MaterialsDeleteRequest?>(null) }
    val preferredDirectoryWidth = remember {
        mutableFloatStateOf(
            WindowPreferences.load().materialsDirectoryPaneWidth?.toFloat() ?: DEFAULT_MATERIALS_DIRECTORY_WIDTH_DP,
        )
    }

    LaunchedEffect(browserState, browserContext, refreshAttempt) {
        filesState = RequirementMaterialsFilesState.Loading
        val root = rootPath
        if (root == null) {
            filesState = RequirementMaterialsFilesState.Failed("任务资料目录路径无效或为空。")
        } else {
            try {
                val catalog = runInterruptible(ioDispatcher) { service.list(root) }
                filesState = RequirementMaterialsFilesState.Loaded(catalog)
                if (!expansionInitialized) {
                    expandedDirectories = initialExpandedMaterialsDirectories(
                        catalog.directories,
                        WindowPreferences.load().expandRequirementMaterialsSecondLevelFolders,
                    )
                    expansionInitialized = true
                }
                val previousPath = selectedRelativePath
                browserState.retainFiles(catalog.files.map { it.relativePath }.toSet())
                expandedDirectories = expandedDirectories.intersect(catalog.directories.toSet())
                val validKeys = catalog.files.map { "file:${it.relativePath}" }.toSet() +
                    catalog.directories.map { "directory:$it" }
                val previousEntryKeys = selectedEntryKeys
                selectedEntryKeys = previousEntryKeys.intersect(validKeys)
                selectionAnchor = selectionAnchor?.takeIf { it in validKeys }
                val selectedPath = selectedRelativePath
                if (selectedEntryKeys.isEmpty() && selectedPath != null &&
                    (!selectionInitialized || previousEntryKeys.isNotEmpty())) {
                    selectedEntryKeys = setOf("file:$selectedPath")
                    selectionAnchor = "file:$selectedPath"
                }
                selectionInitialized = true
                // 自动选择文件或删除后换到下一个文件时，让它的所在目录可见。
                // 恢复同一文件时保留用户主动收起的目录，不强制重新展开。
                if (previousPath != selectedPath && selectedPath != null) {
                    var ancestor = selectedPath.substringBeforeLast('/', "")
                    while (ancestor.isNotEmpty()) {
                        expandedDirectories = expandedDirectories + ancestor
                        ancestor = ancestor.substringBeforeLast('/', "")
                    }
                }
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                filesState = RequirementMaterialsFilesState.Failed(
                    error.message ?: "无法读取任务资料目录。",
                )
            }
        }
    }

    val catalog = (filesState as? RequirementMaterialsFilesState.Loaded)?.catalog
    val selectedFile = catalog?.files?.firstOrNull { it.relativePath == selectedRelativePath }
    val entryPath = selectionAnchor?.substringAfter(':')
    val currentDirectory = if (entryPath in catalog?.directories.orEmpty()) entryPath.orEmpty()
        else (entryPath ?: selectedRelativePath).orEmpty().substringBeforeLast('/', "")
    val fileManagement = rootPath?.let { root -> rememberMaterialsFileManagement(root, currentDirectory,
        onCompleted = { result ->
            result.previousRelativePath?.let { old ->
                browserState.renamePath(old, result.relativePath)
                rootPath?.let { materialsRoot ->
                    controller.markdownDraftStore.rename(
                        taskPath = taskIdentityPath,
                        materialsRoot = materialsRoot.toAbsolutePath().normalize().toString(),
                        previous = old,
                        replacement = result.relativePath,
                    )
                }
                fun remapKey(key: String): String {
                    val path = key.substringAfter(':')
                    val mapped = if (path == old || path.startsWith("$old/")) result.relativePath + path.removePrefix(old) else path
                    return key.substringBefore(':') + ":" + mapped
                }
                selectedEntryKeys = selectedEntryKeys.map(::remapKey).toSet()
                selectionAnchor = selectionAnchor?.let(::remapKey)
            }
            if (result.isDirectory) {
                expandedDirectories = expandedDirectories + result.relativePath
                selectedEntryKeys = setOf("directory:${result.relativePath}")
                selectionAnchor = "directory:${result.relativePath}"
            } else {
                selectedRelativePath = result.relativePath
                documentTabs.openPreview(result.relativePath)
                if (result.previousRelativePath == null && result.relativePath.substringAfterLast('/').endsWith(".md", ignoreCase = true)) {
                    browserState.readingFor(result.relativePath).editing.value = true
                }
                selectedEntryKeys = setOf("file:${result.relativePath}")
                selectionAnchor = "file:${result.relativePath}"
                var parent = result.relativePath.substringBeforeLast('/', "")
                while (parent.isNotEmpty()) { expandedDirectories = expandedDirectories + parent; parent = parent.substringBeforeLast('/', "") }
            }
            refreshAttempt += 1
            controller.showStatus(if (result.previousRelativePath == null) "资料已添加" else "名称已更新")
        }, onError = { controller.showError(IllegalStateException(it)) }, ioDispatcher = ioDispatcher,
        clipboard = imageClipboard, contextKey = browserContext, picker = importPicker) }
    fileManagement?.let { MaterialsFileManagementDialogs(it) }
    LaunchedEffect(browserState, rootPath, selectedFile?.relativePath, refreshAttempt, previewAttempt, catalog) {
        val selected = selectedFile
        val root = catalog?.root
        if (selected == null || root == null) {
            documentState = RequirementMaterialsDocumentState.Empty
        } else if (selected.pdf) {
            // PDF 交给独立的后台页面渲染，不经由 UTF-8 文本预览入口。
            documentState = RequirementMaterialsDocumentState.Pdf(selected.relativePath, refreshAttempt to previewAttempt)
        } else if (!selected.textPreview) {
            documentState = RequirementMaterialsDocumentState.External(selected.relativePath)
        } else {
            documentState = RequirementMaterialsDocumentState.Loading
            try {
                val content = runInterruptible(ioDispatcher) { service.read(root, selected.relativePath) }
                documentState = RequirementMaterialsDocumentState.Loaded(selected.relativePath, content)
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                documentState = RequirementMaterialsDocumentState.Failed(
                    selected.relativePath,
                    error.message ?: "无法读取文件。",
                )
            }
        }
    }

    val treeEntries = remember(catalog?.files) {
        catalog?.files.orEmpty().map { file ->
            LocalSkillFileEntry(
                relativePath = file.relativePath,
                sizeBytes = file.sizeBytes,
                markdown = file.markdown,
            )
        }
    }
    val fileTree = remember(treeEntries, catalog, sortOrder) {
        sortMaterialsFileTree(buildLocalSkillFileTree("任务资料", treeEntries, catalog?.directories.orEmpty()), sortOrder, catalog)
    }
    val visibleRows = remember(fileTree, expandedDirectories) {
        visibleLocalSkillFileTreeRows(fileTree, expandedDirectories).drop(1)
    }

    fun navigateMarkdown(relativePath: String, anchor: String?) {
        val current = catalog ?: return
        val file = current.files.firstOrNull { it.relativePath == relativePath && it.markdown } ?: return
        selectedRelativePath = file.relativePath
        documentTabs.openPreview(file.relativePath)
        selectedEntryKeys = setOf("file:${file.relativePath}")
        selectionAnchor = "file:${file.relativePath}"
        var ancestor = file.relativePath.substringBeforeLast('/', "")
        while (ancestor.isNotEmpty()) {
            expandedDirectories = expandedDirectories + ancestor
            ancestor = ancestor.substringBeforeLast('/', "")
        }
        pendingAnchor = anchor
    }

    fun navigateImage(relativePath: String) {
        val current = catalog ?: return
        val file = current.files.firstOrNull { it.relativePath == relativePath && it.image } ?: return
        selectedRelativePath = file.relativePath
        // Image previews are navigable even when they have never been pinned
        // into the document tab strip.  Opening first also makes the active
        // tab state consistent with the selected file.
        documentTabs.openPreview(file.relativePath)
        selectedEntryKeys = setOf("file:${file.relativePath}")
        selectionAnchor = "file:${file.relativePath}"
        var ancestor = file.relativePath.substringBeforeLast('/', "")
        while (ancestor.isNotEmpty()) {
            expandedDirectories = expandedDirectories + ancestor
            ancestor = ancestor.substringBeforeLast('/', "")
        }
        compactShowingPreview = true
    }

    fun selectEntry(row: LocalSkillFileTreeRow, ctrl: Boolean, shift: Boolean, compact: Boolean) {
        val key = row.key
        val visibleKeys = visibleRows.map(LocalSkillFileTreeRow::key)
        selectedEntryKeys = selectMaterialEntryKeys(selectedEntryKeys, visibleKeys, selectionAnchor, key, ctrl, shift)
        if (!shift) selectionAnchor = key
        val file = (row.node as? LocalSkillFileTreeNode.File)?.entry
        if (file != null) {
            pendingAnchor = null
            selectedRelativePath = file.relativePath
            documentTabs.openPreview(file.relativePath)
            if (compact) compactShowingPreview = true
        }
    }

    fun resolveSelectedEntries(action: (Path, List<Path>) -> Unit) {
        if (resolvingSelection || deleting || selectedEntryKeys.isEmpty() || markdownStoreBusy) return
        val current = catalog ?: return
        val keys = selectedEntryKeys.sorted()
        val identity = selectionActionIdentity
        resolvingSelection = true
        selectionActionJob = actionScope.launch {
            try {
                // Path validation can touch a slow disk; never perform it in a pointer/key handler.
                // The captured selection and root remain the target while the user keeps browsing.
                val paths = runInterruptible(ioDispatcher) {
                    keys.map { service.resolveCopyItem(current.root, it.substringAfter(':')) }
                }
                currentCoroutineContext().ensureActive()
                if (currentSelectionActionIdentity === identity) action(current.root, paths)
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                if (currentSelectionActionIdentity === identity) controller.showError(error)
            } finally {
                resolvingSelection = false
            }
        }
    }
    fun copySelectedEntries() {
        resolveSelectedEntries { _, paths -> onCopyFiles(distinctMaterialTargets(paths)) }
    }
    fun copyPaths() {
        resolveSelectedEntries { _, paths -> onCopyPaths(paths) }
    }
    fun requestDelete() {
        resolveSelectedEntries { root, paths ->
            val relativePaths = distinctMaterialTargets(paths).map { root.relativize(it).toString() }
            deleteSelection = MaterialsDeleteRequest(root, relativePaths, browserContext)
        }
    }
    deleteSelection?.let { request ->
        val paths = request.relativePaths
        AlertDialog(onDismissRequest = { if (!deleting) deleteSelection = null },
            title = { Text("移入回收站？") },
            text = { Text("所选 ${paths.size} 项将移入系统回收站，文件夹包含内部内容。\n" + paths.take(8).joinToString("\n")) },
            confirmButton = { TextButton(enabled = !deleting && catalog?.root == request.root, onClick = {
                if (request.context != browserContext || catalog?.root != request.root) {
                    deleteSelection = null
                    return@TextButton
                }
                deleting = true
                actionScope.launch {
                    try {
                        val checked = runInterruptible(ioDispatcher) { service.resolveDeleteItems(request.root, paths) }
                        val results = controller.systemFileTrash.moveToTrash(checked)
                        val feedback = materialsTrashFeedback(results)
                        if (feedback.error != null) controller.showError(IllegalStateException(feedback.error))
                        else feedback.status?.let(controller::showStatus)
                        refreshAttempt += 1
                    } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
                    catch (error: Exception) { controller.showError(error); refreshAttempt += 1 }
                    finally { deleting = false; deleteSelection = null }
                }
            }) { Text(if (deleting) "正在删除…" else "移入回收站") } },
            dismissButton = { TextButton(enabled = !deleting, onClick = { deleteSelection = null }) { Text("取消") } })
    }
    var pickerOpen by remember(browserState, browserContext) { mutableStateOf(false) }
    var searchOpen by remember(browserState, browserContext) { mutableStateOf(false) }
    var contextCopyPaths by remember(browserState, browserContext) { mutableStateOf<List<String>?>(null) }
    var pendingSearch by remember(browserState, browserContext) { mutableStateOf<MaterialsFullTextSearchHit?>(null) }
    val find = LocalDocumentFind.current
    androidx.compose.runtime.DisposableEffect(find) {
        find?.quickOpen = { pickerOpen = true }
        find?.fullTextSearch = { searchOpen = true }
        onDispose { find?.quickOpen = null; find?.fullTextSearch = null }
    }
    LaunchedEffect(pendingSearch, find, documentState) {
        val target = pendingSearch ?: return@LaunchedEffect
        if (find == null || selectedRelativePath != target.relativePath) return@LaunchedEffect
        val source = (documentState as? RequirementMaterialsDocumentState.Loaded)?.takeIf { it.relativePath == target.relativePath }
        val pdf = (documentState as? RequirementMaterialsDocumentState.Pdf)?.takeIf { it.relativePath == target.relativePath }
        if (source == null && pdf == null) return@LaunchedEffect
        find.change(target.query)
        find.show()
        if (source?.content?.isBlank() == true) { pendingSearch = null; return@LaunchedEffect }
        val hits = snapshotFlow { find.blocks.toMap() to find.hits }.first { (blocks, _) ->
            if (source != null) blocks.values.any { it.text == source.content }
            else blocks.containsKey("pdf:${target.pageIndex}")
        }.second
        val index = if (target.pageIndex == null) hits.indexOfFirst { hit -> find.blocks[hit.block]?.text == source?.content && hit.range.first == target.offset }
            .takeIf { it >= 0 } ?: target.occurrenceIndex.coerceAtMost(hits.lastIndex).coerceAtLeast(0)
            else hits.indexOfFirst { it.block == "pdf:${target.pageIndex}" && it.range.first == target.offset }
                .takeIf { it >= 0 } ?: hits.indexOfFirst { it.block == "pdf:${target.pageIndex}" }.coerceAtLeast(0)
        find.activeIndex = index
        find.navigation += 1
        pendingSearch = null
    }
    fun activateTab(path: String?) {
        selectedRelativePath = path
        selectedEntryKeys = path?.let { setOf("file:$it") }.orEmpty()
        selectionAnchor = path?.let { "file:$it" }
        pendingAnchor = null
        var parent = path?.substringBeforeLast('/', "").orEmpty()
        while (parent.isNotEmpty()) { expandedDirectories = expandedDirectories + parent; parent = parent.substringBeforeLast('/', "") }
        if (path != null) compactShowingPreview = true
    }
    if (searchOpen && rootPath != null) MaterialsFullTextSearchDialog(rootPath,
        onDismiss = { searchOpen = false }, onNavigate = { hit ->
            documentTabs.openPreview(hit.relativePath)
            activateTab(hit.relativePath)
            if (hit.pageIndex == null) browserState.readingFor(hit.relativePath).mode.value = MarkdownPreviewMode.SOURCE
            else browserState.readingFor(hit.relativePath).listPositions["pdf-pages"] = MaterialsListPosition(hit.pageIndex, 0)
            pendingSearch = hit
            searchOpen = false
        }, ioDispatcher = ioDispatcher)
    contextCopyPaths?.let { paths -> AiContextCopyDialog(task, onDismiss = { contextCopyPaths = null },
        requirementTitle = controller.requirementController.loadedMetadataFor(task)?.title,
        materialsRoot = rootPath, selectedMaterialPaths = paths,
        currentBranches = task.services.mapNotNull { workspace -> controller.gitHealth(workspace)?.actualBranch?.let { workspace.worktreePath to it } }.toMap(),
        loadRequirementBody = { controller.loadTaskRequirementBodyForCopy(task) }, ioDispatcher = ioDispatcher,
        onCopied = { controller.showStatus("AI 上下文已复制") }) }
    if (pickerOpen) MaterialsQuickPicker(catalog?.files.orEmpty(), { pickerOpen = false }) { file ->
        selectedRelativePath = file.relativePath
        documentTabs.openPreview(file.relativePath)
        selectedEntryKeys = setOf("file:${file.relativePath}")
        selectionAnchor = "file:${file.relativePath}"
        pendingAnchor = null
        var parent = file.relativePath.substringBeforeLast('/', "")
        while (parent.isNotEmpty()) { expandedDirectories = expandedDirectories + parent; parent = parent.substringBeforeLast('/', "") }
        compactShowingPreview = true
        pickerOpen = false
    }
    CompositionLocalProvider(LocalMaterialsHeaderControls provides MaterialsHeaderControls(
        if (directoryCollapsed || compactShowingPreview) ({ directoryCollapsed = false; compactShowingPreview = false }) else null,
        { pickerOpen = true }, { refreshAttempt += 1 }, filesState is RequirementMaterialsFilesState.Loading,
        search = { searchOpen = true }, copyContext = {
            contextCopyPaths = selectedEntryKeys.map { it.substringAfter(':') }.ifEmpty { listOfNotNull(selectedRelativePath) }
        }, createFolder = fileManagement?.let { { it.createFolder() } }, createMarkdown = fileManagement?.let { { it.createMarkdown() } },
        pasteImage = fileManagement?.let { { it.pasteImage() } }, importFiles = fileManagement?.let { { it.chooseImportFiles() } },
        managingFiles = fileManagement?.acceptsInput == false)) {
    BoxWithConstraints(modifier.fillMaxSize().then(fileManagement?.let { Modifier.materialsFileDropTarget(it) } ?: Modifier)
        .then(if (fileManagement?.dragActive == true) Modifier.border(1.dp, MaterialTheme.colorScheme.primary) else Modifier)
        .onPreviewKeyEvent { event ->
        if (event.type != KeyEventType.KeyDown || !event.isCtrlPressed) false else when {
            event.isShiftPressed && event.key == Key.F -> { searchOpen = true; true }
            event.key == Key.P -> { pickerOpen = true; true }
            event.key == Key.W && selectedRelativePath != null -> { activateTab(documentTabs.close(selectedRelativePath!!)); true }
            else -> false
        }
    }) {
        val availableWidth = maxWidth.value
        val compact = availableWidth < MIN_MATERIALS_SPLIT_WIDTH_DP
        val displayedDirectoryWidth = resolveMaterialsDirectoryWidth(
            preferredDirectoryWidth.floatValue,
            availableWidth,
        ).dp

        if (directoryCollapsed || (compact && compactShowingPreview && selectedFile != null)) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                when (val listing = filesState) {
                    RequirementMaterialsFilesState.Loading -> {
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                        Text("正在读取目录…", style = MaterialTheme.typography.bodySmall)
                    }
                    is RequirementMaterialsFilesState.Failed -> {
                        Text(listing.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                        TextButton(onClick = { refreshAttempt += 1 }) { Text("重试") }
                    }
                    is RequirementMaterialsFilesState.Loaded -> if (treeEntries.isEmpty()) Text("目录为空", style = MaterialTheme.typography.bodySmall)
                }
                RequirementMaterialsDocumentPane(
                    controller = controller,
                    taskPath = taskIdentityPath,
                    root = catalog?.root ?: rootPath ?: Path.of("."),
                    selectedFile = selectedFile,
                    state = documentState,
                    onRetry = { previewAttempt += 1 },
                    onNavigateMarkdown = ::navigateMarkdown,
                    onNavigateImage = ::navigateImage,
                    pendingAnchor = pendingAnchor,
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                    refreshKey = refreshAttempt to previewAttempt,
                    readingState = selectedFile?.let { browserState.readingFor(it.relativePath) },
                    ioDispatcher = ioDispatcher,
                    documentTabs = documentTabs,
                    imagePaths = catalog?.files.orEmpty().filter { it.image }.map { it.relativePath },
                    onSelectTab = { path -> if (documentTabs.select(path)) activateTab(path) },
                    onCloseTab = { path -> val active = documentTabs.activePath; val next = documentTabs.close(path); if (active == path) activateTab(next) },
                )
            }
        } else {
            val directoryModifier = if (compact) Modifier.fillMaxSize() else Modifier.width(displayedDirectoryWidth).fillMaxHeight()
            Row(Modifier.fillMaxSize()) {
                RequirementMaterialsDirectoryPane(
                    state = filesState,
                    files = treeEntries,
                    rows = visibleRows,
                    expandedDirectories = expandedDirectories,
                    selectedEntryKeys = selectedEntryKeys,
                    onToggleDirectory = { path ->
                        expandedDirectories = if (path in expandedDirectories) expandedDirectories - path else expandedDirectories + path
                    },
                    onSelectEntry = { row, ctrl, shift -> selectEntry(row, ctrl, shift, compact) },
                    onPinEntry = { row -> (row.node as? LocalSkillFileTreeNode.File)?.entry?.let { documentTabs.pin(it.relativePath) } },
                    onCopySelected = ::copySelectedEntries,
                    onCopyPaths = ::copyPaths,
                    onContextSelect = { row -> if (row.key !in selectedEntryKeys) selectedEntryKeys = setOf(row.key); selectionAnchor = row.key },
                    onDeleteSelected = ::requestDelete,
                    onRenameEntry = { row -> if (!markdownStoreBusy) fileManagement?.rename(row.key.substringAfter(':')) },
                    onPaste = { event -> fileManagement?.onPasteKeyEvent(event) == true },
                    actionsEnabled = !deleting && !resolvingSelection && fileManagement?.busy != true && !markdownStoreBusy,
                    actionsBusy = resolvingSelection,
                    onCollapse = { directoryCollapsed = true },
                    onRefresh = { refreshAttempt += 1 },
                    sortOrder = sortOrder,
                    onSortOrderChange = { sortOrder = it },
                    readingState = browserState.directoryPosition,
                    modifier = directoryModifier,
                )
                if (!compact) {
                    MaterialsDirectoryResizeHandle(
                        onResizeStart = { preferredDirectoryWidth.floatValue = displayedDirectoryWidth.value },
                        onResize = { dragAmount ->
                            preferredDirectoryWidth.floatValue = resolveMaterialsDirectoryWidth(
                                preferredDirectoryWidth.floatValue + dragAmount,
                                availableWidth,
                            )
                        },
                        onResizeEnd = {
                            WindowPreferences.saveMaterialsDirectoryPaneWidth(preferredDirectoryWidth.floatValue.roundToInt())
                        },
                    )
                    when (val current = filesState) {
                        RequirementMaterialsFilesState.Loading -> BrowserLoadingCard(Modifier.weight(1f).fillMaxHeight())
                        is RequirementMaterialsFilesState.Failed -> BrowserErrorCard(
                            message = current.message,
                            onRetry = { refreshAttempt += 1 },
                            modifier = Modifier.weight(1f).fillMaxHeight(),
                        )
                        is RequirementMaterialsFilesState.Loaded -> {
                            if (current.catalog.files.isEmpty()) {
                                BrowserEmptyCard(
                                    title = "目录中没有文件",
                                    detail = "将资料文件放入该目录后点击刷新，文件夹仍可在左侧选择并复制。",
                                    modifier = Modifier.weight(1f).fillMaxHeight(),
                                )
                            } else {
                                RequirementMaterialsDocumentPane(
                                    controller = controller,
                                    taskPath = taskIdentityPath,
                                    root = current.catalog.root,
                                    selectedFile = selectedFile,
                                    state = documentState,
                                    onRetry = { previewAttempt += 1 },
                                    onNavigateMarkdown = ::navigateMarkdown,
                                    onNavigateImage = ::navigateImage,
                                    pendingAnchor = pendingAnchor,
                                    modifier = Modifier.weight(1f).fillMaxHeight(),
                                    refreshKey = refreshAttempt to previewAttempt,
                                    readingState = selectedFile?.let { browserState.readingFor(it.relativePath) },
                                    ioDispatcher = ioDispatcher,
                                    documentTabs = documentTabs,
                                    imagePaths = current.catalog.files.filter { it.image }.map { it.relativePath },
                                    onSelectTab = { path -> if (documentTabs.select(path)) activateTab(path) },
                                    onCloseTab = { path -> val active = documentTabs.activePath; val next = documentTabs.close(path); if (active == path) activateTab(next) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
}

internal data class MaterialsTrashFeedback(val status: String? = null, val error: String? = null)

/** 取消不是成功或失败；部分结果只按系统明确返回的状态计数。 */
internal fun materialsTrashFeedback(results: List<FileTrashResult>): MaterialsTrashFeedback {
    val successful = results.count { !it.cancelled && it.error == null }
    val cancelled = results.count { it.cancelled }
    val failures = results.filter { !it.cancelled && it.error != null }
    val successText = "$successful 项已移入回收站"
    val cancelText = if (cancelled > 0) "；$cancelled 项已取消" else ""
    return when {
        failures.isNotEmpty() -> MaterialsTrashFeedback(error = successText + cancelText + "；" + failures.take(3)
            .joinToString("；") { "${it.path.fileName}：${it.error}" })
        successful > 0 -> MaterialsTrashFeedback(status = successText + cancelText)
        cancelled > 0 -> MaterialsTrashFeedback(status = "已取消移入回收站")
        else -> MaterialsTrashFeedback()
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun MaterialsDirectoryResizeHandle(
    onResizeStart: () -> Unit,
    onResize: (Float) -> Unit,
    onResizeEnd: () -> Unit,
) {
    val currentOnResizeStart by rememberUpdatedState(onResizeStart)
    val currentOnResize by rememberUpdatedState(onResize)
    val currentOnResizeEnd by rememberUpdatedState(onResizeEnd)
    val density = LocalDensity.current
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text("拖动调整目录宽度") } },
        state = rememberTooltipState(),
    ) {
        Box(
            Modifier
                .width(MATERIALS_RESIZE_HANDLE_WIDTH_DP.dp)
                .fillMaxHeight()
                .pointerHoverIcon(PointerIcon(Cursor.getPredefinedCursor(Cursor.E_RESIZE_CURSOR)))
                .pointerInput(density) {
                    detectHorizontalDragGestures(
                        onDragStart = { currentOnResizeStart() },
                        onHorizontalDrag = { change, dragAmount ->
                            change.consume()
                            currentOnResize(with(density) { dragAmount.toDp().value })
                        },
                        onDragEnd = currentOnResizeEnd,
                    )
                },
            contentAlignment = Alignment.Center,
        ) {
            Box(
                Modifier.fillMaxHeight().padding(vertical = 16.dp).width(2.dp)
                    .background(MaterialTheme.colorScheme.outlineVariant),
            )
        }
    }
}

@Composable
@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, ExperimentalMaterial3Api::class)
private fun RequirementMaterialsDirectoryPane(
    state: RequirementMaterialsFilesState,
    files: List<LocalSkillFileEntry>,
    rows: List<LocalSkillFileTreeRow>,
    expandedDirectories: Set<String>,
    selectedEntryKeys: Set<String>,
    onToggleDirectory: (String) -> Unit,
    onSelectEntry: (LocalSkillFileTreeRow, Boolean, Boolean) -> Unit,
    onPinEntry: (LocalSkillFileTreeRow) -> Unit,
    onCopySelected: () -> Unit,
    onCopyPaths: () -> Unit,
    onContextSelect: (LocalSkillFileTreeRow) -> Unit,
    onDeleteSelected: () -> Unit,
    onRenameEntry: (LocalSkillFileTreeRow) -> Unit,
    onPaste: (androidx.compose.ui.input.key.KeyEvent) -> Boolean,
    actionsEnabled: Boolean,
    actionsBusy: Boolean,
    onCollapse: () -> Unit,
    onRefresh: () -> Unit,
    sortOrder: MaterialsSortOrder,
    onSortOrderChange: (MaterialsSortOrder) -> Unit,
    readingState: MaterialsReadingState,
    modifier: Modifier = Modifier,
) {
    val focusRequester = remember { FocusRequester() }
    val directoryScope = rememberCoroutineScope()
    var contextKey by remember { mutableStateOf<String?>(null) }
    var contextPosition by remember { mutableStateOf(androidx.compose.ui.unit.IntOffset.Zero) }
    var lastPrimaryPress by remember { mutableStateOf<Pair<String, Long>?>(null) }
    var sortMenuExpanded by remember { mutableStateOf(false) }
    val viewConfiguration = LocalViewConfiguration.current
    CompositionLocalProvider(LocalMaterialsReadingState provides readingState) {
    val directoryListState = rememberMaterialsLazyListState("directory")
    Surface(modifier, color = MaterialTheme.colorScheme.surfaceVariant) {
        Column(Modifier.fillMaxSize().padding(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    if (state is RequirementMaterialsFilesState.Loaded) "文档（${files.size}）" else "文档",
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                )
                Box {
                    ActionIconButton("排序：${sortOrder.label}", { sortMenuExpanded = true }, Modifier.size(30.dp)) {
                        Icon(Icons.AutoMirrored.Outlined.Sort, null, Modifier.size(16.dp))
                    }
                    SilverWingDropdownMenu(sortMenuExpanded, { sortMenuExpanded = false }) {
                        MaterialsSortOrder.entries.forEach { option ->
                            DropdownMenuItem(
                                text = { Text(option.label) },
                                trailingIcon = { if (sortOrder == option) Icon(Icons.Outlined.Check, null, Modifier.size(16.dp)) },
                                onClick = {
                                    sortMenuExpanded = false
                                    if (sortOrder != option) {
                                        onSortOrderChange(option)
                                        directoryScope.launch { directoryListState.scrollToItem(0) }
                                    }
                                },
                            )
                        }
                    }
                }
                MaterialsMoreActions()
                ActionIconButton(
                    label = "刷新任务资料目录",
                    onClick = onRefresh,
                    modifier = Modifier.size(30.dp),
                    loading = state is RequirementMaterialsFilesState.Loading,
                ) {
                    Icon(Icons.Outlined.Refresh, "刷新任务资料目录", Modifier.size(16.dp))
                }
                ActionIconButton("折叠文档列表", onCollapse, Modifier.size(30.dp)) {
                    Icon(Icons.Outlined.ChevronLeft, null, Modifier.size(18.dp))
                }
            }
            if (actionsBusy) {
                LinearProgressIndicator(Modifier.fillMaxWidth())
                Text("正在检查所选项…", style = MaterialTheme.typography.bodySmall)
            }
            when (state) {
                RequirementMaterialsFilesState.Loading -> {
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text("正在读取目录…", style = MaterialTheme.typography.bodySmall)
                }
                is RequirementMaterialsFilesState.Failed -> {
                    Text(state.message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = onRefresh) { Text("重试") }
                }
                is RequirementMaterialsFilesState.Loaded -> if (rows.isEmpty()) {
                    Text("目录为空", style = MaterialTheme.typography.bodySmall)
                } else LazyColumn(
                    Modifier.weight(1f).fillMaxWidth()
                        .focusRequester(focusRequester)
                        .onPreviewKeyEvent { event ->
                            if (event.type == KeyEventType.KeyDown && event.key == Key.Escape && contextKey != null) {
                                contextKey = null
                                true
                            } else if (event.type == KeyEventType.KeyDown && event.isCtrlPressed && event.key == Key.C) {
                                onCopySelected()
                                true
                            } else onPaste(event)
                        }.focusable(),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                    state = directoryListState,
                ) {
                items(rows, key = LocalSkillFileTreeRow::key) { row ->
                    val node = row.node
                    val file = (node as? LocalSkillFileTreeNode.File)?.entry
                    val directory = node as? LocalSkillFileTreeNode.Directory
                    val selected = row.key in selectedEntryKeys
                    val interaction = remember(row.key) { MutableInteractionSource() }
                    val hovered by interaction.collectIsHoveredAsState()
                    val focused by interaction.collectIsFocusedAsState()
                    var ctrlPressed by remember(row.key) { mutableStateOf(false) }
                    var shiftPressed by remember(row.key) { mutableStateOf(false) }
                    Box(Modifier.fillMaxWidth()) {
                    TooltipBox(
                        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
                        tooltip = { PlainTooltip { Text(directory?.relativePath ?: file?.relativePath.orEmpty()) } },
                        state = rememberTooltipState(), modifier = Modifier.fillMaxWidth(),
                    ) {
                    Row(
                        Modifier.fillMaxWidth()
                            .background(if (selected) MaterialTheme.colorScheme.primary.copy(alpha = 0.10f)
                                else if (hovered) MaterialTheme.colorScheme.primary.copy(alpha = 0.04f) else Color.Transparent)
                            .border(1.dp, if (focused) MaterialTheme.colorScheme.primary else Color.Transparent)
                            .onPointerEvent(PointerEventType.Press) { event ->
                                if (event.buttons.isSecondaryPressed && actionsEnabled) {
                                    onContextSelect(row)
                                    focusRequester.requestFocus()
                                    contextKey = row.key
                                    event.changes.firstOrNull()?.position?.let { contextPosition = androidx.compose.ui.unit.IntOffset(it.x.toInt(), it.y.toInt()) }
                                    event.changes.forEach { it.consume() }
                                }
                                ctrlPressed = event.keyboardModifiers.isPointerCtrlPressed
                                shiftPressed = event.keyboardModifiers.isPointerShiftPressed
                                if (file != null && event.buttons.isPrimaryPressed && !ctrlPressed && !shiftPressed) {
                                    val now = event.changes.firstOrNull()?.uptimeMillis ?: 0L
                                    if (lastPrimaryPress?.let { (key, time) -> key == row.key &&
                                            now - time in viewConfiguration.doubleTapMinTimeMillis..viewConfiguration.doubleTapTimeoutMillis } == true) {
                                        onPinEntry(row)
                                        lastPrimaryPress = null
                                    } else lastPrimaryPress = row.key to now
                                } else lastPrimaryPress = null
                            }
                            .selectable(selected = selected, interactionSource = interaction, indication = null, role = Role.Button, onClick = {
                                focusRequester.requestFocus()
                                onSelectEntry(row, ctrlPressed, shiftPressed)
                                if (directory != null && !ctrlPressed && !shiftPressed) onToggleDirectory(directory.relativePath)
                            })
                            .padding(start = ((row.depth - 1).coerceAtLeast(0) * 16).dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        when {
                            directory != null -> Icon(
                                if (row.expanded) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight,
                                if (row.expanded) "收起目录 ${directory.name}" else "展开目录 ${directory.name}",
                                Modifier.size(16.dp).clickable {
                                    focusRequester.requestFocus()
                                    onToggleDirectory(directory.relativePath)
                                },
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            else -> Spacer(Modifier.width(16.dp))
                        }
                        Spacer(Modifier.width(4.dp))
                        when {
                            directory != null -> Icon(
                                if (row.expanded) Icons.Outlined.FolderOpen else Icons.Outlined.Folder,
                                null,
                                Modifier.size(16.dp),
                                tint = MaterialTheme.colorScheme.primary,
                            )
                            file != null -> Icon(
                                Icons.Outlined.Description,
                                null,
                                Modifier.size(16.dp),
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        Spacer(Modifier.width(6.dp))
                        Text(
                            directory?.name ?: file?.relativePath?.substringAfterLast('/').orEmpty(),
                            modifier = Modifier.weight(1f),
                            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                            style = MaterialTheme.typography.labelMedium,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    }
                    SilverWingContextMenu(contextKey == row.key, { contextKey = null }, contextPosition) {
                        DropdownMenuItem(text = { Text("复制路径") }, enabled = actionsEnabled,
                            onClick = { contextKey = null; onCopyPaths() })
                        val copyLabel = if (selectedEntryKeys.size > 1) "复制所选项" else if (directory != null) "复制文件夹" else "复制文件"
                        DropdownMenuItem(text = { Text(copyLabel) }, enabled = actionsEnabled,
                            onClick = { contextKey = null; onCopySelected() })
                        DropdownMenuItem(text = { Text("重命名…") }, enabled = actionsEnabled && selectedEntryKeys.size == 1,
                            onClick = { contextKey = null; onRenameEntry(row) })
                        LocalMaterialsHeaderControls.current?.let { controls ->
                            controls.createMarkdown?.let { action -> DropdownMenuItem(text = { Text("新建 Markdown…") }, enabled = !controls.managingFiles,
                                onClick = { contextKey = null; action() }) }
                            controls.createFolder?.let { action -> DropdownMenuItem(text = { Text("新建文件夹…") }, enabled = !controls.managingFiles,
                                onClick = { contextKey = null; action() }) }
                        }
                        DropdownMenuItem(text = { Text("删除", color = MaterialTheme.colorScheme.error) }, enabled = actionsEnabled,
                            onClick = { contextKey = null; onDeleteSelected() })
                    }
                    }
                }
                }
            }
        }
    }
    }
}

@Composable
internal fun RequirementMaterialsDocumentPane(
    controller: DesktopApplication,
    root: Path,
    selectedFile: RequirementMaterialsMarkdownFile?,
    state: RequirementMaterialsDocumentState,
    onRetry: () -> Unit,
    onNavigateMarkdown: (String, String?) -> Unit = { _, _ -> },
    onNavigateImage: (String) -> Unit = {},
    pendingAnchor: String? = null,
    modifier: Modifier = Modifier,
    refreshKey: Any = state,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    readingState: MaterialsReadingState? = null,
    documentTabs: MaterialsDocumentTabsState? = null,
    imagePaths: List<String> = emptyList(),
    onSelectTab: (String) -> Unit = {},
    onCloseTab: (String) -> Unit = {},
    taskPath: String = "",
) {
    if (LocalDocumentFind.current == null) {
        val find = remember(root, selectedFile?.relativePath) { DocumentFindState() }
        DocumentFindScope(find, modifier) { RequirementMaterialsDocumentPane(controller, root, selectedFile, state, onRetry,
            onNavigateMarkdown, onNavigateImage, pendingAnchor, Modifier.fillMaxSize(), refreshKey, ioDispatcher, readingState,
            documentTabs, imagePaths, onSelectTab, onCloseTab, taskPath) }
        return
    }
    CompositionLocalProvider(LocalMaterialsReadingState provides readingState) {
    Surface(modifier, color = MaterialTheme.colorScheme.surface) {
        val loaded = (state as? RequirementMaterialsDocumentState.Loaded)
            ?.takeIf { it.relativePath == selectedFile?.relativePath }
        val pdf = (state as? RequirementMaterialsDocumentState.Pdf)
            ?.takeIf { it.relativePath == selectedFile?.relativePath }
        val external = (state as? RequirementMaterialsDocumentState.External)
            ?.takeIf { it.relativePath == selectedFile?.relativePath }
        val sourcePath = selectedFile?.let { file ->
            root.resolve(file.relativePath)
                .takeIf { Files.isRegularFile(it, LinkOption.NOFOLLOW_LINKS) && !Files.isSymbolicLink(it) }
        }
        val markdownIdentity = selectedFile?.takeIf { it.markdown }?.let { file ->
            MarkdownDraftIdentity(taskPath, root.toAbsolutePath().normalize().toString(), file.relativePath)
        }
        val markdownSession = if (markdownIdentity != null) {
            controller.markdownDraftStore.session(markdownIdentity).collectAsState().value
        } else null
        // The editor owns the live draft.  Reuse it here so switching back to
        // preview renders exactly what the user is editing, rather than the
        // last disk read.
        val displayedMarkdownContent = if (markdownSession?.initialized == true) {
            markdownSession.content
        } else loaded?.content
        val modeState = readingState?.mode ?: remember(root, selectedFile?.relativePath) { mutableStateOf(initialMarkdownPreviewMode()) }
        val mode = modeState.value
        val editing = readingState?.editing?.value == true && selectedFile?.markdown == true && loaded != null
        val outline = if (selectedFile?.markdown == true) rememberMarkdownOutlineState(displayedMarkdownContent.orEmpty(),
            documentKey = sourcePath?.toString() + ":" + displayedMarkdownContent.hashCode(), sourcePath = sourcePath, allowedRoot = root) else null
        Column(Modifier.fillMaxSize().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            documentTabs?.let { tabs -> MaterialsDocumentTabs(tabs, onSelectTab, onCloseTab,
                onPin = { tabs.pin(it) }, modifier = Modifier.fillMaxWidth()) }
            if (selectedFile != null) {
                RequirementMaterialsFileHeader(controller, root, selectedFile, displayedMarkdownContent, mode,
                    onModeChange = { modeState.value = it; readingState?.editing?.value = false }, refreshKey = refreshKey, ioDispatcher = ioDispatcher,
                    outlineState = outline, showIdentity = documentTabs == null || documentTabs.tabs.isEmpty(),
                    editing = editing,
                    onEdit = readingState?.let { { it.editing.value = true } },
                    onPreview = readingState?.let { { it.editing.value = false; modeState.value = MarkdownPreviewMode.RENDERED } },
                    dirty = readingState?.editorDirty?.value == true,
                    saveBusy = readingState?.editorBusy?.value == true,
                    onSave = readingState?.let { { it.editorSaveRequest.intValue++ } })
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            }
            if (selectedFile == null) Row(Modifier.fillMaxWidth()) { MaterialsHeaderLeading(); MaterialsHeaderActions() }
            LocalDocumentFind.current?.let { DocumentFindBar(it) }
            when {
                selectedFile == null -> BrowserEmptyCard(
                    title = "选择一个文件",
                    detail = "从左侧目录选择文件，预览内容或使用外部应用打开。",
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                )
                state is RequirementMaterialsDocumentState.Loading || loaded == null && pdf == null && external == null && state !is RequirementMaterialsDocumentState.Failed -> {
                    Column(
                        Modifier.weight(1f).fillMaxWidth().padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                        Text("正在读取文件…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                state is RequirementMaterialsDocumentState.Failed && state.relativePath == selectedFile.relativePath -> {
                    BrowserErrorCard(
                        message = state.message,
                        onRetry = onRetry,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                    )
                }
                pdf != null -> RequirementMaterialsPdfPreview(
                    root = root,
                    relativePath = pdf.relativePath,
                    refreshKey = pdf.refreshKey,
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                    ioDispatcher = ioDispatcher,
                )
                selectedFile.image -> MaterialsImagePreview(
                    root = root,
                    relativePath = selectedFile.relativePath,
                    refreshKey = refreshKey,
                    images = imagePaths,
                    onNavigate = onNavigateImage,
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                    ioDispatcher = ioDispatcher,
                )
                external != null -> RequirementMaterialsExternalFile(selectedFile, Modifier.weight(1f).fillMaxWidth())
                loaded != null -> if (selectedFile.markdown) {
                    if (editing) MaterialsMarkdownEditor(
                        store = controller.markdownDraftStore,
                        service = remember { MaterialsMarkdownEditorService() },
                        identity = MarkdownDraftIdentity(taskPath, root.toAbsolutePath().normalize().toString(), selectedFile.relativePath),
                        root = root,
                        relativePath = selectedFile.relativePath,
                        originalContent = loaded.content,
                        refreshKey = refreshKey,
                        onSaved = { readingState?.editorDirty?.value = false; controller.showStatus("Markdown 已保存") },
                        onUseExternal = { readingState?.editorDirty?.value = false },
                        onError = controller::showError,
                        appScope = controller.appScope,
                        saveRequest = readingState?.editorSaveRequest?.intValue ?: 0,
                        onDirtyChanged = { readingState?.editorDirty?.value = it },
                        onBusyChanged = { readingState?.editorBusy?.value = it },
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                        ioDispatcher = ioDispatcher,
                    ) else MarkdownDocumentPreview(
                        content = displayedMarkdownContent.orEmpty(),
                        mode = mode,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                        sourcePath = sourcePath,
                        allowedRoot = root,
                        onNavigateLocalLink = onNavigateMarkdown,
                        headingAnchor = pendingAnchor,
                        outlineState = outline,
                    )
                } else RequirementMaterialsTextPreview(
                    content = displayedMarkdownContent.orEmpty(),
                    extension = selectedFile.extension,
                    fileName = selectedFile.fileName,
                    mode = mode,
                    onCopyFragment = { controller.copyText(it, "表格片段已复制") },
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                )
            }
        }
    }
    }
}

@Composable
private fun BrowserLoadingCard(modifier: Modifier = Modifier) {
    OutlinedCard(modifier) {
        Column(Modifier.fillMaxSize().padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            LinearProgressIndicator(Modifier.fillMaxWidth())
            Text("正在读取任务资料目录…", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun BrowserEmptyCard(
    title: String,
    detail: String,
    modifier: Modifier = Modifier,
) {
    OutlinedCard(modifier) {
        Column(
            Modifier.fillMaxSize().padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text(title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun BrowserErrorCard(
    message: String,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    OutlinedCard(modifier) {
        Column(
            Modifier.fillMaxSize().padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text("无法预览任务资料", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            TextButton(onClick = onRetry) { Text("重试") }
        }
    }
}
